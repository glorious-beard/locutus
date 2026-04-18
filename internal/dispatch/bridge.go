package dispatch

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PermRequest is the wire-format request the bridge subprocess sends to the
// supervisor when Claude Code invokes the locutus_permission tool. ID
// matches Claude's tool_use_id so responses route back to the correct call.
type PermRequest struct {
	ID    string         `json:"id"`
	Tool  string         `json:"tool"`
	Input map[string]any `json:"input,omitempty"`
}

// PermDecision is the supervisor's reply, forwarded by the bridge back to
// Claude as an MCP tool_result. Behavior is "allow" or "deny". Message
// optionally explains a denial — Claude surfaces it back to the agent.
type PermDecision struct {
	ID       string `json:"id"`
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

// PermBridge is the supervisor-side endpoint of the permission bridge. It
// owns a Unix-domain socket at SocketPath, reads PermRequest messages from
// connected bridge subprocesses, and surfaces them on Events as
// EventPermissionRequest. The supervisor responds via Respond, which
// delivers a PermDecision back to the originating socket.
//
// Lifecycle: NewPermBridge starts the listener; Close stops the listener,
// closes the events channel, and removes the socket file.
type PermBridge struct {
	SocketPath string
	Events     <-chan AgentEvent

	events   chan AgentEvent
	listener net.Listener
	pending  sync.Map // interactionID string -> chan PermDecision

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewPermBridge creates a new bridge with a freshly-generated socket path
// under os.TempDir(). The socket is chmod'd to 0600 so only the owning
// user can connect.
func NewPermBridge() (*PermBridge, error) {
	id, err := randomHex(8)
	if err != nil {
		return nil, fmt.Errorf("generate socket id: %w", err)
	}
	path := filepath.Join(os.TempDir(), "locutus-perm-"+id+".sock")
	_ = os.Remove(path) // best-effort: clean up a stale socket from a prior crash

	lis, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = lis.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	events := make(chan AgentEvent, 8)
	b := &PermBridge{
		SocketPath: path,
		Events:     events,
		events:     events,
		listener:   lis,
		done:       make(chan struct{}),
	}
	b.wg.Add(1)
	go b.acceptLoop()
	return b, nil
}

// Close stops the listener, waits for in-flight handlers to exit, and
// removes the socket file. Safe to call multiple times.
func (b *PermBridge) Close() error {
	var firstErr error
	b.closeOnce.Do(func() {
		close(b.done)
		if err := b.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			firstErr = err
		}
		b.wg.Wait()
		close(b.events)
		if err := os.Remove(b.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	})
	return firstErr
}

// Respond delivers a decision to the pending request with the matching
// interaction ID. Returns an error if no request with that ID is
// awaiting — this catches cases where the event was dropped or the
// supervisor is replying to a stale ID.
func (b *PermBridge) Respond(interactionID string, decision PermDecision) error {
	v, ok := b.pending.LoadAndDelete(interactionID)
	if !ok {
		return fmt.Errorf("no pending permission request with id %q", interactionID)
	}
	respChan := v.(chan PermDecision)
	decision.ID = interactionID
	// Use a short timeout so a lost reader doesn't hang the supervisor.
	select {
	case respChan <- decision:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timed out delivering decision for %q (reader gone?)", interactionID)
	}
}

func (b *PermBridge) acceptLoop() {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Warn("perm bridge: accept error", "err", err)
			return
		}
		b.wg.Add(1)
		go b.handleConn(conn)
	}
}

func (b *PermBridge) handleConn(conn net.Conn) {
	defer b.wg.Done()
	defer func() { _ = conn.Close() }()

	scanner := bufio.NewScanner(conn)
	// Permission tool calls include the full tool input; bump the buffer
	// so large bash commands or edit payloads aren't truncated.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		var req PermRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			slog.Warn("perm bridge: malformed request", "err", err, "line", string(scanner.Bytes()))
			continue
		}
		if req.ID == "" {
			slog.Warn("perm bridge: request missing id", "tool", req.Tool)
			continue
		}

		respChan := make(chan PermDecision, 1)
		b.pending.Store(req.ID, respChan)

		// Emit the event. Under normal load this is a buffered channel; if
		// it ever blocks, we still want Close() to be able to tear us down,
		// so select on done.
		select {
		case b.events <- AgentEvent{
			Kind:          EventPermissionRequest,
			ToolName:      req.Tool,
			ToolInput:     req.Input,
			InteractionID: req.ID,
			Timestamp:     time.Now().UTC(),
		}:
		case <-b.done:
			b.pending.Delete(req.ID)
			return
		}

		// Wait for the supervisor's decision.
		var decision PermDecision
		select {
		case decision = <-respChan:
		case <-b.done:
			b.pending.Delete(req.ID)
			return
		}

		out, err := json.Marshal(decision)
		if err != nil {
			slog.Warn("perm bridge: marshal decision", "err", err, "id", req.ID)
			continue
		}
		out = append(out, '\n')
		if _, err := conn.Write(out); err != nil {
			slog.Warn("perm bridge: write decision", "err", err, "id", req.ID)
			return
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, net.ErrClosed) {
		slog.Warn("perm bridge: scanner error", "err", err)
	}
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
