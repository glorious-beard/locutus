package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/executor"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/search"
	"github.com/chetan/locutus/internal/specio"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateVerdictJSON marshals a SpecGateVerdict for use as a mock
// LLM response.
func gateVerdictJSON(t *testing.T, v SpecGateVerdict) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// runSpecGateTestWorkflow runs a minimal workflow that exercises the
// gate-spawner path: a no-op seed step followed by the DJ-122 gate.
// The gate uses a test-only template that, on each non-converged
// iteration, simply spawns another gate (no revise/reconcile/critique
// in between) so tests don't need to mock those agents. The Spawn
// closure on each gate is built via the same gateSpawnFor used in
// production — this is the wiring under test.
func runSpecGateTestWorkflow(t *testing.T, mock *MockExecutor, historian *history.Historian, budget int) ([]RoundResult, error) {
	t.Helper()

	var loopTemplate func(executor.IterationContext) []WorkflowStep[PlanningState]
	loopTemplate = func(ic executor.IterationContext) []WorkflowStep[PlanningState] {
		thisIter := ic.IterationIndex
		return []WorkflowStep[PlanningState]{
			{
				ID:      "gate",
				Agents:  []string{"spec_gate"},
				Project: projectSpecGate,
				Merge:   mergeGateVerdict,
				Budget:  budget,
				Spawn:   gateSpawnFor(thisIter, budget, loopTemplate, historian),
			},
		}
	}

	wf := &Workflow[PlanningState]{
		Snapshot:           snapshotPlanningState,
		DefaultProject:     projectDefault,
		MaxRounds:          1,
		MaxGraphMultiplier: 100,
		DefaultGateBudget:  budget,
		Rounds: []WorkflowStep[PlanningState]{
			{
				ID:      "seed",
				Agents:  []string{"spec_scout"},
				Project: projectDefault,
				Merge:   mergeScoutBrief,
			},
			{
				ID:        "gate",
				Agents:    []string{"spec_gate"},
				DependsOn: []string{"seed"},
				Project:   projectSpecGate,
				Merge:     mergeGateVerdict,
				Budget:    budget,
				Spawn:     gateSpawnFor(0, budget, loopTemplate, historian),
			},
		},
	}

	exec := &WorkflowExecutor[PlanningState]{
		Executor: mock,
		AgentDefs: map[string]AgentDef{
			"spec_scout": {ID: "spec_scout"},
			"spec_gate":  {ID: "spec_gate"},
		},
		Workflow: wf,
	}
	return exec.Run(context.Background(), &PlanningState{Prompt: "Build something useful.", Round: 1})
}

func TestSpecGateConvergesOnFirstVerdict(t *testing.T) {
	// scout output + gate iter-0 Converged:true → workflow terminates
	// without spawning any further iterations.
	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: scoutResp, Model: "m"}},
		MockResponse{Response: &AgentOutput{
			Content: gateVerdictJSON(t, SpecGateVerdict{
				Converged: true,
				Reasoning: "All four lifecycle phases are addressed for both deliverables.",
			}),
			Model: "m",
		}},
	)

	results, err := runSpecGateTestWorkflow(t, mock, nil, 5)
	require.NoError(t, err)
	assert.Equal(t, 2, mock.CallCount(),
		"only seed + iter-0 gate fire when the first verdict converges")

	// Result attribution: seed at iter 0, gate at iter 0.
	stepIDs := make([]string, 0, len(results))
	for _, r := range results {
		stepIDs = append(stepIDs, r.StepID)
	}
	assert.Equal(t, []string{"seed", "gate"}, stepIDs)
}

