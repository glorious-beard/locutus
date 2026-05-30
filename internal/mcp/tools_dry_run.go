package mcp

import (
	"context"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// descSpecDryRunReport is the registered description for
// spec_dry_run_report. Per DJ-134 the tool-behavior text lives here
// (registration), not in agent prompts.
const descSpecDryRunReport = "Returns the calling session's ordered list of captured mutations (DJ-147). " +
	"Only meaningful for sessions in dry-run mode — the workflow's would-be spec_propose_* / spec_revise_* / " +
	"spec_delete_* / spec_mark_approach_drifted / spec_update_goals_md_hash calls accumulate in a per-session " +
	"overlay; this tool reads them back. Response shape: {format, captured: [{tool, kind, id, body, timestamp}, ...]}. " +
	"For non-dry-run sessions the response is still well-formed — format is empty and captured is an empty list. " +
	"Format echoes the operator's --format choice (markdown|json) so the agent can decide between verbatim " +
	"emission (json) and prose narration (markdown). Read-only; no input."

// dryRunReportOutput is the response shape for spec_dry_run_report.
// The MCP SDK auto-derives StructuredContent from this typed value;
// JSON field names come from the explicit tags on CapturedMutation
// (see internal/agent/spec_store_overlay.go).
type dryRunReportOutput struct {
	Format   string                   `json:"format"`
	Captured []agent.CapturedMutation `json:"captured"`
}

// registerDryRunTools wires the spec_dry_run_report tool onto the
// server. The handler is read-only: it consults the session's overlay
// capture list and the dry-run format hint stored at MCP initialize
// time; it never mutates the overlay or base store.
func registerDryRunTools(server *mcp.Server, store *agent.SpecStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "spec_dry_run_report",
		Description: descSpecDryRunReport,
	}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, dryRunReportOutput, error) {
		captured := store.OverlayCaptured(req.Session)
		if captured == nil {
			// Non-dry-run sessions (no overlay registered) get an
			// empty list, not a nil slice — the JSON contract is
			// "captured":[], never "captured":null.
			captured = []agent.CapturedMutation{}
		}
		return nil, dryRunReportOutput{
			Format:   SessionDryRunFormat(req.Session),
			Captured: captured,
		}, nil
	})
}
