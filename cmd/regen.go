package cmd

import (
	"context"
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// RegenCmd regenerates stale modules.
type RegenCmd struct{}

func (c *RegenCmd) Run(cli *CLI) error {
	return fmt.Errorf("LLM not configured — regen requires an LLM provider")
}

// RunRegen identifies stale modules via the spec graph and dispatches agents
// to regenerate them. A module is stale when its governing strategy or upstream
// decisions have changed since the last generation.
func RunRegen(ctx context.Context, llm agent.LLM, fsys specio.FS) (*spec.MasterPlan, error) {
	// Load current spec state.
	features, _ := collectObjects[spec.Feature](fsys, ".borg/spec/features")
	decisions, _ := collectObjects[spec.Decision](fsys, ".borg/spec/decisions")
	strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")

	req := agent.PlanRequest{
		Prompt:     "Regenerate stale modules based on current spec state",
		Features:   features,
		Decisions:  decisions,
		Strategies: strategies,
	}

	// Read GOALS.md if present.
	if data, err := fsys.ReadFile("GOALS.md"); err == nil {
		req.GoalsBody = string(data)
	}

	return agent.Plan(ctx, llm, fsys, req)
}
