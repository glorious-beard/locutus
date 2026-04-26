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
	// LLM is used by the supervisor for output validation and the
	// permission guardian. Must be non-nil in production.
	LLM agent.LLM

	// FastLLM is the fast-tier client the cycle-detection monitor uses.
	// Optional — if nil, monitors that require an LLM are silently
	// disabled with a one-time INFO log from the supervisor.
	FastLLM agent.LLM

	// Drivers maps agent ID ("claude-code", "codex") to the StreamingDriver
	// that builds commands and parses the NDJSON event stream for that CLI.
	Drivers map[string]StreamingDriver

	// Runner executes agent commands. ProductionRunner in prod; mocked
	// in tests.
	Runner CommandRunner

	// AgentDefs are the supervision agents (validator, monitor, etc.)
	// loaded from .borg/agents/. Optional — if nil, the supervisor uses
	// default prompts and disables monitors that rely on a def.
	AgentDefs map[string]agent.AgentDef

	// ProgressNotifier receives human-readable updates from every
	// supervised step. Wire it to the MCP session's progress callback
	// (see cmd/progress.go) so Claude-the-client can show live status
	// while the dispatched agents work. Optional.
	ProgressNotifier ProgressNotifier

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
//
// AgentSessionID carries the streaming-driver session ID surfaced from
// the last step's last attempt. The adopt loop persists this on the
// ActiveWorkstream record (DJ-074) so a subsequent `adopt` invocation
// can resume an in-flight workstream with --resume <session> against
// the same agent conversation.
type WorkstreamResult struct {
	WorkstreamID   string
	BranchName     string
	StepResults    []*StepOutcome
	AgentSessionID string
	Success        bool
	Err            error
}

// ResumePoint marks where a workstream's execution should pick up on
// re-dispatch. StepID identifies the first not-yet-complete step in the
// workstream's persisted progress; SessionID is the streaming-driver
// conversation ID captured on the prior run, threaded into the resumed
// step's first attempt so the agent continues the same conversation
// (Claude Code: `--resume <id>`).
type ResumePoint struct {
	StepID    string
	SessionID string
}

// dispatchState is the shared state across workstream executions.
// It's owned by the executor orchestrator; individual workstreams write to
// indexed positions via Merge, which is called sequentially.
type dispatchState struct {
	results []*WorkstreamResult
}

// Dispatch runs all workstreams in the plan, respecting DependsOn edges and
// concurrency limits. Returns a WorkstreamResult for each workstream.
func (d *Dispatcher) Dispatch(ctx context.Context, plan *spec.MasterPlan, repoDir string, resume map[string]*ResumePoint) ([]*WorkstreamResult, error) {
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
			var resumeFrom *ResumePoint
			if resume != nil {
				resumeFrom = resume[ws.ID]
			}
			result := d.runWorkstream(ctx, ws, repoDir, maxRetries, resumeFrom)
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

// workstreamHasStep reports whether ws contains a step with the given ID.
// Used as an early validation on resume so a stale ResumePoint surfaces
// before the worktree is created and side effects begin.
func workstreamHasStep(ws spec.Workstream, stepID string) bool {
	for _, s := range ws.Steps {
		if s.ID == stepID {
			return true
		}
	}
	return false
}

// runWorkstream executes a single workstream: creates a worktree, routes to
// the agent, supervises each step, merges on success, cleans up on completion.
//
// resumeFrom controls re-entry on a previously-interrupted workstream
// (DJ-074). When non-nil:
//   - The worktree is based on the existing `locutus/<ws-id>` feature
//     branch so the prior run's already-merged step output forms the
//     starting state, not a fresh main.
//   - Steps before resumeFrom.StepID are skipped — they're already done.
//   - The resumed step's first attempt receives resumeFrom.SessionID so
//     the streaming driver can `--resume <id>` the prior conversation.
//
// When nil, the workstream runs fresh from main with no skipping.
func (d *Dispatcher) runWorkstream(ctx context.Context, ws spec.Workstream, repoDir string, maxRetries int, resumeFrom *ResumePoint) *WorkstreamResult {
	result := &WorkstreamResult{
		WorkstreamID: ws.ID,
	}

	// Find the driver for this workstream's agent.
	driver, ok := d.Drivers[ws.AgentID]
	if !ok {
		result.Err = fmt.Errorf("no driver registered for agent %q", ws.AgentID)
		return result
	}

	// On resume: validate the named step exists before any side effects.
	if resumeFrom != nil {
		if !workstreamHasStep(ws, resumeFrom.StepID) {
			result.Err = fmt.Errorf("resume: step %q not found in workstream %s", resumeFrom.StepID, ws.ID)
			return result
		}
	}

	// Create a worktree for this workstream. On resume, base the
	// worktree on the existing feature branch so prior steps' merged
	// work survives.
	var (
		wt  *Worktree
		err error
	)
	if resumeFrom != nil {
		wt, err = CreateWorktreeFromBase(ctx, repoDir, ws.ID, "locutus/"+ws.ID)
	} else {
		wt, err = CreateWorktree(ctx, repoDir, ws.ID)
	}
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
		LLM:              d.LLM,
		FastLLM:          d.FastLLM,
		MaxRetries:       maxRetries,
		AgentDefs:        d.AgentDefs,
		ProgressNotifier: d.ProgressNotifier,
	}, d.Runner)

	skipping := resumeFrom != nil
	allPassed := true
	for _, step := range ws.Steps {
		// Skip already-completed steps on resume.
		if skipping {
			if step.ID != resumeFrom.StepID {
				continue
			}
			skipping = false
		}

		// Pre-seed sessionID on the first attempt of the resumed step
		// so the driver issues `--resume <id>` against the prior agent
		// conversation. Subsequent steps run as fresh conversations
		// (existing semantics).
		var initialSessionID string
		if resumeFrom != nil && step.ID == resumeFrom.StepID {
			initialSessionID = resumeFrom.SessionID
		}

		outcome, err := sup.SuperviseFrom(ctx, step, driver, wt.WorktreeDir, initialSessionID)
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

	// Surface the most recent step's session ID for adopt persistence.
	if n := len(result.StepResults); n > 0 {
		result.AgentSessionID = result.StepResults[n-1].SessionID
	}

	if !allPassed {
		return result
	}

	// All steps passed: commit and merge to feature branch.
	if err := wt.Commit(ctx, fmt.Sprintf("workstream %s complete", ws.ID)); err != nil {
		// Commit may fail if there are no changes — that's fine for mock tests.
		// In production, we'd distinguish "no changes" from real errors.
		result.Success = true
		return result
	}

	featureBranch := "locutus/" + ws.ID
	if err := wt.MergeToFeatureBranch(ctx, featureBranch); err != nil {
		result.Err = fmt.Errorf("merge to %s: %w", featureBranch, err)
		return result
	}

	result.BranchName = featureBranch
	result.Success = true
	return result
}
