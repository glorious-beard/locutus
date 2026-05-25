// Package agent — reconciliation surgery for the post-DJ-124 council.
//
// Under DJ-124, decisions are no longer inlined under each feature or
// strategy. The per-axis decision-elaborator produces canonical
// RawDecisionProposal entries (top-level RawSpecProposal.Decisions[]),
// and the narrative-elaborators reference those by id under each
// feature / strategy's Decisions[] field. The reconciler's job is now
// limited to:
//
//   - field-mapping each RawDecisionProposal into a canonical
//     DecisionProposal (id minting on the rare entry without an id;
//     suffixing on slug collisions);
//   - validating that every Feature.Decisions / Strategy.Decisions id
//     resolves to either a new decision in the output or an existing
//     decision in ExistingSpec — dangling references are surfaced as
//     integrity_violation AppliedAction entries so the council's
//     critic loop can address them.
//
// The reconciler agent's verdict (ReconciliationVerdict) is still parsed
// for compatibility with the unchanged spec-reconciler.md prompt and
// schema, but its content is ignored by ApplyReconciliation. Phase 5's
// workflow rewrite will remove the reconciler agent step entirely; this
// stage keeps the schema-level types stable so the agent's strict-mode
// output continues to validate at the API layer.

package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// mergeReconcile is the workflow merge handler for the reconcile step.
// It parses the upstream RawSpecProposal and the reconciler agent's
// verdict, runs ApplyReconciliation, and returns the canonical
// SpecProposal as JSON (for state.ProposedSpec) plus the applied actions
// (so callers can surface integrity violations downstream).
//
// Verdict JSON is parsed (with fence-stripping) but its content is
// ignored; the canonical proposal is derived purely from the raw
// proposal. TODO Phase 5: remove the verdict step entirely once the
// workflow no longer dispatches spec-reconciler.
func mergeReconcile(rawProposalJSON, verdictJSON string, existing *ExistingSpec) (string, []AppliedAction, error) {
	var raw RawSpecProposal
	if err := json.Unmarshal([]byte(rawProposalJSON), &raw); err != nil {
		return "", nil, fmt.Errorf("parse raw proposal: %w", err)
	}
	// Best-effort: the verdict is no-op for the new path but we still
	// parse it so a malformed reconciler agent output surfaces as a
	// workflow concern rather than silently passing through. A parse
	// error here is not fatal — we proceed with an empty verdict and
	// let the field-mapping path produce the canonical proposal.
	var verdict ReconciliationVerdict
	_ = json.Unmarshal([]byte(stripJSONFences(verdictJSON)), &verdict)

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

// stripJSONFences removes leading/trailing markdown code fences from s
// (```json ... ``` or ``` ... ```). The middle is returned as-is, with
// a defensive fallback to the original string when no fence is
// detected so well-formed JSON passes through untouched. Handles the
// common Gemini-with-tools case where the API drops JSON-mode and the
// model wraps in fences out of training-distribution habit.
func stripJSONFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	// Drop the opening fence line (might be ``` or ```json or ```JSON).
	if i := strings.Index(t, "\n"); i > 0 {
		t = t[i+1:]
	} else {
		// Single-line fence wrap is degenerate — return original so
		// the parser surfaces the error with the real bytes.
		return s
	}
	// Drop a trailing closing fence if present.
	t = strings.TrimRight(t, " \n\t")
	if strings.HasSuffix(t, "```") {
		t = strings.TrimSuffix(t, "```")
	}
	return strings.TrimSpace(t)
}

// appendConflictActions filters the applied actions for conflict
// resolutions (the only kind that triggers cascade rewrites) and appends
// them to the running list on PlanningState. Under DJ-124 the reconciler
// no longer emits resolve_conflict actions; the filter is preserved for
// telemetry consumers that may inspect the slice but the typical result
// is an empty list.
func appendConflictActions(existing []AppliedAction, applied []AppliedAction) []AppliedAction {
	for _, a := range applied {
		if a.Kind == "resolve_conflict" {
			existing = append(existing, a)
		}
	}
	return existing
}

