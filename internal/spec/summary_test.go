package spec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHasSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"whitespace only", "   \t\n", false},
		{"single sentence", "A short summary.", true},
		{"no terminal punctuation", "missing period", true}, // HasSummary doesn't check shape
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, HasSummary(tc.in))
		})
	}
}

func TestIsWellFormedSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty rejected", "", false},
		{"whitespace rejected", "   ", false},
		{"one sentence terminated", "A clear one-line summary.", true},
		{"question mark accepted", "Why does this exist?", true},
		{"exclamation accepted", "Critical security choice!", true},
		{"missing punctuation", "no terminal punctuation here", false},
		{"too long", strings.Repeat("a", SummaryMaxChars) + ".", false}, // length includes the period
		{"at limit", strings.Repeat("a", SummaryMaxChars-1) + ".", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsWellFormedSummary(tc.in))
		})
	}
}
