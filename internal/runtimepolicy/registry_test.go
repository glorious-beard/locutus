package runtimepolicy

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry_EmbeddedDefaults(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	floor, ok := reg.MinVersion("claude-code")
	assert.True(t, ok)
	assert.Equal(t, "2.1.154", floor)
	// Runtimes with empty floors report ok=false (no floor declared).
	_, ok = reg.MinVersion("codex")
	assert.False(t, ok)
}

func TestNewRegistry_ProjectOverride(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/runtimes.yaml", []byte(
		"runtimes:\n  claude-code:\n    min_version: \"2.2.0\"\n"), 0o644))
	reg, err := NewRegistry(fsys)
	require.NoError(t, err)
	floor, ok := reg.MinVersion("claude-code")
	assert.True(t, ok)
	assert.Equal(t, "2.2.0", floor, "project override replaces the default floor")
}

func TestNewRegistry_UnknownRuntimeRejected(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/runtimes.yaml", []byte(
		"runtimes:\n  nonsense:\n    min_version: \"1.0.0\"\n"), 0o644))
	_, err := NewRegistry(fsys)
	assert.Error(t, err, "a floor for a runtime absent from AgentSpawns is a config error")
}
