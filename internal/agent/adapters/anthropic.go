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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
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
	return &AnthropicAdapter{client: &c, maxToolRounds: 100}, nil
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
//
// Wraps the dispatch in a `provider.generate` span carrying the OTel
// `gen_ai.*` semantic-convention attributes a downstream tool reading
// the OTLP-JSON trace expects (system, request model, usage, response
// id, operation name). The span ends when Run returns; per-round
// captures inside the multi-round tool-use loop don't open child
// spans (one provider.generate per logical Run is the cleaner shape;
// per-round detail is already in Response.Rounds and the per-call YAML).
func (a *AnthropicAdapter) Run(ctx context.Context, req Request) (*Response, error) {
	if a.requiresThinkingSchemaSplit(req) {
		return a.runSplit(ctx, req)
	}
	return a.runOnce(ctx, req, RecordedRoleSingle)
}

// runOnce is the single-SDK-call dispatch path. The DJ-108 native
// `OutputConfig.Format.Schema` strict-mode enforcement runs here for
// thinking-off + schema requests and the (currently empty) set of
// thinking-on + schema models requiresThinkingSchemaSplit decides not
// to split.
//
// role names the recorded sub-call ("single" for non-split, "reason"
// or "format" for the split's two passes). When a CallRecorder is on
// ctx (DJ-130 Phase 3), runOnce opens one per-SDK-call child record
// under the parent step before dispatching. Recorder absence is
// silent — ad-hoc callers that bypassed LoggingExecutor get the same
// behaviour they had pre-Phase 3.
func (a *AnthropicAdapter) runOnce(ctx context.Context, req Request, role string) (*Response, error) {
	ctx, span := otel.Tracer(adapterTracerName).Start(ctx, "provider.generate",
		oteltrace.WithAttributes(
			attribute.String("gen_ai.system", "anthropic"),
			attribute.String("gen_ai.request.model", req.Model),
			attribute.String("gen_ai.operation.name", "chat"),
		))
	defer span.End()
	var handle CallHandle
	if rec := CallRecorderFromContext(ctx); rec != nil {
		handle = rec.Begin(ctx, role, req.Model, req, time.Now())
	}
	params := buildAnthropicMessageNewParams(req)
	resp, err := a.dispatch(ctx, params, req)
	annotateGenAISpan(span, resp)
	if handle != nil {
		handle.Finish(resp, err)
	}
	return resp, err
}

// requiresThinkingSchemaSplit reports whether (thinking != off) +
// OutputSchema on this provider's model is known to emit degenerate
// output: Claude's `dummy` placeholder regime, where extended thinking
// produces real reasoning but the structured JSON pass drops fields
// or substitutes placeholder tokens. Surfaced by the fifth winplan
// re-run (DJ-130; commit 5d15e7b originally landed the split as a
// dispatcher-layer workaround for the same failure mode on Sonnet 4.6
// and Opus 4.7). Universal across the current Claude model family
// (Haiku 4.5 included) — the Models API's structuredOutputs.supported
// flag claims the combination works, but empirical reliability says
// otherwise.
//
// Future Claude models that handle thinking + schema cleanly opt out
// here with an explicit per-model `return false`; the static gate is
// the source of truth (DJ-130 alternative considered: dynamic
// capability detection via Models API — rejected for the initial
// landing because the API's claim and the empirical behaviour
// diverge).
func (a *AnthropicAdapter) requiresThinkingSchemaSplit(req Request) bool {
	if req.Thinking == ThinkingOff || req.OutputSchema == nil {
		return false
	}
	// Empty FormatModel means the executor couldn't resolve the fast
	// tier (deployer-edited models.yaml, test fixture, etc.). Fall
	// back to single-call so the request still goes through — the
	// caller will see the degenerate output and the operator can fix
	// the config. Splitting against the same model the reasoning pass
	// used would defeat the purpose.
	if req.FormatModel == "" {
		return false
	}
	return true
}

