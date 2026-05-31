package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/glorious-beard/locutus/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateCompareHashes_NoRecord(t *testing.T) {
	tmp := t.TempDir()
	fsys := specio.NewOSFS(tmp)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".borg/state"), 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)
	require.NoError(t, store.Put(agent.KindFeature, "feat-foo", spec.Feature{ID: "feat-foo", Title: "Foo"}, agent.OriginSettled))
	require.NoError(t, store.Put(agent.KindApproach, "app-feat-foo", spec.Approach{
		ID: "app-feat-foo", Title: "Approach foo", ParentID: "feat-foo", Body: "x",
	}, agent.OriginSettled))

	stateStore := state.NewFileStateStore(fsys, state.DefaultStateDir)
	server := NewSpecServer(store, fsys, nil, stateStore, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, _ := server.Connect(context.Background(), serverT, nil)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, _ := client.Connect(context.Background(), clientT, nil)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "state_compare_hashes",
		Arguments: map[string]any{"approach_id": "app-feat-foo"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	body := callToolResultJSON(t, res)
	assert.Contains(t, body, `"has_record":false`)
	assert.Contains(t, body, `"app-feat-foo"`)
	assert.Contains(t, body, `"feat-foo"`)
}

func TestStateCompareHashes_DetectsChangedAndAdded(t *testing.T) {
	tmp := t.TempDir()
	fsys := specio.NewOSFS(tmp)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".borg/state"), 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)
	require.NoError(t, store.Put(agent.KindFeature, "feat-foo", spec.Feature{ID: "feat-foo", Title: "Foo"}, agent.OriginSettled))
	require.NoError(t, store.Put(agent.KindApproach, "app-feat-foo", spec.Approach{
		ID: "app-feat-foo", Title: "Approach foo", ParentID: "feat-foo", Body: "x",
	}, agent.OriginSettled))

	stateStore := state.NewFileStateStore(fsys, state.DefaultStateDir)
	// Seed a state record where one id is present with a stale hash + one extra id (renamed away).
	require.NoError(t, stateStore.Save(state.ReconciliationState{
		ApproachID: "app-feat-foo",
		Status:     state.StatusLive,
		SpecHashes: map[string]string{
			"app-feat-foo": "sha256:stale-self",   // changed (real differs)
			"feat-foo":     "sha256:stale-parent", // changed (real differs)
			"dec-old-axis": "sha256:retired",      // removed (not in current subgraph)
		},
	}))

	server := NewSpecServer(store, fsys, nil, stateStore, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, _ := server.Connect(context.Background(), serverT, nil)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, _ := client.Connect(context.Background(), clientT, nil)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "state_compare_hashes",
		Arguments: map[string]any{"approach_id": "app-feat-foo"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	body := callToolResultJSON(t, res)
	assert.Contains(t, body, `"has_record":true`)
	assert.Contains(t, body, `"changed":["app-feat-foo","feat-foo"]`)
	assert.Contains(t, body, `"removed":["dec-old-axis"]`)
}

func TestStateCompareHashes_ApproachNotFound(t *testing.T) {
	tmp := t.TempDir()
	fsys := specio.NewOSFS(tmp)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".borg/state"), 0o755))
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)
	stateStore := state.NewFileStateStore(fsys, state.DefaultStateDir)
	server := NewSpecServer(store, fsys, nil, stateStore, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, _ := server.Connect(context.Background(), serverT, nil)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, _ := client.Connect(context.Background(), clientT, nil)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "state_compare_hashes",
		Arguments: map[string]any{"approach_id": "app-missing"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error result for missing approach")
}

func TestStateRecordReconciliation_PersistsToDisk(t *testing.T) {
	tmp := t.TempDir()
	fsys := specio.NewOSFS(tmp)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".borg/spec/approaches"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".borg/spec/features"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".borg/state"), 0o755))

	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	// Seed approach + parent so ComputeSpecHashes can find bodies.
	require.NoError(t, store.Put(agent.KindFeature, "feat-foo", spec.Feature{ID: "feat-foo", Title: "Foo"}, agent.OriginSettled))
	require.NoError(t, store.Put(agent.KindApproach, "app-feat-foo", spec.Approach{
		ID: "app-feat-foo", Title: "Approach foo", ParentID: "feat-foo", Body: "x",
	}, agent.OriginSettled))

	stateStore := state.NewFileStateStore(fsys, state.DefaultStateDir)

	server := NewSpecServer(store, fsys, nil, stateStore, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, _ := server.Connect(context.Background(), serverT, nil)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, _ := client.Connect(context.Background(), clientT, nil)
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "state_record_reconciliation",
		Arguments: map[string]any{
			"approach_id":  "app-feat-foo",
			"artifacts":    map[string]any{"internal/foo/foo.go": "sha256:deadbeef"},
			"branch_name":  "adopt/001-app-feat-foo",
			"test_outcome": "passed",
			"test_command": "go test ./...",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "result: %+v", res)

	// File must have been persisted under .borg/state/app-feat-foo.yaml.
	data, err := os.ReadFile(filepath.Join(tmp, ".borg/state/app-feat-foo.yaml"))
	require.NoError(t, err)
	s := string(data)
	assert.Contains(t, s, "approach_id: app-feat-foo")
	assert.Contains(t, s, "status: live")
	assert.Contains(t, s, "spec_hashes:")
	assert.Contains(t, s, "feat-foo: sha256:") // server-computed
	assert.Contains(t, s, "artifacts:")
	assert.Contains(t, s, "internal/foo/foo.go: sha256:deadbeef")
	assert.Contains(t, s, "branch_name: adopt/001-app-feat-foo")
}
