// DJ-128 Phase 6 — tests for the cap-as-commit terminal: locks
// committed decisions, flips contested concerns to wontfix, emits one
// decision_locked event per locked decision, and excludes locked
// decisions from subsequent revise dispatches.

package agent

import (
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLockedDecisionsAreExcludedFromRevise — fixture: dec-X is
// locked; concern names dec-X only; hasReviseableConcerns returns
// false; fanoutReviseableConcerns produces zero items.
func TestLockedDecisionsAreExcludedFromRevise(t *testing.T) {
	state := makePriorWithAlt(t, "Original X", "alt-B")
	state.LockedDecisionIDs = map[string]struct{}{"dec-x": {}}
	state.Concerns = []Concern{{
		Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high",
		Text:               "dec-x is wrong",
		RelatedDecisionIDs: []string{"dec-x"},
	}}

	assert.False(t, hasReviseableConcerns(state),
		"a concern naming only a locked decision must not drive revise dispatch")

	items, err := fanoutReviseableConcerns(state)
	assert.NoError(t, err)
	assert.Empty(t, items,
		"fanoutReviseableConcerns must skip locked decisions")
}

// TestLockedDecisionsSurviveSubsequentRevise — mixed fixture: locked
// dec-X and unlocked dec-Y in the same concern's RelatedDecisionIDs;
// fanout produces one item for dec-Y only.
func TestLockedDecisionsSurviveSubsequentRevise(t *testing.T) {
	state := makePriorWithAlt(t, "Original X", "alt-B")
	// Add dec-y to the in-flight proposal.
	rawY := RawDecisionProposal{
		ID: "dec-y", Title: "Y", Rationale: "r", Confidence: 0.7,
		Alternatives: []spec.Alternative{{Name: "alt-Y", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://y", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:      []string{"axis-b"}, SurfacedBy: []string{"feat-y"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, rawY, 1)})

	state.LockedDecisionIDs = map[string]struct{}{"dec-x": {}}
	state.Concerns = []Concern{{
		Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high",
		Text:               "dec-x and dec-y both conflict on the cost-axis",
		RelatedDecisionIDs: []string{"dec-x", "dec-y"},
	}}

	assert.True(t, hasReviseableConcerns(state),
		"concern naming a mix of locked + unlocked stays reviseable on the unlocked")

	items, err := fanoutReviseableConcerns(state)
	assert.NoError(t, err)
	assert.Len(t, items, 1,
		"fanout produces one item for dec-Y only (dec-X is locked)")
}

// TestLockCappedDecisionsFlipsConcernsToWontfix — calling
// lockCappedDecisions on a fixture with one capped axis flips
// every open concern naming that axis's decision to wontfix.
func TestLockCappedDecisionsFlipsConcernsToWontfix(t *testing.T) {
	state := makePriorWithAlt(t, "Original X", "alt-B")
	state.AxisRevisionCount = map[string]int{"axis-a": 3}
	state.Concerns = []Concern{
		{
			Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high",
			Text:               "dec-x cost concern",
			RelatedDecisionIDs: []string{"dec-x"},
		},
		// Unrelated concern (no related dec); stays open.
		{
			Status: ConcernStatusOpen, AgentID: "architect_critic", Severity: "medium",
			Text:               "different concern",
			RelatedDecisionIDs: []string{"dec-other"},
		},
	}

	locked := lockCappedDecisions(state, []string{"axis-a"})
	require.Len(t, locked, 1, "axis-a covers one decision (dec-x)")
	assert.Equal(t, "dec-x", locked[0].Decision.ID)
	flipConcernsToWontfix(state, locked, 2, 3)
	assert.Equal(t, ConcernStatusWontfix, state.Concerns[0].Status,
		"concern naming the locked decision flips to wontfix")
	assert.Contains(t, state.Concerns[0].Justification, "revision cap",
		"justification names the cap signal")
	assert.Equal(t, ConcernStatusOpen, state.Concerns[1].Status,
		"unrelated concern stays open")
	assert.Contains(t, state.LockedDecisionIDs, "dec-x",
		"locked-decisions set records the lock")
}

// TestAdvisoryConcernsDoNotDriveRevise — DJ-128 Phase 2 / 6
// interaction: a concern whose counterproposal menu is entirely the
// "needs investigation" sentinel is marked Advisory at merge time;
// hasReviseableConcerns / fanoutReviseableConcerns must skip it.
func TestAdvisoryConcernsDoNotDriveRevise(t *testing.T) {
	state := makePriorWithAlt(t, "Original X", "alt-B")
	state.Concerns = []Concern{{
		Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high",
		Text:               "dec-x has a hard-to-verify cost claim",
		RelatedDecisionIDs: []string{"dec-x"},
		Advisory:           true,
	}}

	assert.False(t, hasReviseableConcerns(state),
		"Advisory concerns must not drive revise dispatch")
	items, err := fanoutReviseableConcerns(state)
	assert.NoError(t, err)
	assert.Empty(t, items, "fanout skips Advisory concerns")
}
