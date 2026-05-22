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

// TestBuildReasoningPassMessagesAppendsProseDirective verifies the
// reasoning pass receives the prose directive as a trailing user
// message so the model knows JSON output is handled by the format
// pass. Universal across providers — without the directive, models
// (notably Gemini's strong tier) default to JSON for analytical
// tasks even with OutputSchema stripped and the agent prompt
// scrubbed of explicit JSON framing.
func TestBuildReasoningPassMessagesAppendsProseDirective(t *testing.T) {
	in := []Message{
		{Role: RoleUser, Content: "projected input one"},
		{Role: RoleUser, Content: "projected input two"},
	}

	t.Run("with_prose_example", func(t *testing.T) {
		out := buildReasoningPassMessages("## domain_read\n\nExample content", in)
		require.Len(t, out, 4,
			"prose example first (Cacheable), projected inputs in middle, directive last")

		// Layer 1: prose example, Cacheable=true
		assert.Equal(t, RoleUser, out[0].Role)
		assert.True(t, out[0].Cacheable,
			"example layer is Cacheable=true for per-agent cross-iteration caching")
		assert.Contains(t, out[0].Content, "## Example output shape")
		assert.Contains(t, out[0].Content, "## domain_read")

		// Middle: projected inputs verbatim
		assert.Equal(t, in[0], out[1])
		assert.Equal(t, in[1], out[2])

		// Layer last: prose directive, Cacheable=false
		assert.Equal(t, RoleUser, out[3].Role)
		assert.Equal(t, ReasoningPassProseDirective, out[3].Content)
		assert.False(t, out[3].Cacheable,
			"directive is guidance not content; no cache value in marking it")
	})

	t.Run("without_prose_example", func(t *testing.T) {
		out := buildReasoningPassMessages("", in)
		require.Len(t, out, 3,
			"empty exampleProse → no example layer; just inputs + directive")
		assert.Equal(t, in[0], out[0])
		assert.Equal(t, in[1], out[1])
		assert.Equal(t, ReasoningPassProseDirective, out[2].Content)
	})
}

// TestBuildFormatPassMessagesDoesNotIncludeProseDirective is the
// reciprocal check: the format pass exists to PRODUCE JSON, so the
// "produce prose" directive must not survive into it. The format
// pass receives the example layer (Cacheable) and the reasoning
// prose (uncacheable), nothing else.
func TestBuildFormatPassMessagesDoesNotIncludeProseDirective(t *testing.T) {
	msgs := buildFormatPassMessages("## domain_read\n\nExample", `{"k":"v"}`, "the reasoning prose")
	for _, m := range msgs {
		assert.NotContains(t, m.Content, "prose, not JSON",
			"format pass must not carry the reasoning pass's prose directive — it would tell the formatter to emit prose instead of JSON")
		assert.NotContains(t, m.Content, "convert it to JSON",
			"format pass must not carry the reasoning pass's prose directive")
	}
}

// TestBuildFormatPassMessagesLayersExampleAsCacheableUserMessage
// confirms the DJ-130 follow-up: when an OutputSchema has a
// registered example, the format pass receives it as a Cacheable
// user message BEFORE the reasoning prose — preserving
// CanonicalFormatterPrompt as the Layer-1 cross-agent cache prefix
// in the system position while the example is a Layer-2 per-agent
// cache layer.
//
// Splicing the example into the system prompt would dissolve Layer
// 1 (different agents → different prompts → different cache keys);
// the helper structure tested here is what avoids that regression.
func TestBuildFormatPassMessagesLayersExampleAsCacheableUserMessage(t *testing.T) {
	t.Run("with_prose_and_json_example", func(t *testing.T) {
		msgs := buildFormatPassMessages(
			"## domain_read\n\nExample prose content",
			`{"k":"v"}`,
			"the reasoning prose",
		)
		require.Len(t, msgs, 3, "prose example, JSON example, reasoning prose")

		// Layer 1: prose example (input shape demonstration)
		assert.Equal(t, RoleUser, msgs[0].Role)
		assert.True(t, msgs[0].Cacheable)
		assert.Contains(t, msgs[0].Content, "Example reasoning-prose input")
		assert.Contains(t, msgs[0].Content, "## domain_read")

		// Layer 2: JSON example (output shape demonstration)
		assert.Equal(t, RoleUser, msgs[1].Role)
		assert.True(t, msgs[1].Cacheable,
			"JSON example layer is Cacheable=true so per-agent cross-iteration caching catches it")
		assert.Contains(t, msgs[1].Content, "Example structured output")
		assert.Contains(t, msgs[1].Content, `{"k":"v"}`)

		// Final: per-call reasoning prose, uncached
		assert.Equal(t, RoleUser, msgs[2].Role)
		assert.False(t, msgs[2].Cacheable,
			"per-call reasoning prose is never reusable — Cacheable=false keeps the cache marker off it")
		assert.Equal(t, "the reasoning prose", msgs[2].Content)
	})

	t.Run("with_only_json_example", func(t *testing.T) {
		msgs := buildFormatPassMessages("", `{"k":"v"}`, "the reasoning prose")
		require.Len(t, msgs, 2, "no prose example → only JSON example + reasoning prose")
		assert.Contains(t, msgs[0].Content, "Example structured output")
		assert.Equal(t, "the reasoning prose", msgs[1].Content)
	})

	t.Run("without_any_example", func(t *testing.T) {
		msgs := buildFormatPassMessages("", "", "the reasoning prose")
		require.Len(t, msgs, 1, "no examples registered → just the reasoning prose")
		assert.Equal(t, RoleUser, msgs[0].Role)
		assert.False(t, msgs[0].Cacheable)
		assert.Equal(t, "the reasoning prose", msgs[0].Content)
	})
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
