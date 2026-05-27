// DJ-139 phase 8 — snapshot goal-layer surface. Asserts that
// `locutus status --full` reports goal-layer node counts, names
// features whose `.advances` is empty, and omits both sections
// gracefully when there's nothing to surface.

package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/render"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGoalLayerTestFS seeds a MemFS with the directory shape Phase 8
// expects. Phase 1's SpecStore persists goals + antigoals as JSON-
// only files; the snapshot test uses the same shape directly so the
// fixture matches on-disk reality.
func newGoalLayerTestFS(t *testing.T) specio.FS {
	t.Helper()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/goals", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/antigoals", 0o755))
	return fsys
}

// writeGoal persists a Goal to its canonical JSON-only path. Mirrors
// SpecStore.persistLocked's write shape so the loader sees the same
// bytes a council commit would land.
func writeGoal(t *testing.T, fsys specio.FS, g spec.Goal) {
	t.Helper()
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/goals/"+g.ID, g, ""))
}

func writeAntiGoal(t *testing.T, fsys specio.FS, ag spec.AntiGoal) {
	t.Helper()
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/antigoals/"+ag.ID, ag, ""))
}

// TestSnapshotIncludesGoalLayerCounts — when goals and anti-goals
// exist on disk, GatherSnapshotData reports the counts on
// SnapshotData and the markdown renders the new section.
func TestSnapshotIncludesGoalLayerCounts(t *testing.T) {
	fsys := newGoalLayerTestFS(t)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	writeGoal(t, fsys, spec.Goal{
		ID: "goal-multi-tenancy", Title: "Multi-tenancy", Body: "Support multiple orgs.",
		SourceClause: "Build SaaS", CreatedAt: now, UpdatedAt: now,
	})
	writeGoal(t, fsys, spec.Goal{
		ID: "goal-realtime", Title: "Realtime", Body: "Sub-second updates.",
		SourceClause: "Build SaaS", CreatedAt: now, UpdatedAt: now,
	})
	writeAntiGoal(t, fsys, spec.AntiGoal{
		ID: "agoal-fundraising", Title: "Fundraising tracking",
		Body: "We do not track fundraising.", SourceClause: "Build SaaS",
		CededTo: []string{"Carta"}, CreatedAt: now, UpdatedAt: now,
	})

	data, err := GatherSnapshotData(fsys, render.SnapshotFilters{})
	require.NoError(t, err)
	assert.Equal(t, 2, data.GoalCount)
	assert.Equal(t, 1, data.AntiGoalCount)

	md := render.SnapshotMarkdown(data)
	assert.Contains(t, md, "## Goal layer")
	assert.Contains(t, md, "**Goals:** 2")
	assert.Contains(t, md, "**Anti-goals:** 1")
}

