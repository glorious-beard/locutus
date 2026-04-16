package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// RevisitCmd updates a decision or strategy.
type RevisitCmd struct {
	ID string `arg:"" help:"Decision or strategy ID to revisit."`
}

func (c *RevisitCmd) Run(cli *CLI) error {
	return fmt.Errorf("LLM not configured — revisit requires an LLM provider")
}

// RunRevisit consults the historian for prior alternatives, walks the spec
// graph for blast radius, and triggers re-planning for the affected subgraph.
func RunRevisit(ctx context.Context, llm agent.LLM, fsys specio.FS, targetID string) (*spec.MasterPlan, error) {
	// Load historian to check prior alternatives.
	hist := history.NewHistorian(fsys, ".borg/history")
	alternatives, err := hist.Alternatives(targetID)
	if err != nil {
		return nil, fmt.Errorf("consulting historian: %w", err)
	}

	// Load spec state.
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")

	// Compute blast radius for the target.
	var traces spec.TraceabilityIndex
	if data, err := fsys.ReadFile(".borg/spec/traces.json"); err == nil {
		_ = json.Unmarshal(data, &traces)
	}

	g := spec.BuildGraph(features, nil, decisions, strategies, traces)
	br, err := spec.ComputeBlastRadius(g, targetID)
	if err != nil {
		return nil, fmt.Errorf("blast radius for %s: %w", targetID, err)
	}

	// Build a revisit prompt with context.
	prompt := fmt.Sprintf(
		"Revisit decision/strategy %q. Blast radius: %d decisions, %d strategies, %d files affected.",
		targetID, len(br.Decisions), len(br.Strategies), len(br.Files),
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
