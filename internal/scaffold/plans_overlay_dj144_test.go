package scaffold_test

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// After DJ-144, Claude Code interactive must resolve to the tier-2
// provider playbook (spec_refinement.claude-code.md), NOT the tier-3
// Codex/Gemini self-loop (spec_refinement.interactive.md), which uses
// spec_loop_* tools DJ-143 denies to Claude Code.
func TestClaudeCodeInteractiveDoesNotResolveLoopToolPlaybook(t *testing.T) {
	_, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "claude-code", scaffold.ModeInteractive)
	require.NoError(t, err)
	assert.NotEqual(t, "plans/spec_refinement.interactive.md", src,
		"claude-code interactive must not resolve the Codex/Gemini loop-tool playbook (DJ-143/DJ-144)")
	assert.Equal(t, "plans/spec_refinement.claude-code.md", src,
		"claude-code interactive must resolve the tier-2 workflow playbook")
}

// Codex/Gemini interactive must STILL resolve the tier-3 self-loop
// (DJ-142 unchanged).
func TestCodexInteractiveStillResolvesLoopToolPlaybook(t *testing.T) {
	_, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "codex", scaffold.ModeInteractive)
	require.NoError(t, err)
	assert.Equal(t, "plans/spec_refinement.interactive.md", src)
}
