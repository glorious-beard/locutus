// DJ-136 phase 5 — hook-embedding tests. Asserts the scaffold's
// per-runtime hook fragments are reachable via ReadEmbeddedHook,
// and that the {{LOCUTUS_BIN}} placeholder substitutes correctly.

package scaffold_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/glorious-beard/locutus/internal/scaffold"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadEmbeddedHook_CodexSpecRefinement — the codex hook for
// spec_refinement reads back with the {{LOCUTUS_BIN}} placeholder
// substituted.
func TestReadEmbeddedHook_CodexSpecRefinement(t *testing.T) {
	body, ok, err := scaffold.ReadEmbeddedHook("codex", "spec_refinement", "/usr/local/bin/locutus")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, string(body), `command = "/usr/local/bin/locutus"`)
	assert.NotContains(t, string(body), "{{LOCUTUS_BIN}}",
		"placeholder must be substituted")
	assert.Contains(t, string(body), "[[hooks]]")
}

// TestReadEmbeddedHook_GeminiSpecRefinement — same for gemini.
func TestReadEmbeddedHook_GeminiSpecRefinement(t *testing.T) {
	body, ok, err := scaffold.ReadEmbeddedHook("gemini", "spec_refinement", "/usr/local/bin/locutus")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, string(body), `"command": "/usr/local/bin/locutus"`)
	assert.NotContains(t, string(body), "{{LOCUTUS_BIN}}")
	assert.Contains(t, string(body), `"event": "BeforeTool"`)
}

// TestReadEmbeddedHook_MissingActivity — unknown activity returns
// (nil, false, nil) without surfacing an error.
func TestReadEmbeddedHook_MissingActivity(t *testing.T) {
	body, ok, err := scaffold.ReadEmbeddedHook("codex", "no_such_activity", "/usr/bin/locutus")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, body)
}

// TestReadEmbeddedHook_UnknownRuntime — unknown runtime returns
// (nil, false, nil); no panic on the runtime lookup.
func TestReadEmbeddedHook_UnknownRuntime(t *testing.T) {
	_, ok, err := scaffold.ReadEmbeddedHook("xyzzy", "spec_refinement", "/usr/bin/locutus")
	require.NoError(t, err)
	assert.False(t, ok)
}

// TestReadEmbeddedHook_WindowsPathEscapedForJSON — a Windows-style
// binary path (with backslashes) must be backslash-escaped before
// substitution, so the resulting Gemini JSON parses cleanly. Without
// the escape, `C:\Users\...` produces `\U`, an invalid JSON string
// escape, breaking the publisher on Windows.
func TestReadEmbeddedHook_WindowsPathEscapedForJSON(t *testing.T) {
	winPath := `C:\Users\runneradmin\AppData\Local\locutus.exe`
	body, ok, err := scaffold.ReadEmbeddedHook("gemini", "spec_refinement", winPath)
	require.NoError(t, err)
	require.True(t, ok)

	// JSON must parse — the regression was an "invalid character 'U' in
	// string escape code" error from json.Unmarshal.
	var fragment map[string]any
	require.NoError(t, json.Unmarshal(body, &fragment),
		"gemini hook with Windows path must parse as JSON")

	// And the parsed command must round-trip back to the original path.
	hooks, _ := fragment["hooks"].([]any)
	require.NotEmpty(t, hooks)
	entry, _ := hooks[0].(map[string]any)
	assert.Equal(t, winPath, entry["command"])
}

// TestReadEmbeddedHook_WindowsPathEscapedForTOML — same regression
// shape on the codex TOML hook, where `\U`/`\A`/`\T` are invalid TOML
// basic-string escape sequences.
func TestReadEmbeddedHook_WindowsPathEscapedForTOML(t *testing.T) {
	winPath := `C:\Users\runneradmin\AppData\Local\locutus.exe`
	body, ok, err := scaffold.ReadEmbeddedHook("codex", "spec_refinement", winPath)
	require.NoError(t, err)
	require.True(t, ok)

	// The substituted line must contain the path with backslashes
	// doubled — TOML basic-string semantics, matching the existing
	// tomlString helper in internal/publisher/codex.go.
	assert.Contains(t, string(body), `command = "C:\\Users\\runneradmin\\AppData\\Local\\locutus.exe"`)
}

// TestHookActivities_ListsAvailable — HookActivities returns the
// list of activities that have a hook fragment registered for the
// runtime.
func TestHookActivities_ListsAvailable(t *testing.T) {
	codex := scaffold.HookActivities("codex")
	assert.Contains(t, codex, "spec_refinement",
		"codex hooks must include spec_refinement under DJ-136 phase 5")

	gemini := scaffold.HookActivities("gemini")
	assert.Contains(t, gemini, "spec_refinement",
		"gemini hooks must include spec_refinement under DJ-136 phase 6")

	unknown := scaffold.HookActivities("xyzzy")
	assert.Empty(t, unknown)

	// Sanity: no double-counting of file extensions.
	for _, a := range codex {
		assert.False(t, strings.Contains(a, "."),
			"HookActivities must strip extension; got %q", a)
	}
}
