package cmd

import (
	"fmt"

	"github.com/chetan/locutus/internal/mcp"
	"github.com/chetan/locutus/internal/specio"
)

// McpStopCmd implements `locutus mcp-stop`. Removes the
// per-project Unix socket so the daemon's accept loop unwinds on its
// next connection attempt. No-op when no daemon is running.
//
// Phase 1's StopDaemon is a "remove the socket" implementation that
// doesn't carry PID-file signaling — a future revision should
// SIGTERM the daemon process directly. See TODO in
// internal/mcp/bootstrap.go.
type McpStopCmd struct{}

func (c *McpStopCmd) Run() error {
	root, err := specio.FindProjectRootFromCwd()
	if err != nil {
		return fmt.Errorf("mcp-stop: %w", err)
	}
	if err := mcp.StopDaemon(root); err != nil {
		return fmt.Errorf("mcp-stop: %w", err)
	}
	return nil
}
