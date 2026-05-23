package adapters

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// adapterTracerName is the InstrumentationScope name attached to every
// span the per-provider adapters emit. Distinct from the agent
// package's tracer so trace consumers can filter "show only adapter
// spans" without descending. The two scopes share a TracerProvider
// when InitTracer has run (and the global no-op provider otherwise).
const adapterTracerName = "github.com/chetan/locutus/internal/agent/adapters"

// annotateGenAISpan stamps the OTel `gen_ai.*` semantic-convention
// usage attributes onto a provider.generate span once the call has
// returned. Called at the bottom of each adapter's Run after the
// dispatch loop completes.
//
// Token counts cover the entire logical call (all rounds of the
// adapter's tool-use loop) — Per-round detail still lives in
// Response.Rounds and the per-call YAML; aggregating them on the span
// keeps the trace's single-row view honest about total spend.
//
// Cache attributes are conditional: Anthropic populates both
// creation/read; OpenAI only populates read; Gemini today populates
// neither. Emitting a zero-valued attribute would make trace queries
// noisier, so we skip when the underlying counter is zero. Same
// principle for response.id — only Anthropic exposes a stable id we
// can pass through today.
// startToolCallSpan opens a child span on the current provider.generate
// span carrying the tool-call's identity. The span name is "tool.call";
// per-name filtering uses the gen_ai.tool.name attribute (OTel GenAI
// semantic convention) so trace queries can pivot on tool identity
// without parsing span names. callID discriminates parallel calls the
// model emitted in the same round.
//
// Returned ctx carries the child span context; callers defer span.End()
// after the handler returns and call recordToolCallError when the
// handler errored so the span surfaces as red in trace UIs.
func startToolCallSpan(ctx context.Context, name, callID string) (context.Context, oteltrace.Span) {
	return otel.Tracer(adapterTracerName).Start(ctx, "tool.call",
		oteltrace.WithAttributes(
			attribute.String("gen_ai.tool.name", name),
			attribute.String("gen_ai.tool.call.id", callID),
		),
	)
}

// recordToolCallError stamps the span with an error status + the
// classified error category so trace UIs surface failed tool calls
// as red without forcing the operator to inspect the attached error
// event. The handler's error is also appended via RecordError for
// operators that want the message body.
func recordToolCallError(span oteltrace.Span, err error) {
	if span == nil || err == nil || !span.IsRecording() {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func annotateGenAISpan(span oteltrace.Span, resp *Response) {
	if resp == nil || !span.IsRecording() {
		return
	}
	if resp.Model != "" {
		span.SetAttributes(attribute.String("gen_ai.response.model", resp.Model))
	}
	if resp.InputTokens > 0 {
		span.SetAttributes(attribute.Int("gen_ai.usage.input_tokens", resp.InputTokens))
	}
	if resp.OutputTokens > 0 {
		span.SetAttributes(attribute.Int("gen_ai.usage.output_tokens", resp.OutputTokens))
	}
	if resp.CacheReadInputTokens > 0 {
		span.SetAttributes(attribute.Int("gen_ai.usage.cache_read_input_tokens", resp.CacheReadInputTokens))
	}
	if resp.CacheCreationInputTokens > 0 {
		span.SetAttributes(attribute.Int("gen_ai.usage.cache_creation_input_tokens", resp.CacheCreationInputTokens))
	}
}
