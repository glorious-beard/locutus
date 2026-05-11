package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SpecSummaryResult is the JSON shape the spec_summarizer agent
// returns. A single field — the one-sentence "what" description of a
// spec node — consumed by the SummariesPresent prereq to fill the
// Summary field on legacy nodes. Distinct from any "why" rationale
// already on the node; see internal/scaffold/agents/spec_summarizer.md
// for the per-kind nuance the agent applies.
type SpecSummaryResult struct {
	Summary string `json:"summary"`
}

func init() {
	RegisterSchema("SpecSummaryResult", SpecSummaryResult{
		Summary: "one or two sentences, under 600 chars, ending in . ! or ?",
	})
}

// InvokeSpecSummarizer runs the spec_summarizer agent against a single
// node's content and returns the parsed result. The caller is
// responsible for writing the Summary back to the node on disk.
//
// kind is one of "feature", "strategy", "decision", "bug", "approach"
// and controls only the framing the agent applies — the schema is
// uniform across kinds.
//
// content is the full JSON (for feature/strategy/decision/bug) or the
// markdown body (for approach) of the node being summarized.
func InvokeSpecSummarizer(ctx context.Context, dispatcher AgentDispatcher, def AgentDef, kind, content string) (*SpecSummaryResult, error) {
	user := buildSpecSummarizerPrompt(kind, content)
	input := AgentInput{Messages: []Message{{Role: "user", Content: user}}}
	out, err := dispatcher.Dispatch(ctx, def, input, DispatchOptions{})
	if err != nil {
		return nil, fmt.Errorf("invoke spec summarizer: %w", err)
	}
	var result SpecSummaryResult
	if err := json.Unmarshal([]byte(out.Content), &result); err != nil {
		return nil, fmt.Errorf("invoke spec summarizer: parse output: %w", err)
	}
	if strings.TrimSpace(result.Summary) == "" {
		return nil, fmt.Errorf("invoke spec summarizer: agent returned empty summary")
	}
	return &result, nil
}

func buildSpecSummarizerPrompt(kind, content string) string {
	var b strings.Builder
	b.WriteString("# Node to summarize\n\n")
	fmt.Fprintf(&b, "**Kind:** %s\n\n", kind)
	b.WriteString("**Content:**\n\n```\n")
	b.WriteString(content)
	b.WriteString("\n```\n")
	return b.String()
}
