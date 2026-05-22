package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSurveyDispatchedBeforeElaboratorOnInitialPath drives one
// iteration of the spec-generation workflow with a single open axis
// and asserts that the per-axis dispatch order is survey → elaborator.
// The DJ-132 contract is that the survey's CandidateList lands on
// state.AxisSurveys before the decisions step projects its inputs;
// the call ordering is the externally observable proxy.
func TestSurveyDispatchedBeforeElaboratorOnInitialPath(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)

	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "test domain",
		AxesOpen: []OpenAxis{{
			ID:             "data-store",
			Description:    "What backs the OLTP workload?",
			SourceEvidence: []string{"goals reference store"},
			SurfacedBy:     []string{"feat-realtime"},
		}},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-realtime", Title: "Realtime", Summary: "Live tiles.",
			Decisions: []string{},
		}},
		CritiqueDimensions: []CritiqueDimension{{
			ID: "architecture-coherence", Lens: "architecture",
			FocusQuestion:  "Does the store fit?",
			SourceEvidence: []string{"goals"},
			Disciplines:    []string{"freeform"}, SeverityFloor: "medium",
		}},
		Converged: false,
	})
	scoutConverged := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "test domain",
		AxesOpen:   []OpenAxis{},
		NewNodes:   []NewSpecNode{},
		CritiqueDimensions: []CritiqueDimension{{
			ID: "architecture-coherence", Lens: "architecture",
			FocusQuestion: "q", SourceEvidence: []string{"e"},
			Disciplines: []string{"freeform"}, SeverityFloor: "medium",
		}},
		Converged: true,
	})

	survey := `{"candidates":[
		{"name":"Postgres with PostGIS","first_glance_fit":"Mature geospatial Postgres extension."},
		{"name":"MySQL spatial","first_glance_fit":"Familiar SQL with the spatial type."},
		{"name":"Aurora Serverless v2","first_glance_fit":"Managed elastic Postgres."}
	]}`
	decision := decisionProposalJSON(t, RawDecisionProposal{
		ID:        "dec-postgres-oltp",
		Title:     "Postgres OLTP",
		Rationale: "JSONB; transactional; team familiarity.",
		Alternatives: []spec.Alternative{{
			Name: "MySQL spatial", Rationale: "Familiar default", RejectedBecause: "JSONB story weaker",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://example.com", Excerpt: "json"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "json queries"}},
		Axes:       []string{"data-store"},
		SurfacedBy: []string{"feat-realtime"},
	})
	featureNarrative := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-realtime", Title: "Realtime",
		Description: "Live tiles update.",
		Decisions:   []string{"dec-postgres-oltp"},
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		MockResponse{AgentID: "spec_candidate_survey", Response: &AgentOutput{Content: survey, Model: "m"}},
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decision, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featureNarrative, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutConverged, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(nil, 5)
	_, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build it.",
	}, wf)
	require.NoError(t, err)

	calls := mock.Calls()
	var surveyIdx, elabIdx int = -1, -1
	for i, c := range calls {
		switch c.Def.ID {
		case "spec_candidate_survey":
			if surveyIdx == -1 {
				surveyIdx = i
			}
		case "spec_decision_elaborator":
			if elabIdx == -1 {
				elabIdx = i
			}
		}
	}
	require.NotEqual(t, -1, surveyIdx, "expected at least one spec_candidate_survey call")
	require.NotEqual(t, -1, elabIdx, "expected at least one spec_decision_elaborator call")
	assert.Less(t, surveyIdx, elabIdx,
		"DJ-132: the candidate-survey call must fire BEFORE the decision-elaborator on the initial-dispatch path")
}

