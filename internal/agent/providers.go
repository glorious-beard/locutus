package agent

import (
	"os"
)

// Env-var names every provider's SDK reads to authenticate. Exposed
// as constants so DetectProviders and the CLI's "no provider
// configured" diagnostic don't drift on stringly-typed literals.
const (
	EnvKeyAnthropicAPI      = "ANTHROPIC_API_KEY"
	EnvKeyGeminiAPI         = "GEMINI_API_KEY"
	EnvKeyGoogleAPI         = "GOOGLE_API_KEY"
	EnvKeyOpenAIAPI         = "OPENAI_API_KEY"
	EnvKeyLocutusModel      = "LOCUTUS_MODEL"
	EnvKeyLocutusLLMTimeout = "LOCUTUS_LLM_TIMEOUT"
)

// ProviderName is the canonical short name for a model provider, as
// it appears in agent .md frontmatter `models[].provider` entries
// and as the key in models.yaml's `providers:` map. Adapters report
// the same string from Adapter.Provider() so the executor's adapter
// table keys consistently.
type ProviderName string

const (
	ProviderAnthropic ProviderName = "anthropic"
	ProviderGoogleAI  ProviderName = "googleai"
	ProviderOpenAI    ProviderName = "openai"
)

// DetectedProviders records which provider SDKs have credentials
// available in the current process environment. The executor uses
// this to filter an agent's models[] preference list at dispatch
// time — entries whose provider is missing fall through to the next
// preference rather than failing the call.
type DetectedProviders struct {
	Anthropic bool
	GoogleAI  bool
	OpenAI    bool
}

// Has reports whether the given provider has credentials.
func (p DetectedProviders) Has(name ProviderName) bool {
	switch name {
	case ProviderAnthropic:
		return p.Anthropic
	case ProviderGoogleAI:
		return p.GoogleAI
	case ProviderOpenAI:
		return p.OpenAI
	default:
		return false
	}
}

// Any reports whether at least one provider is configured.
func (p DetectedProviders) Any() bool {
	return p.Anthropic || p.GoogleAI || p.OpenAI
}

// Names returns a stable list of provider names currently enabled,
// suitable for logging.
func (p DetectedProviders) Names() []string {
	var out []string
	if p.Anthropic {
		out = append(out, string(ProviderAnthropic))
	}
	if p.GoogleAI {
		out = append(out, string(ProviderGoogleAI))
	}
	if p.OpenAI {
		out = append(out, string(ProviderOpenAI))
	}
	return out
}

// DetectProviders inspects the environment and returns the set of
// providers Locutus should enable. A provider is "available" when
// its API-key env var is non-empty; presence is not validated
// against the provider's auth endpoint — that surfaces at first
// call.
func DetectProviders() DetectedProviders {
	return DetectedProviders{
		Anthropic: os.Getenv(EnvKeyAnthropicAPI) != "",
		GoogleAI:  os.Getenv(EnvKeyGeminiAPI) != "" || os.Getenv(EnvKeyGoogleAPI) != "",
		OpenAI:    os.Getenv(EnvKeyOpenAIAPI) != "",
	}
}

// LLMAvailable returns true when at least one provider is configured.
// Kept under its historical name so existing CLI guards continue to
// compile through the migration window.
func LLMAvailable() bool {
	return DetectProviders().Any()
}
