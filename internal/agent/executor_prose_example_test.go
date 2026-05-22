package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chetan/locutus/internal/agent/adapters"
)

// TestBuildAdapterRequestDoesNotPrependExample documents the
// reversal of the brief experiment where the executor prepended the
// registered schema example as a Cacheable user message before the
// projected input on single-call (thinking-off + schema) requests.
// The fifth winplan re-run surfaced verbatim example-content bleed
// (scout copying example concern_disposition text; elaborator
// emitting example "MySQL" alternative with its example
// rejected_because intact). Examples now reach the model only via
// the format pass's one-shot demonstration; reasoning-pass and
// single-call paths get the schema descriptions (struct tags) and
// strict-mode enforcement for shape, with no rendered example.
//
// FormatExampleProse / FormatExampleDoc still populate on the
// request for adapters that consume them on the format pass.
func TestBuildAdapterRequestDoesNotPrependExample(t *testing.T) {
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

	t.Run("thinking_off_with_schema_passes_input_unchanged", func(t *testing.T) {
		def := AgentDef{
			ID:           "scout_singlecall",
			SystemPrompt: "you are scout",
			Thinking:     "off",
			OutputSchema: "ScoutBrief",
		}
		req, err := buildAdapterRequest(def, input, pick, NewToolRegistry(), defaultModelConfig())
		require.NoError(t, err)
		require.Len(t, req.Messages, 1,
			"projected input passes through unchanged; no example prepend")
		assert.Equal(t, "do the work", req.Messages[0].Content)
		assert.Equal(t, "you are scout", req.SystemPrompt,
			"system prompt stays byte-stable across calls of the same agent")

		// Example fields still populate for the format pass to use
		// when the request gets split. The adapter's runSplit
		// consumes FormatExampleDoc + FormatExampleProse on the
		// format pass only.
		assert.NotEmpty(t, req.FormatExampleProse,
			"FormatExampleProse populates so the format pass can render it")
		assert.NotEmpty(t, req.FormatExampleDoc,
			"FormatExampleDoc populates so the format pass can render it")
	})

	t.Run("thinking_on_with_schema_passes_input_unchanged", func(t *testing.T) {
		def := AgentDef{
			ID:           "scout",
			SystemPrompt: "you are scout",
			Thinking:     "on",
			OutputSchema: "ScoutBrief",
		}
		req, err := buildAdapterRequest(def, input, pick, NewToolRegistry(), defaultModelConfig())
		require.NoError(t, err)
		require.Len(t, req.Messages, 1, "projected input passes through unchanged")
		assert.Equal(t, "do the work", req.Messages[0].Content)
	})

	t.Run("no_schema_no_example_fields", func(t *testing.T) {
		def := AgentDef{
			ID:           "noschema",
			SystemPrompt: "you are noschema",
			Thinking:     "off",
		}
		req, err := buildAdapterRequest(def, input, pick, NewToolRegistry(), defaultModelConfig())
		require.NoError(t, err)
		require.Len(t, req.Messages, 1)
		assert.Empty(t, req.FormatExampleProse)
		assert.Empty(t, req.FormatExampleDoc)
	})
}

func defaultModelConfig() *ModelConfig {
	cfg, err := DefaultModelConfig()
	if err != nil {
		panic(err)
	}
	return cfg
}
