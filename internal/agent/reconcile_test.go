package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRawSpecProposalNoCrossRefs is a compile-time guard: the structural
// property that makes Phase 2 work is that the raw proposal has NO
// fields containing IDs of other nodes. If a future refactor adds
// `InfluencedBy []string` or similar to InlineDecisionProposal, the
// dangling-reference class returns. This test fails the build before
// that ships.
func TestRawSpecProposalNoCrossRefs(t *testing.T) {
	for _, name := range []string{"InfluencedBy", "DecisionRefs", "Approaches"} {
		_, ok := reflect.TypeOf(InlineDecisionProposal{}).FieldByName(name)
		assert.False(t, ok, "InlineDecisionProposal must not have field %q (re-introduces cross-references)", name)
		_, ok = reflect.TypeOf(RawFeatureProposal{}).FieldByName(name)
		if name != "Decisions" {
			assert.False(t, ok, "RawFeatureProposal must not have field %q", name)
		}
		_, ok = reflect.TypeOf(RawStrategyProposal{}).FieldByName(name)
		if name != "Decisions" {
			assert.False(t, ok, "RawStrategyProposal must not have field %q", name)
		}
	}

	// InlineDecisionProposal must also not carry an ID — IDs are
	// reconciler-assigned. An architect that fabricates IDs is the
	// failure mode Phase 2 was designed to eliminate.
	_, hasID := reflect.TypeOf(InlineDecisionProposal{}).FieldByName("ID")
	assert.False(t, hasID, "InlineDecisionProposal must not have an ID field")
}

func TestApplyReconciliationKeepsSeparateByDefault(t *testing.T) {
	// Empty verdict — every inline decision becomes its own canonical
	// Decision via slug-derived ID. This is the common-case path.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID:    "feat-x",
			Title: "X",
			Decisions: []InlineDecisionProposal{
				{Title: "Use Postgres", Rationale: "r", Confidence: 0.8},
				{Title: "Async ingest", Rationale: "r", Confidence: 0.7},
			},
		}},
	}
	canonical, applied, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)
	require.Empty(t, applied, "empty verdict ⇒ no actions applied")

	require.Len(t, canonical.Decisions, 2, "two inline decisions ⇒ two canonical decisions")
	assert.Equal(t, "dec-use-postgres", canonical.Decisions[0].ID)
	assert.Equal(t, "dec-async-ingest", canonical.Decisions[1].ID)

	require.Len(t, canonical.Features, 1)
	assert.Equal(t, []string{"dec-use-postgres", "dec-async-ingest"}, canonical.Features[0].Decisions,
		"feature.decisions should reference the slug-derived canonical ids")
}

func TestApplyReconciliationDedupes(t *testing.T) {
	// Two features each describe "Use Postgres" inline. Verdict says
	// they're the same; the assembler picks the canonical body and
	// rewrites both features to reference it.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-a", Title: "A", Decisions: []InlineDecisionProposal{{Title: "Use Postgres", Rationale: "from feat-a"}}},
			{ID: "feat-b", Title: "B", Decisions: []InlineDecisionProposal{{Title: "Use Postgres", Rationale: "from feat-b"}}},
		},
	}
	canonical_inline := InlineDecisionProposal{Title: "Use Postgres", Rationale: "merged from feat-a + feat-b", Confidence: 0.9}
	verdict := ReconciliationVerdict{
		Actions: []ReconciliationAction{{
			Kind: "dedupe",
			Sources: []DecisionSourceRef{
				{ParentKind: "feature", ParentID: "feat-a", Index: 0},
				{ParentKind: "feature", ParentID: "feat-b", Index: 0},
			},
			Canonical: &canonical_inline,
		}},
	}

	out, applied, err := ApplyReconciliation(raw, verdict, nil)
	require.NoError(t, err)
	require.Len(t, applied, 1)
	assert.Equal(t, "dedupe", applied[0].Kind)
	assert.Equal(t, "dec-use-postgres", applied[0].CanonicalID)

	require.Len(t, out.Decisions, 1, "dedupe ⇒ one canonical decision, not two")
	assert.Equal(t, "dec-use-postgres", out.Decisions[0].ID)
	assert.Equal(t, "merged from feat-a + feat-b", out.Decisions[0].Rationale)

	require.Len(t, out.Features, 2)
	assert.Equal(t, []string{"dec-use-postgres"}, out.Features[0].Decisions)
	assert.Equal(t, []string{"dec-use-postgres"}, out.Features[1].Decisions,
		"both features should reference the canonical id, not separate copies")
}

