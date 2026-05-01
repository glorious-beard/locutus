package cmd

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/scaffold"
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

func TestRefineGoalsRequiresNonEmptyGOALS(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	// No GOALS.md present.
	_, err := RunRefineGoals(context.Background(), nil, fs, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GOALS.md is empty or missing")
}

func TestRefineGoalsGeneratesSpecGraph(t *testing.T) {
	// Use scaffold.Scaffold to bootstrap a project FS — this writes the
	// six council agents and spec_generation.yaml that the workflow
	// executor needs to load. RunRefineGoals goes through GenerateSpec
	// which loads .borg/agents/ and .borg/workflows/spec_generation.yaml.
	fs := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fs, "test-project"))
	require.NoError(t, fs.WriteFile("GOALS.md", []byte("# WinPlan\nHelp candidates win elections.\n"), 0o644))
	// Drop the convergence agent so the workflow executor's convergence
	// check (which runs once after a max_rounds=1 pass) doesn't try a
	// 7th LLM call. Production users keep the agent — it's harmless when
	// the loop won't iterate again, just a small extra call.
	require.NoError(t, fs.Remove(".borg/agents/convergence.md"))

	// Phase 3 council flow: scout → outline → 1 elaborate_features +
	// 1 elaborate_strategies (fanout) → reconcile → 4 critics (empty)
	// → no revise → no reconcile_revise = 9 calls.
	scoutResp := `{"domain_read":"electoral campaign","technology_options":["x: a vs b"],"implicit_assumptions":["scale: 100k. Default: 1k concurrent"],"watch_outs":[]}`
	outlineResp := `{
		"features": [{"id":"feat-dashboard","title":"Candidate dashboard","summary":"At-a-glance campaign view"}],
		"strategies": [{"id":"strat-frontend","title":"React + TypeScript","kind":"foundational","summary":"frontend stack"}]
	}`
	featureElaborateResp := `{
		"id":"feat-dashboard","title":"Candidate dashboard","description":"At-a-glance campaign view.",
		"decisions":[
			{"title":"Use TanStack Start","rationale":"Best balance of SSR and DX","confidence":0.9,"alternatives":[{"name":"Next.js","rationale":"Mature","rejected_because":"Heavier than needed"}],"citations":[{"kind":"goals","reference":"GOALS.md","span":"lines 6-8","excerpt":"Help candidates win elections."}],"architect_rationale":"GOALS.md framing motivates a low-friction frontend."}
		]
	}`
	strategyElaborateResp := `{
		"id":"strat-frontend","title":"React + TypeScript","kind":"foundational","body":"Frontend prose",
		"decisions":[]
	}`
	reconcileEmpty := `{"actions":[]}`
	// elaborate_features and elaborate_strategies run in parallel
	// (workflow YAML has parallel: true); the mock would race on
	// positional consumption otherwise. Agent-tagged responses match
	// the source agent regardless of arrival order at the mock.
	mock := agent.NewMockLLM(
		agent.MockResponse{Response: &agent.GenerateResponse{Content: scoutResp, Model: "m"}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: outlineResp, Model: "m"}},
		agent.MockResponse{AgentID: "spec_feature_elaborator", Response: &agent.GenerateResponse{Content: featureElaborateResp, Model: "m"}},
		agent.MockResponse{AgentID: "spec_strategy_elaborator", Response: &agent.GenerateResponse{Content: strategyElaborateResp, Model: "m"}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: reconcileEmpty, Model: "m"}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"issues":[]}`, Model: "m"}},
	)

	result, err := RunRefineGoals(context.Background(), mock, fs, nil)
	require.NoError(t, err)
	require.NotNil(t, result.Generated)
	assert.Equal(t, 1, result.Generated.Features)
	assert.Equal(t, 1, result.Generated.Decisions,
		"reconciler with empty verdict mints one canonical decision per inline decision")
	assert.Equal(t, 1, result.Generated.Strategies,
		"outline named one strategy; fanout produced one elaborate_strategies output")
	assert.Equal(t, 0, result.Generated.Approaches,
		"refine no longer emits approaches — they're synthesized at adopt time")
	assert.Equal(t, spec.KindGoals, result.NodeKind)
	assert.Equal(t, spec.RootID, result.NodeID)

	// Verify nodes landed on disk. Decision ID is slug-derived from the
	// inline decision's title ("Use TanStack Start" → "dec-use-tanstack-start").
	_, err = fs.ReadFile(".borg/spec/features/feat-dashboard.json")
	assert.NoError(t, err, "feature JSON should be persisted")
	_, err = fs.ReadFile(".borg/spec/decisions/dec-use-tanstack-start.json")
	assert.NoError(t, err, "decision JSON should be persisted under reconciler-assigned slug id")
	_, err = fs.ReadFile(".borg/spec/strategies/strat-frontend.json")
	assert.NoError(t, err, "strategy JSON should be persisted")
	// Approaches directory should be untouched — adopt populates it.
	_, err = fs.ReadFile(".borg/spec/approaches/app-dashboard.md")
	assert.Error(t, err, "approach md must NOT be persisted by refine")

	// Strategy body should be in the .md sidecar.
	stratMd, err := fs.ReadFile(".borg/spec/strategies/strat-frontend.md")
	require.NoError(t, err)
	assert.Contains(t, string(stratMd), "Frontend prose")

	// Provenance must land on the persisted decision JSON, denormalized
	// per DJ-085 — deleting .locutus/sessions/ never costs the project
	// its justification record.
	decJSON, err := fs.ReadFile(".borg/spec/decisions/dec-use-tanstack-start.json")
	require.NoError(t, err)
	var persisted spec.Decision
	require.NoError(t, json.Unmarshal(decJSON, &persisted))
	require.NotNil(t, persisted.Provenance, "decision JSON should carry provenance")
	require.Equal(t, 1, len(persisted.Provenance.Citations))
	assert.Equal(t, "goals", persisted.Provenance.Citations[0].Kind)
	assert.Equal(t, "Help candidates win elections.", persisted.Provenance.Citations[0].Excerpt,
		"excerpt should be persisted verbatim alongside the decision")
	assert.Equal(t, "GOALS.md framing motivates a low-friction frontend.",
		persisted.Provenance.ArchitectRationale)
	assert.False(t, persisted.Provenance.GeneratedAt.IsZero(),
		"normalize step should stamp GeneratedAt at write time")
}

func TestDispatchRefineUnknownKindFailsGracefully(t *testing.T) {
	_, err := dispatchRefine(context.Background(), nil, nil, "weird", spec.NodeKind("unsupported"), nil)
	require.Error(t, err)
}