// TestSnapshotListsFeaturesWithoutGoalAnchors — features whose
// .advances is empty are surfaced under Validation. Order is
// alphabetical by id.
func TestSnapshotListsFeaturesWithoutGoalAnchors(t *testing.T) {
	fsys := newGoalLayerTestFS(t)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	writeGoal(t, fsys, spec.Goal{
		ID: "goal-realtime", Title: "Realtime", Body: "Sub-second.",
		SourceClause: "Realtime SaaS", CreatedAt: now, UpdatedAt: now,
	})

	// feat-anchored has .advances populated — should NOT appear.
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/features/feat-anchored", spec.Feature{
		ID: "feat-anchored", Title: "Anchored", Status: spec.FeatureStatusProposed,
		Decisions: []string{"dec-x"}, Advances: []string{"goal-realtime"},
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	// feat-zulu and feat-alpha both lack .advances. Inserted z-then-a
	// to verify the snapshot sorts.
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/features/feat-zulu", spec.Feature{
		ID: "feat-zulu", Title: "Zulu", Status: spec.FeatureStatusProposed,
		Decisions: []string{"dec-x"}, CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/features/feat-alpha", spec.Feature{
		ID: "feat-alpha", Title: "Alpha", Status: spec.FeatureStatusProposed,
		Decisions: []string{"dec-x"}, CreatedAt: now, UpdatedAt: now,
	}, ""))

	data, err := GatherSnapshotData(fsys, render.SnapshotFilters{})
	require.NoError(t, err)
	assert.Equal(t, []string{"feat-alpha", "feat-zulu"}, data.FeaturesWithoutGoalAnchors,
		"sorted alphabetically; feat-anchored excluded")

	md := render.SnapshotMarkdown(data)
	assert.Contains(t, md, "Features without goal anchors")
	// Isolate the at-risk sub-section and assert membership against
	// just that span — feat-anchored legitimately appears in the
	// "## Features" section above; the surface that excludes it is
	// the at-risk list under Validation.
	idx := strings.Index(md, "Features without goal anchors")
	require.NotEqual(t, -1, idx, "section header must be present")
	atRiskSection := md[idx:]
	assert.Contains(t, atRiskSection, "`feat-alpha`")
	assert.Contains(t, atRiskSection, "`feat-zulu`")
	assert.NotContains(t, atRiskSection, "`feat-anchored`",
		"features with .advances populated must not appear in the at-risk list")
}

// TestSnapshotAbsentWhenNoFeaturesWithoutAnchors — when every feature
// has .advances populated (or there are no features at all), the
// at-risk section is omitted from the markdown and the field is empty
// in SnapshotData.
func TestSnapshotAbsentWhenNoFeaturesWithoutAnchors(t *testing.T) {
	fsys := newGoalLayerTestFS(t)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	writeGoal(t, fsys, spec.Goal{
		ID: "goal-realtime", Title: "Realtime", Body: "Sub-second.",
		SourceClause: "Realtime SaaS", CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/features/feat-anchored", spec.Feature{
		ID: "feat-anchored", Title: "Anchored", Status: spec.FeatureStatusProposed,
		Decisions: []string{"dec-x"}, Advances: []string{"goal-realtime"},
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	data, err := GatherSnapshotData(fsys, render.SnapshotFilters{})
	require.NoError(t, err)
	assert.Empty(t, data.FeaturesWithoutGoalAnchors)

	md := render.SnapshotMarkdown(data)
	assert.NotContains(t, md, "Features without goal anchors",
		"no at-risk section when every feature advances something")
}

// TestSnapshotMarkdownRendersGoalLayerSection — focuses on the
// markdown rendering itself: header, counts line, and exclusion of
// the section when both counts are zero.
func TestSnapshotMarkdownRendersGoalLayerSection(t *testing.T) {
	t.Run("section visible when counts non-zero", func(t *testing.T) {
		data := render.SnapshotData{GoalCount: 3, AntiGoalCount: 2}
		md := render.SnapshotMarkdown(data)
		assert.Contains(t, md, "## Goal layer")
		// New format includes anchored/unanchored split (DJ-141).
		assert.Contains(t, md, "**Goals:** 3")
		assert.Contains(t, md, "**Anti-goals:** 2")
	})

	t.Run("section omitted when both counts zero", func(t *testing.T) {
		data := render.SnapshotData{GoalCount: 0, AntiGoalCount: 0}
		md := render.SnapshotMarkdown(data)
		assert.NotContains(t, md, "## Goal layer",
			"empty goal layer section should not render at all")
	})

	t.Run("section visible when only anti-goals exist", func(t *testing.T) {
		data := render.SnapshotData{GoalCount: 0, AntiGoalCount: 1}
		md := render.SnapshotMarkdown(data)
		assert.Contains(t, md, "## Goal layer",
			"section renders when either count is non-zero")
		// New format includes anchored/unanchored split (DJ-141).
		assert.Contains(t, md, "**Goals:** 0")
		assert.Contains(t, md, "**Anti-goals:** 1")
	})
}
