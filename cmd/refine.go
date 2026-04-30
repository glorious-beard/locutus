package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
)

// RefineCmd runs council-driven deliberation on any spec node, dispatching
// by NodeKind per DJ-069.
//
// - Decision: fires the cascade (parent prose rewritten, child Approaches
//   drifted, events recorded). See cascade.Cascade.
// - Feature / Strategy / Bug: reverse-cascade — regenerate the parent's
//   prose from its applicable Decisions, mark child Approaches drifted.
// - Approach: re-synthesize the Approach Body from parent prose +
//   applicable Decisions via the synthesizer agent. The refined Approach
//   is itself marked drifted so the next `adopt` replans it.
// - Goals: human-authored; refine returns an explicit error.
type RefineCmd struct {
	ID     string `arg:"" help:"Spec node ID to refine (Goal, Feature, Strategy, Decision, Approach, or Bug)."`
	DryRun bool   `help:"Preview cascade blast radius; do not write spec changes."`
}

// RefineResult is the shared result shape for the CLI and MCP handlers.
// Exactly one of `Cascade` (Decision path) or `Rewrite` (non-Decision
// paths) is populated.
type RefineResult struct {
	NodeID    string             `json:"node_id"`
	NodeKind  spec.NodeKind      `json:"node_kind"`
	Cascade   *cascade.Result    `json:"cascade,omitempty"`
	Rewrite   *RewriteSummary    `json:"rewrite,omitempty"`
	Generated *GenerationSummary `json:"generated,omitempty"`
}

// RewriteSummary captures the outcome of a Feature / Strategy / Bug /
// Approach refine. Updated=false means the rewriter/synthesizer judged
// the node already consistent with its inputs; no writes happened and
// no downstream drift was triggered.
type RewriteSummary struct {
	Updated           bool     `json:"updated"`
	Rationale         string   `json:"rationale,omitempty"`
	DriftedApproaches []string `json:"drifted_approaches,omitempty"`
}

