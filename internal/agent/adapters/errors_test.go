package adapters

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"
)

// fakeRequest / fakeResponse give the SDK Error.Error() implementations
// non-nil pointers to dereference. Without these, assert.ErrorIs's
// chain-printing path segfaults when an assertion fails — masking the
// actual classification bug we're trying to surface.
func fakeRequest() *http.Request {
	u, _ := url.Parse("https://api.example/")
	return &http.Request{Method: "POST", URL: u}
}

func fakeResponse(status int) *http.Response {
	return &http.Response{StatusCode: status}
}

// TestClassifyGeminiError_ServerSideTimeout locks in Bug A's fix:
// Gemini's server-side deadline (504 / Status: DEADLINE_EXCEEDED /
// "deadline expired before operation could complete") must classify
// as ErrTimeout so the executor's fallback walk and RunWithRetry both
// fire. Prior to this fix, server-side timeouts fell through as
// wrapped errors and the elaborator gave up after one Gemini hit
// without trying the agent's Anthropic / OpenAI preferences.
//
// The genai SDK doesn't expose a typed error with StatusCode for
// these cases — we string-match the canonical patterns.
func TestClassifyGeminiError_ServerSideTimeout(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "504 with DEADLINE_EXCEEDED status",
			err:  errors.New("Error 504, Message: Deadline expired before operation could complete., Status: DEADLINE_EXCEEDED, Details: []"),
		},
		{
			name: "DEADLINE_EXCEEDED gRPC status alone",
			err:  errors.New("rpc error: code = DEADLINE_EXCEEDED desc = context deadline exceeded"),
		},
		{
			name: "Lowercase deadline_exceeded variant",
			err:  errors.New("provider returned status: deadline_exceeded"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyGeminiError(tc.err)
			assert.ErrorIs(t, got, ErrTimeout, "server-side deadline must classify as ErrTimeout to enable retry + fallback")
		})
	}
}

// TestClassifyGeminiError_RateLimitStillWorks ensures the new
// timeout-classification cases don't regress the existing rate-limit
// classification.
func TestClassifyGeminiError_RateLimitStillWorks(t *testing.T) {
	got := classifyGeminiError(errors.New("Error 429, Message: rate_limit exceeded"))
	assert.ErrorIs(t, got, ErrRateLimit)
}

// TestClassifyAnthropicError_GatewayTimeout: Anthropic's typed
// *Error carries StatusCode. A 504 must classify as ErrTimeout so
// the executor walks the fallback chain instead of returning the
// raw API error.
func TestClassifyAnthropicError_GatewayTimeout(t *testing.T) {
	apiErr := &anthropic.Error{StatusCode: http.StatusGatewayTimeout, Request: fakeRequest(), Response: fakeResponse(http.StatusGatewayTimeout)}
	got := classifyAnthropicError(apiErr)
	assert.ErrorIs(t, got, ErrTimeout)
}

// TestClassifyAnthropicError_RateLimitStillWorks ensures the
// existing 429 classification still routes to ErrRateLimit.
func TestClassifyAnthropicError_RateLimitStillWorks(t *testing.T) {
	apiErr := &anthropic.Error{StatusCode: http.StatusTooManyRequests, Request: fakeRequest(), Response: fakeResponse(http.StatusTooManyRequests)}
	got := classifyAnthropicError(apiErr)
	assert.ErrorIs(t, got, ErrRateLimit)
}

// TestClassifyOpenAIError_GatewayTimeout: same pattern as Anthropic.
// OpenAI's typed *Error also carries StatusCode; 504 must route to
// ErrTimeout for retry + fallback eligibility.
func TestClassifyOpenAIError_GatewayTimeout(t *testing.T) {
	apiErr := &openai.Error{StatusCode: http.StatusGatewayTimeout, Request: fakeRequest(), Response: fakeResponse(http.StatusGatewayTimeout)}
	got := classifyOpenAIError(apiErr)
	assert.ErrorIs(t, got, ErrTimeout)
}

// TestClassifyOpenAIError_RateLimitStillWorks ensures the existing
// 429 classification still routes to ErrRateLimit.
func TestClassifyOpenAIError_RateLimitStillWorks(t *testing.T) {
	apiErr := &openai.Error{StatusCode: http.StatusTooManyRequests, Request: fakeRequest(), Response: fakeResponse(http.StatusTooManyRequests)}
	got := classifyOpenAIError(apiErr)
	assert.ErrorIs(t, got, ErrRateLimit)
}
