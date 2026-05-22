// DJ-128 alternative-monotonicity discipline for mergeDecisions.
//
// The revise path replaces a prior decision in place. Without
// monotonicity, the prior chosen option and the critic counterproposals
// that lost out vanish from the decision body; next-iteration critics
// see only the current snapshot and can re-litigate the same axis
// with no memory of what was contested.
//
// This file holds the helpers that:
//
//  1. enforce alternatives strictly grow during a revise pass,
//  2. demote the prior chosen option into alternatives when the
//     elaborator omitted it (defensive fold), and
//  3. fold every driving-concern counterproposal into alternatives
//     when the elaborator's Reject revision omitted it (defensive
//     fold).
//
// The elaborator's prompt (Phase 5) drives the elaborator toward
// producing the deliberation log directly; these helpers are a safety
// net for prompt-failure modes.

package agent

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// preservePriorAlternatives folds missing prior alternatives back
// into revised.Alternatives, preserving their original content
// (Name, Rationale, RejectedBecause, Citations) and stamping the
// deliberation-log metadata (RejectedAtIteration,
// RejectedByConcernText) only when not already populated. Called from
// mergeDecisions' replace branches AFTER demote-and-fold and
// REPLACING the previous validate-then-reject discipline.
//
// The motivation is the fifth winplan re-run trace: the elaborator
// (on balanced Gemini) repeatedly emitted revisions that dropped
// prior alternatives (e.g., Neon, access_audit_logs) even though
// the prompt mandated "alternatives strictly grow" and the
// monotonicity validator rejected those revisions wholesale. The
// rejection threw away the elaborator's good work (Flip judgments,
// new counterproposal engagement, rationale updates) along with the
// drop, forcing the next iteration to re-do everything and
// frequently re-drop the same alternatives — oscillating without
// converging. Five revisions of dec-supabase-postgres-persistence in
// that trace failed this way.
//
// The new discipline: take the elaborator's revision as-is for the
// content fields it cares about (chosen / rationale / citations /
// new alternatives), and let the merge layer mechanically preserve
// any prior alternatives the elaborator omitted. Preservation by
// code, not prompt discipline — exactly the kind of structural rule
// LLMs are unreliable at and code is good at. The elaborator's job
// shrinks accordingly: it focuses on engaging with counterproposals
// and producing new content, not on enumerating the full prior
// alternatives list.
//
// Returns the count of prior alternatives this helper folded back
// (zero on a clean revise where the elaborator preserved them all).
func preservePriorAlternatives(prior *RawDecisionProposal, revised *RawDecisionProposal, currentIter int) int {
	if prior == nil || revised == nil {
		return 0
	}
	existingNames := normalizedAlternativeNameSet(revised.Alternatives)
	preserved := 0
	for _, alt := range prior.Alternatives {
		if nameInSet(alt.Name, existingNames) {
			continue
		}
		// The elaborator dropped this alternative — fold it back
		// with its content preserved. Citations slice is cloned to
		// avoid aliasing the prior's underlying array (defensive;
		// the merge function rewrites raw.Decisions in place and a
		// later iteration mutating Citations would otherwise touch
		// the prior's deliberation log).
		fold := spec.Alternative{
			Name:                  alt.Name,
			Rationale:             alt.Rationale,
			RejectedBecause:       alt.RejectedBecause,
			Citations:             append([]spec.Citation(nil), alt.Citations...),
			RejectedAtIteration:   alt.RejectedAtIteration,
			RejectedByConcernText: alt.RejectedByConcernText,
		}
		// Stamp the deliberation-log fields when the prior didn't
		// carry them (first-author alternatives lack the metadata;
		// preservation is the first chance to record provenance).
		if fold.RejectedAtIteration == 0 {
			fold.RejectedAtIteration = currentIter
		}
		revised.Alternatives = append(revised.Alternatives, fold)
		existingNames[normalizeName(alt.Name)] = struct{}{}
		preserved++
	}
	return preserved
}

// recordPreservedAlternativesNotice appends an integrity-critic
// concern naming the elaborator's omission of prior alternatives.
// Not a hard failure — the merge folded them back — but the operator
// sees that the elaborator dropped entries that the merge had to
// preserve mechanically. Used for visibility, not enforcement.
func recordPreservedAlternativesNotice(s *PlanningState, revisedID string, preserved int) {
	if s == nil || preserved == 0 {
		return
	}
	s.Concerns = append(s.Concerns, Concern{
		AgentID:            "integrity_critic",
		Severity:           "low",
		Kind:               "integrity",
		Status:             ConcernStatusOpen,
		Text:               fmt.Sprintf("Revision of %s omitted %d prior alternative(s) from the deliberation log; merge auto-preserved them. The elaborator's emission focused on new content rather than enumerating the full prior alternatives slice, which is the expected discipline under the post-DJ-128 merge-side preservation. Surfaced here for operator visibility; no action required.", revisedID, preserved),
		RelatedDecisionIDs: []string{revisedID},
	})
}

