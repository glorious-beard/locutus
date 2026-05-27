// DJ-139 phase 5 — assertions on the spec-goal-diff-matcher prompt.
// The matcher's prompt is load-bearing: its structured output drives
// every `refine goals` sync (Phase 6). These tests lock in the
// prompt's shape so refactors don't accidentally drop the
// load-bearing pieces (the four diff categories, the source_clause
// matching anchor, realistic example payloads per anti-pattern #5).

package agents_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const specGoalDiffMatcherFilename = "spec-goal-diff-matcher.md"

// TestSpecGoalDiffMatcherPromptLoadsAndIsNonEmpty — the canonical
// agent file exists, is readable, and carries enough body for the
// matcher's job. A short file would mean the prompt skipped one of
// the four diff categories or the matching discipline.
func TestSpecGoalDiffMatcherPromptLoadsAndIsNonEmpty(t *testing.T) {
	body := loadPrompt(t, specGoalDiffMatcherFilename)
	assert.Greater(t, len(body), 2000, "prompt must carry enough body to describe matching + four diff categories + edge cases")
}

// TestSpecGoalDiffMatcherPromptFrontmatterHasHyphenatedID — the
// canonical id matches the filename and the DJ-135 phase 5
// hyphenation invariant. The all-agents sweep in
// hyphenated_ids_dj135_test.go also covers this; the per-agent
// assertion catches drift the moment it lands.
func TestSpecGoalDiffMatcherPromptFrontmatterHasHyphenatedID(t *testing.T) {
	body := loadPrompt(t, specGoalDiffMatcherFilename)
	fm, _ := splitFrontmatter(t, body)
	var found bool
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "id:") {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		assert.Equal(t, "spec-goal-diff-matcher", id, "id must be the canonical hyphenated form")
		found = true
		break
	}
	require.True(t, found, "frontmatter must declare id:")
}

// TestSpecGoalDiffMatcherPromptDescribesSixDiffCategories — the
// matcher's output is a structured diff with exactly six categories
// (Unchanged, Modified, Deleted, Added, Promoted, Contradicted). The
// prompt must walk each one so the model knows the full shape it's emitting.
func TestSpecGoalDiffMatcherPromptDescribesSixDiffCategories(t *testing.T) {
	body := loadPrompt(t, specGoalDiffMatcherFilename)
	_, prose := splitFrontmatter(t, body)
	for _, category := range []string{"unchanged", "modified", "deleted", "added", "promoted", "contradicted"} {
		assert.Contains(t, strings.ToLower(prose), category,
			"prompt must describe the %q diff category", category)
	}
}

// DJ-141 — the matcher must distinguish anchored (source_clause-backed)
// from unanchored (origin-backed) nodes, restrict delete-by-absence to
// anchored nodes, and describe the promoted/contradicted moves.
func TestSpecGoalDiffMatcherPromptDescribesProvenance(t *testing.T) {
	body := loadPrompt(t, specGoalDiffMatcherFilename)
	lower := strings.ToLower(body)
	for _, term := range []string{"anchored", "unanchored", "origin", "promoted", "contradicted"} {
		assert.Contains(t, lower, term, "matcher prompt must describe %q (DJ-141)", term)
	}
	assert.Contains(t, lower, "never deleted",
		"prompt must state unanchored nodes are never deleted by absence from GOALS.md")
}

// TestSpecGoalDiffMatcherPromptReferencesSourceClauseAnchor — the
// matcher matches existing nodes to current GOALS.md claims by
// comparing against `source_clause`. The prompt must name the
// anchor explicitly so the model knows what field to compare on.
func TestSpecGoalDiffMatcherPromptReferencesSourceClauseAnchor(t *testing.T) {
	body := loadPrompt(t, specGoalDiffMatcherFilename)
	_, prose := splitFrontmatter(t, body)
	assert.Contains(t, prose, "source_clause",
		"prompt must name source_clause as the matching anchor")
}

// TestSpecGoalDiffMatcherPromptUsesRealisticExamplePayloads — per
// anti-pattern #5 in docs/agent-conventions.md, example ids must be
// realistic (winplan-style) rather than placeholders. Example
// payloads that say `foo` / `bar` / `placeholder` prime the
// schema-skeleton failure mode.
func TestSpecGoalDiffMatcherPromptUsesRealisticExamplePayloads(t *testing.T) {
	body := loadPrompt(t, specGoalDiffMatcherFilename)
	_, prose := splitFrontmatter(t, body)
	// At least one of the realistic winplan-style ids must appear.
	realistic := []string{
		"agoal-fundraising",
		"goal-strategic-planning-tool",
		"agoal-budget-tracking",
		"agoal-events",
		"agoal-people-crm",
		"goal-school-board-through-state-leg-tier",
	}
	var hits int
	for _, id := range realistic {
		if strings.Contains(prose, id) {
			hits++
		}
	}
	assert.GreaterOrEqual(t, hits, 2, "prompt should use at least two realistic winplan-style example ids (per anti-pattern #5)")

	// Negative: placeholder ids would prime the schema-skeleton mode.
	for _, placeholder := range []string{"goal-foo", "agoal-foo", "goal-bar", "agoal-bar", "goal-placeholder", "agoal-placeholder", "goal-tbd", "agoal-tbd"} {
		assert.NotContains(t, prose, placeholder,
			"prompt must not use placeholder example id %q (anti-pattern #5)", placeholder)
	}
}

// TestSpecGoalDiffMatcherPromptCoversEdgeCases — claim split, claim
// merge, and significant rephrasing are the three edge cases the
// matcher handles. The prompt names each so the model knows how to
// resolve them on the source_clause-matching pass.
func TestSpecGoalDiffMatcherPromptCoversEdgeCases(t *testing.T) {
	body := loadPrompt(t, specGoalDiffMatcherFilename)
	_, prose := splitFrontmatter(t, body)
	lower := strings.ToLower(prose)
	assert.Contains(t, lower, "split", "prompt must cover the claim-split edge case")
	assert.Contains(t, lower, "merge", "prompt must cover the claim-merge edge case")
	assert.Contains(t, lower, "rephras", "prompt must cover significant-rephrasing (substring rephras matches rephrase/rephrasing/rephrased)")
}

// TestSpecGoalDiffMatcherPromptDescribesIDPreservation — ID
// stability is load-bearing for DJ-139: existing dec/feat/strat/app
// citations on .advances / .respects reference goal-layer ids. A
// rephrased claim that semantically continues an existing goal
// keeps the same id; only genuinely-new claims mint new ids.
func TestSpecGoalDiffMatcherPromptDescribesIDPreservation(t *testing.T) {
	body := loadPrompt(t, specGoalDiffMatcherFilename)
	_, prose := splitFrontmatter(t, body)
	lower := strings.ToLower(prose)
	// "preserve" / "stable" / "keep the id" framings all satisfy.
	hasPreserveLanguage := strings.Contains(lower, "preserve") ||
		strings.Contains(lower, "stable") ||
		strings.Contains(lower, "keep the id") ||
		strings.Contains(lower, "same id")
	assert.True(t, hasPreserveLanguage, "prompt must describe id preservation across revisions")
}
