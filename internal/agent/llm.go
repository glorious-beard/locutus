// Package agent provides LLM integration, council orchestration, and planning
// for the Locutus spec-driven project manager.
package agent

import (
	"context"
	"fmt"
)

// Message represents a single message in an LLM conversation.
type Message struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

// GenerateRequest holds the parameters for an LLM generation call.
type GenerateRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	// ThinkingBudget, when > 0, requests provider-side extended-thinking
	// (Claude extended thinking, Gemini thinking budget) with this many
	// reasoning tokens. 0 leaves thinking off. Both providers bill
	// thinking tokens at output rates; setting this for short-prompt
	// agents is wasted spend, so most agents leave it at zero.
	ThinkingBudget int `json:"thinking_budget,omitempty"`
	// OutputSchema, if non-nil, requests structured JSON output conforming
	// to the given schema. The value should be a JSON Schema object.
	OutputSchema any `json:"output_schema,omitempty"`
}

// GenerateResponse holds the result of an LLM generation call.
// Token counts are reported separately rather than as a single
// TokensUsed total so session traces can show input vs output split —
// useful for spotting prompts that have grown unexpectedly large.
//
// ThoughtsTokens reports tokens spent on extended thinking (Claude
// extended thinking, Gemini thinking budgets). Providers bill thinking
// tokens at output-token rates, so this field is the visibility surface
// for "how much did thinking cost this call."
type GenerateResponse struct {
	Content string `json:"content"`
	Model   string `json:"model"`
	// Reasoning holds the model's extended-thinking text when the call
	// was made with a non-zero ThinkingBudget (and the provider returns
	// thinking content; some redact it). Surfaced into the session
	// trace alongside Content so a debugging operator can see what the
	// model was thinking, not just how many tokens it spent.
	Reasoning string `json:"reasoning,omitempty"`
	// RawMessage is a JSON dump of the underlying provider message
	// (every Part — text, reasoning, tool_request, tool_response,
	// custom). Populated only on error paths where Text() and
	// Reasoning don't surface the model's bytes — e.g. a truncated
	// Gemini structured-output response that lives in a non-text part
	// genkit's format handler then rejected. Diagnostic, not part of
	// any happy-path contract.
	RawMessage     string `json:"raw_message,omitempty"`
	InputTokens    int    `json:"input_tokens,omitempty"`
	OutputTokens   int    `json:"output_tokens,omitempty"`
	ThoughtsTokens int    `json:"thoughts_tokens,omitempty"`
	TotalTokens    int    `json:"total_tokens,omitempty"`
}

// LLM abstracts LLM generation so callers are decoupled from the provider.
// The real implementation wraps Genkit; tests use MockLLM.
type LLM interface {
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
}

// ErrRateLimit is returned when the provider returns HTTP 429.
var ErrRateLimit = fmt.Errorf("rate limited (429)")

// ErrTimeout is returned when a generation call exceeds its deadline.
var ErrTimeout = fmt.Errorf("generation timed out")
