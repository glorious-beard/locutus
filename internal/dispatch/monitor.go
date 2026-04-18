package dispatch

import "time"

// monitor is a sliding window over the agent's event stream with a cooldown
// clock and a circuit breaker. It is purely mechanical — no pattern
// detection happens here. All judgments ("is this cycling?") are made by
// the LLM invoked via Supervisor.monitorCycle.
type monitor struct {
	events           []AgentEvent
	windowSize       int
	eventsSinceCheck int
	lastCheckAt      time.Time
	checkEveryEvents int
	checkEveryTime   time.Duration
	circuitTrips     int
	circuitTripMax   int
	now              func() time.Time
}

// newMonitor returns a monitor with the plan's default parameters. Tests
// override fields directly before exercising the struct.
func newMonitor() *monitor {
	now := time.Now
	return &monitor{
		windowSize:       20,
		checkEveryEvents: 15,
		checkEveryTime:   30 * time.Second,
		circuitTripMax:   3,
		now:              now,
		lastCheckAt:      now(),
	}
}

// Observe appends the event to the ring buffer, evicting the oldest entry
// once the window is full, and bumps the per-check counter.
func (m *monitor) Observe(evt AgentEvent) {
	m.events = append(m.events, evt)
	if len(m.events) > m.windowSize {
		// Drop oldest entries to keep a tight bound on memory.
		m.events = append([]AgentEvent(nil), m.events[len(m.events)-m.windowSize:]...)
	}
	m.eventsSinceCheck++
}

// ShouldCheck returns true when either checkEveryEvents observations have
// accumulated since the last check, or checkEveryTime has elapsed with at
// least one observation pending. A tripped circuit breaker suppresses both
// triggers for the remainder of this supervision attempt.
func (m *monitor) ShouldCheck() bool {
	if m.circuitTrips >= m.circuitTripMax {
		return false
	}
	if m.eventsSinceCheck == 0 {
		return false
	}
	if m.eventsSinceCheck >= m.checkEveryEvents {
		return true
	}
	if m.now().Sub(m.lastCheckAt) >= m.checkEveryTime {
		return true
	}
	return false
}

// RecentEvents returns a defensive copy of the ring buffer contents in
// observation order (oldest first, newest last). Callers may not mutate.
func (m *monitor) RecentEvents() []AgentEvent {
	out := make([]AgentEvent, len(m.events))
	copy(out, m.events)
	return out
}

// MarkChecked records that a monitor invocation has just completed. A
// non-nil err increments the circuit-breaker counter; a nil err resets it
// so transient failures don't permanently disable the monitor.
func (m *monitor) MarkChecked(err error) {
	m.eventsSinceCheck = 0
	m.lastCheckAt = m.now()
	if err != nil {
		m.circuitTrips++
		return
	}
	m.circuitTrips = 0
}
