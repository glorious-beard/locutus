package cmd

import (
	"context"
)

// AssimilateCmd dispatches the code_assimilation activity. Per
// DJ-135 phase 5 the verb forks a coding-agent runtime and hands it
// the code_assimilation playbook. The agent reads the existing
// codebase, infers entities + decisions that explain the current
// architecture, and proposes features that name user-visible
// capabilities via the MCP spec_propose_* tools.
//
// Flags from the legacy verb (--dry-run) dropped — re-runnable
// against an in-flight spec graph is the operational model, so a
// dedicated dry-run isn't meaningful.
type AssimilateCmd struct {
	DryRun bool   `name:"dry-run" help:"Capture proposed mutations without writing them; print what would land."`
	Format string `help:"Report format when --dry-run is set." enum:"markdown,json" default:"markdown"`
}

func (c *AssimilateCmd) Run(ctx context.Context, cli *CLI) error {
	return runActivityVerb(ctx, cli, "code_assimilation", "", c.DryRun, c.Format)
}
