package agent

import (
	"context"
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

	opts := []ai.GenerateOption{
		ai.WithModelName(model),
		ai.WithMessages(messages...),
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

	if timeout := llmCallTimeout(); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	resp, err := genkit.Generate(ctx, g.g, opts...)
	if err != nil {
		// Genkit may still hand back the model's raw text on `resp` even
		// when structured-output validation fails (e.g. truncated JSON
		// that didn't pass WithOutputType). Surface it to the caller so
		// the session recorder writes the partial response into the YAML
		// transcript — debugging "JSON parse failed at byte N" without
		// the bytes themselves is impossible. Callers that bail on
		// err != nil don't change behavior; they just ignore the
		// partial response, exactly as before.
		out := &GenerateResponse{Model: model}
		if resp != nil {
			out.Content = resp.Text()
			if u := resp.Usage; u != nil {
				out.InputTokens = u.InputTokens
				out.OutputTokens = u.OutputTokens
				out.ThoughtsTokens = u.ThoughtsTokens
				out.TotalTokens = u.TotalTokens
			}
		}
		return out, fmt.Errorf("genkit generate (model=%s): %w", model, err)
	}
	out := &GenerateResponse{Content: resp.Text(), Model: model}
	if u := resp.Usage; u != nil {
		out.InputTokens = u.InputTokens
		out.OutputTokens = u.OutputTokens
		out.ThoughtsTokens = u.ThoughtsTokens
		out.TotalTokens = u.TotalTokens
	}
	return out, nil
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
		if maxTokens == 0 && req.Temperature == 0 && req.ThinkingBudget <= 0 {
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
		return cfg
	case "anthropic":
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
