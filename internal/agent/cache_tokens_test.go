package agent

import (
	"testing"

	"github.com/chetan/locutus/internal/agent/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOutputFromResponse_CopiesCacheTokens confirms the executor
// copies the cache-token counters from adapters.Response onto
// AgentOutput at both the top level and per-round. The plumbing has
// to land on AgentOutput for the session recorder to pick it up;
// dropping it at the executor seam would silently zero out the
// metadata even when the adapter populated it.
func TestOutputFromResponse_CopiesCacheTokens(t *testing.T) {
	resp := &adapters.Response{
		Content:                  "ok",
		InputTokens:              100,
		OutputTokens:             200,
		CacheCreationInputTokens: 1500, // wrote prefix to cache (first call)
		CacheReadInputTokens:     5298, // read prefix from cache (later calls)
		Rounds: []adapters.Round{
			{Index: 1, InputTokens: 50, OutputTokens: 100, CacheReadInputTokens: 2649},
			{Index: 2, InputTokens: 50, OutputTokens: 100, CacheReadInputTokens: 2649},
		},
	}

	out := outputFromResponse(resp, "claude-sonnet-4-6")

	require.NotNil(t, out)
	assert.Equal(t, 1500, out.CacheCreationInputTokens, "top-level CacheCreationInputTokens must mirror adapter response")
	assert.Equal(t, 5298, out.CacheReadInputTokens, "top-level CacheReadInputTokens must mirror adapter response")

	require.Len(t, out.Rounds, 2, "multi-round response must surface per-round entries")
	for i, r := range out.Rounds {
		assert.Equal(t, resp.Rounds[i].CacheReadInputTokens, r.CacheReadInputTokens,
			"per-round CacheReadInputTokens must round-trip")
	}
}

// TestOutputFromResponse_CacheTokensDefaultZero confirms that
// providers that don't populate cache fields (Gemini today) leave
// the AgentOutput counters at zero — no spurious carry-over from
// uninitialized adapter response.
func TestOutputFromResponse_CacheTokensDefaultZero(t *testing.T) {
	resp := &adapters.Response{
		Content:      "ok",
		InputTokens:  100,
		OutputTokens: 200,
	}

	out := outputFromResponse(resp, "gemini-3-flash-preview")

	assert.Zero(t, out.CacheCreationInputTokens)
	assert.Zero(t, out.CacheReadInputTokens)
}
