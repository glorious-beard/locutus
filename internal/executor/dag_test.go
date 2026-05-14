package executor_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/executor"

	dgraph "github.com/dominikbraun/graph"
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

// --- Per-type concurrency limit tests ---

func TestPerTypeConcurrencyLimit(t *testing.T) {
	// 5 parallel steps: 3 claude-code, 2 codex.
	// TypeLimits: claude-code=1, codex=2. MaxConcurrency=5 (effectively unlimited).
	// Peak concurrent claude-code steps must never exceed 1.

	var ccRunning atomic.Int32
	var ccMaxSeen atomic.Int32
	var cxRunning atomic.Int32
	var cxMaxSeen atomic.Int32

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "cc1", Parallel: true, Type: "claude-code"},
			{ID: "cc2", Parallel: true, Type: "claude-code"},
			{ID: "cc3", Parallel: true, Type: "claude-code"},
			{ID: "cx1", Parallel: true, Type: "codex"},
			{ID: "cx2", Parallel: true, Type: "codex"},
		},
		RunStep: func(_ context.Context, step executor.Step, _ testState) (executor.StepResult, error) {
			switch step.Type {
			case "claude-code":
				cur := ccRunning.Add(1)
				for {
					old := ccMaxSeen.Load()
					if cur <= old || ccMaxSeen.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				ccRunning.Add(-1)
			case "codex":
				cur := cxRunning.Add(1)
				for {
					old := cxMaxSeen.Load()
					if cur <= old || cxMaxSeen.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				cxRunning.Add(-1)
			}
			return executor.StepResult{Output: step.ID + " done"}, nil
		},
		Merge:          merge,
		Snapshot:       snapshot,
		MaxConcurrency: 5,
		TypeLimits:     map[string]int{"claude-code": 1, "codex": 2},
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 5, "all 5 steps should complete")
	assert.LessOrEqual(t, int(ccMaxSeen.Load()), 1, "at most 1 claude-code step should run concurrently")
	assert.LessOrEqual(t, int(cxMaxSeen.Load()), 2, "at most 2 codex steps should run concurrently")
}

func TestPerTypeLimitCombinedWithGlobalLimit(t *testing.T) {
	// 4 parallel steps all Type="claude-code".
	// TypeLimits: claude-code=3. MaxConcurrency=2.
	// Global limit is stricter, so peak concurrency should be 2.

	var running atomic.Int32
	var maxSeen atomic.Int32

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A", Parallel: true, Type: "claude-code"},
			{ID: "B", Parallel: true, Type: "claude-code"},
			{ID: "C", Parallel: true, Type: "claude-code"},
			{ID: "D", Parallel: true, Type: "claude-code"},
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
			return executor.StepResult{Output: step.ID + " done"}, nil
		},
		Merge:          merge,
		Snapshot:       snapshot,
		MaxConcurrency: 2,
		TypeLimits:     map[string]int{"claude-code": 3},
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 4, "all 4 steps should complete")
	assert.LessOrEqual(t, int(maxSeen.Load()), 2, "global limit should cap concurrency at 2")
}

func TestRunRejectsCycle(t *testing.T) {
	// Cycle: A → B → C → A. The original implementation surfaced this
	// mid-run as a generic "deadlock" error after attempting wave
	// scheduling. The refactor catches it before any step runs, via
	// dominikbraun/graph's PreventCycles + TopologicalSort.
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A", DependsOn: []string{"C"}},
			{ID: "B", DependsOn: []string{"A"}},
			{ID: "C", DependsOn: []string{"B"}},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.Error(t, err, "cycles must be rejected before any step runs")
	assert.Contains(t, err.Error(), "cycle",
		"error should name the failure as a cycle, not a generic deadlock")
	assert.Empty(t, results, "no step output should be produced when the graph is invalid")
	assert.Empty(t, state.Log, "no step should run when the graph is invalid")
}

