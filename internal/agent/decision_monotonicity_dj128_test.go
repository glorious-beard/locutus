// DJ-128 Phase 4 — alternative-monotonicity tests for mergeDecisions.

package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makePriorWithAlt returns a one-decision RawSpecProposal carrying a
// single decision dec-X with one named alternative and a chosen Title.
// Used as the baseline fixture across the monotonicity tests.
func makePriorWithAlt(t *testing.T, priorTitle, altName string) *PlanningState {
	t.Helper()
	prior := RawDecisionProposal{
		ID: "dec-x", Title: priorTitle, Rationale: "old", Confidence: 0.7,
		ArchitectRationale: "old",
		Alternatives: []spec.Alternative{{
			Name: altName, Rationale: "considered", RejectedBecause: "lost on the chosen-path axis",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	raw, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)
	return &PlanningState{RawProposal: string(raw)}
}

// TestMergeDecisionsDemotesPriorChosenOption — a flip revise: prior
// chosen demotes into alternatives carrying iteration provenance.
func TestMergeDecisionsDemotesPriorChosenOption(t *testing.T) {
	state := makePriorWithAlt(t, "Original X", "alt-B")
	state.Concerns = []Concern{{
		Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high",
		Text: "Original X is the wrong call on cost",
		Counterproposals: []CriticCounterproposal{{
			Option:   "Adopt alternative B",
			Argument: "Alt-B fits the cost ceiling at the GOALS-§3 scale.",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://b", Excerpt: "$30/mo"}},
		}},
		RelatedDecisionIDs: []string{"dec-x"},
	}}

	revised := RawDecisionProposal{
		ID: "dec-x", Title: "Adopt alternative B",
		Rationale: "Flipped after the cost critic's counterproposal.",
		Confidence: 0.85,
		Alternatives: []spec.Alternative{{
			Name: "alt-B", Rationale: "preserved from prior", RejectedBecause: "still considered",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "web", Reference: "https://b", Excerpt: "$30/mo"}},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, revised, 3)})

	var got RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &got))
	require.Len(t, got.Decisions, 1)
	d := got.Decisions[0]
	// Prior chosen "Original X" must appear as an alternative.
	var demoted *spec.Alternative
	for i := range d.Alternatives {
		if d.Alternatives[i].Name == "Original X" {
			demoted = &d.Alternatives[i]
		}
	}
	require.NotNil(t, demoted, "prior chosen option must be demoted into alternatives")
	assert.Equal(t, 3, demoted.RejectedAtIteration, "demotion carries iteration provenance")
	assert.Contains(t, demoted.RejectedByConcernText, "Original X is the wrong call",
		"demotion's concern-text provenance is the driving concern verbatim")
	assert.Contains(t, demoted.RejectedBecause, "fits the cost ceiling",
		"demotion's RejectedBecause carries the picking counterproposal's Argument")
}

