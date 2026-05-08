package agent

import (
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
)

// TestComputeSpecDiff_AddedAbandonedStableModified covers the four
// change categories ComputeSpecDiff reports. Operator-visible delta
// reporting on `refine goals` (and any other re-run scenario) hangs
// on these counts — without them, every re-run is opaque about what
// actually changed.
func TestComputeSpecDiff_AddedAbandonedStableModified(t *testing.T) {
	before := &ExistingSpec{
		Features: []spec.Feature{
			{ID: "feat-stable", Title: "Stable feature", Description: "unchanged"},
			{ID: "feat-modified", Title: "Original title", Description: "old description"},
			{ID: "feat-abandoned", Title: "Going away", Description: "..."},
		},
		Decisions: []spec.Decision{
			{ID: "dec-stable", Title: "Stable decision", Rationale: "unchanged rationale"},
			{ID: "dec-abandoned", Title: "Going away", Rationale: "..."},
		},
		Strategies: []spec.Strategy{
			{ID: "strat-stable", Title: "Stable strategy"},
		},
	}
	after := &ExistingSpec{
		Features: []spec.Feature{
			{ID: "feat-stable", Title: "Stable feature", Description: "unchanged"},
			{ID: "feat-modified", Title: "Renamed title", Description: "new description"},
			{ID: "feat-new", Title: "New feature", Description: "fresh"},
		},
		Decisions: []spec.Decision{
			{ID: "dec-stable", Title: "Stable decision", Rationale: "unchanged rationale"},
			{ID: "dec-new", Title: "New decision", Rationale: "fresh rationale"},
		},
		Strategies: []spec.Strategy{
			{ID: "strat-stable", Title: "Stable strategy"},
		},
	}

	diff := ComputeSpecDiff(before, after)

	added, modified, abandoned, stable := diff.Counts()
	assert.Equal(t, 2, added, "feat-new + dec-new")
	assert.Equal(t, 1, modified, "feat-modified content changed")
	assert.Equal(t, 2, abandoned, "feat-abandoned + dec-abandoned")
	assert.Equal(t, 3, stable, "feat-stable + dec-stable + strat-stable")

	addedIDs := changeIDs(diff.Added)
	assert.ElementsMatch(t, []string{"feat-new", "dec-new"}, addedIDs)

	modifiedIDs := changeIDs(diff.Modified)
	assert.ElementsMatch(t, []string{"feat-modified"}, modifiedIDs)

	abandonedIDs := changeIDs(diff.Abandoned)
	assert.ElementsMatch(t, []string{"feat-abandoned", "dec-abandoned"}, abandonedIDs)
}

// TestComputeSpecDiff_GreenfieldFirstRun: before is empty, after
// has nodes. Everything is added; nothing modified/abandoned/stable.
// First-run case should not report misleading "abandoned" numbers
// (there was nothing to abandon).
func TestComputeSpecDiff_GreenfieldFirstRun(t *testing.T) {
	before := &ExistingSpec{}
	after := &ExistingSpec{
		Features: []spec.Feature{{ID: "feat-x", Title: "X"}},
		Decisions: []spec.Decision{
			{ID: "dec-y", Title: "Y", Rationale: "..."},
		},
	}

	diff := ComputeSpecDiff(before, after)
	added, modified, abandoned, stable := diff.Counts()

	assert.Equal(t, 2, added)
	assert.Equal(t, 0, modified)
	assert.Equal(t, 0, abandoned)
	assert.Equal(t, 0, stable)
}

// TestComputeSpecDiff_NoChange: before and after identical →
// everything stable, nothing added/modified/abandoned.
func TestComputeSpecDiff_NoChange(t *testing.T) {
	snap := &ExistingSpec{
		Features: []spec.Feature{{ID: "feat-a", Title: "A", Description: "d"}},
	}
	diff := ComputeSpecDiff(snap, snap)
	added, modified, abandoned, stable := diff.Counts()
	assert.Equal(t, 0, added)
	assert.Equal(t, 0, modified)
	assert.Equal(t, 0, abandoned)
	assert.Equal(t, 1, stable)
}

func changeIDs(changes []SpecChange) []string {
	ids := make([]string, 0, len(changes))
	for _, c := range changes {
		ids = append(ids, c.ID)
	}
	return ids
}
