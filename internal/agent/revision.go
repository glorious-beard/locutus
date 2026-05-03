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
)

// RevisionPlan is the spec_revision_triager agent's output. Each
// actionable critic finding is routed to one of three buckets:
//
//   - feature_revisions: concerns targeting an existing feature
//   - strategy_revisions: concerns targeting an existing strategy
//   - additions: concerns proposing a missing feature/strategy
//
// Findings the triager judges non-actionable are simply omitted —
// the trace captures both the input concerns and the output buckets,
// so an operator can compute "what got dropped" by diffing without
// needing a separate `discarded[]` field. Dead output surface is
// degenerate-loop bait on weaker models, so we keep RevisionPlan
// to exactly the fields downstream code consumes.
//
// Splitting features and strategies into separate arrays (rather than
// a single revisions[] with a kind field) lets the workflow's two
// fanouts read each array directly via dotted state path, the same
// way elaborate_features and elaborate_strategies read outline.features
// and outline.strategies.
type RevisionPlan struct {
	FeatureRevisions  []NodeRevision `json:"feature_revisions,omitempty"`
	StrategyRevisions []NodeRevision `json:"strategy_revisions,omitempty"`
	Additions         []string       `json:"additions,omitempty"`
}

// NodeRevision is one routed revision: a node id and the concerns
// targeting it. The fanout dispatcher spawns one elaborator call per
// NodeRevision; the elaborator's revise-mode projection assembles a
// prompt with the prior node content + the targeted concerns.
type NodeRevision struct {
	NodeID   string   `json:"node_id"`
	Concerns []string `json:"concerns"`
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

	// Additions are partial RawSpecProposals appended after the
	// original (and revised) entries. Multiple addition merges (e.g. if
	// the workflow ever runs multiple revise rounds) accumulate.
	if state.AdditionProposals != "" {
		var additions RawSpecProposal
		if err := json.Unmarshal([]byte(state.AdditionProposals), &additions); err != nil {
			slog.Warn("assemble revised raw proposal: skipping malformed additions", "error", err)
		} else {
			// De-dup by id against what's already in merged so a
			// hallucinated addition that collides with an existing id
			// doesn't double-up. Last-writer-wins on collision; the
			// reconciler downstream catches semantic conflicts.
			existingFeatureIDs := make(map[string]struct{}, len(merged.Features))
			for _, f := range merged.Features {
				existingFeatureIDs[f.ID] = struct{}{}
			}
			for _, f := range additions.Features {
				if _, dup := existingFeatureIDs[f.ID]; dup {
					slog.Warn("assemble revised raw proposal: addition collides with existing feature id", "id", f.ID)
					continue
				}
				merged.Features = append(merged.Features, f)
				existingFeatureIDs[f.ID] = struct{}{}
			}
			existingStrategyIDs := make(map[string]struct{}, len(merged.Strategies))
			for _, s := range merged.Strategies {
				existingStrategyIDs[s.ID] = struct{}{}
			}
			for _, s := range additions.Strategies {
				if _, dup := existingStrategyIDs[s.ID]; dup {
					slog.Warn("assemble revised raw proposal: addition collides with existing strategy id", "id", s.ID)
					continue
				}
				merged.Strategies = append(merged.Strategies, s)
				existingStrategyIDs[s.ID] = struct{}{}
			}
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
