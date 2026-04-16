package cmd

import (
	"context"
	"encoding/json"
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

type checkInput struct{}

type diffInput struct {
	ID string `json:"id"`
}

type triageInput struct {
	Input string `json:"input"`
}

type importInput struct {
	Content string `json:"content"`
	Type    string `json:"type"`
}

type analyzeInput struct{}

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

	// --- check ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "check",
		Description: "Validate strategy prerequisites for all active strategies.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input checkInput) (*mcp.CallToolResult, any, error) {
		// Load strategies and check prerequisites.
		strategies, _ := collectObjects[spec.Strategy](fsys, ".borg/spec/strategies")
		if len(strategies) == 0 {
			return textResult("No strategies found."), nil, nil
		}
		return textResult(fmt.Sprintf("Found %d strategies. Prerequisites check requires command execution.", len(strategies))), nil, nil
	})

	// --- diff ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "diff",
		Description: "Preview the blast radius of a spec change: affected decisions, strategies, and files.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input diffInput) (*mcp.CallToolResult, any, error) {
		if input.ID == "" {
			return errorResult("id is required"), nil, nil
		}
		br, err := RunDiff(fsys, input.ID)
		if err != nil {
			return errorResult(fmt.Sprintf("diff failed: %v", err)), nil, nil
		}

		var result string
		result = fmt.Sprintf("Blast radius for %s (%s):\n", br.Root.ID, br.Root.Kind)
		if len(br.Decisions) > 0 {
			result += fmt.Sprintf("  Decisions: %d\n", len(br.Decisions))
			for _, d := range br.Decisions {
				result += fmt.Sprintf("    - %s\n", d.ID)
			}
		}
		if len(br.Strategies) > 0 {
			result += fmt.Sprintf("  Strategies: %d\n", len(br.Strategies))
			for _, s := range br.Strategies {
				result += fmt.Sprintf("    - %s\n", s.ID)
			}
		}
		if len(br.Files) > 0 {
			result += fmt.Sprintf("  Files: %d\n", len(br.Files))
			for _, f := range br.Files {
				result += fmt.Sprintf("    - %s\n", f.ID)
			}
		}
		return textResult(result), nil, nil
	})

	// --- triage ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "triage",
		Description: "Evaluate an issue or feature description against GOALS.md for scope alignment.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input triageInput) (*mcp.CallToolResult, any, error) {
		if input.Input == "" {
			return errorResult("input is required"), nil, nil
		}
		return errorResult("LLM not configured — triage requires an LLM provider"), nil, nil
	})

	// --- import ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "import",
		Description: "Create a feature or bug from markdown with YAML frontmatter.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input importInput) (*mcp.CallToolResult, any, error) {
		if input.Content == "" {
			return errorResult("content is required"), nil, nil
		}
		data := []byte(input.Content)

		switch input.Type {
		case "bug":
			bug, err := ImportBug(fsys, data)
			if err != nil {
				return errorResult(fmt.Sprintf("import bug failed: %v", err)), nil, nil
			}
			return textResult(fmt.Sprintf("Created bug %q (%s)", bug.Title, bug.ID)), nil, nil
		default:
			feat, err := ImportFeature(fsys, data)
			if err != nil {
				return errorResult(fmt.Sprintf("import feature failed: %v", err)), nil, nil
			}
			return textResult(fmt.Sprintf("Created feature %q (%s)", feat.Title, feat.ID)), nil, nil
		}
	})

	// --- analyze ---
	mcp.AddTool(server, &mcp.Tool{
		Name:        "analyze",
		Description: "Analyze an existing codebase (brownfield) to infer spec, detect gaps, and propose remediation.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input analyzeInput) (*mcp.CallToolResult, any, error) {
		return errorResult("LLM not configured — analyze requires an LLM provider"), nil, nil
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

// jsonResult creates a CallToolResult with JSON-encoded content.
func jsonResult(v any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult(fmt.Sprintf("json marshal failed: %v", err))
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}
}
