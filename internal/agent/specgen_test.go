package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecProposalNoApproaches(t *testing.T) {
	// Approaches were removed from the spec-generation council's output
	// per the council-resilience plan: they're synthesized at adopt
	// time when real code context exists. A regression here would
	// silently re-introduce the dangling-reference problem.
	for _, name := range []string{"Approaches"} {
		_, ok := reflect.TypeOf(SpecProposal{}).FieldByName(name)
		assert.False(t, ok, "SpecProposal must not have field %q", name)
		_, ok = reflect.TypeOf(FeatureProposal{}).FieldByName(name)
		assert.False(t, ok, "FeatureProposal must not have field %q", name)
		_, ok = reflect.TypeOf(StrategyProposal{}).FieldByName(name)
		assert.False(t, ok, "StrategyProposal must not have field %q", name)
	}
}

// testHasConcerns is a test-only conditional matching the simpler
// pre-DJ-098 shape — revise fires when any concern was raised by
// challenge or critique. Production uses hasUnmatchedFindings /
// hasFindingClusters via DJ-098's per-cluster fanout.
func testHasConcerns(s *PlanningState) bool { return len(s.Concerns) > 0 }

// testMergeRawProposal stores the architect's raw output verbatim.
// The simpler test workflow goes architect → reconcile (no
// outline + elaborate fanout), so this maps the architect's response
// straight into RawProposal where the reconciler reads it.
func testMergeRawProposal(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.RawProposal = v
	}
}

// testSpecGenWorkflow is a simplified spec-generation workflow used by
// the GenerateSpec tests so each assertion can exercise a specific
// behavior (event bridging, scout-brief threading, critic→revise wiring)
// without setting up the full DJ-098 outline + per-cluster fanout. The
// shape mirrors the pre-DJ-098 council:
//
//	survey → propose → reconcile → critique → revise → reconcile_revise.
//
// Production callers always go through SpecGenerationWorkflow.
var testSpecGenWorkflow = &Workflow[PlanningState]{
	Rounds: []WorkflowStep[PlanningState]{
		{ID: "survey", Agents: []string{"spec_scout"}, Project: projectDefault, Merge: mergeScoutBrief},
		{ID: "propose", Agents: []string{"spec_architect"}, DependsOn: []string{"survey"}, Project: projectPropose, Merge: testMergeRawProposal},
		{ID: "reconcile", Agents: []string{"spec_reconciler"}, DependsOn: []string{"propose"}, Project: projectReconcile, Merge: mergeReconciledProposal},
		{ID: "critique", Agents: []string{"architect_critic", "devops_critic", "sre_critic", "cost_critic"}, Parallel: true, DependsOn: []string{"reconcile"}, Project: projectChallenge, Merge: mergeCriticIssues},
		{ID: "revise", Agents: []string{"spec_architect"}, DependsOn: []string{"critique"}, Conditional: testHasConcerns, Project: projectRevise, Merge: testMergeRawProposal},
		{ID: "reconcile_revise", Agents: []string{"spec_reconciler"}, DependsOn: []string{"revise"}, Conditional: testHasConcerns, Project: projectReconcile, Merge: mergeReconciledProposal},
	},
	MaxRounds: 1,
}

// minAgentMD builds a minimal agent .md file that LoadAgentDefs can
// parse. We don't care about the prose body in tests — the mock LLM is
// what produces output; the agent def just needs to declare model tier
// + output schema so BuildAgentInput wires the request correctly.
func minAgentMD(id, role, capability, schema string) string {
	return fmt.Sprintf(`---
id: %s
role: %s
capability: %s
temperature: 0.3
output_schema: %s
---
Test agent %s.
`, id, role, capability, schema, id)
}

// setupSpecGenFixture builds a MemFS with the six council agents
// testSpecGenWorkflow expects. Tests pass this fs alongside
// testSpecGenWorkflow to generateSpecWithWorkflow.
func setupSpecGenFixture(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/agents", 0o755))
	for _, a := range []struct{ id, role, cap, schema string }{
		{"spec_scout", "survey", "balanced", "ScoutBrief"},
		{"spec_architect", "planning", "strong", "RawSpecProposal"},
		{"spec_reconciler", "reconcile", "balanced", "ReconciliationVerdict"},
		{"architect_critic", "review", "balanced", "CriticIssues"},
		{"devops_critic", "review", "balanced", "CriticIssues"},
		{"sre_critic", "review", "balanced", "CriticIssues"},
		{"cost_critic", "review", "balanced", "CriticIssues"},
	} {
		require.NoError(t, fs.WriteFile(
			fmt.Sprintf(".borg/agents/%s.md", a.id),
			[]byte(minAgentMD(a.id, a.role, a.cap, a.schema)),
			0o644))
	}
	return fs
}

