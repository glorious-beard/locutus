package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Helpers -------------------------------------------------------------

// channelStreamParser yields events from a channel; used when a test needs
// to pace events precisely against external signals (like a bridge
// injecting a permission event mid-stream). Uses the streamResult type
// declared in streaming.go.
type channelStreamParser struct {
	ch      <-chan streamResult
	closeFn func()
}

func (p *channelStreamParser) Next(ctx context.Context) (AgentEvent, error) {
	select {
	case r, ok := <-p.ch:
		if !ok {
			return AgentEvent{}, io.EOF
		}
		return r.evt, r.err
	case <-ctx.Done():
		return AgentEvent{}, ctx.Err()
	}
}

func (p *channelStreamParser) Close() error {
	if p.closeFn != nil {
		p.closeFn()
	}
	return nil
}

// sendPermRequest opens a fresh connection to the bridge and sends one
// PermRequest so the bridge surfaces it as an event.
func sendPermRequest(t *testing.T, socketPath string, req PermRequest) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	data, err := json.Marshal(req)
	require.NoError(t, err)
	data = append(data, '\n')
	_, err = conn.Write(data)
	require.NoError(t, err)
	return conn
}

// --- handleInteraction tests --------------------------------------------

func TestHandleInteraction_PermissionAllow(t *testing.T) {
	validator := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: "ALLOW"},
	})
	bridge, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = bridge.Close() })

	sup := &Supervisor{
		cfg: SupervisorConfig{
			LLM:       validator,
			AgentDefs: map[string]agent.AgentDef{"validator": {ID: "validator", SystemPrompt: "be a guardian"}},
		},
		permBridge: bridge,
	}

	// Inject a permission event via the socket so it shows up on bridge.Events.
	conn := sendPermRequest(t, bridge.SocketPath, PermRequest{
		ID: "req-allow", Tool: "Edit", Input: map[string]any{"file_path": "cmd/auth.go"},
	})
	defer func() { _ = conn.Close() }()

	evt := recvEvent(t, bridge.Events)
	require.Equal(t, "req-allow", evt.InteractionID)

	require.NoError(t, sup.handleInteraction(context.Background(), newTestStep(), evt))

	// The bridge should have forwarded the allow decision back over the socket.
	var resp PermDecision
	readJSON(t, conn, &resp)
	assert.Equal(t, "allow", resp.Behavior)
	assert.Equal(t, "req-allow", resp.ID)
	assert.Equal(t, 1, validator.CallCount(), "validator LLM should be invoked once")
}

func TestHandleInteraction_PermissionDeny(t *testing.T) {
	validator := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: "DENY: network egress not permitted for this step"},
	})
	bridge, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = bridge.Close() })

	sup := &Supervisor{
		cfg: SupervisorConfig{
			LLM:       validator,
			AgentDefs: map[string]agent.AgentDef{"validator": {ID: "validator"}},
		},
		permBridge: bridge,
	}

	conn := sendPermRequest(t, bridge.SocketPath, PermRequest{
		ID: "req-deny", Tool: "Bash", Input: map[string]any{"command": "curl evil.example"},
	})
	defer func() { _ = conn.Close() }()

	evt := recvEvent(t, bridge.Events)
	require.NoError(t, sup.handleInteraction(context.Background(), newTestStep(), evt))

	var resp PermDecision
	readJSON(t, conn, &resp)
	assert.Equal(t, "deny", resp.Behavior)
	assert.Equal(t, "network egress not permitted for this step", resp.Message,
		"deny reason must flow through verbatim")
}

func TestHandleInteraction_MissingValidator_DeniesDefensively(t *testing.T) {
	// No validator agent in AgentDefs → handleInteraction must not hang or
	// pass-through silently. Defensive default: deny with a clear message.
	bridge, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = bridge.Close() })

	sup := &Supervisor{
		cfg:        SupervisorConfig{AgentDefs: map[string]agent.AgentDef{}},
		permBridge: bridge,
	}

	conn := sendPermRequest(t, bridge.SocketPath, PermRequest{
		ID: "req-nov", Tool: "Bash", Input: map[string]any{"command": "ls"},
	})
	defer func() { _ = conn.Close() }()

	evt := recvEvent(t, bridge.Events)
	require.NoError(t, sup.handleInteraction(context.Background(), newTestStep(), evt))

	var resp PermDecision
	readJSON(t, conn, &resp)
	assert.Equal(t, "deny", resp.Behavior, "missing validator must default to deny")
	assert.Contains(t, strings.ToLower(resp.Message), "validator",
		"denial message should mention the cause")
}

// --- runAttempt merge tests ---------------------------------------------

