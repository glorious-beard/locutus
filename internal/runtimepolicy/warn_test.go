package runtimepolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckVersion_WarnsBelowFloor(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	msg, warn := reg.CheckVersion("claude-code", "2.1.100")
	assert.True(t, warn)
	assert.Contains(t, msg, "claude-code")
	assert.Contains(t, msg, "2.1.100")
	assert.Contains(t, msg, "2.1.154")
}

func TestCheckVersion_NoWarnAtOrAboveFloor(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	_, warn := reg.CheckVersion("claude-code", "2.1.154")
	assert.False(t, warn)
	_, warn = reg.CheckVersion("claude-code", "2.3.0")
	assert.False(t, warn)
}

func TestCheckVersion_FailOpen(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	// No floor declared for codex → never warn.
	_, warn := reg.CheckVersion("codex", "0.0.1")
	assert.False(t, warn)
	// Unparseable detected version → fail open, never warn.
	_, warn = reg.CheckVersion("claude-code", "weird-build")
	assert.False(t, warn)
	// Empty detected version (runtime didn't report) → fail open.
	_, warn = reg.CheckVersion("claude-code", "")
	assert.False(t, warn)
	// Unknown runtime → no floor → never warn.
	_, warn = reg.CheckVersion("unknown", "1.0.0")
	assert.False(t, warn)
}
