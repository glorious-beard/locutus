package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// defaultAnthropicMaxTokens is substituted when a request omits
// MaxOutputTokens. The Anthropic API rejects MaxTokens=0
// ("maxTokens not set"), so we always supply a value.
const defaultAnthropicMaxTokens = 4096

// AnthropicAdapter implements adapters.Adapter against
// anthropic-sdk-go's Messages API. It uses native OutputConfig to
// enforce strict-mode schemas (DJ-108), attaches cache_control markers to
// the system prompt for prompt caching, and drives a multi-round
// tool-use loop when custom tools are supplied.
type AnthropicAdapter struct {
	client *anthropicsdk.Client
	// maxToolRounds caps the tool-use loop. The model should
	// converge in a handful of rounds for our agents (reconciler
	// fetches a manifest then a few specific nodes); anything
	// beyond this is almost certainly a degenerate loop.
	maxToolRounds int
}

// anthropicRequestTimeout is the per-attempt timeout we hand to the
// SDK via WithRequestTimeout. Any non-zero value here bypasses the
// SDK's CalculateNonStreamingTimeout preflight check (client.go:140
// in v1.23.0: "if the user has set a specific request timeout, use
// that"), which would otherwise reject Opus 4.7 + max_tokens=32768
// requests upfront because their estimated wall-clock — derived
// from (3600s * max_tokens / 128000) — exceeds the SDK's 10-minute
// default. Observed Anthropic call durations on the strong tier
// top out at 1-3 minutes; 9m45s gives 3-10× headroom while staying
// just under the SDK's nominal "you should probably stream" mark.
// If real calls start exceeding this, that's the signal to migrate
// to streaming — not to keep raising this number.
const anthropicRequestTimeout = 9*time.Minute + 45*time.Second

// NewAnthropicAdapter builds an adapter from ANTHROPIC_API_KEY.
// Returns an error when the env var is unset; callers should gate
// adapter construction on DetectProviders.
func NewAnthropicAdapter() (*AnthropicAdapter, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	c := anthropicsdk.NewClient(
		option.WithAPIKey(key),
		option.WithRequestTimeout(anthropicRequestTimeout),
	)
	return &AnthropicAdapter{client: &c, maxToolRounds: 10}, nil
}

// Provider returns the canonical name for this adapter.
func (a *AnthropicAdapter) Provider() string { return "anthropic" }

// Run dispatches a request through anthropic-sdk-go. The flow:
//
//  1. Build params via buildAnthropicMessageNewParams (system prompt
//     with cache_control marker, user messages, tools, thinking
//     config, and OutputConfig when an output schema is requested).
//  2. Strict-mode JSON enforcement uses MessageNewParams.OutputConfig
//     .Format.Schema (DJ-108: native structured output). The synthetic-
//     tool / forced-tool_choice path used historically is gone — it
//     conflicted with extended thinking on Opus 4.7+ (the deprecated
//     enabled-budget thinking API).
//  3. Custom tools (when present) loop on tool_use responses until the
//     model emits a non-tool stop reason. OutputConfig and tools
//     compose: the model is free to call tools first, then emit the
//     structured response.
//  4. Aggregate per-round telemetry into Response.Rounds when more
//     than one round fired.
func (a *AnthropicAdapter) Run(ctx context.Context, req Request) (*Response, error) {
	params := buildAnthropicMessageNewParams(req)
	return a.dispatch(ctx, params, req)
}

