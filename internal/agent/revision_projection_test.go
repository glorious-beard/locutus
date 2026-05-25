package agent

import (
	"encoding/json"
	"strings"
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
	snap := StateSnapshot[PlanningState]{
		State: PlanningState{
			Prompt:      "Build it.",
			RawProposal: string(raw),
			UnmatchedFindings: []string{
				"missing IaC strategy",
				"no cost ceiling defined",
				"observability tooling not specified",
			},
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
			{ID: "feat-a", Title: "A", Description: "first", Decisions: []string{"dec-use-foo"}},
		},
		Strategies: []RawStrategyProposal{
			{ID: "strat-x", Title: "Stack", Kind: "foundational", Body: "prose", Decisions: []string{"dec-nextjs"}},
		},
		Decisions: []RawDecisionProposal{
			{ID: "dec-use-foo", Title: "use foo"},
			{ID: "dec-nextjs", Title: "Next.js"},
		},
	}
	raw, _ := json.Marshal(original)

	t.Run("feature revise (NodeID set, feat- prefix)", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:    "feat-a",
			NodeID:   "feat-a",
			AgentID:  "spec-feature-elaborator",
			Findings: []string{"add PII encryption", "clarify scale"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot[PlanningState]{
			State: PlanningState{
				Prompt:              "Build it.",
				OriginalRawProposal: string(raw),
				RawProposal:         string(raw),
			},
			FanoutItem: string(clusterRaw),
		}
		msgs := projectFindingCluster(snap)
		require.Len(t, msgs, 1)
		body := msgs[0].Content

		assert.Contains(t, body, "feat-a")
		assert.Contains(t, body, "## Prior content", "header signals revise mode")
		assert.Contains(t, body, "dec-use-foo", "prior decision id reference surfaced under DJ-124")
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
			AgentID:  "spec-strategy-elaborator",
			Findings: []string{"name the IaC tool"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot[PlanningState]{
			State: PlanningState{
				Prompt:              "Build it.",
				OriginalRawProposal: string(raw),
				RawProposal:         string(raw),
			},
			FanoutItem: string(clusterRaw),
		}
		msgs := projectFindingCluster(snap)
		body := msgs[0].Content

		assert.Contains(t, body, "strat-x")
		assert.Contains(t, body, "dec-nextjs", "prior strategy decision id reference surfaced under DJ-124")
		assert.Contains(t, body, "name the IaC tool")
	})

	t.Run("missing prior content surfaces the gap", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:    "feat-ghost",
			NodeID:   "feat-ghost",
			AgentID:  "spec-feature-elaborator",
			Findings: []string{"x"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot[PlanningState]{
			State: PlanningState{
				Prompt:              "Build it.",
				OriginalRawProposal: string(raw),
				RawProposal:         string(raw),
			},
			FanoutItem: string(clusterRaw),
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
			AgentID:  "spec-strategy-elaborator",
			Findings: []string{"missing IaC strategy", "no CI/CD pipeline defined"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot[PlanningState]{
			State: PlanningState{
				Prompt:              "Build it.",
				OriginalRawProposal: string(raw),
				RawProposal:         string(raw),
			},
			FanoutItem: string(clusterRaw),
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

// TestProjectFindingClusterRendersCurrentCommitmentQuoted verifies the
// DJ-122 follow-up plumbing: when a FindingCluster carries the gate's
// quoted "present-but-insufficient" passage, the projection renders it
// as a blockquote that the elaborator sees BEFORE the findings list,
// so the elaborator strengthens the named text rather than rewriting
// the strategy from scratch.
func TestProjectFindingClusterRendersCurrentCommitmentQuoted(t *testing.T) {
	original := RawSpecProposal{
		Strategies: []RawStrategyProposal{
			{ID: "strat-observability", Title: "Observability", Kind: "quality", Body: "prose"},
		},
	}
	raw, _ := json.Marshal(original)

	t.Run("gate-fed cluster surfaces the quoted commitment", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:                   "WinPlan platform: on-call rotation owner",
			NodeID:                  "strat-observability",
			AgentID:                 "spec-strategy-elaborator",
			Findings:                []string{"The proposal commits PagerDuty but not who carries it."},
			CurrentCommitmentQuoted: "WinPlan adopts a developer-led on-call rotation backed by PagerDuty.",
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot[PlanningState]{
			State: PlanningState{
				Prompt:              "Build it.",
				OriginalRawProposal: string(raw),
				RawProposal:         string(raw),
			},
			FanoutItem: string(clusterRaw),
		}
		msgs := projectFindingCluster(snap)
		require.Len(t, msgs, 1)
		body := msgs[0].Content

		assert.Contains(t, body, "## Current commitment to strengthen",
			"projection includes the quoted-commitment section header")
		assert.Contains(t, body, "> WinPlan adopts a developer-led on-call rotation backed by PagerDuty.",
			"quoted commitment rendered as a markdown blockquote so the elaborator sees the exact text")
		assert.Contains(t, body, "The proposal commits PagerDuty but not who carries it.",
			"the gate's per-dimension reasoning still appears in the findings list")

		// The quoted commitment section should appear BEFORE the
		// findings list so the elaborator anchors on the existing
		// text first, then reads what needs strengthening.
		assert.Less(t,
			strings.Index(body, "Current commitment to strengthen"),
			strings.Index(body, "Findings to address"),
			"quoted commitment must appear before the findings list")
	})

	t.Run("critic-routed cluster (empty CurrentCommitmentQuoted) skips the section", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:                   "strat-observability",
			NodeID:                  "strat-observability",
			AgentID:                 "spec-strategy-elaborator",
			Findings:                []string{"no SLO is named"},
			CurrentCommitmentQuoted: "",
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot[PlanningState]{
			State: PlanningState{
				Prompt:              "Build it.",
				OriginalRawProposal: string(raw),
				RawProposal:         string(raw),
			},
			FanoutItem: string(clusterRaw),
		}
		msgs := projectFindingCluster(snap)
		body := msgs[0].Content

		assert.NotContains(t, body, "## Current commitment to strengthen",
			"projection omits the quoted-commitment section when the cluster carries no quoted text")
	})
}

// TestProjectFindingClusterReadsCurrentRawProposal verifies the DJ-122
// follow-up Fix 1: the elaborator's "Prior content" block reads from
// the CURRENT RawProposal, not the pre-iter-0 OriginalRawProposal.
// Without this; the iter-N elaborator rewrites from a blank slate
// every iteration with no awareness of what iter-(N-1) wrote.
func TestProjectFindingClusterReadsCurrentRawProposal(t *testing.T) {
	originalRaw, _ := json.Marshal(RawSpecProposal{
		Strategies: []RawStrategyProposal{
			{ID: "strat-obs", Title: "Observability", Kind: "quality", Body: "initial prose"},
		},
	})
	// RawProposal carries an iter-1 revision: the body has grown to
	// include PagerDuty + an Election Critical Window protocol — the
	// elaborator must see THIS, not the pre-iter-0 baseline above.
	currentRaw, _ := json.Marshal(RawSpecProposal{
		Strategies: []RawStrategyProposal{
			{ID: "strat-obs", Title: "Observability", Kind: "quality", Body: "WinPlan adopts a developer-led on-call rotation backed by PagerDuty; an Election Critical Window protocol coordinates incidents during the 72 hours preceding poll close."},
		},
	})

	cluster := FindingCluster{
		Topic:    "WinPlan platform: on-call rotation owner",
		NodeID:   "strat-obs",
		AgentID:  "spec-strategy-elaborator",
		Findings: []string{"name the specific team carrying the pager"},
	}
	clusterRaw, _ := json.Marshal(cluster)
	snap := StateSnapshot[PlanningState]{
		State: PlanningState{
			Prompt:              "Build it.",
			OriginalRawProposal: string(originalRaw),
			RawProposal:         string(currentRaw),
		},
		FanoutItem: string(clusterRaw),
	}
	msgs := projectFindingCluster(snap)
	body := msgs[0].Content

	assert.Contains(t, body, "Election Critical Window protocol",
		"projection reads from current RawProposal so the iter-1 commitment is visible to the iter-2 elaborator")
	assert.NotContains(t, body, "initial prose",
		"projection does NOT fall back to the pre-iter-0 OriginalRawProposal body")
}

// TestClusterStepProjectionsRenderTheirData — cluster_findings and
// revise (with a FindingCluster fanout item) project the right data
// shape; each step's WorkflowStep carries the correct Project closure
// so the executor doesn't need a central dispatch.
func TestClusterStepProjectionsRenderTheirData(t *testing.T) {
	t.Run("projectClusterFindings renders unmatched findings", func(t *testing.T) {
		snap := StateSnapshot[PlanningState]{
			State: PlanningState{
				Prompt:            "Build it.",
				UnmatchedFindings: []string{"some finding"},
			},
		}
		msgs := projectClusterFindings(snap)
		require.Len(t, msgs, 1)
		assert.Contains(t, msgs[0].Content, "Findings to cluster")
	})

	t.Run("projectFindingCluster renders the cluster's targeted node", func(t *testing.T) {
		cluster := FindingCluster{
			Topic:    "feat-a",
			NodeID:   "feat-a",
			AgentID:  "spec-feature-elaborator",
			Findings: []string{"x"},
		}
		clusterRaw, _ := json.Marshal(cluster)
		snap := StateSnapshot[PlanningState]{
			State:      PlanningState{Prompt: "Build it."},
			FanoutItem: string(clusterRaw),
		}
		msgs := projectFindingCluster(snap)
		assert.Contains(t, msgs[0].Content, "feat-a")
		assert.Contains(t, msgs[0].Content, "Findings to address")
	})
}
