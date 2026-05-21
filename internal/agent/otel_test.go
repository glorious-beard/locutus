package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/chetan/locutus/internal/agent/adapters"
)

// providerSpanExecutor wraps any AgentExecutor and emits a
// `provider.generate` span around each Run, matching what production
// adapters do. Lets workflow / dispatcher trace-shape tests exercise
// the full four-layer hierarchy without needing a real adapter.
type providerSpanExecutor struct {
	inner    AgentExecutor
	provider string
}

func (p *providerSpanExecutor) Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error) {
	ctx, span := otel.Tracer("github.com/chetan/locutus/internal/agent/adapters").Start(ctx, "provider.generate",
		oteltrace.WithAttributes(
			attribute.String("gen_ai.system", p.provider),
			attribute.String("gen_ai.request.model", "test-model"),
			attribute.String("gen_ai.operation.name", "chat"),
		))
	defer span.End()
	return p.inner.Run(ctx, def, input)
}

// installTracerProvider builds a TracerProvider that writes OTLP-JSON
// to <tempDir>/trace.jsonl using the same custom file exporter
// production code uses, then sets it as the package-wide global. The
// returned cleanup restores the prior global so concurrent tests in
// the same package don't see leaked state.
func installTracerProvider(t *testing.T, tempDir string) (string, func()) {
	t.Helper()
	prev := otel.GetTracerProvider()

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		"",
		semconv.ServiceName("locutus-test"),
	))
	require.NoError(t, err)

	tracePath := filepath.Join(tempDir, TraceFileName)
	exp, err := newOTLPJSONFileExporter(tracePath)
	require.NoError(t, err)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	)
	otel.SetTracerProvider(tp)

	cleanup := func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	}
	return tracePath, cleanup
}

// readSpans loads the OTLP-JSON file and returns one decoded span
// envelope per line. The custom exporter writes each span as a
// complete ResourceSpans wrapper; we walk into the nested arrays to
// pull the leaf span object plus the scope name (so tests can assert
// on it without re-implementing the path).
type traceSpan struct {
	Name         string
	TraceID      string
	SpanID       string
	ParentSpanID string
	Scope        string
	Attributes   map[string]string
}