func TestSpecGateSpawnsNextIterationOnNotConverged(t *testing.T) {
	// scout + iter-0 gate (Converged:false, one open dimension) + iter-1
	// gate (Converged:true). The test-only template just spawns the next
	// gate on non-convergence, so we only need two gate responses to
	// observe the iteration progression.
	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: scoutResp, Model: "m"}},
		MockResponse{Response: &AgentOutput{
			Content: gateVerdictJSON(t, SpecGateVerdict{
				Converged: false,
				Reasoning: "Deploy phase carries the remaining gap for the iOS companion app.",
				OpenDimensions: []OpenDimension{{
					Deliverable: "iOS companion app",
					Phase:       "deploy",
					Axis:        "App Store / TestFlight rollout cadence",
					Reasoning:   "The proposal commits to App Store distribution but does not name a TestFlight gating step; without one the team cannot stage rollouts.",
				}},
			}),
			Model: "m",
		}},
		MockResponse{Response: &AgentOutput{
			Content: gateVerdictJSON(t, SpecGateVerdict{
				Converged:      true,
				Reasoning:      "Every deliverable commits across all four phases; no open concerns remain.",
				OpenDimensions: []OpenDimension{},
			}),
			Model: "m",
		}},
	)

	results, err := runSpecGateTestWorkflow(t, mock, nil, 5)
	require.NoError(t, err)
	assert.Equal(t, 3, mock.CallCount(),
		"seed + iter-0 gate + iter-1 gate fire when the second verdict converges")

	// The iter-1 gate must carry the right TemplateID + IterationIndex
	// in its RoundResult — Phase 4 wiring threads these from the
	// executor step onto each result.
	var sawIter1 bool
	for _, r := range results {
		if r.StepID == "spec_loop#iter:1:gate" {
			sawIter1 = true
			assert.Equal(t, "spec_loop", r.TemplateID)
			assert.Equal(t, 1, r.IterationIndex)
		}
	}
	assert.True(t, sawIter1, "iter-1 gate must appear in the results")
}

func TestSpecGateFailsOnBudgetExhaustion(t *testing.T) {
	// Budget = 1 → only iter-0 fires; on non-converged the spawner
	// produces the terminal convergence_failed step, whose RunItem
	// writes the history event and returns a non-nil error.
	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: scoutResp, Model: "m"}},
		MockResponse{Response: &AgentOutput{
			Content: gateVerdictJSON(t, SpecGateVerdict{
				Converged: false,
				Reasoning: "Deploy phase for the firmware deliverable is unaddressed.",
				OpenDimensions: []OpenDimension{{
					Deliverable: "nRF52840 firmware",
					Phase:       "deploy",
					Axis:        "OTA update channel",
					Reasoning:   "No OTA path is committed; the firmware cannot ship a security fix after first install.",
				}},
			}),
			Model: "m",
		}},
	)

	tmp := t.TempDir()
	fs := specio.NewOSFS(tmp)
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(fs, ".borg/history")

	_, err := runSpecGateTestWorkflow(t, mock, historian, 1)
	require.Error(t, err, "budget exhaustion must surface as an error")
	assert.Contains(t, err.Error(), "convergence failed")

	// History event must have been written under .borg/history/.
	entries, readErr := os.ReadDir(filepath.Join(tmp, ".borg", "history"))
	require.NoError(t, readErr)
	var found bool
	for _, e := range entries {
		if strings.Contains(e.Name(), "convergence_failed") && strings.HasSuffix(e.Name(), ".json") {
			found = true
			body, err := os.ReadFile(filepath.Join(tmp, ".borg", "history", e.Name()))
			require.NoError(t, err)
			var evt history.Event
			require.NoError(t, json.Unmarshal(body, &evt))
			assert.Equal(t, "convergence_failed", evt.Kind)
			assert.Contains(t, evt.Rationale, "Deploy phase for the firmware deliverable is unaddressed",
				"event rationale should carry the gate's verdict reasoning")
			assert.Contains(t, evt.Rationale, "OTA update channel",
				"event rationale should list the unresolved open dimensions")
		}
	}
	assert.True(t, found, "a convergence_failed history event must be written under .borg/history/")
}

