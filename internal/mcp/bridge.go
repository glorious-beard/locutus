package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
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
// mode is injected into the client's initialize request as
// _meta["locutus.mode"] so the daemon's session-context module can
// gate tool access per DJ-143. Callers should pass the resolved mode
// ("interactive" or "headless"); "" is accepted and results in no
// injection (the field is set to the empty string).
//
// dryRun + dryRunFormat are injected per DJ-147 when dryRun is true.
// When dryRun is false the keys are omitted entirely so the wire trace
// stays clean on non-dry-run sessions. When dryRun is true and
// dryRunFormat is empty, only locutus.dry_run is set and the daemon
// falls back to its default format.
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
func BridgeStdioToSocket(ctx context.Context, sockPath, mode string, dryRun bool, dryRunFormat string) error {
	return BridgeIOToSocket(ctx, sockPath, os.Stdin, os.Stdout, mode, dryRun, dryRunFormat)
}

// BridgeIOToSocket is BridgeStdioToSocket with explicit reader/writer
// arguments. Lets tests drive the bridge from in-memory pipes
// without touching os.Stdin/Stdout.
//
// Flow:
//  1. Dial the socket.
//  2. Start two copies in parallel: stdin → socket and socket → stdout.
//     The stdin side parses incoming JSON-RPC lines until the first
//     initialize request is seen, rewrites it to inject
//     _meta["locutus.mode"]=mode, then drops to raw byte pumping.
//  3. When stdin closes (client done sending), half-close the socket's
//     write side. The server's read returns EOF, finishes any in-flight
//     responses, and closes its end.
//  4. The bridge returns when the server-side close lands (socket →
//     stdout copy returns) OR when ctx is cancelled.
//
// Critically, we do NOT close the full conn just because stdin EOF'd.
// The server may still be writing its response. Only the socket-side
// completion or ctx cancel triggers full conn close.
func BridgeIOToSocket(ctx context.Context, sockPath string, in io.Reader, out io.Writer, mode string, dryRun bool, dryRunFormat string) error {
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
		err := pumpInWithInitInject(in, conn, mode, dryRun, dryRunFormat)
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

// pumpInWithInitInject scans incoming JSON-RPC lines on in, rewrites
// the first initialize request to include the configured _meta fields
// (locutus.mode per DJ-143, locutus.dry_run + locutus.dry_run_format
// per DJ-147), and then drops to raw io.Copy for the rest of the
// stream.
//
// Non-initialize lines that appear before the initialize are passed
// through verbatim (e.g. pre-init notifications). After the first
// initialize is forwarded, no further parsing occurs — only one
// initialize per MCP session.
func pumpInWithInitInject(in io.Reader, conn io.Writer, mode string, dryRun bool, dryRunFormat string) error {
	br := bufio.NewReader(in)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			rewritten, isInit := maybeInjectInitMeta(line, mode, dryRun, dryRunFormat)
			if _, werr := conn.Write(rewritten); werr != nil {
				return werr
			}
			if isInit {
				_, copyErr := io.Copy(conn, br)
				return copyErr
			}
		}
		if err != nil {
			return err
		}
	}
}

// maybeInjectInitMeta tries to parse line as a JSON-RPC initialize
// request and inject params._meta with the configured Locutus fields:
//   - locutus.mode = mode (DJ-143)
//   - locutus.dry_run = true (DJ-147, only when dryRun is true)
//   - locutus.dry_run_format = dryRunFormat (DJ-147, only when dryRun
//     is true AND dryRunFormat is non-empty)
//
// For dryRun == false the dry-run keys are omitted entirely so the
// wire trace stays clean on non-dry-run sessions. Returns the
// (possibly rewritten) line bytes and a boolean indicating whether an
// initialize was detected and rewritten. Non-JSON or non-initialize
// lines are returned unchanged with isInit=false.
func maybeInjectInitMeta(line []byte, mode string, dryRun bool, dryRunFormat string) ([]byte, bool) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return line, false
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return line, false
	}
	methodRaw, ok := msg["method"]
	if !ok {
		return line, false
	}
	var method string
	if err := json.Unmarshal(methodRaw, &method); err != nil || method != "initialize" {
		return line, false
	}
	var params map[string]any
	if rm, ok := msg["params"]; ok && len(rm) > 0 {
		_ = json.Unmarshal(rm, &params)
	}
	if params == nil {
		params = map[string]any{}
	}
	meta, _ := params["_meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["locutus.mode"] = mode
	if dryRun {
		meta["locutus.dry_run"] = true
		if dryRunFormat != "" {
			meta["locutus.dry_run_format"] = dryRunFormat
		}
	}
	params["_meta"] = meta
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return line, false
	}
	msg["params"] = paramsBytes
	out, err := json.Marshal(msg)
	if err != nil {
		return line, false
	}
	return append(out, '\n'), true
}
