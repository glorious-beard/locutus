package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractFanoutItemsFindingClusters — DJ-098 unified revise. The
// findings.clusters fanout returns one raw-JSON string per
// FindingCluster, parseable back to the typed shape so the projection
// can decide revise vs add per-item.
func TestExtractFanoutItemsFindingClusters(t *testing.T) {
	state := &PlanningState{
		FindingClusters: []FindingCluster{
			{Topic: "feat-a", NodeID: "feat-a", AgentID: "spec_feature_elaborator", Findings: []string{"address PII"}},
			{Topic: "infrastructure-as-code", AgentID: "spec_strategy_elaborator", Findings: []string{"missing IaC", "no CI/CD"}},
		},
	}

	items, err := extractFanoutItems(state, "findings.clusters")
	require.NoError(t, err)
	require.Len(t, items, 2)

	var first FindingCluster
	require.NoError(t, json.Unmarshal([]byte(items[0]), &first))
	assert.Equal(t, "feat-a", first.NodeID)
	assert.Equal(t, "spec_feature_elaborator", first.AgentID)

	var second FindingCluster
	require.NoError(t, json.Unmarshal([]byte(items[1]), &second))
	assert.Empty(t, second.NodeID, "addition cluster has no NodeID")
	assert.Equal(t, "spec_strategy_elaborator", second.AgentID)
	assert.Len(t, second.Findings, 2)
}

// TestExtractFanoutItemsFindingClustersDropsEmpty — defensive guard:
// a FindingCluster with no findings is meaningless and would dispatch
// an elaborator call against nothing. Drop them before fanout.
func TestExtractFanoutItemsFindingClustersDropsEmpty(t *testing.T) {
	state := &PlanningState{
		FindingClusters: []FindingCluster{
			{Topic: "real", AgentID: "spec_strategy_elaborator", Findings: []string{"x"}},
			{Topic: "empty", AgentID: "spec_strategy_elaborator", Findings: nil},
		},
	}
	items, err := extractFanoutItems(state, "findings.clusters")
	require.NoError(t, err)
	require.Len(t, items, 1, "empty-findings cluster must be dropped before dispatch")
}

// TestMechanicalClusterPartitions — the Go pre-pass groups concerns
// by id-mention against the existing-proposal id set. Findings that
// name an existing feature/strategy form per-node clusters; everything
// else lands in unmatched.
func TestMechanicalClusterPartitions(t *testing.T) {
	concerns := []Concern{
		{Text: "Decision dec-foo referenced by feat-dashboard but missing"},
		{Text: "feat-dashboard lacks PII encryption"},
		{Text: "strat-frontend should name a build tool"},
		{Text: "missing infrastructure-as-code strategy"},
		{Text: "no cost ceiling defined"},
		{Text: "feat-ghost-not-in-proposal lacks something"},
	}
	clusters, unmatched := MechanicalCluster(concerns,
		[]string{"feat-dashboard"}, []string{"strat-frontend"})

	require.Len(t, clusters, 2, "two existing-id clusters: feat-dashboard and strat-frontend")
	// feat-dashboard cluster should have 2 findings (the two mentioning it).
	var dashboard, frontend *FindingCluster
	for i := range clusters {
		switch clusters[i].NodeID {
		case "feat-dashboard":
			dashboard = &clusters[i]
		case "strat-frontend":
			frontend = &clusters[i]
		}
	}
	require.NotNil(t, dashboard)
	require.NotNil(t, frontend)
	assert.Equal(t, "spec_feature_elaborator", dashboard.AgentID, "feat- prefix dispatches to feature elaborator")
	assert.Equal(t, "spec_strategy_elaborator", frontend.AgentID, "strat- prefix dispatches to strategy elaborator")
	assert.Len(t, dashboard.Findings, 2)
	assert.Len(t, frontend.Findings, 1)

	// unmatched: the IaC-missing finding, the cost-ceiling finding,
	// AND the feat-ghost finding (id mentioned but not in existing set).
	require.Len(t, unmatched, 3)
	assert.Contains(t, unmatched, "missing infrastructure-as-code strategy")
	assert.Contains(t, unmatched, "no cost ceiling defined")
	assert.Contains(t, unmatched, "feat-ghost-not-in-proposal lacks something",
		"id mentioned but not in existing set falls through to unmatched")
}

