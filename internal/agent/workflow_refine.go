package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
)

// RefineState is the typed blackboard for the decision-target refine
// cascade workflow (Phase 7 of workflow unification). Mirrors
// PlanningState's shape — value-copied into StateSnapshot for parallel
// fanout reads, mutated by the executor goroutine for merges.
//
// All inputs (DecisionID, Decision, Graph, FSys, Store, Brief,
// Features, Strategies, Bugs) are populated by the caller before
// the workflow runs. The Result accumulators (UpdatedFeatures, etc.)
// are populated by the cascade phase's merge handler.
//
// The workflow is a one-phase fanout: one item per parent referencing
// the changed Decision; each item dispatches the rewriter (cascade
// mode) or refiner (--brief mode) agent to produce a RewriteResult.
// The merge handler persists the rewrite, marks child Approaches
// drifted, and records a history event — inlined into the cascade
// phase per Phase 7's "option (a)" decision (the alternative
// "record_history" no-LLM phase would have required executor support
// for no-agent steps).
//
// The persistence helpers and prompt builder are duplicated here from
// internal/cascade rather than imported because internal/cascade
// already depends on internal/agent (the dispatcher) — re-importing
// would create a cycle. The duplication is ~30 lines and stays in
// lockstep through the workflow tests' equivalence assertions.
type RefineState struct {
	// Inputs (populated by RunRefine)
	DecisionID string
	Decision   *spec.Decision
	Graph      *spec.SpecGraph
	FSys       specio.FS
	Store      *state.FileStateStore
	Brief      string

	Features   []spec.Feature
	Strategies []spec.Strategy
	Bugs       []spec.Bug

	// Result accumulators (populated by the cascade merge handler)
	UpdatedFeatures   []string
	UpdatedStrategies []string
	UpdatedBugs       []string
	Skipped           []string
	DriftedApproaches []string
	Events            []history.Event
}

// RefineRewriteResult mirrors cascade.RewriteResult. Defined here so
// the workflow can decode the rewriter agent's structured output
// without importing internal/cascade (which would create a cycle).
// Field tags must match cascade.RewriteResult exactly.
type RefineRewriteResult struct {
	RevisedBody string `json:"revised_body"`
	Changed     bool   `json:"changed"`
	Rationale   string `json:"rationale"`
}

// refineFanoutItem is the per-parent fanout payload. JSON-marshalled
// into a fanout entry; the executor's per-item dispatch picks AgentID
// off the JSON and the Project closure unmarshals to render the
// per-item prompt. Kind selects which spec.* lookup the projection /
// merge use; ID matches the parent in state.{Features,Strategies,Bugs}.
type refineFanoutItem struct {
	AgentID string `json:"agent_id"`
	Kind    string `json:"kind"`
	ID      string `json:"id"`
}

// RefineCascadeWorkflow drives the decision-target refine cascade.
// One phase: cascade fanout. Each item rewrites one parent's prose
// (Feature.Description, Strategy .md body, or Bug.Description) and
// the merge handler persists the change, marks child Approaches
// drifted, and emits a history event.
//
// History recording is inlined into the cascade phase's merge handler
// (Phase 7 option (a)). The alternative — a separate record_history
// phase with no LLM call — would require extending the executor to
// support no-agent steps. The merge-handler approach preserves the
// invariant "rewrite happens before history event" naturally because
// the merge runs in the orchestrator goroutine after every fanout
// dispatch returns.
//
// Snapshot is left nil — the Project closure reads State by value
// into a per-item snapshot via the executor's default shallow copy,
// and the slice fields (Features/Strategies/Bugs) are not mutated by
// the fanout (they're inputs computed once before the workflow runs).
//
// MaxRounds=1 because the workflow is a single-pass DAG; the verb
// has no convergence loop.
var RefineCascadeWorkflow = &Workflow[RefineState]{
	Snapshot: snapshotRefineState,
	Rounds: []WorkflowStep[RefineState]{
		{
			ID:     "cascade",
			Agents: []string{"rewriter"}, // fallback when fanout item omits agent_id
			// Parallel=false mirrors the legacy cascade.Cascade
			// loop, which dispatched parents sequentially. Per-
			// model concurrency caps already bound the practical
			// parallelism the executor would gain from goroutine
			// fanout here, and sequential ordering keeps the
			// merge handler's result-to-item pairing stable for
			// the equivalence tests.
			Parallel: false,
			Fanout:   fanoutRefineParents,
			Project:  projectRefineParent,
			Merge:    mergeRefineCascade,
		},
	},
	MaxRounds: 1,
}

