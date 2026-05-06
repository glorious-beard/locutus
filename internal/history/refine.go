package history

import (
	"fmt"
	"sort"
	"time"
)

// EventKindRefined is the canonical kind for spec-refine events.
// Every refine path (decision/feature/strategy/bug/approach) writes
// one of these as a side-effect of the rewrite (DJ-102). Distinct
// from the legacy per-kind labels (`feature_refined`, etc.) which
// the existing code emits without OldValue/NewValue payloads;
// callers should prefer this constant for new writes so the rollback
// + diff paths can find them via LatestRefinedEvent.
const EventKindRefined = "spec_refined"

// EventKindRolledBack records that a refine was undone. Recorded by
// rollback so subsequent rollbacks see "the most recent change" as
// the rollback itself rather than walking past it.
const EventKindRolledBack = "spec_rolled_back"

// RecordRefined writes a structured refine event to history. priorJSON
// and newJSON are the verbatim file contents before and after the
// rewrite — used by rollback to restore prior state and by --diff to
// render the change. brief is the user-supplied refinement intent
// (empty when refine ran without --brief).
//
// Refine-event ids embed the timestamp so multiple refines on the
// same node sort correctly when listed.
func RecordRefined(h *Historian, nodeID, priorJSON, newJSON, brief string) error {
	now := time.Now()
	rationale := brief
	if rationale == "" {
		rationale = "refine without focused brief"
	}
	return h.Record(Event{
		ID:        fmt.Sprintf("evt-refined-%s-%d", nodeID, now.UnixNano()),
		Timestamp: now,
		Kind:      EventKindRefined,
		TargetID:  nodeID,
		OldValue:  priorJSON,
		NewValue:  newJSON,
		Rationale: rationale,
	})
}

// RecordRolledBack writes a rollback event. The semantics match
// RecordRefined but with old/new values flipped — the file content
// went from `currentJSON` (the post-refine state we're discarding)
// back to `restoredJSON` (the pre-refine state we're restoring).
func RecordRolledBack(h *Historian, nodeID, currentJSON, restoredJSON, sourceEventID string) error {
	now := time.Now()
	return h.Record(Event{
		ID:        fmt.Sprintf("evt-rollback-%s-%d", nodeID, now.UnixNano()),
		Timestamp: now,
		Kind:      EventKindRolledBack,
		TargetID:  nodeID,
		OldValue:  currentJSON,
		NewValue:  restoredJSON,
		Rationale: "rolled back to " + sourceEventID,
	})
}

// LatestRefinedEvent returns the most recent EventKindRefined event
// for nodeID, or nil if none. Skips rolled-back events so the
// "latest refine to undo" is the most recent refine that hasn't
// already been rolled back. Callers handling the no-event case
// should treat nil-without-error as "nothing to rollback."
func LatestRefinedEvent(h *Historian, nodeID string) (*Event, error) {
	all, err := h.EventsForTarget(nodeID)
	if err != nil {
		return nil, err
	}
	// Walk newest-first looking for a refine that hasn't been
	// rolled back. A rollback event for the same target invalidates
	// the immediately-preceding refine.
	sort.Slice(all, func(i, j int) bool { return all[i].Timestamp.After(all[j].Timestamp) })
	rolledBackCount := 0
	for i := range all {
		e := &all[i]
		if e.Kind == EventKindRolledBack {
			rolledBackCount++
			continue
		}
		if e.Kind != EventKindRefined {
			continue
		}
		if rolledBackCount > 0 {
			rolledBackCount--
			continue
		}
		return e, nil
	}
	return nil, nil
}
