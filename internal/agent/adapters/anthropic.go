package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Sentinel errors the executor pattern-matches for retry/timeout
// classification. Re-defined per-adapter package so adapter callers
// don't import internal/agent.
var (
	ErrRateLimit = errors.New("rate limited (429)")
	ErrTimeout   = errors.New("generation timed out")
)

// defaultAnthropicMaxTokens is substituted when a request omits
// MaxOutputTokens. The Anthropic API rejects MaxTokens=0
// ("maxTokens not set"), so we always supply a value.
const defaultAnthropicMaxTokens = 4096

// schemaToolName is the synthetic tool name the adapter forces the
// model to call when a strict-mode schema is required and no custom
// tools are present. The model's tool_use Input becomes the
// schema-conformant JSON output.
const schemaToolName = "submit_response"

// AnthropicAdapter implements adapters.Adapter against
// anthropic-sdk-go's Messages API. It uses forced tool-use to
// enforce strict-mode schemas, attaches cache_control markers to
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

// NewAnthropicAdapter builds an adapter from ANTHROPIC_API_KEY.
// Returns an error when the env var is unset; callers should gate
// adapter construction on DetectProviders.
func NewAnthropicAdapter() (*AnthropicAdapter, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	c := anthropicsdk.NewClient(option.WithAPIKey(key))
	return &AnthropicAdapter{client: &c, maxToolRounds: 10}, nil
}

// Provider returns the canonical name for this adapter.
func (a *AnthropicAdapter) Provider() string { return "anthropic" }

// Run dispatches a request through anthropic-sdk-go. The flow:
//
//  1. Build the system prompt (with cache_control marker), user
//     messages, tool list, and thinking config from the request.
//  2. If the request has an OutputSchema and no custom tools, force
//     tool-use against a synthetic schema tool — the model literally
//     cannot return text, only the tool call with schema-conformant
//     JSON.
//  3. If custom tools are present, advertise them; loop on tool_use
//     responses until the model emits a non-tool stop reason. With
//     both schema + custom tools, the schema is documented in the
//     system prompt only and not enforced via forced-tool — running
//     forced-tool on a custom-tool agent would prevent the loop.
//  4. Aggregate per-round telemetry into Response.Rounds when more
//     than one round fired.
func (a *AnthropicAdapter) Run(ctx context.Context, req Request) (*Response, error) {
	maxTokens := int64(req.MaxOutputTokens)
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}

	// Forced tool-use is gated on "schema set, no custom tools."
	// With custom tools we can't force a single tool because the
	// model needs to be free to call the custom ones.
	useForcedSchemaTool := req.OutputSchema != nil && len(req.Tools) == 0

	system := []anthropicsdk.TextBlockParam{
		{
			Text:         req.SystemPrompt,
			CacheControl: anthropicsdk.NewCacheControlEphemeralParam(),
		},
	}

	tools := buildAnthropicTools(req, useForcedSchemaTool)

	params := anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model(req.Model),
		MaxTokens: maxTokens,
		System:    system,
		Messages:  buildAnthropicMessages(req.Messages),
		Tools:     tools,
	}

	switch req.Thinking {
	case ThinkingOn:
		budget := int64(4096)
		if budget >= maxTokens {
			budget = maxTokens - 1
		}
		params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(budget)
	case ThinkingHigh:
		budget := int64(8192)
		if budget >= maxTokens {
			budget = maxTokens - 1
		}
		params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(budget)
	}

	if useForcedSchemaTool {
		params.ToolChoice = anthropicsdk.ToolChoiceParamOfTool(schemaToolName)
	}

	if req.Grounding {
		// anthropic-sdk-go doesn't expose web_search for client-side
		// dispatch yet. Surface the gap loudly so an operator running
		// on Anthropic only knows the call went out ungrounded.
		slog.Warn("grounding requested but unsupported on Anthropic; proceeding ungrounded",
			"model", req.Model)
	}

	return a.dispatch(ctx, params, req, useForcedSchemaTool)
}

// dispatch runs the request and drives the multi-round tool-use loop
// when custom tools are present. Single-round calls (forced-tool or
// no tools) take the loop's first iteration and return.
func (a *AnthropicAdapter) dispatch(ctx context.Context, params anthropicsdk.MessageNewParams, req Request, forcedSchema bool) (*Response, error) {
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

		out.Rounds = append(out.Rounds, Round{
			Index:        round,
			Reasoning:    reasoning,
			Text:         text,
			Message:      string(raw),
			InputTokens:  int(msg.Usage.InputTokens),
			OutputTokens: int(msg.Usage.OutputTokens),
		})

		// Forced schema-tool path: extract the tool_use Input as
		// the JSON content. No second round; the model already
		// produced the strict-conformant response.
		if forcedSchema {
			for _, tu := range toolUses {
				if tu.Name == schemaToolName {
					out.Content = string(tu.Input)
					out.Reasoning = reasoning
					out.RawMessage = string(raw)
					return finalizeRounds(out), nil
				}
			}
			// Fall through — model emitted text-only somehow.
			out.Content = text
			out.Reasoning = reasoning
			out.RawMessage = string(raw)
			return finalizeRounds(out), nil
		}

		// Tool-use loop: dispatch every tool_use the model emitted
		// and feed the results back as a user message. The model
		// continues until it emits a non-tool stop_reason.
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
func buildAnthropicMessages(in []Message) []anthropicsdk.MessageParam {
	out := make([]anthropicsdk.MessageParam, 0, len(in))
	for _, m := range in {
		switch m.Role {
		case RoleAssistant:
			out = append(out, anthropicsdk.NewAssistantMessage(anthropicsdk.NewTextBlock(m.Content)))
		default: // user / system-as-user
			out = append(out, anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(m.Content)))
		}
	}
	return out
}

// buildAnthropicTools translates request tools + (optionally) the
// synthetic schema tool into the SDK's ToolUnionParam shape. Each
// tool's input_schema is taken verbatim — the executor's
// schema.go has already enforced strict-mode shape.
func buildAnthropicTools(req Request, includeSchemaTool bool) []anthropicsdk.ToolUnionParam {
	var tools []anthropicsdk.ToolUnionParam
	if includeSchemaTool {
		schemaParam := jsonSchemaToInputSchema(req.OutputSchema)
		tool := anthropicsdk.ToolParam{
			Name:        schemaToolName,
			Description: anthropicsdk.String("Submit your response in the structured shape the caller requires. This is the only way to return your answer."),
			InputSchema: schemaParam,
		}
		tools = append(tools, anthropicsdk.ToolUnionParam{OfTool: &tool})
	}
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
func dispatchAnthropicTools(ctx context.Context, registry []ToolDef, toolUses []anthropicsdk.ToolUseBlock) ([]anthropicsdk.ContentBlockParamUnion, error) {
	byName := make(map[string]ToolDef, len(registry))
	for _, t := range registry {
		byName[t.Name] = t
	}
	results := make([]anthropicsdk.ContentBlockParamUnion, 0, len(toolUses))
	for _, tu := range toolUses {
		def, ok := byName[tu.Name]
		if !ok {
			results = append(results, anthropicsdk.NewToolResultBlock(tu.ID,
				fmt.Sprintf(`{"error": "tool %q not registered"}`, tu.Name), true))
			continue
		}
		out, err := def.Handler(ctx, tu.Input)
		if err != nil {
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
		if apiErr.StatusCode == http.StatusTooManyRequests {
			return ErrRateLimit
		}
	}
	return fmt.Errorf("anthropic: %w", err)
}
