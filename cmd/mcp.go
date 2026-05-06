package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// McpCmd starts the MCP server.
type McpCmd struct {
	HTTP string `help:"Start HTTP transport on the given address (e.g. :8080)." optional:""`
}

func (c *McpCmd) Run(ctx context.Context, cli *CLI) error {
	// Mark this process as serving MCP so any council path that asks
	// for a render mode picks the protocol-appropriate sink. Pterm
	// output to stderr would interleave with MCP framing and confuse
	// clients that capture the server's stderr.
	cli.mcpMode = true

	root, err := specio.FindProjectRootFromCwd()
	if err != nil {
		return fmt.Errorf("mcp: %w — start `locutus mcp` from inside a Locutus project", err)
	}

	server := NewMCPServerWithDir(root)

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}

// --- Input types for tool parameters ---

type initInput struct {
	Name string `json:"name"`
}

type statusInput struct {
	Full   bool     `json:"full,omitempty"`
	Format string   `json:"format,omitempty"`
	Kind   []string `json:"kind,omitempty"`
	Status []string `json:"status,omitempty"`
}

type importInput struct {
	Content    string `json:"content"`
	Type       string `json:"type,omitempty"`
	SkipTriage bool   `json:"skip_triage,omitempty"`
	NoPlan     bool   `json:"no_plan,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

type assimilateInput struct {
	DryRun bool `json:"dry_run,omitempty"`
}

type refineInput struct {
	ID     string `json:"id"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type adoptInput struct {
	Scope  string `json:"scope,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type historyInput struct {
	ID           string `json:"id,omitempty"`
	Narrative    bool   `json:"narrative,omitempty"`
	Alternatives bool   `json:"alternatives,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

// NewMCPServerWithDir creates a configured MCP server with all Locutus tools
// registered, operating on the given base directory.
func NewMCPServerWithDir(dir string) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "locutus", Version: "dev"},
		nil,
	)

	fsys := specio.NewOSFS(dir)

	// --- init ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "init",
		Description: "Initialize a new spec-driven project with .borg/ scaffold, GOALS.md, and agent definitions.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input initInput) (*mcp.CallToolResult, any, error) {
		name := input.Name
		if name == "" {
			name = "project"
		}
		if err := scaffold.Scaffold(fsys, name); err != nil {
			return errorResult(fmt.Sprintf("init failed: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("Initialized project %q. Created .borg/ scaffold, GOALS.md, agents, and workflows.", name)), nil, nil
	})

	// --- status ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Show spec summary. With full=true, returns a comprehensive snapshot (markdown by default; format=json for the structured form). Optional kind/status filters narrow the visible set.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input statusInput) (*mcp.CallToolResult, any, error) {
		if !input.Full {
			sd := GatherStatus(fsys)
			return textResult(render.StatusSummary(sd)), nil, nil
		}

		data, err := GatherSnapshotData(fsys, render.SnapshotFilters{
			Kinds:    input.Kind,
			Statuses: input.Status,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("snapshot: %v", err)), nil, nil
		}

		format := input.Format
		if format == "" {
			format = "markdown"
		}
		switch format {
		case "json":
			j, err := json.MarshalIndent(data, "", "  ")
			if err != nil {
				return errorResult(fmt.Sprintf("snapshot json: %v", err)), nil, nil
			}
			return textResult(string(j)), nil, nil
		case "markdown":
			return textResult(render.SnapshotMarkdown(data)), nil, nil
		default:
			return errorResult(fmt.Sprintf("unknown format %q (want markdown or json)", format)), nil, nil
		}
	})

	// --- import (admits with built-in triage gate; Phase B folded the
	// standalone `triage` tool in here) ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "import",
		Description: "Admit a feature or bug. Evaluates against GOALS.md unless skip_triage=true.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input importInput) (*mcp.CallToolResult, any, error) {
		if input.Content == "" {
			return errorResult("content is required"), nil, nil
		}
		kind := input.Type
		if kind == "" {
			kind = "feature"
		}
		var llm agent.AgentExecutor
		if !input.SkipTriage {
			var err error
			llm, _, err = recordingLLM(fsys, dir, "mcp:import")
			if err != nil {
				return errorResult(err.Error()), nil, nil
			}
		}
		result, err := RunImport(ctx, llm, fsys, []byte(input.Content), "", kind, input.SkipTriage, input.NoPlan, input.DryRun, newMCPSink(ctx, req))
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if !result.Accepted {
			reason := "triage rejected"
			if result.Verdict != nil && result.Verdict.Reason != "" {
				reason = result.Verdict.Reason
			}
			return errorResult(fmt.Sprintf("not admitted: %s", reason)), nil, nil
		}
		verb := "Imported"
		if result.DryRun {
			verb = "Dry-run: would import"
		}
		id := result.FeatureID
		if id == "" {
			id = result.BugID
		}
		return textResult(fmt.Sprintf("%s %s (%s) at %s.", verb, kind, id, result.Destination)), nil, nil
	})

	// --- assimilate ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "assimilate",
		Description: "Infer or update spec from an existing codebase.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input assimilateInput) (*mcp.CallToolResult, any, error) {
		llm, err := getLLM()
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		result, err := RunAssimilate(ctx, llm, fsys, true, newMCPSink(ctx, req))
		if err != nil {
			return errorResult(fmt.Sprintf("assimilate failed: %v", err)), nil, nil
		}
		msg := fmt.Sprintf("Assimilation complete: %d decisions, %d entities, %d features, %d gaps.",
			len(result.Decisions), len(result.Entities), len(result.Features), len(result.Gaps))
		return textResult(msg), nil, nil
	})

	// --- refine ---
	// Mirrors the CLI dispatcher: routes by node kind through the same
	// dispatchRefine that powers `locutus refine`. The MCP sink fires
	// progress notifications on the originating session for kinds that
	// drive the council (currently Goals).
	mcp.AddTool(server, &mcp.Tool{
		Name:        "refine",
		Description: "Council-driven deliberation on any spec node (decision, feature, strategy, bug, approach, or goals).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input refineInput) (*mcp.CallToolResult, any, error) {
		if input.ID == "" {
			return errorResult("id is required"), nil, nil
		}
		kind, err := resolveNodeKind(fsys, input.ID)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		if input.DryRun {
			br, err := RunDiff(fsys, input.ID)
			if err != nil {
				return errorResult(err.Error()), nil, nil
			}
			msg := fmt.Sprintf("Refining %s %s (dry-run) — cascade preview: %d decisions, %d strategies, %d approaches affected.",
				kind, input.ID, len(br.Decisions), len(br.Strategies), len(br.Approaches))
			return textResult(msg), nil, nil
		}
		llm, _, err := recordingLLM(fsys, dir, "mcp:refine "+input.ID)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		result, err := dispatchRefine(ctx, llm, fsys, input.ID, kind, newMCPSink(ctx, req))
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		return textResult(formatRefineResultForMCP(result)), nil, nil
	})

	// --- adopt ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "adopt",
		Description: "Bring code into alignment with spec. Classifies every Approach (live/drifted/out_of_spec/unplanned/failed), runs prereq checks, and returns a plan. Use dry_run=true to preview.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input adoptInput) (*mcp.CallToolResult, any, error) {
		report, err := RunAdopt(ctx, fsys, input.Scope, input.DryRun)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		msg := fmt.Sprintf("Adoption: %d live, %d drifted, %d out_of_spec, %d unplanned. %d candidate(s). Prereqs: %t.",
			report.Summary.Live, report.Summary.Drifted, report.Summary.OutOfSpec,
			report.Summary.Unplanned, report.Summary.Candidates, report.PrereqsOK)
		if !report.PrereqsOK {
			return errorResult("prereqs failed: " + msg), nil, nil
		}
		if report.Summary.OutOfSpec > 0 && !report.DryRun {
			return errorResult("out_of_spec drift present: " + msg), nil, nil
		}
		return textResult(msg), nil, nil
	})

	// --- history ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "history",
		Description: "Query the past-tense record of spec changes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input historyInput) (*mcp.CallToolResult, any, error) {
		return runHistoryMCP(fsys, input)
	})

	return server
}

