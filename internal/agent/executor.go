// Package agent provides LLM integration, council orchestration, and
// planning for the Locutus spec-driven project manager.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/agent/adapters"
)

// DefaultLLMCallTimeout caps a single agent call. 15 minutes is
// generous enough for Pro/Opus runs on complex constrained-JSON
// outputs but short enough that a hung call surfaces as an error
// rather than an indefinite stall. Override with LOCUTUS_LLM_TIMEOUT.
const DefaultLLMCallTimeout = 15 * time.Minute

// Message is one turn in the conversation handed to an agent. The
// system prompt comes from AgentDef.SystemPrompt; Messages here
// contain only the user-side turns produced by ProjectState.
//
// Cacheable, when set, marks the message as the tail of a cacheable
// prefix on providers that support explicit cache markers (DJ-106:
// Anthropic). Projection layers emit a Cacheable=true message for
// the static prefix shared across council fanout (GOALS, scout
// brief, outline) followed by a Cacheable=false message carrying
// the per-call variation. The Anthropic adapter places a
// cache_control marker on the Cacheable block; other adapters
// ignore the flag.
type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Cacheable bool   `json:"cacheable,omitempty"`
}

// AgentInput carries the per-call data the executor needs to dispatch
// one agent invocation. The system prompt and per-agent knobs come
// from AgentDef; Messages here are the projection-rendered user-side
// turns.
type AgentInput struct {
	Messages []Message
}

// AgentOutput is the shape of one completed agent call. Field set is
// designed to round-trip cleanly through the session-trace recorder
// (see SessionRecorder) without further translation.
type AgentOutput struct {
	Content        string
	Reasoning      string
	RawMessage     string
	Model          string
	InputTokens    int
	OutputTokens   int
	ThoughtsTokens int
	TotalTokens    int
	// CacheCreationInputTokens / CacheReadInputTokens mirror the
	// provider's prompt-cache metering. See adapters.Response for
	// per-provider semantics. Zero on Gemini today.
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	// Citations aggregates the provider-native search sources cited
	// across all rounds of a grounded call. Empty when grounding was
	// off or the model returned no sources. Mirrors
	// adapters.Response.Citations.
	Citations []Citation
	Rounds    []GenerateRound

	// ToolCalls captures per-tool-invocation outcomes for server-side
	// tools (Anthropic web_search currently). Each entry pairs the
	// query with whether the tool returned data or errored. Used by
	// RunResearch to detect ungrounded calls and by the session
	// recorder to surface a flat tool-call summary in traces.
	// Mirrors adapters.Response.ToolCalls.
	ToolCalls []ToolCall
}

// ToolCall records one server-side tool invocation. Mirrors
// adapters.ToolCall with the field names the session recorder writes
// to YAML.
type ToolCall struct {
	Name      string `json:"name" yaml:"name"`
	Query     string `json:"query,omitempty" yaml:"query,omitempty"`
	Status    string `json:"status" yaml:"status"`
	ErrorCode string `json:"error_code,omitempty" yaml:"error_code,omitempty"`
}

