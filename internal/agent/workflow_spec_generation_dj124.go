// Package agent — DJ-124 scout-driven convergence loop helpers.
//
// This file isolates the new spec-generation primitives the Phase 5
// workflow rewrite introduces: the scout spawner, the per-axis and
// per-affected-node fanout closures, the merge functions for the new
// decision / narrative stages, the affected-node computation, and the
// projections each new step uses.
//
// The legacy gate-loop helpers (gateSpawnFor, mergeGateVerdict,
// convergence{Stuck,Failed}Terminal, etc.) stay in
// workflow_spec_generation.go because the legacy gate-test workflow in
// the test file still drives them.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/executor"
	"github.com/chetan/locutus/internal/history"
)

// scoutSpawnFor builds the Spawn closure for a scout step at iteration
// `myIter` with the given `budget`. The closure parses the latest
// ScoutBrief and decides whether to terminate the workflow, expand the
// next iteration template, or spawn a convergence_stuck /
// convergence_failed terminal:
//
//   - ScoutBrief.Converged == true: returns nil — the workflow
//     terminates as the queue drains.
//   - ScoutBrief.AxesOpen contains an axis ID already in
//     state.DecidedAxesByIter: cycle detected — spawn a
//     convergence_stuck terminal that writes a DJ-103 history event.
//   - myIter+1 >= budget: budget exhausted — spawn a
//     convergence_failed terminal.
//   - Otherwise: expand the loop template at iteration myIter+1.
//
// The closure also accepts a malformed brief: when the brief can't be
// parsed, the spawner surfaces the parse error so the executor fails
// the run visibly rather than silently looping forever.
func scoutSpawnFor(myIter, budget int, loopTemplate func(executor.IterationContext) []WorkflowStep[PlanningState], historian *history.Historian) func(ctx context.Context, snap StateSnapshot[PlanningState], results []RoundResult) ([]WorkflowStep[PlanningState], []executor.Edge, error) {
	return func(_ context.Context, snap StateSnapshot[PlanningState], results []RoundResult) ([]WorkflowStep[PlanningState], []executor.Edge, error) {
		brief, err := parseScoutBriefFromResults(results)
		if err != nil {
			return nil, nil, fmt.Errorf("spec_scout brief at iter %d: %w", myIter, err)
		}
		if brief.Converged {
			return nil, nil, nil
		}

		// Cycle detection: any axis in axes_open whose ID is already in
		// DecidedAxesByIter is a reopened axis. Force-terminate with a
		// convergence_stuck DJ-103 event naming the reopened axes so
		// the failure mode is diagnosable (the loop didn't drift to
		// budget exhaustion on a moving target).
		//
		// Check BEFORE budget — a cycle should fail with the cycle
		// diagnosis, not budget exhaustion.
		if reopened := reopenedAxes(&snap.State, brief.AxesOpen); len(reopened) > 0 {
			terminal := scoutConvergenceStuckTerminal(historian, &snap.State, brief, myIter, reopened)
			return []WorkflowStep[PlanningState]{terminal}, nil, nil
		}

		nextIter := myIter + 1
		if nextIter >= budget {
			terminal := scoutConvergenceFailedTerminal(historian, &snap.State, brief, myIter, budget)
			return []WorkflowStep[PlanningState]{terminal}, nil, nil
		}

		// Expand the next iteration template. ParentNodeID is the
		// current scout's prefixed id so AppendSubgraph wires the loop
		// edge from this scout to each root step in the next iteration.
		var parentID string
		if myIter == 0 {
			parentID = "scout"
		} else {
			parentID = fmt.Sprintf("%s#iter:%d:scout", specLoopTemplateID, myIter)
		}
		steps, edges := AppendSubgraph(loopTemplate, executor.IterationContext{
			TemplateID:     specLoopTemplateID,
			IterationIndex: nextIter,
			ParentNodeID:   parentID,
		})
		return steps, edges, nil
	}
}

// parseScoutBriefFromResults pulls the first non-error spec_scout
// result off the round and decodes it as a ScoutBrief. Surfaces decode
// failures and missing results as errors so the spawner can attribute
// them to the iteration.
func parseScoutBriefFromResults(results []RoundResult) (*ScoutBrief, error) {
	for _, r := range results {
		if r.AgentID != "spec_scout" {
			continue
		}
		if r.Err != nil {
			return nil, fmt.Errorf("spec_scout run failed: %w", r.Err)
		}
		if strings.TrimSpace(r.Output) == "" {
			return nil, fmt.Errorf("spec_scout returned empty output")
		}
		var brief ScoutBrief
		if err := json.Unmarshal([]byte(r.Output), &brief); err != nil {
			return nil, fmt.Errorf("parse spec_scout brief: %w (content=%q)", err, r.Output)
		}
		return &brief, nil
	}
	return nil, fmt.Errorf("no spec_scout result in round")
}

