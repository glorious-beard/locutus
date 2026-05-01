package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegrityCriticAppendsFindings — when the post-reconcile
// ProposedSpec carries a dangling reference, the mechanical critic
// surfaces it as a Concern with Kind="integrity" so revise can address
// it in-workflow rather than waiting for the post-workflow integrity
// loop.
func TestIntegrityCriticAppendsFindings(t *testing.T) {
	dangling := SpecProposal{
		Features: []FeatureProposal{
			{ID: "feat-x", Title: "X", Decisions: []string{"dec-missing"}},
		},
	}
	raw, err := json.Marshal(dangling)
	require.NoError(t, err)

	state := &PlanningState{ProposedSpec: string(raw)}
	step := WorkflowStep{ID: "critique", MergeAs: "critic_issues"}
	mergeResults(state, step, nil) // no LLM critic results, only the integrity pass

	require.Len(t, state.Concerns, 1, "integrity critic should flag the one dangling reference")
	c := state.Concerns[0]
	assert.Equal(t, "integrity_critic", c.AgentID)
	assert.Equal(t, "integrity", c.Kind)
	assert.Equal(t, "high", c.Severity)
	assert.Contains(t, c.Text, "dec-missing")
}

// TestIntegrityCriticSilentOnCleanProposal — common case: Phase 2's
// reconciler produces a clean canonical proposal; the integrity critic
// adds zero findings.
func TestIntegrityCriticSilentOnCleanProposal(t *testing.T) {
	clean := SpecProposal{
		Features: []FeatureProposal{
			{ID: "feat-x", Title: "X", Decisions: []string{"dec-x"}},
		},
		Decisions: []DecisionProposal{
			{ID: "dec-x", Title: "X"},
		},
	}
	raw, err := json.Marshal(clean)
	require.NoError(t, err)

	state := &PlanningState{ProposedSpec: string(raw)}
	step := WorkflowStep{ID: "critique", MergeAs: "critic_issues"}
	mergeResults(state, step, nil)

	assert.Empty(t, state.Concerns, "integrity critic must not flag clean proposals")
}

// TestIntegrityCriticUsesExistingSnapshot — references that resolve via
// Existing.Decisions (extending a persisted spec) should NOT be flagged
// as dangling.
func TestIntegrityCriticUsesExistingSnapshot(t *testing.T) {
	extending := SpecProposal{
		Features: []FeatureProposal{
			{ID: "feat-x", Title: "X", Decisions: []string{"dec-existing"}},
		},
	}
	raw, err := json.Marshal(extending)
	require.NoError(t, err)

	state := &PlanningState{
		ProposedSpec: string(raw),
		Existing:     &ExistingSpec{Decisions: []spec.Decision{{ID: "dec-existing", Title: "Existing"}}},
	}
	step := WorkflowStep{ID: "critique", MergeAs: "critic_issues"}
	mergeResults(state, step, nil)

	assert.Empty(t, state.Concerns,
		"references resolved by the existing-spec snapshot must not trigger integrity findings")
}

// TestCritiqueKindFor maps each canonical critic agent ID to its lens
// label. Unknown agents fall back to "review" so their concerns still
// get a kind tag.
func TestCritiqueKindFor(t *testing.T) {
	cases := map[string]string{
		"architect_critic": "architecture",
		"devops_critic":    "devops",
		"sre_critic":       "sre",
		"cost_critic":      "cost",
		"unknown":          "review",
	}
	for agentID, want := range cases {
		assert.Equal(t, want, critiqueKindFor(agentID), "agent %q", agentID)
	}
}

// TestMergeResultsTagsCriticConcernsWithKind — verifies LLM critic
// concerns land on state.Concerns with Kind populated, so the revise
// projection can group them by lens.
func TestMergeResultsTagsCriticConcernsWithKind(t *testing.T) {
	state := &PlanningState{}
	step := WorkflowStep{ID: "critique", MergeAs: "critic_issues"}
	results := []RoundResult{
		{StepID: "critique", AgentID: "architect_critic", Output: `{"issues":["arch problem"]}`},
		{StepID: "critique", AgentID: "devops_critic", Output: `{"issues":["devops problem"]}`},
	}
	mergeResults(state, step, results)

	require.Len(t, state.Concerns, 2)
	kinds := map[string]string{}
	for _, c := range state.Concerns {
		kinds[c.Text] = c.Kind
	}
	assert.Equal(t, "architecture", kinds["arch problem"])
	assert.Equal(t, "devops", kinds["devops problem"])
}

