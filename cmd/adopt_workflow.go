package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/check"
	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/eval"
	"github.com/chetan/locutus/internal/preflight"
	"github.com/chetan/locutus/internal/reconcile"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
	"github.com/chetan/locutus/internal/workstream"
)

// AdoptState is the workflow blackboard for the full adopt reconcile
// loop (Phase 10 of workflow unification). It carries every input and
// output the eight-step workflow needs:
//
//   - Inputs are populated once by buildAdoptState before the workflow
//     runs (Cfg, FSys/SynthFS, dispatcher + agent defs, store, eval
//     runner, initial graph).
//   - Per-step outputs accumulate as the workflow progresses
//     (Synthesized, PlanToResume, Classifications, Plan, ...). The
//     output sidecar lives on State directly so single-shot RunItem
//     closures can mutate it through pointer-bearing fields — the
//     workflow's steps are all sequential (DependsOn chain) so this
//     is race-free even though Snapshot value-copies the state.
//   - The final AdoptReport is harvested from these fields after the
//     workflow returns.
//
// The eight workflow steps mirror the legacy phased flow of
// RunAdoptWithConfig:
//
//   1. synthesize_missing      — LLM, fanout (synthesizer)
//   2. regenerate_invalidated  — LLM, fanout (approach-regenerator,
//                                ReAct via Phase 6)
//   3. resume_classify         — no-LLM; classifyActivePlans for the
//                                DJ-073/074 resume protocol
//   4. classify                — no-LLM; reconcile.Classify + scope +
//                                check.CheckPrereqs + writePlannedState
//   5. plan_candidates         — LLM; runPlannerForCandidates with the
//                                DJ-030 overlap-retry envelope
//   6. persist_plan            — no-LLM; wsStore.SavePlan + per-Approach
//                                state flips to pre_flight
//   7. preflight               — LLM, fanout; preflight.Preflight per
//                                workstream; merge flips per-Approach
//                                state from pre_flight to in_progress
//   8. dispatch_and_verify     — mixed; cfg.Dispatch + per-Approach
//                                assertions + plan archive
//
// Conditional gating handles every short-circuit (dry-run, prereq
// failure, no candidates, no-LLM, resumed plan). No early-return paths
// — every adopt invocation runs the same 8-step DAG.
type AdoptState struct {
	// Inputs (set once by buildAdoptState; never mutated by steps).
	Cfg            AdoptConfig
	FSys           specio.FS // cfg.FS — writes go here
	SynthFS        specio.FS // dry-run wraps cfg.FS read-only; matches assimilate
	Dispatcher     agent.AgentDispatcher
	SynthesizerDef agent.AgentDef
	RegeneratorDef agent.AgentDef
	EvalRunner     *eval.Runner
	Store          *state.FileStateStore

	// Graph + Loaded are read by multiple steps and refreshed once
	// after synthesize_missing / regenerate_invalidated mutated the
	// approaches directory.
	Graph  *spec.SpecGraph
	Loaded *spec.Loaded

	// Step outputs. Each step writes its own fields; downstream steps
	// gate on these via Conditional, and harvestAdoptReport rolls
	// them up into the returned AdoptReport.
	Synthesized            []string
	Regenerated            []string
	ResumedInvalidated     []string
	Archived               []string
	ResumePoints           map[string]*dispatch.ResumePoint
	PlanToResume           *spec.MasterPlan
	Classifications        []reconcile.Classification
	Summary                AdoptSummary
	PrereqResults          []check.Result
	PrereqsOK              bool
	Plan                   *spec.MasterPlan
	ApproachesByWorkstream map[string][]string
	PreflightResolutions   []preflight.Resolution
	AssumedDecisions       []string
	DispatchedWorkstreams  []WorkstreamOutcome
}

// snapshotAdoptState shallow-copies state for parallel-safe reads.
// Pointer fields (FS, Dispatcher, Store, Graph, Loaded, Plan,
// PlanToResume) and the output slices are shared by reference; this
// is safe because the workflow is sequential (no Parallel=true step
// runs alongside another step that writes the same field, and the
// fanout steps only read inputs / write isolated per-item outputs
// the merge handler aggregates after the fanout drains).
func snapshotAdoptState(s *AdoptState) AdoptState {
	if s == nil {
		return AdoptState{}
	}
	return *s
}

