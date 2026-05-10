package scaffold

import (
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecAdvocate_HasGroundingDiscipline — the spec_advocate prompt
// must carry the empty-research-case guidance. Brittle by design:
// changes to the prompt should be deliberate, not silent regressions
// to confabulation-friendly text. Tested here (against the embedded
// scaffold copy) rather than in internal/agent because the prompt
// lives on disk now per DJ-101 reversal.
func TestSpecAdvocate_HasGroundingDiscipline(t *testing.T) {
	def, err := LoadAgent(specio.NewMemFS(), "spec_advocate")
	require.NoError(t, err, "embedded scaffold must include spec_advocate.md")

	prompt := def.SystemPrompt
	flat := strings.Join(strings.Fields(prompt), " ")

	assert.Contains(t, flat, "GROUNDING DISCIPLINE WHEN RESEARCH IS ABSENT",
		"the advocate must be told what to do when research findings are missing or empty")
	assert.Contains(t, flat, "I don't have grounded evidence",
		"the advocate must have an explicit alternative phrase to substitute for unsourced specifics")
	for _, forbidden := range []string{"version numbers", "ecosystem maturity", "hiring-pool", "case studies"} {
		assert.Contains(t, flat, forbidden,
			"the empty-research disclaimer must enumerate %q as a forbidden assertion class so the model has a concrete rule to follow", forbidden)
	}
	idxDiscipline := strings.Index(prompt, "GROUNDING DISCIPLINE WHEN RESEARCH IS ABSENT")
	idxPointByPoint := strings.Index(prompt, "ALSO address each concern")
	require.True(t, idxDiscipline > 0)
	require.True(t, idxPointByPoint > 0)
	assert.Less(t, idxDiscipline, idxPointByPoint,
		"grounding discipline rule must appear before the structured-response instructions so it constrains the per-concern responses too")
}

// TestSpecChallenger_BroadEvidenceSources — the challenger prompt
// must list the four evidence sources (node rationale, GOALS,
// engineering practices, current practice) and warn the model that
// empty/one-word fields are rejected. The framework-philosophy
// failure mode this addresses (justify against strat-frontend on
// 2026-05-10) was caused by the prompt being too narrow about what
// counted as evidence.
func TestSpecChallenger_BroadEvidenceSources(t *testing.T) {
	def, err := LoadAgent(specio.NewMemFS(), "spec_challenger")
	require.NoError(t, err, "embedded scaffold must include spec_challenger.md")

	flat := strings.Join(strings.Fields(def.SystemPrompt), " ")

	assert.Contains(t, flat, "node's own rationale",
		"evidence sources must include the node's rationale/alternatives/provenance")
	assert.Contains(t, flat, "GOALS.md",
		"evidence sources must include GOALS clauses")
	assert.Contains(t, flat, "Named engineering practices",
		"evidence sources must include named practices")
	assert.Contains(t, flat, "Current practice in the field",
		"evidence sources must include current practice")
	assert.Contains(t, flat, "rejected by a downstream validator",
		"prompt must warn the model that empty/one-word fields fail validation")
}

// TestJustifyResearcher_HasAntiFallbackDirective — the researcher
// must be told to mark findings as ungrounded rather than recall
// from training data when the search tool errors or returns no
// relevant results.
func TestJustifyResearcher_HasAntiFallbackDirective(t *testing.T) {
	def, err := LoadAgent(specio.NewMemFS(), "justify_researcher")
	require.NoError(t, err, "embedded scaffold must include justify_researcher.md")

	flat := strings.Join(strings.Fields(def.SystemPrompt), " ")

	assert.Contains(t, flat, "DO NOT FALL BACK TO TRAINING-DATA RECALL",
		"researcher must be told not to confabulate when search fails")
	assert.Contains(t, flat, "SEARCH TOOL ERROR",
		"researcher must distinguish search-tool error from no-results")
	assert.Contains(t, flat, "SEARCH RETURNED NO RELEVANT RESULTS",
		"researcher must distinguish no-results from tool error")
	assert.True(t, def.Grounding,
		"researcher frontmatter must set grounding: true so the executor wires up provider-native search")
}
