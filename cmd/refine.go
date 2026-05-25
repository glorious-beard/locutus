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
	Target string `arg:"" optional:"" help:"Spec node id to focus on (or 'goals' / omitted for whole-graph refinement). Passed through to the playbook agent as additional context."`
}

func (c *RefineCmd) Run(ctx context.Context, cli *CLI) error {
	return runActivityVerb(ctx, cli, "spec_refinement", c.contextNote())
}

// contextNote returns the per-invocation context appended to the
// playbook body before dispatch. Empty for whole-graph refinement;
// "Focus on spec node X." when a target is named.
func (c *RefineCmd) contextNote() string {
	if c.Target == "" || c.Target == "goals" {
		return ""
	}
	return fmt.Sprintf("Focus this refinement run on spec node %q.", c.Target)
}
