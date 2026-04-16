package cmd

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
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

	feat := spec.Feature{ID: "feat-auth", Title: "User Authentication", Status: spec.FeatureStatusActive, Decisions: []string{"dec-lang"}}
	assert.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-auth", feat, "Auth feature body."))

	dec := spec.Decision{ID: "dec-lang", Title: "Language Choice", Status: spec.DecisionStatusActive, Feature: "feat-auth"}
	assert.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-lang", dec, "We chose Go."))

	strat := spec.Strategy{ID: "strat-go", Title: "Use Go", Kind: spec.StrategyKindFoundational, DecisionID: "dec-lang", Status: "active"}
	assert.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-go", strat, "Go strategy body."))

	traces := spec.TraceabilityIndex{Entries: map[string]spec.TraceEntry{"cmd/main.go": {StrategyID: "strat-go", DecisionIDs: []string{"dec-lang"}, FeatureIDs: []string{"feat-auth"}}}}
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
	assert.Equal(t, []string{"strat-go"}, diffNodeIDs(result.Strategies))
	assert.Equal(t, []string{"cmd/main.go"}, diffNodeIDs(result.Files))
}

func TestDiffFromStrategy(t *testing.T) {
	fs := setupDiffFS(t)
	result, err := RunDiff(fs, "strat-go")
	assert.NoError(t, err)
	if !assert.NotNil(t, result) {
		return
	}
	assert.Equal(t, "strat-go", result.Root.ID)
	assert.Equal(t, []string{"cmd/main.go"}, diffNodeIDs(result.Files))
	assert.Empty(t, result.Decisions)
}

func TestDiffUnknownID(t *testing.T) {
	fs := setupDiffFS(t)
	result, err := RunDiff(fs, "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, result)
}