// runSplit handles the thinking-on + schema combination by issuing
// two SDK calls back-to-back: a reasoning pass against the agent's
// declared model with the schema cleared, and a format pass against
// the provider's fast tier with thinking off + schema set + tools
// stripped. Returns one merged Response carrying the format pass's
// structured Content, the reasoning pass's thinking, and the union
// of metadata + summed token counts.
//
// The reasoning pass keeps grounding and custom tools — the model is
// expected to reason, search, and call tools normally. The format
// pass strips tools (extraction-only) and grounding (the reasoning
// pass already grounded; doing it again on the format pass would
// duplicate cost without surfacing new evidence). The reasoning
// pass's tool calls + citations are carried forward via
// mergeSplitResponses so downstream consumers see the full picture.
func (a *AnthropicAdapter) runSplit(ctx context.Context, req Request) (*Response, error) {
	reasoningReq := req
	reasoningReq.OutputSchema = nil
	reasoningReq.Messages = buildReasoningPassMessages(req.FormatExampleProse, req.Messages)
	reasoning, err := a.runOnce(ctx, reasoningReq, RecordedRoleReason)
	if err != nil {
		return reasoning, fmt.Errorf("anthropic split reason: %w", err)
	}
	if reasoning == nil || reasoning.Content == "" {
		return reasoning, fmt.Errorf("anthropic split reason: empty response")
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
		return formatted, fmt.Errorf("anthropic split format: %w", err)
	}
	return mergeSplitResponses(reasoning, formatted), nil
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
		// DJ-106 visibility: cache write/read counters surface
		// directly in the trace so operators can confirm
		// user-message caching is firing on a fanout. Per-round
		// values aggregate to the top-level fields below; for
		// single-round calls these values represent the entire call.
		out.CacheCreationInputTokens += int(msg.Usage.CacheCreationInputTokens)
		out.CacheReadInputTokens += int(msg.Usage.CacheReadInputTokens)
		totalUsage.in += int(msg.Usage.InputTokens)
		totalUsage.outTok += int(msg.Usage.OutputTokens)
		totalUsage.total += int(msg.Usage.InputTokens + msg.Usage.OutputTokens)

		text, reasoning, toolUses := splitContent(msg.Content)
		raw, _ := json.Marshal(msg.Content)
		citations := extractAnthropicCitations(raw)
		toolCalls := extractAnthropicToolCalls(raw)

		out.Rounds = append(out.Rounds, Round{
			Index:                    round,
			Reasoning:                reasoning,
			Text:                     text,
			Message:                  string(raw),
			InputTokens:              int(msg.Usage.InputTokens),
			OutputTokens:             int(msg.Usage.OutputTokens),
			CacheCreationInputTokens: int(msg.Usage.CacheCreationInputTokens),
			CacheReadInputTokens:     int(msg.Usage.CacheReadInputTokens),
			Citations:                citations,
		})
		out.Citations = mergeCitations(out.Citations, citations)
		out.ToolCalls = append(out.ToolCalls, toolCalls...)

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

		// Refusal handling (SDK v1.29+ structured stop_details).
		// When the API stops the response on policy grounds, the
		// content is empty (or unhelpful) and the caller needs a
		// typed signal so the dispatcher's fallback walk can advance
		// to a different provider/model rather than retry the same
		// pick. Category / Explanation thread through to the trace
		// recorder via the error string; the structured fields stay
		// available to callers that type-assert on *RefusalError.
		if msg.StopReason == anthropicsdk.StopReasonRefusal {
			return finalizeRounds(out), &RefusalError{
				Category:    string(msg.StopDetails.Category),
				Explanation: msg.StopDetails.Explanation,
			}
		}

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
//
// As of anthropic-sdk-go v1.38 the Error type carries a typed Type()
// method returning the API-emitted error_type ("overloaded_error",
// "rate_limit_error", "api_error", etc.) parsed from the response
// envelope. The classifier dispatches on Type() first, then falls
// back to HTTP-status matching for cases where the body didn't carry
// a recognized type (network errors that produce a synthetic Error
// without an envelope; truly unknown status). Type-based dispatch
// disambiguates two cases the status-only path conflated: HTTP 500
// today can mean either "overloaded_error" (server is busy, retry
// will recover) or "api_error" (server-side bug, retry won't help).
// Mapping:
//
//   - context-deadline / canceled → ErrTimeout
//   - rate_limit_error  (typically 429) → RateLimitError with the
//     Retry-After hint for the same-pick retry path
//   - overloaded_error  (typically 529 / 503) → ErrTimeout — retry
//     fires; the server explicitly says capacity will recover
//   - timeout_error     (typically 408 / 504) → ErrTimeout
//   - api_error         (typically 500) → ErrIncompatible —
//     server-side bug; retry pounds the same code path and wastes the
//     budget. Fallback to the next provider preference.
//   - invalid_request_error / authentication_error / permission_error
//     / not_found_error / billing_error / unknown → ErrIncompatible.
//     Fallback advances; RunWithRetry doesn't loop on account-state
//     errors.
//   - Status-only fallback for errors with no typed envelope:
//       * 429 → RateLimitError
//       * 500 / 502 / 503 / 504 → ErrTimeout (transient retry)
//       * everything else → ErrIncompatible
//
// The previous default-branch behavior wrapped non-classified errors
// as a plain fmt.Errorf, which broke the multi-provider fallback chain
// — any unclassified Anthropic error would abort the whole walk before
// the executor tried googleai or openai.
func classifyAnthropicError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	var apiErr *anthropicsdk.Error
	if errors.As(err, &apiErr) {
		// Type-based dispatch first (v1.38+ envelope parse). Falls
		// through to status-based dispatch when the envelope didn't
		// carry a recognized type ("" sentinel).
		switch apiErr.Type() {
		case anthropicsdk.ErrorTypeRateLimitError:
			return rateLimitError(apiErr, err)
		case anthropicsdk.ErrorTypeOverloadedError, anthropicsdk.ErrorTypeTimeoutError:
			return fmt.Errorf("anthropic: %w (underlying: %s)", ErrTimeout, err.Error())
		case anthropicsdk.ErrorTypeAPIError,
			anthropicsdk.ErrorTypeInvalidRequestError,
			anthropicsdk.ErrorTypeAuthenticationError,
			anthropicsdk.ErrorTypePermissionError,
			anthropicsdk.ErrorTypeNotFoundError,
			anthropicsdk.ErrorTypeBillingError:
			return fmt.Errorf("anthropic: %w (underlying: %s)", ErrIncompatible, err.Error())
		}
		// Status-only fallback for errors whose body didn't carry a
		// typed envelope (truly unknown response shape; synthetic
		// errors built without an envelope).
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			return rateLimitError(apiErr, err)
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return fmt.Errorf("anthropic: %w (underlying: %s)", ErrTimeout, err.Error())
		}
	}
	return fmt.Errorf("anthropic: %w (underlying: %s)", ErrIncompatible, err.Error())
}

// rateLimitError builds a RateLimitError carrying the Retry-After
// hint from the response when present. Shared by the type-based
// (rate_limit_error envelope) and status-based (HTTP 429) paths so
// both surface the same retry-hint shape downstream.
func rateLimitError(apiErr *anthropicsdk.Error, err error) error {
	var hint time.Duration
	if apiErr.Response != nil {
		hint = parseRetryAfterSeconds(apiErr.Response.Header)
	}
	return &RateLimitError{RetryAfter: hint, cause: err}
}