// AdoptWorkflow is the eight-step DAG covering Phase 0 through plan
// archive. MaxRounds=1 — adopt is a single-pass workflow with no
// convergence loop.
var AdoptWorkflow = &agent.Workflow[AdoptState]{
	Snapshot: snapshotAdoptState,
	Rounds: []agent.WorkflowStep[AdoptState]{
		{
			ID:          "synthesize_missing",
			Agents:      []string{"synthesizer"},
			Conditional: condSynthRunnable,
			Fanout:      fanoutAdoptSynthesize,
			RunItem:     runAdoptSynthesizeItem,
			Merge:       mergeAdoptSynthesize,
		},
		{
			ID:          "regenerate_invalidated",
			Agents:      []string{"approach-regenerator"},
			DependsOn:   []string{"synthesize_missing"},
			Conditional: condRegenRunnable,
			Fanout:      fanoutAdoptRegenerate,
			RunItem:     runAdoptRegenerateItem,
			Merge:       mergeAdoptRegenerate,
		},
		{
			ID:          "resume_classify",
			Agents:      []string{"adopt-resume-classifier"},
			DependsOn:   []string{"regenerate_invalidated"},
			Conditional: condResumeClassifyRunnable,
			RunItem:     runAdoptResumeClassifyItem,
		},
		{
			ID:          "classify",
			Agents:      []string{"adopt-reconcile-classifier"},
			DependsOn:   []string{"resume_classify"},
			Conditional: condReconcileClassifyRunnable,
			RunItem:     runAdoptClassifyItem,
		},
		{
			ID:          "plan_candidates",
			Agents:      []string{"planner"},
			DependsOn:   []string{"classify"},
			Conditional: condPlanCandidatesRunnable,
			RunItem:     runAdoptPlanCandidatesItem,
		},
		{
			ID:          "persist_plan",
			Agents:      []string{"adopt-plan-persister"},
			DependsOn:   []string{"plan_candidates"},
			Conditional: condPlanReady,
			RunItem:     runAdoptPersistPlanItem,
		},
		{
			ID:          "preflight",
			Agents:      []string{"preflight"},
			DependsOn:   []string{"persist_plan"},
			Conditional: condPreflightRunnable,
			Fanout:      fanoutAdoptPreflight,
			RunItem:     runAdoptPreflightItem,
			Merge:       mergeAdoptPreflight,
		},
		{
			ID:          "dispatch_and_verify",
			Agents:      []string{"adopt-dispatcher"},
			DependsOn:   []string{"preflight"},
			Conditional: condPlanReady,
			RunItem:     runAdoptDispatchAndVerifyItem,
		},
	},
	MaxRounds: 1,
}

// --- Conditionals ----------------------------------------------------

// condSynthRunnable: the synth step needs an LLM dispatcher. When
// adopt runs without one (the older RunAdopt test path), the step
// no-ops.
func condSynthRunnable(s *AdoptState) bool {
	return s != nil && s.Dispatcher != nil
}

// condRegenRunnable: regen also needs the dispatcher + a loaded spec
// snapshot. Loaded is populated by buildAdoptState whenever the
// dispatcher is — they travel together.
func condRegenRunnable(s *AdoptState) bool {
	return s != nil && s.Dispatcher != nil && s.Loaded != nil
}

// condResumeClassifyRunnable: skip the resume protocol on dry-run
// (matches the legacy "if !cfg.DryRun" guard at adopt.go:285).
func condResumeClassifyRunnable(s *AdoptState) bool {
	return s != nil && !s.Cfg.DryRun
}

// condReconcileClassifyRunnable: skip when the resume step already
// picked up a plan — Phases 2-6 of the legacy flow short-circuit in
// that case (adopt.go:302-306).
func condReconcileClassifyRunnable(s *AdoptState) bool {
	return s != nil && s.PlanToResume == nil
}

// condPlanCandidatesRunnable: planner only fires on a non-dry-run
// with prereqs green, at least one drift candidate, and a real plan
// func wired. PlanToResume==nil is already implied by the upstream
// classify step's Conditional gating its writes, but check it
// explicitly so the workflow shape stays readable.
func condPlanCandidatesRunnable(s *AdoptState) bool {
	if s == nil || s.Cfg.DryRun || s.PlanToResume != nil || !s.PrereqsOK {
		return false
	}
	if s.Cfg.Plan == nil || s.Cfg.Dispatch == nil {
		return false
	}
	candidates := reconcile.PlanCandidates(s.Classifications)
	return len(candidates) > 0
}

// condPlanReady: persist_plan + dispatch_and_verify run whenever a
// Plan is in hand — either freshly produced by plan_candidates or
// inherited from PlanToResume via resume_classify.
func condPlanReady(s *AdoptState) bool {
	return s != nil && s.Plan != nil
}

// condPreflightRunnable: only fire pre-flight on a fresh plan. A
// resumed plan already executed its pre-flight in the prior run; the
// legacy code path skipped Phase 6 in that case via the early-return
// at adopt.go:302-306.
func condPreflightRunnable(s *AdoptState) bool {
	return s != nil && s.Plan != nil && s.PlanToResume == nil
}

// --- Step 1: synthesize_missing -------------------------------------

// adoptSynthFanoutItem encodes one parent missing an Approach.
type adoptSynthFanoutItem struct {
	AgentID string   `json:"agent_id"`
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Prose   string   `json:"prose"`
	Deps    []string `json:"deps,omitempty"`
}

// fanoutAdoptSynthesize emits one item per feature / strategy that
// has no Approach child in the in-scope subset of the graph.
// Ordering: alphabetical by parent id, deterministic across runs.
func fanoutAdoptSynthesize(s *AdoptState) ([]string, error) {
	if s == nil || s.Graph == nil {
		return nil, nil
	}
	parents := parentsMissingApproaches(s.SynthFS, s.Graph, s.Cfg.Scope)
	if len(parents) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(parents))
	for _, p := range parents {
		raw, err := json.Marshal(adoptSynthFanoutItem{
			AgentID: "synthesizer",
			Kind:    string(p.Kind),
			ID:      p.ID,
			Title:   p.Title,
			Prose:   p.Prose,
			Deps:    p.Decisions,
		})
		if err != nil {
			return nil, fmt.Errorf("adopt synth fanout marshal: %w", err)
		}
		out = append(out, string(raw))
	}
	return out, nil
}