// validateAlternativeMonotonicity reports whether a revise pass
// preserved the deliberation log: every prior alternative still
// appears in revised.Alternatives, AND the prior chosen option
// appears (the demotion-on-flip discipline). Retained as a
// diagnostic helper (tests use it to assert post-merge invariants);
// no longer called from mergeDecisions' enforcement path —
// preservePriorAlternatives replaces it. See that helper's doc for
// the rationale.
//
// Returns nil on a clean revise; a descriptive error naming the
// missing entry on shrinkage. Names are matched case-insensitively
// after trim; the elaborator is permitted to rephrase the Name as
// long as the substantive option is preserved (fuzzy-match by Name
// prefix overlap).
func validateAlternativeMonotonicity(prior, revised RawDecisionProposal) error {
	revisedNames := normalizedAlternativeNameSet(revised.Alternatives)

	// Every prior alternative must survive into the revised set.
	for _, alt := range prior.Alternatives {
		if !nameInSet(alt.Name, revisedNames) {
			return fmt.Errorf("alternative %q dropped from the revision; revise must preserve every prior alternative", alt.Name)
		}
	}
	// The prior chosen option (Title) must also appear in the revised
	// alternatives — unless the revise was a no-op (Title matches the
	// revised Title), which is the "elaborator kept the same chosen
	// option" case.
	priorTitle := strings.TrimSpace(prior.Title)
	revisedTitle := strings.TrimSpace(revised.Title)
	if priorTitle != "" && !strings.EqualFold(priorTitle, revisedTitle) {
		if !nameInSet(priorTitle, revisedNames) {
			return fmt.Errorf("prior chosen option %q was not demoted into alternatives; revise must preserve the prior chosen as an alternative when the title changes", priorTitle)
		}
	}
	return nil
}

// foldCounterproposalsAsAlternatives ensures every driving-concern
// counterproposal lands in revised.Alternatives. For each
// counterproposal:
//
//   - if its Option matches the revised Title (case-insensitive
//     contains either direction), the counterproposal was picked as
//     the new chosen option — no alternative entry is needed for it.
//   - otherwise, the counterproposal must appear in revised.Alternatives.
//     When it does, it carries the critic-provided Argument as Rationale
//     and Citations preserved verbatim (the elaborator should have done
//     this per Phase 5's prompt; we don't second-guess the elaborator's
//     rejection prose if the entry is already there).
//   - when missing, we append a placeholder entry naming the
//     elaborator's omission so the deliberation log is preserved and
//     record an integrity-violation concern so the operator sees the
//     elaborator slipped.
//
// The "needs investigation" sentinel option is skipped — sentinel
// counterproposals don't drive revisions and shouldn't appear in
// alternatives.
//
// Returns the count of counterproposals auto-folded by this helper
// (zero on a clean revise the elaborator produced correctly).
func foldCounterproposalsAsAlternatives(revised *RawDecisionProposal, drivingConcerns []Concern, currentIter int, priorChosenName string) int {
	if revised == nil {
		return 0
	}
	if len(drivingConcerns) == 0 {
		return 0
	}
	folded := 0
	revisedTitle := strings.TrimSpace(revised.Title)
	existingNames := normalizedAlternativeNameSet(revised.Alternatives)

	for _, c := range drivingConcerns {
		for _, cp := range c.Counterproposals {
			option := strings.TrimSpace(cp.Option)
			if option == "" {
				continue
			}
			if strings.EqualFold(option, needsInvestigationSentinel) {
				continue
			}
			// Was this counterproposal picked as the new chosen
			// option? Fuzzy match on Option ~= revisedTitle.
			if titlesMatch(option, revisedTitle) {
				continue
			}
			// Or was the counterproposal the prior chosen option's
			// title (the elaborator may emit the prior title as the
			// alternative entry instead — preserve order, don't
			// double-add).
			if priorChosenName != "" && titlesMatch(option, priorChosenName) {
				continue
			}
			// Is it already in the revised alternatives?
			if nameInSet(option, existingNames) {
				continue
			}
			// The elaborator omitted it — fold it in defensively. The
			// alternative carries the critic's Argument verbatim as
			// Rationale and the Citations slice verbatim, plus the
			// deliberation-log provenance fields.
			rejectedBecause := fmt.Sprintf("Elaborator did not engage with this counterproposal during the iter-%d revise; merge auto-folded it into alternatives to preserve the deliberation log.", currentIter)
			alt := spec.Alternative{
				Name:                  option,
				Rationale:             cp.Argument,
				RejectedBecause:       rejectedBecause,
				Citations:             append([]spec.Citation(nil), cp.Citations...),
				RejectedAtIteration:   currentIter,
				RejectedByConcernText: c.Text,
			}
			// Sentinel-permitting note: a non-sentinel counterproposal
			// without citations would have been rejected by the
			// validator at merge time. If we reach this branch with
			// empty citations, something earlier failed open — we
			// still fold it in (the deliberation log preserves the
			// option) but log a warning.
			if len(alt.Citations) == 0 {
				slog.Warn("foldCounterproposalsAsAlternatives: counterproposal without citations folded into alternatives",
					"option", option,
					"decision_id", revised.ID,
					"concern_text", c.Text)
			}
			revised.Alternatives = append(revised.Alternatives, alt)
			existingNames[normalizeName(option)] = struct{}{}
			folded++
		}
	}
	return folded
}