func readSpans(t *testing.T, tracePath string) []traceSpan {
	t.Helper()
	f, err := os.Open(tracePath)
	require.NoError(t, err)
	defer f.Close()

	var out []traceSpan
	scanner := bufio.NewScanner(f)
	// 1 MiB ceiling per line — span payloads are tiny; this ceiling
	// only matters if a future test's attribute payload grows huge.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		raw := scanner.Bytes()
		var env map[string]any
		require.NoError(t, json.Unmarshal(raw, &env))
		rs, ok := env["resourceSpans"].([]any)
		require.True(t, ok, "missing resourceSpans key")
		for _, rsi := range rs {
			rsm := rsi.(map[string]any)
			ss, ok := rsm["scopeSpans"].([]any)
			require.True(t, ok, "missing scopeSpans")
			for _, ssi := range ss {
				ssm := ssi.(map[string]any)
				scope := ""
				if sc, ok := ssm["scope"].(map[string]any); ok {
					if n, ok := sc["name"].(string); ok {
						scope = n
					}
				}
				spans, ok := ssm["spans"].([]any)
				require.True(t, ok, "missing spans")
				for _, spi := range spans {
					sm := spi.(map[string]any)
					ts := traceSpan{
						Name:       getString(sm, "name"),
						TraceID:    getString(sm, "traceId"),
						SpanID:     getString(sm, "spanId"),
						Scope:      scope,
						Attributes: map[string]string{},
					}
					if pid, ok := sm["parentSpanId"].(string); ok {
						ts.ParentSpanID = pid
					}
					if attrs, ok := sm["attributes"].([]any); ok {
						for _, ai := range attrs {
							am := ai.(map[string]any)
							key, _ := am["key"].(string)
							if v, ok := am["value"].(map[string]any); ok {
								if sv, ok := v["stringValue"].(string); ok {
									ts.Attributes[key] = sv
								} else if iv, ok := v["intValue"].(string); ok {
									ts.Attributes[key] = iv
								}
							}
						}
					}
					out = append(out, ts)
				}
			}
		}
	}
	require.NoError(t, scanner.Err())
	return out
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// TestTraceShapeWorkflowExecutor verifies the substrate's full span
// hierarchy lands in the OTLP-JSON file in the documented shape:
//
//	workflow.phase → agent.dispatch → llm.attempt → provider.generate
//
// The four spans correspond to the four substrate layers:
// WorkflowExecutor.ExecuteRound opens workflow.phase; Dispatcher.
// Dispatch opens agent.dispatch + per-attempt llm.attempt; the
// adapter (simulated here by providerSpanExecutor) opens
// provider.generate.
//
// DJ-130 Phase 1 wired WorkflowExecutor.executeAgent to dispatch
// through Dispatcher.Dispatch directly, so the workflow's executor
// is just the provider-span wrapper around the mock — no bridge
// needed. The role attribute on agent.dispatch is now
// `workflow:<step-id>` (the role tag executeAgent stamps).
//
// Asserts on structure, not timing — the test is deterministic
// regardless of how fast the in-memory mock returns.
func TestTraceShapeWorkflowExecutor(t *testing.T) {
	tempDir := t.TempDir()
	tracePath, cleanup := installTracerProvider(t, tempDir)
	defer cleanup()

	mock := NewMockExecutor(mockResp("planner output"))
	wrapped := &providerSpanExecutor{inner: mock, provider: "anthropic"}

	defs := map[string]AgentDef{
		"planner": {ID: "planner", SystemPrompt: "You are the planner."},
	}

	wf := &Workflow[PlanningState]{
		Rounds: []WorkflowStep[PlanningState]{{
			ID:     "propose",
			Agents: []string{"planner"},
		}},
		MaxRounds: 1,
	}

	exec := &WorkflowExecutor[PlanningState]{
		Executor:  wrapped,
		AgentDefs: defs,
		Workflow:  wf,
	}

	_, err := exec.Run(context.Background(), &PlanningState{Prompt: "Design X."})
	require.NoError(t, err)

	cleanup() // flush spans before reading the file

	spans := readSpans(t, tracePath)
	require.NotEmpty(t, spans, "trace.jsonl should contain at least four spans")

	byName := map[string]traceSpan{}
	for _, s := range spans {
		byName[s.Name] = s
	}

	phase, ok := byName["workflow.phase"]
	require.True(t, ok, "missing workflow.phase span")
	dispatch, ok := byName["agent.dispatch"]
	require.True(t, ok, "missing agent.dispatch span")
	attempt, ok := byName["llm.attempt"]
	require.True(t, ok, "missing llm.attempt span")
	provider, ok := byName["provider.generate"]
	require.True(t, ok, "missing provider.generate span")

	// Hierarchy: provider's parent is attempt, attempt's parent is
	// dispatch, dispatch's parent is phase, phase's parent is empty
	// (the root of the test trace).
	assert.Equal(t, attempt.SpanID, provider.ParentSpanID,
		"provider.generate should descend from llm.attempt")
	assert.Equal(t, dispatch.SpanID, attempt.ParentSpanID,
		"llm.attempt should descend from agent.dispatch")
	assert.Equal(t, phase.SpanID, dispatch.ParentSpanID,
		"agent.dispatch should descend from workflow.phase")
	assert.Empty(t, phase.ParentSpanID, "workflow.phase should be the root")

	// Trace id is the same across all four spans.
	assert.Equal(t, phase.TraceID, dispatch.TraceID)
	assert.Equal(t, dispatch.TraceID, attempt.TraceID)
	assert.Equal(t, attempt.TraceID, provider.TraceID)

	// Key attributes the documented span model promises.
	assert.Equal(t, "propose", phase.Attributes["locutus.workflow.phase"])
	assert.Equal(t, "planner", phase.Attributes["locutus.agent.id"])
	assert.Equal(t, "planner", dispatch.Attributes["locutus.agent.id"])
	assert.Equal(t, "workflow:propose", dispatch.Attributes["locutus.dispatch.role"],
		"DJ-130 Phase 1: executeAgent tags dispatches with workflow:<step-id>")
	assert.Equal(t, "1", attempt.Attributes["locutus.attempt"])
	assert.Equal(t, "anthropic", provider.Attributes["gen_ai.system"])
	assert.Equal(t, "test-model", provider.Attributes["gen_ai.request.model"])
	assert.Equal(t, "chat", provider.Attributes["gen_ai.operation.name"])
}

