package publisher

import (
	"encoding/json"
	"os"
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
models:
  - {provider: anthropic, tier: strong}
---
# Identity

You survey the spec graph.
`), 0o644))
	// Add one fast-tier and one balanced-tier agent so the model-mapping
	// tests have all three cases covered without further fixture surgery.
	require.NoError(t, fsys.WriteFile(".borg/agents/spec-candidate-survey.md", []byte(
		`---
id: spec-candidate-survey
role: enumeration
models:
  - {provider: anthropic, tier: fast}
---
# Identity

You enumerate candidates.
`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec-feature-elaborator.md", []byte(
		`---
id: spec-feature-elaborator
role: planning
models:
  - {provider: anthropic, tier: balanced}
---
# Identity

You elaborate features.
`), 0o644))
	require.NoError(t, fsys.WriteFile(".borg/agents/spec-decision-elaborator.md", []byte(
		`---
id: spec-decision-elaborator
role: planning
models:
  - {provider: anthropic, tier: strong}
  - {provider: googleai, tier: strong}
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

func TestPublisher_ClaudeCodeEmitsModelFromTier(t *testing.T) {
	// Canonical tier (declared in the canonical agent's frontmatter
	// models: slice) maps to Claude Code's short-form model field:
	// fast → haiku, balanced → sonnet, strong → opus. Agents that
	// declare no models field get no model emitted (inherit parent
	// session's model).
	fsys := seedProject(t, false)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	cases := []struct {
		filename, wantModel string
	}{
		{"spec-scout.md", "model: opus"},
		{"spec-candidate-survey.md", "model: haiku"},
		{"spec-feature-elaborator.md", "model: sonnet"},
		{"spec-decision-elaborator.md", "model: opus"},
	}
	for _, tc := range cases {
		got, err := readAsString(fsys, ".claude/agents/locutus/"+tc.filename)
		require.NoError(t, err, tc.filename)
		assert.Contains(t, got, tc.wantModel,
			"%s should carry %q in its frontmatter (tier→model mapping)", tc.filename, tc.wantModel)
	}
}

func TestPublisher_ClaudeCodeOmitsModelWhenTierUnset(t *testing.T) {
	// An agent without a models: declaration (or an unrecognized
	// tier) gets no model field in the published frontmatter so
	// Claude Code falls back to inheriting the parent session.
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/agents", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/plans", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/agents/no-tier.md", []byte(
		`---
id: no-tier
role: unknown
---
Body.
`), 0o644))
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	got, err := readAsString(fsys, ".claude/agents/locutus/no-tier.md")
	require.NoError(t, err)
	assert.NotContains(t, got, "model:",
		"agent without canonical tier should not emit a model: field")
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

	// Claude Code: .claude/commands/locutus-refine.md (CLI-verb slug;
	// see CanonicalActivity.CLIVerb).
	cc, err := readAsString(fsys, ".claude/commands/locutus-refine.md")
	require.NoError(t, err)
	assert.Contains(t, cc, "spec_refinement playbook")
	assert.Contains(t, cc, "Dispatch spec-scout")

	// Codex: .codex/commands/locutus-refine.toml
	codex, err := readAsString(fsys, ".codex/commands/locutus-refine.toml")
	require.NoError(t, err)
	assert.Contains(t, codex, `name = "locutus-refine"`)
	assert.Contains(t, codex, "Dispatch spec-scout")

	// Gemini: .gemini/extensions/locutus/commands/locutus-refine.toml
	gem, err := readAsString(fsys, ".gemini/extensions/locutus/commands/locutus-refine.toml")
	require.NoError(t, err)
	assert.Contains(t, gem, `name = "locutus-refine"`)
	assert.Contains(t, gem, "prompt = '''")
}

func TestPublisher_SkipsCommandsWhenPlanMissing(t *testing.T) {
	fsys := seedProject(t, false) // no plan files
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	// .claude/commands/ should not exist OR should be empty —
	// activity plan was never authored, no slash command emitted.
	_, err := fsys.ReadFile(".claude/commands/locutus-refine.md")
	assert.Error(t, err, "no plan body → no slash command")
}

func TestPublisher_MCPServerConfigInjected(t *testing.T) {
	// Stub the executable lookup so the test asserts against a
	// stable absolute path rather than wherever the go-test binary
	// lives. Production uses os.Executable.
	const stubPath = "/usr/local/bin/locutus"
	prev := executable
	executable = func() (string, error) { return stubPath, nil }
	t.Cleanup(func() { executable = prev })

	fsys := seedProject(t, false)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	// Claude Code: .mcp.json at project root.
	cc, err := readAsString(fsys, ".mcp.json")
	require.NoError(t, err)
	var parsed claudeMCPFile
	require.NoError(t, json.Unmarshal([]byte(cc), &parsed))
	require.Contains(t, parsed.MCPServers, "locutus")
	assert.Equal(t, stubPath, parsed.MCPServers["locutus"].Command,
		"Claude Code config should carry the absolute binary path, not a bare 'locutus' that depends on $PATH")
	assert.Equal(t, []string{"mcp"}, parsed.MCPServers["locutus"].Args)

	// Codex: .codex/config.toml.
	codex, err := readAsString(fsys, ".codex/config.toml")
	require.NoError(t, err)
	assert.Contains(t, codex, "[mcp_servers.locutus]")
	assert.Contains(t, codex, `command = "`+stubPath+`"`,
		"Codex config should carry the absolute binary path")

	// Gemini: .gemini/extensions/locutus/extension.json.
	gem, err := readAsString(fsys, ".gemini/extensions/locutus/extension.json")
	require.NoError(t, err)
	var manifest geminiManifest
	require.NoError(t, json.Unmarshal([]byte(gem), &manifest))
	assert.Equal(t, "locutus", manifest.Name)
	require.Contains(t, manifest.MCPServers, "locutus")
	assert.Equal(t, stubPath, manifest.MCPServers["locutus"].Command,
		"Gemini extension manifest should carry the absolute binary path")
}

func TestPublisher_MCPServerConfigFallsBackToBareNameOnExecutableErr(t *testing.T) {
	// Defensive case: if os.Executable fails (shouldn't on any
	// supported platform, but guard regardless), we emit the bare
	// "locutus" name that requires the user to install on $PATH.
	// Failing the publish would be worse — at least the bare-name
	// form works for users who DO have it on $PATH.
	prev := executable
	executable = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { executable = prev })

	fsys := seedProject(t, false)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	cc, err := readAsString(fsys, ".mcp.json")
	require.NoError(t, err)
	var parsed claudeMCPFile
	require.NoError(t, json.Unmarshal([]byte(cc), &parsed))
	assert.Equal(t, "locutus", parsed.MCPServers["locutus"].Command,
		"fallback should be the bare command name")
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
