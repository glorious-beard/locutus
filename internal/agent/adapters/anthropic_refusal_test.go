package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefusalError_Surface locks in the RefusalError type contract:
// errors.Is(err, ErrRefusal) matches, the Category and Explanation
// thread through into Error(), and an empty payload still produces a
// usable error message.
func TestRefusalError_Surface(t *testing.T) {
	cases := []struct {
		name    string
		err     *RefusalError
		wantMsg string
	}{
		{
			name:    "both fields populated",
			err:     &RefusalError{Category: "cyber", Explanation: "cannot help draft offensive payloads."},
			wantMsg: "provider refused (cyber): cannot help draft offensive payloads.",
		},
		{
			name:    "category only",
			err:     &RefusalError{Category: "bio"},
			wantMsg: "provider refused (bio)",
		},
		{
			name:    "explanation only",
			err:     &RefusalError{Explanation: "unable to comply."},
			wantMsg: "provider refused: unable to comply.",
		},
		{
			name:    "both empty falls through to sentinel",
			err:     &RefusalError{},
			wantMsg: ErrRefusal.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantMsg, tc.err.Error())
			assert.ErrorIs(t, tc.err, ErrRefusal, "RefusalError must Is-match ErrRefusal so existing fallback checks recognize it")
		})
	}
}

// TestAnthropicAdapter_StopReasonRefusalSurfacesRefusalError drives
// a fake Anthropic /v1/messages endpoint that returns
// stop_reason=refusal with structured stop_details, then asserts the
// adapter returns a *RefusalError carrying the structured fields.
// The branch lives in dispatch() right before the success return; a
// regression that drops the StopReasonRefusal check would let
// silent-empty responses leak through as ordinary success cases,
// surfacing downstream as confusing empty-content errors rather than
// as the policy refusal the API actually emitted.
func TestAnthropicAdapter_StopReasonRefusalSurfacesRefusalError(t *testing.T) {
	const refusalCategory = "cyber"
	const refusalExplanation = "I can't help with that request."

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One stop_reason=refusal response shaped per the v1.29+
		// envelope. The SDK unmarshals this into Message with
		// StopDetails populated.
		body := map[string]any{
			"id":   "msg_test_refusal",
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": ""},
			},
			"model":       "claude-test",
			"stop_reason": "refusal",
			"stop_details": map[string]any{
				"type":        "refusal",
				"category":    refusalCategory,
				"explanation": refusalExplanation,
			},
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 0,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	client := anthropicsdk.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srv.URL+"/"),
	)
	adapter := &AnthropicAdapter{client: &client, maxToolRounds: 4}

	resp, err := adapter.Run(context.Background(), Request{
		Model:           "claude-test",
		SystemPrompt:    "you are a helpful test",
		Messages:        []Message{{Role: RoleUser, Content: "trigger refusal"}},
		MaxOutputTokens: 64,
	})

	require.Error(t, err, "stop_reason=refusal must surface as an error")
	assert.ErrorIs(t, err, ErrRefusal, "the surfaced error must Is-match ErrRefusal so dispatcher fallback advances on the next preference")

	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal, "the surfaced error must be a *RefusalError carrying structured stop_details")
	assert.Equal(t, refusalCategory, refusal.Category, "RefusalError.Category must mirror the API's stop_details.category")
	assert.Equal(t, refusalExplanation, refusal.Explanation, "RefusalError.Explanation must mirror the API's stop_details.explanation")

	// The Response itself should still be returned so the per-call
	// recorder lands the request/response payload in the YAML trace.
	// Refusal is a structured outcome, not a "we lost the response"
	// situation.
	require.NotNil(t, resp, "Response must be returned alongside the RefusalError so the recorder captures the policy-refusal payload in the trace")
}

// TestAnthropicAdapter_RefusalAsErrIncompatibleInDispatcher confirms
// the executor's existing fallback-walk eligibility check matches on
// the refusal — refusals should not retry the same provider but
// SHOULD advance to the next preference. Lower-level adapters return
// the typed error; the dispatcher does the fallback dance off the
// ErrIs check.
func TestAnthropicAdapter_RefusalAsErrIncompatibleInDispatcher(t *testing.T) {
	// errors.Is must match ErrRefusal but NOT ErrTimeout / ErrRateLimit
	// — those would invite same-pick retries.
	err := &RefusalError{Category: "cyber"}
	assert.ErrorIs(t, err, ErrRefusal)
	assert.False(t, errors.Is(err, ErrTimeout), "refusal must not be classified as retryable timeout")
	assert.False(t, errors.Is(err, ErrRateLimit), "refusal must not be classified as rate-limit")
}