// runAdoptSynthesizeItem dispatches the synthesizer for one parent,
// writes the new Approach, and attaches it to the parent's
// Approaches slice. Returns the synthesized Approach ID as the
// RoundResult.Output so the merge can re-collect it deterministically.
func runAdoptSynthesizeItem(ctx context.Context, snap agent.StateSnapshot[AdoptState]) (string, error) {
	var item adoptSynthFanoutItem
	if err := json.Unmarshal([]byte(snap.FanoutItem), &item); err != nil {
		return "", fmt.Errorf("adopt synth: parse fanout item: %w", err)
	}
	st := snap.State
	if st.Dispatcher == nil {
		return "", fmt.Errorf("adopt synth: dispatcher missing on state")
	}

	approachID := "app-" + item.ID
	approach := spec.Approach{
		ID:        approachID,
		Title:     item.Title,
		ParentID:  item.ID,
		Decisions: item.Deps,
	}
	applicable := applicableDecisionsFor(st.Graph, item.Deps)

	prompt := buildSynthesizerPrompt(approach, parentContext{
		Kind: spec.NodeKind(item.Kind), ID: item.ID, Title: item.Title,
		Prose: item.Prose, Decisions: item.Deps,
	}, applicable, "")

	input := agent.AgentInput{Messages: []agent.Message{{Role: "user", Content: prompt}}}
	resp, err := st.Dispatcher.Dispatch(ctx, st.SynthesizerDef, input, agent.DispatchOptions{Role: "synthesizer"})
	if err != nil {
		return "", fmt.Errorf("adopt synth %s: %w", item.ID, err)
	}
	var rw cascade.RewriteResult
	if err := agent.UnmarshalAgentOutput(resp.Content, &rw); err != nil {
		return "", fmt.Errorf("adopt synth %s: parse: %w", item.ID, err)
	}
	approach.Body = rw.RevisedBody
	approach.CreatedAt = time.Now()
	approach.UpdatedAt = approach.CreatedAt

	if err := specio.SaveMarkdown(st.SynthFS, ".borg/spec/approaches/"+approachID+".md", approach, rw.RevisedBody); err != nil {
		return "", fmt.Errorf("adopt synth %s: persist: %w", item.ID, err)
	}
	if err := attachApproachToParent(st.SynthFS, parentContext{
		Kind: spec.NodeKind(item.Kind), ID: item.ID,
	}, approachID); err != nil {
		return "", fmt.Errorf("adopt synth %s: attach: %w", item.ID, err)
	}
	return approachID, nil
}

// mergeAdoptSynthesize collects every per-item Output (the approach id)
// into state.Synthesized in fanout order. Failures land as RoundResult
// errors; surface them via slog rather than aborting the workflow so
// adopt's broader reconcile loop sees at least the successful
// approaches and can decide whether to proceed.
func mergeAdoptSynthesize(s *AdoptState, results []agent.RoundResult) {
	if s == nil {
		return
	}
	for _, r := range results {
		if r.Err != nil {
			slog.Warn("adopt synth: per-parent synthesis failed", "error", r.Err)
			continue
		}
		id := r.Output
		if id == "" {
			continue
		}
		s.Synthesized = append(s.Synthesized, id)
	}
}

// --- Step 2: regenerate_invalidated ---------------------------------

// adoptRegenFanoutItem encodes one Approach scheduled for regeneration.
type adoptRegenFanoutItem struct {
	AgentID    string `json:"agent_id"`
	ApproachID string `json:"approach_id"`
	EventID    string `json:"event_id"`
}

// fanoutAdoptRegenerate emits one item per Approach with a readable
// supersede event. Approaches whose event file is missing are filtered
// out with a slog.Warn — the legacy regenerateInvalidatedApproaches
// soft-degraded on missing events; preserving the same behavior keeps
// equivalence-by-construction with the prior tests.
func fanoutAdoptRegenerate(s *AdoptState) ([]string, error) {
	if s == nil || s.Loaded == nil {
		return nil, nil
	}
	invalidated := invalidatedApproaches(s.Loaded)
	if len(invalidated) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(invalidated))
	for _, app := range invalidated {
		if app.InvalidatedByEventID == "" {
			continue
		}
		_, ok, err := readSupersedeEvent(s.FSys, app.InvalidatedByEventID)
		if err != nil {
			return nil, fmt.Errorf("adopt regen: read event for %s: %w", app.ID, err)
		}
		if !ok {
			slog.Warn("adopt regen: skipping approach with missing supersede event",
				"approach", app.ID, "event_id", app.InvalidatedByEventID)
			continue
		}
		raw, err := json.Marshal(adoptRegenFanoutItem{
			AgentID:    "approach-regenerator",
			ApproachID: app.ID,
			EventID:    app.InvalidatedByEventID,
		})
		if err != nil {
			return nil, fmt.Errorf("adopt regen fanout marshal: %w", err)
		}
		out = append(out, string(raw))
	}
	return out, nil
}

