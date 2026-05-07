// Package adapters defines the per-provider boundary the Locutus
// AgentExecutor dispatches through. Each Adapter translates a
// provider-neutral Request into one or more native SDK calls (driving
// the provider's own tool-use loop where applicable) and returns a
// provider-neutral Response the executor surfaces to its caller.
//
// Adapters do not depend on internal/agent — the types here are
// self-contained so the agent package can import adapters without a
// cycle. The cost is light translation in the executor when threading
// requests in / responses out.
package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors classify dispatch failures for the executor's
// fallback walk. ErrRateLimit and ErrTimeout are transient — the
// executor advances to the next preference AND RunWithRetry backs
// off and re-walks. ErrIncompatible is permanent for that
// preference (the provider can't satisfy the agent's declared
// capabilities); the executor advances to the next preference but
// RunWithRetry does NOT loop on it because no amount of retrying
// will satisfy the request against the same provider.
var (
	ErrRateLimit    = errors.New("rate limited (429)")
	ErrTimeout      = errors.New("generation timed out")
	ErrIncompatible = errors.New("agent capabilities incompatible with provider")
)

// RateLimitError carries a provider's Retry-After hint when the 429
// response included one. errors.Is(err, ErrRateLimit) still matches
// (via the Is method below) so the executor's fallback walk and
// RunWithRetry's retry-eligibility check both keep working without
// changes. RunWithRetry additionally inspects RetryAfter and uses
// that duration in place of its exponential backoff when the value
// is non-zero — sleeping exactly as long as the provider asked for
// is materially better than guessing.
//
// When the header is absent or unparseable, RetryAfter is zero and
// the retry layer falls back to its baseline backoff schedule. The
// genai SDK doesn't expose response headers through a typed error,
// so the Gemini classifier returns plain ErrRateLimit instead of
// this struct.
type RateLimitError struct {
	RetryAfter time.Duration
	cause      error
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return "rate limited (retry after " + e.RetryAfter.String() + ")"
	}
	return ErrRateLimit.Error()
}

// Is makes errors.Is(err, ErrRateLimit) return true so existing
// retry-eligibility checks continue to recognize this as a
// rate-limit failure.
func (e *RateLimitError) Is(target error) bool {
	return target == ErrRateLimit
}

// Unwrap exposes the underlying SDK error so adapter callers can
// pattern-match on provider-typed errors when they need richer
// detail than the sentinel surface provides.
func (e *RateLimitError) Unwrap() error { return e.cause }

