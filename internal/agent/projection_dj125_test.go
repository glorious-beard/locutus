// DJ-125 Phase 4 — manifest-based projection tests.

package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectChallengeRendersManifest verifies the critic projection
// surfaces the manifest content and instructs the critic to fetch
// full bodies via spec_get rather than dumping the proposal verbatim.
func TestProjectChallengeRendersManifest(t *testing.T) {
	state := PlanningState{
		Prompt:       "## GOALS.md\n\ngreenfield project",
		ProposedSpec: `{"features":[{"id":"feat-x","title":"X"}]}`,
		RawProposal:  `{"features":[{"id":"feat-x","title":"X","summary":"X summary."}],"decisions":[{"id":"dec-x","title":"X dec","summary":"X dec summary."}]}`,
	}
	snap := StateSnapshot[PlanningState]{State: state}
	msgs := projectChallenge(snap)

	combined := combineMessages(msgs)
	assert.Contains(t, combined, "## In-flight spec manifest")
	assert.Contains(t, combined, "feat-x")
	assert.Contains(t, combined, "dec-x")
	assert.Contains(t, combined, "spec_get")
	// Must NOT dump the raw proposal JSON blob.
	assert.NotContains(t, combined, `"features":[{"id":"feat-x"`,
		"projection must not dump the raw proposal JSON; the manifest replaces it")
}

// TestProjectScoutIncludesManifestAndConcerns verifies the scout's
// projection carries the manifest plus the concerns section with
// concern indices the scout can dispose by ID.
func TestProjectScoutIncludesManifestAndConcerns(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-postgres", Title: "OLTP store", Summary: "Adopt Postgres."},
		},
	}
	rawJSON, _ := json.Marshal(raw)
	state := PlanningState{
		Prompt:      "## GOALS.md\n\nbuild a thing",
		RawProposal: string(rawJSON),
		Concerns: []Concern{
			{
				AgentID:  "architect_critic",
				Severity: "medium",
				Status:   ConcernStatusOpen,
				Text:     "dec-postgres rationale doesn't address latency",
			},
		},
	}
	snap := StateSnapshot[PlanningState]{State: state}
	msgs := projectScout(snap)
	combined := combineMessages(msgs)

	assert.Contains(t, combined, "## In-flight spec manifest")
	assert.Contains(t, combined, "dec-postgres")
	assert.Contains(t, combined, "## Outstanding critic findings")
	assert.Contains(t, combined, "[c-0/open/architect_critic/medium]",
		"concern must be rendered with its c-<index>/status header so the scout can grade by id")
	// No raw JSON blob.
	assert.NotContains(t, combined, `"decisions":[{"id":"dec-postgres"`,
		"scout projection must not include the RawProposal JSON dump")
}

// TestProjectOpenAxisIncludesManifestAndAxis confirms the
// decision-elaborator sees the manifest plus the specific axis in
// full.
func TestProjectOpenAxisIncludesManifestAndAxis(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-prior", Title: "Prior decision", Summary: "Set in iter 0."},
		},
	}
	rawJSON, _ := json.Marshal(raw)

	axis := OpenAxis{
		ID:             "rollout-cadence",
		Description:    "How often we ship to production",
		SourceEvidence: []string{"goals.md §3 mentions weekly releases"},
		SurfacedBy:     []string{"feat-dashboard"},
	}
	axisJSON, _ := json.Marshal(axis)

	state := PlanningState{
		Prompt:      "## GOALS.md\n\nbuild a thing",
		RawProposal: string(rawJSON),
		AxesOpen:    []OpenAxis{axis},
	}
	snap := StateSnapshot[PlanningState]{State: state, FanoutItem: string(axisJSON)}
	msgs := projectOpenAxis(snap)
	combined := combineMessages(msgs)

	assert.Contains(t, combined, "## In-flight spec manifest")
	assert.Contains(t, combined, "dec-prior")
	assert.Contains(t, combined, "## Open axis to decide")
	assert.Contains(t, combined, "rollout-cadence")
	assert.Contains(t, combined, "How often we ship to production")
}

// TestProjectAffectedNodeIncludesManifestAndNode confirms the
// narrative elaborator sees the manifest plus the specific node it's
// elaborating in full.
func TestProjectAffectedNodeIncludesManifestAndNode(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-stream", Title: "Streaming engine"},
		},
	}
	rawJSON, _ := json.Marshal(raw)

	item := affectedNodeItem{
		AgentID:   "spec_feature_elaborator",
		ID:        "feat-dashboard",
		Kind:      "feature",
		NewNode:   &NewSpecNode{ID: "feat-dashboard", Title: "Dashboard", Summary: "Realtime telemetry view"},
		Decisions: []string{"dec-stream"},
	}
	itemJSON, _ := json.Marshal(item)

	state := PlanningState{
		Prompt:      "## GOALS.md\n\nbuild a thing",
		RawProposal: string(rawJSON),
	}
	snap := StateSnapshot[PlanningState]{State: state, FanoutItem: string(itemJSON)}
	msgs := projectAffectedNode(snap)
	combined := combineMessages(msgs)

	assert.Contains(t, combined, "## In-flight spec manifest")
	assert.Contains(t, combined, "dec-stream")
	assert.Contains(t, combined, "feature to elaborate")
	assert.Contains(t, combined, "feat-dashboard")
	// Must NOT dump the entire RawProposal JSON.
	assert.NotContains(t, combined, `"decisions":[{"id":"dec-stream"`)
}

// TestProjectionsStayBelowSizeCap is the regression guard against
// re-introducing blob projection. On a 30-decision graph, each
// scout/critic/decision-elaborator/narrative projection must stay
// well under 16K chars. The pre-DJ-125 projections blew past this
// trivially on the second winplan run.
func TestProjectionsStayBelowSizeCap(t *testing.T) {
	raw := RawSpecProposal{}
	for i := 0; i < 30; i++ {
		raw.Decisions = append(raw.Decisions, RawDecisionProposal{
			ID:        slugFromIndex("dec-", i),
			Title:     "Decision " + slugFromIndex("", i),
			Summary:   "Adopt option " + slugFromIndex("opt-", i) + ".",
			Axes:      []string{slugFromIndex("axis-", i)},
			Rationale: strings.Repeat("rationale prose ", 80),
		})
	}
	for i := 0; i < 10; i++ {
		raw.Features = append(raw.Features, RawFeatureProposal{
			ID:          slugFromIndex("feat-", i),
			Title:       "Feature " + slugFromIndex("", i),
			Summary:     "Does the X thing.",
			Description: strings.Repeat("description prose ", 100),
			Decisions:   []string{slugFromIndex("dec-", i)},
		})
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)

	state := PlanningState{
		Prompt:      "## GOALS.md\n\nbuild a thing",
		RawProposal: string(rawJSON),
	}
	snap := StateSnapshot[PlanningState]{State: state}

	// Critic projection.
	criticBytes := projectionByteCount(projectChallenge(snap))
	assert.Less(t, criticBytes, 16000,
		"projectChallenge must stay under 16K chars; got %d", criticBytes)

	// Scout projection.
	scoutBytes := projectionByteCount(projectScout(snap))
	assert.Less(t, scoutBytes, 16000,
		"projectScout must stay under 16K chars; got %d", scoutBytes)
}

func combineMessages(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func projectionByteCount(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
	}
	return n
}
