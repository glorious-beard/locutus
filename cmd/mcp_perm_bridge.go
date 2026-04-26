package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/chetan/locutus/internal/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// McpPermBridgeCmd is the subcommand Claude Code spawns via --mcp-config
// to satisfy --permission-prompt-tool. It speaks MCP on stdio, exposes
// a single tool `locutus_permission`, and forwards each invocation over
// a Unix-domain socket to the locutus supervisor that started this
// process. The supervisor's PermBridge (see internal/dispatch/bridge.go)
// is the other side.
type McpPermBridgeCmd struct {
	Socket string `help:"Unix socket path where the supervisor's PermBridge is listening." required:""`
}

// Run starts the bridge. Blocks until stdin closes (Claude exits) or the
// socket connection drops.
func (c *McpPermBridgeCmd) Run(ctx context.Context, cli *CLI) error {
	client, err := dialBridgeSocket(ctx, c.Socket)
	if err != nil {
		return fmt.Errorf("dial supervisor at %s: %w", c.Socket, err)
	}
	defer func() { _ = client.Close() }()

	server := newPermBridgeServer(client)
	return server.Run(ctx, &mcp.StdioTransport{})
}

// permInput is the MCP tool-call input Claude sends to the
// permission-prompt-tool. Field names match the schema advertised by
// newPermBridgeServer and what Claude Code's --permission-prompt-tool
// contract passes.
type permInput struct {
	ToolName  string         `json:"tool_name"`
	Input     map[string]any `json:"input"`
	ToolUseID string         `json:"tool_use_id"`
}

// permResponseBody is the JSON payload Claude expects inside the tool
// result's text content — {behavior, message}. Anything else and Claude
// treats the permission check as an error.
type permResponseBody struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

// newPermBridgeServer builds an MCP server that exposes the
// locutus_permission tool, forwarding each call through the given
// supervisor client.
func newPermBridgeServer(client *bridgeClient) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "locutus-perm-bridge", Version: "dev"},
		nil,
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "locutus_permission",
		Description: "Locutus permission-prompt tool: supervisor decides whether the requested tool call is allowed.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input permInput) (*mcp.CallToolResult, any, error) {
		decision, err := client.request(ctx, dispatch.PermRequest{
			ID:    input.ToolUseID,
			Tool:  input.ToolName,
			Input: input.Input,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("supervisor unreachable: %v", err)), nil, nil
		}
		body, _ := json.Marshal(permResponseBody{
			Behavior: decision.Behavior,
			Message:  decision.Message,
		})
		return textResult(string(body)), nil, nil
	})

	return server
}

// bridgeClient is the socket-side counterpart to the supervisor's
// PermBridge. It holds a single persistent connection and serializes
// access, since Claude invokes the permission tool one call at a time
// within a session.
type bridgeClient struct {
	conn    net.Conn
	scanner *bufio.Scanner
	mu      sync.Mutex
}

func dialBridgeSocket(ctx context.Context, path string) (*bridgeClient, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	return &bridgeClient{conn: conn, scanner: scanner}, nil
}

// request sends a PermRequest and waits for the matching PermDecision.
// Only one request may be in flight at a time per client; mu enforces
// that invariant to keep the request/response pairing on the shared
// socket unambiguous.
func (c *bridgeClient) request(ctx context.Context, req dispatch.PermRequest) (*dispatch.PermDecision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := c.conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Run the read on a goroutine so ctx cancellation can abort the wait.
	type result struct {
		decision *dispatch.PermDecision
		err      error
	}
	done := make(chan result, 1)
	go func() {
		if !c.scanner.Scan() {
			err := c.scanner.Err()
			if err == nil {
				err = io.EOF
			}
			done <- result{err: err}
			return
		}
		var decision dispatch.PermDecision
		if err := json.Unmarshal(c.scanner.Bytes(), &decision); err != nil {
			done <- result{err: fmt.Errorf("parse response: %w", err)}
			return
		}
		done <- result{decision: &decision}
	}()

	select {
	case r := <-done:
		return r.decision, r.err
	case <-ctx.Done():
		// Close the connection to unblock the scanner goroutine.
		_ = c.conn.Close()
		return nil, ctx.Err()
	}
}

// Close shuts the socket connection.
func (c *bridgeClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