// reopenedAxes returns the OpenAxis IDs that already appear in
// state.DecidedAxesByIter from a prior iteration — the cycle signature
// the loop force-terminates on. Includes the iteration index when the
// axis was originally decided so the operator can see the round-trip.
func reopenedAxes(s *PlanningState, axesOpen []OpenAxis) []string {
	if s == nil || len(s.DecidedAxesByIter) == 0 || len(axesOpen) == 0 {
		return nil
	}
	var reopened []string
	for _, axis := range axesOpen {
		id := strings.TrimSpace(axis.ID)
		if id == "" {
			continue
		}
		if iter, ok := s.DecidedAxesByIter[id]; ok {
			reopened = append(reopened, fmt.Sprintf("%s (decided at iter %d)", id, iter+1))
		}
	}
	sort.Strings(reopened) // deterministic output for error messages and history events
	return reopened
}

// scoutConvergenceStuckTerminal mirrors convergenceStuckTerminal but
// is keyed off the scout's brief rather than the legacy gate verdict.
// Writes a DJ-103 history event tagged convergence_stuck and returns a
// non-nil error naming the reopened axes.
func scoutConvergenceStuckTerminal(historian *history.Historian, snapState *PlanningState, brief *ScoutBrief, iter int, reopened []string) WorkflowStep[PlanningState] {
	terminalID := fmt.Sprintf("convergence_stuck_iter:%d", iter)
	snapshotSpec := snapState.ProposedSpec
	concerns := append([]Concern(nil), snapState.Concerns...)
	reopenedCopy := append([]string(nil), reopened...)
	return WorkflowStep[PlanningState]{
		ID:     terminalID,
		Agents: []string{"spec_scout"},
		RunItem: func(_ context.Context, _ StateSnapshot[PlanningState]) (string, error) {
			if historian != nil {
				evt := buildScoutConvergenceStuckEvent(brief, iter, snapshotSpec, concerns, reopenedCopy)
				if err := historian.Record(evt); err != nil {
					slog.Warn("convergence_stuck: failed to record DJ-103 event", "err", err)
				}
			}
			return "", fmt.Errorf(
				"spec-generation council stuck at iter %d: scout reopened %d previously-decided axis/axes. Reopened axes: %s",
				iter+1, len(reopenedCopy), strings.Join(reopenedCopy, "; "),
			)
		},
	}
}

// scoutConvergenceFailedTerminal is the budget-exhaustion terminal for
// the scout-driven loop. Mirrors convergenceFailedTerminal but reports
// the scout's open axes rather than the legacy gate's open dimensions.
func scoutConvergenceFailedTerminal(historian *history.Historian, snapState *PlanningState, brief *ScoutBrief, iter, budget int) WorkflowStep[PlanningState] {
	terminalID := fmt.Sprintf("convergence_failed_iter:%d", iter)
	snapshotSpec := snapState.ProposedSpec
	concerns := append([]Concern(nil), snapState.Concerns...)
	return WorkflowStep[PlanningState]{
		ID:     terminalID,
		Agents: []string{"spec_scout"},
		RunItem: func(_ context.Context, _ StateSnapshot[PlanningState]) (string, error) {
			if historian != nil {
				evt := buildScoutConvergenceFailedEvent(brief, iter, budget, snapshotSpec, concerns)
				if err := historian.Record(evt); err != nil {
					slog.Warn("convergence_failed: failed to record DJ-103 event", "err", err)
				}
			}
			return "", fmt.Errorf(
				"spec-generation council convergence failed after %d iteration(s); %d open axis/axes remain",
				iter+1, len(brief.AxesOpen),
			)
		},
	}
}

