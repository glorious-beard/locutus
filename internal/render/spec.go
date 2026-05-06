package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/spec"
)

// RenderFeature returns a Markdown section for one feature, with
// linked-decision titles inlined and back-references (relevant
// strategies, approaches) listed. The Loaded graph is consulted for
// title resolution and back-refs; nil-safe on missing referents
// (renders the bare id).
//
// stage is the optional implementation-stage classifier from
// spec.DeriveStages; pass an empty map to omit the stage tag.
func RenderFeature(n spec.FeatureNode, l *spec.Loaded, stage spec.StageMap) string {
	var b strings.Builder
	f := n.Spec

	stageTag := ""
	if s, ok := stage[f.ID]; ok && s != "" {
		stageTag = fmt.Sprintf(" · %s", s)
	}
	fmt.Fprintf(&b, "### `%s` — %s *(%s%s)*\n\n", f.ID, f.Title, f.Status, stageTag)

	if strings.TrimSpace(n.Body) != "" {
		b.WriteString(strings.TrimSpace(n.Body))
		b.WriteString("\n\n")
	} else if f.Description != "" {
		b.WriteString(f.Description)
		b.WriteString("\n\n")
	}

	if len(f.AcceptanceCriteria) > 0 {
		b.WriteString("**Acceptance criteria:**\n\n")
		for _, ac := range f.AcceptanceCriteria {
			fmt.Fprintf(&b, "- %s\n", ac)
		}
		b.WriteString("\n")
	}

	if len(f.Decisions) > 0 {
		fmt.Fprintf(&b, "**Linked decisions (%d):**\n\n", len(f.Decisions))
		for _, did := range f.Decisions {
			title := did
			if d := l.DecisionNodeByID(did); d != nil {
				title = d.Spec.Title
			}
			fmt.Fprintf(&b, "- `%s` — %s\n", did, title)
		}
		b.WriteString("\n")
	}

	// Relevant strategies: any strategy that lists at least one of
	// this feature's decisions in its decisions[]. Surface as cross-
	// reference; the operator can navigate.
	relevant := relevantStrategies(f.Decisions, l)
	if len(relevant) > 0 {
		fmt.Fprintf(&b, "**Relevant strategies (%d):** %s\n\n", len(relevant), strings.Join(relevant, ", "))
	}

	if len(f.Approaches) > 0 {
		fmt.Fprintf(&b, "**Approaches (%d):**\n\n", len(f.Approaches))
		for _, aid := range f.Approaches {
			title := aid
			if a := l.ApproachNodeByID(aid); a != nil {
				title = a.Spec.Title
			}
			fmt.Fprintf(&b, "- `%s` — %s\n", aid, title)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("**Approaches:** none yet\n\n")
	}

	return b.String()
}

// RenderStrategy returns a Markdown section for one strategy.
func RenderStrategy(n spec.StrategyNode, l *spec.Loaded, stage spec.StageMap) string {
	var b strings.Builder
	s := n.Spec

	stageTag := ""
	if v, ok := stage[s.ID]; ok && v != "" {
		stageTag = fmt.Sprintf(" · %s", v)
	}
	fmt.Fprintf(&b, "#### `%s` — %s *(%s · %s%s)*\n\n", s.ID, s.Title, s.Kind, s.Status, stageTag)

	if strings.TrimSpace(n.Body) != "" {
		b.WriteString(strings.TrimSpace(n.Body))
		b.WriteString("\n\n")
	}

	if len(s.Decisions) > 0 {
		fmt.Fprintf(&b, "**Linked decisions (%d):**\n\n", len(s.Decisions))
		for _, did := range s.Decisions {
			title := did
			if d := l.DecisionNodeByID(did); d != nil {
				title = d.Spec.Title
			}
			fmt.Fprintf(&b, "- `%s` — %s\n", did, title)
		}
		b.WriteString("\n")
	}

	// Reference back-refs: which features pull this strategy in via
	// their decisions.
	if features := featuresReferencingStrategy(s.ID, l); len(features) > 0 {
		fmt.Fprintf(&b, "**Referenced by features:** %s\n\n", strings.Join(features, ", "))
	}

	if len(s.InfluencedBy) > 0 {
		fmt.Fprintf(&b, "**Influenced by:** %s\n\n", strings.Join(s.InfluencedBy, ", "))
	}
	if influences := l.StrategiesInfluencedByStrategy(s.ID); len(influences) > 0 {
		fmt.Fprintf(&b, "**Influences:** %s\n\n", strings.Join(influences, ", "))
	}

	if len(s.Prerequisites) > 0 {
		fmt.Fprintf(&b, "**Prerequisites:** %s\n\n", strings.Join(s.Prerequisites, ", "))
	}
	if len(s.Commands) > 0 {
		b.WriteString("**Commands:**\n\n")
		for k, v := range s.Commands {
			fmt.Fprintf(&b, "- `%s`: %s\n", k, v)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// RenderDecision returns a Markdown section for one decision,
// including all alternatives, citations, and back-references.
func RenderDecision(n spec.DecisionNode, l *spec.Loaded, stage spec.StageMap) string {
	var b strings.Builder
	d := n.Spec

	stageTag := ""
	if v, ok := stage[d.ID]; ok && v != "" {
		stageTag = fmt.Sprintf(" · %s", v)
	}
	fmt.Fprintf(&b, "### `%s` *(%s%s)*\n\n", d.ID, d.Status, stageTag)
	fmt.Fprintf(&b, "**Title:** %s\n\n", d.Title)
	fmt.Fprintf(&b, "**Confidence:** %.2f\n\n", d.Confidence)

	if d.Rationale != "" {
		b.WriteString("**Rationale:**\n\n")
		b.WriteString(d.Rationale)
		b.WriteString("\n\n")
	}

	if d.Provenance != nil && d.Provenance.ArchitectRationale != "" {
		b.WriteString("**Architect rationale:**\n\n")
		b.WriteString(d.Provenance.ArchitectRationale)
		b.WriteString("\n\n")
	}

	if len(d.Alternatives) > 0 {
		fmt.Fprintf(&b, "**Alternatives considered (%d):**\n\n", len(d.Alternatives))
		for _, a := range d.Alternatives {
			fmt.Fprintf(&b, "- **%s** — %s *Rejected because:* %s\n", a.Name, a.Rationale, a.RejectedBecause)
		}
		b.WriteString("\n")
	}

	if d.Provenance != nil && len(d.Provenance.Citations) > 0 {
		fmt.Fprintf(&b, "**Citations (%d):**\n\n", len(d.Provenance.Citations))
		for _, c := range d.Provenance.Citations {
			ref := c.Reference
			if c.Span != "" {
				ref = fmt.Sprintf("%s (%s)", ref, c.Span)
			}
			fmt.Fprintf(&b, "- *%s* — %s", c.Kind, ref)
			if c.Excerpt != "" {
				fmt.Fprintf(&b, ": %q", truncate(c.Excerpt, 200))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Back-references.
	features := l.FeaturesReferencingDecision(d.ID)
	strategies := l.StrategiesReferencingDecision(d.ID)
	influences := l.DecisionsInfluencedByDecision(d.ID)
	if len(features) > 0 || len(strategies) > 0 || len(d.InfluencedBy) > 0 || len(influences) > 0 {
		b.WriteString("**Lineage and references:**\n\n")
		if len(features) > 0 {
			fmt.Fprintf(&b, "- Referenced by features: %s\n", strings.Join(features, ", "))
		}
		if len(strategies) > 0 {
			fmt.Fprintf(&b, "- Referenced by strategies: %s\n", strings.Join(strategies, ", "))
		}
		if len(d.InfluencedBy) > 0 {
			fmt.Fprintf(&b, "- Influenced by: %s\n", strings.Join(d.InfluencedBy, ", "))
		}
		if len(influences) > 0 {
			fmt.Fprintf(&b, "- Influences: %s\n", strings.Join(influences, ", "))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// RenderApproach returns a Markdown section for one approach.
func RenderApproach(n spec.ApproachNode, l *spec.Loaded) string {
	var b strings.Builder
	a := n.Spec

	fmt.Fprintf(&b, "### `%s` — %s\n\n", a.ID, a.Title)
	if a.ParentID != "" {
		fmt.Fprintf(&b, "**Parent:** `%s`\n\n", a.ParentID)
	}

	if strings.TrimSpace(n.Body) != "" {
		b.WriteString(strings.TrimSpace(n.Body))
		b.WriteString("\n\n")
	}

	if len(a.Decisions) > 0 {
		fmt.Fprintf(&b, "**Consulted decisions:** %s\n\n", strings.Join(a.Decisions, ", "))
	}
	if len(a.Skills) > 0 {
		fmt.Fprintf(&b, "**Skills:** %s\n\n", strings.Join(a.Skills, ", "))
	}
	if len(a.Prerequisites) > 0 {
		fmt.Fprintf(&b, "**Prerequisites:** %s\n\n", strings.Join(a.Prerequisites, ", "))
	}
	if len(a.ArtifactPaths) > 0 {
		fmt.Fprintf(&b, "**Artifact paths:** %s\n\n", strings.Join(a.ArtifactPaths, ", "))
	}
	if len(a.Assertions) > 0 {
		fmt.Fprintf(&b, "**Assertions (%d):**\n\n", len(a.Assertions))
		for _, ax := range a.Assertions {
			fmt.Fprintf(&b, "- *%s* — `%s`", ax.Kind, ax.Target)
			if ax.Message != "" {
				fmt.Fprintf(&b, ": %s", ax.Message)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// RenderBug returns a Markdown section for one bug.
func RenderBug(n spec.BugNode, l *spec.Loaded) string {
	var b strings.Builder
	bug := n.Spec

	fmt.Fprintf(&b, "### `%s` — %s *(%s · severity %s)*\n\n", bug.ID, bug.Title, bug.Status, bug.Severity)
	if bug.FeatureID != "" {
		fmt.Fprintf(&b, "**Feature:** `%s`\n\n", bug.FeatureID)
	}
	if bug.Description != "" {
		b.WriteString(bug.Description)
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(n.Body) != "" {
		b.WriteString(strings.TrimSpace(n.Body))
		b.WriteString("\n\n")
	}
	if len(bug.ReproductionSteps) > 0 {
		b.WriteString("**Reproduction steps:**\n\n")
		for _, s := range bug.ReproductionSteps {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n")
	}
	if bug.RootCause != "" {
		fmt.Fprintf(&b, "**Root cause:** %s\n\n", bug.RootCause)
	}
	if bug.FixPlan != "" {
		fmt.Fprintf(&b, "**Fix plan:** %s\n\n", bug.FixPlan)
	}
	return b.String()
}

// relevantStrategies returns the strategy ids that reference any of
// the given decision ids. Sorted unique.
func relevantStrategies(decisionIDs []string, l *spec.Loaded) []string {
	seen := map[string]struct{}{}
	for _, did := range decisionIDs {
		for _, sid := range l.StrategiesReferencingDecision(did) {
			seen[sid] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for sid := range seen {
		out = append(out, sid)
	}
	sort.Strings(out)
	return out
}

// featuresReferencingStrategy: a strategy doesn't directly back-ref;
// the relationship is computed through shared decisions. A feature
// "references" a strategy when at least one of the feature's
// decisions is also one of the strategy's decisions.
func featuresReferencingStrategy(strategyID string, l *spec.Loaded) []string {
	s := l.StrategyNodeByID(strategyID)
	if s == nil {
		return nil
	}
	stratDecisions := map[string]struct{}{}
	for _, did := range s.Spec.Decisions {
		stratDecisions[did] = struct{}{}
	}
	hits := map[string]struct{}{}
	for _, f := range l.Features {
		for _, did := range f.Spec.Decisions {
			if _, ok := stratDecisions[did]; ok {
				hits[f.Spec.ID] = struct{}{}
				break
			}
		}
	}
	out := make([]string, 0, len(hits))
	for fid := range hits {
		out = append(out, fid)
	}
	sort.Strings(out)
	return out
}

// truncate cuts s to maxRunes appending "…" if cut. Operates on
// runes.
func truncate(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