// TestMergeDecisionsPreservesDroppedAlternatives — a revision that
// omits a prior alternative is APPLIED (not rejected); the merge
// layer folds the missing prior alternative back into the revised
// alternatives slice so the deliberation log is preserved
// mechanically.
//
// The prior pattern (reject-on-violation) caused the fifth winplan
// re-run to oscillate: each revision dropped at least one
// alternative; the rejection threw away the elaborator's Flip
// judgments + new counterproposal engagement along with the drop;
// next iteration the elaborator redid everything and frequently
// re-dropped. Mechanical preservation at the merge layer ends the
// oscillation — the elaborator's good work survives even when its
// alternatives discipline slips.
func TestMergeDecisionsPreservesDroppedAlternatives(t *testing.T) {
	state := makePriorWithAlt(t, "Original X", "alt-B")
	state.Concerns = []Concern{{
		Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high",
		Text:               "Original X is wrong",
		RelatedDecisionIDs: []string{"dec-x"},
	}}

	// Revision drops alt-B AND does not demote Original X. The
	// demote-as-alternative helper adds Original X back; the new
	// preservePriorAlternatives helper adds alt-B back. The
	// elaborator's new alt-D is appended on top.
	revised := RawDecisionProposal{
		ID: "dec-x", Title: "Different chosen", Rationale: "v2", Confidence: 0.85,
		Alternatives: []spec.Alternative{{
			Name: "alt-D", Rationale: "new", RejectedBecause: "new",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://d", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "web", Reference: "https://d", Excerpt: "e"}},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, revised, 3)})

	// The revision IS applied — the new chosen Title takes effect.
	var got RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &got))
	require.Len(t, got.Decisions, 1)
	d := got.Decisions[0]
	assert.Equal(t, "Different chosen", d.Title,
		"the revision applies (chosen Title is the elaborator's emission)")

	// The union of alternatives lands: alt-D (elaborator emission)
	// + Original X (demoted prior chosen) + alt-B (preserved prior
	// alternative).
	names := make(map[string]bool, len(d.Alternatives))
	for _, alt := range d.Alternatives {
		names[alt.Name] = true
	}
	assert.True(t, names["alt-D"], "elaborator's new alternative present")
	assert.True(t, names["Original X"], "prior chosen demoted to alternatives")
	assert.True(t, names["alt-B"], "prior alternative preserved by merge layer")
	assert.Len(t, d.Alternatives, 3, "union of (new + demoted + preserved) = 3 entries")

	// An integrity_critic notice records the preservation for
	// operator visibility — not a hard violation, but the operator
	// sees that the elaborator dropped entries the merge had to
	// preserve.
	var noticed bool
	for _, c := range state.Concerns {
		if c.AgentID == "integrity_critic" && c.Kind == "integrity" &&
			containsAll(c.Text, "omitted", "prior alternative", "auto-preserved") {
			noticed = true
			assert.Equal(t, "low", c.Severity,
				"preservation notice is informational, not high-severity")
		}
	}
	assert.True(t, noticed, "a low-severity integrity notice records the merge-side preservation")
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// TestMergeDecisionsFoldsRejectedCounterproposalsAsAlternatives —
// each driving-concern counterproposal that the elaborator did NOT
// pick becomes an alternative entry in the revised decision with the
// critic's Argument as Rationale and Citations preserved verbatim.
func TestMergeDecisionsFoldsRejectedCounterproposalsAsAlternatives(t *testing.T) {
	state := makePriorWithAlt(t, "Original X", "alt-B")
	cpC := CriticCounterproposal{
		Option:   "Adopt option C",
		Argument: "Option C addresses the latency budget the rationale fails to cite.",
		Citations: []spec.Citation{{Kind: "web", Reference: "https://c", Excerpt: "p99 < 50ms"}},
	}
	cpD := CriticCounterproposal{
		Option:   "Adopt option D",
		Argument: "Option D removes the multi-AZ premium without losing failover.",
		Citations: []spec.Citation{{Kind: "web", Reference: "https://d", Excerpt: "$15/mo with single-AZ"}},
	}
	state.Concerns = []Concern{{
		Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high",
		Text:               "Original X is wrong",
		Counterproposals:   []CriticCounterproposal{cpC, cpD},
		RelatedDecisionIDs: []string{"dec-x"},
	}}

	// Reject revision: elaborator keeps Original X as chosen but adds
	// both counterproposals as alternatives. Preserves the prior alt-B
	// to satisfy monotonicity.
	revised := RawDecisionProposal{
		ID: "dec-x", Title: "Original X", Rationale: "Reject revise — both counterproposals weighed and rejected.",
		Confidence: 0.8,
		Alternatives: []spec.Alternative{
			{Name: "alt-B", Rationale: "preserved", RejectedBecause: "preserved",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}}},
			{Name: "Adopt option C", Rationale: cpC.Argument, RejectedBecause: "elaborator rejected: violates the GOALS §5 simplicity invariant",
				Citations: cpC.Citations},
			{Name: "Adopt option D", Rationale: cpD.Argument, RejectedBecause: "elaborator rejected: introduces an availability regression",
				Citations: cpD.Citations},
		},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, revised, 3)})

	var got RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &got))
	require.Len(t, got.Decisions, 1)
	d := got.Decisions[0]

	var c, dAlt *spec.Alternative
	for i := range d.Alternatives {
		if d.Alternatives[i].Name == "Adopt option C" {
			c = &d.Alternatives[i]
		}
		if d.Alternatives[i].Name == "Adopt option D" {
			dAlt = &d.Alternatives[i]
		}
	}
	require.NotNil(t, c, "option C must appear in alternatives")
	require.NotNil(t, dAlt, "option D must appear in alternatives")
	assert.Equal(t, cpC.Argument, c.Rationale, "option C's Rationale carries the critic's Argument verbatim")
	assert.Equal(t, cpC.Citations, c.Citations, "option C's Citations preserve the critic's slice")
	assert.Equal(t, cpD.Argument, dAlt.Rationale, "option D's Rationale carries the critic's Argument verbatim")
}

