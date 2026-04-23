package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/specio"
)

// AssimilateCmd analyzes an existing codebase and produces a spec graph.
// Formerly named `analyze`; the CLI still accepts the old name via alias.
type AssimilateCmd struct {
	DryRun bool `help:"Run the pipeline but do not write spec files."`
}

func (c *AssimilateCmd) Run(cli *CLI) error {
	llm, err := getLLM()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	fsys := specio.NewOSFS(cwd)

	// Dry-run: wrap fsys so writes are discarded while reads still hit the
	// real filesystem. The pipeline runs to completion and we report what it
	// would have written.
	effective := specio.FS(fsys)
	if c.DryRun {
		effective = newReadOnlyFS(fsys)
	}

	result, err := RunAssimilate(context.Background(), llm, effective)
	if err != nil {
		return err
	}

	if cli.JSON {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	verb := "Assimilation complete"
	if c.DryRun {
		verb = "Assimilation preview (dry-run)"
	}
	fmt.Printf("%s: %d decisions, %d entities, %d features, %d gaps.\n",
		verb, len(result.Decisions), len(result.Entities), len(result.Features), len(result.Gaps))
	return nil
}

// RunAssimilate executes the assimilation analysis pipeline: walks the
// file inventory, loads the current spec snapshot so the LLM can
// distinguish new from existing nodes, delegates inference to
// agent.Analyze, and finally writes the inferred spec back to
// `.borg/spec/` via a per-file atomic pass. A crash-safety sentinel at
// `.borg/spec/.assimilating` is present for the duration of the write
// loop so a crashed prior run is visible to the next invocation.
//
// Dry-run is handled by the caller wrapping fsys with readOnlyFS — all
// writes (including the sentinel) are silently dropped by the wrapper,
// which preserves the preview semantics without extra branching here.
func RunAssimilate(ctx context.Context, llm agent.LLM, fsys specio.FS) (*agent.AssimilationResult, error) {
	inventory, err := agent.WalkInventory(fsys)
	if err != nil {
		return nil, fmt.Errorf("walking inventory: %w", err)
	}

	req := agent.AssimilationRequest{
		Inventory:    inventory,
		ExistingSpec: loadExistingSpec(fsys),
	}
	result, err := agent.Analyze(ctx, llm, fsys, req)
	if err != nil {
		return nil, fmt.Errorf("assimilation analysis: %w", err)
	}

	if err := persistAssimilationResult(fsys, result); err != nil {
		return result, fmt.Errorf("persist assimilation result: %w", err)
	}

	return result, nil
}

