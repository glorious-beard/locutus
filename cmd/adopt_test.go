package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAdoptFixture(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".locutus/state", 0o755))

	feat := spec.Feature{
		ID: "feat-auth", Title: "Auth", Status: spec.FeatureStatusActive,
		Approaches: []string{"app-oauth"},
	}
	app := spec.Approach{
		ID: "app-oauth", Title: "OAuth", ParentID: "feat-auth",
		Body: "brief", ArtifactPaths: []string{"auth.go"},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-auth", feat, "body"))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-oauth.md", app, "## body\n"))
	require.NoError(t, fs.WriteFile("auth.go", []byte("package main"), 0o644))
	return fs
}

func TestRunAdoptDryRunProducesPlan(t *testing.T) {
	fs := setupAdoptFixture(t)
	report, err := RunAdopt(context.Background(), fs, "", true)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.True(t, report.DryRun)
	assert.Len(t, report.Classifications, 1)
	assert.Equal(t, state.StatusUnplanned, report.Classifications[0].Status)
	assert.Equal(t, 1, report.Summary.Candidates)

	// Dry-run must NOT write any state entries.
	store := state.NewFileStateStore(fs, ".locutus/state")
	entries, err := store.Walk()
	require.NoError(t, err)
	assert.Empty(t, entries, "dry-run must not persist state")
}

func TestRunAdoptPersistsPlannedStatus(t *testing.T) {
	fs := setupAdoptFixture(t)
	report, err := RunAdopt(context.Background(), fs, "", false)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.False(t, report.DryRun)

	store := state.NewFileStateStore(fs, ".locutus/state")
	entries, err := store.Walk()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, state.StatusPlanned, entries[0].Status)
	assert.Equal(t, "app-oauth", entries[0].ApproachID)
	assert.NotEmpty(t, entries[0].SpecHash)
}

func TestRunAdoptScopeFilter(t *testing.T) {
	fs := setupAdoptFixture(t)

	// Add a second feature+approach that we'll exclude via scope.
	other := spec.Feature{ID: "feat-other", Title: "Other", Status: spec.FeatureStatusActive, Approaches: []string{"app-other"}}
	otherApp := spec.Approach{ID: "app-other", Title: "Other", ParentID: "feat-other", Body: "x", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-other", other, "body"))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-other.md", otherApp, "body"))

	report, err := RunAdopt(context.Background(), fs, "feat-auth", true)
	require.NoError(t, err)
	require.Len(t, report.Classifications, 1)
	assert.Equal(t, "app-oauth", report.Classifications[0].Approach.ID)
}

func TestRunAdoptSkipsLiveOnWrite(t *testing.T) {
	fs := setupAdoptFixture(t)

	// Pre-populate state with a live entry so we don't overwrite it.
	store := state.NewFileStateStore(fs, ".locutus/state")
	liveHash := "sha256:preseed" // deliberately wrong so re-classification would mark it drifted
	require.NoError(t, store.Save(state.ReconciliationState{
		ApproachID: "app-oauth",
		SpecHash:   liveHash,
		Status:     state.StatusLive,
	}))

	// Now run adopt — app-oauth will classify as drifted (stored hash stale).
	report, err := RunAdopt(context.Background(), fs, "", false)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Summary.Drifted)

	entries, err := store.Walk()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, state.StatusPlanned, entries[0].Status, "drifted should be re-queued as planned")
	assert.NotEqual(t, liveHash, entries[0].SpecHash, "spec hash should be refreshed")
}
