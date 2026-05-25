package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRotateModels_PreservesShortLists — single-entry and empty
// preference lists return unchanged. Rotation needs ≥ 2 providers
// to be meaningful.
func TestRotateModels_PreservesShortLists(t *testing.T) {
	assert.Empty(t, rotateModels(nil, 1), "nil prefs round-trip")
	one := []ModelPreference{{Provider: "anthropic", Tier: "balanced"}}
	assert.Equal(t, one, rotateModels(one, 1),
		"single-entry list returns unchanged")
}

// TestRotateModels_RotatesByN — n=1 puts the second entry first;
// n=2 puts the third entry first; n>=len wraps modulo.
func TestRotateModels_RotatesByN(t *testing.T) {
	prefs := []ModelPreference{
		{Provider: "anthropic", Tier: "balanced"},
		{Provider: "googleai", Tier: "balanced"},
		{Provider: "openai", Tier: "balanced"},
	}

	r1 := rotateModels(prefs, 1)
	require.Len(t, r1, 3)
	assert.Equal(t, "googleai", r1[0].Provider, "n=1 promotes second entry")
	assert.Equal(t, "openai", r1[1].Provider)
	assert.Equal(t, "anthropic", r1[2].Provider)

	r2 := rotateModels(prefs, 2)
	require.Len(t, r2, 3)
	assert.Equal(t, "openai", r2[0].Provider, "n=2 promotes third entry")

	r3 := rotateModels(prefs, 3)
	assert.Equal(t, "anthropic", r3[0].Provider,
		"n=len wraps back to the original first entry")

	r4 := rotateModels(prefs, 4)
	assert.Equal(t, "googleai", r4[0].Provider,
		"n=len+1 wraps to the second entry")
}

// TestRotateModels_DoesNotMutateInput — caller's slice must remain
// untouched so re-rotating from the original is correct.
func TestRotateModels_DoesNotMutateInput(t *testing.T) {
	prefs := []ModelPreference{
		{Provider: "anthropic", Tier: "balanced"},
		{Provider: "googleai", Tier: "balanced"},
		{Provider: "openai", Tier: "balanced"},
	}
	_ = rotateModels(prefs, 1)
	assert.Equal(t, "anthropic", prefs[0].Provider,
		"rotation must not mutate the caller's slice")
}

// TestDispatchChallenger_RotatesProviderOnRetry — three degenerate
// rotation attempts must hit three different providers in declaration
// order. Originally the fix for the strat-frontend "all 3 attempts
// hit Anthropic" failure (winplan 2026-05-10); updated for the
// corrective-retry inner loop (default 2 retries on same provider
// before rotation), so the same 3-provider rotation now expands into
// 3 × (1 + 2) = 9 calls with rotation boundaries at indices 0, 3, 6.
func TestDispatchChallenger_RotatesProviderOnRetry(t *testing.T) {
	dummy := MockResponse{Response: &AgentOutput{
		Content: `{"concerns":[{"weakness":"dummy","evidence":"dummy","counterproposal":"dummy"}]}`,
	}}
	// Nine identical degenerate responses: three per provider, three
	// providers. Validator rejects every one → corrective retry × 2
	// per provider, then rotate, repeat.
	responses := make([]MockResponse, 9)
	for i := range responses {
		responses[i] = dummy
	}
	mock := NewMockExecutor(responses...)

	def := AgentDef{
		ID:           "spec-challenger",
		OutputSchema: "ChallengeBrief",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
			{Provider: "openai", Tier: "balanced"},
		},
	}

	_, err := dispatchChallengerWithRetry(context.Background(), NewDispatcher(mock), def, AgentInput{}, "node-x")
	require.Error(t, err, "all attempts return dummy → terminal failure")

	calls := mock.Calls()
	require.Len(t, calls, 9, "3 provider rotations × (1 initial + 2 corrective) = 9 calls")
	// Rotation boundaries: call 0 anthropic, call 3 googleai, call 6 openai.
	assert.Equal(t, "anthropic", calls[0].Def.Models[0].Provider,
		"attempt 1 starts on the first declared provider")
	assert.Equal(t, "anthropic", calls[1].Def.Models[0].Provider,
		"corrective retry 1 stays on anthropic")
	assert.Equal(t, "anthropic", calls[2].Def.Models[0].Provider,
		"corrective retry 2 stays on anthropic")
	assert.Equal(t, "googleai", calls[3].Def.Models[0].Provider,
		"corrective budget exhausted → rotate to googleai")
	assert.Equal(t, "googleai", calls[5].Def.Models[0].Provider,
		"googleai retries stay on googleai")
	assert.Equal(t, "openai", calls[6].Def.Models[0].Provider,
		"third rotation lands on openai")
	assert.Equal(t, "openai", calls[8].Def.Models[0].Provider,
		"openai retries stay on openai")
}
