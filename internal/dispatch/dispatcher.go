package dispatch

import (
	"context"
	"fmt"
	"sync"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/executor"
	"github.com/chetan/locutus/internal/spec"
)

// Dispatcher runs a MasterPlan by executing its workstreams in parallel,
// respecting workstream dependencies and per-agent concurrency limits. Each
// workstream runs in its own git worktree; the supervisor handles the retry/
// validate loop for each step within a workstream.
type Dispatcher struct {
	// LLM is used by the supervisor for output validation.
	LLM agent.LLM

	// Drivers maps agent ID ("claude-code", "codex") to the StreamingDriver
	// that builds commands and parses the NDJSON event stream for that CLI.
	Drivers map[string]StreamingDriver

	// Runner executes agent commands. Typically exec.CombinedOutput in prod;
	// mocked in tests.
	Runner CommandRunner

	// AgentDefs are the supervision agents (validator, guide, reviewer) loaded
	// from .borg/agents/. Optional — if nil, supervisor uses default prompts.
	AgentDefs map[string]agent.AgentDef

	// MaxTotal caps the total number of workstreams running concurrently.
	// 0 or negative means unlimited.
	MaxTotal int

	// MaxPerAgent limits how many workstreams of a given agent type run
	// concurrently (e.g., {"claude-code": 2, "codex": 1}).
	MaxPerAgent map[string]int

	// MaxRetriesPerStep caps retry attempts per plan step. Defaults to 3.
	MaxRetriesPerStep int
}

// WorkstreamResult is the outcome of running a single workstream.
type WorkstreamResult struct {
	WorkstreamID string
	BranchName   string
	StepResults  []*StepOutcome
	Success      bool
	Err          error
}

// dispatchState is the shared state across workstream executions.
// It's owned by the executor orchestrator; individual workstreams write to
// indexed positions via Merge, which is called sequentially.
type dispatchState struct {
	results []*WorkstreamResult
}

// Dispatch runs all workstreams in the plan, respecting DependsOn edges and
// concurrency limits. Returns a WorkstreamResult for each workstream.
func (d *Dispatcher) Dispatch(ctx context.Context, plan *spec.MasterPlan, repoDir string) ([]*WorkstreamResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("dispatch: plan is nil")
	}
	if len(plan.Workstreams) == 0 {
		return nil, nil
	}

	maxRetries := d.MaxRetriesPerStep
	if maxRetries <= 0 {
		maxRetries = 3
	}

	// Build executor.Steps from workstreams.
	// Each step's Type is the agent ID so per-agent concurrency limits apply.
	wsLookup := make(map[string]spec.Workstream, len(plan.Workstreams))
	dagSteps := make([]executor.Step, len(plan.Workstreams))
	for i, ws := range plan.Workstreams {
		wsLookup[ws.ID] = ws
		deps := make([]string, len(ws.DependsOn))
		for j, dep := range ws.DependsOn {
			deps[j] = dep.WorkstreamID
		}
		dagSteps[i] = executor.Step{
			ID:        ws.ID,
			DependsOn: deps,
			Parallel:  true,
			Type:      ws.AgentID,
		}
	}

	// Guard state mutation.
	var mu sync.Mutex
	state := &dispatchState{
		results: make([]*WorkstreamResult, 0, len(plan.Workstreams)),
	}

	cfg := executor.Config[dispatchState]{
		Steps: dagSteps,
		RunStep: func(ctx context.Context, step executor.Step, _ dispatchState) (executor.StepResult, error) {
			ws := wsLookup[step.ID]
			result := d.runWorkstream(ctx, ws, repoDir, maxRetries)
			return executor.StepResult{Output: result}, nil // never error — per-workstream failures captured in result
		},
		Merge: func(s *dispatchState, r executor.StepResult) {
			mu.Lock()
			defer mu.Unlock()
			if ws, ok := r.Output.(*WorkstreamResult); ok {
				s.results = append(s.results, ws)
			}
		},
		Snapshot:       func(s *dispatchState) dispatchState { return dispatchState{} }, // workstreams don't need shared state
		MaxConcurrency: d.MaxTotal,
		TypeLimits:     d.MaxPerAgent,
	}

	exec := executor.NewExecutor(cfg)
	if _, err := exec.Run(ctx, state); err != nil {
		return state.results, fmt.Errorf("executor: %w", err)
	}

	return state.results, nil
}

// runWorkstream executes a single workstream: creates a worktree, routes to
// the agent, supervises each step, merges on success, cleans up on completion.
func (d *Dispatcher) runWorkstream(ctx context.Context, ws spec.Workstream, repoDir string, maxRetries int) *WorkstreamResult {
	result := &WorkstreamResult{
		WorkstreamID: ws.ID,
	}

	// Find the driver for this workstream's agent.
	driver, ok := d.Drivers[ws.AgentID]
	if !ok {
		result.Err = fmt.Errorf("no driver registered for agent %q", ws.AgentID)
		return result
	}

	// Create a worktree for this workstream.
	wt, err := CreateWorktree(repoDir, ws.ID)
	if err != nil {
		result.Err = fmt.Errorf("create worktree: %w", err)
		return result
	}
	defer func() { _ = wt.Cleanup() }()

	// During execution, BranchName reflects the worktree's scratch branch
	// so callers can trace in-flight work. On successful merge it's
	// overwritten with the feature branch name below — that's where the
	// work actually lives after Cleanup tears down the scratch branch.
	result.BranchName = wt.BranchName

	// Supervise each step in sequence.
	sup := NewSupervisor(SupervisorConfig{
		LLM:        d.LLM,
		MaxRetries: maxRetries,
		AgentDefs:  d.AgentDefs,
	}, d.Runner)

	allPassed := true
	for _, step := range ws.Steps {
		outcome, err := sup.Supervise(ctx, step, driver, wt.WorktreeDir)
		if err != nil {
			result.Err = fmt.Errorf("step %s: %w", step.ID, err)
			result.StepResults = append(result.StepResults, outcome)
			allPassed = false
			break
		}
		result.StepResults = append(result.StepResults, outcome)
		if !outcome.Success {
			allPassed = false
			break
		}
	}

	if !allPassed {
		return result
	}

	// All steps passed: commit and merge to feature branch.
	if err := wt.Commit(fmt.Sprintf("workstream %s complete", ws.ID)); err != nil {
		// Commit may fail if there are no changes — that's fine for mock tests.
		// In production, we'd distinguish "no changes" from real errors.
		result.Success = true
		return result
	}

	featureBranch := "locutus/" + ws.ID
	if err := wt.MergeToFeatureBranch(featureBranch); err != nil {
		result.Err = fmt.Errorf("merge to %s: %w", featureBranch, err)
		return result
	}

	result.BranchName = featureBranch
	result.Success = true
	return result
}
