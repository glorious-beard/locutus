// DJ-128 — tests for degenerateCriticIssueValidator and the related
// CriticIssue / CriticCounterproposal schema discipline.

package agent

import (
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
)

// goodIssue returns a fully-grounded CriticIssue suitable as a
// baseline that the validator accepts. Tests then mutate one field
// to assert the validator catches the specific degenerate case.
func goodIssue() CriticIssue {
	return CriticIssue{
		Weakness: "The dec-postgres rationale does not engage with the $150/mo ceiling in GOALS §3.",
		Evidence: "GOALS §3 names the ceiling explicitly and the rationale only addresses query performance.",
		Counterproposals: []CriticCounterproposal{{
			Option:   "Single-instance RDS Postgres on t4g.small reserved",
			Argument: "A reserved t4g.small lands under $30/mo and meets the JSONB requirement the analytics roadmap depends on.",
			Citations: []spec.Citation{{
				Kind:      "web",
				Reference: "https://aws.amazon.com/rds/postgresql/pricing/",
				Excerpt:   "db.t4g.small reserved (1-year, no upfront): $0.034/hr in us-east-1",
			}},
		}},
	}
}

func TestDegenerateCriticIssueValidatorAcceptsGoodIssue(t *testing.T) {
	ci := &CriticIssues{Issues: []CriticIssue{goodIssue()}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.False(t, deg, "good issue should pass; reason=%q", reason)
}

func TestDegenerateCriticIssueValidatorAcceptsEmptyIssues(t *testing.T) {
	// Empty issues = "critic found nothing to surface" — not degenerate.
	ci := &CriticIssues{}
	_, deg := degenerateCriticIssueValidator(ci)
	assert.False(t, deg)
}

func TestDegenerateCriticIssueValidatorAcceptsNil(t *testing.T) {
	_, deg := degenerateCriticIssueValidator(nil)
	assert.False(t, deg)
}

func TestDegenerateCriticIssueValidatorRejectsShortWeakness(t *testing.T) {
	issue := goodIssue()
	issue.Weakness = "bad"
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "weakness")
}

func TestDegenerateCriticIssueValidatorRejectsPlaceholderWeakness(t *testing.T) {
	issue := goodIssue()
	issue.Weakness = "dummy"
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "placeholder")
}

func TestDegenerateCriticIssueValidatorRejectsShortEvidence(t *testing.T) {
	issue := goodIssue()
	issue.Evidence = "nope"
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "evidence")
}

func TestDegenerateCriticIssueValidatorRejectsEmptyCounterproposals(t *testing.T) {
	issue := goodIssue()
	issue.Counterproposals = nil
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "counterproposals")
}

func TestDegenerateCriticIssueValidatorRejectsPlaceholderOption(t *testing.T) {
	issue := goodIssue()
	issue.Counterproposals[0].Option = "use something else"
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "option")
}

func TestDegenerateCriticIssueValidatorRejectsShortOption(t *testing.T) {
	issue := goodIssue()
	issue.Counterproposals[0].Option = "AB"
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "option")
}

func TestDegenerateCriticIssueValidatorRejectsShortArgument(t *testing.T) {
	issue := goodIssue()
	issue.Counterproposals[0].Argument = "better"
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "argument")
}

// TestCriticCounterproposalAllowsNeedsInvestigationSentinel — the
// literal "needs investigation" Option is the one exception to the
// non-empty-citations rule; empty Citations are permitted in that
// exact case (DJ-128 sentinel).
func TestCriticCounterproposalAllowsNeedsInvestigationSentinel(t *testing.T) {
	issue := goodIssue()
	issue.Counterproposals = []CriticCounterproposal{{
		Option:   "needs investigation",
		Argument: "The proposal's claim warrants follow-up research the critic cannot do in this turn.",
		// Citations intentionally empty — sentinel exception.
	}}
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.False(t, deg, "sentinel Option must permit empty citations; reason=%q", reason)
}

