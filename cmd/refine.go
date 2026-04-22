package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
)

// RefineCmd runs council-driven deliberation on any spec node. It replaces
// the former `revisit` command (which was decision/strategy-only) with a
// node-kind-dispatching refiner that handles Goals, Features, Strategies,
// Decisions, Approaches, and Bugs.
//
// Phase C: the Decision path runs the DJ-069 cascade — rewrites parent
// Feature/Strategy prose, marks child Approaches drifted in the state
// store, and records history events. Other node kinds return a clear
// "not yet implemented" message until future rounds wire the refiner
// into the planner/council for those paths.
type RefineCmd struct {
	ID     string `arg:"" help:"Spec node ID to refine (Goal, Feature, Strategy, Decision, Approach, or Bug)."`
	DryRun bool   `help:"Preview cascade blast radius; do not write spec changes."`
}

// RefineResult is the shared result shape for the CLI and MCP handlers.
// Exactly one of the pointer fields is populated depending on the node
// kind being refined.
type RefineResult struct {
	NodeID   string         `json:"node_id"`
	NodeKind spec.NodeKind  `json:"node_kind"`
	Cascade  *cascade.Result `json:"cascade,omitempty"`
}

func (c *RefineCmd) Run(cli *CLI) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	fsys := specio.NewOSFS(cwd)

	kind, err := resolveNodeKind(fsys, c.ID)
	if err != nil {
		return err
	}

	if c.DryRun {
		return renderRefineDryRun(fsys, c.ID, kind)
	}

	llm, err := getLLM()
	if err != nil {
		return err
	}

	switch kind {
	case spec.KindDecision:
		result, err := RunRefine(context.Background(), llm, fsys, c.ID)
		if err != nil {
			return err
		}
		if cli.JSON {
			return json.NewEncoder(os.Stdout).Encode(result)
		}
		printCascadeSummary(c.ID, result.Cascade)
		return nil
	default:
		return fmt.Errorf("refine for %s is not yet implemented (later Phase C round)", kind)
	}
}

// RunRefine is the Decision-path refinement: fires the DJ-069 cascade.
// The Decision must already be saved in its desired form (either edited
// by the user or produced by a prior council-driven step). Cascade walks
// the graph to find parent Features/Strategies that reference the
// Decision, rewrites their present-tense prose, marks child Approaches
// drifted, and records history events.
func RunRefine(ctx context.Context, llm agent.LLM, fsys specio.FS, decisionID string) (*RefineResult, error) {
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
	bugs, _ := collectObjects[spec.Bug](fsys, ".borg/spec/bugs")
	approaches, _ := collectMarkdown[spec.Approach](fsys, ".borg/spec/approaches")

	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		_ = json.Unmarshal(data, &traces)
	}
	g := spec.BuildGraph(features, bugs, decisions, strategies, approaches, traces)

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

// resolveNodeKind looks up the kind of a spec node by walking the graph. This
// is a cheap operation on a brand-new graph; the cost is dominated by spec
// I/O, not the lookup itself.
func resolveNodeKind(fsys specio.FS, id string) (spec.NodeKind, error) {
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
	bugs, _ := collectObjects[spec.Bug](fsys, ".borg/spec/bugs")
	approaches, _ := collectMarkdown[spec.Approach](fsys, ".borg/spec/approaches")

	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		_ = json.Unmarshal(data, &traces)
	}

	g := spec.BuildGraph(features, bugs, decisions, strategies, approaches, traces)
	nodes := g.Nodes()
	n, ok := nodes[id]
	if !ok {
		return "", fmt.Errorf("unknown spec node %q", id)
	}
	return n.Kind, nil
}

// renderRefineDryRun prints the cascade blast radius without mutating spec.
// Delegates to the same graph code `diff` used; this absorbs that command's
// role under the new verb set.
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
