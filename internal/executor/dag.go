// Package executor provides a generic, reusable step executor with typed shared
// state, bounded parallelism, snapshot isolation, convergence loops, and progress
// events.
//
// The executor is parameterized by a State type. Callers provide:
//   - Steps with declared dependencies
//   - A RunStep function that executes a single step against a state snapshot
//   - A Merge function that applies step results back to the state
//   - An optional Snapshot function for safe concurrent reads
//   - An optional Converged function for iterative convergence loops
//   - An optional MaxConcurrency limit for resource-constrained environments
//
// The DAG itself is held in github.com/dominikbraun/graph (the same library
// the spec graph uses, per DJ-084) — cycle detection and predecessor lookups
// come from there. The runtime semantics (RunStep callbacks, snapshot
// isolation, parallelism limits, convergence loops, conditional steps) live
// in this package.
package executor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	dgraph "github.com/dominikbraun/graph"
)

// Step defines a unit of work in the DAG.
type Step struct {
	ID          string
	DependsOn   []string             // IDs of steps that must complete before this one
	Parallel    bool                 // if true, can run concurrently with other ready steps
	Type        string               // optional; used with Config.TypeLimits for per-type concurrency
	Conditional func(state any) bool // optional; if non-nil and returns false, step is skipped
}

// StepResult is the output of executing a single step.
type StepResult struct {
	StepID string
	Output any // caller-defined payload
	Err    error
}

// Event reports progress during DAG execution.
type Event struct {
	StepID    string    `json:"step_id"`
	Status    string    `json:"status"` // "started", "completed", "skipped", "error", "waiting"
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Config configures the DAG executor.
type Config[S any] struct {
	// Steps defines the DAG. Order in the slice does not matter; dependencies
	// determine execution order.
	Steps []Step

	// RunStep executes a single step. It receives a snapshot of the current state
	// (safe for concurrent reads). Must not mutate the snapshot.
	RunStep func(ctx context.Context, step Step, snapshot S) (StepResult, error)

	// Merge applies a step's result back into the mutable state. Called
	// sequentially by the executor — never concurrently.
	Merge func(state *S, result StepResult)

	// Snapshot creates a read-only copy of the state for concurrent step execution.
	// If nil, the state is passed directly (caller guarantees no concurrent mutation).
	Snapshot func(state *S) S

	// Converged is called after each full pass through the DAG. If it returns true,
	// the executor stops iterating. If nil, the DAG executes exactly once.
	Converged func(ctx context.Context, state *S, iteration int) (bool, error)

	// MaxIterations caps the number of convergence loop iterations. Ignored if
	// Converged is nil. Defaults to 1 if zero.
	MaxIterations int

	// MaxConcurrency limits how many parallel steps run simultaneously.
	// 0 or negative means unlimited.
	MaxConcurrency int

	// TypeLimits constrains per-type concurrency. If a step has a Type that
	// appears in this map, at most TypeLimits[type] steps of that type run
	// concurrently. Steps without a Type are unaffected. Both TypeLimits and
	// MaxConcurrency are enforced simultaneously.
	TypeLimits map[string]int

	// Events receives progress notifications. Nil disables events.
	Events chan Event
}

// Executor runs a DAG of steps with typed shared state.
type Executor[S any] struct {
	cfg Config[S]
}

// NewExecutor creates a DAG executor with the given configuration.
func NewExecutor[S any](cfg Config[S]) *Executor[S] {
	return &Executor[S]{cfg: cfg}
}

// Run executes the DAG, optionally looping until convergence. Returns all
// step results from all iterations.
//
// Before any step runs, Run validates the DAG: it builds the graph,
// rejects duplicate step IDs and edges to undeclared steps, and runs
// dgraph.TopologicalSort to surface cycles up-front. Previously a cycle
// manifested mid-run as a generic "deadlock" error; now it fails fast
// with the cycle's vertices named.
func (e *Executor[S]) Run(ctx context.Context, state *S) ([]StepResult, error) {
	graph, stepMap, err := buildStepGraph(e.cfg.Steps)
	if err != nil {
		return nil, err
	}

	maxIter := e.cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 1
	}
	if e.cfg.Converged == nil {
		maxIter = 1
	}

	var allResults []StepResult

	for iter := 0; iter < maxIter; iter++ {
		results, err := e.executeOnce(ctx, state, graph, stepMap)
		allResults = append(allResults, results...)
		if err != nil {
			return allResults, err
		}

		if e.cfg.Converged == nil {
			break
		}

		converged, err := e.cfg.Converged(ctx, state, iter)
		if err != nil {
			return allResults, fmt.Errorf("convergence check: %w", err)
		}
		if converged {
			break
		}
	}

	return allResults, nil
}

