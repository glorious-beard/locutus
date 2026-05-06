package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// OpenAIResponsesAdapter implements adapters.Adapter against the
// OpenAI Responses API. Strict-mode schemas land as
// `text.format = json_schema` with strict:true; custom tools are
// declared as FunctionTool entries and driven through a tool-call
// loop. Grounding attaches OpenAI's built-in web_search_preview
// tool — unlike Gemini, this composes cleanly with custom tools, so
// Responses is the right adapter for agents that need both.
type OpenAIResponsesAdapter struct {
	client        *openai.Client
	maxToolRounds int
}

// NewOpenAIResponsesAdapter constructs an adapter against
// OPENAI_API_KEY. Returns an error when the env var is unset.
func NewOpenAIResponsesAdapter() (*OpenAIResponsesAdapter, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	c := openai.NewClient(option.WithAPIKey(key))
	return &OpenAIResponsesAdapter{client: &c, maxToolRounds: 10}, nil
}

// Provider returns the canonical name for this adapter.
func (a *OpenAIResponsesAdapter) Provider() string { return "openai" }

// Run dispatches a request through the Responses API. The flow:
//
//  1. Build the input item list (system prompt becomes Instructions;
//     user/assistant turns become EasyInputMessage items).
//  2. Configure strict-mode json_schema when OutputSchema is set.
//  3. Map ThinkingLevel onto reasoning.effort for o-series models.
//  4. Add the web_search_preview built-in tool when Grounding is on.
//  5. Loop on function_call output items, dispatching custom tools
//     and feeding back function_call_output items, until the model
//     emits a non-function-call message.
func (a *OpenAIResponsesAdapter) Run(ctx context.Context, req Request) (*Response, error) {
	items := buildOpenAIInputItems(req.Messages)

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(req.Model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: items},
	}
	if req.SystemPrompt != "" {
		params.Instructions = openai.String(req.SystemPrompt)
	}
	if req.MaxOutputTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(req.MaxOutputTokens))
	}

	switch req.Thinking {
	case ThinkingOn:
		params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffortMedium}
	case ThinkingHigh:
		params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffortHigh}
	}

	if req.OutputSchema != nil {
		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigParamOfJSONSchema(
				openAISchemaName(req.OutputSchema),
				req.OutputSchema,
			),
		}
		if vt := params.Text.Format.OfJSONSchema; vt != nil {
			// OpenAI strict mode demands every property listed in
			// `required` and uses `["type","null"]` unions for
			// optional fields. Schemas with `,omitempty` semantics
			// have a partial required list; setting Strict:true
			// here would force the model to fabricate values. Drop
			// strict for those schemas — the schema doc is still
			// passed and json_schema (non-strict) constrains the
			// shape; the model can omit fields cleanly.
			vt.Strict = openai.Bool(schemaIsFullyRequired(req.OutputSchema))
		}
	}

	tools := buildOpenAITools(req)
	params.Tools = tools

	return a.dispatch(ctx, params, req)
}

// dispatch runs the request and drives the function-call loop when
// the response Output contains function_call items.
func (a *OpenAIResponsesAdapter) dispatch(ctx context.Context, params responses.ResponseNewParams, req Request) (*Response, error) {
	out := &Response{Model: req.Model}

	for round := 1; round <= a.maxToolRounds; round++ {
		resp, err := a.client.Responses.New(ctx, params)
		if err != nil {
			return out, classifyOpenAIError(err)
		}

		text, reasoning, calls := splitOpenAIOutput(resp.Output)
		raw, _ := json.Marshal(resp.Output)

		out.Model = string(resp.Model)
		out.InputTokens = int(resp.Usage.InputTokens)
		out.OutputTokens = int(resp.Usage.OutputTokens)
		out.TotalTokens = int(resp.Usage.TotalTokens)

		out.Rounds = append(out.Rounds, Round{
			Index:        round,
			Reasoning:    reasoning,
			Text:         text,
			Message:      string(raw),
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		})

		if len(calls) == 0 {
			out.Content = text
			out.Reasoning = reasoning
			out.RawMessage = string(raw)
			return finalizeRounds(out), nil
		}

		// Chain the next turn via previous_response_id. The
		// Responses API stores reasoning items, function_call
		// items, and tool/instruction config server-side under
		// the response id; replaying just the tool outputs as
		// new input is the documented pattern for tool-use
		// loops on reasoning-effort models. Without this, the
		// reasoning items emitted in resp.Output would be
		// dropped — and on o-series tiers the API rejects with
		// "Item rs_… of type 'reasoning' was provided without
		// its required following item". The function_call items
		// themselves don't need to be re-sent; the call-id
		// linkage in function_call_output is enough.
		results, err := dispatchOpenAITools(ctx, req.Tools, calls)
		if err != nil {
			return out, err
		}
		params = responses.ResponseNewParams{
			Model:              params.Model,
			PreviousResponseID: openai.String(resp.ID),
			Input:              responses.ResponseNewParamsInputUnion{OfInputItemList: results},
		}
	}

	return out, fmt.Errorf("openai-responses adapter: tool-use loop exceeded %d rounds", a.maxToolRounds)
}

