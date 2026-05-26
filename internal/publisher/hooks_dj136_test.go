// DJ-136 phases 5 + 6 — hook-publishing assertions for Codex and
// Gemini. The publisher emits per-runtime hook fragments to the
// runtime's conventional config file alongside the slash command
// and MCP server registration. Claude Code is a no-op under
// DJ-136 (uses /goal for enforcement rather than hooks).

package publisher

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stableBin stubs locutus binary path resolution so the published
// configs assert against a known string. The real impl returns
// wherever the test binary lives, which varies per machine.
func stableBin(t *testing.T) string {
	const path = "/usr/local/bin/locutus"
	prev := executable
	executable = func() (string, error) { return path, nil }
	t.Cleanup(func() { executable = prev })
	return path
}

// TestCodexHookConfig_ValidPath — after publish, .codex/config.toml
// contains the [[hooks]] section pointing at the validate-decision
// subcommand. We check the literal block markers + the command/args
// substitution; deeper TOML-parse coverage isn't necessary because
// the fragment is generated from the embedded scaffold (which has
// its own tests).
func TestCodexHookConfig_ValidPath(t *testing.T) {
	stableBin(t)
	fsys := seedProject(t, true)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	body, err := readAsString(fsys, ".codex/config.toml")
	require.NoError(t, err)
	assert.Contains(t, body, "[[hooks]]",
		"codex config must include the [[hooks]] table")
	assert.Contains(t, body, `event = "PreToolUse"`)
	assert.Contains(t, body, `matcher = "mcp__locutus__spec_propose_decision"`)
	assert.Contains(t, body, `command = "/usr/local/bin/locutus"`)
	assert.Contains(t, body, `args = ["hook-validate-decision"]`)
	assert.Contains(t, body, "locutus hooks (DJ-136)",
		"codex config must mark the section so re-emit can find + replace it")
}

// TestCodexPublisher_HookSectionIsIdempotent — repeated Publish
// calls converge to the same single-section output; no duplicate
// [[hooks]] tables. Catches the regression of "every reset doubles
// the hook count."
func TestCodexPublisher_HookSectionIsIdempotent(t *testing.T) {
	stableBin(t)
	fsys := seedProject(t, true)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))
	require.NoError(t, Publish(fsys, reg))
	require.NoError(t, Publish(fsys, reg))

	body, err := readAsString(fsys, ".codex/config.toml")
	require.NoError(t, err)
	count := strings.Count(body, "[[hooks]]")
	assert.Equal(t, 1, count,
		"three Publish() calls must converge to a single [[hooks]] table, got %d", count)
}

// TestGeminiHookConfig_ValidPath — after publish,
// .gemini/settings.json's hooks field contains the BeforeTool entry
// for the validate-decision subcommand.
func TestGeminiHookConfig_ValidPath(t *testing.T) {
	bin := stableBin(t)
	fsys := seedProject(t, true)
	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	body, err := readAsString(fsys, ".gemini/settings.json")
	require.NoError(t, err)

	var settings struct {
		Hooks []struct {
			Event   string   `json:"event"`
			Matcher string   `json:"matcher"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &settings))
	require.NotEmpty(t, settings.Hooks)

	var found bool
	for _, h := range settings.Hooks {
		if h.Matcher == "mcp__locutus__spec_propose_decision" {
			found = true
			assert.Equal(t, "BeforeTool", h.Event)
			assert.Equal(t, bin, h.Command)
			assert.Equal(t, []string{"hook-validate-decision"}, h.Args)
		}
	}
	assert.True(t, found, "gemini settings must include a BeforeTool hook for spec_propose_decision")
}

// TestGeminiHookConfig_PreservesUserHooks — user-authored hook
// entries (not pointing at the locutus binary) are preserved across
// re-publish. Locutus only manages its own entries.
func TestGeminiHookConfig_PreservesUserHooks(t *testing.T) {
	stableBin(t)
	fsys := seedProject(t, true)
	require.NoError(t, fsys.MkdirAll(".gemini", 0o755))
	preExisting := `{
  "hooks": [
    {"event": "BeforeTool", "matcher": "Bash", "command": "/usr/bin/my-bash-guard", "args": []}
  ],
  "theme": "dark"
}
`
	require.NoError(t, fsys.WriteFile(".gemini/settings.json", []byte(preExisting), 0o644))

	reg := buildRegistry(t)
	require.NoError(t, Publish(fsys, reg))

	body, err := readAsString(fsys, ".gemini/settings.json")
	require.NoError(t, err)

	// User's bash guard preserved.
	assert.Contains(t, body, `/usr/bin/my-bash-guard`,
		"user-authored hook must survive locutus publish")
	// Non-hooks keys preserved.
	assert.Contains(t, body, `"theme"`,
		"non-hook top-level keys must survive locutus publish")
	// Locutus's hook present too.
	assert.Contains(t, body, "spec_propose_decision",
		"locutus's own hook must be appended alongside the user's")
}
