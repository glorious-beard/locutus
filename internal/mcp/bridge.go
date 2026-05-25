package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// BridgeStdioToSocket dials the daemon's Unix socket and pumps bytes
// in both directions between the calling process's stdin/stdout and
// the socket. To external MCP clients (Claude Code, Codex, Gemini
// CLI) the calling `locutus mcp` invocation looks like a stdio MCP
// server — under the hood it's a thin netcat-style bridge that gives
// every client process its own apparent server while all of them
// actually share one daemon-side MCP session.
//
// The bridge does no JSON-RPC framing or interpretation — the SDK on
// both sides already handles newline-delimited JSON-RPC over the
// byte stream. The bridge only moves bytes.
//
// Returns when:
//   - ctx is cancelled (caller's signal handler fires) — caller's
//     stdin io.Copy completes and Close on the conn unblocks the
//     daemon-side Copy.
//   - The daemon closes the connection — the socket-side Copy
//     completes, the bridge returns.
//   - The caller's stdin closes (parent MCP client exited) — the
//     stdin-side Copy completes; the bridge closes the conn and
//     returns.
func BridgeStdioToSocket(ctx context.Context, sockPath string) error {
	return BridgeIOToSocket(ctx, sockPath, os.Stdin, os.Stdout)
}

// BridgeIOToSocket is BridgeStdioToSocket with explicit reader/writer
// arguments. Lets tests drive the bridge from in-memory pipes
// without touching os.Stdin/Stdout.
//
// Flow:
//  1. Dial the socket.
//  2. Start two copies in parallel: stdin → socket and socket → stdout.
//  3. When stdin closes (client done sending), half-close the socket's
//     write side. The server's read returns EOF, finishes any in-flight
//     responses, and closes its end.
//  4. The bridge returns when the server-side close lands (socket →
//     stdout copy returns) OR when ctx is cancelled.
//
// Critically, we do NOT close the full conn just because stdin EOF'd.
// The server may still be writing its response. Only the socket-side
// completion or ctx cancel triggers full conn close.
func BridgeIOToSocket(ctx context.Context, sockPath string, in io.Reader, out io.Writer) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sockPath)
	if err != nil {
		return fmt.Errorf("mcp: dial %s: %w", sockPath, err)
	}

	// Full-close hook for ctx cancellation. The cleanup channel
	// prevents this goroutine from leaking past the function's
	// natural return.
	stopCancel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopCancel:
		}
	}()
	defer close(stopCancel)

	stdinDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, in)
		// Stdin closed: half-close the write side so the daemon sees
		// EOF on its read but can still write outstanding responses
		// back through the still-open read side of this conn.
		if uc, ok := conn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
		stdinDone <- err
	}()

	// socket → stdout in the foreground. Returns when the server
	// closes its end (clean shutdown after responding to half-close)
	// or when the ctx-cancel goroutine forces a conn close.
	_, copyErr := io.Copy(out, conn)
	_ = conn.Close()

	// Don't synchronously wait for the stdin goroutine — we can't
	// close the caller's reader to unblock it, so on the ctx-cancel
	// path it may stay blocked until the caller closes their pipe.
	// For os.Stdin in the daemon-bridge case the goroutine leaks
	// until process exit, which is fine because the bridge IS the
	// process's main routine. Tests close their pipe explicitly.
	select {
	case <-stdinDone:
	case <-ctx.Done():
		// Best-effort: leak the stdin goroutine; caller's pipe
		// closure (via defer or process exit) will eventually unblock it.
	case <-time.After(100 * time.Millisecond):
		// stdin goroutine didn't finish quickly; assume it's blocked
		// on a still-open reader and let it leak.
	}

	if copyErr != nil && !errors.Is(copyErr, io.EOF) && !errors.Is(copyErr, net.ErrClosed) {
		return fmt.Errorf("mcp: bridge: %w", copyErr)
	}
	return nil
}
