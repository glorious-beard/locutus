// DJ-140 phase 4 — the publisher resolves each runtime's slash-command
// body with mode=interactive. After DJ-144, Claude Code's spec_refinement
// command is the tier-2 workflow playbook (spec_refinement.claude-code.md)
// rather than the deleted /goal wrapper; Codex and Gemini still fall
// through to the cross-runtime one-iteration default body (they have
// no interactive convergence primitive at tier 2 for spec_refinement).
//
// These tests seed the claude-code tier-2 workflow overlay alongside the
// default in .borg/plans/ — the overlay-present layout the publisher
// reads after `locutus update --reset` copies the embedded scaffold.

package publisher

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedProjectWithWorkflowOverlay builds the seedProject layout and
// additionally writes the claude-code tier-2 workflow overlay for
// spec_refinement so the interactive resolution finds it. The overlay
// body is the dynamic-workflow playbook (no /goal); the default body is
// the one-iteration playbook.
func seedProjectWithWorkflowOverlay(t *testing.T) specio.FS {
	t.Helper()
	fsys := seedProject(t, true) // includes the default one-iteration body
	require.NoError(t, fsys.WriteFile(".borg/plans/spec_refinement.claude-code.md", []byte(
		`# Spec Refinement (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives the spec graph to convergence.
`), 0o644))
	return fsys
}

// TestPublisher_ClaudeCodeRefineCommandIsWorkflowPlaybook — after publishing
// with the tier-2 workflow overlay present, .claude/commands/locutus-refine.md
// carries the workflow body (not the /goal wrapper, which was deleted by DJ-144).
func TestPublisher_ClaudeCodeRefineCommandIsWorkflowPlaybook(t *testing.T) {
	fsys := seedProjectWithWorkflowOverlay(t)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	cc, err := readAsString(fsys, ".claude/commands/locutus-refine.md")
	require.NoError(t, err)
	assert.Contains(t, cc, "workflow",
		"Claude Code refine command should be the dynamic-workflow playbook (DJ-144; /goal wrapper was deleted)")
	assert.NotContains(t, cc, "/goal",
		"Claude Code refine command must not carry the deleted /goal directive")
}

// TestPublisher_CodexAndGeminiRefineCommandsAreOneIterationBody —
// Codex and Gemini have no interactive overlay, so their refine
// commands fall through to the cross-runtime one-iteration default
// body and must NOT contain the /goal directive.
func TestPublisher_CodexAndGeminiRefineCommandsAreOneIterationBody(t *testing.T) {
	fsys := seedProjectWithWorkflowOverlay(t)
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
