package agent

import (
	"context"
	"fmt"
)

// ImportState carries the inputs and outputs of the import workflow.
//
// Inputs (set by the cmd-layer before running the workflow):
//   - Data, SourcePath, Kind, GoalsBody — the document to admit and the
//     project context the intake call evaluates against.
//   - SkipPlan, DryRun — verb flags. SkipPlan also gates the plan
//     step's conditional; DryRun is observable to PlanRunner.
//   - LLM — the AgentExecutor used by the intake step's merge handler
//     (it forwards to the existing IntakeDocument helper).
//   - PlanRunner — closure supplied by the cmd-layer that runs the
//     post-admission planning pass when the plan step fires. Receives
//     the same state pointer so it can read IntakeResult and write
//     PlanResult; returns the opaque value (typically a
//     *cmd.GenerationSummary) the cmd-layer interprets after the
//     workflow finishes. Nil disables the plan step (the conditional
//     gates on it).
//
// Output (set by the intake step's merge handler):
//   - IntakeResult — the LLM's parsed verdict.
//
// Output (set by the plan step's merge handler when it fires):
//   - PlanResult — opaque to internal/agent; the cmd-layer interprets
//     it. Today this is a *cmd.GenerationSummary; the field stays any
//     so internal/agent doesn't import cmd.
//
// State semantics:
//   - The intake and plan steps are SYNTHETIC (Agents nil) — neither
//     fires an LLM call through the workflow's executor. Their merge
//     handlers do the real work via existing helpers (IntakeDocument
//     for intake, PlanRunner for plan). The workflow models the verb
//     SHAPE — phase ordering, conditional firing, future trace span
//     hierarchy — without rebuilding what those helpers already do.
//   - The cmd-layer drives the workflow in two RunImportWorkflow
//     calls so persistence can interleave: first call resolves
//     IntakeResult (plan suppressed via SkipPlan); cmd persists the
//     feature; second call fires the plan step (intake re-runs is
//     suppressed because IntakeResult is already populated). The
//     two-call shape preserves the Phase 9 brief's "single workflow
//     definition per verb" property while honouring the existing
//     intake → write → spec-gen ordering import requires.
//   - Per the workflow-unification plan (Phase 9), this honors
//     "Workflowize even single-call verbs" while keeping
//     IntakeDocument and the planning pass unchanged.
type ImportState struct {
	// Inputs
	Data       []byte
	SourcePath string
	Kind       string
	GoalsBody  string
	SkipPlan   bool
	DryRun     bool

	// LLM is the executor the intake step's merge handler uses to
	// invoke IntakeDocument.
	LLM AgentExecutor

	// Sink, when non-nil, receives the workflow's per-step lifecycle
	// events (intake + plan). The inner spec-generation council that
	// the plan step invokes wires its own sink via specgen.GenerateSpec;
	// this surfaces the outer step boundaries that the inner workflow
	// can't.
	Sink EventSink

	// PlanRunner is the cmd-layer-supplied closure that runs the
	// post-admission planning pass. Captures whatever ctx / fsys /
	// sink it needs from its enclosing scope.
	PlanRunner func(state *ImportState) (any, error)

	// Outputs
	IntakeResult *IntakeResult
	PlanResult   any

	// Internal bookkeeping. The merge-handler signature has no error
	// return and no ctx, so the merge handlers stash these on state
	// and RunImportWorkflow surfaces them. Unexported on purpose:
	// callers treat the workflow's Run return as authoritative.
	ctx       context.Context
	intakeErr error
	planErr   error
}

// ImportWorkflow drives the two-phase import shape: intake (always)
// followed by an optional plan step. Both phases are synthetic — the
// merge handlers invoke existing helpers (IntakeDocument and the
// caller-supplied PlanRunner) rather than dispatching agents through
// the workflow's executor. This keeps IntakeDocument and the planning
// pass unchanged while still surfacing import as a workflow-shaped
// verb (per the Phase 9 brief).
//
// Conditional firing on the plan step:
//   - SkipPlan suppresses the step.
//   - A nil IntakeResult (e.g. when intake errored before storing one)
//     suppresses it too — there's nothing to plan against.
//   - A non-admitted intake (Accepted=false or Duplicate=true with a
//     reason) suppresses it. The cmd-layer's existing rejection gate
//     stays the source of truth for downstream rendering.
//   - A nil PlanRunner suppresses it — when the cmd-layer doesn't
//     supply one (e.g. bug imports per DJ-068, --skip-triage paths
//     that bypass LLM entirely), no plan-phase work should fire.
var ImportWorkflow = &Workflow[ImportState]{
	Snapshot: snapshotImportState,
	Rounds: []WorkflowStep[ImportState]{
		{
			ID:          "intake",
			Conditional: shouldRunImportIntake,
			Merge:       mergeImportIntake,
		},
		{
			ID:          "plan",
			DependsOn:   []string{"intake"},
			Conditional: shouldRunImportPlan,
			Merge:       mergeImportPlan,
		},
	},
	MaxRounds: 1,
}