func buildScoutConvergenceStuckEvent(brief *ScoutBrief, iter int, snapshotSpec string, concerns []Concern, reopened []string) history.Event {
	now := time.Now()
	var rationale strings.Builder
	fmt.Fprintf(&rationale, "spec_scout reopened decided axes at iter %d.", iter+1)
	if len(reopened) > 0 {
		rationale.WriteString("\n\nReopened axes:")
		for _, r := range reopened {
			fmt.Fprintf(&rationale, "\n- %s", r)
		}
	}
	if len(brief.AxesOpen) > 0 {
		rationale.WriteString("\n\nCurrent axes_open:")
		for _, a := range brief.AxesOpen {
			fmt.Fprintf(&rationale, "\n- %s: %s", a.ID, a.Description)
		}
	}
	if len(concerns) > 0 {
		rationale.WriteString("\n\nConcerns at the time of stall:")
		for _, c := range concerns {
			fmt.Fprintf(&rationale, "\n- [%s/%s] %s", c.AgentID, c.Severity, c.Text)
		}
	}
	return history.Event{
		ID:        history.EventID("convergence_stuck", "", now),
		Timestamp: now,
		Kind:      "convergence_stuck",
		Rationale: rationale.String(),
		NewValue:  snapshotSpec,
	}
}

func buildScoutConvergenceFailedEvent(brief *ScoutBrief, iter, budget int, snapshotSpec string, concerns []Concern) history.Event {
	now := time.Now()
	var rationale strings.Builder
	fmt.Fprintf(&rationale, "spec_scout did not converge within %d iteration(s); budget exhausted at iter %d.", budget, iter+1)
	if len(brief.AxesOpen) > 0 {
		rationale.WriteString("\n\nOpen axes at exhaustion:")
		for _, a := range brief.AxesOpen {
			fmt.Fprintf(&rationale, "\n- %s: %s", a.ID, a.Description)
		}
	}
	if len(concerns) > 0 {
		rationale.WriteString("\n\nUnresolved concerns at exhaustion:")
		for _, c := range concerns {
			fmt.Fprintf(&rationale, "\n- [%s/%s] %s", c.AgentID, c.Severity, c.Text)
		}
	}
	return history.Event{
		ID:        history.EventID("convergence_failed", "", now),
		Timestamp: now,
		Kind:      "convergence_failed",
		Rationale: rationale.String(),
		NewValue:  snapshotSpec,
	}
}

// hasOpenAxes gates the decisions step on state.AxesOpen having at
// least one entry. False on the rare iteration where the scout emits
// only new_nodes (a scout pass that adds a feature whose axes are all
// already covered by existing decisions).
func hasOpenAxes(s *PlanningState) bool { return s != nil && len(s.AxesOpen) > 0 }

// hasAffectedNodes gates the narrative step on computeAffectedNodes
// returning a non-empty set. Avoids dispatching the narrative fanout
// when no decisions changed and no critic flagged a node by id.
func hasAffectedNodes(s *PlanningState) bool {
	if s == nil {
		return false
	}
	return len(computeAffectedNodes(s, nil)) > 0
}

// fanoutOpenAxes returns one raw-JSON OpenAxis per entry in
// state.AxesOpen. Each item drives one spec_decision_elaborator call.
func fanoutOpenAxes(state *PlanningState) ([]string, error) {
	if state == nil || len(state.AxesOpen) == 0 {
		return nil, nil
	}
	items := make([]any, 0, len(state.AxesOpen))
	for _, a := range state.AxesOpen {
		items = append(items, a)
	}
	return marshalFanoutItems(items)
}

// affectedNodeItem is the per-item shape the narrative fanout
// dispatches against. AgentID drives per-item routing (the executor's
// ExecuteRound sniffs agent_id off each item); ID lets fanoutItemID
// label the per-item dispatch slot; Kind / NewNode / ExistingFeature /
// ExistingStrategy carry the prior body the elaborator must extend.
//
// Exactly one of NewNode / ExistingFeature / ExistingStrategy is set
// on any given item.
type affectedNodeItem struct {
	AgentID          string               `json:"agent_id"`
	ID               string               `json:"id"`
	Kind             string               `json:"kind"`
	NewNode          *NewSpecNode         `json:"new_node,omitempty"`
	ExistingFeature  *RawFeatureProposal  `json:"existing_feature,omitempty"`
	ExistingStrategy *RawStrategyProposal `json:"existing_strategy,omitempty"`
	// Decisions is the authoritative pre-populated decision-ID list for
	// the node this iteration. For existing nodes it's a copy of the
	// node's current Decisions[] (already inclusive of newly-minted
	// decisions from this iter's decision-elaborator). For new nodes
	// it's NewSpecNode.Decisions (likewise inclusive).
	Decisions []string `json:"decisions"`
}