// snapshotRefineState produces a value-copy with slice fields
// preserved by reference. The slice elements (Feature/Strategy/Bug
// values) are read-only inputs the merge handler doesn't mutate, so
// reference-shared slices are safe for parallel fanout reads.
func snapshotRefineState(s *RefineState) RefineState {
	if s == nil {
		return RefineState{}
	}
	return *s
}

// fanoutRefineParents emits one fanout item per parent referencing the
// changed Decision. AgentID is set per-item via refineWorkflowAgentID
// so the executor's per-item dispatch (DJ-098 mechanism) routes to
// "refiner" when the user supplied --brief and "rewriter" otherwise.
//
// Empty parent set returns (nil, nil); the executor short-circuits the
// step without dispatching. This is the "decision is referenced by no
// features/strategies/bugs" path the workflow tests cover.
func fanoutRefineParents(s *RefineState) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	agentID := refineWorkflowAgentID(s.Brief)
	items := make([]any, 0, len(s.Features)+len(s.Strategies)+len(s.Bugs))
	for _, f := range s.Features {
		items = append(items, refineFanoutItem{AgentID: agentID, Kind: "feature", ID: f.ID})
	}
	for _, st := range s.Strategies {
		items = append(items, refineFanoutItem{AgentID: agentID, Kind: "strategy", ID: st.ID})
	}
	for _, b := range s.Bugs {
		items = append(items, refineFanoutItem{AgentID: agentID, Kind: "bug", ID: b.ID})
	}
	if len(items) == 0 {
		return nil, nil
	}
	return marshalFanoutItems(items)
}

// projectRefineParent renders the per-item rewriter prompt. Reads the
// FanoutItem to identify which parent is being rewritten, looks the
// parent up in the snapshot's State, and assembles the user message
// in the same shape cascade.invokeRewriter uses for the legacy
// direct-call path.
//
// The system prompt comes from the registered AgentDef ("rewriter" or
// "refiner") — the projection only contributes the user-side message.
func projectRefineParent(snap StateSnapshot[RefineState]) []Message {
	var item refineFanoutItem
	if err := json.Unmarshal([]byte(snap.FanoutItem), &item); err != nil {
		// Defensive: fanoutRefineParents always produces well-formed
		// items, so this branch is unreachable in practice. Log + emit
		// a placeholder so the executor's call still goes through and
		// the merge handler can surface the failure.
		slog.Warn("refine workflow: malformed fanout item", "error", err, "raw", snap.FanoutItem)
		return []Message{{Role: "user", Content: ""}}
	}

	st := snap.State
	var (
		parentKind  string
		parentID    string
		parentTitle string
		currentBody string
		applicable  []spec.Decision
	)

	switch item.Kind {
	case "feature":
		f := lookupRefineFeature(st.Features, item.ID)
		if f == nil {
			return []Message{{Role: "user", Content: ""}}
		}
		parentKind = "feature"
		parentID = f.ID
		parentTitle = f.Title
		currentBody = f.Description
		applicable = applicableDecisionsFromGraph(st.Graph, f.Decisions)
	case "strategy":
		s := lookupRefineStrategy(st.Strategies, item.ID)
		if s == nil {
			return []Message{{Role: "user", Content: ""}}
		}
		body, _ := st.FSys.ReadFile(".borg/spec/strategies/" + s.ID + ".md")
		parentKind = "strategy"
		parentID = s.ID
		parentTitle = s.Title
		currentBody = string(body)
		applicable = applicableDecisionsFromGraph(st.Graph, s.Decisions)
	case "bug":
		b := lookupRefineBug(st.Bugs, item.ID)
		if b == nil {
			return []Message{{Role: "user", Content: ""}}
		}
		parentKind = "bug"
		parentID = b.ID
		parentTitle = b.Title
		currentBody = b.Description
		// Bugs inherit applicable Decisions from the parent Feature
		// (cmd.RunRefineBug uses the same rule).
		if parent := st.Graph.Feature(b.FeatureID); parent != nil {
			applicable = applicableDecisionsFromGraph(st.Graph, parent.Decisions)
		}
	default:
		return []Message{{Role: "user", Content: ""}}
	}

	changed := []spec.Decision{}
	if st.Decision != nil {
		changed = append(changed, *st.Decision)
	}
	prompt := buildRefineRewriterPrompt(parentKind, parentID, parentTitle, currentBody, st.Brief, applicable, changed)
	return []Message{{Role: "user", Content: prompt}}
}

