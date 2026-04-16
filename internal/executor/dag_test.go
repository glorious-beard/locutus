package executor_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/executor"

	"github.com/stretchr/testify/assert"
)

// testState is a simple accumulator for testing.
type testState struct {
	Log []string
}

func snapshot(s *testState) testState {
	cp := testState{Log: make([]string, len(s.Log))}
	copy(cp.Log, s.Log)
	return cp
}

func merge(s *testState, r executor.StepResult) {
	if msg, ok := r.Output.(string); ok {
		s.Log = append(s.Log, msg)
	}
}

func runStep(_ context.Context, step executor.Step, _ testState) (executor.StepResult, error) {
	return executor.StepResult{Output: step.ID + " done"}, nil
}

// --- Sequencing tests ---

func TestSequentialExecution(t *testing.T) {
	// A → B → C, all sequential. Must execute in order.
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}},
			{ID: "C", DependsOn: []string{"B"}},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 3)
	assert.Equal(t, "A", results[0].StepID)
	assert.Equal(t, "B", results[1].StepID)
	assert.Equal(t, "C", results[2].StepID)
	assert.Equal(t, []string{"A done", "B done", "C done"}, state.Log)
}

func TestDiamondDependency(t *testing.T) {
	//     A
	//    / \
	//   B   C  (parallel)
	//    \ /
	//     D
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}, Parallel: true},
			{ID: "C", DependsOn: []string{"A"}, Parallel: true},
			{ID: "D", DependsOn: []string{"B", "C"}},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 4)

	// A must be first, D must be last.
	assert.Equal(t, "A", results[0].StepID)
	assert.Equal(t, "D", results[3].StepID)

	// B and C can be in either order (parallel).
	middle := []string{results[1].StepID, results[2].StepID}
	assert.ElementsMatch(t, []string{"B", "C"}, middle)
}

func TestParallelStepsGetSameSnapshot(t *testing.T) {
	// B and C run in parallel after A. Both should see A's state but not each other's.
	var bSaw, cSaw int

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}, Parallel: true},
			{ID: "C", DependsOn: []string{"A"}, Parallel: true},
		},
		RunStep: func(_ context.Context, step executor.Step, snap testState) (executor.StepResult, error) {
			switch step.ID {
			case "B":
				bSaw = len(snap.Log)
			case "C":
				cSaw = len(snap.Log)
			}
			return executor.StepResult{Output: step.ID + " done"}, nil
		},
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	_, err := executor.NewExecutor(cfg).Run(context.Background(), state)
	assert.NoError(t, err)

	// Both B and C should have seen exactly 1 entry (from A), not 2.
	assert.Equal(t, 1, bSaw, "B should see A's output only")
	assert.Equal(t, 1, cSaw, "C should see A's output only")
}

// --- Bounded concurrency tests ---

func TestMaxConcurrencyRespected(t *testing.T) {
	// 5 parallel steps, MaxConcurrency=2. At most 2 should run at once.
	var running atomic.Int32
	var maxSeen atomic.Int32

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A", Parallel: true},
			{ID: "B", Parallel: true},
			{ID: "C", Parallel: true},
			{ID: "D", Parallel: true},
			{ID: "E", Parallel: true},
		},
		RunStep: func(_ context.Context, step executor.Step, _ testState) (executor.StepResult, error) {
			cur := running.Add(1)
			// Track peak concurrency.
			for {
				old := maxSeen.Load()
				if cur <= old || maxSeen.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond) // simulate work
			running.Add(-1)
			return executor.StepResult{Output: step.ID}, nil
		},
		Merge:          merge,
		Snapshot:       snapshot,
		MaxConcurrency: 2,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 5)
	assert.LessOrEqual(t, int(maxSeen.Load()), 2, "at most 2 steps should run concurrently")
}

func TestUnlimitedConcurrency(t *testing.T) {
	// 4 parallel steps, no concurrency limit. All should start ~simultaneously.
	var running atomic.Int32
	var maxSeen atomic.Int32

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A", Parallel: true},
			{ID: "B", Parallel: true},
			{ID: "C", Parallel: true},
			{ID: "D", Parallel: true},
		},
		RunStep: func(_ context.Context, step executor.Step, _ testState) (executor.StepResult, error) {
			cur := running.Add(1)
			for {
				old := maxSeen.Load()
				if cur <= old || maxSeen.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			running.Add(-1)
			return executor.StepResult{Output: step.ID}, nil
		},
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 4)
	assert.GreaterOrEqual(t, int(maxSeen.Load()), 3, "with no limit, most steps should run concurrently")
}

// --- Conditional steps ---

func TestConditionalStepSkipped(t *testing.T) {
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}, Conditional: func(state any) bool {
				s := state.(*testState)
				// Only run if log contains "trigger".
				for _, l := range s.Log {
					if l == "trigger" {
						return true
					}
				}
				return false
			}},
			{ID: "C", DependsOn: []string{"B"}},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	// B is skipped, so only A and C produce results.
	assert.Len(t, results, 2)
	assert.Equal(t, "A", results[0].StepID)
	assert.Equal(t, "C", results[1].StepID)
}

