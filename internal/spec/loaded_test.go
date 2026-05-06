package spec

import (
	"testing"
	"time"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture builds a small but representative spec graph in a MemFS:
//   - 2 features, one referencing 2 decisions, one referencing 1
//   - 2 strategies (foundational + quality), one with InfluencedBy
//   - 3 decisions (one shared between feature and strategy; one
//     orphan; one influencing another)
//   - 1 approach attached to one feature
//   - 1 dangling decision reference (feature points at dec-missing)
func fixture(t *testing.T) *specio.MemFS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))

	require.NoError(t, fs.WriteFile(".borg/GOALS.md", []byte("# Goals\n\nBuild a thing.\n"), 0o644))
	require.NoError(t, fs.WriteFile(".borg/manifest.json", []byte(`{"project_name":"fixture","version":"1"}`), 0o644))

	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-alpha", Feature{
		ID: "feat-alpha", Title: "Alpha", Status: FeatureStatusProposed,
		Description: "alpha desc",
		Decisions:   []string{"dec-shared", "dec-only-feature", "dec-missing"},
		Approaches:  []string{"app-alpha"},
		CreatedAt:   now, UpdatedAt: now,
	}, "alpha body prose"))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-beta", Feature{
		ID: "feat-beta", Title: "Beta", Status: FeatureStatusProposed,
		Decisions: []string{"dec-influencer"},
		CreatedAt: now, UpdatedAt: now,
	}, "beta body prose"))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-foundation", Strategy{
		ID: "strat-foundation", Title: "Foundation", Kind: StrategyKindFoundational,
		Status: "proposed", Decisions: []string{"dec-shared"},
	}, "foundation strategy body"))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-quality", Strategy{
		ID: "strat-quality", Title: "Quality", Kind: StrategyKindQuality,
		Status: "proposed", InfluencedBy: []string{"strat-foundation"},
	}, "quality strategy body"))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-shared", Decision{
		ID: "dec-shared", Title: "Shared decision", Status: DecisionStatusProposed,
		Confidence: 0.9, Rationale: "shared rationale",
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-only-feature", Decision{
		ID: "dec-only-feature", Title: "Feature-only decision", Status: DecisionStatusProposed,
		Confidence: 0.7, Rationale: "feature-only rationale",
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-influencer", Decision{
		ID: "dec-influencer", Title: "Influencer", Status: DecisionStatusProposed,
		Confidence: 0.8, Rationale: "influencer rationale",
		InfluencedBy: []string{"dec-shared"},
		CreatedAt:    now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-alpha.md", Approach{
		ID: "app-alpha", Title: "Alpha approach", ParentID: "feat-alpha",
		CreatedAt: now, UpdatedAt: now,
	}, "approach body"))

	return fs
}

func TestLoadSpec_LoadsAllNodes(t *testing.T) {
	fs := fixture(t)
	l, err := LoadSpec(fs)
	require.NoError(t, err)

	assert.Len(t, l.Features, 2)
	assert.Len(t, l.Strategies, 2)
	assert.Len(t, l.Decisions, 3)
	assert.Len(t, l.Approaches, 1)
	assert.Equal(t, "fixture", l.Manifest.ProjectName)
	assert.Contains(t, l.GoalsBody, "Build a thing")
}

func TestLoadSpec_InverseIndexes(t *testing.T) {
	fs := fixture(t)
	l, err := LoadSpec(fs)
	require.NoError(t, err)

	// dec-shared is referenced by feat-alpha (feature) AND strat-foundation (strategy).
	assert.Equal(t, []string{"feat-alpha"}, l.FeaturesReferencingDecision("dec-shared"))
	assert.Equal(t, []string{"strat-foundation"}, l.StrategiesReferencingDecision("dec-shared"))

	// dec-influencer was listed by dec-influencer.influenced_by → dec-shared.
	// So dec-shared influences dec-influencer.
	assert.Equal(t, []string{"dec-influencer"}, l.DecisionsInfluencedByDecision("dec-shared"))

	// strat-quality lists strat-foundation in influenced_by.
	assert.Equal(t, []string{"strat-quality"}, l.StrategiesInfluencedByStrategy("strat-foundation"))

	// app-alpha is referenced by feat-alpha.
	assert.Equal(t, []string{"feat-alpha"}, l.FeaturesReferencingApproach("app-alpha"))
}

