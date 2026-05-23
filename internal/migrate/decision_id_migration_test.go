package migrate

import (
	"path"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeMigrationFS prepares a fresh MemFS with the .borg scaffold dirs
// in place and an empty manifest so spec.LoadSpec succeeds.
func makeMigrationFS(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))
	return fs
}

func writeDecision(t *testing.T, fs specio.FS, id string, axes []string, body string) {
	t.Helper()
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	require.NoError(t, specio.SavePair(fs, path.Join(".borg/spec/decisions", id), spec.Decision{
		ID: id, Title: "decision " + id, Status: spec.DecisionStatusProposed,
		Confidence: 0.8, Rationale: "rationale",
		Axes: axes, SurfacedBy: []string{"feat-x"},
		CreatedAt: now, UpdatedAt: now,
	}, body))
}

func writeFeature(t *testing.T, fs specio.FS, id string, decisions []string) {
	t.Helper()
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	require.NoError(t, specio.SavePair(fs, path.Join(".borg/spec/features", id), spec.Feature{
		ID: id, Title: "feature " + id, Status: spec.FeatureStatusProposed,
		Decisions: decisions,
		CreatedAt: now, UpdatedAt: now,
	}, ""))
}

func writeStrategy(t *testing.T, fs specio.FS, id string, decisions []string) {
	t.Helper()
	require.NoError(t, specio.SavePair(fs, path.Join(".borg/spec/strategies", id), spec.Strategy{
		ID: id, Title: "strategy " + id, Status: "proposed",
		Kind:      spec.StrategyKindFoundational,
		Decisions: decisions,
	}, "body"))
}

func writeApproach(t *testing.T, fs specio.FS, id, parentID string, decisions []string) {
	t.Helper()
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	require.NoError(t, specio.SaveMarkdown(fs, path.Join(".borg/spec/approaches", id+".md"), spec.Approach{
		ID: id, Title: "approach " + id, ParentID: parentID,
		Decisions: decisions,
		CreatedAt: now, UpdatedAt: now,
	}, "approach body"))
}

func loadDecision(t *testing.T, fs specio.FS, id string) spec.Decision {
	t.Helper()
	d, _, err := specio.LoadPair[spec.Decision](fs, path.Join(".borg/spec/decisions", id))
	require.NoError(t, err)
	return d
}

func decisionExists(t *testing.T, fs specio.FS, id string) bool {
	t.Helper()
	_, err := fs.Stat(path.Join(".borg/spec/decisions", id+".json"))
	return err == nil
}

// TestDecisionIDMigration_RenamesByPrimaryAxis is the canonical happy
// path: a single decision `dec-supabase-postgresql` with axes
// ["database-and-spatial-storage"] is renamed to
// `dec-database-and-spatial-storage`; the file moves, the markdown
// sidecar follows, and the JSON's `id` carries the new value.
func TestDecisionIDMigration_RenamesByPrimaryAxis(t *testing.T) {
	fs := makeMigrationFS(t)
	writeDecision(t, fs, "dec-supabase-postgresql", []string{"database-and-spatial-storage"}, "body-with-content")

	historian := history.NewHistorian(fs, ".borg/history")
	res, err := MigrateDecisionIDs(fs, historian)
	require.NoError(t, err)
	require.Len(t, res.Renamed, 1, "exactly one rename expected")
	assert.Equal(t, "dec-supabase-postgresql", res.Renamed[0].OldID)
	assert.Equal(t, "dec-database-and-spatial-storage", res.Renamed[0].NewID)

	// New path has the rewritten decision; old path is gone.
	assert.True(t, decisionExists(t, fs, "dec-database-and-spatial-storage"), "new id's JSON sidecar must exist")
	assert.False(t, decisionExists(t, fs, "dec-supabase-postgresql"), "old id's JSON sidecar must be removed")
	d := loadDecision(t, fs, "dec-database-and-spatial-storage")
	assert.Equal(t, "dec-database-and-spatial-storage", d.ID, "JSON contents' id must be the new id")
}

