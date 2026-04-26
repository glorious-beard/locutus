package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/check"
	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/eval"
	"github.com/chetan/locutus/internal/overlap"
	"github.com/chetan/locutus/internal/preflight"
	"github.com/chetan/locutus/internal/reconcile"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
	"github.com/chetan/locutus/internal/workstream"
)

// AdoptCmd brings code into alignment with spec (the DJ-068 reconcile
// loop). When `--dry-run` is passed it classifies every Approach, runs
// the prereq gate, and exits. Otherwise it invokes the full pipeline:
//
//  1. Resume protocol (DJ-073): any in-flight plans under
//     `.locutus/workstreams/` are inspected. The current behavior
//     unconditionally invalidates and replans; true `--resume <session>`
//     semantics require agent-driver changes that live in a future
//     round. See DJ-073's "Resume protocol" for the target behaviour.
//  2. Classification (DJ-068) + scope filter + prereq gate.
//  3. Planner invocation (agent.Plan) produces a MasterPlan covering the
//     drift/unplanned/failed candidates.
//  4. PlanRecord + ActiveWorkstream records persisted per DJ-073.
//  5. Pre-flight (DJ-071) resolves ambiguities per workstream; assumed
//     Decisions cascade (DJ-069) through cascade.Cascade.
//  6. Dispatcher runs the plan, producing WorkstreamResults.
//  7. Verify: for each Approach under a successful workstream, run its
//     Assertions. Outcomes flip state to live or failed. Artifact hashes
//     are refreshed so the next classification sees the post-dispatch
//     baseline.
//  8. Archive: plans with every covered Approach live are deleted from
//     disk; incomplete plans are left for the next adopt invocation.
type AdoptCmd struct {
	Scope             string `arg:"" optional:"" help:"Limit adoption to spec nodes under this ID (default: all)."`
	DryRun            bool   `help:"Classify and plan without dispatching."`
	DiscardInFlight   bool   `help:"Force invalidate-and-replan on every leftover plan instead of attempting to resume (DJ-074)."`
	PreflightRounds   int    `help:"Maximum pre-flight clarification rounds per workstream (0 = use default of 3)."`
	MaxConcurrent     int    `help:"Cap total concurrent workstreams. 0 = unlimited." default:"0"`
}

// PlanFunc produces a MasterPlan from a PlanRequest. Injectable so tests
// can run the full pipeline without LLM access.
type PlanFunc func(ctx context.Context, req agent.PlanRequest) (*spec.MasterPlan, error)

// DispatchFunc runs a MasterPlan in the given repo directory and returns
// per-workstream results. Injectable so tests can run the full pipeline
// without git worktrees or subprocess agents.
//
// resume is per-workstream resume state (DJ-074); a workstream with a
// non-nil entry skips its already-completed steps and re-attaches to
// the prior agent conversation via SessionID. nil or empty map ⇒ all
// workstreams run fresh.
type DispatchFunc func(ctx context.Context, plan *spec.MasterPlan, repoDir string, resume map[string]*dispatch.ResumePoint) ([]*dispatch.WorkstreamResult, error)

// AdoptConfig bundles everything RunAdoptWithConfig needs. Zero values are
// replaced with defaults where sensible.
type AdoptConfig struct {
	FS       specio.FS
	LLM      agent.LLM
	RepoDir  string
	Plan     PlanFunc
	Dispatch DispatchFunc

	Scope             string
	DryRun            bool
	DiscardInFlight   bool // DJ-074: force invalidate-and-replan on leftover plans
	PreflightRounds   int
	MaxConcurrent     int
}

// AdoptReport is the structured result of an adopt invocation.
type AdoptReport struct {
	Scope           string                     `json:"scope,omitempty"`
	DryRun          bool                       `json:"dry_run"`
	Classifications []reconcile.Classification `json:"classifications"`
	PrereqResults   []check.Result             `json:"prereq_results,omitempty"`
	PrereqsOK       bool                       `json:"prereqs_ok"`
	Summary         AdoptSummary               `json:"summary"`

	// Populated when dispatch actually ran.
	PlanID             string                      `json:"plan_id,omitempty"`
	DispatchedWorkstreams []WorkstreamOutcome      `json:"dispatched_workstreams,omitempty"`
	PreflightResolutions  []preflight.Resolution   `json:"preflight_resolutions,omitempty"`
	AssumedDecisions      []string                 `json:"assumed_decisions,omitempty"` // IDs of new assumed Decisions
	ResumedInvalidated    []string                 `json:"resumed_invalidated,omitempty"` // plan IDs whose prior run was wiped
	Resumed               []string                 `json:"resumed,omitempty"`             // plan IDs picked up via DJ-074 resume
	Archived              []string                 `json:"archived,omitempty"`            // plan IDs deleted on terminal transition
}

// WorkstreamOutcome is the adopt-level view of one workstream's execution:
// whether dispatch succeeded, whether every Approach it covers passed its
// assertions, and which specific Approaches are now live vs failed.
type WorkstreamOutcome struct {
	WorkstreamID string   `json:"workstream_id"`
	BranchName   string   `json:"branch_name,omitempty"`
	Dispatched   bool     `json:"dispatched"`       // dispatcher returned Success
	LiveApproaches   []string `json:"live_approaches,omitempty"`
	FailedApproaches []string `json:"failed_approaches,omitempty"`
	Error            string   `json:"error,omitempty"`
}

