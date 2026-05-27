// DJ-136 phase 3 — assertions on the one-iteration-shape refactor
// of the spec_refinement playbook. The default playbook is now
// shaped as one iteration's work plus a convergence-verdict report
// the harness reads. The outer loop lives in the harness (Claude
// Code's /goal evaluator on the claude-code path; Locutus's runner
// on the codex / gemini paths).

package plans_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadSpecRefinement returns the canonical spec_refinement.md
// content. Reads from the package directory at test time — go test
// sets cwd to the package directory.
func loadSpecRefinement(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("spec_refinement.md")
	require.NoError(t, err)
	return string(b)
}

// TestPlaybookSpecRefinement_HasPlanToolDirective — the playbook
// instructs the orchestrator to call TodoWrite (or its runtime's
// equivalent plan tool) early in the iteration. Surfaced by
// Phase 2's EventPlan rendering in the operator's view.
func TestPlaybookSpecRefinement_HasPlanToolDirective(t *testing.T) {
	body := loadSpecRefinement(t)
	assert.Contains(t, body, "TodoWrite",
		"playbook must name TodoWrite as the plan tool the orchestrator should call")
	assert.Contains(t, strings.ToLower(body), "plan tool",
		"playbook must describe the affordance generically so non-Claude runtimes can substitute their equivalent")
	assert.Contains(t, body, "in_progress",
		"playbook must instruct the orchestrator to update plan entry status as work proceeds")
	assert.Contains(t, body, "completed",
		"playbook must instruct the orchestrator to mark entries completed when work lands")
}

// TestPlaybookSpecRefinement_HasConvergenceVerdictDirective — the
// playbook instructs the orchestrator to surface the scout's
// verdict on the last line of its report so the harness (either
// /goal's evaluator on Claude Code or Locutus's outer loop on
// Codex / Gemini) can read it.
func TestPlaybookSpecRefinement_HasConvergenceVerdictDirective(t *testing.T) {
	body := loadSpecRefinement(t)
	assert.Contains(t, body, "converged: true",
		"playbook must show the verdict line's positive form so the harness knows what to look for")
	assert.Contains(t, body, "converged: false",
		"playbook must show the verdict line's negative form (with reason)")
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "verdict",
		"playbook must name the verdict-line concept so the orchestrator surfaces it deliberately")
	assert.Contains(t, lower, "harness reads",
		"playbook must say the harness reads the verdict — frames why the line exists")
}

// TestPlaybookSpecRefinement_NoOuterLoopFraming — retired phrases
// that drove the orchestrator's own outer loop must not appear in
// the one-iteration default. The harness owns the loop; the
// playbook does one iteration's work. Catches accidental
// regressions on Phase 3's refactor.
func TestPlaybookSpecRefinement_NoOuterLoopFraming(t *testing.T) {
	body := loadSpecRefinement(t)
	lower := strings.ToLower(body)
	retired := []string{
		"loop until",
		"run iterations until",
		"iteration n+1",
		"20 iterations",
		"iteration cap",
		"return to step 1",
		"## the loop",
	}
	for _, phrase := range retired {
		assert.NotContains(t, lower, phrase,
			"retired outer-loop phrase %q appears in spec_refinement.md — the harness owns the loop now",
			phrase)
	}
}
