// Package agent — finding clustering (DJ-098).
//
// Replaces the spec_revision_triager + RevisionPlan three-bucket
// routing with a unified per-cluster elaborator fanout. Critics emit
// findings; the mechanical pre-pass groups findings that mention an
// existing node by id; the LLM clusterer groups the rest by topic.
// Each cluster goes to one elaborator call. The elaborator's output
// (a partial RawSpecProposal with one entry) discriminates between
// revise and add by whether the emitted id matches an existing node.
//
// Why this exists: the triager was the failure point three times in
// three iterations (DJ-092, DJ-095, DJ-097). Every iteration added
// complexity to the routing prompt. The deeper issue was that the
// triager did two jobs (route + decide intent) and an LLM under
// attention pressure lost one of them. The unified design removes
// the multi-bucket routing problem entirely; cluster correctness is
// provider-agnostic and tier-resilient.

package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// FindingCluster is one cluster of related critic findings, dispatched
// to a single elaborator call.
//
// Topic is a human-readable label set either by the mechanical pre-pass
// (e.g. "feat-dashboard") or by the LLM clusterer (e.g. "infrastructure-
// as-code and CI/CD").
//
// Findings carries the verbatim critic findings the elaborator must
// address. One finding may appear in at most one cluster.
//
// AgentID names the elaborator agent dispatched for this cluster:
// "spec_feature_elaborator" or "spec_strategy_elaborator". Set by the
// mechanical pre-pass (from the matched id's prefix) or by post-
// processing the clusterer's per-cluster `kind` field.
//
// NodeID is set when the cluster targets an existing node (revise
// mode); the elaborator preserves this id verbatim. Empty when the
// cluster proposes a new node (addition mode); the elaborator picks a
// slug-derived id.
type FindingCluster struct {
	Topic    string   `json:"topic"`
	Findings []string `json:"findings"`
	AgentID  string   `json:"agent_id"`
	NodeID   string   `json:"node_id,omitempty"`
}

// FindingClusters is the LLM clusterer's structured output: one cluster
// per topic, with every input finding routed into exactly one cluster.
// The clusterer cannot drop, paraphrase, or annotate findings — only
// group them.
type FindingClusters struct {
	Clusters []FindingCluster `json:"clusters,omitempty"`
}

// LLMFindingClusters is the schema the LLM clusterer emits directly.
// Topic + findings + kind only. Workflow-side code converts each to a
// FindingCluster with AgentID set from kind.
type LLMFindingClusters struct {
	Clusters []LLMFindingCluster `json:"clusters,omitempty"`
}

// LLMFindingCluster is the per-cluster shape the clusterer emits.
// Kind is "feature" or "strategy"; the workflow promotes it to AgentID.
type LLMFindingCluster struct {
	Topic    string   `json:"topic"`
	Findings []string `json:"findings"`
	Kind     string   `json:"kind"`
}

// idRefRegex matches feat-XXX and strat-XXX references in finding
// text. Used by the mechanical pre-pass to route findings that name
// an existing node directly.
var idRefRegex = regexp.MustCompile(`\b(feat-[a-z0-9][a-z0-9-]*|strat-[a-z0-9][a-z0-9-]*)\b`)