// TestTraceShapeReActDispatch verifies the ReAct branch's documented
// span hierarchy lands in the OTLP-JSON file:
//
//	agent.dispatch (shape=react)
//	  └── react.iteration #1
//	        ├── llm.attempt → provider.generate
//	        └── tool.invoke (per tool_call)
//	  └── react.iteration #2 (terminal, no tool_calls)
//	        └── llm.attempt → provider.generate
//
// The two-iteration mock ensures both an iteration-with-tool and the
// terminal iteration are exercised in one pass.
func TestTraceShapeReActDispatch(t *testing.T) {
	tempDir := t.TempDir()
	tracePath, cleanup := installTracerProvider(t, tempDir)
	defer cleanup()

	registry := NewToolRegistry()
	registry.Register(adapters.ToolDef{
		Name:        "echo",
		Description: "echoes input",
		InputSchema: map[string]any{"type": "string"},
		Handler: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(map[string]string{"echoed": string(input)})
		},
	})

	mock := NewMockExecutor(
		// Iteration 1: model emits tool_call.
		MockResponse{Response: &AgentOutput{
			Content:   "let me check",
			ToolCalls: []ToolCall{{Name: "echo", Query: "ping", Status: "ok"}},
		}},
		// Iteration 2: model emits final answer.
		MockResponse{Response: &AgentOutput{Content: "done"}},
	)
	wrapped := &providerSpanExecutor{inner: mock, provider: "anthropic"}

	def := AgentDef{
		ID:            "react_test",
		MaxIterations: 5,
		Models:        []ModelPreference{{Provider: "anthropic", Tier: "balanced"}},
	}
	in := AgentInput{Messages: []Message{{Role: "user", Content: "kick off"}}}

	_, err := NewDispatcherWithTools(wrapped, registry).Dispatch(context.Background(), def, in, DispatchOptions{Role: "react_role"})
	require.NoError(t, err)

	cleanup() // flush spans

	spans := readSpans(t, tracePath)
	require.NotEmpty(t, spans)

	var dispatch traceSpan
	var iterations []traceSpan
	var attempts []traceSpan
	var providers []traceSpan
	var toolInvokes []traceSpan
	for _, s := range spans {
		switch s.Name {
		case "agent.dispatch":
			dispatch = s
		case "react.iteration":
			iterations = append(iterations, s)
		case "llm.attempt":
			attempts = append(attempts, s)
		case "provider.generate":
			providers = append(providers, s)
		case "tool.invoke":
			toolInvokes = append(toolInvokes, s)
		}
	}

	require.NotEmpty(t, dispatch.SpanID, "missing agent.dispatch span")
	require.Len(t, iterations, 2, "two react.iteration spans (one per loop iteration)")
	require.Len(t, attempts, 2, "one llm.attempt per iteration")
	require.Len(t, providers, 2, "one provider.generate per iteration")
	require.Len(t, toolInvokes, 1, "one tool.invoke for the iteration that emitted echo")

	// agent.dispatch is the root for the ReAct sub-tree.
	assert.Empty(t, dispatch.ParentSpanID, "agent.dispatch should be root in this test")
	assert.Equal(t, "react", dispatch.Attributes["locutus.dispatch.shape"])
	assert.Equal(t, "react_test", dispatch.Attributes["locutus.agent.id"])
	assert.Equal(t, "react_role", dispatch.Attributes["locutus.dispatch.role"])

	// Both react.iteration spans descend from agent.dispatch.
	for _, it := range iterations {
		assert.Equal(t, dispatch.SpanID, it.ParentSpanID,
			"react.iteration should descend from agent.dispatch")
		assert.Equal(t, "react_test", it.Attributes["locutus.agent.id"])
	}

	// Each llm.attempt descends from a react.iteration.
	iterIDs := map[string]bool{}
	for _, it := range iterations {
		iterIDs[it.SpanID] = true
	}
	for _, a := range attempts {
		assert.True(t, iterIDs[a.ParentSpanID],
			"llm.attempt parent should be a react.iteration")
		assert.Equal(t, "react", a.Attributes["locutus.dispatch.shape"])
	}

	// Each provider.generate descends from an llm.attempt.
	attemptIDs := map[string]bool{}
	for _, a := range attempts {
		attemptIDs[a.SpanID] = true
	}
	for _, p := range providers {
		assert.True(t, attemptIDs[p.ParentSpanID],
			"provider.generate parent should be an llm.attempt")
	}

	// tool.invoke descends from a react.iteration (the one that emitted it).
	for _, ti := range toolInvokes {
		assert.True(t, iterIDs[ti.ParentSpanID],
			"tool.invoke parent should be a react.iteration")
		assert.Equal(t, "echo", ti.Attributes["locutus.tool.name"])
	}

	// Terminal iteration carries the final-answer marker.
	var terminal traceSpan
	for _, it := range iterations {
		if it.Attributes["locutus.react.terminated_by"] == "final_answer" {
			terminal = it
			break
		}
	}
	assert.NotEmpty(t, terminal.SpanID, "one iteration should be marked terminated_by=final_answer")
}