// formatRefineResultForMCP renders a RefineResult as a single text
// line for return through an MCP tool response. Mirrors what the CLI
// printRefineSummary writes to stdout but condenses to one line per
// kind because MCP clients render text content as-is and don't carry
// the same multi-line affordances as a terminal.
func formatRefineResultForMCP(r *RefineResult) string {
	if r == nil {
		return "Refine: no result."
	}
	if r.Cascade != nil {
		c := r.Cascade
		return fmt.Sprintf(
			"Refined decision %s: %d feature(s) rewritten, %d strategy(ies) rewritten, %d approach(es) drifted, %d parent(s) already accurate.",
			r.NodeID, len(c.UpdatedFeatures), len(c.UpdatedStrategies), len(c.DriftedApproaches), len(c.Skipped),
		)
	}
	if r.Generated != nil {
		g := r.Generated
		out := fmt.Sprintf("Refined %s %s: %d feature(s), %d decision(s), %d strategy(ies), %d approach(es).",
			r.NodeKind, r.NodeID, g.Features, g.Decisions, g.Strategies, g.Approaches)
		if len(g.IntegrityWarnings) > 0 {
			out += fmt.Sprintf(" %d dangling reference(s) stripped.", len(g.IntegrityWarnings))
		}
		return out
	}
	if r.Rewrite == nil {
		return fmt.Sprintf("Refined %s %s: no action.", r.NodeKind, r.NodeID)
	}
	if !r.Rewrite.Updated {
		out := fmt.Sprintf("Refined %s %s: no changes — already consistent with inputs.", r.NodeKind, r.NodeID)
		if r.Rewrite.Rationale != "" {
			out += " " + r.Rewrite.Rationale
		}
		return out
	}
	out := fmt.Sprintf("Refined %s %s.", r.NodeKind, r.NodeID)
	if len(r.Rewrite.DriftedApproaches) > 0 {
		out += fmt.Sprintf(" %d approach(es) drifted.", len(r.Rewrite.DriftedApproaches))
	}
	if r.Rewrite.Rationale != "" {
		out += " " + r.Rewrite.Rationale
	}
	return out
}

// textResult creates a successful CallToolResult with text content.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// errorResult creates a CallToolResult that signals a tool-level error.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}