// TestMechanicalClusterDuplicatesAcrossNodes — a single finding that
// names two existing-node ids is duplicated into both clusters. This
// is the "one node per concern" rule from the triager era; preserved
// because semantically the finding targets both.
func TestMechanicalClusterDuplicatesAcrossNodes(t *testing.T) {
	concerns := []Concern{
		{Text: "feat-a and strat-x have a coupling problem"},
	}
	clusters, unmatched := MechanicalCluster(concerns,
		[]string{"feat-a"}, []string{"strat-x"})
	require.Len(t, clusters, 2)
	assert.Empty(t, unmatched)
	// The finding text appears in both clusters' findings list.
	assert.Equal(t, clusters[0].Findings, clusters[1].Findings)
}

// TestPromoteLLMClustersDropsEmpty — a malformed/empty cluster is
// dropped at promotion time. This is the DJ-098 equivalent of the
// `[{}]`-placeholder defense the triager design needed.
func TestPromoteLLMClustersDropsEmpty(t *testing.T) {
	llm := LLMFindingClusters{
		Clusters: []LLMFindingCluster{
			{Topic: "real", Findings: []string{"x"}, Kind: "strategy"},
			{Topic: "empty", Findings: nil, Kind: "strategy"}, // placeholder — drop
		},
	}
	raw, _ := json.Marshal(llm)
	out := PromoteLLMClusters(string(raw))
	require.Len(t, out, 1, "empty-findings cluster must be dropped at promotion")
	assert.Equal(t, "real", out[0].Topic)
}

// TestPromoteLLMClustersDefaultsKindToStrategy — when the model emits
// a cluster with empty/unknown kind, default to strategy (recoverable
// failure mode; most missing-X is missing-strategy).
func TestPromoteLLMClustersDefaultsKindToStrategy(t *testing.T) {
	llm := LLMFindingClusters{
		Clusters: []LLMFindingCluster{
			{Topic: "ambiguous", Findings: []string{"x"}, Kind: ""},
		},
	}
	raw, _ := json.Marshal(llm)
	out := PromoteLLMClusters(string(raw))
	require.Len(t, out, 1)
	assert.Equal(t, "spec_strategy_elaborator", out[0].AgentID)
}

// TestFanoutItemIDFallsBackToTopic — the per-item progress label
// extractor reads `id` (outline items) → `node_id` (revise clusters
// targeting an existing node) → `topic` (addition clusters). Without
// the topic fallback, addition clusters would label as the bare step
// ID and collapse into a single spinner.
func TestFanoutItemIDFallsBackToTopic(t *testing.T) {
	outlineItem := `{"id":"feat-x","title":"X"}`
	revisedCluster := `{"node_id":"feat-y","topic":"feat-y","findings":["fix it"]}`
	addCluster := `{"topic":"infrastructure-as-code","findings":["missing IaC"]}`
	emptyItem := `{}`
	malformed := `not json`

	assert.Equal(t, "feat-x", fanoutItemID(outlineItem))
	assert.Equal(t, "feat-y", fanoutItemID(revisedCluster), "node_id wins over topic when both present")
	assert.Equal(t, "infrastructure-as-code", fanoutItemID(addCluster), "topic is the addition fallback")
	assert.Equal(t, "", fanoutItemID(emptyItem))
	assert.Equal(t, "", fanoutItemID(malformed))
}