// mergeRefineCascade processes each fanout result. The result order
// matches fanoutRefineParents' emission order (the executor preserves
// per-item ordering even with Parallel=true), so the merge can step
// through results and items in parallel.
//
// For each item:
//   - Parse the LLM output as RefineRewriteResult JSON.
//   - If the rewriter reports no change, mark Skipped.
//   - Otherwise persist via inline helpers (matching cascade.PersistRewrite*),
//     mark child Approaches drifted, and record a history event.
//
// The history recording is inlined here (option (a)) rather than
// hoisted to a separate "record_history" phase — the merge handler
// already runs in the orchestrator goroutine after the fanout
// completes, so the ordering invariant ("rewrite then history") is
// naturally preserved without extending the executor for no-agent
// steps.
func mergeRefineCascade(s *RefineState, results []RoundResult) {
	if s == nil {
		return
	}

	// Re-emit the fanout items in the same order to pair each result
	// with its parent. fanoutRefineParents is deterministic so this
	// reproduces the original sequence exactly.
	items, err := fanoutRefineParents(s)
	if err != nil {
		slog.Warn("refine workflow: re-emit fanout items failed in merge", "error", err)
		return
	}
	if len(items) != len(results) {
		// Shouldn't happen in practice — the executor preserves the
		// 1:1 fanout-result mapping. Log and proceed with the shorter
		// of the two so we don't index out of range.
		slog.Warn("refine workflow: results length differs from fanout items",
			"items", len(items),
			"results", len(results))
	}
	n := len(items)
	if len(results) < n {
		n = len(results)
	}

	hist := history.NewHistorian(s.FSys, ".borg/history")

	for i := 0; i < n; i++ {
		var item refineFanoutItem
		if err := json.Unmarshal([]byte(items[i]), &item); err != nil {
			slog.Warn("refine workflow: malformed fanout item in merge", "error", err)
			continue
		}
		r := results[i]
		if r.Err != nil {
			// Surface the underlying error as a warning; the cascade
			// phase as a whole still proceeds for the remaining
			// items (per-node failure isolation, mirrors fanout
			// behavior in the spec-generation workflow).
			slog.Warn("refine workflow: rewriter failed for parent",
				"kind", item.Kind,
				"id", item.ID,
				"error", r.Err)
			continue
		}
		if r.Output == "" {
			continue
		}
		var rw RefineRewriteResult
		if err := json.Unmarshal([]byte(r.Output), &rw); err != nil {
			slog.Warn("refine workflow: parse rewriter output failed",
				"kind", item.Kind,
				"id", item.ID,
				"error", err)
			continue
		}

		switch item.Kind {
		case "feature":
			s.applyFeatureResult(item.ID, &rw, hist)
		case "strategy":
			s.applyStrategyResult(item.ID, &rw, hist)
		case "bug":
			s.applyBugResult(item.ID, &rw, hist)
		}
	}
}

// applyFeatureResult persists a Feature rewrite, marks downstream
// Approaches drifted, and records a history event when the rewriter
// reported a change. Mirrors cascade.RewriteFeature's post-LLM logic.
func (s *RefineState) applyFeatureResult(featureID string, rw *RefineRewriteResult, hist *history.Historian) {
	f := lookupRefineFeature(s.Features, featureID)
	if f == nil {
		return
	}
	if !rw.Changed || strings.TrimSpace(rw.RevisedBody) == strings.TrimSpace(f.Description) {
		s.Skipped = append(s.Skipped, featureID)
		return
	}
	updated := *f
	updated.Description = rw.RevisedBody
	updated.UpdatedAt = time.Now()
	if err := specio.SavePair(s.FSys, ".borg/spec/features/"+f.ID, updated, rw.RevisedBody); err != nil {
		slog.Warn("refine workflow: save feature failed", "id", featureID, "error", err)
		return
	}
	s.UpdatedFeatures = append(s.UpdatedFeatures, featureID)
	if err := markRefineApproachesDrifted(s.Store, f.Approaches, &s.DriftedApproaches); err != nil {
		slog.Warn("refine workflow: mark approaches drifted failed", "id", featureID, "error", err)
		return
	}
	evt := buildRefineCascadeEvent(s.DecisionID, featureID, "feature_rewritten", rw.Rationale)
	if err := hist.Record(evt); err != nil {
		slog.Warn("refine workflow: record feature history event failed", "id", featureID, "error", err)
		return
	}
	s.Events = append(s.Events, evt)
}

