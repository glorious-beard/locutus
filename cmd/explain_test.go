package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureExplain creates a small spec graph for explain tests: one
// feature, one strategy, and one shared decision plus an unreferenced
// approach. Mirrors the shape used by spec/loaded_test.go but smaller —
// explain renders one node at a time so we don't need the full graph.
func fixtureExplain(t *testing.T) specio.FS {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))
	require.NoError(t, fs.WriteFile(".borg/manifest.json",
		[]byte(`{"project_name":"fixture","version":"1"}`), 0o644))

	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-alpha", spec.Feature{
		ID: "feat-alpha", Title: "Alpha feature", Status: spec.FeatureStatusProposed,
		Description: "Alpha description.",
		Decisions:   []string{"dec-shared"},
		CreatedAt:   now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-foundation", spec.Strategy{
		ID: "strat-foundation", Title: "Foundation strategy",
		Kind: spec.StrategyKindFoundational, Status: "proposed",
		Decisions: []string{"dec-shared"},
	}, "Foundation body."))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-shared", spec.Decision{
		ID: "dec-shared", Title: "Shared decision",
		Status: spec.DecisionStatusProposed,
		Confidence: 0.9, Rationale: "Shared rationale text.",
		Alternatives: []spec.Alternative{
			{Name: "Alt A", Rationale: "alt rationale", RejectedBecause: "too costly"},
		},
		Provenance: &spec.DecisionProvenance{
			ArchitectRationale: "architect's note",
			Citations: []spec.Citation{
				{Kind: "goals", Reference: "GOALS.md", Span: "§1"},
			},
		},
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	return fs
}

func TestExplainDecision(t *testing.T) {
	fs := fixtureExplain(t)
	r, err := RunExplain(fs, "dec-shared")
	require.NoError(t, err)

	assert.Equal(t, "dec-shared", r.ID)
	assert.Equal(t, "decision", r.Kind)

	md := r.Markdown
	for _, want := range []string{
		"# `dec-shared`",
		"**Title:** Shared decision",
		"Shared rationale text.",
		"architect's note",
		"Alt A",
		"too costly",
		"GOALS.md (§1)",
		// Back-refs surface both feature and strategy.
		"Referenced by features: feat-alpha",
		"Referenced by strategies: strat-foundation",
	} {
		assert.Contains(t, md, want, "expected %q in explain output", want)
	}
}

func TestExplainFeature(t *testing.T) {
	fs := fixtureExplain(t)
	r, err := RunExplain(fs, "feat-alpha")
	require.NoError(t, err)

	assert.Equal(t, "feature", r.Kind)
	for _, want := range []string{
		"# `feat-alpha`",
		"Alpha feature",
		"Alpha description.",
		"dec-shared",
		// Cross-reference: shared decision links to strat-foundation.
		"Relevant strategies",
	} {
		assert.Contains(t, r.Markdown, want)
	}
}

func TestExplainStrategy(t *testing.T) {
	fs := fixtureExplain(t)
	r, err := RunExplain(fs, "strat-foundation")
	require.NoError(t, err)

	assert.Equal(t, "strategy", r.Kind)
	for _, want := range []string{
		"# `strat-foundation`",
		"Foundation strategy",
		"Foundation body.",
		"dec-shared",
	} {
		assert.Contains(t, r.Markdown, want)
	}
}

func TestExplainUnknownID(t *testing.T) {
	fs := fixtureExplain(t)
	_, err := RunExplain(fs, "dec-nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestExplainBadPrefix(t *testing.T) {
	fs := fixtureExplain(t)
	_, err := RunExplain(fs, "garbage-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown prefix")
}

func TestExplainTopHeaderPrecedesNodeSection(t *testing.T) {
	fs := fixtureExplain(t)
	r, err := RunExplain(fs, "dec-shared")
	require.NoError(t, err)

	topIdx := strings.Index(r.Markdown, "# `dec-shared`")
	subIdx := strings.Index(r.Markdown, "### `dec-shared`")
	require.True(t, topIdx >= 0 && subIdx > topIdx,
		"top-level # header must precede the inner ### subsection from the per-node renderer")
}