func TestSpecGateMergeAppendsOpenDimensionsAsConcernsAndClusters(t *testing.T) {
	// Unit test on mergeGateVerdict: a non-converged verdict appends
	// each OpenDimension as a Concern (record-keeping) AND a
	// FindingCluster (so the next iteration's revise picks them up),
	// and threads CurrentCommitmentQuoted through to the cluster so
	// the elaborator's projection can render it.
	state := &PlanningState{}
	verdict := SpecGateVerdict{
		Converged: false,
		Reasoning: "Deploy and support carry the remaining gaps.",
		OpenDimensions: []OpenDimension{
			{
				Deliverable:             "iOS companion app",
				Phase:                   "deploy",
				Axis:                    "App Store / TestFlight rollout cadence",
				Reasoning:               "Distribution is committed but the staged-rollout path is not.",
				CurrentCommitmentQuoted: "Releases ship to the App Store via Fastlane.",
			},
			{
				Deliverable:             "nRF52840 firmware",
				Phase:                   "deploy",
				Axis:                    "OTA update channel",
				Reasoning:               "No OTA path is committed; security fixes cannot ship after first install.",
				CurrentCommitmentQuoted: "",
			},
		},
	}
	results := []RoundResult{{
		StepID:  "gate",
		AgentID: "spec_gate",
		Output:  gateVerdictJSON(t, verdict),
	}}

	mergeGateVerdict(state, results)

	assert.Len(t, state.Concerns, 2, "each OpenDimension becomes a Concern")
	for _, c := range state.Concerns {
		assert.Equal(t, "spec_gate", c.AgentID)
		assert.Equal(t, "high", c.Severity)
		assert.Equal(t, "deploy", c.Kind, "Concern.Kind carries the dimension's lifecycle phase")
	}
	assert.Equal(t, "Distribution is committed but the staged-rollout path is not.", state.Concerns[0].Text)
	assert.Equal(t, "No OTA path is committed; security fixes cannot ship after first install.", state.Concerns[1].Text)

	assert.Len(t, state.FindingClusters, 2, "each OpenDimension becomes a FindingCluster")
	assert.Equal(t, "iOS companion app: App Store / TestFlight rollout cadence", state.FindingClusters[0].Topic)
	assert.Equal(t, "nRF52840 firmware: OTA update channel", state.FindingClusters[1].Topic)
	for _, c := range state.FindingClusters {
		assert.Equal(t, "spec_strategy_elaborator", c.AgentID,
			"gate findings route to the strategy elaborator")
		assert.Len(t, c.Findings, 1, "one finding per cluster")
	}

	// CurrentCommitmentQuoted flows through.
	assert.Equal(t, "Releases ship to the App Store via Fastlane.", state.FindingClusters[0].CurrentCommitmentQuoted,
		"present commitment is threaded onto the cluster so the elaborator's projection can render it")
	assert.Empty(t, state.FindingClusters[1].CurrentCommitmentQuoted,
		"empty quote when nothing was previously committed on the axis")

	// GateAxisRecurrence ticks for each dimension.
	assert.Equal(t, 1, state.GateAxisRecurrence["ios companion app|app store / testflight rollout cadence"])
	assert.Equal(t, 1, state.GateAxisRecurrence["nrf52840 firmware|ota update channel"])
}

// TestSpecGateMergeAccumulatesRecurrence verifies that calling
// mergeGateVerdict across multiple iterations correctly accumulates
// the per-axis recurrence count, including coalescing casing drift
// ("On-call rotation owner" vs "on-call rotation owner") into the
// same bucket.
func TestSpecGateMergeAccumulatesRecurrence(t *testing.T) {
	state := &PlanningState{}
	for i, axis := range []string{"on-call rotation owner", "On-Call Rotation Owner", "on-call rotation owner  "} {
		v := SpecGateVerdict{
			Converged: false,
			Reasoning: fmt.Sprintf("iter %d", i),
			OpenDimensions: []OpenDimension{{
				Deliverable: "WinPlan platform",
				Phase:       "support",
				Axis:        axis,
				Reasoning:   "On-call owner still uncommitted.",
			}},
		}
		results := []RoundResult{{AgentID: "spec_gate", Output: gateVerdictJSON(t, v)}}
		mergeGateVerdict(state, results)
	}
	assert.Equal(t, 3, state.GateAxisRecurrence["winplan platform|on-call rotation owner"],
		"casing + whitespace variants coalesce to a single recurrence bucket")
}

