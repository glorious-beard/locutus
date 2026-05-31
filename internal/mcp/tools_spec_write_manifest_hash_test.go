// DJ-139 phase 4 — MCP write tool spec_update_goals_md_hash.
//
// The Phase 6 `refine goals` playbook calls this tool at the end of
// a successful goal-layer sync. The handler writes the supplied hash
// through an atomic read-modify-write that preserves every other
// manifest field; synced_at is server-stamped from the wall clock
// (DJ-141), not agent-supplied.

package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecUpdateGoalsMdHashWritesManifest — call the MCP tool with a
// concrete hash; confirm the manifest on disk carries the new hash, a
// server-stamped synced_at within the call window, AND every other
// manifest field survives unchanged.
func TestSpecUpdateGoalsMdHashWritesManifest(t *testing.T) {
	fsys := specio.NewMemFS()
	original := spec.Manifest{
		ProjectName: "locutus",
		Version:     "0.1.0",
		Model:       "claude-sonnet-4-5",
		CreatedAt:   time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC),
	}
	data, err := json.MarshalIndent(original, "", "  ")
	require.NoError(t, err)
	require.NoError(t, fsys.MkdirAll(".borg", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/manifest.json", data, 0o644))

	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	server := NewSpecServer(store, nil, nil, nil, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err = server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })

	hash := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	before := time.Now().UTC()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_update_goals_md_hash",
		Arguments: map[string]any{
			"hash": hash,
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)
	after := time.Now().UTC()

	// Read the manifest back; assert the hash landed and every other
	// field survived.
	got, err := fsys.ReadFile(".borg/manifest.json")
	require.NoError(t, err)
	var m spec.Manifest
	require.NoError(t, json.Unmarshal(got, &m))

	assert.Equal(t, hash, m.GoalsMdHash)
	// DJ-141: synced_at is server-stamped, so it lands within the call window.
	assert.False(t, m.GoalsMdSyncedAt.Before(before), "synced_at must be stamped at/after the call started")
	assert.False(t, m.GoalsMdSyncedAt.After(after), "synced_at must be stamped at/before the call returned")
	assert.Equal(t, original.ProjectName, m.ProjectName, "tool must preserve project_name")
	assert.Equal(t, original.Version, m.Version, "tool must preserve version")
	assert.Equal(t, original.Model, m.Model, "tool must preserve model")
	assert.True(t, m.CreatedAt.Equal(original.CreatedAt), "tool must preserve created_at")
}

// TestSpecUpdateGoalsMdHashRejectsEmptyHash — the hash field is
// required; an empty value is a tool-level error so the playbook
// catches the omission rather than persisting a wrong-shape manifest.
func TestSpecUpdateGoalsMdHashRejectsEmptyHash(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/manifest.json", []byte(`{"project_name":"x","version":"0.1.0"}`), 0o644))

	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	server := NewSpecServer(store, nil, nil, nil, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err = server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_update_goals_md_hash",
		Arguments: map[string]any{
			"hash": "",
		},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for empty hash")
}
