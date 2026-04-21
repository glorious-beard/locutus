package cmd

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/scaffold"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// connectMCPClient creates an MCP server configured to use the given directory,
// connects it via in-memory transports, and returns the client session. The
// server and client are cleaned up when the test ends.
func connectMCPClient(t *testing.T, dir string) *mcp.ClientSession {
	t.Helper()

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	server := NewMCPServerWithDir(dir)
	_, err := server.Connect(ctx, serverTransport, nil)
	assert.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	assert.NoError(t, err)

	t.Cleanup(func() {
		session.Close()
	})

	return session
}

// TestMCPServerInitializes verifies that the MCP server starts, accepts a
// client connection, and reports the Phase B tool set including the core
// names "init", "status", "refine", and "history".
func TestMCPServerInitializes(t *testing.T) {
	dir := t.TempDir()
	session := connectMCPClient(t, dir)

	ctx := context.Background()
	result, err := session.ListTools(ctx, nil)
	assert.NoError(t, err)

	// Phase B: init, status, check, import, assimilate (+analyze alias),
	// refine (+revisit alias), history. At least 7 canonical tools.
	assert.GreaterOrEqual(t, len(result.Tools), 7, "server should register at least 7 tools")

	names := make([]string, len(result.Tools))
	for i, tool := range result.Tools {
		names[i] = tool.Name
	}
	sort.Strings(names)

	assert.Contains(t, names, "init")
	assert.Contains(t, names, "status")
	assert.Contains(t, names, "refine")
	assert.Contains(t, names, "history")
	assert.Contains(t, names, "assimilate")
	assert.NotContains(t, names, "diff", "diff was folded into refine --dry-run in Phase B")
	assert.NotContains(t, names, "triage", "triage was folded into import in Phase B")
}

// TestMCPToolInit calls the "init" tool to scaffold a project in a temp
// directory, then verifies the result indicates success and that the .borg
// directory was created.
func TestMCPToolInit(t *testing.T) {
	dir := t.TempDir()
	session := connectMCPClient(t, dir)

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "init",
		Arguments: map[string]any{"name": "test-project"},
	})
	assert.NoError(t, err)
	assert.False(t, result.IsError, "init tool should not return an error")
	assert.NotEmpty(t, result.Content, "init result should have content")

	// Verify at least one content block contains success indicator.
	text := extractText(result)
	assert.NotEmpty(t, text, "result should contain text content")

	// Verify that the .borg directory was created on disk.
	fsys := specio.NewOSFS(dir)
	_, err = fsys.Stat(".borg")
	assert.NoError(t, err, ".borg directory should exist after init")
}

// TestMCPToolStatus first scaffolds a project, then calls the "status" tool
// and verifies the result contains spec summary information.
func TestMCPToolStatus(t *testing.T) {
	dir := t.TempDir()

	// Pre-scaffold the project so status has something to report.
	fsys := specio.NewOSFS(dir)
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	session := connectMCPClient(t, dir)

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "status",
		Arguments: map[string]any{},
	})
	assert.NoError(t, err)
	assert.False(t, result.IsError, "status tool should not return an error")

	text := extractText(result)
	assert.NotEmpty(t, text, "status result should contain text content")
}

