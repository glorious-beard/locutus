package cmd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpSink translates council events into MCP server-to-client
// notifications on the originating session. Two channels fire in
// parallel:
//
//   - notifications/progress (`Session.NotifyProgress`) — drives the
//     client's progress-bar UI. Requires a progressToken on the
//     originating tool call; skipped when absent because the MCP spec
//     forbids unsolicited progress.
//   - notifications/message (`Session.Log`) — the structured log
//     stream. Fires unconditionally so any MCP client connected to
//     the server sees per-call agent activity even when it didn't ask
//     for progress.
//
// Progress fields use a monotonically-increasing counter rather than
// a percentage because the council's total step count varies (revise
// is conditional on critic findings) and overpromising a total then
// missing it is worse than reporting open-ended progress.
type mcpSink struct {
	ctx     context.Context
	session *mcp.ServerSession
	token   any // nil when the client didn't request progress; Log still fires.

	mu       sync.Mutex
	progress float64
}

// newMCPSink returns a sink bound to the request's session. Returns
// SilentSink only when there is no session at all (synthetic requests
// from CLI; tests). When a session is present the sink fires log
// notifications on every event regardless of progressToken presence,
// and additionally fires progress notifications when the client opted
// into them.
func newMCPSink(ctx context.Context, req *mcp.CallToolRequest) agent.EventSink {
	if req == nil || req.Session == nil {
		return agent.SilentSink{}
	}
	var token any
	if req.Params != nil {
		token = req.Params.GetProgressToken()
	}
	return &mcpSink{ctx: ctx, session: req.Session, token: token}
}

func (s *mcpSink) OnEvent(e agent.WorkflowEvent) {
	msg := formatEventMessage(e)

	// Progress: fire only when the client requested it. Counter advances
	// monotonically across all events on this sink (workflow + direct
	// LLM calls share the counter).
	if s.token != nil {
		s.mu.Lock()
		s.progress++
		current := s.progress
		s.mu.Unlock()

		_ = s.session.NotifyProgress(s.ctx, &mcp.ProgressNotificationParams{
			ProgressToken: s.token,
			Progress:      current,
			Message:       msg,
		})
	}

	// Log stream: fire on every event so MCP clients tail-following
	// notifications/message see direct LLM-call activity (justify, the
	// rewriter/synthesizer subcalls, etc.) even without a progress token.
	// Best-effort — a broken session means the client has gone away;
	// the call continues regardless.
	_ = s.session.Log(s.ctx, &mcp.LoggingMessageParams{
		Level:  levelFor(e.Status),
		Logger: "locutus",
		Data:   logPayload(e),
	})
}

func (s *mcpSink) Close() {}

// formatEventMessage renders one workflow event into a human-readable
// log line. Handles direct LLM calls (empty StepID) by collapsing the
// label so it doesn't render with a leading separator.
func formatEventMessage(e agent.WorkflowEvent) string {
	var label string
	switch {
	case e.StepID != "" && e.AgentID != "":
		label = fmt.Sprintf("%s · %s", e.StepID, e.AgentID)
	case e.AgentID != "":
		label = e.AgentID
	default:
		label = e.StepID
	}
	out := fmt.Sprintf("%s · %s", label, e.Status)
	if e.Message != "" {
		out = fmt.Sprintf("%s — %s", out, e.Message)
	}
	return out
}

// logPayload builds the structured payload sent on the log stream so
// MCP clients consuming notifications/message can filter or render
// events without re-parsing the human-readable message.
func logPayload(e agent.WorkflowEvent) map[string]any {
	payload := map[string]any{
		"status": e.Status,
	}
	if e.StepID != "" {
		payload["step_id"] = e.StepID
	}
	if e.AgentID != "" {
		payload["agent_id"] = e.AgentID
	}
	if e.Message != "" {
		payload["message"] = e.Message
	}
	if !e.Timestamp.IsZero() {
		payload["timestamp"] = e.Timestamp.Format(time.RFC3339Nano)
	}
	return payload
}

// levelFor maps an event status to an MCP logging level (RFC 5424
// names per the MCP spec). Keep it conservative: errors are errors,
// retries surface as warnings, and every other lifecycle marker is
// informational.
func levelFor(status string) mcp.LoggingLevel {
	switch status {
	case "error":
		return mcp.LoggingLevel("error")
	case "retrying":
		return mcp.LoggingLevel("warning")
	default:
		return mcp.LoggingLevel("info")
	}
}
