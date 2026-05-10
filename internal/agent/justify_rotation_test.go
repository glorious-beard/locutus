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
// attempts must hit three different providers in declaration order.
// This is the actual fix for the strat-frontend "all 3 attempts hit
// Anthropic" failure observed in winplan on 2026-05-10.
func TestDispatchChallenger_RotatesProviderOnRetry(t *testing.T) {
	dummy := MockResponse{Response: &AgentOutput{
		Content: `{"concerns":[{"weakness":"dummy","evidence":"dummy","counterproposal":"dummy"}]}`,
	}}
	mock := NewMockExecutor(dummy, dummy, dummy)

	def := AgentDef{
		ID:           "spec_challenger",
		OutputSchema: "ChallengeBrief",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
			{Provider: "openai", Tier: "balanced"},
		},
	}

	_, err := dispatchChallengerWithRetry(context.Background(), mock, def, AgentInput{}, "node-x")
	require.Error(t, err, "all 3 attempts return dummy → terminal failure")

	calls := mock.Calls()
	require.Len(t, calls, 3, "all 3 attempts must dispatch")
	assert.Equal(t, "anthropic", calls[0].Def.Models[0].Provider,
		"attempt 1 hits the first declared provider")
	assert.Equal(t, "googleai", calls[1].Def.Models[0].Provider,
		"attempt 2 rotates to the second provider")
	assert.Equal(t, "openai", calls[2].Def.Models[0].Provider,
		"attempt 3 rotates to the third provider")
}
