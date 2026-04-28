package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/core/api"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/anthropic"
	"github.com/firebase/genkit/go/plugins/googlegenai"
)

// Env-var conventions the Genkit plugins read. Exposed as constants so
// LLMAvailable and DetectProviders can inspect the environment without
// stringly-typed literals scattered around.
const (
	EnvKeyAnthropicAPI = "ANTHROPIC_API_KEY"
	EnvKeyGeminiAPI    = "GEMINI_API_KEY"
	EnvKeyGoogleAPI    = "GOOGLE_API_KEY" // Google AI Studio alternate name
	EnvKeyLocutusModel = "LOCUTUS_MODEL"
)

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

	slog.Info("genkit initialized",
		"providers", detected.Names(),
		"default_model", defaultModel,
	)

	return &GenKitLLM{
		g:            g,
		defaultModel: defaultModel,
		providers:    detected,
	}, nil
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
	if req.Temperature > 0 || req.MaxTokens > 0 {
		opts = append(opts, ai.WithConfig(&ai.GenerationCommonConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxTokens,
		}))
	}
	// If the caller asked for structured output, push it down to the
	// provider so the response is constrained at the API level (Anthropic
	// forced tool-use, Gemini responseSchema). Without this, every model
	// is asked for JSON in prose only, and Gemini in particular wraps
	// output in markdown fences that downstream parsers reject.
	if req.OutputSchema != nil {
		opts = append(opts, ai.WithOutputType(req.OutputSchema))
	}

	resp, err := genkit.Generate(ctx, g.g, opts...)
	if err != nil {
		return nil, fmt.Errorf("genkit generate (model=%s): %w", model, err)
	}
	return &GenerateResponse{Content: resp.Text(), Model: model}, nil
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
