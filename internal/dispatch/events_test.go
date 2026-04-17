package dispatch

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEventKind_String(t *testing.T) {
	cases := []struct {
		kind EventKind
		want string
	}{
		{EventInit, "init"},
		{EventText, "text"},
		{EventToolCall, "tool_call"},
		{EventToolResult, "tool_result"},
		{EventRetry, "api_retry"},
		{EventResult, "result"},
		{EventError, "error"},
		{EventPermissionRequest, "permission_request"},
		{EventClarifyQuestion, "clarify_question"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, string(c.kind), "EventKind constant value mismatch")
	}
}

var claudeTestCfg = DriverConfig{
	PermissionToolName: "locutus_permission",
	QuestionToolName:   "AskUserQuestion",
}

func TestClassifyToolName_Permission(t *testing.T) {
	got := ClassifyToolName("locutus_permission", claudeTestCfg)
	assert.Equal(t, EventPermissionRequest, got)
}

func TestClassifyToolName_Question(t *testing.T) {
	got := ClassifyToolName("AskUserQuestion", claudeTestCfg)
	assert.Equal(t, EventClarifyQuestion, got)
}

func TestClassifyToolName_Unregistered(t *testing.T) {
	got := ClassifyToolName("Edit", claudeTestCfg)
	assert.Equal(t, EventToolCall, got)
}

func TestClassifyToolName_EmptyConfigFallsBackToToolCall(t *testing.T) {
	got := ClassifyToolName("locutus_permission", DriverConfig{})
	assert.Equal(t, EventToolCall, got)
}

func TestSummarizeEvents_Compact(t *testing.T) {
	ts := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	events := []AgentEvent{
		{Kind: EventInit, Timestamp: ts, SessionID: "sess-1"},
		{Kind: EventText, Timestamp: ts.Add(1 * time.Second), Text: "Analyzing the request."},
		{Kind: EventToolCall, Timestamp: ts.Add(2 * time.Second), ToolName: "Read", ToolInput: map[string]any{"path": "/foo.go"}, FilePaths: []string{"/foo.go"}},
		{Kind: EventToolResult, Timestamp: ts.Add(3 * time.Second), ToolName: "Read"},
		{Kind: EventToolCall, Timestamp: ts.Add(4 * time.Second), ToolName: "Edit", ToolInput: map[string]any{"path": "/foo.go", "old": "a", "new": "b"}, FilePaths: []string{"/foo.go"}},
		{Kind: EventToolResult, Timestamp: ts.Add(5 * time.Second), ToolName: "Edit"},
		{Kind: EventText, Timestamp: ts.Add(6 * time.Second), Text: "Edit applied."},
		{Kind: EventToolCall, Timestamp: ts.Add(7 * time.Second), ToolName: "Read", ToolInput: map[string]any{"path": "/foo.go"}, FilePaths: []string{"/foo.go"}},
		{Kind: EventToolResult, Timestamp: ts.Add(8 * time.Second), ToolName: "Read"},
		{Kind: EventToolCall, Timestamp: ts.Add(9 * time.Second), ToolName: "Edit", ToolInput: map[string]any{"path": "/foo.go", "old": "b", "new": "a"}, FilePaths: []string{"/foo.go"}},
		{Kind: EventToolResult, Timestamp: ts.Add(10 * time.Second), ToolName: "Edit"},
		{Kind: EventResult, Timestamp: ts.Add(11 * time.Second), Text: "Done."},
	}

	got := SummarizeEvents(events)

	assert.LessOrEqual(t, len(got), 1024, "summary should be under ~1KB for 12 events")
	assert.Contains(t, got, "Read", "tool names should appear in summary")
	assert.Contains(t, got, "Edit", "tool names should appear in summary")
	assert.Contains(t, got, "/foo.go", "file paths should appear in summary")

	got2 := SummarizeEvents(events)
	assert.Equal(t, got, got2, "summarize must be deterministic for identical input")

	assert.Equal(t, 12, strings.Count(got, "\n")+countTrailingNonNewline(got),
		"summary should have one line per event (12 events)")
}

func countTrailingNonNewline(s string) int {
	if s == "" || strings.HasSuffix(s, "\n") {
		return 0
	}
	return 1
}
