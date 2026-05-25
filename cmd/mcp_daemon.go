package cmd

import (
	"context"
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/mcp"
	"github.com/chetan/locutus/internal/specio"
)

// McpDaemonCmd implements `locutus mcp-daemon --project <root>`. It's
// an internal subcommand — operators don't invoke it directly. The
// `locutus mcp` bridge's EnsureDaemon helper forks this subprocess
// when no daemon is already responsive at the project's socket path.
//
// The daemon opens the project's SpecStore from disk, registers the
// v2 MCP server surface (spec_* tools + spec://manifest resource),
// binds the Unix socket under .locutus/, and runs the accept loop
// until ctx is cancelled. Concurrent bridge invocations share this
// one daemon instance so multi-client coordination works.
type McpDaemonCmd struct {
	Project string `help:"Absolute path to the project root." required:""`
}

func (c *McpDaemonCmd) Run(ctx context.Context, cli *CLI) error {
	// Same rationale as McpCmd.Run: anything running in this process
	// that asks for a render mode should pick the protocol-
	// appropriate (silent) sink, even though the daemon doesn't run
	// council code today.
	cli.mcpMode = true

	fsys := specio.NewOSFS(c.Project)
	store, err := agent.NewSpecStore(fsys)
	if err != nil {
		return fmt.Errorf("mcp-daemon: open spec store at %s: %w", c.Project, err)
	}
	listener, err := mcp.ListenSocket(mcp.SocketPath(c.Project))
	if err != nil {
		return fmt.Errorf("mcp-daemon: %w", err)
	}
	server := mcp.NewSpecServer(store)
	if err := mcp.ServeOnSocket(ctx, listener, server); err != nil && err != context.Canceled {
		return fmt.Errorf("mcp-daemon: serve: %w", err)
	}
	return nil
}
