package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectTriageIncludesNodeIDsAndConcerns — the triager's prompt
// must list the proposal's existing node ids (so the agent has a
// concrete routing target) and the verbatim critic findings (so it
// doesn't paraphrase).
func TestProjectTriageIncludesNodeIDsAndConcerns(t *testing.T) {
	proposal := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-dashboard", Title: "Dashboard"},
		},
		Strategies: []RawStrategyProposal{
			{ID: "strat-frontend", Title: "Stack", Kind: "foundational"},
		},
	}
	raw, _ := json.Marshal(proposal)
	snap := StateSnapshot{
		Prompt:      "Build it.",
		RawProposal: string(raw),
		Concerns: []Concern{
			{AgentID: "architect_critic", Kind: "architecture", Text: "feat-dashboard lacks PII encryption"},
			{AgentID: "cost_critic", Kind: "cost", Text: "missing IaC strategy"},
		},
	}
	msgs := projectTriage(snap)
	require.Len(t, msgs, 1)
	body := msgs[0].Content

	assert.Contains(t, body, "feat-dashboard", "feature id must appear so triager can route to it")
	assert.Contains(t, body, "strat-frontend", "strategy id must appear")
	assert.Contains(t, body, "feat-dashboard lacks PII encryption", "verbatim concern text required")
	assert.Contains(t, body, "missing IaC strategy")
	assert.Contains(t, body, "### architecture", "concerns grouped by kind so the routing intent is visible")
	assert.Contains(t, body, "### cost")
}

// TestProjectReviseNodeRendersPriorContent — the per-node revise
// elaborator's prompt must include the prior RawFeatureProposal /
// RawStrategyProposal verbatim plus the targeted concerns so the
// elaborator can re-emit a corrected version with full context.
func TestProjectReviseNodeRendersPriorContent(t *testing.T) {
	original := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-a", Title: "A", Description: "first", Decisions: []InlineDecisionProposal{{Title: "use foo"}}},
		},
		Strategies: []RawStrategyProposal{
			{ID: "strat-x", Title: "Stack", Kind: "foundational", Body: "prose", Decisions: []InlineDecisionProposal{{Title: "Next.js"}}},
		},
	}
	raw, _ := json.Marshal(original)

	t.Run("feature revise mode", func(t *testing.T) {
		rev := NodeRevision{NodeID: "feat-a", Concerns: []string{"add PII encryption", "clarify scale"}}
		revRaw, _ := json.Marshal(rev)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(revRaw),
		}
		msgs := projectReviseNode(snap, "feature")
		require.Len(t, msgs, 1)
		body := msgs[0].Content

		assert.Contains(t, body, "feat-a")
		assert.Contains(t, body, "use foo", "prior decision title must appear so elaborator sees what it's revising")
		assert.Contains(t, body, "add PII encryption", "verbatim concern text")
		assert.Contains(t, body, "clarify scale")
		assert.Contains(t, body, "RawFeatureProposal", "directive names the output schema")
	})

	t.Run("strategy revise mode", func(t *testing.T) {
		rev := NodeRevision{NodeID: "strat-x", Concerns: []string{"name the IaC tool"}}
		revRaw, _ := json.Marshal(rev)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(revRaw),
		}
		msgs := projectReviseNode(snap, "strategy")
		body := msgs[0].Content

		assert.Contains(t, body, "strat-x")
		assert.Contains(t, body, "Next.js")
		assert.Contains(t, body, "name the IaC tool")
		assert.Contains(t, body, "RawStrategyProposal")
	})

	t.Run("missing prior content surfaces the gap", func(t *testing.T) {
		rev := NodeRevision{NodeID: "feat-ghost", Concerns: []string{"x"}}
		revRaw, _ := json.Marshal(rev)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(revRaw),
		}
		msgs := projectReviseNode(snap, "feature")
		body := msgs[0].Content
		assert.Contains(t, body, "not found", "missing prior content explicit to the model")
	})
}

