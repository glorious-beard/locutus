// Package remediate is the autonomy bridge in brownfield assimilation
// (DJ-045 + DJ-046): given the gap_analyst's findings and the spec
// inferred so far, it produces concrete spec writes — assumed Decisions,
// new Strategies, new or updated Features — so `assimilate` can
// produce a complete, internally consistent spec in one pass without
// pausing for human input.
//
// The package is the persistence-side counterpart to the `remediator`
// agent (internal/scaffold/agents/remediator.md). The agent decides
// *what* to write (consolidation, attachment, voicing); this package
// decides *how* the structured output threads back into the
// AssimilationResult so the existing DJ-075 persistence pass writes
// everything atomically.
//
// Cascade behavior: remediation does NOT trigger cascade. The remediator
// writes new Decisions and updates parent Features in coordination —
// the resulting prose is consistent by construction, so a cascade pass
// would be a no-op rewriter call. If empirical drift emerges between
// remediator-authored prose and the rewriter's voice in later runs,
// revisit this in a follow-up DJ.
package remediate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
)

// Plan is the structured output of the remediator agent. Mirrors the
// JSON the agent produces against the RemediationPlan output schema.
type Plan struct {
	Decisions      []spec.Decision  `json:"decisions,omitempty"`
	Strategies     []spec.Strategy  `json:"strategies,omitempty"`
	Features       []spec.Feature   `json:"features,omitempty"`
	FeatureUpdates []FeatureUpdate  `json:"feature_updates,omitempty"`
}

// FeatureUpdate appends Decision references to an existing Feature
// (either one already on disk or one freshly created in the same Plan).
// Used by the remediator to attach feature-specific gaps to the right
// Feature without rewriting the Feature's prose.
type FeatureUpdate struct {
	FeatureID      string   `json:"feature_id"`
	AddedDecisions []string `json:"added_decisions,omitempty"`
}

// Result is the package's outward-facing summary. Plan is the raw
// agent output for callers that want the full surface; the count
// fields are convenience for status output and tests.
type Result struct {
	Plan              *Plan
	DecisionsCreated  int
	StrategiesCreated int
	FeaturesCreated   int
	FeaturesUpdated   int
}

