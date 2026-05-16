package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRawSpecProposalNoInfluencedBy is a compile-time guard: under
// DJ-124 the per-axis decision-elaborator does not carry inter-decision
// influence relationships. Influence is added during refine, not during
// initial spec generation. If a future refactor adds InfluencedBy to
// RawDecisionProposal the dangling-reference class returns.
func TestRawSpecProposalNoInfluencedBy(t *testing.T) {
	_, ok := reflect.TypeOf(RawDecisionProposal{}).FieldByName("InfluencedBy")
	assert.False(t, ok, "RawDecisionProposal must not carry InfluencedBy (re-introduces inter-decision cross-references)")
}

// TestApplyReconciliationFieldMapsDecisions — the new reconciler's
// primary job. Each RawDecisionProposal in the input becomes one
// DecisionProposal in the output with id preserved from the elaborator's
// authored slug.
func TestApplyReconciliationFieldMapsDecisions(t *testing.T) {
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID: "feat-x", Title: "X", Decisions: []string{"dec-postgres-oltp-store"},
		}},
		Decisions: []RawDecisionProposal{{
			ID:                 "dec-postgres-oltp-store",
			Title:              "OLTP store engine",
			Summary:            "Adopt Postgres for the OLTP store.",
			Rationale:          "Postgres aligns with the JSONB-leaning analytics workload.",
			ArchitectRationale: "Postgres fits the analytics roadmap.",
			Confidence:         0.85,
			Alternatives: []spec.Alternative{{
				Name: "MySQL", Rationale: "common default", RejectedBecause: "weaker JSON ergonomics",
			}},
			Citations: []spec.Citation{{
				Kind: "goals", Reference: "GOALS.md", Excerpt: "JSON queries against events",
			}},
			Axes:       []string{"oltp-store"},
			SurfacedBy: []string{"feat-x"},
		}},
	}
	out, applied, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)
	require.Empty(t, applied, "no dangling references ⇒ no integrity-violation actions")
	require.Len(t, out.Decisions, 1)
	d := out.Decisions[0]
	assert.Equal(t, "dec-postgres-oltp-store", d.ID, "elaborator-authored slug preserved verbatim")
	assert.Equal(t, "OLTP store engine", d.Title)
	assert.Equal(t, "Adopt Postgres for the OLTP store.", d.Summary)
	assert.Equal(t, "Postgres aligns with the JSONB-leaning analytics workload.", d.Rationale)
	assert.Equal(t, "Postgres fits the analytics roadmap.", d.ArchitectRationale)
	assert.InDelta(t, 0.85, d.Confidence, 1e-9)
	require.Len(t, d.Alternatives, 1)
	assert.Equal(t, "MySQL", d.Alternatives[0].Name)
	require.Len(t, d.Citations, 1)
	assert.Equal(t, "goals", d.Citations[0].Kind)

	require.Len(t, out.Features, 1)
	assert.Equal(t, []string{"dec-postgres-oltp-store"}, out.Features[0].Decisions,
		"feature.Decisions should preserve the id references verbatim")
}

// TestApplyReconciliationFlagsDanglingReference — a feature references
// an id that doesn't exist in raw.Decisions or existing; ApplyReconciliation
// surfaces an integrity_violation AppliedAction so the workflow's critic
// loop can address it.
func TestApplyReconciliationFlagsDanglingReference(t *testing.T) {
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID: "feat-x", Title: "X", Decisions: []string{"dec-missing", "dec-present"},
		}},
		Strategies: []RawStrategyProposal{{
			ID: "strat-y", Title: "Y", Kind: "foundational", Decisions: []string{"dec-also-missing"},
		}},
		Decisions: []RawDecisionProposal{{
			ID: "dec-present", Title: "Present",
		}},
	}
	out, applied, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)
	require.Len(t, applied, 2, "two dangling references ⇒ two integrity-violation entries")
	got := map[string]string{}
	for _, a := range applied {
		assert.Equal(t, "integrity_violation", a.Kind)
		require.Len(t, a.AffectedNodes, 1)
		got[a.CanonicalID] = a.AffectedNodes[0].ParentID
	}
	assert.Equal(t, "feat-x", got["dec-missing"])
	assert.Equal(t, "strat-y", got["dec-also-missing"])

	// The valid reference resolves cleanly.
	require.Len(t, out.Features, 1)
	assert.Equal(t, []string{"dec-missing", "dec-present"}, out.Features[0].Decisions,
		"feature.Decisions slice is preserved verbatim — integrity violations are surfaced, not stripped")
}

// TestApplyReconciliationResolvesAgainstExistingSpec — an id present in
// ExistingSpec.Decisions but absent from raw.Decisions is not a dangling
// reference; the workflow is extending an existing spec.
func TestApplyReconciliationResolvesAgainstExistingSpec(t *testing.T) {
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{{
			ID: "feat-x", Title: "X", Decisions: []string{"dec-existing"},
		}},
	}
	existing := &ExistingSpec{Decisions: []spec.Decision{{ID: "dec-existing", Title: "Existing"}}}
	out, applied, err := ApplyReconciliation(raw, ReconciliationVerdict{}, existing)
	require.NoError(t, err)
	assert.Empty(t, applied, "id resolves against existing spec; no integrity violation")
	require.Len(t, out.Features, 1)
	assert.Equal(t, []string{"dec-existing"}, out.Features[0].Decisions)
	assert.Empty(t, out.Decisions, "no new decisions to mint — references resolve via existing snapshot")
}

