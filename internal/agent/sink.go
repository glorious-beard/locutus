package agent

import "sync"

// EventSink consumes WorkflowEvents emitted by a running council so a
// renderer (CLI spinners, MCP progress notifications, plain-text logs)
// can show what's happening. Implementations must be safe for
// concurrent OnEvent calls — parallel critic steps fire events from
// multiple goroutines.
//
// Close is called once after the workflow finishes, win or lose.
// Renderers that own UI state (e.g., a pterm MultiPrinter) should tear
// it down here.
type EventSink interface {
	OnEvent(WorkflowEvent)
	Close()
}

// SilentSink discards every event. Useful as the default when a caller
// hasn't supplied a sink, when --json output is requested (the JSON
// result is the only thing on stdout), or in tests.
type SilentSink struct{}

func (SilentSink) OnEvent(WorkflowEvent) {}
func (SilentSink) Close()                {}

// FuncSink turns an arbitrary callback into an EventSink — handy for
// tests that want to assert which events arrived without spinning up
// a real renderer.
type FuncSink struct {
	OnEventFunc func(WorkflowEvent)
	OnCloseFunc func()
}

func (f FuncSink) OnEvent(e WorkflowEvent) {
	if f.OnEventFunc != nil {
		f.OnEventFunc(e)
	}
}

func (f FuncSink) Close() {
	if f.OnCloseFunc != nil {
		f.OnCloseFunc()
	}
}

// CapturingSink records every event in arrival order. Goroutine-safe.
// Tests use this to assert which agents started/completed.
type CapturingSink struct {
	mu     sync.Mutex
	events []WorkflowEvent
	closed bool
}

func (c *CapturingSink) OnEvent(e WorkflowEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *CapturingSink) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

// Events returns a snapshot of recorded events in arrival order.
func (c *CapturingSink) Events() []WorkflowEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]WorkflowEvent, len(c.events))
	copy(out, c.events)
	return out
}

// Closed reports whether Close has been called.
func (c *CapturingSink) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
