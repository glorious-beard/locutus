// DJ-140 phase 4 — the publisher resolves each runtime's slash-command
// body with mode=interactive. Claude Code's spec_refinement command
// becomes the `/goal` convergence wrapper (interactive convergence
// works inside a Claude Code TUI session); Codex and Gemini fall
// through to the cross-runtime one-iteration default body (they have
// no interactive convergence primitive).
//
// These tests seed the claude-code interactive overlay alongside the
// default in .borg/plans/ — the overlay-present layout the publisher
// reads after `locutus update --reset` copies the embedded scaffold.

package publisher

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedProjectWithInteractiveOverlay builds the seedProject layout and
// additionally writes the claude-code interactive overlay for
// spec_refinement so the interactive resolution finds it. The overlay
// body is the `/goal` wrapper; the default body is the one-iteration
// playbook (no `/goal`).
func seedProjectWithInteractiveOverlay(t *testing.T) specio.FS {
	t.Helper()
	fsys := seedProject(t, true) // includes the default one-iteration body
	require.NoError(t, fsys.WriteFile(".borg/plans/spec_refinement.claude-code.interactive.md", []byte(
		`/goal Drive the target node to convergence by repeatedly invoking the `+"`/locutus-refine`"+` slash command.

Inspect each run's final line — it carries a plain-text verdict in the form `+"`converged: true`"+` or `+"`converged: false; <reason>`"+`.
`), 0o644))
	return fsys
}

// TestPublisher_ClaudeCodeRefineCommandIsGoalWrapper — after publishing
// with the interactive overlay present, .claude/commands/locutus-refine.md
// carries the /goal directive (the interactive convergence wrapper),
// not the bare one-iteration default.
func TestPublisher_ClaudeCodeRefineCommandIsGoalWrapper(t *testing.T) {
	fsys := seedProjectWithInteractiveOverlay(t)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	cc, err := readAsString(fsys, ".claude/commands/locutus-refine.md")
	require.NoError(t, err)
	assert.Contains(t, cc, "/goal",
		"Claude Code refine command should be the interactive /goal convergence wrapper")
}

// TestPublisher_CodexAndGeminiRefineCommandsAreOneIterationBody —
// Codex and Gemini have no interactive overlay, so their refine
// commands fall through to the cross-runtime one-iteration default
// body and must NOT contain the /goal directive.
func TestPublisher_CodexAndGeminiRefineCommandsAreOneIterationBody(t *testing.T) {
	fsys := seedProjectWithInteractiveOverlay(t)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	codex, err := readAsString(fsys, ".codex/commands/locutus-refine.toml")
	require.NoError(t, err)
	assert.NotContains(t, codex, "/goal",
		"Codex refine command should be the one-iteration body, not the /goal wrapper")
	assert.Contains(t, codex, "Dispatch spec-scout",
		"Codex refine command should carry the default playbook body")

	gem, err := readAsString(fsys, ".gemini/extensions/locutus/commands/locutus-refine.toml")
	require.NoError(t, err)
	assert.NotContains(t, gem, "/goal",
		"Gemini refine command should be the one-iteration body, not the /goal wrapper")
	assert.Contains(t, gem, "Dispatch spec-scout",
		"Gemini refine command should carry the default playbook body")
}