// AdoptSummary is a compact count of the reconciled statuses.
type AdoptSummary struct {
	Live       int `json:"live"`
	Drifted    int `json:"drifted"`
	OutOfSpec  int `json:"out_of_spec"`
	Unplanned  int `json:"unplanned"`
	Failed     int `json:"failed"`
	InProgress int `json:"in_progress"`
	Candidates int `json:"candidates"` // count that would be planned
}

const workstreamsDir = ".locutus/workstreams"

func (c *AdoptCmd) Run(ctx context.Context, cli *CLI) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	fsys := specio.NewOSFS(cwd)

	cfg := AdoptConfig{
		FS:              fsys,
		RepoDir:         cwd,
		Scope:           c.Scope,
		DryRun:          c.DryRun,
		DiscardInFlight: c.DiscardInFlight,
		PreflightRounds: c.PreflightRounds,
		MaxConcurrent:   c.MaxConcurrent,
	}

	// Real dispatch requires an LLM; the CLI attempts to build one if any
	// provider env var is set. The no-LLM path is still useful (classify +
	// report) and is exercised by the older unit tests.
	if agent.LLMAvailable() && !c.DryRun {
		llm, err := getLLM()
		if err != nil {
			return err
		}
		cfg.LLM = llm
		cfg.Plan = realPlan(llm, fsys)
		cfg.Dispatch = realDispatch(llm, cfg.FS)
	}

	report, err := RunAdoptWithConfig(ctx, cfg)
	if err != nil {
		return err
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(report)
	}

	renderAdoptReport(report)

	if report.DryRun {
		return nil
	}
	if !report.PrereqsOK {
		return ExitCode(2)
	}
	if report.Summary.OutOfSpec > 0 {
		return ExitCode(1)
	}
	if hasAnyFailedWorkstream(report) {
		return ExitCode(1)
	}
	return nil
}

// RunAdopt is the narrow entry point used by the MCP handler (cmd/mcp.go)
// and the cmd-package tests. It runs classification + prereq gate + planned
// state writes against fsys; pass scope to limit work to a subgraph and
// dryRun to suppress mutations. For the full dispatch pipeline (planner +
// supervised execution), construct AdoptConfig and call RunAdoptWithConfig
// directly — that's the path AdoptCmd.Run uses.
func RunAdopt(ctx context.Context, fsys specio.FS, scope string, dryRun bool) (*AdoptReport, error) {
	return RunAdoptWithConfig(ctx, AdoptConfig{FS: fsys, Scope: scope, DryRun: dryRun})
}

