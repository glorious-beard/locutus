// DJ-142 phase 4 — locks the interactive-command resolution for the
// self-loop tier-3 playbook. DJ-142 phase 3 added
// internal/scaffold/plans/spec_refinement.interactive.md (the
// self-loop variant referencing mcp__locutus__spec_loop_begin /
// spec_advance_iteration). With that tier-3 file present in
// .borg/plans/ (as `locutus update --reset` writes it), the publisher's
// mode=interactive resolution routes per runtime:
//
//   - Claude Code → tier-1 spec_refinement.claude-code.interactive.md
//     (the /goal wrapper) — unchanged from DJ-140.
//   - Codex / Gemini (no runtime overlay) → tier-3
//     spec_refinement.interactive.md (the NEW self-loop body).
//
// The publisher code already resolves mode=interactive per runtime
// (DJ-140), so no publisher code changes here — these tests lock the
// behaviour against regression.
//
// SEEDING NOTE (load-bearing): seedProject / seedProjectWithInteractiveOverlay
// write SPECIFIC files into a MemFS rather than copying the embedded
// scaffold, so the tier-3 file is NOT present unless we add it. These
// tests seed .borg/plans/spec_refinement.interactive.md from the REAL
// embedded body (scaffold.EmbeddedPlansFS()) so the test reflects what
// `locutus update --reset` actually publishes.

package publisher

import (
	"io/fs"
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedProjectWithSelfLoopTier3 builds the interactive-overlay layout
// (default one-iteration body + claude-code interactive /goal overlay)
// and additionally seeds the tier-3 self-loop body
// spec_refinement.interactive.md from the embedded scaffold — the
// layout `locutus update --reset` writes into .borg/plans/. With the
// tier-3 file present, Codex/Gemini interactive resolution lands on the
// self-loop body instead of falling through to the one-iteration default.
func seedProjectWithSelfLoopTier3(t *testing.T) specio.FS {
	t.Helper()
	fsys := seedProjectWithInteractiveOverlay(t) // default body + claude-code /goal overlay
	body, err := fs.ReadFile(scaffold.EmbeddedPlansFS(), "plans/spec_refinement.interactive.md")
	require.NoError(t, err, "embedded tier-3 self-loop playbook must exist (DJ-142 phase 3)")
	require.NoError(t, fsys.WriteFile(".borg/plans/spec_refinement.interactive.md", body, 0o644))
	// Sanity: the seeded tier-3 body is the self-loop variant.
	require.Contains(t, string(body), "spec_loop_begin",
		"embedded tier-3 body should reference the self-loop tool spec_loop_begin")
	return fsys
}

// TestPublisher_CodexInteractiveCommandIsSelfLoop — with the tier-3
// self-loop playbook present in .borg/plans/, Codex has no runtime
// overlay so its refine command resolves to spec_refinement.interactive.md
// (the self-loop body containing spec_loop_begin), NOT the /goal wrapper.
func TestPublisher_CodexInteractiveCommandIsSelfLoop(t *testing.T) {
	fsys := seedProjectWithSelfLoopTier3(t)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	codex, err := readAsString(fsys, ".codex/commands/locutus-refine.toml")
	require.NoError(t, err)
	assert.Contains(t, codex, "spec_loop_begin",
		"Codex refine command should resolve to the tier-3 self-loop body")
	assert.NotContains(t, codex, "/goal",
		"Codex refine command must not pick up the Claude Code /goal wrapper")
}

// TestPublisher_GeminiInteractiveCommandIsSelfLoop — same as Codex:
// Gemini has no runtime overlay, so its refine command (at
// .gemini/extensions/locutus/commands/locutus-refine.toml) resolves to
// the tier-3 self-loop body.
func TestPublisher_GeminiInteractiveCommandIsSelfLoop(t *testing.T) {
	fsys := seedProjectWithSelfLoopTier3(t)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	gem, err := readAsString(fsys, ".gemini/extensions/locutus/commands/locutus-refine.toml")
	require.NoError(t, err)
	assert.Contains(t, gem, "spec_loop_begin",
		"Gemini refine command should resolve to the tier-3 self-loop body")
	assert.NotContains(t, gem, "/goal",
		"Gemini refine command must not pick up the Claude Code /goal wrapper")
}

// TestPublisher_ClaudeCodeInteractiveCommandStillGoalWrapper —
// regression: Claude Code's tier-1 overlay
// (spec_refinement.claude-code.interactive.md) still wins over the
// tier-3 self-loop body, so .claude/commands/locutus-refine.md remains
// the /goal wrapper and does NOT pick up the self-loop body.
func TestPublisher_ClaudeCodeInteractiveCommandStillGoalWrapper(t *testing.T) {
	fsys := seedProjectWithSelfLoopTier3(t)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	cc, err := readAsString(fsys, ".claude/commands/locutus-refine.md")
	require.NoError(t, err)
	assert.Contains(t, cc, "/goal",
		"Claude Code tier-1 overlay wins: refine command stays the /goal wrapper")
	assert.NotContains(t, cc, "spec_loop_begin",
		"Claude Code must not pick up the tier-3 self-loop body")
}
