// DJ-137 phase 2 — assertions on the justification playbook shape.
// The playbook drives a one-shot read-only activity that dispatches
// spec-advocate (and optionally spec-challenger + justify-researcher)
// to produce a structured defense of a named spec node. Tests guard
// the load-bearing directives:
//
//   - opening TodoWrite plan directive (DJ-136 phase 3 convention),
//   - hyphenated subagent ids,
//   - explicit reference to mcp__locutus__spec_get for node + linked
//     context fetches,
//   - dependency-graph traversal instruction with full-content fetch
//     (rationale + alternatives, not just titles),
//   - both markdown and JSON output formats documented.

package plans_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const justificationFile = "justification.md"

func loadJustification(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(justificationFile)
	require.NoError(t, err)
	return string(b)
}

// TestPlaybookJustification_HasPlanToolDirective — the playbook
// opens with a TodoWrite directive (DJ-136 phase 3 convention) so
// the runner's EventPlan rendering (DJ-136 phase 2) surfaces the
// orchestrator's plan inline as the activity executes.
func TestPlaybookJustification_HasPlanToolDirective(t *testing.T) {
	body := loadJustification(t)
	assert.Contains(t, body, "TodoWrite",
		"playbook must name TodoWrite as the plan tool the orchestrator should call")
	assert.Contains(t, strings.ToLower(body), "plan tool",
		"playbook must describe the affordance generically so non-Claude runtimes can substitute their equivalent")
}

// TestPlaybookJustification_ReferencesRequiredAgents — the playbook
// names the three subagents it may dispatch by their hyphenated ids
// (matching the published agent filenames).
func TestPlaybookJustification_ReferencesRequiredAgents(t *testing.T) {
	body := loadJustification(t)
	for _, agent := range []string{"spec-advocate", "spec-challenger", "justify-researcher"} {
		assert.Contains(t, body, agent,
			"playbook must reference %s by its hyphenated agent id", agent)
	}
}

// TestPlaybookJustification_ReferencesSpecGetTool — the playbook
// uses mcp__locutus__spec_get for both the target fetch and the
// linked-context fetch. Catches accidental drift toward reading
// .borg/spec/ files instead.
func TestPlaybookJustification_ReferencesSpecGetTool(t *testing.T) {
	body := loadJustification(t)
	assert.Contains(t, body, "mcp__locutus__spec_get",
		"playbook must use the MCP tool for spec node access")
	// At least two distinct references — one for the target fetch
	// (Step 1) and one for the batched linked-context fetch (Step 2).
	count := strings.Count(body, "mcp__locutus__spec_get")
	assert.GreaterOrEqual(t, count, 2,
		"playbook should reference spec_get at least twice (target fetch + batched linked fetch); got %d", count)
}

// TestPlaybookJustification_RequiresDependencyTraversal — the
// playbook explicitly instructs the orchestrator to fetch linked
// upstream nodes with full content (rationale + alternatives),
// not just titles. This is the load-bearing context expansion
// DJ-137's resolved-question 3 names.
func TestPlaybookJustification_RequiresDependencyTraversal(t *testing.T) {
	body := loadJustification(t)
	assert.Contains(t, body, "batched",
		"playbook must instruct a batched spec_get for linked-context efficiency")
	assert.Contains(t, body, "rationale",
		"playbook must name rationale as part of the full-content fetch")
	assert.Contains(t, body, "alternatives",
		"playbook must name alternatives as part of the full-content fetch")
	assert.Contains(t, strings.ToLower(body), "full struct",
		"playbook must call out 'full struct' (not just titles) for upstream nodes")
}

// TestPlaybookJustification_DocumentsBothFormats — the playbook
// covers both the markdown default and the JSON branch. Each format
// has its own emit-shape section so the orchestrator picks one
// based on the Run context's Output format line.
func TestPlaybookJustification_DocumentsBothFormats(t *testing.T) {
	body := loadJustification(t)
	assert.Contains(t, body, "Output format: markdown",
		"playbook must document the markdown format input line")
	assert.Contains(t, body, "Output format: json",
		"playbook must document the json format input line")
	// Both branches must show their emit shape.
	assert.Contains(t, body, "## Defense",
		"markdown format must show the Defense heading")
	assert.Contains(t, body, `"adversarial"`,
		"json format must show the adversarial key in the schema")
}

// TestPlaybookJustification_DocumentsErrorEnvelope — when the
// target id is missing, the playbook tells the orchestrator to
// emit a structured error in BOTH formats. Without this guard a
// missing-id run might emit partial JSON or a bare error message
// that downstream tooling can't parse.
func TestPlaybookJustification_DocumentsErrorEnvelope(t *testing.T) {
	body := loadJustification(t)
	assert.Contains(t, body, "node_not_found",
		"JSON error envelope must use a stable error code")
	// Markdown error path mentions the missing node.
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "not found",
		"markdown error path must name the not-found case")
}

// TestPlaybookJustification_OneShotShape — the playbook is one-shot
// and read-only. No loop-framing prose (DJ-136 retired those phrases
// from spec_refinement; same applies here from inception). Under
// DJ-140 the harness runs its outer loop universally, so the one-shot
// playbook now MUST emit `converged: true` once to terminate the loop
// cleanly after a single iteration — that terminal verdict is asserted
// separately in TestDispatchedPlaybooksEmitConvergedVerdict.
func TestPlaybookJustification_OneShotShape(t *testing.T) {
	body := loadJustification(t)
	lower := strings.ToLower(body)
	retired := []string{
		"loop until",
		"run iterations until",
		"iteration cap",
	}
	for _, phrase := range retired {
		assert.NotContains(t, lower, phrase,
			"justification is one-shot; retired phrase %q must not appear", phrase)
	}
	assert.Contains(t, body, "Stop",
		"playbook must include a Stop directive — the activity does not loop")
	// DJ-140: the single-pass playbook emits the terminal verdict so
	// the universal outer loop exits after one iteration.
	assert.Contains(t, lower, "converged: true",
		"under DJ-140 the one-shot playbook must emit `converged: true` to terminate the universal outer loop after one iteration")
}
