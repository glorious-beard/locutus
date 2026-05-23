package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/spec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scoutBriefJSON marshals a ScoutBrief for use as a mock LLM response.
// Centralised so the new-workflow tests below all build their scout
// scripts with the same shape.
func scoutBriefJSON(t *testing.T, b ScoutBrief) string {
	t.Helper()
	out, err := json.Marshal(b)
	require.NoError(t, err)
	return string(out)
}

// decisionProposalJSON marshals a RawDecisionProposal — what the
// per-axis decision-elaborator returns under DJ-124.
func decisionProposalJSON(t *testing.T, d RawDecisionProposal) string {
	t.Helper()
	out, err := json.Marshal(d)
	require.NoError(t, err)
	return string(out)
}

// featureProposalJSON marshals a RawFeatureProposal — what the
// narrative-elaborator emits for feature nodes under DJ-124.
func featureProposalJSON(t *testing.T, f RawFeatureProposal) string {
	t.Helper()
	out, err := json.Marshal(f)
	require.NoError(t, err)
	return string(out)
}

// strategyProposalJSON marshals a RawStrategyProposal.
func strategyProposalJSON(t *testing.T, s RawStrategyProposal) string {
	t.Helper()
	out, err := json.Marshal(s)
	require.NoError(t, err)
	return string(out)
}

// setupSpecGenFixtureDJ124 mirrors setupSpecGenFixture but additionally
// registers the spec_decision_elaborator agent the DJ-124 workflow
// dispatches via the decisions fanout. The minimal AgentDef carries
// the model tier + output schema so LoadAgentDefs + BuildAgentInput
// wire the request correctly; the mock LLM stands in for the actual
// model behaviour.
func setupSpecGenFixtureDJ124(t *testing.T) specio.FS {
	t.Helper()
	fs := setupSpecGenFixture(t)
	require.NoError(t, fs.WriteFile(
		".borg/agents/spec_decision_elaborator.md",
		[]byte(minAgentMD("spec_decision_elaborator", "planning", "strong", "RawDecisionProposal")),
		0o644))
	require.NoError(t, fs.WriteFile(
		".borg/agents/spec_feature_elaborator.md",
		[]byte(minAgentMD("spec_feature_elaborator", "planning", "balanced", "RawFeatureProposal")),
		0o644))
	require.NoError(t, fs.WriteFile(
		".borg/agents/spec_strategy_elaborator.md",
		[]byte(minAgentMD("spec_strategy_elaborator", "planning", "balanced", "RawStrategyProposal")),
		0o644))
	// DJ-132 candidate-survey pre-step. Fast tier in production; the
	// minAgentMD shape doesn't care about tier — the mock LLM
	// substitutes the call response, the AgentDef just needs to
	// declare the schema so BuildAgentInput wires the strict-mode
	// output correctly.
	require.NoError(t, fs.WriteFile(
		".borg/agents/spec_candidate_survey.md",
		[]byte(minAgentMD("spec_candidate_survey", "enumeration", "fast", "CandidateList")),
		0o644))
	return fs
}

// emptyCandidateListResp is the smallest CandidateList shape that
// passes the schema's minItems=3 floor. Used as the default survey
// response for DJ-124 tests that don't specifically assert against
// the survey's threading — they only need a non-empty survey so the
// merge fires and the decisions step's projection compiles cleanly.
const emptyCandidateListResp = `{"candidates":[{"name":"option-a","first_glance_fit":"first-glance fit for option-a."},{"name":"option-b","first_glance_fit":"first-glance fit for option-b."},{"name":"option-c","first_glance_fit":"first-glance fit for option-c."}]}`

// TestSpecGenerationWorkflowDispatchOrder drives the new workflow with
// a single open axis + a single new node on iter 0, then a converged
// scout on iter 1. Asserts the agent dispatch order matches the
// DJ-129 round shape: scout(iter0) → decisions(iter1, fanout=1) →
// narrative(iter1, fanout=1) → reconcile(iter1) → critique(iter1,
// fanout=1) → scout(iter1).
func TestSpecGenerationWorkflowDispatchOrder(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)

	iter0Scout := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "test domain",
		AxesOpen: []OpenAxis{{
			ID:             "data-store",
			Description:    "What store backs the OLTP workload?",
			SourceEvidence: []string{"GOALS.md mentions store"},
			SurfacedBy:     []string{"feat-realtime"},
		}},
		NewNodes: []NewSpecNode{{
			Kind:      "feature",
			ID:        "feat-realtime",
			Title:     "Real-time dashboard",
			Summary:   "Live tiles update over WebSocket.",
			Decisions: []string{},
		}},
		CritiqueDimensions: []CritiqueDimension{{
			ID:             "architecture-coherence",
			Lens:           "architecture",
			FocusQuestion:  "Does the proposed store fit the realtime dashboard's read pattern?",
			SourceEvidence: []string{"GOALS.md mentions realtime"},
			Disciplines:    []string{"freeform"},
			SeverityFloor:  "medium",
		}},
		Converged: false,
	})
	iter1Scout := scoutBriefJSON(t, ScoutBrief{
		DomainRead:         "test domain",
		AxesOpen:           []OpenAxis{},
		NewNodes:           []NewSpecNode{},
		CritiqueDimensions: []CritiqueDimension{{ID: "architecture-coherence", Lens: "architecture", FocusQuestion: "q", SourceEvidence: []string{"e"}, Disciplines: []string{"freeform"}, SeverityFloor: "medium"}},
		Converged:          true,
	})

	decisionForDataStore := decisionProposalJSON(t, RawDecisionProposal{
		ID:        "dec-postgres-oltp",
		Title:     "Postgres for OLTP",
		Rationale: "Familiar; JSONB; transactional.",
		Alternatives: []spec.Alternative{{
			Name: "MySQL", Rationale: "Familiar default", RejectedBecause: "JSONB story weaker",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://example.com", Excerpt: "JSON support requires generated columns"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "JSON queries"}},
		Axes:       []string{"data-store"},
		SurfacedBy: []string{"feat-realtime"},
	})
	featureNarrative := featureProposalJSON(t, RawFeatureProposal{
		ID:          "feat-realtime",
		Title:       "Real-time dashboard",
		Description: "Live tiles update over WebSocket; client renders incrementally.",
		Decisions:   []string{"dec-postgres-oltp"},
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: iter0Scout, Model: "m"}},
		MockResponse{AgentID: "spec_candidate_survey", Response: &AgentOutput{Content: emptyCandidateListResp, Model: "m"}},
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decisionForDataStore, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featureNarrative, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: iter1Scout, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(nil, 5)
	_, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build a realtime dashboard.",
	}, wf)
	require.NoError(t, err)

	calls := mock.Calls()
	require.GreaterOrEqual(t, len(calls), 7, "expected at least 7 dispatches with the DJ-132 candidate-survey pre-step in the loop")
	agentOrder := make([]string, 0, len(calls))
	for _, c := range calls {
		agentOrder = append(agentOrder, c.Def.ID)
	}

	assert.Equal(t, "spec_scout", agentOrder[0], "iter-0 scout fires first")
	assert.Equal(t, "spec_candidate_survey", agentOrder[1], "DJ-132 candidate-survey pre-step fires after scout, before decisions")
	assert.Equal(t, "spec_decision_elaborator", agentOrder[2], "decisions step fires after candidate-survey")
	assert.Equal(t, "spec_feature_elaborator", agentOrder[3], "narrative step fires after decisions")
	assert.Equal(t, "spec_reconciler", agentOrder[4], "reconcile fires after narrative")
	assert.Equal(t, "spec_critic_elaborator", agentOrder[5], "critique dispatches the parametric critic")
	assert.Equal(t, "spec_scout", agentOrder[6], "next-iter scout fires after critique")
}

// TestConditionalNarrativeDispatchOnlyTouchesAffected drives a scout
// that surfaces TWO new nodes on iter 0 but only ONE axis covering
// only one of them. After iter 1's decisions, the narrative fanout
// must dispatch only the affected node — not both — even though both
// new nodes appear in NewNodesFromScout.
//
// This locks in the conditional-dispatch contract: unaffected nodes
// keep their (here, absent) prior body across the iteration.
func TestConditionalNarrativeDispatchOnlyTouchesAffected(t *testing.T) {
	// Set up state directly and exercise computeAffectedNodes +
	// fanoutAffectedNodes. The end-to-end mock test (above) already
	// proves the workflow wires through; this test focuses on the
	// computation that produces the dispatched set.
	state := &PlanningState{
		// Two new nodes from the scout. Only feat-a was decided this
		// iteration; feat-b has no covering decision yet (axis stays
		// open across iterations).
		NewNodesFromScout: []NewSpecNode{
			{Kind: "feature", ID: "feat-a", Title: "A", Decisions: []string{"dec-x"}},
			{Kind: "feature", ID: "feat-b", Title: "B", Decisions: []string{}},
		},
		RawProposal: `{
			"features": [],
			"strategies": [],
			"decisions": [{"id":"dec-x","title":"X","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why","citations":[{"kind":"web","reference":"https://x","excerpt":"e"}]}],"citations":[{"kind":"goals","reference":"GOALS.md","excerpt":"e"}],"axes":["data-store"],"surfaced_by":["feat-a"]}]
		}`,
	}

	// Both new nodes show up in the affected set because every
	// NewSpecNode is by definition affected this iteration (no prior
	// body to inherit).
	affected := computeAffectedNodes(state, nil)
	assert.ElementsMatch(t, []string{"feat-a", "feat-b"}, affected,
		"new nodes from the scout are always affected on the iteration they're introduced")

	// Build the fanout items and inspect them.
	items, err := fanoutAffectedNodes(state)
	require.NoError(t, err)
	require.Len(t, items, 2, "both new nodes dispatch on the iteration they're introduced")

	// Verify the decision-ID list is threaded onto each item: feat-a
	// gets dec-x; feat-b's list is empty.
	for _, raw := range items {
		var item affectedNodeItem
		require.NoError(t, json.Unmarshal([]byte(raw), &item))
		switch item.ID {
		case "feat-a":
			assert.Equal(t, []string{"dec-x"}, item.Decisions)
			require.NotNil(t, item.NewNode)
			assert.Equal(t, "feat-a", item.NewNode.ID)
		case "feat-b":
			assert.Empty(t, item.Decisions)
		}
	}

	// Now flip to the steady-state scenario: both nodes are now
	// existing features in RawProposal; on the next iteration only
	// feat-a's decision changed. Only feat-a should be affected.
	state.NewNodesFromScout = nil // scout doesn't re-emit them as new
	state.RawProposal = `{
		"features": [
			{"id":"feat-a","title":"A","description":"prior","decisions":["dec-x"]},
			{"id":"feat-b","title":"B","description":"prior","decisions":["dec-z"]}
		],
		"strategies": [],
		"decisions": [
			{"id":"dec-x","title":"X","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why"}]},
			{"id":"dec-z","title":"Z","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why"}]}
		]
	}`
	affectedSteady := computeAffectedNodes(state, []string{"dec-x"})
	assert.Equal(t, []string{"feat-a"}, affectedSteady,
		"only feat-a should be in the affected set when dec-x changed and feat-b doesn't reference it")
}