func (s *RefineState) applyStrategyResult(strategyID string, rw *RefineRewriteResult, hist *history.Historian) {
	st := lookupRefineStrategy(s.Strategies, strategyID)
	if st == nil {
		return
	}
	currentBody, _ := s.FSys.ReadFile(".borg/spec/strategies/" + st.ID + ".md")
	if !rw.Changed || strings.TrimSpace(rw.RevisedBody) == strings.TrimSpace(string(currentBody)) {
		s.Skipped = append(s.Skipped, strategyID)
		return
	}
	if err := specio.SavePair(s.FSys, ".borg/spec/strategies/"+st.ID, *st, rw.RevisedBody); err != nil {
		slog.Warn("refine workflow: save strategy failed", "id", strategyID, "error", err)
		return
	}
	s.UpdatedStrategies = append(s.UpdatedStrategies, strategyID)
	if err := markRefineApproachesDrifted(s.Store, st.Approaches, &s.DriftedApproaches); err != nil {
		slog.Warn("refine workflow: mark approaches drifted failed", "id", strategyID, "error", err)
		return
	}
	evt := buildRefineCascadeEvent(s.DecisionID, strategyID, "strategy_rewritten", rw.Rationale)
	if err := hist.Record(evt); err != nil {
		slog.Warn("refine workflow: record strategy history event failed", "id", strategyID, "error", err)
		return
	}
	s.Events = append(s.Events, evt)
}

func (s *RefineState) applyBugResult(bugID string, rw *RefineRewriteResult, hist *history.Historian) {
	b := lookupRefineBug(s.Bugs, bugID)
	if b == nil {
		return
	}
	if !rw.Changed || strings.TrimSpace(rw.RevisedBody) == strings.TrimSpace(b.Description) {
		s.Skipped = append(s.Skipped, bugID)
		return
	}
	updated := *b
	updated.Description = rw.RevisedBody
	updated.UpdatedAt = time.Now()
	if err := specio.SavePair(s.FSys, ".borg/spec/bugs/"+b.ID, updated, rw.RevisedBody); err != nil {
		slog.Warn("refine workflow: save bug failed", "id", bugID, "error", err)
		return
	}
	s.UpdatedBugs = append(s.UpdatedBugs, bugID)
	// Bugs propagate drift through their child Approaches (parents
	// of the bug node in the graph). Look these up via the graph
	// rather than walking Bug fields (Bug has no Approaches slice).
	childIDs := childRefineApproachesOf(s.Graph, bugID)
	if err := markRefineApproachesDrifted(s.Store, childIDs, &s.DriftedApproaches); err != nil {
		slog.Warn("refine workflow: mark approaches drifted failed", "id", bugID, "error", err)
		return
	}
	evt := buildRefineCascadeEvent(s.DecisionID, bugID, "bug_rewritten", rw.Rationale)
	if err := hist.Record(evt); err != nil {
		slog.Warn("refine workflow: record bug history event failed", "id", bugID, "error", err)
		return
	}
	s.Events = append(s.Events, evt)
}

// refineWorkflowAgentID picks between "refiner" (intent-driven) and
// "rewriter" (cascade-driven) based on whether the user supplied a
// refinement brief. Mirrors cascade.RewriterAgentID — duplicated to
// keep internal/agent free of internal/cascade dependencies.
func refineWorkflowAgentID(brief string) string {
	if brief != "" {
		return "refiner"
	}
	return "rewriter"
}