// TestApplyReconciliationMintsIDOnEmpty — a RawDecisionProposal with an
// empty ID gets a slug-derived id from its title (the typical decision-
// elaborator authors the id, but the workflow tolerates the absence).
func TestApplyReconciliationMintsIDOnEmpty(t *testing.T) {
	raw := &RawSpecProposal{
		Decisions: []RawDecisionProposal{{
			Title: "Use Postgres",
		}},
	}
	out, _, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)
	require.Len(t, out.Decisions, 1)
	assert.Equal(t, "dec-use-postgres", out.Decisions[0].ID)
}

// TestApplyReconciliationIDCollisionGetsSuffix — two raw decisions with
// the same authored id collide; the second is reassigned via mintDecisionID
// with a -2 suffix.
func TestApplyReconciliationIDCollisionGetsSuffix(t *testing.T) {
	raw := &RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-use-postgres", Title: "Use Postgres", Rationale: "r1"},
			{ID: "dec-use-postgres", Title: "Use Postgres again", Rationale: "r2"},
		},
	}
	out, _, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)
	require.Len(t, out.Decisions, 2)
	assert.Equal(t, "dec-use-postgres", out.Decisions[0].ID)
	assert.Equal(t, "dec-use-postgres-again", out.Decisions[1].ID,
		"second entry on id collision gets reminted from the title")
}

// TestApplyReconciliationCollisionAgainstExisting — a raw decision id
// that collides with an existing spec decision gets reminted so the new
// entry never overwrites the existing one.
func TestApplyReconciliationCollisionAgainstExisting(t *testing.T) {
	raw := &RawSpecProposal{
		Decisions: []RawDecisionProposal{{
			ID: "dec-existing", Title: "Different decision",
		}},
	}
	existing := &ExistingSpec{Decisions: []spec.Decision{{ID: "dec-existing", Title: "Existing"}}}
	out, _, err := ApplyReconciliation(raw, ReconciliationVerdict{}, existing)
	require.NoError(t, err)
	require.Len(t, out.Decisions, 1)
	assert.NotEqual(t, "dec-existing", out.Decisions[0].ID,
		"raw decision colliding with existing id must be reminted, not overwrite")
	assert.Equal(t, "dec-different-decision", out.Decisions[0].ID)
}

// TestApplyReconciliationVerdictIsIgnored — the reconciler agent's
// verdict content is parsed but ignored under DJ-124. Whatever the
// agent emits, ApplyReconciliation produces the same output that the
// raw proposal alone would produce.
func TestApplyReconciliationVerdictIsIgnored(t *testing.T) {
	raw := &RawSpecProposal{
		Decisions: []RawDecisionProposal{{
			ID: "dec-a", Title: "A",
		}},
	}
	withoutVerdict, _, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)

	// A verdict with arbitrary content — ApplyReconciliation should
	// produce the same output as the empty-verdict case.
	withVerdict, _, err := ApplyReconciliation(raw, ReconciliationVerdict{
		Actions: []ReconciliationAction{{
			Kind: "dedupe",
			Sources: []DecisionSourceRef{
				{ParentKind: "feature", ParentID: "feat-x", Index: 0},
			},
		}},
	}, nil)
	require.NoError(t, err)

	assert.Equal(t, withoutVerdict, withVerdict,
		"verdict content must be a no-op under DJ-124 (ApplyReconciliation ignores it)")
}

// TestApplyReconciliationIsDeterministic — same inputs → same outputs,
// byte-for-byte. Pure Go function; no LLM in the loop.
func TestApplyReconciliationIsDeterministic(t *testing.T) {
	raw := &RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-a", Title: "A", Decisions: []string{"dec-x"}},
		},
		Strategies: []RawStrategyProposal{
			{ID: "strat-x", Title: "Stack", Kind: "foundational", Body: "x", Decisions: []string{"dec-x"}},
		},
		Decisions: []RawDecisionProposal{
			{ID: "dec-x", Title: "X"},
		},
	}
	a, _, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)
	b, _, err := ApplyReconciliation(raw, ReconciliationVerdict{}, nil)
	require.NoError(t, err)
	assert.Equal(t, a, b, "ApplyReconciliation must be deterministic on identical inputs")
}

// TestApplyReconciliationNilRawIsNoop — defensive: a nil raw pointer
// returns an empty SpecProposal cleanly rather than panicking.
func TestApplyReconciliationNilRawIsNoop(t *testing.T) {
	out, applied, err := ApplyReconciliation(nil, ReconciliationVerdict{}, nil)
	require.NoError(t, err)
	assert.Empty(t, applied)
	require.NotNil(t, out)
	assert.Empty(t, out.Decisions)
	assert.Empty(t, out.Features)
	assert.Empty(t, out.Strategies)
}

// TestMintDecisionID_CapsRunawayTitleLength — even when the model goes
// off-rails into the title field, the resulting id stays under OS
// filename length limits. spec.SlugID caps the slug at 50 chars; total
// `dec-<slug>` is well under any filesystem's filename limit.
func TestMintDecisionID_CapsRunawayTitleLength(t *testing.T) {
	runawayTitle := strings.Repeat("the-end-the-very-end-the-absolute-end-", 50)
	id := mintDecisionID(runawayTitle, map[string]struct{}{})
	assert.Less(t, len(id), 256, "decision ID must fit within OS filename length limits even when title is degenerate")
	assert.True(t, strings.HasPrefix(id, "dec-"), "decision IDs always carry the dec- prefix")
}