// TestConvergenceWhenScoutReturnsConverged drives the new workflow
// with an iter-0 scout returning Converged:true and asserts the
// workflow exits without dispatching decisions / narrative / critics.
func TestConvergenceWhenScoutReturnsConverged(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)

	converged := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "test domain",
		AxesOpen:   []OpenAxis{},
		NewNodes:   []NewSpecNode{},
		Converged:  true,
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: converged, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(nil, 5)
	_, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build a realtime dashboard.",
	}, wf)
	require.NoError(t, err)

	assert.Equal(t, 1, mock.CallCount(),
		"a converged iter-0 scout terminates the workflow without dispatching downstream steps")
}

// TestCycleDetectionWhenAxisReopens drives a scout that emits axis 'A'
// on iter 0; the decision-elaborator commits A; the iter-1 scout
// re-emits A in axes_open. Expectation: the loop terminates with a
// convergence_stuck DJ-103 history event naming the reopened axis.
func TestCycleDetectionWhenAxisReopens(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)

	scoutIter0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "test",
		AxesOpen: []OpenAxis{{
			ID:             "axis-A",
			Description:    "First axis",
			SourceEvidence: []string{"goals say so"},
			SurfacedBy:     []string{"feat-x"},
		}},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-x", Title: "X", Summary: "feature x",
			Decisions: []string{},
		}},
		Converged: false,
	})
	decisionForA := decisionProposalJSON(t, RawDecisionProposal{
		ID:        "dec-A",
		Title:     "Decision A",
		Rationale: "rationale",
		Alternatives: []spec.Alternative{{
			Name: "alt", Rationale: "r", RejectedBecause: "why",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"axis-A"},
		SurfacedBy: []string{"feat-x"},
	})
	featureForX := featureProposalJSON(t, RawFeatureProposal{
		ID:          "feat-x",
		Title:       "X",
		Description: "feature description",
		Decisions:   []string{"dec-A"},
	})
	// iter-1 scout re-emits axis-A as still open — the cycle signature.
	scoutIter1Cycle := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "test",
		AxesOpen: []OpenAxis{{
			ID:             "axis-A",
			Description:    "First axis (reopened)",
			SourceEvidence: []string{"goals say so"},
			SurfacedBy:     []string{"feat-x"},
		}},
		NewNodes:  []NewSpecNode{},
		Converged: false,
	})

	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutIter0, Model: "m"}},
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decisionForA, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featureForX, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutIter1Cycle, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(historian, 5)
	_, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build it.",
	}, wf)
	require.Error(t, err, "the loop must error out when it reopens a decided axis")
	assert.Contains(t, err.Error(), "stuck")
	assert.Contains(t, err.Error(), "axis-A")

	// History event must be written under .borg/history/.
	entries, readErr := os.ReadDir(filepath.Join(tmp, ".borg", "history"))
	require.NoError(t, readErr)
	var found bool
	for _, e := range entries {
		if strings.Contains(e.Name(), "convergence_stuck") && strings.HasSuffix(e.Name(), ".json") {
			found = true
			body, err := os.ReadFile(filepath.Join(tmp, ".borg", "history", e.Name()))
			require.NoError(t, err)
			var evt history.Event
			require.NoError(t, json.Unmarshal(body, &evt))
			assert.Equal(t, "convergence_stuck", evt.Kind)
			assert.Contains(t, evt.Rationale, "axis-A",
				"history event rationale should name the reopened axis")
		}
	}
	assert.True(t, found, "a convergence_stuck DJ-103 event must be written under .borg/history/")
}

// TestGenerateSpecExercisesNewWorkflow drives generateSpecWithWorkflow
// end-to-end against the new DJ-124 workflow and a MockExecutor
// scripted with the iter-0 scout → decisions → narrative → reconcile
// → critique → iter-1 scout (Converged:true) trajectory. Closes
// [[AUDIT-H8]] from the codebase audit.
func TestGenerateSpecExercisesNewWorkflow(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)

	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "campaign software for political organizing",
		AxesOpen: []OpenAxis{{
			ID:             "data-store",
			Description:    "What backs the OLTP workload?",
			SourceEvidence: []string{"goals reference voter file storage"},
			SurfacedBy:     []string{"feat-voter-file"},
		}},
		NewNodes: []NewSpecNode{{
			Kind:      "feature",
			ID:        "feat-voter-file",
			Title:     "Voter file access",
			Summary:   "Field organizers query the voter file with scoped access.",
			Decisions: []string{},
		}},
		Converged: false,
	})
	dec := decisionProposalJSON(t, RawDecisionProposal{
		ID:        "dec-postgres",
		Title:     "Adopt Postgres",
		Rationale: "JSONB + transactional + team familiarity.",
		Alternatives: []spec.Alternative{{
			Name: "MySQL", Rationale: "Familiar to many teams", RejectedBecause: "JSONB story weaker than postgres",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://dev.mysql.com", Excerpt: "JSON requires generated columns"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "queries over voter file"}},
		Axes:       []string{"data-store"},
		SurfacedBy: []string{"feat-voter-file"},
	})
	feat := featureProposalJSON(t, RawFeatureProposal{
		ID:          "feat-voter-file",
		Title:       "Voter file access",
		Description: "Field organizers query the voter file with scoped access; audited per read.",
		Decisions:   []string{"dec-postgres"},
	})
	scout1 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "campaign software for political organizing",
		AxesOpen:   []OpenAxis{},
		NewNodes:   []NewSpecNode{},
		Converged:  true,
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: dec, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: feat, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout1, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(nil, 5)
	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build voter-file tooling.",
	}, wf)
	require.NoError(t, err)
	require.NotNil(t, proposal, "the new workflow must produce a SpecProposal")

	require.Len(t, proposal.Features, 1, "one feature should land")
	assert.Equal(t, "feat-voter-file", proposal.Features[0].ID)
	assert.Equal(t, []string{"dec-postgres"}, proposal.Features[0].Decisions,
		"feature should reference the decision the elaborator minted")

	require.Len(t, proposal.Decisions, 1, "one decision should land")
	assert.Equal(t, "dec-postgres", proposal.Decisions[0].ID)
}

// TestMergeDecisionsAppendsAndTracksAxes is a unit test on mergeDecisions:
// a single decision-elaborator output appends to RawProposal.Decisions,
// updates DecidedAxesByIter, and propagates the new decision ID onto
// NewNodesFromScout entries whose surfacing chain references the
// decision's surfaced_by set.
func TestMergeDecisionsAppendsAndTracksAxes(t *testing.T) {
	state := &PlanningState{
		AxesOpen: []OpenAxis{{
			ID:             "data-store",
			Description:    "what store?",
			SourceEvidence: []string{"goals"},
			SurfacedBy:     []string{"feat-voter"},
		}},
		NewNodesFromScout: []NewSpecNode{{
			Kind: "feature", ID: "feat-voter", Title: "Voter file", Summary: "voter file",
			Decisions: []string{},
		}},
	}

	results := []RoundResult{{
		AgentID:        "spec_decision_elaborator",
		Output:         decisionProposalJSON(t, RawDecisionProposal{
			ID: "dec-postgres",
			Title: "Postgres",
			Rationale: "JSONB",
			Alternatives: []spec.Alternative{{Name: "MySQL", Rationale: "familiar", RejectedBecause: "weaker JSON"}},
			Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
			Axes: []string{"data-store"},
			SurfacedBy: []string{"feat-voter"},
		}),
		IterationIndex: 1,
	}}

	mergeDecisions(state, results)

	var raw RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &raw))
	require.Len(t, raw.Decisions, 1, "one decision should be appended")
	assert.Equal(t, "dec-postgres", raw.Decisions[0].ID)

	assert.Equal(t, 1, state.DecidedAxesByIter["data-store"],
		"DecidedAxesByIter should record the iter the axis was decided")

	require.Len(t, state.NewNodesFromScout, 1)
	assert.Equal(t, []string{"dec-postgres"}, state.NewNodesFromScout[0].Decisions,
		"NewSpecNode whose surfacing chain matches the decision should pick up the new ID")

	// AxesOpen should be drained of the now-closed axis.
	assert.Empty(t, state.AxesOpen,
		"AxesOpen should drop the axis once a decision closes it")
}

