// DJ-138 phase 4 — RefineCmd surface tests covering the --with
// flag, the tightened plain-mode validation, and the context-note
// shape. Pure unit tests against the validation helpers — no ACP
// dispatch.

package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseRefine builds a kong parser around the CLI and parses
// `locutus refine <args>`. Returns the populated RefineCmd or a
// parse error.
func parseRefine(t *testing.T, args ...string) (*RefineCmd, error) {
	t.Helper()
	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("locutus"),
		kong.Vars{"version": "test"},
		kong.Exit(func(int) {}),
	)
	require.NoError(t, err)
	_, parseErr := parser.Parse(append([]string{"refine"}, args...))
	return &cli.Refine, parseErr
}

// seedSpecGraph populates a MemFS with the minimum spec.LoadSpec
// reads back: one decision, one feature, one strategy. Used by the
// validation tests that need a real spec graph for the
// existence-check path.
func seedSpecGraph(t *testing.T) specio.FS {
	t.Helper()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/strategies", 0o755))

	now := time.Now().UTC()
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/decisions/dec-oltp-store", spec.Decision{
		ID: "dec-oltp-store", Title: "Choose Postgres", Status: spec.DecisionStatusActive,
		Confidence: 1.0, Rationale: "r", Axes: []string{"oltp-store"},
		CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/features/feat-realtime-sync", spec.Feature{
		ID: "feat-realtime-sync", Title: "Realtime sync", Status: spec.FeatureStatusActive,
		Decisions: []string{"dec-oltp-store"}, CreatedAt: now, UpdatedAt: now,
	}, ""))
	require.NoError(t, specio.SavePair(fsys, ".borg/spec/strategies/strat-storage", spec.Strategy{
		ID: "strat-storage", Title: "Storage strategy", Kind: spec.StrategyKindFoundational,
		Status: "active", Decisions: []string{"dec-oltp-store"},
	}, ""))
	return fsys
}

// TestRefineCmd_ParsesPlainTarget — plain `locutus refine`
// preserves the pre-DJ-138 behavior (Target = "goals" default).
func TestRefineCmd_ParsesPlainTarget(t *testing.T) {
	cmd, err := parseRefine(t)
	require.NoError(t, err)
	assert.Equal(t, "goals", cmd.Target)
	assert.Empty(t, cmd.With)
}

// TestRefineCmd_ParsesExplicitTarget — `locutus refine dec-foo`.
func TestRefineCmd_ParsesExplicitTarget(t *testing.T) {
	cmd, err := parseRefine(t, "dec-oltp-store")
	require.NoError(t, err)
	assert.Equal(t, "dec-oltp-store", cmd.Target)
	assert.Empty(t, cmd.With)
}

// TestRefineCmd_ParsesWithFlag — `--with` reads the bias string.
func TestRefineCmd_ParsesWithFlag(t *testing.T) {
	cmd, err := parseRefine(t, "dec-oltp-store", "--with", "use postgres, team owns ops")
	require.NoError(t, err)
	assert.Equal(t, "dec-oltp-store", cmd.Target)
	assert.Equal(t, "use postgres, team owns ops", cmd.With)
}

// TestRefineCmd_WithRoutesToSpecBias — when --with is present,
// resolveActivity picks spec_bias instead of spec_refinement.
func TestRefineCmd_WithRoutesToSpecBias(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "dec-oltp-store", With: "use postgres"}
	got, err := c.resolveActivity(fsys)
	require.NoError(t, err)
	assert.Equal(t, "spec_bias", got)
}

// TestRefineCmd_PlainRoutesToSpecRefinement — without --with, plain
// mode keeps routing to spec_refinement.
func TestRefineCmd_PlainRoutesToSpecRefinement(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "goals"}
	got, err := c.resolveActivity(fsys)
	require.NoError(t, err)
	assert.Equal(t, "spec_refinement", got)
}

// TestRefineCmd_WithRejectsDefaultGoalsTarget — --with requires
// an explicit non-Goal target; the default "goals" is refused.
func TestRefineCmd_WithRejectsDefaultGoalsTarget(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "goals", With: "use postgres"}
	_, err := c.resolveActivity(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--with requires an explicit target id")
}

// TestRefineCmd_WithRejectsEmptyBias — whitespace-only bias is
// rejected with a clear message.
func TestRefineCmd_WithRejectsEmptyBias(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "dec-oltp-store", With: "   "}
	_, err := c.resolveActivity(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--with requires a non-empty bias")
}

