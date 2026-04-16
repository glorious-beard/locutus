package cmd

import (
	"context"
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/specio"
)

// AnalyzeCmd analyzes an existing codebase (brownfield).
type AnalyzeCmd struct{}

func (c *AnalyzeCmd) Run(cli *CLI) error {
	// LLM provider not yet wired up; the agent.Analyze function
	// is fully implemented and tested via the agent package.
	return fmt.Errorf("LLM not configured — analyze requires an LLM provider")
}

// RunAnalyze executes the brownfield analysis pipeline. It walks the file
// inventory, builds a BrownfieldRequest, and delegates to agent.Analyze.
func RunAnalyze(ctx context.Context, llm agent.LLM, fsys specio.FS) (*agent.BrownfieldResult, error) {
	// 1. Walk inventory.
	inventory, err := agent.WalkInventory(fsys)
	if err != nil {
		return nil, fmt.Errorf("walking inventory: %w", err)
	}

	// 2. Create BrownfieldRequest with the inventory.
	req := agent.BrownfieldRequest{Inventory: inventory}

	// 3. Run the brownfield analysis pipeline.
	result, err := agent.Analyze(ctx, llm, fsys, req)
	if err != nil {
		return nil, fmt.Errorf("brownfield analysis: %w", err)
	}

	return result, nil
}