func TestApplyReconciliationResolvesConflict(t *testing.T) {
	// Two features pick incompatible storage. Reconciler picks the
	// winner; loser lands in the winner's alternatives[]; both features
	// reference the winner.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-ingest", Title: "Ingest", Decisions: []InlineDecisionProposal{{Title: "Use Postgres", Rationale: "OLTP", Confidence: 0.8}}},
			{ID: "feat-analytics", Title: "Analytics", Decisions: []InlineDecisionProposal{{Title: "Use ClickHouse", Rationale: "OLAP", Confidence: 0.7}}},
		},
	}
	winner := InlineDecisionProposal{
		Title:      "Use Postgres for OLTP and OLAP",
		Rationale:  "single store covers both at this scale",
		Confidence: 0.85,
		Alternatives: []spec.Alternative{
			{Name: "Postgres+ClickHouse", Rationale: "OLAP separation", RejectedBecause: "operational overhead"},
		},
	}
	loser := InlineDecisionProposal{Title: "Use ClickHouse", Rationale: "OLAP"}
	verdict := ReconciliationVerdict{
		Actions: []ReconciliationAction{{
			Kind: "resolve_conflict",
			Sources: []DecisionSourceRef{
				{ParentKind: "feature", ParentID: "feat-ingest", Index: 0},
				{ParentKind: "feature", ParentID: "feat-analytics", Index: 0},
			},
			Canonical:       &winner,
			Loser:           &loser,
			RejectedBecause: "single Postgres covers both at this scale; ClickHouse adds operational overhead",
		}},
	}

	out, applied, err := ApplyReconciliation(raw, verdict, nil)
	require.NoError(t, err)
	require.Len(t, applied, 1)
	assert.Equal(t, "resolve_conflict", applied[0].Kind)

	require.Len(t, out.Decisions, 1)
	winnerDecision := out.Decisions[0]
	assert.Equal(t, "dec-use-postgres-for-oltp-and-olap", winnerDecision.ID,
		"winner gets a slug-derived id")
	require.GreaterOrEqual(t, len(winnerDecision.Alternatives), 2,
		"loser should be appended to the winner's alternatives[]; the original alternatives are preserved")
	// The last appended alternative is the loser.
	loserAlt := winnerDecision.Alternatives[len(winnerDecision.Alternatives)-1]
	assert.Equal(t, "Use ClickHouse", loserAlt.Name)
	assert.Equal(t, "single Postgres covers both at this scale; ClickHouse adds operational overhead", loserAlt.RejectedBecause)

	// Both features should now reference the winner.
	require.Len(t, out.Features, 2)
	assert.Equal(t, out.Decisions[0].ID, out.Features[0].Decisions[0])
	assert.Equal(t, out.Decisions[0].ID, out.Features[1].Decisions[0])
}

func TestApplyReconciliationReusesExistingID(t *testing.T) {
	// A new inline decision matches an existing decision in the snapshot.
	// Verdict says reuse_existing; the canonical reference points at the
	// existing id, no new decision is minted.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID:        "feat-x",
			Title:     "X",
			Decisions: []InlineDecisionProposal{{Title: "Use Postgres", Rationale: "from feat-x"}},
		}},
	}
	existing := &ExistingSpec{
		Decisions: []spec.Decision{{ID: "dec-existing-postgres", Title: "Use Postgres for OLTP"}},
	}
	verdict := ReconciliationVerdict{
		Actions: []ReconciliationAction{{
			Kind: "reuse_existing",
			Sources: []DecisionSourceRef{
				{ParentKind: "feature", ParentID: "feat-x", Index: 0},
			},
			ExistingID: "dec-existing-postgres",
		}},
	}

	out, applied, err := ApplyReconciliation(raw, verdict, existing)
	require.NoError(t, err)
	require.Len(t, applied, 1)
	assert.Equal(t, "reuse_existing", applied[0].Kind)
	assert.Equal(t, "dec-existing-postgres", applied[0].CanonicalID)

	assert.Empty(t, out.Decisions,
		"reuse_existing must not mint new canonical decisions; the reference is enough")
	require.Len(t, out.Features, 1)
	assert.Equal(t, []string{"dec-existing-postgres"}, out.Features[0].Decisions)
}

