package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureLoaded builds the same shape as internal/spec/loaded_test.go's
// fixture but lives here so render tests don't depend on cross-package
// helpers.
func fixtureLoaded(t *testing.T) (*spec.Loaded, spec.StageMap) {
	t.Helper()
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/strategies", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, fs.MkdirAll(".borg/spec/bugs", 0o755))

	require.NoError(t, fs.WriteFile(".borg/GOALS.md", []byte("# Goals\n\nBuild a thing.\n"), 0o644))
	require.NoError(t, fs.WriteFile(".borg/manifest.json", []byte(`{"project_name":"fixture","version":"1"}`), 0o644))

	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-alpha", spec.Feature{
		ID: "feat-alpha", Title: "Alpha feature", Status: spec.FeatureStatusProposed,
		Description: "Alpha description",
		Decisions:   []string{"dec-shared", "dec-only-feature"},
		CreatedAt:   now, UpdatedAt: now,
	}, "alpha body prose"))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-foundation", spec.Strategy{
		ID: "strat-foundation", Title: "Foundation", Kind: spec.StrategyKindFoundational,
		Status: "proposed", Decisions: []string{"dec-shared"},
	}, "foundation body"))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/strategies/strat-quality", spec.Strategy{
		ID: "strat-quality", Title: "Quality", Kind: spec.StrategyKindQuality,
		Status: "proposed",
	}, "quality body"))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-shared", spec.Decision{
		ID: "dec-shared", Title: "Shared decision", Status: spec.DecisionStatusProposed,
		Confidence: 0.9, Rationale: "shared rationale",
		Alternatives: []spec.Alternative{
			{Name: "Alt A", Rationale: "alt rationale", RejectedBecause: "too costly"},
		},
		Provenance: &spec.DecisionProvenance{
			ArchitectRationale: "architect's take",
			Citations: []spec.Citation{
				{Kind: "goals", Reference: "GOALS.md", Span: "§1"},
			},
		},
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	require.NoError(t, specio.SavePair(fs, ".borg/spec/decisions/dec-only-feature", spec.Decision{
		ID: "dec-only-feature", Title: "Feature-only decision", Status: spec.DecisionStatusProposed,
		Confidence: 0.7, Rationale: "feature-only rationale",
		CreatedAt: now, UpdatedAt: now,
	}, ""))

	loaded, err := spec.LoadSpec(fs)
	require.NoError(t, err)
	stages := spec.DeriveStages(loaded, fs)
	return loaded, stages
}

func TestSnapshotMarkdown_StructureCovers(t *testing.T) {
	loaded, stages := fixtureLoaded(t)
	data := BuildSnapshotData(loaded, stages, "fixture", SnapshotFilters{})
	md := SnapshotMarkdown(data)

	// Top-level sections we expect.
	for _, want := range []string{
		"# fixture — Specification snapshot",
		"## Status",
		"## Goals",
		"## Strategies",
		"### Foundational",
		"### Quality",
		"## Features",
		"## Decisions",
	} {
		assert.Contains(t, md, want, "section %q must appear in snapshot", want)
	}

	// Per-node renderings appear.
	assert.Contains(t, md, "feat-alpha")
	assert.Contains(t, md, "Alpha feature")
	assert.Contains(t, md, "strat-foundation")
	assert.Contains(t, md, "Quality")
	assert.Contains(t, md, "dec-shared")
	assert.Contains(t, md, "shared rationale")

	// Stage tag is rendered.
	assert.Contains(t, md, "drafted", "stage tag visible somewhere")

	// Strategy ordering: Foundational appears before Quality.
	foundIdx := strings.Index(md, "### Foundational")
	qualIdx := strings.Index(md, "### Quality")
	require.True(t, foundIdx >= 0 && qualIdx >= 0)
	assert.Less(t, foundIdx, qualIdx, "Foundational must precede Quality in narrative ordering")
}

func TestSnapshotMarkdown_BackReferences(t *testing.T) {
	loaded, stages := fixtureLoaded(t)
	data := BuildSnapshotData(loaded, stages, "fixture", SnapshotFilters{})
	md := SnapshotMarkdown(data)

	// dec-shared is referenced by feat-alpha (feature) and
	// strat-foundation (strategy). Both back-refs should appear in
	// the decision's section. Match around the bold markdown so
	// rendering changes that touch only the formatting don't break
	// the test.
	assert.Contains(t, md, "Referenced by features:** feat-alpha")
	assert.Contains(t, md, "Referenced by strategies:** strat-foundation")
}

func TestSnapshotMarkdown_FilterByKind(t *testing.T) {
	loaded, stages := fixtureLoaded(t)
	data := BuildSnapshotData(loaded, stages, "fixture", SnapshotFilters{Kinds: []string{"decision"}})
	md := SnapshotMarkdown(data)

	assert.Contains(t, md, "## Decisions")
	assert.NotContains(t, md, "## Features", "features section omitted by --kind=decision")
	assert.NotContains(t, md, "## Strategies", "strategies section omitted by --kind=decision")
}

func TestSnapshotJSON_RoundTrips(t *testing.T) {
	loaded, stages := fixtureLoaded(t)
	data := BuildSnapshotData(loaded, stages, "fixture", SnapshotFilters{})

	encoded, err := json.Marshal(data)
	require.NoError(t, err)

	var decoded SnapshotData
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	assert.Equal(t, "fixture", decoded.ProjectName)
	assert.Len(t, decoded.Features, 1)
	assert.Len(t, decoded.Strategies, 2)
	assert.Len(t, decoded.Decisions, 2)
	assert.Equal(t, "feat-alpha", decoded.Features[0].ID)

	// Inverse-index back-refs survive the round trip.
	for _, d := range decoded.Decisions {
		if d.ID == "dec-shared" {
			assert.Equal(t, []string{"feat-alpha"}, d.ReferencedBy.Features)
			assert.Equal(t, []string{"strat-foundation"}, d.ReferencedBy.Strategies)
		}
	}
}

func TestSnapshotMarkdown_EmptyGraph(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll(".borg/spec/features", 0o755))
	loaded, err := spec.LoadSpec(fs)
	require.NoError(t, err)
	stages := spec.DeriveStages(loaded, fs)

	data := BuildSnapshotData(loaded, stages, "", SnapshotFilters{})
	md := SnapshotMarkdown(data)

	// Even an empty graph produces a valid document with the status
	// section and zero counts.
	assert.Contains(t, md, "# Specification snapshot", "header even without project name")
	assert.Contains(t, md, "## Status")
	assert.NotContains(t, md, "## Goals", "Goals section omitted when GOALS.md is empty")
	assert.NotContains(t, md, "## Decisions", "Decisions section omitted when graph has none")
}
