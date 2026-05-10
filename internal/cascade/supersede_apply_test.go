package cascade

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

// fixtureSupersedeFS returns the same graph as fixtureSupersede but
// also exposes the underlying filesystem so apply tests can read
// post-write state.
func fixtureSupersedeFS(t *testing.T) (specio.FS, *spec.Loaded) {
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

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-target", spec.Decision{
		ID: "dec-target", Title: "Target decision", Status: spec.DecisionStatusProposed,
		Confidence: 0.9, Rationale: "to be superseded",
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-other", spec.Decision{
		ID: "dec-other", Title: "Other decision", Status: spec.DecisionStatusProposed,
		Confidence: 0.9, Rationale: "downstream",
		InfluencedBy: []string{"dec-target"},
		CreatedAt:    now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-alpha", spec.Feature{
		ID: "feat-alpha", Title: "Alpha feature", Status: spec.FeatureStatusProposed,
		Decisions: []string{"dec-target", "dec-other"},
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-foo", spec.Strategy{
		ID: "strat-foo", Title: "Foo strategy",
		Kind: spec.StrategyKindFoundational, Status: "proposed",
		Decisions: []string{"dec-target"},
	}, ""))

	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-alpha.md", spec.Approach{
		ID: "app-alpha", Title: "Alpha approach", ParentID: "feat-alpha",
		Decisions: []string{"dec-target", "dec-other"},
		CreatedAt: now, UpdatedAt: now,
	}, "alpha body"))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-foo.md", spec.Approach{
		ID: "app-foo", Title: "Foo approach", ParentID: "strat-foo",
		Decisions: []string{"dec-target"},
		CreatedAt: now, UpdatedAt: now,
	}, "foo body"))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/bugs/bug-1", spec.Bug{
		ID: "bug-1", Title: "First bug", FeatureID: "feat-alpha",
		Severity: spec.BugSeverityHigh, Status: spec.BugStatusReported,
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	loaded, err := spec.LoadSpec(fs)
	require.NoError(t, err)
	return fs, loaded
}

func loadDecision(t *testing.T, fs specio.FS, id string) spec.Decision {
	t.Helper()
	dec, _, err := specio.LoadPair[spec.Decision](fs, path.Join(".borg/spec/decisions", id))
	require.NoError(t, err)
	return dec
}

func loadFeature(t *testing.T, fs specio.FS, id string) spec.Feature {
	t.Helper()
	feat, _, err := specio.LoadPair[spec.Feature](fs, path.Join(".borg/spec/features", id))
	require.NoError(t, err)
	return feat
}

func loadStrategy(t *testing.T, fs specio.FS, id string) spec.Strategy {
	t.Helper()
	s, _, err := specio.LoadPair[spec.Strategy](fs, path.Join(".borg/spec/strategies", id))
	require.NoError(t, err)
	return s
}

func loadBug(t *testing.T, fs specio.FS, id string) spec.Bug {
	t.Helper()
	b, _, err := specio.LoadPair[spec.Bug](fs, path.Join(".borg/spec/bugs", id))
	require.NoError(t, err)
	return b
}

func loadApproach(t *testing.T, fs specio.FS, id string) spec.Approach {
	t.Helper()
	a, _, err := specio.LoadMarkdown[spec.Approach](fs, path.Join(".borg/spec/approaches", id+".md"))
	require.NoError(t, err)
	return a
}

func fileExists(t *testing.T, fs specio.FS, p string) bool {
	t.Helper()
	_, err := fs.ReadFile(p)
	return err == nil
}

// --- Decision apply ---

