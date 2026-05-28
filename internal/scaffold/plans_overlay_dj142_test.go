// DJ-142 phase 3 — tier-3 interactive variant resolution.
//
// spec_refinement.interactive.md is the mode-only (tier-3) variant for
// interactive runtimes WITHOUT a runtime-specific overlay — Codex and
// Gemini, which have no native goal-loop, so the playbook itself drives
// the convergence loop via the spec_loop_* MCP tools. Claude Code keeps
// its richer tier-1 .claude-code.interactive.md wrapper; headless
// dispatch still collapses to the runtime-neutral default.
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

// TestSpecRefinementInteractiveClaudeCodeStillResolvesToWrapper — Claude
// Code's tier-1 provider+mode wrapper outranks the new tier-3 variant.
func TestSpecRefinementInteractiveClaudeCodeStillResolvesToWrapper(t *testing.T) {
	_, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.NoError(t, err)

	assert.Equal(t, "plans/spec_refinement.claude-code.interactive.md", src,
		"interactive claude-code must keep resolving to the tier-1 /goal wrapper, not the tier-3 mode variant")
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
