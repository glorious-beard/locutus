// DJ-129 — assertions on the new spec_critic_elaborator prompt.

package agents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadCriticElaboratorPrompt(t *testing.T) string {
	t.Helper()
	return loadPrompt(t, "spec_critic_elaborator.md")
}

// loadPrompt reads one prompt file from the canonical scaffold dir.
// Shared by the elaborator-* test files that assert prompt shape.
func loadPrompt(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(wd, name))
	require.NoError(t, err, "reading %s", name)
	return string(b)
}

// TestCriticElaboratorPromptDocumentsAllDisciplines — one section
// per discipline value in the bounded enum.
func TestCriticElaboratorPromptDocumentsAllDisciplines(t *testing.T) {
	body := loadCriticElaboratorPrompt(t)
	for _, discipline := range []string{"web_grounded", "spec_node_grounded", "best_practice_grounded", "goals_grounded", "freeform"} {
		assert.Contains(t, body, discipline,
			"prompt must document the %q discipline section", discipline)
	}
}

// TestCriticElaboratorPromptRequiresCounterproposalMenu — DJ-128
// carry-forward: enumeration discipline named.
func TestCriticElaboratorPromptRequiresCounterproposalMenu(t *testing.T) {
	body := loadCriticElaboratorPrompt(t)
	lower := strings.ToLower(body)
	assert.Contains(t, lower, "counterproposals",
		"prompt must name the counterproposals field")
	hasEnumeration := strings.Contains(lower, "list every") ||
		strings.Contains(lower, "list all") ||
		strings.Contains(lower, "do not pick one arbitrarily")
	assert.True(t, hasEnumeration, "prompt must frame the enumeration discipline")
}

// TestCriticElaboratorPromptDocumentsSentinel — needs-investigation
// sentinel named as a last-resort.
func TestCriticElaboratorPromptDocumentsSentinel(t *testing.T) {
	body := loadCriticElaboratorPrompt(t)
	assert.Contains(t, body, "needs investigation")
	lower := strings.ToLower(body)
	hasLastResort := strings.Contains(lower, "rarely") ||
		strings.Contains(lower, "last-resort") ||
		strings.Contains(lower, "last resort")
	assert.True(t, hasLastResort, "sentinel framed as a last-resort")
}

// TestCriticElaboratorOutputSchemaIsCriticIssues — frontmatter
// declares output_schema: CriticIssues (DJ-128 unchanged).
func TestCriticElaboratorOutputSchemaIsCriticIssues(t *testing.T) {
	body := loadCriticElaboratorPrompt(t)
	assert.Contains(t, body, "output_schema: CriticIssues",
		"agent must declare CriticIssues as its output schema")
}