// Canonical mock-response shorthands so each test reads as a sequence
// rather than a wall of JSON.
const (
	scoutResp     = `{"domain_read":"a project","technology_options":["x: a vs b"],"implicit_assumptions":["scale: 100k. Default: 1k concurrent"],"watch_outs":["x"]}`
	criticEmpty   = `{"issues":[]}`
	criticDangler = `{"issues":["feature feat-x references dec-missing but it is not generated"]}`
	// rawProposalCanonical is a RawSpecProposal under DJ-124: one feature
	// + one strategy referencing a top-level Decision by id. After
	// ApplyReconciliation (with an empty verdict), the top-level
	// RawDecisionProposal is field-mapped to a canonical Decision and
	// the feature / strategy references are preserved.
	rawProposalCanonical = `{
		"features": [{"id":"feat-x","title":"X","description":"a feature","decisions":["dec-use-d"]}],
		"strategies": [{"id":"strat-x","title":"S","kind":"foundational","body":"prose","decisions":["dec-use-d"]}],
		"decisions": [{"id":"dec-use-d","title":"Use D","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why"}]}]
	}`
	// reconcileEmpty is the reconciler's "no merging needed" verdict.
	// Verdict content is no-op under DJ-124; the value is preserved
	// for compatibility with the unchanged reconciler agent prompt.
	reconcileEmpty = `{"actions":[]}`
)

func TestGenerateSpecRequiresGoals(t *testing.T) {
	mock := NewMockExecutor()
	_, err := GenerateSpec(context.Background(), mock, nil, SpecGenRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GoalsBody is required")
}

func TestGenerateSpecRequiresFSys(t *testing.T) {
	mock := NewMockExecutor()
	_, err := GenerateSpec(context.Background(), mock, nil, SpecGenRequest{GoalsBody: "build it"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fsys is required")
}

func TestGenerateSpecCleanProposalSkipsRevise(t *testing.T) {
	// Phase 2 flow: scout → propose → reconcile → 4 critics (all empty)
	// → no revise → no reconcile_revise = 7 calls.
	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: scoutResp, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: rawProposalCanonical, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: reconcileEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
	)
	fs := setupSpecGenFixture(t)

	out, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build something useful.",
	}, testSpecGenWorkflow)
	require.NoError(t, err)
	require.Equal(t, 1, len(out.Features))
	assert.Equal(t, "feat-x", out.Features[0].ID)
	require.Equal(t, 1, len(out.Decisions),
		"empty verdict ⇒ one canonical decision per inline decision; one inline decision in fixture")
	assert.Equal(t, "dec-use-d", out.Decisions[0].ID,
		"canonical IDs are slug-derived from the inline decision's title")
	assert.Equal(t, []string{"dec-use-d"}, out.Features[0].Decisions,
		"feature should reference the canonical decision id assigned by the reconciler")
	assert.Equal(t, 7, mock.CallCount(),
		"seven calls when critics return empty: scout + propose + reconcile + 4 critics, no revise")
}