// TestDecisionIDMigration_RewritesIncomingRefs covers the cascade: a
// feature, strategy, sibling decision (via influenced_by), and an
// approach all hold a reference to the old id, and all should be
// rewritten to the new id.
func TestDecisionIDMigration_RewritesIncomingRefs(t *testing.T) {
	fs := makeMigrationFS(t)
	writeDecision(t, fs, "dec-supabase-postgresql", []string{"database-and-spatial-storage"}, "")
	// Sibling decision that lists dec-supabase-postgresql in its
	// influenced_by. The sibling is itself axis-shaped already so the
	// migration leaves its id alone but rewrites its influenced_by.
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-cache-engine", spec.Decision{
		ID: "dec-cache-engine", Title: "Cache engine", Status: spec.DecisionStatusProposed,
		Confidence: 0.8, Rationale: "r",
		Axes:         []string{"cache-engine"},
		SurfacedBy:   []string{"feat-x"},
		InfluencedBy: []string{"dec-supabase-postgresql"},
		CreatedAt:    now, UpdatedAt: now,
	}, ""))
	writeFeature(t, fs, "feat-alpha", []string{"dec-supabase-postgresql"})
	writeStrategy(t, fs, "strat-foo", []string{"dec-supabase-postgresql"})
	writeApproach(t, fs, "app-alpha", "feat-alpha", []string{"dec-supabase-postgresql"})

	res, err := MigrateDecisionIDs(fs, history.NewHistorian(fs, ".borg/history"))
	require.NoError(t, err)
	require.Len(t, res.Renamed, 1)
	r := res.Renamed[0]
	assert.ElementsMatch(t, []string{"feat-alpha"}, r.FeaturesRewritten)
	assert.ElementsMatch(t, []string{"strat-foo"}, r.StrategiesRewritten)
	assert.ElementsMatch(t, []string{"dec-cache-engine"}, r.DecisionsInfluencedBy)
	assert.ElementsMatch(t, []string{"app-alpha"}, r.ApproachesRewritten)

	// Re-read everything off disk; every reference must carry the new id.
	feat, _, err := specio.LoadPair[spec.Feature](fs, ".borg/spec/features/feat-alpha")
	require.NoError(t, err)
	assert.Equal(t, []string{"dec-database-and-spatial-storage"}, feat.Decisions)

	st, _, err := specio.LoadPair[spec.Strategy](fs, ".borg/spec/strategies/strat-foo")
	require.NoError(t, err)
	assert.Equal(t, []string{"dec-database-and-spatial-storage"}, st.Decisions)

	sib := loadDecision(t, fs, "dec-cache-engine")
	assert.Equal(t, []string{"dec-database-and-spatial-storage"}, sib.InfluencedBy)

	app, _, err := specio.LoadMarkdown[spec.Approach](fs, ".borg/spec/approaches/app-alpha.md")
	require.NoError(t, err)
	assert.Equal(t, []string{"dec-database-and-spatial-storage"}, app.Decisions)
}

// TestDecisionIDMigration_Idempotent confirms a second run on a
// migrated graph performs no renames and emits no history events.
func TestDecisionIDMigration_Idempotent(t *testing.T) {
	fs := makeMigrationFS(t)
	writeDecision(t, fs, "dec-supabase-postgresql", []string{"database-and-spatial-storage"}, "")
	writeFeature(t, fs, "feat-alpha", []string{"dec-supabase-postgresql"})

	hist := history.NewHistorian(fs, ".borg/history")
	res1, err := MigrateDecisionIDs(fs, hist)
	require.NoError(t, err)
	require.Len(t, res1.Renamed, 1)
	require.Len(t, res1.Events, 1)

	// Second pass: every decision is now axis-shaped; nothing to do.
	res2, err := MigrateDecisionIDs(fs, hist)
	require.NoError(t, err)
	assert.Empty(t, res2.Renamed, "idempotent: second pass produces no renames")
	assert.Empty(t, res2.Events, "idempotent: second pass writes no events")
	// Every existing decision shows up in Skipped with the already-axis-
	// shaped reason.
	require.Len(t, res2.Skipped, 1)
	assert.Equal(t, "dec-database-and-spatial-storage", res2.Skipped[0].ID)
	assert.Equal(t, "already-axis-shaped", res2.Skipped[0].Reason)
}

