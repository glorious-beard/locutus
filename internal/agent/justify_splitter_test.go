package agent

import (
	"context"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokeSplitter_RetriesAndRotatesOnValidationFailure(t *testing.T) {
	// First attempt: shard count mismatch (returns 1 shard for 2
	// input decisions). Second attempt: id mismatch (right count,
	// wrong id). Third attempt: clean.
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

	split, err := InvokeSplitter(context.Background(), mock, def, in)
	require.NoError(t, err, "third attempt's clean output must succeed")
	require.NotNil(t, split)
	assert.Equal(t, "shard for a", split.DecisionShards[0].Shard)

	calls := mock.Calls()
	require.Len(t, calls, 3)
	assert.Equal(t, "anthropic", calls[0].Def.Models[0].Provider)
	assert.Equal(t, "googleai", calls[1].Def.Models[0].Provider)
	assert.Equal(t, "openai", calls[2].Def.Models[0].Provider)
}

func TestInvokeSplitter_ExhaustsRetriesOnPersistentDegeneracy(t *testing.T) {
	bad := ChallengeSplit{
		DecisionShards: []DecisionShard{{DecisionID: "dec-WRONG", Shard: "x"}},
	}

	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, bad)}},
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, bad)}},
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, bad)}},
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
		Challenge: "x",
		Decisions: []SplitterDecisionRef{{ID: "dec-a", Title: "A"}},
	}

	_, err := InvokeSplitter(context.Background(), mock, def, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "degenerate output after 3 attempts")
	assert.Equal(t, 3, mock.CallCount())
}
