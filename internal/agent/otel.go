package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// tracerName is the InstrumentationScope name attached to every span
// the substrate emits. Stable so dashboards filtering by scope (e.g.
// "show only Locutus spans") can pin to it without depending on the
// process binary path.
const tracerName = "github.com/chetan/locutus/internal/agent"

// TraceFileName is the OTLP-JSON trace artifact written next to the
// per-call YAMLs in a session directory. One canonical OTLP-JSON span
// per line; replayable through Jaeger / Tempo / `otel-cli`.
const TraceFileName = "trace.jsonl"

// EnvKeyOTLPEndpoint is the standard env var that activates a second
// span processor that exports over OTLP/HTTP to an external collector.
// The file processor stays active regardless — operators running a
// local collector get live spans without losing the file artifact.
const EnvKeyOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

// Tracer returns the package-wide OTel tracer. When InitTracer hasn't
// run, this returns the global no-op tracer so existing test fixtures
// (which never initialize the SDK) stay byte-identical and per-call
// YAMLs leave SpanID empty via omitempty. The trace package's
// "no-op" semantics are: spans created from a no-op tracer have an
// invalid SpanContext (HasSpanID()==false), so SpanIDFromContext
// returns "" and the recorder writes no span_id field.
func Tracer() oteltrace.Tracer {
	return otel.Tracer(tracerName)
}

// SpanIDFromContext extracts the active span's hex-encoded span id, or
// "" when the context carries no span / a no-op span. Used by the
// session recorder to cross-link a per-call YAML to the matching
// `provider.generate` span without forcing every caller to hold an
// otel reference.
func SpanIDFromContext(ctx context.Context) string {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.HasSpanID() {
		return ""
	}
	id := sc.SpanID()
	return hex.EncodeToString(id[:])
}

// TraceIDFromContext extracts the active span's hex-encoded trace id,
// or "" when the context carries no span / a no-op span. Symmetric
// counterpart to SpanIDFromContext for callers (the session manifest)
// that need the trace identifier rather than the leaf span id.
func TraceIDFromContext(ctx context.Context) string {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.HasTraceID() {
		return ""
	}
	id := sc.TraceID()
	return hex.EncodeToString(id[:])
}

// InitTracer wires up the package-wide OTel TracerProvider that emits
// spans to <sessionDir>/trace.jsonl as canonical OTLP-JSON, one span
// per line. Returns a shutdown func the caller defers to flush the
// file and close the provider.
//
// When OTEL_EXPORTER_OTLP_ENDPOINT is set, a second SimpleSpanProcessor
// is registered around an otlptracehttp exporter so operators running
// a local collector get live spans alongside the file artifact.
//
// Calling InitTracer twice in one process is a no-op on the second
// call — the first provider stays installed. Tests that need an
// isolated provider should construct one directly via
// sdktrace.NewTracerProvider rather than going through this helper.
//
// SimpleSpanProcessor is the deliberate choice: synchronous flush on
// OnEnd matches per-call YAMLs' SIGKILL-survivability for completed
// spans. Open spans on a SIGKILL are lost; the per-call YAMLs stay
// authoritative for crash analysis (each recordedCall.Begin flushes
// input before the adapter runs, so completed calls survive).
func InitTracer(sessionDir string) (func(), error) {
	if sessionDir == "" {
		return func() {}, fmt.Errorf("init tracer: empty session dir")
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return func() {}, fmt.Errorf("init tracer: mkdir %s: %w", sessionDir, err)
	}

	// Pass an empty schema URL when merging into the default resource:
	// resource.Default()'s schema URL changes between OTel SDK
	// versions, and merging two resources with non-empty differing
	// schema URLs is an error. Empty here means "inherit whatever the
	// default carries" — we only contribute service.name on top.
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		"",
		semconv.ServiceName("locutus"),
	))
	if err != nil {
		return func() {}, fmt.Errorf("init tracer: build resource: %w", err)
	}

	fileExporter, err := newOTLPJSONFileExporter(filepath.Join(sessionDir, TraceFileName))
	if err != nil {
		return func() {}, fmt.Errorf("init tracer: open trace file: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(fileExporter)),
	}

	var httpExporter *otlptrace.Exporter
	if os.Getenv(EnvKeyOTLPEndpoint) != "" {
		// Background context: we want the exporter wired at startup,
		// not bound to any caller's per-call ctx. Failures here log
		// and proceed — the file exporter is the always-on artifact.
		exp, exporterErr := otlptracehttp.New(context.Background())
		if exporterErr != nil {
			slog.Warn("otel: OTLP HTTP exporter init failed; file exporter still active",
				"error", exporterErr)
		} else {
			httpExporter = exp
			opts = append(opts, sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)))
		}
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			slog.Warn("otel: tracer provider shutdown error", "error", err)
		}
		// Belt-and-suspenders: tp.Shutdown calls into each registered
		// processor's Shutdown which closes the exporter, but if the
		// HTTP exporter had its own state to drain, give it a chance.
		if httpExporter != nil {
			ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel2()
			_ = httpExporter.Shutdown(ctx2)
		}
	}
	return shutdown, nil
}

