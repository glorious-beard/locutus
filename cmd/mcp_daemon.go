package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/mcp"
	"github.com/glorious-beard/locutus/internal/specio"
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
	// DJ-135 phase 5: load the activity registry so the server's
	// prompts surface can serve playbook content via prompts/get.
	// A registry construction failure is surfaced rather than
	// swallowed — operators editing agents.yaml deserve to see the
	// schema error immediately, not lose prompt visibility silently.
	reg, err := activity.NewRegistry(fsys)
	if err != nil {
		return fmt.Errorf("mcp-daemon: activity registry: %w", err)
	}
	listener, err := mcp.ListenSocket(mcp.SocketPath(c.Project))
	if err != nil {
		return fmt.Errorf("mcp-daemon: %w", err)
	}

	// PID file lets `locutus mcp-stop` and `update --reset` send
	// SIGTERM directly to this process instead of fishing for the
	// daemon via socket-side effects. Written after the listener
	// binds so a failed bind doesn't leave a stale PID claiming
	// ownership; removed on shutdown below so a clean exit doesn't
	// leave a stale entry that points at a recycled PID.
	if err := mcp.WritePidFile(c.Project, os.Getpid()); err != nil {
		_ = listener.Close()
		return fmt.Errorf("mcp-daemon: write pid file: %w", err)
	}
	defer func() {
		if err := mcp.RemovePidFile(c.Project); err != nil {
			slog.Warn("mcp-daemon: remove pid file on exit", "error", err)
		}
	}()

	server := mcp.NewSpecServer(store, fsys, reg)
	if err := mcp.ServeOnSocket(ctx, listener, server); err != nil && err != context.Canceled {
		return fmt.Errorf("mcp-daemon: serve: %w", err)
	}
	return nil
}
