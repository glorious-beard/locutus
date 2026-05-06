package render

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/spec"
)

// SnapshotData is the JSON-shape of a comprehensive spec snapshot.
// Stable across runs — tooling diffs against this directly. The
// Markdown renderer consumes the same struct so the two formats
// always agree on what's in a snapshot.
type SnapshotData struct {
	ProjectName          string                  `json:"project_name,omitempty"`
	GeneratedAt          time.Time               `json:"generated_at"`
	StatusCounts         StatusCountsBlock       `json:"status_counts"`
	ImplementationStages StageDistributionBlock  `json:"implementation_stages"`
	Goals                string                  `json:"goals,omitempty"`
	Strategies           []SnapshotStrategy      `json:"strategies"`
	Features             []SnapshotFeature       `json:"features"`
	Decisions            []SnapshotDecision      `json:"decisions"`
	Approaches           []SnapshotApproach      `json:"approaches,omitempty"`
	Bugs                 []SnapshotBug           `json:"bugs,omitempty"`
	Validation           SnapshotValidationBlock `json:"validation"`
}

// StatusCountsBlock summarizes node counts by status across kinds.
type StatusCountsBlock struct {
	Features   StatusCount `json:"features"`
	Strategies StatusCount `json:"strategies"`
	Decisions  StatusCount `json:"decisions"`
	Approaches CountOnly   `json:"approaches"`
	Bugs       CountOnly   `json:"bugs"`
}

// StatusCount tallies a node kind by status value. Per-kind status
// vocabularies differ (decisions have "assumed"; features have
// "removed"); the catch-all `other` bucket holds anything outside
// the canonical set.
type StatusCount struct {
	Total    int `json:"total"`
	Proposed int `json:"proposed,omitempty"`
	Active   int `json:"active,omitempty"`
	Inferred int `json:"inferred,omitempty"`
	Assumed  int `json:"assumed,omitempty"`
	Removed  int `json:"removed,omitempty"`
	Other    int `json:"other,omitempty"`
}

// CountOnly is for kinds without a meaningful status enum (today:
// Approach has no status field).
type CountOnly struct {
	Total int `json:"total"`
}

// StageDistributionBlock mirrors spec.StageDistribution as JSON.
type StageDistributionBlock struct {
	Drafted      int `json:"drafted"`
	Planned      int `json:"planned"`
	Implementing int `json:"implementing"`
	Done         int `json:"done"`
	Drifted      int `json:"drifted"`
}

// SnapshotStrategy is one strategy's fields plus computed back-refs
// and stage. Body is the markdown prose (from .md sidecar).
type SnapshotStrategy struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Kind                 string   `json:"kind"`
	Status               string   `json:"status"`
	ImplementationStage  string   `json:"implementation_stage"`
	Body                 string   `json:"body,omitempty"`
	Decisions            []string `json:"decisions,omitempty"`
	Approaches           []string `json:"approaches,omitempty"`
	InfluencedBy         []string `json:"influenced_by,omitempty"`
	Influences           []string `json:"influences,omitempty"`
	ReferencedByFeatures []string `json:"referenced_by_features,omitempty"`
	Prerequisites        []string `json:"prerequisites,omitempty"`
}

// SnapshotFeature is one feature's fields plus back-refs and stage.
type SnapshotFeature struct {
	ID                  string   `json:"id"`
	Title               string   `json:"title"`
	Status              string   `json:"status"`
	ImplementationStage string   `json:"implementation_stage"`
	Description         string   `json:"description,omitempty"`
	Body                string   `json:"body,omitempty"`
	AcceptanceCriteria  []string `json:"acceptance_criteria,omitempty"`
	Decisions           []string `json:"decisions,omitempty"`
	Approaches          []string `json:"approaches,omitempty"`
	RelevantStrategies  []string `json:"relevant_strategies,omitempty"`
}