// TestRefineCmd_WithRejectsUnknownTarget — --with with an id that
// doesn't resolve to a known node fails fast.
func TestRefineCmd_WithRejectsUnknownTarget(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "dec-not-real", With: "use postgres"}
	_, err := c.resolveActivity(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not resolve to a known spec node")
}

// TestRefineCmd_WithRejectsApproachTarget — --with rejects
// Approach (app-) kinds.
func TestRefineCmd_WithRejectsApproachTarget(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "app-realtime-loader", With: "use postgres"}
	_, err := c.resolveActivity(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Approach")
}

// TestRefineCmd_WithRejectsBugTarget — --with rejects Bug (bug-)
// kinds.
func TestRefineCmd_WithRejectsBugTarget(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "bug-some-bug", With: "use postgres"}
	_, err := c.resolveActivity(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bug")
}

// TestRefineCmd_WithAcceptsDecisionTarget — Decision is a valid
// --with target kind.
func TestRefineCmd_WithAcceptsDecisionTarget(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "dec-oltp-store", With: "bias"}
	got, err := c.resolveActivity(fsys)
	require.NoError(t, err)
	assert.Equal(t, "spec_bias", got)
}

// TestRefineCmd_WithAcceptsFeatureTarget — Feature is a valid
// --with target kind.
func TestRefineCmd_WithAcceptsFeatureTarget(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "feat-realtime-sync", With: "bias"}
	got, err := c.resolveActivity(fsys)
	require.NoError(t, err)
	assert.Equal(t, "spec_bias", got)
}

// TestRefineCmd_WithAcceptsStrategyTarget — Strategy is a valid
// --with target kind.
func TestRefineCmd_WithAcceptsStrategyTarget(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "strat-storage", With: "bias"}
	got, err := c.resolveActivity(fsys)
	require.NoError(t, err)
	assert.Equal(t, "spec_bias", got)
}

// TestRefineCmd_PlainRejectsApproachTarget — DJ-138's plain-mode
// tightening: Approach ids rejected even without --with.
func TestRefineCmd_PlainRejectsApproachTarget(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "app-realtime-loader"}
	_, err := c.resolveActivity(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Approach")
}

// TestRefineCmd_PlainRejectsBugTarget — DJ-138's plain-mode
// tightening: Bug ids rejected even without --with.
func TestRefineCmd_PlainRejectsBugTarget(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "bug-some-bug"}
	_, err := c.resolveActivity(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bug")
}

// TestRefineCmd_PlainAcceptsGoalsDefault — plain mode preserves
// the goals-default behavior.
func TestRefineCmd_PlainAcceptsGoalsDefault(t *testing.T) {
	fsys := seedSpecGraph(t)
	c := &RefineCmd{Target: "goals"}
	got, err := c.resolveActivity(fsys)
	require.NoError(t, err)
	assert.Equal(t, "spec_refinement", got)
}

// TestRefineCmd_PlainAcceptsDeliberationTargets — plain mode
// accepts Decision / Feature / Strategy ids without --with.
func TestRefineCmd_PlainAcceptsDeliberationTargets(t *testing.T) {
	fsys := seedSpecGraph(t)
	for _, target := range []string{"dec-oltp-store", "feat-realtime-sync", "strat-storage"} {
		t.Run(target, func(t *testing.T) {
			c := &RefineCmd{Target: target}
			got, err := c.resolveActivity(fsys)
			require.NoError(t, err)
			assert.Equal(t, "spec_refinement", got)
		})
	}
}

// TestRefineCmd_ContextNoteShape — the playbook reads "Target:"
// and (when --with is set) "Bias:" lines; the context-note
// builder must emit both for the spec_bias playbook to pick them
// up.
func TestRefineCmd_ContextNoteShape(t *testing.T) {
	plain := (&RefineCmd{Target: "dec-oltp-store"}).contextNote()
	assert.Contains(t, plain, "Target: dec-oltp-store")
	assert.NotContains(t, plain, "Bias:")

	withBias := (&RefineCmd{Target: "dec-oltp-store", With: "use postgres"}).contextNote()
	assert.Contains(t, withBias, "Target: dec-oltp-store")
	assert.Contains(t, withBias, "Bias: use postgres")
}

// TestRefineCmd_ContextNoteTrimsWhitespaceBias — leading /
// trailing whitespace on the bias is trimmed; whitespace-only
// bias is treated as empty.
func TestRefineCmd_ContextNoteTrimsWhitespaceBias(t *testing.T) {
	c := &RefineCmd{Target: "dec-oltp-store", With: "  use postgres  "}
	note := c.contextNote()
	assert.Contains(t, note, "Bias: use postgres")
	assert.False(t, strings.Contains(note, "Bias:   "), "leading whitespace should be trimmed")
}