// Remediate invokes the remediator agent on the given gaps with the
// existing inferred spec as context. Empty gaps short-circuit (no LLM
// call). A nil llm with non-empty gaps is an error — silently skipping
// remediation when an LLM provider is unconfigured would produce a
// false-clean result for autonomous brownfield runs.
func Remediate(ctx context.Context, llm agent.AgentExecutor, gaps []agent.Gap, existing *agent.ExistingSpec) (*Result, error) {
	if len(gaps) == 0 {
		return &Result{Plan: &Plan{}}, nil
	}
	if llm == nil {
		return nil, fmt.Errorf("remediate: llm provider is required when gaps are present")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	plan, err := invokeRemediator(ctx, llm, gaps, existing)
	if err != nil {
		return nil, fmt.Errorf("remediate invoke: %w", err)
	}

	return &Result{
		Plan:              plan,
		DecisionsCreated:  len(plan.Decisions),
		StrategiesCreated: len(plan.Strategies),
		FeaturesCreated:   len(plan.Features),
		FeaturesUpdated:   len(plan.FeatureUpdates),
	}, nil
}

// ApplyToAssimilation merges a Plan into the AssimilationResult so the
// downstream persistence pass writes everything atomically. New nodes
// are appended; FeatureUpdates target either a Feature already in
// `result.Features`, a Feature freshly added in `plan.Features`, or a
// Feature still living only in `existing` (in which case it's pulled
// into `result.Features` with the merged Decision refs so persistence
// sees it). Existing-spec Features the remediator did not touch stay
// where they are; the persistence pass re-reads them as-is.
func ApplyToAssimilation(plan *Plan, result *agent.AssimilationResult, existing *agent.ExistingSpec) {
	if plan == nil || result == nil {
		return
	}

	result.Decisions = append(result.Decisions, plan.Decisions...)
	result.Strategies = append(result.Strategies, plan.Strategies...)
	result.Features = append(result.Features, plan.Features...)

	for _, upd := range plan.FeatureUpdates {
		f := findOrPullFeature(upd.FeatureID, result, existing)
		if f == nil {
			// FeatureUpdate references a Feature we cannot locate. Skip
			// silently rather than crash — the agent emitted a stale ref;
			// the gap remains unaddressed but the rest of the plan lands.
			continue
		}
		f.Decisions = mergeUnique(f.Decisions, upd.AddedDecisions)
		writeBackFeature(*f, result)
	}
}

// findOrPullFeature returns a pointer to a Feature already in the
// result, pulled from `existing` and added to the result, or nil if
// the ID is unknown.
func findOrPullFeature(id string, result *agent.AssimilationResult, existing *agent.ExistingSpec) *spec.Feature {
	for i := range result.Features {
		if result.Features[i].ID == id {
			f := result.Features[i]
			return &f
		}
	}
	if existing != nil {
		for _, f := range existing.Features {
			if f.ID == id {
				result.Features = append(result.Features, f)
				return &result.Features[len(result.Features)-1]
			}
		}
	}
	return nil
}

func writeBackFeature(f spec.Feature, result *agent.AssimilationResult) {
	for i := range result.Features {
		if result.Features[i].ID == f.ID {
			result.Features[i] = f
			return
		}
	}
	result.Features = append(result.Features, f)
}

func mergeUnique(existing, added []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(added))
	out := make([]string, 0, len(existing)+len(added))
	for _, id := range existing {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range added {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// invokeRemediator assembles the remediator prompt from gaps + existing
// spec context and parses the structured Plan response.
func invokeRemediator(ctx context.Context, llm agent.AgentExecutor, gaps []agent.Gap, existing *agent.ExistingSpec) (*Plan, error) {
	def := agent.AgentDef{
		ID:           "remediator",
		SystemPrompt: "You are the gap remediator. Respond with valid JSON matching the Plan schema {decisions, strategies, features, feature_updates}.",
	}
	input := agent.AgentInput{Messages: []agent.Message{{Role: "user", Content: buildPrompt(gaps, existing)}}}
	var plan Plan
	if err := agent.RunInto(ctx, llm, def, input, &plan); err != nil {
		return nil, fmt.Errorf("remediator: %w", err)
	}
	return &plan, nil
}

func buildPrompt(gaps []agent.Gap, existing *agent.ExistingSpec) string {
	var b strings.Builder
	b.WriteString("## Gap Analysis\n\n")
	for _, g := range gaps {
		fmt.Fprintf(&b, "### %s (severity: %s)\n", g.Category, g.Severity)
		b.WriteString(g.Description)
		b.WriteString("\n")
		if len(g.AffectedIDs) > 0 {
			fmt.Fprintf(&b, "Affected IDs: %s\n", strings.Join(g.AffectedIDs, ", "))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Existing Inferred Spec\n\n")
	if existing == nil || existing.IsEmpty() {
		b.WriteString("(empty — greenfield assimilation)\n")
		return b.String()
	}

	if len(existing.Features) > 0 {
		b.WriteString("### Features\n")
		writeFeatureList(&b, existing.Features)
	}
	if len(existing.Decisions) > 0 {
		b.WriteString("\n### Decisions\n")
		writeDecisionList(&b, existing.Decisions)
	}
	if len(existing.Strategies) > 0 {
		b.WriteString("\n### Strategies\n")
		writeStrategyList(&b, existing.Strategies)
	}
	return b.String()
}

func writeFeatureList(b *strings.Builder, fs []spec.Feature) {
	sorted := make([]spec.Feature, len(fs))
	copy(sorted, fs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, f := range sorted {
		fmt.Fprintf(b, "- %s: %s (status=%s)\n", f.ID, f.Title, f.Status)
	}
}

func writeDecisionList(b *strings.Builder, ds []spec.Decision) {
	sorted := make([]spec.Decision, len(ds))
	copy(sorted, ds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, d := range sorted {
		fmt.Fprintf(b, "- %s: %s (status=%s)\n", d.ID, d.Title, d.Status)
	}
}

func writeStrategyList(b *strings.Builder, ss []spec.Strategy) {
	sorted := make([]spec.Strategy, len(ss))
	copy(sorted, ss)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, s := range sorted {
		fmt.Fprintf(b, "- %s: %s (status=%s)\n", s.ID, s.Title, s.Status)
	}
}