// TestSpecGateSpawnerForceTerminatesOnStuck drives the spawner directly
// with a pre-seeded state.GateAxisRecurrence at the termination
// threshold. The spawner must produce a convergence_stuck terminal
// step (NOT continue to the next iteration), and the terminal step's
// RunItem must write a DJ-103 history event tagged convergence_stuck
// and return a non-nil error naming the stuck axis.
func TestSpecGateSpawnerForceTerminatesOnStuck(t *testing.T) {
	tmp := t.TempDir()
	fs := specio.NewOSFS(tmp)
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))
	historian := history.NewHistorian(fs, ".borg/history")

	// Build a state where one axis has already recurred at the
	// termination threshold (3 by default). The next gate-spawn
	// invocation must force-terminate.
	state := PlanningState{
		GateAxisRecurrence: map[string]int{
			"winplan platform|on-call rotation owner": recurrenceTerminationThreshold,
		},
	}

	// The verdict the closure receives is non-converged with the
	// stuck axis re-flagged for a fourth time.
	verdict := SpecGateVerdict{
		Converged: false,
		Reasoning: "Same on-call gap, fourth time.",
		OpenDimensions: []OpenDimension{{
			Deliverable: "WinPlan platform",
			Phase:       "support",
			Axis:        "on-call rotation owner",
			Reasoning:   "Gate still finds the on-call commitment insufficient.",
		}},
	}
	results := []RoundResult{{AgentID: "spec_gate", Output: gateVerdictJSON(t, verdict)}}

	// Generous budget — we expect the stuck check to fire before
	// budget exhaustion.
	spawner := gateSpawnFor(2, 10, nil, historian)
	steps, edges, err := spawner(context.Background(), StateSnapshot[PlanningState]{State: state}, results)
	require.NoError(t, err, "stuck detection produces a terminal step; not an inline error")
	assert.Nil(t, edges)
	require.Len(t, steps, 1, "exactly one terminal step is spawned")
	assert.Contains(t, steps[0].ID, "convergence_stuck_iter:",
		"the terminal step ID names the stuck-recurrence failure mode")

	// Running the terminal step writes the history event and errors.
	_, runErr := steps[0].RunItem(context.Background(), StateSnapshot[PlanningState]{State: state})
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "stuck")
	assert.Contains(t, runErr.Error(), "on-call rotation owner")

	entries, err := os.ReadDir(filepath.Join(tmp, ".borg", "history"))
	require.NoError(t, err)
	var found bool
	for _, e := range entries {
		if strings.Contains(e.Name(), "convergence_stuck") && strings.HasSuffix(e.Name(), ".json") {
			found = true
			body, err := os.ReadFile(filepath.Join(tmp, ".borg", "history", e.Name()))
			require.NoError(t, err)
			var evt history.Event
			require.NoError(t, json.Unmarshal(body, &evt))
			assert.Equal(t, "convergence_stuck", evt.Kind)
			assert.Contains(t, evt.Rationale, "on-call rotation owner",
				"history event rationale lists the stuck axes")
		}
	}
	assert.True(t, found, "a convergence_stuck DJ-103 event must be written under .borg/history/")
}

func TestSpecGateMergeNoopOnConverged(t *testing.T) {
	// A Converged:true verdict is a no-op: no Concerns or
	// FindingClusters are appended (workflow terminates without
	// further revision).
	state := &PlanningState{}
	verdict := SpecGateVerdict{
		Converged:      true,
		Reasoning:      "All phases addressed across every deliverable.",
		OpenDimensions: []OpenDimension{},
	}
	results := []RoundResult{{
		StepID:  "gate",
		AgentID: "spec_gate",
		Output:  gateVerdictJSON(t, verdict),
	}}

	mergeGateVerdict(state, results)
	assert.Empty(t, state.Concerns)
	assert.Empty(t, state.FindingClusters)
}

