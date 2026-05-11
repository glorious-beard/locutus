package spec

import "strings"

// SummaryMaxChars is the soft upper bound on a one-sentence summary.
// Violations are logged but not rejected — the LLM generation cost is
// already paid by the time the value reaches us, so rejecting forces
// another round-trip to fix what is almost always a cosmetic miss.
const SummaryMaxChars = 600

// IsWellFormedSummary reports whether s passes the soft sanity checks
// (length and sentence-terminal punctuation). Used by writers that
// want to log a warning when an authoring agent produces a malformed
// summary; never used to reject the write itself.
func IsWellFormedSummary(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if len(s) > SummaryMaxChars {
		return false
	}
	last := s[len(s)-1]
	return last == '.' || last == '!' || last == '?'
}

// HasSummary reports whether a node's Summary field is non-empty after
// whitespace trim. The hard required invariant — the SummariesPresent
// prereq fills any node where HasSummary returns false.
func HasSummary(s string) bool {
	return strings.TrimSpace(s) != ""
}