// newBridgedTestSupervisor wires a Supervisor with a scripted channel
// parser, a real PermBridge, a scripted validator LLM, and a no-op
// progress notifier. Returns the sup, the driver, the bridge, and a
// channel the test can push parser events into.
func newBridgedTestSupervisor(
	t *testing.T,
	validator agent.AgentExecutor,
) (*Supervisor, *fakeStreamingDriver, *PermBridge, chan streamResult) {
	t.Helper()
	bridge, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = bridge.Close() })

	parserCh := make(chan streamResult, 16)
	parser := &channelStreamParser{ch: parserCh}

	rc := &trackingReadCloser{}
	runner := func(cmd *exec.Cmd) (io.ReadCloser, error) { return rc, nil }

	sup := NewSupervisor(SupervisorConfig{
		MaxRetries: 1,
		LLM:        validator,
		AgentDefs:  map[string]agent.AgentDef{"validator": {ID: "validator"}},
	}, runner)
	sup.permBridge = bridge

	return sup, &fakeStreamingDriver{parser: parser}, bridge, parserCh
}

func TestRunAttempt_PermissionEventMergedMidStream(t *testing.T) {
	validator := agent.NewMockExecutor(agent.MockResponse{
		Response: &agent.AgentOutput{Content: "ALLOW"},
	})
	sup, driver, bridge, parserCh := newBridgedTestSupervisor(t, validator)

	// Push the first four parser events. Hold back the terminal event
	// (and the channel close) until after we've confirmed the bridge
	// event was handled — this makes the test deterministic under the
	// race detector, which would otherwise let the parser drain to EOF
	// before the bridge request reaches the listener.
	parserCh <- streamResult{evt: AgentEvent{Kind: EventInit, SessionID: "sess"}}
	parserCh <- streamResult{evt: AgentEvent{Kind: EventText, Text: "thinking"}}
	parserCh <- streamResult{evt: AgentEvent{Kind: EventToolCall, ToolName: "Read", ToolInput: map[string]any{"file_path": "/a"}}}
	parserCh <- streamResult{evt: AgentEvent{Kind: EventToolResult, Text: "contents"}}

	// Start runAttempt in a goroutine so the main test can orchestrate
	// the bridge event in lockstep with the stream.
	type attemptOut struct {
		result *attemptResult
		err    error
	}
	attemptDone := make(chan attemptOut, 1)
	go func() {
		r, err := sup.runAttempt(context.Background(), newTestStep(), driver, "/tmp/work", "", "")
		attemptDone <- attemptOut{r, err}
	}()

	// Dial the bridge socket and send a permission request. The supervisor's
	// handleInteraction will route through the validator LLM and call
	// bridge.Respond; readJSON on this conn will return once that happens,
	// which we use as a synchronization point.
	conn := sendPermRequest(t, bridge.SocketPath, PermRequest{
		ID: "req-mid", Tool: "Bash", Input: map[string]any{"command": "ls"},
	})
	defer func() { _ = conn.Close() }()

	var bridgeResp PermDecision
	readJSON(t, conn, &bridgeResp)
	assert.Equal(t, "allow", bridgeResp.Behavior)
	assert.Equal(t, "req-mid", bridgeResp.ID)

	// Now that the bridge event has been handled, push the terminal event
	// and close the parser so runAttempt drains to EOF.
	parserCh <- streamResult{evt: AgentEvent{Kind: EventResult, Text: "done"}}
	close(parserCh)

	select {
	case out := <-attemptDone:
		require.NoError(t, out.err)
		require.NotNil(t, out.result)
		require.Len(t, out.result.events, 6,
			"expected 5 parser events + 1 bridge permission event; got kinds: %v",
			kindsOf(out.result.events))

		var seenPerm bool
		for _, e := range out.result.events {
			if e.Kind == EventPermissionRequest {
				seenPerm = true
				assert.Equal(t, "req-mid", e.InteractionID)
				assert.Equal(t, "Bash", e.ToolName)
			}
		}
		assert.True(t, seenPerm, "permission event should appear in result.events")
		assert.Equal(t, 1, validator.CallCount(), "validator LLM invoked exactly once")
	case <-time.After(3 * time.Second):
		t.Fatal("runAttempt did not complete after parser channel closed")
	}
}

func TestRunAttempt_NoBridge_BehavesLikeBefore(t *testing.T) {
	// A supervisor without a bridge must still run happy streams end-to-end
	// (regression guard for Parts 5/6 — the merge should be transparent when
	// no bridge is attached).
	parserCh := make(chan streamResult, 8)
	parser := &channelStreamParser{ch: parserCh}
	rc := &trackingReadCloser{}
	runner := func(cmd *exec.Cmd) (io.ReadCloser, error) { return rc, nil }
	sup := NewSupervisor(SupervisorConfig{MaxRetries: 1}, runner)

	parserCh <- streamResult{evt: AgentEvent{Kind: EventInit, SessionID: "s"}}
	parserCh <- streamResult{evt: AgentEvent{Kind: EventText, Text: "hi"}}
	parserCh <- streamResult{evt: AgentEvent{Kind: EventResult, Text: "done"}}
	close(parserCh)

	driver := &fakeStreamingDriver{parser: parser}
	result, err := sup.runAttempt(context.Background(), newTestStep(), driver, "/tmp/work", "", "")
	require.NoError(t, err)
	require.Len(t, result.events, 3)
}

func kindsOf(events []AgentEvent) []EventKind {
	out := make([]EventKind, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

// ctx-aware timing compile check
var _ = time.Now
