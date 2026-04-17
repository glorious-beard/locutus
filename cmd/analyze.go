package cmd

import (
	"context"
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/specio"
)

// AnalyzeCmd analyzes an existing codebase (assimilation).
type AnalyzeCmd struct{}

func (c *AnalyzeCmd) Run(cli *CLI) error {
	// LLM provider not yet wired up; the agent.Analyze function
	// is fully implemented and tested via the agent package.
	return fmt.Errorf("LLM not configured — analyze requires an LLM provider")
}

// RunAnalyze executes the assimilation analysis pipeline. It walks the file
// inventory, builds an AssimilationRequest, and delegates to agent.Analyze.
func RunAnalyze(ctx context.Context, llm agent.LLM, fsys specio.FS) (*agent.AssimilationResult, error) {
	// 1. Walk inventory.
	inventory, err := agent.WalkInventory(fsys)
	if err != nil {
		return nil, fmt.Errorf("walking inventory: %w", err)
	}

	// 2. Create AssimilationRequest with the inventory.
	req := agent.AssimilationRequest{Inventory: inventory}

	// 3. Run the assimilation analysis pipeline.
	result, err := agent.Analyze(ctx, llm, fsys, req)
	if err != nil {
		return nil, fmt.Errorf("assimilation analysis: %w", err)
	}

	return result, nil
}
