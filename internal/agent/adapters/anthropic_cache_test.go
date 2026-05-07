package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildAnthropicMessages_CacheableMarksBlock confirms (DJ-106)
// that a Message with Cacheable=true is emitted as a TextBlock with
// the cache_control marker set. Anthropic places the cache marker
// positionally — everything in the request up to and including the
// marked block becomes the cacheable prefix. The projection layer
// emits Cacheable=true on the static prefix it wants the API to
// cache (GOALS + scout brief + outline) and Cacheable=false on the
// per-call variation.
func TestBuildAnthropicMessages_CacheableMarksBlock(t *testing.T) {
	in := []Message{
		{Role: RoleUser, Content: "static prefix shared across the council fanout", Cacheable: true},
		{Role: RoleUser, Content: "per-call variation"},
	}

	out := buildAnthropicMessages(in)

	// Two same-role messages must collapse into ONE MessageParam
	// with multiple TextBlocks. Anthropic rejects consecutive
	// same-role messages at the API level (alternation required),
	// and the cache marker is positional within a single message,
	// so grouping is mandatory, not optional.
	require.Len(t, out, 1, "adjacent same-role messages must merge into one MessageParam")
	require.Len(t, out[0].Content, 2, "merged message must carry both blocks as separate TextBlocks")

	prefix := out[0].Content[0].OfText
	require.NotNil(t, prefix, "first block must be a TextBlock")
	assert.Equal(t, "static prefix shared across the council fanout", prefix.Text)
	assert.NotEmpty(t, string(prefix.CacheControl.Type), "Cacheable=true block must carry cache_control (Type set to ephemeral)")

	suffix := out[0].Content[1].OfText
	require.NotNil(t, suffix, "second block must be a TextBlock")
	assert.Equal(t, "per-call variation", suffix.Text)
	assert.Empty(t, string(suffix.CacheControl.Type), "Cacheable=false block must NOT carry cache_control")
}

// TestBuildAnthropicMessages_NoCacheableUnchanged ensures the
// pre-DJ-106 behavior is preserved when Cacheable is unset on every
// message. Each message → its own MessageParam with one TextBlock
// (since adapter callers may rely on per-message structure when
// caching isn't in play).
func TestBuildAnthropicMessages_NoCacheableUnchanged(t *testing.T) {
	in := []Message{
		{Role: RoleUser, Content: "first user turn"},
		{Role: RoleAssistant, Content: "model response"},
		{Role: RoleUser, Content: "follow-up"},
	}

	out := buildAnthropicMessages(in)

	require.Len(t, out, 3, "without Cacheable hints, each Message stays its own MessageParam")
	for i := range out {
		require.Len(t, out[i].Content, 1)
		require.NotNil(t, out[i].Content[0].OfText)
		assert.Empty(t, string(out[i].Content[0].OfText.CacheControl.Type), "no Cacheable hint → no cache_control")
	}
}