// TestSurveyOutputThreadsIntoElaboratorInput verifies the survey's
// CandidateList lands on state.AxisSurveys and that projectOpenAxis
// renders the per-axis surveyed candidates in the elaborator's
// projected input. The projection is the seam where the elaborator
// sees the survey's output; if this section is missing, the
// elaborator's prompt has nothing extra to engage with and DJ-132's
// effect doesn't materialize.
func TestSurveyOutputThreadsIntoElaboratorInput(t *testing.T) {
	state := &PlanningState{
		Prompt: "## GOALS.md\n\nBuild it.\n",
		AxisSurveys: map[string]CandidateList{
			"data-store": {Candidates: []SurveyedCandidate{
				{Name: "Postgres with PostGIS", FirstGlanceFit: "Mature geospatial Postgres extension."},
				{Name: "MySQL spatial", FirstGlanceFit: "Familiar SQL with the spatial type."},
				{Name: "Aurora Serverless v2", FirstGlanceFit: "Managed elastic Postgres."},
			}},
		},
	}

	axis := OpenAxis{
		ID:             "data-store",
		Description:    "What backs the OLTP workload?",
		SourceEvidence: []string{"GOALS.md mentions store"},
		SurfacedBy:     []string{"feat-realtime"},
	}
	axisJSON, err := json.Marshal(axis)
	require.NoError(t, err)

	snap := StateSnapshot[PlanningState]{
		State:      *state,
		FanoutItem: string(axisJSON),
	}

	msgs := projectOpenAxis(snap)
	require.NotEmpty(t, msgs)
	full := strings.Join([]string{msgs[0].Content, msgs[len(msgs)-1].Content}, "\n")

	assert.Contains(t, full, "Candidate list",
		"projection must render a Candidate list section when AxisSurveys[axis-id] is populated")
	for _, c := range state.AxisSurveys["data-store"].Candidates {
		assert.Contains(t, full, c.Name,
			"projection must surface each surveyed candidate's name (%q)", c.Name)
		assert.Contains(t, full, c.FirstGlanceFit,
			"projection must surface each surveyed candidate's first-glance fit")
	}
}