// SnapshotDecision is one decision's content plus back-refs.
type SnapshotDecision struct {
	ID                  string                `json:"id"`
	Title               string                `json:"title"`
	Status              string                `json:"status"`
	Confidence          float64               `json:"confidence"`
	ImplementationStage string                `json:"implementation_stage"`
	Rationale           string                `json:"rationale,omitempty"`
	ArchitectRationale  string                `json:"architect_rationale,omitempty"`
	Alternatives        []spec.Alternative    `json:"alternatives,omitempty"`
	Citations           []spec.Citation       `json:"citations,omitempty"`
	InfluencedBy        []string              `json:"influenced_by,omitempty"`
	Influences          []string              `json:"influences,omitempty"`
	ReferencedBy        SnapshotDecisionRefs  `json:"referenced_by"`
}

// SnapshotDecisionRefs holds the inverse-index lookups for a decision.
type SnapshotDecisionRefs struct {
	Features   []string `json:"features,omitempty"`
	Strategies []string `json:"strategies,omitempty"`
}

// SnapshotApproach is one approach with its body and parent ref.
type SnapshotApproach struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	ParentID      string   `json:"parent_id,omitempty"`
	Body          string   `json:"body,omitempty"`
	Decisions     []string `json:"decisions,omitempty"`
	Skills        []string `json:"skills,omitempty"`
	Prerequisites []string `json:"prerequisites,omitempty"`
}

// SnapshotBug is one bug.
type SnapshotBug struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	FeatureID   string `json:"feature_id,omitempty"`
	Severity    string `json:"severity"`
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
}

// SnapshotValidationBlock surfaces the dangling-ref / orphan
// findings produced at load time.
type SnapshotValidationBlock struct {
	DanglingRefs []SnapshotDanglingRef `json:"dangling_refs,omitempty"`
	Orphans      []SnapshotOrphan      `json:"orphans,omitempty"`
}

// SnapshotDanglingRef is the JSON-friendly form of a DanglingRef.
type SnapshotDanglingRef struct {
	FromKind   string `json:"from_kind"`
	FromID     string `json:"from_id"`
	Field      string `json:"field"`
	TargetID   string `json:"target_id"`
	TargetKind string `json:"target_kind,omitempty"`
}

// SnapshotOrphan is the JSON-friendly form of an Orphan.
type SnapshotOrphan struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// BuildSnapshotData assembles a SnapshotData from a Loaded spec, the
// derived stages, and an optional project name. Filtering
// (--kind, --status) is applied here before render so both formats
// agree on the visible set.
func BuildSnapshotData(l *spec.Loaded, stages spec.StageMap, projectName string, filters SnapshotFilters) SnapshotData {
	data := SnapshotData{
		ProjectName: projectName,
		GeneratedAt: time.Now().UTC(),
		Goals:       l.GoalsBody,
	}

	keepKind := func(k string) bool {
		if len(filters.Kinds) == 0 {
			return true
		}
		for _, want := range filters.Kinds {
			if want == k {
				return true
			}
		}
		return false
	}
	keepStatus := func(status string) bool {
		if len(filters.Statuses) == 0 {
			return true
		}
		for _, want := range filters.Statuses {
			if want == status {
				return true
			}
		}
		return false
	}

	if keepKind("strategy") {
		for _, s := range l.Strategies {
			if !keepStatus(s.Spec.Status) {
				continue
			}
			data.Strategies = append(data.Strategies, snapshotStrategyFrom(s, l, stages))
		}
		sortStrategies(data.Strategies)
	}
	if keepKind("feature") {
		for _, f := range l.Features {
			if !keepStatus(string(f.Spec.Status)) {
				continue
			}
			data.Features = append(data.Features, snapshotFeatureFrom(f, l, stages))
		}
	}
	if keepKind("decision") {
		for _, d := range l.Decisions {
			if !keepStatus(string(d.Spec.Status)) {
				continue
			}
			data.Decisions = append(data.Decisions, snapshotDecisionFrom(d, l, stages))
		}
	}
	if keepKind("approach") {
		for _, a := range l.Approaches {
			data.Approaches = append(data.Approaches, snapshotApproachFrom(a))
		}
	}
	if keepKind("bug") {
		for _, b := range l.Bugs {
			if !keepStatus(string(b.Spec.Status)) {
				continue
			}
			data.Bugs = append(data.Bugs, snapshotBugFrom(b))
		}
	}

	data.StatusCounts = countStatuses(l)
	dist := spec.CountStages(stages)
	data.ImplementationStages = StageDistributionBlock{
		Drafted: dist.Drafted, Planned: dist.Planned,
		Implementing: dist.Implementing, Done: dist.Done, Drifted: dist.Drifted,
	}

	for _, dr := range l.DanglingRefs {
		data.Validation.DanglingRefs = append(data.Validation.DanglingRefs, SnapshotDanglingRef{
			FromKind: string(dr.FromKind), FromID: dr.FromID,
			Field: dr.Field, TargetID: dr.TargetID,
			TargetKind: string(dr.TargetKind),
		})
	}
	for _, o := range l.Orphans {
		data.Validation.Orphans = append(data.Validation.Orphans, SnapshotOrphan{
			Kind: string(o.Kind), ID: o.ID,
		})
	}

	return data
}

