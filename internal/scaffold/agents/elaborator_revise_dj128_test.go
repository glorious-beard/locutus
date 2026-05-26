// DJ-128 Phase 5 — assertions on the rewritten spec-decision-elaborator
// revise mode prompt.

package agents_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reviseModeSection returns the revise-mode subset of the
// spec-decision-elaborator prompt — from the "# Revise mode" heading
// to the next top-level "# " heading. Tests focus assertions against
// this section so first-author mode prose doesn't accidentally
// satisfy a revise-mode invariant.
func reviseModeSection(t *testing.T) string {
	t.Helper()
	body := loadPrompt(t, "spec-decision-elaborator.md")
	start := strings.Index(body, "# Revise mode")
	require.GreaterOrEqual(t, start, 0, "revise mode section must be present")
	rest := body[start+len("# Revise mode"):]
	// Find the next top-level heading.
	idx := strings.Index(rest, "\n# ")
	if idx < 0 {
		return rest
	}
	return rest[:idx]
}

// TestDecisionElaboratorReviseModeDescribesMergeSidePreservation —
// scaffolded prompt frames alternatives preservation as a
// merge-layer responsibility (post-fifth-winplan revision of DJ-128),
// not an elaborator-discipline mandate. The elaborator is told it
// doesn't need to enumerate every prior alternative — the merge
// layer preserves them mechanically. This frees the elaborator to
// focus on new content (chosen, new alternatives, rationale
// updates) without the structural-bookkeeping burden that
// previously caused balanced-tier models to oscillate during
// revisions.
func TestDecisionElaboratorReviseModeDescribesMergeSidePreservation(t *testing.T) {
	body := reviseModeSection(t)
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "merge layer preserves",
		"prompt must tell the elaborator that the merge layer handles preservation")
	assert.Contains(t, lower, "you don't need to enumerate every prior alternative",
		"prompt must explicitly relieve the elaborator of the enumeration burden")
	// Negative assertion: the old "alternatives strictly grow"
	// mandate must NOT remain — its presence would reintroduce
	// the structural-bookkeeping prompt burden the merge-side
	// preservation is designed to retire.
	assert.NotContains(t, lower, "alternatives strictly grow",
		"the old 'alternatives strictly grow' mandate must be retired now that the merge layer preserves them")
	assert.NotContains(t, lower, "rejected by the merge layer",
		"the old rejected-by-merge-layer threat must be retired (merge no longer rejects on alternative omission)")
}

// TestDecisionElaboratorReviseModeDescribesFlipAndReject — prompt
// names both the Flip and Reject patterns and ties each to the
// counterproposal-menu evaluation.
func TestDecisionElaboratorReviseModeDescribesFlipAndReject(t *testing.T) {
	body := reviseModeSection(t)
	assert.Contains(t, body, "**Flip.**", "prompt names the Flip pattern")
	assert.Contains(t, body, "**Reject.**", "prompt names the Reject pattern")
	// Both patterns name counterproposals.
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "counterproposal",
		"prompt's pattern descriptions tie to the counterproposal menu")
}

// TestDecisionElaboratorReviseModeRequiresCitationVerification —
// prompt instructs the elaborator to verify counterproposal
// citations (call spec_get / web fetch on cited references) before
// promoting a counterproposal to chosen.
func TestDecisionElaboratorReviseModeRequiresCitationVerification(t *testing.T) {
	body := reviseModeSection(t)
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "verify",
		"prompt must instruct the elaborator to verify citation grounding")
	verifyTool := strings.Contains(lower, "spec_get") ||
		strings.Contains(lower, "web search") ||
		strings.Contains(lower, "web citation")
	assert.True(t, verifyTool,
		"prompt must name the spec_get / web search tooling for verifying counterproposal citations")
}

// TestDecisionElaboratorReviseModeFoldsCriticArgumentVerbatim —
// prompt names the discipline that critic Arguments + Citations
// carry into alternatives verbatim (no paraphrase / no drop).
func TestDecisionElaboratorReviseModeFoldsCriticArgumentVerbatim(t *testing.T) {
	body := reviseModeSection(t)
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "verbatim",
		"prompt must name the verbatim-preservation discipline for critic argument / citations")
	// Specifically: critic's argument + citations carry into alternatives.
	hasArgFold := strings.Contains(lower, "critic's argument") ||
		strings.Contains(lower, "critic's `argument`")
	assert.True(t, hasArgFold,
		"prompt must reference the critic's argument as the source of an alternative's rationale")
}

// TestDecisionElaboratorReviseModeRetainsAxesPreservation — existing
// DJ-126 axis-preservation mandate survives the prompt rewrite.
func TestDecisionElaboratorReviseModeRetainsAxesPreservation(t *testing.T) {
	body := reviseModeSection(t)
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "axes",
		"axes-preservation mandate must survive the rewrite")
	assert.Contains(t, lower, "verbatim",
		"axes are copied character-for-character")
}
