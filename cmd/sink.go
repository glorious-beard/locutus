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

// withProgressSink wraps the LLM with a NotifyingExecutor so direct
// (non-workflow) LLM calls render per-call lifecycle events to the
// chosen sink. Returns:
//
//   - the wrapped LLM (callers should use this from now on);
//   - the underlying sink, suitable for passing to workflow-aware
//     callers that bridge their own per-step events through it
//     (GenerateSpec, Analyze);
//   - a closer the caller MUST defer. The sink lifecycle now lives at
//     the cmd layer rather than inside GenerateSpec/Analyze, so a
//     single sink instance covers both workflow events and the
//     post-workflow direct calls (rewriter, synthesizer, advocate,
//     remediator) that previously had no UI.
//
// Workflow-driven calls suppress NotifyingExecutor emission via
// WithSuppressLLMNotify in WorkflowExecutor.executeAgent so the per-
// step events the workflow already fires aren't doubled.
func withProgressSink(cli *CLI, llm agent.AgentExecutor) (agent.AgentExecutor, agent.EventSink, func()) {
	sink := pickSink(cli)
	return &agent.NotifyingExecutor{Inner: llm, Sink: sink}, sink, sink.Close
}