func (c *RefineCmd) Run(ctx context.Context, cli *CLI) error {
	fsys, root, err := projectFS()
	if err != nil {
		return err
	}

	kind, err := resolveNodeKind(fsys, c.ID)
	if err != nil {
		return err
	}

	if c.DryRun {
		return renderRefineDryRun(fsys, c.ID, kind)
	}

	llm, rec, err := recordingLLM(fsys, root, "refine "+c.ID)
	if err != nil {
		return err
	}

	result, err := dispatchRefine(ctx, llm, fsys, c.ID, kind, pickSink(cli))
	if err != nil {
		// Integrity violations are user-actionable: surface the
		// dangling refs explicitly so they can re-run, switch model,
		// or hand-edit. Generic kong wrapping would drop the warning
		// list — useful information the user paid LLM tokens for.
		if msg, ok := formatIntegrityViolation(err); ok {
			fmt.Fprintln(os.Stderr, msg)
			return ExitCode(1)
		}
		return err
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printRefineSummary(result)
	fmt.Printf("Session: %s\n", rec.Path())
	return nil
}

// dispatchRefine routes a refine by kind. Exported to tests so the Goals
// branch and unknown-kind branch can be covered without building a full
// fixture graph. The sink argument is only consumed by the council-driven
// branches (currently Goals); single-call paths ignore it because they
// don't run a workflow.
func dispatchRefine(ctx context.Context, llm agent.LLM, fsys specio.FS, id string, kind spec.NodeKind, sink agent.EventSink) (*RefineResult, error) {
	switch kind {
	case spec.KindDecision:
		return RunRefine(ctx, llm, fsys, id)
	case spec.KindFeature:
		return RunRefineFeature(ctx, llm, fsys, id)
	case spec.KindBug:
		return RunRefineBug(ctx, llm, fsys, id)
	case spec.KindStrategy:
		return RunRefineStrategy(ctx, llm, fsys, id)
	case spec.KindApproach:
		return RunRefineApproach(ctx, llm, fsys, id)
	case spec.KindGoals:
		return RunRefineGoals(ctx, llm, fsys, sink)
	default:
		return nil, fmt.Errorf("refine for %s is not yet implemented", kind)
	}
}

// RunRefineGoals fires the spec-generation pipeline against GOALS.md.
// It reads the current goals body and the existing spec snapshot, runs an
// LLM call that proposes features/decisions/strategies/approaches, and
// persists the result through the same atomicity layer as `assimilate`.
//
// Re-running is incremental: existing nodes whose IDs the LLM reuses are
// updated in place, new IDs land as new files. Quality strategies
// (testing, observability, deployment) are mandatory in the LLM prompt
// so engineering best practices show up by construction.
func RunRefineGoals(ctx context.Context, llm agent.LLM, fsys specio.FS, sink agent.EventSink) (*RefineResult, error) {
	goalsBody, found := readGoals(fsys)
	if !found || strings.TrimSpace(goalsBody) == "" {
		return nil, fmt.Errorf("refine goals: GOALS.md is empty or missing — populate it before running")
	}

	existing := loadExistingSpec(fsys)
	gen, err := runSpecGeneration(ctx, llm, fsys, agent.SpecGenRequest{
		GoalsBody: goalsBody,
		Existing:  existing,
		Sink:      sink,
	})
	if err != nil {
		return nil, fmt.Errorf("refine goals: %w", err)
	}

	hist := history.NewHistorian(fsys, ".borg/history")
	rationale := fmt.Sprintf("Generated %d feature(s), %d decision(s), %d strategy(ies), %d approach(es) from GOALS.md.",
		gen.Features, gen.Decisions, gen.Strategies, gen.Approaches)
	if err := hist.Record(refineEvent(spec.RootID, "goals_refined", rationale)); err != nil {
		slog.Warn("failed to record goals_refined event", "error", err)
	}

	return &RefineResult{
		NodeID:    spec.RootID,
		NodeKind:  spec.KindGoals,
		Generated: gen,
	}, nil
}

// RunRefine is the Decision-path refinement: fires the DJ-069 cascade.
// The Decision must already be saved in its desired form (either edited
// by the user or produced by a prior council-driven step). Cascade walks
// the graph to find parent Features/Strategies that reference the
// Decision, rewrites their present-tense prose, marks child Approaches
// drifted, and records history events.
func RunRefine(ctx context.Context, llm agent.LLM, fsys specio.FS, decisionID string) (*RefineResult, error) {
	g := buildGraphForRefine(fsys)

	if g.Decision(decisionID) == nil {
		return nil, fmt.Errorf("refine: decision %q not found", decisionID)
	}

	store := state.NewFileStateStore(fsys, ".locutus/state")
	cascadeResult, err := cascade.Cascade(ctx, llm, fsys, g, store, decisionID)
	if err != nil {
		return &RefineResult{NodeID: decisionID, NodeKind: spec.KindDecision, Cascade: cascadeResult}, err
	}

	return &RefineResult{
		NodeID:   decisionID,
		NodeKind: spec.KindDecision,
		Cascade:  cascadeResult,
	}, nil
}

// RunRefineFeature rewrites Feature.Description to reflect its currently
// applicable Decisions, marks child Approaches drifted, and records a
// history event.
func RunRefineFeature(ctx context.Context, llm agent.LLM, fsys specio.FS, featureID string) (*RefineResult, error) {
	g := buildGraphForRefine(fsys)
	f := g.Feature(featureID)
	if f == nil {
		return nil, fmt.Errorf("refine: feature %q not found", featureID)
	}

	applicable := applicableDecisionsFor(g, f.Decisions)
	changed, rationale, err := cascade.RewriteFeature(ctx, llm, fsys, *f, applicable, nil)
	if err != nil {
		return nil, fmt.Errorf("refine feature: %w", err)
	}

	result := &RefineResult{
		NodeID:   featureID,
		NodeKind: spec.KindFeature,
		Rewrite:  &RewriteSummary{Updated: changed, Rationale: rationale},
	}
	if !changed {
		return result, nil
	}

	store := state.NewFileStateStore(fsys, ".locutus/state")
	if err := cascade.MarkApproachesDrifted(store, f.Approaches, &result.Rewrite.DriftedApproaches); err != nil {
		return result, fmt.Errorf("refine feature drift: %w", err)
	}

	hist := history.NewHistorian(fsys, ".borg/history")
	if err := hist.Record(refineEvent(featureID, "feature_refined", rationale)); err != nil {
		return result, fmt.Errorf("refine feature event: %w", err)
	}
	return result, nil
}

// RunRefineStrategy does the same for a Strategy. The prose lives in the
// .md body rather than a struct field; cascade.RewriteStrategy handles
// the round-trip.
func RunRefineStrategy(ctx context.Context, llm agent.LLM, fsys specio.FS, strategyID string) (*RefineResult, error) {
	g := buildGraphForRefine(fsys)
	s := g.Strategy(strategyID)
	if s == nil {
		return nil, fmt.Errorf("refine: strategy %q not found", strategyID)
	}

	applicable := applicableDecisionsFor(g, s.Decisions)
	changed, rationale, err := cascade.RewriteStrategy(ctx, llm, fsys, *s, applicable, nil)
	if err != nil {
		return nil, fmt.Errorf("refine strategy: %w", err)
	}

	result := &RefineResult{
		NodeID:   strategyID,
		NodeKind: spec.KindStrategy,
		Rewrite:  &RewriteSummary{Updated: changed, Rationale: rationale},
	}
	if !changed {
		return result, nil
	}

	store := state.NewFileStateStore(fsys, ".locutus/state")
	if err := cascade.MarkApproachesDrifted(store, s.Approaches, &result.Rewrite.DriftedApproaches); err != nil {
		return result, fmt.Errorf("refine strategy drift: %w", err)
	}
	hist := history.NewHistorian(fsys, ".borg/history")
	if err := hist.Record(refineEvent(strategyID, "strategy_refined", rationale)); err != nil {
		return result, fmt.Errorf("refine strategy event: %w", err)
	}
	return result, nil
}

// RunRefineBug rewrites Bug.Description using the parent Feature's
// applicable Decisions. Bugs have no Decisions slice of their own — they
// inherit the Feature's context. RootCause and FixPlan are
// incident-diagnosis fields and are not touched by refine.
func RunRefineBug(ctx context.Context, llm agent.LLM, fsys specio.FS, bugID string) (*RefineResult, error) {
	g := buildGraphForRefine(fsys)
	b := g.Bug(bugID)
	if b == nil {
		return nil, fmt.Errorf("refine: bug %q not found", bugID)
	}

	var applicable []spec.Decision
	if parent := g.Feature(b.FeatureID); parent != nil {
		applicable = applicableDecisionsFor(g, parent.Decisions)
	}
	changed, rationale, err := cascade.RewriteBug(ctx, llm, fsys, *b, applicable, nil)
	if err != nil {
		return nil, fmt.Errorf("refine bug: %w", err)
	}

	result := &RefineResult{
		NodeID:   bugID,
		NodeKind: spec.KindBug,
		Rewrite:  &RewriteSummary{Updated: changed, Rationale: rationale},
	}
	if !changed {
		return result, nil
	}

	childIDs := childApproachesOf(g, bugID)
	if len(childIDs) > 0 {
		store := state.NewFileStateStore(fsys, ".locutus/state")
		if err := cascade.MarkApproachesDrifted(store, childIDs, &result.Rewrite.DriftedApproaches); err != nil {
			return result, fmt.Errorf("refine bug drift: %w", err)
		}
	}
	hist := history.NewHistorian(fsys, ".borg/history")
	if err := hist.Record(refineEvent(bugID, "bug_refined", rationale)); err != nil {
		return result, fmt.Errorf("refine bug event: %w", err)
	}
	return result, nil
}

// RunRefineApproach re-synthesizes Approach.Body from parent prose and
// applicable Decisions via the synthesizer agent. The refined Approach
// is itself marked drifted so the next `adopt` classifies it and replans.
func RunRefineApproach(ctx context.Context, llm agent.LLM, fsys specio.FS, approachID string) (*RefineResult, error) {
	g := buildGraphForRefine(fsys)
	a := g.Approach(approachID)
	if a == nil {
		return nil, fmt.Errorf("refine: approach %q not found", approachID)
	}

	parent, err := resolveParentContext(fsys, g, a.ParentID)
	if err != nil {
		return nil, fmt.Errorf("refine approach: %w", err)
	}

	applicable := applicableDecisionsFor(g, a.Decisions)
	resp, err := invokeSynthesizer(ctx, llm, *a, parent, applicable)
	if err != nil {
		return nil, fmt.Errorf("refine approach: %w", err)
	}

	result := &RefineResult{
		NodeID:   approachID,
		NodeKind: spec.KindApproach,
		Rewrite:  &RewriteSummary{Updated: resp.Changed, Rationale: resp.Rationale},
	}

	if !resp.Changed || strings.TrimSpace(resp.RevisedBody) == strings.TrimSpace(a.Body) {
		result.Rewrite.Updated = false
		return result, nil
	}

	a.Body = resp.RevisedBody
	a.UpdatedAt = time.Now()
	if err := specio.SaveMarkdown(fsys, ".borg/spec/approaches/"+approachID+".md", *a, resp.RevisedBody); err != nil {
		return result, fmt.Errorf("save approach: %w", err)
	}

	store := state.NewFileStateStore(fsys, ".locutus/state")
	if err := cascade.MarkApproachesDrifted(store, []string{approachID}, &result.Rewrite.DriftedApproaches); err != nil {
		return result, fmt.Errorf("refine approach drift: %w", err)
	}
	hist := history.NewHistorian(fsys, ".borg/history")
	if err := hist.Record(refineEvent(approachID, "approach_refined", resp.Rationale)); err != nil {
		return result, fmt.Errorf("refine approach event: %w", err)
	}
	return result, nil
}

// parentContext is the subset of a parent spec node that the synthesizer
// needs in the prompt. Strategies load prose from disk; Features and Bugs
// carry prose as a struct field.
type parentContext struct {
	Kind      spec.NodeKind
	ID        string
	Title     string
	Prose     string
	Decisions []string
}

func resolveParentContext(fsys specio.FS, g *spec.SpecGraph, parentID string) (parentContext, error) {
	if f := g.Feature(parentID); f != nil {
		return parentContext{
			Kind: spec.KindFeature, ID: f.ID, Title: f.Title,
			Prose: f.Description, Decisions: f.Decisions,
		}, nil
	}
	if s := g.Strategy(parentID); s != nil {
		body, _ := fsys.ReadFile(".borg/spec/strategies/" + s.ID + ".md")
		return parentContext{
			Kind: spec.KindStrategy, ID: s.ID, Title: s.Title,
			Prose: string(body), Decisions: s.Decisions,
		}, nil
	}
	if b := g.Bug(parentID); b != nil {
		return parentContext{
			Kind: spec.KindBug, ID: b.ID, Title: b.Title,
			Prose: b.Description,
		}, nil
	}
	return parentContext{}, fmt.Errorf("parent %q not found", parentID)
}

// invokeSynthesizer assembles the synthesizer prompt and parses the JSON
// reply. Shares the RewriteResult output schema with the rewriter so
// callers can reuse cascade.RewriteResult for parsing.
func invokeSynthesizer(ctx context.Context, llm agent.LLM, a spec.Approach, parent parentContext, applicable []spec.Decision) (*cascade.RewriteResult, error) {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "## Approach\n%s — %s\n\n", a.ID, a.Title)
	fmt.Fprintf(&prompt, "## Parent kind\n%s\n\n", parent.Kind)
	fmt.Fprintf(&prompt, "## Parent ID\n%s\n\n", parent.ID)
	fmt.Fprintf(&prompt, "## Parent title\n%s\n\n", parent.Title)
	prompt.WriteString("## Parent prose\n")
	prompt.WriteString(parent.Prose)
	prompt.WriteString("\n\n## Applicable Decisions\n")
	for _, d := range applicable {
		fmt.Fprintf(&prompt, "- %s (%s, confidence=%.2f): %s — %s\n",
			d.ID, d.Status, d.Confidence, d.Title, d.Rationale)
	}
	prompt.WriteString("\n## Current Approach body\n")
	prompt.WriteString(a.Body)

	req := agent.GenerateRequest{
		Messages: []agent.Message{
			{Role: "system", Content: "You are the approach synthesizer. Respond with valid JSON matching the RewriteResult schema."},
			{Role: "user", Content: prompt.String()},
		},
	}
	var out cascade.RewriteResult
	if err := agent.GenerateInto(agent.WithRole(ctx, "synthesizer"), llm, req, &out); err != nil {
		return nil, fmt.Errorf("synthesizer: %w", err)
	}
	return &out, nil
}

