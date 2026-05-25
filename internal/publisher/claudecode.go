package publisher

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/glorious-beard/locutus/internal/specio"
)

// Claude Code publisher. Format references:
//
//   - Subagents: .claude/agents/<name>.md with YAML frontmatter
//     {name, description}. Claude Code supports subdirectory
//     namespacing within .claude/agents/; we use .claude/agents/locutus/
//     so the publisher's emissions stay grouped and don't collide
//     with user-authored subagents.
//   - Slash commands: .claude/commands/<name>.md — the markdown body
//     IS the prompt. Subdirectory support is recent; we use the
//     flat .claude/commands/locutus-<activity>.md form for max
//     compatibility.
//   - MCP servers: .mcp.json at project root with mcpServers map.
//     Claude Code reads this on session start and exposes the named
//     servers' tools / resources / prompts to attached agents.
//
// The published subagent's body includes guidance pointing it at
// the locutus.* tools (spec_*, plus Phase 5's activity prompts).
// We don't add a `tools` field to the subagent frontmatter because
// limiting tools at the subagent level would require enumerating
// the locutus MCP toolset here — fragile; the orchestrator runtime
// already gates which MCP servers are attached.

func init() { registerRuntime(claudeCodePublisher{}) }

type claudeCodePublisher struct{}

func (claudeCodePublisher) Name() string { return "claude-code" }

// mcpConfigPath is the project-root location Claude Code reads. The
// alternative would be .claude/mcp.json — we use .mcp.json (the
// project-root form) because it matches the convention every other
// MCP-aware tool already understands.
const claudeMCPConfigPath = ".mcp.json"

// claudeMCPServerConfig is the structure Claude Code expects under
// mcpServers. The "command" + "args" pair tells Claude Code how to
// spawn the MCP server as a stdio subprocess; our bridge cmd `locutus
// mcp` then connects to (or forks) the per-project daemon.
type claudeMCPServerConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type claudeMCPFile struct {
	MCPServers map[string]claudeMCPServerConfig `json:"mcpServers"`
}

// EnsureMCPConfig writes .mcp.json so Claude Code attaches the
// locutus MCP server on session start. Re-emission overwrites — we
// don't try to merge with user-authored entries, because the canonical
// place for user MCP server registration is ~/.claude/mcp.json (user-
// scope) not the project-scope .mcp.json. Phase 4 owns project-scope.
func (claudeCodePublisher) EnsureMCPConfig(fsys specio.FS) error {
	file := claudeMCPFile{
		MCPServers: map[string]claudeMCPServerConfig{
			"locutus": {Command: locutusCommand(), Args: []string{"mcp"}},
		},
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return fsys.WriteFile(claudeMCPConfigPath, append(data, '\n'), 0o600)
}

// PublishAgent writes .claude/agents/locutus/<id>.md with the
// CC-shaped frontmatter and the canonical body verbatim.
func (claudeCodePublisher) PublishAgent(agent CanonicalAgent, fsys specio.FS) error {
	dir := ".claude/agents/locutus"
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	frontmatter := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n", agent.ID, deriveDescription(agent))
	body := agent.Body
	if !strings.HasPrefix(body, "\n") {
		body = "\n" + body
	}
	content := frontmatter + body
	return fsys.WriteFile(dir+"/"+agent.ID+".md", []byte(content), 0o644)
}

// PublishActivity writes .claude/commands/locutus-<name>.md. The
// content is the canonical plan body — Claude Code treats the
// command body as the prompt the agent runs when the slash command
// fires.
func (claudeCodePublisher) PublishActivity(act CanonicalActivity, fsys specio.FS) error {
	dir := ".claude/commands"
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := fmt.Sprintf("%s/locutus-%s.md", dir, strings.ReplaceAll(act.Name, "_", "-"))
	return fsys.WriteFile(path, []byte(act.PlanBody), 0o644)
}
