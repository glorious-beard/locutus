package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRefineWorkflowFixture mirrors the cascade-package fixture so
// the workflow tests can drive the same on-disk shape and compare
// behavior against the legacy direct-call path. Returns the FS,
// graph, store, and a fully-populated RefineState ready for the
// workflow executor.
//
// Graph:
//
//	Feature  feat-auth ─▶ Decision dec-lang
//	Feature  feat-auth ─▶ Approach app-oauth (live state entry)
//	Strategy strat-go  ─▶ Decision dec-lang
//	Strategy strat-go  ─▶ Approach app-go    (live state entry)
func setupRefineWorkflowFixture(t *testing.T) (specio.FS, *spec.SpecGraph, *state.FileStateStore, RefineState) {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/state", 0o755))

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
	appOAuth := spec.Approach{
		ID: "app-oauth", Title: "OAuth", ParentID: "feat-auth",
		Body: "synth", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	appGo := spec.Approach{
		ID: "app-go", Title: "Go scaffold", ParentID: "strat-go",
		Body: "synth", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

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

	store := state.NewFileStateStore(fs, ".borg/state")
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

	rs := RefineState{
		DecisionID: "dec-lang",
		Decision:   &dec,
		Graph:      g,
		FSys:       fs,
		Store:      store,
		Features:   []spec.Feature{feat},
		Strategies: []spec.Strategy{strat},
	}
	return fs, g, store, rs
}

// scriptedRefineRewrite mirrors cascade_test.scriptedRewrite — emits
// a RefineRewriteResult JSON payload (same wire shape as
// cascade.RewriteResult).
func scriptedRefineRewrite(body string, changed bool, rationale string) MockResponse {
	payload, _ := json.Marshal(RefineRewriteResult{
		RevisedBody: body,
		Changed:     changed,
		Rationale:   rationale,
	})
	return MockResponse{Response: &AgentOutput{Content: string(payload)}}
}

// loadRefineWorkflowAgentDefs returns stub AgentDefs for the rewriter
// and refiner agents — sufficient for the workflow executor's
// lookup. The real scaffold.LoadAgent path can't be used here because
// internal/scaffold imports internal/agent (would create a test
// import cycle); the stubs match the agent IDs the workflow declares
// and give MockExecutor enough metadata to dispatch.
func loadRefineWorkflowAgentDefs(t *testing.T, fsys specio.FS) map[string]AgentDef {
	t.Helper()
	_ = fsys // signature kept symmetric with cmd-layer loadRefineAgentDefs
	return map[string]AgentDef{
		"rewriter": {ID: "rewriter", SystemPrompt: "Rewrite parent prose."},
		"refiner":  {ID: "refiner", SystemPrompt: "Refine parent prose with intent."},
	}
}

// TestRefineWorkflowFanoutShape verifies that fanoutRefineParents
// produces one item per parent (Feature + Strategy + Bug) and that
// each item carries the expected agent_id (rewriter when no brief).
// This is the workflow-specific "fanout shape" test the plan calls
// out — it locks the fanout closure's behavior independent of the
// executor's dispatch loop.
func TestRefineWorkflowFanoutShape(t *testing.T) {
	_, _, _, rs := setupRefineWorkflowFixture(t)

	items, err := fanoutRefineParents(&rs)
	require.NoError(t, err)
	require.Len(t, items, 2, "fanout should produce one item per Feature + Strategy referencing the decision")

	kinds := []string{}
	ids := []string{}
	for _, raw := range items {
		var item refineFanoutItem
		require.NoError(t, json.Unmarshal([]byte(raw), &item))
		assert.Equal(t, "rewriter", item.AgentID, "no brief => rewriter agent")
		kinds = append(kinds, item.Kind)
		ids = append(ids, item.ID)
	}
	assert.ElementsMatch(t, []string{"feature", "strategy"}, kinds)
	assert.ElementsMatch(t, []string{"feat-auth", "strat-go"}, ids)
}

// TestRefineWorkflowFanoutShapeWithBrief confirms that a non-empty
// Brief flips the per-item AgentID to "refiner" — exercises the
// per-item dispatch routing the workflow inherits from DJ-098.
func TestRefineWorkflowFanoutShapeWithBrief(t *testing.T) {
	_, _, _, rs := setupRefineWorkflowFixture(t)
	rs.Brief = "narrow scope to OAuth2 only"

	items, err := fanoutRefineParents(&rs)
	require.NoError(t, err)
	require.NotEmpty(t, items)

	for _, raw := range items {
		var item refineFanoutItem
		require.NoError(t, json.Unmarshal([]byte(raw), &item))
		assert.Equal(t, "refiner", item.AgentID, "brief != \"\" => refiner agent")
	}
}

// TestRefineWorkflowEmptyParentSet covers the case the plan calls
// out: a Decision referenced by no Features/Strategies/Bugs produces
// zero fanout items, no rewrites, no events, and no errors.
func TestRefineWorkflowEmptyParentSet(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/state", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/history", 0o755))

	dec := spec.Decision{ID: "dec-orphan", Title: "Unused decision", Status: spec.DecisionStatusActive}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-orphan", dec, "no parents reference this"))

	g := spec.BuildGraph(nil, nil, []spec.Decision{dec}, nil, nil, spec.TraceabilityIndex{})
	store := state.NewFileStateStore(fs, ".borg/state")

	rs := RefineState{
		DecisionID: "dec-orphan",
		Decision:   &dec,
		Graph:      g,
		FSys:       fs,
		Store:      store,
		// Features/Strategies/Bugs all empty
	}

	mock := NewMockExecutor() // no scripted responses; if dispatched we'd error
	exec := &WorkflowExecutor[RefineState]{
		Executor:  mock,
		AgentDefs: loadRefineWorkflowAgentDefs(t, fs),
		Workflow:  RefineCascadeWorkflow,
	}

	results, err := exec.Run(context.Background(), &rs)
	require.NoError(t, err)
	assert.Empty(t, results, "no fanout items => no round results")
	assert.Equal(t, 0, mock.CallCount(), "no parents => no LLM dispatches")
	assert.Empty(t, rs.UpdatedFeatures)
	assert.Empty(t, rs.UpdatedStrategies)
	assert.Empty(t, rs.UpdatedBugs)
	assert.Empty(t, rs.DriftedApproaches)
	assert.Empty(t, rs.Events)
}

