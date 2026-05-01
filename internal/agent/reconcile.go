// Package agent — reconciliation surgery for the Phase 2 council.
//
// The architect emits inline decisions under each feature and strategy
// (RawSpecProposal). The reconciler agent clusters those inline decisions
// across the proposal and emits a ReconciliationVerdict describing what
// to do with each cluster: dedupe, resolve_conflict, or reuse_existing.
// Clusters not mentioned in the verdict are implicitly kept_separate —
// each unmentioned inline decision becomes its own canonical Decision.
//
// ApplyReconciliation is the deterministic Go function that consumes the
// verdict + raw proposal + existing-spec snapshot and produces a clean
// SpecProposal with shared decisions and assigned IDs. All judgment is
// in the LLM (the verdict); all surgery is in code (this file).

package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// mergeReconcile is the workflow merge handler for the reconcile step.
// It parses the upstream RawSpecProposal and the reconciler agent's
// verdict, runs ApplyReconciliation, and returns the canonical
// SpecProposal as JSON (for state.ProposedSpec) plus the applied actions
// (so callers can fire cascade rewrites for conflict resolutions).
func mergeReconcile(rawProposalJSON, verdictJSON string, existing *ExistingSpec) (string, []AppliedAction, error) {
	var raw RawSpecProposal
	if err := json.Unmarshal([]byte(rawProposalJSON), &raw); err != nil {
		return "", nil, fmt.Errorf("parse raw proposal: %w", err)
	}
	var verdict ReconciliationVerdict
	if err := json.Unmarshal([]byte(verdictJSON), &verdict); err != nil {
		return "", nil, fmt.Errorf("parse verdict: %w", err)
	}
	canonical, applied, err := ApplyReconciliation(&raw, verdict, existing)
	if err != nil {
		return "", nil, err
	}
	canonical.SortDecisions()
	out, err := json.Marshal(canonical)
	if err != nil {
		return "", nil, fmt.Errorf("marshal canonical proposal: %w", err)
	}
	return string(out), applied, nil
}

// appendConflictActions filters the applied actions for conflict
// resolutions (the only kind that triggers cascade rewrites) and appends
// them to the running list on PlanningState. Dedupe and reuse_existing
// don't change a parent's prose intent — only resolve_conflict does.
func appendConflictActions(existing []AppliedAction, applied []AppliedAction) []AppliedAction {
	for _, a := range applied {
		if a.Kind == "resolve_conflict" {
			existing = append(existing, a)
		}
	}
	return existing
}

// ReconciliationVerdict is the spec_reconciler agent's structured output.
// It describes what to do with each cluster of inline decisions across
// the raw proposal.
type ReconciliationVerdict struct {
	Actions []ReconciliationAction `json:"actions,omitempty"`
}

// ReconciliationAction is one cluster's resolution. Sources point at
// specific (parent, decision-index) tuples in the RawSpecProposal so
// ApplyReconciliation can mechanically rewrite parents to reference
// the canonical decision.
//
// Action kinds:
//   - "dedupe":           identical decisions → one canonical.
//   - "resolve_conflict": incompatible decisions on the same question →
//                         winner survives; loser → winner.alternatives[];
//                         caller fires cascade rewrite for affected nodes.
//   - "reuse_existing":   cluster maps to an existing-spec decision; reuse
//                         that ID instead of minting a new one. Canonical
//                         field is unused.
//
// keep_separate is implicit: any inline decision not referenced by an
// action becomes its own canonical Decision with a slug-derived ID.
type ReconciliationAction struct {
	Kind            string                  `json:"kind"`
	Sources         []DecisionSourceRef     `json:"sources"`
	Canonical       *InlineDecisionProposal `json:"canonical,omitempty"`
	Loser           *InlineDecisionProposal `json:"loser,omitempty"`
	RejectedBecause string                  `json:"rejected_because,omitempty"`
	ExistingID      string                  `json:"existing_id,omitempty"`
}

