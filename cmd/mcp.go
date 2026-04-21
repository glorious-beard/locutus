package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/chetan/locutus/internal/render"
	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// McpCmd starts the MCP server.
type McpCmd struct {
	HTTP string `help:"Start HTTP transport on the given address (e.g. :8080)." optional:""`
}

func (c *McpCmd) Run(cli *CLI) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	server := NewMCPServerWithDir(cwd)

	ctx := context.Background()
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}

// --- Input types for tool parameters ---

type initInput struct {
	Name string `json:"name"`
}

type statusInput struct{}

type importInput struct {
	Content    string `json:"content"`
	Type       string `json:"type,omitempty"`
	SkipTriage bool   `json:"skip_triage,omitempty"`
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
		Description: "Show spec summary: GOALS.md presence, feature/decision/strategy/bug counts, orphans.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input statusInput) (*mcp.CallToolResult, any, error) {
		sd := GatherStatus(fsys)
		summary := render.StatusSummary(sd)
		return textResult(summary), nil, nil
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
		result, err := RunImport(ctx, fsys, []byte(input.Content), kind, input.SkipTriage, input.DryRun)
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
		result, err := RunAssimilate(ctx, llm, fsys)
		if err != nil {
			return errorResult(fmt.Sprintf("assimilate failed: %v", err)), nil, nil
		}
		msg := fmt.Sprintf("Assimilation complete: %d decisions, %d entities, %d features, %d gaps.",
			len(result.Decisions), len(result.Entities), len(result.Features), len(result.Gaps))
		return textResult(msg), nil, nil
	})

	// --- refine ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "refine",
		Description: "Council-driven deliberation on any spec node.",
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
		if kind != spec.KindDecision {
			return errorResult(fmt.Sprintf("refine for %s not yet implemented (Phase B/C)", kind)), nil, nil
		}
		llm, err := getLLM()
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		plan, err := RunRefine(ctx, llm, fsys, input.ID)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		return textResult(fmt.Sprintf("Refined %s: %d workstreams planned.", input.ID, len(plan.Workstreams))), nil, nil
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
