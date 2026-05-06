package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chetan/locutus/internal/agent"
)

// defaultArtifactCapBytes caps the per-file byte count pasted into the
// judge prompt. 64 KiB covers ~1k lines of Go — well above typical review
// scope and comfortably inside modern LLM context windows even after the
// approach body and assertion prompt are factored in.
const defaultArtifactCapBytes = 64 * 1024

// LLMJudge is an Evaluator that asks an LLM to render a structured
// pass/fail judgment against the Approach body, the assertion's Prompt,
// and the artifact contents. It is the built-in evaluator for
// `llm_review` — the only one Locutus registers by default. Future
// evaluators replace or augment it by registering against the same (or
// a new) AssertionKind.
//
// Prompt shape adapted from the LLM-as-judge pattern in adk-python's
// evaluation framework; the JSON-output schema (passed / reasoning /
// confidence) is Locutus-native.
type LLMJudge struct {
	LLM              agent.AgentExecutor
	ArtifactCapBytes int // 0 uses defaultArtifactCapBytes
}

// Name identifies this evaluator in EvalMetric.EvaluatorName.
func (j *LLMJudge) Name() string { return "llm_judge" }

// llmJudgeResponse mirrors the JSON schema llm_judge.md instructs the
// model to emit.
type llmJudgeResponse struct {
	Passed     bool    `json:"passed"`
	Reasoning  string  `json:"reasoning"`
	Confidence float64 `json:"confidence"`
}

// Evaluate renders a judgment. Returns an error only on (a) nil LLM, or
// (b) a provider / parse failure; a "passed=false" verdict is a valid
// EvalMetric, not an error.
func (j *LLMJudge) Evaluate(ctx context.Context, c EvalCase) (*EvalMetric, error) {
	if j.LLM == nil {
		return nil, fmt.Errorf("llm_judge: llm provider is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	prompt := j.buildPrompt(c)

	def := agent.AgentDef{
		ID:           "llm_judge",
		SystemPrompt: "You are the llm_judge evaluator. Respond with valid JSON matching the schema {passed: bool, reasoning: string, confidence: number}.",
	}
	input := agent.AgentInput{Messages: []agent.Message{{Role: "user", Content: prompt}}}
	resp, err := j.LLM.Run(ctx, def, input)
	if err != nil {
		return nil, fmt.Errorf("llm_judge generate: %w", err)
	}
	var out llmJudgeResponse
	if err := json.Unmarshal([]byte(resp.Content), &out); err != nil {
		return nil, fmt.Errorf("llm_judge parse: %w", err)
	}

	score := 0.0
	if out.Passed {
		score = 1.0
	}
	return &EvalMetric{
		EvaluatorName: j.Name(),
		Passed:        out.Passed,
		Score:         score,
		Reasoning:     out.Reasoning,
		Confidence:    out.Confidence,
	}, nil
}

func (j *LLMJudge) artifactCap() int {
	if j.ArtifactCapBytes <= 0 {
		return defaultArtifactCapBytes
	}
	return j.ArtifactCapBytes
}

// buildPrompt assembles the user-message body. Kept deterministic — the
// artifact iteration order is stable so pinning tests aren't flaky, and
// the section headers match what llm_judge.md teaches the model to look
// for.
func (j *LLMJudge) buildPrompt(c EvalCase) string {
	cap := j.artifactCap()
	var b strings.Builder
	fmt.Fprintf(&b, "## Approach\n%s — %s\n\n", c.Approach.ID, c.Approach.Title)
	b.WriteString("## Approach body\n")
	b.WriteString(c.Approach.Body)
	b.WriteString("\n\n")

	b.WriteString("## Assertion to verify\n")
	if c.Assertion.Prompt != "" {
		b.WriteString(c.Assertion.Prompt)
	} else {
		b.WriteString("(no prompt provided)")
	}
	b.WriteString("\n\n")

	b.WriteString("## Artifacts\n")
	paths := make([]string, 0, len(c.Artifacts))
	for p := range c.Artifacts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		content := c.Artifacts[p]
		fmt.Fprintf(&b, "\n### %s\n", p)
		if len(content) > cap {
			fmt.Fprintf(&b, "(file truncated at %d bytes; full size %d bytes)\n", cap, len(content))
			b.WriteString("```\n")
			b.WriteString(content[:cap])
			b.WriteString("\n```\n")
		} else {
			b.WriteString("```\n")
			b.WriteString(content)
			b.WriteString("\n```\n")
		}
	}
	return b.String()
}
