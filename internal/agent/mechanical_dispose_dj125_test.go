// DJ-125 Phase 6 — mechanical concern-disposition pre-pass tests.

package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMechanicalDisposeConcernsStalesOnSettledAxis verifies a concern
// whose RelatedAxisIDs contains a settled axis gets staled.
func TestMechanicalDisposeConcernsStalesOnSettledAxis(t *testing.T) {
	state := &PlanningState{
		DecidedAxesByIter: map[string]int{"auth-provider": 1},
		Concerns: []Concern{
			{
				AgentID:        "architect_critic",
				Status:         ConcernStatusOpen,
				Text:           "Missing commitment on auth-provider",
				RelatedAxisIDs: []string{"auth-provider"},
			},
			{
				AgentID:        "architect_critic",
				Status:         ConcernStatusOpen,
				Text:           "Still need to choose oltp store",
				RelatedAxisIDs: []string{"oltp-store"},
			},
		},
	}
	mechanicalDisposeConcerns(state)

	assert.Equal(t, ConcernStatusStale, state.Concerns[0].Status,
		"axis-settled concern must transition to stale")
	assert.Equal(t, ConcernStatusOpen, state.Concerns[1].Status,
		"concern whose axis is not settled stays open")
}

// TestMechanicalDisposeConcernsLeavesContradictionsOpen verifies that
// a concern naming two decisions both present in the graph but whose
// text is NOT a missing-X pattern (a contradiction or interaction
// flag) stays open for the scout to grade.
func TestMechanicalDisposeConcernsLeavesContradictionsOpen(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-postgres", Title: "OLTP store"},
			{ID: "dec-streaming", Title: "Streaming engine"},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)

	state := &PlanningState{
		RawProposal: string(rawJSON),
		Concerns: []Concern{
			{
				AgentID:            "architect_critic",
				Status:             ConcernStatusOpen,
				Text:               "dec-postgres and dec-streaming contradict on transactional guarantees",
				RelatedDecisionIDs: []string{"dec-postgres", "dec-streaming"},
			},
		},
	}
	mechanicalDisposeConcerns(state)

	assert.Equal(t, ConcernStatusOpen, state.Concerns[0].Status,
		"contradiction concern must NOT be auto-staled — both decisions exist but the interaction is the concern")
}

// TestMechanicalDisposeConcernsStalesOnMissingPatternDecisionPresent
// verifies a missing-X concern whose named decision is now in the
// graph gets staled.
func TestMechanicalDisposeConcernsStalesOnMissingPatternDecisionPresent(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-rollout", Title: "Rollout cadence"},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)

	state := &PlanningState{
		RawProposal: string(rawJSON),
		Concerns: []Concern{
			{
				AgentID:            "architect_critic",
				Status:             ConcernStatusOpen,
				Text:               "Missing rollout-cadence commitment; need to land dec-rollout",
				RelatedDecisionIDs: []string{"dec-rollout"},
			},
		},
	}
	mechanicalDisposeConcerns(state)

	assert.Equal(t, ConcernStatusStale, state.Concerns[0].Status,
		"missing-X pattern + decision now present must transition to stale")
}

// TestMechanicalDisposeConcernsIdempotent verifies a second call
// produces the same state as the first.
func TestMechanicalDisposeConcernsIdempotent(t *testing.T) {
	state := &PlanningState{
		DecidedAxesByIter: map[string]int{"auth-provider": 1},
		Concerns: []Concern{
			{
				Status:         ConcernStatusOpen,
				Text:           "Missing auth-provider commitment",
				RelatedAxisIDs: []string{"auth-provider"},
			},
			{
				Status:         ConcernStatusOpen,
				Text:           "auth flow needs review",
				RelatedAxisIDs: nil,
			},
		},
	}
	mechanicalDisposeConcerns(state)
	firstPass := append([]Concern(nil), state.Concerns...)
	mechanicalDisposeConcerns(state)
	assert.Equal(t, firstPass, state.Concerns,
		"running mechanicalDisposeConcerns twice must be a no-op on the second call")
}

// TestMechanicalDisposeConcernsDoesNotTouchAddressed verifies the pass
// leaves addressed/wontfix concerns alone — those are scout-graded
// dispositions the mechanical pass must not overwrite.
func TestMechanicalDisposeConcernsDoesNotTouchAddressed(t *testing.T) {
	state := &PlanningState{
		DecidedAxesByIter: map[string]int{"auth-provider": 1},
		Concerns: []Concern{
			{
				Status:         ConcernStatusAddressed,
				Text:           "Missing auth-provider commitment",
				RelatedAxisIDs: []string{"auth-provider"},
				Justification:  "scout addressed in iter 1",
			},
			{
				Status:         ConcernStatusWontfix,
				Text:           "wontfix axis stays put",
				RelatedAxisIDs: []string{"auth-provider"},
			},
		},
	}
	mechanicalDisposeConcerns(state)
	assert.Equal(t, ConcernStatusAddressed, state.Concerns[0].Status)
	assert.Equal(t, ConcernStatusWontfix, state.Concerns[1].Status)
}