// RunAdoptWithConfig is the full reconcile-loop entry point. See AdoptCmd
// docs for the phased flow.
func RunAdoptWithConfig(ctx context.Context, cfg AdoptConfig) (*AdoptReport, error) {
	if cfg.FS == nil {
		return nil, fmt.Errorf("adopt: FS is required")
	}

	report := &AdoptReport{
		Scope:  cfg.Scope,
		DryRun: cfg.DryRun,
	}

	// Eval runner for assertions whose verdict requires an LLM call —
	// today only `llm_review`. Constructed once per adopt run; passed
	// into runAssertions for every Approach we verify.
	evalRunner := eval.NewRunner(cfg.LLM)

	// Load the spec graph + state store first so the resume classifier
	// (Phase 1) can do drift detection. Both are reused throughout the
	// rest of the flow.
	graph, err := loadSpecGraph(cfg.FS)
	if err != nil {
		return report, err
	}
	store := state.NewFileStateStore(cfg.FS, ".locutus/state")

	// --- Phase 1: Resume protocol (DJ-073 + DJ-074) ---
	// Default behavior is auto-resume when possible: a leftover plan
	// with no covered-Approach drift becomes a resumeMap that
	// re-dispatches only the not-yet-complete steps with the persisted
	// agent session IDs. A plan whose covered Approaches are all live
	// is archived. Drift triggers invalidate-and-replan.
	// --discard-in-flight forces invalidate on every leftover plan.
	resumePoints := map[string]*dispatch.ResumePoint{}
	var planToResume *spec.MasterPlan
	if !cfg.DryRun {
		classified, err := classifyActivePlans(cfg.FS, cfg.DiscardInFlight, graph, store)
		if err != nil {
			return report, fmt.Errorf("resume: %w", err)
		}
		report.ResumedInvalidated = classified.Invalidated
		report.Archived = append(report.Archived, classified.Archived...)
		resumePoints = classified.ResumeMap
		planToResume = classified.PlanToResume
	}

	// --- Resume short-circuit ---
	// When a leftover plan classified as resumable, skip Phases 2-6
	// (classification + planning + persistence + pre-flight). The plan
	// is already on disk; the persisted state has the prior run's
	// progress; pre-flight already happened. Jump to dispatch with the
	// resumeMap and let Phase 8 verify the outcome.
	if planToResume != nil {
		report.PlanID = planToResume.ID
		report.Resumed = append(report.Resumed, planToResume.ID)
		return runAdoptDispatchAndVerify(ctx, cfg, report, graph, store, planToResume, resumePoints, evalRunner)
	}

	// --- Phase 2: Classification + scope + prereqs ---

	classifications, err := reconcile.Classify(cfg.FS, graph, store)
	if err != nil {
		return report, fmt.Errorf("classify: %w", err)
	}
	if cfg.Scope != "" {
		classifications = filterByScope(classifications, graph, cfg.Scope)
	}
	report.Classifications = classifications
	report.Summary = summariseClassifications(classifications)

	prereqs, perr := check.CheckPrereqs(cfg.FS)
	if perr != nil {
		return report, fmt.Errorf("prereqs: %w", perr)
	}
	report.PrereqResults = prereqs
	report.PrereqsOK = !check.AnyFailed(prereqs)

	if cfg.DryRun || !report.PrereqsOK {
		return report, nil
	}

	// --- Phase 3: Persist transient planned state for every candidate ---
	if err := writePlannedState(store, classifications); err != nil {
		return report, err
	}

	candidates := reconcile.PlanCandidates(classifications)
	if len(candidates) == 0 || cfg.Plan == nil || cfg.Dispatch == nil {
		// No LLM configured or nothing to plan — stop after the state writes.
		return report, nil
	}

	// --- Phase 4: Plan ---
	plan, err := runPlannerForCandidates(ctx, cfg, graph, candidates)
	if err != nil {
		return report, fmt.Errorf("plan: %w", err)
	}
	if plan == nil || len(plan.Workstreams) == 0 {
		return report, nil
	}
	report.PlanID = plan.ID

	// --- Phase 5: Persist PlanRecord + ActiveWorkstreams ---
	wsStore := workstream.NewFileStore(cfg.FS, workstreamsDir, plan.ID)
	if err := wsStore.SavePlan(*plan); err != nil {
		return report, fmt.Errorf("persist plan: %w", err)
	}
	approachesByWorkstream := approachesCoveredByWorkstreams(plan)
	for _, ws := range plan.Workstreams {
		rec := workstream.ActiveWorkstream{
			WorkstreamID: ws.ID,
			PlanID:       plan.ID,
			ApproachIDs:  approachesByWorkstream[ws.ID],
			Plan:         ws,
		}
		if err := wsStore.Save(rec); err != nil {
			return report, fmt.Errorf("persist workstream %s: %w", ws.ID, err)
		}
		// Flip every covered Approach to in_progress + stamp the plan ID.
		for _, aid := range rec.ApproachIDs {
			entry, err := store.Load(aid)
			if err != nil {
				entry = state.ReconciliationState{ApproachID: aid}
			}
			entry.Status = state.StatusPreFlight
			entry.WorkstreamID = ws.ID
			entry.Message = "pre-flight"
			entry.LastReconciled = time.Now()
			if err := store.Save(entry); err != nil {
				return report, fmt.Errorf("mark %s pre_flight: %w", aid, err)
			}
		}
	}

	// --- Phase 6: Pre-flight per workstream ---
	approachesByID := indexApproaches(graph)
	for _, ws := range plan.Workstreams {
		pfReport, err := preflight.Preflight(ctx, cfg.LLM, cfg.FS, graph, store, ws, approachesByID, cfg.PreflightRounds)
		if err != nil {
			return report, fmt.Errorf("preflight %s: %w", ws.ID, err)
		}
		report.PreflightResolutions = append(report.PreflightResolutions, pfReport.Resolutions...)
		for _, d := range pfReport.AssumedDecisions {
			report.AssumedDecisions = append(report.AssumedDecisions, d.ID)
		}
		// Flip the record to reflect pre-flight completion.
		rec, err := wsStore.Load(ws.ID)
		if err == nil {
			rec.PreFlightDone = true
			_ = wsStore.Save(rec)
		}
	}
	// Flip state entries from pre_flight → in_progress ahead of dispatch.
	for _, ws := range plan.Workstreams {
		for _, aid := range approachesByWorkstream[ws.ID] {
			if entry, err := store.Load(aid); err == nil {
				entry.Status = state.StatusInProgress
				entry.Message = "dispatched"
				_ = store.Save(entry)
			}
		}
	}

	return runAdoptDispatchAndVerify(ctx, cfg, report, graph, store, plan, resumePoints, evalRunner)
}

