// DJ-128 — mergeCriticIssues tests for the structured CriticIssue
// shape: counterproposal-menu population, RelatedDecisionIDs union,
// sentinel-only concerns marked advisory, mixed concrete+sentinel
// menus remain reviseable.

package agent

import (
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergeCriticIssuesPopulatesCounterproposalMenu — a critic emits
// one issue with multiple counterproposals; the merged Concern carries
// each Option / Argument / Citations verbatim.
func TestMergeCriticIssuesPopulatesCounterproposalMenu(t *testing.T) {
	issue := CriticIssue{
		Weakness: "The dec-postgres rationale does not engage with the $150/mo ceiling in GOALS §3.",
		Evidence: "GOALS §3 names the ceiling explicitly and the rationale only addresses query performance.",
		Counterproposals: []CriticCounterproposal{{
			Option:   "Single-instance RDS Postgres on t4g.small reserved",
			Argument: "A reserved t4g.small lands under $30/mo and meets the JSONB requirement.",
			Citations: []spec.Citation{{
				Kind: "web", Reference: "https://aws.amazon.com/rds/postgresql/pricing/",
				Excerpt: "db.t4g.small reserved: $0.034/hr in us-east-1",
			}},
		}, {
			Option:   "Supabase Pro tier with PgBouncer",
			Argument: "Supabase's Pro tier bundles pooling and backups at a flat $25/mo.",
			Citations: []spec.Citation{{
				Kind: "web", Reference: "https://supabase.com/pricing",
				Excerpt: "Pro: $25/mo includes PgBouncer connection pooling.",
			}},
		}, {
			Option:   "DigitalOcean Managed Postgres basic plan",
			Argument: "DO Managed Postgres at $15/mo gives backups + monitoring; the team already runs DO droplets.",
			Citations: []spec.Citation{{
				Kind: "web", Reference: "https://www.digitalocean.com/pricing/managed-databases",
				Excerpt: "Basic: $15/mo for 1GB RAM / 10GB storage.",
			}},
		}},
	}
	out, err := json.Marshal(CriticIssues{Issues: []CriticIssue{issue}})
	require.NoError(t, err)

	state := &PlanningState{}
	mergeCriticIssues(state, []RoundResult{
		{AgentID: "cost_critic", Output: string(out), IterationIndex: 2},
	})
	require.Len(t, state.Concerns, 1)
	got := state.Concerns[0]
	require.Len(t, got.Counterproposals, 3)
	for i, want := range issue.Counterproposals {
		assert.Equal(t, want.Option, got.Counterproposals[i].Option, "Option preserved verbatim")
		assert.Equal(t, want.Argument, got.Counterproposals[i].Argument, "Argument preserved verbatim")
		assert.Equal(t, want.Citations, got.Counterproposals[i].Citations, "Citations preserved verbatim")
	}
	assert.False(t, got.Advisory, "concrete counterproposals must not mark the concern advisory")
}

// TestMergeCriticIssuesUnionsRelatedDecisionIDs — critic emits
// explicit RelatedDecisionIDs `[dec-x]`; the text mentions `dec-y`;
// the merged Concern carries both, critic-provided first.
func TestMergeCriticIssuesUnionsRelatedDecisionIDs(t *testing.T) {
	issue := CriticIssue{
		Weakness:           "The dec-y rationale doesn't engage with the cost ceiling.",
		Evidence:           "GOALS §3 names the ceiling explicitly.",
		Counterproposals:   []CriticCounterproposal{validCounterproposals()[0]},
		RelatedDecisionIDs: []string{"dec-x"},
	}
	out, err := json.Marshal(CriticIssues{Issues: []CriticIssue{issue}})
	require.NoError(t, err)

	state := &PlanningState{}
	mergeCriticIssues(state, []RoundResult{
		{AgentID: "cost_critic", Output: string(out), IterationIndex: 1},
	})
	require.Len(t, state.Concerns, 1)
	got := state.Concerns[0]
	assert.Equal(t, []string{"dec-x", "dec-y"}, got.RelatedDecisionIDs,
		"critic-provided ids come first, then regex-extracted")
}

// TestMergeCriticIssuesMarksSentinelConcernsAdvisory — a concern
// whose only counterproposal is the sentinel is marked Advisory and
// hasReviseableConcerns skips it.
func TestMergeCriticIssuesMarksSentinelConcernsAdvisory(t *testing.T) {
	issue := CriticIssue{
		Weakness: "The dec-x rationale's cost claim is hard to verify without internal benchmarking data.",
		Evidence: "The cited vendor page is paywalled and the rationale doesn't link to a primary source.",
		Counterproposals: []CriticCounterproposal{{
			Option:   "needs investigation",
			Argument: "The critic flags a real problem but cannot name a specific alternative this turn.",
		}},
	}
	out, err := json.Marshal(CriticIssues{Issues: []CriticIssue{issue}})
	require.NoError(t, err)

	state := &PlanningState{}
	mergeCriticIssues(state, []RoundResult{
		{AgentID: "cost_critic", Output: string(out), IterationIndex: 2},
	})
	require.Len(t, state.Concerns, 1)
	got := state.Concerns[0]
	assert.True(t, got.Advisory, "sentinel-only counterproposal menu must mark the concern Advisory")
	assert.Equal(t, ConcernStatusOpen, got.Status,
		"Advisory concerns still carry Status=open; the gate against revise is the Advisory flag")
}

// TestMergeCriticIssuesMixedSentinelAndConcrete — a concern with a
// mix of concrete + sentinel counterproposals is NOT advisory; the
// concrete entry keeps it reviseable while the sentinel is preserved
// in the menu.
func TestMergeCriticIssuesMixedSentinelAndConcrete(t *testing.T) {
	issue := CriticIssue{
		Weakness: "The dec-x latency claim isn't grounded in measurable evidence.",
		Evidence: "GOALS §2 names a 50ms p99 budget and the rationale doesn't cite any measurement.",
		Counterproposals: []CriticCounterproposal{{
			Option:   "Adopt single-region ECS Fargate with the smallest task size",
			Argument: "ECS Fargate at 0.25 vCPU / 512MB satisfies the latency budget under expected load and lands under the cost ceiling.",
			Citations: []spec.Citation{{
				Kind: "web", Reference: "https://aws.amazon.com/fargate/pricing/",
				Excerpt: "0.25 vCPU / 512MB Fargate task: $0.012/hr in us-east-1.",
			}},
		}, {
			Option:   "needs investigation",
			Argument: "The critic also flags a measurement gap the elaborator should follow up on.",
		}},
	}
	out, err := json.Marshal(CriticIssues{Issues: []CriticIssue{issue}})
	require.NoError(t, err)

	state := &PlanningState{}
	mergeCriticIssues(state, []RoundResult{
		{AgentID: "architect_critic", Output: string(out), IterationIndex: 2},
	})
	require.Len(t, state.Concerns, 1)
	got := state.Concerns[0]
	assert.False(t, got.Advisory, "mixed concrete + sentinel menu must NOT be advisory")
	require.Len(t, got.Counterproposals, 2, "both counterproposals preserved in the menu")
	assert.Equal(t, "needs investigation", got.Counterproposals[1].Option)
}

// TestMergeCriticIssuesDegenerateOutputFallsBackToRawText — when the
// validator classifies a critic output as degenerate, the raw output
// text still lands as a concern (with no counterproposal menu) so the
// operator sees what the critic emitted.
func TestMergeCriticIssuesDegenerateOutputFallsBackToRawText(t *testing.T) {
	raw := `{"issues":[{"weakness":"x","evidence":"y","counterproposals":[{"option":"dummy","argument":"z","citations":[]}]}]}`
	state := &PlanningState{}
	mergeCriticIssues(state, []RoundResult{
		{AgentID: "cost_critic", Output: raw, IterationIndex: 2},
	})
	require.Len(t, state.Concerns, 1)
	got := state.Concerns[0]
	assert.Equal(t, raw, got.Text, "degenerate output falls back to raw text concern")
	assert.Empty(t, got.Counterproposals, "no counterproposal menu attached to fallback concern")
}
