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
	"github.com/chetan/locutus/internal/render"
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
	ID       string `arg:"" help:"Spec node ID to refine (Goal, Feature, Strategy, Decision, Approach, or Bug)."`
	DryRun   bool   `help:"Preview cascade blast radius; do not write spec changes."`
	Brief    string `help:"Focused refinement intent passed to the rewriter as a 'why this refine' preamble." optional:""`
	Diff     bool   `help:"After refine, print a unified diff (rendered Markdown) between the prior version and the new one."`
	Rollback bool   `help:"Undo the most recent refine: restore the prior JSON from the last spec_refined event."`
}

// RefineOptions carries the per-call refinement knobs through to the
// runners. Built from the CLI flags or the MCP input; passed by value
// so callers can append to it without affecting the original.
type RefineOptions struct {
	// Brief is the user's focused refinement intent. Empty means
	// "refine without a brief" — the runner skips the intent header
	// in the rewriter prompt.
	Brief string
	// Diff requests a unified-diff print after the refine writes.
	// The diff is computed over the per-node Markdown render, not
	// the raw JSON, so timestamp churn doesn't dominate the output.
	Diff bool
}

// validate enforces mutual exclusion between the flag combinations
// the cmd layer accepts. Surfaces on Run for both CLI and MCP.
func (c *RefineCmd) validate() error {
	if c.Rollback {
		if c.Brief != "" || c.Diff || c.DryRun {
			return fmt.Errorf("--rollback is mutually exclusive with --brief, --diff, --dry-run")
		}
	}
	return nil
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
	// Diff is the unified-diff text computed against the prior
	// stored version. Populated when --diff is set and the refine
	// produced an actual change. Empty otherwise.
	Diff string `json:"diff,omitempty"`
	// Rollback summary populated when --rollback fires.
	Rollback *RollbackSummary `json:"rollback,omitempty"`
}