// TestShouldRunConditionalClusters — DJ-098 conditionals.
//
//   - has_unmatched_findings gates the LLM clusterer step.
//   - has_finding_clusters gates the revise fanout and reconcile_revise.
func TestShouldRunConditionalClusters(t *testing.T) {
	t.Run("has_unmatched_findings: empty when nothing unmatched", func(t *testing.T) {
		assert.False(t, shouldRunConditional("has_unmatched_findings", &PlanningState{}))
	})
	t.Run("has_unmatched_findings: true with findings", func(t *testing.T) {
		state := &PlanningState{UnmatchedFindings: []string{"x"}}
		assert.True(t, shouldRunConditional("has_unmatched_findings", state))
	})
	t.Run("has_finding_clusters: empty when nothing clustered", func(t *testing.T) {
		assert.False(t, shouldRunConditional("has_finding_clusters", &PlanningState{}))
	})
	t.Run("has_finding_clusters: true with clusters", func(t *testing.T) {
		state := &PlanningState{
			FindingClusters: []FindingCluster{{Topic: "x", AgentID: "spec_strategy_elaborator", Findings: []string{"y"}}},
		}
		assert.True(t, shouldRunConditional("has_finding_clusters", state))
	})
}

// TestAssembleRevisedRawProposalReplacesByID — the revise-merge takes
// the original assembled proposal, swaps in revised entries by id
// match, appends additions, and leaves untouched nodes verbatim.
//
// DJ-098 rewrites the source: each entry in state.RevisedNodes is
// either a revision (id matches an original) or an addition (fresh id).
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
		RevisedNodes:        []string{revisedFeatA, revisedStratX},
	}
	merged, ok := assembleRevisedRawProposal(state)
	require.True(t, ok)

	var out RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(merged), &out))
	require.Len(t, out.Features, 2)
	require.Len(t, out.Strategies, 2)

	assert.Equal(t, "feat-a", out.Features[0].ID)
	assert.Equal(t, "A revised", out.Features[0].Title)
	require.Len(t, out.Features[0].Decisions, 1)
	assert.Equal(t, "use foo+pii", out.Features[0].Decisions[0].Title)

	// feat-b untouched → carry through verbatim.
	assert.Equal(t, "feat-b", out.Features[1].ID)
	require.Len(t, out.Features[1].Decisions, 1)
	assert.Equal(t, "use bar", out.Features[1].Decisions[0].Title,
		"untouched feature must carry its original decisions through revise")

	assert.Equal(t, "Next.js + Vercel + IaC", out.Strategies[0].Decisions[0].Title)
	assert.Equal(t, "Postgres", out.Strategies[1].Decisions[0].Title)
}

// TestAssembleRevisedRawProposalAppendsAdditions — additions (entries
// in RevisedNodes whose id doesn't match any original) are appended
// after originals.
func TestAssembleRevisedRawProposalAppendsAdditions(t *testing.T) {
	original := RawSpecProposal{
		Features: []RawFeatureProposal{{ID: "feat-a", Title: "A"}},
	}
	originalJSON, _ := json.Marshal(original)

	featAdd, _ := json.Marshal(RawFeatureProposal{ID: "feat-new", Title: "New feature"})
	stratAdd, _ := json.Marshal(RawStrategyProposal{ID: "strat-iac", Title: "Terraform", Kind: "foundational"})

	state := &PlanningState{
		OriginalRawProposal: string(originalJSON),
		RevisedNodes:        []string{string(featAdd), string(stratAdd)},
	}
	merged, ok := assembleRevisedRawProposal(state)
	require.True(t, ok)

	var out RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(merged), &out))
	require.Len(t, out.Features, 2, "original + 1 feature addition")
	require.Len(t, out.Strategies, 1, "1 strategy addition")
	assert.Equal(t, "feat-new", out.Features[1].ID)
	assert.Equal(t, "strat-iac", out.Strategies[0].ID)
}

// TestAssembleRevisedRawProposalUnknownPrefixDropped — a per-cluster
// elaborator output with an id that doesn't start with feat- or
// strat- is logged and skipped (best-effort).
func TestAssembleRevisedRawProposalUnknownPrefixDropped(t *testing.T) {
	originalJSON, _ := json.Marshal(RawSpecProposal{
		Features: []RawFeatureProposal{{ID: "feat-a", Title: "A"}},
	})
	weird, _ := json.Marshal(map[string]any{"id": "xyz-bogus", "title": "Unknown"})

	state := &PlanningState{
		OriginalRawProposal: string(originalJSON),
		RevisedNodes:        []string{string(weird)},
	}
	merged, ok := assembleRevisedRawProposal(state)
	require.True(t, ok)
	var out RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(merged), &out))
	assert.Len(t, out.Features, 1, "unknown-prefix entry must be skipped, not crash assembly")
}

