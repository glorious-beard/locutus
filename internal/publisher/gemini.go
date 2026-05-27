package publisher

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/glorious-beard/locutus/internal/scaffold"
	"github.com/glorious-beard/locutus/internal/specio"
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
			"locutus": {Command: locutusCommand(), Args: []string{"mcp"}},
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

// geminiHookFragment is the JSON shape the per-activity hook files
// embed: a wrapper object with a `hooks` array. Each entry is an
// event + matcher + command + args triple. EnsureHooks merges those
// arrays into a single `.gemini/settings.json` document.
type geminiHookFragment struct {
	Comment string             `json:"_comment,omitempty"`
	Hooks   []geminiHookEntry  `json:"hooks"`
}

type geminiHookEntry struct {
	Event   string   `json:"event"`
	Matcher string   `json:"matcher"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// geminiSettings is the subset of .gemini/settings.json this
// publisher manages. Other keys the operator may have authored are
// preserved by reading the existing file, merging hooks, and
// writing the merged result back. We only ever touch the hooks
// field; everything else round-trips byte-stable in the same map.
type geminiSettings struct {
	Hooks []geminiHookEntry      `json:"hooks,omitempty"`
	Extra map[string]interface{} `json:"-"`
}

const geminiSettingsPath = ".gemini/settings.json"

// EnsureHooks writes .gemini/settings.json's hooks field from the
// embedded fragments. Idempotent — re-runs strip Locutus's own
// hook entries (matched by command path) and re-add them, so the
// file converges to the same shape regardless of how many resets
// run. User-authored hook entries adjacent to Locutus's are
// preserved.
func (geminiPublisher) EnsureHooks(activities []CanonicalActivity, fsys specio.FS) error {
	var locutusEntries []geminiHookEntry
	for _, act := range activities {
		fragmentBytes, ok, err := scaffold.ReadEmbeddedHook("gemini", act.Name, locutusCommand())
		if err != nil {
			return fmt.Errorf("read embedded hook for %s: %w", act.Name, err)
		}
		if !ok {
			continue
		}
		var fragment geminiHookFragment
		if err := json.Unmarshal(fragmentBytes, &fragment); err != nil {
			return fmt.Errorf("parse embedded gemini hook for %s: %w", act.Name, err)
		}
		locutusEntries = append(locutusEntries, fragment.Hooks...)
	}
	if len(locutusEntries) == 0 {
		return nil
	}

	if err := fsys.MkdirAll(".gemini", 0o755); err != nil {
		return err
	}

	// Read existing settings if present so we can preserve user-
	// authored hook entries. A missing file is fine — we'll create
	// one with only the Locutus entries.
	var combined map[string]interface{}
	existing, err := fsys.ReadFile(geminiSettingsPath)
	if err == nil && len(existing) > 0 {
		if err := json.Unmarshal(existing, &combined); err != nil {
			return fmt.Errorf("parse existing %s: %w", geminiSettingsPath, err)
		}
	}
	if combined == nil {
		combined = map[string]interface{}{}
	}

	// Strip prior Locutus entries by command-path match; re-append
	// the current set. Comparing the command field is enough — the
	// substituted {{LOCUTUS_BIN}} resolves to the same absolute
	// path across re-runs unless the binary moved (in which case
	// re-adding under the new path is exactly what we want).
	locutusBin := locutusCommand()
	var preserved []geminiHookEntry
	if raw, ok := combined["hooks"].([]interface{}); ok {
		for _, item := range raw {
			b, err := json.Marshal(item)
			if err != nil {
				continue
			}
			var entry geminiHookEntry
			if err := json.Unmarshal(b, &entry); err != nil {
				continue
			}
			if entry.Command != locutusBin {
				preserved = append(preserved, entry)
			}
		}
	}
	combined["hooks"] = append(preserved, locutusEntries...)

	out, err := json.MarshalIndent(combined, "", "  ")
	if err != nil {
		return err
	}
	return fsys.WriteFile(geminiSettingsPath, append(out, '\n'), 0o600)
}

func (p geminiPublisher) PublishActivity(act CanonicalActivity, fsys specio.FS) error {
	// Resolve with mode=interactive (DJ-140 phase 4). Gemini has no
	// interactive overlay for any activity, so this always falls
	// through to the cross-runtime default body — the one-iteration
	// playbook, not the /goal wrapper (Gemini has no interactive
	// convergence primitive).
	body, _, err := scaffold.ResolvePlaybook(fsys, ".borg/plans", act.Name, p.Name(), scaffold.ModeInteractive)
	if err != nil {
		return err
	}
	dir := geminiExtensionDir + "/commands"
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`name = "locutus-%s"
description = %s
prompt = %s
`,
		act.CLIVerb,
		tomlString(fmt.Sprintf("Locutus activity: %s", act.Name)),
		tomlMultilineString(string(body)),
	)
	path := fmt.Sprintf("%s/locutus-%s.toml", dir, act.CLIVerb)
	return fsys.WriteFile(path, []byte(content), 0o644)
}