// TestMCPToolRefineDryRun scaffolds a project with spec data, then calls
// "refine" with dry_run=true and verifies the result includes cascade preview
// information. Phase B folded the old `diff` tool into `refine --dry-run`.
func TestMCPToolRefineDryRun(t *testing.T) {
	dir := t.TempDir()

	// Set up a real project with spec data on disk.
	fsys := specio.NewOSFS(dir)
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	// Add a feature, decision, strategy, approach, and traces.
	feat := spec.Feature{
		ID:        "feat-auth",
		Title:     "User Authentication",
		Status:    spec.FeatureStatusActive,
		Decisions: []string{"dec-lang"},
		Approaches: []string{"app-auth"},
	}
	assert.NoError(t, specio.SavePair(fsys, ".borg/spec/features/feat-auth", feat, "Auth feature body."))

	dec := spec.Decision{
		ID:     "dec-lang",
		Title:  "Language Choice",
		Status: spec.DecisionStatusActive,
	}
	assert.NoError(t, specio.SavePair(fsys, ".borg/spec/decisions/dec-lang", dec, "We chose Go."))

	strat := spec.Strategy{
		ID:       "strat-go",
		Title:    "Use Go",
		Kind:     spec.StrategyKindFoundational,
		Decisions: []string{"dec-lang"},
		Status:   "active",
	}
	assert.NoError(t, specio.SavePair(fsys, ".borg/spec/strategies/strat-go", strat, "Go strategy body."))

	app := spec.Approach{
		ID:       "app-auth",
		Title:    "Auth Implementation",
		ParentID: "feat-auth",
		ArtifactPaths: []string{"cmd/main.go"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	assert.NoError(t, specio.SaveMarkdown(fsys, ".borg/spec/approaches/app-auth.md", app, "## Auth\n\nImplement OAuth.\n"))

	traces := spec.TraceabilityIndex{
		Entries: map[string]spec.TraceEntry{
			"cmd/main.go": {
				ApproachID:  "app-auth",
				DecisionIDs: []string{"dec-lang"},
				FeatureIDs:  []string{"feat-auth"},
			},
		},
	}
	tracesData, _ := json.Marshal(traces)
	assert.NoError(t, fsys.WriteFile(".borg/spec/traces.json", tracesData, 0o644))

	session := connectMCPClient(t, dir)

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "refine",
		Arguments: map[string]any{"id": "feat-auth", "dry_run": true},
	})
	assert.NoError(t, err)
	assert.False(t, result.IsError, "refine --dry-run should not error for a known ID")

	text := extractText(result)
	assert.Contains(t, text, "feat-auth", "result should reference the queried feature ID")
}

// TestMCPToolRefineUnknownID calls "refine" with an ID that does not exist in
// the spec graph and verifies the result reports an error.
func TestMCPToolRefineUnknownID(t *testing.T) {
	dir := t.TempDir()

	// Scaffold a minimal project with no spec data.
	fsys := specio.NewOSFS(dir)
	err := scaffold.Scaffold(fsys, "test-project")
	assert.NoError(t, err)

	session := connectMCPClient(t, dir)

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "refine",
		Arguments: map[string]any{"id": "nonexistent", "dry_run": true},
	})
	assert.NoError(t, err, "MCP protocol call should succeed even for tool-level errors")
	assert.True(t, result.IsError, "refine tool should signal error for unknown ID")
}

// TestMCPToolListTools verifies that all expected tools are listed and each has
// a non-empty description.
func TestMCPToolListTools(t *testing.T) {
	dir := t.TempDir()
	session := connectMCPClient(t, dir)

	ctx := context.Background()
	result, err := session.ListTools(ctx, nil)
	assert.NoError(t, err)

	expected := []string{"init", "status", "import", "assimilate", "refine", "adopt", "history"}
	names := make(map[string]string) // name -> description
	for _, tool := range result.Tools {
		names[tool.Name] = tool.Description
	}

	for _, name := range expected {
		desc, ok := names[name]
		assert.True(t, ok, "tool %q should be registered", name)
		if ok {
			assert.NotEmpty(t, desc, "tool %q should have a description", name)
		}
	}
}

// TestMCPToolUnknown calls a tool that is not registered and verifies that the
// server returns an error.
func TestMCPToolUnknown(t *testing.T) {
	dir := t.TempDir()
	session := connectMCPClient(t, dir)

	ctx := context.Background()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "nonexistent-tool",
		Arguments: map[string]any{},
	})
	// The MCP SDK should return an error for an unregistered tool, either as
	// a Go error or via the result's IsError field.
	if err != nil {
		// Protocol-level error is acceptable.
		return
	}
	assert.True(t, result.IsError, "calling an unknown tool should return an error result")
}

// extractText concatenates text from all TextContent blocks in a CallToolResult.
func extractText(result *mcp.CallToolResult) string {
	var text string
	for _, block := range result.Content {
		if tc, ok := block.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}
