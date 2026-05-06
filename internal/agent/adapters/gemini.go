package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"google.golang.org/genai"
)

// GeminiAdapter implements adapters.Adapter against the
// google.golang.org/genai SDK. Strict-mode schemas land as
// ResponseSchema on the GenerateContentConfig; custom tools are
// expressed as FunctionDeclarations and driven through a function-
// call loop. Grounding attaches the GoogleSearch tool.
//
// Provider constraints baked in:
//
//   - ResponseSchema is incompatible with custom tools at the API
//     level. When both are requested the adapter logs a Warn and
//     falls back to prompt-only schema enforcement.
//   - GoogleSearch grounding is incompatible with custom tools. The
//     adapter prefers custom tools when both are set and logs a Warn.
type GeminiAdapter struct {
	client        *genai.Client
	maxToolRounds int
}

// NewGeminiAdapter constructs an adapter against GEMINI_API_KEY (or
// GOOGLE_API_KEY as the alternate env var the SDK reads). Returns an
// error when neither is set.
func NewGeminiAdapter(ctx context.Context) (*GeminiAdapter, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY / GOOGLE_API_KEY not set")
	}
	c, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	return &GeminiAdapter{client: c, maxToolRounds: 10}, nil
}

// Provider returns the canonical name for this adapter.
func (g *GeminiAdapter) Provider() string { return "googleai" }

// Run dispatches a request through google.golang.org/genai. The
// flow:
//
//  1. Build GenerateContentConfig (system instruction, max output
//     tokens, thinking budget, schema, tools).
//  2. Call GenerateContent with the user-side messages as Contents.
//  3. If the response contains FunctionCall parts, dispatch them
//     against the request's Tools, append FunctionResponse parts to
//     the next turn, and loop. Otherwise return.
//  4. Aggregate per-round telemetry.
func (g *GeminiAdapter) Run(ctx context.Context, req Request) (*Response, error) {
	cfg := &genai.GenerateContentConfig{}
	if req.SystemPrompt != "" {
		cfg.SystemInstruction = genai.NewContentFromText(req.SystemPrompt, genai.RoleUser)
	}
	if req.MaxOutputTokens > 0 {
		cfg.MaxOutputTokens = int32(req.MaxOutputTokens)
	}

	switch req.Thinking {
	case ThinkingOn:
		budget := int32(4096)
		cfg.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget:  &budget,
			IncludeThoughts: true,
		}
	case ThinkingHigh:
		budget := int32(8192)
		cfg.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget:  &budget,
			IncludeThoughts: true,
		}
	}

	hasCustomTools := len(req.Tools) > 0
	wantSchema := req.OutputSchema != nil
	wantGrounding := req.Grounding

	// Gemini's API rejects responseSchema together with function
	// declarations, and rejects GoogleSearch together with function
	// declarations. Both collisions are deployment-routing errors:
	// the agent shouldn't have routed here in the first place.
	// Surface ErrIncompatible so the executor advances to the next
	// preference in def.Models rather than producing structurally
	// wrong output the downstream parser can't handle.
	if wantSchema && hasCustomTools {
		return nil, fmt.Errorf("%w: gemini cannot combine responseSchema with custom tools (model=%s)",
			ErrIncompatible, req.Model)
	}
	if wantGrounding && hasCustomTools {
		return nil, fmt.Errorf("%w: gemini cannot combine GoogleSearch grounding with custom tools (model=%s)",
			ErrIncompatible, req.Model)
	}

	if wantSchema {
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseSchema = jsonSchemaToGenaiSchema(req.OutputSchema)
	}

	if hasCustomTools {
		decls := make([]*genai.FunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, &genai.FunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  jsonSchemaToGenaiSchema(t.InputSchema),
			})
		}
		cfg.Tools = []*genai.Tool{{FunctionDeclarations: decls}}
	} else if wantGrounding {
		cfg.Tools = []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}
	}

	contents := buildGeminiContents(req.Messages)
	return g.dispatch(ctx, req, contents, cfg)
}