// TestImportFlowUnifiedWithRefineWorkflow locks the DJ-124 Phase 6
// contract: `locutus import`'s post-admission planning pass routes
// through the same SpecGenerationWorkflow as `locutus refine`, with
// the admitted document threaded through SpecGenRequest.Imported.
//
// The mock script mirrors TestGenerateSpecExercisesNewWorkflow but with
// Imported populated. The scout sees the imported content (asserted by
// inspecting the user message it receives), emits a NewSpecNode +
// AxesOpen, the decisions fanout fires, the narrative fanout fires for
// the new feature, critics pass, and the iter-1 scout returns
// Converged:true. The resulting SpecProposal carries the feature with
// resolved decision references.
func TestImportFlowUnifiedWithRefineWorkflow(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)

	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "team-collaboration tooling",
		AxesOpen: []OpenAxis{{
			ID:             "live-update-transport",
			Description:    "How do dashboard tiles receive live updates?",
			SourceEvidence: []string{"imported PRD requests real-time updates"},
			SurfacedBy:     []string{"feat-realtime-dashboard"},
		}},
		NewNodes: []NewSpecNode{{
			Kind:      "feature",
			ID:        "feat-realtime-dashboard",
			Title:     "Real-time dashboard",
			Summary:   "Admins see live updates of project health as work progresses.",
			Decisions: []string{},
		}},
		Converged: false,
	})
	dec := decisionProposalJSON(t, RawDecisionProposal{
		ID:        "dec-websocket-transport",
		Title:     "Adopt WebSocket transport",
		Rationale: "Bidirectional; low-latency; widely supported.",
		Alternatives: []spec.Alternative{{
			Name: "Server-Sent Events", Rationale: "Simpler unidirectional fit", RejectedBecause: "no client-to-server channel",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://html.spec.whatwg.org/sse", Excerpt: "one-way only"}},
		}},
		Citations:  []spec.Citation{{Kind: "imported", Reference: "dashboard.md", Excerpt: "real-time updates"}},
		Axes:       []string{"live-update-transport"},
		SurfacedBy: []string{"feat-realtime-dashboard"},
	})
	feat := featureProposalJSON(t, RawFeatureProposal{
		ID:          "feat-realtime-dashboard",
		Title:       "Real-time dashboard",
		Description: "Admins see live updates of project health; tiles re-render over the WebSocket channel as events arrive.",
		Decisions:   []string{"dec-websocket-transport"},
	})
	scout1 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "team-collaboration tooling",
		AxesOpen:   []OpenAxis{},
		NewNodes:   []NewSpecNode{},
		Converged:  true,
	})

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: dec, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: feat, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout1, Model: "m"}},
	)

	importedDocBody := "# Real-time dashboard\n\nAdmins see live updates of project health as work progresses; tiles re-render as events arrive."
	wf := NewSpecGenerationWorkflow(nil, 5)
	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build collaboration tooling.",
		Imported: []ImportedContent{{
			Path: "dashboard.md",
			Body: importedDocBody,
		}},
	}, wf)
	require.NoError(t, err)
	require.NotNil(t, proposal, "the unified workflow must produce a SpecProposal for import-driven runs")

	// The imported document must have reached the scout's user message —
	// the projection's projectScout emits an `## Imported content` section
	// when state.Imported is non-empty.
	calls := mock.Calls()
	require.NotEmpty(t, calls, "at least one scout call should fire")
	var scoutSawImport bool
	for _, c := range calls {
		if c.Def.ID != "spec_scout" {
			continue
		}
		for _, m := range c.Input.Messages {
			if strings.Contains(m.Content, "## Imported content") && strings.Contains(m.Content, "dashboard.md") && strings.Contains(m.Content, importedDocBody) {
				scoutSawImport = true
				break
			}
		}
		if scoutSawImport {
			break
		}
	}
	assert.True(t, scoutSawImport,
		"the scout's user message must include the imported document body so its gap analysis covers it")

	require.Len(t, proposal.Features, 1, "one feature should land from the imported document")
	assert.Equal(t, "feat-realtime-dashboard", proposal.Features[0].ID)
	assert.Equal(t, []string{"dec-websocket-transport"}, proposal.Features[0].Decisions,
		"feature should reference the decision minted for the imported document's open axis")

	require.Len(t, proposal.Decisions, 1, "one decision should land")
	assert.Equal(t, "dec-websocket-transport", proposal.Decisions[0].ID)
}

// TestMergeNarrativeUpdatesFeatureBody confirms the merge replaces a
// matching feature entry's body and appends new entries that didn't
// exist in the prior RawProposal.
func TestMergeNarrativeUpdatesFeatureBody(t *testing.T) {
	state := &PlanningState{
		RawProposal: `{
			"features": [{"id":"feat-existing","title":"old","description":"old body","decisions":["dec-a"]}],
			"strategies": [],
			"decisions": [{"id":"dec-a","title":"A","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why"}]}]
		}`,
	}

	revised := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-existing", Title: "new", Description: "new body", Decisions: []string{"dec-a"},
	})
	added := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-new", Title: "new feature", Description: "fresh body", Decisions: []string{"dec-a"},
	})
	results := []RoundResult{
		{AgentID: "spec_feature_elaborator", Output: revised},
		{AgentID: "spec_feature_elaborator", Output: added},
	}
	mergeNarrative(state, results)

	var raw RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &raw))
	require.Len(t, raw.Features, 2)
	byID := map[string]RawFeatureProposal{}
	for _, f := range raw.Features {
		byID[f.ID] = f
	}
	require.Contains(t, byID, "feat-existing")
	assert.Equal(t, "new body", byID["feat-existing"].Description,
		"existing feature body should be replaced by the elaborator output")
	require.Contains(t, byID, "feat-new")
	assert.Equal(t, "fresh body", byID["feat-new"].Description,
		"new feature should be appended")
}

// --- DJ-126 Phase 2: revise-decisions fanout / projection tests ---