// runAdoptDispatchAndVerify covers Phases 7-9: dispatch the plan,
// verify assertions per Approach, archive the plan when every
// workstream reached a terminal state. Both the fresh-adopt and
// resume paths funnel through here. Caller has already ensured the
// plan record + per-Approach state entries are in place (either
// freshly written in Phase 5 or persisted by a prior run we're now
// resuming). The pre-flight pass (Phase 6) is also caller-owned —
// resume skips it because the prior run already executed it.
func runAdoptDispatchAndVerify(
	ctx context.Context,
	cfg AdoptConfig,
	report *AdoptReport,
	graph *spec.SpecGraph,
	store *state.FileStateStore,
	plan *spec.MasterPlan,
	resumePoints map[string]*dispatch.ResumePoint,
	evalRunner *eval.Runner,
) (*AdoptReport, error) {
	wsStore := workstream.NewFileStore(cfg.FS, workstreamsDir, plan.ID)
	approachesByWorkstream := approachesCoveredByWorkstreams(plan)
	approachesByID := indexApproaches(graph)

	results, err := cfg.Dispatch(ctx, plan, cfg.RepoDir, resumePoints)
	if err != nil {
		// Dispatcher returns per-workstream errors via WorkstreamResult; a
		// top-level error means the executor itself failed. Persist what
		// we have and bail.
		report.DispatchedWorkstreams = summariseDispatch(results, approachesByWorkstream)
		return report, fmt.Errorf("dispatch: %w", err)
	}

	for _, wsResult := range results {
		outcome := WorkstreamOutcome{
			WorkstreamID: wsResult.WorkstreamID,
			BranchName:   wsResult.BranchName,
			Dispatched:   wsResult.Success,
		}
		if wsResult.Err != nil {
			outcome.Error = wsResult.Err.Error()
		}

		// Record per-step progress on the workstream record (and roll
		// AgentSessionID up for next-run resume per DJ-074).
		recordStepProgress(wsStore, wsResult)

		coveredIDs := approachesByWorkstream[wsResult.WorkstreamID]

		if !wsResult.Success {
			// Dispatch failed for this workstream — every covered Approach
			// is failed; assertions are skipped.
			for _, aid := range coveredIDs {
				writeFailedState(store, aid, "dispatch failed: "+errString(wsResult.Err))
				outcome.FailedApproaches = append(outcome.FailedApproaches, aid)
			}
			report.DispatchedWorkstreams = append(report.DispatchedWorkstreams, outcome)
			continue
		}

		// Dispatch succeeded — run assertions for each covered Approach.
		for _, aid := range coveredIDs {
			approach := approachesByID[aid]
			assertionResults := runAssertions(ctx, approach, cfg.RepoDir, evalRunner, cfg.FS)
			newArtifactHashes := spec.ComputeArtifactHashes(cfg.FS.ReadFile, approach)

			entry, err := store.Load(aid)
			if err != nil {
				entry = state.ReconciliationState{ApproachID: aid}
			}
			entry.Artifacts = newArtifactHashes
			entry.SpecHash = spec.ComputeSpecHash(approach)
			entry.AssertionResults = assertionResults
			entry.LastReconciled = time.Now()
			if allPassed(assertionResults) {
				entry.Status = state.StatusLive
				entry.Message = "assertions passed"
				outcome.LiveApproaches = append(outcome.LiveApproaches, aid)
			} else {
				entry.Status = state.StatusFailed
				entry.Message = "assertion failure"
				outcome.FailedApproaches = append(outcome.FailedApproaches, aid)
			}
			if err := store.Save(entry); err != nil {
				return report, fmt.Errorf("save state %s: %w", aid, err)
			}
		}

		report.DispatchedWorkstreams = append(report.DispatchedWorkstreams, outcome)
	}

	// Archive on terminal transition.
	if planIsTerminal(report.DispatchedWorkstreams) {
		if err := wsStore.DeletePlan(); err != nil {
			return report, fmt.Errorf("archive plan %s: %w", plan.ID, err)
		}
		report.Archived = append(report.Archived, plan.ID)
	}

	return report, nil
}

// planClassification is the outcome of classifying leftover in-flight
// plans against the current spec + state. Per DJ-074:
//   - All covered Approaches unchanged → resume: PlanToResume holds the
//     persisted MasterPlan; ResumeMap entries point each workstream at
//     its first not-yet-complete step with the persisted SessionID.
//   - Any covered Approach drifted → invalidate: plan deleted, listed
//     in Invalidated.
//   - All covered Approaches already live → archive: plan deleted,
//     listed in Archived.
//
// At most one PlanToResume per classification — multiple leftover plans
// in flight at once is unusual; if found, the first (sorted) resumable
// plan wins and the rest invalidate. This matches the "one in-flight
// adopt" mental model the rest of the system assumes.
type planClassification struct {
	ResumeMap    map[string]*dispatch.ResumePoint
	Invalidated  []string
	Archived     []string
	PlanToResume *spec.MasterPlan
}

