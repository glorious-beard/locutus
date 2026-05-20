// DJ-125 Phase 2 — InFlightManifest builder + renderer tests.

package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chetan/locutus/internal/spec"
)

// TestBuildManifestPopulatesAxisState exercises the three axis states.
// settled axes appear from in-flight decisions and from persisted
// decisions; open axes come from state.AxesOpen and must not be
// surfaced if a decision settles them.
func TestBuildManifestPopulatesAxisState(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{
				ID:    "dec-postgres",
				Title: "OLTP store",
				Axes:  []string{"oltp-store"},
			},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)

	state := &PlanningState{
		RawProposal: string(rawJSON),
		Existing: &ExistingSpec{
			Decisions: []spec.Decision{
				{
					ID:    "dec-auth",
					Title: "Auth provider",
					Axes:  []string{"auth-provider"},
				},
			},
		},
		AxesOpen: []OpenAxis{
			{ID: "rollout-cadence", Description: "How often we ship to production"},
			// This one should not appear as open — it's covered by
			// dec-postgres above. The builder reconciles to settled.
			{ID: "oltp-store", Description: "Where transactional state lives"},
		},
	}

	m, err := BuildManifest(state)
	require.NoError(t, err)

	axisByID := map[string]ManifestAxis{}
	for _, a := range m.Axes {
		axisByID[a.ID] = a
	}

	assert.Equal(t, ManifestAxisStateSettled, axisByID["oltp-store"].State)
	assert.Equal(t, "dec-postgres", axisByID["oltp-store"].SettledByDecisionID)

	assert.Equal(t, ManifestAxisStateSettled, axisByID["auth-provider"].State)
	assert.Equal(t, "dec-auth", axisByID["auth-provider"].SettledByDecisionID)

	assert.Equal(t, ManifestAxisStateOpen, axisByID["rollout-cadence"].State)
	assert.Empty(t, axisByID["rollout-cadence"].SettledByDecisionID)
}

// TestBuildManifestPopulatesDecisionState verifies decisions get
// settled_this_iter when their axes were decided at the most-recent
// iteration, settled_prior for older entries, and flagged when an
// open concern names them in RelatedDecisionIDs.
func TestBuildManifestPopulatesDecisionState(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{
				ID:    "dec-prior-postgres",
				Title: "OLTP store (iter 0)",
				Axes:  []string{"oltp-store"},
			},
			{
				ID:    "dec-this-iter-rollout",
				Title: "Rollout cadence (iter 1)",
				Axes:  []string{"rollout-cadence"},
			},
			{
				ID:    "dec-flagged-auth",
				Title: "Auth provider",
				Axes:  []string{"auth-provider"},
			},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)

	state := &PlanningState{
		RawProposal: string(rawJSON),
		DecidedAxesByIter: map[string]int{
			"oltp-store":       0,
			"rollout-cadence":  1,
			"auth-provider":    1,
		},
		Concerns: []Concern{
			{
				AgentID:            "architect_critic",
				Severity:           "medium",
				Text:               "dec-flagged-auth doesn't cover SSO",
				Status:             ConcernStatusOpen,
				RelatedDecisionIDs: []string{"dec-flagged-auth"},
			},
		},
	}

	m, err := BuildManifest(state)
	require.NoError(t, err)

	byID := map[string]ManifestDecision{}
	for _, d := range m.Decisions {
		byID[d.ID] = d
	}

	assert.Equal(t, ManifestDecisionStateSettledPrior, byID["dec-prior-postgres"].State,
		"older iter decision should be settled_prior")
	assert.Equal(t, ManifestDecisionStateSettledThisIter, byID["dec-this-iter-rollout"].State,
		"latest iter decision should be settled_this_iter")
	assert.Equal(t, ManifestDecisionStateFlagged, byID["dec-flagged-auth"].State,
		"decision named in an open concern should be flagged")
	assert.Equal(t, []string{"c-0"}, byID["dec-flagged-auth"].FlaggedConcernIDs)
}

// TestBuildManifestPendingNarrativeForNewNodes confirms NewSpecNode
// entries without an in-flight body land as pending_narrative.
func TestBuildManifestPendingNarrativeForNewNodes(t *testing.T) {
	state := &PlanningState{
		NewNodesFromScout: []NewSpecNode{
			{Kind: "feature", ID: "feat-dashboard", Title: "Dashboard", Summary: "Realtime view", Decisions: []string{"dec-stream"}},
			{Kind: "strategy", ID: "strat-observability", Title: "Observability", Summary: "Datadog + OTEL"},
		},
	}
	m, err := BuildManifest(state)
	require.NoError(t, err)

	require.Len(t, m.Features, 1)
	assert.Equal(t, ManifestNodeStatePendingNarrative, m.Features[0].State)
	assert.Equal(t, "feat-dashboard", m.Features[0].ID)
	assert.Equal(t, []string{"dec-stream"}, m.Features[0].Decisions)

	require.Len(t, m.Strategies, 1)
	assert.Equal(t, ManifestNodeStatePendingNarrative, m.Strategies[0].State)
	assert.Equal(t, "strat-observability", m.Strategies[0].ID)
}