// TestHasReviseableConcernsRequiresOpenAndRelatedIDs locks in the
// DJ-126 conditional gate for the revise-decisions step. The step
// fires only when at least one concern has Status==open AND names at
// least one decision present in the in-flight or existing graph.
// All other combinations are skipped — the scout is the appropriate
// next handler for them (the next iteration's grading pass).
func TestHasReviseableConcernsRequiresOpenAndRelatedIDs(t *testing.T) {
	// Reusable in-flight proposal with one known decision id.
	const rawWithDecX = `{"features":[],"strategies":[],"decisions":[{"id":"dec-x","title":"X","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why","citations":[{"kind":"web","reference":"https://x","excerpt":"e"}]}],"citations":[{"kind":"goals","reference":"GOALS.md","excerpt":"e"}],"axes":["a"],"surfaced_by":["feat-x"]}]}`

	cases := []struct {
		name  string
		state *PlanningState
		want  bool
	}{
		{name: "nil state", state: nil, want: false},
		{name: "no concerns", state: &PlanningState{RawProposal: rawWithDecX}, want: false},
		{
			name: "open concern, no related ids",
			state: &PlanningState{
				RawProposal: rawWithDecX,
				Concerns:    []Concern{{Status: ConcernStatusOpen, Text: "abstract concern", RelatedDecisionIDs: nil}},
			},
			want: false,
		},
		{
			name: "addressed concern with related id",
			state: &PlanningState{
				RawProposal: rawWithDecX,
				Concerns:    []Concern{{Status: ConcernStatusAddressed, Text: "old concern", RelatedDecisionIDs: []string{"dec-x"}}},
			},
			want: false,
		},
		{
			name: "open concern names unknown decision id",
			state: &PlanningState{
				RawProposal: rawWithDecX,
				Concerns:    []Concern{{Status: ConcernStatusOpen, Text: "unresolvable", RelatedDecisionIDs: []string{"dec-missing"}}},
			},
			want: false,
		},
		{
			name: "open concern names known in-flight decision",
			state: &PlanningState{
				RawProposal: rawWithDecX,
				Concerns:    []Concern{{Status: ConcernStatusOpen, Text: "dec-x is wrong", RelatedDecisionIDs: []string{"dec-x"}}},
			},
			want: true,
		},
		{
			name: "legacy concern with empty Status defaults to open",
			state: &PlanningState{
				RawProposal: rawWithDecX,
				Concerns:    []Concern{{Text: "legacy", RelatedDecisionIDs: []string{"dec-x"}}},
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasReviseableConcerns(tc.state)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestFanoutReviseableConcernsDedupesByDecisionID locks in the
// dedup-by-decision-ID fanout shape (DJ-126 Phase 2 mitigation for
// the AxisRevisionCount-inflation failure mode observed on the
// winplan re-run): one fanout item per UNIQUE reviseable decision,
// with every open concern naming that decision aggregated into the
// item's Concerns slice. A concern naming N decisions still produces
// N items; N concerns naming the same decision produce ONE item.
func TestFanoutReviseableConcernsDedupesByDecisionID(t *testing.T) {
	const rawWithDecXY = `{"features":[],"strategies":[],"decisions":[
		{"id":"dec-x","title":"X","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why","citations":[{"kind":"web","reference":"https://x","excerpt":"e"}]}],"citations":[{"kind":"goals","reference":"GOALS.md","excerpt":"e"}],"axes":["a"],"surfaced_by":["feat-x"]},
		{"id":"dec-y","title":"Y","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why","citations":[{"kind":"web","reference":"https://y","excerpt":"e"}]}],"citations":[{"kind":"goals","reference":"GOALS.md","excerpt":"e"}],"axes":["b"],"surfaced_by":["feat-y"]}
	]}`

	state := &PlanningState{
		RawProposal: rawWithDecXY,
		Concerns: []Concern{
			{Status: ConcernStatusOpen, Text: "dec-x is wrong on cost", AgentID: "cost_critic", Severity: "high", RelatedDecisionIDs: []string{"dec-x"}},
			{Status: ConcernStatusOpen, Text: "dec-x contradicts dec-y", AgentID: "architect_critic", Severity: "medium", RelatedDecisionIDs: []string{"dec-x", "dec-y"}},
			{Status: ConcernStatusOpen, Text: "dec-x also has a factual error", AgentID: "sre_critic", Severity: "medium", RelatedDecisionIDs: []string{"dec-x"}},
			{Status: ConcernStatusOpen, Text: "unresolvable", RelatedDecisionIDs: []string{"dec-missing"}},
			{Status: ConcernStatusAddressed, Text: "stale", RelatedDecisionIDs: []string{"dec-x"}},
		},
	}

	items, err := fanoutReviseableConcerns(state)
	require.NoError(t, err)
	// Unique reviseable decisions: dec-x (3 concerns), dec-y (1 concern).
	// dec-missing is filtered (not in graph); addressed concern is filtered.
	// Expect exactly 2 items, not 4 — the dedup is the whole point.
	require.Len(t, items, 2, "fanout dedupes by decision id; multiple concerns about the same decision become one item")

	byID := map[string]reviseableConcernItem{}
	for _, raw := range items {
		var it reviseableConcernItem
		require.NoError(t, json.Unmarshal([]byte(raw), &it))
		assert.Equal(t, "spec_decision_elaborator", it.AgentID)
		assert.NotEmpty(t, it.PriorDecision.ID, "prior_decision body must be populated for the projection")
		assert.Contains(t, it.ID, "rev:", "fanout item id starts with rev: so fanoutItemID labels it as a revision dispatch")
		byID[it.PriorDecision.ID] = it
	}

	require.Contains(t, byID, "dec-x")
	assert.Len(t, byID["dec-x"].Concerns, 3,
		"dec-x's fanout item aggregates every open concern naming it (cost_critic, architect_critic, sre_critic)")
	require.Contains(t, byID, "dec-y")
	assert.Len(t, byID["dec-y"].Concerns, 1,
		"dec-y's fanout item carries the single architect_critic concern that named it")
}

// TestProjectReviseDecisionIncludesPriorDecisionFullBody locks in
// the projection contract: the user message includes the full JSON
// body of the prior decision, not a summary. The agent needs the
// rationale + alternatives + citations to author a coherent revision.
func TestProjectReviseDecisionIncludesPriorDecisionFullBody(t *testing.T) {
	const rawWithDecX = `{"features":[],"strategies":[],"decisions":[{"id":"dec-x","title":"Adopt Datadog","summary":"Datadog for observability.","rationale":"Comprehensive APM and log management.","confidence":0.8,"alternatives":[{"name":"CloudWatch","rationale":"AWS-native","rejected_because":"Less polish","citations":[{"kind":"web","reference":"https://example.com","excerpt":"datadog excerpt"}]}],"citations":[{"kind":"goals","reference":"GOALS.md","excerpt":"observability mentioned"}],"axes":["observability-stack"],"surfaced_by":["feat-monitoring"]}]}`
	state := &PlanningState{
		Prompt:      "GOALS.md content here",
		RawProposal: rawWithDecX,
		Concerns: []Concern{{
			Status:             ConcernStatusOpen,
			AgentID:            "cost_critic",
			Severity:           "high",
			Text:               "Datadog conflicts with the $150 cost ceiling",
			RelatedDecisionIDs: []string{"dec-x"},
		}},
	}
	items, err := fanoutReviseableConcerns(state)
	require.NoError(t, err)
	require.Len(t, items, 1)

	snap := StateSnapshot[PlanningState]{State: *state, FanoutItem: items[0]}
	msgs := projectReviseDecision(snap)
	require.Len(t, msgs, 2, "projection emits two user messages: cacheable prefix + per-item suffix")

	combined := msgs[0].Content + "\n" + msgs[1].Content
	assert.Contains(t, combined, "Revise mode", "projection must carry the Revise mode header keyed by the prompt")
	assert.Contains(t, combined, "Prior decision", "projection must label the prior-decision block the prompt looks for")
	assert.Contains(t, combined, "Critic finding", "projection must label the critic-finding block the prompt looks for")
	assert.Contains(t, combined, "Adopt Datadog", "prior decision's title travels through the projection")
	assert.Contains(t, combined, "Comprehensive APM and log management", "prior decision's rationale (full body) is in the projection")
	assert.Contains(t, combined, "CloudWatch", "prior decision's alternatives travel through the projection")
	assert.Contains(t, combined, "Datadog conflicts with the $150 cost ceiling", "concern text is in the projection")
	assert.Contains(t, combined, "dec-x", "related decision id is in the projection (for spec_get cross-reference)")
}

// TestReviseDecisionsStepConditionalSkipsWhenNoOpenConcerns confirms
// that the conditional gate on the revise-decisions step skips
// cleanly when every concern is in a non-open status (stale / addressed /
// wontfix). The workflow proceeds to reconcile without dispatching
// any spec_decision_elaborator revise calls.
func TestReviseDecisionsStepConditionalSkipsWhenNoOpenConcerns(t *testing.T) {
	state := &PlanningState{
		RawProposal: `{"features":[],"strategies":[],"decisions":[{"id":"dec-x","title":"X","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why","citations":[{"kind":"web","reference":"https://x","excerpt":"e"}]}],"citations":[{"kind":"goals","reference":"GOALS.md","excerpt":"e"}],"axes":["a"],"surfaced_by":["feat-x"]}]}`,
		Concerns: []Concern{
			{Status: ConcernStatusAddressed, Text: "addressed concern", RelatedDecisionIDs: []string{"dec-x"}},
			{Status: ConcernStatusStale, Text: "stale concern", RelatedDecisionIDs: []string{"dec-x"}},
			{Status: ConcernStatusWontfix, Text: "wontfix concern", RelatedDecisionIDs: []string{"dec-x"}},
		},
	}
	assert.False(t, hasReviseableConcerns(state),
		"hasReviseableConcerns must return false when every concern is in a non-open status")

	// Fanout returns no items so the step yields no dispatches even
	// when the conditional gate is bypassed externally.
	items, err := fanoutReviseableConcerns(state)
	require.NoError(t, err)
	assert.Empty(t, items, "fanoutReviseableConcerns must emit zero items when no concern is open")
}

// --- DJ-126 Phase 3: mergeDecisions replace-by-axis-ID tests ---

// makeDecisionResult is a small helper used by the Phase 3 merge tests
// to construct a RoundResult carrying a marshalled RawDecisionProposal
// — the executor's output shape for the decision-elaborator.
func makeDecisionResult(t *testing.T, d RawDecisionProposal, iter int) RoundResult {
	t.Helper()
	out, err := json.Marshal(d)
	require.NoError(t, err)
	return RoundResult{
		AgentID:        "spec_decision_elaborator",
		Output:         string(out),
		IterationIndex: iter,
	}
}

// TestMergeDecisions_MatchesPriorByID is the canonical DJ-133 happy
// path: an in-flight proposal carries dec-x; a revise dispatch returns
// a new RawDecisionProposal with the same id (the elaborator copies
// the axis ID verbatim per DJ-133's prompt contract). The merge
// replaces dec-x in place — id preserved, body overwritten — without
// any axis-intersection inspection (the retired pre-DJ-133 path).
func TestMergeDecisions_MatchesPriorByID(t *testing.T) {
	prior := RawDecisionProposal{
		ID:         "dec-x",
		Title:      "Original X",
		Rationale:  "old rationale",
		Confidence: 0.5,
		Alternatives: []spec.Alternative{{
			Name: "alt-old", Rationale: "old", RejectedBecause: "old",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://old", Excerpt: "old"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "old"}},
		Axes:       []string{"axis-a", "axis-b"},
		SurfacedBy: []string{"feat-x"},
	}
	rawWithPrior, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)

	state := &PlanningState{
		RawProposal: string(rawWithPrior),
		Concerns: []Concern{{
			Status:             ConcernStatusOpen,
			AgentID:            "cost_critic",
			Severity:           "high",
			Text:               "dec-x is wrong on a factual claim",
			RelatedDecisionIDs: []string{"dec-x"},
		}},
	}

	revised := RawDecisionProposal{
		// DJ-133: the elaborator copies the axis ID verbatim, so the
		// revise dispatch's id matches the prior decision's id exactly.
		// The merge identifies the replace target via string equality.
		ID:         "dec-x",
		Title:      "Revised X",
		Rationale:  "corrected rationale",
		Confidence: 0.9,
		// DJ-128: revised alternatives include the prior alt-old AND
		// the prior chosen "Original X" (demoted) AND a new alt-new.
		// The monotonicity discipline requires every prior alternative
		// + the demoted prior chosen to appear in the revised
		// alternatives.
		Alternatives: []spec.Alternative{{
			Name: "alt-new", Rationale: "new", RejectedBecause: "new",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://new", Excerpt: "new"}},
		}, {
			Name: "alt-old", Rationale: "old", RejectedBecause: "old",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://old", Excerpt: "old"}},
		}, {
			Name: "Original X", Rationale: "old chosen path", RejectedBecause: "factual error",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://old", Excerpt: "old"}},
		}},
		Citations:  []spec.Citation{{Kind: "web", Reference: "https://new", Excerpt: "new"}},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, revised, 2)})

	// Re-parse and assert single decision with the prior id but revised body.
	var got RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &got))
	require.Len(t, got.Decisions, 1, "revise must replace in-place, not append")
	assert.Equal(t, "dec-x", got.Decisions[0].ID, "replacement preserves the prior id")
	assert.Equal(t, "Revised X", got.Decisions[0].Title, "title is overwritten by the revision")
	assert.Equal(t, "corrected rationale", got.Decisions[0].Rationale, "rationale is overwritten")
	assert.Equal(t, 0.9, got.Decisions[0].Confidence, "confidence is overwritten")
	assert.Equal(t, []string{"axis-a"}, got.Decisions[0].Axes, "axes are overwritten (the revision's axes set is canonical post-replace)")

	// Concern about dec-x is marked addressed by the replacement.
	require.Len(t, state.Concerns, 1)
	assert.Equal(t, ConcernStatusAddressed, state.Concerns[0].Status, "concern referencing the revised decision id is marked addressed")
	assert.Contains(t, state.Concerns[0].Justification, "dec-x", "justification names the revised decision id")
}

// TestMergeDecisionsAppendsWhenNoAxisIntersection covers the
// first-author path: a fresh decision on a brand-new axis appends
// rather than replacing anything.
func TestMergeDecisionsAppendsWhenNoAxisIntersection(t *testing.T) {
	prior := RawDecisionProposal{
		ID:        "dec-x",
		Title:     "Decision X",
		Rationale: "r",
		Confidence: 0.8,
		Alternatives: []spec.Alternative{{
			Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	rawWithPrior, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)

	state := &PlanningState{RawProposal: string(rawWithPrior)}

	fresh := RawDecisionProposal{
		ID:        "dec-y",
		Title:     "Decision Y",
		Rationale: "r",
		Confidence: 0.7,
		Alternatives: []spec.Alternative{{
			Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://y", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"axis-b"}, // brand-new axis; no intersection
		SurfacedBy: []string{"feat-y"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, fresh, 1)})

	var got RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &got))
	require.Len(t, got.Decisions, 2, "first-author append: incoming axis has no intersection so both decisions survive")
	ids := []string{got.Decisions[0].ID, got.Decisions[1].ID}
	assert.ElementsMatch(t, []string{"dec-x", "dec-y"}, ids)
}

// TestMergeDecisions_DuplicateIDReplacesInPlace — DJ-133's structural
// replacement for TestMergeDecisionsRecordsAmbiguityWhenMultipleExistingMatch.
// Under the retired axis-intersection match, two existing decisions
// on the same axis with different chosen-option-shaped ids produced
// an `ambiguous` integrity violation. Under DJ-133 axis-as-ID makes
// that scenario impossible by construction: two decisions on the same
// axis necessarily share the same id, which the persisted-graph
// integrity check surfaces as a duplicate-ID violation downstream.
//
// What CAN happen here at the merge layer is the normal revise path —
// an incoming decision whose id matches a prior is replaced in place.
// This test pins that down so a future regression doesn't accidentally
// re-introduce an ambiguity path.
func TestMergeDecisions_DuplicateIDReplacesInPlace(t *testing.T) {
	prior := RawDecisionProposal{
		ID: "dec-axis-a", Title: "Original", Rationale: "r", Confidence: 0.8,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	}
	rawWithPrior, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)
	state := &PlanningState{RawProposal: string(rawWithPrior)}

	incoming := RawDecisionProposal{
		// Same id as prior — the elaborator copies the axis ID
		// verbatim, so a revise dispatch on axis-a arrives with
		// id=dec-axis-a.
		ID: "dec-axis-a", Title: "Revised", Rationale: "new", Confidence: 0.9,
		Alternatives: []spec.Alternative{
			{Name: "alt", Rationale: "r", RejectedBecause: "r",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://r", Excerpt: "e"}}},
			{Name: "Original", Rationale: "old chosen", RejectedBecause: "new analysis",
				Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}}},
		},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, incoming, 2)})

	var got RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &got))
	require.Len(t, got.Decisions, 1, "id-equality match must replace in-place, not append")
	assert.Equal(t, "dec-axis-a", got.Decisions[0].ID)
	assert.Equal(t, "Revised", got.Decisions[0].Title, "body is overwritten by the revision")

	// DJ-133 retired the ambiguous-revision integrity concern — its
	// trigger condition is unreachable under axis-as-ID. The merge
	// must not emit an integrity_critic concern from a normal
	// replace path.
	for _, c := range state.Concerns {
		assert.NotEqual(t, "integrity_critic", c.AgentID,
			"DJ-133: the ambiguous-revision integrity concern path is retired; the merge must not emit it from a normal replace-by-ID path")
	}
}

