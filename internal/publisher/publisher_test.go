package publisher

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedProject builds a minimal .borg/ layout: two agent files and an
// optional activity plan. Returns a memfs ready to publish against.
func seedProject(t *testing.T, withPlan bool) specio.FS {
	t.Helper()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/agents", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/plans", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec-scout.md", []byte(
		`---
id: spec-scout
role: survey
---
# Identity

You survey the spec graph.
`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec-decision-elaborator.md", []byte(
		`---
id: spec-decision-elaborator
role: planning
---
# Identity

You decide on one foundational axis.
`), 0o644))
	if withPlan {
		require.NoError(t, fsys.WriteFile(".borg/plans/spec_refinement.md", []byte(
			`# spec_refinement playbook

Dispatch spec-scout. For each axis it surfaces, dispatch
spec-decision-elaborator. Commit each decision via spec_propose_decision.
`), 0o644))
	}
	return fsys
}

// buildRegistry returns the embedded-default registry. Tests don't
// need a project-override file because the test FS doesn't seed one.
func buildRegistry(t *testing.T) *activity.Registry {
	t.Helper()
	reg, err := activity.NewRegistry(nil)
	require.NoError(t, err)
	return reg
}

// readAsString is a one-liner reader that returns "" + the error
// when missing — keeps test assertions linear.
func readAsString(fsys specio.FS, path string) (string, error) {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func TestPublisher_ClaudeCodeEmitsSubagent(t *testing.T) {
	fsys := seedProject(t, false)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	got, err := readAsString(fsys, ".claude/agents/locutus/spec-scout.md")
	require.NoError(t, err)
	assert.Contains(t, got, "name: spec-scout")
	assert.Contains(t, got, "description: spec-scout — Locutus survey agent")
	assert.Contains(t, got, "You survey the spec graph.")
}

func TestPublisher_CodexEmitsSubagentTOML(t *testing.T) {
	fsys := seedProject(t, false)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	got, err := readAsString(fsys, ".codex/agents/locutus-spec-scout.toml")
	require.NoError(t, err)
	assert.Contains(t, got, `name = "locutus-spec-scout"`)
	assert.Contains(t, got, `description = "spec-scout — Locutus survey agent"`)
	assert.Contains(t, got, "developer_instructions = '''")
	assert.Contains(t, got, "You survey the spec graph.")
}

func TestPublisher_GeminiEmitsSubagentMD(t *testing.T) {
	fsys := seedProject(t, false)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	got, err := readAsString(fsys, ".gemini/extensions/locutus/agents/locutus-spec-scout.md")
	require.NoError(t, err)
	assert.Contains(t, got, "name: locutus-spec-scout")
	assert.Contains(t, got, "You survey the spec graph.")
}

func TestPublisher_SlashCommandPointsAtCanonicalPlan(t *testing.T) {
	fsys := seedProject(t, true)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	// Claude Code: .claude/commands/locutus-spec-refinement.md
	cc, err := readAsString(fsys, ".claude/commands/locutus-spec-refinement.md")
	require.NoError(t, err)
	assert.Contains(t, cc, "spec_refinement playbook")
	assert.Contains(t, cc, "Dispatch spec-scout")

	// Codex: .codex/commands/locutus-spec-refinement.toml
	codex, err := readAsString(fsys, ".codex/commands/locutus-spec-refinement.toml")
	require.NoError(t, err)
	assert.Contains(t, codex, `name = "locutus-spec-refinement"`)
	assert.Contains(t, codex, "Dispatch spec-scout")

	// Gemini: .gemini/extensions/locutus/commands/locutus-spec-refinement.toml
	gem, err := readAsString(fsys, ".gemini/extensions/locutus/commands/locutus-spec-refinement.toml")
	require.NoError(t, err)
	assert.Contains(t, gem, `name = "locutus-spec-refinement"`)
	assert.Contains(t, gem, "prompt = '''")
}

func TestPublisher_SkipsCommandsWhenPlanMissing(t *testing.T) {
	fsys := seedProject(t, false) // no plan files
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	// .claude/commands/ should not exist OR should be empty —
	// activity plan was never authored, no slash command emitted.
	_, err := fsys.ReadFile(".claude/commands/locutus-spec-refinement.md")
	assert.Error(t, err, "no plan body → no slash command")
}

func TestPublisher_MCPServerConfigInjected(t *testing.T) {
	fsys := seedProject(t, false)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	// Claude Code: .mcp.json at project root.
	cc, err := readAsString(fsys, ".mcp.json")
	require.NoError(t, err)
	var parsed claudeMCPFile
	require.NoError(t, json.Unmarshal([]byte(cc), &parsed))
	require.Contains(t, parsed.MCPServers, "locutus")
	assert.Equal(t, "locutus", parsed.MCPServers["locutus"].Command)
	assert.Equal(t, []string{"mcp"}, parsed.MCPServers["locutus"].Args)

	// Codex: .codex/config.toml.
	codex, err := readAsString(fsys, ".codex/config.toml")
	require.NoError(t, err)
	assert.Contains(t, codex, "[mcp_servers.locutus]")
	assert.Contains(t, codex, `command = "locutus"`)

	// Gemini: .gemini/extensions/locutus/extension.json.
	gem, err := readAsString(fsys, ".gemini/extensions/locutus/extension.json")
	require.NoError(t, err)
	var manifest geminiManifest
	require.NoError(t, json.Unmarshal([]byte(gem), &manifest))
	assert.Equal(t, "locutus", manifest.Name)
	require.Contains(t, manifest.MCPServers, "locutus")
	assert.Equal(t, "locutus", manifest.MCPServers["locutus"].Command)
}

func TestPublisher_ReEmissionOverwritesGenerated(t *testing.T) {
	fsys := seedProject(t, false)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	// Mutate the emitted file. A re-publish should restore it.
	require.NoError(t, fsys.WriteFile(".claude/agents/locutus/spec-scout.md", []byte("STALE"), 0o644))

	require.NoError(t, Publish(fsys, reg))

	got, err := readAsString(fsys, ".claude/agents/locutus/spec-scout.md")
	require.NoError(t, err)
	assert.NotEqual(t, "STALE", got, "re-publish overwrites stale content")
	assert.Contains(t, got, "You survey the spec graph.")
}

func TestPublisher_RejectsAgentMissingID(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/agents", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents/missing-id.md", []byte("---\nrole: nothing\n---\nbody\n"), 0o644))
	reg := buildRegistry(t)

	err := Publish(fsys, reg)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "missing required id")
}
