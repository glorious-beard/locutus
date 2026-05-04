package agent

import (
	"encoding/json"
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
		// Directive text ("Produce the corrected RawFeatureProposal...")
		// lives in the elaborator's .md system prompt; no longer asserted
		// in the projection output. See DJ-097.
		assert.NotContains(t, body, "Produce the corrected",
			"projections are data-only; directives must not leak into the user message (DJ-097)")
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
		assert.NotContains(t, body, "Produce the corrected",
			"data-only; directive lives in the elaborator's .md")
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

// TestProjectAdditionElaborateRendersConcernAndExistingNodes — the
// per-finding addition projection (Phase 4) must include the verbatim
// critic finding driving the addition AND the existing-nodes "do NOT
// re-emit" list. Without the finding the elaborator has nothing to
// author from; without the existing list it risks re-introducing
// already-present nodes.
func TestProjectAdditionElaborateRendersConcernAndExistingNodes(t *testing.T) {
	original := RawSpecProposal{
		Features:   []RawFeatureProposal{{ID: "feat-dashboard", Title: "Dashboard"}},
		Strategies: []RawStrategyProposal{{ID: "strat-frontend", Title: "Stack", Kind: "foundational"}},
	}
	raw, _ := json.Marshal(original)

	t.Run("strategy addition mode", func(t *testing.T) {
		added := AddedNode{Kind: "strategy", SourceConcern: "missing infrastructure-as-code strategy"}
		addedRaw, _ := json.Marshal(added)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(addedRaw),
		}
		msgs := projectAdditionElaborate(snap, "strategy")
		require.Len(t, msgs, 1)
		body := msgs[0].Content

		assert.Contains(t, body, "do NOT re-emit", "explicit gate against duplicating existing nodes")
		assert.Contains(t, body, "feat-dashboard")
		assert.Contains(t, body, "strat-frontend")
		assert.Contains(t, body, "missing infrastructure-as-code strategy",
			"verbatim critic finding text drives the addition")
		// Directive ("Produce one RawStrategyProposal... invent the id...")
		// lives in the elaborator's .md "addition mode" addendum.
		// Projection is data-only — see DJ-097.
		assert.NotContains(t, body, "Produce one Raw",
			"projections must not carry directives; the elaborator's .md owns them")
	})

	t.Run("feature addition mode", func(t *testing.T) {
		added := AddedNode{Kind: "feature", SourceConcern: "the plan lacks a feature for data export"}
		addedRaw, _ := json.Marshal(added)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(addedRaw),
		}
		msgs := projectAdditionElaborate(snap, "feature")
		body := msgs[0].Content

		assert.Contains(t, body, "the plan lacks a feature for data export")
		assert.NotContains(t, body, "Produce one Raw",
			"data-only; directive in .md")
	})

	t.Run("missing source_concern surfaces the gap", func(t *testing.T) {
		added := AddedNode{Kind: "strategy"} // no source_concern
		addedRaw, _ := json.Marshal(added)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(addedRaw),
		}
		msgs := projectAdditionElaborate(snap, "strategy")
		body := msgs[0].Content
		assert.Contains(t, body, "(missing", "missing source_concern explicit to the model")
	})
}

// TestProjectStateRoutesReviseStepsCorrectly — the projection
// dispatcher must route revise_features / revise_strategies /
// revise_feature_additions / revise_strategy_additions / triage to
// the right projection, including when fanout suffixes are present
// on the step ID.
func TestProjectStateRoutesReviseStepsCorrectly(t *testing.T) {
	plan := RevisionPlan{Additions: []AddedNode{{Kind: "strategy", SourceConcern: "x"}}}
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
		// Routing is verified via the data-shaped header that's
		// distinct per projection — "feature to revise" vs
		// "strategy to revise" vs "Feature to propose (addition)".
		assert.Contains(t, msgs[0].Content, "feat-a")
		assert.Contains(t, msgs[0].Content, "feature to revise")
	})

	t.Run("revise_strategies routes to revise-strategy projection", func(t *testing.T) {
		rev := NodeRevision{NodeID: "strat-x", Concerns: []string{"x"}}
		revRaw, _ := json.Marshal(rev)
		snap := snap
		snap.FanoutItem = string(revRaw)
		msgs := ProjectState("revise_strategies (strat-x)", snap)
		assert.Contains(t, msgs[0].Content, "strat-x")
		assert.Contains(t, msgs[0].Content, "strategy to revise")
	})

	t.Run("revise_feature_additions routes to addition-feature projection", func(t *testing.T) {
		added := AddedNode{Kind: "feature", SourceConcern: "missing data-export feature"}
		addedRaw, _ := json.Marshal(added)
		snap := snap
		snap.FanoutItem = string(addedRaw)
		msgs := ProjectState("revise_feature_additions (feat-data-export)", snap)
		assert.Contains(t, msgs[0].Content, "missing data-export feature")
		assert.Contains(t, msgs[0].Content, "Feature to propose (addition)")
	})

	t.Run("revise_strategy_additions routes to addition-strategy projection", func(t *testing.T) {
		added := AddedNode{Kind: "strategy", SourceConcern: "missing IaC strategy"}
		addedRaw, _ := json.Marshal(added)
		snap := snap
		snap.FanoutItem = string(addedRaw)
		msgs := ProjectState("revise_strategy_additions (strat-iac)", snap)
		assert.Contains(t, msgs[0].Content, "missing IaC strategy")
		assert.Contains(t, msgs[0].Content, "Strategy to propose (addition)")
	})
}
