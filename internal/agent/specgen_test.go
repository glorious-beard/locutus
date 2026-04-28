package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testWorkflowYAML mirrors internal/scaffold/workflows/spec_generation.yaml
// at the level of detail the executor cares about — kept inline so tests
// don't depend on the scaffold package (which would create an import
// cycle: scaffold imports agent).
const testWorkflowYAML = `rounds:
  - id: survey
    agent: spec_scout
    merge_as: scout_brief
  - id: propose
    agent: spec_architect
    depends_on: [survey]
    merge_as: proposed_spec
  - id: critique
    agents: [architect_critic, devops_critic, sre_critic, cost_critic]
    parallel: true
    depends_on: [propose]
    merge_as: critic_issues
  - id: revise
    agent: spec_architect
    depends_on: [critique]
    conditional: has_concerns
    merge_as: revisions
max_rounds: 1
`

// minAgentMD builds a minimal agent .md file that LoadAgentDefs can
// parse. We don't care about the prose body in tests — the mock LLM is
// what produces output; the agent def just needs to declare model tier
// + output schema so BuildGenerateRequest wires the request correctly.
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

// setupSpecGenFixture builds a MemFS with the six council agents and the
// spec-generation workflow YAML pre-populated. Tests use this as the
// fsys argument to GenerateSpec.
func setupSpecGenFixture(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/agents", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/workflows", 0o755))
	for _, a := range []struct{ id, role, cap, schema string }{
		{"spec_scout", "survey", "balanced", "ScoutBrief"},
		{"spec_architect", "planning", "strong", "SpecProposal"},
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
	require.NoError(t, fs.WriteFile(".borg/workflows/spec_generation.yaml", []byte(testWorkflowYAML), 0o644))
	return fs
}

// Canonical mock-response shorthands so each test reads as a sequence
// rather than a wall of JSON.
const (
	scoutResp     = `{"domain_read":"a project","technology_options":["x: a vs b"],"implicit_assumptions":["scale: 100k. Default: 1k concurrent"],"watch_outs":["x"]}`
	criticEmpty   = `{"issues":[]}`
	criticDangler = `{"issues":["feature feat-x references dec-missing but it is not generated"]}`
)

