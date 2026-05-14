package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/dispatch/policy"
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
	LLM agent.AgentExecutor

	// FastLLM is the fast-tier client the cycle-detection monitor uses.
	// Optional — if nil, monitors that require an LLM are silently
	// disabled with a one-time INFO log from the supervisor.
	FastLLM agent.AgentExecutor

	// OpenConn returns a per-workstream ACP connection for the given
	// agent id. The cmd layer populates this with a closure that calls
	// acp.Open with the agent's Spawn descriptor and wraps the result in
	// an adapter so dispatch doesn't need to import the acp package
	// (which would close a cycle via acp's import of dispatch.AgentEvent).
	//
	// Required in production. The dispatcher returns "no connection
	// opener registered for agent X" for any workstream whose agent id
	// has no entry — same shape as the pre-DJ-119 missing-driver error.
	OpenConn func(ctx context.Context, agentID string) (PromptConn, error)

	// Runner executes auxiliary subprocess commands (git, etc.) for the
	// supervisor's worktree/commit/merge plumbing. Not used for the
	// coding-agent transport itself — that flows through OpenConn under
	// DJ-119. ProductionRunner in prod; mocked in tests.
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

	// MaxRetriesPerStep caps retry attempts per plan step. Defaults to 3.
	MaxRetriesPerStep int

	// OnStepComplete fires after every PlanStep finishes (success or
	// failure) so the caller can persist progress mid-workstream. Per
	// DJ-073 ("On each PlanStep completion: dispatcher calls Save on the
	// affected ActiveWorkstream"), the cmd/adopt path wires this to a
	// per-step workstream.FileStore.Save so a SIGKILL during PlanStep N
	// of M doesn't cost the user the prior N-1 steps' work on resume.
	// Optional — when nil, persistence happens only via the tail
	// recordStepProgress call after the workstream returns (existing
	// behaviour, no resume granularity).
	OnStepComplete StepCompleteHandler

	// PolicyForWorkstream returns the permission policy threaded through
	// every Prompt for the given workstream. The cmd layer wires this to a
	// closure that constructs a guardian.Guardian per workstream (DJ-119
	// Phase 4; coarsened by DJ-121 from per-step to per-workstream).
	// Optional — when nil, the supervisor falls back to
	// policy.AllowOncePolicy, the test/early-integration placeholder.
	PolicyForWorkstream func(ws spec.Workstream) policy.Policy
}

// StepCompleteHandler is the per-step persistence hook. It runs
// synchronously between steps, after Commit + Merge for successful steps
// and immediately on failure. Errors from the handler are not propagated
// (best-effort persistence) but should be logged by the handler itself.
type StepCompleteHandler func(ctx context.Context, evt StepEvent)

