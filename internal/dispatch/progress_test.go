package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// These tests exercise progressMessage directly — the single function
// that decides which AgentEvents become user-visible progress updates.
// runAttempt-level integration is covered by
// TestRunAttempt_EmitsProgressForToolCalls in supervisor_stream_test.go.

func TestProgressMessage_ForwardsToolCallsWithFiles(t *testing.T) {
	evt := AgentEvent{
		Kind:      EventToolCall,
		ToolName:  "Edit",
		FilePaths: []string{"cmd/auth.go"},
	}
	msg := progressMessage(evt)
	assert.Contains(t, msg, "Edit",
		"progress message should mention the tool name")
	assert.Contains(t, msg, "cmd/auth.go",
		"progress message should surface the file path so the user sees what's being touched")
}

func TestProgressMessage_ForwardsToolCallsWithoutFiles(t *testing.T) {
	evt := AgentEvent{Kind: EventToolCall, ToolName: "Bash"}
	msg := progressMessage(evt)
	assert.Contains(t, msg, "Bash", "tool name still shows even without a file path")
}

func TestProgressMessage_ForwardsErrors(t *testing.T) {
	evt := AgentEvent{Kind: EventError, Text: "rate-limited"}
	msg := progressMessage(evt)
	assert.Contains(t, msg, "rate-limited")
}

func TestProgressMessage_SuppressesNoise(t *testing.T) {
	// These event kinds should never surface as progress updates:
	// text deltas (too chatty), api retries (internal detail), lifecycle
	// events (init/result/tool_result — either redundant or uninteresting
	// for a user-facing stream).
	noisy := []EventKind{
		EventText,
		EventRetry,
		EventInit,
		EventResult,
		EventToolResult,
	}
	for _, k := range noisy {
		t.Run(string(k), func(t *testing.T) {
			msg := progressMessage(AgentEvent{Kind: k, Text: "should not leak"})
			assert.Empty(t, msg, "EventKind %q should produce no progress message", k)
		})
	}
}
