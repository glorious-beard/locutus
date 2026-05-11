package agent

import (
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildSpecManifest_PrefersAuthoredSummary verifies that when a
// node has an authored Summary, BuildSpecManifest returns it verbatim
// rather than the derived truncation. This is the post-prereq normal
// path: every node carries an authored one-liner and the manifest
// returns those.
func TestBuildSpecManifest_PrefersAuthoredSummary(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	dec := spec.Decision{
		ID:        "dec-pg",
		Title:     "Postgres",
		Status:    spec.DecisionStatusActive,
		Summary:   "Adopt Postgres for the OLTP store.",
		Rationale: "Long-form rationale about replication, tooling, and operational maturity that would otherwise be truncated as the summary.",
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-pg", dec, "body"))

	m := BuildSpecManifest(fs)
	require.Len(t, m.Decisions, 1)
	assert.Equal(t, "Adopt Postgres for the OLTP store.", m.Decisions[0].Summary,
		"authored Summary wins over derived truncation of Rationale")
}

// TestBuildSpecManifest_FallsBackToTruncation verifies that nodes
// without an authored Summary still produce a non-empty entry — the
// fallback path keeps the manifest usable on legacy projects the
// prereq hasn't yet visited.
func TestBuildSpecManifest_FallsBackToTruncation(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	dec := spec.Decision{
		ID:        "dec-legacy",
		Title:     "Legacy",
		Status:    spec.DecisionStatusActive,
		Rationale: "Legacy decision that predates the Summary field; the manifest derives a truncation from this prose.",
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-legacy", dec, "body"))

	m := BuildSpecManifest(fs)
	require.Len(t, m.Decisions, 1)
	assert.NotEmpty(t, m.Decisions[0].Summary, "fallback truncation must produce a non-empty entry")
	assert.Contains(t, m.Decisions[0].Summary, "Legacy decision",
		"fallback should derive from the Rationale field")
}

// TestBuildSpecManifest_AllKindsAuthoredSummary covers every spec kind
// the manifest exposes — Feature, Strategy, Decision, Bug, Approach —
// to confirm the authored Summary path threads through each.
func TestBuildSpecManifest_AllKindsAuthoredSummary(t *testing.T) {
	fs := specio.NewMemFS()
	for _, dir := range []string{"features", "strategies", "decisions", "bugs", "approaches"} {
		require.NoError(t, fs.MkdirAll(".borg/spec/"+dir, 0o755))
	}

	feat := spec.Feature{ID: "feat-a", Title: "F", Status: spec.FeatureStatusActive, Summary: "Feature summary."}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-a", feat, ""))

	strat := spec.Strategy{ID: "strat-a", Title: "S", Kind: spec.StrategyKindFoundational, Status: "active", Summary: "Strategy summary."}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-a", strat, ""))

	dec := spec.Decision{ID: "dec-a", Title: "D", Status: spec.DecisionStatusActive, Summary: "Decision summary."}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-a", dec, ""))

	bug := spec.Bug{ID: "bug-a", Title: "B", FeatureID: "feat-a", Severity: spec.BugSeverityLow, Status: spec.BugStatusReported, Summary: "Bug summary."}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/bugs/bug-a", bug, ""))

	app := spec.Approach{ID: "app-a", Title: "A", ParentID: "feat-a", Summary: "Approach summary."}
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-a.md", app, "body"))

	m := BuildSpecManifest(fs)
	require.Len(t, m.Features, 1)
	require.Len(t, m.Strategies, 1)
	require.Len(t, m.Decisions, 1)
	require.Len(t, m.Bugs, 1)
	require.Len(t, m.Approaches, 1)

	assert.Equal(t, "Feature summary.", m.Features[0].Summary)
	assert.Equal(t, "Strategy summary.", m.Strategies[0].Summary)
	assert.Equal(t, "Decision summary.", m.Decisions[0].Summary)
	assert.Equal(t, "Bug summary.", m.Bugs[0].Summary)
	assert.Equal(t, "Approach summary.", m.Approaches[0].Summary)
}

// TestBuildSpecManifest_WhitespaceOnlySummaryFallsBack confirms that
// HasSummary's whitespace trim is honored — a Summary of just spaces
// is treated as absent so the fallback path engages.
func TestBuildSpecManifest_WhitespaceOnlySummaryFallsBack(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))

	dec := spec.Decision{
		ID:        "dec-ws",
		Title:     "Whitespace",
		Status:    spec.DecisionStatusActive,
		Summary:   "   \t\n  ",
		Rationale: "Fallback prose used when Summary is whitespace-only.",
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-ws", dec, "body"))

	m := BuildSpecManifest(fs)
	require.Len(t, m.Decisions, 1)
	assert.Contains(t, m.Decisions[0].Summary, "Fallback prose",
		"whitespace-only Summary should be treated as absent and trigger fallback")
}
