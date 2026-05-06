package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReconciliationVerdictDiscriminatedSchema confirms the hand-
// authored override produces a discriminated union that requires
// canonical-per-kind. The original bug: ReconciliationAction has
// every field `,omitempty` so reflection produced a permissive
// schema, and the model emitted dedupe actions without canonical.
// Strict-mode adapters now reject malformed actions at the API.
func TestReconciliationVerdictDiscriminatedSchema(t *testing.T) {
	schema, err := SchemaFor("ReconciliationVerdict")
	require.NoError(t, err)

	// Top-level shape: object with "actions" array.
	require.Equal(t, "object", schema["type"])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	actions, ok := props["actions"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "array", actions["type"])

	// items must be a oneOf of three variants (dedupe, resolve_conflict,
	// reuse_existing) — that's the discrimination.
	items, ok := actions["items"].(map[string]any)
	require.True(t, ok)
	oneOf, ok := items["oneOf"].([]any)
	require.True(t, ok)
	require.Len(t, oneOf, 3, "three action kinds")

	// Each variant pins kind via an enum of one literal AND requires
	// the kind-specific fields. Verify by walking the variants.
	gotKinds := map[string][]string{}
	for _, v := range oneOf {
		variant := v.(map[string]any)
		variantProps := variant["properties"].(map[string]any)
		kindProp := variantProps["kind"].(map[string]any)
		kindEnum := kindProp["enum"].([]any)
		require.Len(t, kindEnum, 1, "kind discriminator pinned to one literal")
		kind := kindEnum[0].(string)

		required := variant["required"].([]any)
		var requiredStrs []string
		for _, r := range required {
			requiredStrs = append(requiredStrs, r.(string))
		}
		gotKinds[kind] = requiredStrs
	}

	assert.ElementsMatch(t, []string{"kind", "sources", "canonical"}, gotKinds["dedupe"],
		"dedupe requires canonical (the field the model was skipping)")
	assert.ElementsMatch(t, []string{"kind", "sources", "canonical", "loser", "rejected_because"}, gotKinds["resolve_conflict"],
		"resolve_conflict requires canonical + loser + rejected_because")
	assert.ElementsMatch(t, []string{"kind", "sources", "existing_id"}, gotKinds["reuse_existing"],
		"reuse_existing requires existing_id, not canonical")
}

// TestReconciliationVerdictSchemaSerializes confirms the schema
// round-trips through JSON cleanly so adapters can pass it to the
// provider SDK as-is. Catches regressions where the override
// includes a non-marshalable type.
func TestReconciliationVerdictSchemaSerializes(t *testing.T) {
	schema, err := SchemaFor("ReconciliationVerdict")
	require.NoError(t, err)
	_, err = json.Marshal(schema)
	require.NoError(t, err)
}
