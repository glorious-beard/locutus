package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSilentSinkSatisfiesInterface(t *testing.T) {
	var sink EventSink = SilentSink{}
	// Just exercising the methods to confirm no panic.
	sink.OnEvent(WorkflowEvent{StepID: "x"})
	sink.Close()
}

func TestCapturingSinkRecordsEventsInOrder(t *testing.T) {
	c := &CapturingSink{}
	c.OnEvent(WorkflowEvent{StepID: "survey", Status: "started", Timestamp: time.Now()})
	c.OnEvent(WorkflowEvent{StepID: "survey", Status: "completed"})
	c.OnEvent(WorkflowEvent{StepID: "propose", Status: "started"})

	events := c.Events()
	assert.Len(t, events, 3)
	assert.Equal(t, "survey", events[0].StepID)
	assert.Equal(t, "started", events[0].Status)
	assert.Equal(t, "completed", events[1].Status)
	assert.Equal(t, "propose", events[2].StepID)

	assert.False(t, c.Closed())
	c.Close()
	assert.True(t, c.Closed())
}

func TestFuncSinkInvokesCallbacks(t *testing.T) {
	var got []WorkflowEvent
	closed := false
	sink := FuncSink{
		OnEventFunc: func(e WorkflowEvent) { got = append(got, e) },
		OnCloseFunc: func() { closed = true },
	}
	sink.OnEvent(WorkflowEvent{StepID: "a"})
	sink.OnEvent(WorkflowEvent{StepID: "b"})
	sink.Close()

	assert.Len(t, got, 2)
	assert.True(t, closed)
}

func TestFuncSinkNilCallbacksAreSafe(t *testing.T) {
	// nil callbacks should not panic — used in tests that only care
	// about a subset of the lifecycle.
	sink := FuncSink{}
	sink.OnEvent(WorkflowEvent{StepID: "x"})
	sink.Close()
}
