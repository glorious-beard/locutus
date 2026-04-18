package agent

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// EnvKeyModelsConfig is the env-var callers set to override the embedded
// model-tier config with a file on disk. Empty path = use the embedded
// defaults that ship with this build.
const EnvKeyModelsConfig = "LOCUTUS_MODELS_CONFIG"

//go:embed models.yaml
var embeddedModelsYAML []byte

// ModelConfig maps each CapabilityTier to an ordered list of candidate
// model strings. ResolveTier walks the list and returns the first entry
// whose provider prefix is enabled in the given DetectedProviders.
// List order is the user's preference when multiple providers match.
type ModelConfig struct {
	Tiers map[CapabilityTier][]string `yaml:"tiers"`
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

// LoadModelConfig reads the user's override file if LOCUTUS_MODELS_CONFIG
// is set, else returns the embedded defaults. Missing override file is
// an error (the user asked for it via env var, so they expect it to
// exist) — silent fallback would hide typos. If the env var is empty,
// returns the embedded defaults with no error.
func LoadModelConfig() (*ModelConfig, error) {
	path := os.Getenv(EnvKeyModelsConfig)
	if path == "" {
		return DefaultModelConfig()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s=%q: %w", EnvKeyModelsConfig, path, err)
	}
	return parseModelConfig(data)
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
