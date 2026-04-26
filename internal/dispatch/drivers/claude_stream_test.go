package drivers

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/chetan/locutus/internal/dispatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadFixture(t *testing.T, name string) io.Reader {
	t.Helper()
	path := filepath.Join("testdata", name)
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func drainParser(t *testing.T, p dispatch.StreamParser) []dispatch.AgentEvent {
	t.Helper()
	var events []dispatch.AgentEvent
	for {
		evt, err := p.Next(context.Background())
		if err == io.EOF {
			return events
		}
		require.NoError(t, err)
		events = append(events, evt)
	}
}

func TestClaudeStream_InitEvent(t *testing.T) {
	p := ClaudeCodeDriver{}.ParseStream(loadFixture(t, "claude_simple.ndjson"))
	t.Cleanup(func() { _ = p.Close() })

	evt, err := p.Next(context.Background())
	require.NoError(t, err)
	assert.Equal(t, dispatch.EventInit, evt.Kind)
	assert.NotEmpty(t, evt.SessionID, "init event must carry the session ID")
}

func TestClaudeStream_TextReassembly(t *testing.T) {
	p := ClaudeCodeDriver{}.ParseStream(loadFixture(t, "claude_with_tool_use.ndjson"))
	t.Cleanup(func() { _ = p.Close() })

	events := drainParser(t, p)

	var textEvents []dispatch.AgentEvent
	for _, e := range events {
		if e.Kind == dispatch.EventText {
			textEvents = append(textEvents, e)
		}
	}
	require.NotEmpty(t, textEvents, "expected at least one EventText from reassembly")

	// The fixture's post-tool assistant text is ~470 chars across 8 text_deltas.
	// One of the emitted EventText events must carry the full reassembled content.
	longest := textEvents[0]
	for _, e := range textEvents[1:] {
		if len(e.Text) > len(longest.Text) {
			longest = e
		}
	}
	assert.Greater(t, len(longest.Text), 200, "reassembled text should be the full accumulated content, not a fragment")
	assert.Contains(t, longest.Text, "sample.txt")
	assert.Contains(t, longest.Text, "hello world")
}

func TestClaudeStream_ToolCallReassembly(t *testing.T) {
	p := ClaudeCodeDriver{}.ParseStream(loadFixture(t, "claude_with_tool_use.ndjson"))
	t.Cleanup(func() { _ = p.Close() })

	events := drainParser(t, p)

	var toolCalls []dispatch.AgentEvent
	for _, e := range events {
		if e.Kind == dispatch.EventToolCall {
			toolCalls = append(toolCalls, e)
		}
	}
	require.Len(t, toolCalls, 1, "fixture has exactly one tool call (Read)")

	tc := toolCalls[0]
	assert.Equal(t, "Read", tc.ToolName)
	require.NotNil(t, tc.ToolInput, "ToolInput must be populated from reassembled input_json_deltas")
	assert.Equal(t, "/private/tmp/locutus-fixture-capture/sample.txt", tc.ToolInput["file_path"])
	assert.Contains(t, tc.FilePaths, "/private/tmp/locutus-fixture-capture/sample.txt",
		"FilePaths should surface file paths extracted from ToolInput")
}

func TestClaudeStream_ToolResult(t *testing.T) {
	p := ClaudeCodeDriver{}.ParseStream(loadFixture(t, "claude_with_tool_use.ndjson"))
	t.Cleanup(func() { _ = p.Close() })

	events := drainParser(t, p)

	var results []dispatch.AgentEvent
	for _, e := range events {
		if e.Kind == dispatch.EventToolResult {
			results = append(results, e)
		}
	}
	require.Len(t, results, 1, "fixture has exactly one tool_result event")
	assert.Contains(t, results[0].Text, "hello world",
		"tool_result content must be preserved in Text")
}

func TestClaudeStream_ResultEvent(t *testing.T) {
	p := ClaudeCodeDriver{}.ParseStream(loadFixture(t, "claude_simple.ndjson"))
	t.Cleanup(func() { _ = p.Close() })

	events := drainParser(t, p)

	var finalResults []dispatch.AgentEvent
	for _, e := range events {
		if e.Kind == dispatch.EventResult {
			finalResults = append(finalResults, e)
		}
	}
	require.Len(t, finalResults, 1, "fixture has exactly one result event")
	assert.Equal(t, "pong", finalResults[0].Text)
}

func TestClaudeStream_IgnoresRateLimitAndAssistantDuplicates(t *testing.T) {
	// The tool_use fixture has 1 rate_limit_event and 3 assistant events;
	// none should produce duplicate AgentEvents. Tool calls in particular must
	// appear exactly once (from reassembly), not duplicated by the assistant
	// full-message events that carry the same content block.
	p := ClaudeCodeDriver{}.ParseStream(loadFixture(t, "claude_with_tool_use.ndjson"))
	t.Cleanup(func() { _ = p.Close() })

	events := drainParser(t, p)

	toolCalls := 0
	for _, e := range events {
		if e.Kind == dispatch.EventToolCall {
			toolCalls++
		}
	}
	assert.Equal(t, 1, toolCalls, "tool call must appear exactly once")

	for _, e := range events {
		assert.NotEqual(t, dispatch.EventKind("rate_limit_event"), e.Kind,
			"rate_limit_event is provider noise, must be dropped")
	}
}

func TestClaudeStream_IgnoresThinkingSignatureDeltas(t *testing.T) {
	// The tool_use fixture includes a thinking block whose delta is a
	// signature_delta (opaque base64). These must be consumed without
	// producing events and without leaking into text events.
	p := ClaudeCodeDriver{}.ParseStream(loadFixture(t, "claude_with_tool_use.ndjson"))
	t.Cleanup(func() { _ = p.Close() })

	events := drainParser(t, p)

	for _, e := range events {
		if e.Kind == dispatch.EventText {
			assert.NotContains(t, e.Text, "EoUC",
				"signature_delta content must not leak into EventText")
		}
	}
}

func TestClaudeStream_EOFTerminates(t *testing.T) {
	p := ClaudeCodeDriver{}.ParseStream(loadFixture(t, "claude_simple.ndjson"))
	t.Cleanup(func() { _ = p.Close() })

	// Drain all events, then Next must return io.EOF.
	for {
		_, err := p.Next(context.Background())
		if err == io.EOF {
			return
		}
		require.NoError(t, err)
	}
}

func TestClaudeStream_CtxCancelMidStream(t *testing.T) {
	p := ClaudeCodeDriver{}.ParseStream(loadFixture(t, "claude_with_tool_use.ndjson"))
	t.Cleanup(func() { _ = p.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	// Prime the parser with one event so we know it's past init.
	_, err := p.Next(ctx)
	require.NoError(t, err)

	cancel()
	_, err = p.Next(ctx)
	require.ErrorIs(t, err, context.Canceled, "Next must return ctx.Err() after cancellation")
}

func TestClaudeDriver_RespondToAgent(t *testing.T) {
	driver := ClaudeCodeDriver{}
	cmd, err := driver.RespondToAgent(context.Background(), "sess-abc-123", "allow: safe for this workdir")
	require.NoError(t, err)
	require.NotNil(t, cmd)

	assert.Equal(t, "claude", cmd.Args[0])
	assert.Contains(t, cmd.Args, "--resume")
	assert.Contains(t, cmd.Args, "sess-abc-123")

	foundResponse := false
	for _, a := range cmd.Args {
		if a == "allow: safe for this workdir" {
			foundResponse = true
			break
		}
	}
	assert.True(t, foundResponse, "response text should appear verbatim in command args")
}
