// DJ-133 — assertions on the rewritten spec-decision-elaborator id
// section. Under DJ-133 the elaborator no longer mints a slug from the
// chosen option; it copies the axis ID verbatim into the decision's id.

package agents_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// idSection returns the "### id" subsection of the elaborator prompt
// (between the heading line at column 0 and the next heading) so the
// assertions don't get satisfied by stray inline `### id`
// cross-references elsewhere in the file.
func idSection(t *testing.T) string {
	t.Helper()
	body := loadPrompt(t, "spec-decision-elaborator.md")
	// Anchor on a heading that starts at column 0 — a leading newline
	// before "### id" disambiguates the heading line from an inline
	// `### id` backtick reference.
	const anchor = "\n### id\n"
	start := strings.Index(body, anchor)
	if start < 0 {
		return ""
	}
	rest := body[start+len(anchor):]
	for _, term := range []string{"\n### ", "\n## ", "\n# "} {
		if idx := strings.Index(rest, term); idx >= 0 {
			rest = rest[:idx]
		}
	}
	return rest
}

// TestDecisionElaboratorPromptDescribesAxisAsID — the rewritten id
// section instructs the model to copy the axis ID verbatim and shows
// an axis-shaped example.
func TestDecisionElaboratorPromptDescribesAxisAsID(t *testing.T) {
	section := idSection(t)
	assert.NotEmpty(t, section, "### id section must exist in spec-decision-elaborator.md")
	assert.Contains(t, section, "Copy the input axis ID verbatim",
		"DJ-133: the id section must instruct the model to copy the axis id verbatim")
	assert.Contains(t, section, "dec-oltp-store",
		"DJ-133: the id section must include an axis-shaped example id")
}

// TestDecisionElaboratorPromptDropsSlugMinting — the legacy
// slug-from-chosen-option wording is gone from the id section. The
// pre-DJ-133 prompt said "A stable slug derived from the chosen
// option" and named the reconciler-suffix path; both phrases are
// load-bearing primes for the model to mint a chosen-option-shaped id.
func TestDecisionElaboratorPromptDropsSlugMinting(t *testing.T) {
	section := idSection(t)
	assert.NotEmpty(t, section)
	assert.NotContains(t, section, "derived from the chosen option",
		"DJ-133: 'derived from the chosen option' wording must be retired from the id section")
	assert.NotContains(t, section, "suffix with `-2`",
		"DJ-133: the reconciler-suffix language must be retired from the id section")
	assert.NotContains(t, section, "you pick the natural slug",
		"DJ-133: the 'pick the natural slug' framing must be retired")
}