// buildGraphForRefine constructs a SpecGraph from the on-disk spec. This
// is a cheap operation on a fresh graph; the cost is spec I/O, not the
// lookup itself.
func buildGraphForRefine(fsys specio.FS) *spec.SpecGraph {
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
	bugs, _ := collectObjects[spec.Bug](fsys, ".borg/spec/bugs")
	approaches, _ := collectMarkdown[spec.Approach](fsys, ".borg/spec/approaches")

	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		if err := json.Unmarshal(data, &traces); err != nil {
			slog.Warn("traces.json unmarshal failed; proceeding without traces", "error", err)
		}
	}
	return spec.BuildGraph(features, bugs, decisions, strategies, approaches, traces)
}

func applicableDecisionsFor(g *spec.SpecGraph, ids []string) []spec.Decision {
	out := make([]spec.Decision, 0, len(ids))
	for _, id := range ids {
		if d := g.Decision(id); d != nil {
			out = append(out, *d)
		}
	}
	return out
}

func childApproachesOf(g *spec.SpecGraph, parentID string) []string {
	var out []string
	for id, node := range g.Nodes() {
		if node.Kind != spec.KindApproach {
			continue
		}
		if a := g.Approach(id); a != nil && a.ParentID == parentID {
			out = append(out, a.ID)
		}
	}
	return out
}

