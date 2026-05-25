package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/render"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureInvalidated builds a memfs with one valid approach and one
// invalidated approach so renderer tests can assert the badge / banner
// only fires when the marker is set.
func fixtureInvalidated(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))

	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-foo", spec.Feature{
		ID: "feat-foo", Title: "Foo feature with auth", Status: spec.FeatureStatusProposed,
		Approaches: []string{"app-valid", "app-stale"},
		CreatedAt:  now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-valid.md", spec.Approach{
		ID: "app-valid", Title: "Valid auth approach", ParentID: "feat-foo",
		CreatedAt: now, UpdatedAt: now,
	}, "valid body"))

	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-stale.md", spec.Approach{
		ID: "app-stale", Title: "Stale auth approach", ParentID: "feat-foo",
		InvalidatedByEventID: "20260509T120953-001-node-superseded",
		CreatedAt:            now, UpdatedAt: now,
	}, "stale body — pre-supersede"))

	return fs
}

// fixtureInvalidatedDisk mirrors fixtureInvalidated but lives on the
// real filesystem so search.Open can write Bluge segments. Returns
// the FS and OS root for RunList tests.
func fixtureInvalidatedDisk(t *testing.T) (specio.FS, string) {
	t.Helper()
	root := t.TempDir()
	fs := specio.NewOSFS(root)
	for _, dir := range []string{"features", "strategies", "decisions", "approaches", "bugs"} {
		require.NoError(t, fs.MkdirAll(".borg/spec/"+dir, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, ".borg", "manifest.json"),
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))

	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-foo", spec.Feature{
		ID: "feat-foo", Title: "Foo feature with auth", Status: spec.FeatureStatusProposed,
		Approaches: []string{"app-valid", "app-stale"},
		CreatedAt:  now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-valid.md", spec.Approach{
		ID: "app-valid", Title: "Valid auth approach", ParentID: "feat-foo",
		CreatedAt: now, UpdatedAt: now,
	}, "valid body"))

	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-stale.md", spec.Approach{
		ID: "app-stale", Title: "Stale auth approach", ParentID: "feat-foo",
		InvalidatedByEventID: "20260509T120953-001-node-superseded",
		CreatedAt:            now, UpdatedAt: now,
	}, "stale body — pre-supersede"))

	return fs, root
}

// --- list badge ---

func TestListBadgeMarksInvalidatedApproach(t *testing.T) {
	fs, root := fixtureInvalidatedDisk(t)
	r, err := RunList(fs, root, "auth", "")
	require.NoError(t, err)
	require.NotEmpty(t, r.Hits)

	// Both approaches match "auth" — badge must distinguish them.
	var validRow, staleRow string
	for _, line := range strings.Split(r.Markdown, "\n") {
		if strings.Contains(line, "app-valid") {
			validRow = line
		}
		if strings.Contains(line, "app-stale") {
			staleRow = line
		}
	}
	require.NotEmpty(t, staleRow, "stale approach must appear in list output")
	require.NotEmpty(t, validRow, "valid approach must appear in list output")

	assert.Contains(t, staleRow, "[invalidated]",
		"invalidated approach row must carry the [invalidated] badge")
	assert.NotContains(t, validRow, "[invalidated]",
		"valid approach row must not carry the badge")
}

func TestListResultExposesInvalidatedFlag(t *testing.T) {
	fs, root := fixtureInvalidatedDisk(t)
	r, err := RunList(fs, root, "auth", "")
	require.NoError(t, err)

	hitByID := map[string]ListHit{}
	for _, h := range r.Hits {
		hitByID[h.ID] = h
	}
	require.Contains(t, hitByID, "app-stale")
	require.Contains(t, hitByID, "app-valid")
	assert.True(t, hitByID["app-stale"].Invalidated,
		"ListHit.Invalidated must surface the marker for downstream consumers")
	assert.False(t, hitByID["app-valid"].Invalidated)
}

// --- explain banner ---

func TestExplainBannerOnInvalidatedApproach(t *testing.T) {
	fs := fixtureInvalidated(t)
	r, err := RunExplain(fs, "app-stale")
	require.NoError(t, err)

	assert.Contains(t, r.Markdown, "Invalidated by event",
		"invalidated approach must render a banner naming the event")
	assert.Contains(t, r.Markdown, "20260509T120953-001-node-superseded",
		"banner must include the event id so the operator can locate it")
	assert.Contains(t, r.Markdown, "locutus adopt",
		"banner must point the operator at adopt as the next step")
}

func TestExplainNoBannerOnValidApproach(t *testing.T) {
	fs := fixtureInvalidated(t)
	r, err := RunExplain(fs, "app-valid")
	require.NoError(t, err)

	assert.NotContains(t, r.Markdown, "Invalidated by event",
		"valid approach must not render the invalidation banner")
}

// --- status section ---

func TestStatusGathersInvalidatedApproaches(t *testing.T) {
	fs := fixtureInvalidated(t)
	sd := GatherStatus(fs)

	assert.ElementsMatch(t, []string{"app-stale"}, sd.InvalidatedApproaches,
		"GatherStatus must populate InvalidatedApproaches with ids of stale approaches")
}

func TestStatusSummaryRendersPendingReconcileSection(t *testing.T) {
	fs := fixtureInvalidated(t)
	sd := GatherStatus(fs)
	out := render.StatusSummary(sd)

	assert.Contains(t, out, "Pending reconcile",
		"status summary must include a 'Pending reconcile' section when invalidated approaches exist")
	assert.Contains(t, out, "app-stale",
		"the section must list the invalidated approach by id")
}

func TestStatusSummaryOmitsPendingReconcileWhenNone(t *testing.T) {
	sd := render.StatusData{
		GoalsPresent:  true,
		FeatureCount:  1,
		DecisionCount: 0,
	}
	out := render.StatusSummary(sd)
	assert.NotContains(t, out, "Pending reconcile",
		"empty invalidated list must not produce the section header")
}
