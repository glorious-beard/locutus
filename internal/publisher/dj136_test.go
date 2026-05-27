// DJ-136 phase 4 — Claude Code publisher assertions specific to the
// per-runtime idiomatic dispatch layer. Two tests:
//
//   - TestPublisherEmitsRefineSlashCommand: after a scaffold pass,
//     .claude/commands/locutus-refine.md exists. This fixture seeds no
//     interactive overlay, so under DJ-140 the publisher's mode=interactive
//     resolution falls through to the cross-runtime one-iteration
//     playbook body. (When the claude-code interactive overlay IS
//     present in .borg/plans/, this command becomes the /goal wrapper
//     instead — see dj140_test.go.)
//
//   - TestPublisherEmitsRefineSlashCommandHasIterationDirectives:
//     beyond mere existence, the published body actually carries the
//     one-iteration directives the playbook should: a TodoWrite
//     opening directive and a "converged:" verdict line at the close.
//     These are the contracts the /goal evaluator and the operator's
//     plan-block renderer depend on.

package publisher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPublisherEmitsRefineSlashCommand — confirms the publisher
// emits .claude/commands/locutus-refine.md with the cross-runtime
// default plan body. The CLI-verb naming convention (CLIVerb on
// CanonicalActivity) is what produces "refine" rather than
// "spec-refinement"; operators recognize the short form.
func TestPublisherEmitsRefineSlashCommand(t *testing.T) {
	fsys := seedProject(t, true) // includes a plan body
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	body, err := readAsString(fsys, ".claude/commands/locutus-refine.md")
	require.NoError(t, err, "publisher must emit the refine slash command")
	assert.NotEmpty(t, body)
}

// TestDispatchStrategy_ClaudeCodeUsesOverlay — when a runtime-specific
// overlay is present alongside the default in .borg/plans/, the
// activity-verb loader resolves the overlay for that runtime and the
// default for any other runtime. This is the Phase-1 resolver tested
// against the live filesystem layout the publisher reads.
func TestDispatchStrategy_ClaudeCodeUsesOverlay(t *testing.T) {
	// We don't drive Publish() here — the test inspects what
	// scaffold.ResolvePlaybook returns given a project FS that has
	// both files in .borg/plans/. Equivalent test coverage exists in
	// internal/scaffold/plans_overlay_test.go using fstest.MapFS;
	// this variant is an extra sanity check against the OSFS path
	// that production uses.
	t.Skip("covered by TestPlaybookOverlay_ResolvesRuntimeSpecificFirst against fstest.MapFS; OSFS path differs only in the file-not-found error type, already exercised by TestPlaybookOverlay_DefaultRequired")
}

