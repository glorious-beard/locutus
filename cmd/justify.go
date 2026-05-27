package cmd

import (
	"context"
	"fmt"
	"strings"
)

// JustifyCmd implements `locutus justify <id> [--against "..."] [--format markdown|json]`.
//
// Dispatches the `justification` activity to a coding-agent runtime
// via ACP. The runtime executes the published justification playbook,
// which fetches the target node + its dependency-graph context via
// `mcp__locutus__spec_get`, optionally dispatches `spec-challenger`
// (when --against was set) and `justify-researcher` (for grounded
// fact-checking), then dispatches `spec-advocate` to produce a
// structured defense. The output lands on stdout in the operator-
// chosen format (markdown default; json opt-in for downstream
// tooling).
//
// The verb is read-only — no spec graph mutations, no hooks. One
// dispatch, no outer loop. Errors (missing target id) surface as
// a structured error envelope in the requested format.
//
// Restores the verb DJ-135 phase 5 retired, under the post-DJ-135
// activity-dispatch architecture (commits 915a784 + 0731747 retired
// the council-side Go dispatchers but left the agent prompts in
// place; this verb gives them a playbook to invoke). See DJ-137 for
// the design rationale and the JSON output schema.
type JustifyCmd struct {
	ID      string `arg:"" required:"" help:"Spec node id to justify (e.g. dec-primary-datastore, feat-voter-universe, strat-multitenant-isolation)."`
	Against string `help:"Challenge prompt for adversarial dialogue. When set, spec-challenger writes a structured critique first and spec-advocate responds to it."`
	Format  string `help:"Output format: markdown (default) or json." default:"markdown" enum:"markdown,json"`
}

func (c *JustifyCmd) Run(ctx context.Context, cli *CLI) error {
	return runActivityVerb(ctx, cli, "justification", c.contextNote())
}

// contextNote builds the Run-context block the activity verb
// appends to the playbook body. The playbook reads `Target node:`,
// `Output format:`, and optional `Challenge from user:` lines from
// this context to drive its dispatch (which subagents to run, what
// shape to emit). The structure mirrors the format the playbook
// documents in its "Inputs" section verbatim — drift between this
// builder and the playbook would mean the orchestrator misses input
// lines.
func (c *JustifyCmd) contextNote() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Target node: %s\n", c.ID)
	fmt.Fprintf(&b, "Output format: %s\n", c.Format)
	if c.Against != "" {
		fmt.Fprintf(&b, "\nChallenge from user (for adversarial dialogue):\n%s\n", c.Against)
	}
	return b.String()
}