func refineEvent(nodeID, kind, rationale string) history.Event {
	return history.Event{
		ID:        fmt.Sprintf("evt-refine-%s-%d", nodeID, time.Now().UnixNano()),
		Timestamp: time.Now(),
		Kind:      kind,
		TargetID:  nodeID,
		Rationale: rationale,
	}
}

func printRefineSummary(r *RefineResult) {
	if r == nil {
		fmt.Println("Refine: no result.")
		return
	}
	if r.Cascade != nil {
		printCascadeSummary(r.NodeID, r.Cascade)
		return
	}
	if r.Generated != nil {
		fmt.Printf("Refined %s %s: %d feature(s), %d decision(s), %d strategy(ies), %d approach(es).\n",
			r.NodeKind, r.NodeID,
			r.Generated.Features, r.Generated.Decisions, r.Generated.Strategies, r.Generated.Approaches)
		if len(r.Generated.IntegrityWarnings) > 0 {
			fmt.Printf("  %d dangling reference(s) stripped from the LLM output:\n", len(r.Generated.IntegrityWarnings))
			for _, w := range r.Generated.IntegrityWarnings {
				fmt.Printf("    - %s\n", w)
			}
		}
		return
	}
	if r.Rewrite == nil {
		fmt.Printf("Refined %s %s: no action.\n", r.NodeKind, r.NodeID)
		return
	}
	fmt.Printf("Refined %s %s:\n", r.NodeKind, r.NodeID)
	if !r.Rewrite.Updated {
		fmt.Println("  No changes — already consistent with inputs.")
		if r.Rewrite.Rationale != "" {
			fmt.Printf("  Rationale: %s\n", r.Rewrite.Rationale)
		}
		return
	}
	if r.Rewrite.Rationale != "" {
		fmt.Printf("  Rationale: %s\n", r.Rewrite.Rationale)
	}
	if len(r.Rewrite.DriftedApproaches) > 0 {
		fmt.Printf("  Approaches drifted: %d\n", len(r.Rewrite.DriftedApproaches))
		for _, a := range r.Rewrite.DriftedApproaches {
			fmt.Printf("    - %s\n", a)
		}
	}
}

