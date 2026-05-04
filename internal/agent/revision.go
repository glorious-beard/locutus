// Package agent — revise-step fanout (Phase 1 of council-tools-and-revise-fanout).
//
// The revise step used to be a single architect call producing the full
// RawSpecProposal. On real runs (winplan 2026-05-02) the architect
// short-circuited under critic-finding pressure and emitted empty
// `decisions: [{}]` placeholders on every strategy. The reconciler
// dropped the placeholders, leaving every persisted strategy with zero
// decisions — exactly the failure Phase 3's elaborate fanout was
// designed to prevent, just one round later.
//
// The new revise topology:
//
//   triage → routes critic concerns to per-node revisions or additions
//          → revise_features (fanout, one elaborator call per affected feature)
//          → revise_strategies (fanout, one elaborator call per affected strategy)
//          → revise_additions (single architect call, only when there are additions)
//          → reconcile_revise (consumes the merged RawSpecProposal)
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

// RevisionPlan is the spec_revision_triager agent's output. EVERY
// critic finding routes to one of three buckets — there is no
// fourth "non-actionable, omit" bucket. The triager's authority is
// routing, not judging actionability; the critic already did the
// judgment by emitting the finding (DJ-095).
//
//   - feature_revisions: concerns targeting an existing feature
//   - strategy_revisions: concerns targeting an existing strategy
//   - additions: concerns proposing a missing feature/strategy,
//     each carrying a `kind` so the per-finding fanout dispatches
//     to the right elaborator agent
//
// Splitting features and strategies into separate revision arrays
// (rather than a single revisions[] with a kind field) lets the
// workflow's two fanouts read each array directly via dotted state
// path, the same way elaborate_features and elaborate_strategies
// read outline.features and outline.strategies. Additions are a
// single array because the kind tag is what the fanout filter
// uses to dispatch the right elaborator agent.
type RevisionPlan struct {
	FeatureRevisions  []NodeRevision `json:"feature_revisions,omitempty"`
	StrategyRevisions []NodeRevision `json:"strategy_revisions,omitempty"`
	Additions         []AddedNode    `json:"additions,omitempty"`
}

// NodeRevision is one routed revision: a node id and the concerns
// targeting it. The fanout dispatcher spawns one elaborator call per
// NodeRevision; the elaborator's revise-mode projection assembles a
// prompt with the prior node content + the targeted concerns.
type NodeRevision struct {
	NodeID   string   `json:"node_id"`
	Concerns []string `json:"concerns"`
}

// AddedNode is one routed addition: a critic finding proposing a
// missing feature or strategy. The kind decides which elaborator
// agent the fanout dispatches to; the source_concern is the verbatim
// finding text the elaborator addresses by inventing one node
// (id, title, decisions) from scratch.
//
// Empty kind defaults to "strategy" downstream — most ambiguous
// "missing X" findings turn out to be missing-strategy gaps, and a
// strategy that should have been a feature is recoverable by the
// reconciler / next refine pass; an addition silently skipped is not.
type AddedNode struct {
	Kind          string `json:"kind"`           // "feature" or "strategy"
	SourceConcern string `json:"source_concern"` // verbatim critic finding text
}

