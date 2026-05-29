package scaffold_test

import (
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecRefinementClaudeCodeHeadlessPlaybook(t *testing.T) {
	body, src, err := scaffold.ResolvePlaybook(
		scaffold.EmbeddedPlansFS(), "plans", "spec_refinement", "claude-code", scaffold.ModeHeadless)
	require.NoError(t, err)
	assert.Equal(t, "plans/spec_refinement.claude-code.md", src,
		"claude-code headless must resolve to the tier-2 workflow playbook")
	text := string(body)
	// Keyword trigger (DJ-144 §5): the word "workflow" must appear.
	assert.Contains(t, strings.ToLower(text), "workflow")
	// Cap-injection token (DJ-144 §6).
	assert.Contains(t, text, "{{max_iterations}}")
	// Must NOT reference the Codex/Gemini loop tools (DJ-143 denies them to CC).
	assert.NotContains(t, text, "spec_loop_begin")
	assert.NotContains(t, text, "spec_advance_iteration")
}
