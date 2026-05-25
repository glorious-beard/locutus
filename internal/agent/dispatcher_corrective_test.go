package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// degenerateOnce returns a Validator that rejects the first N outputs
// (with the given reason) and accepts the (N+1)th. Used to assert the
// dispatcher recovers correctly within a single provider when the
// model "fixes itself" after corrective feedback.
func degenerateOnce(reason string, rejections int) func(*AgentOutput) (string, bool) {
	calls := 0
	return func(_ *AgentOutput) (string, bool) {
		calls++
		if calls <= rejections {
			return reason, true
		}
		return "", false
	}
}

// TestDispatch_CorrectiveRetryRecoversOnSameProvider — the canonical
// success path. Provider returns one degenerate response, dispatcher
// appends a corrective turn, same provider returns valid output on
// retry. Total 2 calls, both on anthropic, no rotation.
func TestDispatch_CorrectiveRetryRecoversOnSameProvider(t *testing.T) {
	first := MockResponse{Response: &AgentOutput{Content: `degenerate`}}
	second := MockResponse{Response: &AgentOutput{Content: `clean`}}
	mock := NewMockExecutor(first, second)

	def := AgentDef{
		ID: "spec-challenger",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
		},
	}

	out, err := NewDispatcher(mock).Dispatch(context.Background(), def, AgentInput{
		Messages: []Message{{Role: "user", Content: "do the thing"}},
	}, DispatchOptions{
		MaxAttempts: 2,
		Validator:   degenerateOnce("placeholder echoed", 1),
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "clean", out.Content)

	calls := mock.Calls()
	require.Len(t, calls, 2)
	assert.Equal(t, "anthropic", calls[0].Def.Models[0].Provider,
		"first call hits the first provider")
	assert.Equal(t, "anthropic", calls[1].Def.Models[0].Provider,
		"corrective retry stays on the same provider (cache stays warm)")
}

// TestDispatch_CorrectiveTurnCarriesReason — the second call's input
// must contain the validator's reason verbatim in a user turn that
// follows the model's prior response. This is the load-bearing
// content: without the reason the model has nothing specific to
// correct against.
func TestDispatch_CorrectiveTurnCarriesReason(t *testing.T) {
	first := MockResponse{Response: &AgentOutput{Content: `bad output`}}
	second := MockResponse{Response: &AgentOutput{Content: `good`}}
	mock := NewMockExecutor(first, second)

	def := AgentDef{ID: "x", Models: []ModelPreference{{Provider: "anthropic", Tier: "balanced"}}}

	_, err := NewDispatcher(mock).Dispatch(context.Background(), def, AgentInput{
		Messages: []Message{{Role: "user", Content: "original request"}},
	}, DispatchOptions{
		MaxAttempts: 1,
		Validator:   degenerateOnce("verdict not in enum (got \"foo\")", 1),
	})
	require.NoError(t, err)

	calls := mock.Calls()
	require.Len(t, calls, 2)

	// Second call's input: original user turn + assistant turn (prior
	// response) + user turn (corrective feedback). Three messages.
	secondMessages := calls[1].Input.Messages
	require.Len(t, secondMessages, 3,
		"second call must carry the original prompt + assistant turn + corrective user turn")
	assert.Equal(t, "user", secondMessages[0].Role)
	assert.Equal(t, "original request", secondMessages[0].Content)
	assert.Equal(t, "assistant", secondMessages[1].Role)
	assert.Equal(t, "bad output", secondMessages[1].Content,
		"assistant turn must echo the prior degenerate response")
	assert.Equal(t, "user", secondMessages[2].Role)
	assert.Contains(t, secondMessages[2].Content, "verdict not in enum (got \"foo\")",
		"corrective turn must carry the validator's reason verbatim")
}

// TestDispatch_CorrectiveBudgetExhaustedRotatesProviders — after
// burning the corrective-retry budget on a provider, the dispatcher
// rotates to the next provider, NOT before. With CorrectiveRetries=2
// and 2 providers, the call sequence is:
//   1. anthropic initial  (degenerate)
//   2. anthropic corrective #1 (degenerate)
//   3. anthropic corrective #2 (degenerate)
//   4. googleai initial   (clean — succeed)
func TestDispatch_CorrectiveBudgetExhaustedRotatesProviders(t *testing.T) {
	dummy := MockResponse{Response: &AgentOutput{Content: `dummy`}}
	clean := MockResponse{Response: &AgentOutput{Content: `clean`}}
	mock := NewMockExecutor(dummy, dummy, dummy, clean)

	def := AgentDef{
		ID: "agent",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
		},
	}

	out, err := NewDispatcher(mock).Dispatch(context.Background(), def, AgentInput{}, DispatchOptions{
		MaxAttempts: 2,
		Validator:   degenerateOnce("dummy", 3),
	})
	require.NoError(t, err)
	assert.Equal(t, "clean", out.Content)

	calls := mock.Calls()
	require.Len(t, calls, 4)
	assert.Equal(t, "anthropic", calls[0].Def.Models[0].Provider)
	assert.Equal(t, "anthropic", calls[1].Def.Models[0].Provider, "corrective #1 stays on anthropic")
	assert.Equal(t, "anthropic", calls[2].Def.Models[0].Provider, "corrective #2 stays on anthropic")
	assert.Equal(t, "googleai", calls[3].Def.Models[0].Provider, "rotation only after corrective budget exhausted")
}

