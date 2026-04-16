package history

import (
	"testing"
	"time"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
)

const histDir = ".borg/history"

func setupHistorian(t *testing.T) (*Historian, *specio.MemFS) {
	t.Helper()
	mfs := specio.NewMemFS()
	err := mfs.MkdirAll(histDir, 0o755)
	assert.NoError(t, err)
	h := NewHistorian(mfs, histDir)
	return h, mfs
}

func ts(hour int) time.Time {
	return time.Date(2026, 4, 15, hour, 0, 0, 0, time.UTC)
}

func TestRecordAndRetrieve(t *testing.T) {
	h, _ := setupHistorian(t)

	evt := Event{
		ID:           "evt-001",
		Timestamp:    ts(10),
		Kind:         "decision_created",
		TargetID:     "dec-lang",
		OldValue:     "",
		NewValue:     "Go",
		Rationale:    "Performance and simplicity",
		Alternatives: []string{"Python", "Rust"},
	}

	err := h.Record(evt)
	assert.NoError(t, err)

	events, err := h.Events()
	assert.NoError(t, err)
	assert.Len(t, events, 1)

	got := events[0]
	assert.Equal(t, evt.ID, got.ID)
	assert.True(t, evt.Timestamp.Equal(got.Timestamp))
	assert.Equal(t, evt.Kind, got.Kind)
	assert.Equal(t, evt.TargetID, got.TargetID)
	assert.Equal(t, evt.OldValue, got.OldValue)
	assert.Equal(t, evt.NewValue, got.NewValue)
	assert.Equal(t, evt.Rationale, got.Rationale)
	assert.Equal(t, evt.Alternatives, got.Alternatives)
}

func TestRecordMultipleEvents(t *testing.T) {
	h, _ := setupHistorian(t)

	// Record events out of chronological order.
	evts := []Event{
		{ID: "evt-002", Timestamp: ts(14), Kind: "decision_updated", TargetID: "dec-lang"},
		{ID: "evt-001", Timestamp: ts(10), Kind: "decision_created", TargetID: "dec-lang"},
		{ID: "evt-003", Timestamp: ts(18), Kind: "strategy_created", TargetID: "strat-go"},
	}
	for _, e := range evts {
		err := h.Record(e)
		assert.NoError(t, err)
	}

	events, err := h.Events()
	assert.NoError(t, err)
	assert.Len(t, events, 3)

	// Must be returned sorted by timestamp ascending.
	assert.Equal(t, "evt-001", events[0].ID)
	assert.Equal(t, "evt-002", events[1].ID)
	assert.Equal(t, "evt-003", events[2].ID)
}

func TestEventsForTarget(t *testing.T) {
	h, _ := setupHistorian(t)

	evts := []Event{
		{ID: "evt-001", Timestamp: ts(10), Kind: "decision_created", TargetID: "dec-lang"},
		{ID: "evt-002", Timestamp: ts(11), Kind: "decision_updated", TargetID: "dec-lang"},
		{ID: "evt-003", Timestamp: ts(12), Kind: "strategy_created", TargetID: "strat-go"},
		{ID: "evt-004", Timestamp: ts(13), Kind: "decision_updated", TargetID: "dec-lang"},
		{ID: "evt-005", Timestamp: ts(14), Kind: "strategy_updated", TargetID: "strat-go"},
	}
	for _, e := range evts {
		err := h.Record(e)
		assert.NoError(t, err)
	}

	got, err := h.EventsForTarget("dec-lang")
	assert.NoError(t, err)
	assert.Len(t, got, 3)
	for _, e := range got {
		assert.Equal(t, "dec-lang", e.TargetID)
	}
}

func TestAlternatives(t *testing.T) {
	h, _ := setupHistorian(t)

	evts := []Event{
		{
			ID: "evt-001", Timestamp: ts(10), Kind: "decision_created",
			TargetID: "dec-lang", Alternatives: []string{"Python", "Rust"},
		},
		{
			ID: "evt-002", Timestamp: ts(12), Kind: "decision_updated",
			TargetID: "dec-lang", Alternatives: []string{"Java"},
		},
	}
	for _, e := range evts {
		err := h.Record(e)
		assert.NoError(t, err)
	}

	alts, err := h.Alternatives("dec-lang")
	assert.NoError(t, err)
	assert.Len(t, alts, 3)
	assert.ElementsMatch(t, []string{"Python", "Rust", "Java"}, alts)
}

func TestEventsEmpty(t *testing.T) {
	h, _ := setupHistorian(t)

	events, err := h.Events()
	assert.NoError(t, err)
	assert.Empty(t, events)
}

func TestRecordCreatesFile(t *testing.T) {
	h, mfs := setupHistorian(t)

	evt := Event{
		ID:        "evt-001",
		Timestamp: ts(10),
		Kind:      "decision_created",
		TargetID:  "dec-lang",
		NewValue:  "Go",
	}

	err := h.Record(evt)
	assert.NoError(t, err)

	// Verify a .json file exists in the history directory.
	files := mfs.ListDir(histDir)
	assert.Len(t, files, 1)
	assert.Contains(t, files[0], ".json")
}