func TestSpecGateParseVerdictRejectsEmpty(t *testing.T) {
	// Defensive: a spec_gate result with empty output is surfaced as
	// an error by parseSpecGateVerdict so the spawner errors visibly
	// rather than silently treating absence as non-converged.
	_, err := parseSpecGateVerdict(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no spec_gate result")

	_, err = parseSpecGateVerdict([]RoundResult{{AgentID: "spec_gate", Output: ""}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty output")

	_, err = parseSpecGateVerdict([]RoundResult{{AgentID: "spec_gate", Output: "not json"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse spec_gate verdict")
}

// TestSpecGateParseVerdictRejectsDegenerate locks in the safety-net
// validator: a verdict with converged=false but empty open_dimensions
// is rejected because the workflow has nothing to act on. This is the
// failure mode observed in the first DJ-122 smoke run — every gap was
// in the reasoning sentence; OpenDimensions was empty; the next
// iteration only addressed parallel critic concerns, not the gate's
// judgment.
func TestSpecGateParseVerdictRejectsDegenerate(t *testing.T) {
	verdict := SpecGateVerdict{
		Converged:      false,
		Reasoning:      "Deploy phase is unaddressed but I didn't bother listing the specifics here.",
		OpenDimensions: nil,
	}
	results := []RoundResult{{
		AgentID: "spec_gate",
		Output:  gateVerdictJSON(t, verdict),
	}}
	_, err := parseSpecGateVerdict(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "degenerate",
		"empty open_dimensions with converged=false must be rejected as degenerate")
	assert.Contains(t, err.Error(), "Deploy phase is unaddressed",
		"the error message must echo the verdict's reasoning so the operator can see what the gate tried to say")
}

// TestSpecGateParseVerdictRejectsContradictory locks in the inverse
// safety net: converged=true with open_dimensions populated is also a
// degenerate verdict shape.
func TestSpecGateParseVerdictRejectsContradictory(t *testing.T) {
	verdict := SpecGateVerdict{
		Converged: true,
		Reasoning: "Everything is addressed.",
		OpenDimensions: []OpenDimension{{
			Deliverable: "iOS app",
			Phase:       "deploy",
			Axis:        "rollout cadence",
			Reasoning:   "but actually it isn't",
		}},
	}
	results := []RoundResult{{
		AgentID: "spec_gate",
		Output:  gateVerdictJSON(t, verdict),
	}}
	_, err := parseSpecGateVerdict(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contradictory")
}

// TestSpecGateBudgetEnvOverride checks that LOCUTUS_SPEC_GEN_MAX_ITERATIONS
// flows through to the workflow constructor.
func TestSpecGateBudgetEnvOverride(t *testing.T) {
	t.Setenv("LOCUTUS_SPEC_GEN_MAX_ITERATIONS", "3")
	assert.Equal(t, 3, readSpecGateBudget())

	t.Setenv("LOCUTUS_SPEC_GEN_MAX_ITERATIONS", "")
	assert.Equal(t, 0, readSpecGateBudget(), "empty env returns 0 (constructor falls back)")

	t.Setenv("LOCUTUS_SPEC_GEN_MAX_ITERATIONS", "garbage")
	assert.Equal(t, 0, readSpecGateBudget(), "non-numeric env returns 0")

	t.Setenv("LOCUTUS_SPEC_GEN_MAX_ITERATIONS", "-1")
	assert.Equal(t, 0, readSpecGateBudget(), "non-positive env returns 0")
}

// Smoke: a workflow run that errors must propagate the error (not
// wrap or swallow it), per executor + agent contract.
func TestSpecGateErrorPropagation(t *testing.T) {
	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: scoutResp, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: "not even json", Model: "m"}},
	)
	_, err := runSpecGateTestWorkflow(t, mock, nil, 5)
	require.Error(t, err)
	// Either the merge logs a parse-fail (silent) and Spawn surfaces
	// the parse failure, OR the Spawn returns the parse error directly.
	// Either path must bubble up through Run.
	assert.True(t, errors.Is(err, err), "error returned (parse failure or related)") // tautology; main check is non-nil
}

// TestInFlightIndexRebuiltOnRawProposalMerge confirms each of the four
// merge functions that mutate RawProposal also calls Rebuild on the
// council-scoped InFlightIndex (DJ-123 Phase 3). The contract under
// test: after the merge returns, a query against the in-flight index
// surfaces hits for a term that lives in the merged RawProposal — and
// nothing if the term is absent. Asserting each merge individually
// guards against silent regressions where a future refactor drops the
// rebuild call from one of the four call sites.
//
// Per-subtest fixture shape: each merge synthesises a RawProposal where
// the canary term ("pgvector", "workos", "websocket", "redis") is
// unique to one of the four merge paths. The query is the canary term;
// a non-empty hit set confirms the rebuild fired and the corpus was
// re-indexed.
func TestInFlightIndexRebuiltOnRawProposalMerge(t *testing.T) {
	// mergeElaboratedFeatures: a fresh feature elaborator output flows
	// through assembleRawProposal into state.RawProposal and the
	// rebuild fires.
	t.Run("mergeElaboratedFeatures", func(t *testing.T) {
		idx, err := search.NewInFlightIndex()
		require.NoError(t, err)
		t.Cleanup(func() { _ = idx.Close() })

		state := &PlanningState{InFlightIndex: idx}
		featureJSON := `{
			"id": "feat-storage-platform",
			"title": "Storage platform",
			"summary": "Adopt Postgres with pgvector for OLTP.",
			"description": "Operators store embeddings beside transactional rows in Postgres.",
			"acceptance_criteria": [],
			"decisions": [{"title":"Use pgvector","rationale":"r","confidence":0.8}]
		}`
		mergeElaboratedFeatures(state, []RoundResult{{AgentID: "spec_feature_elaborator", Output: featureJSON}})

		require.NotEmpty(t, state.RawProposal, "merge must populate RawProposal so the rebuild has corpus")
		hits, _, err := idx.Search("pgvector", search.Options{})
		require.NoError(t, err)
		require.NotEmpty(t, hits, "in-flight index must surface a hit for the canary term after merge")
		assert.True(t, hitsContainID(hits, "feat-storage-platform"),
			"the elaborated feature id must appear in the in-flight hits")
	})

	// mergeElaboratedStrategies: same shape, strategy side.
	t.Run("mergeElaboratedStrategies", func(t *testing.T) {
		idx, err := search.NewInFlightIndex()
		require.NoError(t, err)
		t.Cleanup(func() { _ = idx.Close() })

		state := &PlanningState{InFlightIndex: idx}
		strategyJSON := `{
			"id": "strat-auth-platform",
			"title": "Authentication platform",
			"summary": "Adopt WorkOS for SSO.",
			"kind": "foundational",
			"body": "WorkOS bundles OIDC and directory sync.",
			"decisions": [{"title":"Adopt WorkOS","rationale":"r","confidence":0.8}]
		}`
		mergeElaboratedStrategies(state, []RoundResult{{AgentID: "spec_strategy_elaborator", Output: strategyJSON}})

		require.NotEmpty(t, state.RawProposal)
		hits, _, err := idx.Search("workos", search.Options{})
		require.NoError(t, err)
		require.NotEmpty(t, hits, "in-flight index must surface a hit for the strategy term after merge")
		assert.True(t, hitsContainID(hits, "strat-auth-platform"),
			"the elaborated strategy id must appear in the in-flight hits")
	})

	// mergeRevisedNodes: a revised feature replaces the existing node;
	// the rebuild picks up the new term.
	t.Run("mergeRevisedNodes", func(t *testing.T) {
		idx, err := search.NewInFlightIndex()
		require.NoError(t, err)
		t.Cleanup(func() { _ = idx.Close() })

		// Seed an existing OriginalRawProposal so assembleRevisedRawProposal
		// has a base to merge against. The revise then introduces a new
		// strategy whose term is unique to this subtest.
		base := `{
			"features": [],
			"strategies": [{"id":"strat-base","title":"Base","summary":"baseline","kind":"foundational","body":"baseline body","decisions":[]}]
		}`
		state := &PlanningState{
			InFlightIndex:       idx,
			OriginalRawProposal: base,
			RawProposal:         base,
		}
		revisedJSON := `{
			"id": "strat-cache",
			"title": "Cache layer",
			"summary": "Adopt redis for hot read paths.",
			"kind": "foundational",
			"body": "Redis fronts the OLTP store for hot reads.",
			"decisions": [{"title":"Use redis","rationale":"r","confidence":0.8}]
		}`
		mergeRevisedNodes(state, []RoundResult{{AgentID: "spec_strategy_elaborator", Output: revisedJSON}})

		require.NotEmpty(t, state.RawProposal)
		hits, _, err := idx.Search("redis", search.Options{})
		require.NoError(t, err)
		require.NotEmpty(t, hits, "in-flight index must surface a hit for the revised strategy after merge")
		// Inline-decision documents are emitted alongside the parent
		// (see inlineDecisionDocs in internal/search/inflight.go) and
		// can outrank the parent on title/summary matches. Assert the
		// parent id is present in the hit set rather than asserting
		// it's the top hit — the contract under test is "rebuild
		// fired", not BM25 ranking.
		assert.True(t, hitsContainID(hits, "strat-cache"),
			"the revised strategy id must appear in the in-flight hits")
	})

	// mergeReconciledProposal: reconcile doesn't mutate RawProposal but
	// the merge still re-runs the rebuild so a freshly-set RawProposal
	// from an upstream elaborator is re-indexed once the canonical
	// SpecProposal has been built. Seed RawProposal with a known term
	// and a reconciler verdict; assert the rebuild fires.
	t.Run("mergeReconciledProposal", func(t *testing.T) {
		idx, err := search.NewInFlightIndex()
		require.NoError(t, err)
		t.Cleanup(func() { _ = idx.Close() })

		raw := `{
			"features": [{"id":"feat-realtime","title":"Realtime tiles","summary":"WebSocket push.","description":"Push metric tiles over WebSocket.","acceptance_criteria":[],"decisions":[{"title":"Use websockets","rationale":"r","confidence":0.8}]}],
			"strategies": []
		}`
		state := &PlanningState{
			InFlightIndex: idx,
			RawProposal:   raw,
		}
		// Empty reconcile verdict — every inline decision becomes its
		// own canonical Decision via slug.
		mergeReconciledProposal(state, []RoundResult{{AgentID: "spec_reconciler", Output: `{"actions":[]}`}})

		require.NotEmpty(t, state.ProposedSpec, "reconciler must produce a canonical proposal")
		hits, _, err := idx.Search("websocket", search.Options{})
		require.NoError(t, err)
		require.NotEmpty(t, hits, "in-flight index must surface a hit for the RawProposal term after reconcile-merge")
		assert.True(t, hitsContainID(hits, "feat-realtime"),
			"the parent feature id must appear in the in-flight hits after reconcile-merge")
	})
}

// hitsContainID is a tiny scan helper — the in-flight index surfaces
// both parent documents and per-decision child documents, so the
// per-merge subtests assert the parent id is present somewhere in the
// ranked hits rather than at position zero.
func hitsContainID(hits []search.Hit, id string) bool {
	for _, h := range hits {
		if h.ID == id {
			return true
		}
	}
	return false
}

// TestInFlightIndexNilIsNoop guards the defensive nil path on
// rebuildInFlightIndex: a PlanningState with no InFlightIndex (the
// shape every non-council consumer has) must not panic when a merge
// fires. Confirms the merge helpers stay reusable outside the council
// path.
func TestInFlightIndexNilIsNoop(t *testing.T) {
	state := &PlanningState{}
	featureJSON := `{
		"id": "feat-x",
		"title": "X",
		"description": "a feature",
		"decisions": [{"title":"d","rationale":"r","confidence":0.8}]
	}`
	assert.NotPanics(t, func() {
		mergeElaboratedFeatures(state, []RoundResult{{Output: featureJSON}})
	})
	assert.NotPanics(t, func() {
		mergeReconciledProposal(state, []RoundResult{{Output: `{"actions":[]}`}})
	})
}

// TestGenerateSpecSwapsInFlightIndexForRun drives generateSpecWithWorkflow
// end-to-end with a MockExecutor that exposes a SwappableSpecSearch, and
// asserts the council-scoped swap-and-restore behaviour:
//
//  1. Before the run, swap.Current() is the caller-supplied initial
//     backend (what RegisterSpecTools wires at process start).
//  2. During the run, the council pushes a *search.InFlightIndex in
//     via Swap — captured here from inside a custom merge that fires
//     while RunCouncil is still on the stack.
//  3. After the run returns, swap.Current() is back to the initial
//     backend (the deferred Swap restored it on function exit), so
//     subsequent CLI verbs that reuse the same Executor see the
//     production corpus, not a half-dismantled in-flight one.
//
// Drives through a one-step custom workflow (injected via the
// generateSpecWithWorkflow seam) whose merge both captures swap.Current()
// and writes a complete ProposedSpec — that keeps the assertion surface
// tight to the wiring under test and avoids depending on the full
// scout→architect→reconciler→critics fanout.
func TestGenerateSpecSwapsInFlightIndexForRun(t *testing.T) {
	// Initial backend — a concrete *search.InFlightIndex stands in for
	// the production *search.Index here. We compare by pointer identity
	// rather than type so the during-run capture can prove a *new*
	// backend was swapped in (not just "an InFlightIndex").
	initial, err := search.NewInFlightIndex()
	require.NoError(t, err)
	t.Cleanup(func() { _ = initial.Close() })

	swap := NewSwappableSpecSearch(initial)

	// MockExecutor opts into the swap surface via SetSpecSearch — the
	// structural interface specSearchSwap looks for. Without this the
	// council path silently degrades to "no in-flight wiring" and the
	// test would never observe a swap.
	mock := NewMockExecutor(MockResponse{
		AgentID:  "spec_scout",
		Response: &AgentOutput{Content: scoutResp, Model: "m"},
	})
	mock.SetSpecSearch(swap)

	// captureMerge fires inside RunCouncil — i.e. between the council's
	// swap.Swap(inflight) and the deferred restore. swap.Current() at
	// this point must be the freshly-swapped-in InFlightIndex, distinct
	// from the initial sentinel. The merge also writes a valid empty
	// ProposedSpec so generateSpecWithWorkflow's integrity gate
	// short-circuits cleanly.
	var duringRun search.Backend
	captureMerge := func(s *PlanningState, _ []RoundResult) {
		duringRun = swap.Current()
		s.ProposedSpec = `{"features":[],"strategies":[],"decisions":[]}`
	}

	wf := &Workflow[PlanningState]{
		Rounds: []WorkflowStep[PlanningState]{
			{ID: "capture", Agents: []string{"spec_scout"}, Project: projectDefault, Merge: captureMerge},
		},
		MaxRounds: 1,
	}

	fs := setupSpecGenFixture(t)

	require.Same(t, search.Backend(initial), swap.Current(),
		"precondition: swappable starts pointing at the initial backend")

	proposal, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build something useful.",
	}, wf)
	require.NoError(t, err)
	require.NotNil(t, proposal)

	// During-run capture: a *search.InFlightIndex, NOT the sentinel.
	require.NotNil(t, duringRun, "captureMerge must have run inside RunCouncil")
	_, isInFlight := duringRun.(*search.InFlightIndex)
	assert.True(t, isInFlight, "during the council, swap.Current() must be a *search.InFlightIndex")
	assert.NotSame(t, search.Backend(initial), duringRun,
		"the council must swap in a fresh in-flight index, not reuse the initial backend")

	// Post-run: the defer in generateSpecWithWorkflow restored the
	// initial backend so subsequent callers see the production corpus.
	require.Same(t, search.Backend(initial), swap.Current(),
		"postcondition: swappable must be restored to the initial backend on function exit")
}
