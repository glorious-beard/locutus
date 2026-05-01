package agent

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// EnvKeyModelsConfig is the env-var callers set to override the embedded
// model-tier config with a file on disk. Empty path = use the project's
// .borg/models.yaml when present, else the embedded defaults that ship
// with this build.
const EnvKeyModelsConfig = "LOCUTUS_MODELS_CONFIG"

// ProjectModelsConfigPath is the in-tree path scaffolded by `locutus init`.
// LoadModelConfig reads from here on every invocation when the env-var
// override is unset, so per-project edits to model preferences are picked
// up without rebuilding or setting an env var.
const ProjectModelsConfigPath = ".borg/models.yaml"

//go:embed models.yaml
var embeddedModelsYAML []byte

// EmbeddedModelsYAML returns the model-tier config bytes baked into the
// binary at build time. Exposed so the scaffold package can seed
// .borg/models.yaml on `locutus init` from the same source of truth as
// the runtime fallback.
func EmbeddedModelsYAML() []byte {
	// Defensive copy — the caller is the scaffold writer and shouldn't
	// be able to mutate the package's embedded bytes.
	out := make([]byte, len(embeddedModelsYAML))
	copy(out, embeddedModelsYAML)
	return out
}

// ModelConfig is the project-tunable model configuration:
//   - Tiers maps each CapabilityTier to an ordered list of candidate
//     model strings.
//   - Models holds per-model knobs (currently MaxOutputTokens) so
//     model-side idiosyncrasies — like Gemini's 8k API default being
//     too small for spec-generation output — live in YAML the user can
//     override, not in compiled code.
type ModelConfig struct {
	Tiers  map[CapabilityTier][]string `yaml:"tiers"`
	Models map[string]ModelKnobs       `yaml:"models,omitempty"`
}

// ModelKnobs are per-model defaults applied when a GenerateRequest
// doesn't override them. Each model's idiosyncrasies — output-token
// cap defaults, concurrency throttles, future fields like thinking
// budget — live here so users can tune per-project via
// .borg/models.yaml without rebuilding.
type ModelKnobs struct {
	// MaxOutputTokens is the default token cap when the request omits
	// MaxTokens. Zero means "no project-level default" — the provider
	// plugin's own default applies. For Anthropic, where the plugin
	// rejects MaxTokens == 0, code falls back to defaultAnthropicMaxTokens
	// only when neither request nor knob supplies a value.
	MaxOutputTokens int `yaml:"max_output_tokens,omitempty"`
	// ConcurrentRequests caps how many in-flight Generate calls may
	// target this model simultaneously. Zero means "unlimited"; any
	// positive value is enforced by GenKitLLM via a per-model
	// semaphore. Set this lower for free-tier quotas (Gemini 2.5
	// flash-lite at 15 RPM) or preview models with strict caps; set
	// it higher for paid tiers with generous limits. Without a cap,
	// fanout-shaped workloads (Phase 3 elaborate steps) can fire 10+
	// concurrent calls and trip provider 429s, which then stall the
	// whole workflow on backoff.
	ConcurrentRequests int `yaml:"concurrent_requests,omitempty"`
}

// KnobsFor returns the ModelKnobs configured for a model string, or a
// zero-value ModelKnobs when the model is not listed. Safe on nil.
func (c *ModelConfig) KnobsFor(model string) ModelKnobs {
	if c == nil {
		return ModelKnobs{}
	}
	return c.Models[model]
}

// parseModelConfig unmarshals YAML bytes into a ModelConfig. Returns an
// error on malformed YAML or empty tiers map; a config with zero tiers
// would silently resolve everything to empty, which is worse than a
// clear error.
func parseModelConfig(data []byte) (*ModelConfig, error) {
	var cfg ModelConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse model config: %w", err)
	}
	if len(cfg.Tiers) == 0 {
		return nil, fmt.Errorf("parse model config: tiers map is empty")
	}
	return &cfg, nil
}

var (
	defaultConfigOnce sync.Once
	defaultConfig     *ModelConfig
	defaultConfigErr  error
)

// DefaultModelConfig returns the config embedded at build time via
// //go:embed models.yaml. Parsed lazily on first call, then cached for
// the process lifetime. A parse failure here is effectively a build
// error — the embedded bytes are compiled in.
func DefaultModelConfig() (*ModelConfig, error) {
	defaultConfigOnce.Do(func() {
		defaultConfig, defaultConfigErr = parseModelConfig(embeddedModelsYAML)
	})
	return defaultConfig, defaultConfigErr
}

// LoadModelConfig resolves the model-tier config in this precedence:
//
//  1. LOCUTUS_MODELS_CONFIG env var (explicit override). Missing file is
//     an error — silent fallback would hide typos.
//  2. .borg/models.yaml under the nearest ancestor that contains the
//     project root marker (so subcommands run from a subdirectory still
//     pick up the project's model preferences). Missing or unreadable
//     falls through silently — the file is optional.
//  3. The embedded defaults baked into the binary.
func LoadModelConfig() (*ModelConfig, error) {
	if path := os.Getenv(EnvKeyModelsConfig); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s=%q: %w", EnvKeyModelsConfig, path, err)
		}
		return parseModelConfig(data)
	}
	if root, err := findProjectRootForConfig(); err == nil {
		if data, err := os.ReadFile(filepath.Join(root, ProjectModelsConfigPath)); err == nil {
			return parseModelConfig(data)
		}
	}
	return DefaultModelConfig()
}

// findProjectRootForConfig walks up from the current working directory
// looking for the project root marker. Returns the absolute path or an
// error when no ancestor contains it. Kept inline (rather than importing
// internal/specio) to avoid a layering cycle: specio is a pure FS shim
// and shouldn't depend on agent, but agent has historically depended on
// specio, so the helper is duplicated by design here.
func findProjectRootForConfig() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ".borg/manifest.json")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no project root found")
		}
		abs = parent
	}
}

// ResolveTier returns the first model string in the tier's candidate
// list whose provider prefix is enabled in providers. Returns empty
// string when no candidate matches — callers should fall back to an
// explicit Model field or error out. Tiers not present in the config
// also return empty.
func (c *ModelConfig) ResolveTier(tier CapabilityTier, providers DetectedProviders) string {
	if c == nil {
		return ""
	}
	candidates, ok := c.Tiers[tier]
	if !ok {
		return ""
	}
	for _, m := range candidates {
		prefix, _, ok := strings.Cut(m, "/")
		if !ok {
			// No provider prefix — not routable by Genkit, skip.
			continue
		}
		switch prefix {
		case "anthropic":
			if providers.Anthropic {
				return m
			}
		case "googleai":
			if providers.GoogleAI {
				return m
			}
		}
	}
	return ""
}