// otlpJSONFileExporter writes one canonical OTLP-JSON span per line to
// a file. Each line is a complete ResourceSpans envelope so any OTLP-
// JSON-aware tool (Jaeger ingest, Tempo, `otel-cli replay`) can pull
// the file in and reconstruct the trace without further translation.
//
// We hand-marshal rather than depending on the otlptrace internal
// tracetransform package (which is unexported). The OTLP-JSON wire
// format is stable and small enough to maintain in tree.
type otlpJSONFileExporter struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

func newOTLPJSONFileExporter(path string) (*otlpJSONFileExporter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &otlpJSONFileExporter{file: f}, nil
}

// ExportSpans writes each span as one OTLP-JSON ResourceSpans line.
// SimpleSpanProcessor calls this with len(spans)==1 on every OnEnd,
// which matches the per-line shape directly; we still iterate so a
// hypothetical batch caller works correctly.
func (e *otlpJSONFileExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("otlp-json file exporter: closed")
	}
	for _, span := range spans {
		if span == nil {
			continue
		}
		envelope := encodeResourceSpan(span)
		data, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Errorf("otlp-json marshal: %w", err)
		}
		if _, err := e.file.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("otlp-json write: %w", err)
		}
	}
	return nil
}

// Shutdown flushes and closes the file. SimpleSpanProcessor calls
// Shutdown when the TracerProvider shuts down; we tolerate repeated
// invocations.
func (e *otlpJSONFileExporter) Shutdown(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	if e.file != nil {
		err := e.file.Close()
		e.file = nil
		return err
	}
	return nil
}