func TestApplySupersede_Decision_NewSlug(t *testing.T) {
	fs, loaded := fixtureSupersedeFS(t)
	plan, err := ComputeSupersedePlan(loaded, "dec-target", "dec-replacement", "evt-001")
	require.NoError(t, err)

	now := time.Date(2026, 5, 9, 13, 0, 0, 0, time.UTC)
	newDec := spec.Decision{
		ID: "dec-replacement", Title: "Replacement decision",
		Status: spec.DecisionStatusProposed, Confidence: 0.85,
		Rationale: "replaces dec-target with new alternatives",
		Alternatives: []spec.Alternative{
			{Name: "Old approach", Rationale: "was dec-target", RejectedBecause: "broke down"},
		},
		CreatedAt: now, UpdatedAt: now,
	}

	historian := history.NewHistorian(fs, ".borg/history")
	evt, err := ApplySupersedeDecision(fs, plan, newDec, "Address: WorkOS was never evaluated", "", historian)
	require.NoError(t, err)
	require.NotNil(t, evt)

	// Old node deleted, new node written.
	assert.False(t, fileExists(t, fs, ".borg/spec/decisions/dec-target.json"))
	assert.False(t, fileExists(t, fs, ".borg/spec/decisions/dec-target.md"))
	assert.True(t, fileExists(t, fs, ".borg/spec/decisions/dec-replacement.json"))

	// Feature.Decisions[] rewritten.
	feat := loadFeature(t, fs, "feat-alpha")
	assert.Equal(t, []string{"dec-replacement", "dec-other"}, feat.Decisions,
		"feature.Decisions[] rewritten in original order with old→new substitution")

	// Strategy.Decisions[] rewritten.
	strat := loadStrategy(t, fs, "strat-foo")
	assert.Equal(t, []string{"dec-replacement"}, strat.Decisions)

	// Decision.InfluencedBy[] rewritten.
	other := loadDecision(t, fs, "dec-other")
	assert.Equal(t, []string{"dec-replacement"}, other.InfluencedBy)

	// Approach invalidated + Decisions[] rewritten.
	appAlpha := loadApproach(t, fs, "app-alpha")
	assert.Equal(t, "evt-001", appAlpha.InvalidatedByEventID)
	assert.True(t, appAlpha.IsInvalidated())
	assert.Equal(t, []string{"dec-replacement", "dec-other"}, appAlpha.Decisions,
		"invalidated approach must have its Decisions[] entry rewritten")

	appFoo := loadApproach(t, fs, "app-foo")
	assert.Equal(t, "evt-001", appFoo.InvalidatedByEventID)
	assert.Equal(t, []string{"dec-replacement"}, appFoo.Decisions)

	// History event.
	assert.Equal(t, "evt-001", evt.ID)
	assert.Equal(t, history.EventKindNodeSuperseded, evt.Kind)
	assert.Equal(t, "dec-target", evt.TargetID)
	assert.Equal(t, "dec-replacement", evt.NewValue)
	require.NotNil(t, evt.Supersede)
	assert.Equal(t, "decision", evt.Supersede.NodeKind)
	assert.False(t, evt.Supersede.InPlace)
	assert.Equal(t, "Address: WorkOS was never evaluated", evt.Supersede.Motivation)
	assert.ElementsMatch(t, []string{"feat-alpha"}, evt.Supersede.FeaturesDecisionsRewritten)
	assert.ElementsMatch(t, []string{"strat-foo"}, evt.Supersede.StrategiesDecisionsRewritten)
	assert.ElementsMatch(t, []string{"dec-other"}, evt.Supersede.DecisionsInfluencedByRewritten)
	assert.ElementsMatch(t, []string{"app-alpha", "app-foo"}, evt.Supersede.ApproachesInvalidated)

	// History event persisted to disk.
	assert.True(t, fileExists(t, fs, ".borg/history/evt-001.json"))
}

