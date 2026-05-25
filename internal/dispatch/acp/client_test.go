package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

// TestHandleExtensionMethod_RoutesSDKMessageToActivePromptSink confirms
// the happy path: an _claude/sdkMessage notification for an active
// session writes the raw `message` payload as one JSON line to that
// session's sdkSink.
func TestHandleExtensionMethod_RoutesSDKMessageToActivePromptSink(t *testing.T) {
	c := newClient()
	var sink bytes.Buffer
	sid := acpsdk.SessionId("sess-1")
	c.register(sid, &activePrompt{sdkSink: &sink})
	t.Cleanup(func() { c.unregister(sid) })

	payload := `{"type":"assistant","subagent_type":"spec-scout"}`
	params := []byte(`{"sessionId":"sess-1","message":` + payload + `}`)
	resp, err := c.HandleExtensionMethod(context.Background(), claudeSDKMessageMethod, params)
	if err != nil {
		t.Fatalf("HandleExtensionMethod returned err: %v", err)
	}
	if resp != nil {
		t.Fatalf("notification handlers should return nil response; got %v", resp)
	}
	got := strings.TrimRight(sink.String(), "\n")
	if got != payload {
		t.Fatalf("sink got %q; want %q", got, payload)
	}
	if !strings.HasSuffix(sink.String(), "\n") {
		t.Fatalf("sink line should end with newline; got %q", sink.String())
	}
}

func TestHandleExtensionMethod_UnknownMethodReturnsMethodNotFound(t *testing.T) {
	c := newClient()
	_, err := c.HandleExtensionMethod(context.Background(), "_unknown/method", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected MethodNotFound err for unknown extension; got nil")
	}
}

func TestHandleExtensionMethod_LateDeliveryAfterPromptClosesSilentlyDrops(t *testing.T) {
	// session/update notifications on a retired session are silently
	// dropped (see SessionUpdate handler). _claude/sdkMessage follows
	// the same posture — a late-delivered message for a session that's
	// already been unregistered must not panic or return an error.
	c := newClient()
	params := []byte(`{"sessionId":"sess-already-closed","message":{"type":"assistant"}}`)
	resp, err := c.HandleExtensionMethod(context.Background(), claudeSDKMessageMethod, params)
	if err != nil {
		t.Fatalf("late delivery should be silent; got err %v", err)
	}
	if resp != nil {
		t.Fatalf("late delivery should return nil; got %v", resp)
	}
}

func TestHandleExtensionMethod_MalformedParamsAreLoggedAndDropped(t *testing.T) {
	// A malformed extension payload from a buggy agent must not kill
	// the session — log and drop.
	c := newClient()
	resp, err := c.HandleExtensionMethod(context.Background(), claudeSDKMessageMethod, json.RawMessage(`not-json`))
	if err != nil {
		t.Fatalf("malformed params should not error; got %v", err)
	}
	if resp != nil {
		t.Fatalf("malformed params should return nil; got %v", resp)
	}
}

func TestHandleExtensionMethod_NilSinkIsNoOp(t *testing.T) {
	// A session registered without an sdkSink (capture not enabled)
	// must accept the notification without panicking.
	c := newClient()
	sid := acpsdk.SessionId("sess-no-sink")
	c.register(sid, &activePrompt{}) // sdkSink defaults to nil
	t.Cleanup(func() { c.unregister(sid) })

	params := []byte(`{"sessionId":"sess-no-sink","message":{"type":"assistant"}}`)
	if _, err := c.HandleExtensionMethod(context.Background(), claudeSDKMessageMethod, params); err != nil {
		t.Fatalf("nil sink should be a silent no-op; got err %v", err)
	}
}