// buildRefineRewriterPrompt assembles the user-side prompt for the
// rewriter or refiner agent. Mirrors cascade.BuildRewriterPrompt — the
// duplication keeps internal/agent cycle-free; the cascade test suite
// proves the formatting matches via the equivalence test in
// internal/cascade/cascade_test.go.
func buildRefineRewriterPrompt(parentKind, parentID, parentTitle, currentBody, brief string, applicable, changed []spec.Decision) string {
	var prompt strings.Builder
	if brief != "" {
		fmt.Fprintf(&prompt, "## Refinement intent\n%s\n\n", brief)
	}
	fmt.Fprintf(&prompt, "## Parent kind\n%s\n\n", parentKind)
	fmt.Fprintf(&prompt, "## Parent ID\n%s\n\n", parentID)
	fmt.Fprintf(&prompt, "## Parent title\n%s\n\n", parentTitle)
	prompt.WriteString("## Current parent prose\n")
	prompt.WriteString(currentBody)
	prompt.WriteString("\n\n## Applicable Decisions\n")
	for _, d := range applicable {
		fmt.Fprintf(&prompt, "- %s (%s, confidence=%.2f): %s — %s\n", d.ID, d.Status, d.Confidence, d.Title, d.Rationale)
	}
	if brief == "" {
		prompt.WriteString("\n## Recently changed Decisions\n")
		for _, d := range changed {
			fmt.Fprintf(&prompt, "- %s — %s\n", d.ID, d.Title)
		}
	}
	return prompt.String()
}

// markRefineApproachesDrifted mirrors cascade.MarkApproachesDrifted.
// Duplicated to keep internal/agent cycle-free.
func markRefineApproachesDrifted(store *state.FileStateStore, approachIDs []string, out *[]string) error {
	for _, id := range approachIDs {
		existing, err := store.Load(id)
		if err != nil {
			continue
		}
		existing.SpecHash = ""
		existing.Status = state.StatusDrifted
		existing.Message = "cascaded from upstream Decision change"
		existing.LastReconciled = time.Now()
		existing.WorkstreamID = ""
		existing.AssertionResults = nil
		if err := store.Save(existing); err != nil {
			return fmt.Errorf("mark approach %s drifted: %w", id, err)
		}
		*out = append(*out, id)
	}
	return nil
}

// buildRefineCascadeEvent mirrors cascade.CascadeEvent.
func buildRefineCascadeEvent(decisionID, parentID, kind, rationale string) history.Event {
	now := time.Now()
	return history.Event{
		ID:        history.EventID("cascade", parentID, now),
		Timestamp: now,
		Kind:      kind,
		TargetID:  parentID,
		Rationale: fmt.Sprintf("Cascade from decision %s: %s", decisionID, rationale),
	}
}

// applicableDecisionsFromGraph mirrors cascade.ApplicableDecisions.
func applicableDecisionsFromGraph(g *spec.SpecGraph, ids []string) []spec.Decision {
	out := make([]spec.Decision, 0, len(ids))
	for _, id := range ids {
		if d := g.Decision(id); d != nil {
			out = append(out, *d)
		}
	}
	return out
}

// childRefineApproachesOf returns the IDs of Approaches whose
// ParentID matches the given parent. Used for Bug parents whose
// Approaches aren't carried as a struct field. Mirrors
// cmd.childApproachesOf.
func childRefineApproachesOf(g *spec.SpecGraph, parentID string) []string {
	if g == nil {
		return nil
	}
	var out []string
	for id, node := range g.Nodes() {
		if node.Kind != spec.KindApproach {
			continue
		}
		if a := g.Approach(id); a != nil && a.ParentID == parentID {
			out = append(out, a.ID)
		}
	}
	return out
}

func lookupRefineFeature(features []spec.Feature, id string) *spec.Feature {
	for i := range features {
		if features[i].ID == id {
			return &features[i]
		}
	}
	return nil
}

func lookupRefineStrategy(strategies []spec.Strategy, id string) *spec.Strategy {
	for i := range strategies {
		if strategies[i].ID == id {
			return &strategies[i]
		}
	}
	return nil
}

func lookupRefineBug(bugs []spec.Bug, id string) *spec.Bug {
	for i := range bugs {
		if bugs[i].ID == id {
			return &bugs[i]
		}
	}
	return nil
}
