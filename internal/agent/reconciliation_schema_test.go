package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReconciliationVerdictSchema_NoOneOf locks in DJ-111: the
// ReconciliationVerdict schema must NOT contain `oneOf` anywhere.
// Anthropic's native output_config.format.schema rejects oneOf
// (Gemini and OpenAI accept it, but supporting all three providers
// uniformly requires the flatter shape). The Go-side
// ReconciliationAction was already a flat struct with optional
// fields tagged omitempty; the apply switch in reconcile.go does
// per-kind validation, so the schema's role is to enforce shape
// (object with `actions[]`), kind enum, and required `kind` +
// `sources` fields — not the per-kind required-field branching the
// previous oneOf encoded.
func TestReconciliationVerdictSchema_NoOneOf(t *testing.T) {
	schema, err := SchemaFor("ReconciliationVerdict")
	require.NoError(t, err)

	// Walk the schema tree and assert oneOf is absent.
	hasOneOf := containsKey(schema, "oneOf")
	assert.False(t, hasOneOf, "ReconciliationVerdict schema must not use `oneOf` — Anthropic's native output_config rejects it (DJ-111)")
}

// TestReconciliationVerdictSchema_KindEnumIntact verifies the
// flattened schema still constrains `kind` to the three known
// action variants. The schema's load-bearing job after DJ-111 is
// (a) require an `actions` array, (b) require each action to have
// `kind` + `sources`, (c) constrain `kind` to the known enum.
// Per-kind required fields (canonical, loser, rejected_because,
// existing_id) drift to prompt guidance + apply-time validation.
func TestReconciliationVerdictSchema_KindEnumIntact(t *testing.T) {
	schema, err := SchemaFor("ReconciliationVerdict")
	require.NoError(t, err)

	props, _ := schema["properties"].(map[string]any)
	require.NotNil(t, props)
	actions, _ := props["actions"].(map[string]any)
	require.NotNil(t, actions)
	items, _ := actions["items"].(map[string]any)
	require.NotNil(t, items)
	itemProps, _ := items["properties"].(map[string]any)
	require.NotNil(t, itemProps)
	kind, _ := itemProps["kind"].(map[string]any)
	require.NotNil(t, kind)

	enum, _ := kind["enum"].([]any)
	enumStrs := make([]string, 0, len(enum))
	for _, v := range enum {
		if s, ok := v.(string); ok {
			enumStrs = append(enumStrs, s)
		}
	}
	assert.ElementsMatch(t, []string{"dedupe", "resolve_conflict", "reuse_existing"}, enumStrs,
		"action.kind enum must include exactly the three known action variants")

	required, _ := items["required"].([]any)
	requiredStrs := make([]string, 0, len(required))
	for _, v := range required {
		if s, ok := v.(string); ok {
			requiredStrs = append(requiredStrs, s)
		}
	}
	assert.Contains(t, requiredStrs, "kind", "kind must be required at the schema layer")
	assert.Contains(t, requiredStrs, "sources", "sources must be required at the schema layer")
	// Canonical, loser, rejected_because, existing_id are intentionally NOT required —
	// per-kind requirements live in the prompt and apply-time validation.
	for _, optionalField := range []string{"canonical", "loser", "rejected_because", "existing_id"} {
		assert.NotContains(t, requiredStrs, optionalField,
			"%s must be optional at the schema layer (per-kind requirements enforced downstream)", optionalField)
	}
}

// containsKey walks an arbitrary JSON-Schema-like map and reports
// whether the given key appears anywhere in the tree. Used to
// confirm `oneOf` is absent regardless of nesting depth.
func containsKey(node any, key string) bool {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if k == key {
				return true
			}
			if containsKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsKey(child, key) {
				return true
			}
		}
	}
	return false
}

// TestReconciliationVerdictSchema_DocumentationConsistency is a soft
// check that the schemaToolName concept is fully removed (DJ-108 +
// DJ-111). If a future refactor re-introduces the synthetic-tool
// pattern, it would re-introduce a path that needs oneOf-aware
// handling on Anthropic — this assertion makes the regression loud.
func TestReconciliationVerdictSchema_DocumentationConsistency(t *testing.T) {
	schema, err := SchemaFor("ReconciliationVerdict")
	require.NoError(t, err)

	// JSON-marshal-ish sanity: the schema is a plain map with no
	// references to the legacy synthetic schema tool.
	for k := range schema {
		assert.False(t, strings.Contains(k, "submit_response"),
			"schema must not reference the legacy synthetic schema tool")
	}
}
