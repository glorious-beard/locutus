// Package agent — revise-step assembly (DJ-098 unified per-cluster).
//
// History (kept for the next person who wonders why the design is shaped
// this way):
//
//   - DJ-092 introduced per-node revise fanout (one elaborator call per
//     affected feature/strategy) to fix the architect short-circuiting
//     under multi-finding pressure with empty `decisions: [{}]`.
//   - DJ-095 added a triager + RevisionPlan + per-finding addition
//     fanout to handle "missing X" findings the original design dropped.
//   - DJ-097 tightened the triager schema example to defeat `[{}]`
//     placeholders — but the same model that emitted placeholders then
//     dropped one of three array fields, losing 25 of 40 critic findings.
//
// DJ-098 collapsed the triager. Critic findings flow through:
//
//   - MechanicalCluster (Go, no LLM): groups findings by id-mention into
//     per-node clusters. Findings without an id reference fall through.
//   - spec_finding_clusterer (LLM, single job): groups the unmatched
//     findings by topic. Schema is one array; one decision dimension.
//   - revise (fanout: findings.clusters): per-cluster elaborator call.
//     Each call emits one RawFeatureProposal or RawStrategyProposal
//     addressing the cluster's findings. The output's id discriminates
//     revise (matches existing) vs add (fresh id).
//
// The merged RawSpecProposal is the original (state.OriginalRawProposal)
// with revised nodes swapped in by ID and additions appended. Untouched
// nodes carry forward verbatim.