// classifyActivePlans inspects every leftover plan subdirectory under
// `.locutus/workstreams/` and decides what to do per DJ-074's resume
// protocol. discardInFlight short-circuits the analysis and forces
// invalidate-and-replan on every plan regardless of drift state — the
// `--discard-in-flight` opt-out for "I know this plan is wrong, start
// over."
//
// graph + store are needed to compute drift: an Approach is considered
// drifted when its current ComputeSpecHash differs from the SpecHash
// recorded on the prior run's state entry, or when the Approach has
// been removed from the spec entirely.
func classifyActivePlans(fsys specio.FS, discardInFlight bool, graph *spec.SpecGraph, store *state.FileStateStore) (*planClassification, error) {
	out := &planClassification{ResumeMap: map[string]*dispatch.ResumePoint{}}

	planIDs, err := workstream.ListActivePlans(fsys, workstreamsDir)
	if err != nil {
		return out, err
	}
	if len(planIDs) == 0 {
		return out, nil
	}

	// On --discard-in-flight, every plan invalidates regardless of drift.
	if discardInFlight {
		for _, id := range planIDs {
			ws := workstream.NewFileStore(fsys, workstreamsDir, id)
			if err := ws.DeletePlan(); err != nil {
				return out, fmt.Errorf("delete stale plan %s: %w", id, err)
			}
			out.Invalidated = append(out.Invalidated, id)
		}
		return out, nil
	}

	for _, id := range planIDs {
		ws := workstream.NewFileStore(fsys, workstreamsDir, id)
		planRec, perr := ws.LoadPlan()
		if perr != nil {
			// Corrupt or missing plan.yaml — invalidate; no shape to resume from.
			_ = ws.DeletePlan()
			out.Invalidated = append(out.Invalidated, id)
			continue
		}
		records, rerr := ws.Walk()
		if rerr != nil || len(records) == 0 {
			_ = ws.DeletePlan()
			out.Invalidated = append(out.Invalidated, id)
			continue
		}

		action := classifyPlanAction(records, store, graph)
		switch action {
		case planActionArchive:
			_ = ws.DeletePlan()
			out.Archived = append(out.Archived, id)
		case planActionInvalidate:
			_ = ws.DeletePlan()
			out.Invalidated = append(out.Invalidated, id)
		case planActionResume:
			// Already chose another plan to resume → invalidate this one.
			if out.PlanToResume != nil {
				_ = ws.DeletePlan()
				out.Invalidated = append(out.Invalidated, id)
				continue
			}
			// Build ResumePoint per workstream; if any workstream has no
			// resumable shape (missing SessionID or all-steps-complete in
			// a record marked drift-but-not-yet-replanned), fall back to
			// invalidate for safety — partial resume risks burning tokens
			// on an inconsistent state.
			resumeMap := map[string]*dispatch.ResumePoint{}
			resumable := true
			for _, rec := range records {
				rp := buildResumePoint(rec)
				if rp == nil {
					resumable = false
					break
				}
				resumeMap[rec.WorkstreamID] = rp
			}
			if !resumable {
				_ = ws.DeletePlan()
				out.Invalidated = append(out.Invalidated, id)
				continue
			}
			plan := planRec.Plan
			out.PlanToResume = &plan
			out.ResumeMap = resumeMap
		}
	}

	return out, nil
}

// planAction enumerates the per-plan classifier verdicts.
type planAction int

const (
	planActionInvalidate planAction = iota
	planActionResume
	planActionArchive
)

// classifyPlanAction inspects the records of a single leftover plan and
// returns the verdict. Archive wins iff every covered Approach is live;
// invalidate fires on any drift; otherwise resume.
func classifyPlanAction(records []workstream.ActiveWorkstream, store *state.FileStateStore, graph *spec.SpecGraph) planAction {
	allLive := true
	for _, rec := range records {
		for _, aid := range rec.ApproachIDs {
			approach := graph.Approach(aid)
			if approach == nil {
				return planActionInvalidate // Approach removed from spec.
			}
			entry, err := store.Load(aid)
			if err != nil {
				// No state entry for a covered Approach: the prior run
				// never reached the persistence step. Treat as drift.
				return planActionInvalidate
			}
			if entry.SpecHash == "" || entry.SpecHash != spec.ComputeSpecHash(*approach) {
				return planActionInvalidate
			}
			if entry.Status != state.StatusLive {
				allLive = false
			}
		}
	}
	if allLive {
		return planActionArchive
	}
	return planActionResume
}

// buildResumePoint walks an ActiveWorkstream's StepStatus and returns a
// ResumePoint for the first step that is not yet StepComplete. Returns
// nil when the workstream has no AgentSessionID (resume target needs
// one) or when every step is already complete (no work left for this
// workstream — caller's classifier should have routed to archive in
// that case).
func buildResumePoint(rec workstream.ActiveWorkstream) *dispatch.ResumePoint {
	if rec.AgentSessionID == "" {
		return nil
	}
	for _, step := range rec.Plan.Steps {
		progress := rec.StepByID(step.ID)
		if progress.Status != workstream.StepComplete {
			return &dispatch.ResumePoint{
				StepID:    step.ID,
				SessionID: rec.AgentSessionID,
			}
		}
	}
	return nil
}

func loadSpecGraph(fsys specio.FS) (*spec.SpecGraph, error) {
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	bugs, _ := collectObjects[spec.Bug](fsys, ".borg/spec/bugs")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
	approaches, _ := collectMarkdown[spec.Approach](fsys, ".borg/spec/approaches")

	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		if err := json.Unmarshal(data, &traces); err != nil {
			slog.Warn("traces.json unmarshal failed; proceeding without traces", "error", err)
		}
	}
	return spec.BuildGraph(features, bugs, decisions, strategies, approaches, traces), nil
}

func filterByScope(cs []reconcile.Classification, g *spec.SpecGraph, scope string) []reconcile.Classification {
	inScope := make(map[string]bool)
	for _, a := range g.ApproachesUnder(scope) {
		inScope[a.ID] = true
	}
	out := make([]reconcile.Classification, 0, len(cs))
	for _, c := range cs {
		if inScope[c.Approach.ID] {
			out = append(out, c)
		}
	}
	return out
}

// writePlannedState persists a `planned` snapshot for every candidate. Keeps
// the older test fixture behaviour (no-LLM run lands here and exits).
func writePlannedState(store *state.FileStateStore, cs []reconcile.Classification) error {
	for _, c := range cs {
		if c.Status == state.StatusLive || c.Status == state.StatusOutOfSpec {
			continue
		}
		entry := state.ReconciliationState{
			ApproachID:     c.Approach.ID,
			SpecHash:       c.CurrentHash,
			Artifacts:      c.StoredFiles,
			Status:         state.StatusPlanned,
			Message:        "queued for adoption",
			LastReconciled: time.Now(),
		}
		if err := store.Save(entry); err != nil {
			return fmt.Errorf("saving state for %s: %w", c.Approach.ID, err)
		}
	}
	return nil
}