// ReconciliationVerdict is the spec-reconciler agent's structured output.
// Under DJ-124 its content is ignored by ApplyReconciliation — the
// reconciler agent's prompt and schema are preserved only so the agent's
// strict-mode output continues to validate at the API layer pending
// Phase 5's workflow rewrite.
type ReconciliationVerdict struct {
	Actions []ReconciliationAction `json:"actions,omitempty" jsonschema:"description=Cluster resolutions across the raw proposal. Each action names a kind (dedupe / resolve_conflict / reuse_existing) and the source inline decisions it operates on. Under DJ-124 the workflow ignores verdict content; the field is retained for compatibility with the unchanged reconciler agent prompt."`
}

// ReconciliationAction is one cluster's resolution. Canonical and Loser
// are opaque json.RawMessage values — the reconciler agent's prompt
// (unchanged in this stage) still describes them as inline decisions,
// but ApplyReconciliation never reads them. TODO Phase 5: remove the
// reconciler step and these types.
type ReconciliationAction struct {
	Kind            string              `json:"kind" jsonschema:"enum=dedupe,enum=resolve_conflict,enum=reuse_existing,description=The cluster's resolution kind. Retained for compatibility with the unchanged reconciler agent prompt; verdict content is ignored under DJ-124."`
	Sources         []DecisionSourceRef `json:"sources" jsonschema:"minItems=1,description=The inline decisions this action operates on; identified by (parent_kind; parent_id; index) tuples within the input RawSpecProposal. Ignored downstream under DJ-124."`
	Canonical       json.RawMessage     `json:"canonical,omitempty" jsonschema:"description=Opaque object the reconciler agent emits for dedupe / resolve_conflict actions. Ignored by ApplyReconciliation under DJ-124."`
	Loser           json.RawMessage     `json:"loser,omitempty" jsonschema:"description=Opaque object the reconciler agent emits for resolve_conflict actions. Ignored under DJ-124."`
	RejectedBecause string              `json:"rejected_because,omitempty" jsonschema:"description=For kind=resolve_conflict only: one to two sentences explaining why Loser was rejected in favor of Canonical. Ignored under DJ-124."`
	ExistingID      string              `json:"existing_id,omitempty" jsonschema:"description=For kind=reuse_existing only: the id (starting 'dec-') of the existing decision the cluster maps to. Ignored under DJ-124."`
}

// DecisionSourceRef pinpoints one inline decision in a RawSpecProposal.
// Retained for verdict-shape compatibility under DJ-124; ApplyReconciliation
// uses it only when recording AffectedNodes on integrity_violation entries.
type DecisionSourceRef struct {
	ParentKind string `json:"parent_kind"`
	ParentID   string `json:"parent_id"`
	Index      int    `json:"index"`
}

// AppliedAction records what ApplyReconciliation did. Under DJ-124 the
// primary use is recording integrity_violation entries for feature /
// strategy decision references that don't resolve against the new +
// existing decision set; the workflow's critic loop surfaces these.
type AppliedAction struct {
	Kind          string              // "integrity_violation" under DJ-124; reserved for future categories
	CanonicalID   string              // the dangling decision id for integrity_violation
	AffectedNodes []DecisionSourceRef // parent feature/strategy whose Decisions[] carried the bad ref
}

