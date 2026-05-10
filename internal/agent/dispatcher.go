package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// AgentDispatcher is the unified entry point for every LLM operation in
// Locutus. It wraps AgentExecutor with retry, provider rotation, and
// per-call validation so call sites stop reinventing those patterns.
//
// The dispatcher serves three shapes:
//
//   - Structured one-shot — most agents. Single Generate, parse output.
//     Triggered when AgentDef.Grounding is false and MaxIterations <= 1.
//   - Provider-side grounding — researcher / scout / cost_critic.
//     Single Generate; the provider runs an internal tool loop
//     (web_search) and returns aggregated tool_calls metadata. Triggered
//     by AgentDef.Grounding=true. The Locutus-side flow is identical to
//     structured one-shot — the loop is opaque to us.
//   - Locutus-side ReAct (workflow-unification Phase 4). Triggered by
//     AgentDef.MaxIterations > 1. The dispatcher drives the loop:
//     each iteration calls the adapter, looks up any tool_calls the
//     model emitted in the global ToolRegistry, executes them, appends
//     the results as user-role messages, and continues until the model
//     emits a non-tool response or MaxIterations is exceeded.
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
//
// Tools, when set, is the registry the ReAct branch consults to
// resolve tool_call handlers emitted by the model. Production wiring
// passes the same registry the Executor was constructed with so the
// dispatcher and the adapter agree on what's callable. ReAct is a
// no-op without a registry — agents with MaxIterations>1 fail at
// dispatch time when Tools is nil rather than silently executing as
// single-call.
type Dispatcher struct {
	Executor AgentExecutor
	Tools    *ToolRegistry
}

// NewDispatcher returns a Dispatcher wrapping exec. exec must be
// non-nil; the dispatcher offers no in-process fallback.
//
// If exec is a *Executor, its tool registry is auto-attached so the
// ReAct branch can resolve handlers. Other AgentExecutor
// implementations (mocks, wrappers) need NewDispatcherWithTools when
// they want ReAct support.
func NewDispatcher(exec AgentExecutor) *Dispatcher {
	d := &Dispatcher{Executor: exec}
	if e, ok := exec.(*Executor); ok && e != nil {
		d.Tools = e.Tools()
	}
	return d
}