// runPlannerForCandidates seeds the planner with the drift set plus the
// transitively reachable non-live dependencies (DJ-068). Caller guarantees
// candidates is non-empty.
func runPlannerForCandidates(
	ctx context.Context,
	cfg AdoptConfig,
	graph *spec.SpecGraph,
	candidates []reconcile.Classification,
) (*spec.MasterPlan, error) {
	seedIDs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		seedIDs = append(seedIDs, c.Approach.ID)
	}
	nonLiveApproach := func(n spec.GraphNode) bool {
		return n.Kind == spec.KindApproach
	}
	deps, err := graph.TransitiveDeps(seedIDs, nonLiveApproach)
	if err != nil {
		return nil, fmt.Errorf("transitive deps: %w", err)
	}

	// Load the current spec as flat slices for the PlanRequest.
	features := collectFeatures(graph)
	decisions := collectDecisions(graph)
	strategies := collectStrategies(graph)

	promptIDs := make([]string, 0, len(deps))
	for _, d := range deps {
		promptIDs = append(promptIDs, d.ID)
	}
	sort.Strings(promptIDs)
	prompt := fmt.Sprintf("Plan reconciliation for Approaches: %v. Candidates were: %v.", promptIDs, seedIDs)

	req := agent.PlanRequest{
		Prompt:     prompt,
		Features:   features,
		Decisions:  decisions,
		Strategies: strategies,
	}

	// GOALS.md lives at the project root.
	if data, err := cfg.FS.ReadFile("GOALS.md"); err == nil {
		req.GoalsBody = string(data)
	}

	approachesByID := make(map[string]spec.Approach, len(deps))
	for _, d := range deps {
		if d.Kind == spec.KindApproach {
			if a := graph.Approach(d.ID); a != nil {
				approachesByID[d.ID] = *a
			}
		}
	}

	return planWithOverlapRetry(ctx, cfg.Plan, req, approachesByID)
}

// maxOverlapRetries caps how many times the planner is asked to
// restructure on file overlap before adopt errors out (DJ-030).
const maxOverlapRetries = 3

// planWithOverlapRetry runs the planner, checks for inter-workstream file
// overlap, and on conflict re-asks the planner with the overlap report
// embedded in the prompt. After maxOverlapRetries unsuccessful attempts,
// surfaces the persistent overlap as an error so the operator can
// intervene.
func planWithOverlapRetry(ctx context.Context, planFn PlanFunc, req agent.PlanRequest, approachesByID map[string]spec.Approach) (*spec.MasterPlan, error) {
	currentReq := req
	for attempt := 0; attempt <= maxOverlapRetries; attempt++ {
		plan, err := planFn(ctx, currentReq)
		if err != nil {
			return nil, err
		}
		reports := overlap.Detect(plan, approachesByID)
		if len(reports) == 0 {
			return plan, nil
		}
		if attempt == maxOverlapRetries {
			return nil, fmt.Errorf(
				"planner produced overlapping workstreams after %d retries:\n%s",
				maxOverlapRetries, overlap.FormatReports(reports),
			)
		}
		currentReq = augmentWithOverlapFeedback(currentReq, reports)
	}
	// unreachable — loop returns or errors on every iteration
	return nil, fmt.Errorf("plan retry loop exited unexpectedly")
}

// augmentWithOverlapFeedback appends an "Overlap conflicts" section to
// the planner prompt so the next call sees the conflict list. Two valid
// resolutions are spelled out: merge the conflicting workstreams (single
// agent session) or add a depends_on edge (sequential execution).
func augmentWithOverlapFeedback(req agent.PlanRequest, reports []overlap.Report) agent.PlanRequest {
	req.Prompt += "\n\n## Overlap conflicts (must restructure)\n\n" +
		"The previous plan produced file overlaps between parallel workstreams. " +
		"Restructure to eliminate these — either merge the conflicting workstreams " +
		"into one (so a single agent owns the writes) or add a `depends_on` edge so " +
		"the workstreams execute sequentially.\n\n" +
		overlap.FormatReports(reports)
	return req
}

// approachesCoveredByWorkstreams computes the unique Approach set each
// workstream touches via its PlanSteps. Callers use this map to stamp
// WorkstreamID on state entries and to know which Approaches to verify.
func approachesCoveredByWorkstreams(plan *spec.MasterPlan) map[string][]string {
	out := make(map[string][]string, len(plan.Workstreams))
	for _, ws := range plan.Workstreams {
		seen := make(map[string]struct{})
		var covered []string
		for _, step := range ws.Steps {
			if step.ApproachID == "" {
				continue
			}
			if _, ok := seen[step.ApproachID]; ok {
				continue
			}
			seen[step.ApproachID] = struct{}{}
			covered = append(covered, step.ApproachID)
		}
		sort.Strings(covered)
		out[ws.ID] = covered
	}
	return out
}

func indexApproaches(g *spec.SpecGraph) map[string]spec.Approach {
	out := make(map[string]spec.Approach)
	for id, n := range g.Nodes() {
		if n.Kind != spec.KindApproach {
			continue
		}
		if a := g.Approach(id); a != nil {
			out[id] = *a
		}
	}
	return out
}

