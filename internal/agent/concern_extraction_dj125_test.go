// DJ-125 Phase 5 — concern-extraction tests for mergeCriticIssues.

package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergeCriticIssuesExtractsDecisionIDs verifies decision-id
// references in concern text land on Concern.RelatedDecisionIDs.
func TestMergeCriticIssuesExtractsDecisionIDs(t *testing.T) {
	issues := CriticIssues{
		Issues: []string{
			"dec-postgres rationale doesn't address the 50ms p99 latency budget",
			"two unrelated decisions dec-stream and dec-cache contradict",
		},
	}
	out, err := json.Marshal(issues)
	require.NoError(t, err)

	state := &PlanningState{}
	mergeCriticIssues(state, []RoundResult{
		{AgentID: "architect_critic", Output: string(out), IterationIndex: 2},
	})

	// One per issue + zero integrity findings on empty state.
	require.GreaterOrEqual(t, len(state.Concerns), 2)
	first := state.Concerns[0]
	assert.Equal(t, []string{"dec-postgres"}, first.RelatedDecisionIDs)
	assert.Equal(t, 2, first.IterationRaised)
	assert.Equal(t, ConcernStatusOpen, first.Status)

	second := state.Concerns[1]
	assert.ElementsMatch(t, []string{"dec-stream", "dec-cache"}, second.RelatedDecisionIDs)
}

// TestMergeCriticIssuesExtractsAxisIDs verifies that axis IDs known
// to the council (settled in DecidedAxesByIter or open in AxesOpen)
// land on Concern.RelatedAxisIDs when they appear in concern text.
func TestMergeCriticIssuesExtractsAxisIDs(t *testing.T) {
	issues := CriticIssues{
		Issues: []string{
			"The auth-provider axis is settled but the auth flow doesn't cover SSO",
			"oltp-store choice is risky for the workload",
			// Should NOT match: "auth" alone is not a known axis ID.
			"auth flow needs review",
		},
	}
	out, err := json.Marshal(issues)
	require.NoError(t, err)

	state := &PlanningState{
		DecidedAxesByIter: map[string]int{"auth-provider": 1, "oltp-store": 0},
		AxesOpen: []OpenAxis{
			{ID: "rollout-cadence"},
		},
	}
	mergeCriticIssues(state, []RoundResult{
		{AgentID: "architect_critic", Output: string(out), IterationIndex: 3},
	})

	require.Len(t, state.Concerns, 3)
	assert.Equal(t, []string{"auth-provider"}, state.Concerns[0].RelatedAxisIDs)
	assert.Equal(t, []string{"oltp-store"}, state.Concerns[1].RelatedAxisIDs)
	assert.Empty(t, state.Concerns[2].RelatedAxisIDs,
		"axis-substring (auth) without whole-word match must not produce a related-axis hit")
}

// TestMergeCriticIssuesPopulatesIterationRaised verifies the iteration
// index threads through from RoundResult onto the new concern.
func TestMergeCriticIssuesPopulatesIterationRaised(t *testing.T) {
	issues := CriticIssues{Issues: []string{"some concern"}}
	out, _ := json.Marshal(issues)
	state := &PlanningState{}

	mergeCriticIssues(state, []RoundResult{
		{AgentID: "devops_critic", Output: string(out), IterationIndex: 4},
	})
	require.Len(t, state.Concerns, 1)
	assert.Equal(t, 4, state.Concerns[0].IterationRaised)
	assert.Equal(t, ConcernStatusOpen, state.Concerns[0].Status)
}

// TestExtractAxisRefsWholeWordBoundary pins the whole-word behaviour
// of axisIDInText — "auth-provider" must not match inside
// "authentication-provider".
func TestExtractAxisRefsWholeWordBoundary(t *testing.T) {
	knownAxes := []string{"auth-provider", "rollout-cadence"}
	got := extractAxisRefsFromText("we are switching to authentication-provider-v2", knownAxes)
	assert.Empty(t, got, "axis ID embedded inside a larger slug must not match")

	got = extractAxisRefsFromText("the auth-provider decision is fine; rollout-cadence is open", knownAxes)
	assert.ElementsMatch(t, []string{"auth-provider", "rollout-cadence"}, got)
}
