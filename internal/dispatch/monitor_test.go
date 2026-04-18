package dispatch

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMonitor_RingBufferEviction(t *testing.T) {
	m := newMonitor()
	m.windowSize = 5

	for i := 0; i < 10; i++ {
		m.Observe(AgentEvent{Kind: EventText, Text: fmt.Sprintf("e%d", i)})
	}

	recent := m.RecentEvents()
	require.Len(t, recent, 5, "ring buffer should keep exactly windowSize events")
	assert.Equal(t, "e5", recent[0].Text, "oldest retained event is e5 (e0..e4 evicted)")
	assert.Equal(t, "e9", recent[4].Text, "most recent is e9")
}

func TestMonitor_CooldownByEventCount(t *testing.T) {
	m := newMonitor()
	m.checkEveryEvents = 5
	m.checkEveryTime = 24 * time.Hour // effectively disable time trigger

	assert.False(t, m.ShouldCheck(), "no events observed yet")

	for i := 0; i < 4; i++ {
		m.Observe(AgentEvent{Kind: EventText})
	}
	assert.False(t, m.ShouldCheck(), "4 events < checkEveryEvents=5")

	m.Observe(AgentEvent{Kind: EventText})
	assert.True(t, m.ShouldCheck(), "5 events == checkEveryEvents=5 → should trigger")
}

func TestMonitor_CooldownByTime(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	m := newMonitor()
	m.now = func() time.Time { return now }
	m.lastCheckAt = now
	m.checkEveryEvents = 1_000 // disable event trigger
	m.checkEveryTime = 10 * time.Second

	m.Observe(AgentEvent{Kind: EventText})
	assert.False(t, m.ShouldCheck(), "no time elapsed yet")

	now = now.Add(9 * time.Second)
	assert.False(t, m.ShouldCheck(), "9s < 10s threshold")

	now = now.Add(2 * time.Second)
	assert.True(t, m.ShouldCheck(), "11s > 10s threshold → time trigger fires")
}

func TestMonitor_CircuitBreaker_TripsAfterThreshold(t *testing.T) {
	m := newMonitor()
	m.checkEveryEvents = 1 // trivial event gate so focus stays on breaker
	m.checkEveryTime = 24 * time.Hour
	m.circuitTripMax = 3

	for i := 0; i < 3; i++ {
		m.Observe(AgentEvent{Kind: EventText})
		require.True(t, m.ShouldCheck(), "attempt %d must be allowed before the circuit trips", i+1)
		m.MarkChecked(fmt.Errorf("mock monitor error %d", i))
	}

	// Fourth observation after three consecutive errors → breaker tripped.
	m.Observe(AgentEvent{Kind: EventText})
	assert.False(t, m.ShouldCheck(), "circuit should be tripped after 3 consecutive errors")

	// And it stays tripped for further events.
	for i := 0; i < 10; i++ {
		m.Observe(AgentEvent{Kind: EventText})
		assert.False(t, m.ShouldCheck(), "circuit stays tripped for the remainder of the attempt")
	}
}

func TestMonitor_CircuitBreaker_SuccessResets(t *testing.T) {
	// Additional: a successful check clears the consecutive-error counter so
	// transient monitor failures don't permanently disable the monitor.
	m := newMonitor()
	m.checkEveryEvents = 1
	m.checkEveryTime = 24 * time.Hour
	m.circuitTripMax = 3

	m.Observe(AgentEvent{Kind: EventText})
	m.MarkChecked(fmt.Errorf("transient"))
	m.Observe(AgentEvent{Kind: EventText})
	m.MarkChecked(fmt.Errorf("transient"))
	m.Observe(AgentEvent{Kind: EventText})
	m.MarkChecked(nil) // success — clears the breaker

	// Now two more errors should not trip (counter reset to zero).
	for i := 0; i < 2; i++ {
		m.Observe(AgentEvent{Kind: EventText})
		require.True(t, m.ShouldCheck())
		m.MarkChecked(fmt.Errorf("err %d", i))
	}
	m.Observe(AgentEvent{Kind: EventText})
	assert.True(t, m.ShouldCheck(), "2 errors after a success should not trip the breaker")
}