// TestMergeDecisionsMarksDrivingConcernAddressed locks in the
// downstream signal: after a revise replacement lands, the open
// concern whose RelatedDecisionIDs contained the replaced decision
// id is marked Status=addressed with a justification naming the
// revision iteration. The scout's next-iteration grading pass sees
// the addressed status and treats the concern as resolved.
func TestMergeDecisionsMarksDrivingConcernAddressed(t *testing.T) {
	prior := RawDecisionProposal{
		ID: "dec-datadog", Title: "Adopt Datadog", Rationale: "old",
		Confidence: 0.8,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://d", Excerpt: "e"}}}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"observability-stack"},
		SurfacedBy: []string{"feat-monitoring"},
	}
	rawWithPrior, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)

	state := &PlanningState{
		RawProposal: string(rawWithPrior),
		Concerns: []Concern{
			{
				Status:             ConcernStatusOpen,
				AgentID:            "cost_critic",
				Severity:           "high",
				Text:               "Datadog conflicts with the $150 cost ceiling",
				RelatedDecisionIDs: []string{"dec-datadog"},
			},
			{
				// Unrelated concern; should stay open.
				Status:             ConcernStatusOpen,
				AgentID:            "architect_critic",
				Severity:           "medium",
				Text:               "Unrelated concern about something else",
				RelatedDecisionIDs: []string{"dec-other"},
			},
		},
	}

	revised := RawDecisionProposal{
		// DJ-133: the revise dispatch's id matches the prior decision's id
		// verbatim (the elaborator copies the axis ID through). The
		// chosen-option flip from Datadog to CloudWatch shows up in
		// title / rationale; the id stays stable.
		ID: "dec-datadog", Title: "Adopt CloudWatch", Rationale: "revised",
		Confidence: 0.85,
		// DJ-128: revised alternatives preserve the prior alternative
		// "alt" AND demote the prior chosen "Adopt Datadog" — the
		// monotonicity validator requires every prior alternative to
		// survive plus the prior chosen to appear when the Title flips.
		Alternatives: []spec.Alternative{
			{Name: "Adopt Datadog", Rationale: "old", RejectedBecause: "cost",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://aws", Excerpt: "e"}}},
			{Name: "alt", Rationale: "r", RejectedBecause: "r",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://d", Excerpt: "e"}}},
		},
		Citations:  []spec.Citation{{Kind: "web", Reference: "https://aws", Excerpt: "e"}},
		Axes:       []string{"observability-stack"},
		SurfacedBy: []string{"feat-monitoring"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, revised, 2)})

	// Driving concern flipped to addressed; unrelated concern stays open.
	require.Len(t, state.Concerns, 2)
	var addressed, unrelated *Concern
	for i := range state.Concerns {
		if state.Concerns[i].AgentID == "cost_critic" {
			addressed = &state.Concerns[i]
		}
		if state.Concerns[i].AgentID == "architect_critic" {
			unrelated = &state.Concerns[i]
		}
	}
	require.NotNil(t, addressed)
	require.NotNil(t, unrelated)
	assert.Equal(t, ConcernStatusAddressed, addressed.Status, "the concern flagging the replaced decision is marked addressed")
	assert.Contains(t, addressed.Justification, "dec-datadog", "justification names the revised decision id")
	assert.Contains(t, addressed.Justification, "iter 3", "justification names the iteration index (iter 2 + 1 for 1-based display)")
	assert.Equal(t, ConcernStatusOpen, unrelated.Status, "unrelated concerns stay open after the revision")
}

// --- DJ-126 Phase 4: per-axis revision-count cap tests ---

