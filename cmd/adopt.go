package cmd

import (
	"context"
	"fmt"
)

// AdoptCmd dispatches the code_adoption activity. Per DJ-135 phase 5
// the verb forks a coding-agent runtime and hands it the
// code_adoption playbook. The agent classifies every Approach in the
// spec graph against the current codebase, surfaces drift, and
// proposes remediation by calling back into Locutus's MCP tools.
//
// Flags from the legacy verb (--scope, --dry-run) dropped in this
// rewrite. Scope semantics are encoded in the playbook prose; if
// runtime selection of subsets is needed, the playbook agent reads
// the supervisor's --scope as a run-context note (added in a
// follow-up if needed).
type AdoptCmd struct {
	Scope  string `help:"Optional scope hint passed to the playbook agent as run context. Free-form (e.g. an Approach id or filesystem path)."`
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
