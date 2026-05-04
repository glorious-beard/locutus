package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectClusterFindingsIncludesUnmatchedAndExisting — the
// clusterer's prompt must list the proposal's existing nodes (kind-
// classification context) and the verbatim unmatched findings (the
// input the clusterer must group losslessly).
func TestProjectClusterFindingsIncludesUnmatchedAndExisting(t *testing.T) {
	proposal := RawSpecProposal{
		Features:   []RawFeatureProposal{{ID: "feat-dashboard", Title: "Dashboard"}},
		Strategies: []RawStrategyProposal{{ID: "strat-frontend", Title: "Stack", Kind: "foundational"}},
	}
	raw, _ := json.Marshal(proposal)
	snap := StateSnapshot{
		Prompt:      "Build it.",
		RawProposal: string(raw),
		UnmatchedFindings: []string{
			"missing IaC strategy",
			"no cost ceiling defined",
			"observability tooling not specified",
		},
	}
	msgs := projectClusterFindings(snap)
	require.Len(t, msgs, 1)
	body := msgs[0].Content

	assert.Contains(t, body, "feat-dashboard", "existing feature ids supply kind-classification context")
	assert.Contains(t, body, "strat-frontend")
	assert.Contains(t, body, "missing IaC strategy", "verbatim unmatched-finding text required")
	assert.Contains(t, body, "no cost ceiling defined")
	assert.Contains(t, body, "observability tooling not specified")
	// Lossless-grouping mandate, kind-defaulting rule live in the
	// agent .md, not the projection (DJ-097).
	assert.NotContains(t, body, "Total entries", "directives must not leak into the projection (DJ-097)")
}

// TestProjectFindingClusterRendersTargetedNode — when a cluster
// targets an existing node, the elaborator's prompt must include the
// prior content (so it can re-emit a corrected version) and the
// targeted findings (verbatim).
func TestProjectFindingClusterRendersTargetedNode(t *testing.T) {
	original := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-a", Title: "A", Description: "first", Decisions: []InlineDecisionProposal{{Title: "use foo"}}},
		},
		Strategies: []RawStrategyProposal{
			{ID: "strat-x", Title: "Stack", Kind: "foundational", Body: "prose", Decisions: []InlineDecisionProposal{{Title: "Next.js"}}},
		},
	}
	raw, _ := json.Marshal(original)

	t.Run("feature revise (NodeID set, feat- prefix)", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:    "feat-a",
			NodeID:   "feat-a",
			AgentID:  "spec_feature_elaborator",
			Findings: []string{"add PII encryption", "clarify scale"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(clusterRaw),
		}
		msgs := projectFindingCluster(snap)
		require.Len(t, msgs, 1)
		body := msgs[0].Content

		assert.Contains(t, body, "feat-a")
		assert.Contains(t, body, "## Prior content", "header signals revise mode")
		assert.Contains(t, body, "use foo", "prior decision title surfaced")
		assert.Contains(t, body, "add PII encryption", "verbatim cluster finding")
		assert.Contains(t, body, "clarify scale")
		// Revise/add discrimination directive lives in the elaborator
		// .md system prompt, not in the projection (DJ-097).
		assert.NotContains(t, body, "Produce the corrected", "directives must not leak into projection")
	})

	t.Run("strategy revise (NodeID set, strat- prefix)", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:    "strat-x",
			NodeID:   "strat-x",
			AgentID:  "spec_strategy_elaborator",
			Findings: []string{"name the IaC tool"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(clusterRaw),
		}
		msgs := projectFindingCluster(snap)
		body := msgs[0].Content

		assert.Contains(t, body, "strat-x")
		assert.Contains(t, body, "Next.js", "prior strategy decision title surfaced")
		assert.Contains(t, body, "name the IaC tool")
	})

	t.Run("missing prior content surfaces the gap", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:    "feat-ghost",
			NodeID:   "feat-ghost",
			AgentID:  "spec_feature_elaborator",
			Findings: []string{"x"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(clusterRaw),
		}
		msgs := projectFindingCluster(snap)
		body := msgs[0].Content
		assert.Contains(t, body, "not found", "missing prior content explicit to the model")
	})
}

// TestProjectFindingClusterRendersAddMode — when a cluster has no
// NodeID (new-node case), the projection must show the existing-nodes
// list (id-collision avoidance) and the cluster's findings, but NOT a
// "Prior content" block.
func TestProjectFindingClusterRendersAddMode(t *testing.T) {
	original := RawSpecProposal{
		Features:   []RawFeatureProposal{{ID: "feat-dashboard", Title: "Dashboard"}},
		Strategies: []RawStrategyProposal{{ID: "strat-frontend", Title: "Stack", Kind: "foundational"}},
	}
	raw, _ := json.Marshal(original)

	t.Run("strategy add (no NodeID, kind=strategy)", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:    "infrastructure-as-code and CI/CD",
			AgentID:  "spec_strategy_elaborator",
			Findings: []string{"missing IaC strategy", "no CI/CD pipeline defined"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot{
			Prompt:              "Build it.",
			OriginalRawProposal: string(raw),
			FanoutItem:          string(clusterRaw),
		}
		msgs := projectFindingCluster(snap)
		require.Len(t, msgs, 1)
		body := msgs[0].Content

		assert.Contains(t, body, "## Existing nodes", "existing-nodes section labeled")
		assert.Contains(t, body, "feat-dashboard")
		assert.Contains(t, body, "strat-frontend")
		assert.Contains(t, body, "infrastructure-as-code and CI/CD", "topic verbatim")
		assert.Contains(t, body, "missing IaC strategy", "verbatim cluster finding")
		assert.Contains(t, body, "no CI/CD pipeline defined")
		assert.NotContains(t, body, "## Prior content", "no prior content in add mode")
		assert.NotContains(t, body, "Targeted node", "no targeted-node section in add mode")
	})
}

// TestProjectStateRoutesClusterStepsCorrectly — the projection
// dispatcher must route cluster_findings and revise (with fanout
// suffix carrying a FindingCluster) to the right projections.
func TestProjectStateRoutesClusterStepsCorrectly(t *testing.T) {
	t.Run("cluster_findings routes to projectClusterFindings", func(t *testing.T) {
		snap := StateSnapshot{
			Prompt:            "Build it.",
			UnmatchedFindings: []string{"some finding"},
		}
		msgs := ProjectState("cluster_findings", snap)
		require.Len(t, msgs, 1)
		assert.Contains(t, msgs[0].Content, "Findings to cluster")
	})

	t.Run("revise with fanout item routes to projectFindingCluster", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:    "feat-a",
			NodeID:   "feat-a",
			AgentID:  "spec_feature_elaborator",
			Findings: []string{"x"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot{
			Prompt:     "Build it.",
			FanoutItem: string(clusterRaw),
		}
		msgs := ProjectState("revise (feat-a)", snap)
		assert.Contains(t, msgs[0].Content, "feat-a")
		assert.Contains(t, msgs[0].Content, "Findings to address")
	})
}
