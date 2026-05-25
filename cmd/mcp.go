package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/glorious-beard/locutus/internal/mcp"
	"github.com/glorious-beard/locutus/internal/specio"
)

// McpCmd implements `locutus mcp`. The command is a thin bridge: it
// discovers (or forks) the per-project MCP daemon and proxies the
// calling process's stdin/stdout to the daemon's Unix socket. To an
// external MCP client (Claude Code, Codex, Gemini CLI) the invocation
// looks like a standard stdio MCP server; under the hood every
// connection shares one daemon-side SpecStore.
//
// Per DJ-135, the daemon-per-project + bridge-per-client model is
// what makes singleton coordination possible: writes through one
// client are immediately visible to reads on another, and
// notifications/resources/updated fans out to every subscribed
// session.
type McpCmd struct{}

// Run discovers or forks the daemon, then bridges stdio to its socket.
// Returns when the calling MCP client closes stdin (clean shutdown)
// or when ctx is cancelled (SIGINT etc.).
func (c *McpCmd) Run(ctx context.Context, cli *CLI) error {
	// Mark this process as serving MCP so any council path that asks
	// for a render mode picks the protocol-appropriate sink. The
	// bridge doesn't run council code itself, but the flag carries
	// through anything that might.
	cli.mcpMode = true

	root, err := specio.FindProjectRootFromCwd()
	if err != nil {
		return fmt.Errorf("mcp: %w — start `locutus mcp` from inside a Locutus project", err)
	}
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("mcp: resolve own binary: %w", err)
	}
	sockPath, err := mcp.EnsureDaemon(ctx, root, binary)
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	if err := mcp.BridgeStdioToSocket(ctx, sockPath); err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}
