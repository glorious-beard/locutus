package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/dispatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatchUsesOuterLoopForAllRuntimes(t *testing.T) {
	// Per DJ-140, every runtime's headless dispatch is driven by the
	// Locutus outer loop — the claude-code single-dispatch special-case
	// is gone. /goal is interactive-only and unavailable in the headless
	// ACP dispatch path.
	for _, rt := range []string{"claude-code", "codex", "gemini"} {
		t.Run(rt, func(t *testing.T) {
			assert.True(t, dispatchUsesOuterLoop(rt),
				"runtime %q must use the Locutus outer loop", rt)
		})
	}
}

func TestMakeSessionDir_CreatesNestedStructure(t *testing.T) {
	root := t.TempDir()
	dir, err := makeSessionDir(root)
	require.NoError(t, err)

	// Path shape: <root>/.locutus/sessions/<YYYYMMDD>/<HHMM>/<sid>/
	rel, err := filepath.Rel(root, dir)
	require.NoError(t, err)
	parts := strings.Split(rel, string(filepath.Separator))
	require.Len(t, parts, 5, "unexpected path depth: %q", rel)
	assert.Equal(t, ".locutus", parts[0])
	assert.Equal(t, "sessions", parts[1])
	assert.Len(t, parts[2], 8, "date component should be YYYYMMDD")
	assert.Len(t, parts[3], 4, "time component should be HHMM")
	assert.Len(t, parts[4], 6, "sid component should be 6 chars")

	// Confirm the directory exists.
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestWriteJSONLine_AppendsOneLinePerEvent(t *testing.T) {
	var buf bytes.Buffer
	ev := dispatch.AgentEvent{
		Kind:      dispatch.EventToolCall,
		Timestamp: time.Now().UTC(),
		ToolName:  "spec_propose_decision",
	}
	require.NoError(t, writeJSONLine(&buf, ev))
	require.NoError(t, writeJSONLine(&buf, ev))

	// Two lines, each one JSON object terminated by newline.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)

	for _, line := range lines {
		var decoded map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &decoded))
		assert.Equal(t, "tool_call", decoded["Kind"])
		assert.Equal(t, "spec_propose_decision", decoded["ToolName"])
	}
}