func printCascadeSummary(id string, r *cascade.Result) {
	if r == nil {
		fmt.Printf("Refined %s: cascade produced no changes.\n", id)
		return
	}
	fmt.Printf("Refined decision %s:\n", id)
	if len(r.UpdatedFeatures) > 0 {
		fmt.Printf("  Features rewritten:   %d\n", len(r.UpdatedFeatures))
		for _, f := range r.UpdatedFeatures {
			fmt.Printf("    - %s\n", f)
		}
	}
	if len(r.UpdatedStrategies) > 0 {
		fmt.Printf("  Strategies rewritten: %d\n", len(r.UpdatedStrategies))
		for _, s := range r.UpdatedStrategies {
			fmt.Printf("    - %s\n", s)
		}
	}
	if len(r.DriftedApproaches) > 0 {
		fmt.Printf("  Approaches drifted:   %d\n", len(r.DriftedApproaches))
		for _, a := range r.DriftedApproaches {
			fmt.Printf("    - %s\n", a)
		}
	}
	if len(r.Skipped) > 0 {
		fmt.Printf("  Parents already accurate (skipped): %d\n", len(r.Skipped))
	}
	if len(r.UpdatedFeatures)+len(r.UpdatedStrategies)+len(r.DriftedApproaches) == 0 {
		fmt.Println("  Cascade was a no-op — spec graph already reflects the decision.")
	}
}

