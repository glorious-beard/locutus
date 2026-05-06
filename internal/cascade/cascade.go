// Package cascade propagates Decision revisions into the spec graph per
// DJ-069. When a Decision is revised, every Feature and Strategy that
// references it has its present-tense prose rewritten by a fast-tier LLM
// to reflect the new constraint, and every Approach that hangs off those
// parents is marked `drifted` in the state store so the next `adopt` run
// replans and re-synthesizes it.
//
// The cascade is intentionally narrow: it does not re-synthesize Approach
// bodies (that's planner work), it does not run council deliberation
// (that's `refine`), and it does not make new Decisions (that's
// pre-flight). It transforms one spec node's ripple into a consistent set
// of mechanical updates, and records history events along the way.
package cascade

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
)

// Result summarises a cascade pass. Populated even on partial failure so
// callers can surface the work that did land.
type Result struct {
	UpdatedFeatures   []string        // IDs of Features whose Description was rewritten
	UpdatedStrategies []string        // IDs of Strategies whose Description was rewritten
	DriftedApproaches []string        // IDs of Approaches re-queued for reconciliation
	Skipped           []string        // IDs of parents the rewriter judged already accurate
	Events            []history.Event // events recorded for historian (for tests and dry runs)
}

// RewriteResult is the JSON shape the rewriter agent returns.
type RewriteResult struct {
	RevisedBody string `json:"revised_body"`
	Changed     bool   `json:"changed"`
	Rationale   string `json:"rationale"`
}

// Cascade propagates the revision of a single Decision through the spec
// graph. The Decision must already be saved to disk — Cascade reads it
// via the graph and the supplied FS.
//
// Algorithm:
//  1. Load the Decision and its current applicable-Decisions siblings from
//     each parent Feature or Strategy that references the Decision.
//  2. For every parent, ask the rewriter LLM whether the current prose
//     accurately reflects the current Decisions. If yes, skip the parent.
//     If no, save the revised prose.
//  3. For every Approach under a rewritten parent, zero the state-store
//     SpecHash so the next classification routes it through `drifted` →
//     `planned`. WorkstreamID and AssertionResults are cleared per DJ-072
//     — they refer to a plan that was built against the old parent.
//  4. Record one historian event per rewritten parent.
//
// Returns `Result` plus an error if the LLM call or persistence failed.
// Partial progress is preserved in `Result` even when the error is non-nil.
func Cascade(
	ctx context.Context,
	llm agent.AgentExecutor,
	fsys specio.FS,
	graph *spec.SpecGraph,
	store *state.FileStateStore,
	decisionID string,
) (*Result, error) {
	dec := graph.Decision(decisionID)
	if dec == nil {
		return nil, fmt.Errorf("cascade: decision %q not in graph", decisionID)
	}

	result := &Result{}
	hist := history.NewHistorian(fsys, ".borg/history")

	features, strategies := findParents(graph, decisionID)

	for _, f := range features {
		applicable := applicableDecisions(graph, f.Decisions)
		changed, rationale, err := RewriteFeature(ctx, llm, fsys, f, applicable, []spec.Decision{*dec})
		if err != nil {
			return result, fmt.Errorf("cascade rewrite feature %s: %w", f.ID, err)
		}
		if !changed {
			result.Skipped = append(result.Skipped, f.ID)
			continue
		}
		result.UpdatedFeatures = append(result.UpdatedFeatures, f.ID)
		if err := MarkApproachesDrifted(store, f.Approaches, &result.DriftedApproaches); err != nil {
			return result, err
		}
		evt := cascadeEvent(decisionID, f.ID, "feature_rewritten", rationale)
		if err := hist.Record(evt); err != nil {
			return result, fmt.Errorf("cascade record feature event %s: %w", f.ID, err)
		}
		result.Events = append(result.Events, evt)
	}

	for _, s := range strategies {
		applicable := applicableDecisions(graph, s.Decisions)
		changed, rationale, err := RewriteStrategy(ctx, llm, fsys, s, applicable, []spec.Decision{*dec})
		if err != nil {
			return result, fmt.Errorf("cascade rewrite strategy %s: %w", s.ID, err)
		}
		if !changed {
			result.Skipped = append(result.Skipped, s.ID)
			continue
		}
		result.UpdatedStrategies = append(result.UpdatedStrategies, s.ID)
		if err := MarkApproachesDrifted(store, s.Approaches, &result.DriftedApproaches); err != nil {
			return result, err
		}
		evt := cascadeEvent(decisionID, s.ID, "strategy_rewritten", rationale)
		if err := hist.Record(evt); err != nil {
			return result, fmt.Errorf("cascade record strategy event %s: %w", s.ID, err)
		}
		result.Events = append(result.Events, evt)
	}

	return result, nil
}