// SnapshotFilters constrains the snapshot's visible set. Empty
// slices mean "include all."
type SnapshotFilters struct {
	Kinds    []string // "feature" | "strategy" | "decision" | "approach" | "bug"
	Statuses []string // "proposed" | "active" | "inferred" | "assumed" | "removed"
}

func snapshotStrategyFrom(n spec.StrategyNode, l *spec.Loaded, stages spec.StageMap) SnapshotStrategy {
	s := n.Spec
	return SnapshotStrategy{
		ID:                   s.ID,
		Title:                s.Title,
		Kind:                 string(s.Kind),
		Status:               s.Status,
		ImplementationStage:  string(stages[s.ID]),
		Body:                 strings.TrimSpace(n.Body),
		Decisions:            s.Decisions,
		Approaches:           s.Approaches,
		InfluencedBy:         s.InfluencedBy,
		Influences:           l.StrategiesInfluencedByStrategy(s.ID),
		ReferencedByFeatures: featuresReferencingStrategy(s.ID, l),
		Prerequisites:        s.Prerequisites,
	}
}

func snapshotFeatureFrom(n spec.FeatureNode, l *spec.Loaded, stages spec.StageMap) SnapshotFeature {
	f := n.Spec
	return SnapshotFeature{
		ID:                  f.ID,
		Title:               f.Title,
		Status:              string(f.Status),
		ImplementationStage: string(stages[f.ID]),
		Description:         f.Description,
		Body:                strings.TrimSpace(n.Body),
		AcceptanceCriteria:  f.AcceptanceCriteria,
		Decisions:           f.Decisions,
		Approaches:          f.Approaches,
		RelevantStrategies:  relevantStrategies(f.Decisions, l),
	}
}

func snapshotDecisionFrom(n spec.DecisionNode, l *spec.Loaded, stages spec.StageMap) SnapshotDecision {
	d := n.Spec
	out := SnapshotDecision{
		ID:                  d.ID,
		Title:               d.Title,
		Status:              string(d.Status),
		Confidence:          d.Confidence,
		ImplementationStage: string(stages[d.ID]),
		Rationale:           d.Rationale,
		Alternatives:        d.Alternatives,
		InfluencedBy:        d.InfluencedBy,
		Influences:          l.DecisionsInfluencedByDecision(d.ID),
		ReferencedBy: SnapshotDecisionRefs{
			Features:   l.FeaturesReferencingDecision(d.ID),
			Strategies: l.StrategiesReferencingDecision(d.ID),
		},
	}
	if d.Provenance != nil {
		out.ArchitectRationale = d.Provenance.ArchitectRationale
		out.Citations = d.Provenance.Citations
	}
	return out
}

func snapshotApproachFrom(n spec.ApproachNode) SnapshotApproach {
	a := n.Spec
	return SnapshotApproach{
		ID:            a.ID,
		Title:         a.Title,
		ParentID:      a.ParentID,
		Body:          strings.TrimSpace(n.Body),
		Decisions:     a.Decisions,
		Skills:        a.Skills,
		Prerequisites: a.Prerequisites,
	}
}

func snapshotBugFrom(n spec.BugNode) SnapshotBug {
	b := n.Spec
	return SnapshotBug{
		ID:          b.ID,
		Title:       b.Title,
		FeatureID:   b.FeatureID,
		Severity:    string(b.Severity),
		Status:      string(b.Status),
		Description: b.Description,
	}
}

