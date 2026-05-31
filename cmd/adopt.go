package cmd

import (
	"context"
	"fmt"
)

// AdoptCmd dispatches the code_adoption activity (DJ-149).
// The activity reads the spec graph and the state store, identifies
// which approaches need work (missing for feat/strat in scope /
// unbound / spec-drifted / code-drifted / orphan-parent), synthesizes
// approach bodies for parents without one (per DJ-087 — refine builds
// the deliberation layer, adopt owns approach synthesis), dispatches
// the coding-agent runtime to implement in stacked worktrees
// (adopt/<NNN>-<approach-id> branches with phase-N+1-branches-off-N),
// and records reconciliation outcomes — including test-asserted
// live/failed status per DJ-068's honest-state principle — in
// .borg/state/.
//
// The runtime decides parallelism, branch ordering, and worktree
// management (per DJ-144's trajectory of trusting the runtime).
// Locutus writes plan files to .locutus/sessions/<sid>/plans/; the
// runtime reads and implements.
//
// Preconditions checked by the playbook in Step 0 (refused with a
// helpful error when missing): GOALS.md exists; goal layer is
// populated (operator ran `locutus refine goals` first); at least
// one approach OR one feat/strat without approach (otherwise nothing
// to adopt).
//
// Flags inherit from DJ-147: --dry-run + --format markdown|json;
// mutations capture via captureOnly at the MCP boundary; the
// playbook adds LOCUTUS_DRY_RUN guards around worktree creation +
// code generation.
type AdoptCmd struct {
	Scope  string `help:"Optional scope filter (approach id, parent feat-/strat- id, or directory prefix). Default: all approaches needing work."`
	DryRun bool   `name:"dry-run" help:"Capture proposed mutations without writing them; print what would land."`
	Format string `help:"Report format when --dry-run is set." enum:"markdown,json" default:"markdown"`
}

func (c *AdoptCmd) Run(ctx context.Context, cli *CLI) error {
	contextNote := ""
	if c.Scope != "" {
		contextNote = fmt.Sprintf("Focus this adoption run on scope %q.", c.Scope)
	}
	return runActivityVerb(ctx, cli, "code_adoption", contextNote, c.DryRun, c.Format)
}
