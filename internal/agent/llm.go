// Package agent provides LLM integration, council orchestration, and planning
// for the Locutus spec-driven project manager.
package agent

import (
	"context"
	"fmt"
	"time"
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
	// Grounding, when true, enables provider-native search-grounding for
	// the call. On Gemini routes this attaches the GoogleSearch tool to
	// the request, letting the model verify claims against current web
	// results. On Anthropic routes it logs a warning and proceeds
	// ungrounded — Genkit Go's anthropic plugin doesn't yet expose
	// web_search. Off by default; turned on per-agent via the
	// `grounding: true` frontmatter field.
	//
	// Important Gemini constraint: GoogleSearch is incompatible with
	// custom Genkit function-call tools. An agent that wants both
	// grounding and custom tools won't work on Gemini today. For our
	// council that's not a collision — the scout uses grounding (no
	// other tools); the reconciler uses lookup tools (no grounding).
	Grounding bool `json:"grounding,omitempty"`
	// Tools names Genkit-registered tools the model may call during
	// this request. Each entry is the tool's registered name (e.g.
	// "spec_list_manifest"); the GenKitLLM resolves each to an
	// ai.ToolName and passes them via ai.WithTools. Models loop on
	// tool dispatches until they emit a final response.
	//
	// Important: on Gemini, attaching tools disables API-level JSON
	// mode (gemini.go:311). Agents that combine `tools` with
	// `output_schema` get the schema in the prompt as documentation
	// but no provider-side enforcement; the merge handler must parse
	// defensively (strip markdown fences, etc.).
	Tools []string `json:"tools,omitempty"`
	// Timeout caps the per-call wall-clock duration. Zero falls back
	// to the global LOCUTUS_LLM_TIMEOUT (default 15m). Set tight on
	// fanout-bounded agents (elaborators) to bound the cost of a
	// degenerate loop without aborting the workflow — the cancelled
	// call surfaces as a regular error through per-node failure
	// isolation. Threaded from AgentDef.Timeout via
	// BuildGenerateRequest after string→Duration parse.
	Timeout time.Duration `json:"timeout,omitempty"`
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
	// Rounds carries per-round captures of the model's output during
	// a multi-turn tool-use loop. Populated only when the call had
	// more than one round (single-round calls leave it nil and rely
	// on the top-level Reasoning/Content/RawMessage fields). Each
	// round records the model's text + reasoning + raw message and
	// per-round usage so an operator debugging a tool-use trace can
	// see what the model asked the tools to do, not just the final
	// response after the loop completed.
	Rounds []GenerateRound `json:"rounds,omitempty"`
}

// GenerateRound is one model invocation inside a multi-turn tool-use
// loop. The middleware accumulates one entry per call into the
// underlying provider; Genkit's tool-dispatch loop drives multiple
// invocations as the model emits tool_request → runtime dispatches →
// tool_response → model continues, repeating until the model emits
// final text without further tool requests.
//
// Message is the JSON-serialised *ai.Message — contains every part
// (text, reasoning, tool_request) the model produced that round. The
// tool_response payloads from the runtime's dispatches are NOT in
// Message — they appear as input messages on the NEXT round and are
// captured implicitly via the next entry's input usage. An operator
// debugging "what did the tool return" can inspect the call's session
// trace alongside the recorded tool dispatches.
type GenerateRound struct {
	Index          int    `json:"index"`
	Reasoning      string `json:"reasoning,omitempty"`
	Text           string `json:"text,omitempty"`
	Message        string `json:"message,omitempty"`
	InputTokens    int    `json:"input_tokens,omitempty"`
	OutputTokens   int    `json:"output_tokens,omitempty"`
	ThoughtsTokens int    `json:"thoughts_tokens,omitempty"`
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