// runAdoptRegenerateItem rebuilds the RegenerateApproachContext for
// one approach, dispatches the ReAct-capable approach-regenerator,
// writes the revised body, and clears InvalidatedByEventID. Returns
// the approach id as RoundResult.Output for the merge to aggregate.
//
// Skips (returns empty id, no error) when the parent context can't be
// resolved — matches the legacy soft-degrade so a single broken
// reference doesn't kill an adopt run.
func runAdoptRegenerateItem(ctx context.Context, snap agent.StateSnapshot[AdoptState]) (string, error) {
	var item adoptRegenFanoutItem
	if err := json.Unmarshal([]byte(snap.FanoutItem), &item); err != nil {
		return "", fmt.Errorf("adopt regen: parse fanout item: %w", err)
	}
	st := snap.State
	if st.Dispatcher == nil {
		return "", fmt.Errorf("adopt regen: dispatcher missing on state")
	}
	if st.Loaded == nil {
		return "", fmt.Errorf("adopt regen: loaded spec missing on state")
	}

	var app *spec.Approach
	for i, n := range st.Loaded.Approaches {
		if n.Spec.ID == item.ApproachID {
			app = &st.Loaded.Approaches[i].Spec
			break
		}
	}
	if app == nil {
		return "", fmt.Errorf("adopt regen: approach %s not in loaded spec", item.ApproachID)
	}

	evt, ok, err := readSupersedeEvent(st.FSys, item.EventID)
	if err != nil {
		return "", fmt.Errorf("adopt regen: re-read event for %s: %w", item.ApproachID, err)
	}
	if !ok {
		slog.Warn("adopt regen: event vanished between fanout and dispatch",
			"approach", item.ApproachID, "event_id", item.EventID)
		return "", nil
	}

	rctx, err := buildRegenerateContext(st.Loaded, *app, evt)
	if err != nil {
		slog.Warn("adopt regen: skipping approach with missing parent context",
			"approach", item.ApproachID, "error", err)
		return "", nil
	}

	result, err := agent.InvokeApproachRegenerator(ctx, st.Dispatcher, st.RegeneratorDef, rctx)
	if err != nil {
		return "", fmt.Errorf("regenerate %s: %w", item.ApproachID, err)
	}

	updated := *app
	updated.Body = result.RevisedBody
	updated.InvalidatedByEventID = ""
	updated.UpdatedAt = time.Now().UTC()

	fp := path.Join(".borg/spec/approaches", updated.ID+".md")
	if err := specio.SaveMarkdown(st.FSys, fp, updated, result.RevisedBody); err != nil {
		return "", fmt.Errorf("adopt regen: persist %s: %w", updated.ID, err)
	}
	return updated.ID, nil
}

// mergeAdoptRegenerate collects regenerated ids into state.Regenerated.
// Per-item failures land as RoundResult errors and surface via slog.
func mergeAdoptRegenerate(s *AdoptState, results []agent.RoundResult) {
	if s == nil {
		return
	}
	for _, r := range results {
		if r.Err != nil {
			slog.Warn("adopt regen: per-approach regen failed", "error", r.Err)
			continue
		}
		if r.Output == "" {
			continue
		}
		s.Regenerated = append(s.Regenerated, r.Output)
	}
}

// --- Step 3: resume_classify (no-LLM) -------------------------------

// runAdoptResumeClassifyItem runs the DJ-073/074 resume classifier on
// any leftover plans under .locutus/workstreams/. On a resumable
// plan, it populates state.PlanToResume and state.ResumePoints and
// also stamps state.Plan so the downstream dispatch_and_verify step
// has a non-nil Plan to operate on.
//
// Also refreshes state.Graph + state.Loaded ahead of the resume
// classifier — the synth/regen steps above may have written new
// approaches that the classifier needs to see.
func runAdoptResumeClassifyItem(ctx context.Context, snap agent.StateSnapshot[AdoptState]) (string, error) {
	// Mutate through the shared pointer fields. The workflow is
	// sequential so there's no concurrent reader on these fields
	// between resume_classify's RunItem and the next step's snap.
	st := pinAdoptState(ctx, snap)
	if st == nil {
		return "", fmt.Errorf("adopt resume_classify: state pointer missing")
	}

	// Reload the spec if synth/regen ran — both write to .borg/spec
	// and downstream classification needs the freshened graph.
	if len(st.Synthesized) > 0 || len(st.Regenerated) > 0 {
		fresh, err := loadSpecGraph(st.FSys)
		if err != nil {
			return "", fmt.Errorf("adopt resume_classify: reload graph: %w", err)
		}
		st.Graph = fresh
	}

	classified, err := classifyActivePlans(st.FSys, st.Cfg.DiscardInFlight, st.Graph, st.Store)
	if err != nil {
		return "", fmt.Errorf("resume: %w", err)
	}
	st.ResumedInvalidated = classified.Invalidated
	st.Archived = append(st.Archived, classified.Archived...)
	st.ResumePoints = classified.ResumeMap
	st.PlanToResume = classified.PlanToResume
	if classified.PlanToResume != nil {
		st.Plan = classified.PlanToResume
	}
	return "", nil
}

