package cmd

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func diffNodeIDs(nodes []spec.GraphNode) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	sort.Strings(ids)
	return ids
}

func setupDiffFS(t *testing.T) specio.FS {
	t.Helper()

	fs := specio.NewMemFS()
	fs.MkdirAll(".borg", 0o755)
	fs.MkdirAll(".borg/spec", 0o755)
	fs.MkdirAll(".borg/spec/features", 0o755)
	fs.MkdirAll(".borg/spec/decisions", 0o755)
	fs.MkdirAll(".borg/spec/strategies", 0o755)
	fs.MkdirAll(".borg/spec/bugs", 0o755)
	fs.MkdirAll(".borg/spec/approaches", 0o755)

	feat := spec.Feature{ID: "feat-auth", Title: "User Authentication", Status: spec.FeatureStatusActive, Decisions: []string{"dec-lang"}, Approaches: []string{"app-auth"}}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-auth", feat, "Auth feature body."))

	dec := spec.Decision{ID: "dec-lang", Title: "Language Choice", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-lang", dec, "We chose Go."))

	strat := spec.Strategy{ID: "strat-go", Title: "Use Go", Kind: spec.StrategyKindFoundational, Decisions: []string{"dec-lang"}, Status: "active"}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-go", strat, "Go strategy body."))

	app := spec.Approach{ID: "app-auth", Title: "Auth Implementation", ParentID: "feat-auth", ArtifactPaths: []string{"cmd/main.go"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-auth.md", app, "## Auth\n\nImplement OAuth.\n"))

	traces := spec.TraceabilityIndex{Entries: map[string]spec.TraceEntry{"cmd/main.go": {ApproachID: "app-auth", DecisionIDs: []string{"dec-lang"}, FeatureIDs: []string{"feat-auth"}}}}
	tracesData, _ := json.Marshal(traces)
	fs.WriteFile(".borg/spec/traces.json", tracesData, 0o644)

	return fs
}

func TestDiffFromFeature(t *testing.T) {
	fs := setupDiffFS(t)
	result, err := RunDiff(fs, "feat-auth")
	assert.NoError(t, err)
	if !assert.NotNil(t, result) {
		return
	}
	assert.Equal(t, "feat-auth", result.Root.ID)
	assert.Equal(t, []string{"dec-lang"}, diffNodeIDs(result.Decisions))
	assert.Empty(t, result.Strategies)
	assert.Equal(t, []string{"app-auth"}, diffNodeIDs(result.Approaches))
}

func TestDiffFromApproach(t *testing.T) {
	fs := setupDiffFS(t)
	result, err := RunDiff(fs, "app-auth")
	assert.NoError(t, err)
	if !assert.NotNil(t, result) {
		return
	}
	assert.Equal(t, "app-auth", result.Root.ID)
	assert.Empty(t, result.Approaches)
	assert.Empty(t, result.Decisions)
}

func TestDiffUnknownID(t *testing.T) {
	fs := setupDiffFS(t)
	result, err := RunDiff(fs, "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, result)
}