// shouldRunImportIntake gates the intake step. The cmd-layer may
// invoke the workflow more than once when the verb interleaves
// persistence between intake and plan (intake → persist → plan); on
// the second call IntakeResult is already populated and intake should
// not re-fire.
func shouldRunImportIntake(s *ImportState) bool {
	if s == nil {
		return false
	}
	return s.IntakeResult == nil && s.intakeErr == nil
}

// snapshotImportState returns a value-copy of ImportState safe for
// concurrent reads. The state has no slice/map fields beyond Data
// (which the workflow treats as immutable input); a shallow copy is
// sufficient. We still expose an explicit Snapshot so the workflow
// machinery doesn't fall through to the executor's default copy
// (which is also shallow but documented as "fine for verbs whose
// state has no slices or maps") — being explicit documents the
// choice.
func snapshotImportState(s *ImportState) ImportState { return *s }

// shouldRunImportPlan gates the plan step. See ImportWorkflow doc for
// the four conditions evaluated.
func shouldRunImportPlan(s *ImportState) bool {
	if s == nil {
		return false
	}
	if s.SkipPlan || s.PlanRunner == nil {
		return false
	}
	if s.IntakeResult == nil {
		return false
	}
	// An intake verdict counts as an admission only when the LLM said
	// so. The cmd-layer's rejection gate (Reason != "" && (!Accepted ||
	// Duplicate)) decides downstream; we mirror that here so the plan
	// step doesn't fire on a rejected admission.
	if !s.IntakeResult.Accepted || s.IntakeResult.Duplicate {
		return false
	}
	return true
}

// mergeImportIntake runs the intake LLM call by delegating to
// IntakeDocument and stashes the result on state. Errors land on
// state.intakeErr and surface via RunImportWorkflow's return —
// IntakeResult stays nil so the conditional on the plan step fails
// closed (no planning against a missing verdict).
//
// Ctx comes off state — RunImportWorkflow stamps it before
// executor.Run; the merge signature has no ctx of its own.
func mergeImportIntake(s *ImportState, _ []RoundResult) {
	if s == nil || s.LLM == nil {
		return
	}
	ctx := contextFromImportState(s)
	res, err := IntakeDocument(WithRole(ctx, "intake"), s.LLM, s.Kind, string(s.Data), s.GoalsBody)
	if err != nil {
		// Stash the error on a sentinel so cmd-layer can surface it.
		// The merge contract has no error return; the cmd-layer reads
		// IntakeResult and treats nil as "intake did not produce a
		// verdict" (combined with the workflow's Run error return,
		// the actual cause is preserved).
		s.intakeErr = err
		return
	}
	s.IntakeResult = res
}

// mergeImportPlan invokes the cmd-supplied PlanRunner closure and
// stashes its return value on state. Errors land in state.planErr;
// the cmd-layer reads PlanResult after the workflow returns and
// surfaces planErr as needed.
func mergeImportPlan(s *ImportState, _ []RoundResult) {
	if s == nil || s.PlanRunner == nil {
		return
	}
	res, err := s.PlanRunner(s)
	if err != nil {
		s.planErr = err
		return
	}
	s.PlanResult = res
}

// contextFromImportState returns the ctx the workflow was invoked
// with. RunImportWorkflow stamps it on state before executor.Run;
// falls back to context.Background to avoid nil-ctx panics on tests
// that drive merge handlers directly without the workflow runner.
func contextFromImportState(s *ImportState) context.Context {
	if s != nil && s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// RunImportWorkflow is the cmd-facing entry point. Constructs the
// workflow executor, runs it against the supplied state, and surfaces
// the merge-handler errors stashed on state.
//
// The cmd-layer sets all input fields plus PlanRunner before calling.
// On return, IntakeResult and PlanResult are set per the steps that
// fired; merge errors are returned alongside the workflow's own
// dispatch error (precedence: dispatch err > intake merge err > plan
// merge err).
func RunImportWorkflow(ctx context.Context, state *ImportState) error {
	if state == nil {
		return fmt.Errorf("import workflow: nil state")
	}
	state.ctx = ctx

	executor := &WorkflowExecutor[ImportState]{
		Workflow: ImportWorkflow,
	}
	defer executor.BridgeToSink(state.Sink)()
	if _, err := executor.Run(ctx, state); err != nil {
		return err
	}
	if state.intakeErr != nil {
		return state.intakeErr
	}
	if state.planErr != nil {
		return state.planErr
	}
	return nil
}