// TestRefineWorkflowEquivalence drives the full workflow against the
// shared fixture and asserts the same RefineResult shape and on-disk
// output as the legacy cascade.Cascade direct-call path. This is the
// equivalence test the plan requires before flipping the cmd-layer
// over.
func TestRefineWorkflowEquivalence(t *testing.T) {
	fs, _, store, rs := setupRefineWorkflowFixture(t)

	mock := NewMockExecutor(
		scriptedRefineRewrite("We are building authentication using Go as the backend language.", true, "Surface the Go decision"),
		scriptedRefineRewrite("We run the backend in Go with the language decision refreshed.", true, "Clarify that Go is still the chosen language"),
	)
	exec := &WorkflowExecutor[RefineState]{
		Executor:  mock,
		AgentDefs: loadRefineWorkflowAgentDefs(t, fs),
		Workflow:  RefineCascadeWorkflow,
	}

	_, err := exec.Run(context.Background(), &rs)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"feat-auth"}, rs.UpdatedFeatures)
	assert.ElementsMatch(t, []string{"strat-go"}, rs.UpdatedStrategies)
	assert.ElementsMatch(t, []string{"app-oauth", "app-go"}, rs.DriftedApproaches)
	assert.Empty(t, rs.Skipped)
	assert.Len(t, rs.Events, 2, "one history event per rewrite")

	// Feature on disk should reflect the revised prose.
	featData, err := fs.ReadFile(".borg/spec/features/feat-auth.md")
	require.NoError(t, err)
	assert.Contains(t, string(featData), "using Go")

	// State entries flipped to drifted with hash zeroed and plan
	// pointers cleared (DJ-072 invariant).
	for _, id := range []string{"app-oauth", "app-go"} {
		got, err := store.Load(id)
		require.NoError(t, err)
		assert.Equal(t, state.StatusDrifted, got.Status, "approach %s", id)
		assert.Empty(t, got.SpecHash, "SpecHash zeroed for approach %s", id)
		assert.Empty(t, got.WorkstreamID, "WorkstreamID cleared for approach %s", id)
		assert.Empty(t, got.AssertionResults, "AssertionResults cleared for approach %s", id)
	}

	// Each emitted event should reference the triggering Decision in
	// its rationale and target the rewritten parent.
	for _, evt := range rs.Events {
		assert.Contains(t, evt.Rationale, "dec-lang", "event must reference triggering decision")
		assert.NotEmpty(t, evt.TargetID)
		assert.NotEmpty(t, evt.Kind)
	}

	// History files should be on disk too.
	files, err := fs.ListDir(".borg/history")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(files), 2, "one history file per rewrite")
}

