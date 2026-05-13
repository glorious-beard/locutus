package agent

import (
	"context"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokeSplitter_RetriesAndRotatesOnValidationFailure(t *testing.T) {
	// Three degenerate responses on anthropic exhaust the corrective-
	// retry budget (1 initial + 2 corrective); googleai's first
	// response (call 4) is clean. Exercises both the inner corrective
	// loop and the outer rotation step.
	wrongCount := ChallengeSplit{
		DecisionShards: []DecisionShard{{DecisionID: "dec-a", Shard: "x"}},
	}
	wrongID := ChallengeSplit{
		DecisionShards: []DecisionShard{
			{DecisionID: "dec-WRONG", Shard: "x"},
			{DecisionID: "dec-b", Shard: "y"},
		},
	}
	good := ChallengeSplit{
		DecisionShards: []DecisionShard{
			{DecisionID: "dec-a", Shard: "shard for a"},
			{DecisionID: "dec-b", Shard: "shard for b"},
		},
		Rationale: "split cleanly",
	}

	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, wrongCount)}},
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, wrongID)}},
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, wrongCount)}},
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, good)}},
	)

	def := AgentDef{
		ID:           "justify_splitter",
		OutputSchema: "ChallengeSplit",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
			{Provider: "openai", Tier: "balanced"},
		},
	}
	in := SplitterInput{
		ParentID:   "strat-foo",
		ParentKind: spec.KindStrategy,
		Challenge:  "challenge text",
		Decisions: []SplitterDecisionRef{
			{ID: "dec-a", Title: "A"},
			{ID: "dec-b", Title: "B"},
		},
	}

	split, err := InvokeSplitter(context.Background(), NewDispatcher(mock), def, in)
	require.NoError(t, err, "googleai's first response must be accepted after anthropic's corrective budget exhausts")
	require.NotNil(t, split)
	assert.Equal(t, "shard for a", split.DecisionShards[0].Shard)

	calls := mock.Calls()
	require.Len(t, calls, 4)
	assert.Equal(t, "anthropic", calls[0].Def.Models[0].Provider, "initial on anthropic")
	assert.Equal(t, "anthropic", calls[1].Def.Models[0].Provider, "corrective retry 1 stays on anthropic")
	assert.Equal(t, "anthropic", calls[2].Def.Models[0].Provider, "corrective retry 2 stays on anthropic")
	assert.Equal(t, "googleai", calls[3].Def.Models[0].Provider, "corrective budget exhausted → rotate")
}

func TestInvokeSplitter_ExhaustsRetriesOnPersistentDegeneracy(t *testing.T) {
	bad := ChallengeSplit{
		DecisionShards: []DecisionShard{{DecisionID: "dec-WRONG", Shard: "x"}},
	}

	// 3 provider rotations × (1 initial + 2 corrective) = 9 calls
	// before terminal failure.
	scripts := make([]MockResponse, 9)
	for i := range scripts {
		scripts[i] = MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, bad)}}
	}
	mock := NewMockExecutor(scripts...)

	def := AgentDef{
		ID:           "justify_splitter",
		OutputSchema: "ChallengeSplit",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
			{Provider: "openai", Tier: "balanced"},
		},
	}
	in := SplitterInput{
		Challenge: "x",
		Decisions: []SplitterDecisionRef{{ID: "dec-a", Title: "A"}},
	}

	_, err := InvokeSplitter(context.Background(), NewDispatcher(mock), def, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "3 provider attempts")
	assert.Contains(t, err.Error(), "2 corrective retries")
	assert.Equal(t, 9, mock.CallCount())
}
