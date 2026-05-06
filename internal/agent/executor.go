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
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
	Rounds         []GenerateRound
}

// GenerateRound captures one model invocation inside a multi-round
// tool-use loop. Mirrors adapters.Round with the field names the
// session recorder writes to YAML.
type GenerateRound struct {
	Index          int    `json:"index"`
	Reasoning      string `json:"reasoning,omitempty"`
	Text           string `json:"text,omitempty"`
	Message        string `json:"message,omitempty"`
	InputTokens    int    `json:"input_tokens,omitempty"`
	OutputTokens   int    `json:"output_tokens,omitempty"`
	ThoughtsTokens int    `json:"thoughts_tokens,omitempty"`
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
//  2. Acquire the per-(provider, model) concurrency slot.
//  3. Apply the per-call timeout (agent override, env override, or
//     default).
//  4. Fire any acquired-callback the caller threaded via context
//     (used by the workflow to flip a "queued" spinner to "running").
//  5. Translate to adapters.Request; dispatch through the adapter;
//     translate adapters.Response back to AgentOutput.
//
// Run walks the agent's models[] preference list in declaration
// order. For each preference, it dispatches one adapter call. On a
// retryable failure (ErrRateLimit / ErrTimeout) it advances to the
// next preference and emits a slog.Warn so sustained primary
// failures show up in the operator log. On a non-retryable failure
// it returns immediately. If every preference is exhausted, Run
// returns the last error so RunWithRetry's exponential backoff sees
// a retryable sentinel and can re-walk after a delay.
func (e *Executor) Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error) {
	picks, err := ResolveAvailable(def, e.providers, e.cfg)
	if err != nil {
		return nil, err
	}

	timeout := perCallTimeout(def)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var lastErr error
	for i, pick := range picks {
		out, err := e.runOne(ctx, def, input, pick)
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
			slog.Warn("agent fallback: primary preference failed; advancing to next",
				"agent", def.ID,
				"failed_provider", pick.Provider, "failed_tier", pick.Tier,
				"next_provider", next.Provider, "next_tier", next.Tier,
				"error", err)
		}
	}
	return nil, lastErr
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

	req, err := buildAdapterRequest(def, input, pick, e.tools)
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
	if len(resp.Rounds) > 1 {
		out.Rounds = make([]GenerateRound, len(resp.Rounds))
		for i, r := range resp.Rounds {
			out.Rounds[i] = GenerateRound{
				Index:          r.Index,
				Reasoning:      r.Reasoning,
				Text:           r.Text,
				Message:        r.Message,
				InputTokens:    r.InputTokens,
				OutputTokens:   r.OutputTokens,
				ThoughtsTokens: r.ThoughtsTokens,
			}
		}
	}
	return out
}

// buildAdapterRequest projects an AgentDef + AgentInput + resolved
// pick into the provider-neutral adapters.Request. Resolves the
// strict-mode schema and tool definitions; the adapter consumes them
// in its provider-native shape.
func buildAdapterRequest(def AgentDef, input AgentInput, pick *ResolvedModel, registry *ToolRegistry) (adapters.Request, error) {
	req := adapters.Request{
		Model:           pick.Model,
		SystemPrompt:    BuildSystemPrompt(def),
		MaxOutputTokens: pick.MaxOutputTokens,
		Thinking:        pick.Thinking,
		Grounding:       def.Grounding,
	}
	for _, m := range input.Messages {
		req.Messages = append(req.Messages, adapters.Message{
			Role:    adapters.Role(m.Role),
			Content: m.Content,
		})
	}
	if def.OutputSchema != "" {
		schema, err := SchemaFor(def.OutputSchema)
		if err != nil {
			return req, err
		}
		req.OutputSchema = schema
	}
	if len(def.Tools) > 0 {
		for _, name := range def.Tools {
			tool, ok := registry.Resolve(name)
			if !ok {
				return req, fmt.Errorf("tool %q not registered", name)
			}
			req.Tools = append(req.Tools, tool)
		}
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