// sortStrategies orders foundational → quality → derived for the
// rendered Markdown narrative; JSON consumers can re-sort as needed.
func sortStrategies(s []SnapshotStrategy) {
	rank := map[string]int{
		"foundational": 0,
		"quality":      1,
		"derived":      2,
	}
	sort.SliceStable(s, func(i, j int) bool {
		ri, oi := rank[s[i].Kind]
		rj, oj := rank[s[j].Kind]
		if !oi {
			ri = 99
		}
		if !oj {
			rj = 99
		}
		if ri != rj {
			return ri < rj
		}
		return s[i].ID < s[j].ID
	})
}

func countStatuses(l *spec.Loaded) StatusCountsBlock {
	out := StatusCountsBlock{
		Features:   StatusCount{Total: len(l.Features)},
		Strategies: StatusCount{Total: len(l.Strategies)},
		Decisions:  StatusCount{Total: len(l.Decisions)},
		Approaches: CountOnly{Total: len(l.Approaches)},
		Bugs:       CountOnly{Total: len(l.Bugs)},
	}
	for _, f := range l.Features {
		bucketStatus(&out.Features, string(f.Spec.Status))
	}
	for _, s := range l.Strategies {
		bucketStatus(&out.Strategies, s.Spec.Status)
	}
	for _, d := range l.Decisions {
		bucketStatus(&out.Decisions, string(d.Spec.Status))
	}
	return out
}

func bucketStatus(c *StatusCount, status string) {
	switch status {
	case "proposed":
		c.Proposed++
	case "active":
		c.Active++
	case "inferred":
		c.Inferred++
	case "assumed":
		c.Assumed++
	case "removed":
		c.Removed++
	default:
		c.Other++
	}
}