func TestGenerateSpecBridgesEventsToSink(t *testing.T) {
	// Phase 2 clean-proposal flow used elsewhere; we only care that the
	// workflow's events make it through to the supplied EventSink. This
	// validates the bridging goroutine in GenerateSpec (channel → sink)
	// and confirms Close() fires after the run.
	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: scoutResp, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: rawProposalCanonical, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: reconcileEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
	)
	fs := setupSpecGenFixture(t)

	sink := &CapturingSink{}
	_, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build something useful.",
		Sink:      sink,
	}, testSpecGenWorkflow)
	require.NoError(t, err)

	events := sink.Events()
	require.NotEmpty(t, events, "sink should have received at least one started/completed event")

	// Per-agent events have AgentID set; iteration / DAG step events
	// don't. Filter to agent-level events so the assertions are stable
	// regardless of the surrounding workflow scaffolding.
	var agentStarted, agentCompleted int
	seenAgents := map[string]bool{}
	for _, e := range events {
		if e.AgentID == "" {
			continue
		}
		seenAgents[e.AgentID] = true
		switch e.Status {
		case "started":
			agentStarted++
		case "completed":
			agentCompleted++
		}
	}
	assert.Equal(t, agentStarted, agentCompleted,
		"every agent started should pair with a completed in a clean run")
	assert.GreaterOrEqual(t, agentStarted, 7,
		"seven agents (scout + proposer + reconciler + 4 critics) should each emit started+completed")
	for _, want := range []string{"spec_scout", "spec_architect", "spec_reconciler", "architect_critic", "devops_critic", "sre_critic", "cost_critic"} {
		assert.True(t, seenAgents[want], "expected events for agent %q", want)
	}

	assert.False(t, sink.Closed(),
		"GenerateSpec must NOT close the caller's sink — the cmd layer keeps it open for post-workflow direct LLM calls and is responsible for closing it itself")
}

func TestValidateIsPure(t *testing.T) {
	// Validate must return warnings without mutating the proposal —
	// callers rely on a clean snapshot for the integrity-revise loop.
	p := &SpecProposal{
		Features: []FeatureProposal{
			{ID: "feat-x", Title: "X", Decisions: []string{"dec-missing"}},
		},
	}
	original := *p
	originalDecisions := append([]string{}, p.Features[0].Decisions...)

	warnings := p.Validate(nil)
	require.Len(t, warnings, 1)
	assert.Equal(t, "dec-missing", warnings[0].MissingID)

	// Proposal must be unchanged.
	assert.Equal(t, original.Features[0].ID, p.Features[0].ID)
	assert.Equal(t, originalDecisions, p.Features[0].Decisions,
		"Validate must not strip refs — Strip is the destructive variant")
}

func TestStripMutates(t *testing.T) {
	p := &SpecProposal{
		Features: []FeatureProposal{
			{ID: "feat-x", Title: "X", Decisions: []string{"dec-missing", "dec-real"}},
		},
		Decisions: []DecisionProposal{
			{ID: "dec-real", Title: "Real"},
		},
	}
	warnings := p.Strip(nil)
	require.Len(t, warnings, 1)
	assert.Equal(t, "dec-missing", warnings[0].MissingID)
	assert.Equal(t, []string{"dec-real"}, p.Features[0].Decisions,
		"dangling ref must be stripped, valid ref preserved")
}

func TestGenerateSpecCritiqueRevisesProposal(t *testing.T) {
	// Phase 2 flow with critic findings:
	// scout → propose (raw) → reconcile → 4 critics (1 flags, 3 empty)
	// → revise → reconcile_revise = 9 calls.
	rawRevised := `{
		"features": [{"id":"feat-x","title":"X","description":"a feature","decisions":["dec-use-d","dec-cache-reads"]}],
		"strategies": [{"id":"strat-x","title":"S","kind":"foundational","body":"prose","decisions":["dec-use-d"]}],
		"decisions": [{"id":"dec-use-d","title":"Use D","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why"}]},{"id":"dec-cache-reads","title":"Cache reads","rationale":"r","confidence":0.7,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why"}]}]
	}`
	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: scoutResp, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: rawProposalCanonical, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: reconcileEmpty, Model: "m"}},
		// Four critic responses: one flags, three are empty. Order is
		// non-deterministic across goroutines, but the count is fixed.
		MockResponse{Response: &AgentOutput{Content: criticDangler, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		// revise emits a new RawSpecProposal.
		MockResponse{Response: &AgentOutput{Content: rawRevised, Model: "m"}},
		// reconcile_revise emits an empty verdict (no clusters need merging).
		MockResponse{Response: &AgentOutput{Content: reconcileEmpty, Model: "m"}},
	)
	fs := setupSpecGenFixture(t)

	out, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build something.",
	}, testSpecGenWorkflow)
	require.NoError(t, err)
	require.Equal(t, 1, len(out.Features))
	require.Equal(t, 2, len(out.Decisions),
		"revise added a second inline decision; reconcile_revise minted a separate canonical id for it")
	assert.Equal(t, 9, mock.CallCount(),
		"nine calls when critics flag issues: scout + propose + reconcile + 4 critics + revise + reconcile_revise")
}