// GenerateRound captures one model invocation inside a multi-round
// tool-use loop. Mirrors adapters.Round with the field names the
// session recorder writes to YAML.
type GenerateRound struct {
	Index                    int        `json:"index"`
	Reasoning                string     `json:"reasoning,omitempty"`
	Text                     string     `json:"text,omitempty"`
	Message                  string     `json:"message,omitempty"`
	InputTokens              int        `json:"input_tokens,omitempty"`
	OutputTokens             int        `json:"output_tokens,omitempty"`
	ThoughtsTokens           int        `json:"thoughts_tokens,omitempty"`
	CacheCreationInputTokens int        `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int        `json:"cache_read_input_tokens,omitempty"`
	Citations                []Citation `json:"citations,omitempty"`
}

// Citation is a provider-native search source the model cited in
// one round of a grounded call. Mirrors adapters.Citation.
type Citation struct {
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// Sentinel errors for retry and timeout classification. Aliased
// from the adapters package so adapters can return them without
// importing internal/agent (which would cycle), and callers in
// internal/agent can pattern-match adapter returns via errors.Is
// against the agent-package name.
var (
	ErrRateLimit    = adapters.ErrRateLimit
	ErrTimeout      = adapters.ErrTimeout
	ErrIncompatible = adapters.ErrIncompatible
)

// AgentExecutor is the agent-level boundary the workflow dispatches
// through. Run takes an AgentDef plus the user-side input and returns
// the agent's response. Capability routing (which provider/model
// serves the call), strict-mode schema enforcement, prompt-cache
// markers, the multi-round tool-use loop, per-model concurrency, and
// per-call timeout all live inside the executor — adapters stay
// narrow.
type AgentExecutor interface {
	Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error)
}

// Executor is the production AgentExecutor backed by per-provider
// SDK adapters. Construct with NewExecutor; one instance per process
// is sufficient (adapters and the registry are safe for concurrent
// use).
type Executor struct {
	cfg         *ModelConfig
	providers   DetectedProviders
	adapters    map[ProviderName]adapters.Adapter
	concurrency *ConcurrencyManager
	tools       *ToolRegistry
	// specSearch is the swappable spec_search backend wired by cmd/llm.go
	// at registration time. GenerateSpec reaches for it (via a type
	// assertion on AgentExecutor) to push a *search.InFlightIndex in at
	// council start and restore the disk backend at council end. Nil
	// when no spec_search backend was wired — mock executors in tests
	// never need this, and the council path falls back to running
	// without in-flight swap when nil.
	specSearch *SwappableSpecSearch

	// specListManifest / specGet are the DJ-125 counterparts to
	// specSearch — RAG-tool swappables for spec_list_manifest and
	// spec_get. Same lifecycle: production wires the on-disk
	// fsSpecProvider at registration time; GenerateSpec pushes an
	// InFlightSpecStore in for the duration of a council run so all
	// three RAG tools see the in-flight RawProposal.
	specListManifest *SwappableSpecListManifest
	specGet          *SwappableSpecGet
}

// NewExecutor wires up an Executor with the given adapter set, model
// config, detected provider availability, and tool registry. nil
// tools is treated as an empty registry; nil cfg returns an error
// (the executor cannot resolve any preference without it).
func NewExecutor(cfg *ModelConfig, providers DetectedProviders, adapterSet []adapters.Adapter, tools *ToolRegistry) (*Executor, error) {
	if cfg == nil {
		return nil, fmt.Errorf("executor: nil model config")
	}
	if !providers.Any() {
		return nil, fmt.Errorf(
			"no LLM provider configured: set %s, %s, or %s",
			EnvKeyAnthropicAPI, EnvKeyGeminiAPI, EnvKeyOpenAIAPI,
		)
	}
	if tools == nil {
		tools = NewToolRegistry()
	}
	table := make(map[ProviderName]adapters.Adapter, len(adapterSet))
	for _, a := range adapterSet {
		table[ProviderName(a.Provider())] = a
	}
	return &Executor{
		cfg:         cfg,
		providers:   providers,
		adapters:    table,
		concurrency: NewConcurrencyManager(),
		tools:       tools,
	}, nil
}

// Tools returns the registry the executor dispatches against. Used
// by setup code (cmd/llm.go) to register spec-lookup tools after
// construction without a circular dependency.
func (e *Executor) Tools() *ToolRegistry { return e.tools }

// SetSpecSearch captures the swappable spec_search backend wired by
// cmd/llm.go after the on-disk index is opened. GenerateSpec reads it
// back via SpecSearch() and pushes a council-scoped in-flight index in
// for the duration of a run. Safe to call exactly once at startup;
// re-setting at runtime is supported but not used (the swap path uses
// SwappableSpecSearch.Swap directly).
func (e *Executor) SetSpecSearch(s *SwappableSpecSearch) { e.specSearch = s }

// SpecSearch returns the wired spec_search swappable, or nil when
// nothing was wired (mock executor in tests; CLI path that failed to
// open the on-disk index). GenerateSpec checks for nil before swapping
// — a missing swappable degrades the council gracefully to "no
// in-flight spec_search" rather than crashing.
func (e *Executor) SpecSearch() *SwappableSpecSearch { return e.specSearch }

// SetSpecListManifest / SpecListManifest mirror the spec_search wiring
// for the DJ-125 spec_list_manifest tool. cmd/llm.go installs the
// swappable at registration time; GenerateSpec swaps the in-flight
// provider in at council start and restores the on-disk provider at
// council end.
func (e *Executor) SetSpecListManifest(s *SwappableSpecListManifest) { e.specListManifest = s }
func (e *Executor) SpecListManifest() *SwappableSpecListManifest     { return e.specListManifest }

// SetSpecGet / SpecGet do the same for spec_get.
func (e *Executor) SetSpecGet(s *SwappableSpecGet) { e.specGet = s }
func (e *Executor) SpecGet() *SwappableSpecGet     { return e.specGet }

// Providers reports which provider SDKs the executor was
// initialized with. Used by the CLI's startup banner.
func (e *Executor) Providers() DetectedProviders { return e.providers }

// Banner returns a one-line startup string describing which
// providers are configured. Printed to stderr at the start of each
// invocation that touches an agent.
func (e *Executor) Banner() string {
	names := e.providers.Names()
	if len(names) == 0 {
		return "locutus: no providers configured"
	}
	return "locutus: providers=" + strings.Join(names, ",")
}

// Run dispatches one agent call. Steps:
//
//  1. Resolve the (provider, model) pick from the agent's models[]
//     preference list against availability + the tier table.
//  2. For each pick: apply the per-pick timeout, acquire the
//     concurrency slot, fire any acquired-callback, dispatch.
//  3. Translate adapters.Response back to AgentOutput on success.
//
// DJ-110: per-pick timeout (was previously per-walk). The agent's
// configured Timeout (frontmatter or LOCUTUS_LLM_TIMEOUT) bounds
// each provider attempt INDIVIDUALLY, not the entire fallback walk.
// Without this, a first provider that hung the full timeout would
// leave the fallback path with zero budget — every subsequent pick
// would fail instantly with context-canceled, and the "fallback
// chain" the agent's preference list documented was theatrical.
//
// Worst-case wall clock is now N × per-pick timeout for an N-pick
// preference list; a parent context with its own timeout (set by
// the caller) is the right place to bound total walk time when
// that matters. RunWithRetry's exponential backoff still re-walks
// the whole list on retry-eligible failures.
//
// On a non-retryable failure (programming error, parse error,
// schema rejection) Run returns immediately. On retryable
// failure (ErrRateLimit / ErrTimeout / ErrIncompatible) it
// advances to the next preference and emits a slog.Warn.
//
// Rate-limit hybrid: when a provider returns ErrRateLimit with a
// usable Retry-After hint (under rateLimitWaitThreshold), Run sleeps
// on the same pick and retries once before advancing. Above the
// threshold, or when there's no hint, it advances immediately — the
// fast-fallback default. When this is the only pick (no next), Run
// always waits, since rotation isn't an option. The rationale lives
// on maybeWaitOnPick; the env knob is LOCUTUS_RATE_LIMIT_WAIT_THRESHOLD.
func (e *Executor) Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error) {
	picks, err := ResolveAvailable(def, e.providers, e.cfg)
	if err != nil {
		return nil, err
	}

	perPickTimeout := perCallTimeout(def)

	var lastErr error
	for i, pick := range picks {
		out, err := e.runOnePickWithRetryAfter(ctx, def, input, pick, perPickTimeout, i, len(picks))
		if err == nil {
			return out, nil
		}
		lastErr = err
		// Fallback-eligible: rate-limit / timeout (transient) or
		// incompatibility (permanent for this provider but maybe
		// satisfiable by the next preference). Anything else —
		// programming errors, parse errors, schema errors — fails
		// the call so the operator sees the real cause.
		if !errors.Is(err, ErrRateLimit) && !errors.Is(err, ErrTimeout) && !errors.Is(err, ErrIncompatible) {
			return out, err
		}
		if i+1 < len(picks) {
			next := picks[i+1]
			// Demoted from Warn → Debug. The 429-triggered rotation
			// isn't actionable by a dev — the executor already did
			// the right thing by advancing — and a WARN line on every
			// rate-limit muddles the operator-facing console. Trace
			// files keep the detail for post-mortem analysis.
			slog.Debug("agent fallback: primary preference failed; advancing to next",
				"agent", def.ID,
				"failed_provider", pick.Provider, "failed_tier", pick.Tier,
				"next_provider", next.Provider, "next_tier", next.Tier,
				"error", err)
		}
	}
	return nil, lastErr
}

// runOnePickWithRetryAfter dispatches one pick with a single
// retry-after-aware second attempt on rate-limit. The flow:
//
//  1. Dispatch with a fresh per-pick timeout.
//  2. On success or non-rate-limit error, return.
//  3. On ErrRateLimit, consult maybeWaitOnPick: if the provider gave
//     a usable Retry-After (<= rateLimitWaitThreshold) OR this is the
//     only pick, sleep and retry once with a fresh per-pick timeout.
//     Otherwise return the rate-limit error so Run advances to the
//     next pick.
//  4. The retry's outcome is final for this pick — a second rate-
//     limit returns through to Run so it can rotate. The outer
//     RunWithRetry layer will sleep+rewalk if everything 429s.
//
// pickIdx and pickCount let maybeWaitOnPick decide whether there's a
// next pick to fall over to (and therefore whether to wait even when
// Retry-After is over threshold).
func (e *Executor) runOnePickWithRetryAfter(ctx context.Context, def AgentDef, input AgentInput, pick *ResolvedModel, perPickTimeout time.Duration, pickIdx, pickCount int) (*AgentOutput, error) {
	out, err := e.dispatchPickWithTimeout(ctx, def, input, pick, perPickTimeout)
	if err == nil {
		return out, nil
	}
	if !errors.Is(err, ErrRateLimit) {
		return out, err
	}

	sleep, source, wait := maybeWaitOnPick(err, pickIdx, pickCount)
	if !wait {
		return out, err
	}

	// slog at debug-level only — the operator-facing surface is the
	// sink callback below, which flips the spinner to "retrying" with
	// the wait duration. WARN/INFO log lines on top would compete
	// with the spinner for attention and tell the operator nothing
	// actionable; the trace files still capture this at debug for
	// post-mortem analysis.
	slog.Debug("agent rate-limited; sleeping on same provider before retry",
		"agent", def.ID,
		"provider", pick.Provider,
		"tier", pick.Tier,
		"sleep", sleep,
		"sleep_source", source,
		"is_only_pick", pickCount == 1,
	)
	if cb := RateLimitWaitCallbackFromContext(ctx); cb != nil {
		cb(sleep)
	}
	select {
	case <-ctx.Done():
		return nil, ErrTimeout
	case <-time.After(sleep):
	}

	// One retry on the same pick after the wait. Fresh per-pick
	// timeout so the budget the initial attempt consumed (mostly the
	// rate-limited round-trip) doesn't starve the retry.
	return e.dispatchPickWithTimeout(ctx, def, input, pick, perPickTimeout)
}

// dispatchPickWithTimeout is the shared per-attempt dispatch: derive
// a per-pick timeout from the parent ctx, run the adapter, return.
// Split out so the rate-limit retry path can call it twice with
// independent timeout budgets.
func (e *Executor) dispatchPickWithTimeout(ctx context.Context, def AgentDef, input AgentInput, pick *ResolvedModel, perPickTimeout time.Duration) (*AgentOutput, error) {
	pickCtx := ctx
	var cancel context.CancelFunc
	if perPickTimeout > 0 {
		pickCtx, cancel = context.WithTimeout(ctx, perPickTimeout)
	}
	out, err := e.runOne(pickCtx, def, input, pick)
	if cancel != nil {
		cancel()
	}
	return out, err
}

// rateLimitWaitThresholdDefault caps the per-pick wait when a
// provider returns Retry-After. Values up to this duration sleep on
// the same pick; longer values advance to the next preference (or,
// when there's no next pick, sleep anyway since rotation isn't an
// option).
//
// 90s catches typical fast-tier rate-limit responses (5-60s) with a
// margin for burst conditions, without making the operator stare at
// a spinner for minutes. The env override
// LOCUTUS_RATE_LIMIT_WAIT_THRESHOLD takes a Go duration string
// ("45s", "2m"); empty or unparseable falls back to the default.
const rateLimitWaitThresholdDefault = 90 * time.Second

// rateLimitWaitThreshold returns the effective threshold,
// recomputed per call so an env change between calls is picked up.
// Hot-path overhead is one os.Getenv + one ParseDuration — both
// trivially cheap relative to an LLM round-trip.
func rateLimitWaitThreshold() time.Duration {
	if v := strings.TrimSpace(os.Getenv("LOCUTUS_RATE_LIMIT_WAIT_THRESHOLD")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return rateLimitWaitThresholdDefault
}

// maybeWaitOnPick decides whether to sleep on the current pick
// before retrying, or fall through to the next preference. Returns
// (sleep duration, source label, wait?) where wait=false means the
// caller should advance.
//
// Rules:
//   - Not a RateLimitError, or RetryAfter is zero (no provider hint):
//     don't wait — advance to next pick (fast fallback, current
//     Layer 1 behavior preserved).
//   - RetryAfter <= threshold: wait. The provider gave us a usable
//     hint and the wait is bounded; sleeping beats rotating to a
//     different provider's quota.
//   - RetryAfter > threshold AND there's a next pick: don't wait —
//     advance. A long rate-limit estimate means the provider is in
//     measured cool-down; rotating to a different provider is faster
//     than sleeping.
//   - RetryAfter > threshold AND this is the only pick: wait anyway.
//     Rotation isn't an option, so the threshold doesn't apply —
//     better to sleep through than fail immediately and force the
//     outer RunWithRetry to sleep the same duration regardless.
func maybeWaitOnPick(err error, pickIdx, pickCount int) (time.Duration, string, bool) {
	var rlErr *adapters.RateLimitError
	if !errors.As(err, &rlErr) || rlErr.RetryAfter <= 0 {
		return 0, "", false
	}
	threshold := rateLimitWaitThreshold()
	hasNextPick := pickIdx+1 < pickCount
	if rlErr.RetryAfter <= threshold {
		return rlErr.RetryAfter, "retry-after", true
	}
	if !hasNextPick {
		return rlErr.RetryAfter, "retry-after-no-fallback", true
	}
	return 0, "", false
}

// runOne dispatches a single adapter call against a resolved pick.
// Concurrency, the acquired-callback, request build, and response
// translation all live here; Run is the loop driver.
func (e *Executor) runOne(ctx context.Context, def AgentDef, input AgentInput, pick *ResolvedModel) (*AgentOutput, error) {
	adapter, ok := e.adapters[pick.Provider]
	if !ok {
		return nil, fmt.Errorf("agent %q: no adapter registered for provider %q", def.ID, pick.Provider)
	}

	release, err := e.concurrency.Acquire(ctx, string(pick.Provider), pick.Model, pick.ConcurrentRequests)
	if err != nil {
		return nil, err
	}
	defer release()

	if cb := AcquiredCallbackFromContext(ctx); cb != nil {
		cb()
	}

	req, err := buildAdapterRequest(def, input, pick, e.tools, e.cfg)
	if err != nil {
		return nil, fmt.Errorf("agent %q: build request: %w", def.ID, err)
	}

	resp, err := adapter.Run(ctx, req)
	if err != nil {
		return outputFromResponse(resp, pick.Model), err
	}
	return outputFromResponse(resp, pick.Model), nil
}

// outputFromResponse translates adapters.Response → AgentOutput. nil
// response (error path before the adapter populated anything) yields
// a zero-value output with the resolved model so callers / traces
// still record what was attempted.
func outputFromResponse(resp *adapters.Response, model string) *AgentOutput {
	out := &AgentOutput{Model: model}
	if resp == nil {
		return out
	}
	if resp.Model != "" {
		out.Model = resp.Model
	}
	out.Content = resp.Content
	out.Reasoning = resp.Reasoning
	out.RawMessage = resp.RawMessage
	out.InputTokens = resp.InputTokens
	out.OutputTokens = resp.OutputTokens
	out.ThoughtsTokens = resp.ThoughtsTokens
	out.TotalTokens = resp.TotalTokens
	out.CacheCreationInputTokens = resp.CacheCreationInputTokens
	out.CacheReadInputTokens = resp.CacheReadInputTokens
	if len(resp.Citations) > 0 {
		out.Citations = make([]Citation, len(resp.Citations))
		for i, c := range resp.Citations {
			out.Citations[i] = Citation{URL: c.URL, Title: c.Title, Snippet: c.Snippet}
		}
	}
	if len(resp.ToolCalls) > 0 {
		out.ToolCalls = make([]ToolCall, len(resp.ToolCalls))
		for i, t := range resp.ToolCalls {
			out.ToolCalls[i] = ToolCall{
				Name:      t.Name,
				Query:     t.Query,
				Status:    t.Status,
				ErrorCode: t.ErrorCode,
			}
		}
	}
	if len(resp.Rounds) > 1 {
		out.Rounds = make([]GenerateRound, len(resp.Rounds))
		for i, r := range resp.Rounds {
			gr := GenerateRound{
				Index:                    r.Index,
				Reasoning:                r.Reasoning,
				Text:                     r.Text,
				Message:                  r.Message,
				InputTokens:              r.InputTokens,
				OutputTokens:             r.OutputTokens,
				ThoughtsTokens:           r.ThoughtsTokens,
				CacheCreationInputTokens: r.CacheCreationInputTokens,
				CacheReadInputTokens:     r.CacheReadInputTokens,
			}
			if len(r.Citations) > 0 {
				gr.Citations = make([]Citation, len(r.Citations))
				for j, c := range r.Citations {
					gr.Citations[j] = Citation{URL: c.URL, Title: c.Title, Snippet: c.Snippet}
				}
			}
			out.Rounds[i] = gr
		}
	}
	return out
}

// buildAdapterRequest projects an AgentDef + AgentInput + resolved
// pick into the provider-neutral adapters.Request. Resolves the
// strict-mode schema and tool definitions; the adapter consumes them
// in its provider-native shape.
//
// Every tool registered in the global ToolRegistry is exposed to
// every agent. The per-agent allowlist (AgentDef.Tools) was removed
// in workflow-unification Phase 4 because today's tools are all
// internal Locutus-defined read-only spec lookups; the allowlist
// was documentation that loosely matched reality, not enforcement.
// When external tools (with side effects) eventually land, a
// richer capability model will replace this all-or-nothing
// exposure.
func buildAdapterRequest(def AgentDef, input AgentInput, pick *ResolvedModel, registry *ToolRegistry, cfg *ModelConfig) (adapters.Request, error) {
	req := adapters.Request{
		Model:           pick.Model,
		SystemPrompt:    BuildSystemPrompt(def),
		MaxOutputTokens: pick.MaxOutputTokens,
		Thinking:        pick.Thinking,
		Grounding:       def.Grounding,
	}
	// DJ-130: populate the format pass model from the picked
	// provider's `fast:` tier so the adapter's runSplit can extract
	// against a cheap/reliable model. Empty when the provider has no
	// fast tier configured — the adapter falls back to single-call.
	if cfg != nil {
		if tierCfg, ok := cfg.Resolve(string(pick.Provider), string(TierFast)); ok {
			req.FormatModel = tierCfg.Model
			req.FormatMaxOutputTokens = tierCfg.MaxOutputTokens
		}
	}
	for _, m := range input.Messages {
		req.Messages = append(req.Messages, adapters.Message{
			Role:      adapters.Role(m.Role),
			Content:   m.Content,
			Cacheable: m.Cacheable,
		})
	}
	if def.OutputSchema != "" {
		schema, err := SchemaFor(def.OutputSchema)
		if err != nil {
			return req, err
		}
		req.OutputSchema = schema
		// DJ-130 follow-up: thread the registered example payloads
		// to the adapter for the format pass's one-shot
		// demonstration (input prose → output JSON pairing). Empty
		// when the schema uses RegisterSchemaOverride; the format
		// pass then falls back to bare CanonicalFormatterPrompt +
		// strict-mode schema enforcement.
		//
		// Examples are NOT prepended into the request's user
		// messages for the reasoning pass or for single-call paths.
		// The fifth winplan re-run trace surfaced two distinct
		// failures from doing that: (1) scout copying example
		// concern_disposition text verbatim into its actual
		// concern_dispositions output; (2) decision-elaborator
		// anchoring on the example's 1-alternative list length
		// against a revise that needed 6+ alternatives. The example
		// taught the model to imitate content, not just shape — and
		// LLMs treat example content as evidence about what
		// belongs in the output. Schema descriptions (via struct
		// tags) and strict-mode enforcement carry shape without
		// the content-imitation risk.
		req.FormatExampleDoc = SchemaPromptDoc(def.OutputSchema)
		req.FormatExampleProse = SchemaProsePromptDoc(def.OutputSchema)
	}
	for _, name := range registry.Names() {
		tool, ok := registry.Resolve(name)
		if !ok {
			// Concurrent deregistration is not a supported
			// production path; surface as an internal error
			// rather than silently dropping the tool.
			return req, fmt.Errorf("tool %q registered then disappeared", name)
		}
		req.Tools = append(req.Tools, tool)
	}
	return req, nil
}

// perCallTimeout returns the deadline for one agent call. Precedence:
// explicit AgentDef.Timeout (per-agent frontmatter) wins; falls
// through to LOCUTUS_LLM_TIMEOUT, then DefaultLLMCallTimeout. A
// zero-or-negative env value disables the cap entirely so users on
// slow networks can opt out.
func perCallTimeout(def AgentDef) time.Duration {
	if def.Timeout != "" {
		if d, err := time.ParseDuration(def.Timeout); err == nil {
			return d
		}
		slog.Warn("invalid agent timeout; falling back to global default",
			"agent", def.ID, "value", def.Timeout)
	}
	if v := os.Getenv(EnvKeyLocutusLLMTimeout); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		slog.Warn("invalid LOCUTUS_LLM_TIMEOUT; using default",
			"value", v, "default", DefaultLLMCallTimeout)
	}
	return DefaultLLMCallTimeout
}
