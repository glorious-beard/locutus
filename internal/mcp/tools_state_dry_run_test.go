package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDryRunCapturesStateMarkStatus(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)
	server := NewSpecServer(store, fsys, nil, nil, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, _ := server.Connect(context.Background(), serverT, nil)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, _ := client.Connect(context.Background(), clientT, nil)
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

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "state_mark_status",
		Arguments: map[string]any{
			"approach_id": "app-feat-foo",
			"status":      "planned",
			"message":     "operator manually scheduled for next adopt",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	caps := store.OverlayCaptured(ss)
	require.Len(t, caps, 1)
	assert.Equal(t, "state_mark_status", caps[0].Tool)
	assert.Equal(t, "app-feat-foo", caps[0].ID)
}

func TestDryRunCapturesStateDeleteRecord(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)
	server := NewSpecServer(store, fsys, nil, nil, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, _ := server.Connect(context.Background(), serverT, nil)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, _ := client.Connect(context.Background(), clientT, nil)
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

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "state_delete_record",
		Arguments: map[string]any{
			"approach_id": "app-feat-foo",
			"reason":      "parent feature retired",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	caps := store.OverlayCaptured(ss)
	require.Len(t, caps, 1)
	assert.Equal(t, "state_delete_record", caps[0].Tool)
}

func TestDryRunStateReadAfterWrite(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)
	server := NewSpecServer(store, fsys, nil, nil, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, _ := server.Connect(context.Background(), serverT, nil)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, _ := client.Connect(context.Background(), clientT, nil)
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

	// Write via the mark-status tool (cheaper than full reconciliation).
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "state_mark_status",
		Arguments: map[string]any{
			"approach_id": "app-feat-foo",
			"status":      "planned",
		},
	})
	require.NoError(t, err)

	// Read back via state_get_record — should see the overlay write.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "state_get_record",
		Arguments: map[string]any{"approach_ids": []string{"app-feat-foo"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	body := callToolResultJSON(t, res)
	assert.Contains(t, body, "app-feat-foo")
	assert.Contains(t, body, "planned")

	// List should include the overlay-only id.
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "state_list_records"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	assert.Contains(t, callToolResultJSON(t, res), "app-feat-foo")
}

func TestDryRunCapturesStateWrites(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{
			"record_reconciliation", "state_record_reconciliation",
			map[string]any{
				"approach_id":  "app-feat-foo",
				"artifacts":    map[string]any{"f.go": "sha256:abc"},
				"branch_name":  "adopt/001-app-feat-foo",
				"test_outcome": "passed",
				"test_command": "go test ./...",
			},
		},
		{
			"refresh_artifacts", "state_refresh_artifacts",
			map[string]any{
				"approach_id": "app-feat-foo",
				"artifacts":   map[string]any{"f.go": "sha256:new"},
				"reason":      "drift-classifier judged formatting-only",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSessionRuntimes()
			fsys := specio.NewMemFS()
			store, err := agent.NewSpecStore(fsys)
			require.NoError(t, err)

			server := NewSpecServer(store, fsys, nil, nil, nil)
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

			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			require.NoError(t, err)
			require.False(t, res.IsError, "tool result: %+v", res)

			caps := store.OverlayCaptured(ss)
			require.Len(t, caps, 1)
			assert.Equal(t, tc.tool, caps[0].Tool)
			assert.Equal(t, "app-feat-foo", caps[0].ID)
		})
	}
}
