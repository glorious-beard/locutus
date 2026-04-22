package cascade_test

import (
	"context"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupCascadeFixture builds a minimal graph:
//
//	Feature feat-auth ─▶ Decision dec-lang
//	Feature feat-auth ─▶ Approach app-oauth (with existing live state entry)
//	Strategy strat-go ─▶ Decision dec-lang (same decision — shared edge)
//	Strategy strat-go ─▶ Approach app-go   (also live)
//
// Tests scripts a rewriter response per parent and verifies that updates
// propagate to the parent prose, approach state entries, and historian.
func setupCascadeFixture(t *testing.T) (specio.FS, *spec.SpecGraph, *state.FileStateStore) {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))
	require.NoError(t, fs.MkdirAll(".locutus/state", 0o755))

	feat := spec.Feature{
		ID:          "feat-auth",
		Title:       "Authentication",
		Status:      spec.FeatureStatusActive,
		Description: "We are building authentication using the previously-chosen language.",
		Decisions:   []string{"dec-lang"},
		Approaches:  []string{"app-oauth"},
	}
	dec := spec.Decision{ID: "dec-lang", Title: "Use Go", Status: spec.DecisionStatusActive, Confidence: 0.9}
	strat := spec.Strategy{
		ID:         "strat-go",
		Title:      "Backend in Go",
		Kind:       spec.StrategyKindFoundational,
		Status:     "active",
		Decisions:  []string{"dec-lang"},
		Approaches: []string{"app-go"},
	}
	appOAuth := spec.Approach{ID: "app-oauth", Title: "OAuth", ParentID: "feat-auth", Body: "synth", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	appGo := spec.Approach{ID: "app-go", Title: "Go scaffold", ParentID: "strat-go", Body: "synth", CreatedAt: time.Now(), UpdatedAt: time.Now()}

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-auth", feat, feat.Description))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-lang", dec, "We chose Go."))
	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-go", strat, "We run the backend in Go."))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-oauth.md", appOAuth, "body"))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-go.md", appGo, "body"))

	g := spec.BuildGraph(
		[]spec.Feature{feat}, nil,
		[]spec.Decision{dec},
		[]spec.Strategy{strat},
		[]spec.Approach{appOAuth, appGo},
		spec.TraceabilityIndex{},
	)

	store := state.NewFileStateStore(fs, ".locutus/state")
	// Preseed both Approaches as live so we can verify they transition to
	// drifted with WorkstreamID + AssertionResults cleared.
	for _, id := range []string{"app-oauth", "app-go"} {
		require.NoError(t, store.Save(state.ReconciliationState{
			ApproachID:   id,
			SpecHash:     "sha256:some-prior-hash",
			Status:       state.StatusLive,
			WorkstreamID: "ws-prior",
			AssertionResults: []state.AssertionResult{
				{Passed: true, Output: "all tests passed before cascade"},
			},
		}))
	}

	return fs, g, store
}

// scriptedRewrite builds a MockResponse that a rewriter call will consume.
// The JSON body matches the RewriteResult schema.
func scriptedRewrite(body string, changed bool, rationale string) agent.MockResponse {
	payload := `{"revised_body":` + quote(body) + `,"changed":` + boolJSON(changed) + `,"rationale":` + quote(rationale) + `}`
	return agent.MockResponse{Response: &agent.GenerateResponse{Content: payload}}
}

func quote(s string) string { return `"` + s + `"` }
func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestCascadeRewritesBothParentsAndDriftsApproaches(t *testing.T) {
	fs, g, store := setupCascadeFixture(t)

	llm := agent.NewMockLLM(
		scriptedRewrite("We are building authentication using Go as the backend language.", true, "Surface the Go decision"),
		scriptedRewrite("We run the backend in Go with the language decision refreshed.", true, "Clarify that Go is still the chosen language"),
	)

	result, err := cascade.Cascade(context.Background(), llm, fs, g, store, "dec-lang")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.ElementsMatch(t, []string{"feat-auth"}, result.UpdatedFeatures)
	assert.ElementsMatch(t, []string{"strat-go"}, result.UpdatedStrategies)
	assert.ElementsMatch(t, []string{"app-oauth", "app-go"}, result.DriftedApproaches)
	assert.Empty(t, result.Skipped)
	assert.Len(t, result.Events, 2)

	// Verify Feature file on disk reflects the revised prose.
	featData, err := fs.ReadFile(".borg/spec/features/feat-auth.md")
	require.NoError(t, err)
	assert.Contains(t, string(featData), "using Go")

	// Verify both state entries were flipped to drifted with hash zeroed
	// and plan pointers cleared (DJ-072 invariant).
	for _, id := range []string{"app-oauth", "app-go"} {
		got, err := store.Load(id)
		require.NoError(t, err)
		assert.Equal(t, state.StatusDrifted, got.Status)
		assert.Empty(t, got.SpecHash, "SpecHash must be zeroed for classifier drift detection")
		assert.Empty(t, got.WorkstreamID, "stale WorkstreamID must be cleared")
		assert.Empty(t, got.AssertionResults, "stale AssertionResults must be cleared")
	}
}

