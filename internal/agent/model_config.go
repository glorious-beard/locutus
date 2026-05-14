package agent

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
//
// Reasoning mode is intentionally absent here. Whether to use extended
// thinking is a per-agent decision (declared in agent frontmatter),
// not a tier property. The previous design coupled them, which led to
// the structured-emit-degradation failure mode: balanced-tier agents
// inherited thinking-on by default, and thinking + structured output +
// tool use is empirically unreliable across providers.
type TierConfig struct {
	// Model is the concrete provider-side model identifier.
	Model string `yaml:"model"`
	// MaxOutputTokens caps the model's response length. Zero means
	// "use the provider default"; Anthropic's adapter substitutes a
	// safe minimum since its SDK rejects MaxTokens=0.
	MaxOutputTokens int `yaml:"max_output_tokens,omitempty"`
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
	// FormatProviders names the rotation order the dispatcher uses
	// for the structured-output format pass (the "call 2" of the
	// reason-then-format split for agents with thinking + schema).
	// Each entry resolves to that provider's `fast` tier at load
	// time; the dispatcher walks the list in order and skips
	// providers whose adapter init fails. Empty list means "use the
	// providers in declaration order above" — kept as a soft default
	// so a stripped-down models.yaml still routes format calls
	// somewhere reasonable.
	FormatProviders []string `yaml:"format_providers,omitempty"`
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

// FormatProviderOrder returns the provider names the dispatcher walks
// for the structured-output format pass. When FormatProviders is set
// explicitly, returns it verbatim. When unset, defaults to the
// declaration order of the providers map (stable across runs because
// YAML preserves map insertion order via yaml.v3 when unmarshaled
// into a structured shape — but the providers map is a hash, so we
// have to sort for determinism). Sorted alphabetically for
// stability when the default kicks in. Operators who want a specific
// order set it explicitly.
func (c *ModelConfig) FormatProviderOrder() []string {
	if c == nil {
		return nil
	}
	if len(c.FormatProviders) > 0 {
		out := make([]string, len(c.FormatProviders))
		copy(out, c.FormatProviders)
		return out
	}
	names := make([]string, 0, len(c.Providers))
	for name := range c.Providers {
		names = append(names, name)
	}
	// Deterministic default. Operators who care about order set
	// format_providers explicitly.
	sort.Strings(names)
	return names
}

// parseModelConfig unmarshals YAML bytes into a ModelConfig. Returns
// an error on malformed YAML or an empty providers map — a config
// with no providers would silently make every dispatch unroutable,
// which is worse than a clear error at load time. Also validates
// every format_providers entry resolves to a provider with a `fast`
// tier; typos here would silently disable the format pass for an
// agent that needs it.
func parseModelConfig(data []byte) (*ModelConfig, error) {
	var cfg ModelConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse model config: %w", err)
	}
	if len(cfg.Providers) == 0 {
		return nil, fmt.Errorf("parse model config: providers map is empty")
	}
	for _, name := range cfg.FormatProviders {
		tiers, ok := cfg.Providers[name]
		if !ok {
			return nil, fmt.Errorf("parse model config: format_providers names %q, which has no entry under providers:", name)
		}
		if _, ok := tiers[string(TierFast)]; !ok {
			return nil, fmt.Errorf("parse model config: format_providers names %q, which has no `fast` tier — format pass needs the fast tier", name)
		}
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
