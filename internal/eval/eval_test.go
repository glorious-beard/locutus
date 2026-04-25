package eval_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/eval"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptJudge returns a MockLLM that emits one LLM-judge JSON response.
func scriptJudge(t *testing.T, passed bool, reasoning string, confidence float64) *agent.MockLLM {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"passed":     passed,
		"reasoning":  reasoning,
		"confidence": confidence,
	})
	require.NoError(t, err)
	return agent.NewMockLLM(agent.MockResponse{
		Response: &agent.GenerateResponse{Content: string(payload)},
	})
}

func fixtureApproach() spec.Approach {
	return spec.Approach{
		ID:            "app-auth",
		Title:         "Auth implementation",
		ParentID:      "feat-auth",
		Body:          "Implement OAuth2 middleware per strat-go.",
		ArtifactPaths: []string{"internal/auth/middleware.go"},
	}
}

func fixtureAssertion(prompt string) spec.Assertion {
	return spec.Assertion{
		Kind:    spec.AssertionKindLLMReview,
		Prompt:  prompt,
		Message: "reviewer must verify auth flow",
	}
}

func TestLLMJudgePassing(t *testing.T) {
	llm := scriptJudge(t, true, "OAuth2 middleware present and wired", 0.92)
	judge := &eval.LLMJudge{LLM: llm}

	metric, err := judge.Evaluate(context.Background(), eval.EvalCase{
		Approach:  fixtureApproach(),
		Assertion: fixtureAssertion("Verify OAuth2 middleware is wired"),
		Artifacts: map[string]string{"internal/auth/middleware.go": "package auth\n// OAuth2 middleware\n"},
	})
	require.NoError(t, err)
	require.NotNil(t, metric)
	assert.True(t, metric.Passed)
	assert.Equal(t, "llm_judge", metric.EvaluatorName)
	assert.InDelta(t, 0.92, metric.Confidence, 0.001)
	assert.Contains(t, metric.Reasoning, "OAuth2")
}

func TestLLMJudgeFailing(t *testing.T) {
	llm := scriptJudge(t, false, "middleware file is empty", 0.85)
	judge := &eval.LLMJudge{LLM: llm}

	metric, err := judge.Evaluate(context.Background(), eval.EvalCase{
		Approach:  fixtureApproach(),
		Assertion: fixtureAssertion("Verify middleware is wired"),
		Artifacts: map[string]string{"internal/auth/middleware.go": ""},
	})
	require.NoError(t, err)
	assert.False(t, metric.Passed)
	assert.Contains(t, metric.Reasoning, "empty")
}

func TestLLMJudgeNilLLMReturnsError(t *testing.T) {
	judge := &eval.LLMJudge{LLM: nil}
	_, err := judge.Evaluate(context.Background(), eval.EvalCase{
		Approach:  fixtureApproach(),
		Assertion: fixtureAssertion("anything"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm")
}

func TestLLMJudgePromptContainsTriad(t *testing.T) {
	llm := scriptJudge(t, true, "ok", 1.0)
	judge := &eval.LLMJudge{LLM: llm}

	_, err := judge.Evaluate(context.Background(), eval.EvalCase{
		Approach:  fixtureApproach(),
		Assertion: fixtureAssertion("Verify-this-sentinel-uniq"),
		Artifacts: map[string]string{"internal/auth/middleware.go": "package auth // artifact-sentinel-content"},
	})
	require.NoError(t, err)

	calls := llm.Calls()
	require.Len(t, calls, 1)
	var prompt strings.Builder
	for _, m := range calls[0].Request.Messages {
		prompt.WriteString(m.Content)
		prompt.WriteString("\n")
	}
	p := prompt.String()

	assert.Contains(t, p, "app-auth", "approach ID")
	assert.Contains(t, p, "OAuth2 middleware per strat-go", "approach body")
	assert.Contains(t, p, "Verify-this-sentinel-uniq", "assertion prompt")
	assert.Contains(t, p, "internal/auth/middleware.go", "artifact path")
	assert.Contains(t, p, "artifact-sentinel-content", "artifact body")
}

func TestLLMJudgeArtifactTruncation(t *testing.T) {
	llm := scriptJudge(t, true, "ok", 1.0)
	judge := &eval.LLMJudge{LLM: llm, ArtifactCapBytes: 100}

	big := strings.Repeat("X", 5000)
	_, err := judge.Evaluate(context.Background(), eval.EvalCase{
		Approach:  fixtureApproach(),
		Assertion: fixtureAssertion("check"),
		Artifacts: map[string]string{
			"big.go":   big,
			"small.go": "package small",
		},
	})
	require.NoError(t, err)

	calls := llm.Calls()
	require.Len(t, calls, 1)
	var prompt strings.Builder
	for _, m := range calls[0].Request.Messages {
		prompt.WriteString(m.Content)
	}
	p := prompt.String()

	assert.NotContains(t, p, big, "full big file must not be in prompt")
	assert.Contains(t, p, "truncated", "truncation marker must be present")
	assert.Contains(t, p, "package small", "small file must be uncapped")
}

func TestRunnerRoutesByAssertionKind(t *testing.T) {
	llm := scriptJudge(t, true, "ok", 0.88)
	r := eval.NewRunner(llm)

	metric, err := r.Evaluate(context.Background(), spec.AssertionKindLLMReview, eval.EvalCase{
		Approach:  fixtureApproach(),
		Assertion: fixtureAssertion("check"),
	})
	require.NoError(t, err)
	assert.True(t, metric.Passed)
	assert.Equal(t, "llm_judge", metric.EvaluatorName)
}

func TestRunnerUnregisteredKindErrors(t *testing.T) {
	r := eval.NewRunner(nil)
	_, err := r.Evaluate(context.Background(), spec.AssertionKindTestPass, eval.EvalCase{
		Approach:  fixtureApproach(),
		Assertion: spec.Assertion{Kind: spec.AssertionKindTestPass},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, eval.ErrNoEvaluator)
}

// stubEvaluator lets us verify Register dispatches to a custom evaluator.
type stubEvaluator struct{ called bool }

func (s *stubEvaluator) Name() string { return "stub" }
func (s *stubEvaluator) Evaluate(ctx context.Context, c eval.EvalCase) (*eval.EvalMetric, error) {
	s.called = true
	return &eval.EvalMetric{EvaluatorName: "stub", Passed: true, Score: 1.0}, nil
}

func TestRunnerRegisterCustomEvaluator(t *testing.T) {
	r := eval.NewRunner(nil)
	s := &stubEvaluator{}
	r.Register(spec.AssertionKindTestPass, s)

	metric, err := r.Evaluate(context.Background(), spec.AssertionKindTestPass, eval.EvalCase{
		Approach:  fixtureApproach(),
		Assertion: spec.Assertion{Kind: spec.AssertionKindTestPass},
	})
	require.NoError(t, err)
	assert.True(t, s.called)
	assert.Equal(t, "stub", metric.EvaluatorName)
}