// MechanicalCluster groups concerns by id reference. Returns:
//
//   - clusters[]: one per existing node mentioned by id in any concern.
//     AgentID is set from the id prefix (feat- → spec_feature_elaborator,
//     strat- → spec_strategy_elaborator). NodeID is the matched id.
//   - unmatched[]: the verbatim text of findings that mentioned no
//     existing-node id. The LLM clusterer takes these.
//
// A finding mentioning two existing-node ids is duplicated into both
// clusters — same rule the triager used. Findings whose id reference
// names a node not in existingFeatureIDs / existingStrategyIDs go to
// unmatched (the id is invented or stale).
func MechanicalCluster(concerns []Concern, existingFeatureIDs, existingStrategyIDs []string) (clusters []FindingCluster, unmatched []string) {
	featureSet := make(map[string]struct{}, len(existingFeatureIDs))
	for _, id := range existingFeatureIDs {
		featureSet[id] = struct{}{}
	}
	strategySet := make(map[string]struct{}, len(existingStrategyIDs))
	for _, id := range existingStrategyIDs {
		strategySet[id] = struct{}{}
	}

	// Order-preserving cluster map: keep the order in which node ids
	// first appear so deterministic output across runs.
	clusterByID := make(map[string]*FindingCluster)
	var clusterOrder []string

	addToCluster := func(nodeID string, findingText string) {
		c, ok := clusterByID[nodeID]
		if !ok {
			agentID := "spec_strategy_elaborator"
			if strings.HasPrefix(nodeID, "feat-") {
				agentID = "spec_feature_elaborator"
			}
			c = &FindingCluster{
				Topic:   nodeID,
				AgentID: agentID,
				NodeID:  nodeID,
			}
			clusterByID[nodeID] = c
			clusterOrder = append(clusterOrder, nodeID)
		}
		c.Findings = append(c.Findings, findingText)
	}

	for _, concern := range concerns {
		text := concern.Text
		if strings.TrimSpace(text) == "" {
			continue
		}
		matches := idRefRegex.FindAllString(text, -1)
		// De-dup matches within one finding (e.g. a finding mentions
		// the same id twice) and filter to existing nodes.
		seen := make(map[string]struct{}, len(matches))
		var matched []string
		for _, m := range matches {
			if _, dup := seen[m]; dup {
				continue
			}
			seen[m] = struct{}{}
			if _, ok := featureSet[m]; ok {
				matched = append(matched, m)
				continue
			}
			if _, ok := strategySet[m]; ok {
				matched = append(matched, m)
				continue
			}
			// id mentioned but not in existing set — fall through to
			// unmatched (the id was hallucinated by the critic, or a
			// stale reference; the LLM clusterer will route it as a
			// missing-node finding).
		}
		if len(matched) == 0 {
			unmatched = append(unmatched, text)
			continue
		}
		for _, id := range matched {
			addToCluster(id, text)
		}
	}

	clusters = make([]FindingCluster, 0, len(clusterOrder))
	for _, id := range clusterOrder {
		clusters = append(clusters, *clusterByID[id])
	}
	return clusters, unmatched
}

// PromoteLLMClusters converts the LLM clusterer's output into a
// FindingClusters list with AgentID set from each cluster's kind.
// Empty/unknown kind defaults to "strategy" (most "missing X" findings
// are missing-strategy gaps; misclassification is recoverable by the
// reconciler / next refine pass).
//
// Empty findings arrays are dropped with a slog.Warn — the same
// `[{}]` placeholder pattern that broke earlier triager iterations.
func PromoteLLMClusters(raw string) []FindingCluster {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var llm LLMFindingClusters
	if err := json.Unmarshal([]byte(raw), &llm); err != nil {
		slog.Warn("promote LLM clusters: malformed clusterer output", "error", err)
		return nil
	}
	out := make([]FindingCluster, 0, len(llm.Clusters))
	for i, c := range llm.Clusters {
		if len(c.Findings) == 0 {
			slog.Warn("promote LLM clusters: dropping cluster with no findings (likely placeholder)", "index", i, "topic", c.Topic)
			continue
		}
		agentID := "spec_strategy_elaborator"
		if strings.TrimSpace(c.Kind) == "feature" {
			agentID = "spec_feature_elaborator"
		}
		topic := strings.TrimSpace(c.Topic)
		if topic == "" {
			topic = fmt.Sprintf("cluster-%d", i+1)
		}
		out = append(out, FindingCluster{
			Topic:    topic,
			Findings: c.Findings,
			AgentID:  agentID,
		})
	}
	return out
}

// CountUnmatched returns the count of verbatim findings the mechanical
// pre-pass left unmatched. Used by the workflow's `has_unmatched_findings`
// conditional to decide whether the LLM clusterer step should run.
func CountUnmatched(state *PlanningState) int {
	if state == nil {
		return 0
	}
	return len(state.UnmatchedFindings)
}
