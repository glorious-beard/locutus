// DJ-136 phase 4 — assertions specific to the Claude Code spec_refinement
// playbook. DJ-144 deleted the /goal wrapper overlay
// (spec_refinement.claude-code.interactive.md) and the tier-2 workflow
// playbook (spec_refinement.claude-code.md) is now what Claude Code
// resolves for BOTH interactive and headless contexts. Tests guard:
//
//   - presence of the "workflow" framing (dynamic-workflow keyword),
//   - absence of the /goal directive (interactive-only, breaks headless),
//   - size budget (no accidental bloat).

package plans_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DJ-144 phase 4: the /goal wrapper was deleted; the tier-2 workflow
// playbook is now the canonical Claude Code playbook for spec_refinement.
const claudeCodeWorkflowFile = "spec_refinement.claude-code.md"

// loadClaudeCodeWorkflow returns the tier-2 workflow playbook content.
func loadClaudeCodeWorkflow(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(claudeCodeWorkflowFile)
	require.NoError(t, err)
	return string(b)
}

// TestClaudeCodeWorkflow_IsWorkflowShaped — the playbook opens with the
// "workflow" framing keyword established in DJ-144 and must NOT carry
// the /goal directive (which is interactive-only and breaks headless
// ACP dispatch).
func TestClaudeCodeWorkflow_IsWorkflowShaped(t *testing.T) {
	body := loadClaudeCodeWorkflow(t)
	assert.True(t, strings.Contains(body, "workflow"),
		"tier-2 playbook must carry the 'workflow' framing (DJ-144 dynamic-workflow)")
	assert.False(t, strings.HasPrefix(body, "/goal "),
		"tier-2 playbook must not open with /goal — that directive is interactive-only and breaks headless ACP dispatch")
	assert.NotContains(t, body, "/goal",
		"tier-2 playbook must not reference /goal at all")
}

// TestClaudeCodeWorkflow_FitsReasonableSizeLimit — guard against
// accidental bloat; the workflow playbook is intentionally richer than
// the old /goal wrapper, but shouldn't balloon past 64KB.
func TestClaudeCodeWorkflow_FitsReasonableSizeLimit(t *testing.T) {
	body := loadClaudeCodeWorkflow(t)
	assert.LessOrEqual(t, len(body), 64*1024,
		"workflow playbook body is %d bytes — unexpected bloat", len(body))
}
