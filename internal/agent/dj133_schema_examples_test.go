package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRawDecisionProposalExampleUsesAxisAsID locks in DJ-133's axis-as-ID
// convention on the schema example payload the prompt-doc renderer sends
// to the model on every call: the registered example's id is exactly
// `dec-<axes[0]>`. If the example drifts back to a chosen-option-shaped
// slug, the model's "what a valid response looks like" prime drifts with
// it.
func TestRawDecisionProposalExampleUsesAxisAsID(t *testing.T) {
	example, ok := SchemaExample("RawDecisionProposal")
	require.True(t, ok, "RawDecisionProposal example must be registered")
	d, ok := example.(RawDecisionProposal)
	require.True(t, ok, "RawDecisionProposal example must register as the typed shape")
	require.NotEmpty(t, d.Axes, "RawDecisionProposal example must carry at least one axis")
	expected := "dec-" + d.Axes[0]
	assert.Equal(t, expected, d.ID,
		"DJ-133: RawDecisionProposal example's id must equal 'dec-' + axes[0] verbatim")
}

// TestRawSpecProposalExampleUsesAxisAsID applies the same constraint to
// the parent shape's nested decision example. The model sees this shape
// on every decision-elaborator and reconciler call.
func TestRawSpecProposalExampleUsesAxisAsID(t *testing.T) {
	example, ok := SchemaExample("RawSpecProposal")
	require.True(t, ok, "RawSpecProposal example must be registered")
	s, ok := example.(RawSpecProposal)
	require.True(t, ok)
	require.NotEmpty(t, s.Decisions, "RawSpecProposal example must include at least one decision")
	for i, d := range s.Decisions {
		require.NotEmpty(t, d.Axes, "decisions[%d] must carry at least one axis", i)
		expected := "dec-" + d.Axes[0]
		assert.Equal(t, expected, d.ID,
			"DJ-133: RawSpecProposal.decisions[%d] id must equal 'dec-' + axes[0] verbatim", i)
	}
}

// TestRawDecisionProposalIDDescriptionMentionsAxisCopy asserts the
// field's jsonschema description (which travels into the schema doc
// the model sees on every call) names the axis-copy contract rather
// than the retired slug-from-chosen contract.
func TestRawDecisionProposalIDDescriptionMentionsAxisCopy(t *testing.T) {
	schema, err := SchemaFor("RawDecisionProposal")
	require.NoError(t, err)
	data, err := json.Marshal(schema)
	require.NoError(t, err)
	schemaDoc := string(data)
	idDesc := extractFieldDescriptionDJ133(t, schemaDoc, "id")
	assert.Contains(t, idDesc, "axis",
		"DJ-133: RawDecisionProposal.id schema description must name 'axis' as the slug source")
	assert.NotContains(t, idDesc, "derived from the title",
		"DJ-133: the title-derivation language must be retired from the id schema description")
}

// extractFieldDescriptionDJ133 pulls the `description` for a given
// property out of the JSON schema document. Tolerant of either string-
// only or struct-mode shapes — both forms surface a `description` key.
func extractFieldDescriptionDJ133(t *testing.T, schemaDoc string, field string) string {
	t.Helper()
	idx := strings.Index(schemaDoc, `"`+field+`"`)
	require.NotEqual(t, -1, idx, "field %q must be present in schema document", field)
	descIdx := strings.Index(schemaDoc[idx:], `"description"`)
	require.NotEqual(t, -1, descIdx, "field %q must carry a description", field)
	descStart := idx + descIdx + len(`"description"`)
	descStart = descStart + strings.Index(schemaDoc[descStart:], `"`) + 1
	descEnd := descStart + strings.Index(schemaDoc[descStart:], `"`)
	return schemaDoc[descStart:descEnd]
}
