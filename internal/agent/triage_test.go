package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvaluateAccepted(t *testing.T) {
	goalsBody := "Build a CLI tool for project management."
	input := "Add a status command to show project state."

	verdictJSON := `{"accepted": true, "reason": "Aligns with CLI tool goals", "suggested_labels": ["enhancement", "cli"]}`
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: verdictJSON, Model: "test-model"},
	})

	verdict, err := EvaluateAgainstGoals(context.Background(), mock, goalsBody, input)
	assert.NoError(t, err)
	assert.True(t, verdict.Accepted)
	assert.Equal(t, "Aligns with CLI tool goals", verdict.Reason)
	assert.Equal(t, []string{"enhancement", "cli"}, verdict.SuggestedLabels)
	assert.False(t, verdict.Duplicate)
	assert.Empty(t, verdict.DuplicateOf)

	// Verify the LLM request includes the GOALS.md content.
	calls := mock.Calls()
	assert.Equal(t, 1, len(calls))
	messagesJSON, _ := json.Marshal(calls[0].Request.Messages)
	assert.True(t, strings.Contains(string(messagesJSON), goalsBody),
		"LLM request should include GOALS.md content")
}

func TestEvaluateRejected(t *testing.T) {
	goalsBody := "Build a CLI tool for project management."
	input := "Add a mobile app."

	verdictJSON := `{"accepted": false, "reason": "Mobile app is out of scope"}`
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: verdictJSON, Model: "test-model"},
	})

	verdict, err := EvaluateAgainstGoals(context.Background(), mock, goalsBody, input)
	assert.NoError(t, err)
	assert.False(t, verdict.Accepted)
	assert.Equal(t, "Mobile app is out of scope", verdict.Reason)
	assert.Empty(t, verdict.SuggestedLabels)

	// Verify the LLM request includes the GOALS.md content.
	calls := mock.Calls()
	messagesJSON, _ := json.Marshal(calls[0].Request.Messages)
	assert.True(t, strings.Contains(string(messagesJSON), goalsBody),
		"LLM request should include GOALS.md content")
}

func TestEvaluateDuplicate(t *testing.T) {
	goalsBody := "Build a CLI tool for project management."
	input := "Add a status command."

	verdictJSON := `{"accepted": false, "duplicate": true, "duplicate_of": "feat-status", "reason": "Duplicate of existing status feature"}`
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: verdictJSON, Model: "test-model"},
	})

	verdict, err := EvaluateAgainstGoals(context.Background(), mock, goalsBody, input)
	assert.NoError(t, err)
	assert.False(t, verdict.Accepted)
	assert.True(t, verdict.Duplicate)
	assert.Equal(t, "feat-status", verdict.DuplicateOf)
	assert.Equal(t, "Duplicate of existing status feature", verdict.Reason)

	// Verify the LLM request includes the GOALS.md content.
	calls := mock.Calls()
	messagesJSON, _ := json.Marshal(calls[0].Request.Messages)
	assert.True(t, strings.Contains(string(messagesJSON), goalsBody),
		"LLM request should include GOALS.md content")
}

func TestEvaluateEmptyGoals(t *testing.T) {
	goalsBody := ""
	input := "Add a status command to show project state."

	verdictJSON := `{"accepted": true, "reason": "Looks reasonable", "suggested_labels": ["enhancement"]}`
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: verdictJSON, Model: "test-model"},
	})

	verdict, err := EvaluateAgainstGoals(context.Background(), mock, goalsBody, input)
	assert.NoError(t, err)
	assert.True(t, verdict.Accepted)
	assert.Equal(t, "Looks reasonable", verdict.Reason)

	// Should still call the LLM even with empty goals.
	assert.Equal(t, 1, mock.CallCount())
}

func TestEvaluateLLMError(t *testing.T) {
	goalsBody := "Build a CLI tool for project management."
	input := "Add a status command."

	llmErr := fmt.Errorf("provider unavailable")
	mock := NewMockLLM(MockResponse{Err: llmErr})

	verdict, err := EvaluateAgainstGoals(context.Background(), mock, goalsBody, input)
	assert.ErrorIs(t, err, llmErr)
	assert.Nil(t, verdict)
}

func TestEvaluateMalformedResponse(t *testing.T) {
	goalsBody := "Build a CLI tool for project management."
	input := "Add a status command."

	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: "this is not json at all", Model: "test-model"},
	})

	verdict, err := EvaluateAgainstGoals(context.Background(), mock, goalsBody, input)
	assert.Error(t, err)
	assert.Nil(t, verdict)

	// Verify the LLM was still called.
	assert.Equal(t, 1, mock.CallCount())
}
