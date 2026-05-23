package adapters

import (
	"context"
	"time"
)

// CallRecorder is the per-SDK-call recording surface DJ-130 Phase 3
// plumbs into each adapter so per-call YAMLs map one-to-one with
// actual provider round-trips (Anthropic Messages.New, Gemini
// GenerateContent, OpenAI Responses.New). The agent package's
// SessionRecorder satisfies the interface; the adapter package
// references it via this interface so the dependency stays one-way
// (agent imports adapters; adapters know nothing about agent).
//
// One CallHandle per SDK call. For single-call dispatch (non-split,
// non-ReAct) the adapter opens one handle with role "single". For
// the thinking + schema split, the adapter opens one handle per pass
// with role "reason" and "format" — both children of the parent step
// the agent-side LoggingExecutor opened before delegating.
type CallRecorder interface {
	// Begin opens a child call record under the parent step and
	// returns a CallHandle the adapter calls Finish on once the SDK
	// call returns. role names the sub-call's purpose
	// ("single"/"reason"/"format"); model is the concrete model
	// string the SDK was invoked with; req carries the inputs the
	// recorder writes into the child YAML.
	Begin(ctx context.Context, role string, model string, req Request, started time.Time) CallHandle
}

// CallHandle is one in-flight SDK call's recorder bookkeeping. Finish
// flushes the call's response (or error) to disk. Idempotent on nil so
// adapters can `defer handle.Finish(resp, err)` without nil-checking
// when the recorder isn't wired (test fixtures, ad-hoc calls).
//
// BeginToolCall opens a sub-record for one client-dispatched tool
// invocation within this SDK round-trip. The recorder mutates the
// per-call YAML to inline the tool call's name + input + output
// (truncated) + timing so an operator reading the trace sees what
// the model asked for and what it got back without correlating
// against a separate file. callID discriminates parallel tool calls
// the model emitted in the same round.
type CallHandle interface {
	Finish(resp *Response, err error)
	BeginToolCall(ctx context.Context, name string, callID string, input []byte, started time.Time) ToolCallHandle
}

// ToolCallHandle is one in-flight tool dispatch's recorder bookkeeping.
// Finish stamps the output (or error) + timing onto the parent call's
// tool-call slice and reflushes the per-call YAML. Idempotent on nil.
type ToolCallHandle interface {
	Finish(output []byte, err error)
}

// noopHandle satisfies ToolCallHandle when no recorder is wired. Lets
// dispatch helpers call Finish unconditionally without nil-checking
// at every error branch.
type noopHandle struct{}

// Finish is a no-op so dispatchXTools can defer Finish without
// branching on whether a recorder was wired.
func (noopHandle) Finish([]byte, error) {}

// runRecordedTool invokes a tool's Handler under both an OTel span
// and a recorder sub-record. Returns the handler's raw output (or
// error). dispatchAnthropicTools / dispatchGeminiTools /
// dispatchOpenAITools all funnel through here so the per-provider
// dispatch logic stays focused on packaging the result for the
// provider's wire shape, while the cross-cutting concerns (span,
// recording, timing) land in one place.
//
// The span carries tool.name + call.id; the recorder entry carries
// input + output + status + timing. Both close on return regardless
// of handler outcome — error paths still emit a closed span and a
// finalized recorder entry so the trace surfaces failed calls.
func runRecordedTool(ctx context.Context, handle CallHandle, def ToolDef, callID string, input []byte) ([]byte, error) {
	started := time.Now()
	callCtx, span := startToolCallSpan(ctx, def.Name, callID)
	defer span.End()
	var th ToolCallHandle = noopHandle{}
	if handle != nil {
		th = handle.BeginToolCall(callCtx, def.Name, callID, input, started)
	}
	out, err := def.Handler(callCtx, input)
	th.Finish(out, err)
	if err != nil {
		recordToolCallError(span, err)
	}
	return out, err
}

// Recorded sub-call role labels. Adapters pass one of these as the
// role argument to CallRecorder.Begin so the per-step folder lists
// each child YAML by purpose:
//
//   - Single: the entire Run was one SDK call (the common case).
//   - Reason: the reasoning pass of the DJ-130 thinking + schema split
//     (thinking on, schema cleared, tools/grounding retained).
//   - Format: the format pass of the split (thinking off, schema set,
//     tools/grounding stripped, provider fast tier).
//
// Centralised here so all three adapters stay consistent — drift would
// make the per-step folder layout harder to consume across providers.
const (
	RecordedRoleSingle = "single"
	RecordedRoleReason = "reason"
	RecordedRoleFormat = "format"
)

// callRecorderContextKey carries a CallRecorder for the duration of a
// LoggingExecutor.Run delegation. Adapters read it via
// CallRecorderFromContext; absence means recording is off (the
// caller didn't wrap with LoggingExecutor) and adapters skip the
// per-SDK-call writes silently.
type callRecorderContextKey struct{}

// WithCallRecorder returns a context carrying r so downstream
// adapters can open per-SDK-call records under the parent step.
// Production wiring is one call from LoggingExecutor.Run after it
// opens the parent step; ad-hoc adapter callers leave the value
// unset and recording stays off.
func WithCallRecorder(ctx context.Context, r CallRecorder) context.Context {
	return context.WithValue(ctx, callRecorderContextKey{}, r)
}

// CallRecorderFromContext returns the CallRecorder set via
// WithCallRecorder, or nil when none was set. Adapters use the nil
// to skip the per-SDK-call write (single-call adapters that
// pre-dated DJ-130 had no recorder access either; the nil branch
// preserves that behaviour for ad-hoc callers).
func CallRecorderFromContext(ctx context.Context) CallRecorder {
	if v, ok := ctx.Value(callRecorderContextKey{}).(CallRecorder); ok {
		return v
	}
	return nil
}

// callHandleContextKey carries the active SDK-call's CallHandle so
// dispatchXTools (called from inside the SDK round-trip) can open
// tool-call sub-records without each dispatch function having to
// thread the handle through its signature.
type callHandleContextKey struct{}

// WithCallHandle returns a context carrying h so downstream
// tool-dispatch helpers can open sub-records under the current
// per-SDK-call YAML.
func WithCallHandle(ctx context.Context, h CallHandle) context.Context {
	return context.WithValue(ctx, callHandleContextKey{}, h)
}

// CallHandleFromContext returns the CallHandle set via WithCallHandle,
// or nil when none was set (ad-hoc callers; tests; legacy paths).
// dispatchXTools tolerates nil — recording skips silently.
func CallHandleFromContext(ctx context.Context) CallHandle {
	if v, ok := ctx.Value(callHandleContextKey{}).(CallHandle); ok {
		return v
	}
	return nil
}