func TestRunRejectsDuplicateStepIDs(t *testing.T) {
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{ID: "A"},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	_, err := executor.NewExecutor(cfg).Run(context.Background(), &testState{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate step id")
}

func TestRunRejectsUndeclaredDependency(t *testing.T) {
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A", DependsOn: []string{"missing"}},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	_, err := executor.NewExecutor(cfg).Run(context.Background(), &testState{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `depends on undeclared step "missing"`)
}

func TestStepTypeFieldOptional(t *testing.T) {
	// Steps without a Type field should work normally and not be affected by TypeLimits.
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A", Parallel: true},
			{ID: "B", Parallel: true},
			{ID: "C", Parallel: true},
		},
		RunStep:    runStep,
		Merge:      merge,
		Snapshot:   snapshot,
		TypeLimits: map[string]int{"claude-code": 1},
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 3, "all steps should complete even with TypeLimits set")
	assert.ElementsMatch(t,
		[]string{"A done", "B done", "C done"},
		state.Log,
		"all step outputs should be merged",
	)
}

// --- DJ-122 Phase 1: Spawn capability ---

// TestSpawnAppendsStepsAndEdges verifies that a step's Spawn callback can add
// a new vertex and an explicit edge after the step runs, and that the new
// step then executes in dependency order.
func TestSpawnAppendsStepsAndEdges(t *testing.T) {
	spawnFired := 0
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{
				ID: "A",
				Spawn: func(_ context.Context, _ any, _ any) ([]executor.Step, []executor.Edge, error) {
					spawnFired++
					return []executor.Step{{ID: "B"}}, []executor.Edge{{From: "A", To: "B"}}, nil
				},
			},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Equal(t, 1, spawnFired, "spawn should fire exactly once (after A completes)")
	assert.Len(t, results, 2, "both A and the spawned B should execute")
	assert.Equal(t, "A", results[0].StepID, "A runs first")
	assert.Equal(t, "B", results[1].StepID, "B runs after A via the spawned edge")
	assert.Equal(t, []string{"A done", "B done"}, state.Log)
}

// TestSpawnedStepCanCarryDependsOn verifies that DependsOn on spawned steps
// is honored exactly like initial-graph deps — so the agent-layer template
// helper can express internal subgraph edges without needing the explicit
// Edge list for every connection.
func TestSpawnedStepCanCarryDependsOn(t *testing.T) {
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{
				ID: "root",
				Spawn: func(_ context.Context, _ any, _ any) ([]executor.Step, []executor.Edge, error) {
					return []executor.Step{
							{ID: "child1", DependsOn: []string{"root"}},
							{ID: "child2", DependsOn: []string{"child1"}},
						},
						nil, nil
				},
			},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.NoError(t, err)
	assert.Len(t, results, 3)
	assert.Equal(t, "root", results[0].StepID)
	assert.Equal(t, "child1", results[1].StepID)
	assert.Equal(t, "child2", results[2].StepID)
}

// TestSpawnRespectsGraphSizeCap verifies that a runaway spawner that keeps
// appending nodes hits MaxGraphMultiplier and errors with ErrGraphSizeExceeded
// naming the spawner id.
func TestSpawnRespectsGraphSizeCap(t *testing.T) {
	// Recursive spawner: every spawned step has the same spawner attached,
	// so each step appends one more step. With 1 initial step and
	// multiplier 3, the cap is 3 total nodes; the spawner must fail
	// before exceeding that.
	var recursiveSpawner func(ctx context.Context, snap any, out any) ([]executor.Step, []executor.Edge, error)
	counter := 0
	recursiveSpawner = func(_ context.Context, _ any, _ any) ([]executor.Step, []executor.Edge, error) {
		counter++
		id := fmt.Sprintf("gen-%d", counter)
		return []executor.Step{{ID: id, Spawn: recursiveSpawner}}, nil, nil
	}

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "root", Spawn: recursiveSpawner},
		},
		RunStep:           runStep,
		Merge:             merge,
		Snapshot:          snapshot,
		MaxGraphMultiplier: 3, // cap = 3 * 1 initial = 3 nodes total
	}

	state := &testState{}
	_, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, executor.ErrGraphSizeExceeded),
		"error should wrap ErrGraphSizeExceeded, got: %v", err)
	// The most-recently-spawning step must be named in the error so users
	// can find the runaway spawner. After 2 successful spawns (gen-1, gen-2)
	// the graph holds 3 nodes; the next spawner attempt from gen-2 trips
	// the cap.
	assert.Contains(t, err.Error(), "gen-2",
		"error should name the spawner that tripped the cap")
}

