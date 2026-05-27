// DJ-139 phase 6 — assertions on the goal-layer sync + citation-walk
// extensions to the spec_refinement playbook. The playbook now runs
// three steps in order: (1) goal-layer sync against GOALS.md with a
// manifest-hash short-circuit; (2) the existing one-iteration spec
// refinement against the manifest (goal layer is implicit context);
// (3) a citation walk that judges .advances / .respects on touched
// nodes against the final goal-layer state. See DJ-139 phase 6.

package plans_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecRefinementPlaybookReferencesGoalDiffMatcher — Step 0 dispatches
// the spec-goal-diff-matcher subagent when GOALS.md has changed since
// the last sync. The playbook must name the agent by its hyphenated id
// so the runtime's Task tool can resolve it from the published agent set.
func TestSpecRefinementPlaybookReferencesGoalDiffMatcher(t *testing.T) {
	body := loadSpecRefinement(t)
	assert.Contains(t, body, "spec-goal-diff-matcher",
		"playbook must name the matcher subagent by its hyphenated id (Step 0 dispatch)")
}

// TestSpecRefinementPlaybookReferencesManifestHash — Step 0's
// short-circuit reads the manifest's goals_md_hash and compares it
// against the current GOALS.md hash. The playbook must name the
// manifest hash, the manifest-update tool, and the Bash + shasum
// path the agent uses to compute the new hash.
func TestSpecRefinementPlaybookReferencesManifestHash(t *testing.T) {
	body := loadSpecRefinement(t)
	assert.Contains(t, body, "goals_md_hash",
		"playbook must name the manifest's goals_md_hash field that drives the short-circuit")
	assert.Contains(t, body, "mcp__locutus__spec_update_goals_md_hash",
		"playbook must name the manifest-update tool the agent calls after a successful sync")
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "shasum",
		"playbook must give the agent a concrete hash-computation path (Bash + shasum) for GOALS.md")
}

// TestSpecRefinementPlaybookDescribesThreeStepsInOrder — the playbook
// now organises its work as sync → iterate → cite. The three section
// headers must appear in that order in the file so the orchestrator
// reads them in the right sequence.
func TestSpecRefinementPlaybookDescribesThreeStepsInOrder(t *testing.T) {
	body := loadSpecRefinement(t)
	syncIdx := strings.Index(body, "## Step 0")
	iterIdx := strings.Index(body, "## The iteration")
	citeIdx := strings.Index(body, "## Step N+1")
	require.Greater(t, syncIdx, -1, "playbook must have a Step 0 header (goal-layer sync)")
	require.Greater(t, iterIdx, -1, "playbook must preserve the existing iteration section")
	require.Greater(t, citeIdx, -1, "playbook must have a Step N+1 (citation walk) header")
	assert.Less(t, syncIdx, iterIdx,
		"Step 0 (sync) must come before the iteration section")
	assert.Less(t, iterIdx, citeIdx,
		"Iteration section must come before the Step N+1 citation walk")
}

// TestSpecRefinementPlaybookReferencesAtRiskSurface — the final
// report includes a "Features without goal anchors" section when
// the citation walk leaves any feature with an empty .advances
// list. The playbook must name the surface so the orchestrator
// produces it in the report.
func TestSpecRefinementPlaybookReferencesAtRiskSurface(t *testing.T) {
	body := loadSpecRefinement(t)
	assert.Contains(t, body, "Features without goal anchors",
		"playbook must name the at-risk surface heading in the final report")
}

// TestSpecRefinementPlaybookReferencesBootstrapAffordance — the
// one-time first-run affordance lets the matcher treat existing
// scope-encoding decisions as secondary claim sources alongside
// GOALS.md. The playbook must mark this as conditional on the
// first run (no existing goal-* / agoal-* nodes), not a permanent
// feature of the workflow.
func TestSpecRefinementPlaybookReferencesBootstrapAffordance(t *testing.T) {
	body := loadSpecRefinement(t)
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "first run",
		"playbook must scope the bootstrap affordance to the first run")
	assert.Contains(t, lower, "secondary",
		"playbook must describe scope-encoding decisions as a secondary claim source on bootstrap")
	assert.Contains(t, lower, "scope-encoding",
		"playbook must name the scope-encoding decisions the bootstrap affordance applies to")
}