// SnapshotMarkdown renders SnapshotData as a single coherent
// Markdown document. Order: header → status → goals → strategies
// (grouped by kind) → features → decisions → approaches → bugs →
// validation.
func SnapshotMarkdown(d SnapshotData) string {
	var b strings.Builder

	header := "Specification snapshot"
	if d.ProjectName != "" {
		header = d.ProjectName + " — Specification snapshot"
	}
	fmt.Fprintf(&b, "# %s\n\n", header)
	fmt.Fprintf(&b, "*Generated %s*\n\n", d.GeneratedAt.Format(time.RFC3339))

	// Status section.
	b.WriteString("## Status\n\n")
	b.WriteString(snapshotCountsTable(d.StatusCounts))
	b.WriteString("\n")
	fmt.Fprintf(&b, "**Implementation stages:** drafted: %d · planned: %d · implementing: %d · done: %d · drifted: %d\n\n",
		d.ImplementationStages.Drafted,
		d.ImplementationStages.Planned,
		d.ImplementationStages.Implementing,
		d.ImplementationStages.Done,
		d.ImplementationStages.Drifted,
	)

	if strings.TrimSpace(d.Goals) != "" {
		b.WriteString("## Goals\n\n")
		for _, line := range strings.Split(strings.TrimSpace(d.Goals), "\n") {
			fmt.Fprintf(&b, "> %s\n", line)
		}
		b.WriteString("\n")
	}

	if len(d.Strategies) > 0 {
		b.WriteString("## Strategies\n\n")
		// Already sorted foundational → quality → derived.
		var lastKind string
		for _, s := range d.Strategies {
			if s.Kind != lastKind {
				fmt.Fprintf(&b, "### %s\n\n", strings.Title(s.Kind))
				lastKind = s.Kind
			}
			b.WriteString(renderSnapshotStrategy(s))
			b.WriteString("---\n\n")
		}
	}

	if len(d.Features) > 0 {
		b.WriteString("## Features\n\n")
		for _, f := range d.Features {
			b.WriteString(renderSnapshotFeature(f))
			b.WriteString("---\n\n")
		}
	}

	if len(d.Decisions) > 0 {
		b.WriteString("## Decisions\n\n")
		// Alphabetize for stable index ordering.
		decs := make([]SnapshotDecision, len(d.Decisions))
		copy(decs, d.Decisions)
		sort.Slice(decs, func(i, j int) bool { return decs[i].ID < decs[j].ID })
		for _, dec := range decs {
			b.WriteString(renderSnapshotDecision(dec))
			b.WriteString("---\n\n")
		}
	}

	if len(d.Approaches) > 0 {
		b.WriteString("## Approaches\n\n")
		for _, a := range d.Approaches {
			b.WriteString(renderSnapshotApproach(a))
			b.WriteString("---\n\n")
		}
	}

	if len(d.Bugs) > 0 {
		b.WriteString("## Bugs\n\n")
		for _, bg := range d.Bugs {
			b.WriteString(renderSnapshotBug(bg))
			b.WriteString("---\n\n")
		}
	}

	if len(d.Validation.DanglingRefs) > 0 || len(d.Validation.Orphans) > 0 {
		b.WriteString("## Validation\n\n")
		if len(d.Validation.DanglingRefs) > 0 {
			fmt.Fprintf(&b, "**Dangling references (%d):**\n\n", len(d.Validation.DanglingRefs))
			for _, dr := range d.Validation.DanglingRefs {
				fmt.Fprintf(&b, "- `%s/%s` field `%s` → `%s` (not found)\n",
					dr.FromKind, dr.FromID, dr.Field, dr.TargetID)
			}
			b.WriteString("\n")
		}
		if len(d.Validation.Orphans) > 0 {
			fmt.Fprintf(&b, "**Orphans (%d):** nodes with no incoming references — informational only.\n\n", len(d.Validation.Orphans))
			for _, o := range d.Validation.Orphans {
				fmt.Fprintf(&b, "- `%s/%s`\n", o.Kind, o.ID)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

func snapshotCountsTable(c StatusCountsBlock) string {
	var b strings.Builder
	b.WriteString("| Category   | Total | Proposed | Active | Inferred | Assumed | Removed |\n")
	b.WriteString("|------------|-------|----------|--------|----------|---------|---------|\n")
	fmt.Fprintf(&b, "| Features   | %d    | %d        | %d      | %d        | —       | %d      |\n",
		c.Features.Total, c.Features.Proposed, c.Features.Active, c.Features.Inferred, c.Features.Removed)
	fmt.Fprintf(&b, "| Strategies | %d    | %d        | %d      | %d        | —       | %d      |\n",
		c.Strategies.Total, c.Strategies.Proposed, c.Strategies.Active, c.Strategies.Inferred, c.Strategies.Removed)
	fmt.Fprintf(&b, "| Decisions  | %d    | %d        | %d      | %d        | %d       | —       |\n",
		c.Decisions.Total, c.Decisions.Proposed, c.Decisions.Active, c.Decisions.Inferred, c.Decisions.Assumed)
	fmt.Fprintf(&b, "| Approaches | %d    | —         | —       | —         | —       | —       |\n", c.Approaches.Total)
	fmt.Fprintf(&b, "| Bugs       | %d    | —         | —       | —         | —       | —       |\n", c.Bugs.Total)
	return b.String()
}

// renderSnapshotStrategy etc. are SnapshotData-shaped renderers that
// don't need the original Loaded graph (back-refs are precomputed).
// Used by SnapshotMarkdown.
func renderSnapshotStrategy(s SnapshotStrategy) string {
	var b strings.Builder
	stage := ""
	if s.ImplementationStage != "" {
		stage = " · " + s.ImplementationStage
	}
	fmt.Fprintf(&b, "#### `%s` — %s *(%s · %s%s)*\n\n", s.ID, s.Title, s.Kind, s.Status, stage)
	if s.Body != "" {
		b.WriteString(s.Body)
		b.WriteString("\n\n")
	}
	if len(s.Decisions) > 0 {
		fmt.Fprintf(&b, "**Linked decisions (%d):** %s\n\n", len(s.Decisions), strings.Join(s.Decisions, ", "))
	}
	if len(s.ReferencedByFeatures) > 0 {
		fmt.Fprintf(&b, "**Referenced by features:** %s\n\n", strings.Join(s.ReferencedByFeatures, ", "))
	}
	if len(s.InfluencedBy) > 0 {
		fmt.Fprintf(&b, "**Influenced by:** %s\n\n", strings.Join(s.InfluencedBy, ", "))
	}
	return b.String()
}

func renderSnapshotFeature(f SnapshotFeature) string {
	var b strings.Builder
	stage := ""
	if f.ImplementationStage != "" {
		stage = " · " + f.ImplementationStage
	}
	fmt.Fprintf(&b, "### `%s` — %s *(%s%s)*\n\n", f.ID, f.Title, f.Status, stage)
	if f.Body != "" {
		b.WriteString(f.Body)
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
		fmt.Fprintf(&b, "**Linked decisions (%d):** %s\n\n", len(f.Decisions), strings.Join(f.Decisions, ", "))
	}
	if len(f.RelevantStrategies) > 0 {
		fmt.Fprintf(&b, "**Relevant strategies:** %s\n\n", strings.Join(f.RelevantStrategies, ", "))
	}
	if len(f.Approaches) > 0 {
		fmt.Fprintf(&b, "**Approaches (%d):** %s\n\n", len(f.Approaches), strings.Join(f.Approaches, ", "))
	} else {
		b.WriteString("**Approaches:** none yet\n\n")
	}
	return b.String()
}

func renderSnapshotDecision(d SnapshotDecision) string {
	var b strings.Builder
	stage := ""
	if d.ImplementationStage != "" {
		stage = " · " + d.ImplementationStage
	}
	fmt.Fprintf(&b, "### `%s` *(%s%s)*\n\n", d.ID, d.Status, stage)
	fmt.Fprintf(&b, "**Title:** %s\n\n", d.Title)
	fmt.Fprintf(&b, "**Confidence:** %.2f\n\n", d.Confidence)
	if d.Rationale != "" {
		fmt.Fprintf(&b, "**Rationale:** %s\n\n", d.Rationale)
	}
	if d.ArchitectRationale != "" {
		fmt.Fprintf(&b, "**Architect rationale:** %s\n\n", d.ArchitectRationale)
	}
	if len(d.Alternatives) > 0 {
		b.WriteString("**Alternatives:**\n\n")
		for _, a := range d.Alternatives {
			fmt.Fprintf(&b, "- **%s** — %s *Rejected because:* %s\n", a.Name, a.Rationale, a.RejectedBecause)
		}
		b.WriteString("\n")
	}
	if len(d.Citations) > 0 {
		b.WriteString("**Citations:**\n\n")
		for _, c := range d.Citations {
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
	if len(d.ReferencedBy.Features) > 0 {
		fmt.Fprintf(&b, "**Referenced by features:** %s\n\n", strings.Join(d.ReferencedBy.Features, ", "))
	}
	if len(d.ReferencedBy.Strategies) > 0 {
		fmt.Fprintf(&b, "**Referenced by strategies:** %s\n\n", strings.Join(d.ReferencedBy.Strategies, ", "))
	}
	if len(d.InfluencedBy) > 0 {
		fmt.Fprintf(&b, "**Influenced by:** %s\n\n", strings.Join(d.InfluencedBy, ", "))
	}
	if len(d.Influences) > 0 {
		fmt.Fprintf(&b, "**Influences:** %s\n\n", strings.Join(d.Influences, ", "))
	}
	return b.String()
}

func renderSnapshotApproach(a SnapshotApproach) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### `%s` — %s\n\n", a.ID, a.Title)
	if a.ParentID != "" {
		fmt.Fprintf(&b, "**Parent:** `%s`\n\n", a.ParentID)
	}
	if a.Body != "" {
		b.WriteString(a.Body)
		b.WriteString("\n\n")
	}
	if len(a.Decisions) > 0 {
		fmt.Fprintf(&b, "**Consulted decisions:** %s\n\n", strings.Join(a.Decisions, ", "))
	}
	return b.String()
}

func renderSnapshotBug(g SnapshotBug) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### `%s` — %s *(%s · severity %s)*\n\n", g.ID, g.Title, g.Status, g.Severity)
	if g.FeatureID != "" {
		fmt.Fprintf(&b, "**Feature:** `%s`\n\n", g.FeatureID)
	}
	if g.Description != "" {
		b.WriteString(g.Description)
		b.WriteString("\n\n")
	}
	return b.String()
}