// TestRenderManifestStableOutput verifies the renderer's output is
// deterministic across calls — the prompt-cache layer relies on
// identical bytes for repeated calls inside a fanout.
func TestRenderManifestStableOutput(t *testing.T) {
	state := makeFixtureState()
	m1, err := BuildManifest(state)
	require.NoError(t, err)
	m2, err := BuildManifest(state)
	require.NoError(t, err)

	assert.Equal(t, RenderManifest(m1), RenderManifest(m2),
		"two identical builds must produce identical render bytes")
}

// TestRenderManifestCompactSize is the regression guard against
// re-introducing blob projection. A 30-decision manifest's text
// rendering must stay under 8K characters; this is well below the
// 16K projection cap Phase 4 enforces.
func TestRenderManifestCompactSize(t *testing.T) {
	raw := RawSpecProposal{}
	for i := 0; i < 30; i++ {
		raw.Decisions = append(raw.Decisions, RawDecisionProposal{
			ID:       slugFromIndex("dec-", i),
			Title:    "Decision " + slugFromIndex("", i),
			Summary:  "Adopt option " + slugFromIndex("opt-", i) + " for axis " + slugFromIndex("axis-", i) + ".",
			Axes:     []string{slugFromIndex("axis-", i)},
			Rationale: strings.Repeat("rationale prose ", 50),
		})
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)

	m, err := BuildManifest(&PlanningState{RawProposal: string(rawJSON)})
	require.NoError(t, err)

	rendered := RenderManifest(m)
	assert.Less(t, len(rendered), 8000,
		"30-decision manifest render must stay compact; got %d chars", len(rendered))
}

// TestBuildManifestConcernsCarryStatus is a smoke check that the
// Concerns section mirrors the underlying state.Concerns values and
// defaults missing Status to "open" for legacy entries.
func TestBuildManifestConcernsCarryStatus(t *testing.T) {
	state := &PlanningState{
		Concerns: []Concern{
			{AgentID: "architect_critic", Text: "legacy entry"},
			{AgentID: "devops_critic", Text: "addressed entry", Status: ConcernStatusAddressed, Justification: "Now covered by dec-rollout"},
		},
	}
	m, err := BuildManifest(state)
	require.NoError(t, err)
	require.Len(t, m.Concerns, 2)
	assert.Equal(t, ConcernStatusOpen, m.Concerns[0].Status, "legacy entry defaults to open")
	assert.Equal(t, ConcernStatusAddressed, m.Concerns[1].Status)
	assert.Equal(t, "Now covered by dec-rollout", m.Concerns[1].Justification)
}

func makeFixtureState() *PlanningState {
	raw := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-dashboard", Title: "Dashboard", Summary: "Realtime telemetry view.", Decisions: []string{"dec-stream"}},
		},
		Strategies: []RawStrategyProposal{
			{ID: "strat-observability", Title: "Observability", Summary: "Datadog + OTEL.", Decisions: []string{"dec-otel"}},
		},
		Decisions: []RawDecisionProposal{
			{ID: "dec-stream", Title: "Streaming engine", Axes: []string{"streaming"}, Summary: "Adopt Redpanda."},
			{ID: "dec-otel", Title: "Telemetry pipeline", Axes: []string{"telemetry"}, Summary: "OpenTelemetry SDK + Datadog backend."},
		},
	}
	rawJSON, _ := json.Marshal(raw)
	return &PlanningState{
		RawProposal: string(rawJSON),
		DecidedAxesByIter: map[string]int{
			"streaming": 1,
			"telemetry": 1,
		},
		AxesOpen: []OpenAxis{
			{ID: "rollout-cadence", Description: "How often we ship to production."},
		},
		Concerns: []Concern{
			{AgentID: "cost_critic", Text: "Datadog is expensive at high cardinality"},
		},
	}
}

func slugFromIndex(prefix string, i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	a := letters[i%len(letters) : i%len(letters)+1]
	b := letters[(i*7)%len(letters) : (i*7)%len(letters)+1]
	return prefix + a + b
}