// buildOpenAIInputItems translates neutral Messages into the input-
// item list the Responses API expects. RoleSystem is dropped — the
// system prompt arrives as Instructions, not an input item.
func buildOpenAIInputItems(in []Message) responses.ResponseInputParam {
	out := make(responses.ResponseInputParam, 0, len(in))
	for _, m := range in {
		if m.Role == RoleSystem {
			continue
		}
		role := "user"
		if m.Role == RoleAssistant {
			role = "assistant"
		}
		msg := responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRole(role),
			Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(m.Content)},
			Type:    responses.EasyInputMessageTypeMessage,
		}
		out = append(out, responses.ResponseInputItemUnionParam{OfMessage: &msg})
	}
	return out
}

// buildOpenAITools translates request tools (custom + grounding)
// into the SDK's ToolUnionParam list. Function tools opt into
// strict:true so the schema is enforced at the API level. The
// web_search_preview built-in is appended when grounding is set.
func buildOpenAITools(req Request) []responses.ToolUnionParam {
	var tools []responses.ToolUnionParam
	for _, t := range req.Tools {
		ft := responses.FunctionToolParam{
			Name:       t.Name,
			Parameters: t.InputSchema,
			Strict:     openai.Bool(true),
		}
		if t.Description != "" {
			ft.Description = openai.String(t.Description)
		}
		tools = append(tools, responses.ToolUnionParam{OfFunction: &ft})
	}
	if req.Grounding {
		tools = append(tools, responses.ToolUnionParam{
			OfWebSearchPreview: &responses.WebSearchToolParam{
				Type: responses.WebSearchToolTypeWebSearchPreview,
			},
		})
	}
	return tools
}

// openAISchemaName synthesizes the json_schema name field. Strict
// requires a name matching `[a-zA-Z0-9_-]{1,64}` — derive from the
// schema's title when present, otherwise fall back to a stable
// generic name.
func openAISchemaName(schema map[string]any) string {
	if title, ok := schema["title"].(string); ok && title != "" {
		return sanitizeOpenAISchemaName(title)
	}
	return "structured_response"
}

func sanitizeOpenAISchemaName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "structured_response"
	}
	return b.String()
}

// openAICall captures the fields we need from a function_call output
// item. The SDK's ResponseOutputItemUnion has these fields directly
// when Type=="function_call".
type openAICall struct {
	callID    string
	name      string
	arguments string
	id        string
}

// splitOpenAIOutput walks the Output array and groups items into
// (text, reasoning, function_calls). Reasoning items live as a
// distinct Type=="reasoning" with Summary[] entries.
func splitOpenAIOutput(items []responses.ResponseOutputItemUnion) (text, reasoning string, calls []openAICall) {
	var textParts, reasoningParts []string
	for _, item := range items {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" && c.Text != "" {
					textParts = append(textParts, c.Text)
				}
			}
		case "reasoning":
			for _, s := range item.Summary {
				if s.Text != "" {
					reasoningParts = append(reasoningParts, s.Text)
				}
			}
		case "function_call":
			calls = append(calls, openAICall{
				callID:    item.CallID,
				name:      item.Name,
				arguments: item.Arguments,
				id:        item.ID,
			})
		}
	}
	return strings.Join(textParts, ""), strings.Join(reasoningParts, "\n\n"), calls
}

// dispatchOpenAITools invokes each tool's Handler and packages the
// results as function_call_output input items for the next turn.
func dispatchOpenAITools(ctx context.Context, registry []ToolDef, calls []openAICall) ([]responses.ResponseInputItemUnionParam, error) {
	byName := make(map[string]ToolDef, len(registry))
	for _, t := range registry {
		byName[t.Name] = t
	}
	results := make([]responses.ResponseInputItemUnionParam, 0, len(calls))
	for _, call := range calls {
		def, ok := byName[call.name]
		if !ok {
			results = append(results, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: call.callID,
					Output: fmt.Sprintf(`{"error": "tool %q not registered"}`, call.name),
				},
			})
			continue
		}
		out, err := def.Handler(ctx, json.RawMessage(call.arguments))
		if err != nil {
			results = append(results, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: call.callID,
					Output: fmt.Sprintf(`{"error": %q}`, err.Error()),
				},
			})
			continue
		}
		results = append(results, responses.ResponseInputItemUnionParam{
			OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
				CallID: call.callID,
				Output: string(out),
			},
		})
	}
	return results, nil
}

// classifyOpenAIError translates SDK errors into the neutral
// sentinels the executor's retry layer pattern-matches.
func classifyOpenAIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusTooManyRequests {
			return ErrRateLimit
		}
	}
	return fmt.Errorf("openai: %w", err)
}
