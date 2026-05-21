// DJ-125 Phase 5 — concern-extraction tests for mergeCriticIssues.

package agent

import (
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validCounterproposals returns a fully-grounded two-entry
// CriticCounterproposal slice for tests that need a non-degenerate
// issue. Pulls the construction out of every test so adding a new
// schema-validator field doesn't require touching every fixture.
func validCounterproposals() []CriticCounterproposal {
	return []CriticCounterproposal{{
		Option:   "Single-instance RDS Postgres on t4g.small reserved",
		Argument: "A reserved t4g.small lands under $30/mo and meets the JSONB requirement the analytics roadmap depends on.",
		Citations: []spec.Citation{{
			Kind:      "web",
			Reference: "https://aws.amazon.com/rds/postgresql/pricing/",
			Excerpt:   "db.t4g.small reserved (1-year, no upfront): $0.034/hr in us-east-1",
		}},
	}, {
		Option:   "Supabase Pro tier with PgBouncer",
		Argument: "Supabase's Pro tier bundles pooling and backups at a flat $25/mo and absorbs the ops burden the small team cannot carry.",
		Citations: []spec.Citation{{
			Kind:      "web",
			Reference: "https://supabase.com/pricing",
			Excerpt:   "Pro: $25/mo includes 8GB database storage, daily backups, and PgBouncer connection pooling.",
		}},
	}}
}

// TestMergeCriticIssuesExtractsDecisionIDs verifies decision-id
// references in concern text land on Concern.RelatedDecisionIDs.
func TestMergeCriticIssuesExtractsDecisionIDs(t *testing.T) {
	issues := CriticIssues{
		Issues: []CriticIssue{{
			Weakness:         "dec-postgres rationale doesn't address the 50ms p99 latency budget from GOALS §3.",
			Evidence:         "GOALS §3 names 50ms p99 as a hard ceiling and the rationale doesn't mention latency at all.",
			Counterproposals: validCounterproposals(),
		}, {
			Weakness:         "two unrelated decisions dec-stream and dec-cache contradict on the realtime path.",
			Evidence:         "dec-stream commits to push delivery while dec-cache implies request-pull semantics for the same data.",
			Counterproposals: validCounterproposals(),
		}},
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
		Issues: []CriticIssue{{
			Weakness:         "The auth-provider axis is settled but the auth flow doesn't cover SSO at the org tier.",
			Evidence:         "GOALS §4 names enterprise SSO as a tier-2 requirement; the auth-provider rationale stops at password auth.",
			Counterproposals: validCounterproposals(),
		}, {
			Weakness:         "oltp-store choice is risky for the workload described in GOALS.",
			Evidence:         "GOALS describes a bursty traffic pattern and the rationale assumes steady-state load.",
			Counterproposals: validCounterproposals(),
		}, {
			// Should NOT match: "auth" alone is not a known axis ID.
			Weakness:         "auth flow needs review against the new compliance regime.",
			Evidence:         "The compliance regime named in GOALS §5 added field-level audit trails that the flow does not produce.",
			Counterproposals: validCounterproposals(),
		}},
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
	issues := CriticIssues{Issues: []CriticIssue{{
		Weakness:         "some concern about the proposal's coverage of the GOALS §2 latency budget.",
		Evidence:         "GOALS §2 names p99 latency as a hard requirement; the proposal does not cite it anywhere.",
		Counterproposals: validCounterproposals(),
	}}}
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
