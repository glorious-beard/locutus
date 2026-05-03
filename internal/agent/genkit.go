package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/core/api"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/anthropic"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"google.golang.org/genai"
)

// defaultAnthropicMaxTokens is used when a request omits MaxTokens.
// The Anthropic plugin rejects requests with MaxTokens == 0
// ("maxTokens not set"), so we always supply a value.
const defaultAnthropicMaxTokens = 4096

// Env-var conventions the Genkit plugins read. Exposed as constants so
// LLMAvailable and DetectProviders can inspect the environment without
// stringly-typed literals scattered around.
const (
	EnvKeyAnthropicAPI      = "ANTHROPIC_API_KEY"
	EnvKeyGeminiAPI         = "GEMINI_API_KEY"
	EnvKeyGoogleAPI         = "GOOGLE_API_KEY" // Google AI Studio alternate name
	EnvKeyLocutusModel      = "LOCUTUS_MODEL"
	EnvKeyLocutusLLMTimeout = "LOCUTUS_LLM_TIMEOUT" // per-call deadline override
)

// DefaultLLMCallTimeout caps a single Genkit Generate call. 15 minutes
// is generous enough for Pro/Opus runs on complex constrained-JSON
// outputs, but short enough that a hung call surfaces as an error
// instead of an indefinite stall. Override with LOCUTUS_LLM_TIMEOUT.
const DefaultLLMCallTimeout = 15 * time.Minute

// DetectedProviders records which Genkit plugins were enabled based on
// env-var presence. Callers can use this to surface diagnostics
// ("using provider X") or to pick an appropriate default model.
type DetectedProviders struct {
	Anthropic bool
	GoogleAI  bool
}

// Any reports whether at least one provider is configured.
func (p DetectedProviders) Any() bool { return p.Anthropic || p.GoogleAI }

// Names returns a stable list of provider names currently enabled,
// suitable for logging.
func (p DetectedProviders) Names() []string {
	var out []string
	if p.Anthropic {
		out = append(out, "anthropic")
	}
	if p.GoogleAI {
		out = append(out, "googleai")
	}
	return out
}

// DetectProviders inspects the environment and returns the set of
// providers locutus should register. A plugin is only added when its
// required env var is present — the Genkit plugins panic during Init
// if their key is missing, so guarding here is essential.
func DetectProviders() DetectedProviders {
	return DetectedProviders{
		Anthropic: os.Getenv(EnvKeyAnthropicAPI) != "",
		GoogleAI:  os.Getenv(EnvKeyGeminiAPI) != "" || os.Getenv(EnvKeyGoogleAPI) != "",
	}
}

// LLMAvailable returns true if at least one LLM provider is configured.
// Does not validate the key — just checks presence.
func LLMAvailable() bool {
	return DetectProviders().Any()
}

// GenKitLLM implements the LLM interface using the Genkit Go SDK. One
// instance is constructed per process (Genkit's plugin init is not
// re-entrant); construct it lazily when an LLM is actually needed and
// pass it to everything that takes an agent.LLM.
type GenKitLLM struct {
	g            *genkit.Genkit
	defaultModel string
	providers    DetectedProviders
	// modelConfig holds per-model knobs (currently MaxOutputTokens
	// defaults) loaded once at construction. Looked up in
	// buildProviderConfig so request-level config inherits sensible
	// per-model defaults from .borg/models.yaml without recomputing
	// per Generate.
	modelConfig *ModelConfig
	// concurrencyLimits maps a model string to a buffered channel
	// acting as a semaphore. Generate acquires before calling the
	// provider and releases on return; configured per-model via the
	// concurrent_requests knob in models.yaml. Lazy-initialised on
	// first request to a given model so models without a cap incur
	// no overhead.
	concurrencyMu     sync.Mutex
	concurrencyLimits map[string]chan struct{}
}

// initOnce guards genkit.Init against being called more than once per
// process — the plugins panic on double Init. Tests that need a fresh
// env should run in a subprocess (or stub the LLM with MockLLM).
var initOnce sync.Once
var initErr error
var shared *GenKitLLM

