// DJ-144 phase 3.5 — keyword-trigger headless playbooks for the three
// other convergent activities: feature_ingestion, code_adoption,
// code_assimilation.
//
// Each activity gets a tier-2 provider overlay
// <activity>.claude-code.md that is the dynamic-workflow keyword
// playbook for headless Claude Code dispatch. These three files
// mirror the pattern established for spec_refinement by Task 3.1.
//
// The tests assert that each overlay:
//   - Resolves as the tier-2 source for claude-code headless.
//   - Contains the "workflow" keyword (DJ-144 dynamic-workflow trigger).
//   - Contains the {{max_iterations}} cap-injection token (DJ-144 §6).
//   - Does NOT reference spec_loop_begin or spec_advance_iteration (DJ-143
//     denies those tools to Claude Code).
package scaffold_test

import (
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOtherConvergentActivitiesClaudeCodeHeadlessPlaybooks(t *testing.T) {
	for _, act := range []string{"feature_ingestion", "code_adoption", "code_assimilation"} {
		act := act
		t.Run(act, func(t *testing.T) {
			body, src, err := scaffold.ResolvePlaybook(
				scaffold.EmbeddedPlansFS(), "plans", act, "claude-code", scaffold.ModeHeadless)
			require.NoError(t, err)

			// Tier-2 source path: the provider overlay must win.
			assert.Equal(t, "plans/"+act+".claude-code.md", src,
				"claude-code headless must resolve to the tier-2 workflow playbook, not the base default")

			text := string(body)

			// Dynamic-workflow keyword (DJ-144 §5).
			assert.Contains(t, strings.ToLower(text), "workflow",
				"playbook must carry the 'workflow' framing keyword (DJ-144)")

			// Cap-injection token (DJ-144 §6).
			assert.Contains(t, text, "{{max_iterations}}",
				"playbook must carry the {{max_iterations}} cap token for harness injection")

			// Must NOT reference the Codex/Gemini loop tools (DJ-143 denies them to CC).
			assert.NotContains(t, text, "spec_loop_begin",
				"claude-code workflow playbook must not reference spec_loop_begin (DJ-143)")
			assert.NotContains(t, text, "spec_advance_iteration",
				"claude-code workflow playbook must not reference spec_advance_iteration (DJ-143)")
		})
	}
}