// TestRevisePathSkipsSurvey verifies that the revise dispatch — which
// goes through projectReviseDecision rather than projectOpenAxis —
// does not render the candidate-list section. Revises engage with
// critic findings + the prior decision's alternatives; introducing
// the survey on revises would duplicate work and conflict with the
// DJ-132 design decision #1 (survey runs on initial dispatch only).
func TestRevisePathSkipsSurvey(t *testing.T) {
	priorDecision := RawDecisionProposal{
		ID:        "dec-postgres-oltp",
		Title:     "Postgres OLTP",
		Rationale: "Prior rationale.",
		Alternatives: []spec.Alternative{{
			Name: "MySQL", Rationale: "fam.", RejectedBecause: "weak json",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"data-store"},
		SurfacedBy: []string{"feat-realtime"},
	}
	item := reviseableConcernItem{
		AgentID:       "spec_decision_elaborator",
		ID:            "rev:dec-postgres-oltp",
		PriorDecision: priorDecision,
		Concerns: []Concern{{
			AgentID: "cost_critic", Severity: "high", Kind: "cost",
			Text: "Aurora is over budget.",
		}},
	}
	itemJSON, err := json.Marshal(item)
	require.NoError(t, err)

	// AxisSurveys is populated here to confirm the revise projection
	// still does NOT render a candidate-list section despite the data
	// being available. The revise dispatch uses projectReviseDecision,
	// not projectOpenAxis; the survey is silently absent by design.
	state := &PlanningState{
		Prompt: "## GOALS.md\n\nBuild it.\n",
		AxisSurveys: map[string]CandidateList{
			"data-store": {Candidates: []SurveyedCandidate{
				{Name: "Postgres", FirstGlanceFit: "fit"},
				{Name: "MySQL", FirstGlanceFit: "fit"},
				{Name: "Aurora", FirstGlanceFit: "fit"},
			}},
		},
	}
	snap := StateSnapshot[PlanningState]{
		State:      *state,
		FanoutItem: string(itemJSON),
	}

	msgs := projectReviseDecision(snap)
	require.NotEmpty(t, msgs)
	full := strings.Join([]string{msgs[0].Content, msgs[len(msgs)-1].Content}, "\n")

	assert.NotContains(t, full, "Candidate list",
		"projectReviseDecision must NOT render the candidate-list section — revises engage with critic findings, not the survey")
	assert.Contains(t, full, "Revise mode",
		"projectReviseDecision must still render the Revise mode section (sanity)")
}

// TestSurveyEmptyFallsThroughGracefully verifies that when no survey
// output is present for an axis (survey errored / returned empty /
// the workflow didn't dispatch it), projectOpenAxis still produces a
// valid prompt without the candidate-list block. This is the
// degradation path under reversal criterion (a) — if the survey
// silently misfires on some axes, the elaborator still runs against
// its own enumeration as it did pre-DJ-132.
func TestSurveyEmptyFallsThroughGracefully(t *testing.T) {
	axis := OpenAxis{
		ID:             "data-store",
		Description:    "What backs the OLTP workload?",
		SourceEvidence: []string{"goals reference store"},
		SurfacedBy:     []string{"feat-realtime"},
	}
	axisJSON, err := json.Marshal(axis)
	require.NoError(t, err)

	for name, surveys := range map[string]map[string]CandidateList{
		"nil_map":           nil,
		"empty_map":         {},
		"unrelated_axis":    {"other-axis": {Candidates: []SurveyedCandidate{{Name: "x", FirstGlanceFit: "y"}}}},
		"empty_candidates":  {"data-store": {Candidates: []SurveyedCandidate{}}},
	} {
		t.Run(name, func(t *testing.T) {
			state := &PlanningState{
				Prompt:      "## GOALS.md\n\nBuild it.\n",
				AxisSurveys: surveys,
			}
			snap := StateSnapshot[PlanningState]{
				State:      *state,
				FanoutItem: string(axisJSON),
			}

			msgs := projectOpenAxis(snap)
			require.NotEmpty(t, msgs)
			full := strings.Join([]string{msgs[0].Content, msgs[len(msgs)-1].Content}, "\n")

			assert.NotContains(t, full, "Candidate list",
				"projection must omit the Candidate list section when no survey results are available for the axis")
			assert.Contains(t, full, "Open axis to decide",
				"projection must still render the open-axis section (the elaborator's primary input)")
			assert.Contains(t, full, "data-store",
				"projection must still surface the axis ID")
		})
	}
}

// TestMergeCandidateSurveysCorrelatesByAxisID verifies the merge
// handler keys the resulting AxisSurveys map by the OpenAxis.ID
// embedded in RoundResult.FanoutItem. The correlation matters because
// the projection later looks up surveys by axis ID; a merge that
// landed the CandidateList under the wrong key would silently strand
// the survey output where the projection can't find it.
func TestMergeCandidateSurveysCorrelatesByAxisID(t *testing.T) {
	axisA := OpenAxis{ID: "axis-a", Description: "axis A", SourceEvidence: []string{"e"}, SurfacedBy: []string{"feat-1"}}
	axisB := OpenAxis{ID: "axis-b", Description: "axis B", SourceEvidence: []string{"e"}, SurfacedBy: []string{"feat-2"}}
	axisAJSON, _ := json.Marshal(axisA)
	axisBJSON, _ := json.Marshal(axisB)

	listA := `{"candidates":[{"name":"A1","first_glance_fit":"fit A1."},{"name":"A2","first_glance_fit":"fit A2."},{"name":"A3","first_glance_fit":"fit A3."}]}`
	listB := `{"candidates":[{"name":"B1","first_glance_fit":"fit B1."},{"name":"B2","first_glance_fit":"fit B2."},{"name":"B3","first_glance_fit":"fit B3."}]}`

	s := &PlanningState{}
	results := []RoundResult{
		{AgentID: "spec_candidate_survey", FanoutItem: string(axisAJSON), Output: listA},
		{AgentID: "spec_candidate_survey", FanoutItem: string(axisBJSON), Output: listB},
	}
	mergeCandidateSurveys(s, results)

	require.Len(t, s.AxisSurveys, 2)
	require.Contains(t, s.AxisSurveys, "axis-a")
	require.Contains(t, s.AxisSurveys, "axis-b")
	assert.Equal(t, "A1", s.AxisSurveys["axis-a"].Candidates[0].Name)
	assert.Equal(t, "B1", s.AxisSurveys["axis-b"].Candidates[0].Name)
}

// TestMergeCandidateSurveysResetsBetweenIterations verifies that the
// merge handler clears AxisSurveys at the start of every call so
// stale surveys from a prior iteration's open-axis set don't leak
// through. The DJ-132 contract is that AxisSurveys reflects only the
// CURRENT iteration's surveyed axes.
func TestMergeCandidateSurveysResetsBetweenIterations(t *testing.T) {
	axisA := OpenAxis{ID: "axis-a", Description: "axis A", SourceEvidence: []string{"e"}, SurfacedBy: []string{"feat-1"}}
	axisAJSON, _ := json.Marshal(axisA)
	listA := `{"candidates":[{"name":"A1","first_glance_fit":"fit A1."},{"name":"A2","first_glance_fit":"fit A2."},{"name":"A3","first_glance_fit":"fit A3."}]}`

	s := &PlanningState{
		// Stale state from a prior iteration.
		AxisSurveys: map[string]CandidateList{
			"axis-old": {Candidates: []SurveyedCandidate{
				{Name: "X1", FirstGlanceFit: "f"}, {Name: "X2", FirstGlanceFit: "f"}, {Name: "X3", FirstGlanceFit: "f"},
			}},
		},
	}
	mergeCandidateSurveys(s, []RoundResult{
		{AgentID: "spec_candidate_survey", FanoutItem: string(axisAJSON), Output: listA},
	})

	require.NotContains(t, s.AxisSurveys, "axis-old",
		"prior-iteration surveys must be cleared before the new iteration's surveys land")
	require.Contains(t, s.AxisSurveys, "axis-a",
		"the new iteration's survey must populate under its axis ID")
}

// TestMergeCandidateSurveysTolerantOfPartialFailures verifies the
// merge skips results with non-nil Err, empty Output, or malformed
// JSON without aborting. Partial failure is a graceful degradation
// to the elaborator's own enumeration on the un-surveyed axes; the
// reversal criterion (a) is the signal that survey coverage is too
// thin, but a per-iteration misfire shouldn't fail the whole run.
func TestMergeCandidateSurveysTolerantOfPartialFailures(t *testing.T) {
	axisA := OpenAxis{ID: "axis-a", Description: "axis A", SourceEvidence: []string{"e"}, SurfacedBy: []string{"feat-1"}}
	axisB := OpenAxis{ID: "axis-b", Description: "axis B", SourceEvidence: []string{"e"}, SurfacedBy: []string{"feat-2"}}
	axisC := OpenAxis{ID: "axis-c", Description: "axis C", SourceEvidence: []string{"e"}, SurfacedBy: []string{"feat-3"}}
	axisAJSON, _ := json.Marshal(axisA)
	axisBJSON, _ := json.Marshal(axisB)
	axisCJSON, _ := json.Marshal(axisC)
	listA := `{"candidates":[{"name":"A1","first_glance_fit":"fit A1."},{"name":"A2","first_glance_fit":"fit A2."},{"name":"A3","first_glance_fit":"fit A3."}]}`

	s := &PlanningState{}
	mergeCandidateSurveys(s, []RoundResult{
		// Healthy result for axis-a.
		{AgentID: "spec_candidate_survey", FanoutItem: string(axisAJSON), Output: listA},
		// Error result for axis-b — merge skips.
		{AgentID: "spec_candidate_survey", FanoutItem: string(axisBJSON), Err: assert.AnError},
		// Malformed output for axis-c — merge skips.
		{AgentID: "spec_candidate_survey", FanoutItem: string(axisCJSON), Output: "{not json"},
	})

	require.Len(t, s.AxisSurveys, 1, "only the healthy result lands")
	require.Contains(t, s.AxisSurveys, "axis-a")
}
