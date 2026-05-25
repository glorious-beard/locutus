// Package acp wraps github.com/coder/acp-go-sdk for use as Locutus's
// coding-agent dispatcher per DJ-119. The Connection type is the only entry
// point the supervisor talks to; everything else (the acp.Client
// implementation, event translation, future archive) is package-internal.
//
// Phase 1 scope (per DJ-119): the foundation — spawn + initialize +
// session/new + prompt-with-streaming-events + close. Deliberately omitted
// in Phase 1: real supervisor wiring (Phase 3), the production Policy
// implementation (Phase 4), W3C trace-context propagation in _meta
// (Phase 5), JSON-RPC frame archive (a Phase 1 follow-up; the test harness
// is also a debugging path).
package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/glorious-beard/locutus/internal/dispatch"
	"github.com/glorious-beard/locutus/internal/dispatch/policy"
	acpsdk "github.com/coder/acp-go-sdk"
)

// Spawn describes how to launch an ACP-server subprocess. The supervisor
// builds one of these per workstream by looking up the agent name in a
// registry; the actual command per agent is:
//
//	"claude-code" → {Cmd: "claude-agent-acp"}
//	"codex"       → {Cmd: "codex-acp"}
//	"gemini"      → {Cmd: "gemini", Args: []string{"--acp"}}
//
// Env defaults to os.Environ() when nil; pass an explicit slice to override
// (e.g. injecting subscription auth env vars per-attempt).
type Spawn struct {
	Cmd  string
	Args []string
	Env  []string
}

// Connection is one running ACP-server subprocess plus the client-side
// state required to drive it: capability cache, per-session routing for
// inbound notifications + permission requests, and the SDK connection
// handle for outbound requests.
//
// A Connection is single-threaded with respect to NewSession / Prompt /
// Cancel / Close — the supervisor's existing dispatch model is one
// connection per workstream, so concurrent calls on one Connection aren't
// in scope for Phase 1. Within one session, multiple sequential Prompt
// calls are explicitly supported (the long-lived-session lifecycle that
// Phase 0 verified across all three target agents).
type Connection struct {
	conn       *acpsdk.ClientSideConnection
	client     *client
	capsCache  acpsdk.AgentCapabilities
	spawnedCmd *exec.Cmd // nil when constructed via openWithIO (tests)
}

// Open launches the ACP-server subprocess described by spawn, performs the
// ACP initialize handshake, and returns a Connection ready to create
// sessions. The caller owns the returned Connection and MUST call Close;
// failing to do so leaks a subprocess.
//
// archiveDir is reserved for a future JSON-RPC frame archive (Phase 1
// follow-up). Pass "" to disable; today it's accepted and ignored.
func Open(ctx context.Context, spawn Spawn, archiveDir string) (*Connection, error) {
	if spawn.Cmd == "" {
		return nil, errors.New("acp.Open: empty Spawn.Cmd")
	}
	cmd := exec.CommandContext(ctx, spawn.Cmd, spawn.Args...)
	if spawn.Env != nil {
		cmd.Env = spawn.Env
	}
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp.Open: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp.Open: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp.Open: start %s: %w", spawn.Cmd, err)
	}

	c, err := openWithIO(ctx, stdin, stdout, archiveDir)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	c.spawnedCmd = cmd
	return c, nil
}

// openWithIO is the spawn-free entry point used by tests. It expects stdin
// to be the writer that delivers bytes to the ACP server, and stdout to be
// the reader that receives bytes from it — matching the SDK's
// NewClientSideConnection contract.
func openWithIO(ctx context.Context, stdin io.Writer, stdout io.Reader, _ string) (*Connection, error) {
	cl := newClient()
	sdkConn := acpsdk.NewClientSideConnection(cl, stdin, stdout)

	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	initResp, err := sdkConn.Initialize(initCtx, acpsdk.InitializeRequest{
		Meta:            injectTraceparent(initCtx, nil),
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		// Phase 1 advertises no client capabilities: no fs, no terminal.
		// This is the safe default — adding them is a forward-compatible
		// change in a later phase if a specific agent benefit emerges.
		ClientCapabilities: acpsdk.ClientCapabilities{},
	})
	if err != nil {
		return nil, fmt.Errorf("acp.Open: initialize: %w", err)
	}

	return &Connection{
		conn:      sdkConn,
		client:    cl,
		capsCache: initResp.AgentCapabilities,
	}, nil
}

