package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultModelConfig_EmbeddedParsesAndHasAllTiers(t *testing.T) {
	cfg, err := DefaultModelConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Every capability tier we declare in council.go must have at least
	// one candidate in the embedded YAML — otherwise agents with that
	// tier silently resolve to empty.
	for _, tier := range []CapabilityTier{CapabilityFast, CapabilityBalanced, CapabilityStrong} {
		t.Run(string(tier), func(t *testing.T) {
			list, ok := cfg.Tiers[tier]
			require.Truef(t, ok, "tier %q missing from embedded models.yaml", tier)
			require.NotEmptyf(t, list, "tier %q has no candidates", tier)
			for i, m := range list {
				assert.Contains(t, m, "/", "candidate %d in tier %q lacks provider prefix: %q", i, tier, m)
			}
		})
	}
}

func TestResolveTier_PicksFirstAvailableProvider(t *testing.T) {
	cfg := &ModelConfig{
		Tiers: map[CapabilityTier][]string{
			CapabilityFast: {
				"googleai/gemini-2.5-flash-lite",
				"anthropic/claude-haiku-4-5-20251001",
			},
			CapabilityStrong: {
				"anthropic/claude-opus-4-7",
				"googleai/gemini-2.5-pro",
			},
		},
	}

	cases := []struct {
		name      string
		tier      CapabilityTier
		providers DetectedProviders
		want      string
	}{
		{
			"google only, fast → first google entry",
			CapabilityFast, DetectedProviders{GoogleAI: true},
			"googleai/gemini-2.5-flash-lite",
		},
		{
			"anthropic only, fast → falls through to anthropic entry",
			CapabilityFast, DetectedProviders{Anthropic: true},
			"anthropic/claude-haiku-4-5-20251001",
		},
		{
			"both, fast → first entry wins (google)",
			CapabilityFast, DetectedProviders{Anthropic: true, GoogleAI: true},
			"googleai/gemini-2.5-flash-lite",
		},
		{
			"both, strong → first entry wins (anthropic)",
			CapabilityStrong, DetectedProviders{Anthropic: true, GoogleAI: true},
			"anthropic/claude-opus-4-7",
		},
		{
			"neither provider, fast → empty",
			CapabilityFast, DetectedProviders{},
			"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, cfg.ResolveTier(c.tier, c.providers))
		})
	}
}

func TestResolveTier_UnknownTierReturnsEmpty(t *testing.T) {
	cfg := &ModelConfig{Tiers: map[CapabilityTier][]string{
		CapabilityFast: {"googleai/gemini-2.5-flash-lite"},
	}}
	got := cfg.ResolveTier(CapabilityTier("nonexistent"), DetectedProviders{GoogleAI: true})
	assert.Equal(t, "", got, "unknown tier should not panic; should return empty")
}

func TestResolveTier_NilConfigSafe(t *testing.T) {
	var cfg *ModelConfig
	assert.Equal(t, "", cfg.ResolveTier(CapabilityFast, DetectedProviders{GoogleAI: true}),
		"nil receiver must not panic; returns empty")
}

func TestResolveTier_SkipsEntriesWithoutProviderPrefix(t *testing.T) {
	// Candidate entries missing a "/" are unroutable by Genkit. The
	// resolver should skip them rather than return a bad string.
	cfg := &ModelConfig{Tiers: map[CapabilityTier][]string{
		CapabilityFast: {
			"gpt-5-nano",                     // no prefix, unroutable
			"unknown/some-model",             // unknown provider
			"googleai/gemini-2.5-flash-lite", // first routable
		},
	}}
	got := cfg.ResolveTier(CapabilityFast, DetectedProviders{GoogleAI: true})
	assert.Equal(t, "googleai/gemini-2.5-flash-lite", got)
}

func TestLoadModelConfig_EmbeddedWhenEnvUnset(t *testing.T) {
	t.Setenv(EnvKeyModelsConfig, "")
	cfg, err := LoadModelConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	// Should match embedded defaults (same tiers).
	_, hasFast := cfg.Tiers[CapabilityFast]
	assert.True(t, hasFast)
}

