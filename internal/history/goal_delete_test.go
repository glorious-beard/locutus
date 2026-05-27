// DJ-139 phase 3 — history events for goal and antigoal deletion.
//
// Goals and AntiGoals are the persisted LLM interpretation of GOALS.md
// (DJ-139); when GOALS.md drops a claim the corresponding goal-* or
// agoal-* node disappears from the graph. The deletion is real (no
// soft-delete; the node is removed from .borg/spec/), so the
// historian's record is the audit trail. These tests cover the
// dedicated RecordGoalDeleted and RecordAntiGoalDeleted helpers.

package history_test

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/history"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordGoalDeletedRoundTrip — RecordGoalDeleted writes an event
// of kind EventKindGoalDeleted with TargetID set to the goal id and
// Rationale set to the supplied reason; EventsForTarget reads it back
// verbatim.
func TestRecordGoalDeletedRoundTrip(t *testing.T) {
	_, h := newHistFS(t)

	require.NoError(t, history.RecordGoalDeleted(h, "goal-strategic-planning-tool", "dropped from GOALS.md in 2026-05-27 edit"))

	events, err := h.EventsForTarget("goal-strategic-planning-tool")
	require.NoError(t, err)
	require.Len(t, events, 1, "exactly one event recorded")

	evt := events[0]
	assert.Equal(t, history.EventKindGoalDeleted, evt.Kind)
	assert.Equal(t, "goal-strategic-planning-tool", evt.TargetID)
	assert.Equal(t, "dropped from GOALS.md in 2026-05-27 edit", evt.Rationale)
	assert.False(t, evt.Timestamp.IsZero(), "timestamp populated")
}

// TestRecordAntiGoalDeletedRoundTrip — same as the goal test for the
// agoal- prefix.
func TestRecordAntiGoalDeletedRoundTrip(t *testing.T) {
	_, h := newHistFS(t)

	require.NoError(t, history.RecordAntiGoalDeleted(h, "agoal-fundraising", "the fundraising carve-out was lifted; no longer out-of-scope"))

	events, err := h.EventsForTarget("agoal-fundraising")
	require.NoError(t, err)
	require.Len(t, events, 1)

	evt := events[0]
	assert.Equal(t, history.EventKindAntiGoalDeleted, evt.Kind)
	assert.Equal(t, "agoal-fundraising", evt.TargetID)
	assert.Equal(t, "the fundraising carve-out was lifted; no longer out-of-scope", evt.Rationale)
}

// TestEventKindConstants — the snake_case event-kind strings match
// the existing naming convention (`spec_refined`, `spec_rolled_back`,
// `spec_biased`, `approach_drifted`). DJ-139 adds `goal_deleted` and
// `antigoal_deleted`.
func TestEventKindConstants(t *testing.T) {
	assert.Equal(t, "goal_deleted", history.EventKindGoalDeleted)
	assert.Equal(t, "antigoal_deleted", history.EventKindAntiGoalDeleted)
}
