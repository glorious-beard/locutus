// DJ-138 phase 5 — spec_bias playbook shape tests. Asserts the
// playbook is loadable, references the MCP tools the cascade
// uses, names the canonical subagents, and carries the
// re-invocation skip-discipline note that makes idempotent
// re-runs the contract documented in DJ-138 failure model §10.

package plans_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readPlaybook(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	require.NoError(t, err)
	require.NotEmpty(t, body, "%s must be non-empty", name)
	return string(body)
}

// TestSpecBiasPlaybook_LoadsAndIsNonEmpty — sanity: the
// playbook file ships and embeds.
func TestSpecBiasPlaybook_LoadsAndIsNonEmpty(t *testing.T) {
	body := readPlaybook(t, "spec_bias.md")
	assert.Greater(t, len(body), 1000, "spec_bias playbook should be a substantive document, not a stub")
}

// TestSpecBiasPlaybook_ReferencesMCPWriteTools — the cascade is
// a write-cascade; the playbook must reference each write tool
// the cascade exercises so the orchestrator finds them.
func TestSpecBiasPlaybook_ReferencesMCPWriteTools(t *testing.T) {
	body := readPlaybook(t, "spec_bias.md")
	required := []string{
		"mcp__locutus__spec_revise_decision",
		"mcp__locutus__spec_revise_feature",
		"mcp__locutus__spec_revise_strategy",
		"mcp__locutus__spec_propose_decision",
		"mcp__locutus__spec_mark_approach_drifted",
	}
	for _, tool := range required {
		assert.Containsf(t, body, tool,
			"spec_bias playbook must reference %s — the cascade exercises this write tool", tool)
	}
}

// TestSpecBiasPlaybook_ReferencesMCPReadTools — the cascade
// reads the spec graph extensively; the playbook must name the
// read tools so the orchestrator has the navigation surface.
func TestSpecBiasPlaybook_ReferencesMCPReadTools(t *testing.T) {
	body := readPlaybook(t, "spec_bias.md")
	for _, tool := range []string{
		"mcp__locutus__spec_get",
		"mcp__locutus__spec_list_manifest",
		"mcp__locutus__spec_search",
	} {
		assert.Containsf(t, body, tool, "spec_bias playbook must reference %s", tool)
	}
}

// TestSpecBiasPlaybook_ReferencesTodoWrite — DJ-136 convention:
// playbooks open with TodoWrite so the operator sees scheduled
// work via the runner's plan-event rendering.
func TestSpecBiasPlaybook_ReferencesTodoWrite(t *testing.T) {
	body := readPlaybook(t, "spec_bias.md")
	assert.Contains(t, body, "TodoWrite",
		"playbook should request the runtime's plan tool early per DJ-136 convention")
}

// TestSpecBiasPlaybook_DocumentsClosureWalk — the closure-walk
// algorithm is the cascade's load-bearing bound; the playbook
// must spell it out explicitly so the orchestrator doesn't
// improvise.
func TestSpecBiasPlaybook_DocumentsClosureWalk(t *testing.T) {
	body := readPlaybook(t, "spec_bias.md")
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "parent_id",
		"closure-walk must reference parent_id for approach-level drift detection")
	assert.Contains(t, lower, "decisions[]",
		"closure-walk must reference decisions[] for the citing-feature/strategy/approach scan")
}

// TestSpecBiasPlaybook_DocumentsConvergenceVerdict — DJ-136
// convention: the playbook ends with the canonical verdict line
// (`converged: true` or `converged: false; <reason>`) that the
// outer-loop runner reads.
func TestSpecBiasPlaybook_DocumentsConvergenceVerdict(t *testing.T) {
	body := readPlaybook(t, "spec_bias.md")
	assert.Contains(t, body, "converged: true")
	assert.Contains(t, body, "converged: false")
}

// TestSpecBiasPlaybook_DocumentsReInvocationSkip — DJ-138 §10
// failure model: the playbook MUST tell the orchestrator to
// skip nodes whose state already reflects the bias on
// re-invocation. Idempotent re-run is the contract that makes
// git-as-rollback a credible layer for partial-cascade
// recovery.
func TestSpecBiasPlaybook_DocumentsReInvocationSkip(t *testing.T) {
	body := readPlaybook(t, "spec_bias.md")
	lower := strings.ToLower(body)
	// Look for the skip-discipline framing — phrasing latitude
	// here (the prose can evolve) but the concept must be
	// present.
	assert.True(t,
		strings.Contains(lower, "already reflects the bias") ||
			strings.Contains(lower, "skip them") ||
			strings.Contains(lower, "idempotent"),
		"spec_bias playbook must document the re-invocation skip discipline for idempotent re-runs")
}

// TestSpecBiasPlaybook_RejectsRetiredFlagProse — defensive: the
// playbook must not mention the retired --brief / --supersede
// flag names. DJ-135 phase 5 retired them; DJ-138 subsumes
// them under --with. Mention in playbook prose would prime the
// orchestrator on a vocabulary that doesn't exist in the
// runtime.
func TestSpecBiasPlaybook_RejectsRetiredFlagProse(t *testing.T) {
	body := readPlaybook(t, "spec_bias.md")
	lower := strings.ToLower(body)
	for _, retired := range []string{"--brief", "--supersede", "--rollback", "--diff"} {
		assert.NotContains(t, lower, retired,
			"spec_bias playbook must not mention retired flag %q — DJ-138 subsumes them under --with", retired)
	}
}