// DecisionSourceRef pinpoints one inline decision in a RawSpecProposal.
// Parent kind is "feature" or "strategy"; ParentID is the feature/strategy
// ID; Index is the position in that parent's decisions[] slice.
type DecisionSourceRef struct {
	ParentKind string `json:"parent_kind"`
	ParentID   string `json:"parent_id"`
	Index      int    `json:"index"`
}

// AppliedAction records what ApplyReconciliation did with one verdict
// action. Returned to callers so they know which canonical IDs landed
// where, and which conflicts fired (so the caller can trigger cascade
// rewrites for affected feature/strategy nodes).
type AppliedAction struct {
	Kind          string              // mirrors ReconciliationAction.Kind
	CanonicalID   string              // ID assigned (or reused) for the cluster
	AffectedNodes []DecisionSourceRef // sources in the cluster — feature/strategy IDs whose prose may need cascade-rewrite
}

// ApplyReconciliation transforms a RawSpecProposal + verdict into a
// canonical SpecProposal with shared, ID'd decisions. Pure function:
// same inputs → same outputs.
//
// Algorithm:
//  1. Walk verdict.Actions and apply each. For each action, mark its
//     source inline decisions as "claimed" so they don't get duplicated
//     in step 2. Mint or reuse the canonical decision's ID.
//  2. Walk every parent's inline decisions. For each unclaimed decision,
//     mint a canonical decision with a slug-derived ID and record the
//     reference on the parent.
//  3. Emit FeatureProposal / StrategyProposal with decision-id references
//     and a shared top-level decisions[] array.
//
// IDs are deterministic: dec-<slug-of-canonical-title>. Collisions (two
// distinct canonical titles slugifying to the same string) get -2, -3
// suffixes appended.
//
// existing, when non-nil, supplies decisions whose IDs may be reused
// via the "reuse_existing" action; ApplyReconciliation does not mutate
// or re-emit existing decisions, just references them.
func ApplyReconciliation(raw *RawSpecProposal, verdict ReconciliationVerdict, existing *ExistingSpec) (*SpecProposal, []AppliedAction, error) {
	if raw == nil {
		return &SpecProposal{}, nil, nil
	}

	out := &SpecProposal{}
	applied := make([]AppliedAction, 0, len(verdict.Actions))

	// Track which (parent_kind, parent_id, index) tuples have been
	// claimed by an action so the implicit-keep-separate pass doesn't
	// emit them twice.
	claimed := make(map[string]struct{})
	claimKey := func(s DecisionSourceRef) string {
		return fmt.Sprintf("%s/%s/%d", s.ParentKind, s.ParentID, s.Index)
	}

	// per-parent map of inline-decision-index → canonical-id, used to
	// rewrite each parent's decisions[] into a list of IDs at the end.
	type parentDecisionRefs struct {
		ids []string
	}
	featureRefs := make(map[string]*parentDecisionRefs, len(raw.Features))
	for _, f := range raw.Features {
		featureRefs[f.ID] = &parentDecisionRefs{}
	}
	strategyRefs := make(map[string]*parentDecisionRefs, len(raw.Strategies))
	for _, s := range raw.Strategies {
		strategyRefs[s.ID] = &parentDecisionRefs{}
	}

	// Existing decision IDs (for ID-collision avoidance during minting).
	usedIDs := make(map[string]struct{})
	if existing != nil {
		for _, d := range existing.Decisions {
			usedIDs[d.ID] = struct{}{}
		}
	}

	addDecisionRef := func(s DecisionSourceRef, id string) error {
		switch s.ParentKind {
		case "feature":
			refs, ok := featureRefs[s.ParentID]
			if !ok {
				return fmt.Errorf("reconcile: source feature %q not in proposal", s.ParentID)
			}
			refs.ids = append(refs.ids, id)
		case "strategy":
			refs, ok := strategyRefs[s.ParentID]
			if !ok {
				return fmt.Errorf("reconcile: source strategy %q not in proposal", s.ParentID)
			}
			refs.ids = append(refs.ids, id)
		default:
			return fmt.Errorf("reconcile: source parent_kind %q not recognized (want feature|strategy)", s.ParentKind)
		}
		return nil
	}

	for i, action := range verdict.Actions {
		switch action.Kind {
		case "dedupe", "resolve_conflict":
			if action.Canonical == nil {
				return nil, applied, fmt.Errorf("reconcile action %d (%s): canonical decision missing", i, action.Kind)
			}
			if isEmptyInlineDecision(*action.Canonical) {
				return nil, applied, fmt.Errorf("reconcile action %d (%s): canonical decision has empty title", i, action.Kind)
			}
			id := mintDecisionID(action.Canonical.Title, usedIDs)
			usedIDs[id] = struct{}{}
			canonical := canonicalDecisionFromInline(*action.Canonical, id)
			if action.Kind == "resolve_conflict" && action.Loser != nil {
				canonical.Alternatives = append(canonical.Alternatives, spec.Alternative{
					Name:            action.Loser.Title,
					Rationale:       action.Loser.Rationale,
					RejectedBecause: action.RejectedBecause,
				})
			}
			out.Decisions = append(out.Decisions, canonical)
			for _, s := range action.Sources {
				claimed[claimKey(s)] = struct{}{}
				if err := addDecisionRef(s, id); err != nil {
					return nil, applied, err
				}
			}
			applied = append(applied, AppliedAction{
				Kind:          action.Kind,
				CanonicalID:   id,
				AffectedNodes: action.Sources,
			})
		case "reuse_existing":
			if action.ExistingID == "" {
				return nil, applied, fmt.Errorf("reconcile action %d (reuse_existing): existing_id missing", i)
			}
			if existing == nil || !existingHasDecision(existing, action.ExistingID) {
				return nil, applied, fmt.Errorf("reconcile action %d: reuse_existing references unknown id %q", i, action.ExistingID)
			}
			for _, s := range action.Sources {
				claimed[claimKey(s)] = struct{}{}
				if err := addDecisionRef(s, action.ExistingID); err != nil {
					return nil, applied, err
				}
			}
			applied = append(applied, AppliedAction{
				Kind:          action.Kind,
				CanonicalID:   action.ExistingID,
				AffectedNodes: action.Sources,
			})
		default:
			return nil, applied, fmt.Errorf("reconcile action %d: unknown kind %q", i, action.Kind)
		}
	}

	// Implicit keep-separate pass: every inline decision not claimed
	// by an action becomes its own canonical Decision with a fresh ID.
	//
	// Inline decisions whose Title is empty are treated as architect
	// placeholder noise (Gemini Flash has been observed emitting
	// `decisions: [{}]` on strategies — empty objects that satisfy
	// schema shape but carry no content). Drop them rather than minting
	// `dec-untitled` IDs that pollute the spec graph. The integrity
	// critic flags downstream impact (a feature that ends up with zero
	// decisions because its inline entries were all empty) so revise
	// can re-emit real content.
	for _, f := range raw.Features {
		for idx, d := range f.Decisions {
			ref := DecisionSourceRef{ParentKind: "feature", ParentID: f.ID, Index: idx}
			if _, ok := claimed[claimKey(ref)]; ok {
				continue
			}
			if isEmptyInlineDecision(d) {
				continue
			}
			id := mintDecisionID(d.Title, usedIDs)
			usedIDs[id] = struct{}{}
			out.Decisions = append(out.Decisions, canonicalDecisionFromInline(d, id))
			featureRefs[f.ID].ids = append(featureRefs[f.ID].ids, id)
		}
	}
	for _, s := range raw.Strategies {
		for idx, d := range s.Decisions {
			ref := DecisionSourceRef{ParentKind: "strategy", ParentID: s.ID, Index: idx}
			if _, ok := claimed[claimKey(ref)]; ok {
				continue
			}
			if isEmptyInlineDecision(d) {
				continue
			}
			id := mintDecisionID(d.Title, usedIDs)
			usedIDs[id] = struct{}{}
			out.Decisions = append(out.Decisions, canonicalDecisionFromInline(d, id))
			strategyRefs[s.ID].ids = append(strategyRefs[s.ID].ids, id)
		}
	}

	// Build canonical features and strategies with id-only decision refs.
	for _, f := range raw.Features {
		out.Features = append(out.Features, FeatureProposal{
			ID:                 f.ID,
			Title:              f.Title,
			Description:        f.Description,
			AcceptanceCriteria: f.AcceptanceCriteria,
			Decisions:          dedupeRefs(featureRefs[f.ID].ids),
		})
	}
	for _, s := range raw.Strategies {
		out.Strategies = append(out.Strategies, StrategyProposal{
			ID:        s.ID,
			Title:     s.Title,
			Kind:      s.Kind,
			Body:      s.Body,
			Decisions: dedupeRefs(strategyRefs[s.ID].ids),
		})
	}

	return out, applied, nil
}

