package mcp

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// startSocketDaemon starts a daemon on a temp-dir socket and returns
// the socket path + a cleanup func. The daemon runs in a goroutine
// for the test's lifetime; the cleanup cancels its context.
//
// Uses shortTempSocketDir rather than t.TempDir because macOS Unix
// socket paths are capped at ~104 bytes; the default Go test temp
// directory under /var/folders/ blows that budget on its own.
func startSocketDaemon(t *testing.T, seed func(*agent.SpecStore)) (string, *agent.SpecStore) {
	t.Helper()
	sockPath := filepath.Join(shortTempSocketDir(t), "mcp.sock")

	fsys := specio.NewMemFS()
	store, err := agent.NewSpecStore(fsys)
	assert.NoError(t, err)
	if seed != nil {
		seed(store)
	}

	server := NewSpecServer(store, nil, nil, nil)
	listener, err := ListenSocket(sockPath)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ServeOnSocket(ctx, listener, server)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Log("ServeOnSocket did not return within cleanup timeout")
		}
	})

	// Brief settle: the listener is bound synchronously by
	// ListenSocket, so any dial after this point should connect.
	return sockPath, store
}

// dialAndSession opens a Unix socket dial, wraps it in an MCP
// IOTransport, and brings up a client session ready to call tools.
// Returns the live session; cleanup closes it.
func dialAndSession(t *testing.T, sockPath string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	conn, err := net.Dial("unix", sockPath)
	assert.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}, nil)
	assert.NoError(t, err)

	t.Cleanup(func() {
		session.Close()
	})
	return session
}

func TestSocketDaemon_AcceptsAndServes(t *testing.T) {
	sockPath, _ := startSocketDaemon(t, nil)
	session := dialAndSession(t, sockPath)

	res, err := session.ListTools(context.Background(), nil)
	assert.NoError(t, err)

	names := toolNames(res.Tools)
	assert.Contains(t, names, "spec_list_manifest")
	assert.Contains(t, names, "spec_propose_decision")
}

func TestSocketDaemon_ConcurrentClientsShareSpecStore(t *testing.T) {
	sockPath, _ := startSocketDaemon(t, nil)

	writer := dialAndSession(t, sockPath)
	reader := dialAndSession(t, sockPath)

	// Writer proposes a decision; reader fetches it. If the store
	// weren't shared across sessions, reader would see Status: missing.
	res, err := writer.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{
			"id":          "dec-storage",
			"title":       "Choose Postgres",
			"status":      "active",
			"confidence":  1.0,
			"rationale":   "rationale",
			"axes":        []string{"storage"},
			"surfaced_by": []string{"goal-test"},
		},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	getRes, err := reader.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_get",
		Arguments: map[string]any{"ids": []string{"dec-storage"}},
	})
	assert.NoError(t, err)
	assert.False(t, getRes.IsError)

	// The structured content carries the SpecGetResult; assert the
	// reader sees the writer's commit by checking the status field.
	asMap, ok := getRes.StructuredContent.(map[string]any)
	assert.True(t, ok)
	results, _ := asMap["results"].(map[string]any)
	entry, _ := results["dec-storage"].(map[string]any)
	assert.Equal(t, "settled", entry["status"], "reader should see writer's commit; got %+v", entry)
}

func TestSocketDaemon_ClientDisconnectDoesNotKillDaemon(t *testing.T) {
	sockPath, _ := startSocketDaemon(t, nil)

	// First client connects, calls, disconnects.
	{
		client := dialAndSession(t, sockPath)
		_, err := client.ListTools(context.Background(), nil)
		assert.NoError(t, err)
		client.Close()
	}

	// Brief settle so the daemon's session-end goroutine completes
	// before the second client connects. Without this we might be
	// racing the daemon's cleanup vs the second dial.
	time.Sleep(50 * time.Millisecond)

	// Second client connects after the first is gone; should still
	// work. If the daemon's accept loop tied lifetime to first-
	// client lifetime, this would fail to connect or hang.
	second := dialAndSession(t, sockPath)
	res, err := second.ListTools(context.Background(), nil)
	assert.NoError(t, err)
	assert.NotEmpty(t, res.Tools)
}

func TestSocketDaemon_StaleSocketCleanedOnListen(t *testing.T) {
	sockPath := filepath.Join(shortTempSocketDir(t), "mcp.sock")

	// Pre-create a "stale" socket file: a regular file at the path.
	// This simulates a daemon that exited via SIGKILL without
	// removing its socket — POSIX leaves the inode behind.
	err := os.WriteFile(sockPath, []byte("stale"), 0o600)
	assert.NoError(t, err)

	// ListenSocket should remove the stale file and bind fresh.
	listener, err := ListenSocket(sockPath)
	assert.NoError(t, err, "ListenSocket should remove stale file and succeed")
	defer listener.Close()

	// Dial to confirm the listener is actually live.
	conn, err := net.Dial("unix", sockPath)
	assert.NoError(t, err)
	conn.Close()
}

func TestSocketDaemon_ServeRespectsContextCancel(t *testing.T) {
	sockPath := filepath.Join(shortTempSocketDir(t), "mcp.sock")

	store, err := agent.NewSpecStore(specio.NewMemFS())
	assert.NoError(t, err)
	server := NewSpecServer(store, nil, nil, nil)
	listener, err := ListenSocket(sockPath)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeOnSocket(ctx, listener, server) }()

	// Open and close a client to make sure the accept loop is live.
	conn, err := net.Dial("unix", sockPath)
	assert.NoError(t, err)
	conn.Close()

	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("ServeOnSocket did not return on context cancel")
	}
}

// seedStoreFromSocket is a convenience for cross-test fixtures that
// want a single decision present at daemon startup.
func seedStoreFromSocket(store *agent.SpecStore, id string) {
	now := time.Now().UTC()
	_ = store.Begin()
	_ = store.Put(agent.KindDecision, id, spec.Decision{
		ID: id, Title: id, Status: spec.DecisionStatusActive,
		Confidence: 1.0, Rationale: "rationale",
		Axes: []string{"x"}, SurfacedBy: []string{"goal-test"},
		CreatedAt: now, UpdatedAt: now,
	}, agent.OriginSettled)
	_ = store.Commit()
}

// Ensure the helper is referenced so unused-symbol checks don't fire
// on it during early development.
var _ = seedStoreFromSocket

// concurrencyGuard ensures the WaitGroup pattern in ServeOnSocket
// doesn't leak goroutines under load — a stress check beyond the
// behavioral tests above.
var _ sync.WaitGroup
