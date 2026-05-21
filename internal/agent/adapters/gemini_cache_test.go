package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
)

// TestGeminiUsageSurfacesCacheReadTokens (DJ-130 follow-up)
// verifies the Gemini adapter extracts CachedContentTokenCount from
// the SDK's UsageMetadata and surfaces it as the per-call cache-read
// count. Without this, operators tail per-call YAMLs and see zero
// cache_read_input_tokens on every Gemini call even when the
// prefix-cache layering DJ-130 designed for is firing — implicit
// caching on Pro models would be invisible.
func TestGeminiUsageSurfacesCacheReadTokens(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        12000,
			CandidatesTokenCount:    800,
			ThoughtsTokenCount:      200,
			TotalTokenCount:         13000,
			CachedContentTokenCount: 9500, // implicit-cache hit: 9.5k of the 12k prompt was served from cache
		},
	}

	u := geminiUsage(resp)
	assert.Equal(t, 12000, u.in,
		"PromptTokenCount maps to in — INCLUDES the cached tokens per Gemini's contract")
	assert.Equal(t, 800, u.out)
	assert.Equal(t, 200, u.thoughts)
	assert.Equal(t, 13000, u.total)
	assert.Equal(t, 9500, u.cacheRead,
		"CachedContentTokenCount surfaces on cacheRead so per-call traces show cache hits")
}

// TestGeminiUsageZeroCacheReadWhenCold confirms an uncached call
// reports cacheRead=0 — the most common case on the first call
// before the prefix cache warms up.
func TestGeminiUsageZeroCacheReadWhenCold(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     12000,
			CandidatesTokenCount: 800,
			TotalTokenCount:      12800,
			// CachedContentTokenCount omitted = 0
		},
	}

	u := geminiUsage(resp)
	assert.Equal(t, 0, u.cacheRead,
		"unset CachedContentTokenCount → cacheRead is zero; no cache hit on a cold call")
}

// TestGeminiUsageNilResponseSafety guards against a nil
// UsageMetadata (rare but possible on adapter-level error paths
// before the SDK populated the response) producing a zero-value
// struct rather than a nil panic.
func TestGeminiUsageNilResponseSafety(t *testing.T) {
	assert.Equal(t, geminiUsageTotals{}, geminiUsage(nil))
	assert.Equal(t, geminiUsageTotals{}, geminiUsage(&genai.GenerateContentResponse{}))
}