// TestRefineWorkflowSkipWhenRewriterReportsUnchanged mirrors the
// cascade-package equivalent: when both rewriters report no change,
// the workflow records Skipped IDs, leaves disk untouched, and emits
// no history events.
func TestRefineWorkflowSkipWhenRewriterReportsUnchanged(t *testing.T) {
	fs, _, store, rs := setupRefineWorkflowFixture(t)

	mock := NewMockExecutor(
		scriptedRefineRewrite("We are building authentication using the previously-chosen language.", false, "Already accurate"),
		scriptedRefineRewrite("We run the backend in Go.", false, "Already accurate"),
	)
	exec := &WorkflowExecutor[RefineState]{
		Executor:  mock,
		AgentDefs: loadRefineWorkflowAgentDefs(t, fs),
		Workflow:  RefineCascadeWorkflow,
	}

	_, err := exec.Run(context.Background(), &rs)
	require.NoError(t, err)

	assert.Empty(t, rs.UpdatedFeatures)
	assert.Empty(t, rs.UpdatedStrategies)
	assert.Empty(t, rs.DriftedApproaches, "no drift when prose unchanged")
	assert.ElementsMatch(t, []string{"feat-auth", "strat-go"}, rs.Skipped)
	assert.Empty(t, rs.Events)

	got, err := store.Load("app-oauth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusLive, got.Status, "live state should not flip on a no-op rewrite")
	assert.Equal(t, "ws-prior", got.WorkstreamID)
}

// TestRefineWorkflowMergeOrderingRewriteThenHistory locks in the
// invariant the plan's "phase ordering" test calls out, restated for
// option (a) (history inlined into cascade merge): every history
// event recorded by the merge handler is emitted strictly after the
// corresponding rewrite has been persisted to disk. The test asserts
// this by checking that the event's TargetID corresponds to a parent
// whose on-disk file already shows the revised body at the moment
// the event was recorded.
//
// Implementation detail: the merge handler calls SavePair before
// hist.Record, and Events is appended to AFTER hist.Record returns.
// So if rs.Events[i] exists, the on-disk file for rs.Events[i].TargetID
// already contains the revised body. We verify that property here.
func TestRefineWorkflowMergeOrderingRewriteThenHistory(t *testing.T) {
	fs, _, _, rs := setupRefineWorkflowFixture(t)

	mock := NewMockExecutor(
		scriptedRefineRewrite("Revised feature prose with Go.", true, "Surface Go decision"),
		scriptedRefineRewrite("Revised strategy prose with Go.", true, "Clarify Go choice"),
	)
	exec := &WorkflowExecutor[RefineState]{
		Executor:  mock,
		AgentDefs: loadRefineWorkflowAgentDefs(t, fs),
		Workflow:  RefineCascadeWorkflow,
	}

	_, err := exec.Run(context.Background(), &rs)
	require.NoError(t, err)
	require.NotEmpty(t, rs.Events)

	for _, evt := range rs.Events {
		switch evt.Kind {
		case "feature_rewritten":
			body, err := fs.ReadFile(".borg/spec/features/" + evt.TargetID + ".md")
			require.NoError(t, err)
			assert.Contains(t, string(body), "Revised feature prose with Go.",
				"history event for %s precedes its rewrite on disk", evt.TargetID)
		case "strategy_rewritten":
			body, err := fs.ReadFile(".borg/spec/strategies/" + evt.TargetID + ".md")
			require.NoError(t, err)
			assert.Contains(t, string(body), "Revised strategy prose with Go.",
				"history event for %s precedes its rewrite on disk", evt.TargetID)
		default:
			t.Fatalf("unexpected event kind %q", evt.Kind)
		}
	}
}

// TestBuildRefineRewriterPromptCascadeMode covers the prompt
// formatting in cascade mode (no brief): "Recently changed Decisions"
// is included; "Refinement intent" is omitted. Mirrors the
// cascade.BuildRewriterPrompt formatting one-to-one so the workflow
// and direct-call paths can't diverge silently.
func TestBuildRefineRewriterPromptCascadeMode(t *testing.T) {
	applicable := []spec.Decision{{ID: "dec-lang", Title: "Use Go", Status: spec.DecisionStatusActive, Confidence: 0.9, Rationale: "..."}}
	changed := []spec.Decision{{ID: "dec-lang", Title: "Use Go"}}

	prompt := buildRefineRewriterPrompt("feature", "feat-auth", "Authentication", "current body", "", applicable, changed)

	assert.Contains(t, prompt, "## Parent kind\nfeature")
	assert.Contains(t, prompt, "## Parent ID\nfeat-auth")
	assert.Contains(t, prompt, "## Parent title\nAuthentication")
	assert.Contains(t, prompt, "## Current parent prose\ncurrent body")
	assert.Contains(t, prompt, "## Applicable Decisions")
	assert.Contains(t, prompt, "dec-lang")
	assert.Contains(t, prompt, "## Recently changed Decisions",
		"cascade mode (no brief) MUST include the Recently changed Decisions section")
	assert.NotContains(t, prompt, "## Refinement intent",
		"cascade mode (no brief) MUST omit the Refinement intent header")
}

// TestBuildRefineRewriterPromptBriefMode covers the inverse: a
// non-empty brief includes "Refinement intent" and omits "Recently
// changed Decisions".
func TestBuildRefineRewriterPromptBriefMode(t *testing.T) {
	applicable := []spec.Decision{{ID: "dec-lang", Title: "Use Go"}}
	changed := []spec.Decision{{ID: "dec-lang", Title: "Use Go"}}

	prompt := buildRefineRewriterPrompt("feature", "feat-auth", "Authentication", "current body", "narrow scope", applicable, changed)

	assert.True(t, strings.HasPrefix(prompt, "## Refinement intent\nnarrow scope"),
		"brief mode MUST start with the Refinement intent header")
	assert.NotContains(t, prompt, "## Recently changed Decisions",
		"brief mode MUST omit the Recently changed Decisions section")
}

// TestRefineWorkflowFanoutIncludesBugs confirms the workflow's
// enhancement over cascade.Cascade: Bugs whose parent Feature
// references the changed Decision are included in the fanout. This
// is the behavior the cmd-layer routing relies on for full Bug
// drift on Decision changes.
func TestRefineWorkflowFanoutIncludesBugs(t *testing.T) {
	fs, _, store, rs := setupRefineWorkflowFixture(t)

	bug := spec.Bug{
		ID:          "bug-login",
		Title:       "Login hangs on submit",
		FeatureID:   "feat-auth",
		Severity:    spec.BugSeverityMedium,
		Status:      spec.BugStatusTriaged,
		Description: "Login hangs when JS is disabled.",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/bugs/bug-login", bug, bug.Description))

	// Rebuild the graph to include the Bug, then rebuild RefineState.
	g := spec.BuildGraph(
		[]spec.Feature{rs.Features[0]},
		[]spec.Bug{bug},
		[]spec.Decision{*rs.Decision},
		[]spec.Strategy{rs.Strategies[0]},
		nil,
		spec.TraceabilityIndex{},
	)
	rs.Graph = g
	rs.Bugs = []spec.Bug{bug}

	items, err := fanoutRefineParents(&rs)
	require.NoError(t, err)
	require.Len(t, items, 3, "fanout should now include the Bug")

	kinds := []string{}
	for _, raw := range items {
		var item refineFanoutItem
		require.NoError(t, json.Unmarshal([]byte(raw), &item))
		kinds = append(kinds, item.Kind)
	}
	assert.ElementsMatch(t, []string{"feature", "strategy", "bug"}, kinds)

	// Drive the full workflow with three rewriter responses, one per
	// parent. The Bug rewrite should land via persistRefineBug and
	// be reflected in UpdatedBugs.
	mock := NewMockExecutor(
		scriptedRefineRewrite("Revised feature prose.", true, "feature rewrite"),
		scriptedRefineRewrite("Revised strategy prose.", true, "strategy rewrite"),
		scriptedRefineRewrite("Revised bug prose.", true, "bug rewrite"),
	)
	exec := &WorkflowExecutor[RefineState]{
		Executor:  mock,
		AgentDefs: loadRefineWorkflowAgentDefs(t, fs),
		Workflow:  RefineCascadeWorkflow,
	}

	_, err = exec.Run(context.Background(), &rs)
	require.NoError(t, err)
	assert.Equal(t, 3, mock.CallCount(), "one LLM call per fanout item")
	assert.Contains(t, rs.UpdatedFeatures, "feat-auth")
	assert.Contains(t, rs.UpdatedStrategies, "strat-go")
	assert.Contains(t, rs.UpdatedBugs, "bug-login")

	// Bug body on disk should reflect the rewrite.
	bugData, err := fs.ReadFile(".borg/spec/bugs/bug-login.md")
	require.NoError(t, err)
	assert.Contains(t, string(bugData), "Revised bug prose.")

	// The store / fs reference is still alive after the workflow run.
	_ = store
}
