// DJ-147 Task 7 — spec_dry_run_report surfaces the calling session's
// ordered capture list. Three cases: (1) ordered propose captures
// round-trip, (2) a session with no captures gets an empty list, and
// (3) the spec_update_goals_md_hash capture appears in the response.
package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecDryRunReport_ReturnsCapturedOrdered(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	require.NoError(t, fsys.MkdirAll(".borg/spec/features", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	reg, err := activity.NewRegistry(fsys)
	require.NoError(t, err)
	server := NewSpecServer(store, fsys, reg, nil, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	for d := time.Now().Add(time.Second); time.Now().Before(d); {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	storeSessionContext(ss, "claude-code", "headless", true, "json")
	store.RegisterOverlay(ss)
	t.Cleanup(func() { store.UnregisterOverlay(ss) })

	// Propose two things in order. Argument shapes mirror the real
	// schemas in tools_spec_write.go (status, confidence required for
	// decisions; decisions list required for features).
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{
			"id":         "dec-a",
			"title":      "A",
			"status":     "proposed",
			"confidence": 1.0,
			"rationale":  "because reasons",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "propose_decision must succeed; got: %+v", res.Content)
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_feature",
		Arguments: map[string]any{
			"id":        "feat-a",
			"title":     "A",
			"status":    "proposed",
			"decisions": []any{"dec-a"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "propose_feature must succeed; got: %+v", res.Content)

	// Call the report tool.
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "spec_dry_run_report"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	body := callToolResultJSON(t, res)
	var parsed struct {
		Format   string `json:"format"`
		Captured []struct {
			Tool string `json:"tool"`
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"captured"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &parsed))
	assert.Equal(t, "json", parsed.Format)
	require.Len(t, parsed.Captured, 2)
	assert.Equal(t, "spec_propose_decision", parsed.Captured[0].Tool)
	assert.Equal(t, "dec-a", parsed.Captured[0].ID)
	assert.Equal(t, "spec_propose_feature", parsed.Captured[1].Tool)
	assert.Equal(t, "feat-a", parsed.Captured[1].ID)
}

func TestSpecDryRunReport_EmptyWhenNoCaptures(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	reg, err := activity.NewRegistry(fsys)
	require.NoError(t, err)
	server := NewSpecServer(store, fsys, reg, nil, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	for d := time.Now().Add(time.Second); time.Now().Before(d); {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	storeSessionContext(ss, "claude-code", "headless", true, "markdown")
	store.RegisterOverlay(ss)
	t.Cleanup(func() { store.UnregisterOverlay(ss) })

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "spec_dry_run_report"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	body := callToolResultJSON(t, res)
	assert.Contains(t, body, `"captured":[]`, "empty session returns an empty list")
}

func TestSpecDryRunReport_IncludesGoalsMdHashOverride(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec", 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	reg, err := activity.NewRegistry(fsys)
	require.NoError(t, err)
	server := NewSpecServer(store, fsys, reg, nil, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	for d := time.Now().Add(time.Second); time.Now().Before(d); {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	storeSessionContext(ss, "claude-code", "headless", true, "json")
	store.RegisterOverlay(ss)
	t.Cleanup(func() { store.UnregisterOverlay(ss) })

	// Trigger the manifest-hash capture.
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_update_goals_md_hash",
		Arguments: map[string]any{"hash": "abc123"},
	})
	require.NoError(t, err)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "spec_dry_run_report"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	body := callToolResultJSON(t, res)
	// The hash should surface in the captured list — the tool name and
	// hash value both must be visible somewhere in the JSON.
	assert.Contains(t, body, "abc123")
	assert.Contains(t, body, "spec_update_goals_md_hash")
}
