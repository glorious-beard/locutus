package publisher

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chetan/locutus/internal/specio"
)

// Gemini CLI publisher. Format notes (subject to Phase 5 verification):
//
//   - Extensions: Gemini CLI supports per-project extensions under
//     .gemini/extensions/<extension-name>/. The extension has its
//     own manifest (extension.json) plus an agents/ + commands/
//     layout. We emit ours at .gemini/extensions/locutus/.
//   - Subagents: .gemini/extensions/locutus/agents/locutus-<id>.md
//     with markdown + frontmatter. Filename prefix matches the Codex
//     approach for consistency, though the namespacing is doubly
//     scoped (extension dir + filename prefix) here.
//   - Slash commands: .gemini/extensions/locutus/commands/locutus-
//     <activity>.toml.
//   - MCP servers: declared inside the extension manifest itself
//     (manifest.json mcpServers map). When Gemini loads the
//     extension, the listed MCP servers are attached to the session.

func init() { registerRuntime(geminiPublisher{}) }

type geminiPublisher struct{}

func (geminiPublisher) Name() string { return "gemini" }

const geminiExtensionDir = ".gemini/extensions/locutus"

type geminiManifest struct {
	Name        string                            `json:"name"`
	Version     string                            `json:"version"`
	Description string                            `json:"description"`
	MCPServers  map[string]claudeMCPServerConfig  `json:"mcpServers"`
}

// EnsureMCPConfig writes the extension's manifest.json. Re-emission
// overwrites; the manifest is the canonical extension descriptor.
func (geminiPublisher) EnsureMCPConfig(fsys specio.FS) error {
	if err := fsys.MkdirAll(geminiExtensionDir, 0o755); err != nil {
		return err
	}
	manifest := geminiManifest{
		Name:        "locutus",
		Version:     "dj-135-phase-4",
		Description: "Locutus spec-driven project manager — exposes spec graph tools, resources, and activity prompts via MCP.",
		MCPServers: map[string]claudeMCPServerConfig{
			"locutus": {Command: "locutus", Args: []string{"mcp"}},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return fsys.WriteFile(geminiExtensionDir+"/extension.json", append(data, '\n'), 0o600)
}

func (geminiPublisher) PublishAgent(agent CanonicalAgent, fsys specio.FS) error {
	dir := geminiExtensionDir + "/agents"
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	frontmatter := fmt.Sprintf("---\nname: locutus-%s\ndescription: %s\n---\n", agent.ID, deriveDescription(agent))
	body := agent.Body
	if !strings.HasPrefix(body, "\n") {
		body = "\n" + body
	}
	content := frontmatter + body
	return fsys.WriteFile(dir+"/locutus-"+agent.ID+".md", []byte(content), 0o644)
}

func (geminiPublisher) PublishActivity(act CanonicalActivity, fsys specio.FS) error {
	dir := geminiExtensionDir + "/commands"
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`name = "locutus-%s"
description = %s
prompt = %s
`,
		strings.ReplaceAll(act.Name, "_", "-"),
		tomlString(fmt.Sprintf("Locutus activity: %s", act.Name)),
		tomlMultilineString(act.PlanBody),
	)
	path := fmt.Sprintf("%s/locutus-%s.toml", dir, strings.ReplaceAll(act.Name, "_", "-"))
	return fsys.WriteFile(path, []byte(content), 0o644)
}