// assembleRevisedRawProposal builds the merged RawSpecProposal that
// reconcile_revise consumes. Reads:
//
//   - state.OriginalRawProposal — the assembled output of the elaborate
//     fanouts (before any revise activity)
//   - state.RevisedFeatures / state.RevisedStrategies — per-node revised
//     RawFeatureProposal / RawStrategyProposal JSONs from the revise
//     fanouts
//   - state.AdditionProposals — partial RawSpecProposal from
//     revise_additions (when present)
//
// Revisions replace the original by ID match. Additions append. Order
// of original entries is preserved so trace consumers see a stable
// shape across runs.
//
// Returns (assembled, true) when at least one revision or addition is
// present. Returns (original, true) unchanged when nothing has been
// revised yet — safe to call after every revise-related merge.
//
// Best-effort: malformed individual revision/addition entries are
// dropped with a slog.Warn rather than failing the whole assembly.
// One bad elaborator output shouldn't poison the rest, mirroring the
// elaborate path's failure-isolation policy.
func assembleRevisedRawProposal(state *PlanningState) (string, bool) {
	if state == nil || state.OriginalRawProposal == "" {
		return "", false
	}
	var original RawSpecProposal
	if err := json.Unmarshal([]byte(state.OriginalRawProposal), &original); err != nil {
		slog.Warn("assemble revised raw proposal: original is malformed", "error", err)
		return "", false
	}

	// Index revised features/strategies by id for replacement lookup.
	featureOverrides := make(map[string]RawFeatureProposal, len(state.RevisedFeatures))
	for _, raw := range state.RevisedFeatures {
		var f RawFeatureProposal
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			slog.Warn("assemble revised raw proposal: skipping malformed feature revision", "error", err)
			continue
		}
		if f.ID == "" {
			slog.Warn("assemble revised raw proposal: skipping feature revision with empty id")
			continue
		}
		featureOverrides[f.ID] = f
	}
	strategyOverrides := make(map[string]RawStrategyProposal, len(state.RevisedStrategies))
	for _, raw := range state.RevisedStrategies {
		var s RawStrategyProposal
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			slog.Warn("assemble revised raw proposal: skipping malformed strategy revision", "error", err)
			continue
		}
		if s.ID == "" {
			slog.Warn("assemble revised raw proposal: skipping strategy revision with empty id")
			continue
		}
		strategyOverrides[s.ID] = s
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

	// Additions are per-finding elaborator outputs (Phase 4 fanout).
	// Each entry in state.AdditionProposals is one RawFeatureProposal
	// or RawStrategyProposal JSON; the kind is inferred from the id
	// prefix (`feat-` / `strat-`). De-dup by id against what's already
	// in merged so a hallucinated addition that collides with an
	// existing id doesn't double-up; the reconciler downstream catches
	// semantic conflicts (5 critics' "missing observability" → 5
	// elaborator outputs collapsed to 1 canonical strategy).
	existingFeatureIDs := make(map[string]struct{}, len(merged.Features))
	for _, f := range merged.Features {
		existingFeatureIDs[f.ID] = struct{}{}
	}
	existingStrategyIDs := make(map[string]struct{}, len(merged.Strategies))
	for _, s := range merged.Strategies {
		existingStrategyIDs[s.ID] = struct{}{}
	}
	for _, raw := range state.AdditionProposals {
		switch {
		case strings.HasPrefix(strings.TrimSpace(extractRawID(raw)), "feat-"):
			var f RawFeatureProposal
			if err := json.Unmarshal([]byte(raw), &f); err != nil {
				slog.Warn("assemble revised raw proposal: skipping malformed feature addition", "error", err)
				continue
			}
			if _, dup := existingFeatureIDs[f.ID]; dup {
				slog.Warn("assemble revised raw proposal: addition collides with existing feature id", "id", f.ID)
				continue
			}
			merged.Features = append(merged.Features, f)
			existingFeatureIDs[f.ID] = struct{}{}
		case strings.HasPrefix(strings.TrimSpace(extractRawID(raw)), "strat-"):
			var s RawStrategyProposal
			if err := json.Unmarshal([]byte(raw), &s); err != nil {
				slog.Warn("assemble revised raw proposal: skipping malformed strategy addition", "error", err)
				continue
			}
			if _, dup := existingStrategyIDs[s.ID]; dup {
				slog.Warn("assemble revised raw proposal: addition collides with existing strategy id", "id", s.ID)
				continue
			}
			merged.Strategies = append(merged.Strategies, s)
			existingStrategyIDs[s.ID] = struct{}{}
		default:
			slog.Warn("assemble revised raw proposal: addition has unrecognized id prefix; expected feat- or strat-", "raw", truncateForLog(raw))
		}
	}

	out, err := json.Marshal(&merged)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// findRawFeature returns the prior RawFeatureProposal for the given id,
// or false when not present in the original raw proposal. Used by the
// revise-feature projection to render the prior node content into the
// elaborator's prompt.
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
// raw proposal, used by the triage projection to give the triager a
// concrete list of routable node ids.
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

// extractRawID parses raw JSON for an `id` field and returns it.
// Used by the additions-merge path to sniff whether a per-finding
// elaborator output is a feature or a strategy without committing
// to either schema up front. Returns empty when the raw JSON is
// malformed or has no id; the caller logs and skips.
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
// output — used by the additions-merge path so a malformed addition
// doesn't dump kilobytes of YAML into stderr. Operator can find the
// full payload in the per-call trace; the warning just signals which
// addition got skipped.
func truncateForLog(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
