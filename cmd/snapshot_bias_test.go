// DJ-138 phase 6 — snapshot recent-biased section. Asserts that
// `locutus status --full` surfaces spec_biased history events
// when they exist, omits the section gracefully when none have
// run, and applies the truncation + ordering contract.

package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/history"
	"github.com/glorious-beard/locutus/internal/render"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSnapshotTestFS(t *testing.T) specio.FS {
	t.Helper()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/history", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/traces.json", []byte(`{"entries":{}}`), 0o644))
	return fsys
}

func recordBias(t *testing.T, fsys specio.FS, target, bias string, br history.BlastRadiusEstimate) string {
	t.Helper()
	hist := history.NewHistorian(fsys, ".borg/history")
	id, err := history.RecordBiased(hist, target, bias, "sess-test", br)
	require.NoError(t, err)
	// Sleep so successive events have strictly-increasing
	// timestamps the historian sort sees.
	time.Sleep(2 * time.Millisecond)
	return id
}

// TestStatusFull_SurfacesRecentBiased — when spec_biased events
// exist, the snapshot includes them in RecentBiased + the
// markdown renders the "Recent biases" section.
func TestStatusFull_SurfacesRecentBiased(t *testing.T) {
	fsys := newSnapshotTestFS(t)
	// Seed a small spec graph so LoadSpec doesn't fail.
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/decisions/dec-oltp-store",
		spec.Decision{ID: "dec-oltp-store", Title: "Choose Postgres", Status: spec.DecisionStatusActive,
			Confidence: 1.0, Rationale: "r", Axes: []string{"oltp-store"}}, ""))

	recordBias(t, fsys, "dec-oltp-store", "use postgres, the team owns ops",
		history.BlastRadiusEstimate{DecisionsTouched: 1, FeaturesRewritten: 4, ApproachesDrifted: 11})

	data, err := GatherSnapshotData(fsys, render.SnapshotFilters{})
	require.NoError(t, err)
	require.Len(t, data.RecentBiased, 1)
	assert.Equal(t, "dec-oltp-store", data.RecentBiased[0].TargetID)
	assert.Equal(t, "use postgres, the team owns ops", data.RecentBiased[0].BiasPreview)
	assert.Equal(t, "1 dec, 4 feat, 0 strat, 11 app", data.RecentBiased[0].BlastRadius)

	md := render.SnapshotMarkdown(data)
	assert.Contains(t, md, "## Recent biases")
	assert.Contains(t, md, "dec-oltp-store")
	assert.Contains(t, md, "use postgres")
}

// TestStatusFull_AbsentWhenNoBiases — an empty history log
// produces an empty RecentBiased and no "Recent biases" section
// in the markdown.
func TestStatusFull_AbsentWhenNoBiases(t *testing.T) {
	fsys := newSnapshotTestFS(t)

	data, err := GatherSnapshotData(fsys, render.SnapshotFilters{})
	require.NoError(t, err)
	assert.Empty(t, data.RecentBiased)

	md := render.SnapshotMarkdown(data)
	assert.NotContains(t, md, "## Recent biases", "no section header when no biases ran")
}

// TestStatusFull_TruncatesLongBias — bias text longer than the
// preview length is truncated with an ellipsis. The full bias
// stays in the history event.
func TestStatusFull_TruncatesLongBias(t *testing.T) {
	fsys := newSnapshotTestFS(t)
	long := strings.Repeat("x", 200)
	recordBias(t, fsys, "dec-oltp-store", long,
		history.BlastRadiusEstimate{DecisionsTouched: 1})

	data, err := GatherSnapshotData(fsys, render.SnapshotFilters{})
	require.NoError(t, err)
	require.Len(t, data.RecentBiased, 1)
	preview := data.RecentBiased[0].BiasPreview
	assert.LessOrEqual(t, len(preview), biasPreviewLength,
		"preview should not exceed the configured cap")
	assert.True(t, strings.HasSuffix(preview, "…"), "long preview should end with ellipsis")
}

// TestStatusFull_OrdersBiasesNewestFirst — multiple bias events
// surface newest-first so the operator sees the most recent
// cascade at the top.
func TestStatusFull_OrdersBiasesNewestFirst(t *testing.T) {
	fsys := newSnapshotTestFS(t)
	recordBias(t, fsys, "dec-a", "first",
		history.BlastRadiusEstimate{DecisionsTouched: 1})
	recordBias(t, fsys, "dec-b", "second",
		history.BlastRadiusEstimate{DecisionsTouched: 1})
	recordBias(t, fsys, "dec-c", "third",
		history.BlastRadiusEstimate{DecisionsTouched: 1})

	data, err := GatherSnapshotData(fsys, render.SnapshotFilters{})
	require.NoError(t, err)
	require.Len(t, data.RecentBiased, 3)
	assert.Equal(t, "dec-c", data.RecentBiased[0].TargetID, "newest first")
	assert.Equal(t, "dec-b", data.RecentBiased[1].TargetID)
	assert.Equal(t, "dec-a", data.RecentBiased[2].TargetID)
}
