package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ensure bytes is referenced even when only the alternative test
// shape uses bytes.Buffer indirectly via syncBuffer.
var _ = bytes.NewReader

// TestBridge_StdioToSocket_RoundTrip wires a bridge instance against
// a real socket daemon, sends an MCP initialize request through the
// bridge's stdin, and asserts that the initialize response arrives
// on the bridge's stdout. This is the end-to-end proof that an
// external MCP client could use `locutus mcp` as its server command
// without knowing a socket is underneath.
//
// Uses an io.Pipe rather than bytes.Reader so the bridge's stdin
// stays open after the initialize bytes are written — closing stdin
// would half-close the socket's write side, and we want to observe
// the response on the still-open read side independently of how the
// bridge handles half-close.
func TestBridge_StdioToSocket_RoundTrip(t *testing.T) {
	sockPath, _ := startSocketDaemon(t, nil)

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "bridge-test",
				"version": "test",
			},
		},
	}
	initBytes, err := json.Marshal(initReq)
	assert.NoError(t, err)

	inR, inW := io.Pipe()
	var out syncBuffer
	defer inR.Close()
	defer inW.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bridgeDone := make(chan error, 1)
	go func() {
		bridgeDone <- BridgeIOToSocket(ctx, sockPath, inR, &out, "interactive")
	}()

	// Write the initialize bytes; keep the pipe open after so the
	// bridge doesn't half-close the socket.
	go func() {
		_, _ = inW.Write(append(initBytes, '\n'))
	}()

	// Poll stdout for the response. The SDK's JSON marshaling has no
	// spaces by default, but allow both "id":1 and "id": 1 forms.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), `"id":1`) || strings.Contains(out.String(), `"id": 1`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := out.String()
	t.Logf("bridge stdout (len=%d): %q", len(got), got)
	assert.Contains(t, got, `"id"`, "expected initialize response on bridge stdout")
	assert.Contains(t, got, `"jsonrpc"`)

	cancel()
	select {
	case <-bridgeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not unwind on context cancel")
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer wrapper. The bridge's
// io.Copy(out, conn) runs on a goroutine and the test reads out
// from the main goroutine, so a plain bytes.Buffer would race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// DJ-143 — the bridge parses the client's first initialize JSON-RPC
// request, injects _meta["locutus.mode"] using the configured mode,
// and then drops to raw byte pumping for the rest of the session.

func TestBridgeInjectsLocutusModeIntoInitialize(t *testing.T) {
	// Mock server: listen on a tmp socket, accept one connection, read
	// one JSON-RPC line, decode it, capture _meta["locutus.mode"].
	// Uses shortTempSocketDir (rooted at /tmp) because macOS Unix
	// socket paths are capped at ~104 bytes; t.TempDir() paths blow
	// that budget.
	sock := filepath.Join(shortTempSocketDir(t), "mcp.sock")
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	captured := make(chan map[string]any, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		var msg map[string]any
		_ = json.Unmarshal(line, &msg)
		params, _ := msg["params"].(map[string]any)
		meta, _ := params["_meta"].(map[string]any)
		captured <- meta
	}()

	// Drive the bridge with an initialize request on its "stdin".
	in, inW := io.Pipe()
	out, outW := io.Pipe()
	go func() {
		initLine := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"claude-code","version":"test"}}}` + "\n"
		_, _ = inW.Write([]byte(initLine))
		_ = inW.Close()
	}()
	go func() { _, _ = io.Copy(io.Discard, out) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = BridgeIOToSocket(ctx, sock, in, outW, "headless")

	select {
	case meta := <-captured:
		require.NotNil(t, meta, "_meta must be present on initialize")
		assert.Equal(t, "headless", meta["locutus.mode"])
	case <-time.After(2 * time.Second):
		t.Fatal("server never received an initialize")
	}
}

func TestBridgePassesThroughNonInitializeLinesUnchanged(t *testing.T) {
	// See TestBridgeInjectsLocutusModeIntoInitialize for why we use
	// shortTempSocketDir rather than t.TempDir.
	sock := filepath.Join(shortTempSocketDir(t), "mcp.sock")
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	captured := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		captured <- line
	}()

	in, inW := io.Pipe()
	out, outW := io.Pipe()
	go func() {
		// A non-initialize first line (e.g. a notification) must pass
		// through verbatim. The bridge only rewrites the first
		// initialize it sees.
		nonInit := `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}` + "\n"
		_, _ = inW.Write([]byte(nonInit))
		_ = inW.Close()
	}()
	go func() { _, _ = io.Copy(io.Discard, out) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = BridgeIOToSocket(ctx, sock, in, outW, "headless")

	select {
	case got := <-captured:
		assert.Contains(t, string(got), "notifications/cancelled")
		assert.NotContains(t, string(got), "locutus.mode") // untouched
	case <-time.After(2 * time.Second):
		t.Fatal("server never received the passthrough line")
	}
}

// Compile-time confirmation we used io for the EOF filter check.
var _ = io.EOF
