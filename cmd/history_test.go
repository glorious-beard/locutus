package cmd

import (
	"testing"
	"time"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupHistoryFS(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))
	h := history.NewHistorian(fs, ".borg/history")

	require.NoError(t, h.Record(history.Event{
		ID:        "evt-001",
		Timestamp: time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
		Kind:      "decision_created",
		TargetID:  "dec-lang",
		Rationale: "Performance and simplicity",
		Alternatives: []string{"Python", "Rust"},
	}))
	require.NoError(t, h.Record(history.Event{
		ID:        "evt-002",
		Timestamp: time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC),
		Kind:      "decision_updated",
		TargetID:  "dec-lang",
		Rationale: "Team consensus",
	}))
	require.NoError(t, h.Record(history.Event{
		ID:        "evt-003",
		Timestamp: time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC),
		Kind:      "feature_added",
		TargetID:  "feat-auth",
		Rationale: "Customer request",
	}))
	return fs
}

func TestHistoryMCPAllEvents(t *testing.T) {
	fs := setupHistoryFS(t)
	res, _, err := runHistoryMCP(fs, historyInput{})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, res.Content, 1)
}

func TestHistoryMCPFilterByTarget(t *testing.T) {
	fs := setupHistoryFS(t)
	res, _, err := runHistoryMCP(fs, historyInput{ID: "dec-lang"})
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestHistoryMCPAlternatives(t *testing.T) {
	fs := setupHistoryFS(t)
	res, _, err := runHistoryMCP(fs, historyInput{ID: "dec-lang", Alternatives: true})
	require.NoError(t, err)
	require.False(t, res.IsError, "expected success, got error")
}

func TestHistoryMCPAlternativesRequiresID(t *testing.T) {
	fs := setupHistoryFS(t)
	res, _, err := runHistoryMCP(fs, historyInput{Alternatives: true})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error when id missing")
}

func TestHistoryMCPNarrativeMissing(t *testing.T) {
	fs := setupHistoryFS(t)
	res, _, err := runHistoryMCP(fs, historyInput{Narrative: true})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestHistoryMCPEmpty(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))
	res, _, err := runHistoryMCP(fs, historyInput{})
	require.NoError(t, err)
	require.False(t, res.IsError)
	// Text says "No history events recorded."
	assert.NotEmpty(t, res.Content)
}

func TestHistoryFirstLine(t *testing.T) {
	assert.Equal(t, "first", firstLine("first\nsecond"))
	assert.Equal(t, "only", firstLine("only"))
	assert.Equal(t, "", firstLine(""))
}