// TestSpawnRejectsCycle verifies that a spawner whose newEdges would
// introduce a cycle is rejected via dgraph.ErrEdgeCreatesCycle, wrapped
// with the spawner id.
func TestSpawnRejectsCycle(t *testing.T) {
	// Initial graph: A → B (B depends on A).
	// B's spawner tries to add an edge B → A, closing the cycle.
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{ID: "A"},
			{
				ID:        "B",
				DependsOn: []string{"A"},
				Spawn: func(_ context.Context, _ any, _ any) ([]executor.Step, []executor.Edge, error) {
					return nil, []executor.Edge{{From: "B", To: "A"}}, nil
				},
			},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	_, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, dgraph.ErrEdgeCreatesCycle),
		"error should wrap dgraph.ErrEdgeCreatesCycle, got: %v", err)
	assert.Contains(t, err.Error(), `"B"`,
		"error should name the spawner that tried to introduce the cycle")
}

// TestSpawnSeesOwnOutput verifies that the spawner is invoked with a
// snapshot taken AFTER the producing step's result is merged, so the
// spawner can branch on its own output.
func TestSpawnSeesOwnOutput(t *testing.T) {
	var sawLogLen int
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{
				ID: "A",
				Spawn: func(_ context.Context, snap any, out any) ([]executor.Step, []executor.Edge, error) {
					s := snap.(testState)
					sawLogLen = len(s.Log)
					assert.Equal(t, "A done", out, "spawn output arg should be the step's StepResult.Output")
					return nil, nil, nil
				},
			},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	_, err := executor.NewExecutor(cfg).Run(context.Background(), state)
	assert.NoError(t, err)
	assert.Equal(t, 1, sawLogLen, "spawner should see post-merge state including its own output")
}

// TestSpawnDoesNotFireOnSkip verifies that conditional-skipped steps do not
// invoke their Spawn callback.
func TestSpawnDoesNotFireOnSkip(t *testing.T) {
	fired := false
	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{
				ID:          "A",
				Conditional: func(_ any) bool { return false },
				Spawn: func(_ context.Context, _ any, _ any) ([]executor.Step, []executor.Edge, error) {
					fired = true
					return nil, nil, nil
				},
			},
		},
		RunStep:  runStep,
		Merge:    merge,
		Snapshot: snapshot,
	}

	state := &testState{}
	_, err := executor.NewExecutor(cfg).Run(context.Background(), state)
	assert.NoError(t, err)
	assert.False(t, fired, "spawn must not fire when the producing step is skipped")
}

// --- DJ-122 Phase 2: iteration metadata + AppendSubgraph ---

// convergenceLoopTemplate is the test fixture for the Phase 2 loop tests.
// Each iteration adds a "work" + "gate" pair. The gate captures the
// current iteration in its closure; its Spawn calls AppendSubgraph to
// produce the next iteration unless terminateAt has been reached.
// terminateAt < 0 means never terminate.
func convergenceLoopTemplate(terminateAt int) func(executor.IterationContext) []executor.Step {
	var template func(executor.IterationContext) []executor.Step
	template = func(ic executor.IterationContext) []executor.Step {
		currentIter := ic.IterationIndex
		return []executor.Step{
			{ID: "work"},
			{
				ID:        "gate",
				DependsOn: []string{"work"},
				Spawn: func(_ context.Context, _ any, _ any) ([]executor.Step, []executor.Edge, error) {
					if terminateAt >= 0 && currentIter >= terminateAt {
						return nil, nil, nil
					}
					gateID := fmt.Sprintf("loop#iter:%d:gate", currentIter)
					steps, edges := executor.AppendSubgraph(template, executor.IterationContext{
						TemplateID:     "loop",
						IterationIndex: currentIter + 1,
						ParentNodeID:   gateID,
					})
					return steps, edges, nil
				},
			},
		}
	}
	return template
}

func seedLoopStep(template func(executor.IterationContext) []executor.Step) executor.Step {
	return executor.Step{
		ID: "seed",
		Spawn: func(_ context.Context, _ any, _ any) ([]executor.Step, []executor.Edge, error) {
			steps, edges := executor.AppendSubgraph(template, executor.IterationContext{
				TemplateID:     "loop",
				IterationIndex: 0,
				ParentNodeID:   "seed",
			})
			return steps, edges, nil
		},
	}
}

