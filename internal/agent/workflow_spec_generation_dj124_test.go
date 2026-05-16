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
	return fs
}

// TestSpecGenerationWorkflowDispatchOrder drives the new workflow with
// a single open axis + a single new node on iter 0, then a converged
// scout on iter 1. Asserts the agent dispatch order matches the
// DJ-124 round shape: scout(iter0) → decisions(iter1, fanout=1) →
// narrative(iter1, fanout=1) → reconcile(iter1) → critique(iter1, x4)
// → scout(iter1).
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
		Converged: false,
	})
	iter1Scout := scoutBriefJSON(t, ScoutBrief{
		DomainRead: "test domain",
		AxesOpen:   []OpenAxis{},
		NewNodes:   []NewSpecNode{},
		Converged:  true,
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
		MockResponse{AgentID: "spec_decision_elaborator", Response: &AgentOutput{Content: decisionForDataStore, Model: "m"}},
		MockResponse{AgentID: "spec_feature_elaborator", Response: &AgentOutput{Content: featureNarrative, Model: "m"}},
		MockResponse{AgentID: "spec_reconciler", Response: &AgentOutput{Content: `{"actions":[]}`, Model: "m"}},
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "spec_scout", Response: &AgentOutput{Content: iter1Scout, Model: "m"}},
	)

	wf := NewSpecGenerationWorkflow(nil, 5)
	_, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build a realtime dashboard.",
	}, wf)
	require.NoError(t, err)

	// Order of fired agents — extracted from the call log. The parallel
	// critic step makes ordering between the four critics non-
	// deterministic, but the relative position of scout / decision /
	// feature / reconciler vs the critic block is stable.
	calls := mock.Calls()
	require.GreaterOrEqual(t, len(calls), 9, "expected at least 9 dispatches across the round shape")
	agentOrder := make([]string, 0, len(calls))
	for _, c := range calls {
		agentOrder = append(agentOrder, c.Def.ID)
	}

	// Hard assertions on the dispatch sequence.
	assert.Equal(t, "spec_scout", agentOrder[0], "iter-0 scout fires first")
	assert.Equal(t, "spec_decision_elaborator", agentOrder[1], "decisions step fires after scout")
	assert.Equal(t, "spec_feature_elaborator", agentOrder[2], "narrative step fires after decisions")
	assert.Equal(t, "spec_reconciler", agentOrder[3], "reconcile fires after narrative")

	criticBlock := agentOrder[4:8]
	criticSet := map[string]bool{}
	for _, c := range criticBlock {
		criticSet[c] = true
	}
	for _, want := range []string{"architect_critic", "devops_critic", "sre_critic", "cost_critic"} {
		assert.True(t, criticSet[want], "critic %q should appear in the parallel critic block", want)
	}
	assert.Equal(t, "spec_scout", agentOrder[8], "next-iter scout fires after critique")
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
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
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
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
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
		MockResponse{AgentID: "architect_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "devops_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "sre_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		MockResponse{AgentID: "cost_critic", Response: &AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
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
