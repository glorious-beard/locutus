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
	"github.com/chetan/locutus/internal/spec"
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
			return nil, nil, fmt.Errorf("spec-scout brief at iter %d: %w", myIter, err)
		}
		// DJ-125 Phase 7: convergence is brief.Converged AND no
		// concern is still open after the scout's dispositions land
		// on state. The mergeScoutBrief step has already applied
		// ConcernDispositions to state.Concerns by the time this
		// spawner fires, so countOpenConcerns reads the effective
		// status. A scout claiming converged=true while open
		// concerns remain is rejected — the scout must either
		// dispose every open concern or report converged=false.
		openCount := countOpenConcerns(&snap.State)
		// DJ-129: convergence requires dimension stability — a scout
		// that surfaces a new critique dimension this iteration must
		// run at least one more iteration so the new dimension's
		// critic-elaborator gets a chance to surface concerns. Per
		// design decision #7, retirement does not block; only
		// new-addition does. mergeScoutBrief captured the stability
		// decision into LastDimensionsStable BEFORE folding the new
		// dims into CritiqueDimensionsByIter, so reading it here
		// reflects the prior map shape (the correct comparison).
		//
		// Empty CurrentCritiqueDimensions is trivially stable — this
		// also covers test paths that drive the spawner with hand-
		// built state that never went through mergeScoutBrief.
		dimsStable := len(snap.State.CurrentCritiqueDimensions) == 0 || snap.State.LastDimensionsStable
		if brief.Converged && openCount == 0 && dimsStable {
			return nil, nil, nil
		}
		if brief.Converged && openCount > 0 {
			return nil, nil, fmt.Errorf(
				"spec-scout claims converged=true at iter %d but %d concern(s) remain open after dispositions — the scout must dispose every open concern (addressed; wontfix) or report converged=false",
				myIter+1, openCount,
			)
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

		// DJ-126 Phase 4: per-axis revision-count cap. When any axis
		// has been revised at or above the cap (default 3; env override
		// LOCUTUS_DECISION_REVISION_CAP), the revise dispatch is
		// oscillating on that axis rather than converging.
		// Force-terminate with convergence_revision_capped — distinct
		// from convergence_stuck (which flags scout-side reopens) so
		// the two failure modes stay diagnosable.
		if capped := axesExceedingRevisionCap(&snap.State, readDecisionRevisionCap()); len(capped) > 0 {
			terminal := scoutConvergenceRevisionCappedTerminal(historian, &snap.State, brief, myIter, capped)
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

// parseScoutBriefFromResults pulls the first non-error spec-scout
// result off the round and decodes it as a ScoutBrief. Surfaces decode
// failures and missing results as errors so the spawner can attribute
// them to the iteration.
func parseScoutBriefFromResults(results []RoundResult) (*ScoutBrief, error) {
	for _, r := range results {
		if r.AgentID != "spec-scout" {
			continue
		}
		if r.Err != nil {
			return nil, fmt.Errorf("spec-scout run failed: %w", r.Err)
		}
		if strings.TrimSpace(r.Output) == "" {
			return nil, fmt.Errorf("spec-scout returned empty output")
		}
		var brief ScoutBrief
		if err := json.Unmarshal([]byte(r.Output), &brief); err != nil {
			return nil, fmt.Errorf("parse spec-scout brief: %w (content=%q)", err, r.Output)
		}
		return &brief, nil
	}
	return nil, fmt.Errorf("no spec-scout result in round")
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
		Agents: []string{"spec-scout"},
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
		Agents: []string{"spec-scout"},
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

// scoutConvergenceRevisionCappedTerminal is the per-axis-revision-cap
// terminal for the scout-driven loop. Mirrors scoutConvergenceStuckTerminal
// but is keyed off AxisRevisionCount rather than DecidedAxesByIter — a
// different failure mode (revise-side oscillation rather than scout-
// side reopen) that warrants its own DJ-103 event kind.
//
// DJ-128 cap-as-commit: rather than erroring out, this terminal
// commits the latest revision of each capped decision and lets the
// scout's normal convergence rule fire on the next iteration.
//
// Steps:
//
//  1. Mark Locked: true on each in-flight decision whose axes
//     intersect the capped set so the deliberation log carries the
//     cap signal and subsequent revise dispatches skip these
//     decisions (Phase 6 filter in hasReviseableConcerns).
//  2. Flip every open concern whose RelatedDecisionIDs name a locked
//     decision to Status=wontfix with a justification naming the cap.
//     The scout's next-iteration grading pass sees the wontfix
//     dispositions and Converged=true naturally.
//  3. Record a convergence_revision_capped DJ-103 event for the
//     operator's history view.
//  4. Record one decision_locked DJ-103 event per locked decision so
//     `locutus history` distinguishes cap-as-commit signals from
//     normal revisions.
//
// Returns nil error: the cap is no longer a workflow failure under
// DJ-128. The next-iteration template still spawns and the scout's
// regular convergence rule fires naturally on the next call.
func scoutConvergenceRevisionCappedTerminal(historian *history.Historian, snapState *PlanningState, brief *ScoutBrief, iter int, capped []string) WorkflowStep[PlanningState] {
	terminalID := fmt.Sprintf("convergence_revision_capped_iter:%d", iter)
	cap := readDecisionRevisionCap()
	cappedCopy := append([]string(nil), capped...)
	cappedAxisIDs := axisIDsExceedingRevisionCap(snapState, cap)
	briefCopy := brief
	// Capture the iteration / cap config in the closure so the merge
	// handler has the same parameters the RunItem saw.
	return WorkflowStep[PlanningState]{
		ID:     terminalID,
		Agents: []string{"spec-scout"},
		RunItem: func(_ context.Context, _ StateSnapshot[PlanningState]) (string, error) {
			// The RunItem path operates on a snapshot, not the
			// orchestrator state; the Merge below applies the lock /
			// wontfix mutations to the real state, plus emits the
			// DJ-103 events. nil error means the loop ends with
			// cap-as-commit semantics rather than the pre-DJ-128
			// error-and-exit.
			return "ok", nil
		},
		Merge: func(s *PlanningState, _ []RoundResult) {
			// (1) Lock the capped decisions on the live orchestrator
			// state; collect their bodies + concerns for the per-
			// decision event.
			lockedDecisions := lockCappedDecisions(s, cappedAxisIDs)
			// (2) Flip the contested concerns to wontfix in place.
			flipConcernsToWontfix(s, lockedDecisions, iter, cap)
			// (3) Record the aggregate convergence_revision_capped
			// event for operator visibility.
			snapshotSpec := s.ProposedSpec
			concerns := append([]Concern(nil), s.Concerns...)
			if historian != nil {
				evt := buildScoutConvergenceRevisionCappedEvent(briefCopy, iter, cap, snapshotSpec, concerns, cappedCopy)
				if err := historian.Record(evt); err != nil {
					slog.Warn("convergence_revision_capped: failed to record DJ-103 event", "err", err)
				}
				// (4) One decision_locked event per locked decision.
				for _, ld := range lockedDecisions {
					devt := buildDecisionLockedEvent(ld.Decision, ld.ContestedConcerns, iter, cap)
					if err := historian.Record(devt); err != nil {
						slog.Warn("decision_locked: failed to record DJ-103 event", "err", err, "id", ld.Decision.ID)
					}
				}
			}
		},
	}
}

// lockedDecisionRecord pairs a capped decision with the concerns
// that were contesting it at lock time. Used by the cap-as-commit
// terminal to record one decision_locked DJ-103 event per locked
// decision with the contested-concerns block in the rationale.
type lockedDecisionRecord struct {
	Decision          RawDecisionProposal
	ContestedConcerns []Concern
}

// lockCappedDecisions sets Locked=true on each in-flight decision
// whose Axes[] intersects the capped axis IDs. Re-marshals the
// state.RawProposal so subsequent merge calls see the locked flag.
// Returns the per-decision records the terminal uses to write the
// decision_locked DJ-103 events.
func lockCappedDecisions(s *PlanningState, cappedAxisIDs []string) []lockedDecisionRecord {
	if s == nil || len(cappedAxisIDs) == 0 || s.RawProposal == "" {
		return nil
	}
	var raw RawSpecProposal
	if err := json.Unmarshal([]byte(s.RawProposal), &raw); err != nil {
		slog.Warn("lockCappedDecisions: malformed RawProposal; skipping", "err", err)
		return nil
	}
	cappedSet := make(map[string]struct{}, len(cappedAxisIDs))
	for _, a := range cappedAxisIDs {
		cappedSet[strings.TrimSpace(a)] = struct{}{}
	}
	var records []lockedDecisionRecord
	mutated := false
	for i := range raw.Decisions {
		d := &raw.Decisions[i]
		intersects := false
		for _, axis := range d.Axes {
			if _, ok := cappedSet[strings.TrimSpace(axis)]; ok {
				intersects = true
				break
			}
		}
		if !intersects {
			continue
		}
		mutated = true
		records = append(records, lockedDecisionRecord{
			Decision:          *d,
			ContestedConcerns: openConcernsTargeting(s, d.ID),
		})
	}
	if mutated {
		// Re-marshal RawProposal so downstream consumers see the
		// effects of any state changes (currently none on raw, but
		// reserved for when persistence picks up Locked from the
		// proposal body).
		if out, err := json.Marshal(raw); err == nil {
			s.RawProposal = string(out)
		}
	}
	// Mark the locked records on PlanningState so the SpecProposal
	// returned from GenerateSpec can carry Locked=true through to the
	// persistence layer.
	if s.LockedDecisionIDs == nil {
		s.LockedDecisionIDs = make(map[string]struct{})
	}
	for _, r := range records {
		s.LockedDecisionIDs[r.Decision.ID] = struct{}{}
	}
	return records
}

// openConcernsTargeting returns the open concerns whose
// RelatedDecisionIDs contain priorID. Snapshot before mutation so
// the event's rationale carries the as-flagged concern text.
func openConcernsTargeting(s *PlanningState, priorID string) []Concern {
	if s == nil || priorID == "" {
		return nil
	}
	var out []Concern
	for i := range s.Concerns {
		c := s.Concerns[i]
		if effectiveConcernStatus(&c) != ConcernStatusOpen {
			continue
		}
		if !stringSliceContains(c.RelatedDecisionIDs, priorID) {
			continue
		}
		copy := c
		if len(c.RelatedDecisionIDs) > 0 {
			copy.RelatedDecisionIDs = append([]string(nil), c.RelatedDecisionIDs...)
		}
		if len(c.RelatedAxisIDs) > 0 {
			copy.RelatedAxisIDs = append([]string(nil), c.RelatedAxisIDs...)
		}
		if len(c.Counterproposals) > 0 {
			copy.Counterproposals = append([]CriticCounterproposal(nil), c.Counterproposals...)
		}
		out = append(out, copy)
	}
	return out
}

// flipConcernsToWontfix walks state.Concerns and marks any open
// concern whose RelatedDecisionIDs intersects a locked decision id
// as Status=wontfix with a justification naming the cap firing.
// Concerns naming a mix of locked and unlocked decisions stay open
// for the unlocked ones (Phase 6's hasReviseableConcerns filter
// keeps them dispatchable on the unlocked axis).
func flipConcernsToWontfix(s *PlanningState, lockedDecisions []lockedDecisionRecord, iter, cap int) {
	if s == nil || len(lockedDecisions) == 0 {
		return
	}
	lockedIDs := make(map[string]struct{}, len(lockedDecisions))
	for _, r := range lockedDecisions {
		lockedIDs[r.Decision.ID] = struct{}{}
	}
	justification := fmt.Sprintf(
		"Axis hit revision cap of %d at iter %d; council could not resolve critic↔elaborator disagreement; the current decision is committed as the ship-quality answer; the alternatives slice carries the contested reasoning.",
		cap, iter+1,
	)
	for i := range s.Concerns {
		c := &s.Concerns[i]
		if effectiveConcernStatus(c) != ConcernStatusOpen {
			continue
		}
		intersectsLocked := false
		hasUnlocked := false
		for _, did := range c.RelatedDecisionIDs {
			if _, ok := lockedIDs[strings.TrimSpace(did)]; ok {
				intersectsLocked = true
			} else {
				hasUnlocked = true
			}
		}
		if !intersectsLocked {
			continue
		}
		if hasUnlocked {
			// Concern still actionable on the unlocked decision(s);
			// leave its status alone. hasReviseableConcerns / fanout
			// filter locked-only matches separately.
			continue
		}
		c.Status = ConcernStatusWontfix
		c.Justification = justification
	}
}

// buildDecisionLockedEvent records the cap-as-commit signal for one
// decision. Distinct from decision_revised so `locutus history` can
// distinguish a normal revision from a cap-fired commit.
func buildDecisionLockedEvent(decision RawDecisionProposal, contestedConcerns []Concern, iter, cap int) history.Event {
	now := time.Now()
	var rationale strings.Builder
	fmt.Fprintf(&rationale, "Decision %s locked at iter %d: per-axis revision cap of %d fired and the council committed the latest revision as the ship-quality answer.",
		decision.ID, iter+1, cap)
	if len(contestedConcerns) > 0 {
		rationale.WriteString("\n\nContested concerns flipped to wontfix:")
		for _, c := range contestedConcerns {
			fmt.Fprintf(&rationale, "\n- [%s/%s] %s", c.AgentID, c.Severity, c.Text)
		}
	}
	body, _ := json.MarshalIndent(decision, "", "  ")
	return history.Event{
		ID:        history.EventID("decision_locked", decision.ID, now),
		Timestamp: now,
		Kind:      "decision_locked",
		TargetID:  decision.ID,
		Rationale: rationale.String(),
		NewValue:  string(body),
	}
}

func buildScoutConvergenceRevisionCappedEvent(brief *ScoutBrief, iter, cap int, snapshotSpec string, concerns []Concern, capped []string) history.Event {
	now := time.Now()
	var rationale strings.Builder
	fmt.Fprintf(&rationale, "spec-council revision cap of %d hit at iter %d on %d axis/axes.", cap, iter+1, len(capped))
	if len(capped) > 0 {
		rationale.WriteString("\n\nCapped axes:")
		for _, c := range capped {
			fmt.Fprintf(&rationale, "\n- %s", c)
		}
	}
	if brief != nil && len(brief.AxesOpen) > 0 {
		rationale.WriteString("\n\nAxes still open at termination:")
		for _, a := range brief.AxesOpen {
			fmt.Fprintf(&rationale, "\n- %s: %s", a.ID, a.Description)
		}
	}
	if len(concerns) > 0 {
		rationale.WriteString("\n\nUnresolved concerns at termination:")
		for _, c := range concerns {
			fmt.Fprintf(&rationale, "\n- [%s/%s/%s] %s", c.AgentID, c.Severity, c.Status, c.Text)
		}
	}
	return history.Event{
		ID:        history.EventID("convergence_revision_capped", "", now),
		Timestamp: now,
		Kind:      "convergence_revision_capped",
		Rationale: rationale.String(),
		NewValue:  snapshotSpec,
	}
}

func buildScoutConvergenceStuckEvent(brief *ScoutBrief, iter int, snapshotSpec string, concerns []Concern, reopened []string) history.Event {
	now := time.Now()
	var rationale strings.Builder
	fmt.Fprintf(&rationale, "spec-scout reopened decided axes at iter %d.", iter+1)
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
	fmt.Fprintf(&rationale, "spec-scout did not converge within %d iteration(s); budget exhausted at iter %d.", budget, iter+1)
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

// countOpenConcerns returns the number of concerns whose effective
// status is open. Concerns without an explicit Status (legacy
// pre-DJ-125 entries) count as open. DJ-125 Phase 7: the scout
// convergence rule reads this to confirm no open concerns remain
// before accepting brief.Converged.
func countOpenConcerns(s *PlanningState) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, c := range s.Concerns {
		status := c.Status
		if status == "" {
			status = ConcernStatusOpen
		}
		if status == ConcernStatusOpen {
			n++
		}
	}
	return n
}

// mechanicalDisposeConcerns walks state.Concerns with Status == open
// and stales those whose related axis has been settled (or whose
// related decision is now present in the graph for a "missing X" finding).
// DJ-125 Phase 6: cheap regex/lookup pass; no LLM call. Reduces the
// scout's grading load to the judgment-call findings (contradictions,
// factual claims, integration gaps).
//
// Rules:
//   - If any RelatedAxisID is recorded in DecidedAxesByIter →
//     Status = stale. The axis has been decided; whatever concern
//     was raised about its absence is now answered.
//   - Else if any RelatedDecisionID is present in the in-flight
//     proposal or state.Existing AND the concern's text matches a
//     "missing X" pattern → Status = stale. The decision now exists.
//     Conservative on the missing-pattern check: only fires when the
//     concern text begins with words like "missing", "no", "lacks",
//     "absent", etc., so contradictions ("dec-a and dec-b conflict")
//     stay open for the scout to grade.
//   - Else leave Status = open.
//
// Each transition logs at debug level so the forensic trail captures
// which concerns aged out mechanically vs which the scout disposed.
// Idempotent: a second call produces the same final state.
func mechanicalDisposeConcerns(s *PlanningState) {
	if s == nil || len(s.Concerns) == 0 {
		return
	}

	// Build the lookup sets once per call: settled axes and known
	// decision IDs (in-flight + existing).
	settledAxes := make(map[string]struct{}, len(s.DecidedAxesByIter))
	for id := range s.DecidedAxesByIter {
		settledAxes[id] = struct{}{}
	}
	knownDecisions := collectKnownDecisionIDs(s)

	for i := range s.Concerns {
		c := &s.Concerns[i]
		if c.Status != ConcernStatusOpen && c.Status != "" {
			// Leave addressed / stale / wontfix alone — already
			// disposed by a prior pass or the scout.
			continue
		}
		// Default an empty Status (legacy) to open before any
		// transition so the post-pass shape is uniform.
		if c.Status == "" {
			c.Status = ConcernStatusOpen
		}

		// (1) Stale on any settled related axis.
		if anyInSet(c.RelatedAxisIDs, settledAxes) {
			slog.Debug("concern auto-stale: related axis settled",
				"concern_text", truncate(c.Text, 80),
				"related_axes", c.RelatedAxisIDs)
			c.Status = ConcernStatusStale
			continue
		}

		// (2) Stale on a missing-X pattern where every named
		//     decision is now in the graph.
		if isMissingPatternConcern(c.Text) && len(c.RelatedDecisionIDs) > 0 && allInSet(c.RelatedDecisionIDs, knownDecisions) {
			slog.Debug("concern auto-stale: missing-X pattern resolved",
				"concern_text", truncate(c.Text, 80),
				"related_decisions", c.RelatedDecisionIDs)
			c.Status = ConcernStatusStale
			continue
		}
	}
}

// collectKnownDecisionIDs returns the union of decision IDs in the
// in-flight proposal and state.Existing. Used by the mechanical
// dispose pass to detect "decision now exists" transitions.
func collectKnownDecisionIDs(s *PlanningState) map[string]struct{} {
	out := make(map[string]struct{})
	if s == nil {
		return out
	}
	if strings.TrimSpace(s.RawProposal) != "" {
		var prop RawSpecProposal
		if err := json.Unmarshal([]byte(s.RawProposal), &prop); err == nil {
			for _, d := range prop.Decisions {
				if id := strings.TrimSpace(d.ID); id != "" {
					out[id] = struct{}{}
				}
			}
		}
	}
	if s.Existing != nil {
		for _, d := range s.Existing.Decisions {
			if id := strings.TrimSpace(d.ID); id != "" {
				out[id] = struct{}{}
			}
		}
	}
	return out
}

// anyInSet reports whether any element of ids is in set.
func anyInSet(ids []string, set map[string]struct{}) bool {
	for _, id := range ids {
		if _, ok := set[strings.TrimSpace(id)]; ok {
			return true
		}
	}
	return false
}

// allInSet reports whether every element of ids is in set. Returns
// false on an empty ids slice (callers gate on len > 0).
func allInSet(ids []string, set map[string]struct{}) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if _, ok := set[strings.TrimSpace(id)]; !ok {
			return false
		}
	}
	return true
}

// missingPatternPrefixes are the lowercase leading tokens that mark a
// concern as a "missing-X" finding. Conservative list — only stale
// concerns whose text starts with one of these. Contradiction
// findings ("dec-a and dec-b conflict") don't begin with these and
// stay open for the scout to grade.
var missingPatternPrefixes = []string{
	"missing",
	"no ",
	"lacks",
	"lacking",
	"absent",
	"absence",
	"need",
	"needs",
	"should add",
	"should include",
	"should commit",
	"never commits",
	"never specifies",
	"never says",
	"not committed",
	"not specified",
	"not addressed",
	"undefined",
	"unspecified",
	"undecided",
	"untouched",
	"unstated",
}

// isMissingPatternConcern reports whether the concern text begins
// with a "missing X" lead-in. Case-insensitive on the prefix.
func isMissingPatternConcern(text string) bool {
	t := strings.TrimSpace(strings.ToLower(text))
	for _, p := range missingPatternPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

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
// state.AxesOpen. Each item drives one spec-decision-elaborator call.
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
				AgentID:         "spec-feature-elaborator",
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
				AgentID:          "spec-strategy-elaborator",
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
		return "spec-strategy-elaborator"
	case "feature":
		return "spec-feature-elaborator"
	default:
		return "spec-feature-elaborator"
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

// mergeCandidateSurveys parses each spec-candidate-survey output as a
// CandidateList and stores it in state.AxisSurveys keyed by the
// OpenAxis ID the call was dispatched on (DJ-132). The axis ID is read
// off RoundResult.FanoutItem (the JSON-marshaled OpenAxis the fanout
// dispatcher set on the call). The map is reset at the start of every
// merge call so stale surveys from prior iterations don't leak through
// when the next iteration's open-axis set changes.
//
// Malformed responses (parse failure, empty candidates) are dropped
// silently — the elaborator's projection falls through to its own
// enumeration when no survey is present for an axis, identical to the
// pre-DJ-132 behaviour. The reversal criterion (a) on DJ-132 is the
// signal that survey coverage is too thin; logging silently keeps the
// workflow degradable rather than failing the run on a survey misfire.
func mergeCandidateSurveys(s *PlanningState, results []RoundResult) {
	if s == nil {
		return
	}
	// Reset every iteration. Empty or partially-failed iterations
	// land an empty map so projectOpenAxis sees no survey for any
	// axis and the elaborator runs as it did pre-DJ-132.
	s.AxisSurveys = nil
	if len(results) == 0 {
		return
	}
	out := make(map[string]CandidateList, len(results))
	for _, r := range results {
		if r.Err != nil || strings.TrimSpace(r.Output) == "" {
			continue
		}
		if strings.TrimSpace(r.FanoutItem) == "" {
			slog.Warn("mergeCandidateSurveys: survey result has empty FanoutItem; cannot correlate to axis", "step", r.StepID)
			continue
		}
		var axis OpenAxis
		if err := json.Unmarshal([]byte(r.FanoutItem), &axis); err != nil {
			slog.Warn("mergeCandidateSurveys: skipping survey with malformed FanoutItem", "error", err)
			continue
		}
		axisID := strings.TrimSpace(axis.ID)
		if axisID == "" {
			slog.Warn("mergeCandidateSurveys: survey FanoutItem missing axis ID")
			continue
		}
		var list CandidateList
		if err := json.Unmarshal([]byte(r.Output), &list); err != nil {
			slog.Warn("mergeCandidateSurveys: skipping malformed survey output", "axis", axisID, "error", err)
			continue
		}
		if len(list.Candidates) == 0 {
			// Empty candidates is a degenerate response; treat it as
			// no survey for this axis so the elaborator falls through.
			slog.Warn("mergeCandidateSurveys: survey returned no candidates; falling through to elaborator's own enumeration", "axis", axisID)
			continue
		}
		out[axisID] = list
	}
	if len(out) > 0 {
		s.AxisSurveys = out
	}
}

// projectCandidateSurvey builds the spec-candidate-survey's user
// message. Per-call inputs: GOALS.md + scout brief (for the axis's
// initial framing via technology_options + the project context the
// survey filters against) + the in-flight manifest (for existing
// decisions that constrain the candidate space on adjacent axes) +
// the OpenAxis being surveyed in full.
//
// Mirrors projectOpenAxis's shape (same prefix structure) so the
// per-axis dispatch carries equivalent context to both calls. The
// survey's prompt does enumeration with the same scoping inputs the
// elaborator will later use for judgment.
func projectCandidateSurvey(snap StateSnapshot[PlanningState]) []Message {
	st := snap.State
	var prefix strings.Builder
	prefix.WriteString(st.Prompt)
	if st.ScoutBrief != "" {
		if formatted := formatScoutBrief(st.ScoutBrief); formatted != "" {
			prefix.WriteString("\n\n## Scout brief\n\n")
			prefix.WriteString(formatted)
		}
	}
	if rendered := renderManifestForProjection(&st); rendered != "" {
		prefix.WriteString("\n\n## In-flight spec manifest (use spec_get to fetch full content of any node)\n\n")
		prefix.WriteString(rendered)
	}

	var suffix strings.Builder
	suffix.WriteString("## Open axis to survey\n\n")
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

// mergeDecisions parses each decision-elaborator output as a
// RawDecisionProposal and either appends it (first-author dispatch
// path) or replaces an existing decision in-place by ID match (revise
// dispatch path).
//
// ID match semantics (DJ-133):
//   - Under DJ-133 the elaborator copies the axis ID verbatim into
//     incoming.ID (per its prompt's "### id" section), so revise
//     dispatches arrive with incoming.ID equal to the prior decision's
//     ID. Match is exact-string equality:
//
//   - incoming.ID matches an in-flight decision's ID → REPLACE in
//     place. Body, rationale, alternatives, and citations are
//     overwritten by the revision; the merge-layer preservation
//     helpers (demote prior chosen / fold counterproposals / preserve
//     prior alternatives) carry the deliberation log forward. Every
//     open concern whose RelatedDecisionIDs contains the preserved ID
//     is marked Status=addressed.
//   - incoming.ID matches an existing-graph decision's ID → append the
//     revision under the existing ID. The persisted node is replaced
//     at GenerateSpec persist time when the raw proposal carries the
//     revised body at the same ID.
//   - No match → first-author append. The incoming ID is preserved as
//     authored; mintDecisionID is the defensive fallback for the
//     degenerate case where the elaborator left ID empty or somehow
//     produced a duplicate slug (shouldn't happen under the axis-as-ID
//     contract, but the slug-mint fallback keeps the merge robust to
//     prompt drift).
//
// The pre-DJ-133 axis-intersection match retired with the elaborator's
// slug-from-chosen mint logic; the ambiguous-revision integrity
// concern retired with it (two decisions sharing the same axis now
// have the same ID by construction, which the persisted-graph
// integrity check surfaces as a duplicate-ID violation downstream).
//
// Idempotent — calling twice with the same results produces the same
// final state.
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
	var replacedThisCall []string
	for _, r := range results {
		if r.Err != nil || strings.TrimSpace(r.Output) == "" {
			continue
		}
		var d RawDecisionProposal
		if err := json.Unmarshal([]byte(r.Output), &d); err != nil {
			slog.Warn("mergeDecisions: skipping malformed decision-elaborator output", "error", err)
			continue
		}

		// DJ-133: replace-by-ID. The elaborator copies the axis ID
		// verbatim into incoming.ID (per its prompt's "### id"
		// section); revise dispatches arrive with incoming.ID equal to
		// the prior decision's ID. Match by exact string equality
		// against the in-flight proposal first, then against the
		// persisted graph.
		incomingID := strings.TrimSpace(d.ID)
		matchedInFlight := -1
		var matchedExistingID string
		if incomingID != "" {
			for i, prior := range raw.Decisions {
				if prior.ID == incomingID {
					matchedInFlight = i
					break
				}
			}
			if matchedInFlight < 0 && s.Existing != nil {
				for _, ed := range s.Existing.Decisions {
					if ed.ID == incomingID {
						matchedExistingID = ed.ID
						break
					}
				}
			}
		}

		switch {
		case matchedInFlight >= 0:
			// Replace an in-flight decision in place.
			idx := matchedInFlight
			prior := raw.Decisions[idx]
			priorID := prior.ID
			d.ID = priorID
			// DJ-128: snapshot driving concerns BEFORE the deliberation-
			// log helpers run (and BEFORE markConcernsAddressedByRevision
			// flips statuses) so they see the concerns in as-flagged
			// form. The counterproposal-fold helper needs the snapshot
			// to fold each unpicked counterproposal into alternatives;
			// queueDecisionRevisedEvent will re-capture the same set
			// (independently) for the history event.
			driving := drivingConcernsForDecision(s, priorID)
			// DJ-128 + post-fifth-winplan revision: alternative
			// preservation is now mechanical at the merge boundary
			// rather than a prompt-discipline mandate enforced by
			// reject-on-violation. Three folds run in order:
			//   1. demote prior chosen → alternative on Flip
			//   2. fold critic counterproposals as alternatives
			//   3. preserve any prior alternatives the elaborator
			//      omitted (the new step; replaces the old
			//      validate-then-reject path)
			// The elaborator's prompt now says "you don't need to
			// enumerate every prior alternative; the merge layer
			// preserves them." It focuses on chosen + new
			// alternatives + counterproposal engagement; structural
			// preservation is the merge layer's job.
			demotePriorChosenAsAlternative(&prior, &d, driving, currentIter)
			foldedCount := foldCounterproposalsAsAlternatives(&d, driving, currentIter, prior.Title)
			if foldedCount > 0 {
				recordCounterproposalFoldNotice(s, priorID, foldedCount)
			}
			preservedCount := preservePriorAlternatives(&prior, &d, currentIter)
			if preservedCount > 0 {
				recordPreservedAlternativesNotice(s, priorID, preservedCount)
			}
			raw.Decisions[idx] = d
			replacedThisCall = append(replacedThisCall, priorID)
			// Record axes for cycle/iteration tracking. earliest-wins
			// preserves the iter index of the original first-author
			// decision rather than the revise iteration.
			for _, axisID := range d.Axes {
				axisID = strings.TrimSpace(axisID)
				if axisID == "" {
					continue
				}
				if _, existed := s.DecidedAxesByIter[axisID]; !existed {
					s.DecidedAxesByIter[axisID] = currentIter
				}
			}
			incrementAxisRevisionCounts(s, d.Axes)
			appendDecisionIDToMatchingNewNodes(s, priorID, d)
			// DJ-126 Phase 5: queue a decision_revised history event
			// BEFORE markConcernsAddressedByRevision flips statuses, so
			// the event's rationale carries the as-flagged concern text.
			queueDecisionRevisedEvent(s, prior, d, priorID, currentIter)
			markConcernsAddressedByRevision(s, priorID, &d, currentIter)
			continue

		case matchedExistingID != "":
			// The matched decision lives in the persisted graph
			// (state.Existing) — append the revision under the existing
			// id rather than appending a duplicate. The persisted node
			// itself is replaced at GenerateSpec persist time when the
			// raw proposal carries the revised version under the same id.
			priorID := matchedExistingID
			var prior RawDecisionProposal
			if s.Existing != nil {
				for _, ed := range s.Existing.Decisions {
					if ed.ID == priorID {
						prior = decisionToRawProposal(ed)
						break
					}
				}
			}
			d.ID = priorID
			driving := drivingConcernsForDecision(s, priorID)
			// Same merge-side preservation as the in-flight branch
			// above; see that branch's comment for the rationale.
			demotePriorChosenAsAlternative(&prior, &d, driving, currentIter)
			foldedCount := foldCounterproposalsAsAlternatives(&d, driving, currentIter, prior.Title)
			if foldedCount > 0 {
				recordCounterproposalFoldNotice(s, priorID, foldedCount)
			}
			preservedCount := preservePriorAlternatives(&prior, &d, currentIter)
			if preservedCount > 0 {
				recordPreservedAlternativesNotice(s, priorID, preservedCount)
			}
			raw.Decisions = append(raw.Decisions, d)
			usedIDs[priorID] = struct{}{}
			replacedThisCall = append(replacedThisCall, priorID)
			for _, axisID := range d.Axes {
				axisID = strings.TrimSpace(axisID)
				if axisID == "" {
					continue
				}
				if _, existed := s.DecidedAxesByIter[axisID]; !existed {
					s.DecidedAxesByIter[axisID] = currentIter
				}
			}
			incrementAxisRevisionCounts(s, d.Axes)
			appendDecisionIDToMatchingNewNodes(s, priorID, d)
			queueDecisionRevisedEvent(s, prior, d, priorID, currentIter)
			markConcernsAddressedByRevision(s, priorID, &d, currentIter)
			continue
		}

		// First-author path: no axis intersection. Mint or preserve the
		// incoming id, then append.
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

	if len(mintedThisCall) == 0 && len(replacedThisCall) == 0 {
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
	// DJ-125 Phase 6: a fresh decision may settle an axis that prior
	// concerns flagged. Re-run the mechanical dispose pass so the
	// scout's next pass sees those concerns as stale rather than open.
	mechanicalDisposeConcerns(s)
}

// incrementAxisRevisionCounts bumps state.AxisRevisionCount for each
// axis in axes by 1. Lazily initialises the map. Called by
// mergeDecisions after a replace-by-axis-ID match fires so the per-
// axis cap detector (axesExceedingRevisionCap) can flag runaway
// revision oscillation.
func incrementAxisRevisionCounts(s *PlanningState, axes []string) {
	if s == nil || len(axes) == 0 {
		return
	}
	if s.AxisRevisionCount == nil {
		s.AxisRevisionCount = make(map[string]int)
	}
	for _, a := range axes {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		s.AxisRevisionCount[a]++
	}
}

// axesExceedingRevisionCap returns the axis IDs whose revision count
// is at or above cap. Used by the scout spawner's per-axis cap check
// (DJ-126 Phase 4) — when any axis hits the cap, the loop force-
// terminates with a convergence_revision_capped event.
//
// Returns a stable sorted list so error messages and history events
// are reproducible across runs.
func axesExceedingRevisionCap(s *PlanningState, cap int) []string {
	if s == nil || cap <= 0 || len(s.AxisRevisionCount) == 0 {
		return nil
	}
	var over []string
	for axisID, count := range s.AxisRevisionCount {
		if count >= cap {
			over = append(over, fmt.Sprintf("%s (revised %d×)", axisID, count))
		}
	}
	sort.Strings(over)
	return over
}

// axisIDsExceedingRevisionCap returns just the axis IDs (no "revised
// N×" suffix) that hit cap. Used by the cap-as-commit terminal
// (DJ-128) to look up which decisions to flip to Locked. Sorted for
// determinism.
func axisIDsExceedingRevisionCap(s *PlanningState, cap int) []string {
	if s == nil || cap <= 0 || len(s.AxisRevisionCount) == 0 {
		return nil
	}
	var ids []string
	for axisID, count := range s.AxisRevisionCount {
		if count >= cap {
			ids = append(ids, axisID)
		}
	}
	sort.Strings(ids)
	return ids
}

// queueDecisionRevisedEvent appends a PendingDecisionRevisedEvent
// entry to state for every replace-by-axis-ID match in
// mergeDecisions. The wrapper Merge closure drains the queue through
// the historian after each merge call. Driving concerns are captured
// here (before markConcernsAddressedByRevision flips statuses) so the
// emitted event records the concern in its open form.
func queueDecisionRevisedEvent(s *PlanningState, prior, revised RawDecisionProposal, priorID string, iter int) {
	if s == nil {
		return
	}
	var driving []Concern
	for i := range s.Concerns {
		c := s.Concerns[i]
		if effectiveConcernStatus(&c) != ConcernStatusOpen {
			continue
		}
		if !stringSliceContains(c.RelatedDecisionIDs, priorID) {
			continue
		}
		// Deep-copy slice fields so the queued snapshot stays
		// independent of the post-merge address mutation.
		copy := c
		if len(c.RelatedDecisionIDs) > 0 {
			copy.RelatedDecisionIDs = append([]string(nil), c.RelatedDecisionIDs...)
		}
		if len(c.RelatedAxisIDs) > 0 {
			copy.RelatedAxisIDs = append([]string(nil), c.RelatedAxisIDs...)
		}
		driving = append(driving, copy)
	}
	s.PendingDecisionRevisedEvents = append(s.PendingDecisionRevisedEvents, PendingDecisionRevisedEvent{
		Prior:           prior,
		Revised:         revised,
		DrivingConcerns: driving,
		Iter:            iter,
	})
}

// markConcernsAddressedByRevision walks state.Concerns and marks any
// open concern whose RelatedDecisionIDs contains the revised decision
// id as Status=addressed with a one-sentence justification naming the
// revision iteration. The signal is implicit: a successful axis-
// intersection replace addresses every concern that pointed at the
// replaced decision. The scout's next-iteration grading pass (DJ-125
// Phase 7) sees the disposition and treats the concerns as resolved
// for convergence purposes.
//
// When a concern names multiple decisions and only some are revised,
// it stays open — the concern is addressed only once every named
// decision has been revised in the iteration.
func markConcernsAddressedByRevision(s *PlanningState, revisedID string, d *RawDecisionProposal, iter int) {
	if s == nil || revisedID == "" {
		return
	}
	for i := range s.Concerns {
		c := &s.Concerns[i]
		if effectiveConcernStatus(c) != ConcernStatusOpen {
			continue
		}
		if !stringSliceContains(c.RelatedDecisionIDs, revisedID) {
			continue
		}
		c.Status = ConcernStatusAddressed
		title := strings.TrimSpace(d.Title)
		if title == "" {
			title = revisedID
		}
		c.Justification = fmt.Sprintf("Decision %s (%s) was revised at iter %d in response to this concern.", revisedID, title, iter+1)
	}
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

// projectScout builds the spec-scout's user message. The scout reads
// GOALS.md + imported content + the in-flight manifest (DJ-125
// Phase 4) + concerns + dangling references + prior brief (on iter > 0)
// and emits the new ScoutBrief.
//
// Pre-DJ-125 the projection dumped state.RawProposal as a JSON blob
// (~8K-30K chars on the second winplan re-run, blown past the 8K cap
// before the tactical 200K bump). Post-DJ-125 the scout sees the
// manifest's structural overview with state markers (axes
// settled/open, decisions settled_this_iter/flagged, features and
// strategies authored/pending). Full content for any specific node is
// available via the spec_get tool the scout already has registered.
//
// On iter 0 the message contains only GOALS.md, imported content, and
// the existing spec flag (greenfield runs omit the latter). On every
// subsequent iteration the message also contains the prior brief, the
// manifest, the iteration's concerns, and the dangling references —
// the inputs the scout needs to re-judge convergence.
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

	if rendered := renderManifestForProjection(&st); rendered != "" {
		b.WriteString("\n\n## In-flight spec manifest (use spec_get to fetch any node body)\n\n")
		b.WriteString(rendered)
		b.WriteString("\n")
	}

	if len(st.Concerns) > 0 {
		b.WriteString("\n## Outstanding critic findings\n")
		for i, c := range st.Concerns {
			status := c.Status
			if status == "" {
				status = ConcernStatusOpen
			}
			fmt.Fprintf(&b, "- [c-%d/%s/%s/%s] %s", i, status, c.AgentID, c.Severity, c.Text)
			if c.Justification != "" {
				fmt.Fprintf(&b, " (justification: %s)", c.Justification)
			}
			b.WriteString("\n")
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

// projectOpenAxis builds the spec-decision-elaborator's user message.
// Per-call inputs: GOALS.md + scout brief (for technology_options /
// watch_outs / implicit_assumptions context) + the in-flight manifest
// (DJ-125 Phase 4 — sibling settled decisions, axes in flight, flagged
// concerns) + the OpenAxis being decided in full + the Candidate List
// section from the DJ-132 pre-survey when present for this axis.
//
// DJ-132: when state.AxisSurveys carries an entry keyed by the axis's
// ID, render it as a Candidate List section between the manifest and
// the open-axis block. The elaborator's prompt assumes the section's
// presence on initial dispatch; absent entries (revise dispatches —
// the revise projection uses projectReviseDecision, not this one — or
// survey misfires) fall through to the elaborator's own enumeration,
// identical to the pre-DJ-132 behaviour.
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
	if rendered := renderManifestForProjection(&st); rendered != "" {
		prefix.WriteString("\n\n## In-flight spec manifest (use spec_get to fetch full content of any node)\n\n")
		prefix.WriteString(rendered)
	}

	var suffix strings.Builder
	suffix.WriteString("## Open axis to decide\n\n")
	var axisID string
	if snap.FanoutItem == "" {
		suffix.WriteString("(missing — fanout did not populate FanoutItem)\n")
	} else {
		var axis OpenAxis
		if err := json.Unmarshal([]byte(snap.FanoutItem), &axis); err != nil {
			suffix.WriteString(snap.FanoutItem)
		} else {
			axisID = strings.TrimSpace(axis.ID)
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

	if survey, ok := st.AxisSurveys[axisID]; ok && len(survey.Candidates) > 0 {
		suffix.WriteString("\n## Candidate list (surveyed for this axis)\n\n")
		suffix.WriteString("A pre-survey enumerated the candidate space for this axis (DJ-132). Pick from this list and author proper rationale; write `rejected_because` for each unpicked candidate; cite each. You may surface additional candidates beyond the survey when the axis warrants — the survey is a starting point, not an exhaustive set.\n\n")
		for _, c := range survey.Candidates {
			fmt.Fprintf(&suffix, "- **%s** — %s\n", c.Name, c.FirstGlanceFit)
		}
	}

	return []Message{
		{Role: "user", Content: prefix.String(), Cacheable: true},
		{Role: "user", Content: suffix.String()},
	}
}

// projectAffectedNode builds the narrative-elaborator's user message.
// Per-call inputs: GOALS.md + scout brief + the in-flight manifest
// (DJ-125 Phase 4 — replaces the pre-DJ-125 sibling-as-blob
// projection) + the affected node (new or existing) in full + the
// authoritative decisions-ID list. The decisions-ID list is the
// elaborator's sole source of truth for the output's Decisions[]
// field.
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
	if rendered := renderManifestForProjection(&st); rendered != "" {
		prefix.WriteString("\n\n## In-flight spec manifest (sibling features / strategies and the decisions they reference; use spec_get to fetch full bodies)\n\n")
		prefix.WriteString(rendered)
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

// reviseableConcernItem is the per-item shape the revise-decisions
// fanout dispatches against. Each item targets ONE prior decision and
// carries every open concern whose RelatedDecisionIDs name that
// decision. Multiple concerns about the same decision are aggregated
// into a single fanout item — one revision dispatch handles them
// jointly rather than spawning per-concern parallel dispatches that
// pile redundant replacements onto the same axis (and inflate the
// per-axis revision-count cap artificially).
//
// AgentID is always spec-decision-elaborator (the revise mode lives
// in that prompt). ID is "rev:<dec-id>" so fanoutItemID returns a
// unique label per dispatch slot and the per-call YAML traces are
// diagnosable.
type reviseableConcernItem struct {
	AgentID       string              `json:"agent_id"`
	ID            string              `json:"id"`
	PriorDecision RawDecisionProposal `json:"prior_decision"`
	Concerns      []Concern           `json:"concerns"`
}

// hasReviseableConcerns gates the revise-decisions step. Returns true
// when at least one concern has Status==open AND names at least one
// decision ID present in the in-flight proposal or the existing graph
// AND not yet locked by the cap-as-commit terminal.
//
// DJ-126: the revise dispatch only fires for concerns the workflow
// can act on — a concern whose RelatedDecisionIDs are empty or name
// no known decisions is left for the scout to grade in the next pass.
//
// DJ-128: Advisory concerns (counterproposal menu was sentinel-only)
// are skipped; locked decisions (set by cap-as-commit) are skipped;
// a concern naming a mix of locked + unlocked decisions stays
// reviseable on the unlocked entries.
func hasReviseableConcerns(s *PlanningState) bool {
	if s == nil || len(s.Concerns) == 0 {
		return false
	}
	known := collectKnownDecisionIDs(s)
	if len(known) == 0 {
		return false
	}
	for _, c := range s.Concerns {
		if effectiveConcernStatus(&c) != ConcernStatusOpen {
			continue
		}
		if c.Advisory {
			continue
		}
		if len(c.RelatedDecisionIDs) == 0 {
			continue
		}
		for _, did := range c.RelatedDecisionIDs {
			id := strings.TrimSpace(did)
			if _, ok := known[id]; !ok {
				continue
			}
			if isLockedDecision(s, id) {
				continue
			}
			return true
		}
	}
	return false
}

// isLockedDecision reports whether the decision id has been locked by
// the cap-as-commit terminal (DJ-128).
func isLockedDecision(s *PlanningState, id string) bool {
	if s == nil || len(s.LockedDecisionIDs) == 0 || id == "" {
		return false
	}
	_, locked := s.LockedDecisionIDs[id]
	return locked
}

// effectiveConcernStatus returns the Status field of c, defaulting to
// ConcernStatusOpen for legacy pre-DJ-125 entries that omitted Status.
// Mirrors the same defaulting used by countOpenConcerns.
func effectiveConcernStatus(c *Concern) ConcernStatus {
	if c == nil || c.Status == "" {
		return ConcernStatusOpen
	}
	return c.Status
}

// fanoutReviseableConcerns walks state.Concerns and emits one fanout
// item per unique reviseable decision (dedup-by-decision-ID), with
// every open concern naming that decision aggregated into the item's
// Concerns slice. A single concern naming N related decisions still
// produces N items (one per decision); N concerns naming the same
// decision produce ONE item (with all N concerns rendered jointly).
//
// The dedup-by-decision shape avoids per-call inflation of the
// per-axis revision-count cap (DJ-126 Phase 4). With per-(concern,
// decision)-pair dispatch, 5 concerns flagging the same decision in
// one iteration would produce 5 parallel revisions piling on top of
// each other, bumping AxisRevisionCount by 5 in a single phase —
// guaranteed to trip the cap=3 even on the very first iteration of
// revisions. Joint dispatch increments by 1 per decision per
// iteration, restoring the cap's intended semantics ("cross-iteration
// oscillation" rather than "per-concern fanout").
//
// Each item carries the full prior decision body (rendered as a
// RawDecisionProposal) so the projection has the inputs the revise
// prompt expects (the "Prior decision" block). Decisions in the
// in-flight proposal take precedence over the existing-graph snapshot
// when the same id appears in both — the in-flight version reflects
// any same-iteration first-author or revision updates.
//
// Iteration order is preserved: decisions appear in the order their
// first naming concern appears in state.Concerns, and concerns within
// each item appear in state.Concerns order. Deterministic dispatch
// makes per-call traces reproducible.
func fanoutReviseableConcerns(s *PlanningState) ([]string, error) {
	if s == nil || len(s.Concerns) == 0 {
		return nil, nil
	}

	priorByID := make(map[string]RawDecisionProposal)
	if s.Existing != nil {
		for _, d := range s.Existing.Decisions {
			id := strings.TrimSpace(d.ID)
			if id == "" {
				continue
			}
			// DJ-128: skip locked decisions — cap-as-commit committed
			// them and they should not be revised again.
			if isLockedDecision(s, id) {
				continue
			}
			priorByID[id] = decisionToRawProposal(d)
		}
	}
	if strings.TrimSpace(s.RawProposal) != "" {
		var raw RawSpecProposal
		if err := json.Unmarshal([]byte(s.RawProposal), &raw); err == nil {
			for _, d := range raw.Decisions {
				id := strings.TrimSpace(d.ID)
				if id == "" {
					continue
				}
				if isLockedDecision(s, id) {
					continue
				}
				priorByID[id] = d
			}
		}
	}
	if len(priorByID) == 0 {
		return nil, nil
	}

	// Group open concerns by decision ID, preserving first-occurrence
	// order for deterministic dispatch.
	type concernGroup struct {
		decisionID string
		concerns   []Concern
	}
	groups := make(map[string]*concernGroup)
	var order []string
	for _, c := range s.Concerns {
		if effectiveConcernStatus(&c) != ConcernStatusOpen {
			continue
		}
		// DJ-128: Advisory concerns are sentinel-only counterproposal
		// menus surfaced for human review; they do not drive revise
		// dispatch.
		if c.Advisory {
			continue
		}
		for _, didRaw := range c.RelatedDecisionIDs {
			did := strings.TrimSpace(didRaw)
			if _, ok := priorByID[did]; !ok {
				// Either unknown or locked — skip.
				continue
			}
			g, ok := groups[did]
			if !ok {
				g = &concernGroup{decisionID: did}
				groups[did] = g
				order = append(order, did)
			}
			g.concerns = append(g.concerns, c)
		}
	}

	if len(order) == 0 {
		return nil, nil
	}
	items := make([]any, 0, len(order))
	for _, did := range order {
		g := groups[did]
		items = append(items, reviseableConcernItem{
			AgentID:       "spec-decision-elaborator",
			ID:            fmt.Sprintf("rev:%s", did),
			PriorDecision: priorByID[did],
			Concerns:      g.concerns,
		})
	}
	return marshalFanoutItems(items)
}

// decisionToRawProposal converts a persisted spec.Decision into a
// RawDecisionProposal for the revise projection's "Prior decision"
// block. Provenance (citations + architect_rationale) is denormalized
// onto the raw shape so the prompt sees the same fields the revise
// output will carry. SourceSession + GeneratedAt are dropped — they
// don't travel through the raw proposal shape.
func decisionToRawProposal(d spec.Decision) RawDecisionProposal {
	var citations []spec.Citation
	var arch string
	if d.Provenance != nil {
		citations = append([]spec.Citation(nil), d.Provenance.Citations...)
		arch = d.Provenance.ArchitectRationale
	}
	return RawDecisionProposal{
		ID:                 d.ID,
		Summary:            d.Summary,
		Title:              d.Title,
		Rationale:          d.Rationale,
		ArchitectRationale: arch,
		Confidence:         d.Confidence,
		Alternatives:       append([]spec.Alternative(nil), d.Alternatives...),
		Citations:          citations,
		Axes:               append([]string(nil), d.Axes...),
		SurfacedBy:         append([]string(nil), d.SurfacedBy...),
	}
}

// projectReviseDecision builds the spec-decision-elaborator's user
// message for a revise-decisions fanout call. Per-call inputs:
// GOALS.md + scout brief + the in-flight manifest + the "Prior
// decision" block (the full body of the decision being revised) +
// the "Critic finding to address" block (the concern text, severity,
// and related decision IDs). The prompt's Revise mode section keys
// on the two block headings.
func projectReviseDecision(snap StateSnapshot[PlanningState]) []Message {
	st := snap.State
	var prefix strings.Builder
	prefix.WriteString(st.Prompt)
	if st.ScoutBrief != "" {
		if formatted := formatScoutBrief(st.ScoutBrief); formatted != "" {
			prefix.WriteString("\n\n## Scout brief\n\n")
			prefix.WriteString(formatted)
		}
	}
	if rendered := renderManifestForProjection(&st); rendered != "" {
		prefix.WriteString("\n\n## In-flight spec manifest (use spec_get to fetch full content of any node)\n\n")
		prefix.WriteString(rendered)
	}

	var suffix strings.Builder
	var item reviseableConcernItem
	if snap.FanoutItem != "" {
		_ = json.Unmarshal([]byte(snap.FanoutItem), &item)
	}

	suffix.WriteString("## Revise mode\n\n")
	suffix.WriteString("You are revising an existing decision in response to one or more critic findings. Preserve the prior decision's `id`, `axes`, and `surfaced_by` verbatim; the revised body replaces the prior decision in the graph at the same id. A single revision addresses every finding listed below — your new rationale and alternatives reflect the union of the corrections those findings ask for.\n\n")

	suffix.WriteString("### Prior decision (the version being revised)\n\n")
	suffix.WriteString("```json\n")
	if data, err := json.MarshalIndent(item.PriorDecision, "", "  "); err == nil {
		suffix.Write(data)
	} else {
		suffix.WriteString("(unable to render — fanout item payload was malformed)")
	}
	suffix.WriteString("\n```\n\n")

	if n := len(item.Concerns); n > 0 {
		if n == 1 {
			suffix.WriteString("### Critic finding to address\n\n")
		} else {
			fmt.Fprintf(&suffix, "### Critic findings to address (%d concerns; address every one in this single revision)\n\n", n)
		}
		// Collect related axis IDs across all concerns for the trailing
		// summary line; dedup by appearance.
		axisSet := make(map[string]struct{})
		var axisOrder []string
		for i, c := range item.Concerns {
			fmt.Fprintf(&suffix, "**Finding %d.** %s\n", i+1, c.Text)
			var meta []string
			if c.Severity != "" {
				meta = append(meta, fmt.Sprintf("severity %s", c.Severity))
			}
			if c.AgentID != "" {
				meta = append(meta, fmt.Sprintf("raised by %s", c.AgentID))
			}
			if c.Kind != "" {
				meta = append(meta, fmt.Sprintf("kind %s", c.Kind))
			}
			if len(meta) > 0 {
				fmt.Fprintf(&suffix, "  - (%s)\n", strings.Join(meta, "; "))
			}
			if len(c.RelatedDecisionIDs) > 0 {
				suffix.WriteString("  - Related decision IDs:")
				for _, did := range c.RelatedDecisionIDs {
					fmt.Fprintf(&suffix, " `%s`", did)
				}
				suffix.WriteString("\n")
			}
			for _, a := range c.RelatedAxisIDs {
				a = strings.TrimSpace(a)
				if a == "" {
					continue
				}
				if _, ok := axisSet[a]; !ok {
					axisSet[a] = struct{}{}
					axisOrder = append(axisOrder, a)
				}
			}
			suffix.WriteString("\n")
		}
		if len(axisOrder) > 0 {
			suffix.WriteString("Related axis IDs across findings:")
			for _, a := range axisOrder {
				fmt.Fprintf(&suffix, " `%s`", a)
			}
			suffix.WriteString("\n")
		}
		suffix.WriteString("\nRead full bodies of any sibling decisions named above with `spec_get` before authoring the revision; the revision must be coherent with whichever direction those siblings are committing.\n")
	}

	return []Message{
		{Role: "user", Content: prefix.String(), Cacheable: true},
		{Role: "user", Content: suffix.String()},
	}
}

