package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDispatch_SplitsForThinkingOnPlusSchema verifies the dispatcher's
// reason-then-format split fires for agents that declare BOTH
// `thinking != "off"` AND `output_schema`, when the executor exposes
// FormatPreferences via the FormatProvider interface. Two LLM calls
// fire: the reasoning call (the agent's def with OutputSchema
// cleared) and the format call (a synthetic def with thinking off,
// no tools, schema set, fast-tier rotation).
func TestDispatch_SplitsForThinkingOnPlusSchema(t *testing.T) {
	mock := &MockExecutor{
		FormatPrefs: []ModelPreference{
			{Provider: "anthropic", Tier: "fast"},
		},
	}
	mock.Reset(
		// Call 1: reasoning. Returns prose listing concerns.
		MockResponse{
			AgentID: "spec_challenger",
			Response: &AgentOutput{
				Content: "Concern: the rationale claims X but does not address Y.",
				Model:   "claude-sonnet-4-6",
			},
		},
		// Call 2: format. Returns the structured JSON the agent's
		// schema expects.
		MockResponse{
			AgentID: "spec_challenger-format",
			Response: &AgentOutput{
				Content: `{"concerns":[{"weakness":"the rationale claims X but does not address Y","evidence":"the node's rationale section","counterproposal":"add a section addressing Y"}]}`,
				Model:   "claude-haiku-4-5-20251001",
			},
		},
	)

	d := NewDispatcher(mock)
	def := AgentDef{
		ID:           "spec_challenger",
		Thinking:     "on",
		OutputSchema: "ChallengeBrief",
		Models:       []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
		SystemPrompt: "You are the spec challenger...",
	}
	out, err := d.Dispatch(context.Background(), def, AgentInput{
		Messages: []Message{{Role: "user", Content: "challenge this"}},
	}, DispatchOptions{})

	require.NoError(t, err)
	require.NotNil(t, out)

	calls := mock.Calls()
	require.Len(t, calls, 2, "split path runs exactly 2 LLM calls")

	// Call 1: reasoning. Agent's original def with schema cleared.
	assert.Equal(t, "spec_challenger", calls[0].Def.ID)
	assert.Equal(t, "on", calls[0].Def.Thinking, "reasoning call preserves thinking")
	assert.Empty(t, calls[0].Def.OutputSchema, "reasoning call has OutputSchema cleared")

	// Call 2: format. Synthetic def, fast tier, no thinking.
	assert.Equal(t, "spec_challenger-format", calls[1].Def.ID)
	assert.Equal(t, "off", calls[1].Def.Thinking, "format call runs with thinking off")
	assert.Equal(t, "ChallengeBrief", calls[1].Def.OutputSchema, "format call carries the original schema")
	require.Len(t, calls[1].Def.Models, 1, "format call uses FormatPrefs")
	assert.Equal(t, "anthropic", calls[1].Def.Models[0].Provider)
	assert.Equal(t, "fast", calls[1].Def.Models[0].Tier)
	assert.Contains(t, calls[1].Def.SystemPrompt, "Extract the structured content")

	// Format call's user message is the reasoning output verbatim.
	require.Len(t, calls[1].Input.Messages, 1)
	assert.Equal(t, "Concern: the rationale claims X but does not address Y.",
		calls[1].Input.Messages[0].Content)

	// Final content is the format call's structured output.
	assert.Contains(t, out.Content, "weakness", "merged result carries the format call's JSON")
	assert.NotContains(t, out.Content, "Concern:", "the prose reasoning isn't smuggled into the final Content")
}

// TestDispatch_NoSplitWhenThinkingOff confirms the split predicate
// short-circuits when thinking is off — even with a schema set and
// FormatPrefs available, single-call mode is preserved. This is the
// non-thinking + schema regime where strict-mode enforcement alone
// suffices (the Path A example payload reaches the model via
// BuildSystemPrompt, not via the split).
func TestDispatch_NoSplitWhenThinkingOff(t *testing.T) {
	mock := &MockExecutor{
		FormatPrefs: []ModelPreference{{Provider: "anthropic", Tier: "fast"}},
	}
	mock.Reset(MockResponse{
		Response: &AgentOutput{Content: `{"summary":"a one-sentence node summary."}`},
	})

	d := NewDispatcher(mock)
	def := AgentDef{
		ID:           "spec_summarizer",
		Thinking:     "off",
		OutputSchema: "SpecSummaryResult",
		Models:       []ModelPreference{{Provider: "anthropic", Tier: "fast"}},
	}
	_, err := d.Dispatch(context.Background(), def, AgentInput{
		Messages: []Message{{Role: "user", Content: "summarize"}},
	}, DispatchOptions{})

	require.NoError(t, err)
	assert.Equal(t, 1, mock.CallCount(), "thinking-off agents take the single-call path")
}

