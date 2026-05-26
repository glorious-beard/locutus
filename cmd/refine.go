package cmd

import (
	"context"
	"fmt"
)

// RefineCmd dispatches the spec_refinement activity. Per DJ-135
// phase 5 the verb no longer drives the Go council in-process —
// instead it forks a coding-agent runtime (Claude Code / Codex /
// Gemini, picked by activity.Registry.Resolve) and hands it the
// spec_refinement playbook from .borg/plans/. The agent calls back
// into Locutus's MCP server (spec_* tools, spec://manifest resource)
// to mutate the graph.
//
// Flags from the legacy verb (--brief, --supersede, --diff, --rollback)
// dropped in this rewrite. Each was tied to council orchestration; if
// the new model needs equivalent semantics they land in a follow-up.
type RefineCmd struct {
	Target string `arg:"" optional:"" default:"goals" help:"Spec node id to focus on. Defaults to 'goals' (the root node — refines the whole graph against GOALS.md). Pass a specific id (e.g. 'dec-oltp-store') to scope the run to that subtree."`
}

func (c *RefineCmd) Run(ctx context.Context, cli *CLI) error {
	return runActivityVerb(ctx, cli, "spec_refinement", c.contextNote())
}

// contextNote returns the per-invocation context appended to the
// playbook body before dispatch. Refine always has a target — the
// default is "goals" (the root node), and a specific spec id scopes
// the run to that node's subtree. The Target field is populated to
// "goals" by Kong's default tag when the arg is omitted.
func (c *RefineCmd) contextNote() string {
	target := c.Target
	if target == "" {
		target = "goals"
	}
	return fmt.Sprintf("Target: %s", target)
}