// --- Step 4: classify (no-LLM) --------------------------------------

// runAdoptClassifyItem covers Phase 2 of the legacy flow: classify,
// scope-filter, summarise, prereq-check, and (when prereqs pass on a
// non-dry-run) persist planned state for every candidate. All four
// fold into one no-LLM step so the workflow trace has a single
// "classify" phase span instead of four micro-phases.
//
// Side effect: writes state.Classifications, .Summary, .PrereqResults,
// .PrereqsOK. On non-dry-run + prereqs green, also persists planned
// reconciliation state via writePlannedState (the legacy Phase 3).
func runAdoptClassifyItem(ctx context.Context, snap agent.StateSnapshot[AdoptState]) (string, error) {
	st := pinAdoptState(ctx, snap)
	if st == nil {
		return "", fmt.Errorf("adopt classify: state pointer missing")
	}

	classifications, err := reconcile.Classify(st.FSys, st.Graph, st.Store)
	if err != nil {
		return "", fmt.Errorf("classify: %w", err)
	}
	if st.Cfg.Scope != "" {
		classifications = filterByScope(classifications, st.Graph, st.Cfg.Scope)
	}
	st.Classifications = classifications
	st.Summary = summariseClassifications(classifications)

	prereqs, perr := check.CheckPrereqs(st.FSys)
	if perr != nil {
		return "", fmt.Errorf("prereqs: %w", perr)
	}
	st.PrereqResults = prereqs
	st.PrereqsOK = !check.AnyFailed(prereqs)

	if st.Cfg.DryRun || !st.PrereqsOK {
		return "", nil
	}
	if err := writePlannedState(st.Store, classifications); err != nil {
		return "", fmt.Errorf("write planned state: %w", err)
	}
	return "", nil
}

// --- Step 5: plan_candidates (LLM) ----------------------------------

// runAdoptPlanCandidatesItem dispatches the planner with the DJ-030
// overlap-retry envelope and writes the resulting MasterPlan onto
// state.Plan. When the planner returns nil or an empty workstream
// list the step stores no plan; downstream steps gate on
// state.Plan != nil and no-op.
func runAdoptPlanCandidatesItem(ctx context.Context, snap agent.StateSnapshot[AdoptState]) (string, error) {
	st := pinAdoptState(ctx, snap)
	if st == nil {
		return "", fmt.Errorf("adopt plan_candidates: state pointer missing")
	}

	candidates := reconcile.PlanCandidates(st.Classifications)
	plan, err := runPlannerForCandidates(ctx, st.Cfg, st.Graph, candidates)
	if err != nil {
		return "", fmt.Errorf("plan: %w", err)
	}
	if plan == nil || len(plan.Workstreams) == 0 {
		return "", nil
	}
	st.Plan = plan
	return "", nil
}

// --- Step 6: persist_plan (no-LLM) ----------------------------------

// runAdoptPersistPlanItem writes the PlanRecord + ActiveWorkstream
// shards for a freshly-produced plan and flips every covered
// Approach to StatusPreFlight ahead of pre-flight. Resumed plans
// already have their persistence on disk from the prior run; for
// those, this step short-circuits.
func runAdoptPersistPlanItem(ctx context.Context, snap agent.StateSnapshot[AdoptState]) (string, error) {
	st := pinAdoptState(ctx, snap)
	if st == nil {
		return "", fmt.Errorf("adopt persist_plan: state pointer missing")
	}

	st.ApproachesByWorkstream = approachesCoveredByWorkstreams(st.Plan)

	// Resumed plans already have their PlanRecord + ActiveWorkstreams
	// persisted on disk from the prior run.
	if st.PlanToResume != nil {
		return "", nil
	}

	wsStore := workstream.NewFileStore(st.FSys, workstreamsDir, st.Plan.ID)
	if err := wsStore.SavePlan(*st.Plan); err != nil {
		return "", fmt.Errorf("persist plan: %w", err)
	}
	for _, ws := range st.Plan.Workstreams {
		rec := workstream.ActiveWorkstream{
			WorkstreamID: ws.ID,
			PlanID:       st.Plan.ID,
			ApproachIDs:  st.ApproachesByWorkstream[ws.ID],
			Plan:         ws,
		}
		if err := wsStore.Save(rec); err != nil {
			return "", fmt.Errorf("persist workstream %s: %w", ws.ID, err)
		}
		for _, aid := range rec.ApproachIDs {
			entry, err := st.Store.Load(aid)
			if err != nil {
				entry = state.ReconciliationState{ApproachID: aid}
			}
			entry.Status = state.StatusPreFlight
			entry.WorkstreamID = ws.ID
			entry.Message = "pre-flight"
			entry.LastReconciled = time.Now()
			if err := st.Store.Save(entry); err != nil {
				return "", fmt.Errorf("mark %s pre_flight: %w", aid, err)
			}
		}
	}
	return "", nil
}

// --- Step 7: preflight (LLM, fanout) --------------------------------