func collectFeatures(g *spec.SpecGraph) []spec.Feature {
	var out []spec.Feature
	for id, n := range g.Nodes() {
		if n.Kind == spec.KindFeature {
			if f := g.Feature(id); f != nil {
				out = append(out, *f)
			}
		}
	}
	return out
}

func collectDecisions(g *spec.SpecGraph) []spec.Decision {
	var out []spec.Decision
	for id, n := range g.Nodes() {
		if n.Kind == spec.KindDecision {
			if d := g.Decision(id); d != nil {
				out = append(out, *d)
			}
		}
	}
	return out
}

func collectStrategies(g *spec.SpecGraph) []spec.Strategy {
	var out []spec.Strategy
	for id, n := range g.Nodes() {
		if n.Kind == spec.KindStrategy {
			if s := g.Strategy(id); s != nil {
				out = append(out, *s)
			}
		}
	}
	return out
}

// recordStepProgress translates a WorkstreamResult's StepOutcomes into the
// workstream record's StepProgress map. Step IDs aren't carried on
// StepOutcome (the dispatcher iterates in plan order and breaks on
// failure), so we zip by index against the persisted workstream's Steps.
// If the record was deleted (edge case during cleanup), this is a no-op.
func recordStepProgress(store *workstream.FileStore, r *dispatch.WorkstreamResult) {
	rec, err := store.Load(r.WorkstreamID)
	if err != nil {
		return
	}
	for i, so := range r.StepResults {
		if so == nil || i >= len(rec.Plan.Steps) {
			continue
		}
		progress := workstream.StepProgress{StepID: rec.Plan.Steps[i].ID}
		if so.Success {
			progress.Status = workstream.StepComplete
		} else {
			progress.Status = workstream.StepFailed
			if so.Escalation != "" {
				progress.Message = so.Escalation
			}
		}
		rec.RecordProgress(progress)
	}
	// Persist the streaming-driver session ID surfaced by runWorkstream
	// (DJ-074). On a future adopt invocation, the resume classifier reads
	// this field to populate ResumePoint.SessionID for re-attaching to the
	// same agent conversation.
	if r.AgentSessionID != "" {
		rec.AgentSessionID = r.AgentSessionID
	}
	_ = store.Save(rec)
}

func writeFailedState(store *state.FileStateStore, approachID, message string) {
	entry, err := store.Load(approachID)
	if err != nil {
		entry = state.ReconciliationState{ApproachID: approachID}
	}
	entry.Status = state.StatusFailed
	entry.Message = message
	entry.LastReconciled = time.Now()
	_ = store.Save(entry)
}

func errString(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}

// summariseDispatch is a best-effort rollup when the dispatcher returned a
// top-level error mid-execution. We can still report whatever results came
// back before the error.
func summariseDispatch(results []*dispatch.WorkstreamResult, covered map[string][]string) []WorkstreamOutcome {
	out := make([]WorkstreamOutcome, 0, len(results))
	for _, r := range results {
		if r == nil {
			continue
		}
		outcome := WorkstreamOutcome{WorkstreamID: r.WorkstreamID, Dispatched: r.Success, BranchName: r.BranchName}
		if r.Err != nil {
			outcome.Error = r.Err.Error()
		}
		outcome.FailedApproaches = append(outcome.FailedApproaches, covered[r.WorkstreamID]...)
		out = append(out, outcome)
	}
	return out
}

// planIsTerminal returns true when every dispatched workstream produced only
// live Approaches and no failures. Only then does the plan get archived.
func planIsTerminal(outcomes []WorkstreamOutcome) bool {
	if len(outcomes) == 0 {
		return false
	}
	for _, o := range outcomes {
		if !o.Dispatched || len(o.FailedApproaches) > 0 {
			return false
		}
	}
	return true
}

func hasAnyFailedWorkstream(r *AdoptReport) bool {
	for _, o := range r.DispatchedWorkstreams {
		if !o.Dispatched || len(o.FailedApproaches) > 0 {
			return true
		}
	}
	return false
}

// --- Real planner + dispatcher constructors used by the CLI ---

func realPlan(llm agent.LLM, fsys specio.FS) PlanFunc {
	return func(ctx context.Context, req agent.PlanRequest) (*spec.MasterPlan, error) {
		return agent.Plan(ctx, llm, fsys, req)
	}
}

func realDispatch(llm agent.LLM, fsys specio.FS) DispatchFunc {
	return func(ctx context.Context, plan *spec.MasterPlan, repoDir string, resume map[string]*dispatch.ResumePoint) ([]*dispatch.WorkstreamResult, error) {
		wsStore := workstream.NewFileStore(fsys, workstreamsDir, plan.ID)
		d := &dispatch.Dispatcher{
			LLM:            llm,
			FastLLM:        llm, // same provider for now; upgrade to a fast-tier model later
			OnStepComplete: persistStepProgress(wsStore),
		}
		return d.Dispatch(ctx, plan, repoDir, resume)
	}
}

