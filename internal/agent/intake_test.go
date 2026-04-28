package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIntakeAccepted(t *testing.T) {
	goalsBody := "Build a CLI tool for project management."
	input := "Add a status command to show project state."

	resultJSON := `{"id":"feat-status","title":"Status command","accepted":true,"reason":"Aligns with CLI tool goals","suggested_labels":["enhancement","cli"]}`
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: resultJSON, Model: "test-model"},
	})

	r, err := IntakeDocument(context.Background(), mock, "feature", input, goalsBody)
	assert.NoError(t, err)
	assert.Equal(t, "feat-status", r.ID)
	assert.Equal(t, "Status command", r.Title)
	assert.True(t, r.Accepted)
	assert.Equal(t, "Aligns with CLI tool goals", r.Reason)
	assert.Equal(t, []string{"enhancement", "cli"}, r.SuggestedLabels)
	assert.False(t, r.Duplicate)
	assert.Empty(t, r.DuplicateOf)

	calls := mock.Calls()
	assert.Equal(t, 1, len(calls))
	messagesJSON, _ := json.Marshal(calls[0].Request.Messages)
	assert.True(t, strings.Contains(string(messagesJSON), goalsBody),
		"LLM request should include GOALS.md content")
}

func TestIntakeRejected(t *testing.T) {
	goalsBody := "Build a CLI tool for project management."
	input := "Add a mobile app."

	resultJSON := `{"id":"feat-mobile-app","title":"Mobile app","accepted":false,"reason":"Mobile app is out of scope"}`
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: resultJSON, Model: "test-model"},
	})

	r, err := IntakeDocument(context.Background(), mock, "feature", input, goalsBody)
	assert.NoError(t, err)
	assert.Equal(t, "feat-mobile-app", r.ID)
	assert.False(t, r.Accepted)
	assert.Equal(t, "Mobile app is out of scope", r.Reason)
}

func TestIntakeDuplicate(t *testing.T) {
	goalsBody := "Build a CLI tool for project management."
	input := "Add a status command."

	resultJSON := `{"id":"feat-status","title":"Status command","accepted":false,"duplicate":true,"duplicate_of":"feat-status","reason":"Duplicate of existing status feature"}`
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: resultJSON, Model: "test-model"},
	})

	r, err := IntakeDocument(context.Background(), mock, "feature", input, goalsBody)
	assert.NoError(t, err)
	assert.True(t, r.Duplicate)
	assert.Equal(t, "feat-status", r.DuplicateOf)
}

func TestIntakeWithoutGoals(t *testing.T) {
	input := "Add a status command to show project state."

	resultJSON := `{"id":"feat-status","title":"Status command","accepted":true}`
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: resultJSON, Model: "test-model"},
	})

	r, err := IntakeDocument(context.Background(), mock, "feature", input, "")
	assert.NoError(t, err)
	assert.Equal(t, "feat-status", r.ID)
	assert.True(t, r.Accepted)

	calls := mock.Calls()
	assert.Equal(t, 1, len(calls))
	userMsg := calls[0].Request.Messages[1].Content
	assert.False(t, strings.Contains(userMsg, "GOALS.md"),
		"user message should omit GOALS.md when goalsBody is empty")
}

func TestIntakeBugPrefixInstructions(t *testing.T) {
	resultJSON := `{"id":"bug-login-crash","title":"Login crash","accepted":true}`
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: resultJSON, Model: "test-model"},
	})

	r, err := IntakeDocument(context.Background(), mock, "bug", "Login crashes on submit.", "")
	assert.NoError(t, err)
	assert.Equal(t, "bug-login-crash", r.ID)

	calls := mock.Calls()
	systemMsg := calls[0].Request.Messages[0].Content
	assert.True(t, strings.Contains(systemMsg, `"bug-"`),
		"system prompt should instruct the bug- prefix for kind=bug")
}

func TestIntakeLLMError(t *testing.T) {
	llmErr := fmt.Errorf("provider unavailable")
	mock := NewMockLLM(MockResponse{Err: llmErr})

	r, err := IntakeDocument(context.Background(), mock, "feature", "anything", "goals")
	assert.ErrorIs(t, err, llmErr)
	assert.Nil(t, r)
}

func TestIntakeMalformedResponse(t *testing.T) {
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: "this is not json at all", Model: "test-model"},
	})

	r, err := IntakeDocument(context.Background(), mock, "feature", "anything", "goals")
	assert.Error(t, err)
	assert.Nil(t, r)
	assert.Equal(t, 1, mock.CallCount())
}

func TestIntakeEmptyIDIsError(t *testing.T) {
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: `{"id":"","title":"x"}`, Model: "test-model"},
	})

	r, err := IntakeDocument(context.Background(), mock, "feature", "anything", "")
	assert.Error(t, err)
	assert.Nil(t, r)
}
