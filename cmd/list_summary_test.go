package cmd

import (
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListMatchesAuthoredSummary covers the wiring from DJ-114's
// authored Summary field through to list's scoring. Before the wiring
// landed, a curated one-sentence "what is this node" line on a node
// whose title/body did NOT contain the query token was invisible to
// list — the gap the prereq's effort was supposed to close. This test
// pins that summary text now counts.
func TestListMatchesAuthoredSummary(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)

	// Decision whose title and rationale don't mention "authentication"
	// but whose authored Summary does. This is the synonym-bridge case
	// summaries are supposed to close.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-adopt-workos", spec.Decision{
		ID:        "dec-adopt-workos",
		Title:     "Adopt WorkOS",
		Summary:   "Adopt WorkOS for authentication, organization management, and audit logging.",
		Rationale: "WorkOS free tier and built-in RBAC.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	// Unrelated decision so we can verify the search isn't matching
	// everything indiscriminately.
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-pmtiles", spec.Decision{
		ID:        "dec-pmtiles",
		Title:     "Use PMTiles",
		Summary:   "Serve map tiles as PMTiles on Cloud Storage.",
		Rationale: "Serverless delivery.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	r, err := RunList(fs, "authentication", "")
	require.NoError(t, err)
	require.NotEmpty(t, r.Hits)
	assert.Equal(t, "dec-adopt-workos", r.Hits[0].ID,
		"authored Summary must surface the node even when title/body don't carry the query token")
	for _, h := range r.Hits {
		assert.NotEqual(t, "dec-pmtiles", h.ID,
			"unrelated nodes whose Summary doesn't carry the token must not match")
	}
}

// TestListSummaryWeightSitsBetweenIDAndTitle locks the relative weight
// of Summary against Title and ID so a future tweak to weightSummary
// can't silently flip the ranking. Concretely: a node with the query
// token in its Title ranks ABOVE a node with the token only in its
// Summary, and a node with the token in its Summary ranks ABOVE a
// node with the token only in body/rationale.
func TestListSummaryWeightSitsBetweenIDAndTitle(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)

	// Token "auth" in Title only (weightTitle=3 per match).
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-titleonly", spec.Decision{
		ID:    "dec-titleonly",
		Title: "Adopt auth provider",
		// Summary intentionally lacks the token.
		Summary:   "Adopt a service provider.",
		Rationale: "Plain prose.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	// Token "auth" in Summary only (weightSummary=2 per match).
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-summaryonly", spec.Decision{
		ID:        "dec-summaryonly",
		Title:     "Use Service X",
		Summary:   "Use Service X for auth flows.",
		Rationale: "Plain prose.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	// Token "auth" in Rationale (body) only (weightBody=1 per match).
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-bodyonly", spec.Decision{
		ID:        "dec-bodyonly",
		Title:     "Use Service Y",
		Summary:   "Use Service Y broadly.",
		Rationale: "Y is used for the auth pathway.",
		Status:    spec.DecisionStatusProposed,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	r, err := RunList(fs, "auth", "")
	require.NoError(t, err)
	require.Len(t, r.Hits, 3)
	assert.Equal(t, "dec-titleonly", r.Hits[0].ID, "title hit must rank above summary hit")
	assert.Equal(t, "dec-summaryonly", r.Hits[1].ID, "summary hit must rank above body hit")
	assert.Equal(t, "dec-bodyonly", r.Hits[2].ID, "body hit must rank last")
}

// TestListSummaryAcrossKinds confirms every node-kind scorer reads
// the Summary field, not just decisions. Easy regression vector if
// someone adds a new kind and forgets to thread Summary into the
// scoring function.
func TestListSummaryAcrossKinds(t *testing.T) {
	fs := specio.NewMemFS()
	for _, dir := range []string{"features", "strategies", "decisions", "bugs", "approaches"} {
		require.NoError(t, fs.MkdirAll(".borg/spec/"+dir, 0o755))
	}
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)

	// Each node has the token "widgets" ONLY in its Summary. If a
	// kind's scorer doesn't read Summary, that kind won't show up in
	// the hit list.
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

	r, err := RunList(fs, "widgets", "")
	require.NoError(t, err)
	ids := hitIDs(r.Hits)
	for _, want := range []string{"feat-a", "strat-a", "dec-a", "bug-a", "app-a"} {
		assert.Contains(t, ids, want, "kind matched only via Summary must surface in list")
	}
}
