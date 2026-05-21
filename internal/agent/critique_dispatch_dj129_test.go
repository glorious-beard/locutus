package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFanoutCritiqueDimensionsEmitsOneItemPerDimension — fixture with
// 3 dimensions; assert 3 fanout items each carrying the full dimension
// + the expected AgentID + id format.
func TestFanoutCritiqueDimensionsEmitsOneItemPerDimension(t *testing.T) {
	s := &PlanningState{
		CurrentCritiqueDimensions: []CritiqueDimension{
			{ID: "cost-ceiling-coverage", Lens: "cost", FocusQuestion: "q1", SourceEvidence: []string{"e"}, Disciplines: []string{"web_grounded"}, SeverityFloor: "high"},
			{ID: "voter-file-privacy", Lens: "compliance", FocusQuestion: "q2", SourceEvidence: []string{"e"}, Disciplines: []string{"goals_grounded"}, SeverityFloor: "high"},
			{ID: "election-cycle-traffic", Lens: "sre", FocusQuestion: "q3", SourceEvidence: []string{"e"}, Disciplines: []string{"best_practice_grounded"}, SeverityFloor: "medium"},
		},
	}
	items, err := fanoutCritiqueDimensions(s)
	require.NoError(t, err)
	require.Len(t, items, 3)
	for i, raw := range items {
		var item CritiqueDimensionItem
		require.NoError(t, json.Unmarshal([]byte(raw), &item))
		assert.Equal(t, "spec_critic_elaborator", item.AgentID)
		assert.Equal(t, "crit:"+s.CurrentCritiqueDimensions[i].ID, item.ID)
		assert.Equal(t, s.CurrentCritiqueDimensions[i], item.Dimension)
	}
}