// TestAxisRevisionCountIncrementsOnReplace locks in the bookkeeping:
// each replace-by-axis-ID match bumps AxisRevisionCount for every axis
// in the revised decision. First-author appends do NOT increment the
// count — only revisions count toward the cap.
func TestAxisRevisionCountIncrementsOnReplace(t *testing.T) {
	prior := RawDecisionProposal{
		ID: "dec-x", Title: "X", Rationale: "r", Confidence: 0.8,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	}
	rawWithPrior, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)
	state := &PlanningState{RawProposal: string(rawWithPrior)}

	// First revision: count becomes 1.
	rev1 := RawDecisionProposal{
		ID: "dec-x", Title: "Rev1", Rationale: "rev1", Confidence: 0.85,
		// DJ-128: alternatives include the prior alt AND the prior
		// chosen "X" to satisfy monotonicity.
		Alternatives: []spec.Alternative{
			{Name: "alt", Rationale: "r", RejectedBecause: "r",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://r1", Excerpt: "e"}}},
			{Name: "X", Rationale: "old chosen", RejectedBecause: "revised",
				Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}}},
		},
		Citations: []spec.Citation{{Kind: "web", Reference: "https://r1", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, rev1, 1)})
	require.Equal(t, 1, state.AxisRevisionCount["axis-a"], "first revision bumps the count to 1")

	// Second revision on the same axis: count becomes 2.
	rev2 := rev1
	rev2.Title = "Rev2"
	rev2.Rationale = "rev2"
	// DJ-128: rev2 demotes Rev1 — needs alt + X + Rev1 as alternatives.
	rev2.Alternatives = []spec.Alternative{
		{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://r1", Excerpt: "e"}}},
		{Name: "X", Rationale: "old chosen", RejectedBecause: "revised",
			Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}}},
		{Name: "Rev1", Rationale: "first revision", RejectedBecause: "revised again",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://r1", Excerpt: "e"}}},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, rev2, 2)})
	require.Equal(t, 2, state.AxisRevisionCount["axis-a"], "second revision bumps the count to 2")

	// Append a first-author decision on a brand-new axis: that axis
	// must NOT show up in AxisRevisionCount (only revisions count).
	fresh := RawDecisionProposal{
		ID: "dec-y", Title: "Y", Rationale: "r", Confidence: 0.7,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://y", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:      []string{"axis-b"}, SurfacedBy: []string{"feat-y"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, fresh, 3)})
	_, hasB := state.AxisRevisionCount["axis-b"]
	assert.False(t, hasB, "first-author appends do not increment AxisRevisionCount; axis-b should be absent")
	assert.Equal(t, 2, state.AxisRevisionCount["axis-a"], "axis-a count is unchanged by an unrelated first-author append")
}

// TestRevisionCapTerminatesWhenExceeded drives a full workflow run
// with an env-cap of 2: the scout never converges; the council keeps
// revising dec-x; on iteration 3 the per-axis cap fires before the
// scout's regular spawn logic runs. Asserts the workflow exits with
// a convergence_revision_capped DJ-103 event.
func TestRevisionCapTerminatesWhenExceeded(t *testing.T) {
	t.Setenv("LOCUTUS_DECISION_REVISION_CAP", "2")

	fs := setupSpecGenFixtureDJ124(t)

	// DJ-129: scout surfaces one cost dimension every iteration. The
	// fanout dispatches one spec_critic_elaborator call per iter.
	costDim := CritiqueDimension{
		ID: "cost-ceiling-coverage", Lens: "cost",
		FocusQuestion:  "Does the proposal engage with the GOALS cost ceiling?",
		SourceEvidence: []string{"GOALS.md cost ceiling clause"},
		Disciplines:    []string{"goals_grounded"},
		SeverityFloor:  "high",
	}
	scoutIter0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "test",
		AxesOpen: []OpenAxis{{
			ID: "axis-a", Description: "first axis",
			SourceEvidence: []string{"e"}, SurfacedBy: []string{"feat-x"},
		}},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-x", Title: "X", Summary: "x", Decisions: []string{},
		}},
		CritiqueDimensions: []CritiqueDimension{costDim},
		Converged:          false,
	})
	decFirstAuthor := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-x", Title: "X v0", Rationale: "v0", Confidence: 0.7,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	})
	featureForX := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-x", Title: "X", Description: "body", Decisions: []string{"dec-x"},
	})

	// Critic on iter-1 raises a concern naming dec-x — driving a revise
	// dispatch on iter-2 onwards. DJ-128 structured critic shape:
	// weakness + evidence + a grounded counterproposal menu.
	criticIssuesFlagX := mustJSON(t, CriticIssues{Issues: []CriticIssue{{
		Weakness: "The dec-x rationale does not engage with the cost ceiling implied by GOALS.md.",
		Evidence: "GOALS.md names a cost ceiling that the rationale never cites or engages with.",
		Counterproposals: []CriticCounterproposal{{
			Option:   "Adopt a cheaper alternative with explicit cost engagement",
			Argument: "A cheaper alternative would fit the cost ceiling and force the rationale to address the budget constraint explicitly.",
			Citations: []spec.Citation{{
				Kind: "goals", Reference: "GOALS.md", Excerpt: "cost ceiling",
			}},
		}},
		RelatedDecisionIDs: []string{"dec-x"},
	}}})

	// Iter-1 scout (after iter-0 critique) — keeps converged=false with
	// no new axes; concern about dec-x stays open. Triggers iter-2.
	scoutKeepOpen := scoutBriefJSON(t, ScoutBrief{
		DomainRead:          "test",
		AxesOpen:            []OpenAxis{}, // no new axes
		NewNodes:            []NewSpecNode{},
		CritiqueDimensions:  []CritiqueDimension{costDim},
		ConcernDispositions: nil,
		Converged:           false,
	})

	// Iter-2 revise output (one revision of dec-x). DJ-128: the
	// monotonicity discipline requires every prior alternative to
	// survive the revision; the demote / fold helpers in mergeDecisions
	// handle prior-chosen demotion + counterproposal folding, but the
	// elaborator must still preserve every alt the prior carried.
	revV1 := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-x", Title: "X v1", Rationale: "v1", Confidence: 0.8,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x1", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "web", Reference: "https://x1", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	})
	// Iter-3 revise output (second revision; count==2 hits the cap=2).
	// Carries the alternatives accumulated by iter-2's merge: the prior
	// `alt`, the demoted prior chosen `X v0`, and the folded counter-
	// proposal `Adopt a cheaper alternative ...`. Without these the
	// monotonicity validator would reject the revision, the cap counter
	// would stay at 1, and the loop would hit budget instead of cap.
	revV2 := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-x", Title: "X v2", Rationale: "v2", Confidence: 0.85,
		Alternatives: []spec.Alternative{
			{Name: "alt", Rationale: "r", RejectedBecause: "r",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://x2", Excerpt: "e"}}},
			{Name: "X v0", Rationale: "first version", RejectedBecause: "revised in iter 2",
				Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}}},
			{Name: "Adopt a cheaper alternative with explicit cost engagement",
				Rationale: "A cheaper alternative would fit the cost ceiling.",
				RejectedBecause: "Elaborator picked X v1 instead.",
				Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "cost ceiling"}}},
		},
		Citations: []spec.Citation{{Kind: "web", Reference: "https://x2", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	})

	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	// Workflow trajectory (cap=2, budget=10 to make sure the cap, not budget, fires):
	//
	//   iter-0 scout (open)
	//   iter-1 decisions (first-author dec-x)
	//          narrative
	//          revise-decisions (skipped — no critic concerns yet)
	//          reconcile
	//          critique x4 (cost_critic raises concern on dec-x)
	//          scout (no convergence; iter-2 spawns)
	//   iter-2 decisions (no open axes — skipped)
	//          narrative (no affected nodes — skipped)
	//          revise-decisions (revV1; bumps count to 1)
	//          reconcile
	//          critique x4 (still raises concern on the revised dec-x)
	//          scout (no convergence; iter-3 spawns)
	//   iter-3 decisions/narrative skipped
	//          revise-decisions (revV2; bumps count to 2 → at cap)
	//          reconcile
	//          critique x4 (concern persists)
	//          scout → cap fires; convergence_revision_capped terminal
	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutIter0, Model: "m"}},
		// iter-1
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decFirstAuthor, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featureForX, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: criticIssuesFlagX, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutKeepOpen, Model: "m"}},
		// iter-2 (revise fires)
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: revV1, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: criticIssuesFlagX, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutKeepOpen, Model: "m"}},
		// iter-3 (revise fires again; count hits 2; cap=2 fires at tail scout)
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: revV2, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: criticIssuesFlagX, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutKeepOpen, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(historian, 10)
	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build it.",
	}, wf)
	// DJ-128 cap-as-commit: the cap firing is no longer a workflow
	// failure — the latest revision is committed with Locked=true and
	// the loop exits cleanly. The convergence_revision_capped event
	// still surfaces for operator visibility.
	require.NoError(t, err, "loop exits cleanly under DJ-128 cap-as-commit semantics")
	require.NotNil(t, proposal)

	// The final proposal must carry dec-x with Locked=true.
	var locked *DecisionProposal
	for i := range proposal.Decisions {
		if proposal.Decisions[i].ID == "dec-x" {
			locked = &proposal.Decisions[i]
		}
	}
	require.NotNil(t, locked, "final proposal must carry dec-x")
	assert.True(t, locked.Locked, "capped decision must be flipped to Locked=true")

	// DJ-103 event must be written under .borg/history/.
	entries, readErr := os.ReadDir(filepath.Join(tmp, ".borg", "history"))
	require.NoError(t, readErr)
	var foundCapEvent, foundLockedEvent bool
	for _, e := range entries {
		if strings.Contains(e.Name(), "convergence_revision_capped") && strings.HasSuffix(e.Name(), ".json") {
			foundCapEvent = true
			body, err := os.ReadFile(filepath.Join(tmp, ".borg", "history", e.Name()))
			require.NoError(t, err)
			var evt history.Event
			require.NoError(t, json.Unmarshal(body, &evt))
			assert.Equal(t, "convergence_revision_capped", evt.Kind)
			assert.Contains(t, evt.Rationale, "axis-a",
				"history event rationale names the capped axis")
		}
		if strings.Contains(e.Name(), "decision_locked") && strings.HasSuffix(e.Name(), ".json") {
			foundLockedEvent = true
			body, err := os.ReadFile(filepath.Join(tmp, ".borg", "history", e.Name()))
			require.NoError(t, err)
			var evt history.Event
			require.NoError(t, json.Unmarshal(body, &evt))
			assert.Equal(t, "decision_locked", evt.Kind)
			assert.Equal(t, "dec-x", evt.TargetID,
				"decision_locked event names the locked decision id")
		}
	}
	assert.True(t, foundCapEvent, "convergence_revision_capped DJ-103 event must be written")
	assert.True(t, foundLockedEvent, "decision_locked DJ-103 event must be written per locked decision (DJ-128)")
}

// TestRevisionCapPerAxisIndependent verifies the counts are per-axis:
// axis-a revised 2 times and axis-b revised 1 time at cap=3 must NOT
// trigger the cap (neither axis has hit 3). Distinct from the aggregate
// total of 3 — the cap is per-axis by design.
func TestRevisionCapPerAxisIndependent(t *testing.T) {
	state := &PlanningState{
		AxisRevisionCount: map[string]int{
			"axis-a": 2,
			"axis-b": 1,
		},
	}
	assert.Empty(t, axesExceedingRevisionCap(state, 3),
		"per-axis cap=3 with axis-a=2 + axis-b=1 must not flag anything; aggregate count is irrelevant")

	state.AxisRevisionCount["axis-b"]++
	assert.Empty(t, axesExceedingRevisionCap(state, 3),
		"axis-b at 2 still under cap=3; no flag")

	state.AxisRevisionCount["axis-a"]++ // now 3
	got := axesExceedingRevisionCap(state, 3)
	require.Len(t, got, 1, "axis-a at 3 trips cap=3; axis-b at 2 stays under")
	assert.Contains(t, got[0], "axis-a")
}

// TestDecisionRevisionCapEnvOverride locks in the env-var override for
// LOCUTUS_DECISION_REVISION_CAP. Mirrors the readSpecGateBudget test
// pattern: empty / non-numeric / non-positive values fall back to the
// default; a positive integer overrides.
func TestDecisionRevisionCapEnvOverride(t *testing.T) {
	t.Setenv("LOCUTUS_DECISION_REVISION_CAP", "5")
	assert.Equal(t, 5, readDecisionRevisionCap())

	t.Setenv("LOCUTUS_DECISION_REVISION_CAP", "")
	assert.Equal(t, defaultDecisionRevisionCap, readDecisionRevisionCap(),
		"empty env falls back to default")

	t.Setenv("LOCUTUS_DECISION_REVISION_CAP", "garbage")
	assert.Equal(t, defaultDecisionRevisionCap, readDecisionRevisionCap(),
		"non-numeric env falls back to default")

	t.Setenv("LOCUTUS_DECISION_REVISION_CAP", "-1")
	assert.Equal(t, defaultDecisionRevisionCap, readDecisionRevisionCap(),
		"non-positive env falls back to default")
}

// --- DJ-126 Phase 5: decision_revised history event tests ---

