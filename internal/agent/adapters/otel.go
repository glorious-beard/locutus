package adapters

import (
	"go.opentelemetry.io/otel/attribute"
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
