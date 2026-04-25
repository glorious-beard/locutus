package cmd

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveNodeKindFromFeature(t *testing.T) {
	fs := setupBlastRadiusFS(t)
	kind, err := resolveNodeKind(fs, "feat-auth")
	require.NoError(t, err)
	assert.Equal(t, spec.KindFeature, kind)
}

func TestResolveNodeKindFromDecision(t *testing.T) {
	fs := setupBlastRadiusFS(t)
	kind, err := resolveNodeKind(fs, "dec-lang")
	require.NoError(t, err)
	assert.Equal(t, spec.KindDecision, kind)
}

func TestResolveNodeKindFromApproach(t *testing.T) {
	fs := setupBlastRadiusFS(t)
	kind, err := resolveNodeKind(fs, "app-auth")
	require.NoError(t, err)
	assert.Equal(t, spec.KindApproach, kind)
}

func TestResolveNodeKindUnknown(t *testing.T) {
	fs := setupBlastRadiusFS(t)
	_, err := resolveNodeKind(fs, "nonexistent")
	assert.Error(t, err)
}

func TestRefineDryRunRendersBlastRadius(t *testing.T) {
	fs := setupBlastRadiusFS(t)
	out := captureStdout(func() {
		err := renderRefineDryRun(fs, "dec-lang", spec.KindDecision)
		require.NoError(t, err)
	})
	assert.Contains(t, out, "cascade preview")
}

// setupRefineFS extends setupBlastRadiusFS with a Bug and wires up a state entry for
// the Approach so drift-on-refine can be asserted.
func setupRefineFS(t *testing.T) specio.FS {
	t.Helper()
	fs := setupBlastRadiusFS(t)

	bug := spec.Bug{
		ID:          "bug-login",
		Title:       "Login hangs on submit",
		FeatureID:   "feat-auth",
		Severity:    spec.BugSeverityMedium,
		Status:      spec.BugStatusTriaged,
		Description: "Users report a hang on submit when JS is disabled.",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/bugs/bug-login", bug, "Users report a hang on submit when JS is disabled."))

	// Seed a state entry for app-auth so `StatusDrifted` can be asserted
	// after refine cascades child Approaches.
	store := state.NewFileStateStore(fs, ".locutus/state")
	require.NoError(t, store.Save(state.ReconciliationState{
		ApproachID:     "app-auth",
		Status:         state.StatusLive,
		SpecHash:       "original-hash",
		LastReconciled: time.Now(),
	}))

	return fs
}

// scriptRewriter returns a MockLLM that emits a single RewriteResult JSON
// response matching the rewriter/synthesizer output schema.
func scriptRewriter(t *testing.T, changed bool, body, rationale string) *agent.MockLLM {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"revised_body": body,
		"changed":      changed,
		"rationale":    rationale,
	})
	require.NoError(t, err)
	return agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: string(payload)},
	})
}

func TestRefineFeatureRegeneratesDescription(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, true, "Authenticate users with OAuth2, implemented in Go.", "reflect dec-lang constraint")

	result, err := RunRefineFeature(context.Background(), llm, fs, "feat-auth")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Rewrite)
	assert.True(t, result.Rewrite.Updated)
	assert.Equal(t, spec.KindFeature, result.NodeKind)

	f, body, err := specio.LoadPair[spec.Feature](fs, ".borg/spec/features/feat-auth")
	require.NoError(t, err)
	assert.Contains(t, f.Description, "OAuth2")
	assert.Contains(t, body, "OAuth2")

	store := state.NewFileStateStore(fs, ".locutus/state")
	entry, err := store.Load("app-auth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusDrifted, entry.Status)
	assert.Empty(t, entry.SpecHash, "SpecHash zeroed on drift")
	assert.Contains(t, result.Rewrite.DriftedApproaches, "app-auth")
}

func TestRefineFeatureNoOpWhenRewriterReportsUnchanged(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, false, "Auth feature body.", "already accurate")

	result, err := RunRefineFeature(context.Background(), llm, fs, "feat-auth")
	require.NoError(t, err)
	require.NotNil(t, result.Rewrite)
	assert.False(t, result.Rewrite.Updated)
	assert.Empty(t, result.Rewrite.DriftedApproaches)

	store := state.NewFileStateStore(fs, ".locutus/state")
	entry, err := store.Load("app-auth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusLive, entry.Status, "no-op must not touch downstream state")
}

func TestRefineStrategyUpdatesBody(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, true, "Use Go with generics for the core modules.", "reflect dec-lang")

	result, err := RunRefineStrategy(context.Background(), llm, fs, "strat-go")
	require.NoError(t, err)
	require.NotNil(t, result.Rewrite)
	assert.True(t, result.Rewrite.Updated)
	assert.Equal(t, spec.KindStrategy, result.NodeKind)

	_, body, err := specio.LoadPair[spec.Strategy](fs, ".borg/spec/strategies/strat-go")
	require.NoError(t, err)
	assert.Contains(t, body, "generics")
}

func TestRefineBugSameAsFeature(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, true, "Login times out after 10s when JS is disabled.", "tightened scope")

	result, err := RunRefineBug(context.Background(), llm, fs, "bug-login")
	require.NoError(t, err)
	require.NotNil(t, result.Rewrite)
	assert.True(t, result.Rewrite.Updated)
	assert.Equal(t, spec.KindBug, result.NodeKind)

	b, _, err := specio.LoadPair[spec.Bug](fs, ".borg/spec/bugs/bug-login")
	require.NoError(t, err)
	assert.Contains(t, b.Description, "times out")
}

func TestRefineApproachResynthesizesBody(t *testing.T) {
	fs := setupRefineFS(t)
	llm := scriptRewriter(t, true, "## Auth\n\nImplement OAuth2 per strat-go, using Go.\n", "re-synthesized from parent")

	result, err := RunRefineApproach(context.Background(), llm, fs, "app-auth")
	require.NoError(t, err)
	require.NotNil(t, result.Rewrite)
	assert.True(t, result.Rewrite.Updated)
	assert.Equal(t, spec.KindApproach, result.NodeKind)

	a, body, err := specio.LoadMarkdown[spec.Approach](fs, ".borg/spec/approaches/app-auth.md")
	require.NoError(t, err)
	assert.Contains(t, body, "OAuth2")
	assert.Contains(t, a.Body, "OAuth2")

	store := state.NewFileStateStore(fs, ".locutus/state")
	entry, err := store.Load("app-auth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusDrifted, entry.Status, "refined Approach must be drifted for replan")
	assert.Empty(t, entry.SpecHash)
}

func TestRefineGoalsReturnsExplicitNotSupported(t *testing.T) {
	_, err := dispatchRefine(context.Background(), nil, nil, "goals", spec.KindGoals)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "goals are human-authored")
	assert.Contains(t, err.Error(), "GOALS.md")
}

func TestDispatchRefineUnknownKindFailsGracefully(t *testing.T) {
	_, err := dispatchRefine(context.Background(), nil, nil, "weird", spec.NodeKind("unsupported"))
	require.Error(t, err)
}