// buildStepGraph constructs a dominikbraun/graph DAG from the step list,
// validates structure, and returns it along with an id→Step lookup. Any
// of these conditions returns an error: duplicate step ID, edge to an
// undeclared step, or a cycle (caught by TopologicalSort).
func buildStepGraph(steps []Step) (dgraph.Graph[string, Step], map[string]Step, error) {
	stepMap := make(map[string]Step, len(steps))
	g := dgraph.New(func(s Step) string { return s.ID }, dgraph.Directed(), dgraph.PreventCycles())

	for _, s := range steps {
		if _, dup := stepMap[s.ID]; dup {
			return nil, nil, fmt.Errorf("duplicate step id %q", s.ID)
		}
		stepMap[s.ID] = s
		if err := g.AddVertex(s); err != nil {
			return nil, nil, fmt.Errorf("add step %q to graph: %w", s.ID, err)
		}
	}

	// Edge direction is dependency → dependent: dgraph.PredecessorMap then
	// gives us each step's prerequisites, which is what wave-selection
	// reads. dgraph.PreventCycles errors on AddEdge if a cycle would form,
	// so we get cycle detection at edge-insertion time.
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			if _, ok := stepMap[dep]; !ok {
				return nil, nil, fmt.Errorf("step %q depends on undeclared step %q", s.ID, dep)
			}
			if err := g.AddEdge(dep, s.ID); err != nil {
				if errors.Is(err, dgraph.ErrEdgeCreatesCycle) {
					return nil, nil, fmt.Errorf("dependency cycle reaches step %q via %q", s.ID, dep)
				}
				return nil, nil, fmt.Errorf("add dependency %q → %q: %w", dep, s.ID, err)
			}
		}
	}

	// Belt-and-braces: TopologicalSort double-checks acyclicity. With
	// PreventCycles in play this should always succeed, but a future
	// refactor that drops PreventCycles would still trip the check here.
	if _, err := dgraph.TopologicalSort(g); err != nil {
		return nil, nil, fmt.Errorf("dependency cycle: %w", err)
	}

	return g, stepMap, nil
}

// executeOnce runs the DAG once, executing steps in dependency order with
// bounded parallelism. Wave selection comes from the graph's predecessor
// map: a step is "ready" when every predecessor has been marked completed.
func (e *Executor[S]) executeOnce(ctx context.Context, state *S, g dgraph.Graph[string, Step], stepMap map[string]Step) ([]StepResult, error) {
	predecessors, err := g.PredecessorMap()
	if err != nil {
		return nil, fmt.Errorf("predecessor map: %w", err)
	}

	completed := make(map[string]bool, len(stepMap))
	var allResults []StepResult

	for len(completed) < len(stepMap) {
		ready := readySteps(stepMap, predecessors, completed)
		if len(ready) == 0 {
			// Should be unreachable: buildStepGraph runs TopologicalSort
			// up front, so a cycle can't survive into executeOnce.
			// Surface a precise error if it ever does.
			return allResults, fmt.Errorf("no steps ready but %d incomplete (likely a graph-building bug)", len(stepMap)-len(completed))
		}

		// Separate parallel-eligible and sequential steps.
		var parallel, sequential []Step
		for _, s := range ready {
			if s.Parallel {
				parallel = append(parallel, s)
			} else {
				sequential = append(sequential, s)
			}
		}

		// Execute parallel steps with bounded concurrency.
		if len(parallel) > 0 {
			results, skipped, err := e.runParallel(ctx, state, parallel)
			for _, r := range results {
				if e.cfg.Merge != nil {
					e.cfg.Merge(state, r)
				}
				allResults = append(allResults, r)
				completed[r.StepID] = true
			}
			// Conditional-skipped parallel steps must also be marked
			// completed so the wave loop converges. Without this the
			// outer loop infinite-loops because skipped steps are
			// neither executed nor recorded as done — they stay in
			// `ready` forever. The sequential branch already handles
			// this via runSingle's skip return.
			for _, id := range skipped {
				completed[id] = true
			}
			if err != nil {
				return allResults, err
			}
		}

		// Execute sequential steps one at a time.
		for _, s := range sequential {
			result, skip, err := e.runSingle(ctx, state, s)
			if skip {
				completed[s.ID] = true
				continue
			}
			if e.cfg.Merge != nil {
				e.cfg.Merge(state, result)
			}
			allResults = append(allResults, result)
			completed[s.ID] = true
			if err != nil {
				return allResults, err
			}
		}
	}

	return allResults, nil
}

