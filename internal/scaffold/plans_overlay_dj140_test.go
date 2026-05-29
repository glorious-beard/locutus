// DJ-140 phase 3 — the /goal wrapper is interactive-only.
//
// The Claude Code spec_refinement overlay carries a `/goal` directive,
// which is an interactive-only Claude Code feature that fails in
// headless ACP dispatch ("/goal isn't available in this environment").
// Phase 3 renames the overlay to carry the `.interactive` mode suffix
// so headless dispatch (which skips the mode tiers) falls through to
// the one-iteration runtime-neutral default `spec_refinement.md`.
//
// DJ-144 update: Task 3.1 adds a tier-2 overlay
// spec_refinement.claude-code.md (mode-agnostic, provider-specific)
// that is the dynamic-workflow keyword playbook for headless dispatch.
// Headless claude-code now resolves to that tier-2 overlay rather than
// the default; the /goal guard still holds because the tier-2 file must
// not carry /goal (enforced by TestSpecRefinementClaudeCodeHeadlessPlaybook
// in spec_refinement_dj144_test.go).
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

// TestSpecRefinementHeadlessResolvesToWorkflowPlaybook — a headless
// claude-code request skips the mode tiers (so the .interactive
// overlay at tier 1 does not match) and resolves to the tier-2
// provider overlay spec_refinement.claude-code.md added by DJ-144.
// That overlay is the dynamic-workflow keyword playbook; it must NOT
// carry the `/goal` directive (which is interactive-only and breaks
// headless dispatch). Prior to DJ-144, this resolved to the
// runtime-neutral default; the tier-2 overlay now takes precedence.
func TestSpecRefinementHeadlessResolvesToWorkflowPlaybook(t *testing.T) {
	body, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "claude-code", scaffold.ModeHeadless)
	require.NoError(t, err)

	assert.Equal(t, "plans/spec_refinement.claude-code.md", src,
		"headless claude-code must resolve to the tier-2 workflow playbook (DJ-144), not the default or the interactive /goal wrapper")
	assert.NotContains(t, string(body), "/goal",
		"the headless workflow playbook must not carry the interactive-only /goal directive")
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