// fanoutAffectedNodes builds the per-affected-node fanout items. For
// each ID in computeAffectedNodes:
//
//   - If the ID matches a NewSpecNode in state.NewNodesFromScout, the
//     item carries the NewSpecNode plus the kind-determined agent_id.
//   - Else if the ID matches an existing feature or strategy in
//     state.RawProposal, the item carries that node and the
//     corresponding agent_id.
//   - Else the ID is skipped (the affected set referenced a node that
//     no longer exists — most often a stale critic finding).
//
// Empty fanout returns nil so the conditional skip path fires.
func fanoutAffectedNodes(state *PlanningState) ([]string, error) {
	if state == nil {
		return nil, nil
	}
	ids := computeAffectedNodes(state, nil)
	if len(ids) == 0 {
		return nil, nil
	}

	var raw RawSpecProposal
	if state.RawProposal != "" {
		if err := json.Unmarshal([]byte(state.RawProposal), &raw); err != nil {
			return nil, fmt.Errorf("parse raw proposal for narrative fanout: %w", err)
		}
	}
	featureByID := make(map[string]RawFeatureProposal, len(raw.Features))
	for _, f := range raw.Features {
		featureByID[f.ID] = f
	}
	strategyByID := make(map[string]RawStrategyProposal, len(raw.Strategies))
	for _, s := range raw.Strategies {
		strategyByID[s.ID] = s
	}
	newByID := make(map[string]NewSpecNode, len(state.NewNodesFromScout))
	for _, n := range state.NewNodesFromScout {
		newByID[n.ID] = n
	}

	items := make([]any, 0, len(ids))
	for _, id := range ids {
		if n, ok := newByID[id]; ok {
			agentID := agentIDForKind(n.Kind)
			itemKind := strings.TrimSpace(n.Kind)
			if itemKind == "" {
				itemKind = inferKindFromID(id)
			}
			nodeCopy := n
			items = append(items, affectedNodeItem{
				AgentID:   agentID,
				ID:        id,
				Kind:      itemKind,
				NewNode:   &nodeCopy,
				Decisions: append([]string(nil), n.Decisions...),
			})
			continue
		}
		if f, ok := featureByID[id]; ok {
			fCopy := f
			items = append(items, affectedNodeItem{
				AgentID:         "spec_feature_elaborator",
				ID:              id,
				Kind:            "feature",
				ExistingFeature: &fCopy,
				Decisions:       append([]string(nil), f.Decisions...),
			})
			continue
		}
		if s, ok := strategyByID[id]; ok {
			sCopy := s
			items = append(items, affectedNodeItem{
				AgentID:          "spec_strategy_elaborator",
				ID:               id,
				Kind:             "strategy",
				ExistingStrategy: &sCopy,
				Decisions:        append([]string(nil), s.Decisions...),
			})
			continue
		}
		// ID matches nothing — skip silently. The most common cause is
		// a stale critic finding referencing a node a prior iteration
		// dropped.
		slog.Warn("narrative fanout: affected ID resolved to no known node; skipping", "id", id)
	}
	if len(items) == 0 {
		return nil, nil
	}
	return marshalFanoutItems(items)
}

// agentIDForKind maps a NewSpecNode.Kind to the elaborator agent that
// authors its narrative. Unknown kinds default to feature-elaborator
// (the dominant kind); the integrity critic catches genuinely-broken
// node kinds downstream.
func agentIDForKind(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "strategy":
		return "spec_strategy_elaborator"
	case "feature":
		return "spec_feature_elaborator"
	default:
		return "spec_feature_elaborator"
	}
}

// inferKindFromID falls back to ID-prefix sniffing when NewSpecNode.Kind
// is empty. feat-* → feature; strat-* → strategy; anything else →
// feature (the safer default).
func inferKindFromID(id string) string {
	switch {
	case strings.HasPrefix(id, "feat-"):
		return "feature"
	case strings.HasPrefix(id, "strat-"):
		return "strategy"
	default:
		return "feature"
	}
}

