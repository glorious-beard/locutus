package agent

import (
	"context"
	"fmt"
	"time"
)

// NotifyingExecutor wraps an AgentExecutor and emits per-call lifecycle
// events to an EventSink, so non-workflow commands (justify, adopt's
// planner subcalls, import's intake, refine's rewriter/synthesizer,
// assimilate's remediator) get the same console feedback as workflow-
// driven ones.
//
// The wrapper is transparent on the data path — Inner.Run is called
// unconditionally and its result returned verbatim. The sink only
// observes; it never gates execution.
//
// Suppression: when called from inside a WorkflowExecutor step, the
// workflow already emits its own per-step events on the same sink, and
// double-emitting would render two spinners per call. WorkflowExecutor
// stamps the context with WithSuppressLLMNotify before dispatching each
// agent so this decorator skips emission for those calls.
type NotifyingExecutor struct {
	Inner AgentExecutor
	Sink  EventSink
}

// Run implements AgentExecutor. Emits started/completed/error events
// keyed by AgentID with empty StepID. The cliSink renders empty-StepID
// events as `agentID` (no step prefix); the MCP sink folds them into
// progress messages identically to workflow events.
func (n *NotifyingExecutor) Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error) {
	if n == nil || n.Sink == nil || n.Inner == nil {
		// Defensive: a nil decorator behaves like a passthrough so
		// callers can wrap unconditionally without nil checks.
		if n != nil && n.Inner != nil {
			return n.Inner.Run(ctx, def, input)
		}
		return nil, nil
	}

	if SuppressLLMNotify(ctx) {
		return n.Inner.Run(ctx, def, input)
	}

	// Rate-limit wait fires from inside Inner.Run. Register a sink
	// callback that surfaces it as a "retrying" event keyed by
	// AgentID — same shape the workflow path uses, so cliSink renders
	// rate-limit pauses identically whether the call originated
	// inside or outside a workflow.
	ctx = WithRateLimitWaitCallback(ctx, func(sleep time.Duration) {
		n.Sink.OnEvent(WorkflowEvent{
			AgentID:   def.ID,
			Status:    "retrying",
			Message:   fmt.Sprintf("rate-limited; waiting %s", sleep.Round(time.Second)),
			Timestamp: time.Now(),
		})
	})

	n.Sink.OnEvent(WorkflowEvent{
		AgentID:   def.ID,
		Status:    "started",
		Timestamp: time.Now(),
	})

	out, err := n.Inner.Run(ctx, def, input)

	if err != nil {
		n.Sink.OnEvent(WorkflowEvent{
			AgentID:   def.ID,
			Status:    "error",
			Message:   err.Error(),
			Timestamp: time.Now(),
		})
	} else {
		n.Sink.OnEvent(WorkflowEvent{
			AgentID:   def.ID,
			Status:    "completed",
			Timestamp: time.Now(),
		})
	}

	return out, err
}

// SpecSearch passes the inner executor's spec_search swappable
// through. NotifyingExecutor doesn't own a swappable itself — it's
// a sink-emission wrapper. The pass-through is load-bearing for
// council runs: specgen.go's specSearchSwap helper type-asserts the
// outer AgentExecutor to find the swappable, and the cmd-layer
// wraps the production *Executor in NotifyingExecutor (via
// withProgressSink). Without this method on the wrapper the
// assertion fails silently and the council never applies its
// in-flight overlay swap, leaving spec_search / spec_list_manifest /
// spec_get tool handlers pointed at the FS-backed default for the
// entire run.
//
// See LoggingExecutor.SpecSearch for the latency history and the
// failure mode this method (and its SpecListManifest / SpecGet
// siblings below) closes.
func (n *NotifyingExecutor) SpecSearch() *SwappableSpecSearch {
	if n == nil || n.Inner == nil {
		return nil
	}
	if p, ok := n.Inner.(interface{ SpecSearch() *SwappableSpecSearch }); ok {
		return p.SpecSearch()
	}
	return nil
}

// SpecListManifest passes the inner executor's spec_list_manifest
// swappable through. See SpecSearch above for the load-bearing
// rationale.
func (n *NotifyingExecutor) SpecListManifest() *SwappableSpecListManifest {
	if n == nil || n.Inner == nil {
		return nil
	}
	if p, ok := n.Inner.(interface {
		SpecListManifest() *SwappableSpecListManifest
	}); ok {
		return p.SpecListManifest()
	}
	return nil
}

// SpecGet passes the inner executor's spec_get swappable through.
// See SpecSearch above for the load-bearing rationale.
func (n *NotifyingExecutor) SpecGet() *SwappableSpecGet {
	if n == nil || n.Inner == nil {
		return nil
	}
	if p, ok := n.Inner.(interface{ SpecGet() *SwappableSpecGet }); ok {
		return p.SpecGet()
	}
	return nil
}

// suppressLLMNotifyKey is the context value the workflow executor sets
// to tell NotifyingExecutor "I'm running this call as a workflow step;
// don't emit per-call events on top of the per-step ones I'm already
// firing." Direct LLM call sites (RunInto, RunJustify and friends)
// leave it unset so notifications fire.
type suppressLLMNotifyKey struct{}

// WithSuppressLLMNotify returns a context that disables NotifyingExecutor
// emission for downstream calls. Used by WorkflowExecutor.executeAgent;
// no other call site should need this.
func WithSuppressLLMNotify(ctx context.Context) context.Context {
	return context.WithValue(ctx, suppressLLMNotifyKey{}, true)
}

// SuppressLLMNotify reports whether NotifyingExecutor emission is
// suppressed for the given context.
func SuppressLLMNotify(ctx context.Context) bool {
	v, _ := ctx.Value(suppressLLMNotifyKey{}).(bool)
	return v
}