// demotePriorChosenAsAlternative ensures the prior chosen option
// appears in revised.Alternatives with deliberation-log provenance
// (RejectedAtIteration + RejectedByConcernText). Called from the
// replace branches in mergeDecisions BEFORE
// validateAlternativeMonotonicity so the validator's prior-chosen
// preservation check passes on a normal flip revise. The elaborator's
// prompt drives it toward producing the demotion entry itself; this
// helper is a defensive fold that fills in the gap when the
// elaborator omits it.
//
// Returns true when a demotion was synthesized by this call (false
// when the elaborator's revision already contained the prior chosen,
// or when the prior chosen is the revised chosen — a no-op flip).
func demotePriorChosenAsAlternative(prior, revised *RawDecisionProposal, drivingConcerns []Concern, currentIter int) bool {
	if prior == nil || revised == nil {
		return false
	}
	priorTitle := strings.TrimSpace(prior.Title)
	revisedTitle := strings.TrimSpace(revised.Title)
	if priorTitle == "" {
		return false
	}
	// No-op flip: the elaborator picked the same chosen option, so
	// there's nothing to demote (the prior alternatives are preserved
	// by the monotonicity check).
	if strings.EqualFold(priorTitle, revisedTitle) {
		return false
	}
	existingNames := normalizedAlternativeNameSet(revised.Alternatives)
	if nameInSet(priorTitle, existingNames) {
		return false
	}
	// Synthesize the demotion entry from the prior decision's body.
	// The RejectedBecause is the driving-concern Argument when one of
	// the counterproposals matches the revised Title (the picked
	// option); otherwise, the first driving-concern Weakness is the
	// fallback rationale.
	rejected := synthesizeRejectedBecause(drivingConcerns, revisedTitle)
	concernText := firstConcernText(drivingConcerns)
	revised.Alternatives = append(revised.Alternatives, spec.Alternative{
		Name:                  priorTitle,
		Rationale:             prior.ArchitectRationale,
		RejectedBecause:       rejected,
		Citations:             append([]spec.Citation(nil), prior.Citations...),
		RejectedAtIteration:   currentIter,
		RejectedByConcernText: concernText,
	})
	return true
}

// recordMonotonicityViolation appends an integrity-critic concern
// naming the elaborator's revise error so the next iteration's scout
// pass sees the problem. The concern carries the revised decision id
// in RelatedDecisionIDs so DJ-126's revise dispatch can engage it.
func recordMonotonicityViolation(s *PlanningState, revisedID string, err error) {
	if s == nil || err == nil {
		return
	}
	s.Concerns = append(s.Concerns, Concern{
		AgentID:            "integrity_critic",
		Severity:           "high",
		Kind:               "integrity",
		Status:             ConcernStatusOpen,
		Text:               fmt.Sprintf("Revision of %s violated alternative-monotonicity discipline: %s", revisedID, err.Error()),
		RelatedDecisionIDs: []string{revisedID},
	})
}