// ApplyReconciliation field-maps a RawSpecProposal into a SpecProposal.
// Pure function: same inputs → same outputs. Under DJ-124 the verdict is
// ignored.
//
// Algorithm:
//  1. For each raw.Decisions[i], mint a canonical DecisionProposal:
//     preserve raw.ID when set (suffix on collision against existing or
//     prior new decisions); else mint via mintDecisionID.
//  2. For each raw.Features[i] and raw.Strategies[i], emit the matching
//     proposal preserving Decisions[] ids verbatim.
//  3. Validate every Decisions[] id resolves to a new decision in the
//     output or an existing decision in ExistingSpec; surface dangling
//     references as integrity_violation AppliedAction entries.
func ApplyReconciliation(raw *RawSpecProposal, verdict ReconciliationVerdict, existing *ExistingSpec) (*SpecProposal, []AppliedAction, error) {
	_ = verdict // verdict content is no-op pending Phase 5 workflow rewrite
	if raw == nil {
		return &SpecProposal{}, nil, nil
	}

	out := &SpecProposal{}
	var applied []AppliedAction

	// usedIDs tracks ids already taken across the existing spec and the
	// new decisions emitted so far, so mintDecisionID can suffix on
	// collision deterministically.
	usedIDs := make(map[string]struct{})
	if existing != nil {
		for _, d := range existing.Decisions {
			usedIDs[d.ID] = struct{}{}
		}
	}

	// Track new-decision ids for downstream reference resolution.
	newDecisionIDs := make(map[string]struct{}, len(raw.Decisions))

	for _, d := range raw.Decisions {
		id := strings.TrimSpace(d.ID)
		if id == "" {
			id = mintDecisionID(d.Title, usedIDs)
		} else if _, taken := usedIDs[id]; taken {
			// Collision against existing or prior new decision —
			// suffix via the same minting helper so the result is
			// deterministic and never overwrites an existing entry.
			id = mintDecisionID(d.Title, usedIDs)
		}
		usedIDs[id] = struct{}{}
		newDecisionIDs[id] = struct{}{}
		out.Decisions = append(out.Decisions, DecisionProposal{
			ID:                 id,
			Summary:            d.Summary,
			Title:              d.Title,
			Rationale:          d.Rationale,
			ArchitectRationale: d.ArchitectRationale,
			Confidence:         d.Confidence,
			Alternatives:       d.Alternatives,
			Citations:          d.Citations,
		})
	}

	for _, f := range raw.Features {
		out.Features = append(out.Features, FeatureProposal{
			ID:                 f.ID,
			Summary:            f.Summary,
			Title:              f.Title,
			Description:        f.Description,
			AcceptanceCriteria: f.AcceptanceCriteria,
			Decisions:          append([]string(nil), f.Decisions...),
		})
		for _, ref := range f.Decisions {
			if _, ok := newDecisionIDs[ref]; ok {
				continue
			}
			if existingHasDecision(existing, ref) {
				continue
			}
			applied = append(applied, AppliedAction{
				Kind:        "integrity_violation",
				CanonicalID: ref,
				AffectedNodes: []DecisionSourceRef{
					{ParentKind: "feature", ParentID: f.ID},
				},
			})
		}
	}

	for _, s := range raw.Strategies {
		out.Strategies = append(out.Strategies, StrategyProposal{
			ID:        s.ID,
			Summary:   s.Summary,
			Title:     s.Title,
			Kind:      s.Kind,
			Body:      s.Body,
			Decisions: append([]string(nil), s.Decisions...),
		})
		for _, ref := range s.Decisions {
			if _, ok := newDecisionIDs[ref]; ok {
				continue
			}
			if existingHasDecision(existing, ref) {
				continue
			}
			applied = append(applied, AppliedAction{
				Kind:        "integrity_violation",
				CanonicalID: ref,
				AffectedNodes: []DecisionSourceRef{
					{ParentKind: "strategy", ParentID: s.ID},
				},
			})
		}
	}

	return out, applied, nil
}

// mintDecisionID derives a stable id from a decision title. Collisions
// against `used` get a numeric suffix appended.
//
// Slug derivation goes through spec.SlugID, which caps the slug at
// 50 chars — the cap is load-bearing for filesystem safety even when
// the model goes off-rails into a pathologically-long title.
func mintDecisionID(title string, used map[string]struct{}) string {
	base := "dec-" + spec.SlugID(title)
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

// SortDecisions sorts a SpecProposal's top-level decisions[] alphabetically
// by ID for deterministic output across runs (map iteration is otherwise
// non-deterministic when callers build the proposal from maps). Callers
// that want stability across runs should call this before persisting.
func (p *SpecProposal) SortDecisions() {
	sort.Slice(p.Decisions, func(i, j int) bool {
		return p.Decisions[i].ID < p.Decisions[j].ID
	})
}
