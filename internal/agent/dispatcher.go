package agent

import (
	"context"
	"fmt"
	"log/slog"
)

// AgentDispatcher is the unified entry point for every LLM operation in
// Locutus. It wraps AgentExecutor with retry, provider rotation, and
// per-call validation so call sites stop reinventing those patterns.
//
// Today the dispatcher serves two existing dispatch shapes:
//
//   - Structured one-shot — most agents. Single Generate, parse output.
//     Triggered when AgentDef.Grounding is false and the agent declares
//     no Locutus-side tools.
//   - Provider-side grounding — researcher / scout / cost_critic.
//     Single Generate; the provider runs an internal tool loop
//     (web_search) and returns aggregated tool_calls metadata. Triggered
//     by AgentDef.Grounding=true. The Locutus-side flow is identical to
//     structured one-shot — the loop is opaque to us.
//
// Phase 4 of the workflow-unification plan adds a third shape:
// Locutus-side ReAct loops, triggered when an agent declares Tools and
// MaxIterations > 1. Until then the dispatcher branches only on
// validator + rotation + retry — all three shapes look identical from
// the dispatcher's perspective.
type AgentDispatcher interface {
	Dispatch(ctx context.Context, def AgentDef, input AgentInput, opts DispatchOptions) (*AgentOutput, error)
}

// DispatchOptions tunes a single dispatcher call. Zero values are
// sensible defaults: no role tag, no schema override, one attempt
// (no degeneracy retry), no validator, default transport-retry config.
type DispatchOptions struct {
	// Role tags the call's session-trace context (e.g. "split",
	// "synthesis", "challenge"). Empty leaves any role already on
	// ctx untouched.
	Role string

	// OutputSchema overrides AgentDef.OutputSchema for this call.
	// Useful when a single agent .md is reused with different schemas
	// (e.g. spec_advocate produces JustificationBrief in solo mode and
	// AdversarialDefense in adversarial mode).
	OutputSchema string

	// MaxAttempts caps degeneracy-retry attempts. Zero means 1 (no
	// degeneracy retry). Degeneracy-prone agents pass 3 to cover the
	// standard provider rotation [anthropic, googleai, openai] exactly
	// once.
	MaxAttempts int

	// Validator runs against each successful adapter response. When it
	// returns (reason, true), the dispatcher treats the response as
	// degenerate — logs the reason at WARN, rotates providers, and
	// retries (up to MaxAttempts). Nil disables validation; the first
	// non-error response is returned as-is.
	Validator func(out *AgentOutput) (reason string, degenerate bool)

	// Retry overrides the default transport-error retry config used
	// inside each attempt. Nil means use executionRetryConfig().
	Retry *RetryConfig
}

// Dispatcher is the production AgentDispatcher backed by an
// AgentExecutor. Concurrent-safe: the underlying executor and retry
// helpers are; the dispatcher itself holds no mutable state.
type Dispatcher struct {
	Executor AgentExecutor
}

// NewDispatcher returns a Dispatcher wrapping exec. exec must be
// non-nil; the dispatcher offers no in-process fallback.
func NewDispatcher(exec AgentExecutor) *Dispatcher {
	return &Dispatcher{Executor: exec}
}

// Dispatch executes the agent, retrying with provider rotation when
// the validator marks the response degenerate.
//
// Per-attempt flow:
//
//  1. Rotate AgentDef.Models left by attempt-1 so each attempt hits a
//     different provider. Single-provider Models slices skip rotation.
//  2. RunWithRetry against the rotated def — handles transport-error
//     retry (ErrRateLimit / ErrTimeout) within the attempt.
//  3. Apply Validator. If it reports degenerate, log + retry; else
//     return.
//
// Returns the last AgentOutput plus an error when MaxAttempts is
// exhausted; the output is the offending response so callers can
// surface it for diagnosis.
func (d *Dispatcher) Dispatch(ctx context.Context, def AgentDef, input AgentInput, opts DispatchOptions) (*AgentOutput, error) {
	if opts.Role != "" {
		ctx = WithRole(ctx, opts.Role)
	}
	if opts.OutputSchema != "" {
		def.OutputSchema = opts.OutputSchema
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	retry := executionRetryConfig()
	if opts.Retry != nil {
		retry = *opts.Retry
	}

	var lastOut *AgentOutput
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptDef := def
		attemptDef.Models = rotateModels(def.Models, attempt-1)

		out, err := RunWithRetry(ctx, d.Executor, attemptDef, input, retry)
		if err != nil {
			return out, err
		}
		lastOut = out

		if opts.Validator == nil {
			return out, nil
		}
		reason, degenerate := opts.Validator(out)
		if !degenerate {
			return out, nil
		}
		if attempt < maxAttempts {
			slog.Warn("dispatcher: degenerate output; retrying with provider rotation",
				"agent", def.ID,
				"attempt", attempt,
				"max_attempts", maxAttempts,
				"provider_attempted", primaryProvider(attemptDef.Models),
				"next_provider", primaryProvider(rotateModels(def.Models, attempt)),
				"reason", reason)
			continue
		}
		return out, fmt.Errorf("dispatcher: agent %q emitted degenerate output after %d attempts (%s)", def.ID, maxAttempts, reason)
	}
	// Unreachable in practice — the loop returns on every iteration —
	// but Go's flow analysis requires a terminal return.
	return lastOut, fmt.Errorf("dispatcher: retry loop exited without a result (agent %q)", def.ID)
}
