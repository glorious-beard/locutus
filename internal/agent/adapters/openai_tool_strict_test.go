package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildOpenAITools_StrictMatchesSchemaShape pins the fix for the
// 400 "Invalid schema for function 'spec_search': 'required' is
// required to be supplied and to be an array including every key in
// properties" failure operators hit when justify (or any other verb)
// fell through to OpenAI and the request carried a tool with optional
// fields. Strict-mode validation demands every property in `required`;
// our spec_search tool legitimately has optional `kind` and `limit`.
//
// The fix mirrors what the output-schema path already does: use
// schemaIsFullyRequired to decide Strict per-tool. This test asserts
// the per-tool decision matches the schema shape so a future
// regression that hardcodes Strict=true (like the original bug) fails
// red here.
func TestBuildOpenAITools_StrictMatchesSchemaShape(t *testing.T) {
	cases := []struct {
		name       string
		tool       ToolDef
		wantStrict bool
	}{
		{
			name: "all properties required → strict ok",
			tool: ToolDef{
				Name: "spec_get",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{"type": "string"},
					},
					"required": []any{"id"},
				},
			},
			wantStrict: true,
		},
		{
			name: "partial required → strict off (was the bug)",
			tool: ToolDef{
				Name: "spec_search",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
						"kind":  map[string]any{"type": "string"},
						"limit": map[string]any{"type": "integer"},
					},
					"required": []any{"query"},
				},
			},
			wantStrict: false,
		},
		{
			name: "no properties → strict ok (empty schema is trivially full)",
			tool: ToolDef{
				Name: "spec_list_manifest",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
					"required":   []any{},
				},
			},
			wantStrict: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tools := buildOpenAITools(Request{Tools: []ToolDef{tc.tool}})
			require.Len(t, tools, 1)
			require.NotNil(t, tools[0].OfFunction)
			require.NotNil(t, tools[0].OfFunction.Strict)
			assert.Equal(t, tc.wantStrict, tools[0].OfFunction.Strict.Value)
		})
	}
}
