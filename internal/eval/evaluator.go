// Package eval provides a pluggable evaluation framework for Locutus
// assertions whose judgments cannot be reduced to a deterministic check —
// the first caller is `llm_review`, and the shape is designed to welcome
// future evaluators (safety_review, latency_review, multi-judge consensus)
// without a central switch statement.
//
// Shape adapted from google/adk-python's evaluation framework (Evaluator
// interface, EvalCase / EvalMetric separation, pluggable runner). Locutus
// drops ADK's EvalSet / Invocation abstractions (those assume a chat-turn
// runtime we don't have) and keys dispatch on spec.AssertionKind rather
// than on evaluator names. See NOTICE for attribution. Governed by DJ-077
// (selective adoption) and DJ-078 (markdown agent defs, no ADK YAML).
package eval

import (
	"context"
	"errors"

	"github.com/chetan/locutus/internal/spec"
)

// Evaluator judges an EvalCase and produces an EvalMetric. Implementations
// are pure relative to the context and case — no hidden state, no retained
// mutation across calls.
type Evaluator interface {
	Name() string
	Evaluate(ctx context.Context, c EvalCase) (*EvalMetric, error)
}

// EvalCase carries everything an Evaluator needs to render a judgment.
// Artifacts is a path → full file contents map; callers populate it from
// spec.Approach.ArtifactPaths, subject to any per-file size cap the
// Evaluator applies during prompt assembly.
type EvalCase struct {
	Approach  spec.Approach
	Assertion spec.Assertion
	Artifacts map[string]string
}

// EvalMetric is the structured verdict from an Evaluator.
//
// Passed is the binary gate the assertion runner branches on.
// Score is a 0..1 quantity for future numeric evaluators; LLMJudge sets
// it to 1.0 on pass and 0.0 on fail so callers that want a uniform shape
// have one.
// Confidence reports the evaluator's subjective certainty — never used to
// gate Passed (DJ-077 design: trust the boolean, surface the number).
type EvalMetric struct {
	EvaluatorName string  `json:"evaluator_name"`
	Passed        bool    `json:"passed"`
	Score         float64 `json:"score"`
	Reasoning     string  `json:"reasoning,omitempty"`
	Confidence    float64 `json:"confidence,omitempty"`
}

// ErrNoEvaluator is returned by Runner.Evaluate when no evaluator is
// registered for the given AssertionKind. Callers distinguish this from
// an evaluator's internal failure so the dispatcher can degrade gracefully
// (e.g. treat unregistered kinds as "skipped" rather than "failed").
var ErrNoEvaluator = errors.New("eval: no evaluator registered for kind")
