package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// StreamParser reads provider-specific NDJSON and yields normalized
// AgentEvents one at a time. Implementations are pull-based so the
// supervisor controls lifecycle — no goroutine leak on cancellation.
//
// Next returns io.EOF when the stream is exhausted, or ctx.Err() if the
// context is canceled between reads. Callers must Close the parser when
// done to release the underlying reader.
type StreamParser interface {
	Next(ctx context.Context) (AgentEvent, error)
	Close() error
}

// EventKind is a provider-agnostic classification of a single event emitted
// by a coding-agent stream.
type EventKind string

const (
	EventInit              EventKind = "init"
	EventText              EventKind = "text"
	EventToolCall          EventKind = "tool_call"
	EventToolResult        EventKind = "tool_result"
	EventRetry             EventKind = "api_retry"
	EventResult            EventKind = "result"
	EventError             EventKind = "error"
	EventPermissionRequest EventKind = "permission_request"
	EventClarifyQuestion   EventKind = "clarify_question"
)

// AgentEvent is a normalized event from a coding-agent stream. Driver
// parsers translate provider-specific NDJSON into this shape; the
// permission bridge (Part 7) produces EventPermissionRequest events
// outside the parser path.
type AgentEvent struct {
	Kind      EventKind
	Timestamp time.Time
	SessionID string
	ToolName  string
	ToolInput map[string]any
	Text      string
	FilePaths []string
	Raw       json.RawMessage
	// InteractionID is set only for events emitted by the permission
	// bridge (EventPermissionRequest, EventClarifyQuestion). It matches
	// Claude's tool_use_id so the supervisor's handleInteraction can
	// route the decision back to the originating bridge request.
	InteractionID string
}

// DriverConfig declares provider-level tool-name conventions used for
// structural event classification. Empty fields disable the corresponding
// routing (e.g., a driver without a permission-prompt tool simply never
// emits EventPermissionRequest).
type DriverConfig struct {
	PermissionToolName string
	QuestionToolName   string
}

// ClassifyToolName maps a tool-call event's tool name to an EventKind using
// the driver's tool-name registry. An unregistered tool name yields
// EventToolCall — judgment about what the tool is "doing" is the LLM
// monitor's job, not ours.
func ClassifyToolName(toolName string, cfg DriverConfig) EventKind {
	if cfg.PermissionToolName != "" && toolName == cfg.PermissionToolName {
		return EventPermissionRequest
	}
	if cfg.QuestionToolName != "" && toolName == cfg.QuestionToolName {
		return EventClarifyQuestion
	}
	return EventToolCall
}

// SummarizeEvents produces a compact, deterministic text representation of a
// recent-events slice suitable for inclusion in the monitor LLM's prompt.
// It preserves tool names, file paths, and short text snippets but omits
// raw JSON bodies and truncates long content.
func SummarizeEvents(events []AgentEvent) string {
	var b strings.Builder
	for i, e := range events {
		if i > 0 {
			b.WriteByte('\n')
		}
		writeEventLine(&b, e)
	}
	return b.String()
}

func writeEventLine(b *strings.Builder, e AgentEvent) {
	fmt.Fprintf(b, "[%s] %s", e.Timestamp.UTC().Format("15:04:05"), e.Kind)
	switch e.Kind {
	case EventInit:
		if e.SessionID != "" {
			fmt.Fprintf(b, " session=%s", e.SessionID)
		}
	case EventText, EventResult:
		if e.Text != "" {
			fmt.Fprintf(b, ": %s", truncate(singleLine(e.Text), 120))
		}
	case EventToolCall, EventPermissionRequest, EventClarifyQuestion:
		if e.ToolName != "" {
			fmt.Fprintf(b, " %s", e.ToolName)
		}
		if len(e.FilePaths) > 0 {
			fmt.Fprintf(b, " files=[%s]", strings.Join(e.FilePaths, ","))
		}
		if inputs := compactInput(e.ToolInput, e.FilePaths); inputs != "" {
			fmt.Fprintf(b, " %s", inputs)
		}
	case EventToolResult:
		if e.ToolName != "" {
			fmt.Fprintf(b, " %s", e.ToolName)
		}
	case EventError:
		if e.Text != "" {
			fmt.Fprintf(b, ": %s", truncate(singleLine(e.Text), 120))
		}
	}
}

func compactInput(input map[string]any, skipFilePaths []string) string {
	if len(input) == 0 {
		return ""
	}
	skip := make(map[string]bool, len(skipFilePaths))
	for _, p := range skipFilePaths {
		skip[p] = true
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := fmt.Sprintf("%v", input[k])
		if skip[v] {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, truncate(singleLine(v), 40)))
	}
	return strings.Join(parts, " ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}
