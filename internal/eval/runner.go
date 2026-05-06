package eval

import (
	"context"
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/spec"
)

// Runner dispatches EvalCases to Evaluators keyed by AssertionKind. The
// shape is keyed-by-kind rather than keyed-by-name because assertion
// dispatch in Locutus is driven by the Assertion.Kind the planner
// recorded on the Approach, not by evaluator identity. A future caller
// that wants to run multiple evaluators against one AssertionKind can
// compose them in a consensus wrapper and register that wrapper.
type Runner struct {
	evaluators map[spec.AssertionKind]Evaluator
}

// NewRunner returns a Runner pre-registered with LLMJudge for
// AssertionKindLLMReview. Passing a nil llm is explicitly permitted — the
// Runner still works, but AssertionKindLLMReview evaluations fail fast
// with a clear "llm provider required" error. Callers without an LLM
// should either not register LLMJudge or accept that error.
func NewRunner(llm agent.AgentExecutor) *Runner {
	r := &Runner{evaluators: make(map[spec.AssertionKind]Evaluator)}
	r.Register(spec.AssertionKindLLMReview, &LLMJudge{LLM: llm})
	return r
}

// Register binds an Evaluator to an AssertionKind. Overwrites any prior
// registration without warning — callers wanting composition must build
// the wrapper themselves.
func (r *Runner) Register(kind spec.AssertionKind, e Evaluator) {
	r.evaluators[kind] = e
}

// Evaluate dispatches. Returns ErrNoEvaluator (wrapped) when no evaluator
// is registered for the kind, so callers can distinguish "not supported"
// from "failed."
func (r *Runner) Evaluate(ctx context.Context, kind spec.AssertionKind, c EvalCase) (*EvalMetric, error) {
	e, ok := r.evaluators[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoEvaluator, kind)
	}
	return e.Evaluate(ctx, c)
}
