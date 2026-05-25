package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listFixtureDisk seeds a tempdir-rooted project with the .borg
// scaffold so search.Open has somewhere to write its segments.
func listFixtureDisk(t *testing.T, kinds ...string) (specio.FS, string) {
	t.Helper()
	root := t.TempDir()
	fs := specio.NewOSFS(root)
	for _, k := range kinds {
		require.NoError(t, fs.MkdirAll(".borg/spec/"+k, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, ".borg", "manifest.json"),
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))
	return fs, root
}

// TestListMatchesAuthoredSummary covers the wiring from DJ-114's
// authored Summary field through to list's BM25 scoring. The synonym-
// bridge case (title/body silent on the query token, Summary carries
// it) must surface the node at rank 0.
func TestListMatchesAuthoredSummary(t *testing.T) {
	fs, root := listFixtureDisk(t, "decisions")
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-adopt-workos", spec.Decision{
		ID:        "dec-adopt-workos",
		Title:     "Adopt WorkOS",
		Summary:   "Adopt WorkOS for authentication, organization management, and audit logging.",
		Rationale: "WorkOS free tier and built-in RBAC.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	// Unrelated decision so we verify the search isn't matching
	// everything indiscriminately.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-pmtiles", spec.Decision{
		ID:        "dec-pmtiles",
		Title:     "Use PMTiles",
		Summary:   "Serve map tiles as PMTiles on Cloud Storage.",
		Rationale: "Serverless delivery.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	r, err := RunList(fs, root, "authentication", "")
	require.NoError(t, err)
	require.NotEmpty(t, r.Hits)
	assert.Equal(t, "dec-adopt-workos", r.Hits[0].ID,
		"authored Summary must surface the node even when title/body don't carry the query token")
	for _, h := range r.Hits {
		assert.NotEqual(t, "dec-pmtiles", h.ID,
			"unrelated nodes whose Summary doesn't carry the token must not match")
	}
}

// TestListRanksTitleAboveSummaryAboveBody pins the per-field boost
// hierarchy that mirrors the prior heuristic weights (Title=3,
// Summary=2, Body=1) through to BM25 ranking: a node with the query
// token in Title outranks a node with it only in Summary, which
// outranks a node with it only in body/rationale.
func TestListRanksTitleAboveSummaryAboveBody(t *testing.T) {
	fs, root := listFixtureDisk(t, "decisions")
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-titleonly", spec.Decision{
		ID:        "dec-titleonly",
		Title:     "Adopt auth provider",
		Summary:   "Adopt a service provider.",
		Rationale: "Plain prose.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-summaryonly", spec.Decision{
		ID:        "dec-summaryonly",
		Title:     "Use Service X",
		Summary:   "Use Service X for auth flows.",
		Rationale: "Plain prose.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-bodyonly", spec.Decision{
		ID:        "dec-bodyonly",
		Title:     "Use Service Y",
		Summary:   "Use Service Y broadly.",
		Rationale: "Y is used for the auth pathway.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	r, err := RunList(fs, root, "auth", "")
	require.NoError(t, err)
	require.Len(t, r.Hits, 3)
	assert.Equal(t, "dec-titleonly", r.Hits[0].ID, "title hit must rank above summary hit")
	assert.Equal(t, "dec-summaryonly", r.Hits[1].ID, "summary hit must rank above body hit")
	assert.Equal(t, "dec-bodyonly", r.Hits[2].ID, "body hit must rank last")
}

// TestListSummaryAcrossKinds confirms every node kind's Summary
// participates in BM25 scoring. Easy regression vector if a new kind
// is added and someone forgets to wire Summary into its document.
func TestListSummaryAcrossKinds(t *testing.T) {
	fs, root := listFixtureDisk(t, "features", "strategies", "decisions", "bugs", "approaches")
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)

	// Each node has "widgets" ONLY in its Summary.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-a", spec.Feature{
		ID: "feat-a", Title: "F", Status: spec.FeatureStatusProposed,
		Summary:   "Render widgets on the dashboard.",
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-a", spec.Strategy{
		ID: "strat-a", Title: "S", Kind: spec.StrategyKindFoundational, Status: "proposed",
		Summary: "Frontend renders widgets via SSR.",
	}, "body"))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-a", spec.Decision{
		ID: "dec-a", Title: "D", Status: spec.DecisionStatusProposed,
		Summary:   "Use a widgets library for layout primitives.",
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/bugs/bug-a", spec.Bug{
		ID: "bug-a", Title: "B", FeatureID: "feat-a",
		Severity: spec.BugSeverityLow, Status: spec.BugStatusReported,
		Summary:   "Widgets fail to render on cold load.",
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-a.md", spec.Approach{
		ID: "app-a", Title: "A", ParentID: "feat-a",
		Summary: "Wire the widgets endpoint behind RLS.",
	}, "body"))

	r, err := RunList(fs, root, "widgets", "")
	require.NoError(t, err)
	ids := hitIDs(r.Hits)
	for _, want := range []string{"feat-a", "strat-a", "dec-a", "bug-a", "app-a"} {
		assert.Contains(t, ids, want, "kind matched only via Summary must surface in list")
	}
}
