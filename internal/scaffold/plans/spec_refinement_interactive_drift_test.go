// DJ-142 phase 3 — drift guard for the duplicated per-iteration core.
//
// The interactive self-loop variant spec_refinement.interactive.md
// duplicates the per-iteration WORK body of the headless canonical
// spec_refinement.md rather than composing it at load time (DJ-142
// resolved-question 4). The two playbooks diverge only at the opening
// (the interactive file adds a self-loop preamble driven by the
// spec_loop_* tools) and the closing tail (interactive reports the
// verdict THROUGH spec_advance_iteration; headless emits a trailing
// verdict line for the harness).
//
// The shared region is bracketed by sentinel HTML comments in both
// files. This test extracts the bracketed bytes from each and asserts
// they are byte-identical — the editor's contract is: edit the core in
// one file, copy it verbatim into the other, run this test.

package plans_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	coreBeginSentinel = "<!-- BEGIN per-iteration-core -->"
	coreEndSentinel   = "<!-- END per-iteration-core -->"
)

// extractCore returns the bytes strictly between the single BEGIN and
// END per-iteration-core sentinels in body. It fails the test if the
// file does not contain exactly one BEGIN and exactly one END.
func extractCore(t *testing.T, file string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	require.NoErrorf(t, err, "read %s", file)
	body := string(raw)

	require.Equalf(t, 1, strings.Count(body, coreBeginSentinel),
		"%s must contain exactly one %q sentinel", file, coreBeginSentinel)
	require.Equalf(t, 1, strings.Count(body, coreEndSentinel),
		"%s must contain exactly one %q sentinel", file, coreEndSentinel)

	begin := strings.Index(body, coreBeginSentinel) + len(coreBeginSentinel)
	end := strings.Index(body, coreEndSentinel)
	require.Greaterf(t, end, begin,
		"%s: END sentinel must follow BEGIN sentinel", file)

	return body[begin:end]
}

// TestSpecRefinementInteractive_CoreMatchesCanonical — the per-iteration
// core bracketed in spec_refinement.interactive.md is byte-identical to
// the bracketed core in spec_refinement.md. This is the drift guard: the
// duplication is deliberate, and any edit to one file's core must be
// mirrored into the other verbatim.
func TestSpecRefinementInteractive_CoreMatchesCanonical(t *testing.T) {
	const (
		canonical   = "spec_refinement.md"
		interactive = "spec_refinement.interactive.md"
	)
	canonicalCore := extractCore(t, canonical)
	interactiveCore := extractCore(t, interactive)

	assert.Equalf(t, canonicalCore, interactiveCore,
		"per-iteration-core has drifted between %s and %s — the bracketed regions must be byte-identical; "+
			"edit the core in one file and copy it verbatim into the other (this duplication is intentional per DJ-142)",
		canonical, interactive)
}

// TestSpecRefinementInteractive_ReferencesLoopTools — the interactive
// variant drives its own convergence loop via the spec_loop_* MCP tools
// added in DJ-142 phase 2.
func TestSpecRefinementInteractive_ReferencesLoopTools(t *testing.T) {
	raw, err := os.ReadFile("spec_refinement.interactive.md")
	require.NoError(t, err)
	body := string(raw)

	assert.Contains(t, body, "spec_loop_begin",
		"interactive variant must call spec_loop_begin to allocate or recover the run")
	assert.Contains(t, body, "spec_advance_iteration",
		"interactive variant must report the verdict through spec_advance_iteration to drive its loop")
}

// TestSpecRefinementInteractive_HasCompressionRecoveryNote — the
// interactive variant tells the agent how to resume after losing track
// mid-run (e.g. context compaction): re-call spec_loop_begin with the
// same (activity, target) to recover the current iteration.
func TestSpecRefinementInteractive_HasCompressionRecoveryNote(t *testing.T) {
	raw, err := os.ReadFile("spec_refinement.interactive.md")
	require.NoError(t, err)
	body := string(raw)

	assert.Contains(t, body, "lose track",
		"interactive variant must describe the compression-recovery affordance")
	assert.Contains(t, body, "re-call `mcp__locutus__spec_loop_begin`",
		"interactive variant must instruct re-calling spec_loop_begin to resume rather than restart")
}