// TestDecisionIDMigration_HandlesCompositeAxes covers the composite-
// axes case: a decision with multiple axes uses the primary axis as
// the id; the rename is flagged Composite so the operator sees the
// secondary axes weren't reflected in the id.
func TestDecisionIDMigration_HandlesCompositeAxes(t *testing.T) {
	fs := makeMigrationFS(t)
	writeDecision(t, fs, "dec-fargate-compute-platform",
		[]string{"compute-platform", "aws-region"}, "")

	res, err := MigrateDecisionIDs(fs, history.NewHistorian(fs, ".borg/history"))
	require.NoError(t, err)
	require.Len(t, res.Renamed, 1)
	r := res.Renamed[0]
	assert.Equal(t, "dec-compute-platform", r.NewID)
	assert.True(t, r.Composite, "composite-axes rename must set Composite=true")
	assert.Equal(t, []string{"compute-platform", "aws-region"}, r.Axes,
		"all axes are preserved on the rename record so the operator can audit which were dropped from the id")

	// The persisted decision keeps both axes — only the id was rewritten.
	d := loadDecision(t, fs, "dec-compute-platform")
	assert.Equal(t, []string{"compute-platform", "aws-region"}, d.Axes)
}

// TestDecisionIDMigration_SkipsEmptyAxes leaves decisions with no axes
// alone (legacy pre-DJ-124 state) and surfaces a skip entry naming the
// reason so the operator can hand-fix or accept the stale id.
func TestDecisionIDMigration_SkipsEmptyAxes(t *testing.T) {
	fs := makeMigrationFS(t)
	writeDecision(t, fs, "dec-legacy-thing", nil, "")
	writeDecision(t, fs, "dec-postgres-oltp-store", []string{"oltp-store"}, "")

	res, err := MigrateDecisionIDs(fs, history.NewHistorian(fs, ".borg/history"))
	require.NoError(t, err)
	require.Len(t, res.Renamed, 1, "the axis-carrying decision is renamed; the empty-axes one is skipped")
	assert.Equal(t, "dec-oltp-store", res.Renamed[0].NewID)
	require.Len(t, res.Skipped, 1)
	assert.Equal(t, "dec-legacy-thing", res.Skipped[0].ID)
	assert.Equal(t, "empty-axes", res.Skipped[0].Reason)

	// The legacy decision is untouched on disk.
	assert.True(t, decisionExists(t, fs, "dec-legacy-thing"), "empty-axes decision is not removed")
}

// TestDecisionIDMigration_DetectsConflicts surfaces a hard error
// before any rename when two source decisions would map to the same
// target id. Pre-flight, atomic: a conflict aborts the entire pass so
// the operator never sees half-migrated state.
func TestDecisionIDMigration_DetectsConflicts(t *testing.T) {
	fs := makeMigrationFS(t)
	writeDecision(t, fs, "dec-fargate-compute-platform",
		[]string{"compute-platform"}, "")
	writeDecision(t, fs, "dec-ecs-compute-platform",
		[]string{"compute-platform"}, "")

	res, err := MigrateDecisionIDs(fs, history.NewHistorian(fs, ".borg/history"))
	require.Error(t, err, "two decisions mapping to the same target id must abort the pass")
	assert.Contains(t, err.Error(), "dec-compute-platform")
	assert.Contains(t, err.Error(), "dec-ecs-compute-platform")
	assert.Contains(t, err.Error(), "dec-fargate-compute-platform")
	assert.Empty(t, res.Renamed, "no rename should land when the pass aborts")
	// Both source files remain on disk.
	assert.True(t, decisionExists(t, fs, "dec-fargate-compute-platform"))
	assert.True(t, decisionExists(t, fs, "dec-ecs-compute-platform"))
}

// TestDecisionIDMigration_WritesHistoryEvent locks in the per-decision
// history event: kind=decision_id_migration, target_id=old, new_value=
// new, rationale enumerates the rewritten refs.
func TestDecisionIDMigration_WritesHistoryEvent(t *testing.T) {
	fs := makeMigrationFS(t)
	writeDecision(t, fs, "dec-supabase-postgresql", []string{"database-and-spatial-storage"}, "")
	writeFeature(t, fs, "feat-alpha", []string{"dec-supabase-postgresql"})

	historian := history.NewHistorian(fs, ".borg/history")
	res, err := MigrateDecisionIDs(fs, historian)
	require.NoError(t, err)
	require.Len(t, res.Events, 1)

	evt := res.Events[0]
	assert.Equal(t, EventKindDecisionIDMigration, evt.Kind)
	assert.Equal(t, "dec-supabase-postgresql", evt.TargetID)
	assert.Equal(t, "dec-database-and-spatial-storage", evt.NewValue)
	assert.Contains(t, evt.Rationale, "feat-alpha", "rationale enumerates the rewritten refs")
	assert.Contains(t, evt.Rationale, "DJ-133", "rationale names the governing DJ")

	// History event is durable on disk.
	events, err := historian.Events()
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, EventKindDecisionIDMigration, events[0].Kind)
}
