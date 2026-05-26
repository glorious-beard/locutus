package acp

import (
	"encoding/json"
	"time"

	"github.com/glorious-beard/locutus/internal/dispatch"
	acpsdk "github.com/coder/acp-go-sdk"
)

// translateUpdate converts an ACP session/update notification into the
// dispatch.AgentEvent shape the supervisor's event loop already consumes.
// Returns skip=true for update kinds we deliberately drop (plan,
// availableCommandsUpdate, modeUpdate, etc. — none load-bearing for the
// initial migration; folded in later if a phase needs them).
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

	default:
		// plan, availableCommandsUpdate, currentModeUpdate, configOptionUpdate,
		// userMessageChunk, etc. — preserved in the archive via Raw but not
		// surfaced as supervisor events in Phase 1.
		return dispatch.AgentEvent{}, true
	}
}