// buildAnthropicMessageNewParams projects a neutral Request into the
// SDK's MessageNewParams shape. Pure function so the param-construction
// logic (especially the structured-output / thinking interplay) can be
// unit-tested without touching the network.
//
// DJ-108: structured output uses MessageNewParams.OutputConfig.Format
// .Schema. Thinking uses ThinkingConfigAdaptiveParam (the
// enabled-budget API is deprecated on Opus 4.7+; adaptive composes
// cleanly with everything). When OutputSchema is set, the OutputConfig
// .Effort knob hints the model's reasoning budget; otherwise adaptive
// thinking runs without an Effort hint and the model self-pacing.
func buildAnthropicMessageNewParams(req Request) anthropicsdk.MessageNewParams {
	maxTokens := int64(req.MaxOutputTokens)
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}

	system := []anthropicsdk.TextBlockParam{
		{
			Text:         req.SystemPrompt,
			CacheControl: anthropicsdk.NewCacheControlEphemeralParam(),
		},
	}

	params := anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model(req.Model),
		MaxTokens: maxTokens,
		System:    system,
		Messages:  buildAnthropicMessages(req.Messages),
		Tools:     buildAnthropicTools(req),
	}

	useNativeStructured := req.OutputSchema != nil
	if useNativeStructured {
		params.OutputConfig = anthropicsdk.OutputConfigParam{
			Format: anthropicsdk.JSONOutputFormatParam{
				Schema: req.OutputSchema,
			},
		}
	}

	switch req.Thinking {
	case ThinkingOn:
		params.Thinking = anthropicsdk.ThinkingConfigParamUnion{
			OfAdaptive: &anthropicsdk.ThinkingConfigAdaptiveParam{},
		}
		if useNativeStructured {
			params.OutputConfig.Effort = anthropicsdk.OutputConfigEffortMedium
		}
	case ThinkingHigh:
		params.Thinking = anthropicsdk.ThinkingConfigParamUnion{
			OfAdaptive: &anthropicsdk.ThinkingConfigAdaptiveParam{},
		}
		if useNativeStructured {
			params.OutputConfig.Effort = anthropicsdk.OutputConfigEffortHigh
		}
	case ThinkingOff:
		// Leave Thinking unset; default is no thinking.
	}

	if req.Grounding {
		// Server-side web_search tool. Anthropic returns search hits
		// as web_search_tool_result content blocks; extractAnthropic-
		// Citations flattens those (and any text-block citations
		// referencing them) into Round.Citations. Composes with
		// OutputConfig — the model can search before producing the
		// structured response (per the Anthropic docs example).
		params.Tools = append(params.Tools, anthropicsdk.ToolUnionParam{
			OfWebSearchTool20250305: &anthropicsdk.WebSearchTool20250305Param{},
		})
	}

	return params
}

// dispatch runs the request and drives the multi-round tool-use loop
// when custom tools are present. With native structured output (DJ-108)
// the model emits its response as text content blocks even when an
// OutputSchema is set — the API enforces conformance server-side, so
// the dispatch path is uniform across schema and free-form calls.
func (a *AnthropicAdapter) dispatch(ctx context.Context, params anthropicsdk.MessageNewParams, req Request) (*Response, error) {
	out := &Response{Model: req.Model}
	totalUsage := struct{ in, outTok, total int }{}

	for round := 1; round <= a.maxToolRounds; round++ {
		msg, err := a.client.Messages.New(ctx, params)
		if err != nil {
			return out, classifyAnthropicError(err)
		}

		out.Model = string(msg.Model)
		out.InputTokens = int(msg.Usage.InputTokens)
		out.OutputTokens = int(msg.Usage.OutputTokens)
		out.TotalTokens = int(msg.Usage.InputTokens + msg.Usage.OutputTokens)
		totalUsage.in += int(msg.Usage.InputTokens)
		totalUsage.outTok += int(msg.Usage.OutputTokens)
		totalUsage.total += int(msg.Usage.InputTokens + msg.Usage.OutputTokens)

		text, reasoning, toolUses := splitContent(msg.Content)
		raw, _ := json.Marshal(msg.Content)
		citations := extractAnthropicCitations(raw)

		out.Rounds = append(out.Rounds, Round{
			Index:        round,
			Reasoning:    reasoning,
			Text:         text,
			Message:      string(raw),
			InputTokens:  int(msg.Usage.InputTokens),
			OutputTokens: int(msg.Usage.OutputTokens),
			Citations:    citations,
		})
		out.Citations = mergeCitations(out.Citations, citations)

		// Tool-use loop: dispatch every custom-tool tool_use the model
		// emitted and feed the results back as a user message. The
		// model continues until it emits a non-tool stop_reason.
		// Server tools (web_search) are handled by Anthropic itself
		// and arrive as web_search_tool_result content blocks rather
		// than tool_use blocks — they don't drive this loop.
		if msg.StopReason == anthropicsdk.StopReasonToolUse && len(toolUses) > 0 {
			results, err := dispatchAnthropicTools(ctx, req.Tools, toolUses)
			if err != nil {
				return out, err
			}
			params.Messages = append(params.Messages, msg.ToParam())
			params.Messages = append(params.Messages, anthropicsdk.NewUserMessage(results...))
			continue
		}

		out.Content = text
		out.Reasoning = reasoning
		out.RawMessage = string(raw)
		return finalizeRounds(out), nil
	}

	return out, fmt.Errorf("anthropic adapter: tool-use loop exceeded %d rounds", a.maxToolRounds)
}

