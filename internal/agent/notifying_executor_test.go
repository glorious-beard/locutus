package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNotifyingExecutor_EmitsStartedAndCompleted — a successful direct
// LLM call surfaces one started + one completed event keyed by the
// agent's ID, with no enclosing StepID (NotifyingExecutor doesn't
// know about workflow step IDs because it sits below them).
func TestNotifyingExecutor_EmitsStartedAndCompleted(t *testing.T) {
	mock := NewMockExecutor(MockResponse{Response: &AgentOutput{Content: "ok"}})
	sink := &CapturingSink{}
	exec := &NotifyingExecutor{Inner: mock, Sink: sink}

	out, err := exec.Run(context.Background(), AgentDef{ID: "spec-advocate"}, AgentInput{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "ok", out.Content)

	events := sink.Events()
	require.Len(t, events, 2, "one started + one completed")
	assert.Equal(t, "spec-advocate", events[0].AgentID)
	assert.Equal(t, "started", events[0].Status)
	assert.Empty(t, events[0].StepID, "direct calls have no enclosing step")
	assert.Equal(t, "spec-advocate", events[1].AgentID)
	assert.Equal(t, "completed", events[1].Status)
}

// TestNotifyingExecutor_EmitsErrorOnFailure — an LLM error surfaces
// as a single error event carrying the message.
func TestNotifyingExecutor_EmitsErrorOnFailure(t *testing.T) {
	mock := NewMockExecutor(MockResponse{Err: errors.New("rate limited")})
	sink := &CapturingSink{}
	exec := &NotifyingExecutor{Inner: mock, Sink: sink}

	_, err := exec.Run(context.Background(), AgentDef{ID: "researcher"}, AgentInput{})
	require.Error(t, err)

	events := sink.Events()
	require.Len(t, events, 2, "started + error")
	assert.Equal(t, "started", events[0].Status)
	assert.Equal(t, "error", events[1].Status)
	assert.Contains(t, events[1].Message, "rate limited")
}

// TestNotifyingExecutor_SuppressedInsideWorkflow — the WorkflowExecutor
// stamps WithSuppressLLMNotify before each step's LLM dispatch so the
// per-step events the workflow already emits aren't doubled up by
// per-call events from the wrapping NotifyingExecutor. Simulate that
// stamping here and assert no events fire.
func TestNotifyingExecutor_SuppressedInsideWorkflow(t *testing.T) {
	mock := NewMockExecutor(MockResponse{Response: &AgentOutput{Content: "ok"}})
	sink := &CapturingSink{}
	exec := &NotifyingExecutor{Inner: mock, Sink: sink}

	ctx := WithSuppressLLMNotify(context.Background())
	_, err := exec.Run(ctx, AgentDef{ID: "spec-scout"}, AgentInput{})
	require.NoError(t, err)

	assert.Empty(t, sink.Events(),
		"suppressed context must skip notification emission so workflow per-step events aren't doubled")
}

// TestNotifyingExecutor_NilSinkOrInnerIsPassthrough — a defensively-
// constructed wrapper (nil sink, but real inner) still runs the
// underlying call so callers can wrap unconditionally without nil
// checks.
func TestNotifyingExecutor_NilSinkOrInnerIsPassthrough(t *testing.T) {
	mock := NewMockExecutor(MockResponse{Response: &AgentOutput{Content: "passthrough"}})
	exec := &NotifyingExecutor{Inner: mock, Sink: nil}

	out, err := exec.Run(context.Background(), AgentDef{ID: "x"}, AgentInput{})
	require.NoError(t, err)
	assert.Equal(t, "passthrough", out.Content)
}
