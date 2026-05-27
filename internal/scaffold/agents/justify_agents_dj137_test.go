// DJ-137 phase 1 — frontmatter audit assertions on the five
// justify-related agent prompts. Locks in the cleanup that drops
// council-era `output_schema:` fields referencing Go types that
// no longer exist (DJ-135 phase 5 retired them).

package agents_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// justifyAgentSet — the five agent prompts the DJ-137 justification
// activity may dispatch (advocate + challenger + researcher are v1;
// splitter + synthesizer are kept-published-but-unused for ad-hoc
// operator invocation per the DJ's out-of-scope notes).
var justifyAgentSet = []string{
	"spec-advocate.md",
	"spec-challenger.md",
	"justify-researcher.md",
	"justify-splitter.md",
	"justify-synthesizer.md",
}

// TestJustifyAgents_DropOutputSchemaFrontmatter — none of the five
// agents carry the council-era `output_schema:` field. The Go
// schema types it referenced (AdversarialDefense, ChallengeBrief,
// ResearchBrief, ChallengeSplit, SynthesisVerdict) retired with
// the council in DJ-135 phase 5; the prompts' prose carries the
// output shape now (which sections to write, which fields to
// populate).
func TestJustifyAgents_DropOutputSchemaFrontmatter(t *testing.T) {
	for _, name := range justifyAgentSet {
		t.Run(name, func(t *testing.T) {
			body := loadPrompt(t, name)
			fm, _ := splitFrontmatter(t, body)
			assert.NotContains(t, fm, "output_schema:",
				"%s carries output_schema: in frontmatter — DJ-137 phase 1 drops it because the Go type retired with the council", name)
		})
	}
}

// TestJustifyAgents_DropDeadSchemaReferencesInBody — the prompt
// bodies must not refer to the retired Go schema type names. The
// agents tell the orchestrator the output shape in prose; a stale
// "emit a ChallengeBrief object" line would mislead a reader and
// hint at machinery that no longer exists.
func TestJustifyAgents_DropDeadSchemaReferencesInBody(t *testing.T) {
	deadTypes := []string{
		"AdversarialDefense",
		"ChallengeBrief",
		"ResearchBrief",
		"ChallengeSplit",
		"SynthesisVerdict",
	}
	for _, name := range justifyAgentSet {
		t.Run(name, func(t *testing.T) {
			body := loadPrompt(t, name)
			_, prose := splitFrontmatter(t, body)
			for _, dt := range deadTypes {
				assert.NotContains(t, prose, dt,
					"%s body references defunct schema type %q — describe the output shape in prose instead",
					name, dt)
			}
		})
	}
}

// TestJustifyAgents_HaveHyphenatedID — DJ-135 phase 5 invariant
// (hyphenated agent ids) applies to the justify set too. Catches
// regressions where a copy-paste from the council-era prompts
// brings back snake_case.
func TestJustifyAgents_HaveHyphenatedID(t *testing.T) {
	for _, name := range justifyAgentSet {
		t.Run(name, func(t *testing.T) {
			body := loadPrompt(t, name)
			fm, _ := splitFrontmatter(t, body)
			// Find the "id:" line.
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "id:") {
					continue
				}
				id := strings.TrimSpace(strings.TrimPrefix(line, "id:"))
				assert.False(t, strings.Contains(id, "_"),
					"%s id %q contains underscores — DJ-135 phase 5 invariant requires hyphenated ids", name, id)
				return
			}
			t.Fatalf("%s: no id: line in frontmatter", name)
		})
	}
}

// splitFrontmatter splits a markdown prompt into (frontmatter,
// body) at the closing `---` line. Returns the frontmatter region
// (the YAML block between the two `---` delimiters) and the prose
// body. Crashes the test if no closing `---` is found.
func splitFrontmatter(t *testing.T, body string) (string, string) {
	t.Helper()
	require.True(t, strings.HasPrefix(body, "---\n"), "prompt must open with `---` frontmatter delimiter")
	rest := body[len("---\n"):]
	close := strings.Index(rest, "\n---\n")
	require.True(t, close >= 0, "prompt must carry closing `---` after frontmatter")
	return rest[:close], rest[close+len("\n---\n"):]
}