// finalizeRounds drops the per-round slice when only one round fired.
// Single-round calls' data is already on the top-level fields; an
// extra one-element slice would just clutter session traces.
func finalizeRounds(out *Response) *Response {
	if len(out.Rounds) <= 1 {
		out.Rounds = nil
	}
	return out
}

// buildAnthropicMessages translates neutral Messages into the SDK's
// MessageParam shape. RoleSystem is not handled here — the executor
// places the system prompt in the System field, not the Messages
// list.
//
// DJ-106: when ANY message in `in` carries Cacheable=true, adjacent
// same-role messages are merged into a single MessageParam with one
// TextBlock per Message. The Cacheable=true block receives a
// cache_control marker so the API caches everything up to and
// including it. When no Cacheable hint is set on any input, the
// per-Message MessageParam shape is preserved (keeps existing
// caller behavior unchanged).
func buildAnthropicMessages(in []Message) []anthropicsdk.MessageParam {
	if !anyCacheable(in) {
		out := make([]anthropicsdk.MessageParam, 0, len(in))
		for _, m := range in {
			out = append(out, oneBlockMessageParam(m))
		}
		return out
	}

	// Group runs of adjacent same-role messages into a single
	// MessageParam with N TextBlocks. The cache marker on a
	// Cacheable block applies positionally, so the grouping must
	// preserve order within a role.
	var out []anthropicsdk.MessageParam
	i := 0
	for i < len(in) {
		role := in[i].Role
		j := i
		var blocks []anthropicsdk.ContentBlockParamUnion
		for j < len(in) && in[j].Role == role {
			blocks = append(blocks, textBlockFromMessage(in[j]))
			j++
		}
		out = append(out, anthropicsdk.MessageParam{
			Role:    anthropicMessageRole(role),
			Content: blocks,
		})
		i = j
	}
	return out
}

func anyCacheable(in []Message) bool {
	for _, m := range in {
		if m.Cacheable {
			return true
		}
	}
	return false
}

// textBlockFromMessage constructs a ContentBlockParamUnion holding a
// TextBlockParam, with cache_control set when the source message is
// Cacheable. Used by the multi-block grouping path.
func textBlockFromMessage(m Message) anthropicsdk.ContentBlockParamUnion {
	block := anthropicsdk.TextBlockParam{Text: m.Content}
	if m.Cacheable {
		block.CacheControl = anthropicsdk.NewCacheControlEphemeralParam()
	}
	return anthropicsdk.ContentBlockParamUnion{OfText: &block}
}

// oneBlockMessageParam preserves the historical 1-Message → 1-Param
// shape used when no Cacheable hint is present anywhere in the
// input. Avoids gratuitous structural changes for callers that
// don't care about caching.
func oneBlockMessageParam(m Message) anthropicsdk.MessageParam {
	if m.Role == RoleAssistant {
		return anthropicsdk.NewAssistantMessage(anthropicsdk.NewTextBlock(m.Content))
	}
	return anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(m.Content))
}

func anthropicMessageRole(r Role) anthropicsdk.MessageParamRole {
	if r == RoleAssistant {
		return anthropicsdk.MessageParamRoleAssistant
	}
	return anthropicsdk.MessageParamRoleUser
}

// buildAnthropicTools translates request tools into the SDK's
// ToolUnionParam shape. Each tool's input_schema is taken verbatim —
// the executor's schema.go has already enforced strict-mode shape.
//
// DJ-108: the synthetic schema tool path is gone; strict-mode JSON is
// enforced via MessageNewParams.OutputConfig.Format.Schema directly.
// Only request-supplied custom tools (e.g., spec_lookup for the
// reconciler) are advertised here. Server tools (web_search) are
// appended separately by the caller when Grounding is set.
func buildAnthropicTools(req Request) []anthropicsdk.ToolUnionParam {
	var tools []anthropicsdk.ToolUnionParam
	for _, t := range req.Tools {
		tool := anthropicsdk.ToolParam{
			Name:        t.Name,
			Description: anthropicsdk.String(t.Description),
			InputSchema: jsonSchemaToInputSchema(t.InputSchema),
		}
		tools = append(tools, anthropicsdk.ToolUnionParam{OfTool: &tool})
	}
	return tools
}

