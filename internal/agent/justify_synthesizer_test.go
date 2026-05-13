package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDegenerateSynthesisVerdict_NilOrEmpty(t *testing.T) {
	reason, deg := degenerateSynthesisVerdict(nil)
	assert.True(t, deg)
	assert.Contains(t, reason, "nil")
}

func TestDegenerateSynthesisVerdict_NonEnumVerdict(t *testing.T) {
	v := &SynthesisVerdict{
		Defense: "valid 2-paragraph defense...",
		Verdict: "broke_down because dec-1 broke and...",
	}
	reason, deg := degenerateSynthesisVerdict(v)
	assert.True(t, deg, "verdict containing prose must be flagged")
	assert.Contains(t, reason, "verdict not in enum")
}

func TestDegenerateSynthesisVerdict_EmptyDefense(t *testing.T) {
	v := &SynthesisVerdict{Defense: "", Verdict: "broke_down"}
	reason, deg := degenerateSynthesisVerdict(v)
	assert.True(t, deg)
	assert.Contains(t, reason, "defense is empty")
}

func TestDegenerateSynthesisVerdict_RunawayDefense(t *testing.T) {
	// 9000-rune defense — over the 8000 ceiling. Catches the
	// chain-of-thought-into-output failure mode where the model
	// dumps its scratchpad into a string field.
	long := strings.Repeat("x", 9000)
	v := &SynthesisVerdict{Defense: long, Verdict: "broke_down"}
	reason, deg := degenerateSynthesisVerdict(v)
	assert.True(t, deg)
	assert.Contains(t, reason, "defense is")
}

func TestDegenerateSynthesisVerdict_HealthyVerdict(t *testing.T) {
	v := &SynthesisVerdict{
		Defense:   "Strategy partially holds. Per-decision verdicts concurred.",
		Verdict:   "broke_down",
		Rationale: "dec-1 broke; dec-2 partial; strategy as a whole broke down.",
	}
	_, deg := degenerateSynthesisVerdict(v)
	assert.False(t, deg, "well-formed verdict must not be flagged")
}

func TestInvokeSynthesizer_RetriesAndRotates(t *testing.T) {
	// Three degenerate responses on anthropic exhaust the corrective-
	// retry budget (1 initial + 2 corrective), forcing a rotation to
	// googleai; googleai's first attempt returns clean. Verifies both
	// the inner corrective loop and the outer rotation step.
	bad := SynthesisVerdict{
		Defense: strings.Repeat("x", 9000),
		Verdict: "broke_down",
	}
	good := SynthesisVerdict{
		Defense:   "Real defense paragraph one. Real defense paragraph two.",
		Verdict:   "broke_down",
		Rationale: "aggregation lands on broke_down because dec-1 broke and is load-bearing",
	}

	mock := NewMockExecutor(
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, bad)}},
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, bad)}},
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, bad)}},
		MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, good)}},
	)

	def := AgentDef{
		ID:           "justify_synthesizer",
		OutputSchema: "SynthesisVerdict",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
			{Provider: "openai", Tier: "balanced"},
		},
	}
	in := SynthesisInput{
		ParentID:    "strat-foo",
		ParentKind:  spec.KindStrategy,
		ParentTitle: "Foo",
		Challenge:   "challenge text",
		PerDecisionResults: []PerDecisionResult{
			{DecisionID: "dec-x", Verdict: "broke_down", Defense: "x"},
		},
	}

	verdict, err := InvokeSynthesizer(context.Background(), NewDispatcher(mock), def, in)
	require.NoError(t, err, "googleai's first response must be accepted after anthropic's corrective budget exhausts")
	require.NotNil(t, verdict)
	assert.Equal(t, "broke_down", verdict.Verdict)

	calls := mock.Calls()
	require.Len(t, calls, 4, "3 calls on anthropic (initial + 2 corrective) + 1 on googleai")
	assert.Equal(t, "anthropic", calls[0].Def.Models[0].Provider, "initial hits the first provider")
	assert.Equal(t, "anthropic", calls[1].Def.Models[0].Provider, "corrective retry 1 stays on anthropic")
	assert.Equal(t, "anthropic", calls[2].Def.Models[0].Provider, "corrective retry 2 stays on anthropic")
	assert.Equal(t, "googleai", calls[3].Def.Models[0].Provider, "corrective budget exhausted → rotate to googleai")
}

func TestInvokeSynthesizer_ExhaustsRetriesOnPersistentDegeneracy(t *testing.T) {
	bad := SynthesisVerdict{
		Defense: "fine",
		Verdict: "broke_down with extra prose that fails the enum check",
	}

	// Persistent degeneracy: synthesizerMaxAttempts (3) provider
	// rotations × (1 initial + defaultCorrectiveRetries (2)) = 9 calls
	// before terminal failure.
	scripts := make([]MockResponse, 9)
	for i := range scripts {
		scripts[i] = MockResponse{Response: &AgentOutput{Content: mustJSONFor(t, bad)}}
	}
	mock := NewMockExecutor(scripts...)

	def := AgentDef{
		ID:           "justify_synthesizer",
		OutputSchema: "SynthesisVerdict",
		Models: []ModelPreference{
			{Provider: "anthropic", Tier: "balanced"},
			{Provider: "googleai", Tier: "balanced"},
			{Provider: "openai", Tier: "balanced"},
		},
	}
	in := SynthesisInput{
		ParentID: "strat-foo",
		PerDecisionResults: []PerDecisionResult{
			{DecisionID: "dec-x", Verdict: "broke_down", Defense: "x"},
		},
	}

	_, err := InvokeSynthesizer(context.Background(), NewDispatcher(mock), def, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "3 provider attempts")
	assert.Contains(t, err.Error(), "2 corrective retries")
	assert.Equal(t, 9, mock.CallCount(),
		"3 provider attempts × (1 initial + 2 corrective) = 9 calls before terminal failure")
}

func mustJSONFor(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}
