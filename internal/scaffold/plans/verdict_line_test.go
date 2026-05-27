// DJ-140 phase 5a — universal outer-loop convergence guard.
//
// DJ-140 made the harness outer loop universal across all runtimes
// (previously claude-code single-dispatched). Consequence: EVERY
// activity playbook dispatched headlessly through runActivityVerb must
// end with a `converged:` verdict line, or the loop re-dispatches it
// uselessly until the max-iteration cap fires.
//
// This guard asserts that every base activity playbook that is
// dispatched through the outer loop contains the `converged:` verdict
// token, so the regression can't silently return. The verdict token
// matches what internal/runner.IsConverged detects
// (`(?im)^\s*converged:\s*true\b`).
//
// Provider+mode overlays (e.g. spec_refinement.claude-code.interactive.md)
// are NOT outer-loop-dispatched — they're published as interactive
// slash commands — so only the base <activity>.md files are checked.

package plans_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dispatchedActivities are the activity playbook base names that
// runActivityVerb dispatches through the outer loop. These match the
// rows in internal/activity/agents-default.yaml.
var dispatchedActivities = []string{
	"spec_refinement",
	"feature_ingestion",
	"code_adoption",
	"code_assimilation",
	"justification",
	"spec_bias",
}

// TestDispatchedPlaybooksEmitConvergedVerdict — every base activity
// playbook dispatched through the outer loop must carry a `converged:`
// verdict token. Overlay files (<activity>.<runtime>.md and
// <activity>.<runtime>.<mode>.md) are excluded: only the base
// <activity>.md is read, since the .interactive.md overlay is
// published as a slash command and never outer-loop-dispatched.
func TestDispatchedPlaybooksEmitConvergedVerdict(t *testing.T) {
	for _, activity := range dispatchedActivities {
		path := activity + ".md"
		body, err := os.ReadFile(path)
		require.NoErrorf(t, err, "reading base playbook %q", path)
		assert.Containsf(t, string(body), "converged:",
			"playbook %q lacks a `converged:` verdict line — the universal outer loop (DJ-140) will re-dispatch it to the max-iteration cap; add a convergence-verdict section",
			path)
	}
}