// computeAffectedNodes returns the set of feature / strategy IDs that
// need narrative re-elaboration this iteration. The union is:
//
//   - nodes in state.RawProposal whose Decisions[] contains any
//     decision id minted this iteration (delta surface: a decision
//     changed, so the nodes that reference it must re-author);
//   - nodes named (by id-regex match) in any entry of state.Concerns
//     (critic-flagged: a critic flagged this node specifically);
//   - every NewSpecNode.ID from state.NewNodesFromScout (the scout
//     introduced this node this iteration; it has no prior body).
//
// newDecisionIDs is the optional set of decision IDs minted this
// iteration. When nil, computeAffectedNodes derives the set from
// state.DecidedAxesByIter (axis → iter map; the iter-keyed entries
// added in mergeDecisions). When non-nil it's used directly — useful
// for tests that drive the set explicitly.
//
// Dedup-preserving the first-seen order so dispatch is deterministic
// across runs.
func computeAffectedNodes(state *PlanningState, newDecisionIDs []string) []string {
	if state == nil {
		return nil
	}

	// Resolve the iteration-changed decision-ID set.
	changed := make(map[string]struct{}, len(newDecisionIDs))
	for _, id := range newDecisionIDs {
		changed[id] = struct{}{}
	}
	// When the caller didn't pass an explicit set, we don't have a
	// recorded "decisions minted this iteration" — the cheap proxy is
	// "any decision in state.RawProposal whose ID's axis appears in
	// DecidedAxesByIter". For Stage C we keep this conservative: when
	// changed is empty, only NewSpecNode and concern-named nodes drive
	// the affected set. The merge functions are responsible for
	// recording the decision ID delta when finer-grained scoping
	// matters.

	seen := make(map[string]struct{})
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	// 1. Existing nodes referencing a changed decision.
	if len(changed) > 0 && state.RawProposal != "" {
		var raw RawSpecProposal
		if err := json.Unmarshal([]byte(state.RawProposal), &raw); err == nil {
			for _, f := range raw.Features {
				for _, d := range f.Decisions {
					if _, ok := changed[d]; ok {
						add(f.ID)
						break
					}
				}
			}
			for _, s := range raw.Strategies {
				for _, d := range s.Decisions {
					if _, ok := changed[d]; ok {
						add(s.ID)
						break
					}
				}
			}
		}
	}

	// 2. Nodes named (by id-regex) in any concern text.
	for _, c := range state.Concerns {
		matches := idRefRegex.FindAllString(c.Text, -1)
		for _, m := range matches {
			add(m)
		}
	}

	// 3. New nodes from the scout (each one has no prior body).
	for _, n := range state.NewNodesFromScout {
		add(n.ID)
	}

	return out
}

// mergeDecisions parses each decision-elaborator output as a
// RawDecisionProposal, mints / preserves the decision ID, appends it
// to state.RawProposal.Decisions[], updates state.NewNodesFromScout
// entries that surfaced the decided axis to include the new ID, and
// records DecidedAxesByIter for cycle detection.
//
// Idempotent — calling twice with the same results produces the same
// final state (duplicate IDs are skipped; duplicate axis records
// preserve the earliest iter index).
func mergeDecisions(s *PlanningState, results []RoundResult) {
	if s == nil || len(results) == 0 {
		return
	}
	if s.DecidedAxesByIter == nil {
		s.DecidedAxesByIter = make(map[string]int)
	}

	// Parse the current RawProposal so we can append decisions in place.
	var raw RawSpecProposal
	if s.RawProposal != "" {
		if err := json.Unmarshal([]byte(s.RawProposal), &raw); err != nil {
			slog.Warn("mergeDecisions: existing RawProposal is malformed; starting fresh", "error", err)
			raw = RawSpecProposal{}
		}
	}

	// Index already-known IDs so id-minting / collision-suffixing stays
	// deterministic.
	usedIDs := make(map[string]struct{}, len(raw.Decisions))
	for _, d := range raw.Decisions {
		usedIDs[d.ID] = struct{}{}
	}
	if s.Existing != nil {
		for _, d := range s.Existing.Decisions {
			usedIDs[d.ID] = struct{}{}
		}
	}

	// Iteration index — every result in a fanout shares the same
	// iteration. Capture from the first non-empty result.
	currentIter := 0
	for _, r := range results {
		if r.Output != "" {
			currentIter = r.IterationIndex
			break
		}
	}

	var mintedThisCall []string
	for _, r := range results {
		if r.Err != nil || strings.TrimSpace(r.Output) == "" {
			continue
		}
		var d RawDecisionProposal
		if err := json.Unmarshal([]byte(r.Output), &d); err != nil {
			slog.Warn("mergeDecisions: skipping malformed decision-elaborator output", "error", err)
			continue
		}
		id := strings.TrimSpace(d.ID)
		if id == "" {
			id = mintDecisionID(d.Title, usedIDs)
		} else if _, taken := usedIDs[id]; taken {
			id = mintDecisionID(d.Title, usedIDs)
		}
		d.ID = id
		usedIDs[id] = struct{}{}
		mintedThisCall = append(mintedThisCall, id)

		raw.Decisions = append(raw.Decisions, d)

		// Record each axis this decision closes at the current iter
		// index. Earliest entry wins on duplicate axis IDs (a future
		// iteration re-deciding the same axis is a cycle and the
		// spawner will surface it).
		for _, axisID := range d.Axes {
			axisID = strings.TrimSpace(axisID)
			if axisID == "" {
				continue
			}
			if _, existed := s.DecidedAxesByIter[axisID]; !existed {
				s.DecidedAxesByIter[axisID] = currentIter
			}
		}

		// Append the new ID to any NewSpecNode whose pre-populated
		// Decisions[] is missing it AND whose surfacing context
		// intersects this decision's axes / surfaced_by set. The scout
		// already pre-populated existing-decision IDs onto the new
		// node; this loop adds the just-now-minted ones.
		appendDecisionIDToMatchingNewNodes(s, id, d)
	}

	if len(mintedThisCall) == 0 {
		return
	}

	// Drop any state.AxesOpen entry whose axis ID is now in
	// DecidedAxesByIter — closed axes shouldn't redispatch on a
	// subsequent decisions step within the same iteration (defensive;
	// the workflow only dispatches one decisions step per iteration).
	if len(s.AxesOpen) > 0 {
		filtered := s.AxesOpen[:0]
		for _, axis := range s.AxesOpen {
			if _, decided := s.DecidedAxesByIter[strings.TrimSpace(axis.ID)]; decided {
				continue
			}
			filtered = append(filtered, axis)
		}
		s.AxesOpen = filtered
	}

	// Re-marshal RawProposal with the appended decisions.
	out, err := json.Marshal(raw)
	if err != nil {
		slog.Warn("mergeDecisions: re-marshal RawProposal failed; state.RawProposal unchanged", "error", err)
		return
	}
	s.RawProposal = string(out)
	s.OriginalRawProposal = s.RawProposal
	rebuildInFlightIndex(s)
}