// dispatch runs GenerateContent and drives the function-call loop
// when the response contains FunctionCall parts.
func (g *GeminiAdapter) dispatch(ctx context.Context, req Request, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*Response, error) {
	out := &Response{Model: req.Model}

	for round := 1; round <= g.maxToolRounds; round++ {
		resp, err := g.client.Models.GenerateContent(ctx, req.Model, contents, cfg)
		if err != nil {
			return out, classifyGeminiError(err)
		}

		text, reasoning, calls := splitGeminiContent(resp)
		raw, _ := json.Marshal(resp)

		usage := geminiUsage(resp)
		out.Model = resp.ModelVersion
		out.InputTokens = usage.in
		out.OutputTokens = usage.out
		out.ThoughtsTokens = usage.thoughts
		out.TotalTokens = usage.total

		out.Rounds = append(out.Rounds, Round{
			Index:          round,
			Reasoning:      reasoning,
			Text:           text,
			Message:        string(raw),
			InputTokens:    usage.in,
			OutputTokens:   usage.out,
			ThoughtsTokens: usage.thoughts,
		})

		if len(calls) == 0 {
			out.Content = text
			out.Reasoning = reasoning
			out.RawMessage = string(raw)
			return finalizeRounds(out), nil
		}

		// Append the model turn so the next call has the
		// FunctionCall in conversation history, then a user turn
		// with FunctionResponse parts carrying handler output.
		if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
			contents = append(contents, resp.Candidates[0].Content)
		}
		respParts, err := dispatchGeminiTools(ctx, req.Tools, calls)
		if err != nil {
			return out, err
		}
		contents = append(contents, &genai.Content{Role: genai.RoleUser, Parts: respParts})
	}

	return out, fmt.Errorf("gemini adapter: tool-use loop exceeded %d rounds", g.maxToolRounds)
}

// buildGeminiContents translates neutral Messages into the SDK's
// Content list. RoleSystem entries are dropped — the executor
// places the system prompt in cfg.SystemInstruction.
func buildGeminiContents(in []Message) []*genai.Content {
	out := make([]*genai.Content, 0, len(in))
	for _, m := range in {
		if m.Role == RoleSystem {
			continue
		}
		role := genai.Role(genai.RoleUser)
		if m.Role == RoleAssistant {
			role = genai.Role(genai.RoleModel)
		}
		out = append(out, genai.NewContentFromText(m.Content, role))
	}
	return out
}

// splitGeminiContent walks the first candidate's parts and groups
// them into (text, reasoning, function_calls). Multi-candidate
// responses are not requested by our adapters; if the API ever
// returns more than one we ignore the extras.
func splitGeminiContent(resp *genai.GenerateContentResponse) (text, reasoning string, calls []*genai.FunctionCall) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return "", "", nil
	}
	var textParts, reasoningParts []string
	for _, p := range resp.Candidates[0].Content.Parts {
		if p == nil {
			continue
		}
		if p.FunctionCall != nil && p.FunctionCall.Name != "" {
			calls = append(calls, p.FunctionCall)
			continue
		}
		if p.Thought {
			if p.Text != "" {
				reasoningParts = append(reasoningParts, p.Text)
			}
			continue
		}
		if p.Text != "" {
			textParts = append(textParts, p.Text)
		}
	}
	return strings.Join(textParts, ""), strings.Join(reasoningParts, "\n\n"), calls
}

type geminiUsageTotals struct{ in, out, thoughts, total int }

func geminiUsage(resp *genai.GenerateContentResponse) geminiUsageTotals {
	if resp == nil || resp.UsageMetadata == nil {
		return geminiUsageTotals{}
	}
	u := resp.UsageMetadata
	return geminiUsageTotals{
		in:       int(u.PromptTokenCount),
		out:      int(u.CandidatesTokenCount),
		thoughts: int(u.ThoughtsTokenCount),
		total:    int(u.TotalTokenCount),
	}
}