func TestConvergenceLoopRunsToCompletion(t *testing.T) {
	// Terminate at iteration 2 → iterations 0, 1, 2 run.
	template := convergenceLoopTemplate(2)

	cfg := executor.Config[testState]{
		Steps:              []executor.Step{seedLoopStep(template)},
		RunStep:            runStep,
		Merge:              merge,
		Snapshot:           snapshot,
		MaxGraphMultiplier: 50,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)
	assert.NoError(t, err)
	// seed + 3 iterations × 2 nodes per iter = 7 results.
	assert.Len(t, results, 7)

	seenIDs := make(map[string]bool, len(results))
	for _, r := range results {
		seenIDs[r.StepID] = true
	}
	assert.True(t, seenIDs["loop#iter:0:work"])
	assert.True(t, seenIDs["loop#iter:0:gate"])
	assert.True(t, seenIDs["loop#iter:1:work"])
	assert.True(t, seenIDs["loop#iter:1:gate"])
	assert.True(t, seenIDs["loop#iter:2:work"])
	assert.True(t, seenIDs["loop#iter:2:gate"])
	assert.False(t, seenIDs["loop#iter:3:work"], "loop must terminate at iter 2")
}

func TestConvergenceLoopHitsBudget(t *testing.T) {
	// terminateAt = -1 → never converges. The graph-size cap must stop
	// the runaway loop and surface the spawner that tripped it.
	template := convergenceLoopTemplate(-1)

	cfg := executor.Config[testState]{
		Steps:              []executor.Step{seedLoopStep(template)},
		RunStep:            runStep,
		Merge:              merge,
		Snapshot:           snapshot,
		MaxGraphMultiplier: 5, // cap = 5 total nodes; loop blows past this fast
	}

	state := &testState{}
	_, err := executor.NewExecutor(cfg).Run(context.Background(), state)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, executor.ErrGraphSizeExceeded),
		"runaway convergence loop must hit ErrGraphSizeExceeded, got: %v", err)
	// The spawner that tripped the cap should be named — a gate from
	// some specific iteration (the seed already fired earlier).
	assert.Contains(t, err.Error(), "gate",
		"the gate that tripped the cap should be named in the error")
}

func TestIterationMetadataInResults(t *testing.T) {
	template := convergenceLoopTemplate(1) // iterations 0 and 1

	cfg := executor.Config[testState]{
		Steps:              []executor.Step{seedLoopStep(template)},
		RunStep:            runStep,
		Merge:              merge,
		Snapshot:           snapshot,
		MaxGraphMultiplier: 50,
	}

	state := &testState{}
	results, err := executor.NewExecutor(cfg).Run(context.Background(), state)
	assert.NoError(t, err)

	byID := make(map[string]executor.StepResult, len(results))
	for _, r := range results {
		byID[r.StepID] = r
	}

	assert.Equal(t, "", byID["seed"].TemplateID, "initial-graph step has no template id")
	assert.Equal(t, 0, byID["seed"].IterationIndex, "initial-graph step has zero iteration index")

	for _, base := range []string{"work", "gate"} {
		for iter := 0; iter <= 1; iter++ {
			id := fmt.Sprintf("loop#iter:%d:%s", iter, base)
			r, ok := byID[id]
			assert.True(t, ok, "expected result for %s", id)
			assert.Equal(t, "loop", r.TemplateID, "%s template id", id)
			assert.Equal(t, iter, r.IterationIndex, "%s iteration index", id)
		}
	}
}

