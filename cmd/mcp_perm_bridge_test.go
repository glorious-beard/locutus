package cmd

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests wire the bridge subcommand's client side against a real
// supervisor-side PermBridge (in-process). The full subprocess flow —
// spawning `locutus mcp-perm-bridge` and driving it via MCP JSON-RPC on
// stdio — is covered later by a gated integration test against the
// built locutus binary.

func newTestBridge(t *testing.T) (*dispatch.PermBridge, *bridgeClient) {
	t.Helper()
	b, err := dispatch.NewPermBridge()
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	client, err := dialBridgeSocket(context.Background(), b.SocketPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return b, client
}

// respondOnce runs a goroutine that waits for one event on the bridge
// and replies with the given decision. Returns a channel the caller can
// use to wait for the event (and inspect it) before asserting the
// client-side result.
func respondOnce(t *testing.T, b *dispatch.PermBridge, decision dispatch.PermDecision) chan dispatch.AgentEvent {
	t.Helper()
	seen := make(chan dispatch.AgentEvent, 1)
	go func() {
		select {
		case evt := <-b.Events:
			seen <- evt
			if err := b.Respond(evt.InteractionID, decision); err != nil {
				t.Errorf("Respond: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("timed out waiting for event in respondOnce")
		}
	}()
	return seen
}

func TestBridgeClient_RoundtripAllow(t *testing.T) {
	b, client := newTestBridge(t)

	seen := respondOnce(t, b, dispatch.PermDecision{Behavior: "allow"})

	decision, err := client.request(context.Background(), dispatch.PermRequest{
		ID:    "toolu_abc",
		Tool:  "Bash",
		Input: map[string]any{"command": "ls"},
	})
	require.NoError(t, err)
	require.NotNil(t, decision)
	assert.Equal(t, "allow", decision.Behavior)
	assert.Equal(t, "toolu_abc", decision.ID, "response ID echoes the request ID")

	evt := <-seen
	assert.Equal(t, dispatch.EventPermissionRequest, evt.Kind)
	assert.Equal(t, "Bash", evt.ToolName)
	assert.Equal(t, "toolu_abc", evt.InteractionID)
	assert.Equal(t, "ls", evt.ToolInput["command"])
}

func TestPermBridgeServer_AllowResponseShape(t *testing.T) {
	// The supervisor responds allow; the MCP handler must translate that
	// to a CallToolResult whose single text content carries the JSON body
	// Claude's permission-prompt-tool contract expects:
	//   {"behavior":"allow"}
	b, client := newTestBridge(t)
	server := newPermBridgeServer(client)
	require.NotNil(t, server, "server should construct without error")

	_ = respondOnce(t, b, dispatch.PermDecision{Behavior: "allow"})

	decision, err := client.request(context.Background(), dispatch.PermRequest{
		ID: "id-allow", Tool: "Edit", Input: map[string]any{"file_path": "/a"},
	})
	require.NoError(t, err)

	// Exercise the exact translation the MCP handler performs.
	body, _ := json.Marshal(permResponseBody{Behavior: decision.Behavior, Message: decision.Message})
	result := textResult(string(body))
	require.IsType(t, &mcp.CallToolResult{}, result)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.Truef(t, ok, "expected *mcp.TextContent, got %T", result.Content[0])

	var parsed permResponseBody
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &parsed))
	assert.Equal(t, "allow", parsed.Behavior)
	assert.Empty(t, parsed.Message, "allow response should not carry a message")
	assert.False(t, result.IsError)
}

func TestPermBridgeServer_DenyResponseShape(t *testing.T) {
	b, client := newTestBridge(t)

	_ = respondOnce(t, b, dispatch.PermDecision{
		Behavior: "deny",
		Message:  "network egress not permitted for this step",
	})

	decision, err := client.request(context.Background(), dispatch.PermRequest{
		ID: "id-deny", Tool: "Bash", Input: map[string]any{"command": "curl evil.example"},
	})
	require.NoError(t, err)

	body, _ := json.Marshal(permResponseBody{Behavior: decision.Behavior, Message: decision.Message})
	result := textResult(string(body))
	require.Len(t, result.Content, 1)

	textContent := result.Content[0].(*mcp.TextContent)
	var parsed permResponseBody
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &parsed))
	assert.Equal(t, "deny", parsed.Behavior)
	assert.Equal(t, "network egress not permitted for this step", parsed.Message,
		"deny message must flow through verbatim for Claude to surface it")
}

func TestBridgeClient_SerializesConcurrentRequests(t *testing.T) {
	// Claude Code shouldn't issue overlapping permission prompts in practice
	// (permission is checked before tool execution, which happens one at a
	// time in a session), but the client must not corrupt the stream under
	// accidental overlap. Enforced by bridgeClient.mu.
	b, client := newTestBridge(t)

	// Server side: respond to events as they arrive, each with the request's
	// own ID echoed back in the message so we can detect cross-talk.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3; i++ {
			select {
			case evt := <-b.Events:
				_ = b.Respond(evt.InteractionID, dispatch.PermDecision{
					Behavior: "allow",
					Message:  evt.InteractionID,
				})
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()

	type result struct {
		d   *dispatch.PermDecision
		err error
	}
	results := make(chan result, 3)
	for _, id := range []string{"A", "B", "C"} {
		go func(id string) {
			d, err := client.request(context.Background(),
				dispatch.PermRequest{ID: id, Tool: "Bash", Input: map[string]any{"command": "echo " + id}})
			results <- result{d: d, err: err}
		}(id)
	}

	for i := 0; i < 3; i++ {
		select {
		case r := <-results:
			require.NoError(t, r.err)
			require.NotNil(t, r.d)
			assert.Equal(t, r.d.ID, r.d.Message,
				"each response.ID must match its own message (no cross-talk)")
		case <-time.After(3 * time.Second):
			t.Fatalf("only got %d of 3 responses", i)
		}
	}
	<-done
}

func TestBridgeClient_CtxCancelUnblocks(t *testing.T) {
	b, client := newTestBridge(t)
	_ = b // consume — supervisor never responds

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.request(ctx, dispatch.PermRequest{ID: "x", Tool: "Bash", Input: map[string]any{}})
		done <- err
	}()

	// Let the request actually reach the socket, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled, "ctx cancel must unblock the request")
	case <-time.After(2 * time.Second):
		t.Fatal("client.request did not return after ctx cancel")
	}
}