// parseRetryAfterSeconds reads the Retry-After header and returns
// the duration. Returns zero when the header is missing, non-
// numeric (HTTP-date form is not supported — both Anthropic and
// OpenAI use the seconds form per their docs as of 2026-05), or
// non-positive.
func parseRetryAfterSeconds(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// Adapter is implemented by each provider-specific adapter
// (Anthropic, Gemini, OpenAI Responses). Run executes one logical
// agent call, including any internal tool-use loop the model drives.
type Adapter interface {
	// Provider returns the canonical provider name this adapter
	// serves (e.g. "anthropic", "googleai", "openai"). Used for
	// log lines and the Pick.Provider match in the executor's
	// adapter table.
	Provider() string

	// Run dispatches the request to the provider and returns a
	// neutral Response. Multi-round tool-use is the adapter's
	// responsibility: the executor hands over the full ToolDef
	// list, the adapter loops as the model emits tool calls,
	// dispatches Handler, feeds results back into the next turn,
	// and aggregates per-round telemetry into Response.Rounds.
	Run(ctx context.Context, req Request) (*Response, error)
}

// Role is the message role on a chat turn.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in the conversation handed to the adapter.
// System prompts are passed as the first message with RoleSystem;
// adapters split them out into provider-native system fields where
// the API expects that shape (Anthropic, Gemini system_instruction).
//
// Cacheable marks this message's content as the trailing edge of a
// cacheable prefix on providers that support explicit cache markers
// (DJ-106). Adjacent same-role messages are merged into a single
// API-level message; the cache_control marker is placed on the
// last block flagged Cacheable. Other adapters ignore the flag —
// Gemini caching uses a separate cachedContent resource, OpenAI's
// Responses API caches identical prefixes server-side automatically.
type Message struct {
	Role      Role
	Content   string
	Cacheable bool
}

// ThinkingLevel is a coarse thinking-budget enum the executor
// resolves from per-tier config in models.yaml. Adapters translate
// to provider-specific values:
//
//   - Anthropic: ThinkingOff disables; ThinkingOn → 4096 budget;
//     ThinkingHigh → 8192 budget.
//   - Gemini: ThinkingOn → IncludeThoughts true + 4096 budget;
//     ThinkingHigh → 8192 budget.
//   - OpenAI o-series: ThinkingOn → reasoning_effort medium;
//     ThinkingHigh → reasoning_effort high.
type ThinkingLevel string

const (
	ThinkingOff  ThinkingLevel = "off"
	ThinkingOn   ThinkingLevel = "on"
	ThinkingHigh ThinkingLevel = "high"
)

// Request is the provider-neutral input the executor builds for a
// single agent call. Fields stay flat / value-typed so adapters can
// inspect them without callbacks back into the agent package.
type Request struct {
	// Model is the concrete provider-side model string the policy
	// resolved (e.g. "claude-sonnet-4-6", "gemini-2.5-flash"). The
	// adapter passes this to its SDK as-is — no further resolution
	// or fallback inside the adapter.
	Model string

	// SystemPrompt is the agent's system-prompt body. Adapters
	// place this in the provider-native system slot rather than as
	// a chat turn so prompt-cache prefixes stay aligned with
	// provider expectations.
	SystemPrompt string

	// Messages are the user-side turns (already projected from
	// PlanningState). Adapters DO NOT see RoleSystem entries here;
	// the system prompt arrives via SystemPrompt.
	Messages []Message

	// MaxOutputTokens caps the model's response length. Zero means
	// "use the provider default" — Anthropic adapters substitute a
	// safe minimum since the SDK rejects MaxTokens=0.
	MaxOutputTokens int

	// Thinking is the resolved thinking-level. Adapters set
	// provider-specific budget knobs from this enum.
	Thinking ThinkingLevel

	// OutputSchema is the JSON Schema (as a generic map) the model's
	// response must conform to. Each adapter projects this into the
	// provider-native strict-mode shape: Anthropic forced tool-use,
	// Gemini responseSchema, OpenAI Responses json_schema strict.
	// Nil means free-form output. Mutually exclusive with Tools on
	// Gemini (the adapter falls back to prompt-only schema doc with
	// a Warn when both are set).
	OutputSchema map[string]any

	// Grounding requests provider-native search grounding. Each
	// adapter attaches its provider's server-side search tool:
	// Gemini GoogleSearch, OpenAI web_search_preview, Anthropic
	// web_search_20250305. Provider-returned sources land on
	// Round.Citations and Response.Citations.
	Grounding bool

	// Tools is the set of locutus-owned tools the model may dispatch.
	// Each carries its name, description, JSON-Schema input shape,
	// and a handler the adapter invokes when the model emits a tool
	// request. The adapter drives the loop until the model emits a
	// final response without further tool requests.
	Tools []ToolDef
}

// ToolDef is one entry in Request.Tools. The adapter advertises the
// (Name, Description, InputSchema) tuple to the model in the
// provider's tool-spec format and invokes Handler when the model
// dispatches a call. Handler input is the model-emitted JSON
// arguments verbatim; output is the JSON the adapter feeds back as
// the tool result on the next turn.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

// Response is the provider-neutral result of one agent call. Token
// counts are reported separately rather than as one TokensUsed total
// so session traces can show the input/output/thoughts split — useful
// for spotting prompts that have grown unexpectedly large or thinking
// spends that overshot the agent's intent.
type Response struct {
	// Content is the model's final text response. For schema-
	// constrained calls this is the strict-mode JSON output.
	Content string

	// Reasoning is the concatenated extended-thinking text when the
	// call ran with a non-zero thinking budget. Empty when thinking
	// was off or the provider redacted thoughts.
	Reasoning string

	// RawMessage is a JSON dump of the underlying provider's final
	// message payload. Surfaced for diagnostic-only paths where
	// Content alone doesn't carry enough information (truncated
	// structured outputs, non-text parts in tool-use loops).
	RawMessage string

	// Model echoes the model the adapter actually called. Useful
	// when the executor falls back across the agent's models[]
	// preference list and the trace needs to record which fallback
	// fired.
	Model string

	InputTokens    int
	OutputTokens   int
	ThoughtsTokens int
	TotalTokens    int

	// Citations is the aggregate of provider-native search sources
	// the model cited across the call (all rounds, deduped on URL).
	// Empty when grounding was off or the model returned no sources.
	// Surfaced separately from Rounds[].Citations so callers that
	// don't care about per-round attribution can still inspect what
	// the call grounded against.
	Citations []Citation

	// Rounds carries per-round captures across a multi-round tool-
	// use loop. Single-round calls leave it nil and rely on the
	// top-level Reasoning / Content / RawMessage fields. The
	// executor surfaces this through to the session trace so an
	// operator can see what the model asked tools to do, not just
	// the final response after the loop completed.
	Rounds []Round
}

// Round is one model invocation inside a multi-round tool-use loop.
// Mirrors the per-round shape session traces already record so the
// adapter doesn't need a translation layer in the executor.
type Round struct {
	Index          int
	Reasoning      string
	Text           string
	Message        string
	InputTokens    int
	OutputTokens   int
	ThoughtsTokens int
	// Citations are the provider-native search-grounding sources the
	// model cited in this round. Populated by adapters whose provider
	// returned grounding metadata (Gemini groundingMetadata, OpenAI
	// url_citation annotations, Anthropic web_search_tool_result
	// blocks). Empty when grounding was off or the model returned no
	// sources.
	Citations []Citation
}

// Citation is one provider-native search source surfaced from a
// grounded call. URL is always populated; Title is best-effort
// (Gemini and Anthropic always provide it; OpenAI sometimes omits).
// Snippet is the cited excerpt where the provider returns one
// (Anthropic web_search results carry encrypted_content / page text;
// Gemini and OpenAI generally don't).
type Citation struct {
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}
