package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// RefineCmd runs council-driven deliberation on any spec node. It replaces
// the former `revisit` command (which was decision/strategy-only) with a
// node-kind-dispatching refiner that handles Goals, Features, Strategies,
// Decisions, Approaches, and Bugs.
//
// Phase A implements the Decision path end-to-end (what `revisit` did); the
// other kinds return a clear "not yet implemented for <kind>" message until
// Phase B/C wire them into the cascade and reconciler.
type RefineCmd struct {
	ID     string `arg:"" help:"Spec node ID to refine (Goal, Feature, Strategy, Decision, Approach, or Bug)."`
	DryRun bool   `help:"Preview cascade blast radius; do not write spec changes."`
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
		plan, err := RunRefine(context.Background(), llm, fsys, c.ID)
		if err != nil {
			return err
		}
		if cli.JSON {
			return json.NewEncoder(os.Stdout).Encode(plan)
		}
		fmt.Printf("Refined decision %s: %d workstreams planned.\n", c.ID, len(plan.Workstreams))
		return nil
	default:
		return fmt.Errorf("refine for %s is not yet implemented (Phase B/C)", kind)
	}
}

// RunRefine is the Decision-path refinement: consult the historian for prior
// alternatives, walk the spec graph for blast radius, and trigger council
// re-planning for the affected subgraph.
func RunRefine(ctx context.Context, llm agent.LLM, fsys specio.FS, targetID string) (*spec.MasterPlan, error) {
	hist := history.NewHistorian(fsys, ".borg/history")
	alternatives, err := hist.Alternatives(targetID)
	if err != nil {
		return nil, fmt.Errorf("consulting historian: %w", err)
	}

	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
	approaches, _ := collectMarkdown[spec.Approach](fsys, ".borg/spec/approaches")

	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		_ = json.Unmarshal(data, &traces)
	}

	g := spec.BuildGraph(features, nil, decisions, strategies, approaches, traces)
	br, err := spec.ComputeBlastRadius(g, targetID)
	if err != nil {
		return nil, fmt.Errorf("blast radius for %s: %w", targetID, err)
	}

	prompt := fmt.Sprintf(
		"Revisit decision/strategy %q. Blast radius: %d decisions, %d strategies, %d approaches affected.",
		targetID, len(br.Decisions), len(br.Strategies), len(br.Approaches),
	)
	if len(alternatives) > 0 {
		prompt += fmt.Sprintf(" Previously considered alternatives: %v.", alternatives)
	}

	req := agent.PlanRequest{
		Prompt:     prompt,
		Features:   features,
		Decisions:  decisions,
		Strategies: strategies,
	}
	if data, err := fsys.ReadFile("GOALS.md"); err == nil {
		req.GoalsBody = string(data)
	}

	return agent.Plan(ctx, llm, fsys, req)
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