// TestCriticCounterproposalRequiresCitationsForNonSentinel — concrete
// counterproposals must carry citations even when the Option and
// Argument are otherwise valid.
func TestCriticCounterproposalRequiresCitationsForNonSentinel(t *testing.T) {
	issue := goodIssue()
	issue.Counterproposals[0].Citations = nil
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "citations")
}

// TestCriticCounterproposalRejectsEmptyCitationReference — citations
// with a blank reference can't carry provenance.
func TestCriticCounterproposalRejectsEmptyCitationReference(t *testing.T) {
	issue := goodIssue()
	issue.Counterproposals[0].Citations = []spec.Citation{{
		Kind:      "web",
		Reference: "  ",
		Excerpt:   "something",
	}}
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "reference")
}

// TestCriticCounterproposalRejectsWebCitationMissingExcerpt — web
// pages change, so the excerpt is the durable evidence.
func TestCriticCounterproposalRejectsWebCitationMissingExcerpt(t *testing.T) {
	issue := goodIssue()
	issue.Counterproposals[0].Citations = []spec.Citation{{
		Kind:      "web",
		Reference: "https://example.com/pricing",
		// Excerpt intentionally empty.
	}}
	ci := &CriticIssues{Issues: []CriticIssue{issue}}
	reason, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg)
	assert.Contains(t, reason, "excerpt")
}

// TestDegenerateCriticIssueValidatorRejectsUngroundedCounterproposals
// — fixture mirroring the plan's "use something else" placeholder
// case; rejected and a counterexample with grounded prose + a real
// web citation is accepted (the goodIssue baseline already covers
// the accept case but assert it explicitly here too).
func TestDegenerateCriticIssueValidatorRejectsUngroundedCounterproposals(t *testing.T) {
	ungrounded := CriticIssue{
		Weakness: "The proposal does not engage with the cost ceiling.",
		Evidence: "GOALS §3 names a $150/mo ceiling that the proposal does not cite.",
		Counterproposals: []CriticCounterproposal{{
			Option:    "use something else",
			Argument:  "better",
			Citations: nil,
		}},
	}
	ci := &CriticIssues{Issues: []CriticIssue{ungrounded}}
	_, deg := degenerateCriticIssueValidator(ci)
	assert.True(t, deg, "ungrounded placeholder counterproposal must be rejected")

	// Counterexample: a grounded version of the same issue is accepted.
	groundedCI := &CriticIssues{Issues: []CriticIssue{goodIssue()}}
	reason, deg := degenerateCriticIssueValidator(groundedCI)
	assert.False(t, deg, "grounded counterproposal must be accepted; reason=%q", reason)
}

// TestIsAdvisoryCounterproposalMenuTrueOnAllSentinel — every
// counterproposal carrying the sentinel marks the menu advisory.
func TestIsAdvisoryCounterproposalMenuTrueOnAllSentinel(t *testing.T) {
	cps := []CriticCounterproposal{
		{Option: "needs investigation"},
		{Option: "NEEDS INVESTIGATION"}, // case-insensitive
	}
	assert.True(t, isAdvisoryCounterproposalMenu(cps))
}

// TestIsAdvisoryCounterproposalMenuFalseOnMix — a single concrete
// counterproposal keeps the menu reviseable.
func TestIsAdvisoryCounterproposalMenuFalseOnMix(t *testing.T) {
	cps := []CriticCounterproposal{
		{Option: "needs investigation"},
		{Option: "Adopt Aurora Serverless v2"},
	}
	assert.False(t, isAdvisoryCounterproposalMenu(cps))
}

// TestIsAdvisoryCounterproposalMenuFalseOnEmpty — empty slice can't
// be advisory (it's degenerate, but that's the validator's call).
func TestIsAdvisoryCounterproposalMenuFalseOnEmpty(t *testing.T) {
	assert.False(t, isAdvisoryCounterproposalMenu(nil))
}
