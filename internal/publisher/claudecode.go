package publisher

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/glorious-beard/locutus/internal/scaffold"
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
//
// The model: field is emitted when the canonical agent declares a
// `models:` tier. Claude Code subagent frontmatter accepts the
// short-form values haiku/sonnet/opus (mapping to the latest of each
// model family) or specific model ids; we use the short form so the
// published subagents track the current frontier without needing
// re-publish when Anthropic releases a new minor version. Tier
// mapping mirrors what the legacy adapters resolved each tier to
// against Anthropic's lineup:
//
//	fast      → haiku   (was claude-haiku-4-5 in pre-DJ-135 models.yaml)
//	balanced  → sonnet  (was claude-sonnet-4-6)
//	strong    → opus    (was claude-opus-4-7)
//
// Agents that declare no `models:` (or an unrecognized tier) get no
// model field — Claude Code defaults to inheriting the parent
// session's model.
func (claudeCodePublisher) PublishAgent(agent CanonicalAgent, fsys specio.FS) error {
	dir := ".claude/agents/locutus"
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var fm strings.Builder
	fm.WriteString("---\n")
	fmt.Fprintf(&fm, "name: %s\n", agent.ID)
	fmt.Fprintf(&fm, "description: %s\n", deriveDescription(agent))
	if m := claudeCodeModelForTier(agent.Tier); m != "" {
		fmt.Fprintf(&fm, "model: %s\n", m)
	}
	fm.WriteString("---\n")
	body := agent.Body
	if !strings.HasPrefix(body, "\n") {
		body = "\n" + body
	}
	return fsys.WriteFile(dir+"/"+agent.ID+".md", []byte(fm.String()+body), 0o644)
}

// claudeCodeModelForTier maps a canonical tier label to the Claude
// Code subagent `model:` short-form value. Returns "" for an
// unrecognized or absent tier — the publisher omits the field, and
// Claude Code falls back to inheriting the parent session's model.
//
// We intentionally don't pin specific model ids (claude-opus-4-7,
// etc.) here — the short form lets published subagents track the
// current frontier without a re-publish on every Anthropic minor
// release. The downside is that a future Anthropic release that
// changes what "opus" maps to could surprise us; mitigated by
// `locutus update --reset` being the conventional refresh path
// when behavior shifts.
func claudeCodeModelForTier(tier string) string {
	switch tier {
	case "fast":
		return "haiku"
	case "balanced":
		return "sonnet"
	case "strong":
		return "opus"
	default:
		return ""
	}
}

// EnsureHooks is a no-op for Claude Code under DJ-136. The
// runtime's `/goal` slash command (used by the spec_refinement
// overlay) provides the enforcement surface; PreToolUse hooks
// aren't required to make the activity safe. If a future activity
// needs Claude Code hooks, this method gains an implementation
// then.
func (claudeCodePublisher) EnsureHooks(_ []CanonicalActivity, _ specio.FS) error {
	return nil
}

// PublishActivity writes .claude/commands/locutus-<cli-verb>.md. The
// body is resolved with mode=interactive (DJ-140 phase 4): for the
// spec_refinement activity this lands the claude-code interactive
// overlay (the `/goal` convergence wrapper at
// spec_refinement.claude-code.interactive.md), since interactive
// convergence works inside a Claude Code TUI session. Activities
// without an interactive overlay fall through to the cross-runtime
// default body, unchanged. Claude Code treats the command body as the
// prompt the agent runs when the slash command fires.
func (claudeCodePublisher) PublishActivity(act CanonicalActivity, fsys specio.FS) error {
	body, _, err := scaffold.ResolvePlaybook(fsys, ".borg/plans", act.Name, "claude-code", scaffold.ModeInteractive)
	if err != nil {
		return err
	}
	dir := ".claude/commands"
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := fmt.Sprintf("%s/locutus-%s.md", dir, act.CLIVerb)
	return fsys.WriteFile(path, body, 0o644)
}
