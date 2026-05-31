// DJ-143 — the three spec_loop_* tools are restricted to Codex and
// Gemini sessions. A Claude Code session calling any of them must
// receive a runtime-restriction error naming the runtime, the mode,
// the tool, and the docs reference. A Codex (or Gemini) session calls
// must pass through to the loop store unchanged.
package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecLoopBeginDeniesClaudeCodeSession(t *testing.T) {
	clearSessionRuntimes()
	_, cs := startServerWithClient(t, "claude-code", "interactive")

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_loop_begin",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "claude-code must be denied")
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "spec_loop_begin")
	assert.Contains(t, tc.Text, "claude-code")
	assert.Contains(t, strings.ToLower(tc.Text), "interactive")
	assert.Contains(t, tc.Text, "docs/runtime-affordances.md")
}

func TestSpecLoopBeginAllowsCodexSession(t *testing.T) {
	clearSessionRuntimes()
	_, cs := startServerWithClient(t, "codex", "interactive")

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_loop_begin",
		Arguments: map[string]any{"activity": "spec_refinement", "target": "goals"},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "codex must succeed")
}

func TestSpecLoopAllToolsDeniedForClaudeCode(t *testing.T) {
	for _, tool := range []string{"spec_loop_begin", "spec_loop_status", "spec_advance_iteration"} {
		t.Run(tool, func(t *testing.T) {
			clearSessionRuntimes()
			_, cs := startServerWithClient(t, "claude-code", "interactive")
			args := map[string]any{"activity": "spec_refinement", "target": "goals"}
			if tool == "spec_advance_iteration" {
				args["converged"] = false
			}
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name: tool, Arguments: args,
			})
			require.NoError(t, err)
			assert.True(t, res.IsError, "%s must deny claude-code", tool)
		})
	}
}

// startServerWithClient wires a fresh NewSpecServer + ClientSession on
// in-memory transports. The SDK initialize handshake will set the
// session's runtime via ClientInfo.name to the runtime parameter; we
// then override the mode by calling storeSessionRuntime directly,
// since the SDK client cannot inject _meta (that's the bridge's job
// in production — Task 4).
func startServerWithClient(t *testing.T, runtime, mode string) (*mcp.ServerSession, *mcp.ClientSession) {
	t.Helper()
	store, err := agent.NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)
	server := NewSpecServer(store, nil, nil, nil, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: runtime, Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	// Wait for InitializedHandler to fire and capture runtime from ClientInfo.
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if rt, _ := sessionRuntimeFor(ss); rt == runtime {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rt, _ := sessionRuntimeFor(ss); rt != runtime {
		t.Fatalf("server never captured runtime=%q for session", runtime)
	}

	// Override mode (the SDK client cannot inject _meta in test).
	storeSessionRuntime(ss, runtime, mode)
	return ss, cs
}
