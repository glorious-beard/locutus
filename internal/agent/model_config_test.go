package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultModelConfig_EmbeddedParses(t *testing.T) {
	cfg, err := DefaultModelConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Every provider declared in the embedded YAML must carry the
	// canonical fast/balanced/strong tiers — agents reference these
	// names from frontmatter.
	for _, provider := range []string{"anthropic", "googleai", "openai"} {
		t.Run(provider, func(t *testing.T) {
			tiers, ok := cfg.Providers[provider]
			require.Truef(t, ok, "provider %q missing from embedded models.yaml", provider)
			for _, tier := range []string{"fast", "balanced", "strong"} {
				entry, ok := tiers[tier]
				require.Truef(t, ok, "%s/%s missing", provider, tier)
				require.NotEmpty(t, entry.Model, "%s/%s has no model string", provider, tier)
			}
		})
	}
}

func TestResolve_ReturnsTierConfig(t *testing.T) {
	cfg := &ModelConfig{
		Providers: map[string]map[string]TierConfig{
			"anthropic": {
				"balanced": TierConfig{Model: "claude-sonnet-4-6", MaxOutputTokens: 16384, Thinking: "on"},
			},
		},
	}

	got, ok := cfg.Resolve("anthropic", "balanced")
	require.True(t, ok)
	assert.Equal(t, "claude-sonnet-4-6", got.Model)
	assert.Equal(t, 16384, got.MaxOutputTokens)
	assert.Equal(t, "on", got.Thinking)
}

func TestResolve_MissingProviderOrTier(t *testing.T) {
	cfg := &ModelConfig{
		Providers: map[string]map[string]TierConfig{
			"anthropic": {"balanced": TierConfig{Model: "claude-sonnet-4-6"}},
		},
	}

	_, ok := cfg.Resolve("openai", "balanced")
	assert.False(t, ok, "missing provider")

	_, ok = cfg.Resolve("anthropic", "nonexistent")
	assert.False(t, ok, "missing tier on known provider")
}

func TestResolve_NilConfigSafe(t *testing.T) {
	var cfg *ModelConfig
	_, ok := cfg.Resolve("anthropic", "balanced")
	assert.False(t, ok, "nil receiver must not panic")
}

func TestLoadModelConfig_EmbeddedWhenEnvUnset(t *testing.T) {
	t.Setenv(EnvKeyModelsConfig, "")
	cfg, err := LoadModelConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	_, ok := cfg.Resolve("anthropic", "balanced")
	assert.True(t, ok)
}

func TestLoadModelConfig_OverrideFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yaml")
	overrideYAML := `providers:
  anthropic:
    fast:
      model: claude-haiku-4-5-20251001
      max_output_tokens: 8192
      thinking: off
    balanced:
      model: claude-sonnet-4-6
      max_output_tokens: 16384
    strong:
      model: claude-opus-4-7
      max_output_tokens: 32768
`
	require.NoError(t, os.WriteFile(path, []byte(overrideYAML), 0o644))

	t.Setenv(EnvKeyModelsConfig, path)
	cfg, err := LoadModelConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	got, ok := cfg.Resolve("anthropic", "fast")
	require.True(t, ok)
	assert.Equal(t, "claude-haiku-4-5-20251001", got.Model)

	_, ok = cfg.Resolve("googleai", "balanced")
	assert.False(t, ok, "override drops googleai entirely; resolve should fail")
}

func TestLoadModelConfig_MissingOverrideFileErrors(t *testing.T) {
	t.Setenv(EnvKeyModelsConfig, "/nonexistent/models.yaml")
	_, err := LoadModelConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent/models.yaml")
}

func TestLoadModelConfig_ProjectFileBeatsEmbedded(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".borg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".borg/manifest.json"), []byte("{}"), 0o644))
	projectYAML := `providers:
  anthropic:
    fast:
      model: project-pin
    balanced:
      model: claude-sonnet-4-6
    strong:
      model: claude-opus-4-7
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".borg/models.yaml"), []byte(projectYAML), 0o644))

	t.Chdir(dir)
	t.Setenv(EnvKeyModelsConfig, "")

	cfg, err := LoadModelConfig()
	require.NoError(t, err)
	got, ok := cfg.Resolve("anthropic", "fast")
	require.True(t, ok)
	assert.Equal(t, "project-pin", got.Model,
		"project .borg/models.yaml should take precedence over embedded defaults")
}

func TestParseModelConfig_EmptyProvidersIsError(t *testing.T) {
	_, err := parseModelConfig([]byte("providers: {}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "providers map is empty")
}

func TestParseModelConfig_MalformedYAMLIsError(t *testing.T) {
	_, err := parseModelConfig([]byte("providers:\n  anthropic: [\n"))
	require.Error(t, err)
}
