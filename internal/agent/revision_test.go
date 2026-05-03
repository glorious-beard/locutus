package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractFanoutItemsRevisionPlan — feature/strategy revision arrays
// resolve to one raw-JSON string per NodeRevision, parseable back to
// the typed shape. Mirrors TestExtractFanoutItems for the outline path.
func TestExtractFanoutItemsRevisionPlan(t *testing.T) {
	plan := RevisionPlan{
		FeatureRevisions: []NodeRevision{
			{NodeID: "feat-a", Concerns: []string{"address PII encryption"}},
			{NodeID: "feat-b", Concerns: []string{"clarify scale target"}},
		},
		StrategyRevisions: []NodeRevision{
			{NodeID: "strat-x", Concerns: []string{"name the cloud vendor"}},
		},
	}
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	state := &PlanningState{RevisionPlan: string(raw)}

	t.Run("feature_revisions returns one item per entry", func(t *testing.T) {
		items, err := extractFanoutItems(state, "revision_plan.feature_revisions")
		require.NoError(t, err)
		require.Len(t, items, 2)

		var first NodeRevision
		require.NoError(t, json.Unmarshal([]byte(items[0]), &first))
		assert.Equal(t, "feat-a", first.NodeID)
		require.Len(t, first.Concerns, 1)
		assert.Equal(t, "address PII encryption", first.Concerns[0])
	})

	t.Run("strategy_revisions returns one item per entry", func(t *testing.T) {
		items, err := extractFanoutItems(state, "revision_plan.strategy_revisions")
		require.NoError(t, err)
		require.Len(t, items, 1)

		var first NodeRevision
		require.NoError(t, json.Unmarshal([]byte(items[0]), &first))
		assert.Equal(t, "strat-x", first.NodeID)
	})

	t.Run("missing revision plan returns empty", func(t *testing.T) {
		empty := &PlanningState{}
		items, err := extractFanoutItems(empty, "revision_plan.feature_revisions")
		require.NoError(t, err)
		assert.Empty(t, items)
	})
}

// TestFanoutItemIDFallsBackToNodeID — the per-item progress label
// extractor reads `id` for outline items and `node_id` for revision
// items. Without the fallback, revise-fanout entries would label as
// the bare step ID and collapse into a single spinner.
func TestFanoutItemIDFallsBackToNodeID(t *testing.T) {
	outlineItem := `{"id":"feat-x","title":"X"}`
	revisionItem := `{"node_id":"feat-y","concerns":["fix it"]}`
	emptyItem := `{}`
	malformed := `not json`

	assert.Equal(t, "feat-x", fanoutItemID(outlineItem))
	assert.Equal(t, "feat-y", fanoutItemID(revisionItem),
		"revision items use node_id; fanoutItemID must fall back so the per-item event labels render")
	assert.Equal(t, "", fanoutItemID(emptyItem))
	assert.Equal(t, "", fanoutItemID(malformed))
}

// TestShouldRunConditionalHasAdditions — the has_additions conditional
// gates the revise_additions step. True only when the triager
// produced a non-empty additions array.
func TestShouldRunConditionalHasAdditions(t *testing.T) {
	t.Run("no plan", func(t *testing.T) {
		assert.False(t, shouldRunConditional("has_additions", &PlanningState{}))
	})
	t.Run("plan with no additions", func(t *testing.T) {
		raw, _ := json.Marshal(RevisionPlan{
			FeatureRevisions: []NodeRevision{{NodeID: "feat-a", Concerns: []string{"x"}}},
		})
		state := &PlanningState{RevisionPlan: string(raw)}
		assert.False(t, shouldRunConditional("has_additions", state))
	})
	t.Run("plan with additions", func(t *testing.T) {
		raw, _ := json.Marshal(RevisionPlan{
			Additions: []string{"missing IaC strategy"},
		})
		state := &PlanningState{RevisionPlan: string(raw)}
		assert.True(t, shouldRunConditional("has_additions", state))
	})
	t.Run("malformed plan", func(t *testing.T) {
		state := &PlanningState{RevisionPlan: "not json"}
		assert.False(t, shouldRunConditional("has_additions", state),
			"a malformed plan must not throw; surface as no-additions and let the trace show the malformed output")
	})
}

// TestAssembleRevisedRawProposalReplacesByID — the revise-merge takes
// the original assembled proposal, swaps revised features/strategies
// in by ID, appends additions, and leaves untouched nodes verbatim.
//
// This is the load-bearing assertion for Phase 1's bug fix: prior to
// the revise fanout, the architect would emit empty placeholder
// decisions for unrelated strategies during revise. With per-node
// revision the unaffected nodes carry through with their original
// decisions intact.
func TestAssembleRevisedRawProposalReplacesByID(t *testing.T) {
	original := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-a", Title: "A", Decisions: []InlineDecisionProposal{{Title: "use foo"}}},
			{ID: "feat-b", Title: "B", Decisions: []InlineDecisionProposal{{Title: "use bar"}}},
		},
		Strategies: []RawStrategyProposal{
			{ID: "strat-x", Title: "Stack", Decisions: []InlineDecisionProposal{{Title: "Next.js + Vercel"}}},
			{ID: "strat-y", Title: "DB", Decisions: []InlineDecisionProposal{{Title: "Postgres"}}},
		},
	}
	originalJSON, _ := json.Marshal(original)

	revisedFeatA := `{"id":"feat-a","title":"A revised","decisions":[{"title":"use foo+pii"}]}`
	revisedStratX := `{"id":"strat-x","title":"Stack","decisions":[{"title":"Next.js + Vercel + IaC"}]}`

	state := &PlanningState{
		OriginalRawProposal: string(originalJSON),
		RevisedFeatures:     []string{revisedFeatA},
		RevisedStrategies:   []string{revisedStratX},
	}
	merged, ok := assembleRevisedRawProposal(state)
	require.True(t, ok)

	var out RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(merged), &out))
	require.Len(t, out.Features, 2)
	require.Len(t, out.Strategies, 2)

	// feat-a was revised → swap in.
	assert.Equal(t, "feat-a", out.Features[0].ID)
	assert.Equal(t, "A revised", out.Features[0].Title)
	require.Len(t, out.Features[0].Decisions, 1)
	assert.Equal(t, "use foo+pii", out.Features[0].Decisions[0].Title)

	// feat-b was untouched → carry through verbatim with its decision.
	assert.Equal(t, "feat-b", out.Features[1].ID)
	require.Len(t, out.Features[1].Decisions, 1)
	assert.Equal(t, "use bar", out.Features[1].Decisions[0].Title,
		"untouched feature must carry its original decisions through revise — the bug Phase 1 fixes is exactly when this fails")

	// strat-x was revised → swap in.
	assert.Equal(t, "Next.js + Vercel + IaC", out.Strategies[0].Decisions[0].Title)
	// strat-y was untouched → carry through.
	assert.Equal(t, "Postgres", out.Strategies[1].Decisions[0].Title)
}