// NewGenKitLLM returns a process-wide GenKitLLM, initializing the Genkit
// runtime and registering every provider whose API key is present in
// the environment. Returns an error (not a panic) when no provider is
// configured, or when Genkit initialization fails.
func NewGenKitLLM() (*GenKitLLM, error) {
	initOnce.Do(func() {
		shared, initErr = buildGenKitLLM()
	})
	return shared, initErr
}

func buildGenKitLLM() (*GenKitLLM, error) {
	detected := DetectProviders()
	if !detected.Any() {
		return nil, fmt.Errorf(
			"no LLM provider configured: set %s or %s (or %s)",
			EnvKeyAnthropicAPI, EnvKeyGeminiAPI, EnvKeyGoogleAPI,
		)
	}

	var plugins []api.Plugin
	if detected.Anthropic {
		plugins = append(plugins, &anthropic.Anthropic{})
	}
	if detected.GoogleAI {
		plugins = append(plugins, &googlegenai.GoogleAI{})
	}

	ctx := context.Background()
	// genkit.Init panics through the plugin Init path on misconfiguration.
	// We've guarded by only registering plugins whose env var is set, so
	// panics here would indicate a Genkit bug we want to surface.
	g := genkit.Init(ctx, genkit.WithPlugins(plugins...))

	defaultModel := os.Getenv(EnvKeyLocutusModel)
	if defaultModel == "" {
		defaultModel = pickDefaultModel(detected)
	}

	// Load the model config once at startup so per-model knobs
	// (max_output_tokens, etc.) are available to buildProviderConfig
	// without re-parsing YAML on every Generate. A load error here is
	// non-fatal — fall back to a nil config (zero-valued knobs) so
	// the LLM still works against provider-default caps.
	modelConfig, mcErr := LoadModelConfig()
	if mcErr != nil {
		slog.Warn("model config load failed; using provider defaults", "error", mcErr)
	}

	// Genkit init detail used to live at INFO level; per the new
	// log-level defaults (WARN), it's now logged at DEBUG so -v / -vv
	// can surface it without polluting normal output. Callers that
	// want a user-facing one-liner should print GenKitLLM.Banner()
	// to stderr.
	slog.Debug("genkit initialized",
		"providers", detected.Names(),
		"default_model", defaultModel,
	)

	return &GenKitLLM{
		g:            g,
		defaultModel: defaultModel,
		providers:    detected,
		modelConfig:  modelConfig,
	}, nil
}

// Banner returns a human-readable startup line describing which
// providers were registered and which model will be used by default.
// The CLI prints this to stderr at the start of each invocation that
// touches an LLM so users always know which model their session is
// running against.
func (g *GenKitLLM) Banner() string {
	providers := g.providers.Names()
	if len(providers) == 0 {
		return fmt.Sprintf("locutus: model=%s (no providers configured)", g.defaultModel)
	}
	return fmt.Sprintf("locutus: model=%s providers=%s",
		g.defaultModel, strings.Join(providers, ","))
}

// pickDefaultModel chooses a sensible fallback model given which
// providers are registered. Delegates to ModelConfig.ResolveTier on
// the balanced tier so the list-per-tier preference (including user
// overrides via LOCUTUS_MODELS_CONFIG) is honored. Falls back to the
// DefaultModel constant only when no tier entry matches any enabled
// provider — a configuration error we want to surface at Generate
// time rather than paper over.
func pickDefaultModel(p DetectedProviders) string {
	if cfg, err := LoadModelConfig(); err == nil {
		if model := cfg.ResolveTier(CapabilityBalanced, p); model != "" {
			return model
		}
	}
	return DefaultModel
}

// Providers reports which Genkit plugins this LLM was initialized with.
func (g *GenKitLLM) Providers() DetectedProviders { return g.providers }