// TestDispatch_NoSplitWhenNoSchema confirms the split predicate
// short-circuits when no OutputSchema is declared. A reasoning-on
// agent without a schema has no JSON to format into.
func TestDispatch_NoSplitWhenNoSchema(t *testing.T) {
	mock := &MockExecutor{
		FormatPrefs: []ModelPreference{{Provider: "anthropic", Tier: "fast"}},
	}
	mock.Reset(MockResponse{Response: &AgentOutput{Content: "free-form response"}})

	d := NewDispatcher(mock)
	def := AgentDef{
		ID:       "no_schema_agent",
		Thinking: "on",
		Models:   []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}
	_, err := d.Dispatch(context.Background(), def, AgentInput{
		Messages: []Message{{Role: "user", Content: "do the thing"}},
	}, DispatchOptions{})

	require.NoError(t, err)
	assert.Equal(t, 1, mock.CallCount(), "no schema means no split")
}

// TestDispatch_NoSplitWhenExecutorOmitsFormatProvider confirms the
// split predicate short-circuits when the executor doesn't implement
// FormatProvider (or returns empty prefs). Defensive — production
// always supplies *Executor which always has format providers from
// models.yaml, but a test mock can omit them, and we don't want the
// dispatcher to misbehave in that case.
func TestDispatch_NoSplitWhenExecutorOmitsFormatProvider(t *testing.T) {
	mock := &MockExecutor{} // No FormatPrefs.
	mock.Reset(MockResponse{Response: &AgentOutput{Content: "single-call response"}})

	d := NewDispatcher(mock)
	def := AgentDef{
		ID:           "spec_challenger",
		Thinking:     "on",
		OutputSchema: "ChallengeBrief",
		Models:       []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}
	_, err := d.Dispatch(context.Background(), def, AgentInput{
		Messages: []Message{{Role: "user", Content: "test"}},
	}, DispatchOptions{})

	require.NoError(t, err)
	assert.Equal(t, 1, mock.CallCount(),
		"executor without FormatPreferences falls back to single-call")
}

// TestDispatch_SplitPropagatesReasoningError surfaces a reasoning-call
// failure cleanly without ever making the format call. Burning a format
// call on a reasoning-step error wastes API spend and confuses the
// trace.
func TestDispatch_SplitPropagatesReasoningError(t *testing.T) {
	mock := &MockExecutor{
		FormatPrefs: []ModelPreference{{Provider: "anthropic", Tier: "fast"}},
	}
	mock.Reset(
		MockResponse{AgentID: "spec_challenger", Err: assertAnError{}},
	)

	d := NewDispatcher(mock)
	def := AgentDef{
		ID:           "spec_challenger",
		Thinking:     "on",
		OutputSchema: "ChallengeBrief",
		Models:       []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}
	_, err := d.Dispatch(context.Background(), def, AgentInput{
		Messages: []Message{{Role: "user", Content: "challenge"}},
	}, DispatchOptions{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reasoning", "error message names the failed phase")
	assert.Equal(t, 1, mock.CallCount(),
		"format call must not fire when reasoning fails")
}

// TestDispatch_SplitRotatesFormatProvidersOnFailure verifies the
// format call walks through FormatPrefs in order when each preceding
// provider errors. Defensive against transient infra failures
// (Gemini's intermittent 503s; rate limits) that the eval surfaced.
func TestDispatch_SplitRotatesFormatProvidersOnFailure(t *testing.T) {
	mock := &MockExecutor{
		FormatPrefs: []ModelPreference{
			{Provider: "anthropic", Tier: "fast"},
			{Provider: "googleai", Tier: "fast"},
			{Provider: "openai", Tier: "fast"},
		},
	}
	mock.Reset(
		// Reasoning succeeds.
		MockResponse{
			AgentID:  "spec_challenger",
			Response: &AgentOutput{Content: "reasoning prose"},
		},
		// First format provider errors with a retryable error.
		MockResponse{AgentID: "spec_challenger-format", Err: ErrRateLimit},
		// Second format provider succeeds.
		MockResponse{
			AgentID:  "spec_challenger-format",
			Response: &AgentOutput{Content: `{"concerns":[]}`},
		},
	)

	d := NewDispatcher(mock)
	def := AgentDef{
		ID:           "spec_challenger",
		Thinking:     "on",
		OutputSchema: "ChallengeBrief",
		Models:       []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}
	_, err := d.Dispatch(context.Background(), def, AgentInput{
		Messages: []Message{{Role: "user", Content: "test"}},
	}, DispatchOptions{})

	require.NoError(t, err)
	calls := mock.Calls()
	// 1 reasoning + 2 format (one failure rotation, one success) = 3 total.
	assert.Equal(t, 3, len(calls), "format rotation runs each preference in turn")
	assert.True(t, strings.HasSuffix(calls[1].Def.ID, "-format"))
	assert.True(t, strings.HasSuffix(calls[2].Def.ID, "-format"))
}

type assertAnError struct{}

func (assertAnError) Error() string { return "reasoning blew up" }
