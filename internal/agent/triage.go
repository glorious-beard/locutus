package agent

import (
	"context"
	"encoding/json"
	"fmt"
)

// TriageVerdict is the structured result of evaluating an issue against GOALS.md.
type TriageVerdict struct {
	Accepted        bool     `json:"accepted"`
	Reason          string   `json:"reason"`
	SuggestedLabels []string `json:"suggested_labels,omitempty"`
	Duplicate       bool     `json:"duplicate"`
	DuplicateOf     string   `json:"duplicate_of,omitempty"`
}

// EvaluateAgainstGoals runs an LLM call to evaluate whether the input aligns
// with the GOALS.md content.
func EvaluateAgainstGoals(ctx context.Context, llm LLM, goalsBody string, input string) (*TriageVerdict, error) {
	req := GenerateRequest{
		Model: DefaultModel,
		Messages: []Message{
			{
				Role:    "system",
				Content: "You evaluate whether proposed features align with project goals. Respond with valid JSON matching the TriageVerdict schema.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("## GOALS.md\n\n%s\n\n## Input\n\n%s", goalsBody, input),
			},
		},
	}

	resp, err := llm.Generate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("triage evaluation: %w", err)
	}

	var verdict TriageVerdict
	if err := json.Unmarshal([]byte(resp.Content), &verdict); err != nil {
		return nil, fmt.Errorf("triage evaluation: failed to parse LLM response: %w", err)
	}

	return &verdict, nil
}