func TestGenerateSpecScoutBriefReachesProposer(t *testing.T) {
	// The scout's structured output should land in the proposer's user
	// message in human-readable form (rendered by formatScoutBrief).
	scout := `{
		"domain_read":"electoral campaign tooling",
		"technology_options":["frontend: Next.js vs Remix"],
		"implicit_assumptions":["scale: 100k registered, 1k concurrent. Default: small team"],
		"watch_outs":["BigQuery costs at scale"]
	}`
	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: scout, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: rawProposalCanonical, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: reconcileEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: criticEmpty, Model: "m"}},
	)
	fs := setupSpecGenFixture(t)

	_, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build something.",
	}, testSpecGenWorkflow)
	require.NoError(t, err)

	// Find the proposer call. Order: 0=scout, 1=propose, 2=reconcile, 3-6=critics.
	calls := mock.Calls()
	require.GreaterOrEqual(t, len(calls), 2)
	proposerUser := calls[1].Input.Messages[len(calls[1].Input.Messages)-1].Content
	assert.Contains(t, proposerUser, "electoral campaign tooling",
		"proposer should see the scout's domain_read")
	assert.Contains(t, proposerUser, "scale: 100k registered, 1k concurrent",
		"proposer should see the scout's implicit_assumptions to commit to them")
	assert.Contains(t, proposerUser, "BigQuery costs at scale",
		"proposer should see the scout's watch_outs")
}

func TestGenerateSpecCriticIssuesReachReviser(t *testing.T) {
	// Each critic emits CriticIssues; merge_as=critic_issues flattens
	// each issue into a Concern entry. The revise call (architect round
	// 2) should then see the concerns formatted in its user message.
	scout := `{"domain_read":"x"}`
	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: scout, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: rawProposalCanonical, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: reconcileEmpty, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: `{"issues":["arch issue"]}`, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: `{"issues":["devops issue"]}`, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: `{"issues":["sre issue"]}`, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: `{"issues":["cost issue"]}`, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: rawProposalCanonical, Model: "m"}},
		MockResponse{Response: &AgentOutput{Content: reconcileEmpty, Model: "m"}},
	)
	fs := setupSpecGenFixture(t)

	_, err := generateSpecWithWorkflow(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "x",
	}, testSpecGenWorkflow)
	require.NoError(t, err)

	calls := mock.Calls()
	require.Equal(t, 9, len(calls),
		"scout + propose + reconcile + 4 critics + revise + reconcile_revise = 9")
	// The revise call is at index 7 (after the 4 critics). Its user
	// content should mention every critic's issue, with role-based
	// attribution from projectRevise.
	revise := calls[7].Input.Messages
	revisePrompt := strings.Join(messageContents(revise), "\n")
	assert.Contains(t, revisePrompt, "arch issue")
	assert.Contains(t, revisePrompt, "devops issue")
	assert.Contains(t, revisePrompt, "sre issue")
	assert.Contains(t, revisePrompt, "cost issue")
}

