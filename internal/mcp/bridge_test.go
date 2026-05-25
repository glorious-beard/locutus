package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
		bridgeDone <- BridgeIOToSocket(ctx, sockPath, inR, &out)
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

// Compile-time confirmation we used io for the EOF filter check.
var _ = io.EOF
