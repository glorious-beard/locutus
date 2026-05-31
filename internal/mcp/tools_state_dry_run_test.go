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
