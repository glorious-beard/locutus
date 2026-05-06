package agent

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// EnvKeyModelsConfig is the env-var callers set to override the
// embedded model-tier config with a file on disk. Empty path means
// use the project's .borg/models.yaml when present, else the embedded
// defaults baked into this build.
const EnvKeyModelsConfig = "LOCUTUS_MODELS_CONFIG"

// ProjectModelsConfigPath is the in-tree path scaffolded by
// `locutus init`. LoadModelConfig reads from here on every invocation
// when the env-var override is unset, so per-project edits to model
// preferences are picked up without rebuilding or setting an env var.
const ProjectModelsConfigPath = ".borg/models.yaml"

//go:embed models.yaml
var embeddedModelsYAML []byte

// EmbeddedModelsYAML returns the model-tier config bytes baked into
// the binary at build time. Exposed so the scaffold writer can seed
// .borg/models.yaml on `locutus init` from the same source of truth
// as the runtime fallback.
func EmbeddedModelsYAML() []byte {
	out := make([]byte, len(embeddedModelsYAML))
	copy(out, embeddedModelsYAML)
	return out
}

// Tier names a per-provider operating point. Three tiers cover the
// council's needs: fast (cheap, judgment-light), balanced (default
// for most council steps), strong (expensive, used for the steps
// where the spec hangs off the model's reasoning quality).
type Tier string

const (
	TierFast     Tier = "fast"
	TierBalanced Tier = "balanced"
	TierStrong   Tier = "strong"
)

// TierConfig is the per-(provider, tier) knob bundle. The model
// string is what the adapter passes to its SDK; the rest are
// operational defaults the executor applies to the request before
// dispatch. Agents do not configure any of these — frontmatter only
// declares (provider, tier) preferences.
type TierConfig struct {
	// Model is the concrete provider-side model identifier.
	Model string `yaml:"model"`
	// MaxOutputTokens caps the model's response length. Zero means
	// "use the provider default"; Anthropic's adapter substitutes a
	// safe minimum since its SDK rejects MaxTokens=0.
	MaxOutputTokens int `yaml:"max_output_tokens,omitempty"`
	// Thinking is the coarse extended-thinking level —
	// "off" / "on" / "high". The adapter maps to provider-specific
	// budget knobs. Empty defaults to "off".
	Thinking string `yaml:"thinking,omitempty"`
	// ConcurrentRequests caps in-flight calls to this
	// (provider, model) so fanout-shaped workloads don't exceed
	// provider RPM ceilings. Zero means unbounded; positive values
	// are enforced by the executor's concurrency manager.
	ConcurrentRequests int `yaml:"concurrent_requests,omitempty"`
}

// ModelConfig is the parsed per-provider tier table. The shape
// mirrors the YAML on disk one-to-one for round-trip simplicity.
type ModelConfig struct {
	Providers map[string]map[string]TierConfig `yaml:"providers"`
}

// Resolve returns the TierConfig for a (provider, tier) pair and a
// presence flag. Missing provider or missing tier within a known
// provider both return ok=false; callers should fall through to the
// next preference rather than substituting defaults.
func (c *ModelConfig) Resolve(provider, tier string) (TierConfig, bool) {
	if c == nil {
		return TierConfig{}, false
	}
	tiers, ok := c.Providers[provider]
	if !ok {
		return TierConfig{}, false
	}
	cfg, ok := tiers[tier]
	if !ok {
		return TierConfig{}, false
	}
	return cfg, true
}

// parseModelConfig unmarshals YAML bytes into a ModelConfig. Returns
// an error on malformed YAML or an empty providers map — a config
// with no providers would silently make every dispatch unroutable,
// which is worse than a clear error at load time.
func parseModelConfig(data []byte) (*ModelConfig, error) {
	var cfg ModelConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse model config: %w", err)
	}
	if len(cfg.Providers) == 0 {
		return nil, fmt.Errorf("parse model config: providers map is empty")
	}
	return &cfg, nil
}

var (
	defaultConfigOnce sync.Once
	defaultConfig     *ModelConfig
	defaultConfigErr  error
)

// DefaultModelConfig returns the config embedded at build time via
// //go:embed models.yaml. Parsed lazily on first call, then cached
// for the process lifetime. A parse failure here is effectively a
// build error — the embedded bytes are compiled in.
func DefaultModelConfig() (*ModelConfig, error) {
	defaultConfigOnce.Do(func() {
		defaultConfig, defaultConfigErr = parseModelConfig(embeddedModelsYAML)
	})
	return defaultConfig, defaultConfigErr
}

// LoadModelConfig resolves the model-tier config in this precedence:
//
//  1. LOCUTUS_MODELS_CONFIG env var — explicit override. Missing file
//     is an error so typos surface loudly.
//  2. .borg/models.yaml under the nearest project root (so subcommands
//     run from a subdirectory still pick up project preferences).
//     Missing or unreadable falls through silently — the file is
//     optional.
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

// findProjectRootForConfig walks up from the current working
// directory looking for the project root marker. Returns the
// absolute path or an error when no ancestor contains it. Kept
// inline (rather than importing internal/specio) to avoid a layering
// cycle: specio is a pure FS shim and shouldn't depend on agent.
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