func TestGenerateSpecRequiresGoals(t *testing.T) {
	mock := NewMockLLM()
	_, err := GenerateSpec(context.Background(), mock, nil, SpecGenRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GoalsBody is required")
}

func TestGenerateSpecRequiresFSys(t *testing.T) {
	mock := NewMockLLM()
	_, err := GenerateSpec(context.Background(), mock, nil, SpecGenRequest{GoalsBody: "build it"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fsys is required")
}

func TestGenerateSpecCleanProposalSkipsRevise(t *testing.T) {
	// scout → proposer → 4 critics (all empty) → no revise = 6 calls.
	proposal := `{
		"features": [{"id":"feat-x","title":"X","description":"a feature","decisions":["dec-x"]}],
		"decisions": [{"id":"dec-x","title":"D","rationale":"r","confidence":0.8,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why"}]}],
		"strategies": [{"id":"strat-x","title":"S","kind":"foundational","body":"prose"}],
		"approaches": [{"id":"app-x","title":"A","parent_id":"feat-x","body":"sketch"}]
	}`
	mock := NewMockLLM(
		MockResponse{Response: &GenerateResponse{Content: scoutResp, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: proposal, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
	)
	fs := setupSpecGenFixture(t)

	out, err := GenerateSpec(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build something useful.",
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(out.Features))
	assert.Equal(t, "feat-x", out.Features[0].ID)
	assert.Equal(t, 6, mock.CallCount(), "six calls when critics return empty: scout + proposer + 4 critics, no revise")
}

func TestGenerateSpecCritiqueRevisesProposal(t *testing.T) {
	// scout → proposer (dangling ref) → 4 critics (1 flags, 3 empty) →
	// revise → 7 calls.
	proposerInitial := `{"features":[{"id":"feat-x","title":"X","decisions":["dec-missing"]}],"decisions":[]}`
	proposerRevised := `{"features":[{"id":"feat-x","title":"X","decisions":["dec-missing"]}],"decisions":[{"id":"dec-missing","title":"Missing","rationale":"r","confidence":0.7,"alternatives":[{"name":"alt","rationale":"r","rejected_because":"why"}]}]}`
	mock := NewMockLLM(
		MockResponse{Response: &GenerateResponse{Content: scoutResp, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: proposerInitial, Model: "m"}},
		// Four critic responses: one flags, three are empty. Order is
		// non-deterministic across goroutines, but the count is fixed.
		MockResponse{Response: &GenerateResponse{Content: criticDangler, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: proposerRevised, Model: "m"}},
	)
	fs := setupSpecGenFixture(t)

	out, err := GenerateSpec(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build something.",
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(out.Features))
	require.Equal(t, 1, len(out.Decisions),
		"critic flagged the missing decision; revise should have generated it")
	assert.Equal(t, "dec-missing", out.Decisions[0].ID)
	assert.Equal(t, 7, mock.CallCount(),
		"seven calls when critics flag issues: scout + proposer + 4 critics + revise")
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
	mock := NewMockLLM(
		MockResponse{Response: &GenerateResponse{Content: scout, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: `{"features":[]}`, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: criticEmpty, Model: "m"}},
	)
	fs := setupSpecGenFixture(t)

	_, err := GenerateSpec(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "Build something.",
	})
	require.NoError(t, err)

	// Find the proposer call. Order: 0=scout, 1=propose, 2-5=critics.
	calls := mock.Calls()
	require.GreaterOrEqual(t, len(calls), 2)
	proposerUser := calls[1].Request.Messages[len(calls[1].Request.Messages)-1].Content
	assert.Contains(t, proposerUser, "electoral campaign tooling",
		"proposer should see the scout's domain_read")
	assert.Contains(t, proposerUser, "scale: 100k registered, 1k concurrent",
		"proposer should see the scout's implicit_assumptions to commit to them")
	assert.Contains(t, proposerUser, "BigQuery costs at scale",
		"proposer should see the scout's watch_outs")
}

func TestGenerateSpecCriticIssuesReachReviser(t *testing.T) {
	// Each critic emits CriticIssues; merge_as=critic_issues flattens
	// each issue into a Concern entry. The revise call (proposer round
	// 2) should then see the concerns formatted in its user message.
	scout := `{"domain_read":"x"}`
	proposerInitial := `{"features":[{"id":"feat-x","title":"X","decisions":["dec-x"]}],"decisions":[{"id":"dec-x","title":"D","rationale":"r","confidence":0.8}]}`
	mock := NewMockLLM(
		MockResponse{Response: &GenerateResponse{Content: scout, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: proposerInitial, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: `{"issues":["arch issue"]}`, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: `{"issues":["devops issue"]}`, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: `{"issues":["sre issue"]}`, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: `{"issues":["cost issue"]}`, Model: "m"}},
		MockResponse{Response: &GenerateResponse{Content: proposerInitial, Model: "m"}},
	)
	fs := setupSpecGenFixture(t)

	_, err := GenerateSpec(context.Background(), mock, fs, SpecGenRequest{
		GoalsBody: "x",
	})
	require.NoError(t, err)

	calls := mock.Calls()
	require.Equal(t, 7, len(calls), "scout + proposer + 4 critics + revise")
	// Last call is the revise; its user content should mention every
	// critic's issue, with role-based attribution from projectRevise.
	revise := calls[6].Request.Messages
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

	warnings := p.ValidateAndStrip(nil)
	assert.Equal(t, []string{"dec-real"}, p.Features[0].Decisions, "missing dec ref should be stripped")
	require.Equal(t, 1, len(warnings))
	assert.Equal(t, "feature", warnings[0].NodeKind)
	assert.Equal(t, "decisions", warnings[0].Field)
	assert.Equal(t, "dec-missing", warnings[0].MissingID)
}

func TestValidateAndStripDropsApproachWithMissingParent(t *testing.T) {
	p := &SpecProposal{
		Features: []FeatureProposal{
			{ID: "feat-a", Title: "A"},
		},
		Approaches: []ApproachProposal{
			{ID: "app-real", Title: "Real", ParentID: "feat-a"},
			{ID: "app-orphan", Title: "Orphan", ParentID: "feat-missing"},
		},
	}

	warnings := p.ValidateAndStrip(nil)
	require.Equal(t, 1, len(p.Approaches), "orphan approach should be dropped")
	assert.Equal(t, "app-real", p.Approaches[0].ID)

	require.Equal(t, 1, len(warnings))
	assert.Equal(t, "approach", warnings[0].NodeKind)
	assert.Equal(t, "parent_id", warnings[0].Field)
	assert.Equal(t, "feat-missing", warnings[0].MissingID)
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

	warnings := p.ValidateAndStrip(existing)
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
				Span:      "lines 6-8",
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
	assert.Equal(t, "lines 6-8", d.Provenance.Citations[0].Span)
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
		Approaches: []ApproachProposal{
			{ID: "app-a", Title: "A", ParentID: "feat-a", Body: "sketch"},
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
	assert.Equal(t, "sketch", r.Approaches[0].Body)
}
