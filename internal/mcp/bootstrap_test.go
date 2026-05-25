package mcp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBootstrap_DiscoversLiveDaemon(t *testing.T) {
	// Stand up a daemon at the canonical project location, then call
	// EnsureDaemon with the same project root. It should probe the
	// socket, find it responsive, and return without forking.
	projectRoot := shortTempSocketDir(t)
	// The daemon's listener path must equal SocketPath(projectRoot)
	// for EnsureDaemon to find it. Substitute the test-temp socket
	// into that location by stashing it directly.
	sockPath := SocketPath(projectRoot)

	// startSocketDaemon uses its own temp dir; we need it to bind
	// the project's expected path. Build the daemon machinery
	// inline here so we control the socket path.
	store := bootstrapTestStore(t)
	listener, err := ListenSocket(sockPath)
	assert.NoError(t, err)
	server := NewSpecServer(store, nil, nil)
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
		}
	})

	got, err := EnsureDaemon(context.Background(), projectRoot, "/nonexistent/binary")
	assert.NoError(t, err, "EnsureDaemon should not fork (binary path is bogus) when daemon already responds")
	assert.Equal(t, sockPath, got)
}

func TestBootstrap_ForkFailsWithBogusBinary(t *testing.T) {
	// No daemon running, fork target is a path that doesn't exist —
	// EnsureDaemon should surface the fork error.
	projectRoot := shortTempSocketDir(t)
	// Ensure the canonical socket path is absent.
	_ = removeIfExists(filepath.Join(projectRoot, ".locutus", "mcp.sock"))

	_, err := EnsureDaemon(context.Background(), projectRoot, "/definitely/not/a/real/binary/path")
	assert.Error(t, err, "EnsureDaemon should fail when fork target doesn't exist")
}

func TestStopDaemon_RemovesSocket(t *testing.T) {
	// Live daemon → StopDaemon removes the socket file. We don't
	// signal a PID in Phase 1 (see the TODO in bootstrap.go), so
	// the test only asserts the socket-file removal contract.
	projectRoot := shortTempSocketDir(t)
	sockPath := SocketPath(projectRoot)
	store := bootstrapTestStore(t)
	listener, err := ListenSocket(sockPath)
	assert.NoError(t, err)
	server := NewSpecServer(store, nil, nil)
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
		}
	})

	assert.True(t, probeDaemon(sockPath), "daemon should be probable before stop")
	err = StopDaemon(projectRoot)
	assert.NoError(t, err)
	assert.False(t, fileExists(sockPath), "socket file should be removed by StopDaemon")
}

func TestStopDaemon_NoDaemonIsNoOp(t *testing.T) {
	projectRoot := shortTempSocketDir(t)
	err := StopDaemon(projectRoot)
	assert.NoError(t, err, "StopDaemon on a non-running daemon should be a no-op")
}
