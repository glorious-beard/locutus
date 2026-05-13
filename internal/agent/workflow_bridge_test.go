package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBridgeToSink_NilSinkIsNoOp confirms the helper degrades cleanly
// when no sink is supplied: returns a non-nil no-op closer, leaves
// Events nil so emitEvent's existing fast path still skips emission.
// Mirrors how non-CLI callers (MCP without a sink, future
// programmatic consumers) get a transparent workflow without paying
// for an unused goroutine.
func TestBridgeToSink_NilSinkIsNoOp(t *testing.T) {
	exec := &WorkflowExecutor[struct{}]{}
	closer := exec.BridgeToSink(nil)
	assert.NotNil(t, closer, "closer must be callable even with nil sink")
	closer() // must not panic
	assert.Nil(t, exec.Events, "Events should stay nil so emitEvent skips emission")
}

// TestBridgeToSink_ForwardsEmittedEvents is the integration property
// every wired-up call site relies on: calling emitEvent on the bridged
// executor causes sink.OnEvent to fire with the same payload. The
// regression that prompted this work was Events being nil so emitEvent
// was silently no-op'ing; this test pins the corrected behaviour.
func TestBridgeToSink_ForwardsEmittedEvents(t *testing.T) {
	sink := &CapturingSink{}
	exec := &WorkflowExecutor[struct{}]{}
	closer := exec.BridgeToSink(sink)

	exec.emitEvent("step-1", "agent-a", "started", "")
	exec.emitEvent("step-1", "agent-a", "completed", "")
	exec.emitEvent("step-2", "agent-b", "queued", "warming")

	closer() // drains the channel before we inspect

	events := sink.Events()
	assert.Len(t, events, 3)
	assert.Equal(t, "step-1", events[0].StepID)
	assert.Equal(t, "agent-a", events[0].AgentID)
	assert.Equal(t, "started", events[0].Status)
	assert.Equal(t, "completed", events[1].Status)
	assert.Equal(t, "step-2", events[2].StepID)
	assert.Equal(t, "warming", events[2].Message)
}

// TestBridgeToSink_DoubleCloseSafe pins the sync.Once contract — the
// helper hands the closer back to the caller for defer, and defer
// fires once; but a panic-recovering caller or a test that calls it
// twice should see a no-op rather than a "send on closed channel"
// panic.
func TestBridgeToSink_DoubleCloseSafe(t *testing.T) {
	sink := &CapturingSink{}
	exec := &WorkflowExecutor[struct{}]{}
	closer := exec.BridgeToSink(sink)
	closer()
	closer() // must not panic
}