// TestSpanIDFromContextNoOp confirms the no-op tracer produces an
// empty span id, so existing test fixtures that never call
// InitTracer keep emitting byte-identical recordedCall YAML. This is
// the contract the omitempty tag on recordedCall.SpanID relies on —
// a regression here would force every fixture file to grow a
// `span_id: ""` line on the next test run.
func TestSpanIDFromContextNoOp(t *testing.T) {
	// Don't install a TracerProvider — fall through to the global
	// no-op. Span context is invalid; HasSpanID() returns false.
	id := SpanIDFromContext(context.Background())
	assert.Empty(t, id, "no-op tracer should yield empty span id")

	tid := TraceIDFromContext(context.Background())
	assert.Empty(t, tid, "no-op tracer should yield empty trace id")
}

// TestOTLPJSONFileExporterCanonical verifies the file exporter writes
// canonical OTLP-JSON: each line is a parseable ResourceSpans
// envelope with hex-encoded ids and the documented field shapes. A
// downstream tool that ingests OTLP-JSON (Jaeger, otel-cli) needs
// these invariants to replay the trace.
func TestOTLPJSONFileExporterCanonical(t *testing.T) {
	tempDir := t.TempDir()
	tracePath, cleanup := installTracerProvider(t, tempDir)
	defer cleanup()

	tracer := otel.Tracer("test-canonical")
	_, span := tracer.Start(context.Background(), "test.span",
		oteltrace.WithAttributes(
			attribute.String("test.string", "value"),
			attribute.Int("test.int", 42),
			attribute.Bool("test.bool", true),
		))
	span.End()
	cleanup()

	data, err := os.ReadFile(tracePath)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// One span = one line.
	var env map[string]any
	require.NoError(t, json.Unmarshal(data[:len(data)-1], &env))

	rs := env["resourceSpans"].([]any)
	require.Len(t, rs, 1)
	rs0 := rs[0].(map[string]any)

	// Resource carries service.name from the InitTracer setup.
	resource := rs0["resource"].(map[string]any)
	resAttrs := resource["attributes"].([]any)
	require.NotEmpty(t, resAttrs)

	scopeSpans := rs0["scopeSpans"].([]any)
	require.Len(t, scopeSpans, 1)
	ss0 := scopeSpans[0].(map[string]any)
	scope := ss0["scope"].(map[string]any)
	assert.Equal(t, "test-canonical", scope["name"])

	spans := ss0["spans"].([]any)
	require.Len(t, spans, 1)
	sp := spans[0].(map[string]any)
	assert.Equal(t, "test.span", sp["name"])
	traceID := sp["traceId"].(string)
	spanID := sp["spanId"].(string)
	assert.Len(t, traceID, 32, "trace id should be 16 bytes hex (32 chars)")
	assert.Len(t, spanID, 16, "span id should be 8 bytes hex (16 chars)")
	assert.Regexp(t, "^[0-9a-f]+$", traceID)
	assert.Regexp(t, "^[0-9a-f]+$", spanID)

	// Attribute encoding: each entry has key + typed value.
	attrs := sp["attributes"].([]any)
	require.Len(t, attrs, 3)
	got := map[string]any{}
	for _, ai := range attrs {
		am := ai.(map[string]any)
		k := am["key"].(string)
		v := am["value"].(map[string]any)
		got[k] = v
	}
	assert.Equal(t, "value", got["test.string"].(map[string]any)["stringValue"])
	assert.Equal(t, "42", got["test.int"].(map[string]any)["intValue"])
	assert.Equal(t, true, got["test.bool"].(map[string]any)["boolValue"])
}