// TestAppendSubgraphRewritesInternalDeps verifies the helper rewrites
// DependsOn entries that reference sibling base ids, while leaving
// external references untouched, and produces parent edges only to
// "template-root" steps (those with no sibling dep).
func TestAppendSubgraphRewritesInternalDeps(t *testing.T) {
	template := func(_ executor.IterationContext) []executor.Step {
		return []executor.Step{
			{ID: "a"},                                       // root (no deps)
			{ID: "b", DependsOn: []string{"a"}},             // sibling dep — should rewrite, not a root
			{ID: "c", DependsOn: []string{"a", "external"}}, // mixed — only "a" rewrites, not a root
			{ID: "d", DependsOn: []string{"external"}},      // external only — still a root
		}
	}

	steps, edges := executor.AppendSubgraph(template, executor.IterationContext{
		TemplateID:     "tpl",
		IterationIndex: 0,
		ParentNodeID:   "parent",
	})

	assert.Len(t, steps, 4)

	depsByID := make(map[string][]string, len(steps))
	for _, s := range steps {
		depsByID[s.ID] = s.DependsOn
	}

	assert.Nil(t, depsByID["tpl#iter:0:a"], "a has no deps")
	assert.Equal(t, []string{"tpl#iter:0:a"}, depsByID["tpl#iter:0:b"], "b's sibling dep rewritten")
	assert.Equal(t, []string{"tpl#iter:0:a", "external"}, depsByID["tpl#iter:0:c"], "c rewrites only the sibling")
	assert.Equal(t, []string{"external"}, depsByID["tpl#iter:0:d"], "d's external dep untouched")

	// Roots: "a" (no deps) and "d" (only external dep). "b" and "c"
	// have sibling deps so they're NOT roots.
	seen := map[string]bool{}
	for _, e := range edges {
		seen[e.From+"->"+e.To] = true
	}
	assert.Equal(t, map[string]bool{
		"parent->tpl#iter:0:a": true,
		"parent->tpl#iter:0:d": true,
	}, seen, "parent edges only to roots (a and d)")
}

// --- DJ-122 Phase 3: graph_mutated event ---

// TestGraphMutatedEventEmitted verifies that successful Spawn appends
// fire a "graph_mutated" event with SpawnerID + appended/total counts.
// Step-completed events for the spawner and its spawned steps still
// flow normally.
func TestGraphMutatedEventEmitted(t *testing.T) {
	events := make(chan executor.Event, 20)

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{
				ID: "A",
				Spawn: func(_ context.Context, _ any, _ any) ([]executor.Step, []executor.Edge, error) {
					// Append two new steps: B (root) and C (depends on B).
					return []executor.Step{
							{ID: "B"},
							{ID: "C", DependsOn: []string{"B"}},
						},
						[]executor.Edge{{From: "A", To: "B"}}, nil
				},
			},
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
	var mutations []executor.Event
	for e := range events {
		if e.Status == "graph_mutated" {
			mutations = append(mutations, e)
		}
	}
	assert.Len(t, mutations, 1, "exactly one graph_mutated event expected")
	if len(mutations) == 1 {
		assert.NotNil(t, mutations[0].Mutation, "mutation payload populated")
		assert.Equal(t, "A", mutations[0].StepID, "event StepID echoes SpawnerID")
		assert.Equal(t, "A", mutations[0].Mutation.SpawnerID)
		assert.Equal(t, 2, mutations[0].Mutation.AppendedNodeCount, "B and C appended")
		assert.Equal(t, 3, mutations[0].Mutation.TotalNodeCount, "A + B + C after append")
	}
}

// TestGraphMutatedEventSuppressedOnEmptySpawn verifies that a spawner
// returning no new steps (regardless of whether it returns edges-only)
// does NOT fire a graph_mutated event — the node count didn't move.
func TestGraphMutatedEventSuppressedOnEmptySpawn(t *testing.T) {
	events := make(chan executor.Event, 20)

	cfg := executor.Config[testState]{
		Steps: []executor.Step{
			{
				ID: "A",
				Spawn: func(_ context.Context, _ any, _ any) ([]executor.Step, []executor.Edge, error) {
					return nil, nil, nil
				},
			},
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
	for e := range events {
		assert.NotEqual(t, "graph_mutated", e.Status, "empty spawn must not emit graph_mutated")
	}
}

// TestAppendSubgraphEmptyParent verifies that a zero-value
// ParentNodeID suppresses the auto-edges — useful for stand-alone
// template expansion.
func TestAppendSubgraphEmptyParent(t *testing.T) {
	template := func(_ executor.IterationContext) []executor.Step {
		return []executor.Step{{ID: "x"}}
	}
	steps, edges := executor.AppendSubgraph(template, executor.IterationContext{
		TemplateID:     "tpl",
		IterationIndex: 0,
		// ParentNodeID empty.
	})
	assert.Len(t, steps, 1)
	assert.Empty(t, edges, "no parent edges when ParentNodeID is empty")
}
