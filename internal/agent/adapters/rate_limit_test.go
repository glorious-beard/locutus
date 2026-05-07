package adapters

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRateLimitError_IsErrRateLimit confirms that the new
// RateLimitError still satisfies errors.Is(err, ErrRateLimit) — the
// executor's fallback walk and RunWithRetry's retry-eligibility
// check both pattern-match against ErrRateLimit, so the new typed
// error must NOT break that contract.
func TestRateLimitError_IsErrRateLimit(t *testing.T) {
	rl := &RateLimitError{RetryAfter: 30 * time.Second}
	assert.ErrorIs(t, rl, ErrRateLimit)
}

// fakeResponseWithHeader builds a minimal *http.Response carrying a
// Retry-After header. Required for both Anthropic and OpenAI
// classifier tests — those SDKs read the header off the response
// stored on apiErr.Response.
func fakeResponseWithHeader(status int, retryAfter string) *http.Response {
	h := http.Header{}
	if retryAfter != "" {
		h.Set("Retry-After", retryAfter)
	}
	return &http.Response{StatusCode: status, Header: h}
}

func TestClassifyAnthropicError_RateLimitParsesRetryAfter(t *testing.T) {
	apiErr := &anthropic.Error{
		StatusCode: http.StatusTooManyRequests,
		Request:    fakeRequest(),
		Response:   fakeResponseWithHeader(http.StatusTooManyRequests, "30"),
	}

	got := classifyAnthropicError(apiErr)

	assert.ErrorIs(t, got, ErrRateLimit, "must remain rate-limit-classified for fallback eligibility")
	var rlErr *RateLimitError
	require.True(t, errors.As(got, &rlErr), "must be a *RateLimitError carrying the Retry-After hint")
	assert.Equal(t, 30*time.Second, rlErr.RetryAfter)
}

func TestClassifyAnthropicError_RateLimitWithoutHeader(t *testing.T) {
	apiErr := &anthropic.Error{
		StatusCode: http.StatusTooManyRequests,
		Request:    fakeRequest(),
		Response:   fakeResponseWithHeader(http.StatusTooManyRequests, ""),
	}

	got := classifyAnthropicError(apiErr)

	assert.ErrorIs(t, got, ErrRateLimit)
	var rlErr *RateLimitError
	require.True(t, errors.As(got, &rlErr))
	assert.Zero(t, rlErr.RetryAfter, "RetryAfter must be zero when the header is absent so RunWithRetry falls back to exponential")
}

func TestClassifyOpenAIError_RateLimitParsesRetryAfter(t *testing.T) {
	apiErr := &openai.Error{
		StatusCode: http.StatusTooManyRequests,
		Request:    fakeRequest(),
		Response:   fakeResponseWithHeader(http.StatusTooManyRequests, "45"),
	}

	got := classifyOpenAIError(apiErr)

	assert.ErrorIs(t, got, ErrRateLimit)
	var rlErr *RateLimitError
	require.True(t, errors.As(got, &rlErr))
	assert.Equal(t, 45*time.Second, rlErr.RetryAfter)
}

// TestClassifyGeminiError_RateLimitWithoutHeader: the genai SDK
// doesn't expose a typed error with response headers, so the Gemini
// classifier returns plain ErrRateLimit. RunWithRetry falls back to
// exponential backoff in that case.
func TestClassifyGeminiError_RateLimitWithoutHeader(t *testing.T) {
	got := classifyGeminiError(errors.New("Error 429, rate_limit exceeded"))
	assert.ErrorIs(t, got, ErrRateLimit)
	// Gemini path doesn't attempt header parsing — verify it isn't
	// silently emitting a RateLimitError with RetryAfter > 0.
	var rlErr *RateLimitError
	if errors.As(got, &rlErr) {
		assert.Zero(t, rlErr.RetryAfter, "Gemini classifier must not invent a RetryAfter")
	}
}