// readySteps returns steps that have every predecessor satisfied (in
// completed) and aren't themselves completed yet. Iteration order is
// driven by stepMap (Go's map iteration is randomised, but the wave
// then partitions into parallel/sequential which makes ordering within
// the wave irrelevant for correctness; only the set matters).
func readySteps(stepMap map[string]Step, predecessors map[string]map[string]dgraph.Edge[string], completed map[string]bool) []Step {
	var ready []Step
	for id, step := range stepMap {
		if completed[id] {
			continue
		}
		if !allPredecessorsCompleted(predecessors[id], completed) {
			continue
		}
		ready = append(ready, step)
	}
	return ready
}

func allPredecessorsCompleted(preds map[string]dgraph.Edge[string], completed map[string]bool) bool {
	for predID := range preds {
		if !completed[predID] {
			return false
		}
	}
	return true
}

// runSingle executes one step sequentially. Returns (result, skipped, error).
func (e *Executor[S]) runSingle(ctx context.Context, state *S, step Step) (StepResult, bool, error) {
	// Check conditional.
	if step.Conditional != nil && !step.Conditional(state) {
		e.emit(step.ID, "skipped", "")
		return StepResult{}, true, nil
	}

	e.emit(step.ID, "started", "")

	snap := e.snapshot(state)
	result, err := e.cfg.RunStep(ctx, step, snap)
	result.StepID = step.ID

	if err != nil {
		e.emit(step.ID, "error", err.Error())
		return result, false, err
	}

	e.emit(step.ID, "completed", "")
	return result, false, nil
}

// runParallel executes steps concurrently with bounded concurrency.
// All parallel steps receive the same snapshot (taken before any start).
// Results are collected, then the caller merges them sequentially.
//
// Returns (results, skipped, err) — `skipped` lists step IDs whose
// conditional returned false. The caller MUST mark those IDs completed
// so the wave loop converges; otherwise skipped steps stay in `ready`
// indefinitely and the executor loops forever.
func (e *Executor[S]) runParallel(ctx context.Context, state *S, steps []Step) ([]StepResult, []string, error) {
	snap := e.snapshot(state)

	// Filter out conditional-skipped steps.
	var toRun []Step
	var skipped []string
	for _, s := range steps {
		if s.Conditional != nil && !s.Conditional(state) {
			e.emit(s.ID, "skipped", "")
			skipped = append(skipped, s.ID)
			continue
		}
		toRun = append(toRun, s)
	}

	if len(toRun) == 0 {
		return nil, skipped, nil
	}

	results := make([]StepResult, len(toRun))
	var firstErr error
	var errOnce sync.Once

	// Semaphore for bounded concurrency.
	sem := make(chan struct{}, e.concurrencyLimit(len(toRun)))

	// Per-type semaphores for type-level concurrency limits.
	typeSems := make(map[string]chan struct{})
	for typ, limit := range e.cfg.TypeLimits {
		if limit > 0 {
			typeSems[typ] = make(chan struct{}, limit)
		}
	}

	var wg sync.WaitGroup
	wg.Add(len(toRun))

	for i, s := range toRun {
		go func(idx int, step Step) {
			defer wg.Done()

			// Acquire global semaphore slot. Select on ctx so a Ctrl-C
			// during workstream queue-up doesn't park goroutines waiting
			// for slots that will never come.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errOnce.Do(func() { firstErr = ctx.Err() })
				return
			}
			defer func() { <-sem }()

			// Acquire per-type semaphore slot if applicable.
			if step.Type != "" {
				if tsem, ok := typeSems[step.Type]; ok {
					select {
					case tsem <- struct{}{}:
					case <-ctx.Done():
						errOnce.Do(func() { firstErr = ctx.Err() })
						return
					}
					defer func() { <-tsem }()
				}
			}

			e.emit(step.ID, "started", "")

			r, err := e.cfg.RunStep(ctx, step, snap)
			r.StepID = step.ID
			results[idx] = r

			if err != nil {
				e.emit(step.ID, "error", err.Error())
				errOnce.Do(func() { firstErr = err })
			} else {
				e.emit(step.ID, "completed", "")
			}
		}(i, s)
	}

	wg.Wait()
	return results, skipped, firstErr
}

func (e *Executor[S]) snapshot(state *S) S {
	if e.cfg.Snapshot != nil {
		return e.cfg.Snapshot(state)
	}
	return *state
}

func (e *Executor[S]) concurrencyLimit(n int) int {
	if e.cfg.MaxConcurrency > 0 && e.cfg.MaxConcurrency < n {
		return e.cfg.MaxConcurrency
	}
	return n
}

func (e *Executor[S]) emit(stepID, status, message string) {
	if e.cfg.Events != nil {
		select {
		case e.cfg.Events <- Event{
			StepID:    stepID,
			Status:    status,
			Message:   message,
			Timestamp: time.Now(),
		}:
		default:
		}
	}
}
