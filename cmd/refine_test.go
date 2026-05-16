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
	store := state.NewFileStateStore(fs, ".borg/state")
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
func scriptRewriter(t *testing.T, changed bool, body, rationale string) *agent.MockExecutor {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"revised_body": body,
		"changed":      changed,
		"rationale":    rationale,
	})
	require.NoError(t, err)
	return agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: string(payload)},
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

	store := state.NewFileStateStore(fs, ".borg/state")
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

	store := state.NewFileStateStore(fs, ".borg/state")
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

	store := state.NewFileStateStore(fs, ".borg/state")
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
	// DJ-124 Stage C: end-to-end refine exercising the new scout-driven
	// workflow. The round shape is now:
	//
	//   iter 0: scout (axes_open + new_nodes; converged=false)
	//   iter 1: decisions (one per open axis)
	//           → narrative (one per affected node)
	//           → reconcile → critique (x4 empty)
	//           → scout (converged=true)
	//
	// Use scaffold.Scaffold to bootstrap a project FS — this writes the
	// council agents the workflow executor needs to load. The workflow
	// shape itself lives in code (agent.NewSpecGenerationWorkflow);
	// RunRefineGoals goes through GenerateSpec which loads
	// .borg/agents/ and binds them to the in-code workflow.
	fs := specio.NewMemFS()
	require.NoError(t, scaffold.Scaffold(fs, "test-project"))
	require.NoError(t, fs.WriteFile("GOALS.md", []byte("# WinPlan\nHelp candidates win elections.\n"), 0o644))
	// Drop the convergence agent so the workflow executor's convergence
	// check (which runs once after a max_rounds=1 pass) doesn't try an
	// extra LLM call. Production users keep the agent — it's harmless
	// when the loop won't iterate again.
	require.NoError(t, fs.Remove(".borg/agents/convergence.md"))

	scout0 := `{
		"domain_read":"electoral campaign tooling",
		"technology_options":["frontend: TanStack Start vs Next.js"],
		"implicit_assumptions":["scale: 100k registered, 1k concurrent"],
		"watch_outs":[],
		"axes_open":[{
			"id":"frontend-framework",
			"description":"Which framework backs the candidate dashboard?",
			"source_evidence":["Help candidates win elections."],
			"surfaced_by":["feat-dashboard"]
		}],
		"new_nodes":[
			{"kind":"feature","id":"feat-dashboard","title":"Candidate dashboard","summary":"At-a-glance campaign view","decisions":[]},
			{"kind":"strategy","id":"strat-frontend","title":"React + TypeScript","summary":"frontend stack","decisions":[]}
		],
		"converged":false
	}`
	decisionForFrontend := `{
		"id":"dec-use-tanstack-start",
		"title":"Use TanStack Start",
		"rationale":"Best balance of SSR and DX for the team's stack.",
		"architect_rationale":"GOALS.md framing motivates a low-friction frontend.",
		"confidence":0.9,
		"alternatives":[{
			"name":"Next.js",
			"rationale":"Mature framework with broad ecosystem",
			"rejected_because":"Heavier than needed for the dashboard scope",
			"citations":[{"kind":"web","reference":"https://nextjs.org","excerpt":"Next.js is the React Framework for the Web."}]
		}],
		"citations":[{"kind":"goals","reference":"GOALS.md","span":"lines 6-8","excerpt":"Help candidates win elections."}],
		"axes":["frontend-framework"],
		"surfaced_by":["feat-dashboard"]
	}`
	featureNarrative := `{
		"id":"feat-dashboard",
		"title":"Candidate dashboard",
		"description":"At-a-glance campaign view rendered by TanStack Start.",
		"decisions":["dec-use-tanstack-start"]
	}`
	strategyNarrative := `{
		"id":"strat-frontend",
		"title":"React + TypeScript",
		"kind":"foundational",
		"body":"Frontend prose",
		"decisions":["dec-use-tanstack-start"]
	}`
	reconcileEmpty := `{"actions":[]}`
	scoutConverged := `{
		"domain_read":"electoral campaign tooling",
		"technology_options":[],
		"implicit_assumptions":[],
		"watch_outs":[],
		"axes_open":[],
		"new_nodes":[],
		"converged":true
	}`

	mock := agent.NewMockExecutor(
		agent.MockResponse{AgentID: "spec_scout", Response: &agent.AgentOutput{Content: scout0, Model: "m"}},
		agent.MockResponse{AgentID: "spec_decision_elaborator", Response: &agent.AgentOutput{Content: decisionForFrontend, Model: "m"}},
		agent.MockResponse{AgentID: "spec_feature_elaborator", Response: &agent.AgentOutput{Content: featureNarrative, Model: "m"}},
		agent.MockResponse{AgentID: "spec_strategy_elaborator", Response: &agent.AgentOutput{Content: strategyNarrative, Model: "m"}},
		agent.MockResponse{AgentID: "spec_reconciler", Response: &agent.AgentOutput{Content: reconcileEmpty, Model: "m"}},
		agent.MockResponse{AgentID: "architect_critic", Response: &agent.AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{AgentID: "devops_critic", Response: &agent.AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{AgentID: "sre_critic", Response: &agent.AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{AgentID: "cost_critic", Response: &agent.AgentOutput{Content: `{"issues":[]}`, Model: "m"}},
		agent.MockResponse{AgentID: "spec_scout", Response: &agent.AgentOutput{Content: scoutConverged, Model: "m"}},
	)

	result, err := RunRefineGoals(context.Background(), mock, fs, nil)
	require.NoError(t, err)
	require.NotNil(t, result.Generated)
	assert.Equal(t, 1, result.Generated.Features)
	assert.Equal(t, 1, result.Generated.Decisions,
		"per-axis decision-elaborator minted one decision for the open axis")
	assert.Equal(t, 1, result.Generated.Strategies,
		"scout surfaced one new strategy node; narrative elaborator produced its body")
	assert.Equal(t, 0, result.Generated.Approaches,
		"refine no longer emits approaches — they're synthesized at adopt time")
	assert.Equal(t, spec.KindGoals, result.NodeKind)
	assert.Equal(t, spec.RootID, result.NodeID)

	// Verify nodes landed on disk. The decision ID is the slug the
	// decision-elaborator emitted directly (no reconciler dedupe under
	// DJ-124; the field-map preserves the ID verbatim when set).
	_, err = fs.ReadFile(".borg/spec/features/feat-dashboard.json")
	assert.NoError(t, err, "feature JSON should be persisted")
	_, err = fs.ReadFile(".borg/spec/decisions/dec-use-tanstack-start.json")
	assert.NoError(t, err, "decision JSON should be persisted under the elaborator-assigned slug id")
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

// TestRunRefineDecisionDrivesCascadeWorkflow verifies that the
// decision-target RunRefine path goes through the workflow-driven
// cascade (Phase 7) and produces the legacy cascade.Result shape.
// Equivalence test for the workflow migration: the on-disk Feature
// body, store entry transitions, and RefineResult.Cascade fields
// match what cascade.Cascade would have produced for the same fixture.
//
// The setupRefineFS fixture also seeds bug-login (FeatureID=feat-auth).
// The workflow's enhancement over the legacy cascade.Cascade includes
// Bugs whose parent Feature references the changed Decision, so this
// test scripts a third response for the bug rewrite.
func TestRunRefineDecisionDrivesCascadeWorkflow(t *testing.T) {
	fs := setupRefineFS(t)

	// Three responses: Feature, Strategy, Bug — emitted in that order
	// by fanoutRefineParents. Sequential fanout in the workflow keeps
	// the assignment stable across runs.
	mock := agent.NewMockExecutor(
		agent.MockResponse{Response: &agent.AgentOutput{
			Content: `{"revised_body":"Auth feature, now uses Go.","changed":true,"rationale":"surface go decision"}`,
		}},
		agent.MockResponse{Response: &agent.AgentOutput{
			Content: `{"revised_body":"Use Go end-to-end, with strict typing.","changed":true,"rationale":"clarify go choice"}`,
		}},
		agent.MockResponse{Response: &agent.AgentOutput{
			Content: `{"revised_body":"Login times out under Go-side timeouts.","changed":true,"rationale":"reflect go decision"}`,
		}},
	)

	result, err := RunRefine(context.Background(), mock, fs, "dec-lang", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, spec.KindDecision, result.NodeKind)
	assert.Equal(t, "dec-lang", result.NodeID)
	require.NotNil(t, result.Cascade, "decision target must surface a cascade.Result")
	assert.ElementsMatch(t, []string{"feat-auth"}, result.Cascade.UpdatedFeatures)
	assert.ElementsMatch(t, []string{"strat-go"}, result.Cascade.UpdatedStrategies)
	assert.Contains(t, result.Cascade.DriftedApproaches, "app-auth")
	assert.Empty(t, result.Cascade.Skipped)
	assert.Len(t, result.Cascade.Events, 3,
		"one history event per Feature + Strategy + Bug rewritten")

	// Feature on disk should reflect the new body.
	feat, body, err := specio.LoadPair[spec.Feature](fs, ".borg/spec/features/feat-auth")
	require.NoError(t, err)
	assert.Contains(t, feat.Description, "uses Go")
	assert.Contains(t, body, "uses Go")

	// Approach state for app-auth should be flipped to drifted with
	// SpecHash zeroed (DJ-072 invariant).
	store := state.NewFileStateStore(fs, ".borg/state")
	got, err := store.Load("app-auth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusDrifted, got.Status)
	assert.Empty(t, got.SpecHash)
}

// TestRunRefineDecisionUnknownIDFails covers the "decision not found"
// path — the workflow construction should reject unknown IDs the
// same way the legacy cascade.Cascade did.
func TestRunRefineDecisionUnknownIDFails(t *testing.T) {
	fs := setupRefineFS(t)
	mock := agent.NewMockExecutor()
	_, err := RunRefine(context.Background(), mock, fs, "dec-nope", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dec-nope")
}
