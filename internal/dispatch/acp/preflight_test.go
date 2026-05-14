package acp

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPreflightReportsMissingBinariesWithHints verifies that:
//   - every agent in AgentSpawns appears in the report;
//   - results are sorted by AgentID;
//   - found binaries carry the resolved path and an empty install hint;
//   - missing binaries carry a non-empty install hint matching the agent.
//
// exec.LookPath is stubbed via the package-level `lookPath` indirection so
// the test result doesn't depend on which ACP binaries are installed on the
// CI host.
func TestPreflightReportsMissingBinariesWithHints(t *testing.T) {
	// Stub: claude-agent-acp is present, the other two are missing.
	restore := stubLookPath(map[string]string{
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
	})
	defer restore()

	results := Preflight()
	require.Len(t, results, len(AgentSpawns))

	// Sorted by AgentID — claude-code, codex, gemini in that order.
	assert.Equal(t, "claude-code", results[0].AgentID)
	assert.Equal(t, "codex", results[1].AgentID)
	assert.Equal(t, "gemini", results[2].AgentID)

	// claude-code resolves.
	assert.True(t, results[0].Found)
	assert.Equal(t, "/usr/local/bin/claude-agent-acp", results[0].ResolvedAt)
	assert.Empty(t, results[0].InstallHint)
	assert.Equal(t, "claude-agent-acp", results[0].Binary)

	// codex missing → install hint mentions GitHub releases, NOT crates.io.
	assert.False(t, results[1].Found)
	assert.Empty(t, results[1].ResolvedAt)
	assert.Contains(t, results[1].InstallHint, "github.com/zed-industries/codex-acp")
	assert.NotContains(t, results[1].InstallHint, "cargo install codex-acp\n", "must not suggest cargo install as the primary install command")

	// gemini missing → install hint mentions the npm package.
	assert.False(t, results[2].Found)
	assert.Contains(t, results[2].InstallHint, "@google/gemini-cli")

	// MissingCount agrees with the rows.
	assert.Equal(t, 2, MissingCount(results))
}

// TestPreflightAllPresent verifies the happy path: when every binary is on
// PATH, no install hints surface and MissingCount is zero.
func TestPreflightAllPresent(t *testing.T) {
	restore := stubLookPath(map[string]string{
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
		"codex-acp":        "/usr/local/bin/codex-acp",
		"gemini":           "/usr/local/bin/gemini",
	})
	defer restore()

	results := Preflight()
	for _, r := range results {
		assert.True(t, r.Found, "expected %s to be found", r.AgentID)
		assert.Empty(t, r.InstallHint, "found agent %s should have no hint", r.AgentID)
	}
	assert.Equal(t, 0, MissingCount(results))
}

// TestPreflightAllMissing verifies that with nothing on PATH every agent
// reports a non-empty install hint.
func TestPreflightAllMissing(t *testing.T) {
	restore := stubLookPath(nil)
	defer restore()

	results := Preflight()
	require.Len(t, results, len(AgentSpawns))
	for _, r := range results {
		assert.False(t, r.Found, "expected %s to be missing", r.AgentID)
		assert.NotEmpty(t, r.InstallHint, "missing agent %s must carry an install hint", r.AgentID)
	}
	assert.Equal(t, len(AgentSpawns), MissingCount(results))
}

// stubLookPath replaces the package-level lookPath with a closure that
// returns the given path for binaries in `installed` and an error otherwise.
// Returns a restore function the caller defers.
func stubLookPath(installed map[string]string) func() {
	orig := lookPath
	lookPath = func(name string) (string, error) {
		if p, ok := installed[name]; ok {
			return p, nil
		}
		return "", errors.New("executable file not found in $PATH")
	}
	return func() { lookPath = orig }
}