// adoptPreflightFanoutItem encodes one workstream id for the fanout.
// preflight.Preflight needs the full Workstream value, but state.Plan
// carries the index — the RunItem closure resolves WorkstreamID back
// to the Workstream via state.Plan.Workstreams.
type adoptPreflightFanoutItem struct {
	AgentID      string `json:"agent_id"`
	WorkstreamID string `json:"workstream_id"`
}

// adoptPreflightResult is the per-fanout-item output marshalled into
// RoundResult.Output for the merge handler to aggregate.
type adoptPreflightResult struct {
	WorkstreamID     string                 `json:"workstream_id"`
	Resolutions      []preflight.Resolution `json:"resolutions,omitempty"`
	AssumedDecisions []string               `json:"assumed_decisions,omitempty"`
}

// fanoutAdoptPreflight emits one item per workstream in the current
// plan, in plan order. The fanout produces no items when no plan is
// in hand — Conditional has already gated on state.Plan != nil.
func fanoutAdoptPreflight(s *AdoptState) ([]string, error) {
	if s == nil || s.Plan == nil {
		return nil, nil
	}
	out := make([]string, 0, len(s.Plan.Workstreams))
	for _, ws := range s.Plan.Workstreams {
		raw, err := json.Marshal(adoptPreflightFanoutItem{
			AgentID:      "preflight",
			WorkstreamID: ws.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("adopt preflight fanout marshal: %w", err)
		}
		out = append(out, string(raw))
	}
	return out, nil
}

// runAdoptPreflightItem runs preflight.Preflight for one workstream
// and stamps the corresponding ActiveWorkstream record's PreFlightDone
// flag. The merge handler later collects resolutions + assumed
// decisions across all items and flips per-Approach state from
// pre_flight to in_progress.
func runAdoptPreflightItem(ctx context.Context, snap agent.StateSnapshot[AdoptState]) (string, error) {
	var item adoptPreflightFanoutItem
	if err := json.Unmarshal([]byte(snap.FanoutItem), &item); err != nil {
		return "", fmt.Errorf("adopt preflight: parse fanout item: %w", err)
	}
	st := snap.State
	if st.Plan == nil {
		return "", fmt.Errorf("adopt preflight: plan missing on state")
	}

	var target *spec.Workstream
	for i, ws := range st.Plan.Workstreams {
		if ws.ID == item.WorkstreamID {
			target = &st.Plan.Workstreams[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("adopt preflight: workstream %s not in plan", item.WorkstreamID)
	}

	approachesByID := indexApproaches(st.Graph)
	pfReport, err := preflight.Preflight(ctx, st.Cfg.LLM, st.FSys, st.Graph, st.Store, *target, approachesByID, st.Cfg.PreflightRounds)
	if err != nil {
		return "", fmt.Errorf("preflight %s: %w", item.WorkstreamID, err)
	}

	wsStore := workstream.NewFileStore(st.FSys, workstreamsDir, st.Plan.ID)
	if rec, err := wsStore.Load(item.WorkstreamID); err == nil {
		rec.PreFlightDone = true
		_ = wsStore.Save(rec)
	}

	result := adoptPreflightResult{
		WorkstreamID: item.WorkstreamID,
		Resolutions:  pfReport.Resolutions,
	}
	for _, d := range pfReport.AssumedDecisions {
		result.AssumedDecisions = append(result.AssumedDecisions, d.ID)
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("adopt preflight: marshal result: %w", err)
	}
	return string(out), nil
}

// mergeAdoptPreflight aggregates per-workstream resolutions + assumed
// decision ids and flips per-Approach state entries from pre_flight
// to in_progress ahead of dispatch.
func mergeAdoptPreflight(s *AdoptState, results []agent.RoundResult) {
	if s == nil {
		return
	}
	for _, r := range results {
		if r.Err != nil {
			// Per-workstream pre-flight failures propagate as the
			// step's overall error via Run; the legacy code path
			// used the same fail-fast contract (adopt.go:387-389).
			// Surface via slog so the merge can still aggregate
			// resolutions from any preceding successful items.
			slog.Warn("adopt preflight: per-workstream failed", "error", r.Err)
			continue
		}
		if r.Output == "" {
			continue
		}
		var res adoptPreflightResult
		if err := json.Unmarshal([]byte(r.Output), &res); err != nil {
			slog.Warn("adopt preflight: merge unmarshal", "error", err)
			continue
		}
		s.PreflightResolutions = append(s.PreflightResolutions, res.Resolutions...)
		s.AssumedDecisions = append(s.AssumedDecisions, res.AssumedDecisions...)
	}

	// Flip pre_flight → in_progress for every covered Approach.
	if s.Plan == nil {
		return
	}
	for _, ws := range s.Plan.Workstreams {
		for _, aid := range s.ApproachesByWorkstream[ws.ID] {
			if entry, err := s.Store.Load(aid); err == nil {
				entry.Status = state.StatusInProgress
				entry.Message = "dispatched"
				_ = s.Store.Save(entry)
			}
		}
	}
}

// --- Step 8: dispatch_and_verify (mixed) ----------------------------

// runAdoptDispatchAndVerifyItem covers Phases 7-9: cfg.Dispatch over
// the master plan, per-Approach assertion verification, and plan
// archive on a terminal transition. Mirrors the legacy
// runAdoptDispatchAndVerify body verbatim — the only difference is
// that it reads inputs from AdoptState rather than function args
// and writes results back to state for the harvester.
func runAdoptDispatchAndVerifyItem(ctx context.Context, snap agent.StateSnapshot[AdoptState]) (string, error) {
	st := pinAdoptState(ctx, snap)
	if st == nil {
		return "", fmt.Errorf("adopt dispatch_and_verify: state pointer missing")
	}
	if st.Plan == nil {
		return "", fmt.Errorf("adopt dispatch_and_verify: plan missing on state")
	}
	if st.Cfg.Dispatch == nil {
		// No dispatcher wired: the test paths that exercise classify
		// + plan-only stop here. Matches the legacy "Plan/Dispatch
		// stay nil on dry-run" gate that returned after writePlanned.
		return "", nil
	}

	wsStore := workstream.NewFileStore(st.FSys, workstreamsDir, st.Plan.ID)
	approachesByWorkstream := st.ApproachesByWorkstream
	approachesByID := indexApproaches(st.Graph)

	results, err := st.Cfg.Dispatch(ctx, st.Plan, st.Cfg.RepoDir, st.ResumePoints)
	if err != nil {
		st.DispatchedWorkstreams = summariseDispatch(results, approachesByWorkstream)
		return "", fmt.Errorf("dispatch: %w", err)
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

		recordStepProgress(wsStore, wsResult)

		coveredIDs := approachesByWorkstream[wsResult.WorkstreamID]

		if !wsResult.Success {
			for _, aid := range coveredIDs {
				writeFailedState(st.Store, aid, "dispatch failed: "+errString(wsResult.Err))
				outcome.FailedApproaches = append(outcome.FailedApproaches, aid)
			}
			st.DispatchedWorkstreams = append(st.DispatchedWorkstreams, outcome)
			continue
		}

		for _, aid := range coveredIDs {
			approach := approachesByID[aid]
			assertionResults := runAssertions(ctx, approach, st.Cfg.RepoDir, st.EvalRunner, st.FSys)
			newArtifactHashes := spec.ComputeArtifactHashes(st.FSys.ReadFile, approach)

			entry, err := st.Store.Load(aid)
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
			if err := st.Store.Save(entry); err != nil {
				return "", fmt.Errorf("save state %s: %w", aid, err)
			}
		}

		st.DispatchedWorkstreams = append(st.DispatchedWorkstreams, outcome)
	}

	if planIsTerminal(st.DispatchedWorkstreams) {
		if err := wsStore.DeletePlan(); err != nil {
			return "", fmt.Errorf("archive plan %s: %w", st.Plan.ID, err)
		}
		st.Archived = append(st.Archived, st.Plan.ID)
	}
	return "", nil
}

// --- State-pointer trick --------------------------------------------

// Single-shot no-LLM steps (resume_classify, classify, plan_candidates,
// persist_plan, dispatch_and_verify) need to write back into the
// verb's state with non-JSON-friendly values (Plan *MasterPlan,
// reloaded *spec.SpecGraph, dispatch.ResumePoint maps, etc). The
// executor's StateSnapshot[S] is a value-copy via the workflow's
// Snapshot closure; mutations to its scalar fields don't propagate.
//
// To keep RunItem closures simple, the cmd-layer entry point stashes
// the live *AdoptState pointer on the request context before calling
// executor.Run. Steps recover it via adoptStateFromContext. The
// workflow is sequential (DependsOn chain) so there are no
// concurrent writers; the only parallelism is inside fanout steps,
// and those write to RoundResult.Output and let Merge aggregate
// (Merge already receives *S directly from the executor).
type adoptStateKey struct{}

func contextWithAdoptState(ctx context.Context, st *AdoptState) context.Context {
	return context.WithValue(ctx, adoptStateKey{}, st)
}

func adoptStateFromContext(ctx context.Context) *AdoptState {
	if v := ctx.Value(adoptStateKey{}); v != nil {
		if st, ok := v.(*AdoptState); ok {
			return st
		}
	}
	return nil
}

// pinAdoptState resolves the live *AdoptState for a single-shot step's
// RunItem. The state pointer travels on context per
// contextWithAdoptState; the snap argument is unused but kept so the
// RunItem signature stays uniform with the fanout RunItems above
// (which read snap.FanoutItem).
func pinAdoptState(ctx context.Context, _ agent.StateSnapshot[AdoptState]) *AdoptState {
	return adoptStateFromContext(ctx)
}

// --- Helper: synthesizer prompt -------------------------------------

// buildSynthesizerPrompt assembles the synthesizer user-message body.
// Extracted from invokeSynthesizer in refine.go so the workflow's
// RunItem can render the same prompt without re-routing through the
// invokeSynthesizer wrapper (which does its own dispatch). brief is
// optional — adopt's invocation always passes empty.
func buildSynthesizerPrompt(a spec.Approach, parent parentContext, applicable []spec.Decision, brief string) string {
	var prompt []byte
	prompt = appendSection(prompt, "## Refinement intent\n%s\n\n", brief)
	prompt = appendf(prompt, "## Approach\n%s — %s\n\n", a.ID, a.Title)
	prompt = appendf(prompt, "## Parent kind\n%s\n\n", parent.Kind)
	prompt = appendf(prompt, "## Parent ID\n%s\n\n", parent.ID)
	prompt = appendf(prompt, "## Parent title\n%s\n\n", parent.Title)
	prompt = append(prompt, "## Parent prose\n"...)
	prompt = append(prompt, parent.Prose...)
	prompt = append(prompt, "\n\n## Applicable Decisions\n"...)
	for _, d := range applicable {
		prompt = appendf(prompt, "- %s (%s, confidence=%.2f): %s — %s\n",
			d.ID, d.Status, d.Confidence, d.Title, d.Rationale)
	}
	prompt = append(prompt, "\n## Current Approach body\n"...)
	prompt = append(prompt, a.Body...)
	return string(prompt)
}

// appendSection writes a formatted block only when value is non-empty.
// Keeps the synthesizer prompt builder symmetric with invokeSynthesizer's
// brief-presence guard.
func appendSection(buf []byte, format, value string) []byte {
	if value == "" {
		return buf
	}
	return appendf(buf, format, value)
}

func appendf(buf []byte, format string, args ...any) []byte {
	return append(buf, []byte(fmt.Sprintf(format, args...))...)
}

// --- Entry point: build state, run workflow, harvest report ---------

// buildAdoptState constructs an AdoptState from cfg + the pre-loaded
// graph. Always returns a usable state; nil LLM / nil dispatcher
// surfaces as Conditional gates inside the workflow.
func buildAdoptState(cfg AdoptConfig, graph *spec.SpecGraph) (*AdoptState, error) {
	st := &AdoptState{
		Cfg:        cfg,
		FSys:       cfg.FS,
		SynthFS:    cfg.FS,
		Graph:      graph,
		Store:      state.NewFileStateStore(cfg.FS, state.DefaultStateDir),
		EvalRunner: eval.NewRunner(cfg.LLM),
	}
	if cfg.DryRun {
		st.SynthFS = newReadOnlyFS(cfg.FS)
	}
	if cfg.LLM != nil {
		loaded, err := spec.LoadSpec(cfg.FS)
		if err != nil {
			return nil, fmt.Errorf("load spec: %w", err)
		}
		st.Loaded = loaded
		synthDef, regenDef, err := loadAdoptApproachAgentDefs(cfg.FS)
		if err != nil {
			return nil, err
		}
		st.SynthesizerDef = synthDef
		st.RegeneratorDef = regenDef
		st.Dispatcher = agent.NewDispatcher(cfg.LLM)
	}
	return st, nil
}

// harvestAdoptReport rolls state outputs into an AdoptReport. Mirrors
// the field set RunAdoptWithConfig used to populate inline.
func harvestAdoptReport(st *AdoptState) *AdoptReport {
	report := &AdoptReport{
		Scope:                 st.Cfg.Scope,
		DryRun:                st.Cfg.DryRun,
		Classifications:       st.Classifications,
		PrereqResults:         st.PrereqResults,
		PrereqsOK:             st.PrereqsOK,
		Summary:               st.Summary,
		SynthesizedApproaches: st.Synthesized,
		RegeneratedApproaches: st.Regenerated,
		PreflightResolutions:  st.PreflightResolutions,
		AssumedDecisions:      st.AssumedDecisions,
		ResumedInvalidated:    st.ResumedInvalidated,
		Archived:              st.Archived,
		DispatchedWorkstreams: st.DispatchedWorkstreams,
	}
	if st.Plan != nil {
		report.PlanID = st.Plan.ID
	}
	if st.PlanToResume != nil {
		report.Resumed = append(report.Resumed, st.PlanToResume.ID)
	}
	return report
}

// loadAdoptApproachAgentDefs loads the two AgentDefs the workflow
// dispatches against (synthesizer + approach-regenerator). Both
// agents have embedded scaffold copies; project overrides at
// `.borg/agents/<id>.md` win.
func loadAdoptApproachAgentDefs(fsys specio.FS) (agent.AgentDef, agent.AgentDef, error) {
	synth, err := scaffold.LoadAgent(fsys, "synthesizer")
	if err != nil {
		return agent.AgentDef{}, agent.AgentDef{}, fmt.Errorf("load synthesizer: %w", err)
	}
	regen, err := scaffold.LoadAgent(fsys, "approach-regenerator")
	if err != nil {
		return agent.AgentDef{}, agent.AgentDef{}, fmt.Errorf("load approach-regenerator: %w", err)
	}
	return synth, regen, nil
}

// runAdoptWorkflow executes AdoptWorkflow against the supplied state.
// The state pointer travels on ctx via contextWithAdoptState so the
// single-shot RunItem closures can mutate state without the
// JSON-through-Output dance the fanout merges use.
func runAdoptWorkflow(ctx context.Context, st *AdoptState) error {
	ctx = contextWithAdoptState(ctx, st)
	executor := &agent.WorkflowExecutor[AdoptState]{
		Executor:  st.Cfg.LLM,
		AgentDefs: map[string]agent.AgentDef{},
		Workflow:  AdoptWorkflow,
	}
	if _, err := executor.Run(ctx, st); err != nil {
		return fmt.Errorf("adopt workflow: %w", err)
	}
	return nil
}
