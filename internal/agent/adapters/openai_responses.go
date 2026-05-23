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

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
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
	return &OpenAIResponsesAdapter{client: &c, maxToolRounds: 100}, nil
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
//
// Wraps the dispatch in a `provider.generate` span carrying the OTel
// `gen_ai.*` semantic-convention attributes. CacheReadInputTokens is
// populated by the dispatch loop from
// usage.input_tokens_details.cached_tokens; emitted only when
// non-zero (no separate creation charge on Responses, so creation
// stays zero).
func (a *OpenAIResponsesAdapter) Run(ctx context.Context, req Request) (*Response, error) {
	if a.requiresThinkingSchemaSplit(req) {
		return a.runSplit(ctx, req)
	}
	return a.runOnce(ctx, req, RecordedRoleSingle)
}

// runOnce is the single-SDK-call dispatch path. Responses API
// json_schema strict-mode runs here for thinking-off + schema
// requests.
//
// role names the recorded sub-call (DJ-130 Phase 3) — see
// AnthropicAdapter.runOnce for the recorder contract.
func (a *OpenAIResponsesAdapter) runOnce(ctx context.Context, req Request, role string) (*Response, error) {
	ctx, span := otel.Tracer(adapterTracerName).Start(ctx, "provider.generate",
		oteltrace.WithAttributes(
			attribute.String("gen_ai.system", "openai"),
			attribute.String("gen_ai.request.model", req.Model),
			attribute.String("gen_ai.operation.name", "chat"),
		))
	defer span.End()
	var handle CallHandle
	if rec := CallRecorderFromContext(ctx); rec != nil {
		handle = rec.Begin(ctx, role, req.Model, req, time.Now())
		// Push the handle onto ctx so dispatchOpenAITools can open
		// per-tool-call sub-records.
		ctx = WithCallHandle(ctx, handle)
	}
	resp, err := a.runInner(ctx, req)
	annotateGenAISpan(span, resp)
	if handle != nil {
		handle.Finish(resp, err)
	}
	return resp, err
}

// requiresThinkingSchemaSplit reports whether (thinking != off) +
// OutputSchema on this provider's model is known to emit empty `{}`
// tool args / structured output. Surfaced on gpt-5-nano under low
// reasoning_effort + structured output (DJ-130 motivating context);
// the failure mode is shared across the gpt-5 family today even
// though strict json_schema validation is enforced server-side.
//
// Empty FormatModel falls back to single-call (see Anthropic's
// requiresThinkingSchemaSplit for the rationale).
func (a *OpenAIResponsesAdapter) requiresThinkingSchemaSplit(req Request) bool {
	if req.Thinking == ThinkingOff || req.OutputSchema == nil {
		return false
	}
	if req.FormatModel == "" {
		return false
	}
	return true
}

// runSplit handles the thinking-on + schema combination by issuing
// two Responses API calls back-to-back: a reasoning pass against the
// agent's declared model with the schema cleared, and a format pass
// against the provider's fast tier (gpt-5-mini) with thinking off +
// schema set + tools stripped. Returns one merged Response.
//
// Mirrors AnthropicAdapter.runSplit; see there for the merge
// semantics and the reasoning-pass-keeps-grounding rationale.
func (a *OpenAIResponsesAdapter) runSplit(ctx context.Context, req Request) (*Response, error) {
	reasoningReq := req
	reasoningReq.OutputSchema = nil
	reasoningReq.Messages = buildReasoningPassMessages(req.FormatExampleProse, req.Messages)
	reasoning, err := a.runOnce(ctx, reasoningReq, RecordedRoleReason)
	if err != nil {
		return reasoning, fmt.Errorf("openai split reason: %w", err)
	}
	if reasoning == nil || reasoning.Content == "" {
		return reasoning, fmt.Errorf("openai split reason: empty response")
	}

	formatReq := Request{
		Model:           req.FormatModel,
		SystemPrompt:    CanonicalFormatterPrompt,
		Messages:        buildFormatPassMessages(req.FormatExampleProse, req.FormatExampleDoc, reasoning.Content),
		MaxOutputTokens: req.FormatMaxOutputTokens,
		Thinking:        ThinkingOff,
		OutputSchema:    req.OutputSchema,
	}
	formatted, err := a.runOnce(ctx, formatReq, RecordedRoleFormat)
	if err != nil {
		return formatted, fmt.Errorf("openai split format: %w", err)
	}
	return mergeSplitResponses(reasoning, formatted), nil
}