// canonicalDecisionFromInline lifts an InlineDecisionProposal into the
// id-bearing DecisionProposal shape the persistence layer expects.
func canonicalDecisionFromInline(d InlineDecisionProposal, id string) DecisionProposal {
	return DecisionProposal{
		ID:                 id,
		Title:              d.Title,
		Rationale:          d.Rationale,
		Confidence:         d.Confidence,
		Alternatives:       d.Alternatives,
		Citations:          d.Citations,
		ArchitectRationale: d.ArchitectRationale,
	}
}

// mintDecisionID derives a stable id from a decision title. Collisions
// against `used` get a numeric suffix appended. Caller MUST guard
// against empty titles via isEmptyInlineDecision; minting from an empty
// title would produce a `dec-` slug that pollutes the spec graph.
func mintDecisionID(title string, used map[string]struct{}) string {
	base := "dec-" + slugify(title)
	if _, taken := used[base]; !taken {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if _, taken := used[candidate]; !taken {
			return candidate
		}
	}
}

// isEmptyInlineDecision reports whether the inline decision is a
// placeholder with no usable content. The architect's contract requires
// title + rationale + at least one alternative + at least one citation;
// empty objects (`{}`) and title-only stubs are dropped at apply time
// rather than persisted as `dec-untitled` noise. Title is the load-
// bearing field — the slug ID derives from it, and a decision with no
// title is meaningless to a coding agent or auditor.
func isEmptyInlineDecision(d InlineDecisionProposal) bool {
	return strings.TrimSpace(d.Title) == ""
}

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	out := strings.ToLower(strings.TrimSpace(s))
	out = nonAlphaNum.ReplaceAllString(out, "-")
	out = strings.Trim(out, "-")
	return out
}

func existingHasDecision(e *ExistingSpec, id string) bool {
	if e == nil {
		return false
	}
	for _, d := range e.Decisions {
		if d.ID == id {
			return true
		}
	}
	return false
}

// dedupeRefs removes duplicate decision IDs from a parent's decisions[]
// while preserving first-seen order. A reconciler verdict that points
// the same parent at the same canonical via two different inline-source
// indexes (legitimate when the architect inadvertently described the
// same decision twice on the same parent) collapses to one reference.
func dedupeRefs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// SortDecisions sorts a SpecProposal's top-level decisions[] alphabetically
// by ID for deterministic output across runs (map iteration is otherwise
// non-deterministic when callers build the proposal from maps). Callers
// that want stability across runs should call this before persisting.
func (p *SpecProposal) SortDecisions() {
	sort.Slice(p.Decisions, func(i, j int) bool {
		return p.Decisions[i].ID < p.Decisions[j].ID
	})
}
