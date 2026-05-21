package agent

import (
	"fmt"
	"strings"
)

// needsInvestigationSentinel is the literal Option value a critic may
// emit when it sees a real problem but genuinely cannot name a
// concrete alternative (DJ-128). The validator permits empty Citations
// only on counterproposals whose Option equals this sentinel exactly;
// the merge layer marks concerns advisory when every counterproposal
// is the sentinel.
const needsInvestigationSentinel = "needs investigation"

// criticPlaceholderTokens mirrors challengerPlaceholderTokens but covers
// the additional one-letter / one-word patterns observed in critic
// outputs that the schema's minItems / required tags do not catch on
// their own.
var criticPlaceholderTokens = map[string]struct{}{
	"dummy":                {},
	"placeholder":          {},
	"todo":                 {},
	"tbd":                  {},
	"foo":                  {},
	"bar":                  {},
	"baz":                  {},
	"example":              {},
	"sample":               {},
	"lorem":                {},
	"ipsum":                {},
	"n/a":                  {},
	"none":                 {},
	"...":                  {},
	"x":                    {},
	"y":                    {},
	"z":                    {},
	"use something else":   {},
	"alternative":          {},
	"another option":       {},
	"other":                {},
	"something better":     {},
	"a different approach": {},
}

// minCriticFieldLen is the floor for Weakness / Evidence / Argument
// length in runes. A real critic finding is at least a short sentence;
// one-word answers ("better", "no") are placeholders that should
// trigger a corrective retry. Matches challenger discipline.
const minCriticFieldLen = 20

// minOptionLen is the floor for CriticCounterproposal.Option in runes.
// Real options name a concrete product / pattern / behavior — "Aurora
// Serverless v2" or "drop to single-region ECS" — which is shorter
// than a full sentence but still longer than one word.
const minOptionLen = 4

// degenerateCriticIssueValidator reports whether a CriticIssues output
// looks like the schema-skeleton failure mode (one-word placeholder
// counterproposals; empty arguments; missing citations) rather than a
// real structured critique. Returns (reason, true) on first detected
// degeneracy; ("", false) when every issue passes muster.
//
// Triggers (any one is sufficient):
//
//   - Any Weakness or Evidence field shorter than minCriticFieldLen
//     runes or in the placeholder set.
//   - Any Counterproposal whose Option is shorter than minOptionLen or
//     in the placeholder set (the sentinel "needs investigation" is
//     not in the placeholder set and is permitted).
//   - Any Counterproposal whose Argument is shorter than
//     minCriticFieldLen or in the placeholder set.
//   - A non-sentinel Counterproposal with zero Citations.
//   - A citation with an empty Reference.
//   - A web-kind citation missing its Excerpt (the URL alone is not
//     load-bearing — pages change, so excerpts are required for web).
//
// Empty Issues array is NOT degenerate — that's the legitimate
// "critic found nothing to surface" outcome. The convergence loop
// reads it as zero-finding-this-iteration.
func degenerateCriticIssueValidator(ci *CriticIssues) (string, bool) {
	if ci == nil || len(ci.Issues) == 0 {
		return "", false
	}
	for i, issue := range ci.Issues {
		if reason, deg := degenerateProse("weakness", issue.Weakness); deg {
			return fmt.Sprintf("issue[%d].%s", i, reason), true
		}
		if reason, deg := degenerateProse("evidence", issue.Evidence); deg {
			return fmt.Sprintf("issue[%d].%s", i, reason), true
		}
		if len(issue.Counterproposals) == 0 {
			return fmt.Sprintf("issue[%d].counterproposals is empty (minItems=1)", i), true
		}
		for j, cp := range issue.Counterproposals {
			if reason, deg := degenerateCounterproposal(cp); deg {
				return fmt.Sprintf("issue[%d].counterproposals[%d].%s", i, j, reason), true
			}
		}
	}
	return "", false
}

// degenerateProse runs the placeholder / minimum-length check used for
// Weakness / Evidence / Argument. Returns ("<label> is ...", true) on
// degeneracy.
func degenerateProse(label, val string) (string, bool) {
	trimmed := strings.TrimSpace(val)
	lower := strings.ToLower(trimmed)
	if _, isPlaceholder := criticPlaceholderTokens[lower]; isPlaceholder {
		return fmt.Sprintf("%s is a known placeholder token %q", label, trimmed), true
	}
	if len([]rune(trimmed)) < minCriticFieldLen {
		return fmt.Sprintf("%s is shorter than %d runes (%q)", label, minCriticFieldLen, trimmed), true
	}
	return "", false
}

// degenerateCounterproposal validates one CriticCounterproposal.
// Returns ("<field> is ...", true) on degeneracy.
func degenerateCounterproposal(cp CriticCounterproposal) (string, bool) {
	option := strings.TrimSpace(cp.Option)
	optionLower := strings.ToLower(option)
	isSentinel := optionLower == needsInvestigationSentinel

	if option == "" {
		return "option is empty", true
	}
	if _, isPlaceholder := criticPlaceholderTokens[optionLower]; isPlaceholder {
		return fmt.Sprintf("option is a known placeholder token %q", option), true
	}
	if !isSentinel && len([]rune(option)) < minOptionLen {
		return fmt.Sprintf("option is shorter than %d runes (%q)", minOptionLen, option), true
	}

	if reason, deg := degenerateProse("argument", cp.Argument); deg {
		return reason, true
	}

	if isSentinel {
		// Sentinel counterproposals are permitted to carry empty
		// Citations — that's the whole point of the sentinel. The
		// merge layer marks the resulting concern Advisory.
		return "", false
	}

	if len(cp.Citations) == 0 {
		return "citations is empty (minItems=1 on non-sentinel options)", true
	}
	for k, c := range cp.Citations {
		if strings.TrimSpace(c.Reference) == "" {
			return fmt.Sprintf("citations[%d].reference is empty", k), true
		}
		if c.Kind == "web" && strings.TrimSpace(c.Excerpt) == "" {
			return fmt.Sprintf("citations[%d] is kind=web but missing excerpt (web pages change; the excerpt is the durable evidence)", k), true
		}
	}
	return "", false
}

// isAdvisoryCounterproposalMenu reports whether every counterproposal
// in the slice carries the literal "needs investigation" sentinel — the
// signal that the concern is advisory-only (DJ-128). Empty slices
// return false; concerns with a mix of sentinel and concrete
// counterproposals return false (the concrete entries make it
// reviseable; the sentinel is preserved in the menu for human review).
func isAdvisoryCounterproposalMenu(cps []CriticCounterproposal) bool {
	if len(cps) == 0 {
		return false
	}
	for _, cp := range cps {
		if strings.ToLower(strings.TrimSpace(cp.Option)) != needsInvestigationSentinel {
			return false
		}
	}
	return true
}
