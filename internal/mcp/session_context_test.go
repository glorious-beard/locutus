// DJ-143 — the session-context module captures (runtime, mode) at
// MCP initialize time from ClientInfo.name + _meta["locutus.mode"]
// and exposes SessionRuntime + requireRuntime for handler use.
package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSessionKey is a stand-in used as a map key for the unit tests.
type fakeSessionKey struct{ id string }

func TestSessionRuntimeStoresAndRetrieves(t *testing.T) {
	clearSessionRuntimes()
	key := &fakeSessionKey{id: "s1"}
	storeSessionRuntime(key, "claude-code", "interactive")
	rt, mode := sessionRuntimeFor(key)
	assert.Equal(t, "claude-code", rt)
	assert.Equal(t, "interactive", mode)
}

func TestSessionRuntimeUnknownReturnsEmpty(t *testing.T) {
	clearSessionRuntimes()
	rt, mode := sessionRuntimeFor(&fakeSessionKey{id: "missing"})
	assert.Empty(t, rt)
	assert.Empty(t, mode)
}

func TestSessionRuntimeNormalizesCase(t *testing.T) {
	clearSessionRuntimes()
	key := &fakeSessionKey{id: "s2"}
	storeSessionRuntime(key, "Claude-Code", "  Interactive  ")
	rt, mode := sessionRuntimeFor(key)
	assert.Equal(t, "claude-code", rt)
	assert.Equal(t, "interactive", mode)
}

type emptyArgs struct{}
type emptyResult struct{}

func TestRequireRuntimeAllowsListed(t *testing.T) {
	clearSessionRuntimes()
	called := false
	inner := func(_ context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, emptyResult, error) {
		called = true
		return nil, emptyResult{}, nil
	}
	wrapped := requireRuntimeAny(inner, "codex", "gemini")

	req := &mcp.CallToolRequest{Session: nil, Params: &mcp.CallToolParamsRaw{Name: "spec_loop_begin"}}
	// Bind the request.Session (nil here) as the key the wrapper looks up.
	storeSessionRuntime(req.Session, "codex", "interactive")

	_, _, err := wrapped(context.Background(), req, emptyArgs{})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRequireRuntimeDeniesUnlisted(t *testing.T) {
	clearSessionRuntimes()
	inner := func(_ context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, emptyResult, error) {
		t.Fatal("inner handler must not run when runtime is denied")
		return nil, emptyResult{}, nil
	}
	wrapped := requireRuntimeAny(inner, "codex", "gemini")

	req := &mcp.CallToolRequest{Session: nil, Params: &mcp.CallToolParamsRaw{Name: "spec_loop_begin"}}
	storeSessionRuntime(req.Session, "claude-code", "interactive")

	res, _, err := wrapped(context.Background(), req, emptyArgs{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "claude-code")
	assert.Contains(t, tc.Text, "spec_loop_begin")
}

func TestDenyErrorMessageContainsAllFields(t *testing.T) {
	got := denyErrorMessageFor("claude-code", "interactive", "spec_loop_begin", "codex", "gemini")
	assert.Contains(t, strings.ToLower(got), "claude-code")
	assert.Contains(t, got, "interactive")
	assert.Contains(t, got, "spec_loop_begin")
	assert.Contains(t, got, "codex")
	assert.Contains(t, got, "gemini")
	assert.Contains(t, got, "docs/runtime-affordances.md")
}

// TestInitializedHandlerExtractsRuntimeAndMode drives a full MCP
// initialize exchange against an in-memory transport pair and asserts
// that the daemon captured the client's ClientInfo.name as runtime.
// Mode defaults to "interactive" because the SDK's Client.Connect does
// not populate InitializeParams.Meta — the bridge sets _meta in
// production; the fallback covers the test path.
func TestInitializedHandlerExtractsRuntimeAndMode(t *testing.T) {
	clearSessionRuntimes()

	server := mcp.NewServer(
		&mcp.Implementation{Name: "locutus-test", Version: "0.0.0"},
		&mcp.ServerOptions{
			InitializedHandler: newInitializedHandler(),
		},
	)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(
		&mcp.Implementation{Name: "claude-code", Version: "test"},
		nil,
	)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	// Wait for InitializedHandler to fire (the SDK fires it after the
	// client's notifications/initialized notification).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	rt, mode := sessionRuntimeFor(ss)
	assert.Equal(t, "claude-code", rt)
	// The SDK's Client.Connect does not populate InitializeParams.Meta;
	// the bridge sets _meta["locutus.mode"] in production. The handler
	// falls back to "interactive" when _meta is absent.
	assert.Equal(t, "interactive", mode)
}