// Capabilities returns the agent's capability set as reported in the
// initialize response. Callers (typically the supervisor's per-attempt
// setup) use this to gate behavior on optional features like
// sessionCapabilities.close, sessionCapabilities.resume, and the various
// mcpCapabilities transports.
func (c *Connection) Capabilities() acpsdk.AgentCapabilities {
	return c.capsCache
}

// NewSession creates a new conversation session on the connected agent.
// cwd must be an absolute path (ACP protocol requirement). Phase 1's
// no-client-capabilities posture means no MCP servers are wired through;
// the SDK requires the slice anyway, so we send an empty one. If we ever
// need to expose Locutus tools via MCP, add a NewSessionWithMCP variant
// rather than changing this signature (it's what dispatch.PromptConn
// declares).
func (c *Connection) NewSession(ctx context.Context, cwd string) (string, error) {
	resp, err := c.conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Meta:       injectTraceparent(ctx, nil),
		Cwd:        cwd,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		return "", fmt.Errorf("acp.NewSession: %w", err)
	}
	return string(resp.SessionId), nil
}

// Prompt sends one prompt turn on the given session and returns a channel
// of streaming events that closes when the turn completes. The last event
// is always either dispatch.EventResult (carrying the StopReason as Text)
// or dispatch.EventError (carrying the error string). The synchronous
// error return is for setup failures only — failures during the prompt
// turn arrive as terminal events on the channel.
//
// pol is the supervisor's permission decider for THIS prompt only; pass a
// different Policy on a retry to model strict-then-loose behavior. Pass
// nil to cancel any permission request the agent makes (defensive default,
// useful for prompts that shouldn't be allowed to invoke tools). The
// Policy type lives in internal/dispatch/policy so implementations don't
// need to import the acpsdk types this package translates from.
func (c *Connection) Prompt(ctx context.Context, sessionID, text string, pol policy.Policy) (<-chan dispatch.AgentEvent, error) {
	if sessionID == "" {
		return nil, errors.New("acp.Prompt: empty sessionID")
	}
	sid := acpsdk.SessionId(sessionID)
	events := make(chan dispatch.AgentEvent, 16)
	c.client.register(sid, &activePrompt{events: events, policy: pol})

	go func() {
		defer close(events)
		defer c.client.unregister(sid)

		resp, err := c.conn.Prompt(ctx, acpsdk.PromptRequest{
			Meta:      injectTraceparent(ctx, nil),
			SessionId: sid,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(text)},
		})

		terminal := dispatch.AgentEvent{
			Timestamp: time.Now().UTC(),
			SessionID: sessionID,
		}
		if err != nil {
			terminal.Kind = dispatch.EventError
			terminal.Text = err.Error()
		} else {
			terminal.Kind = dispatch.EventResult
			terminal.Text = string(resp.StopReason)
		}
		select {
		case events <- terminal:
		case <-ctx.Done():
		}
	}()

	return events, nil
}

// Cancel sends a session/cancel notification, instructing the agent to
// abort an in-flight prompt turn for the given session. Per protocol, the
// agent responds to the corresponding session/prompt request with
// StopReason: cancelled — the caller's Prompt events channel will close
// after delivering that terminal event.
func (c *Connection) Cancel(ctx context.Context, sessionID string) error {
	return c.conn.Cancel(ctx, acpsdk.CancelNotification{
		Meta:      injectTraceparent(ctx, nil),
		SessionId: acpsdk.SessionId(sessionID),
	})
}

// Close terminates the agent subprocess. Uses session-aware shutdown when
// supported (sessionCapabilities.close on each active session) and falls
// back to killing the process when not — Gemini's empty
// sessionCapabilities is the canonical "no graceful close" case from
// Phase 0.
//
// Calling Close on a Connection constructed via openWithIO (no
// spawnedCmd) is a no-op for the subprocess teardown; the caller is
// responsible for the pipes.
func (c *Connection) Close() error {
	if c.spawnedCmd == nil {
		return nil
	}
	// Best-effort kill — graceful session/close per session is a Phase 3+
	// concern once the supervisor knows which sessions are active. For
	// Phase 1 the subprocess kill is sufficient and matches behavior on
	// agents that don't advertise sessionCapabilities.close anyway.
	if c.spawnedCmd.Process != nil {
		_ = c.spawnedCmd.Process.Kill()
	}
	_, _ = c.spawnedCmd.Process.Wait()
	return nil
}