// TestAssembleRevisedRawProposalEmptyOriginalReturnsNothing — assembly
// is a no-op without an original (e.g. before elaborate completes).
func TestAssembleRevisedRawProposalEmptyOriginalReturnsNothing(t *testing.T) {
	state := &PlanningState{
		RevisedNodes: []string{`{"id":"feat-a"}`},
	}
	merged, ok := assembleRevisedRawProposal(state)
	assert.False(t, ok)
	assert.Empty(t, merged)
}

// TestExecuteRoundReviseFanoutSkipsWithoutClusters — the revise step
// is conditional has_finding_clusters. With no clusters it would skip;
// even if it doesn't, an empty FindingClusters returns no fanout
// items and the step is a no-op without firing any LLM calls.
func TestExecuteRoundReviseFanoutSkipsWithoutClusters(t *testing.T) {
	state := &PlanningState{}
	mock := NewMockExecutor()
	ex := &WorkflowExecutor{
		Executor: mock,
		AgentDefs: map[string]AgentDef{
			"spec_feature_elaborator":  {ID: "spec_feature_elaborator"},
			"spec_strategy_elaborator": {ID: "spec_strategy_elaborator"},
		},
	}
	step := WorkflowStep{
		ID:     "revise",
		Agent:  "spec_strategy_elaborator",
		Fanout: "findings.clusters",
	}
	results, err := ex.ExecuteRound(context.Background(), step, state)
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Equal(t, 0, mock.CallCount(),
		"no clusters ⇒ zero fanout items ⇒ zero LLM calls")
}

// TestMergeResultsRevisedNodesAccumulates — each per-cluster fanout
// call appends one elaborator output to RevisedNodes; subsequent
// merges accumulate without overwriting. Replaces the old per-feature/
// per-strategy/per-addition slices with one unified slice.
func TestMergeResultsRevisedNodesAccumulates(t *testing.T) {
	state := &PlanningState{}
	step := WorkflowStep{ID: "revise", MergeAs: "revised_nodes"}
	mergeResults(state, step, []RoundResult{
		{StepID: "revise (feat-a)", AgentID: "spec_feature_elaborator", Output: `{"id":"feat-a"}`},
		{StepID: "revise (strat-iac)", AgentID: "spec_strategy_elaborator", Output: `{"id":"strat-iac"}`},
	})
	require.Len(t, state.RevisedNodes, 2)
	assert.Contains(t, state.RevisedNodes[0], "feat-a")
	assert.Contains(t, state.RevisedNodes[1], "strat-iac")
}

// TestMergeResultsFindingClustersPromotesLLMOutput — the
// finding_clusters merge handler promotes the LLM clusterer's
// LLMFindingClusters JSON into FindingCluster entries (with AgentID
// set from kind), appended to whatever the mechanical pre-pass
// already produced.
func TestMergeResultsFindingClustersPromotesLLMOutput(t *testing.T) {
	state := &PlanningState{
		// Pre-existing mechanical cluster.
		FindingClusters: []FindingCluster{
			{Topic: "feat-a", NodeID: "feat-a", AgentID: "spec_feature_elaborator", Findings: []string{"x"}},
		},
	}
	step := WorkflowStep{ID: "cluster_findings", MergeAs: "finding_clusters"}
	llmOutput := `{"clusters":[{"topic":"infrastructure-as-code","findings":["missing IaC"],"kind":"strategy"}]}`
	mergeResults(state, step, []RoundResult{
		{StepID: "cluster_findings", AgentID: "spec_finding_clusterer", Output: llmOutput},
	})
	require.Len(t, state.FindingClusters, 2, "mechanical pre-existing + LLM-promoted cluster")
	assert.Equal(t, "feat-a", state.FindingClusters[0].NodeID, "mechanical cluster preserved")
	assert.Equal(t, "infrastructure-as-code", state.FindingClusters[1].Topic)
	assert.Equal(t, "spec_strategy_elaborator", state.FindingClusters[1].AgentID,
		"kind=strategy promotes to spec_strategy_elaborator")
}
