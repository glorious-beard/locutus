package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDryRunContextNote_Markdown(t *testing.T) {
	note := dryRunContextNote("markdown")
	assert.Contains(t, note, "Dry-run mode is active")
	assert.Contains(t, note, "spec_dry_run_report")
	assert.Contains(t, note, "prose closing summary", "markdown variant must instruct narration, not verbatim emission")
	assert.NotContains(t, note, "fenced ```json", "markdown must not include the json verbatim instruction")
}

func TestDryRunContextNote_JSON(t *testing.T) {
	note := dryRunContextNote("json")
	assert.Contains(t, note, "Dry-run mode is active")
	assert.Contains(t, note, "spec_dry_run_report")
	assert.Contains(t, note, "fenced", "json variant must instruct verbatim fenced emission")
	assert.NotContains(t, note, "prose closing summary", "json must not also ask for narration")
}

func TestDryRunContextNote_UnknownFallsBackToMarkdown(t *testing.T) {
	note := dryRunContextNote("xml")
	assert.Contains(t, note, "prose closing summary", "unknown format falls back to markdown")
}

func TestRenderDryRunReportFromToolsJSONL_FormatJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "tools.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join([]string{
		`{"Kind":"tool_call","ToolName":"spec_propose_decision","ToolInput":{"id":"dec-a","title":"A","chosen_option":"x"}}`,
		`{"Kind":"tool_call","ToolName":"Read","ToolInput":{"file_path":"GOALS.md"}}`,
		`{"Kind":"tool_call","ToolName":"spec_propose_feature","ToolInput":{"id":"feat-a","title":"A"}}`,
		"",
	}, "\n")), 0o644))

	out := renderDryRunReportFromToolsJSONL(path, "json")
	assert.Contains(t, out, `"tool":"spec_propose_decision"`)
	assert.Contains(t, out, `"tool":"spec_propose_feature"`)
	assert.NotContains(t, out, `"tool":"Read"`, "non-mutation tools must not appear in the report")

	// JSON must be parseable
	var parsed struct {
		Format   string `json:"format"`
		Captured []any  `json:"captured"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	assert.Equal(t, "json", parsed.Format)
	assert.Len(t, parsed.Captured, 2)
}

func TestRenderDryRunReportFromToolsJSONL_FormatMarkdown(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "tools.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join([]string{
		`{"Kind":"tool_call","ToolName":"spec_propose_decision","ToolInput":{"id":"dec-a","title":"A"}}`,
		`{"Kind":"tool_call","ToolName":"spec_propose_feature","ToolInput":{"id":"feat-a","title":"A"}}`,
		"",
	}, "\n")), 0o644))

	out := renderDryRunReportFromToolsJSONL(path, "markdown")
	assert.Contains(t, out, "Dry-run captured")
	assert.Contains(t, out, "dec-a")
	assert.Contains(t, out, "feat-a")
}

func TestRenderDryRunReportFromToolsJSONL_MissingFile(t *testing.T) {
	out := renderDryRunReportFromToolsJSONL("/nonexistent/path", "markdown")
	assert.Empty(t, out, "missing file returns empty string (graceful fallback to agent narration)")
}