func TestApplyReconciliationReuseExistingUnknownIDDegrades(t *testing.T) {
	// reuse_existing pointing at an unknown id is a bad LLM verdict.
	// Skip the action and let the implicit-keep-separate pass mint a
	// canonical from the source. Workflow proceeds with a valid (un-
	// deduped) SpecProposal instead of failing the whole council.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID:        "feat-x",
			Title:     "X",
			Decisions: []InlineDecisionProposal{{Title: "Use Postgres"}},
		}},
	}
	verdict := ReconciliationVerdict{
		Actions: []ReconciliationAction{{
			Kind: "reuse_existing",
			Sources: []DecisionSourceRef{
				{ParentKind: "feature", ParentID: "feat-x", Index: 0},
			},
			ExistingID: "dec-not-in-snapshot",
		}},
	}

	out, applied, err := ApplyReconciliation(raw, verdict, &ExistingSpec{})
	require.NoError(t, err)
	assert.Empty(t, applied, "skipped action emits no AppliedAction")
	require.Len(t, out.Decisions, 1, "source falls through to keep-separate; mints its own canonical")
	assert.Equal(t, "dec-use-postgres", out.Decisions[0].ID)
}

func TestApplyReconciliationIsDeterministic(t *testing.T) {
	// Same raw + same verdict ⇒ same output, byte-for-byte. Pure Go
	// function; no LLM in the loop.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-a", Title: "A", Decisions: []InlineDecisionProposal{{Title: "Use Postgres"}, {Title: "Cache reads"}}},
		},
		Strategies: []RawStrategyProposal{
			{ID: "strat-x", Title: "Stack", Kind: "foundational", Body: "x", Decisions: []InlineDecisionProposal{{Title: "Use Go"}}},
		},
	}
	verdict := ReconciliationVerdict{}

	a, _, err := ApplyReconciliation(raw, verdict, nil)
	require.NoError(t, err)
	b, _, err := ApplyReconciliation(raw, verdict, nil)
	require.NoError(t, err)

	assert.Equal(t, a, b, "ApplyReconciliation must be deterministic on identical inputs")
}

func TestApplyReconciliationIDCollisionGetsSuffix(t *testing.T) {
	// Two distinct titles slugify to the same string ("Use Postgres."
	// and "Use Postgres" both → "use-postgres"). The second mint
	// gets a "-2" suffix to avoid collision.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID:    "feat-x",
			Title: "X",
			Decisions: []InlineDecisionProposal{
				{Title: "Use Postgres", Rationale: "r1"},
				{Title: "Use Postgres.", Rationale: "r2"},
			},
		}},
	}

	out, _, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)
	require.Len(t, out.Decisions, 2)
	assert.Equal(t, "dec-use-postgres", out.Decisions[0].ID)
	assert.Equal(t, "dec-use-postgres-2", out.Decisions[1].ID,
		"slug collision ⇒ second decision gets a numeric suffix")
}

func TestApplyReconciliationUnknownActionKindDegrades(t *testing.T) {
	// Unknown action kind: skip with a Warn. Sources stay unclaimed
	// and fall to implicit-keep-separate. Workflow proceeds.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID:        "feat-x",
			Title:     "X",
			Decisions: []InlineDecisionProposal{{Title: "Use Postgres"}},
		}},
	}
	verdict := ReconciliationVerdict{
		Actions: []ReconciliationAction{{
			Kind: "merge", // not one of dedupe/resolve_conflict/reuse_existing
			Sources: []DecisionSourceRef{
				{ParentKind: "feature", ParentID: "feat-x", Index: 0},
			},
		}},
	}
	out, applied, err := ApplyReconciliation(raw, verdict, nil)
	require.NoError(t, err)
	assert.Empty(t, applied)
	require.Len(t, out.Decisions, 1, "source falls through to keep-separate")
}

func TestApplyReconciliationBadSourceParentDegrades(t *testing.T) {
	// A dedupe action whose ONLY source points at a parent not in the
	// proposal degrades to "no-op": no canonical lands (would dangle
	// with no parent referring to it), no applied entry, no error.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{ID: "feat-x", Title: "X"}},
	}
	verdict := ReconciliationVerdict{
		Actions: []ReconciliationAction{{
			Kind:      "dedupe",
			Sources:   []DecisionSourceRef{{ParentKind: "feature", ParentID: "feat-not-in-proposal", Index: 0}},
			Canonical: &InlineDecisionProposal{Title: "Use Postgres"},
		}},
	}
	out, applied, err := ApplyReconciliation(raw, verdict, nil)
	require.NoError(t, err)
	assert.Empty(t, applied)
	assert.Empty(t, out.Decisions, "no canonical when every source is rejected")
}