// encodeResourceSpan projects an SDK span into a canonical OTLP-JSON
// ResourceSpans envelope. Field names and shapes follow
// opentelemetry-proto/trace/v1/trace.proto's protojson encoding so
// downstream tools that parse OTLP-JSON read the file directly.
//
// IDs are hex-encoded (the OTLP-JSON spec encodes binary fields as
// lowercase hex strings); timestamps are RFC3339Nano integers in
// nanoseconds since the epoch (the spec uses string-encoded uint64
// for fixed64 fields, so we render as decimal strings).
func encodeResourceSpan(s sdktrace.ReadOnlySpan) map[string]any {
	traceID := s.SpanContext().TraceID()
	spanID := s.SpanContext().SpanID()

	span := map[string]any{
		"traceId":           hex.EncodeToString(traceID[:]),
		"spanId":            hex.EncodeToString(spanID[:]),
		"name":              s.Name(),
		"kind":              spanKindCode(s.SpanKind()),
		"startTimeUnixNano": fmt.Sprintf("%d", s.StartTime().UnixNano()),
		"endTimeUnixNano":   fmt.Sprintf("%d", s.EndTime().UnixNano()),
	}
	if parent := s.Parent(); parent.HasSpanID() {
		pid := parent.SpanID()
		span["parentSpanId"] = hex.EncodeToString(pid[:])
	}
	if attrs := s.Attributes(); len(attrs) > 0 {
		span["attributes"] = encodeAttributes(attrs)
	}
	if events := s.Events(); len(events) > 0 {
		evs := make([]map[string]any, 0, len(events))
		for _, ev := range events {
			entry := map[string]any{
				"timeUnixNano": fmt.Sprintf("%d", ev.Time.UnixNano()),
				"name":         ev.Name,
			}
			if len(ev.Attributes) > 0 {
				entry["attributes"] = encodeAttributes(ev.Attributes)
			}
			evs = append(evs, entry)
		}
		span["events"] = evs
	}
	st := s.Status()
	if st.Code != 0 {
		span["status"] = map[string]any{
			"code":    int(st.Code),
			"message": st.Description,
		}
	}

	resAttrs := encodeAttributes(s.Resource().Attributes())
	scope := map[string]any{"name": s.InstrumentationScope().Name}
	if v := s.InstrumentationScope().Version; v != "" {
		scope["version"] = v
	}

	return map[string]any{
		"resourceSpans": []map[string]any{
			{
				"resource": map[string]any{"attributes": resAttrs},
				"scopeSpans": []map[string]any{
					{
						"scope": scope,
						"spans": []map[string]any{span},
					},
				},
			},
		},
	}
}

// spanKindCode maps OTel SpanKind values to the integer codes the
// OTLP proto uses (0=unspecified, 1=internal, 2=server, 3=client,
// 4=producer, 5=consumer). The substrate uses INTERNAL across the
// board — none of these spans cross a network boundary on their own;
// the provider.generate leaf documents the network call but sits
// inside our process.
func spanKindCode(k oteltrace.SpanKind) int {
	switch k {
	case oteltrace.SpanKindInternal:
		return 1
	case oteltrace.SpanKindServer:
		return 2
	case oteltrace.SpanKindClient:
		return 3
	case oteltrace.SpanKindProducer:
		return 4
	case oteltrace.SpanKindConsumer:
		return 5
	default:
		return 0
	}
}

// encodeAttributes projects attribute.KeyValue slices into the OTLP-
// JSON `attributes` shape: each entry is an object with a `key` field
// and a `value` object whose key documents the wire type (stringValue,
// intValue, boolValue, doubleValue, arrayValue). Other types are
// stringified — Locutus only emits string / int / bool today.
func encodeAttributes(attrs []attribute.KeyValue) []map[string]any {
	out := make([]map[string]any, 0, len(attrs))
	for _, a := range attrs {
		entry := map[string]any{"key": string(a.Key)}
		switch a.Value.Type() {
		case attribute.STRING:
			entry["value"] = map[string]any{"stringValue": a.Value.AsString()}
		case attribute.BOOL:
			entry["value"] = map[string]any{"boolValue": a.Value.AsBool()}
		case attribute.INT64:
			entry["value"] = map[string]any{"intValue": fmt.Sprintf("%d", a.Value.AsInt64())}
		case attribute.FLOAT64:
			entry["value"] = map[string]any{"doubleValue": a.Value.AsFloat64()}
		case attribute.STRINGSLICE:
			vals := a.Value.AsStringSlice()
			arr := make([]map[string]any, 0, len(vals))
			for _, v := range vals {
				arr = append(arr, map[string]any{"stringValue": v})
			}
			entry["value"] = map[string]any{"arrayValue": map[string]any{"values": arr}}
		default:
			entry["value"] = map[string]any{"stringValue": a.Value.Emit()}
		}
		out = append(out, entry)
	}
	return out
}
