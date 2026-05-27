// DJ-136 phase 4 — assertions specific to the spec_refinement
// Claude Code overlay. The overlay carries a `/goal` directive
// invoking the published `/locutus-refine` slash command; iteration
// is driven by Claude Code's goal evaluator. Tests guard:
//
//   - presence of the /goal directive + termination predicate,
//   - reference to the published slash command,
//   - size <= 4KB per the /goal length budget.

package plans_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DJ-140 phase 3: the /goal overlay is interactive-only — renamed
// with the .interactive mode suffix so headless ACP dispatch falls
// through to the one-iteration default. The overlay content is
// unchanged.
const claudeCodeOverlayFile = "spec_refinement.claude-code.interactive.md"

// loadClaudeCodeOverlay returns the canonical overlay content.
func loadClaudeCodeOverlay(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(claudeCodeOverlayFile)
	require.NoError(t, err)
	return string(b)
}

// TestClaudeCodeOverlay_HasGoalDirective — the overlay opens with a
// /goal directive and the termination predicate names the
// convergence verdict plus an iteration ceiling.
func TestClaudeCodeOverlay_HasGoalDirective(t *testing.T) {
	body := loadClaudeCodeOverlay(t)
	assert.True(t, strings.HasPrefix(body, "/goal "),
		"overlay must open with the /goal directive — Claude Code treats the first line as the goal definition")
	assert.Contains(t, body, "converged: true",
		"termination predicate must name the positive verdict the scout reports")
	assert.Contains(t, body, "20",
		"termination predicate must include an iteration ceiling (20 per the DJ)")
	assert.Contains(t, body, "locutus-refine",
		"overlay must invoke the published /locutus-refine slash command — that's what the goal loop dispatches per iteration")
}

// TestClaudeCodeOverlay_FitsIn4KB — Claude Code's /goal length
// budget is 4KB. Generous for a wrapper directive, but the bound
// catches accidental bloat that would only fail at session-start.
func TestClaudeCodeOverlay_FitsIn4KB(t *testing.T) {
	body := loadClaudeCodeOverlay(t)
	assert.LessOrEqual(t, len(body), 4000,
		"overlay body is %d bytes — Claude Code /goal accepts up to 4KB", len(body))
}
