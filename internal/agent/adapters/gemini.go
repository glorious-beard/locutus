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
// google.golang.org/genai SDK targeting Gemini 3 series models.
//
// Schemas land via responseJsonSchema (raw JSON Schema, broader
// feature support than the legacy responseSchema's OpenAPI subset).
// Custom tool parameters land via parametersJsonSchema for the same
// reason. Per Gemini 3's "Migrating from 2.5 > Tool Support" note,
// responseJsonSchema composes cleanly with built-in tools
// (GoogleSearch, URLContext) AND custom function calling — the
// schema XOR tools restriction that bit Gemini 2.5 is gone.
//
// Earlier branches handled 2.5's incompatibilities (two-phase
// grounded→schematize, ErrIncompatible for schema+tools). They've
// been removed; this adapter assumes Gemini 3+ and the embedded
// models.yaml routes the googleai provider to gemini-3.x tiers.
// A deployer who pins back to 2.5 will rediscover the API rejection
// and either upgrade or hold off on agents that combine schema with
// tools/grounding.
type GeminiAdapter struct {
	client        *genai.Client
	maxToolRounds int
}

// NewGeminiAdapter constructs an adapter against GEMINI_API_KEY (or
// GOOGLE_API_KEY as the alternate env var the SDK reads). Returns
// an error when neither is set.
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

// Run dispatches a request through google.golang.org/genai. Schema,
// custom tools, and grounding combine freely — Gemini 3 supports
// the full matrix in a single call. The function-call loop handles
// multi-round tool use; non-tool responses return immediately.
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

	if req.OutputSchema != nil {
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseJsonSchema = req.OutputSchema
	}

	var tools []*genai.Tool
	if len(req.Tools) > 0 {
		decls := make([]*genai.FunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, &genai.FunctionDeclaration{
				Name:                 t.Name,
				Description:          t.Description,
				ParametersJsonSchema: t.InputSchema,
			})
		}
		tools = append(tools, &genai.Tool{FunctionDeclarations: decls})
	}
	if req.Grounding {
		tools = append(tools, &genai.Tool{GoogleSearch: &genai.GoogleSearch{}})
	}
	cfg.Tools = tools

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
		citations := extractGeminiCitations(resp)

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
			Citations:      citations,
		})
		out.Citations = mergeCitations(out.Citations, citations)

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

// classifyGeminiError translates SDK errors into the neutral
// sentinels the executor's retry layer pattern-matches. The genai
// SDK surfaces both 429s and 504s as generic errors carrying the
// status text in the message; we string-match for the relevant
// patterns rather than coupling to the SDK's internal error types.
//
// Server-side deadlines (504 / Status: DEADLINE_EXCEEDED) classify
// as ErrTimeout — same sentinel as our local context timeout — so
// the executor's fallback walk and RunWithRetry both fire. Without
// this, a flaky preview-tier Gemini that exceeds its own server
// deadline would produce a non-retryable wrapped error and the
// agent would fail without trying its other model preferences.
func classifyGeminiError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(msg, "DEADLINE_EXCEEDED") || strings.Contains(lower, "deadline_exceeded") || strings.Contains(lower, "deadline expired") {
		return ErrTimeout
	}
	if strings.Contains(msg, "429") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "resource_exhausted") {
		return ErrRateLimit
	}
	return fmt.Errorf("gemini: %w", err)
}