func TestApplySupersede_Decision_InPlace(t *testing.T) {
	fs, loaded := fixtureSupersedeFS(t)
	plan, err := ComputeSupersedePlan(loaded, "dec-target", "dec-target", "evt-002")
	require.NoError(t, err)

	now := time.Date(2026, 5, 9, 13, 0, 0, 0, time.UTC)
	newDec := spec.Decision{
		ID: "dec-target", Title: "Target decision (revised)",
		Status: spec.DecisionStatusProposed, Confidence: 0.7,
		Rationale: "revised in place — alternatives section expanded",
		CreatedAt: now, UpdatedAt: now,
	}

	historian := history.NewHistorian(fs, ".borg/history")
	evt, err := ApplySupersedeDecision(fs, plan, newDec, "expand alternatives", "", historian)
	require.NoError(t, err)

	// Decision file exists at the same id, content updated.
	assert.True(t, fileExists(t, fs, ".borg/spec/decisions/dec-target.json"))
	dec := loadDecision(t, fs, "dec-target")
	assert.Equal(t, "Target decision (revised)", dec.Title)
	assert.InDelta(t, 0.7, dec.Confidence, 0.01)

	// Feature/strategy/decision id-references untouched.
	feat := loadFeature(t, fs, "feat-alpha")
	assert.Equal(t, []string{"dec-target", "dec-other"}, feat.Decisions,
		"in-place: feature.Decisions[] entries unchanged")
	strat := loadStrategy(t, fs, "strat-foo")
	assert.Equal(t, []string{"dec-target"}, strat.Decisions)
	other := loadDecision(t, fs, "dec-other")
	assert.Equal(t, []string{"dec-target"}, other.InfluencedBy)

	// Approaches invalidated, but Decisions[] entries unchanged.
	appAlpha := loadApproach(t, fs, "app-alpha")
	assert.Equal(t, "evt-002", appAlpha.InvalidatedByEventID)
	assert.Equal(t, []string{"dec-target", "dec-other"}, appAlpha.Decisions,
		"in-place: invalidated approach Decisions[] unchanged")

	// History event flags InPlace.
	require.NotNil(t, evt.Supersede)
	assert.True(t, evt.Supersede.InPlace)
	assert.Empty(t, evt.Supersede.FeaturesDecisionsRewritten)
	assert.ElementsMatch(t, []string{"app-alpha", "app-foo"}, evt.Supersede.ApproachesInvalidated)
}

// --- Feature apply ---

func TestApplySupersede_Feature_NewSlug(t *testing.T) {
	fs, loaded := fixtureSupersedeFS(t)
	plan, err := ComputeSupersedePlan(loaded, "feat-alpha", "feat-replacement", "evt-003")
	require.NoError(t, err)

	now := time.Date(2026, 5, 9, 13, 0, 0, 0, time.UTC)
	newFeat := spec.Feature{
		ID: "feat-replacement", Title: "Replacement feature",
		Status:    spec.FeatureStatusProposed,
		Decisions: []string{"dec-target", "dec-other"},
		CreatedAt: now, UpdatedAt: now,
	}

	historian := history.NewHistorian(fs, ".borg/history")
	evt, err := ApplySupersedeFeature(fs, plan, newFeat, "rescope", "", historian)
	require.NoError(t, err)

	// Old feature gone, new feature written.
	assert.False(t, fileExists(t, fs, ".borg/spec/features/feat-alpha.json"))
	assert.True(t, fileExists(t, fs, ".borg/spec/features/feat-replacement.json"))

	// Bug FeatureID rewritten.
	b := loadBug(t, fs, "bug-1")
	assert.Equal(t, "feat-replacement", b.FeatureID)

	// Approach invalidated + ParentID rewritten.
	appAlpha := loadApproach(t, fs, "app-alpha")
	assert.Equal(t, "evt-003", appAlpha.InvalidatedByEventID)
	assert.Equal(t, "feat-replacement", appAlpha.ParentID)

	// History event.
	require.NotNil(t, evt.Supersede)
	assert.Equal(t, "feature", evt.Supersede.NodeKind)
	assert.ElementsMatch(t, []string{"bug-1"}, evt.Supersede.BugsFeatureIDRewritten)
	assert.ElementsMatch(t, []string{"app-alpha"}, evt.Supersede.ApproachesInvalidated)
}