// dispatchGeminiTools invokes each tool's Handler and packages the
// results as FunctionResponse parts for the next user turn. The
// FunctionResponse.Response payload stores the handler's JSON
// output under a uniform "output" key — Gemini's documented
// contract for passing tool results back into the conversation.
//
// Handler errors are fed back to the model so the loop can recover
// from input mistakes. Context-cancellation / deadline errors
// bubble up instead — these signal the caller's deadline, not a
// recoverable input error.
func dispatchGeminiTools(ctx context.Context, registry []ToolDef, calls []*genai.FunctionCall) ([]*genai.Part, error) {
	byName := make(map[string]ToolDef, len(registry))
	for _, t := range registry {
		byName[t.Name] = t
	}
	parts := make([]*genai.Part, 0, len(calls))
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return nil, classifyGeminiError(err)
		}
		def, ok := byName[call.Name]
		if !ok {
			parts = append(parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{
				ID:       call.ID,
				Name:     call.Name,
				Response: map[string]any{"error": fmt.Sprintf("tool %q not registered", call.Name)},
			}})
			continue
		}
		input, err := json.Marshal(call.Args)
		if err != nil {
			parts = append(parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{
				ID:       call.ID,
				Name:     call.Name,
				Response: map[string]any{"error": "marshal input: " + err.Error()},
			}})
			continue
		}
		out, err := def.Handler(ctx, input)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, ErrTimeout
			}
			parts = append(parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{
				ID:       call.ID,
				Name:     call.Name,
				Response: map[string]any{"error": err.Error()},
			}})
			continue
		}
		var decoded any
		if uErr := json.Unmarshal(out, &decoded); uErr != nil {
			decoded = string(out)
		}
		parts = append(parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{
			ID:       call.ID,
			Name:     call.Name,
			Response: map[string]any{"output": decoded},
		}})
	}
	return parts, nil
}

// jsonSchemaToGenaiSchema projects a JSON-Schema map onto the SDK's
// *genai.Schema. Only the subset Gemini accepts is mapped: type,
// properties, required, items, enum, description, additionalProperties.
// Unknown / unmappable keys are silently dropped — Gemini rejects
// schemas that contain fields it doesn't understand.
func jsonSchemaToGenaiSchema(schema map[string]any) *genai.Schema {
	if schema == nil {
		return nil
	}
	out := &genai.Schema{}
	if t, ok := schema["type"].(string); ok {
		out.Type = jsonSchemaTypeToGenai(t)
	}
	if desc, ok := schema["description"].(string); ok {
		out.Description = desc
	}
	if enum, ok := schema["enum"].([]any); ok {
		for _, e := range enum {
			if s, ok := e.(string); ok {
				out.Enum = append(out.Enum, s)
			}
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		out.Properties = map[string]*genai.Schema{}
		for k, v := range props {
			if m, ok := v.(map[string]any); ok {
				out.Properties[k] = jsonSchemaToGenaiSchema(m)
			}
		}
	}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				out.Required = append(out.Required, s)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		out.Items = jsonSchemaToGenaiSchema(items)
	}
	return out
}

// jsonSchemaTypeToGenai maps the JSON-Schema primitive type to
// genai.Type. Unknown primitives default to STRING — a safer
// fallback than the SDK's zero value (which the API rejects).
func jsonSchemaTypeToGenai(t string) genai.Type {
	switch t {
	case "object":
		return genai.TypeObject
	case "array":
		return genai.TypeArray
	case "string":
		return genai.TypeString
	case "integer":
		return genai.TypeInteger
	case "number":
		return genai.TypeNumber
	case "boolean":
		return genai.TypeBoolean
	default:
		return genai.TypeString
	}
}

// classifyGeminiError translates SDK errors into the neutral
// sentinels the executor's retry layer pattern-matches. The genai
// SDK surfaces 429s as a generic error containing the status text;
// we string-match for the relevant patterns rather than coupling to
// the SDK's internal error types.
func classifyGeminiError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	msg := err.Error()
	if strings.Contains(msg, "429") || strings.Contains(strings.ToLower(msg), "rate limit") || strings.Contains(strings.ToLower(msg), "resource_exhausted") {
		return ErrRateLimit
	}
	return fmt.Errorf("gemini: %w", err)
}
