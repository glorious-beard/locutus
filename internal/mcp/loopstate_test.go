package mcp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoopStore_BeginFreshStartsAtZero(t *testing.T) {
	s := newLoopStore()
	rec := s.Begin(loopKey{"tok", "a", "t"}, 20)
	assert.Equal(t, 0, rec.iteration)
	assert.Equal(t, 20, rec.maxIter)
	assert.NotEqual(t, "", rec.runID)
}

func TestLoopStore_AdvanceIncrementsAndContinues(t *testing.T) {
	s := newLoopStore()
	key := loopKey{"tok", "a", "t"}
	s.Begin(key, 20)

	rec, cont := s.Advance(key, false, "r")
	assert.Equal(t, 1, rec.iteration)
	assert.True(t, cont)

	got, ok := s.Status(key)
	assert.True(t, ok)
	assert.Equal(t, 1, got.iteration)
	assert.Equal(t, "r", got.lastVerdict)
}

func TestLoopStore_AdvanceStopsOnConverged(t *testing.T) {
	s := newLoopStore()
	key := loopKey{"tok", "a", "t"}
	s.Begin(key, 20)

	_, cont := s.Advance(key, true, "done")
	assert.False(t, cont)

	_, ok := s.Status(key)
	assert.False(t, ok)
}

func TestLoopStore_AdvanceStopsAtCap(t *testing.T) {
	s := newLoopStore()
	key := loopKey{"tok", "a", "t"}
	s.Begin(key, 2)

	rec, cont := s.Advance(key, false, "")
	assert.Equal(t, 1, rec.iteration)
	assert.True(t, cont)

	rec, cont = s.Advance(key, false, "")
	assert.Equal(t, 2, rec.iteration)
	assert.False(t, cont)

	_, ok := s.Status(key)
	assert.False(t, ok)
}

func TestLoopStore_BeginRecoversLiveRecord(t *testing.T) {
	s := newLoopStore()
	key := loopKey{"tok", "a", "t"}
	s.Begin(key, 20)
	s.Advance(key, false, "")

	rec := s.Begin(key, 99)
	assert.Equal(t, 1, rec.iteration)
	assert.Equal(t, 20, rec.maxIter)
}

func TestLoopStore_FreshAfterTerminal(t *testing.T) {
	s := newLoopStore()
	key := loopKey{"tok", "a", "t"}
	s.Begin(key, 20)
	s.Advance(key, true, "done") // terminal, record dropped

	rec := s.Begin(key, 20)
	assert.Equal(t, 0, rec.iteration)
}

func TestLoopStore_SessionTokenIsolation(t *testing.T) {
	s := newLoopStore()
	keyA := loopKey{"tokA", "a", "t"}
	keyB := loopKey{"tokB", "a", "t"}
	s.Begin(keyA, 20)
	s.Begin(keyB, 20)

	s.Advance(keyA, false, "")

	gotA, _ := s.Status(keyA)
	gotB, _ := s.Status(keyB)
	assert.Equal(t, 1, gotA.iteration)
	assert.Equal(t, 0, gotB.iteration)
}

func TestLoopStore_AdvanceMissingKeyIsNoop(t *testing.T) {
	s := newLoopStore()
	rec, cont := s.Advance(loopKey{"tok", "a", "t"}, false, "")
	assert.Equal(t, loopRecord{}, rec)
	assert.False(t, cont)
}

func TestLoopStore_GCDropsStale(t *testing.T) {
	s := newLoopStore()
	key := loopKey{"tok", "a", "t"}
	s.Begin(key, 20)

	// GC(0): updatedAt==now is not older than now-0, so the record survives.
	s.GC(0)
	_, ok := s.Status(key)
	assert.True(t, ok)

	// Age the record past the TTL, then GC drops it.
	time.Sleep(5 * time.Millisecond)
	s.GC(time.Millisecond)
	_, ok = s.Status(key)
	assert.False(t, ok)
}