// TestBuildRevisePromptDirectiveShape — the new revise prompt opens
// with an explicit rejection, groups findings by kind, lists required
// actions, and ends with a directive to re-emit the complete corrected
// proposal. Mirrors the reviseForIntegrity prompt shape.
func TestBuildRevisePromptDirectiveShape(t *testing.T) {
	concerns := []Concern{
		{AgentID: "architect_critic", Severity: "high", Kind: "architecture", Text: "Stack picks Django but GOALS.md says Go"},
		{AgentID: "integrity_critic", Severity: "high", Kind: "integrity", Text: `feature feat-x.decisions references unknown id "dec-missing"`},
		{AgentID: "devops_critic", Severity: "medium", Kind: "devops", Text: "Deployment posture unspecified"},
	}
	out := buildRevisePrompt(concerns, nil)

	assert.True(t, strings.HasPrefix(out, "STOP."),
		"directive prompt should open with STOP. for explicit rejection")
	assert.Contains(t, out, "rejected",
		"prompt must explicitly mark prior proposal as rejected")
	// Findings grouped by kind, alphabetically (architecture, devops, integrity).
	archIdx := strings.Index(out, "### architecture")
	devopsIdx := strings.Index(out, "### devops")
	integrityIdx := strings.Index(out, "### integrity")
	require.True(t, archIdx >= 0 && devopsIdx >= 0 && integrityIdx >= 0,
		"findings must be grouped under per-kind headings")
	assert.Less(t, archIdx, devopsIdx, "kind headings should appear in alphabetical order")
	assert.Less(t, devopsIdx, integrityIdx, "kind headings should appear in alphabetical order")

	// Each finding's text appears verbatim under its kind.
	assert.Contains(t, out, "Stack picks Django but GOALS.md says Go")
	assert.Contains(t, out, `feature feat-x.decisions references unknown id "dec-missing"`)
	assert.Contains(t, out, "Deployment posture unspecified")

	// Required-actions block + don't-paraphrase guidance + complete-output directive.
	assert.Contains(t, out, "Required actions")
	assert.Contains(t, out, "Do not paraphrase")
	assert.Contains(t, out, "COMPLETE corrected RawSpecProposal",
		"must direct the architect to re-emit the full proposal, not a diff")
}

// TestProjectReviseShowsRawProposalToArchitect — the revise projection
// surfaces RawProposal (what the architect actually produced) rather
// than the canonical ProposedSpec (the reconciler's transform), so the
// rejection language matches what the architect emitted.
func TestProjectReviseShowsRawProposalToArchitect(t *testing.T) {
	snap := StateSnapshot{
		Prompt:       "build it",
		RawProposal:  `{"features":[{"id":"feat-x","title":"X","decisions":[{"title":"Use D"}]}]}`,
		ProposedSpec: `{"features":[{"id":"feat-x","title":"X","decisions":["dec-use-d"]}],"decisions":[{"id":"dec-use-d","title":"Use D"}]}`,
		Concerns:     []Concern{{Kind: "architecture", Text: "x"}},
	}
	msgs := projectRevise(snap)

	require.GreaterOrEqual(t, len(msgs), 2)
	// The assistant message should be the raw proposal, not the canonical.
	var assistant string
	for _, m := range msgs {
		if m.Role == "assistant" {
			assistant = m.Content
			break
		}
	}
	assert.Contains(t, assistant, `"title":"Use D"`,
		"assistant message should be the architect's RawProposal (inline decisions)")
	assert.NotContains(t, assistant, `"id":"dec-use-d"`,
		"assistant message should NOT be the post-reconcile canonical (which has reconciler-assigned ids)")
}