// TestDecisionRevisedEventRecordedOnReplace drives the wrapper Merge
// closure end-to-end: a replace-by-axis-ID match produces a queued
// PendingDecisionRevisedEvent; the wrapper drains it through the
// historian; the on-disk DJ-103 event carries the right target_id +
// prior/revised bodies.
func TestDecisionRevisedEventRecordedOnReplace(t *testing.T) {
	prior := RawDecisionProposal{
		ID: "dec-x", Title: "X v0", Rationale: "v0 rationale", Confidence: 0.7,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	}
	rawWithPrior, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)
	state := &PlanningState{
		RawProposal: string(rawWithPrior),
		Concerns: []Concern{{
			Status:             ConcernStatusOpen,
			AgentID:            "cost_critic",
			Severity:           "high",
			Text:               "X is wrong on cost",
			RelatedDecisionIDs: []string{"dec-x"},
		}},
	}
	revised := RawDecisionProposal{
		ID: "dec-x", Title: "X v1", Rationale: "v1 rationale", Confidence: 0.85,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x1", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "web", Reference: "https://x1", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	}

	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	mergeFn := mergeDecisionsRecording(historian)
	mergeFn(state, []RoundResult{makeDecisionResult(t, revised, 2)})

	// PendingDecisionRevisedEvents must have been drained by the wrapper.
	assert.Empty(t, state.PendingDecisionRevisedEvents,
		"wrapper Merge closure must drain PendingDecisionRevisedEvents after each call")

	events, err := historian.Events()
	require.NoError(t, err)
	require.Len(t, events, 1, "exactly one decision_revised event must be recorded")
	evt := events[0]
	assert.Equal(t, "decision_revised", evt.Kind)
	assert.Equal(t, "dec-x", evt.TargetID, "event target_id is the revised decision id")
	assert.Contains(t, evt.OldValue, "v0 rationale", "OldValue carries the prior decision body")
	assert.Contains(t, evt.NewValue, "v1 rationale", "NewValue carries the revised decision body")
	assert.Contains(t, evt.Rationale, "dec-x", "rationale names the decision id")
}

// TestDecisionRevisedEventIncludesDrivingConcern locks in the
// rationale-field contract: every driving concern (open concerns
// whose RelatedDecisionIDs contained the revised decision id at
// merge time) is named in the event's Rationale field. Snapshots
// taken BEFORE markConcernsAddressedByRevision flips status so the
// rationale carries the as-flagged finding text.
func TestDecisionRevisedEventIncludesDrivingConcern(t *testing.T) {
	prior := RawDecisionProposal{
		ID: "dec-datadog", Title: "Datadog v0", Rationale: "v0", Confidence: 0.7,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://d", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:      []string{"observability-stack"}, SurfacedBy: []string{"feat-monitoring"},
	}
	rawWithPrior, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)
	state := &PlanningState{
		RawProposal: string(rawWithPrior),
		Concerns: []Concern{
			{
				Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high",
				Text:               "Datadog conflicts with the $150 cost ceiling",
				RelatedDecisionIDs: []string{"dec-datadog"},
			},
			{
				Status: ConcernStatusOpen, AgentID: "sre_critic", Severity: "medium",
				Text:               "Datadog requires k8s sidecar that's out of scope",
				RelatedDecisionIDs: []string{"dec-datadog"},
			},
			{
				// Unrelated concern; must NOT appear in the rationale.
				Status: ConcernStatusOpen, AgentID: "architect_critic", Severity: "low",
				Text:               "Different unrelated concern",
				RelatedDecisionIDs: []string{"dec-other"},
			},
		},
	}
	revised := RawDecisionProposal{
		// DJ-133: id mirrors the prior decision's id verbatim (the
		// elaborator copies the axis ID through). Title / rationale
		// carry the chosen-option flip.
		ID: "dec-datadog", Title: "CloudWatch", Rationale: "switched",
		Confidence: 0.85,
		// DJ-128: revised alternatives preserve every prior alternative
		// (alt) AND demote the prior chosen (Datadog v0). Without
		// alt the monotonicity validator would reject the revision.
		Alternatives: []spec.Alternative{
			{Name: "Datadog v0", Rationale: "polish", RejectedBecause: "cost",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://aws", Excerpt: "e"}}},
			{Name: "alt", Rationale: "r", RejectedBecause: "r",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://d", Excerpt: "e"}}},
		},
		Citations: []spec.Citation{{Kind: "web", Reference: "https://aws", Excerpt: "e"}},
		Axes:      []string{"observability-stack"}, SurfacedBy: []string{"feat-monitoring"},
	}

	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	mergeFn := mergeDecisionsRecording(historian)
	mergeFn(state, []RoundResult{makeDecisionResult(t, revised, 2)})

	events, err := historian.Events()
	require.NoError(t, err)
	require.Len(t, events, 1)
	rationale := events[0].Rationale
	assert.Contains(t, rationale, "Datadog conflicts with the $150 cost ceiling",
		"rationale must name the cost critic's concern text")
	assert.Contains(t, rationale, "Datadog requires k8s sidecar",
		"rationale must name the sre critic's concern text")
	assert.Contains(t, rationale, "cost_critic", "rationale carries the raising agent_id for each concern")
	assert.Contains(t, rationale, "sre_critic", "rationale carries every related concern's agent_id")
	assert.NotContains(t, rationale, "Different unrelated concern",
		"rationale must NOT include unrelated concerns whose RelatedDecisionIDs didn't name the revised decision")
}

// TestMergeDecisionsRecordingNilHistorianIsNoOp verifies the wrapper
// is nil-safe: when historian is nil, the wrapper still drains the
// pending slice (so state stays clean for the next iteration) without
// panicking or erroring.
func TestMergeDecisionsRecordingNilHistorianIsNoOp(t *testing.T) {
	prior := RawDecisionProposal{
		ID: "dec-x", Title: "X", Rationale: "r", Confidence: 0.8,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	}
	rawWithPrior, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)
	state := &PlanningState{
		RawProposal: string(rawWithPrior),
		Concerns: []Concern{{
			Status: ConcernStatusOpen, Text: "concern", RelatedDecisionIDs: []string{"dec-x"},
		}},
	}
	revised := RawDecisionProposal{
		ID: "dec-x", Title: "X v1", Rationale: "v1", Confidence: 0.9,
		Alternatives: []spec.Alternative{{Name: "alt", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x1", Excerpt: "e"}}}},
		Citations: []spec.Citation{{Kind: "web", Reference: "https://x1", Excerpt: "e"}},
		Axes:      []string{"axis-a"}, SurfacedBy: []string{"feat-x"},
	}

	mergeFn := mergeDecisionsRecording(nil)
	mergeFn(state, []RoundResult{makeDecisionResult(t, revised, 1)})

	assert.Empty(t, state.PendingDecisionRevisedEvents,
		"nil-historian wrapper still drains the pending slice so state stays clean")
}

// --- DJ-126 Phase 6: end-to-end loop convergence via revision ---

// TestLoopConvergesAfterForcedContradictionViaRevision drives a full
// workflow run where iter-1's critique surfaces a contradiction
// (cost_critic flags dec-datadog); iter-2's revise-decisions step
// dispatches on the flagged decision and the mock returns a revised
// dec-cloudwatch on the same axis; mergeDecisions replaces dec-datadog
// in place and marks the cost concern as addressed; iter-2's scout
// returns Converged:true.
//
// Asserts:
//   - the workflow exits cleanly within 3 iterations;
//   - a decision_revised DJ-103 event is recorded;
//   - the final ProposedSpec's decisions[] carries the revised body.
func TestLoopConvergesAfterForcedContradictionViaRevision(t *testing.T) {
	fs := setupSpecGenFixtureDJ124(t)

	// DJ-129: scout surfaces a cost dimension every iter; one
	// spec_critic_elaborator call fires per iter.
	costDim := CritiqueDimension{
		ID: "cost-ceiling-coverage", Lens: "cost",
		FocusQuestion:  "Does the proposal fit the $150/mo ceiling?",
		SourceEvidence: []string{"GOALS.md $150/mo ceiling clause"},
		Disciplines:    []string{"goals_grounded", "web_grounded"},
		SeverityFloor:  "high",
	}
	// iter-0 scout: two axes + one new node.
	scout0 := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "monitoring product",
		AxesOpen: []OpenAxis{
			{ID: "auth-provider", Description: "How do users authenticate?",
				SourceEvidence: []string{"goals mention sso"}, SurfacedBy: []string{"feat-monitoring"}},
			{ID: "observability-stack", Description: "What backs metrics and logs?",
				SourceEvidence: []string{"goals mention SLOs"}, SurfacedBy: []string{"feat-monitoring"}},
		},
		NewNodes: []NewSpecNode{{
			Kind: "feature", ID: "feat-monitoring", Title: "Monitoring product",
			Summary: "Metrics + logs dashboard.", Decisions: []string{},
		}},
		CritiqueDimensions: []CritiqueDimension{costDim},
		Converged:          false,
	})

	// iter-1 first-author decisions: dec-cognito-auth, dec-datadog-obs.
	decCognito := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-cognito-auth", Title: "Cognito auth", Rationale: "AWS-native",
		Confidence: 0.85,
		Alternatives: []spec.Alternative{{Name: "Auth0", Rationale: "polish", RejectedBecause: "cost",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://auth", Excerpt: "e"}}}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"auth-provider"}, SurfacedBy: []string{"feat-monitoring"},
	})
	decDatadog := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-datadog-obs", Title: "Datadog observability", Rationale: "Best APM",
		Confidence: 0.8,
		Alternatives: []spec.Alternative{{Name: "CloudWatch", Rationale: "AWS-native", RejectedBecause: "less polish",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://cw", Excerpt: "e"}}}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"observability-stack"}, SurfacedBy: []string{"feat-monitoring"},
	})

	// iter-1 narrative: feat-monitoring references both decisions.
	featMonitoring := featureProposalJSON(t, RawFeatureProposal{
		ID: "feat-monitoring", Title: "Monitoring product",
		Description: "Metrics + logs.", Decisions: []string{"dec-cognito-auth", "dec-datadog-obs"},
	})

	// iter-1 critique: cost_critic flags the dec-datadog cost issue.
	// extractDecisionRefsFromText picks up the dec-datadog-obs mention
	// in the Weakness text and populates RelatedDecisionIDs.
	costIssue := mustJSON(t, CriticIssues{Issues: []CriticIssue{{
		Weakness: "dec-datadog-obs conflicts with the $150 monthly cost ceiling per GOALS.md.",
		Evidence: "GOALS.md names a $150 monthly ceiling and Datadog's per-host pricing exceeds it at the assumed fleet size.",
		Counterproposals: []CriticCounterproposal{{
			Option:   "Switch from Datadog to CloudWatch + Sentry",
			Argument: "CloudWatch + Sentry combined land under $50/mo at the assumed scale where Datadog Pro lands at ~$300/mo.",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://aws.amazon.com/cloudwatch/pricing/", Excerpt: "CloudWatch Logs: $0.50 per GB ingested"}},
		}},
		RelatedDecisionIDs: []string{"dec-datadog-obs"},
	}}})
	noIssues := `{"issues":[]}`

	// iter-1 scout (tail): nothing new; concern stays open; not converged.
	scoutIter1Open := scoutBriefJSON(t, ScoutBrief{
		DomainRead:         "monitoring product",
		AxesOpen:           []OpenAxis{},
		NewNodes:           []NewSpecNode{},
		CritiqueDimensions: []CritiqueDimension{costDim},
		Converged:          false,
	})

	// iter-2 revise-decisions: dec-datadog-obs revised to CloudWatch
	// (same axis, same id). DJ-128: the elaborator's alternatives
	// must preserve every prior alternative (CloudWatch was prior alt;
	// promoted to the chosen here so it appears as the Title — the
	// monotonicity validator skips the title-vs-alt-name match) AND
	// demote the prior chosen Datadog observability — my merge helpers
	// fill in the demotion + counterproposal fold automatically.
	revisedDecCloudWatch := decisionProposalJSON(t, RawDecisionProposal{
		ID: "dec-datadog-obs", Title: "CloudWatch observability",
		Rationale:  "CloudWatch fits within the cost ceiling.",
		Confidence: 0.85,
		Alternatives: []spec.Alternative{
			{Name: "Datadog", Rationale: "best APM", RejectedBecause: "exceeds cost ceiling",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://datadog", Excerpt: "e"}}},
			// CloudWatch was the prior alternative; the elaborator
			// promoted it to chosen. Preserve it in the alternatives
			// slice anyway so monotonicity holds (the validator does
			// not treat promotion to Title as removal-from-alts).
			{Name: "CloudWatch", Rationale: "AWS-native", RejectedBecause: "promoted to chosen — preserved here as deliberation log",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://cw", Excerpt: "e"}}},
		},
		Citations:  []spec.Citation{{Kind: "web", Reference: "https://aws", Excerpt: "CloudWatch pricing"}},
		Axes:       []string{"observability-stack"}, // same axis → triggers replace
		SurfacedBy: []string{"feat-monitoring"},
	})

	// iter-2 scout: converged (concern now addressed). The cost dimension
	// remains stable across iterations so dimensionsAreStable holds.
	scoutIter2Converged := scoutBriefJSON(t, ScoutBrief{
		DomainRead:         "monitoring product",
		AxesOpen:           []OpenAxis{},
		NewNodes:           []NewSpecNode{},
		CritiqueDimensions: []CritiqueDimension{costDim},
		Converged:          true,
	})

	tmp := t.TempDir()
	osFS := specio.NewOSFS(tmp)
	require.NoError(t, osFS.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(osFS, ".borg/history")

	mock := NewMockExecutor(
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scout0, Model: "m"}},
		// iter-1 (first-author + narrative + reconcile + critique + tail scout)
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decCognito, Model: "m"}},
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decDatadog, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featMonitoring, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: costIssue, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutIter1Open, Model: "m"}},
		// iter-2 (decisions + narrative skipped; revise-decisions fires; reconcile; critique; tail scout)
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: revisedDecCloudWatch, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_critic_elaborator", Response: &AgentOutput{Content: noIssues, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: scoutIter2Converged, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(historian, 5)
	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Ship a monitoring product within a $150/mo budget.",
	}, wf)
	require.NoError(t, err, "loop must converge via the revise path")
	require.NotNil(t, proposal)

	// The final proposal carries the revised CloudWatch decision under
	// the preserved id dec-datadog-obs.
	var foundRevised *DecisionProposal
	for i := range proposal.Decisions {
		if proposal.Decisions[i].ID == "dec-datadog-obs" {
			foundRevised = &proposal.Decisions[i]
			break
		}
	}
	require.NotNil(t, foundRevised, "the revised decision must persist at the preserved id")
	assert.Equal(t, "CloudWatch observability", foundRevised.Title,
		"final proposal carries the revised body, not the original datadog body")
	assert.Contains(t, foundRevised.Rationale, "cost ceiling",
		"final proposal carries the revised rationale citing the cost reason")

	// A decision_revised DJ-103 event must be recorded.
	events, err := historian.Events()
	require.NoError(t, err)
	var revisedEvents []history.Event
	for _, e := range events {
		if e.Kind == "decision_revised" {
			revisedEvents = append(revisedEvents, e)
		}
	}
	require.Len(t, revisedEvents, 1, "exactly one decision_revised event must be recorded for the loop")
	assert.Equal(t, "dec-datadog-obs", revisedEvents[0].TargetID,
		"event names the revised decision id (preserved across the replace)")
	assert.Contains(t, revisedEvents[0].Rationale, "cost ceiling",
		"event rationale captures the driving concern text")
}