// RollbackSummary describes the outcome of `refine --rollback`. Empty
// SourceEventID means no prior refine event was found and nothing was
// changed.
type RollbackSummary struct {
	SourceEventID string `json:"source_event_id,omitempty"`
	Restored      bool   `json:"restored"`
	Note          string `json:"note,omitempty"`
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
	if err := c.validate(); err != nil {
		return err
	}
	fsys, root, err := projectFS()
	if err != nil {
		return err
	}

	if c.Rollback {
		result, err := RunRollback(fsys, c.ID)
		if err != nil {
			return err
		}
		if cli.JSON {
			return json.NewEncoder(os.Stdout).Encode(result)
		}
		printRollbackSummary(result)
		return nil
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

	opts := RefineOptions{Brief: c.Brief, Diff: c.Diff}
	result, err := dispatchRefineWithOptions(ctx, llm, fsys, c.ID, kind, opts, pickSink(cli))
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
	if result.Diff != "" {
		fmt.Println()
		fmt.Println(result.Diff)
	}
	_ = rec.Close()
	fmt.Printf("Session: %s/ (per-call YAML under calls/)\n", rec.Path())
	return nil
}

// dispatchRefine routes a refine by kind. Exported to tests so the Goals
// branch and unknown-kind branch can be covered without building a full
// fixture graph. The sink argument is only consumed by the council-driven
// branches (currently Goals); single-call paths ignore it because they
// don't run a workflow.
//
// Preserved as a thin wrapper over dispatchRefineWithOptions so existing
// callers (mcp handler, refine_test.go) keep their signature.
func dispatchRefine(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, id string, kind spec.NodeKind, sink agent.EventSink) (*RefineResult, error) {
	return dispatchRefineWithOptions(ctx, llm, fsys, id, kind, RefineOptions{}, sink)
}

// dispatchRefineWithOptions threads RefineOptions to the runners.
// Capture the prior JSON before firing the runner so --diff can
// render before/after even on paths that don't record OldValue
// in history (e.g. the cascade decision path).
func dispatchRefineWithOptions(
	ctx context.Context,
	llm agent.AgentExecutor,
	fsys specio.FS,
	id string,
	kind spec.NodeKind,
	opts RefineOptions,
	sink agent.EventSink,
) (*RefineResult, error) {
	if opts.Brief != "" {
		ctx = cascade.WithBrief(ctx, opts.Brief)
	}

	// Snapshot the prior content for diff rendering. Best-effort:
	// kinds without a single canonical sidecar (Goals) skip.
	var priorJSON []byte
	if path, ok := nodeSidecarPath(kind, id); ok {
		priorJSON, _ = fsys.ReadFile(path)
	}

	var (
		result *RefineResult
		err    error
	)
	switch kind {
	case spec.KindDecision:
		result, err = RunRefine(ctx, llm, fsys, id)
	case spec.KindFeature:
		result, err = RunRefineFeature(ctx, llm, fsys, id)
	case spec.KindBug:
		result, err = RunRefineBug(ctx, llm, fsys, id)
	case spec.KindStrategy:
		result, err = RunRefineStrategy(ctx, llm, fsys, id)
	case spec.KindApproach:
		result, err = RunRefineApproach(ctx, llm, fsys, id)
	case spec.KindGoals:
		result, err = RunRefineGoals(ctx, llm, fsys, sink)
	default:
		return nil, fmt.Errorf("refine for %s is not yet implemented", kind)
	}
	if err != nil {
		return result, err
	}

	if opts.Diff {
		result.Diff = computeRefineDiff(fsys, kind, id, priorJSON, opts.Brief)
	}
	return result, nil
}

// nodeSidecarPath returns the on-disk path of a node's JSON sidecar
// (or .md for approaches) by kind and id. ok=false for kinds without
// a single canonical file (Goals, the default kind from an unknown
// id).
func nodeSidecarPath(kind spec.NodeKind, id string) (string, bool) {
	switch kind {
	case spec.KindFeature:
		return ".borg/spec/features/" + id + ".json", true
	case spec.KindStrategy:
		return ".borg/spec/strategies/" + id + ".json", true
	case spec.KindDecision:
		return ".borg/spec/decisions/" + id + ".json", true
	case spec.KindBug:
		return ".borg/spec/bugs/" + id + ".json", true
	case spec.KindApproach:
		return ".borg/spec/approaches/" + id + ".md", true
	}
	return "", false
}

// computeRefineDiff renders prior + new node Markdown via the same
// stub-Loaded path on both sides so the diff captures only the
// node's own change — not back-reference resolution from other
// nodes that the live graph would surface. brief is appended to the
// "+++ after" header so the diff records the user's intent inline.
// Returns "" on any read or render error — diff is a UX nicety,
// never load-bearing.
func computeRefineDiff(fsys specio.FS, kind spec.NodeKind, id string, priorJSON []byte, brief string) string {
	if len(priorJSON) == 0 {
		return ""
	}
	path, ok := nodeSidecarPath(kind, id)
	if !ok {
		return ""
	}
	newJSON, err := fsys.ReadFile(path)
	if err != nil {
		return ""
	}
	if string(priorJSON) == string(newJSON) {
		return ""
	}
	priorMD, err := renderNodeFromJSON(kind, id, priorJSON)
	if err != nil {
		return ""
	}
	newMD, err := renderNodeFromJSON(kind, id, newJSON)
	if err != nil {
		return ""
	}
	headerOld := fmt.Sprintf("a/%s (before refine)", id)
	headerNew := fmt.Sprintf("b/%s (after refine", id)
	if brief != "" {
		headerNew += fmt.Sprintf(", brief=%q", brief)
	}
	headerNew += ")"
	return render.RenderDiff(headerOld, headerNew, priorMD, newMD)
}

// renderNodeFromJSON renders one node from its raw JSON bytes. Used
// to render the pre-refine state for diff display, since the spec
// graph already moved to the post-refine state by the time the diff
// runs. Approaches are markdown-only — when kind=Approach, priorJSON
// is actually the .md body and we render it directly.
func renderNodeFromJSON(kind spec.NodeKind, id string, priorJSON []byte) (string, error) {
	stub := &spec.Loaded{}
	stage := spec.StageMap{}
	switch kind {
	case spec.KindFeature:
		var f spec.Feature
		if err := json.Unmarshal(priorJSON, &f); err != nil {
			return "", err
		}
		return render.RenderFeature(spec.FeatureNode{Spec: f, Body: f.Description}, stub, stage), nil
	case spec.KindStrategy:
		var s spec.Strategy
		if err := json.Unmarshal(priorJSON, &s); err != nil {
			return "", err
		}
		return render.RenderStrategy(spec.StrategyNode{Spec: s}, stub, stage), nil
	case spec.KindDecision:
		var d spec.Decision
		if err := json.Unmarshal(priorJSON, &d); err != nil {
			return "", err
		}
		return render.RenderDecision(spec.DecisionNode{Spec: d}, stub, stage), nil
	case spec.KindBug:
		var b spec.Bug
		if err := json.Unmarshal(priorJSON, &b); err != nil {
			return "", err
		}
		return render.RenderBug(spec.BugNode{Spec: b}, stub), nil
	case spec.KindApproach:
		// priorJSON is the markdown body for approaches.
		// Stub Approach so the renderer can produce a header.
		return string(priorJSON), nil
	}
	return "", fmt.Errorf("renderNodeFromJSON: kind %s not supported", kind)
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
func RunRefineGoals(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, sink agent.EventSink) (*RefineResult, error) {
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
func RunRefine(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, decisionID string) (*RefineResult, error) {
	g := buildGraphForRefine(fsys)

	if g.Decision(decisionID) == nil {
		return nil, fmt.Errorf("refine: decision %q not found", decisionID)
	}

	store := state.NewFileStateStore(fsys, state.DefaultStateDir)
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
// history event with old/new bytes (DJ-102 — feeds rollback and --diff).
func RunRefineFeature(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, featureID string) (*RefineResult, error) {
	g := buildGraphForRefine(fsys)
	f := g.Feature(featureID)
	if f == nil {
		return nil, fmt.Errorf("refine: feature %q not found", featureID)
	}

	priorBytes, _ := fsys.ReadFile(".borg/spec/features/" + featureID + ".json")
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

	store := state.NewFileStateStore(fsys, state.DefaultStateDir)
	if err := cascade.MarkApproachesDrifted(store, f.Approaches, &result.Rewrite.DriftedApproaches); err != nil {
		return result, fmt.Errorf("refine feature drift: %w", err)
	}

	newBytes, _ := fsys.ReadFile(".borg/spec/features/" + featureID + ".json")
	hist := history.NewHistorian(fsys, ".borg/history")
	brief := cascade.BriefFromContext(ctx)
	if err := history.RecordRefined(hist, featureID, string(priorBytes), string(newBytes), briefOrRationale(brief, rationale)); err != nil {
		return result, fmt.Errorf("refine feature event: %w", err)
	}
	return result, nil
}

// RunRefineStrategy does the same for a Strategy. The prose lives in the
// .md body rather than a struct field; cascade.RewriteStrategy handles
// the round-trip.
func RunRefineStrategy(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, strategyID string) (*RefineResult, error) {
	g := buildGraphForRefine(fsys)
	s := g.Strategy(strategyID)
	if s == nil {
		return nil, fmt.Errorf("refine: strategy %q not found", strategyID)
	}

	priorBytes, _ := fsys.ReadFile(".borg/spec/strategies/" + strategyID + ".json")
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

	store := state.NewFileStateStore(fsys, state.DefaultStateDir)
	if err := cascade.MarkApproachesDrifted(store, s.Approaches, &result.Rewrite.DriftedApproaches); err != nil {
		return result, fmt.Errorf("refine strategy drift: %w", err)
	}
	newBytes, _ := fsys.ReadFile(".borg/spec/strategies/" + strategyID + ".json")
	hist := history.NewHistorian(fsys, ".borg/history")
	brief := cascade.BriefFromContext(ctx)
	if err := history.RecordRefined(hist, strategyID, string(priorBytes), string(newBytes), briefOrRationale(brief, rationale)); err != nil {
		return result, fmt.Errorf("refine strategy event: %w", err)
	}
	return result, nil
}

// RunRefineBug rewrites Bug.Description using the parent Feature's
// applicable Decisions. Bugs have no Decisions slice of their own — they
// inherit the Feature's context. RootCause and FixPlan are
// incident-diagnosis fields and are not touched by refine.
func RunRefineBug(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, bugID string) (*RefineResult, error) {
	g := buildGraphForRefine(fsys)
	b := g.Bug(bugID)
	if b == nil {
		return nil, fmt.Errorf("refine: bug %q not found", bugID)
	}

	var applicable []spec.Decision
	if parent := g.Feature(b.FeatureID); parent != nil {
		applicable = applicableDecisionsFor(g, parent.Decisions)
	}
	priorBytes, _ := fsys.ReadFile(".borg/spec/bugs/" + bugID + ".json")
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
		store := state.NewFileStateStore(fsys, state.DefaultStateDir)
		if err := cascade.MarkApproachesDrifted(store, childIDs, &result.Rewrite.DriftedApproaches); err != nil {
			return result, fmt.Errorf("refine bug drift: %w", err)
		}
	}
	newBytes, _ := fsys.ReadFile(".borg/spec/bugs/" + bugID + ".json")
	hist := history.NewHistorian(fsys, ".borg/history")
	brief := cascade.BriefFromContext(ctx)
	if err := history.RecordRefined(hist, bugID, string(priorBytes), string(newBytes), briefOrRationale(brief, rationale)); err != nil {
		return result, fmt.Errorf("refine bug event: %w", err)
	}
	return result, nil
}

// RunRefineApproach re-synthesizes Approach.Body from parent prose and
// applicable Decisions via the synthesizer agent. The refined Approach
// is itself marked drifted so the next `adopt` classifies it and replans.
func RunRefineApproach(ctx context.Context, llm agent.AgentExecutor, fsys specio.FS, approachID string) (*RefineResult, error) {
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

	priorBytes, _ := fsys.ReadFile(".borg/spec/approaches/" + approachID + ".md")
	a.Body = resp.RevisedBody
	a.UpdatedAt = time.Now()
	if err := specio.SaveMarkdown(fsys, ".borg/spec/approaches/"+approachID+".md", *a, resp.RevisedBody); err != nil {
		return result, fmt.Errorf("save approach: %w", err)
	}

	store := state.NewFileStateStore(fsys, state.DefaultStateDir)
	if err := cascade.MarkApproachesDrifted(store, []string{approachID}, &result.Rewrite.DriftedApproaches); err != nil {
		return result, fmt.Errorf("refine approach drift: %w", err)
	}
	newBytes, _ := fsys.ReadFile(".borg/spec/approaches/" + approachID + ".md")
	hist := history.NewHistorian(fsys, ".borg/history")
	brief := cascade.BriefFromContext(ctx)
	if err := history.RecordRefined(hist, approachID, string(priorBytes), string(newBytes), briefOrRationale(brief, resp.Rationale)); err != nil {
		return result, fmt.Errorf("refine approach event: %w", err)
	}
	return result, nil
}

// briefOrRationale picks the most useful string for the history
// event's Rationale field. The user's brief takes priority — it's
// the explicit "why we refined" — and falls back to the agent's
// auto-generated rationale (rewriter or synthesizer self-report)
// when no brief was supplied. Empty fallback yields empty rationale,
// which RecordRefined replaces with a neutral default.
func briefOrRationale(brief, rationale string) string {
	if strings.TrimSpace(brief) != "" {
		return brief
	}
	return rationale
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
func invokeSynthesizer(ctx context.Context, llm agent.AgentExecutor, a spec.Approach, parent parentContext, applicable []spec.Decision) (*cascade.RewriteResult, error) {
	var prompt strings.Builder
	if brief := cascade.BriefFromContext(ctx); brief != "" {
		fmt.Fprintf(&prompt, "## Refinement intent\n%s\n\n", brief)
	}
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

	def := agent.AgentDef{
		ID:           "synthesizer",
		SystemPrompt: "You are the approach synthesizer. Respond with valid JSON matching the RewriteResult schema. Set revised_body to the new approach Markdown, changed=true if the body changed, and rationale to a one-line summary.",
		OutputSchema: "RewriteResult",
	}
	input := agent.AgentInput{Messages: []agent.Message{{Role: "user", Content: prompt.String()}}}
	var out cascade.RewriteResult
	if err := agent.RunInto(agent.WithRole(ctx, "synthesizer"), llm, def, input, &out); err != nil {
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

// RunRollback restores a node to the OldValue captured in its most
// recent spec_refined event and records a spec_rolled_back event so
// subsequent rollbacks see it. Does NOT cascade — restoring a
// decision can leave linked features/strategies inconsistent; the
// summary's Note flags this for the operator. Returns a
// RefineResult with Rollback populated.
//
// nodeID kind is inferred from the id prefix; if the id is unknown
// or the node has no recorded refine event, returns a clean
// "nothing to rollback" result rather than an error so scripts that
// optimistically rollback on every refine don't fail.
func RunRollback(fsys specio.FS, nodeID string) (*RefineResult, error) {
	hist := history.NewHistorian(fsys, ".borg/history")
	evt, err := history.LatestRefinedEvent(hist, nodeID)
	if err != nil {
		return nil, fmt.Errorf("rollback: lookup history: %w", err)
	}
	if evt == nil {
		return &RefineResult{
			NodeID: nodeID,
			Rollback: &RollbackSummary{
				Restored: false,
				Note:     "no spec_refined event found for this node — nothing to rollback",
			},
		}, nil
	}

	kind, err := resolveNodeKind(fsys, nodeID)
	if err != nil {
		return nil, err
	}
	path, ok := nodeSidecarPath(kind, nodeID)
	if !ok {
		return nil, fmt.Errorf("rollback: kind %s has no canonical sidecar path", kind)
	}

	currentBytes, err := fsys.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("rollback: read current %s: %w", path, err)
	}
	if err := specio.AtomicWriteFile(fsys, path, []byte(evt.OldValue), 0o644); err != nil {
		return nil, fmt.Errorf("rollback: write %s: %w", path, err)
	}
	if err := history.RecordRolledBack(hist, nodeID, string(currentBytes), evt.OldValue, evt.ID); err != nil {
		return nil, fmt.Errorf("rollback: record event: %w", err)
	}

	note := fmt.Sprintf("restored from %s; cascaded parents (if any) are NOT undone — inspect with `locutus history %s`", evt.ID, nodeID)
	return &RefineResult{
		NodeID:   nodeID,
		NodeKind: kind,
		Rollback: &RollbackSummary{
			SourceEventID: evt.ID,
			Restored:      true,
			Note:          note,
		},
	}, nil
}

// printRollbackSummary writes the rollback outcome to stdout.
func printRollbackSummary(r *RefineResult) {
	if r == nil || r.Rollback == nil {
		fmt.Println("Rollback: no result.")
		return
	}
	if !r.Rollback.Restored {
		fmt.Printf("Rollback %s: %s\n", r.NodeID, r.Rollback.Note)
		return
	}
	fmt.Printf("Rolled back %s %s (source event: %s)\n", r.NodeKind, r.NodeID, r.Rollback.SourceEventID)
	if r.Rollback.Note != "" {
		fmt.Printf("Note: %s\n", r.Rollback.Note)
	}
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