// resolveNodeKind looks up the kind of a spec node by walking the graph.
func resolveNodeKind(fsys specio.FS, id string) (spec.NodeKind, error) {
	g := buildGraphForRefine(fsys)
	nodes := g.Nodes()
	n, ok := nodes[id]
	if !ok {
		return "", fmt.Errorf("unknown spec node %q", id)
	}
	return n.Kind, nil
}

// renderRefineDryRun prints the cascade blast radius without mutating spec.
func renderRefineDryRun(fsys specio.FS, id string, kind spec.NodeKind) error {
	br, err := RunDiff(fsys, id)
	if err != nil {
		return err
	}

	fmt.Printf("Refining %s %s — cascade preview (no changes written):\n", kind, id)
	if len(br.Decisions) > 0 {
		fmt.Printf("  Decisions affected:  %d\n", len(br.Decisions))
		for _, d := range br.Decisions {
			fmt.Printf("    - %s\n", d.ID)
		}
	}
	if len(br.Strategies) > 0 {
		fmt.Printf("  Strategies affected: %d\n", len(br.Strategies))
		for _, s := range br.Strategies {
			fmt.Printf("    - %s\n", s.ID)
		}
	}
	if len(br.Approaches) > 0 {
		fmt.Printf("  Approaches drifted:  %d\n", len(br.Approaches))
		for _, a := range br.Approaches {
			fmt.Printf("    - %s\n", a.ID)
		}
	}
	return nil
}