// recordCounterproposalFoldNotice appends an integrity-critic concern
// naming the elaborator's omission of counterproposals from the
// alternatives slice. Not a hard failure — the merge folded the
// counterproposals in defensively — but the operator should see that
// the elaborator slipped.
func recordCounterproposalFoldNotice(s *PlanningState, revisedID string, foldedCount int) {
	if s == nil || foldedCount == 0 {
		return
	}
	s.Concerns = append(s.Concerns, Concern{
		AgentID:            "integrity_critic",
		Severity:           "medium",
		Kind:               "integrity",
		Status:             ConcernStatusOpen,
		Text:               fmt.Sprintf("Revision of %s omitted %d counterproposal(s) from the alternatives deliberation log; merge auto-folded them. The elaborator should engage with every counterproposal explicitly.", revisedID, foldedCount),
		RelatedDecisionIDs: []string{revisedID},
	})
}

// normalizedAlternativeNameSet returns the set of alternative names
// in lower-cased trimmed form for case-insensitive lookup. The set
// keys are normalised once; callers use normalizeName() for query.
func normalizedAlternativeNameSet(alts []spec.Alternative) map[string]struct{} {
	out := make(map[string]struct{}, len(alts))
	for _, alt := range alts {
		out[normalizeName(alt.Name)] = struct{}{}
	}
	return out
}

// normalizeName lower-cases and trims whitespace for case-insensitive
// alternative-name matching. Sub-string fuzz is intentionally left
// out — the monotonicity discipline keys on substantive name equality,
// not paraphrasing.
func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// nameInSet checks whether name (after normalisation) is in the set.
func nameInSet(name string, set map[string]struct{}) bool {
	if name == "" {
		return false
	}
	_, ok := set[normalizeName(name)]
	return ok
}

// titlesMatch reports whether two option / title strings refer to the
// same thing under fuzzy comparison. Used to detect when a critic
// counterproposal got picked as the new chosen option (Option ~=
// revised Title). Compares trim+lower equality OR substring-contains
// in either direction (the elaborator may have shortened or expanded
// the title).
func titlesMatch(a, b string) bool {
	na := normalizeName(a)
	nb := normalizeName(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	if strings.Contains(na, nb) || strings.Contains(nb, na) {
		return true
	}
	return false
}

// synthesizeRejectedBecause builds a one-sentence RejectedBecause for
// the demoted prior chosen option. Strategy: walk the driving
// concerns and pick the counterproposal whose Option matches the
// revised Title (the picked option), use its Argument as the
// rejection reason. Fallback to the first driving concern's Weakness
// + Evidence when no counterproposal matched.
func synthesizeRejectedBecause(drivingConcerns []Concern, revisedTitle string) string {
	for _, c := range drivingConcerns {
		for _, cp := range c.Counterproposals {
			if strings.EqualFold(strings.TrimSpace(cp.Option), needsInvestigationSentinel) {
				continue
			}
			if titlesMatch(cp.Option, revisedTitle) {
				if strings.TrimSpace(cp.Argument) != "" {
					return cp.Argument
				}
			}
		}
	}
	for _, c := range drivingConcerns {
		if strings.TrimSpace(c.Text) != "" {
			return c.Text
		}
	}
	return "Prior chosen option demoted to alternatives by an iteration revise pass."
}

// firstConcernText returns the first non-empty concern Text from the
// driving concerns slice. Used as the deliberation-log
// RejectedByConcernText for the prior-chosen demotion.
func firstConcernText(drivingConcerns []Concern) string {
	for _, c := range drivingConcerns {
		if t := strings.TrimSpace(c.Text); t != "" {
			return t
		}
	}
	return ""
}

// drivingConcernsForDecision returns the open Concerns whose
// RelatedDecisionIDs contains the prior id. The slice is captured
// BEFORE markConcernsAddressedByRevision so the deliberation-log
// helpers see the concerns in their as-flagged form. Mirrors the
// queueDecisionRevisedEvent capture pattern.
func drivingConcernsForDecision(s *PlanningState, priorID string) []Concern {
	if s == nil || priorID == "" {
		return nil
	}
	var out []Concern
	for i := range s.Concerns {
		c := s.Concerns[i]
		if effectiveConcernStatus(&c) != ConcernStatusOpen {
			continue
		}
		if !stringSliceContains(c.RelatedDecisionIDs, priorID) {
			continue
		}
		copy := c
		if len(c.RelatedDecisionIDs) > 0 {
			copy.RelatedDecisionIDs = append([]string(nil), c.RelatedDecisionIDs...)
		}
		if len(c.RelatedAxisIDs) > 0 {
			copy.RelatedAxisIDs = append([]string(nil), c.RelatedAxisIDs...)
		}
		if len(c.Counterproposals) > 0 {
			copy.Counterproposals = append([]CriticCounterproposal(nil), c.Counterproposals...)
		}
		out = append(out, copy)
	}
	return out
}
