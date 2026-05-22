package adapters

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// anthropicErrorWithType builds an anthropic.Error whose Type()
// returns the named envelope error_type. The errorType field is
// internal to the SDK; the supported construction path is
// UnmarshalJSON on the canonical envelope shape, which exercises the
// same parse the production SDK runs after every API response.
func anthropicErrorWithType(t *testing.T, status int, errorType string) *anthropic.Error {
	t.Helper()
	apiErr := &anthropic.Error{StatusCode: status, Request: fakeRequest(), Response: fakeResponse(status)}
	envelope := []byte(`{"type":"error","error":{"type":"` + errorType + `","message":"synthetic"}}`)
	if err := apiErr.UnmarshalJSON(envelope); err != nil {
		t.Fatalf("unmarshal synthetic anthropic error envelope: %v", err)
	}
	return apiErr
}

// TestClassifyAnthropicError_TypedDispatch verifies the v1.38+ typed
// path: each error_type the API emits maps to its expected sentinel.
// The overloaded_error vs api_error split is the load-bearing case
// — both can surface as HTTP 500 today, but overloaded means retry
// will recover (server is busy) while api_error means a server-side
// bug and retry just pounds the same code path.
func TestClassifyAnthropicError_TypedDispatch(t *testing.T) {
	cases := []struct {
		errorType string
		want      error
		desc      string
	}{
		{"rate_limit_error", ErrRateLimit, "rate_limit_error → RateLimitError (carries Retry-After hint)"},
		{"overloaded_error", ErrTimeout, "overloaded_error → ErrTimeout (server says capacity will recover; retry fires)"},
		{"timeout_error", ErrTimeout, "timeout_error → ErrTimeout (transient; retry fires)"},
		{"api_error", ErrIncompatible, "api_error → ErrIncompatible (server-side bug; retry won't help, fallback to next provider)"},
		{"invalid_request_error", ErrIncompatible, "invalid_request_error → ErrIncompatible (e.g. credit balance too low)"},
		{"authentication_error", ErrIncompatible, "authentication_error → ErrIncompatible"},
		{"permission_error", ErrIncompatible, "permission_error → ErrIncompatible"},
		{"not_found_error", ErrIncompatible, "not_found_error → ErrIncompatible"},
		{"billing_error", ErrIncompatible, "billing_error → ErrIncompatible"},
	}
	for _, tc := range cases {
		t.Run(tc.errorType, func(t *testing.T) {
			// Use a generic 500 status to isolate the type-dispatch
			// path from the status fallback. The classifier should
			// route on Type() regardless of what the status says.
			apiErr := anthropicErrorWithType(t, http.StatusInternalServerError, tc.errorType)
			got := classifyAnthropicError(apiErr)
			assert.ErrorIs(t, got, tc.want, tc.desc)
		})
	}
}

// TestClassifyAnthropicError_TypedRateLimitPreservesRetryAfter
// verifies the typed rate_limit_error path still extracts the
// Retry-After hint from the response header — the previous
// status-only path read the header off apiErr.Response; the typed
// path must keep that behaviour or the same-pick retry loses its
// throttle hint.
func TestClassifyAnthropicError_TypedRateLimitPreservesRetryAfter(t *testing.T) {
	apiErr := anthropicErrorWithType(t, http.StatusTooManyRequests, "rate_limit_error")
	apiErr.Response.Header = http.Header{"Retry-After": []string{"42"}}
	got := classifyAnthropicError(apiErr)
	var rle *RateLimitError
	require.ErrorAs(t, got, &rle, "rate_limit_error must classify as RateLimitError")
	assert.Equal(t, 42*time.Second, rle.RetryAfter, "Retry-After header must thread through to RateLimitError.RetryAfter")
}

// TestClassifyAnthropicError_GatewayTimeout: Anthropic's typed
// *Error carries StatusCode. A 504 must classify as ErrTimeout so
// the executor walks the fallback chain instead of returning the
// raw API error.
//
// This locks in the status-only fallback path: when the response
// body carries no typed envelope (synthetic errors; truly unknown
// payloads), classification falls through to HTTP-status matching.
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

// TestClassifyAnthropicError_NonRetryableAdvancesToNextProvider locks
// in the fallthrough contract: any provider-side error that isn't a
// rate-limit or server-timeout must still classify as ErrIncompatible
// so the executor's fallback walk advances to the next preference.
// The motivating case was a 400 with "credit balance too low" — an
// account-state error Anthropic returns as invalid_request_error,
// which previously fell into the catch-all wrap and aborted the whole
// walk without trying googleai or openai.
func TestClassifyAnthropicError_NonRetryableAdvancesToNextProvider(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{"400 invalid_request (billing / malformed)", http.StatusBadRequest},
		{"401 authentication", http.StatusUnauthorized},
		{"403 permission", http.StatusForbidden},
		{"404 not found", http.StatusNotFound},
		{"413 request too large", http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := &anthropic.Error{StatusCode: tc.status, Request: fakeRequest(), Response: fakeResponse(tc.status)}
			got := classifyAnthropicError(apiErr)
			assert.ErrorIs(t, got, ErrIncompatible, "non-rate-limit 4xx must classify as ErrIncompatible so the fallback chain advances")
		})
	}
}

// TestClassifyAnthropicError_ServerErrorIsRetryable verifies 5xx
// server errors (other than 504 which is already covered) classify as
// ErrTimeout so both the in-walk rotation and the outer RunWithRetry
// re-walk fire.
func TestClassifyAnthropicError_ServerErrorIsRetryable(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		apiErr := &anthropic.Error{StatusCode: status, Request: fakeRequest(), Response: fakeResponse(status)}
		got := classifyAnthropicError(apiErr)
		assert.ErrorIsf(t, got, ErrTimeout, "status %d should classify as ErrTimeout (retryable transient)", status)
	}
}

// TestClassifyOpenAIError_NonRetryableAdvancesToNextProvider — same
// contract as the Anthropic variant.
func TestClassifyOpenAIError_NonRetryableAdvancesToNextProvider(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusRequestEntityTooLarge} {
		apiErr := &openai.Error{StatusCode: status, Request: fakeRequest(), Response: fakeResponse(status)}
		got := classifyOpenAIError(apiErr)
		assert.ErrorIsf(t, got, ErrIncompatible, "status %d should classify as ErrIncompatible so the fallback chain advances", status)
	}
}

// TestClassifyOpenAIError_ServerErrorIsRetryable — same contract as
// Anthropic.
func TestClassifyOpenAIError_ServerErrorIsRetryable(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		apiErr := &openai.Error{StatusCode: status, Request: fakeRequest(), Response: fakeResponse(status)}
		got := classifyOpenAIError(apiErr)
		assert.ErrorIsf(t, got, ErrTimeout, "status %d should classify as ErrTimeout (retryable transient)", status)
	}
}

// TestClassifyGeminiError_NonRetryableAdvancesToNextProvider: the
// genai SDK doesn't expose typed status codes, so this exercises the
// catch-all default. Any unclassified error must still be ErrIncompatible
// so the executor advances to the next provider.
func TestClassifyGeminiError_NonRetryableAdvancesToNextProvider(t *testing.T) {
	got := classifyGeminiError(errors.New("some random gemini sdk error with no status hint"))
	assert.ErrorIs(t, got, ErrIncompatible, "unclassified gemini errors must default to ErrIncompatible so the fallback chain advances")
}
