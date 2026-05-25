package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SocketSubpath is the per-project socket location under the project
// root. The full path is <project-root>/<SocketSubpath>. Kept as a
// constant so the daemon, the bridge, and the bootstrap discovery
// helper agree on where to look.
const SocketSubpath = ".locutus/mcp.sock"

// SocketPath returns the absolute socket path for a project root.
// Callers pass the result to ListenSocket (daemon side) or to
// net.Dial (bridge / client side).
func SocketPath(projectRoot string) string {
	return filepath.Join(projectRoot, SocketSubpath)
}

// ListenSocket opens a Unix socket listener at path, removing any
// stale socket file left behind by a prior daemon that exited
// without cleanup. POSIX doesn't auto-remove socket files on process
// exit (let alone SIGKILL), so the stale-cleanup step is unconditional:
// stat the path, remove if present, then bind. If a live daemon is
// already listening on the path the second Listen will fail with
// EADDRINUSE — the caller should treat that as "someone else owns
// the socket" and fall back to dialing.
//
// The parent directory is created with 0o700 so .locutus/ stays
// project-private when first written.
func ListenSocket(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mcp: mkdir %s: %w", dir, err)
	}

	// Remove any stale socket file. Ignore "not exist" — that's the
	// happy path. Surface other errors (permission denied on a file
	// we can't read, etc.) so the operator sees them.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mcp: remove stale socket %s: %w", path, err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("mcp: listen %s: %w", path, err)
	}
	// Restrict to the project owner. Default umask leaves world-writable
	// sockets in some shells.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("mcp: chmod %s: %w", path, err)
	}
	return listener, nil
}

// ServeOnSocket runs the accept loop: for each accepted connection,
// hand it to *mcp.Server.Connect with an IOTransport wrapping the
// conn. Each accepted client gets its own MCP session; the server
// instance is shared so SpecStore writes through one session are
// immediately visible to reads on another.
//
// ServeOnSocket blocks until either the listener returns a permanent
// error (context cancellation closes the listener, surfacing
// net.ErrClosed) or the context is cancelled. Per-session errors
// are logged but do not unwind the loop — one misbehaving client
// shouldn't take down the daemon.
//
// Shutdown: when ctx is cancelled, the listener is closed AND every
// in-flight conn is closed. The SDK's ServerSession.Wait returns
// only when the client closes the connection, so closing the conn
// from this side is the only way to unwind a session that has no
// active client traffic. Without this, a graceful daemon shutdown
// would hang waiting on idle sessions.
func ServeOnSocket(ctx context.Context, listener net.Listener, server *mcp.Server) error {
	var (
		mu      sync.Mutex
		conns   = map[net.Conn]struct{}{}
		closing bool
	)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
		mu.Lock()
		closing = true
		for c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				wg.Wait()
				return ctx.Err()
			}
			slog.Warn("mcp: accept error", "error", err)
			continue
		}

		// Race guard: if cancellation fired between our Accept return
		// and acquiring the mutex, close the just-accepted conn and
		// skip session setup. Without this we'd register a conn the
		// shutdown goroutine already finished iterating over and leak
		// the session.
		mu.Lock()
		if closing {
			mu.Unlock()
			_ = conn.Close()
			continue
		}
		conns[conn] = struct{}{}
		mu.Unlock()

		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			defer func() {
				_ = c.Close()
				mu.Lock()
				delete(conns, c)
				mu.Unlock()
			}()
			session, err := server.Connect(ctx, &mcp.IOTransport{Reader: c, Writer: c}, nil)
			if err != nil {
				slog.Warn("mcp: session connect failed", "error", err)
				return
			}
			if err := session.Wait(); err != nil && !errors.Is(err, context.Canceled) {
				slog.Debug("mcp: session ended", "error", err)
			}
		}(conn)
	}
}