// TestAssembleRevisedRawProposalAppendsAdditions — additions are
// appended after originals (and revisions). Collisions with existing
// IDs are dropped (last-writer-wins on the original; addition is
// ignored to avoid duplicate entries that would confuse the
// reconciler).
func TestAssembleRevisedRawProposalAppendsAdditions(t *testing.T) {
	original := RawSpecProposal{
		Features: []RawFeatureProposal{{ID: "feat-a", Title: "A"}},
	}
	originalJSON, _ := json.Marshal(original)

	additions := RawSpecProposal{
		Features:   []RawFeatureProposal{{ID: "feat-new", Title: "New feature"}},
		Strategies: []RawStrategyProposal{{ID: "strat-iac", Title: "Terraform", Kind: "foundational"}},
	}
	additionsJSON, _ := json.Marshal(additions)

	state := &PlanningState{
		OriginalRawProposal: string(originalJSON),
		AdditionProposals:   string(additionsJSON),
	}
	merged, ok := assembleRevisedRawProposal(state)
	require.True(t, ok)

	var out RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(merged), &out))
	require.Len(t, out.Features, 2, "original + 1 addition")
	require.Len(t, out.Strategies, 1, "addition only (no original strategies)")
	assert.Equal(t, "feat-new", out.Features[1].ID)
	assert.Equal(t, "strat-iac", out.Strategies[0].ID)
}

// TestAssembleRevisedRawProposalEmptyOriginalReturnsNothing — the
// merge requires an original; without one (e.g. before elaborate
// completes), assembly is a no-op so the reconciler isn't fed garbage.
func TestAssembleRevisedRawProposalEmptyOriginalReturnsNothing(t *testing.T) {
	state := &PlanningState{
		RevisedFeatures: []string{`{"id":"feat-a"}`},
	}
	merged, ok := assembleRevisedRawProposal(state)
	assert.False(t, ok)
	assert.Empty(t, merged)
}

// TestExecuteRoundReviseFanoutSkipsWithoutPlan — the revise_features
// fanout step is conditional has_concerns. When no concerns exist
// it would skip; even if it doesn't, an empty RevisionPlan returns no
// fanout items and the step is a no-op without firing any LLM calls.
func TestExecuteRoundReviseFanoutSkipsWithoutPlan(t *testing.T) {
	state := &PlanningState{}
	mock := NewMockLLM()
	ex := &WorkflowExecutor{
		LLM:       mock,
		AgentDefs: map[string]AgentDef{"spec_feature_elaborator": {ID: "spec_feature_elaborator"}},
	}
	step := WorkflowStep{
		ID:     "revise_features",
		Agent:  "spec_feature_elaborator",
		Fanout: "revision_plan.feature_revisions",
	}
	results, err := ex.ExecuteRound(context.Background(), step, state)
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Equal(t, 0, mock.CallCount(),
		"empty revision plan ⇒ zero fanout items ⇒ zero LLM calls")
}

// TestMergeResultsTriagerOutputPopulatesPlan — the revision_plan
// merge handler stashes the triager output verbatim on
// PlanningState.RevisionPlan so downstream fanouts can read it.
func TestMergeResultsTriagerOutputPopulatesPlan(t *testing.T) {
	state := &PlanningState{}
	planJSON := `{"feature_revisions":[{"node_id":"feat-a","concerns":["x"]}]}`
	step := WorkflowStep{ID: "triage", MergeAs: "revision_plan"}
	mergeResults(state, step, []RoundResult{
		{StepID: "triage", AgentID: "spec_revision_triager", Output: planJSON},
	})
	assert.Equal(t, planJSON, state.RevisionPlan)
}

// TestMergeResultsRevisedFeaturesAccumulates — each per-node revise
// fanout call appends one revised RawFeatureProposal; subsequent
// merges accumulate without overwriting prior entries.
func TestMergeResultsRevisedFeaturesAccumulates(t *testing.T) {
	state := &PlanningState{}
	step := WorkflowStep{ID: "revise_features", MergeAs: "revised_features"}
	mergeResults(state, step, []RoundResult{
		{StepID: "revise_features", AgentID: "spec_feature_elaborator", Output: `{"id":"feat-a"}`},
		{StepID: "revise_features", AgentID: "spec_feature_elaborator", Output: `{"id":"feat-b"}`},
	})
	require.Len(t, state.RevisedFeatures, 2)
	assert.Contains(t, state.RevisedFeatures[0], "feat-a")
	assert.Contains(t, state.RevisedFeatures[1], "feat-b")
}
