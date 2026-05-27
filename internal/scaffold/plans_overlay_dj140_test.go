// DJ-140 phase 3 — the /goal wrapper is interactive-only.
//
// The Claude Code spec_refinement overlay carries a `/goal` directive,
// which is an interactive-only Claude Code feature that fails in
// headless ACP dispatch ("/goal isn't available in this environment").
// Phase 3 renames the overlay to carry the `.interactive` mode suffix
// so headless dispatch (which skips the mode tiers) falls through to
// the one-iteration runtime-neutral default `spec_refinement.md`.
//
// These tests resolve against the EMBEDDED plans FS, so they double as
// proof that the renamed file is actually embedded (the embed directive
// in scaffold.go is `//go:embed plans/*.md`, which globs the renamed
// file in).

package scaffold_test

import (
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecRefinementHeadlessResolvesToDefaultNotGoalWrapper — a
// headless claude-code request skips the mode tiers, so the
// .interactive overlay does not match; resolution falls through to the
// runtime-neutral default. The resolved body must NOT contain `/goal`
// (the interactive-only directive that breaks headless dispatch).
func TestSpecRefinementHeadlessResolvesToDefaultNotGoalWrapper(t *testing.T) {
	body, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "claude-code", scaffold.ModeHeadless)
	require.NoError(t, err)

	assert.Equal(t, "plans/spec_refinement.md", src,
		"headless claude-code must resolve to the one-iteration default, not the interactive /goal wrapper")
	assert.NotContains(t, string(body), "/goal",
		"the headless default must not carry the interactive-only /goal directive")
}

// TestSpecRefinementInteractiveResolvesToGoalWrapper — an interactive
// claude-code request activates the mode tiers, so the
// provider+mode overlay (spec_refinement.claude-code.interactive.md)
// wins. Its body carries the `/goal` directive. This also proves the
// renamed file is embedded.
func TestSpecRefinementInteractiveResolvesToGoalWrapper(t *testing.T) {
	body, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.NoError(t, err)

	assert.Equal(t, "plans/spec_refinement.claude-code.interactive.md", src,
		"interactive claude-code must resolve to the renamed /goal wrapper overlay")
	assert.True(t, strings.Contains(string(body), "/goal"),
		"the interactive overlay must carry the /goal directive")
}