// Genkit returns the underlying *genkit.Genkit instance so callers can
// register tools, flows, or schemas against the same runtime the LLM
// uses for Generate. Used by cmd/llm.go to register the spec_lookup
// tools after construction.
func (g *GenKitLLM) Genkit() *genkit.Genkit { return g.g }

// DefaultModel returns the model string this LLM will use when a
// GenerateRequest omits Model.
func (g *GenKitLLM) DefaultModel() string { return g.defaultModel }

// Generate dispatches the request through Genkit to the plugin whose
// prefix matches the model string. Empty Model falls back to the LLM's
// configured default. A Model referring to an unregistered provider is
// rewritten to an equivalent-tier model on a registered one so
// supervisor-internal prompts can keep their provider-agnostic defaults
// while Gemini-only users still get real responses.
func (g *GenKitLLM) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	model := g.resolveModel(req.Model)

	messages := make([]*ai.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, ai.NewTextMessage(toGenkitRole(m.Role), m.Content))
	}

	// Capture middleware: snapshots the model's raw output before
	// genkit's format-handler validation runs. Two complications:
	//
	//  1. Genkit returns (nil, err) when WithOutputType validation
	//     fails (ai/generate.go ~line 381) — dropping the response
	//     entirely from the caller's perspective.
	//  2. The format handler MUTATES resp.Message in place on its
	//     parse path (resp.Message, err = formatHandler.ParseMessage(...)),
	//     so even capturing the *ai.ModelResponse pointer and
	//     reading captured.Message afterward sees nil — the same
	//     struct the format handler nilled out.
	//
	// To survive both, the middleware snapshots Text/Reasoning/Usage/
	// Message-as-JSON immediately, before unwinding back into genkit's
	// post-processing. The error path falls back to these snapshots so
	// the trace records what the model actually returned.
	var (
		capturedText       string
		capturedReasoning  string
		capturedMessageRaw string
		capturedUsage      *ai.GenerationUsage
		capturedRounds     []GenerateRound
	)
	captureMW := func(next ai.ModelFunc) ai.ModelFunc {
		return func(ctx context.Context, mreq *ai.ModelRequest, cb ai.ModelStreamCallback) (*ai.ModelResponse, error) {
			r, e := next(ctx, mreq, cb)
			if r != nil {
				// Overwriting captures hold the most-recent round's
				// data — used by the error path below to surface what
				// the model emitted last when genkit drops resp on a
				// format-handler rejection.
				capturedText = r.Text()
				capturedReasoning = extractReasoning(r)
				capturedUsage = r.Usage
				if r.Message != nil {
					if data, mErr := json.Marshal(r.Message); mErr == nil {
						capturedMessageRaw = string(data)
					}
				}
				// Per-round append: in tool-use loops the middleware
				// fires once per model invocation. Accumulate each
				// round's snapshot so the trace shows what the model
				// emitted at every step (including tool_request parts
				// in raw Message), not just the final response after
				// the loop completed. Single-round calls produce a
				// one-element slice we drop below to keep traces tight.
				round := GenerateRound{
					Index:     len(capturedRounds) + 1,
					Text:      capturedText,
					Reasoning: capturedReasoning,
					Message:   capturedMessageRaw,
				}
				if u := capturedUsage; u != nil {
					round.InputTokens = u.InputTokens
					round.OutputTokens = u.OutputTokens
					round.ThoughtsTokens = u.ThoughtsTokens
				}
				capturedRounds = append(capturedRounds, round)
			}
			return r, e
		}
	}

	opts := []ai.GenerateOption{
		ai.WithModelName(model),
		ai.WithMessages(messages...),
		ai.WithMiddleware(captureMW),
	}
	if cfg := buildProviderConfig(model, req, g.modelConfig); cfg != nil {
		opts = append(opts, ai.WithConfig(cfg))
	}
	// If the caller asked for structured output, push it down to the
	// provider so the response is constrained at the API level (Anthropic
	// forced tool-use, Gemini responseSchema). Without this, every model
	// is asked for JSON in prose only, and Gemini in particular wraps
	// output in markdown fences that downstream parsers reject.
	if req.OutputSchema != nil {
		opts = append(opts, ai.WithOutputType(req.OutputSchema))
	}

	// Tool-call attachments. Each entry is the registered tool name;
	// ai.ToolName satisfies ai.ToolRef without requiring the caller to
	// look the tool up first. Genkit's tool-use loop dispatches the
	// model's tool_request blocks to the registered handler and feeds
	// the response back until the model emits a final answer.
	//
	// On Gemini, attaching tools disables API-level JSON mode (see
	// plugins/googlegenai/gemini.go:311): the schema-as-prompt-doc
	// still applies, but downstream parsers must tolerate markdown
	// fences and other non-strict shapes. The reconciler's merge
	// handler strips fences for this reason.
	if len(req.Tools) > 0 {
		toolRefs := make([]ai.ToolRef, len(req.Tools))
		for i, name := range req.Tools {
			toolRefs[i] = ai.ToolName(name)
		}
		opts = append(opts, ai.WithTools(toolRefs...))
	}

	if timeout := llmCallTimeout(); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Per-model concurrency throttle: acquire before the call, release
	// after. Honors ctx cancellation so a stuck queue surfaces as a
	// timeout rather than a deadlock. Models without a configured
	// concurrent_requests cap return a nil semaphore — no-op.
	if release, err := g.acquireConcurrency(ctx, model); err != nil {
		return nil, err
	} else if release != nil {
		defer release()
	}
	// Notify any caller-supplied "I just left the queue" callback. The
	// workflow executor uses this to flip a "queued" spinner to a
	// "running" one in the CLI sink. Fires regardless of whether the
	// model was actually throttled — a callback that gets invoked
	// nearly-instantly on unthrottled models is harmless; the cliSink's
	// "queued" → "started" transition is idempotent.
	if cb := AcquiredCallbackFromContext(ctx); cb != nil {
		cb()
	}

	resp, err := genkit.Generate(ctx, g.g, opts...)
	out := &GenerateResponse{Model: model}
	// Only surface per-round captures when there were multiple rounds —
	// a single-round call's data is already on the top-level
	// Reasoning/Content/RawMessage fields, and emitting a one-entry
	// Rounds slice would duplicate without informing.
	if len(capturedRounds) > 1 {
		out.Rounds = capturedRounds
	}
	if resp != nil {
		// Success path: the post-format-handler resp is the canonical
		// source for Content / Reasoning / Usage.
		out.Content = resp.Text()
		out.Reasoning = extractReasoning(resp)
		if u := resp.Usage; u != nil {
			out.InputTokens = u.InputTokens
			out.OutputTokens = u.OutputTokens
			out.ThoughtsTokens = u.ThoughtsTokens
			out.TotalTokens = u.TotalTokens
		}
	} else if err != nil {
		// Error path: genkit dropped the response (likely format-
		// handler rejection). Fall back to the snapshots taken inside
		// the middleware before genkit's post-processing nilled out
		// resp.Message. RawMessage carries the full message JSON so
		// non-text parts (Anthropic forced-tool-use blocks, Gemini
		// structured frames, custom plugin parts) surface in the
		// trace even when Text() returns empty on a truncated
		// structured response.
		out.Content = capturedText
		out.Reasoning = capturedReasoning
		out.RawMessage = capturedMessageRaw
		if u := capturedUsage; u != nil {
			out.InputTokens = u.InputTokens
			out.OutputTokens = u.OutputTokens
			out.ThoughtsTokens = u.ThoughtsTokens
			out.TotalTokens = u.TotalTokens
		}
	}
	if err != nil {
		return out, fmt.Errorf("genkit generate (model=%s): %w", model, err)
	}
	return out, nil
}

