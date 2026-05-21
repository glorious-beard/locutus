package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chetan/locutus/internal/agent/adapters"
)

// TestBuildAdapterRequestPrependsProseExample exercises the DJ-130
// follow-up wiring: when an agent is single-call (thinking off +
// schema), the executor prepends the registered schema example —
// rendered as labeled prose — as a Cacheable user message before
// the projected input. The system prompt stays byte-stable (no
// JSON appended); the example reaches the model via the user-
// message layer where it gets per-agent cache layering.
//
// Two reciprocal cases:
//   - thinking-off with schema: prose example prepended
//   - thinking-on with schema: NOT prepended (the adapter's
//     runSplit handles example placement on the split path)
func TestBuildAdapterRequestPrependsProseExample(t *testing.T) {
	pick := &ResolvedModel{
		Provider:        ProviderAnthropic,
		Tier:            "balanced",
		Model:           "claude-sonnet-4-6",
		MaxOutputTokens: 4096,
		Thinking:        adapters.ThinkingOff,
	}
	input := AgentInput{
		Messages: []Message{{Role: "user", Content: "do the work"}},
	}

	t.Run("thinking_off_with_schema_prepends_prose_example", func(t *testing.T) {
		def := AgentDef{
			ID:           "scout_singlecall",
			SystemPrompt: "you are scout",
			Thinking:     "off",
			OutputSchema: "ScoutBrief",
		}
		req, err := buildAdapterRequest(def, input, pick, NewToolRegistry(), defaultModelConfig())
		require.NoError(t, err)
		require.Len(t, req.Messages, 2,
			"prose example as user-msg #1, projected input as user-msg #2")

		assert.Equal(t, adapters.RoleUser, req.Messages[0].Role)
		assert.True(t, req.Messages[0].Cacheable,
			"prose example is Cacheable=true so per-agent cross-iteration caching catches it")
		assert.Contains(t, req.Messages[0].Content, "## Example output shape")
		assert.Contains(t, req.Messages[0].Content, "## domain_read",
			"the prose example carries the schema's labeled-prose rendering")

		assert.Equal(t, "do the work", req.Messages[1].Content,
			"projected input survives unchanged as user-msg #2")

		assert.Equal(t, "you are scout", req.SystemPrompt,
			"system prompt stays byte-stable; no Example output block appended")
	})

	t.Run("thinking_on_with_schema_does_not_prepend", func(t *testing.T) {
		// Split-path agent. The adapter's runSplit handles example
		// placement (prose example before projected input on the
		// reasoning pass; prose + JSON examples before reasoning
		// prose on the format pass). Doing it here would put the
		// example in front of a request the adapter then re-splits.
		def := AgentDef{
			ID:           "scout",
			SystemPrompt: "you are scout",
			Thinking:     "on",
			OutputSchema: "ScoutBrief",
		}
		req, err := buildAdapterRequest(def, input, pick, NewToolRegistry(), defaultModelConfig())
		require.NoError(t, err)
		require.Len(t, req.Messages, 1, "no prepend on the split-path request; the adapter handles it")
		assert.Equal(t, "do the work", req.Messages[0].Content)

		// But the prose example is still on the request for the
		// adapter to consume in runSplit.
		assert.NotEmpty(t, req.FormatExampleProse,
			"split-path requests still carry FormatExampleProse for the adapter's runSplit")
		assert.Contains(t, req.FormatExampleProse, "## domain_read")
	})

	t.Run("no_schema_no_prepend", func(t *testing.T) {
		def := AgentDef{
			ID:           "noschema",
			SystemPrompt: "you are noschema",
			Thinking:     "off",
		}
		req, err := buildAdapterRequest(def, input, pick, NewToolRegistry(), defaultModelConfig())
		require.NoError(t, err)
		require.Len(t, req.Messages, 1, "no schema → no example layer; just the projected input")
		assert.Empty(t, req.FormatExampleProse)
	})
}

func defaultModelConfig() *ModelConfig {
	cfg, err := DefaultModelConfig()
	if err != nil {
		panic(err)
	}
	return cfg
}
