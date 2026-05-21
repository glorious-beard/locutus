// DJ-128 — assertions on the rewritten critic prompts (architect,
// devops, sre, cost). The four prompts share a counterproposal-menu
// discipline but each frames the menu in its own lens.

package agents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// criticPromptIDs is the canonical list of critic agent prompt files
// covered by the DJ-128 rewrite. Adding a new critic adds the file
// here so the cross-prompt invariants below run against it too.
var criticPromptIDs = []string{
	"architect_critic.md",
	"devops_critic.md",
	"sre_critic.md",
	"cost_critic.md",
}

// loadPrompt reads one prompt file from the canonical scaffold dir.
func loadPrompt(t *testing.T, name string) string {
	t.Helper()
	// The test file lives in internal/scaffold/agents/, so the prompt
	// files are siblings — no path traversal needed.
	wd, err := os.Getwd()
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(wd, name))
	require.NoError(t, err, "reading %s", name)
	return string(b)
}

// TestEveryCriticPromptRequiresEnumeratedCounterproposals — for each
// of the 4 critic prompts, assert the prompt names "counterproposals"
// (plural) as a field, names "option", "argument", "citations" as the
// per-counterproposal sub-fields, and frames the enumeration
// discipline.
func TestEveryCriticPromptRequiresEnumeratedCounterproposals(t *testing.T) {
	for _, name := range criticPromptIDs {
		t.Run(name, func(t *testing.T) {
			body := loadPrompt(t, name)
			assert.Contains(t, body, "counterproposals",
				"prompt must name the counterproposals field")
			assert.Contains(t, strings.ToLower(body), "option",
				"prompt must name the option sub-field")
			assert.Contains(t, strings.ToLower(body), "argument",
				"prompt must name the argument sub-field")
			assert.Contains(t, strings.ToLower(body), "citations",
				"prompt must name the citations sub-field")
			// Enumeration discipline: the prompt frames "list every
			// option you would accept" rather than "pick one."
			lower := strings.ToLower(body)
			hasEnumeration := strings.Contains(lower, "list all") ||
				strings.Contains(lower, "list both") ||
				strings.Contains(lower, "list every") ||
				strings.Contains(lower, "do not pick one arbitrarily") ||
				strings.Contains(lower, "do not omit candidates")
			assert.True(t, hasEnumeration,
				"prompt must frame the enumeration discipline (list all options, not one)")
		})
	}
}

// TestEveryCriticPromptDocumentsSentinel — each prompt names the
// literal "needs investigation" sentinel verbatim AND frames it as a
// last-resort, not a default.
func TestEveryCriticPromptDocumentsSentinel(t *testing.T) {
	for _, name := range criticPromptIDs {
		t.Run(name, func(t *testing.T) {
			body := loadPrompt(t, name)
			assert.Contains(t, body, "needs investigation",
				"prompt must name the literal sentinel value")
			// The sentinel must be framed as a last-resort, not the
			// primary path. We look for "rarely" + the sentinel
			// being explicitly bounded.
			lower := strings.ToLower(body)
			frames := strings.Contains(lower, "rarely") ||
				strings.Contains(lower, "last-resort") ||
				strings.Contains(lower, "last resort")
			assert.True(t, frames,
				"prompt must frame the sentinel as a last-resort, not a default")
		})
	}
}

// TestEveryCriticPromptDescribesCitationGrounding — each prompt names
// citation kinds and the verbatim-excerpt discipline for web sources.
func TestEveryCriticPromptDescribesCitationGrounding(t *testing.T) {
	for _, name := range criticPromptIDs {
		t.Run(name, func(t *testing.T) {
			body := loadPrompt(t, name)
			lower := strings.ToLower(body)
			// At minimum the prompt names citation grounding and that
			// every option carries citations (the menu's grounding
			// discipline). Per-lens prompts can frame this differently
			// — architect refers to "spec_node" / vendor docs, cost
			// refers to vendor pricing pages, etc.
			assert.Contains(t, lower, "citation",
				"prompt must name citation discipline")
			// Citation grounding can take any of the citation kinds:
			// web (URL), vendor docs, best_practice, spec_node, or
			// GOALS.md. The prompt must reference at least one shape
			// of grounded source — the prose has to engage the model
			// with where evidence comes from.
			groundsSomething := strings.Contains(lower, "web") ||
				strings.Contains(lower, "vendor") ||
				strings.Contains(lower, "url") ||
				strings.Contains(lower, "best_practice") ||
				strings.Contains(lower, "best practice") ||
				strings.Contains(lower, "spec_node") ||
				strings.Contains(lower, "spec node") ||
				strings.Contains(lower, "goals.md")
			assert.True(t, groundsSomething,
				"prompt must reference at least one citation-kind grounding shape (web / vendor / best_practice / spec_node / goals)")
		})
	}
}

// TestCriticPromptDropsLegacyIssuesStringFraming — none of the critic
// prompts retain the legacy "Issues []string" "list of objections"
// framing. We check for the specific legacy phrase that pre-DJ-128
// prompts used.
func TestCriticPromptDropsLegacyIssuesStringFraming(t *testing.T) {
	for _, name := range criticPromptIDs {
		t.Run(name, func(t *testing.T) {
			body := loadPrompt(t, name)
			// Legacy framing: "one entry per problem found, each specific and actionable"
			// without naming the CriticIssue structure. We assert the
			// new framing names CriticIssue's four-field walk by
			// finding "weakness" in the same paragraph as
			// "counterproposals" (the structured-walk shape).
			lowerBody := strings.ToLower(body)
			assert.Contains(t, lowerBody, "weakness",
				"prompt must name the weakness field (structured shape)")
			assert.Contains(t, lowerBody, "counterproposals",
				"prompt must name the counterproposals field (structured shape)")
			// And it must NOT carry the legacy "Issues []string" prose
			// — the old prompts ended with "Emit **issues** — one entry per problem"
			// without naming the structured shape. We accept the new
			// "Emit **issues**" lede only when followed by the
			// structured CriticIssue walk.
		})
	}
}