// appendDecisionIDToMatchingNewNodes adds `id` to the Decisions[]
// slice of each NewSpecNode that surfaced an axis this decision
// answers. The match key is axis-ID intersection: if any element of
// d.Axes appears in the new node's SurfacedBy chain (transitively
// through scout-emitted OpenAxis.SurfacedBy) OR the new node is named
// in d.SurfacedBy, the new node references this decision.
//
// Practical approximation: we append to every NewSpecNode whose ID is
// named in d.SurfacedBy. The scout's brief surfaces both directions
// (axes → surfacing-node IDs and new-node → covering decisions), so
// this is the canonical match. Duplicate-id append is dedup'd.
func appendDecisionIDToMatchingNewNodes(s *PlanningState, id string, d RawDecisionProposal) {
	if s == nil || len(s.NewNodesFromScout) == 0 || id == "" {
		return
	}
	surfacing := make(map[string]struct{}, len(d.SurfacedBy))
	for _, sb := range d.SurfacedBy {
		surfacing[strings.TrimSpace(sb)] = struct{}{}
	}
	for i := range s.NewNodesFromScout {
		nid := strings.TrimSpace(s.NewNodesFromScout[i].ID)
		if _, ok := surfacing[nid]; !ok {
			continue
		}
		if !stringSliceContains(s.NewNodesFromScout[i].Decisions, id) {
			s.NewNodesFromScout[i].Decisions = append(s.NewNodesFromScout[i].Decisions, id)
		}
	}
}

func stringSliceContains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// mergeNarrative parses each narrative-elaborator output as a
// RawFeatureProposal or RawStrategyProposal (id-prefix sniff) and
// replaces (or appends) the matching entry in
// state.RawProposal.Features / Strategies. Idempotent and order-
// independent.
func mergeNarrative(s *PlanningState, results []RoundResult) {
	if s == nil || len(results) == 0 {
		return
	}

	var raw RawSpecProposal
	if s.RawProposal != "" {
		if err := json.Unmarshal([]byte(s.RawProposal), &raw); err != nil {
			slog.Warn("mergeNarrative: existing RawProposal is malformed; starting fresh", "error", err)
			raw = RawSpecProposal{}
		}
	}

	featureIdx := make(map[string]int, len(raw.Features))
	for i, f := range raw.Features {
		featureIdx[f.ID] = i
	}
	strategyIdx := make(map[string]int, len(raw.Strategies))
	for i, st := range raw.Strategies {
		strategyIdx[st.ID] = i
	}

	for _, r := range results {
		if r.Err != nil || strings.TrimSpace(r.Output) == "" {
			continue
		}
		id := strings.TrimSpace(extractRawID(r.Output))
		switch {
		case strings.HasPrefix(id, "feat-"):
			var f RawFeatureProposal
			if err := json.Unmarshal([]byte(r.Output), &f); err != nil {
				slog.Warn("mergeNarrative: skipping malformed feature output", "error", err, "id", id)
				continue
			}
			if i, ok := featureIdx[f.ID]; ok {
				raw.Features[i] = f
			} else {
				raw.Features = append(raw.Features, f)
				featureIdx[f.ID] = len(raw.Features) - 1
			}
		case strings.HasPrefix(id, "strat-"):
			var st RawStrategyProposal
			if err := json.Unmarshal([]byte(r.Output), &st); err != nil {
				slog.Warn("mergeNarrative: skipping malformed strategy output", "error", err, "id", id)
				continue
			}
			if i, ok := strategyIdx[st.ID]; ok {
				raw.Strategies[i] = st
			} else {
				raw.Strategies = append(raw.Strategies, st)
				strategyIdx[st.ID] = len(raw.Strategies) - 1
			}
		default:
			slog.Warn("mergeNarrative: narrative output has unrecognized id prefix; expected feat- or strat-", "id", id)
		}
	}

	out, err := json.Marshal(raw)
	if err != nil {
		slog.Warn("mergeNarrative: re-marshal RawProposal failed; state.RawProposal unchanged", "error", err)
		return
	}
	s.RawProposal = string(out)
	s.OriginalRawProposal = s.RawProposal
	rebuildInFlightIndex(s)
}