func TestApplySupersede_Feature_InPlace(t *testing.T) {
	fs, loaded := fixtureSupersedeFS(t)
	plan, err := ComputeSupersedePlan(loaded, "feat-alpha", "feat-alpha", "evt-004")
	require.NoError(t, err)

	now := time.Date(2026, 5, 9, 13, 0, 0, 0, time.UTC)
	newFeat := spec.Feature{
		ID: "feat-alpha", Title: "Alpha feature (revised)",
		Status:    spec.FeatureStatusProposed,
		Decisions: []string{"dec-target", "dec-other"},
		CreatedAt: now, UpdatedAt: now,
	}

	historian := history.NewHistorian(fs, ".borg/history")
	_, err = ApplySupersedeFeature(fs, plan, newFeat, "tighten scope", "", historian)
	require.NoError(t, err)

	// Bug.FeatureID untouched.
	b := loadBug(t, fs, "bug-1")
	assert.Equal(t, "feat-alpha", b.FeatureID)

	// Approach invalidated, ParentID unchanged.
	app := loadApproach(t, fs, "app-alpha")
	assert.Equal(t, "evt-004", app.InvalidatedByEventID)
	assert.Equal(t, "feat-alpha", app.ParentID)
}

// --- Strategy apply ---

func TestApplySupersede_Strategy_NewSlug(t *testing.T) {
	fs, loaded := fixtureSupersedeFS(t)
	plan, err := ComputeSupersedePlan(loaded, "strat-foo", "strat-replacement", "evt-005")
	require.NoError(t, err)

	newStrat := spec.Strategy{
		ID: "strat-replacement", Title: "Replacement strategy",
		Kind: spec.StrategyKindFoundational, Status: "proposed",
		Decisions: []string{"dec-target"},
	}

	historian := history.NewHistorian(fs, ".borg/history")
	evt, err := ApplySupersedeStrategy(fs, plan, newStrat, "shift to new approach", "", historian)
	require.NoError(t, err)

	assert.False(t, fileExists(t, fs, ".borg/spec/strategies/strat-foo.json"))
	assert.True(t, fileExists(t, fs, ".borg/spec/strategies/strat-replacement.json"))

	// Approach invalidated + ParentID rewritten.
	app := loadApproach(t, fs, "app-foo")
	assert.Equal(t, "evt-005", app.InvalidatedByEventID)
	assert.Equal(t, "strat-replacement", app.ParentID)

	require.NotNil(t, evt.Supersede)
	assert.Equal(t, "strategy", evt.Supersede.NodeKind)
	assert.ElementsMatch(t, []string{"app-foo"}, evt.Supersede.ApproachesInvalidated)
}

func TestApplySupersede_Strategy_InPlace(t *testing.T) {
	fs, loaded := fixtureSupersedeFS(t)
	plan, err := ComputeSupersedePlan(loaded, "strat-foo", "strat-foo", "evt-006")
	require.NoError(t, err)

	newStrat := spec.Strategy{
		ID: "strat-foo", Title: "Foo strategy (revised)",
		Kind: spec.StrategyKindFoundational, Status: "proposed",
		Decisions: []string{"dec-target"},
	}

	historian := history.NewHistorian(fs, ".borg/history")
	_, err = ApplySupersedeStrategy(fs, plan, newStrat, "tweak", "", historian)
	require.NoError(t, err)

	app := loadApproach(t, fs, "app-foo")
	assert.Equal(t, "evt-006", app.InvalidatedByEventID)
	assert.Equal(t, "strat-foo", app.ParentID)
}

// --- Cross-cutting ---

func TestApplySupersedeDecision_NewIDMismatchPlanRejected(t *testing.T) {
	fs, loaded := fixtureSupersedeFS(t)
	plan, err := ComputeSupersedePlan(loaded, "dec-target", "dec-replacement", "evt-x")
	require.NoError(t, err)

	// New decision has a different id than the plan was computed for.
	// Apply must refuse rather than silently writing the wrong file.
	mismatched := spec.Decision{ID: "dec-totally-different", Title: "x", Status: spec.DecisionStatusProposed}
	historian := history.NewHistorian(fs, ".borg/history")
	_, err = ApplySupersedeDecision(fs, plan, mismatched, "", "", historian)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id")
}

func TestApplySupersedeDecision_WrongKindRejected(t *testing.T) {
	fs, loaded := fixtureSupersedeFS(t)
	plan, err := ComputeSupersedePlan(loaded, "feat-alpha", "feat-replacement", "evt-y")
	require.NoError(t, err)

	historian := history.NewHistorian(fs, ".borg/history")
	_, err = ApplySupersedeDecision(fs, plan, spec.Decision{ID: "feat-replacement"}, "", "", historian)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kind")
}
