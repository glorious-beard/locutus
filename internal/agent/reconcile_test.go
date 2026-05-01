package agent

import (
	"reflect"
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

func TestApplyReconciliationReuseExistingRejectsUnknownID(t *testing.T) {
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

	_, _, err := ApplyReconciliation(raw, verdict, &ExistingSpec{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dec-not-in-snapshot")
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

func TestApplyReconciliationRejectsUnknownActionKind(t *testing.T) {
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{ID: "feat-x", Title: "X"}},
	}
	verdict := ReconciliationVerdict{
		Actions: []ReconciliationAction{{Kind: "merge"}}, // not one of dedupe/resolve_conflict/reuse_existing
	}
	_, _, err := ApplyReconciliation(raw, verdict, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown kind")
}

func TestApplyReconciliationRejectsBadSourceParent(t *testing.T) {
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
	_, _, err := ApplyReconciliation(raw, verdict, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "feat-not-in-proposal")
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Use Postgres", "use-postgres"},
		{"Use TanStack Start", "use-tanstack-start"},
		{"Provision for 1k concurrent at p99", "provision-for-1k-concurrent-at-p99"},
		{"  spaces  ", "spaces"},
		{"!@#", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, slugify(c.in), "slugify(%q)", c.in)
	}
}