func TestLoadModelConfig_OverrideFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yaml")
	overrideYAML := `tiers:
  fast:
    - anthropic/claude-haiku-4-5-20251001
  balanced:
    - anthropic/claude-sonnet-4-6
  strong:
    - anthropic/claude-opus-4-7
`
	require.NoError(t, os.WriteFile(path, []byte(overrideYAML), 0o644))

	t.Setenv(EnvKeyModelsConfig, path)
	cfg, err := LoadModelConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// User's override drops googleai entirely — a Gemini-only user
	// would now resolve to empty for every tier (fail loud rather
	// than quietly routing through a provider they didn't ask for).
	assert.Equal(t, []string{"anthropic/claude-haiku-4-5-20251001"}, cfg.Tiers[CapabilityFast])
	got := cfg.ResolveTier(CapabilityFast, DetectedProviders{GoogleAI: true})
	assert.Equal(t, "", got, "Gemini-only user gets empty when override lists only Anthropic")
}

func TestLoadModelConfig_MissingOverrideFileErrors(t *testing.T) {
	// User set the env var → they expect the file to exist. Silent
	// fallback to embedded would hide a typo.
	t.Setenv(EnvKeyModelsConfig, "/nonexistent/models.yaml")
	_, err := LoadModelConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent/models.yaml")
}

func TestLoadModelConfig_ProjectFileBeatsEmbedded(t *testing.T) {
	// .borg/models.yaml under the project root should win over the
	// embedded defaults when the env-var override is unset. Lets users
	// edit per-project model preferences without setting
	// LOCUTUS_MODELS_CONFIG.
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".borg"), 0o755))
	// manifest.json is the project root marker — the walk-up bails out
	// without it, falling back to the embedded defaults.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".borg/manifest.json"), []byte("{}"), 0o644))
	projectYAML := `tiers:
  fast:
    - anthropic/claude-haiku-4-5-20251001
  balanced:
    - anthropic/claude-sonnet-4-6
  strong:
    - anthropic/claude-opus-4-7
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".borg/models.yaml"), []byte(projectYAML), 0o644))

	t.Chdir(dir)
	t.Setenv(EnvKeyModelsConfig, "")

	cfg, err := LoadModelConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"anthropic/claude-haiku-4-5-20251001"}, cfg.Tiers[CapabilityFast],
		"project .borg/models.yaml should take precedence over embedded defaults")
}

func TestLoadModelConfig_EnvVarBeatsProjectFile(t *testing.T) {
	// Project has one config; user sets env var pointing elsewhere.
	// Env var wins (explicit > implicit).
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".borg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".borg/models.yaml"), []byte(`tiers:
  fast: [googleai/gemini-2.5-flash-lite]
  balanced: [googleai/gemini-2.5-flash]
  strong: [googleai/gemini-2.5-pro]
`), 0o644))

	overrideDir := t.TempDir()
	overridePath := filepath.Join(overrideDir, "override.yaml")
	require.NoError(t, os.WriteFile(overridePath, []byte(`tiers:
  fast: [anthropic/claude-haiku-4-5-20251001]
  balanced: [anthropic/claude-sonnet-4-6]
  strong: [anthropic/claude-opus-4-7]
`), 0o644))

	t.Chdir(projectDir)
	t.Setenv(EnvKeyModelsConfig, overridePath)

	cfg, err := LoadModelConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"anthropic/claude-haiku-4-5-20251001"}, cfg.Tiers[CapabilityFast],
		"env-var override should win over project .borg/models.yaml")
}

func TestParseModelConfig_EmptyTiersIsError(t *testing.T) {
	// A config with no tiers silently resolves everything to empty;
	// we want the loader to refuse it so misconfiguration is loud.
	_, err := parseModelConfig([]byte("tiers: {}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tiers map is empty")
}

func TestParseModelConfig_MalformedYAMLIsError(t *testing.T) {
	_, err := parseModelConfig([]byte("tiers:\n  fast: [\n"))
	require.Error(t, err)
}
