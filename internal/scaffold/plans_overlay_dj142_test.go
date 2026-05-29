// DJ-142 phase 3 — tier-3 interactive variant resolution.
//
// spec_refinement.interactive.md is the mode-only (tier-3) variant for
// interactive runtimes WITHOUT a runtime-specific overlay — Codex and
// Gemini, which have no native goal-loop, so the playbook itself drives
// the convergence loop via the spec_loop_* MCP tools. Claude Code uses
// its tier-2 .claude-code.md workflow playbook (DJ-144 deleted the
// tier-1 .claude-code.interactive.md /goal wrapper); headless dispatch
// still collapses to the runtime-neutral default.
//
// Resolution precedence (DJ-140): tier1 provider+mode → tier2 provider →
// tier3 mode → tier4 default. These tests pin the three relevant cells.

package scaffold_test

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecRefinementInteractiveCodexResolvesToModeTier — Codex has no
// provider overlay, so an interactive request misses tiers 1 and 2 and
// lands on the tier-3 mode-only variant spec_refinement.interactive.md.
func TestSpecRefinementInteractiveCodexResolvesToModeTier(t *testing.T) {
	_, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "codex", scaffold.ModeInteractive)
	require.NoError(t, err)

	assert.Equal(t, "plans/spec_refinement.interactive.md", src,
		"interactive codex (no provider overlay) must resolve to the tier-3 mode-only variant")
}

// TestSpecRefinementInteractiveClaudeCodeResolvesToWorkflowNotLoopTools —
// Claude Code's tier-2 provider overlay (spec_refinement.claude-code.md)
// outranks the tier-3 self-loop variant (spec_refinement.interactive.md).
// DJ-144 deleted the tier-1 /goal wrapper; the tier-2 workflow playbook
// is now the winning resolution for Claude Code interactive, preventing
// the spec_loop_* tools (denied to Claude Code by DJ-143) from being
// reached.
func TestSpecRefinementInteractiveClaudeCodeResolvesToWorkflowNotLoopTools(t *testing.T) {
	_, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.NoError(t, err)

	assert.Equal(t, "plans/spec_refinement.claude-code.md", src,
		"interactive claude-code must resolve to the tier-2 workflow playbook, not the tier-3 loop-tool variant (DJ-144)")
}

// TestSpecRefinementHeadlessCodexResolvesToDefault — headless dispatch
// skips the mode tiers, so codex headless falls through to the
// runtime-neutral default even though the tier-3 variant now exists.
func TestSpecRefinementHeadlessCodexResolvesToDefault(t *testing.T) {
	_, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "codex", scaffold.ModeHeadless)
	require.NoError(t, err)

	assert.Equal(t, "plans/spec_refinement.md", src,
		"headless codex must skip the mode tiers and resolve to the runtime-neutral default")
}