// TestFanoutCritiqueDimensionsHandlesEmptySet — returns empty slice
// without error when no dimensions are surfaced (e.g. iter-0 before
// any decisions exist).
func TestFanoutCritiqueDimensionsHandlesEmptySet(t *testing.T) {
	s := &PlanningState{}
	items, err := fanoutCritiqueDimensions(s)
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestRecordDimensionStabilityNeverOverwritesFirstSeen — once a
// dimension's id is recorded, subsequent calls preserve the original
// first-seen iteration (per design decision #7: retirement-then-
// recurrence is a positive signal, not a reset).
func TestRecordDimensionStabilityNeverOverwritesFirstSeen(t *testing.T) {
	s := &PlanningState{
		CritiqueDimensionsByIter: map[string]int{"cost-ceiling-coverage": 1},
	}
	current := []CritiqueDimension{{ID: "cost-ceiling-coverage", Lens: "cost", FocusQuestion: "q", SourceEvidence: []string{"e"}, Disciplines: []string{"web_grounded"}, SeverityFloor: "high"}}
	recordDimensionStability(s, current, 3)
	assert.Equal(t, 1, s.CritiqueDimensionsByIter["cost-ceiling-coverage"], "first-seen iter stays at 1 even when dimension recurs at iter 3")
}

// TestRecordDimensionStabilityRecordsNewIDs — a never-before-seen
// dimension lands in the map with the current iter as first-seen.
func TestRecordDimensionStabilityRecordsNewIDs(t *testing.T) {
	s := &PlanningState{}
	current := []CritiqueDimension{{ID: "voter-file-privacy", Lens: "compliance", FocusQuestion: "q", SourceEvidence: []string{"e"}, Disciplines: []string{"goals_grounded"}, SeverityFloor: "high"}}
	recordDimensionStability(s, current, 2)
	require.NotNil(t, s.CritiqueDimensionsByIter)
	assert.Equal(t, 2, s.CritiqueDimensionsByIter["voter-file-privacy"])
}

// TestDimensionStabilityAllowsRetirement — iter-N surfaces a SUBSET
// of the prior iteration's dimensions; assert dimensionsAreStable
// returns true.
func TestDimensionStabilityAllowsRetirement(t *testing.T) {
	s := &PlanningState{
		CritiqueDimensionsByIter: map[string]int{
			"cost-ceiling-coverage": 1,
			"voter-file-privacy":    1,
		},
		CurrentCritiqueDimensions: []CritiqueDimension{
			// Only one dimension surfaced this iter; the other retired.
			{ID: "cost-ceiling-coverage", Lens: "cost", FocusQuestion: "q", SourceEvidence: []string{"e"}, Disciplines: []string{"web_grounded"}, SeverityFloor: "high"},
		},
	}
	assert.True(t, dimensionsAreStable(s), "retirement is not churn")
}

// TestDimensionStabilityRejectsNewAddition — iter-N surfaces a
// dimension id not yet in CritiqueDimensionsByIter; assert false.
func TestDimensionStabilityRejectsNewAddition(t *testing.T) {
	s := &PlanningState{
		CritiqueDimensionsByIter: map[string]int{"cost-ceiling-coverage": 1},
		CurrentCritiqueDimensions: []CritiqueDimension{
			{ID: "cost-ceiling-coverage", Lens: "cost", FocusQuestion: "q", SourceEvidence: []string{"e"}, Disciplines: []string{"web_grounded"}, SeverityFloor: "high"},
			// Brand-new dimension not in the map yet.
			{ID: "voter-file-privacy", Lens: "compliance", FocusQuestion: "q", SourceEvidence: []string{"e"}, Disciplines: []string{"goals_grounded"}, SeverityFloor: "high"},
		},
	}
	assert.False(t, dimensionsAreStable(s), "new-dimension introduction blocks stability")
}

// TestDimensionStabilityAllowsRecurrence — iter-N surfaces a
// dimension that was retired in iter-(N-1) but appeared earlier; the
// id is already in the map so recurrence is not new-addition.
func TestDimensionStabilityAllowsRecurrence(t *testing.T) {
	s := &PlanningState{
		CritiqueDimensionsByIter: map[string]int{
			"voter-file-privacy": 1,
			// Recorded at iter 1; retired at iter 2; recurring now.
		},
		CurrentCritiqueDimensions: []CritiqueDimension{
			{ID: "voter-file-privacy", Lens: "compliance", FocusQuestion: "q", SourceEvidence: []string{"e"}, Disciplines: []string{"goals_grounded"}, SeverityFloor: "high"},
		},
	}
	assert.True(t, dimensionsAreStable(s), "recurrence of a previously-seen id is not new-addition")
}

// TestDimensionStabilityEmptySetIsStable — no current dimensions
// (e.g. iter-0 before any decisions) is trivially stable.
func TestDimensionStabilityEmptySetIsStable(t *testing.T) {
	s := &PlanningState{}
	assert.True(t, dimensionsAreStable(s))
}

// TestMergeScoutBriefPopulatesCritiqueDimensions — the scout's
// CritiqueDimensions[] flows onto state.CurrentCritiqueDimensions,
// and recordDimensionStability fires (the map gets the new ids).
func TestMergeScoutBriefPopulatesCritiqueDimensions(t *testing.T) {
	brief := ScoutBrief{
		DomainRead: "test",
		CritiqueDimensions: []CritiqueDimension{
			{ID: "cost-ceiling-coverage", Lens: "cost", FocusQuestion: "q", SourceEvidence: []string{"e"}, Disciplines: []string{"web_grounded"}, SeverityFloor: "high"},
		},
		Converged: false,
	}
	out, err := json.Marshal(brief)
	require.NoError(t, err)

	state := &PlanningState{}
	mergeScoutBrief(state, []RoundResult{{AgentID: "spec_scout", Output: string(out), IterationIndex: 2}})

	require.Len(t, state.CurrentCritiqueDimensions, 1)
	assert.Equal(t, "cost-ceiling-coverage", state.CurrentCritiqueDimensions[0].ID)
	assert.Equal(t, 2, state.CritiqueDimensionsByIter["cost-ceiling-coverage"])
}

// TestConvergenceRequiresDimensionStability — sanity check at the
// unit level that an unstable set returns false; the full integration
// is in Phase 6's TestDJ129DimensionInstabilityBlocksConvergence.
func TestConvergenceRequiresDimensionStability(t *testing.T) {
	s := &PlanningState{
		CritiqueDimensionsByIter: map[string]int{},
		CurrentCritiqueDimensions: []CritiqueDimension{
			{ID: "brand-new-dimension", Lens: "cost", FocusQuestion: "q", SourceEvidence: []string{"e"}, Disciplines: []string{"web_grounded"}, SeverityFloor: "high"},
		},
	}
	assert.False(t, dimensionsAreStable(s), "unit-level sanity check before the e2e exercise in Phase 6")
}
