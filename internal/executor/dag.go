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
package executor

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Step defines a unit of work in the DAG.
type Step struct {
	ID          string
	DependsOn   []string // IDs of steps that must complete before this one
	Parallel    bool     // if true, can run concurrently with other ready steps
	Conditional func(state any) bool // optional; if non-nil and returns false, step is skipped
}

// StepResult is the output of executing a single step.
type StepResult struct {
	StepID string
	Output any    // caller-defined payload
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
func (e *Executor[S]) Run(ctx context.Context, state *S) ([]StepResult, error) {
	maxIter := e.cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 1
	}
	if e.cfg.Converged == nil {
		maxIter = 1
	}

	var allResults []StepResult

	for iter := 0; iter < maxIter; iter++ {
		results, err := e.executeOnce(ctx, state)
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

// executeOnce runs the DAG once, executing steps in dependency order with
// bounded parallelism.
func (e *Executor[S]) executeOnce(ctx context.Context, state *S) ([]StepResult, error) {
	// Build lookup and track completion.
	completed := make(map[string]bool)
	stepMap := make(map[string]Step, len(e.cfg.Steps))
	for _, s := range e.cfg.Steps {
		stepMap[s.ID] = s
	}

	var allResults []StepResult

	for len(completed) < len(e.cfg.Steps) {
		// Find all steps whose dependencies are satisfied and aren't yet completed.
		var ready []Step
		for _, s := range e.cfg.Steps {
			if completed[s.ID] {
				continue
			}
			if depsReady(s.DependsOn, completed) {
				ready = append(ready, s)
			}
		}

		if len(ready) == 0 && len(completed) < len(e.cfg.Steps) {
			return allResults, fmt.Errorf("deadlock: no steps ready but %d incomplete", len(e.cfg.Steps)-len(completed))
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
			results, err := e.runParallel(ctx, state, parallel)
			for _, r := range results {
				if e.cfg.Merge != nil {
					e.cfg.Merge(state, r)
				}
				allResults = append(allResults, r)
				completed[r.StepID] = true
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
func (e *Executor[S]) runParallel(ctx context.Context, state *S, steps []Step) ([]StepResult, error) {
	snap := e.snapshot(state)

	// Filter out conditional-skipped steps.
	var toRun []Step
	for _, s := range steps {
		if s.Conditional != nil && !s.Conditional(state) {
			e.emit(s.ID, "skipped", "")
			continue
		}
		toRun = append(toRun, s)
	}

	if len(toRun) == 0 {
		return nil, nil
	}

	results := make([]StepResult, len(toRun))
	var firstErr error
	var errOnce sync.Once

	// Semaphore for bounded concurrency.
	sem := make(chan struct{}, e.concurrencyLimit(len(toRun)))

	var wg sync.WaitGroup
	wg.Add(len(toRun))

	for i, s := range toRun {
		go func(idx int, step Step) {
			defer wg.Done()

			// Acquire semaphore slot.
			sem <- struct{}{}
			defer func() { <-sem }()

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
	return results, firstErr
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

func depsReady(deps []string, completed map[string]bool) bool {
	for _, d := range deps {
		if !completed[d] {
			return false
		}
	}
	return true
}