// --- DJ-126 Phase 2 regression: dedup prevents revision-cap inflation ---

// TestFanoutDedupPreventsRevisionCapInflation locks in the
// dedup-by-decision-ID fix from the post-validation winplan re-run.
//
// Failure mode this guards against: with per-(concern, decision)-pair
// fanout, N concerns flagging the same decision in one iteration
// would dispatch N parallel revise calls. mergeDecisions processes
// them sequentially against the same axis: each result intersects
// with the prior-replaced decision's axes, so each result REPLACES
// the in-flight decision again and bumps AxisRevisionCount by 1.
// On the winplan re-run, 5 concerns about dec-neon-voter-data-store
// pushed the count to 5 in a single iteration and tripped the cap
// (default 3) before the next iteration could ever fire — even though
// the loop was making forward progress.
//
// Post-fix: fanoutReviseableConcerns groups open concerns by
// decision id; one fanout item per unique decision; one merge
// result per item; one AxisRevisionCount increment per decision per
// iteration. The cap=3 now means what the plan intended: three
// rounds of cross-iteration oscillation, not three concerns about
// one decision in one round.
func TestFanoutDedupPreventsRevisionCapInflation(t *testing.T) {
	const rawWithDec = `{"features":[],"strategies":[],"decisions":[
		{"id":"dec-neon","title":"Neon for OLTP","rationale":"Postgres-compatible.","confidence":0.8,"alternatives":[{"name":"RDS","rationale":"familiar","rejected_because":"cost","citations":[{"kind":"web","reference":"https://rds","excerpt":"e"}]}],"citations":[{"kind":"goals","reference":"GOALS.md","excerpt":"e"}],"axes":["voter-data-store"],"surfaced_by":["feat-voter"]}
	]}`

	// Five distinct open concerns, all flagging dec-neon. With the
	// pre-fix per-(concern, decision)-pair fanout this would produce
	// 5 items; with the post-fix dedup it produces exactly 1.
	state := &PlanningState{
		RawProposal: rawWithDec,
		Concerns: []Concern{
			{Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high", Text: "dec-neon exceeds cost ceiling", RelatedDecisionIDs: []string{"dec-neon"}},
			{Status: ConcernStatusOpen, AgentID: "sre_critic", Severity: "medium", Text: "dec-neon lacks backup story", RelatedDecisionIDs: []string{"dec-neon"}},
			{Status: ConcernStatusOpen, AgentID: "devops_critic", Severity: "medium", Text: "dec-neon migration tooling undefined", RelatedDecisionIDs: []string{"dec-neon"}},
			{Status: ConcernStatusOpen, AgentID: "architect_critic", Severity: "low", Text: "dec-neon connection limits unclear", RelatedDecisionIDs: []string{"dec-neon"}},
			{Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "low", Text: "dec-neon idle-billing rules ungrounded", RelatedDecisionIDs: []string{"dec-neon"}},
		},
	}

	items, err := fanoutReviseableConcerns(state)
	require.NoError(t, err)
	require.Len(t, items, 1, "five concerns about the same decision must produce exactly one fanout item")

	// Simulate the executor: one fanout item produces one revise
	// result; the merge runs against that single result.
	revised := RawDecisionProposal{
		ID:        "dec-neon",
		Title:     "Neon (revised)",
		Rationale: "Addresses cost / backup / migration / connection / billing concerns.",
		Confidence: 0.9,
		Alternatives: []spec.Alternative{{Name: "RDS", Rationale: "r", RejectedBecause: "r",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://rds", Excerpt: "e"}}}},
		Citations:  []spec.Citation{{Kind: "web", Reference: "https://neon", Excerpt: "e"}},
		Axes:       []string{"voter-data-store"},
		SurfacedBy: []string{"feat-voter"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, revised, 2)})

	// One revision dispatch → one replace → one count bump.
	require.Equal(t, 1, state.AxisRevisionCount["voter-data-store"],
		"a single revise-decisions phase bumps AxisRevisionCount by exactly 1, regardless of how many concerns named the decision")

	// All five concerns get marked addressed by the single replacement
	// (they all referenced dec-neon in RelatedDecisionIDs).
	addressedCount := 0
	for _, c := range state.Concerns {
		if c.Status == ConcernStatusAddressed {
			addressedCount++
		}
	}
	assert.Equal(t, 5, addressedCount,
		"every concern referencing the revised decision id is marked addressed by the replacement, even though only one revision dispatched")
}
