package dispatch

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers

func dialBridge(t *testing.T, path string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", path)
	require.NoError(t, err, "dial bridge socket")
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func sendJSON(t *testing.T, conn net.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	data = append(data, '\n')
	_, err = conn.Write(data)
	require.NoError(t, err)
}

func readJSON(t *testing.T, conn net.Conn, out any) {
	t.Helper()
	scanner := bufio.NewScanner(conn)
	require.True(t, scanner.Scan(), "socket closed before response; err=%v", scanner.Err())
	require.NoError(t, json.Unmarshal(scanner.Bytes(), out))
}

func recvEvent(t *testing.T, ch <-chan AgentEvent) AgentEvent {
	t.Helper()
	select {
	case evt := <-ch:
		return evt
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge event")
		return AgentEvent{}
	}
}

// tests

func TestPermBridge_SocketRoundtrip(t *testing.T) {
	b, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	assert.NotEmpty(t, b.SocketPath, "bridge must expose its socket path for --mcp-config")

	conn := dialBridge(t, b.SocketPath)

	// Fake bridge subprocess sends a permission request.
	sendJSON(t, conn, PermRequest{
		ID:    "toolu_abc",
		Tool:  "Bash",
		Input: map[string]any{"command": "rm /tmp/x", "description": "delete"},
	})

	// Supervisor side sees the event.
	evt := recvEvent(t, b.Events)
	assert.Equal(t, EventPermissionRequest, evt.Kind)
	assert.Equal(t, "Bash", evt.ToolName)
	assert.Equal(t, "rm /tmp/x", evt.ToolInput["command"])
	assert.Equal(t, "toolu_abc", evt.InteractionID, "InteractionID must carry the tool_use_id")

	// Supervisor side responds allow.
	require.NoError(t, b.Respond(evt.InteractionID, PermDecision{Behavior: "allow"}))

	// Fake bridge reads the response.
	var resp PermDecision
	readJSON(t, conn, &resp)
	assert.Equal(t, "toolu_abc", resp.ID, "response ID must echo the request ID")
	assert.Equal(t, "allow", resp.Behavior)
	assert.Empty(t, resp.Message)
}

func TestPermBridge_DenyWithMessage(t *testing.T) {
	b, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	conn := dialBridge(t, b.SocketPath)

	sendJSON(t, conn, PermRequest{
		ID: "toolu_xyz", Tool: "Bash", Input: map[string]any{"command": "curl evil.example"},
	})
	evt := recvEvent(t, b.Events)

	require.NoError(t, b.Respond(evt.InteractionID, PermDecision{
		Behavior: "deny",
		Message:  "network egress not permitted for this step",
	}))

	var resp PermDecision
	readJSON(t, conn, &resp)
	assert.Equal(t, "deny", resp.Behavior)
	assert.Equal(t, "network egress not permitted for this step", resp.Message)
}

func TestPermBridge_SequentialRequestsOnOneConnection(t *testing.T) {
	// A single bridge subprocess may be reused by claude across many tool
	// calls in one session. Verify the protocol handles multiple requests
	// serially on the same connection.
	b, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	conn := dialBridge(t, b.SocketPath)

	for _, id := range []string{"req1", "req2", "req3"} {
		sendJSON(t, conn, PermRequest{ID: id, Tool: "Bash", Input: map[string]any{"command": "echo hi"}})
		evt := recvEvent(t, b.Events)
		require.Equal(t, id, evt.InteractionID)

		require.NoError(t, b.Respond(evt.InteractionID, PermDecision{Behavior: "allow"}))

		var resp PermDecision
		readJSON(t, conn, &resp)
		assert.Equal(t, id, resp.ID)
		assert.Equal(t, "allow", resp.Behavior)
	}
}

func TestPermBridge_RespondUnknownIDErrors(t *testing.T) {
	b, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	err = b.Respond("nonexistent", PermDecision{Behavior: "allow"})
	require.Error(t, err, "responding to an unknown ID should error, not panic or hang")
}

func TestPermBridge_CloseStopsListener(t *testing.T) {
	b, err := NewPermBridge()
	require.NoError(t, err)

	path := b.SocketPath
	require.NoError(t, b.Close())

	// After close, the socket file should be cleaned up and no new connections
	// should succeed.
	_, err = net.DialTimeout("unix", path, 100*time.Millisecond)
	assert.Error(t, err, "Close should stop the listener and make the socket unreachable")
}

func TestPermBridge_SocketPermissions(t *testing.T) {
	// The socket should be mode 0600 — readable/writable only by the owning
	// user. Prevents cross-user access in shared /tmp.
	b, err := NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	info, err := os.Stat(b.SocketPath)
	require.NoError(t, err)
	mode := info.Mode().Perm()
	assert.Equal(t, os.FileMode(0o600), mode, "socket should be mode 0600; got %v", mode)
}
