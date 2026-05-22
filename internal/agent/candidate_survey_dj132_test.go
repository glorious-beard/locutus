package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCandidateListSchemaRegistered verifies SchemaFor returns a
// non-nil schema for CandidateList and the schema carries the
// load-bearing constraints DJ-132 depends on: minItems=3 on the
// candidates array; description tags on name and first_glance_fit.
func TestCandidateListSchemaRegistered(t *testing.T) {
	schema, err := SchemaFor("CandidateList")
	require.NoError(t, err)
	require.NotNil(t, schema)

	body, err := json.Marshal(schema)
	require.NoError(t, err)
	text := string(body)

	assert.Contains(t, text, "candidates",
		"CandidateList schema must name the candidates property")
	assert.Contains(t, text, "name",
		"SurveyedCandidate.name must travel through to the schema")
	assert.Contains(t, text, "first_glance_fit",
		"SurveyedCandidate.first_glance_fit must travel through to the schema")
	// minItems=3 is the schema-level enforcement that prevents 1-2
	// candidate degenerate output. The jsonschema library renders this
	// as `"minItems":3` in the JSON; assert against that literal.
	assert.Contains(t, text, "\"minItems\":3",
		"CandidateList.Candidates must carry minItems=3 (DJ-132 conservative floor)")
}

// TestCandidateListExamplePayloadIsDescriptive verifies the registered
// example payload uses descriptive prose for field values (per the
// agent-conventions §5 placeholder ban + DJ-132 schema-skeleton
// invariant). Without this guardrail a future edit could regress the
// example back to "dummy" / "TBD" / one-word fits and prime the
// schema-skeleton failure mode the validator exists to catch.
func TestCandidateListExamplePayloadIsDescriptive(t *testing.T) {
	example, ok := SchemaExample("CandidateList")
	require.True(t, ok, "CandidateList must be registered with an example payload")
	require.NotNil(t, example)

	body, err := json.Marshal(example)
	require.NoError(t, err)
	text := string(body)

	for _, placeholder := range []string{"dummy", "placeholder", "TBD", "foo", "bar", "example1"} {
		assert.NotContains(t, text, placeholder,
			"CandidateList example payload must use descriptive prose, not placeholder token %q (agent-conventions §5)", placeholder)
	}
}