// findParents returns the Features and Strategies that list decisionID in
// their `Decisions` slice. Iterates the graph's node maps rather than
// maintaining a reverse index — cascade is a rare operation; the cost is
// bounded by graph size, not by cascade frequency.
func findParents(g *spec.SpecGraph, decisionID string) ([]spec.Feature, []spec.Strategy) {
	var features []spec.Feature
	var strategies []spec.Strategy
	for id, node := range g.Nodes() {
		switch node.Kind {
		case spec.KindFeature:
			if f := g.Feature(id); f != nil && contains(f.Decisions, decisionID) {
				features = append(features, *f)
			}
		case spec.KindStrategy:
			if s := g.Strategy(id); s != nil && contains(s.Decisions, decisionID) {
				strategies = append(strategies, *s)
			}
		}
	}
	return features, strategies
}

func contains(list []string, id string) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

// applicableDecisions loads the current content of every Decision id. Missing
// IDs are silently skipped — they'd be caught by validation elsewhere and
// shouldn't block a cascade against live siblings.
func applicableDecisions(g *spec.SpecGraph, ids []string) []spec.Decision {
	out := make([]spec.Decision, 0, len(ids))
	for _, id := range ids {
		if d := g.Decision(id); d != nil {
			out = append(out, *d)
		}
	}
	return out
}

// RewriteFeature calls the rewriter agent for a Feature and, when it
// reports a change, saves the updated body to the spec store. Exported so
// `refine` can reuse the same mechanism for non-Decision-driven rewrites.
func RewriteFeature(
	ctx context.Context,
	llm agent.AgentExecutor,
	fsys specio.FS,
	f spec.Feature,
	applicable, changed []spec.Decision,
) (bool, string, error) {
	result, err := invokeRewriter(ctx, llm, "feature", f.ID, f.Title, f.Description, applicable, changed)
	if err != nil {
		return false, "", err
	}
	if !result.Changed || strings.TrimSpace(result.RevisedBody) == strings.TrimSpace(f.Description) {
		return false, result.Rationale, nil
	}
	f.Description = result.RevisedBody
	f.UpdatedAt = time.Now()
	if err := specio.SavePair(fsys, ".borg/spec/features/"+f.ID, f, result.RevisedBody); err != nil {
		return false, "", fmt.Errorf("save feature: %w", err)
	}
	return true, result.Rationale, nil
}

// RewriteStrategy does the same for a Strategy. Strategies don't carry a
// Description field; their prose is stored in the markdown body of the
// .md companion. We round-trip through SavePair which writes both the JSON
// and the body.
func RewriteStrategy(
	ctx context.Context,
	llm agent.AgentExecutor,
	fsys specio.FS,
	s spec.Strategy,
	applicable, changed []spec.Decision,
) (bool, string, error) {
	currentBody, _ := fsys.ReadFile(".borg/spec/strategies/" + s.ID + ".md")
	result, err := invokeRewriter(ctx, llm, "strategy", s.ID, s.Title, string(currentBody), applicable, changed)
	if err != nil {
		return false, "", err
	}
	if !result.Changed || strings.TrimSpace(result.RevisedBody) == strings.TrimSpace(string(currentBody)) {
		return false, result.Rationale, nil
	}
	if err := specio.SavePair(fsys, ".borg/spec/strategies/"+s.ID, s, result.RevisedBody); err != nil {
		return false, "", fmt.Errorf("save strategy: %w", err)
	}
	return true, result.Rationale, nil
}

// RewriteBug rewrites a Bug's Description using the same rewriter agent as
// Feature/Strategy. Bugs have no Decisions field of their own — callers
// pass the parent Feature's Decisions as `applicable`. RootCause and
// FixPlan are incident-diagnosis fields and are left untouched.
func RewriteBug(
	ctx context.Context,
	llm agent.AgentExecutor,
	fsys specio.FS,
	b spec.Bug,
	applicable, changed []spec.Decision,
) (bool, string, error) {
	result, err := invokeRewriter(ctx, llm, "bug", b.ID, b.Title, b.Description, applicable, changed)
	if err != nil {
		return false, "", err
	}
	if !result.Changed || strings.TrimSpace(result.RevisedBody) == strings.TrimSpace(b.Description) {
		return false, result.Rationale, nil
	}
	b.Description = result.RevisedBody
	b.UpdatedAt = time.Now()
	if err := specio.SavePair(fsys, ".borg/spec/bugs/"+b.ID, b, result.RevisedBody); err != nil {
		return false, "", fmt.Errorf("save bug: %w", err)
	}
	return true, result.Rationale, nil
}