// projectScout builds the spec_scout's user message. The scout reads
// GOALS.md + imported content + the in-flight RawProposal + concerns +
// dangling references + prior brief (on iter > 0) and emits the new
// ScoutBrief.
//
// On iter 0 the message contains only GOALS.md, imported content, and
// the existing spec flag (greenfield runs omit the latter). On every
// subsequent iteration the message also contains the prior brief, the
// in-flight proposal, the iteration's concerns, and the dangling
// references — the inputs the scout needs to re-judge convergence.
func projectScout(snap StateSnapshot[PlanningState]) []Message {
	st := snap.State
	var b strings.Builder
	b.WriteString(st.Prompt)

	if len(st.Imported) > 0 {
		b.WriteString("\n\n## Imported content (admitted via `locutus import`)\n")
		for _, doc := range st.Imported {
			fmt.Fprintf(&b, "\n### %s\n\n%s\n", doc.Path, doc.Body)
		}
	}

	if st.PriorScoutBrief != "" {
		b.WriteString("\n\n## Your prior scout brief (use this as the baseline; surface only what changed)\n\n")
		if formatted := formatScoutBrief(st.PriorScoutBrief); formatted != "" {
			b.WriteString(formatted)
		} else {
			b.WriteString(st.PriorScoutBrief)
		}
	}

	if st.RawProposal != "" {
		b.WriteString("\n\n## In-flight proposal (post-narrative; what the loop has committed so far)\n\n```json\n")
		b.WriteString(st.RawProposal)
		b.WriteString("\n```\n")
	}

	if len(st.Concerns) > 0 {
		b.WriteString("\n## Outstanding critic findings\n")
		for _, c := range st.Concerns {
			fmt.Fprintf(&b, "- [%s/%s] %s\n", c.AgentID, c.Severity, c.Text)
		}
	}

	if len(st.DanglingReferences) > 0 {
		b.WriteString("\n## Dangling decision references (integrity-critic)\n")
		for _, d := range st.DanglingReferences {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\nCorrect new_nodes[].decisions[] so every entry resolves to a decision in the proposal or the existing graph.\n")
	}

	if st.Existing != nil && !st.Existing.IsEmpty() {
		b.WriteString("\n## Existing spec is present\n\nA persisted spec snapshot exists at `.borg/spec/`; the `spec_list_manifest` and `spec_get` tools will return non-empty results. Call `spec_list_manifest` first to scan ids + summaries.\n")
	}

	return []Message{{Role: "user", Content: b.String()}}
}

// projectOpenAxis builds the spec_decision_elaborator's user message.
// Per-call inputs: GOALS.md + scout brief (for technology_options /
// watch_outs / implicit_assumptions context) + the OpenAxis being
// decided.
func projectOpenAxis(snap StateSnapshot[PlanningState]) []Message {
	st := snap.State
	var prefix strings.Builder
	prefix.WriteString(st.Prompt)
	if st.ScoutBrief != "" {
		if formatted := formatScoutBrief(st.ScoutBrief); formatted != "" {
			prefix.WriteString("\n\n## Scout brief\n\n")
			prefix.WriteString(formatted)
		}
	}

	var suffix strings.Builder
	suffix.WriteString("## Open axis to decide\n\n")
	if snap.FanoutItem == "" {
		suffix.WriteString("(missing — fanout did not populate FanoutItem)\n")
	} else {
		var axis OpenAxis
		if err := json.Unmarshal([]byte(snap.FanoutItem), &axis); err != nil {
			suffix.WriteString(snap.FanoutItem)
		} else {
			fmt.Fprintf(&suffix, "- **ID:** `%s`\n- **Description:** %s\n", axis.ID, axis.Description)
			if len(axis.SourceEvidence) > 0 {
				suffix.WriteString("- **Source evidence:**\n")
				for _, ev := range axis.SourceEvidence {
					fmt.Fprintf(&suffix, "  - %s\n", ev)
				}
			}
			if len(axis.SurfacedBy) > 0 {
				suffix.WriteString("- **Surfaced by:**\n")
				for _, sb := range axis.SurfacedBy {
					fmt.Fprintf(&suffix, "  - %s\n", sb)
				}
			}
		}
	}

	return []Message{
		{Role: "user", Content: prefix.String(), Cacheable: true},
		{Role: "user", Content: suffix.String()},
	}
}

// projectAffectedNode builds the narrative-elaborator's user message.
// Per-call inputs: GOALS.md + scout brief + the affected node (new or
// existing) + the authoritative decisions-ID list. The decisions-ID
// list is the elaborator's sole source of truth for the output's
// Decisions[] field.
func projectAffectedNode(snap StateSnapshot[PlanningState]) []Message {
	st := snap.State
	var prefix strings.Builder
	prefix.WriteString(st.Prompt)
	if st.ScoutBrief != "" {
		if formatted := formatScoutBrief(st.ScoutBrief); formatted != "" {
			prefix.WriteString("\n\n## Scout brief\n\n")
			prefix.WriteString(formatted)
		}
	}
	if st.RawProposal != "" {
		prefix.WriteString("\n\n## In-flight proposal (sibling features / strategies for situational context)\n\n```json\n")
		prefix.WriteString(st.RawProposal)
		prefix.WriteString("\n```\n")
	}

	var suffix strings.Builder
	var item affectedNodeItem
	if snap.FanoutItem != "" {
		_ = json.Unmarshal([]byte(snap.FanoutItem), &item)
	}

	suffix.WriteString(fmt.Sprintf("## %s to elaborate\n\n", item.Kind))
	fmt.Fprintf(&suffix, "- **ID:** `%s`\n", item.ID)

	switch {
	case item.NewNode != nil:
		fmt.Fprintf(&suffix, "- **Title:** %s\n", item.NewNode.Title)
		fmt.Fprintf(&suffix, "- **Summary:** %s\n", item.NewNode.Summary)
		suffix.WriteString("- **Origin:** newly identified by the scout this iteration; no prior body exists.\n")
	case item.ExistingFeature != nil:
		fmt.Fprintf(&suffix, "- **Title:** %s\n", item.ExistingFeature.Title)
		suffix.WriteString("\n### Prior content (the version you are revising)\n\n```json\n")
		if data, err := json.MarshalIndent(item.ExistingFeature, "", "  "); err == nil {
			suffix.Write(data)
		} else {
			suffix.WriteString(snap.FanoutItem)
		}
		suffix.WriteString("\n```\n")
	case item.ExistingStrategy != nil:
		fmt.Fprintf(&suffix, "- **Title:** %s\n", item.ExistingStrategy.Title)
		fmt.Fprintf(&suffix, "- **Kind:** %s\n", item.ExistingStrategy.Kind)
		suffix.WriteString("\n### Prior content (the version you are revising)\n\n```json\n")
		if data, err := json.MarshalIndent(item.ExistingStrategy, "", "  "); err == nil {
			suffix.Write(data)
		} else {
			suffix.WriteString(snap.FanoutItem)
		}
		suffix.WriteString("\n```\n")
	default:
		suffix.WriteString("(no node payload — the fanout dispatcher did not populate FanoutItem; this is a wiring bug)\n")
	}

	suffix.WriteString("\n### Authoritative decisions list (copy verbatim into output.decisions[])\n\n")
	if len(item.Decisions) == 0 {
		suffix.WriteString("(empty — every axis this node depends on is still open. Defer to the workflow integrity check.)\n")
	} else {
		for _, d := range item.Decisions {
			fmt.Fprintf(&suffix, "- `%s`\n", d)
		}
	}

	return []Message{
		{Role: "user", Content: prefix.String(), Cacheable: true},
		{Role: "user", Content: suffix.String()},
	}
}