// TestMergeDecisionsAutoFoldsMissingCounterproposalAlternatives —
// elaborator omits a counterproposal entirely; the merge auto-folds
// it into alternatives with a placeholder RejectedBecause and records
// an integrity-violation concern naming the omission.
func TestMergeDecisionsAutoFoldsMissingCounterproposalAlternatives(t *testing.T) {
	state := makePriorWithAlt(t, "Original X", "alt-B")
	cpC := CriticCounterproposal{
		Option:   "Adopt option C",
		Argument: "Option C addresses the latency budget the rationale fails to cite.",
		Citations: []spec.Citation{{Kind: "web", Reference: "https://c", Excerpt: "p99 < 50ms"}},
	}
	cpD := CriticCounterproposal{
		Option:   "Adopt option D",
		Argument: "Option D removes the multi-AZ premium without losing failover.",
		Citations: []spec.Citation{{Kind: "web", Reference: "https://d", Excerpt: "$15/mo with single-AZ"}},
	}
	state.Concerns = []Concern{{
		Status: ConcernStatusOpen, AgentID: "cost_critic", Severity: "high",
		Text:               "Original X is wrong",
		Counterproposals:   []CriticCounterproposal{cpC, cpD},
		RelatedDecisionIDs: []string{"dec-x"},
	}}

	// Reject revision but ELABORATOR OMITS option D from alternatives.
	// The merge auto-folds D in defensively.
	revised := RawDecisionProposal{
		ID: "dec-x", Title: "Original X", Rationale: "Reject revise — only C considered.",
		Confidence: 0.8,
		Alternatives: []spec.Alternative{
			{Name: "alt-B", Rationale: "preserved", RejectedBecause: "preserved",
				Citations: []spec.Citation{{Kind: "web", Reference: "https://x", Excerpt: "e"}}},
			{Name: "Adopt option C", Rationale: cpC.Argument, RejectedBecause: "elaborator rejected C explicitly",
				Citations: cpC.Citations},
		},
		Citations:  []spec.Citation{{Kind: "goals", Reference: "GOALS.md", Excerpt: "e"}},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, revised, 3)})

	var got RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &got))
	require.Len(t, got.Decisions, 1)
	d := got.Decisions[0]

	// Option D was auto-folded by the merge.
	var dAlt *spec.Alternative
	for i := range d.Alternatives {
		if d.Alternatives[i].Name == "Adopt option D" {
			dAlt = &d.Alternatives[i]
		}
	}
	require.NotNil(t, dAlt, "missing counterproposal D must be auto-folded by the merge")
	assert.Equal(t, cpD.Argument, dAlt.Rationale)
	assert.Equal(t, cpD.Citations, dAlt.Citations)
	assert.Contains(t, dAlt.RejectedBecause, "Elaborator did not engage",
		"auto-fold's RejectedBecause names the elaborator's omission")

	// An integrity_critic notice was appended.
	var notice *Concern
	for i := range state.Concerns {
		if state.Concerns[i].AgentID == "integrity_critic" && state.Concerns[i].Kind == "integrity" {
			notice = &state.Concerns[i]
		}
	}
	require.NotNil(t, notice, "merge must record an integrity_critic notice on auto-fold")
	assert.Contains(t, notice.Text, "auto-folded",
		"notice text names the auto-fold")
}

// TestMergeDecisionsDemotionPreservesAlternativeCitations — the prior
// chosen option's citations carry into its alternative entry.
func TestMergeDecisionsDemotionPreservesAlternativeCitations(t *testing.T) {
	prior := RawDecisionProposal{
		ID: "dec-x", Title: "Original X", Rationale: "old", Confidence: 0.7,
		ArchitectRationale: "old chosen architectural reasoning",
		Alternatives: []spec.Alternative{{
			Name: "alt-B", Rationale: "considered", RejectedBecause: "lost",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://b", Excerpt: "e"}},
		}},
		Citations: []spec.Citation{
			{Kind: "goals", Reference: "GOALS.md", Excerpt: "first citation"},
			{Kind: "web", Reference: "https://x", Excerpt: "second citation"},
		},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	raw, err := json.Marshal(RawSpecProposal{Decisions: []RawDecisionProposal{prior}})
	require.NoError(t, err)
	state := &PlanningState{RawProposal: string(raw)}

	revised := RawDecisionProposal{
		ID: "dec-x", Title: "Revised X",
		Rationale:  "v2",
		Confidence: 0.85,
		Alternatives: []spec.Alternative{{
			Name: "alt-B", Rationale: "preserved", RejectedBecause: "preserved",
			Citations: []spec.Citation{{Kind: "web", Reference: "https://b", Excerpt: "e"}},
		}},
		Citations:  []spec.Citation{{Kind: "web", Reference: "https://new", Excerpt: "e"}},
		Axes:       []string{"axis-a"},
		SurfacedBy: []string{"feat-x"},
	}
	mergeDecisions(state, []RoundResult{makeDecisionResult(t, revised, 4)})

	var got RawSpecProposal
	require.NoError(t, json.Unmarshal([]byte(state.RawProposal), &got))
	require.Len(t, got.Decisions, 1)
	d := got.Decisions[0]

	var demoted *spec.Alternative
	for i := range d.Alternatives {
		if d.Alternatives[i].Name == "Original X" {
			demoted = &d.Alternatives[i]
		}
	}
	require.NotNil(t, demoted)
	assert.Equal(t, prior.Citations, demoted.Citations,
		"demoted prior chosen carries the prior decision's citations verbatim")
	assert.Equal(t, "old chosen architectural reasoning", demoted.Rationale,
		"demoted prior chosen carries the prior ArchitectRationale as its Rationale")
}
