package cmd

import (
	"github.com/chetan/locutus/internal/agent"
)

// pickSink selects the appropriate EventSink for the current CLI
// render mode. CLI commands (refine goals, import) call this once at
// the top of their council-driven path and pass the result into
// SpecGenRequest.Sink. MCP tool handlers don't go through here — they
// construct an mcpSink directly because it needs the inbound request.
func pickSink(cli *CLI) agent.EventSink {
	switch cli.RenderMode() {
	case RenderModeRich:
		return newCLISink()
	case RenderModePlain:
		return newPlainSink()
	case RenderModeSilent, RenderModeMCP:
		// MCP-from-CLI is a misuse — RenderModeMCP should only happen
		// inside the McpCmd handlers, which build their own sink. Fall
		// back to silent to avoid leaking pterm output onto an MCP
		// session's stderr.
		return agent.SilentSink{}
	default:
		return agent.SilentSink{}
	}
}