// NewDispatcherWithTools returns a Dispatcher wrapping exec with an
// explicit tool registry. Use when exec is not the production
// *Executor (e.g. tests using MockExecutor) but the caller still
// wants the ReAct branch to resolve handlers.
func NewDispatcherWithTools(exec AgentExecutor, tools *ToolRegistry) *Dispatcher {
	return &Dispatcher{Executor: exec, Tools: tools}
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
//
// Wraps the loop in `agent.dispatch` and per-attempt `llm.attempt`
// spans so the OTLP-JSON trace artifact reflects the substrate's
// retry/rotation shape. Phase 4 (ReAct branch) plugs in above this
// function via an early return — its instrumentation matches the
// shape used here so the trace remains uniform across dispatch
// shapes.
func (d *Dispatcher) Dispatch(ctx context.Context, def AgentDef, input AgentInput, opts DispatchOptions) (*AgentOutput, error) {
	if def.MaxIterations > 1 {
		return d.dispatchReAct(ctx, def, input, opts)
	}
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

	// agent.dispatch span: one per Dispatch call. Carries the agent
	// id and (optionally) the role tag so a trace reader can group
	// every llm.attempt + provider.generate descendant under the
	// agent that drove them. Span ends when this function returns,
	// regardless of success / failure / panic-induced unwind.
	dispatchAttrs := []attribute.KeyValue{attribute.String("locutus.agent.id", def.ID)}
	if opts.Role != "" {
		dispatchAttrs = append(dispatchAttrs, attribute.String("locutus.dispatch.role", opts.Role))
	}
	dispatchCtx, dispatchSpan := Tracer().Start(ctx, "agent.dispatch",
		oteltrace.WithAttributes(dispatchAttrs...))
	defer dispatchSpan.End()
	ctx = dispatchCtx

	var lastOut *AgentOutput
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptDef := def
		attemptDef.Models = rotateModels(def.Models, attempt-1)

		// llm.attempt span: one per try. Even when MaxAttempts==1
		// (the common case) this gives the trace a stable child
		// layer between agent.dispatch and provider.generate so the
		// shape doesn't change when degenerate retries kick in.
		attemptCtx, attemptSpan := Tracer().Start(ctx, "llm.attempt",
			oteltrace.WithAttributes(
				attribute.Int("locutus.attempt", attempt),
				attribute.String("locutus.agent.id", def.ID),
				attribute.String("locutus.attempt.provider", primaryProvider(attemptDef.Models)),
			))

		out, err := RunWithRetry(attemptCtx, d.Executor, attemptDef, input, retry)
		if err != nil {
			attemptSpan.End()
			return out, err
		}
		lastOut = out

		if opts.Validator == nil {
			attemptSpan.End()
			return out, nil
		}
		reason, degenerate := opts.Validator(out)
		if !degenerate {
			attemptSpan.End()
			return out, nil
		}
		attemptSpan.SetAttributes(attribute.String("locutus.degenerate.reason", reason))
		attemptSpan.End()
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

// reactSafetyCeiling caps MaxIterations regardless of the agent's
// declared value. A runaway model emitting tool_calls indefinitely
// would otherwise burn provider quota; 20 iterations is far above
// the realistic working depth of any planning task we expect to
// run. Agents needing a tighter cap declare a smaller
// MaxIterations; agents needing more should make the case in the
// plan that drives the change.
const reactSafetyCeiling = 20

// dispatchReAct drives the Locutus-side ReAct loop. Each iteration:
//
//  1. Build adapter request with the current message history.
//  2. Call RunWithRetry — handles transport-error retry within the
//     iteration. The synthetic accumulated AgentOutput grows one
//     GenerateRound per iteration so the recorded call captures the
//     full trace, mirroring how the adapter's internal multi-round
//     captures work today.
//  3. If the model emitted tool_calls, look up each handler in the
//     registry, execute, append the JSON result as a user-role
//     message, and continue.
//  4. If no tool_calls, return — this is the model's final answer.
//
// Bounded by min(def.MaxIterations, reactSafetyCeiling). Exceeding
// the cap returns an error tagged with the agent id and N.
//
// Validator (when supplied via opts) runs against the FINAL response
// only, not per-iteration. A degenerate final answer triggers the
// same provider-rotation+retry pattern Dispatch already uses, with
// each rotation re-running the entire ReAct loop from the
// caller-supplied input. opts.MaxAttempts caps the retry count;
// zero defaults to 1 (no rotation).
//
// Agents trigger this branch by declaring MaxIterations > 1 on the
// AgentDef (frontmatter `max_iterations: 3`). Agents without a tool
// strategy still get every registered tool exposed to the adapter
// (per Phase 4's "no per-agent allowlist" decision), so the model
// can call any tool the registry knows about.
func (d *Dispatcher) dispatchReAct(ctx context.Context, def AgentDef, input AgentInput, opts DispatchOptions) (*AgentOutput, error) {
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
	maxIters := def.MaxIterations
	if maxIters > reactSafetyCeiling {
		maxIters = reactSafetyCeiling
	}
	retry := executionRetryConfig()
	if opts.Retry != nil {
		retry = *opts.Retry
	}

	var lastOut *AgentOutput
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptDef := def
		attemptDef.Models = rotateModels(def.Models, attempt-1)

		out, err := d.runReActAttempt(ctx, attemptDef, input, retry, maxIters)
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
			slog.Warn("dispatcher: degenerate ReAct output; retrying with provider rotation",
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
	return lastOut, fmt.Errorf("dispatcher: ReAct retry loop exited without a result (agent %q)", def.ID)
}

// runReActAttempt drives one provider-rotation attempt of the ReAct
// loop. Splits out from dispatchReAct so the rotation+validator
// scaffolding stays readable. Returns the accumulated AgentOutput
// (with one GenerateRound per iteration) on success, or an error on
// adapter failure / handler failure / cap exceeded.
func (d *Dispatcher) runReActAttempt(
	ctx context.Context,
	def AgentDef,
	input AgentInput,
	retry RetryConfig,
	maxIters int,
) (*AgentOutput, error) {
	// Working copy of the conversation. The dispatcher appends one
	// assistant turn (the model's tool_call emission) and one
	// user turn (the tool result payload) per iteration, then
	// re-sends the whole history on the next call.
	convo := make([]Message, len(input.Messages))
	copy(convo, input.Messages)

	// Per-iteration accumulator. Each iteration produces one
	// GenerateRound; assembling them under a single AgentOutput
	// keeps the recorded-call shape identical to the adapter's
	// existing multi-round captures (Anthropic, OpenAI Responses).
	accumulated := &AgentOutput{}

	for iter := 1; iter <= maxIters; iter++ {
		out, err := RunWithRetry(ctx, d.Executor, def, AgentInput{Messages: convo}, retry)
		if err != nil {
			return out, err
		}

		mergeReActIteration(accumulated, out, iter)

		if len(out.ToolCalls) == 0 {
			// Final answer: model returned text without further
			// tool requests. Promote its content as the
			// dispatcher's response and return.
			accumulated.Content = out.Content
			accumulated.Reasoning = out.Reasoning
			accumulated.RawMessage = out.RawMessage
			if accumulated.Model == "" {
				accumulated.Model = out.Model
			}
			return accumulated, nil
		}

		// Append the model's textual response (if any) so the
		// model sees its own intermediate reasoning on the next
		// iteration. Empty content can happen when the model
		// emitted only a tool_call block; an empty assistant
		// turn would be wire-illegal on Anthropic, so we skip it.
		if out.Content != "" {
			convo = append(convo, Message{Role: reactRoleAssistant, Content: out.Content})
		}

		// Execute every tool_call in order. The dispatcher uses
		// the same ToolRegistry the adapter would dispatch
		// against; this keeps the Locutus-side and provider-side
		// tool execution paths byte-identical.
		for _, call := range out.ToolCalls {
			result, toolErr := d.invokeTool(ctx, def, call)
			if toolErr != nil {
				return accumulated, fmt.Errorf("dispatcher: ReAct agent %q tool %q: %w", def.ID, call.Name, toolErr)
			}
			convo = append(convo, Message{Role: reactRoleUser, Content: result})
		}
	}

	return accumulated, fmt.Errorf("dispatcher: ReAct loop exceeded MaxIterations=%d for agent %q", maxIters, def.ID)
}

// invokeTool resolves the tool by name in the dispatcher's registry
// and executes its handler with the model-emitted arguments. Returns
// the marshaled JSON result the dispatcher will feed back as the
// next user turn. Surfaces a descriptive error when the tool is
// unknown to the registry, when the registry is missing entirely
// (the dispatcher was constructed without one), or when the
// handler is nil (registered as a metadata-only entry — which
// shouldn't happen in production but the test surface is more
// defensive).
//
// The Query field on ToolCall is the convention for server-side
// tools (web_search) where the only useful payload is a query
// string. Locutus-side tools receive their arguments via the
// adapter's tool-use protocol; the mock executor scripting tests
// shapes the input via the same Query field for simplicity. When a
// future production agent emits typed JSON args, this is the spot
// to extend the wire shape.
func (d *Dispatcher) invokeTool(ctx context.Context, def AgentDef, call ToolCall) (string, error) {
	if d.Tools == nil {
		return "", fmt.Errorf("dispatcher has no tool registry; cannot invoke %q (agent %q has MaxIterations>1 but Dispatcher.Tools is nil)", call.Name, def.ID)
	}
	tool, ok := d.Tools.Resolve(call.Name)
	if !ok {
		return "", fmt.Errorf("tool %q not registered", call.Name)
	}
	if tool.Handler == nil {
		return "", fmt.Errorf("tool %q has no handler (registry entry is metadata-only)", call.Name)
	}

	// Locutus-side tools receive their args as JSON. Mock-driven
	// tests script the arg payload via ToolCall.Query for
	// convenience (one string field rather than synthesizing a
	// JSON object); pass it as a JSON string when present, else
	// pass the empty object.
	var args json.RawMessage
	if call.Query != "" {
		quoted, _ := json.Marshal(call.Query)
		args = json.RawMessage(quoted)
	} else {
		args = json.RawMessage("{}")
	}

	result, err := tool.Handler(ctx, args)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// mergeReActIteration appends the iteration's GenerateRound to the
// accumulated output and updates the running totals (tokens,
// citations, server-side tool calls). Index is the iteration
// number; this overrides any per-call Round.Index so the merged
// trace numbers iterations 1..N rather than re-using whatever the
// adapter emitted.
func mergeReActIteration(accumulated *AgentOutput, iter *AgentOutput, index int) {
	round := GenerateRound{
		Index:                    index,
		Reasoning:                iter.Reasoning,
		Text:                     iter.Content,
		Message:                  iter.RawMessage,
		InputTokens:              iter.InputTokens,
		OutputTokens:             iter.OutputTokens,
		ThoughtsTokens:           iter.ThoughtsTokens,
		CacheCreationInputTokens: iter.CacheCreationInputTokens,
		CacheReadInputTokens:     iter.CacheReadInputTokens,
		Citations:                iter.Citations,
	}
	accumulated.Rounds = append(accumulated.Rounds, round)
	accumulated.InputTokens += iter.InputTokens
	accumulated.OutputTokens += iter.OutputTokens
	accumulated.ThoughtsTokens += iter.ThoughtsTokens
	accumulated.TotalTokens += iter.TotalTokens
	accumulated.CacheCreationInputTokens += iter.CacheCreationInputTokens
	accumulated.CacheReadInputTokens += iter.CacheReadInputTokens
	if len(iter.Citations) > 0 {
		accumulated.Citations = append(accumulated.Citations, iter.Citations...)
	}
	if len(iter.ToolCalls) > 0 {
		accumulated.ToolCalls = append(accumulated.ToolCalls, iter.ToolCalls...)
	}
}

// reactRoleAssistant / reactRoleUser mirror the role strings the
// adapter package uses. Duplicated here so dispatcher.go doesn't
// import internal/agent/adapters just for two constants — the
// agent package's Message.Role is a free string in this layer.
const (
	reactRoleAssistant = "assistant"
	reactRoleUser      = "user"
)
