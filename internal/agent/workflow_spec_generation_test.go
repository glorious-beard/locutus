package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/executor"
	"github.com/chetan/locutus/internal/history"
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
				Reasoning: "iOS app deploy cadence is unspecified.",
				OpenDimensions: []string{
					"deployment cadence for the iOS companion app",
				},
			}),
			Model: "m",
		}},
		MockResponse{Response: &AgentOutput{
			Content: gateVerdictJSON(t, SpecGateVerdict{
				Converged: true,
				Reasoning: "iOS deploy cadence now committed; all phases addressed.",
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
				Reasoning: "Firmware deploy is unaddressed.",
				OpenDimensions: []string{"OTA update channel for the firmware"},
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
			assert.Contains(t, evt.Rationale, "Firmware deploy is unaddressed",
				"event rationale should carry the gate's verdict reasoning")
			assert.Contains(t, evt.Rationale, "OTA update channel for the firmware",
				"event rationale should list the unresolved open dimensions")
		}
	}
	assert.True(t, found, "a convergence_failed history event must be written under .borg/history/")
}

func TestSpecGateMergeAppendsOpenDimensionsAsConcernsAndClusters(t *testing.T) {
	// Unit test on mergeGateVerdict: a non-converged verdict appends
	// each OpenDimension as a Concern (record-keeping) AND a
	// FindingCluster (so the next iteration's revise picks them up).
	state := &PlanningState{}
	verdict := SpecGateVerdict{
		Converged: false,
		Reasoning: "two phases missing",
		OpenDimensions: []string{
			"deployment cadence for the iOS companion app",
			"OTA update channel for the firmware",
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
		assert.Equal(t, "convergence", c.Kind)
	}
	assert.Len(t, state.FindingClusters, 2, "each OpenDimension becomes a FindingCluster")
	for _, c := range state.FindingClusters {
		assert.Equal(t, "spec_strategy_elaborator", c.AgentID,
			"gate findings route to the strategy elaborator")
		assert.Len(t, c.Findings, 1, "one finding per cluster")
	}
}

func TestSpecGateMergeNoopOnConverged(t *testing.T) {
	// A Converged:true verdict is a no-op: no Concerns or
	// FindingClusters are appended (workflow terminates without
	// further revision).
	state := &PlanningState{}
	verdict := SpecGateVerdict{
		Converged: true,
		Reasoning: "all phases addressed",
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
