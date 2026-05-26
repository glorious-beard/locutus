// DJ-129 — assertions on the spec-scout prompt's new CritiqueDimensions
// identification section.

package agents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadScoutPrompt(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(wd, "spec-scout.md"))
	require.NoError(t, err)
	return string(b)
}

// TestScoutPromptNamesCritiqueDimensionsField — the prompt mentions
// the field by name and walks its sub-fields in schema order.
func TestScoutPromptNamesCritiqueDimensionsField(t *testing.T) {
	body := loadScoutPrompt(t)
	assert.Contains(t, body, "critique_dimensions")
	for _, sub := range []string{"focus_question", "source_evidence", "disciplines", "severity_floor"} {
		assert.Contains(t, body, sub, "prompt must walk the %q sub-field", sub)
	}
}

// TestScoutPromptDocumentsDisciplineEnum — every discipline value is
// named verbatim so the scout knows what's available.
func TestScoutPromptDocumentsDisciplineEnum(t *testing.T) {
	body := loadScoutPrompt(t)
	for _, v := range []string{"web_grounded", "spec_node_grounded", "best_practice_grounded", "goals_grounded", "freeform"} {
		assert.Contains(t, body, v, "discipline value %q must be in prompt", v)
	}
}

// TestScoutPromptProvidesLensDiversity — example dimensions span at
// least three lenses so the scout treats lens as open-ended.
func TestScoutPromptProvidesLensDiversity(t *testing.T) {
	body := loadScoutPrompt(t)
	lower := strings.ToLower(body)
	lensCount := 0
	for _, lens := range []string{"cost", "sre", "compliance", "security", "vendor-portability", "election-cycle", "accessibility"} {
		if strings.Contains(lower, lens) {
			lensCount++
		}
	}
	assert.GreaterOrEqual(t, lensCount, 3, "prompt should show at least 3 different lenses to teach lens diversity")
}

// TestScoutPromptDocumentsStabilitySemantics — prompt names the
// "mostly stable; retire-when-resolved" framing per design decision #7.
func TestScoutPromptDocumentsStabilitySemantics(t *testing.T) {
	body := loadScoutPrompt(t)
	lower := strings.ToLower(body)
	hasStability := strings.Contains(lower, "stable") || strings.Contains(lower, "retire")
	assert.True(t, hasStability, "prompt must name the stability / retirement semantic")
}