package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// assembleRevisedRawProposal builds the merged RawSpecProposal that
// reconcile_revise consumes. Reads:
//
//   - state.OriginalRawProposal — the assembled output of the elaborate
//     fanouts (before any revise activity)
//   - state.RevisedNodes — per-cluster elaborator outputs, each one a
//     RawFeatureProposal or RawStrategyProposal JSON. The id prefix
//     (feat- vs strat-) discriminates kind; whether the id matches an
//     existing node discriminates revise (override) vs add (append).
//
// Revisions replace the original by ID match. Additions append. Order
// of original entries is preserved so trace consumers see a stable
// shape across runs.
//
// Returns (assembled, true) when at least one revised node is present.
// Returns (original, true) unchanged when nothing has been revised yet
// — safe to call after every revise-related merge.
//
// Best-effort: malformed individual entries are dropped with a slog.Warn
// rather than failing the whole assembly. One bad elaborator output
// shouldn't poison the rest, mirroring the elaborate path's failure-
// isolation policy.
func assembleRevisedRawProposal(state *PlanningState) (string, bool) {
	if state == nil || state.OriginalRawProposal == "" {
		return "", false
	}
	var original RawSpecProposal
	if err := json.Unmarshal([]byte(state.OriginalRawProposal), &original); err != nil {
		slog.Warn("assemble revised raw proposal: original is malformed", "error", err)
		return "", false
	}

	// Index original ids so we can split each revised node into
	// override (id matches) vs addition (fresh id).
	originalFeatureIDs := make(map[string]struct{}, len(original.Features))
	for _, f := range original.Features {
		originalFeatureIDs[f.ID] = struct{}{}
	}
	originalStrategyIDs := make(map[string]struct{}, len(original.Strategies))
	for _, s := range original.Strategies {
		originalStrategyIDs[s.ID] = struct{}{}
	}

	featureOverrides := make(map[string]RawFeatureProposal, len(state.RevisedNodes))
	strategyOverrides := make(map[string]RawStrategyProposal, len(state.RevisedNodes))
	var featureAdditions []RawFeatureProposal
	var strategyAdditions []RawStrategyProposal

	for _, raw := range state.RevisedNodes {
		id := strings.TrimSpace(extractRawID(raw))
		switch {
		case strings.HasPrefix(id, "feat-"):
			var f RawFeatureProposal
			if err := json.Unmarshal([]byte(raw), &f); err != nil {
				slog.Warn("assemble revised raw proposal: skipping malformed feature node", "error", err)
				continue
			}
			if _, isOverride := originalFeatureIDs[f.ID]; isOverride {
				featureOverrides[f.ID] = f
				continue
			}
			featureAdditions = append(featureAdditions, f)
		case strings.HasPrefix(id, "strat-"):
			var s RawStrategyProposal
			if err := json.Unmarshal([]byte(raw), &s); err != nil {
				slog.Warn("assemble revised raw proposal: skipping malformed strategy node", "error", err)
				continue
			}
			if _, isOverride := originalStrategyIDs[s.ID]; isOverride {
				strategyOverrides[s.ID] = s
				continue
			}
			strategyAdditions = append(strategyAdditions, s)
		default:
			slog.Warn("assemble revised raw proposal: revised node has unrecognized id prefix; expected feat- or strat-", "raw", truncateForLog(raw))
		}
	}

	merged := RawSpecProposal{}
	for _, f := range original.Features {
		if override, ok := featureOverrides[f.ID]; ok {
			merged.Features = append(merged.Features, override)
			continue
		}
		merged.Features = append(merged.Features, f)
	}
	for _, s := range original.Strategies {
		if override, ok := strategyOverrides[s.ID]; ok {
			merged.Strategies = append(merged.Strategies, override)
			continue
		}
		merged.Strategies = append(merged.Strategies, s)
	}

	// Additions: dedupe by id against what's already in merged so a
	// hallucinated id collision doesn't double-up. Semantic dedup
	// (5 elaborators independently inventing strat-observability,
	// strat-monitoring, strat-otel) is OUT OF SCOPE here — the
	// clustering step upstream is meant to collapse those 5 critic
	// findings into 1 cluster, so this code rarely sees the case.
	// If it does, the next refine pass cleans up.
	mergedFeatureIDs := make(map[string]struct{}, len(merged.Features))
	for _, f := range merged.Features {
		mergedFeatureIDs[f.ID] = struct{}{}
	}
	mergedStrategyIDs := make(map[string]struct{}, len(merged.Strategies))
	for _, s := range merged.Strategies {
		mergedStrategyIDs[s.ID] = struct{}{}
	}
	for _, f := range featureAdditions {
		if _, dup := mergedFeatureIDs[f.ID]; dup {
			slog.Warn("assemble revised raw proposal: feature addition collides with existing id", "id", f.ID)
			continue
		}
		merged.Features = append(merged.Features, f)
		mergedFeatureIDs[f.ID] = struct{}{}
	}
	for _, s := range strategyAdditions {
		if _, dup := mergedStrategyIDs[s.ID]; dup {
			slog.Warn("assemble revised raw proposal: strategy addition collides with existing id", "id", s.ID)
			continue
		}
		merged.Strategies = append(merged.Strategies, s)
		mergedStrategyIDs[s.ID] = struct{}{}
	}

	out, err := json.Marshal(&merged)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// findRawFeature returns the prior RawFeatureProposal for the given id,
// or false when not present in the original raw proposal. Used by the
// per-cluster projection to render prior node content when the cluster
// targets an existing feature (revise mode).
func findRawFeature(originalRaw, id string) (RawFeatureProposal, bool) {
	if originalRaw == "" || id == "" {
		return RawFeatureProposal{}, false
	}
	var p RawSpecProposal
	if err := json.Unmarshal([]byte(originalRaw), &p); err != nil {
		return RawFeatureProposal{}, false
	}
	for _, f := range p.Features {
		if f.ID == id {
			return f, true
		}
	}
	return RawFeatureProposal{}, false
}

// findRawStrategy is the strategy counterpart.
func findRawStrategy(originalRaw, id string) (RawStrategyProposal, bool) {
	if originalRaw == "" || id == "" {
		return RawStrategyProposal{}, false
	}
	var p RawSpecProposal
	if err := json.Unmarshal([]byte(originalRaw), &p); err != nil {
		return RawStrategyProposal{}, false
	}
	for _, s := range p.Strategies {
		if s.ID == id {
			return s, true
		}
	}
	return RawStrategyProposal{}, false
}

// proposalNodeIDs returns the feature and strategy ids present in the
// raw proposal, used by the per-cluster projection to give the
// elaborator a list of existing ids it must not collide with when
// inventing a new node.
func proposalNodeIDs(rawProposal string) (features []string, strategies []string) {
	if rawProposal == "" {
		return nil, nil
	}
	var p RawSpecProposal
	if err := json.Unmarshal([]byte(rawProposal), &p); err != nil {
		return nil, nil
	}
	for _, f := range p.Features {
		features = append(features, fmt.Sprintf("%s — %s", f.ID, f.Title))
	}
	for _, s := range p.Strategies {
		strategies = append(strategies, fmt.Sprintf("%s (%s) — %s", s.ID, s.Kind, s.Title))
	}
	return features, strategies
}

// proposalIDLists returns the bare feature and strategy ids in the raw
// proposal, used by MechanicalCluster to filter id references against
// the actually-present-in-proposal set.
func proposalIDLists(rawProposal string) (features []string, strategies []string) {
	if rawProposal == "" {
		return nil, nil
	}
	var p RawSpecProposal
	if err := json.Unmarshal([]byte(rawProposal), &p); err != nil {
		return nil, nil
	}
	for _, f := range p.Features {
		features = append(features, f.ID)
	}
	for _, s := range p.Strategies {
		strategies = append(strategies, s.ID)
	}
	return features, strategies
}

// extractRawID parses raw JSON for an `id` field and returns it.
// Used to sniff whether a per-cluster elaborator output is a feature
// or a strategy without committing to either schema up front. Returns
// empty when the raw JSON is malformed or has no id; the caller logs
// and skips.
func extractRawID(raw string) string {
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return ""
	}
	return v.ID
}

// truncateForLog clips a long string to a fixed prefix for slog
// output so a malformed entry doesn't dump kilobytes of YAML into
// stderr. Operator can find the full payload in the per-call trace.
func truncateForLog(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
