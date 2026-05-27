package history

import "time"

// EventKindGoalDeleted records that a goal-* node was removed from
// the spec graph (DJ-139). Goals and AntiGoals are the persisted
// LLM interpretation of GOALS.md; when a scope claim disappears
// from GOALS.md the corresponding node is deleted from .borg/spec/,
// and this event preserves the audit trail. TargetID is the goal id;
// Rationale carries the operator- or agent-supplied reason for the
// deletion (e.g. "claim dropped from GOALS.md in 2026-05-27 edit").
const EventKindGoalDeleted = "goal_deleted"

// EventKindAntiGoalDeleted records that an agoal-* node was removed
// from the spec graph (DJ-139). Same semantics as
// EventKindGoalDeleted; the kind constant exists separately so the
// history log can be queried per polarity.
const EventKindAntiGoalDeleted = "antigoal_deleted"

// RecordGoalDeleted writes a goal_deleted event for the given goal
// id. reason is the human- or agent-supplied reason for the
// deletion — recorded verbatim as the event's Rationale field so a
// subsequent walk of the history log can surface why the goal
// disappeared. The event's TargetID is the goal id; OldValue and
// NewValue are left empty because the deleted body is captured
// elsewhere (the on-disk JSON before deletion) and a refine-style
// before/after pair would mislead readers expecting a survivable
// node.
func RecordGoalDeleted(h *Historian, goalID, reason string) error {
	now := time.Now()
	return h.Record(Event{
		ID:        EventID("goal-deleted", goalID, now),
		Timestamp: now,
		Kind:      EventKindGoalDeleted,
		TargetID:  goalID,
		Rationale: reason,
	})
}

// RecordAntiGoalDeleted writes an antigoal_deleted event for the
// given agoal- id. Same semantics as RecordGoalDeleted; the kind
// discriminator lets history queries filter by polarity.
func RecordAntiGoalDeleted(h *Historian, antiGoalID, reason string) error {
	now := time.Now()
	return h.Record(Event{
		ID:        EventID("antigoal-deleted", antiGoalID, now),
		Timestamp: now,
		Kind:      EventKindAntiGoalDeleted,
		TargetID:  antiGoalID,
		Rationale: reason,
	})
}