// persistStepProgress returns a dispatch.StepCompleteHandler that records
// each PlanStep's outcome on the ActiveWorkstream record as soon as the
// dispatcher emits it (per DJ-073). A SIGKILL after this handler returns
// for step N still leaves StepStatus[N] = complete on disk, so the next
// adopt's buildResumePoint resumes at step N+1 instead of replaying the
// whole workstream.
//
// Errors are logged and swallowed: the handler is best-effort progress
// tracking, not the source of truth (the git feature branch is). A Save
// failure after a successful merge is recoverable on retry — the agent
// re-runs the step via --resume, CommitIfChanges sees no diff, and the
// next OnStepComplete invocation re-saves with the same status.
func persistStepProgress(wsStore *workstream.FileStore) dispatch.StepCompleteHandler {
	return func(ctx context.Context, evt dispatch.StepEvent) {
		rec, err := wsStore.Load(evt.WorkstreamID)
		if err != nil {
			slog.Warn("step progress: load failed",
				"workstream_id", evt.WorkstreamID,
				"step_id", evt.StepID,
				"error", err,
			)
			return
		}
		now := time.Now()
		progress := workstream.StepProgress{
			StepID:  evt.StepID,
			Status:  workstream.StepComplete,
			EndedAt: &now,
			Message: evt.Message,
		}
		if !evt.Success {
			progress.Status = workstream.StepFailed
		}
		rec.RecordProgress(progress)
		if evt.SessionID != "" {
			rec.AgentSessionID = evt.SessionID
		}
		if err := wsStore.Save(rec); err != nil {
			slog.Warn("step progress: save failed",
				"workstream_id", evt.WorkstreamID,
				"step_id", evt.StepID,
				"error", err,
			)
		}
	}
}

func summariseClassifications(cs []reconcile.Classification) AdoptSummary {
	var s AdoptSummary
	for _, c := range cs {
		switch c.Status {
		case state.StatusLive:
			s.Live++
		case state.StatusDrifted:
			s.Drifted++
			s.Candidates++
		case state.StatusOutOfSpec:
			s.OutOfSpec++
		case state.StatusUnplanned:
			s.Unplanned++
			s.Candidates++
		case state.StatusFailed:
			s.Failed++
			s.Candidates++
		case state.StatusInProgress, state.StatusPlanned, state.StatusPreFlight:
			s.InProgress++
			s.Candidates++
		}
	}
	return s
}

func renderAdoptReport(r *AdoptReport) {
	heading := "Adoption plan"
	if r.DryRun {
		heading = "Adoption plan (dry-run)"
	}
	if r.Scope != "" {
		heading += fmt.Sprintf(" — scope %s", r.Scope)
	}
	fmt.Println(heading)
	fmt.Println()

	if len(r.Classifications) == 0 {
		fmt.Println("  No Approach nodes found. Nothing to reconcile.")
		return
	}

	for _, c := range r.Classifications {
		fmt.Printf("  %-12s  %s\n", c.Status, c.Approach.ID)
	}
	fmt.Println()
	fmt.Printf("Summary: %d live, %d drifted, %d out_of_spec, %d unplanned, %d failed, %d in_progress. %d candidate(s).\n",
		r.Summary.Live, r.Summary.Drifted, r.Summary.OutOfSpec,
		r.Summary.Unplanned, r.Summary.Failed, r.Summary.InProgress, r.Summary.Candidates)

	if len(r.PrereqResults) > 0 {
		fmt.Println()
		status := "ok"
		if !r.PrereqsOK {
			status = "FAILED — adoption will abort"
		}
		fmt.Printf("Prerequisites: %s\n", status)
		for _, p := range r.PrereqResults {
			for _, pr := range p.Passed {
				fmt.Printf("  ok    %s — %s\n", p.StrategyID, pr)
			}
			for _, f := range p.Failed {
				fmt.Printf("  FAIL  %s — %s (%s)\n", p.StrategyID, f.Prerequisite, f.Err)
			}
		}
	}

	if len(r.ResumedInvalidated) > 0 {
		fmt.Println()
		fmt.Printf("Discarded %d prior in-flight plan(s) and replanned: %v\n",
			len(r.ResumedInvalidated), r.ResumedInvalidated)
		fmt.Println("  (True per-session resume is not yet implemented — see DJ-074.")
		fmt.Println("   To inspect in-flight plans before running adopt, use `locutus status --in-flight`.)")
	}

	if r.PlanID != "" {
		fmt.Println()
		fmt.Printf("Dispatched plan %s\n", r.PlanID)
		for _, o := range r.DispatchedWorkstreams {
			icon := "✓"
			if !o.Dispatched {
				icon = "✗"
			}
			fmt.Printf("  %s %s  (%d live, %d failed)\n", icon, o.WorkstreamID, len(o.LiveApproaches), len(o.FailedApproaches))
		}
		if len(r.AssumedDecisions) > 0 {
			fmt.Printf("  Pre-flight created %d assumed Decision(s)\n", len(r.AssumedDecisions))
		}
		if len(r.Archived) > 0 {
			fmt.Printf("  Archived %d completed plan(s)\n", len(r.Archived))
		}
	}

	if r.Summary.OutOfSpec > 0 {
		fmt.Println()
		fmt.Println("One or more Approaches have out_of_spec drift (artifacts changed outside Locutus).")
		fmt.Println("Resolve each by running `locutus refine <id>` to update spec to match, or by")
		fmt.Println("reverting the artifact and re-running `locutus adopt`.")
	}
}
