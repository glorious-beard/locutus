package plans_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// playbookInvariant locks in the two defensive directives required of
// every spec-mutating playbook (DJ-144 follow-up): all spec mutations
// route through the MCP tools (never direct .borg/spec/ writes); and
// GOALS.md is read-only for playbooks that touch the goal layer.
//
// The matcher prompt (internal/scaffold/agents/spec-goal-diff-matcher.md)
// already has "you do not edit GOALS.md"; this test locks the parallel
// guardrail into the orchestrator playbooks so the coding agent at the
// outer scope honors the same contract.
type playbookInvariant struct {
	file       string
	hasGoalsMD bool // playbook touches GOALS.md → must contain the read-only directive
}

func TestPlaybookInvariants(t *testing.T) {
	cases := []playbookInvariant{
		{"spec_refinement.md", true},
		{"spec_refinement.claude-code.md", true},
		{"spec_refinement.interactive.md", true},
		{"feature_ingestion.md", true},
		{"feature_ingestion.claude-code.md", true},
		{"code_adoption.md", false},
		{"code_adoption.claude-code.md", false},
		{"spec_bias.md", false},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			body, err := os.ReadFile(tc.file)
			require.NoError(t, err, "open %s", tc.file)
			s := string(body)

			// Directive A — spec mutations via MCP tools, never direct .borg/spec/ writes.
			assert.Contains(t, s, "mcp__locutus__spec_*", "%s missing Directive A: MCP tool reference", tc.file)
			assert.Contains(t, s, ".borg/spec/", "%s missing Directive A: .borg/spec/ path reference", tc.file)
			assert.Contains(t, strings.ToLower(s), "never call", "%s missing Directive A: prohibition phrase", tc.file)

			if tc.hasGoalsMD {
				// Directive B — GOALS.md is read-only; mutations route through goal-layer tools.
				assert.Contains(t, s, "GOALS.md", "%s missing Directive B: GOALS.md reference", tc.file)
				assert.Contains(t, strings.ToLower(s), "read-only", "%s missing Directive B: read-only phrase", tc.file)
			}
		})
	}
}