// TestProjectReviseAdditionsListsExistingAndAdditions — the additions
// projection tells the architect what nodes exist (do NOT re-emit) and
// what addition concerns to address. Both pieces are required: without
// the existing list the architect re-introduces nodes; without the
// additions list it has nothing to author.
func TestProjectReviseAdditionsListsExistingAndAdditions(t *testing.T) {
	original := RawSpecProposal{
		Features:   []RawFeatureProposal{{ID: "feat-a", Title: "A"}},
		Strategies: []RawStrategyProposal{{ID: "strat-x", Title: "Stack", Kind: "foundational"}},
	}
	raw, _ := json.Marshal(original)
	plan := RevisionPlan{
		Additions: []string{"missing IaC strategy", "missing audit-log feature"},
	}
	planRaw, _ := json.Marshal(plan)

	snap := StateSnapshot{
		Prompt:              "Build it.",
		OriginalRawProposal: string(raw),
		RevisionPlan:        string(planRaw),
	}
	msgs := projectReviseAdditions(snap)
	require.Len(t, msgs, 1)
	body := msgs[0].Content

	assert.Contains(t, body, "do NOT re-emit", "explicit gate against re-emitting existing nodes")
	assert.Contains(t, body, "feat-a")
	assert.Contains(t, body, "strat-x")
	assert.Contains(t, body, "missing IaC strategy", "verbatim addition text")
	assert.Contains(t, body, "missing audit-log feature")
	assert.True(t, strings.Contains(body, "RawSpecProposal"),
		"directive references the output schema")
}

// TestProjectReviseAdditionsEmptyPlan — when the triager produced no
// additions, the architect prompt should make that explicit so the
// architect emits an empty proposal instead of inventing additions.
func TestProjectReviseAdditionsEmptyPlan(t *testing.T) {
	snap := StateSnapshot{
		Prompt:       "Build it.",
		RevisionPlan: `{}`,
	}
	msgs := projectReviseAdditions(snap)
	body := msgs[0].Content
	assert.Contains(t, body, "(none in the revision plan",
		"empty additions must be surfaced so the architect doesn't hallucinate")
}

// TestProjectStateRoutesReviseStepsCorrectly — the projection
// dispatcher must route revise_features / revise_strategies /
// revise_additions / triage to the right projection, including when
// fanout suffixes are present on the step ID.
func TestProjectStateRoutesReviseStepsCorrectly(t *testing.T) {
	plan := RevisionPlan{Additions: []string{"x"}}
	planRaw, _ := json.Marshal(plan)
	snap := StateSnapshot{
		Prompt:       "Build it.",
		RevisionPlan: string(planRaw),
		Concerns:     []Concern{{AgentID: "architect_critic", Kind: "architecture", Text: "concern text"}},
	}

	t.Run("triage routes to projectTriage", func(t *testing.T) {
		msgs := ProjectState("triage", snap)
		require.Len(t, msgs, 1)
		assert.Contains(t, msgs[0].Content, "Critic findings to route")
	})

	t.Run("revise_features with fanout suffix routes to revise-feature projection", func(t *testing.T) {
		rev := NodeRevision{NodeID: "feat-a", Concerns: []string{"x"}}
		revRaw, _ := json.Marshal(rev)
		snap := snap
		snap.FanoutItem = string(revRaw)
		msgs := ProjectState("revise_features (feat-a)", snap)
		assert.Contains(t, msgs[0].Content, "feat-a")
		assert.Contains(t, msgs[0].Content, "RawFeatureProposal")
	})

	t.Run("revise_strategies routes to revise-strategy projection", func(t *testing.T) {
		rev := NodeRevision{NodeID: "strat-x", Concerns: []string{"x"}}
		revRaw, _ := json.Marshal(rev)
		snap := snap
		snap.FanoutItem = string(revRaw)
		msgs := ProjectState("revise_strategies (strat-x)", snap)
		assert.Contains(t, msgs[0].Content, "strat-x")
		assert.Contains(t, msgs[0].Content, "RawStrategyProposal")
	})

	t.Run("revise_additions routes to additions projection", func(t *testing.T) {
		msgs := ProjectState("revise_additions", snap)
		assert.Contains(t, msgs[0].Content, "Additions to propose")
	})
}
