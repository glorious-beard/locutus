package cmd

import (
	"fmt"

	"github.com/glorious-beard/locutus/internal/mcp"
	"github.com/glorious-beard/locutus/internal/specio"
)

// McpStopCmd implements `locutus mcp-stop`. Reads the per-project
// PID file at .locutus/mcp.pid, sends SIGTERM to the daemon, and
// waits briefly for it to exit (escalates to SIGKILL if it doesn't).
// No-op when no PID file is found or the recorded PID is already
// dead. Socket and PID files are cleaned up either way so the next
// `locutus mcp` invocation starts from a fresh slate.
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