// TestDispatch_CorrectiveBudgetResetsAfterRotation — when rotation
// fires, the new provider starts with a fresh corrective budget AND
// a fresh (uncorrupted-by-prior-provider) prompt. The corrective
// turns accumulated against anthropic don't follow the conversation
// onto googleai; googleai sees the original prompt.
func TestDispatch_CorrectiveBudgetResetsAfterRotation(t *testing.T) {
	dummy := MockResponse{Response: &AgentOutput{Content: `dummy`}}
	clean := MockResponse{Response: &AgentOutput{Content: `clean`}}
	mock := NewMockExecutor(dummy, dummy, dummy, clean)

	def := AgentDef{
		ID: "agent",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
		},
	}

	originalMessage := "original prompt"
	_, err := NewDispatcher(mock).Dispatch(context.Background(), def, AgentInput{
		Messages: []Message{{Role: "user", Content: originalMessage}},
	}, DispatchOptions{
		MaxAttempts: 2,
		Validator:   degenerateOnce("dummy", 3),
	})
	require.NoError(t, err)

	calls := mock.Calls()
	require.Len(t, calls, 4)

	// Call 4 (the first googleai call): must see ONLY the original
	// user turn. The 4 corrective turns accumulated against anthropic
	// must not have followed.
	googleaiInput := calls[3].Input.Messages
	require.Len(t, googleaiInput, 1, "googleai sees only the original user turn (corrective context resets)")
	assert.Equal(t, originalMessage, googleaiInput[0].Content)
}

// TestDispatch_CorrectiveRetriesDisabled — passing CorrectiveRetries:
// -1 restores the legacy single-shot-per-provider behaviour. Useful
// for tests that need to verify rotation without the inner loop, and
// for any caller that wants opt-out semantics.
func TestDispatch_CorrectiveRetriesDisabled(t *testing.T) {
	dummy := MockResponse{Response: &AgentOutput{Content: `dummy`}}
	clean := MockResponse{Response: &AgentOutput{Content: `clean`}}
	mock := NewMockExecutor(dummy, clean)

	def := AgentDef{
		ID: "agent",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
		},
	}

	out, err := NewDispatcher(mock).Dispatch(context.Background(), def, AgentInput{}, DispatchOptions{
		MaxAttempts:       2,
		CorrectiveRetries: -1, // opt out: no corrective retry, immediate rotation on degeneracy
		Validator:         degenerateOnce("dummy", 1),
	})
	require.NoError(t, err)
	assert.Equal(t, "clean", out.Content)

	calls := mock.Calls()
	require.Len(t, calls, 2, "without corrective retries, degenerate output → immediate rotation")
	assert.Equal(t, "anthropic", calls[0].Def.Models[0].Provider)
	assert.Equal(t, "googleai", calls[1].Def.Models[0].Provider)
}

// TestDispatch_ErrorReportsCorrectiveAndRotationCount — terminal
// failure error message names both budgets so the operator can see
// the dispatcher exhausted the full retry surface, not just the
// outer rotation loop.
func TestDispatch_ErrorReportsCorrectiveAndRotationCount(t *testing.T) {
	dummy := MockResponse{Response: &AgentOutput{Content: `dummy`}}
	responses := make([]MockResponse, 6)
	for i := range responses {
		responses[i] = dummy
	}
	mock := NewMockExecutor(responses...)

	def := AgentDef{
		ID: "agent",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
		},
	}

	_, err := NewDispatcher(mock).Dispatch(context.Background(), def, AgentInput{}, DispatchOptions{
		MaxAttempts: 2,
		Validator:   degenerateOnce("always degenerate", 100),
	})
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "2 provider attempts") &&
			strings.Contains(err.Error(), "2 corrective retries"),
		"error must name both retry budgets, got: %s", err.Error())
}