// extractReasoning concatenates every PartReasoning text part in the
// response message. Returns empty when the model produced no reasoning
// (most common — only thinking-enabled calls populate this).
func extractReasoning(resp *ai.ModelResponse) string {
	if resp == nil || resp.Message == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range resp.Message.Content {
		if p == nil || !p.IsReasoning() {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// acquireConcurrency claims one slot in the per-model semaphore. The
// returned release closure must be called when the LLM call returns,
// or the slot leaks and subsequent calls eventually deadlock. Returns
// (nil, nil) when no cap is configured for the model — caller treats
// the absent release as a no-op. Honors ctx cancellation so a stuck
// queue surfaces as the caller's timeout, not a hung goroutine.
func (g *GenKitLLM) acquireConcurrency(ctx context.Context, model string) (release func(), err error) {
	cap := g.modelConfig.KnobsFor(model).ConcurrentRequests
	if cap <= 0 {
		return nil, nil
	}
	sem := g.semaphoreFor(model, cap)
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// semaphoreFor returns the per-model semaphore channel, creating it
// on first request. The channel is buffered to `cap`, so up to `cap`
// in-flight calls can hold a slot simultaneously. Subsequent lookups
// reuse the same channel even if the user later edits the cap in
// .borg/models.yaml — the runtime cap is fixed at process start.
func (g *GenKitLLM) semaphoreFor(model string, cap int) chan struct{} {
	g.concurrencyMu.Lock()
	defer g.concurrencyMu.Unlock()
	if g.concurrencyLimits == nil {
		g.concurrencyLimits = map[string]chan struct{}{}
	}
	sem, ok := g.concurrencyLimits[model]
	if !ok {
		sem = make(chan struct{}, cap)
		g.concurrencyLimits[model] = sem
	}
	return sem
}

// llmCallTimeout returns the per-call deadline for a Genkit Generate
// call. Defaults to 15 minutes — long enough that legitimate Pro/Opus
// runs on a complex constrained-JSON output finish, short enough that
// a stuck call surfaces as an error rather than burning a session.
// LOCUTUS_LLM_TIMEOUT accepts any time.ParseDuration string ("0"
// disables the cap entirely).
func llmCallTimeout() time.Duration {
	if v := os.Getenv(EnvKeyLocutusLLMTimeout); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		slog.Warn("invalid LOCUTUS_LLM_TIMEOUT; using default", "value", v, "default", DefaultLLMCallTimeout)
	}
	return DefaultLLMCallTimeout
}

// buildProviderConfig produces the provider-specific config struct that
// Genkit's gemini/anthropic plugins expect. They each only accept their
// own native config type (or a map[string]any with matching JSON tags) —
// passing ai.GenerationCommonConfig fails with INVALID_ARGUMENT. Returns
// nil when no config fields apply, in which case the caller should skip
// ai.WithConfig entirely. The Anthropic plugin requires MaxTokens > 0,
// so we always supply a default for that provider even if the caller
// omitted one.
// buildProviderConfig produces the provider-specific config struct that
// Genkit's gemini/anthropic plugins expect. Precedence for MaxOutputTokens:
//
//  1. req.MaxTokens (caller-explicit) wins.
//  2. Per-model knob from the supplied ModelConfig (e.g. .borg/models.yaml
//     entry under `models.<model-string>.max_output_tokens`).
//  3. Provider-side fallback — for googleai/* this is the Gemini API
//     default; for anthropic/* the SDK rejects 0 so we substitute
//     defaultAnthropicMaxTokens.
//
// Idiosyncrasies like "Gemini's 8k default truncates the spec architect"
// belong as YAML knobs the user can tune per project, not as compiled-in
// constants.
func buildProviderConfig(model string, req GenerateRequest, mcfg *ModelConfig) any {
	prefix, _, _ := strings.Cut(model, "/")
	knobs := mcfg.KnobsFor(model)
	switch prefix {
	case "googleai":
		maxTokens := req.MaxTokens
		if maxTokens == 0 {
			maxTokens = knobs.MaxOutputTokens
		}
		// Gemini accepts MaxOutputTokens == 0 as "use API default"; we
		// only set the field when we have a concrete value to apply.
		// Grounding always materializes a config (we need to attach
		// the GoogleSearch tool), so it can't take the early return.
		if maxTokens == 0 && req.Temperature == 0 && req.ThinkingBudget <= 0 && !req.Grounding {
			return nil
		}
		cfg := &genai.GenerateContentConfig{}
		if maxTokens > 0 {
			cfg.MaxOutputTokens = int32(maxTokens)
		}
		if req.Temperature > 0 {
			t := float32(req.Temperature)
			cfg.Temperature = &t
		}
		if req.ThinkingBudget > 0 {
			budget := int32(req.ThinkingBudget)
			cfg.ThinkingConfig = &genai.ThinkingConfig{
				ThinkingBudget: &budget,
			}
		}
		if req.Grounding {
			// Attach the GoogleSearch tool so the model can verify
			// claims against current web results. The googlegenai
			// plugin merges this with any framework-side tools, but
			// note: Gemini's API rejects GoogleSearch + Genkit
			// function-call tools simultaneously — see the test at
			// plugins/googlegenai/googleai_live_test.go:241. Agents
			// that combine `grounding: true` with custom tools won't
			// work today; not a collision in our council (scout uses
			// grounding only; reconciler uses tools only).
			cfg.Tools = append(cfg.Tools, &genai.Tool{GoogleSearch: &genai.GoogleSearch{}})
		}
		return cfg
	case "anthropic":
		if req.Grounding {
			// Genkit Go's anthropic plugin doesn't yet expose
			// web_search; signaling the gap loudly so an operator
			// running on Anthropic only knows the scout brief was
			// produced ungrounded. The call still proceeds — falling
			// back to ungrounded is a quality regression, not a
			// failure. Wire web_search through here when upstream
			// lands it, gated on the same Grounding flag.
			slog.Warn("grounding requested but unsupported on Anthropic; proceeding ungrounded",
				"model", model)
		}
		maxTokens := int64(req.MaxTokens)
		if maxTokens == 0 {
			maxTokens = int64(knobs.MaxOutputTokens)
		}
		if maxTokens == 0 {
			maxTokens = defaultAnthropicMaxTokens
		}
		cfg := &anthropicsdk.MessageNewParams{MaxTokens: maxTokens}
		if req.Temperature > 0 {
			cfg.Temperature = param.NewOpt(req.Temperature)
		}
		if req.ThinkingBudget > 0 {
			// Anthropic requires budget_tokens >= 1024 and < max_tokens.
			// Clamp on both ends so a misconfigured budget doesn't
			// surface as a provider 400 the user has to debug from a
			// stack trace.
			budget := int64(req.ThinkingBudget)
			if budget < 1024 {
				budget = 1024
			}
			if budget >= maxTokens {
				budget = maxTokens - 1
			}
			cfg.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(budget)
		}
		return cfg
	default:
		return nil
	}
}

// resolveModel picks the model string to pass to Genkit: the request's
// model if set and its provider is registered, else the LLM's default.
// Empty requests get the default. A "claude-..." or "gemini-..." with
// no provider prefix gets the default too (Genkit requires prefixes).
func (g *GenKitLLM) resolveModel(requested string) string {
	if requested == "" {
		return g.defaultModel
	}
	prefix, _, ok := strings.Cut(requested, "/")
	if !ok {
		// No provider prefix — Genkit won't route this. Fall back.
		return g.defaultModel
	}
	switch prefix {
	case "anthropic":
		if g.providers.Anthropic {
			return requested
		}
	case "googleai":
		if g.providers.GoogleAI {
			return requested
		}
	}
	// Requested provider is not registered; fall back so the call still
	// succeeds on whatever we do have.
	return g.defaultModel
}

// toGenkitRole maps locutus roles (user/system/assistant) onto Genkit's
// vocabulary (user/system/model). Anything unrecognized maps to user —
// the safest default for a chat message.
func toGenkitRole(role string) ai.Role {
	switch role {
	case "user":
		return ai.RoleUser
	case "system":
		return ai.RoleSystem
	case "assistant", "model":
		return ai.RoleModel
	default:
		return ai.RoleUser
	}
}