// TestApplyReconciliationDropsEmptyInlineDecisions — Gemini Flash has
// been observed emitting `decisions: [{}]` on strategies (empty objects
// satisfying schema shape but carrying no content). ApplyReconciliation
// must drop these silently rather than minting `dec-untitled` IDs.
// Real inline decisions on the same parent are preserved.
func TestApplyReconciliationDropsEmptyInlineDecisions(t *testing.T) {
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID:    "feat-x",
			Title: "X",
			Decisions: []InlineDecisionProposal{
				{Title: "Use Postgres", Rationale: "OLTP"},
				{}, // empty placeholder
				{Title: " ", Rationale: "still empty after trim"}, // whitespace-only title
			},
		}},
		Strategies: []RawStrategyProposal{{
			ID:    "strat-database",
			Title: "Database",
			Kind:  "foundational",
			Body:  "We need a database.",
			// Architect emitted [{}] — empty placeholder.
			Decisions: []InlineDecisionProposal{{}},
		}},
	}

	out, _, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)

	require.Len(t, out.Decisions, 1, "exactly one canonical decision; the two empties dropped")
	assert.Equal(t, "dec-use-postgres", out.Decisions[0].ID)
	assert.NotContains(t, out.Decisions[0].ID, "dec-untitled",
		"empty placeholders must NEVER produce dec-untitled ids")

	require.Len(t, out.Features, 1)
	assert.Equal(t, []string{"dec-use-postgres"}, out.Features[0].Decisions,
		"feature.decisions only references the real canonical decision")
	require.Len(t, out.Strategies, 1)
	assert.Empty(t, out.Strategies[0].Decisions,
		"strategy with only empty placeholders ends up with no decisions; integrity critic will flag")
}

func TestApplyReconciliationEmptyCanonicalDegrades(t *testing.T) {
	// A reconciler that emits a dedupe action with an empty canonical
	// is malformed (DJ-098 bug class — observed on Gemini Pro skipping
	// the canonical field for dedupe actions). Skip the action and
	// let the source fall through to keep-separate; never mint a
	// dec-untitled id.
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID: "feat-x", Title: "X",
			Decisions: []InlineDecisionProposal{{Title: "Use Postgres"}},
		}},
	}
	verdict := ReconciliationVerdict{
		Actions: []ReconciliationAction{{
			Kind:      "dedupe",
			Sources:   []DecisionSourceRef{{ParentKind: "feature", ParentID: "feat-x", Index: 0}},
			Canonical: &InlineDecisionProposal{}, // empty title
		}},
	}
	out, applied, err := ApplyReconciliation(raw, verdict, nil)
	require.NoError(t, err)
	assert.Empty(t, applied)
	require.Len(t, out.Decisions, 1)
	assert.Equal(t, "dec-use-postgres", out.Decisions[0].ID,
		"source falls through to keep-separate")
}

func TestIsEmptyInlineDecision_DropsPathologicalTitle(t *testing.T) {
	// Same pathology that triggered the slug-cap fix: a model
	// spirals into the title field producing thousands of chars
	// of self-narrating prose. Even with the slug cap protecting
	// the filename, persisting a 5000-char title to the spec graph
	// would bloat YAML, break renders, and surface in audit
	// outputs as nonsense. Treat such decisions as empty so the
	// apply pass drops them with a warning, same as title-only
	// stubs.
	d := InlineDecisionProposal{
		Title: strings.Repeat("the-end-the-very-end-the-absolute-end-", 50),
	}
	assert.True(t, isEmptyInlineDecision(d),
		"decision with pathologically long title must be treated as empty (apply pass drops it with a warning)")
}

func TestIsEmptyInlineDecision_AcceptsNormalTitle(t *testing.T) {
	d := InlineDecisionProposal{Title: "Use PostgreSQL 16 with PostGIS for geospatial workloads"}
	assert.False(t, isEmptyInlineDecision(d), "normal title length is not pathological")
}

func TestMintDecisionID_CapsRunawayTitleLength(t *testing.T) {
	// Observed pathology: a model went off-rails in the title field of
	// an InlineDecisionProposal and produced a 5000+ char self-narrating
	// ramble. Without a slug-length cap, the resulting `dec-<slug>` ID
	// became a filename longer than the OS limit (typically 255 bytes),
	// failing the spec save and leaving the spec graph half-written.
	//
	// mintDecisionID now delegates to spec.SlugID which caps slug length
	// at 50 chars; total filename `dec-<slug>.json` stays well under any
	// filesystem's filename limit even before any collision suffix.
	runawayTitle := strings.Repeat("the-end-the-very-end-the-absolute-end-", 50)
	id := mintDecisionID(runawayTitle, map[string]struct{}{})

	assert.Less(t, len(id), 256, "decision ID must fit within OS filename length limits even when title is degenerate")
	assert.True(t, strings.HasPrefix(id, "dec-"), "decision IDs always carry the dec- prefix")
}
