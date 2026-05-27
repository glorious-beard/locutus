// DJ-136 phase 5 — hook-validate-decision subcommand tests.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHookValidateDecision_RejectsMissingID — empty id fails the
// DJ-133 invariant (id is `dec-<axis>`).
func TestHookValidateDecision_RejectsMissingID(t *testing.T) {
	err := validateDecisionFromReader(strings.NewReader(`{"tool_input":{"id":"","axes":["foo"]}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decision id is required")
}

// TestHookValidateDecision_RejectsMissingDecPrefix — id lacking the
// `dec-` prefix is rejected even if axes is present.
func TestHookValidateDecision_RejectsMissingDecPrefix(t *testing.T) {
	err := validateDecisionFromReader(strings.NewReader(`{"tool_input":{"id":"oltp-store"}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must start with `dec-`")
}

// TestHookValidateDecision_RejectsEmptyAxisSuffix — `dec-` alone is
// rejected; the axis suffix must be non-empty.
func TestHookValidateDecision_RejectsEmptyAxisSuffix(t *testing.T) {
	err := validateDecisionFromReader(strings.NewReader(`{"tool_input":{"id":"dec-"}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty axis suffix")
}

// TestHookValidateDecision_RejectsAxisMismatch — when axes is set
// explicitly, axes[0] must match the id suffix.
func TestHookValidateDecision_RejectsAxisMismatch(t *testing.T) {
	err := validateDecisionFromReader(strings.NewReader(`{"tool_input":{"id":"dec-oltp-store","axes":["different-axis"]}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

// TestHookValidateDecision_AllowsValidDecision — well-formed id +
// matching axis passes.
func TestHookValidateDecision_AllowsValidDecision(t *testing.T) {
	err := validateDecisionFromReader(strings.NewReader(`{"tool_input":{"id":"dec-oltp-store","axes":["oltp-store"]}}`))
	require.NoError(t, err)
}

// TestHookValidateDecision_AllowsBackfillPath — id with `dec-` prefix
// and no axes field passes (the MCP write tool backfills axes from
// the id; the hook permits this convergence-by-construction path).
func TestHookValidateDecision_AllowsBackfillPath(t *testing.T) {
	err := validateDecisionFromReader(strings.NewReader(`{"tool_input":{"id":"dec-oltp-store"}}`))
	require.NoError(t, err)
}

// TestHookValidateDecision_AcceptsTopLevelInput — runtimes that
// pass the input at the top level (no wrapper) are handled by the
// fallback unmarshal.
func TestHookValidateDecision_AcceptsTopLevelInput(t *testing.T) {
	err := validateDecisionFromReader(strings.NewReader(`{"id":"dec-oltp-store"}`))
	require.NoError(t, err)
}

// TestHookValidateDecision_AcceptsCamelCaseWrapper — Gemini's
// BeforeTool uses `toolInput` (camelCase). The wrapper handles both
// `tool_input` and `toolInput`.
func TestHookValidateDecision_AcceptsCamelCaseWrapper(t *testing.T) {
	err := validateDecisionFromReader(strings.NewReader(`{"toolInput":{"id":"dec-foo"}}`))
	require.NoError(t, err)
}

// TestHookValidateDecision_RejectsEmptyStdin — an empty input is a
// programming error in the hook config; reject loudly so the
// operator notices.
func TestHookValidateDecision_RejectsEmptyStdin(t *testing.T) {
	err := validateDecisionFromReader(bytes.NewReader(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty input")
}
