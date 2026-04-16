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
	// OutputSchema, if non-nil, requests structured JSON output conforming
	// to the given schema. The value should be a JSON Schema object.
	OutputSchema any `json:"output_schema,omitempty"`
}

// GenerateResponse holds the result of an LLM generation call.
type GenerateResponse struct {
	Content    string `json:"content"`
	Model      string `json:"model"`
	TokensUsed int    `json:"tokens_used,omitempty"`
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