// jsonSchemaToInputSchema projects a JSON-Schema map into the SDK's
// ToolInputSchemaParam. Only properties + required + additional
// fields land in the typed slots; anything else is forwarded via
// ExtraFields so we don't lose strict-mode markers like
// additionalProperties:false.
func jsonSchemaToInputSchema(schema map[string]any) anthropicsdk.ToolInputSchemaParam {
	if schema == nil {
		return anthropicsdk.ToolInputSchemaParam{Properties: map[string]any{}}
	}
	out := anthropicsdk.ToolInputSchemaParam{ExtraFields: map[string]any{}}
	if props, ok := schema["properties"].(map[string]any); ok {
		out.Properties = props
	} else {
		out.Properties = map[string]any{}
	}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				out.Required = append(out.Required, s)
			}
		}
	}
	for k, v := range schema {
		if k == "type" || k == "properties" || k == "required" {
			continue
		}
		out.ExtraFields[k] = v
	}
	return out
}

// splitContent walks the response content blocks and groups them
// into text, reasoning, and tool_use parts. Other block types
// (server tool results, code execution, etc.) are ignored — none of
// our adapters' agents use them today.
func splitContent(content []anthropicsdk.ContentBlockUnion) (text, reasoning string, toolUses []anthropicsdk.ToolUseBlock) {
	var textParts, reasoningParts []string
	for _, b := range content {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
		case "thinking":
			reasoningParts = append(reasoningParts, b.Thinking)
		case "tool_use":
			toolUses = append(toolUses, anthropicsdk.ToolUseBlock{
				ID:    b.ID,
				Name:  b.Name,
				Input: b.Input,
				Type:  "tool_use",
			})
		}
	}
	return strings.Join(textParts, "\n"), strings.Join(reasoningParts, "\n\n"), toolUses
}

// dispatchAnthropicTools invokes each tool's Handler and packages
// the results as ContentBlockParamUnion ToolResult blocks suitable
// for the next user-message turn.
//
// Handler errors are fed back to the model as is_error tool_result
// blocks so the model can recover (typo'd id → retry with manifest,
// etc.). Two exceptions bubble up instead: context.Canceled and
// context.DeadlineExceeded — these signal the caller's deadline,
// not a recoverable input error, and feeding them to the model
// would let the loop continue past cancellation.
func dispatchAnthropicTools(ctx context.Context, registry []ToolDef, toolUses []anthropicsdk.ToolUseBlock) ([]anthropicsdk.ContentBlockParamUnion, error) {
	byName := make(map[string]ToolDef, len(registry))
	for _, t := range registry {
		byName[t.Name] = t
	}
	results := make([]anthropicsdk.ContentBlockParamUnion, 0, len(toolUses))
	for _, tu := range toolUses {
		if err := ctx.Err(); err != nil {
			return nil, classifyAnthropicError(err)
		}
		def, ok := byName[tu.Name]
		if !ok {
			results = append(results, anthropicsdk.NewToolResultBlock(tu.ID,
				fmt.Sprintf(`{"error": "tool %q not registered"}`, tu.Name), true))
			continue
		}
		out, err := def.Handler(ctx, tu.Input)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, ErrTimeout
			}
			results = append(results, anthropicsdk.NewToolResultBlock(tu.ID,
				fmt.Sprintf(`{"error": %q}`, err.Error()), true))
			continue
		}
		results = append(results, anthropicsdk.NewToolResultBlock(tu.ID, string(out), false))
	}
	return results, nil
}

// classifyAnthropicError translates the SDK's error shape into the
// neutral sentinels the executor's retry layer pattern-matches.
// Anthropic errors carry an HTTP status; 429 is rate-limited,
// context-deadline exceeded becomes ErrTimeout.
func classifyAnthropicError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	var apiErr *anthropicsdk.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			var hint time.Duration
			if apiErr.Response != nil {
				hint = parseRetryAfterSeconds(apiErr.Response.Header)
			}
			return &RateLimitError{RetryAfter: hint, cause: err}
		case http.StatusGatewayTimeout:
			return ErrTimeout
		}
	}
	return fmt.Errorf("anthropic: %w", err)
}