func TestConditionalStepFires(t *testing.T) {
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}, Conditional: func(state any) bool {
				s := state.(*testState)
				for _, l := range s.Log {
					if l == "A done" {
						return true
					}
				}
				return false
			}},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, "B", results[1].StepID)
}

// --- Convergence loop ---

func TestConvergenceLoop(t *testing.T) {
	iteration := 0

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "work"},
		},
		RunStep: func(_ context.Context, step executor.Step, _ testState) (executor.StepResult, error) {
			return executor.StepResult{Output: fmt.Sprintf("iteration %d", iteration)}, nil
		},
		Merge:    merge,
		Snapshot: snapshot,
		Converged: func(_ context.Context, state *testState, iter int) (bool, error) {
			iteration = iter + 1
			return iter >= 2, nil // converge after 3 iterations (0, 1, 2)
		},
		MaxIterations: 10,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 3) // 3 iterations
	assert.Len(t, state.Log, 3)
}

func TestConvergenceMaxIterationsCap(t *testing.T) {
	// Never converges, but max iterations = 3.
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "work"},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
		Converged: func(_ context.Context, _ *testState, _ int) (bool, error) {
			return false, nil // never converges
		},
		MaxIterations: 3,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 3, "should cap at MaxIterations")
}

func TestNoConvergenceFuncRunsOnce(t *testing.T) {
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
		// No Converged func — should run exactly once.
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 2)
}

// --- Error handling ---

func TestStepErrorStopsExecution(t *testing.T) {
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}},
			{ID: "C", DependsOn: []string{"B"}},
		},
		RunStep: func(_ context.Context, step executor.Step, _ testState) (executor.StepResult, error) {
			if step.ID == "B" {
				return executor.StepResult{}, fmt.Errorf("step B failed")
			}
			return executor.StepResult{Output: step.ID + " done"}, nil
		},
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "step B failed")
	// A completed, B failed, C never ran.
	assert.Len(t, results, 2)
	assert.Equal(t, "A", results[0].StepID)
	assert.Equal(t, "B", results[1].StepID)
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}},
		},
		RunStep: func(ctx context.Context, step executor.Step, _ testState) (executor.StepResult, error) {
			if step.ID == "A" {
				cancel() // cancel after first step
			}
			if ctx.Err() != nil {
				return executor.StepResult{}, ctx.Err()
			}
			return executor.StepResult{Output: step.ID + " done"}, nil
		},
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	_, err := executor.NewExecutor(cfg).Run(ctx, state)

	assert.Error(t, err)
}

// --- Progress events ---

func TestProgressEvents(t *testing.T) {
	events := make(chan executor.Event, 20)

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
		Events:   events,
	}

	state := &testState{}
	_, err := executor.NewExecutor(cfg).Run(context.Background(), state)
	assert.NoError(t, err)

	close(events)
	var evts []executor.Event
	for e := range events {
		evts = append(evts, e)
	}

	// Each step should have started + completed = 4 events total.
	assert.Len(t, evts, 4)

	statuses := map[string][]string{}
	for _, e := range evts {
		statuses[e.StepID] = append(statuses[e.StepID], e.Status)
	}
	assert.Contains(t, statuses["A"], "started")
	assert.Contains(t, statuses["A"], "completed")
	assert.Contains(t, statuses["B"], "started")
	assert.Contains(t, statuses["B"], "completed")
}

// --- Edge cases ---

func TestEmptyDAG(t *testing.T) {
	cfg := executor.Config[testState]{
		Steps:    []executor.Step{},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Empty(t, results)
}

func TestSingleStep(t *testing.T) {
	cfg := executor.Config[testState]{
		Steps:    []executor.Step{{ID: "only"}},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "only", results[0].StepID)
}

func TestMergeCalledInDependencyOrder(t *testing.T) {
	// Verify that merge is called in the correct order even with parallel steps.
	var mergeOrder []string

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "B", DependsOn: []string{"A"}, Parallel: true},
			{ID: "C", DependsOn: []string{"A"}, Parallel: true},
			{ID: "D", DependsOn: []string{"B", "C"}},
		},
		RunStep: runStep,
		Merge: func(s *testState, r executor.StepResult) {
			mergeOrder = append(mergeOrder, r.StepID)
			merge(s, r)
		},
		Snapshot: snapshot,
	}

	state := &testState{}
	_, err := executor.NewExecutor(cfg).Run(context.Background(), state)
	assert.NoError(t, err)

	// A must be first, D must be last. B and C can be in either order.
	assert.Equal(t, "A", mergeOrder[0])
	assert.Equal(t, "D", mergeOrder[3])
	assert.ElementsMatch(t, []string{"B", "C"}, mergeOrder[1:3])
}