func TestCascadeSkipsWhenRewriterReportsNoChange(t *testing.T) {
	fs, g, store := setupCascadeFixture(t)

	// Both rewrites report no change needed.
	llm := agent.NewMockLLM(
		scriptedRewrite("We are building authentication using the previously-chosen language.", false, "Already accurate"),
		scriptedRewrite("We run the backend in Go.", false, "Already accurate"),
	)

	result, err := cascade.Cascade(context.Background(), llm, fs, g, store, "dec-lang")
	require.NoError(t, err)

	assert.Empty(t, result.UpdatedFeatures)
	assert.Empty(t, result.UpdatedStrategies)
	assert.Empty(t, result.DriftedApproaches, "no drift when prose unchanged")
	assert.ElementsMatch(t, []string{"feat-auth", "strat-go"}, result.Skipped)
	assert.Empty(t, result.Events)

	// State entries should be untouched.
	got, err := store.Load("app-oauth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusLive, got.Status)
	assert.Equal(t, "ws-prior", got.WorkstreamID)
}

func TestCascadeUnknownDecisionErrors(t *testing.T) {
	fs, g, store := setupCascadeFixture(t)
	llm := agent.NewMockLLM()

	_, err := cascade.Cascade(context.Background(), llm, fs, g, store, "nope")
	assert.Error(t, err)
}

// TestCascadeRecordsHistoryEvents verifies that one event is persisted per
// rewritten parent. Events feed the historian's narrative layer; losing
// them would make spec changes opaque to the "why did this change?" tool.
func TestCascadeRecordsHistoryEvents(t *testing.T) {
	fs, g, store := setupCascadeFixture(t)

	llm := agent.NewMockLLM(
		scriptedRewrite("revised feat", true, "updated to reflect Go"),
		scriptedRewrite("revised strat", true, "clarified Go choice"),
	)

	result, err := cascade.Cascade(context.Background(), llm, fs, g, store, "dec-lang")
	require.NoError(t, err)
	require.Len(t, result.Events, 2)

	// Each event carries the target parent ID and a rationale that includes
	// the triggering decision.
	for _, evt := range result.Events {
		assert.Contains(t, evt.Rationale, "dec-lang", "event must reference the triggering decision")
		assert.NotEmpty(t, evt.TargetID)
		assert.NotEmpty(t, evt.Kind)
	}

	// Files on disk should include the events the historian persisted.
	files, err := fs.ListDir(".borg/history")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(files), 2)
}

// TestCascadeMarksDriftEvenWithoutPriorState covers the case where an
// Approach has no state entry yet (e.g., freshly added). Cascade should
// not error; those approaches are already `unplanned` from the classifier's
// perspective, and there's nothing to mark drifted.
func TestCascadeMarksDriftEvenWithoutPriorState(t *testing.T) {
	fs, g, _ := setupCascadeFixture(t)
	// Fresh store with no pre-seeded entries.
	emptyStore := state.NewFileStateStore(fs, ".locutus/state-fresh")
	require.NoError(t, fs.MkdirAll(".locutus/state-fresh", 0o755))

	llm := agent.NewMockLLM(
		scriptedRewrite("revised feat", true, "note the Go decision"),
		scriptedRewrite("revised strat", true, "confirm Go"),
	)

	result, err := cascade.Cascade(context.Background(), llm, fs, g, emptyStore, "dec-lang")
	require.NoError(t, err)
	// Parents still got rewritten; approaches without state entries are
	// simply not in the DriftedApproaches set (they're already unplanned).
	assert.NotEmpty(t, result.UpdatedFeatures)
	assert.NotEmpty(t, result.UpdatedStrategies)
	assert.Empty(t, result.DriftedApproaches)
}
