package reconcile_test

import (
	"testing"
	"time"

	"github.com/chetan/locutus/internal/reconcile"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupReconcileFixture(t *testing.T) (specio.FS, *spec.SpecGraph, *state.FileStateStore, map[string]spec.Decision) {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".locutus/state", 0o755))

	feat := spec.Feature{ID: "feat-auth", Title: "Auth", Status: spec.FeatureStatusActive, Approaches: []string{"app-oauth"}}
	dec := spec.Decision{ID: "dec-lang", Title: "Go", Status: spec.DecisionStatusActive}
	app := spec.Approach{
		ID: "app-oauth", Title: "OAuth", ParentID: "feat-auth",
		Body: "brief", ArtifactPaths: []string{"auth.go"}, Decisions: []string{"dec-lang"},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-auth", feat, "body"))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-lang", dec, "body"))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-oauth.md", app, "## Body\n"))
	require.NoError(t, fs.WriteFile("auth.go", []byte("package main"), 0o644))

	traces := spec.TraceabilityIndex{}
	g := spec.BuildGraph(
		[]spec.Feature{feat}, nil,
		[]spec.Decision{dec},
		nil,
		[]spec.Approach{app},
		traces,
	)

	store := state.NewFileStateStore(fs, ".locutus/state")
	decMap := map[string]spec.Decision{"dec-lang": dec}
	return fs, g, store, decMap
}

func TestClassifyUnplannedWhenNoState(t *testing.T) {
	fs, g, store, decMap := setupReconcileFixture(t)
	results, err := reconcile.Classify(fs, g, store, decMap)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, state.StatusUnplanned, results[0].Status)
	assert.Empty(t, results[0].StoredHash)
	assert.NotEmpty(t, results[0].CurrentHash)
}

func TestClassifyLiveWhenHashesMatch(t *testing.T) {
	fs, g, store, decMap := setupReconcileFixture(t)

	// First pass: compute current state, save as "live".
	results, err := reconcile.Classify(fs, g, store, decMap)
	require.NoError(t, err)
	require.NoError(t, store.Save(state.ReconciliationState{
		ApproachID:     "app-oauth",
		SpecHash:       results[0].CurrentHash,
		Artifacts:      results[0].CurrentFiles,
		Status:         state.StatusLive,
		LastReconciled: time.Now(),
	}))

	// Re-classify: stored hashes match current — status should stay live.
	results, err = reconcile.Classify(fs, g, store, decMap)
	require.NoError(t, err)
	assert.Equal(t, state.StatusLive, results[0].Status)
}

func TestClassifyDriftedWhenSpecChanges(t *testing.T) {
	fs, g, store, decMap := setupReconcileFixture(t)

	// Save a stale spec_hash to simulate spec having changed since last reconcile.
	require.NoError(t, store.Save(state.ReconciliationState{
		ApproachID: "app-oauth",
		SpecHash:   "sha256:stale",
		Artifacts:  spec.ComputeArtifactHashes(fs.ReadFile, *g.Approach("app-oauth")),
		Status:     state.StatusLive,
	}))

	results, err := reconcile.Classify(fs, g, store, decMap)
	require.NoError(t, err)
	assert.Equal(t, state.StatusDrifted, results[0].Status)
	assert.True(t, results[0].DriftedSpec())
}

func TestClassifyOutOfSpecWhenArtifactChanges(t *testing.T) {
	fs, g, store, decMap := setupReconcileFixture(t)

	// Save current spec_hash + current artifact hashes so the only
	// observable change afterward is an artifact edit.
	app := g.Approach("app-oauth")
	liveHash := spec.ComputeSpecHash(*app, []spec.Decision{decMap["dec-lang"]})
	artifacts := spec.ComputeArtifactHashes(fs.ReadFile, *app)
	require.NoError(t, store.Save(state.ReconciliationState{
		ApproachID: "app-oauth",
		SpecHash:   liveHash,
		Artifacts:  artifacts,
		Status:     state.StatusLive,
	}))

	// Edit the artifact outside Locutus.
	require.NoError(t, fs.WriteFile("auth.go", []byte("package main\n// edited"), 0o644))

	results, err := reconcile.Classify(fs, g, store, decMap)
	require.NoError(t, err)
	assert.Equal(t, state.StatusOutOfSpec, results[0].Status)
	assert.True(t, results[0].DriftedArtifacts())
}

func TestPlanCandidatesExcludesLive(t *testing.T) {
	cs := []reconcile.Classification{
		{Status: state.StatusLive},
		{Status: state.StatusDrifted},
		{Status: state.StatusUnplanned},
		{Status: state.StatusOutOfSpec},
		{Status: state.StatusFailed},
	}
	candidates := reconcile.PlanCandidates(cs)
	assert.Len(t, candidates, 3)
	for _, c := range candidates {
		assert.NotEqual(t, state.StatusLive, c.Status)
		assert.NotEqual(t, state.StatusOutOfSpec, c.Status)
	}
}

func TestOutOfSpecFilter(t *testing.T) {
	cs := []reconcile.Classification{
		{Approach: spec.Approach{ID: "a"}, Status: state.StatusLive},
		{Approach: spec.Approach{ID: "b"}, Status: state.StatusOutOfSpec},
	}
	flagged := reconcile.OutOfSpec(cs)
	assert.Len(t, flagged, 1)
	assert.Equal(t, "b", flagged[0].Approach.ID)
}
