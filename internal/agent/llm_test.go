package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMockLLMBasicResponse(t *testing.T) {
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{
			Content:      "hello",
			Model:        "test-model",
			InputTokens:  6,
			OutputTokens: 4,
			TotalTokens:  10,
		},
	})

	resp, err := mock.Generate(context.Background(), GenerateRequest{Model: "test-model"})
	assert.NoError(t, err)
	assert.Equal(t, "hello", resp.Content)
	assert.Equal(t, "test-model", resp.Model)
	assert.Equal(t, 6, resp.InputTokens)
	assert.Equal(t, 4, resp.OutputTokens)
	assert.Equal(t, 10, resp.TotalTokens)
	assert.Equal(t, 1, mock.CallCount())
}

func TestMockLLMMultipleResponses(t *testing.T) {
	mock := NewMockLLM(
		MockResponse{Response: &GenerateResponse{Content: "first"}},
		MockResponse{Response: &GenerateResponse{Content: "second"}},
		MockResponse{Response: &GenerateResponse{Content: "third"}},
	)

	ctx := context.Background()
	for i, want := range []string{"first", "second", "third"} {
		resp, err := mock.Generate(ctx, GenerateRequest{Model: fmt.Sprintf("m%d", i)})
		assert.NoError(t, err)
		assert.Equal(t, want, resp.Content)
	}
}

func TestMockLLMErrorResponse(t *testing.T) {
	providerErr := fmt.Errorf("provider error")
	mock := NewMockLLM(MockResponse{Err: providerErr})

	resp, err := mock.Generate(context.Background(), GenerateRequest{})
	assert.ErrorIs(t, err, providerErr)
	assert.Nil(t, resp)
}

func TestMockLLMExhausted(t *testing.T) {
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: "only one"},
	})

	ctx := context.Background()
	_, err := mock.Generate(ctx, GenerateRequest{})
	assert.NoError(t, err)

	_, err = mock.Generate(ctx, GenerateRequest{})
	assert.ErrorIs(t, err, ErrTimeout)
}

func TestMockLLMRecordsCalls(t *testing.T) {
	mock := NewMockLLM(
		MockResponse{Response: &GenerateResponse{Content: "a"}},
		MockResponse{Response: &GenerateResponse{Content: "b"}},
	)

	ctx := context.Background()
	mock.Generate(ctx, GenerateRequest{
		Model:    "gpt-4",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	mock.Generate(ctx, GenerateRequest{
		Model:    "claude-3",
		Messages: []Message{{Role: "system", Content: "you are helpful"}, {Role: "user", Content: "hey"}},
	})

	calls := mock.Calls()
	assert.Len(t, calls, 2)
	assert.Equal(t, "gpt-4", calls[0].Request.Model)
	assert.Equal(t, "hi", calls[0].Request.Messages[0].Content)
	assert.Equal(t, "claude-3", calls[1].Request.Model)
	assert.Len(t, calls[1].Request.Messages, 2)
}

func TestMockLLMReset(t *testing.T) {
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: "old"},
	})

	ctx := context.Background()
	mock.Generate(ctx, GenerateRequest{})
	assert.Equal(t, 1, mock.CallCount())

	mock.Reset(MockResponse{
		Response: &GenerateResponse{Content: "new"},
	})

	assert.Equal(t, 0, mock.CallCount())

	resp, err := mock.Generate(ctx, GenerateRequest{})
	assert.NoError(t, err)
	assert.Equal(t, "new", resp.Content)
}

func TestRetrySucceedsFirstAttempt(t *testing.T) {
	mock := NewMockLLM(MockResponse{
		Response: &GenerateResponse{Content: "ok"},
	})

	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	resp, err := GenerateWithRetry(context.Background(), mock, GenerateRequest{}, cfg)
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	assert.Equal(t, 1, mock.CallCount())
}

func TestRetryOnRateLimit(t *testing.T) {
	mock := NewMockLLM(
		MockResponse{Err: ErrRateLimit},
		MockResponse{Err: ErrRateLimit},
		MockResponse{Response: &GenerateResponse{Content: "finally"}},
	)

	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	resp, err := GenerateWithRetry(context.Background(), mock, GenerateRequest{}, cfg)
	assert.NoError(t, err)
	assert.Equal(t, "finally", resp.Content)
	assert.Equal(t, 3, mock.CallCount())
}

func TestRetryOnTimeout(t *testing.T) {
	mock := NewMockLLM(
		MockResponse{Err: ErrTimeout},
		MockResponse{Response: &GenerateResponse{Content: "recovered"}},
	)

	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	resp, err := GenerateWithRetry(context.Background(), mock, GenerateRequest{}, cfg)
	assert.NoError(t, err)
	assert.Equal(t, "recovered", resp.Content)
	assert.Equal(t, 2, mock.CallCount())
}

func TestRetryExhaustsAttempts(t *testing.T) {
	mock := NewMockLLM(
		MockResponse{Err: ErrRateLimit},
		MockResponse{Err: ErrRateLimit},
		MockResponse{Err: ErrRateLimit},
	)

	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	resp, err := GenerateWithRetry(context.Background(), mock, GenerateRequest{}, cfg)
	assert.ErrorIs(t, err, ErrRateLimit)
	assert.Nil(t, resp)
	assert.Equal(t, 3, mock.CallCount())
}

func TestRetryDoesNotRetryOtherErrors(t *testing.T) {
	nonRetryable := fmt.Errorf("invalid request")
	mock := NewMockLLM(MockResponse{Err: nonRetryable})

	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	_, err := GenerateWithRetry(context.Background(), mock, GenerateRequest{}, cfg)
	assert.ErrorIs(t, err, nonRetryable)
	assert.Equal(t, 1, mock.CallCount())
}

func TestRetryRespectsContextCancellation(t *testing.T) {
	mock := NewMockLLM(
		MockResponse{Response: &GenerateResponse{Content: "should not reach"}},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	_, err := GenerateWithRetry(ctx, mock, GenerateRequest{}, cfg)
	assert.ErrorIs(t, err, ErrTimeout)
	assert.Equal(t, 0, mock.CallCount())
}
