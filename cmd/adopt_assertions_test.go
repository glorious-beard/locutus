package cmd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/eval"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func judgeMock(t *testing.T, passed bool, reasoning string) *agent.MockExecutor {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"passed":     passed,
		"reasoning":  reasoning,
		"confidence": 0.9,
	})
	require.NoError(t, err)
	return agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: string(payload)},
	})
}

func TestEvaluateAssertionLLMReviewPassing(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll("internal/auth", 0o755))
	require.NoError(t, fs.WriteFile("internal/auth/middleware.go",
		[]byte("package auth\n// OAuth2 middleware\n"), 0o644))

	runner := eval.NewRunner(judgeMock(t, true, "middleware is wired"))
	approach := spec.Approach{
		ID:            "app-auth",
		Title:         "Auth implementation",
		Body:          "Implement OAuth2 middleware.",
		ArtifactPaths: []string{"internal/auth/middleware.go"},
	}
	assertion := spec.Assertion{
		Kind:   spec.AssertionKindLLMReview,
		Prompt: "Verify OAuth2 middleware is wired",
	}

	passed, output := evaluateAssertion(context.Background(), assertion, approach, "", runner, fs)
	assert.True(t, passed)
	assert.Contains(t, output, "wired")
}

func TestEvaluateAssertionLLMReviewFailing(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll("internal/auth", 0o755))
	require.NoError(t, fs.WriteFile("internal/auth/middleware.go", []byte(""), 0o644))

	runner := eval.NewRunner(judgeMock(t, false, "middleware file is empty"))
	approach := spec.Approach{
		ID:            "app-auth",
		Title:         "Auth impl",
		ArtifactPaths: []string{"internal/auth/middleware.go"},
	}
	assertion := spec.Assertion{Kind: spec.AssertionKindLLMReview, Prompt: "check"}

	passed, output := evaluateAssertion(context.Background(), assertion, approach, "", runner, fs)
	assert.False(t, passed)
	assert.Contains(t, output, "empty")
}

func TestEvaluateAssertionLLMReviewNilRunnerReturnsExplicitError(t *testing.T) {
	fs := specio.NewMemFS()
	approach := spec.Approach{ID: "app-auth"}
	assertion := spec.Assertion{Kind: spec.AssertionKindLLMReview, Prompt: "check"}

	passed, output := evaluateAssertion(context.Background(), assertion, approach, "", nil, fs)
	assert.False(t, passed)
	assert.Contains(t, output, "llm")
}

func TestEvaluateAssertionLLMReviewMissingArtifactContinues(t *testing.T) {
	fs := specio.NewMemFS()
	runner := eval.NewRunner(judgeMock(t, false, "file missing"))
	approach := spec.Approach{
		ID:            "app-auth",
		ArtifactPaths: []string{"does/not/exist.go"},
	}
	assertion := spec.Assertion{Kind: spec.AssertionKindLLMReview, Prompt: "check"}

	passed, _ := evaluateAssertion(context.Background(), assertion, approach, "", runner, fs)
	assert.False(t, passed, "missing artifact surfaces as judge verdict, not panic")
}

func TestRunAssertionsMixedKinds(t *testing.T) {
	fs := specio.NewMemFS()
	require.NoError(t, fs.MkdirAll("internal/auth", 0o755))
	require.NoError(t, fs.WriteFile("internal/auth/middleware.go", []byte("package auth"), 0o644))

	runner := eval.NewRunner(judgeMock(t, true, "ok"))
	approach := spec.Approach{
		ID:            "app-auth",
		ArtifactPaths: []string{"internal/auth/middleware.go"},
		Assertions: []spec.Assertion{
			{Kind: spec.AssertionKindFileExists, Target: "internal/auth/middleware.go"},
			{Kind: spec.AssertionKindLLMReview, Prompt: "check shape"},
		},
	}

	results := runAssertions(context.Background(), approach, "", runner, fs)
	require.Len(t, results, 2)
	// FileExists runs against the mem FS only if repoDir-based file lookup
	// finds it; with repoDir="" and a MemFS adopt path, it won't. What we
	// assert here is shape — two results, both evaluated, llm_review
	// populated with reasoning from the mock.
	assert.Equal(t, spec.AssertionKindFileExists, results[0].Assertion.Kind)
	assert.Equal(t, spec.AssertionKindLLMReview, results[1].Assertion.Kind)
	assert.True(t, results[1].Passed)
	assert.Contains(t, results[1].Output, "ok")
}
