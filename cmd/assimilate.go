package cmd

import (
	"context"
)

// AssimilateCmd dispatches the code_assimilation activity (DJ-148).
// The activity reads brownfield source code, infers/revises
// features+decisions+strategies (code-is-truth direction),
// synthesizes approaches binding inferred specs to source files
// with source_hash, and produces coherent (spec, code, approach)
// state.
//
// Preconditions checked by the playbook in Step 0 (refused with a
// helpful error when missing):
//   - GOALS.md exists in the working tree
//   - Goal layer is populated (operator ran `locutus refine goals` first)
//
// The --dry-run + --format flags inherit from DJ-147; mutations
// capture in the per-session overlay via the captureOnly wrapper
// at the MCP tool boundary.
type AssimilateCmd struct {
	DryRun bool   `name:"dry-run" help:"Capture proposed mutations without writing them; print what would land."`
	Format string `help:"Report format when --dry-run is set." enum:"markdown,json" default:"markdown"`
}

func (c *AssimilateCmd) Run(ctx context.Context, cli *CLI) error {
	return runActivityVerb(ctx, cli, "code_assimilation", "", c.DryRun, c.Format)
}
