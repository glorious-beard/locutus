package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReconciliationVerdictFlatSchema confirms the hand-authored
// override produces a flat object schema with `kind` enum and
// optional variant fields (DJ-111: replaced the oneOf
// discriminated-union shape because Anthropic's native
// output_config.format.schema rejects oneOf).
//
// The original bug this schema was authored to address —
// ReconciliationAction's permissive omitempty fields letting the
// model emit dedupe actions without canonical — moves from
// schema-layer enforcement to prompt + apply-time validation.
// spec_reconciler.md documents the per-kind requirements; the
// reconcile.go apply switch validates per-kind on receipt.
func TestReconciliationVerdictFlatSchema(t *testing.T) {
	schema, err := SchemaFor("ReconciliationVerdict")
	require.NoError(t, err)

	// Top-level shape: object with "actions" array.
	require.Equal(t, "object", schema["type"])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	actions, ok := props["actions"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "array", actions["type"])

	// items must be a single flat object — NOT a oneOf union.
	items, ok := actions["items"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, items, "oneOf",
		"items must be a flat object schema (DJ-111: Anthropic native output_config rejects oneOf)")

	// kind must be an enum constraining to the three known action variants.
	itemProps, ok := items["properties"].(map[string]any)
	require.True(t, ok)
	kind, ok := itemProps["kind"].(map[string]any)
	require.True(t, ok)
	enum, ok := kind["enum"].([]any)
	require.True(t, ok)
	enumStrs := make([]string, 0, len(enum))
	for _, v := range enum {
		enumStrs = append(enumStrs, v.(string))
	}
	assert.ElementsMatch(t, []string{"dedupe", "resolve_conflict", "reuse_existing"}, enumStrs,
		"kind enum must include exactly the three known action variants")

	// kind + sources required at the schema layer; everything else
	// optional and validated downstream.
	required, ok := items["required"].([]any)
	require.True(t, ok)
	requiredStrs := make([]string, 0, len(required))
	for _, r := range required {
		requiredStrs = append(requiredStrs, r.(string))
	}
	assert.ElementsMatch(t, []string{"kind", "sources"}, requiredStrs,
		"only kind + sources are required at the schema layer; per-kind requirements are enforced via prompt + apply-time validation")
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
