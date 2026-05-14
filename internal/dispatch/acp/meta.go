package acp

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"

	oteltrace "go.opentelemetry.io/otel/trace"
)

// Phase 5 — W3C trace context propagation on the ACP transport (DJ-119).
//
// ACP's `_meta` field is the protocol-blessed home for OpenTelemetry interop
// (see the Extensibility section of the protocol docs). The keys `traceparent`,
// `tracestate`, and `baggage` are reserved for W3C trace context. This file
// implements the traceparent half of that contract in both directions:
//
//   - Outgoing: every request Connection emits (Initialize, NewSession,
//     Prompt, Cancel) injects `_meta.traceparent` derived from the calling
//     ctx's active span, when there is one.
//   - Inbound: SessionUpdate and RequestPermission handlers extract any
//     inbound `_meta.traceparent` and surface it via a structured slog
//     field. There is no client-side handler span today, so the slog field
//     is the discoverable hook; if a future phase adds per-notification
//     spans, the same parsed SpanContext can be promoted to an OTel span
//     link without touching callers.
//
// Design choice (OTel vs. stdlib helpers): Locutus already depends on
// go.opentelemetry.io/otel/trace (see internal/agent/otel.go), so the
// canonical SpanContext type is reused here rather than rolling a small
// string-only helper. The cost is zero new dependencies; the benefit is one
// type that flows from supervisor spans → ACP outgoing → ACP inbound and back.
//
// We deliberately handle ONLY the `traceparent` field. `tracestate` and
// `baggage` are protocol-reserved but Locutus has no upstream producer for
// them today, so they are out of scope per the no-aspirational-fields rule.
// Coexisting `_meta` keys (e.g. Claude Code's `_meta.claudeCode.promptQueueing`)
// are untouched by these helpers.

const traceparentKey = "traceparent"

// injectTraceparent returns a Meta map carrying the W3C `traceparent` for
// ctx's active span context, when one is present. Returns the original meta
// (or nil) unchanged when ctx has no valid span context, and never
// overwrites a caller-supplied `traceparent` value.
func injectTraceparent(ctx context.Context, meta map[string]any) map[string]any {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return meta
	}
	tp := encodeTraceparent(sc)
	if meta == nil {
		return map[string]any{traceparentKey: tp}
	}
	if _, exists := meta[traceparentKey]; exists {
		return meta
	}
	meta[traceparentKey] = tp
	return meta
}

// encodeTraceparent renders a SpanContext as a W3C traceparent string:
//
//	00-<32 hex trace_id>-<16 hex span_id>-<2 hex trace_flags>
//
// The version is fixed at "00", which is the only version the W3C spec has
// defined to date. SpanContext.IsValid() is the caller's responsibility.
func encodeTraceparent(sc oteltrace.SpanContext) string {
	tid := sc.TraceID()
	sid := sc.SpanID()
	flags := byte(sc.TraceFlags())
	return fmt.Sprintf("00-%s-%s-%02x",
		hex.EncodeToString(tid[:]),
		hex.EncodeToString(sid[:]),
		flags,
	)
}

// extractTraceparent returns the raw `traceparent` string from meta, or "" if
// absent / wrong type. Inbound parsing is intentionally tolerant: a
// malformed traceparent is logged downstream as the raw string rather than
// rejected, because the protocol does not promise that every peer ships a
// well-formed value and dropping it on the floor would be worse than logging
// it.
func extractTraceparent(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	raw, ok := meta[traceparentKey]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return s
}

// logInboundTraceparent surfaces an inbound `_meta.traceparent` via the
// package slog logger. The structured key is `remote_traceparent` so it does
// not collide with the outgoing-direction conventions any future supervisor-
// side instrumentation may adopt.
//
// Today the SessionUpdate / RequestPermission handlers do not create their
// own OTel spans, so logging is the discoverable surface. When a future
// phase adds handler-side spans, callers should additionally attach the
// parsed SpanContext as a span link; that promotion can happen without
// changing this helper's signature.
func logInboundTraceparent(msg string, meta map[string]any, extra ...any) {
	tp := extractTraceparent(meta)
	if tp == "" {
		return
	}
	args := append([]any{"remote_traceparent", tp}, extra...)
	slog.Debug(msg, args...)
}
