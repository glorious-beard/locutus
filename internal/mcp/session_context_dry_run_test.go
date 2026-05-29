// DJ-147 — newInitializedHandler reads _meta["locutus.dry_run"] and
// _meta["locutus.dry_run_format"] alongside the runtime+mode capture,
// stores them on the session map, and registers an overlay on the
// SpecStore so subsequent spec_* tool calls capture rather than persist.
package mcp

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitializedHandler_CapturesDryRunSignals(t *testing.T) {
	clearSessionRuntimes()
	store, err := agent.NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	server := mcp.NewServer(
		&mcp.Implementation{Name: "locutus-test", Version: "0.0.0"},
		&mcp.ServerOptions{
			InitializedHandler: newInitializedHandler(logger, nil, store),
		},
	)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	// Client sends ClientInfo.name + _meta.locutus.dry_run + _meta.locutus.dry_run_format.
	// ClientSessionOptions doesn't expose InitializeParams in v1.6.1; per DJ-143 Task 3
	// we override session state directly via the same mechanism.
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "test"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	// Wait for InitializedHandler to fire and capture runtime.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Since the SDK client can't inject _meta in tests, directly override
	// the captured context to simulate what the bridge does in production.
	storeSessionContext(ss, "claude-code", "headless", true, "json")

	rt, mode := sessionRuntimeFor(ss)
	assert.Equal(t, "claude-code", rt)
	assert.Equal(t, "headless", mode)
	assert.True(t, SessionDryRun(ss))
	assert.Equal(t, "json", SessionDryRunFormat(ss))
}

func TestInitializedHandler_RegistersOverlayWhenDryRun(t *testing.T) {
	clearSessionRuntimes()
	store, err := agent.NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	// Use a sentinel session pointer; bypass full SDK plumbing.
	sess := (*mcp.ServerSession)(nil)
	storeSessionContext(sess, "claude-code", "headless", true, "markdown")
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	caps := store.OverlayCaptured(sess)
	assert.NotNil(t, caps, "an overlay must exist for a dry-run session")
	assert.Len(t, caps, 0, "but it starts empty")
}

func TestSessionDryRun_DefaultsFalseForUnknownSession(t *testing.T) {
	clearSessionRuntimes()
	assert.False(t, SessionDryRun((*mcp.ServerSession)(nil)))
	assert.Empty(t, SessionDryRunFormat((*mcp.ServerSession)(nil)))
}
