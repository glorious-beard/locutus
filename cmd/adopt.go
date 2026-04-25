package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/check"
	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/eval"
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
//     `.locutus/workstreams/` are inspected. For this round the MVP
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
	PreflightRounds   int    `help:"Maximum pre-flight clarification rounds per workstream (0 = use default of 3)."`
	MaxConcurrent     int    `help:"Cap total concurrent workstreams. 0 = unlimited." default:"0"`
}

// PlanFunc produces a MasterPlan from a PlanRequest. Injectable so tests
// can run the full pipeline without LLM access.
type PlanFunc func(ctx context.Context, req agent.PlanRequest) (*spec.MasterPlan, error)

// DispatchFunc runs a MasterPlan in the given repo directory and returns
// per-workstream results. Injectable so tests can run the full pipeline
// without git worktrees or subprocess agents.
type DispatchFunc func(ctx context.Context, plan *spec.MasterPlan, repoDir string) ([]*dispatch.WorkstreamResult, error)

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

func (c *AdoptCmd) Run(cli *CLI) error {
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
		cfg.Dispatch = realDispatch(llm)
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
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
		os.Exit(2)
	}
	if report.Summary.OutOfSpec > 0 {
		os.Exit(1)
	}
	if hasAnyFailedWorkstream(report) {
		os.Exit(1)
	}
	return nil
}

// RunAdopt preserves the original signature for backward compat (MCP
// handler + older tests). It runs classification + prereq gate + planned
// state writes; if you want the full dispatch pipeline, use
// RunAdoptWithConfig with a Plan + Dispatch wired in.
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

	// --- Phase 1: Resume protocol (DJ-073) ---
	// MVP: invalidate and replan on every detected active plan. True resume
	// via coding-agent --resume is deferred to a later round.
	if !cfg.DryRun {
		invalidated, err := resumeOrInvalidateActivePlans(cfg.FS)
		if err != nil {
			return report, fmt.Errorf("resume: %w", err)
		}
		report.ResumedInvalidated = invalidated
	}

	// --- Phase 2: Classification + scope + prereqs ---
	graph, err := loadSpecGraph(cfg.FS)
	if err != nil {
		return report, err
	}
	store := state.NewFileStateStore(cfg.FS, ".locutus/state")

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

	// --- Phase 7: Dispatch ---
	results, err := cfg.Dispatch(ctx, plan, cfg.RepoDir)
	if err != nil {
		// Dispatcher returns per-workstream errors via WorkstreamResult; a
		// top-level error means the executor itself failed. Persist what
		// we have and bail.
		report.DispatchedWorkstreams = summariseDispatch(results, approachesByWorkstream)
		return report, fmt.Errorf("dispatch: %w", err)
	}

	// --- Phase 8: Verify assertions, record StepProgress, flip state ---
	for _, wsResult := range results {
		outcome := WorkstreamOutcome{
			WorkstreamID: wsResult.WorkstreamID,
			BranchName:   wsResult.BranchName,
			Dispatched:   wsResult.Success,
		}
		if wsResult.Err != nil {
			outcome.Error = wsResult.Err.Error()
		}

		// Record per-step progress on the workstream record.
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

	// --- Phase 9: Archive on terminal transition ---
	if planIsTerminal(report.DispatchedWorkstreams) {
		if err := wsStore.DeletePlan(); err != nil {
			return report, fmt.Errorf("archive plan %s: %w", plan.ID, err)
		}
		report.Archived = append(report.Archived, plan.ID)
	}

	return report, nil
}

// resumeOrInvalidateActivePlans finds any leftover plan subdirectories
// under `.locutus/workstreams/` and deletes them, returning the list of
// plan IDs that were wiped. MVP behavior for the DJ-073 resume protocol —
// true agent-session resume is a future round.
func resumeOrInvalidateActivePlans(fsys specio.FS) ([]string, error) {
	planIDs, err := workstream.ListActivePlans(fsys, workstreamsDir)
	if err != nil {
		return nil, err
	}
	if len(planIDs) == 0 {
		return nil, nil
	}
	var invalidated []string
	for _, id := range planIDs {
		ws := workstream.NewFileStore(fsys, workstreamsDir, id)
		if err := ws.DeletePlan(); err != nil {
			return invalidated, fmt.Errorf("delete stale plan %s: %w", id, err)
		}
		invalidated = append(invalidated, id)

		// Any state entries still pointing at this plan should have their
		// workstream pointer cleared per the DJ-072 clear-on-drift rule —
		// the prior plan is gone, so the reference is stale.
		store := state.NewFileStateStore(fsys, ".locutus/state")
		entries, _ := store.Walk()
		for _, e := range entries {
			if e.WorkstreamID == "" {
				continue
			}
			// We don't know which workstream IDs belonged to this plan
			// without reading the record (which we just deleted), so we
			// conservatively leave state entries alone — their pointers
			// become historical. The next classification will re-queue
			// anything still drifted, producing a fresh WorkstreamID on
			// the next planner run.
		}
	}
	return invalidated, nil
}

func loadSpecGraph(fsys specio.FS) (*spec.SpecGraph, error) {
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	bugs, _ := collectObjects[spec.Bug](fsys, ".borg/spec/bugs")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
	approaches, _ := collectMarkdown[spec.Approach](fsys, ".borg/spec/approaches")

	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		_ = json.Unmarshal(data, &traces)
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

	return cfg.Plan(ctx, req)
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

func realDispatch(llm agent.LLM) DispatchFunc {
	return func(ctx context.Context, plan *spec.MasterPlan, repoDir string) ([]*dispatch.WorkstreamResult, error) {
		d := &dispatch.Dispatcher{
			LLM:     llm,
			FastLLM: llm, // same provider for now; upgrade to a fast-tier model later
		}
		return d.Dispatch(ctx, plan, repoDir)
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