func TestLoadSpec_DanglingRefs(t *testing.T) {
	fs := fixture(t)
	l, err := LoadSpec(fs)
	require.NoError(t, err)

	// feat-alpha lists dec-missing which doesn't exist.
	require.Len(t, l.DanglingRefs, 1)
	dr := l.DanglingRefs[0]
	assert.Equal(t, KindFeature, dr.FromKind)
	assert.Equal(t, "feat-alpha", dr.FromID)
	assert.Equal(t, "decisions", dr.Field)
	assert.Equal(t, "dec-missing", dr.TargetID)
	assert.Equal(t, KindDecision, dr.TargetKind)
}

func TestLoadSpec_Orphans(t *testing.T) {
	fs := fixture(t)
	l, err := LoadSpec(fs)
	require.NoError(t, err)

	// No decisions are orphans in this fixture — each is referenced
	// by at least a feature, strategy, or another decision via
	// influenced_by. Approaches: app-alpha is referenced by
	// feat-alpha. So no orphans.
	assert.Empty(t, l.Orphans, "fixture has no orphans")
}

func TestDeriveStages_DraftedWhenNoApproachInFeature(t *testing.T) {
	fs := fixture(t)
	l, err := LoadSpec(fs)
	require.NoError(t, err)

	stages := DeriveStages(l, fs)

	// feat-alpha lists app-alpha but no workstream is in flight →
	// planned.
	assert.Equal(t, StagePlanned, stages["feat-alpha"])

	// feat-beta has no approaches → drafted.
	assert.Equal(t, StageDrafted, stages["feat-beta"])

	// dec-shared inherits from its most-advanced referrer:
	// feat-alpha (planned) > strat-foundation (drafted).
	assert.Equal(t, StagePlanned, stages["dec-shared"])

	// dec-only-feature is referenced only by feat-alpha (planned).
	assert.Equal(t, StagePlanned, stages["dec-only-feature"])

	// dec-influencer is referenced only by feat-beta (drafted).
	assert.Equal(t, StageDrafted, stages["dec-influencer"])
}

func TestDeriveStages_ImplementingWhenWorkstreamExists(t *testing.T) {
	fs := fixture(t)

	// Drop a workstream YAML referencing app-alpha. Triggers
	// "implementing" stage for feat-alpha and decisions it shares.
	require.NoError(t, fs.MkdirAll(".locutus/workstreams/plan-1", 0o755))
	require.NoError(t, fs.WriteFile(".locutus/workstreams/plan-1/ws-1.yaml",
		[]byte(`workstream_id: ws-1
plan_id: plan-1
approach_ids:
  - app-alpha
`), 0o644))

	l, err := LoadSpec(fs)
	require.NoError(t, err)
	stages := DeriveStages(l, fs)

	assert.Equal(t, StageImplementing, stages["feat-alpha"], "feat-alpha promotes to implementing")
	assert.Equal(t, StageImplementing, stages["dec-shared"], "dec-shared inherits implementing from feat-alpha")
	assert.Equal(t, StageImplementing, stages["dec-only-feature"])

	// feat-beta doesn't share an approach with the workstream.
	assert.Equal(t, StageDrafted, stages["feat-beta"])
}

func TestCountStages_TalliesCorrectly(t *testing.T) {
	stages := StageMap{
		"a": StageDrafted,
		"b": StageDrafted,
		"c": StagePlanned,
		"d": StageImplementing,
	}
	d := CountStages(stages)
	assert.Equal(t, 2, d.Drafted)
	assert.Equal(t, 1, d.Planned)
	assert.Equal(t, 1, d.Implementing)
	assert.Equal(t, 0, d.Done)
}