func (a *OpenAIResponsesAdapter) runInner(ctx context.Context, req Request) (*Response, error) {
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
		citations := extractOpenAICitations(raw)

		out.Model = string(resp.Model)
		out.InputTokens = int(resp.Usage.InputTokens)
		out.OutputTokens = int(resp.Usage.OutputTokens)
		out.TotalTokens = int(resp.Usage.TotalTokens)
		// OpenAI Responses surfaces cached-prefix tokens via
		// usage.input_tokens_details.cached_tokens. There is no
		// separate "creation" charge — automatic prefix caching
		// folds the write into the regular input_tokens count on
		// the first call, so CacheCreationInputTokens stays zero.
		cachedTokens := int(resp.Usage.InputTokensDetails.CachedTokens)
		out.CacheReadInputTokens += cachedTokens

		out.Rounds = append(out.Rounds, Round{
			Index:                round,
			Reasoning:            reasoning,
			Text:                 text,
			Message:              string(raw),
			InputTokens:          int(resp.Usage.InputTokens),
			OutputTokens:         int(resp.Usage.OutputTokens),
			CacheReadInputTokens: cachedTokens,
			Citations:            citations,
		})
		out.Citations = mergeCitations(out.Citations, citations)

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
// strict:true *only when* the tool's input schema is strict-
// compatible — every property listed in `required`. OpenAI's strict
// validator rejects tools whose schemas have optional fields (the
// 400 surface is `'required' is required to be supplied and to be
// an array including every key in properties`). schemaIsFullyRequired
// is the same gate the output-schema path uses; mirroring it here
// keeps the two strict-mode call sites consistent.
//
// The web_search_preview built-in is appended when grounding is set.
func buildOpenAITools(req Request) []responses.ToolUnionParam {
	var tools []responses.ToolUnionParam
	for _, t := range req.Tools {
		ft := responses.FunctionToolParam{
			Name:       t.Name,
			Parameters: t.InputSchema,
			Strict:     openai.Bool(schemaIsFullyRequired(t.InputSchema)),
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
//
// Handler errors are fed back to the model so the loop can recover
// from input mistakes. Context-cancellation / deadline errors
// bubble up instead — these signal the caller's deadline, not a
// recoverable input error.
func dispatchOpenAITools(ctx context.Context, registry []ToolDef, calls []openAICall) ([]responses.ResponseInputItemUnionParam, error) {
	byName := make(map[string]ToolDef, len(registry))
	for _, t := range registry {
		byName[t.Name] = t
	}
	handle := CallHandleFromContext(ctx)
	results := make([]responses.ResponseInputItemUnionParam, 0, len(calls))
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return nil, classifyOpenAIError(err)
		}
		def, ok := byName[call.name]
		if !ok {
			callCtx, span := startToolCallSpan(ctx, call.name, call.callID)
			unknownErr := fmt.Errorf("tool %q not registered", call.name)
			recordToolCallError(span, unknownErr)
			var th ToolCallHandle = noopHandle{}
			if handle != nil {
				th = handle.BeginToolCall(callCtx, call.name, call.callID, []byte(call.arguments), time.Now())
			}
			th.Finish(nil, unknownErr)
			span.End()
			results = append(results, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: call.callID,
					Output: fmt.Sprintf(`{"error": "tool %q not registered"}`, call.name),
				},
			})
			continue
		}
		out, err := runRecordedTool(ctx, handle, def, call.callID, []byte(call.arguments))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, ErrTimeout
			}
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
// sentinels the executor's retry layer pattern-matches. See
// classifyAnthropicError for the canonical mapping; this mirrors it.
func classifyOpenAIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			var hint time.Duration
			if apiErr.Response != nil {
				hint = parseRetryAfterSeconds(apiErr.Response.Header)
			}
			return &RateLimitError{RetryAfter: hint, cause: err}
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return fmt.Errorf("openai: %w (underlying: %s)", ErrTimeout, err.Error())
		}
	}
	return fmt.Errorf("openai: %w (underlying: %s)", ErrIncompatible, err.Error())
}
