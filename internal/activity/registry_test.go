package activity

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry_LoadsEmbeddedDefaults(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	names := reg.Names()
	// One activity per CLI verb that dispatches to a coding-agent
	// runtime (refine, import, adopt, assimilate). See DJ-135 phase 5
	// CLI-verb → activity mapping.
	assert.Contains(t, names, "spec_refinement")
	assert.Contains(t, names, "feature_ingestion")
	assert.Contains(t, names, "code_adoption")
	assert.Contains(t, names, "code_assimilation")
}

func TestActivityRegistry_ResolvesByName(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)

	act, ok := reg.Lookup("spec_refinement")
	assert.True(t, ok)
	assert.Equal(t, "spec_refinement", act.Name)
	assert.NotEmpty(t, act.Runtimes, "default activities ship with non-empty runtime lists")

	_, ok = reg.Lookup("not-a-real-activity")
	assert.False(t, ok)
}

func TestAgentsYAML_ParsesAndValidates(t *testing.T) {
	cases := []struct {
		name      string
		yaml      string
		expectErr bool
		errSubstr string
	}{
		{
			name: "well-formed minimal",
			yaml: `activities:
  spec_refinement:
    runtimes: [claude-code]
`,
			expectErr: false,
		},
		{
			name:      "empty input",
			yaml:      ``,
			expectErr: true,
			errSubstr: "empty input",
		},
		{
			name: "missing activities map",
			yaml: `other-key: value
`,
			expectErr: true,
			errSubstr: "no activities defined",
		},
		{
			name:      "malformed yaml",
			yaml:      `activities: [not a map]`,
			expectErr: true,
			errSubstr: "parse",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAgentsYAML([]byte(tc.yaml))
			if tc.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAgentsYAML_RejectsUnknownRuntime(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents.yaml", []byte(`activities:
  bogus_activity:
    runtimes: [does-not-exist]
`), 0o600))

	_, err := NewRegistry(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `runtime "does-not-exist"`)
}

func TestAgentsYAML_RejectsEmptyRuntimesList(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents.yaml", []byte(`activities:
  empty_activity:
    runtimes: []
`), 0o600))

	_, err := NewRegistry(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtimes list is empty")
}

func TestAgentsYAML_ProjectOverridesShipDefaults(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents.yaml", []byte(`activities:
  spec_refinement:
    runtimes: [gemini]
  custom_activity:
    runtimes: [codex, claude-code]
`), 0o600))

	reg, err := NewRegistry(fsys)
	require.NoError(t, err)

	// spec_refinement: project override REPLACES the default list.
	// No partial-list merging — explicit override semantics per the
	// NewRegistry contract.
	act, ok := reg.Lookup("spec_refinement")
	require.True(t, ok)
	assert.Equal(t, []string{"gemini"}, act.Runtimes, "project override replaces default runtime list wholesale")

	// custom_activity: project-only entry comes through.
	act, ok = reg.Lookup("custom_activity")
	require.True(t, ok)
	assert.Equal(t, []string{"codex", "claude-code"}, act.Runtimes)

	// Default activities not mentioned in the override remain available.
	_, ok = reg.Lookup("code_adoption")
	assert.True(t, ok, "defaults survive when override omits them")
}

func TestAgentsYAML_MissingOverrideFileFallsBackToDefaults(t *testing.T) {
	fsys := specio.NewMemFS() // no .borg/agents.yaml
	reg, err := NewRegistry(fsys)
	require.NoError(t, err, "missing override file is not an error")

	// Same shape as TestActivityRegistry_ResolvesByName.
	_, ok := reg.Lookup("spec_refinement")
	assert.True(t, ok)
}

func TestRuntimeDetection_PrefersFirstAvailable(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)

	// Simulate a host where only codex is installed. The default
	// runtimes list is [claude-code, codex, gemini]; codex is the
	// only one that "resolves" — Resolve should pick it.
	resolver := &Resolver{
		LookPath: func(name string) (string, error) {
			if name == "codex-acp" {
				return "/fake/codex-acp", nil
			}
			return "", exec.ErrNotFound
		},
	}
	got, err := reg.Resolve("spec_refinement", resolver)
	require.NoError(t, err)
	assert.Equal(t, "codex", got)
}

func TestRuntimeDetection_FirstAvailableWinsOverLaterCandidates(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)

	// All three present; default list is [claude-code, codex, gemini];
	// claude-code (first listed) wins.
	resolver := &Resolver{
		LookPath: func(name string) (string, error) {
			return "/fake/" + name, nil
		},
	}
	got, err := reg.Resolve("spec_refinement", resolver)
	require.NoError(t, err)
	assert.Equal(t, "claude-code", got)
}

func TestRuntimeDetection_FailsCleanlyWhenNoneInstalled(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)

	resolver := &Resolver{
		LookPath: func(name string) (string, error) {
			return "", exec.ErrNotFound
		},
	}
	_, err = reg.Resolve("spec_refinement", resolver)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no runtime available")
	// Error names checked candidates so the operator knows what's
	// missing.
	assert.Contains(t, err.Error(), "claude-code")
	assert.Contains(t, err.Error(), "codex")
	assert.Contains(t, err.Error(), "gemini")
}

func TestRuntimeDetection_RejectsUnknownActivity(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)

	_, err = reg.Resolve("does-not-exist", &Resolver{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `not registered`)
}

// Compile-time check that errors.Is is referenced (used in detect.go
// indirectly via exec.ErrNotFound semantics).
var _ = errors.Is
