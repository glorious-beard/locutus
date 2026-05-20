// DJ-125 Phase 1 — Concern model expansion tests.
//
// Covers the new enum-shaped Status field and the deep-copy contract
// the orchestrator depends on for the per-Concern slice payload.

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestConcernStatusEnumValues pins the four disposition values DJ-125
// introduces. The schema description on Concern.Status names this exact
// set; any drift here without a matching schema update would let the
// scout grading pass return a value the workflow can't act on.
func TestConcernStatusEnumValues(t *testing.T) {
	assert.Equal(t, ConcernStatus("open"), ConcernStatusOpen)
	assert.Equal(t, ConcernStatus("addressed"), ConcernStatusAddressed)
	assert.Equal(t, ConcernStatus("stale"), ConcernStatusStale)
	assert.Equal(t, ConcernStatus("wontfix"), ConcernStatusWontfix)
}

// TestSnapshotPlanningStateDeepCopiesConcernFields verifies that
// snapshot consumers cannot mutate the orchestrator's Concerns by
// writing through the snapshot's RelatedDecisionIDs / RelatedAxisIDs
// slices. The orchestrator hands snapshots to parallel agents; a
// shared slice header would let one goroutine clobber another's view.
func TestSnapshotPlanningStateDeepCopiesConcernFields(t *testing.T) {
	original := &PlanningState{
		Concerns: []Concern{
			{
				AgentID:            "architect_critic",
				Severity:           "medium",
				Text:               "auth provider not committed",
				Status:             ConcernStatusOpen,
				RelatedDecisionIDs: []string{"dec-auth-provider"},
				RelatedAxisIDs:     []string{"auth-provider"},
			},
		},
	}

	snap := snapshotPlanningState(original)

	// Mutate via the snapshot. If snapshotPlanningState only copied the
	// outer Concerns slice header, this write would land on the same
	// backing array as original.Concerns[0].RelatedDecisionIDs.
	snap.Concerns[0].RelatedDecisionIDs[0] = "dec-mutated"
	snap.Concerns[0].RelatedAxisIDs[0] = "axis-mutated"

	assert.Equal(t, []string{"dec-auth-provider"}, original.Concerns[0].RelatedDecisionIDs,
		"snapshot mutation leaked into original RelatedDecisionIDs")
	assert.Equal(t, []string{"auth-provider"}, original.Concerns[0].RelatedAxisIDs,
		"snapshot mutation leaked into original RelatedAxisIDs")

	// Appending should also not propagate — a snapshot consumer can
	// grow its own per-Concern related-id list without trampling the
	// orchestrator's view.
	snap.Concerns[0].RelatedDecisionIDs = append(snap.Concerns[0].RelatedDecisionIDs, "dec-extra")
	assert.Len(t, original.Concerns[0].RelatedDecisionIDs, 1)
}
