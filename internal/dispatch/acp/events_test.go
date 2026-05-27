// DJ-136 phase 2 — assertions on the plan-notification translation.
// The Phase-1 cut of DJ-135 dropped plan notifications on the floor;
// DJ-136 reverses that so the operator sees the orchestrator's
// scheduled work inline.

package acp

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/dispatch"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventPlan_TranslateFullReplacement — given a SessionNotification
// whose Update.Plan carries 3 entries with mixed statuses, translateUpdate
// emits one EventPlan with the entries in order, mapped statuses, and
// a non-empty Raw payload.
func TestEventPlan_TranslateFullReplacement(t *testing.T) {
	pending := acpsdk.PlanEntryStatusPending
	inProg := acpsdk.PlanEntryStatusInProgress
	done := acpsdk.PlanEntryStatusCompleted
	high := acpsdk.PlanEntryPriorityHigh
	med := acpsdk.PlanEntryPriorityMedium

	n := acpsdk.SessionNotification{
		SessionId: "sess-test",
		Update: acpsdk.SessionUpdate{
			Plan: &acpsdk.SessionUpdatePlan{
				SessionUpdate: "plan",
				Entries: []acpsdk.PlanEntry{
					{Content: "Read GOALS.md", Status: done, Priority: high},
					{Content: "Dispatch spec-scout", Status: inProg, Priority: high},
					{Content: "Commit decisions", Status: pending, Priority: med},
				},
			},
		},
	}

	ev, skip := translateUpdate(n)
	require.False(t, skip, "plan notifications must not be skipped")
	require.Equal(t, dispatch.EventPlan, ev.Kind)
	require.Len(t, ev.PlanEntries, 3)

	assert.Equal(t, "Read GOALS.md", ev.PlanEntries[0].Content)
	assert.Equal(t, "completed", ev.PlanEntries[0].Status)
	assert.Equal(t, "high", ev.PlanEntries[0].Priority)

	assert.Equal(t, "Dispatch spec-scout", ev.PlanEntries[1].Content)
	assert.Equal(t, "in_progress", ev.PlanEntries[1].Status)

	assert.Equal(t, "Commit decisions", ev.PlanEntries[2].Content)
	assert.Equal(t, "pending", ev.PlanEntries[2].Status)
	assert.Equal(t, "medium", ev.PlanEntries[2].Priority)

	assert.NotEmpty(t, ev.Raw, "Raw must carry the full notification for archive consumers")
	assert.Equal(t, "sess-test", ev.SessionID)
}

// TestEventPlan_TranslateEmptyEntries — an agent retracting its plan
// sends a notification with an empty entries list. The dispatch layer
// surfaces this as an EventPlan with PlanEntries == nil-or-empty;
// the renderer is responsible for the "cleared" line.
func TestEventPlan_TranslateEmptyEntries(t *testing.T) {
	n := acpsdk.SessionNotification{
		Update: acpsdk.SessionUpdate{
			Plan: &acpsdk.SessionUpdatePlan{
				SessionUpdate: "plan",
				Entries:       nil,
			},
		},
	}
	ev, skip := translateUpdate(n)
	require.False(t, skip)
	require.Equal(t, dispatch.EventPlan, ev.Kind)
	assert.Empty(t, ev.PlanEntries)
}
