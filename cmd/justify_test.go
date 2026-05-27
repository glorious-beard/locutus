// DJ-137 phase 4 — assertions on the JustifyCmd parser shape and
// the context-note string the verb builds. Doesn't actually
// dispatch ACP — that's the empirical-validation surface, deferred
// to a real-runtime session. These tests guard the CLI surface and
// the playbook-input contract (Target / Output format / Challenge
// lines) the playbook reads.

package cmd

import (
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseJustify is a small helper that builds a kong parser around
// the CLI struct and parses the given args. Returns the populated
// JustifyCmd or a parse error.
func parseJustify(t *testing.T, args ...string) (*JustifyCmd, error) {
	t.Helper()
	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("locutus"),
		kong.Vars{"version": "test"},
		kong.Exit(func(int) {}),
	)
	require.NoError(t, err)
	_, parseErr := parser.Parse(append([]string{"justify"}, args...))
	return &cli.Justify, parseErr
}

// TestJustifyCmd_ParsesBareID — `locutus justify dec-foo` parses
// with default format = markdown and empty against.
func TestJustifyCmd_ParsesBareID(t *testing.T) {
	cmd, err := parseJustify(t, "dec-foo")
	require.NoError(t, err)
	assert.Equal(t, "dec-foo", cmd.ID)
	assert.Equal(t, "markdown", cmd.Format, "default format must be markdown")
	assert.Empty(t, cmd.Against)
}

// TestJustifyCmd_ParsesAgainstFlag — adversarial dialogue flag.
func TestJustifyCmd_ParsesAgainstFlag(t *testing.T) {
	cmd, err := parseJustify(t, "dec-foo", "--against", "concern text")
	require.NoError(t, err)
	assert.Equal(t, "dec-foo", cmd.ID)
	assert.Equal(t, "concern text", cmd.Against)
	assert.Equal(t, "markdown", cmd.Format)
}

// TestJustifyCmd_ParsesFormatJSON — explicit JSON format.
func TestJustifyCmd_ParsesFormatJSON(t *testing.T) {
	cmd, err := parseJustify(t, "dec-foo", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, "dec-foo", cmd.ID)
	assert.Equal(t, "json", cmd.Format)
}

// TestJustifyCmd_ParsesCombinedFlags — adversarial + JSON together.
func TestJustifyCmd_ParsesCombinedFlags(t *testing.T) {
	cmd, err := parseJustify(t, "dec-foo", "--against", "concern", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, "dec-foo", cmd.ID)
	assert.Equal(t, "concern", cmd.Against)
	assert.Equal(t, "json", cmd.Format)
}

// TestJustifyCmd_RejectsInvalidFormat — kong's enum tag rejects
// formats outside {markdown, json}. The error names the valid set.
func TestJustifyCmd_RejectsInvalidFormat(t *testing.T) {
	_, err := parseJustify(t, "dec-foo", "--format", "yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yaml")
}

// TestJustifyCmd_RequiresID — positional id is required.
func TestJustifyCmd_RequiresID(t *testing.T) {
	_, err := parseJustify(t)
	require.Error(t, err)
}

// TestJustifyCmd_ContextNoteShape — the context note the verb
// builds matches the playbook's Inputs section verbatim. Drift
// between this builder and the playbook would mean the
// orchestrator misses inputs at runtime.
func TestJustifyCmd_ContextNoteShape(t *testing.T) {
	cmd := &JustifyCmd{ID: "dec-primary-datastore", Format: "markdown"}
	note := cmd.contextNote()
	assert.Contains(t, note, "Target node: dec-primary-datastore")
	assert.Contains(t, note, "Output format: markdown")
	assert.NotContains(t, note, "Challenge from user")

	cmd.Against = "L2 propensity-score field gating is fragile"
	cmd.Format = "json"
	note = cmd.contextNote()
	assert.Contains(t, note, "Target node: dec-primary-datastore")
	assert.Contains(t, note, "Output format: json")
	assert.Contains(t, note, "Challenge from user (for adversarial dialogue):")
	assert.Contains(t, note, "L2 propensity-score field gating is fragile")
}

// TestJustifyCmd_ContextNoteOmitsEmptyChallenge — when --against is
// empty, no "Challenge from user" line appears. The playbook reads
// the presence of the line as the signal to dispatch spec-challenger,
// so an empty line would cause a spurious dispatch.
func TestJustifyCmd_ContextNoteOmitsEmptyChallenge(t *testing.T) {
	cmd := &JustifyCmd{ID: "dec-foo", Format: "markdown", Against: ""}
	note := cmd.contextNote()
	lower := strings.ToLower(note)
	assert.NotContains(t, lower, "challenge",
		"empty --against must not produce a 'Challenge from user' line in the run context")
}
