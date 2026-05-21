package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequiresThinkingSchemaSplitAcrossAdapters exercises the gate
// predicate each provider adapter exposes for the DJ-130 thinking +
// schema split. The predicate is pure (no SDK round-trip), so we can
// table-drive it across all three adapters without HTTP mocking; the
// load-bearing empirical verification of the split actually firing
// happens via the smoke-run check named in the DJ-130 plan.
func TestRequiresThinkingSchemaSplitAcrossAdapters(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
	}

	type predicate func(Request) bool
	adapters := []struct {
		name     string
		gate     predicate
		strong   string
		fastTier string
	}{
		{
			name:     "anthropic",
			gate:     (&AnthropicAdapter{}).requiresThinkingSchemaSplit,
			strong:   "claude-opus-4-7",
			fastTier: "claude-haiku-4-5-20251001",
		},
		{
			name:     "gemini",
			gate:     (&GeminiAdapter{}).requiresThinkingSchemaSplit,
			strong:   "gemini-3.1-pro-preview",
			fastTier: "gemini-3.1-flash-lite-preview",
		},
		{
			name:     "openai",
			gate:     (&OpenAIResponsesAdapter{}).requiresThinkingSchemaSplit,
			strong:   "gpt-5",
			fastTier: "gpt-5-mini",
		},
	}

	for _, a := range adapters {
		t.Run(a.name+"/splits_when_thinking_on_plus_schema_plus_fast_tier", func(t *testing.T) {
			req := Request{
				Model:        a.strong,
				Thinking:     ThinkingOn,
				OutputSchema: schema,
				FormatModel:  a.fastTier,
			}
			assert.True(t, a.gate(req), "split fires for thinking-on + schema + fast tier wired")
		})

		t.Run(a.name+"/splits_when_thinking_high_plus_schema_plus_fast_tier", func(t *testing.T) {
			req := Request{
				Model:        a.strong,
				Thinking:     ThinkingHigh,
				OutputSchema: schema,
				FormatModel:  a.fastTier,
			}
			assert.True(t, a.gate(req), "ThinkingHigh exhibits the same failure mode as ThinkingOn — both split")
		})

		t.Run(a.name+"/no_split_when_thinking_off", func(t *testing.T) {
			req := Request{
				Model:        a.strong,
				Thinking:     ThinkingOff,
				OutputSchema: schema,
				FormatModel:  a.fastTier,
			}
			assert.False(t, a.gate(req), "thinking-off uses native single-call structured output")
		})

		t.Run(a.name+"/no_split_when_no_schema", func(t *testing.T) {
			req := Request{
				Model:       a.strong,
				Thinking:    ThinkingOn,
				FormatModel: a.fastTier,
			}
			assert.False(t, a.gate(req), "no schema means nothing to format into")
		})

		t.Run(a.name+"/no_split_when_no_format_model", func(t *testing.T) {
			req := Request{
				Model:        a.strong,
				Thinking:     ThinkingOn,
				OutputSchema: schema,
			}
			assert.False(t, a.gate(req),
				"missing fast-tier fallback degrades gracefully to single-call rather than splitting against the same strong-tier model (which would defeat the purpose)")
		})
	}
}

// TestMergeSplitResponsesPreservesReasoningSideState confirms the
// shared merger keeps the format pass's structured Content while
// carrying the reasoning pass's thinking, tool calls, citations, and
// per-round captures forward — and that token counts sum across both
// calls. The merge contract is the same across all three adapters; a
// regression here breaks the trace shape uniformly.
func TestMergeSplitResponsesPreservesReasoningSideState(t *testing.T) {
	reasoning := &Response{
		Content:                  "free-form prose drafting two strategies", // gets DROPPED — format pass's Content wins
		Reasoning:                "model thought about it",
		InputTokens:              100,
		OutputTokens:             200,
		ThoughtsTokens:           50,
		TotalTokens:              350,
		CacheCreationInputTokens: 10,
		CacheReadInputTokens:     5,
		ToolCalls:                []ToolCall{{Name: "web_search", Query: "X"}},
		Citations:                []Citation{{URL: "https://example.com"}},
		Rounds:                   []Round{{Index: 1, Text: "round-1"}},
	}
	formatted := &Response{
		Content:      `{"strategies":["strat-a","strat-b"]}`,
		Model:        "claude-haiku-4-5-20251001",
		InputTokens:  20,
		OutputTokens: 30,
		TotalTokens:  50,
	}

	merged := mergeSplitResponses(reasoning, formatted)
	require.NotNil(t, merged)

	assert.Equal(t, formatted.Content, merged.Content,
		"format pass's structured Content is the agent's contract")
	assert.Equal(t, reasoning.Reasoning, merged.Reasoning,
		"reasoning thinking carries forward")
	assert.Equal(t, reasoning.ToolCalls, merged.ToolCalls,
		"reasoning-side tool calls carry forward (the format pass strips tools)")
	assert.Equal(t, reasoning.Citations, merged.Citations,
		"reasoning-side citations carry forward (the format pass strips grounding)")
	assert.Equal(t, reasoning.Rounds, merged.Rounds,
		"reasoning-side rounds carry forward")

	assert.Equal(t, 120, merged.InputTokens, "input tokens sum across both calls")
	assert.Equal(t, 230, merged.OutputTokens, "output tokens sum")
	assert.Equal(t, 50, merged.ThoughtsTokens, "thoughts tokens come from reasoning pass only")
	assert.Equal(t, 400, merged.TotalTokens, "total tokens sum")
	assert.Equal(t, 10, merged.CacheCreationInputTokens, "cache creation tokens preserved")
	assert.Equal(t, 5, merged.CacheReadInputTokens, "cache read tokens preserved")
}

// TestMergeSplitResponsesNilSafety confirms the merger handles partial
// inputs (e.g. an adapter error before the format pass populated a
// Response) without panicking. The reasoning return path on a format
// failure surfaces a nil formatted Response; the merger must return
// the reasoning verbatim in that case so the caller sees what
// happened pre-failure.
func TestMergeSplitResponsesNilSafety(t *testing.T) {
	reasoning := &Response{Content: "prose"}
	assert.Equal(t, reasoning, mergeSplitResponses(reasoning, nil),
		"nil formatted returns reasoning unchanged")

	formatted := &Response{Content: `{"x":1}`}
	merged := mergeSplitResponses(nil, formatted)
	require.NotNil(t, merged)
	assert.Equal(t, formatted.Content, merged.Content,
		"nil reasoning returns formatted Content (the agent's contract still holds)")
}
