package adapters

import (
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildParams_NativeOutputConfig confirms (DJ-108) the
// migration from synthetic-tool-plus-forced-tool_choice to native
// MessageNewParams.OutputConfig.Format.Schema. When a Request has
// OutputSchema set, the resulting params must:
//
//  1. Populate OutputConfig.Format with the schema.
//  2. NOT set ToolChoice (no forced tool_choice).
//  3. NOT inject the synthetic schema tool into Tools.
//
// The native API removes the conflict between strict-mode JSON and
// extended thinking that surfaced when DJ-107 routed structured-
// output agents to Anthropic-first.
func TestBuildParams_NativeOutputConfig(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
		"required": []string{"answer"},
	}
	req := Request{
		Model:           "claude-opus-4-7",
		MaxOutputTokens: 1024,
		Messages:        []Message{{Role: RoleUser, Content: "ping"}},
		OutputSchema:    schema,
	}

	params := buildAnthropicMessageNewParams(req)

	require.NotNil(t, params.OutputConfig.Format.Schema, "OutputConfig.Format.Schema must be set when Request.OutputSchema is provided")
	assert.Equal(t, schema, params.OutputConfig.Format.Schema, "schema must round-trip verbatim")

	// No forced tool_choice.
	assert.Nil(t, params.ToolChoice.OfTool, "must not force a specific tool — native OutputConfig replaces forced tool_choice")

	// No synthetic schema tool — the migration removed it entirely.
	// (The legacy name was "submit_response"; verifying by name keeps
	// this assertion meaningful even though the const is now gone.)
	for _, tool := range params.Tools {
		if tool.OfTool != nil {
			assert.NotEqual(t, "submit_response", tool.OfTool.Name, "synthetic schema tool must not be injected")
		}
	}
}

// TestBuildParams_AdaptiveThinkingWithStructuredEffort confirms the
// thinking-budget migration for Opus 4.7+: enabled-budget API is
// replaced by adaptive thinking + OutputConfig.Effort. The mapping
// is ThinkingOn → medium, ThinkingHigh → high.
func TestBuildParams_AdaptiveThinkingWithStructuredEffort(t *testing.T) {
	cases := []struct {
		name       string
		level      ThinkingLevel
		wantEffort anthropicsdk.OutputConfigEffort
	}{
		{name: "ThinkingOn → medium", level: ThinkingOn, wantEffort: anthropicsdk.OutputConfigEffortMedium},
		{name: "ThinkingHigh → high", level: ThinkingHigh, wantEffort: anthropicsdk.OutputConfigEffortHigh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{
				Model:           "claude-opus-4-7",
				MaxOutputTokens: 1024,
				Messages:        []Message{{Role: RoleUser, Content: "ping"}},
				OutputSchema:    map[string]any{"type": "object"},
				Thinking:        tc.level,
			}

			params := buildAnthropicMessageNewParams(req)

			require.NotNil(t, params.Thinking.OfAdaptive, "must use adaptive thinking (the only API supported on Opus 4.7+)")
			assert.Nil(t, params.Thinking.OfEnabled, "deprecated enabled-budget API must NOT be used")
			assert.Equal(t, tc.wantEffort, params.OutputConfig.Effort)
		})
	}
}

// TestBuildParams_ThinkingOffNoEffort confirms that ThinkingOff
// produces no thinking config and no Effort hint, regardless of
// whether OutputSchema is set.
func TestBuildParams_ThinkingOffNoEffort(t *testing.T) {
	req := Request{
		Model:           "claude-haiku-4-5-20251001",
		MaxOutputTokens: 1024,
		Messages:        []Message{{Role: RoleUser, Content: "ping"}},
		OutputSchema:    map[string]any{"type": "object"},
		Thinking:        ThinkingOff,
	}

	params := buildAnthropicMessageNewParams(req)

	assert.Nil(t, params.Thinking.OfAdaptive)
	assert.Nil(t, params.Thinking.OfEnabled)
	assert.Equal(t, anthropicsdk.OutputConfigEffort(""), params.OutputConfig.Effort)
}

// TestBuildParams_AdaptiveThinkingNoSchema confirms that when
// thinking is on but no OutputSchema is set, adaptive thinking
// applies without an Effort hint (Effort lives on OutputConfig and
// only matters for structured outputs).
func TestBuildParams_AdaptiveThinkingNoSchema(t *testing.T) {
	req := Request{
		Model:           "claude-sonnet-4-6",
		MaxOutputTokens: 1024,
		Messages:        []Message{{Role: RoleUser, Content: "ping"}},
		Thinking:        ThinkingOn,
		// no OutputSchema
	}

	params := buildAnthropicMessageNewParams(req)

	require.NotNil(t, params.Thinking.OfAdaptive, "thinking on → adaptive even without schema")
	assert.Equal(t, anthropicsdk.OutputConfigEffort(""), params.OutputConfig.Effort, "no schema → no Effort")
	assert.Nil(t, params.OutputConfig.Format.Schema, "no schema → no Format.Schema")
}

// TestBuildParams_GroundingComposes confirms that grounding (web
// search) and OutputConfig coexist — the migration's whole point.
// The model can call web_search before producing the structured
// response; nothing forces a specific tool choice.
func TestBuildParams_GroundingComposes(t *testing.T) {
	req := Request{
		Model:           "claude-sonnet-4-6",
		MaxOutputTokens: 1024,
		Messages:        []Message{{Role: RoleUser, Content: "ping"}},
		OutputSchema:    map[string]any{"type": "object"},
		Grounding:       true,
	}

	params := buildAnthropicMessageNewParams(req)

	require.NotNil(t, params.OutputConfig.Format.Schema, "structured output must be configured")
	assert.Nil(t, params.ToolChoice.OfTool, "no forced tool_choice — model is free to call web_search before responding")

	hasWebSearch := false
	for _, tool := range params.Tools {
		if tool.OfWebSearchTool20250305 != nil {
			hasWebSearch = true
		}
	}
	assert.True(t, hasWebSearch, "web_search tool must be present alongside OutputConfig")
}