// InvokeRewriter is the in-memory rewriter call. RewriteFeature /
// RewriteStrategy wrap it with disk I/O for the persisted-spec flow;
// callers operating on in-memory proposals (e.g. the council's post-
// reconcile cascade pass before persistence) call this directly.
func InvokeRewriter(
	ctx context.Context,
	llm agent.AgentExecutor,
	parentKind, parentID, parentTitle, currentBody string,
	applicable, changed []spec.Decision,
) (*RewriteResult, error) {
	return invokeRewriter(ctx, llm, parentKind, parentID, parentTitle, currentBody, applicable, changed)
}

// invokeRewriter assembles the rewriter prompt and runs the LLM call with
// provider-enforced structured output (via agent.GenerateInto → Genkit
// WithOutputType). The schema for RewriteResult is derived from the Go
// type, so Gemini's responseSchema and Anthropic's OutputConfig
// constrain the response at the API layer rather than after the fact.
func invokeRewriter(
	ctx context.Context,
	llm agent.AgentExecutor,
	parentKind, parentID, parentTitle, currentBody string,
	applicable, changed []spec.Decision,
) (*RewriteResult, error) {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "## Parent kind\n%s\n\n", parentKind)
	fmt.Fprintf(&prompt, "## Parent ID\n%s\n\n", parentID)
	fmt.Fprintf(&prompt, "## Parent title\n%s\n\n", parentTitle)
	prompt.WriteString("## Current parent prose\n")
	prompt.WriteString(currentBody)
	prompt.WriteString("\n\n## Applicable Decisions\n")
	for _, d := range applicable {
		fmt.Fprintf(&prompt, "- %s (%s, confidence=%.2f): %s — %s\n", d.ID, d.Status, d.Confidence, d.Title, d.Rationale)
	}
	prompt.WriteString("\n## Recently changed Decisions\n")
	for _, d := range changed {
		fmt.Fprintf(&prompt, "- %s — %s\n", d.ID, d.Title)
	}

	def := agent.AgentDef{
		ID:           "rewriter",
		SystemPrompt: "You are the cascade rewriter. Respond with valid JSON matching the RewriteResult schema.",
	}
	input := agent.AgentInput{Messages: []agent.Message{{Role: "user", Content: prompt.String()}}}
	var out RewriteResult
	if err := agent.RunInto(agent.WithRole(ctx, "rewriter"), llm, def, input, &out); err != nil {
		return nil, fmt.Errorf("rewriter: %w", err)
	}
	return &out, nil
}

// MarkApproachesDrifted zeroes the stored SpecHash for each Approach (and
// clears stale plan pointers per DJ-072) so the next adopt classification
// routes it through drifted → planned. Exported for `refine`, which marks
// child Approaches drifted on non-Decision rewrites.
func MarkApproachesDrifted(store *state.FileStateStore, approachIDs []string, out *[]string) error {
	for _, id := range approachIDs {
		existing, err := store.Load(id)
		if err != nil {
			// No state entry yet: the Approach is already `unplanned`
			// from the classifier's perspective. Nothing to do.
			continue
		}
		existing.SpecHash = ""
		existing.Status = state.StatusDrifted
		existing.Message = "cascaded from upstream Decision change"
		existing.LastReconciled = time.Now()
		// Clear plan pointers — the prior plan referred to the old parent.
		existing.WorkstreamID = ""
		existing.AssertionResults = nil
		if err := store.Save(existing); err != nil {
			return fmt.Errorf("mark approach %s drifted: %w", id, err)
		}
		*out = append(*out, id)
	}
	return nil
}

func cascadeEvent(decisionID, parentID, kind, rationale string) history.Event {
	return history.Event{
		ID:        fmt.Sprintf("evt-cascade-%s-%d", parentID, time.Now().UnixNano()),
		Timestamp: time.Now(),
		Kind:      kind,
		TargetID:  parentID,
		Rationale: fmt.Sprintf("Cascade from decision %s: %s", decisionID, rationale),
	}
}
