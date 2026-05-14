package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/dispatch/policy"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// traceparentRE matches a well-formed W3C traceparent: version (00), 32-hex
// trace id, 16-hex span id, 2-hex trace flags. Used as a coarse-grained
// shape check in the outgoing-direction test; per-field equality lives in
// the test itself.
var traceparentRE = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

func testSpanContext(t *testing.T) oteltrace.SpanContext {
	t.Helper()
	tid, err := oteltrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	sid, err := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)
	return oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     false,
	})
}

func TestEncodeTraceparent_RoundTrip(t *testing.T) {
	sc := testSpanContext(t)
	got := encodeTraceparent(sc)
	want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	assert.Equal(t, want, got, "traceparent should follow W3C format exactly")
	assert.Regexp(t, traceparentRE, got, "traceparent should match the W3C shape regex")
}

func TestInjectTraceparent_NoSpanIsNoOp(t *testing.T) {
	ctx := context.Background()

	// nil meta stays nil.
	assert.Nil(t, injectTraceparent(ctx, nil), "no span + nil meta should stay nil")

	// existing meta is returned unchanged with no traceparent injected.
	pre := map[string]any{"claudeCode": map[string]any{"promptQueueing": true}}
	post := injectTraceparent(ctx, pre)
	_, hasTP := post["traceparent"]
	assert.False(t, hasTP, "no span should not inject traceparent")
	assert.Contains(t, post, "claudeCode", "coexisting meta keys must survive")
}

func TestInjectTraceparent_PreservesCallerSuppliedTraceparent(t *testing.T) {
	ctx := oteltrace.ContextWithSpanContext(context.Background(), testSpanContext(t))
	pre := map[string]any{"traceparent": "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-00"}
	post := injectTraceparent(ctx, pre)
	assert.Equal(t, pre["traceparent"], post["traceparent"], "caller-supplied traceparent must not be overwritten")
}

// captureSlog redirects slog output through a JSON handler into a buffer so
// tests can assert on emitted records. Restores the prior default on cleanup.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var mu sync.Mutex
	buf := &lockedBuffer{mu: &mu, buf: &bytes.Buffer{}}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf.buf
}

// lockedBuffer is a tiny synchronized writer so the JSON handler can run from
// the SDK's reader goroutine while the test reads the buffer.
type lockedBuffer struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// TestOutgoing_TraceparentInjected confirms that NewSession and Prompt
// requests carry `_meta.traceparent` derived from the calling ctx's span
// context. Verified at the agent side via fakeAgent.NewSession /
// fakeAgent.Prompt observing the request payload directly.
func TestOutgoing_TraceparentInjected(t *testing.T) {
	sc := testSpanContext(t)
	wantTP := encodeTraceparent(sc)

	var (
		newSessionMetaTP atomic.Value // string
		promptMetaTP     atomic.Value // string
	)

	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(_ context.Context, _ *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			if tp, ok := p.Meta["traceparent"].(string); ok {
				promptMetaTP.Store(tp)
			} else {
				promptMetaTP.Store("")
			}
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	// Wrap NewSession to capture inbound meta. We rely on the existing
	// fakeAgent.NewSession counter + a one-shot capture via closure.
	captureNewSession := func(req acpsdk.NewSessionRequest) {
		if tp, ok := req.Meta["traceparent"].(string); ok {
			newSessionMetaTP.Store(tp)
		} else {
			newSessionMetaTP.Store("")
		}
	}
	agent.onNewSession = captureNewSession

	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	baseCtx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()
	ctx := oteltrace.ContextWithSpanContext(baseCtx, sc)

	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)

	events, err := conn.Prompt(ctx, sessionID, "hello", nil)
	require.NoError(t, err)
	for range events {
		// drain
	}

	gotNew, _ := newSessionMetaTP.Load().(string)
	gotPrompt, _ := promptMetaTP.Load().(string)
	assert.Equal(t, wantTP, gotNew, "NewSession request should carry traceparent")
	assert.Equal(t, wantTP, gotPrompt, "Prompt request should carry traceparent")
	assert.Regexp(t, traceparentRE, gotPrompt, "outgoing traceparent should be W3C-shaped")
}

