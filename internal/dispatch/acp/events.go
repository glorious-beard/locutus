package acp

import (
	"encoding/json"
	"time"

	"github.com/glorious-beard/locutus/internal/dispatch"
	acpsdk "github.com/coder/acp-go-sdk"
)

// translateUpdate converts an ACP session/update notification into the
// dispatch.AgentEvent shape the supervisor's event loop already consumes.
// Returns skip=true for update kinds we deliberately drop
// (availableCommandsUpdate, modeUpdate, etc. — none load-bearing for
// the initial migration; folded in later if a phase needs them). Plan
// notifications were dropped in DJ-135 phase 1 and re-surfaced as
// EventPlan under DJ-136 phase 2.
//
// The translation is intentionally lossy: rawInput, locations, and content
// types like diff are preserved on the AgentEvent's Raw field as the
// JSON-encoded notification, so downstream archivers can recover full
// fidelity without the supervisor needing to know about every variant.
func translateUpdate(n acpsdk.SessionNotification) (dispatch.AgentEvent, bool) {
	u := n.Update
	ev := dispatch.AgentEvent{
		Timestamp: time.Now().UTC(),
		SessionID: string(n.SessionId),
	}
	if raw, err := json.Marshal(n); err == nil {
		ev.Raw = raw
	}

	switch {
	case u.AgentMessageChunk != nil:
		if t := u.AgentMessageChunk.Content.Text; t != nil {
			ev.Kind = dispatch.EventText
			ev.Text = t.Text
			return ev, false
		}
		// Non-text content (image/audio/resource) — not surfaced as EventText
		// today. Drop with the Raw payload retained for the archive.
		return dispatch.AgentEvent{}, true

	case u.AgentThoughtChunk != nil:
		// Internal reasoning — also surface as EventText for now so the
		// supervisor's monitor can see the agent's narration. A dedicated
		// EventThought kind is a candidate for a later phase if we need to
		// distinguish chain-of-thought from user-facing text.
		if t := u.AgentThoughtChunk.Content.Text; t != nil {
			ev.Kind = dispatch.EventText
			ev.Text = t.Text
			return ev, false
		}
		return dispatch.AgentEvent{}, true

	case u.ToolCall != nil:
		tc := u.ToolCall
		ev.Kind = dispatch.EventToolCall
		ev.ToolName = tc.Title
		if input, ok := tc.RawInput.(map[string]any); ok {
			ev.ToolInput = input
		}
		for _, loc := range tc.Locations {
			ev.FilePaths = append(ev.FilePaths, loc.Path)
		}
		return ev, false

	case u.ToolCallUpdate != nil:
		// We only surface terminal updates (completed / failed) as
		// EventToolResult — in-progress updates are status-only and the
		// monitor cares about completion, not progress. Status is *pointer*
		// in the SDK (omitted-on-update is meaningful); nil means "no
		// status change in this update," not a terminal state.
		if u.ToolCallUpdate.Status == nil {
			return dispatch.AgentEvent{}, true
		}
		switch *u.ToolCallUpdate.Status {
		case acpsdk.ToolCallStatusCompleted, acpsdk.ToolCallStatusFailed:
			ev.Kind = dispatch.EventToolResult
			ev.ToolName = string(u.ToolCallUpdate.ToolCallId)
			return ev, false
		}
		return dispatch.AgentEvent{}, true

	case u.Plan != nil:
		// Full replacement, per the ACP Agent Plan spec — each
		// notification carries the complete list, not a delta. The
		// dispatch layer surfaces it as one EventPlan; the renderer
		// in internal/runner produces the multi-line operator view.
		ev.Kind = dispatch.EventPlan
		ev.PlanEntries = make([]dispatch.PlanEntry, 0, len(u.Plan.Entries))
		for _, e := range u.Plan.Entries {
			ev.PlanEntries = append(ev.PlanEntries, dispatch.PlanEntry{
				Content:  e.Content,
				Status:   string(e.Status),
				Priority: string(e.Priority),
			})
		}
		return ev, false

	default:
		// availableCommandsUpdate, currentModeUpdate, configOptionUpdate,
		// userMessageChunk, etc. — preserved in the archive via Raw but not
		// surfaced as supervisor events. Plan notifications were lifted
		// out of this default branch in DJ-136 phase 2.
		return dispatch.AgentEvent{}, true
	}
}