// StepEvent describes the outcome of a single PlanStep.
type StepEvent struct {
	WorkstreamID string
	StepID       string
	SessionID    string // last-known agent conversation ID
	Success      bool
	Message      string // populated on failure (escalation reason or commit/merge error)
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
		// Sequential execution per DJ-121: workstreams run one at a time
		// in DAG-topological order, even when DependsOn permits parallelism.
		// Correctness over throughput — downstream workstreams benefit from
		// seeing the complete merged output of every upstream workstream.
		// Parallel:false on every step ensures the executor schedules them
		// strictly sequentially.
		dagSteps[i] = executor.Step{
			ID:        ws.ID,
			DependsOn: deps,
			Parallel:  false,
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
		Snapshot: func(s *dispatchState) dispatchState { return dispatchState{} }, // workstreams don't need shared state
		// MaxConcurrency and TypeLimits intentionally omitted per DJ-121:
		// sequential execution is the default. Parallel:false on each
		// executor.Step is the load-bearing guard.
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

// runWorkstream executes a single workstream: creates a worktree, opens an
// ACP connection on the agent for that workstream, supervises each step,
// merges on success, and cleans up on completion.
//
// Under DJ-119 the connection lifetime is workstream-scoped: one
// acp.Connection + one ACP session, shared across all steps and all retry
// attempts within those steps. Per-attempt subprocess spawning (the
// pre-DJ-119 driver model) is gone.
//
// resumeFrom controls re-entry on a previously-interrupted workstream
// (DJ-074). When non-nil:
//   - The worktree is based on the existing `locutus/<ws-id>` feature
//     branch so the prior run's already-merged step output forms the
//     starting state, not a fresh main.
//   - Steps before resumeFrom.StepID are skipped — they're already done.
//   - resumeFrom.SessionID is preserved on the WorkstreamResult for
//     telemetry but is NOT replayed into the new ACP session. The
//     workstream-scoped Connection means cross-process conversation
//     resume isn't available without a `session/load` call on a
//     freshly-spawned subprocess — that's a separate concern to be
//     addressed under its own DJ if it becomes load-bearing. The
//     step-level (git) resume is the durable contract DJ-074 promises.
//
// When nil, the workstream runs fresh from main with no skipping.
func (d *Dispatcher) runWorkstream(ctx context.Context, ws spec.Workstream, repoDir string, maxRetries int, resumeFrom *ResumePoint) *WorkstreamResult {
	result := &WorkstreamResult{
		WorkstreamID: ws.ID,
	}

	// Connection opener must be wired by the caller. Missing entry is the
	// shape of the pre-DJ-119 missing-driver error.
	if d.OpenConn == nil {
		result.Err = fmt.Errorf("no connection opener configured on Dispatcher")
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

	// Write the workstream's plan into the worktree before the agent's
	// first prompt sees the directory (DJ-121). The agent reads
	// _locutus/plan.md to understand what to build and maintains its own
	// _locutus/checklist.md as it works. Best-effort: a plan-write failure
	// downgrades to a logged warning rather than failing the workstream —
	// the prompt text still carries the same content and the agent can
	// proceed without the artifact, just without resume-side step
	// continuity.
	if err := WriteWorkstreamPlan(wt.WorktreeDir, ws); err != nil {
		slog.Warn("write workstream plan",
			"workstream_id", ws.ID,
			"worktree", wt.WorktreeDir,
			"error", err,
		)
	}

	// Open the ACP connection for this workstream and create the shared
	// session rooted at the worktree directory. Both close at workstream
	// end via the deferred Close + the supervisor-level ctx cancel.
	conn, err := d.OpenConn(ctx, ws.AgentID)
	if err != nil {
		result.Err = fmt.Errorf("open acp connection for agent %q: %w", ws.AgentID, err)
		return result
	}
	defer func() { _ = conn.Close() }()

	sessionID, err := conn.NewSession(ctx, wt.WorktreeDir)
	if err != nil {
		result.Err = fmt.Errorf("acp.NewSession on %q: %w", ws.AgentID, err)
		return result
	}
	result.AgentSessionID = sessionID

	// Supervise the workstream as one unit (DJ-121): one retry-and-validate
	// loop per workstream, one commit + merge to the feature branch on
	// success. The agent owns its own step decomposition via
	// _locutus/checklist.md it maintains in the worktree.
	sup := NewSupervisor(SupervisorConfig{
		LLM:                 d.LLM,
		FastLLM:             d.FastLLM,
		MaxRetries:          maxRetries,
		AgentDefs:           d.AgentDefs,
		ProgressNotifier:    d.ProgressNotifier,
		PolicyForWorkstream: d.PolicyForWorkstream,
	}, d.Runner)

	featureBranch := "locutus/" + ws.ID

	outcome, supErr := sup.Supervise(ctx, ws, conn, sessionID)
	wsSuccess := supErr == nil && outcome != nil && outcome.Success
	wsMessage := ""
	if outcome != nil {
		if outcome.SessionID != "" {
			result.AgentSessionID = outcome.SessionID
		}
		if !wsSuccess && outcome.Escalation != "" {
			wsMessage = outcome.Escalation
		}
		result.StepResults = append(result.StepResults, outcome)
	}
	if supErr != nil {
		wsMessage = supErr.Error()
	}

	// Per-workstream commit + merge on success. A commit failure demotes
	// the workstream to failed so the persisted record is honest about
	// what landed.
	anyMerged := false
	if wsSuccess {
		committed, commitErr := wt.CommitIfChanges(ctx, fmt.Sprintf("workstream %s", ws.ID))
		if commitErr != nil {
			wsSuccess = false
			wsMessage = fmt.Sprintf("commit workstream %s: %v", ws.ID, commitErr)
			supErr = commitErr
		} else if committed {
			if mergeErr := wt.MergeToFeatureBranch(ctx, featureBranch); mergeErr != nil {
				wsSuccess = false
				wsMessage = fmt.Sprintf("merge workstream %s: %v", ws.ID, mergeErr)
				supErr = mergeErr
			} else {
				anyMerged = true
			}
		}
	}

	// Persist progress for this workstream. SIGKILL after this point on a
	// successful workstream still leaves the work on the feature branch
	// and the record marked complete; resume picks up at the next
	// workstream. The StepEvent's StepID is intentionally empty under
	// DJ-121 — the unit of progress is now the workstream itself; the
	// field is retained on the event for downstream-handler compatibility
	// until Phase 5 collapses StepProgress entirely.
	if d.OnStepComplete != nil {
		d.OnStepComplete(ctx, StepEvent{
			WorkstreamID: ws.ID,
			StepID:       "",
			SessionID:    result.AgentSessionID,
			Success:      wsSuccess,
			Message:      wsMessage,
		})
	}

	if !wsSuccess {
		if supErr != nil && result.Err == nil {
			result.Err = fmt.Errorf("workstream %s: %w", ws.ID, supErr)
		}
		return result
	}

	if anyMerged {
		// The workstream's work landed on the feature branch — that's
		// where the durable artefact lives after Cleanup tears down the
		// scratch branch.
		result.BranchName = featureBranch
	}

	result.Success = true
	return result
}