func messageContents(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func TestValidateAndStripRemovesDanglingDecisionRefs(t *testing.T) {
	p := &SpecProposal{
		Features: []FeatureProposal{
			{ID: "feat-a", Title: "A", Decisions: []string{"dec-real", "dec-missing"}},
		},
		Decisions: []DecisionProposal{
			{ID: "dec-real", Title: "Real"},
		},
	}

	warnings := p.Strip(nil)
	assert.Equal(t, []string{"dec-real"}, p.Features[0].Decisions, "missing dec ref should be stripped")
	require.Equal(t, 1, len(warnings))
	assert.Equal(t, "feature", warnings[0].NodeKind)
	assert.Equal(t, "decisions", warnings[0].Field)
	assert.Equal(t, "dec-missing", warnings[0].MissingID)
}

func TestValidateAndStripResolvesAgainstExistingSpec(t *testing.T) {
	// A reference to an id that's only in the existing spec snapshot
	// (not in the new proposal) is still valid — the LLM is extending.
	p := &SpecProposal{
		Features: []FeatureProposal{
			{ID: "feat-a", Title: "A", Decisions: []string{"dec-existing"}},
		},
	}
	existing := &ExistingSpec{
		Decisions: []spec.Decision{{ID: "dec-existing", Title: "Existing"}},
	}

	warnings := p.Strip(existing)
	assert.Empty(t, warnings)
	assert.Equal(t, []string{"dec-existing"}, p.Features[0].Decisions)
}

func TestToAssimilationResultDenormalizesProvenance(t *testing.T) {
	// Citations + ArchitectRationale on the proposal must land on the
	// persisted spec.Decision so the spec is self-contained — deleting
	// .locutus/sessions/ never costs the project its justification.
	p := &SpecProposal{
		Decisions: []DecisionProposal{{
			ID:         "dec-frontend",
			Title:      "Use TanStack Start",
			Rationale:  "Long-form rationale paragraph.",
			Confidence: 0.9,
			Alternatives: []spec.Alternative{{
				Name: "Next.js", Rationale: "mature", RejectedBecause: "too heavy",
			}},
			Citations: []spec.Citation{{
				Kind:      "goals",
				Reference: "GOALS.md",
				Excerpt:   "Use Tanstack Start, Tanstack Query, Tanstack Table",
			}},
			ArchitectRationale: "GOALS.md mandates TanStack Start.",
		}},
	}

	r := p.ToAssimilationResult()
	require.Equal(t, 1, len(r.Decisions))
	d := r.Decisions[0]

	require.NotNil(t, d.Provenance, "provenance should be denormalized onto the persisted decision")
	require.Equal(t, 1, len(d.Provenance.Citations))
	assert.Equal(t, "goals", d.Provenance.Citations[0].Kind)
	assert.Equal(t, "GOALS.md", d.Provenance.Citations[0].Reference)
	assert.Equal(t, "Use Tanstack Start, Tanstack Query, Tanstack Table", d.Provenance.Citations[0].Excerpt,
		"excerpt should be persisted verbatim so the citation survives the source moving")
	assert.Equal(t, "GOALS.md mandates TanStack Start.", d.Provenance.ArchitectRationale)
}

func TestToAssimilationResultLeavesProvenanceNilWhenAbsent(t *testing.T) {
	// A DecisionProposal without citations or architect_rationale (e.g.
	// from older council output, or hand-authored input) should not
	// land with a hollow Provenance{} on the persisted Decision —
	// nil signals "no denormalized provenance," distinguishable from
	// "council ran but produced nothing."
	p := &SpecProposal{
		Decisions: []DecisionProposal{{
			ID:        "dec-bare",
			Title:     "Bare decision",
			Rationale: "no citations on this one",
		}},
	}
	r := p.ToAssimilationResult()
	require.Equal(t, 1, len(r.Decisions))
	assert.Nil(t, r.Decisions[0].Provenance)
}

func TestToAssimilationResultStampsProposedStatus(t *testing.T) {
	p := &SpecProposal{
		Features:   []FeatureProposal{{ID: "feat-a", Title: "A"}},
		Decisions:  []DecisionProposal{{ID: "dec-a", Title: "D"}},
		Strategies: []StrategyProposal{{ID: "strat-a", Title: "S", Kind: "foundational"}},
	}
	r := p.ToAssimilationResult()
	assert.Equal(t, spec.FeatureStatusProposed, r.Features[0].Status,
		"greenfield generation should mark features `proposed`, not `inferred`")
	assert.Equal(t, spec.DecisionStatusProposed, r.Decisions[0].Status)
	assert.Equal(t, "proposed", r.Strategies[0].Status)
}

func TestSpecProposalToAssimilationResultRoundTrip(t *testing.T) {
	p := &SpecProposal{
		Features: []FeatureProposal{
			{ID: "feat-a", Title: "A", Description: "desc", Decisions: []string{"dec-a"}},
		},
		Decisions: []DecisionProposal{
			{ID: "dec-a", Title: "D", Rationale: "r", Confidence: 0.7},
		},
		Strategies: []StrategyProposal{
			{ID: "strat-a", Title: "S", Kind: "foundational", Body: "body"},
		},
	}

	r := p.ToAssimilationResult()
	require.NotNil(t, r)
	assert.Equal(t, "feat-a", r.Features[0].ID)
	assert.Equal(t, "desc", r.Features[0].Description)
	assert.Equal(t, []string{"dec-a"}, r.Features[0].Decisions)
	assert.Equal(t, "dec-a", r.Decisions[0].ID)
	assert.Equal(t, 0.7, r.Decisions[0].Confidence)
	assert.Equal(t, spec.StrategyKind("foundational"), r.Strategies[0].Kind)
}