// TestOutgoing_NoSpan_NoTraceparent confirms the no-op path: a ctx with no
// span context produces requests with no `_meta.traceparent` field at all
// (the Meta map is omitempty on the wire, so the field is silent).
func TestOutgoing_NoSpan_NoTraceparent(t *testing.T) {
	var captured atomic.Value // bool

	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(_ context.Context, _ *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			_, has := p.Meta["traceparent"]
			captured.Store(has)
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()

	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)

	events, err := conn.Prompt(ctx, sessionID, "hello", nil)
	require.NoError(t, err)
	for range events {
	}

	got, _ := captured.Load().(bool)
	assert.False(t, got, "ctx without span context should not produce traceparent in Meta")
}

// TestInbound_TraceparentLogged confirms that when the agent sends a
// SessionNotification with `_meta.traceparent`, the client surfaces it via
// the package slog logger as `remote_traceparent`. No OTel handler-side
// span is created today; the slog field is the discoverable hook.
func TestInbound_TraceparentLogged(t *testing.T) {
	buf := captureSlog(t)

	remoteTP := "00-deadbeefdeadbeefdeadbeefdeadbeef-cafebabecafebabe-01"

	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			content := acpsdk.ContentBlock{Text: &acpsdk.ContentBlockText{Type: "text", Text: "ok"}}
			_ = asc.SessionUpdate(ctx, acpsdk.SessionNotification{
				Meta:      map[string]any{"traceparent": remoteTP},
				SessionId: p.SessionId,
				Update: acpsdk.SessionUpdate{
					AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
						SessionUpdate: "agent_message_chunk",
						Content:       content,
					},
				},
			})
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()

	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)
	events, err := conn.Prompt(ctx, sessionID, "hello", nil)
	require.NoError(t, err)
	for range events {
	}

	// Inspect the captured slog records.
	out := buf.String()
	require.NotEmpty(t, out, "expected at least one slog record")
	require.True(t, assertLogContainsRemoteTP(t, out, remoteTP),
		"expected a slog record with remote_traceparent=%q; got:\n%s", remoteTP, out)
}

// TestInbound_RequestPermissionTraceparentLogged confirms the same surface on
// the session/request_permission method path — the second inbound call site
// per the Phase 5 scope.
func TestInbound_RequestPermissionTraceparentLogged(t *testing.T) {
	buf := captureSlog(t)

	remoteTP := "00-11111111111111111111111111111111-2222222222222222-01"

	agent := &fakeAgent{
		caps: acpsdk.AgentCapabilities{LoadSession: true},
		onPrompt: func(ctx context.Context, asc *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			resp, err := asc.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
				Meta:      map[string]any{"traceparent": remoteTP},
				SessionId: p.SessionId,
				ToolCall:  acpsdk.ToolCallUpdate{ToolCallId: "tc1"},
				Options: []acpsdk.PermissionOption{
					{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow"},
				},
			})
			if err != nil {
				return acpsdk.PromptResponse{}, err
			}
			if resp.Outcome.Selected == nil {
				return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonRefusal}, nil
			}
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	stdin, stdout, cancel := wireFake(t, agent)
	defer cancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ctxCancel()

	conn, err := openWithIO(ctx, stdin, stdout, "")
	require.NoError(t, err)
	defer conn.Close()

	sessionID, err := conn.NewSession(ctx, "/tmp")
	require.NoError(t, err)
	events, err := conn.Prompt(ctx, sessionID, "act", policy.AllowOncePolicy{})
	require.NoError(t, err)
	for range events {
	}

	out := buf.String()
	require.NotEmpty(t, out, "expected at least one slog record")
	require.True(t, assertLogContainsRemoteTP(t, out, remoteTP),
		"expected a slog record with remote_traceparent=%q; got:\n%s", remoteTP, out)
}

// assertLogContainsRemoteTP scans newline-delimited JSON slog records for a
// record carrying remote_traceparent=want.
func assertLogContainsRemoteTP(t *testing.T, ndjson, want string) bool {
	t.Helper()
	for _, line := range bytes.Split([]byte(ndjson), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if got, ok := rec["remote_traceparent"].(string); ok && got == want {
			return true
		}
	}
	return false
}

