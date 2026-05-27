// DJ-138 phase 2 — spec_mark_approach_drifted tool. Surface-contract
// tests covering the persistence round-trip, idempotency, and the
// kind / existence validation gates.

package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedApproach lands a clean (non-invalidated) approach into the
// store for the drift tool to mark. Pre-DJ-138, no MCP path wrote
// InvalidatedByEventID; the field was originally set only by the
// pre-DJ-135 council. seedApproach exists so the tool tests can
// start from a known-clean state without writing through MCP.
func seedApproach(t *testing.T, store *agent.SpecStore, id, parentID string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(agent.KindApproach, id, spec.Approach{
		ID:        id,
		Title:     "approach " + id,
		ParentID:  parentID,
		CreatedAt: now,
		UpdatedAt: now,
	}, agent.OriginSettled))
	require.NoError(t, store.Commit())
}

// TestSpecMarkApproachDriftedSetsField — the canonical happy path:
// call the tool, observe Approach.InvalidatedByEventID is set on
// readback.
func TestSpecMarkApproachDriftedSetsField(t *testing.T) {
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		seedFeature(store, "feat-realtime-sync", "Realtime sync")
		seedApproach(t, store, "app-realtime-loader", "feat-realtime-sync")
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_mark_approach_drifted",
		Arguments: map[string]any{
			"approach_id": "app-realtime-loader",
			"event_id":    "spec_biased-2026-05-27T00:00:00Z",
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "tool call should not error; got: %+v", res)

	got := store.GetSpec([]string{"app-realtime-loader"})
	entry := got.Results["app-realtime-loader"]
	approach, ok := entry.Body.(spec.Approach)
	require.True(t, ok, "body should be spec.Approach; got %T", entry.Body)
	assert.Equal(t, "spec_biased-2026-05-27T00:00:00Z", approach.InvalidatedByEventID)
	assert.True(t, approach.IsInvalidated())
}

// TestSpecMarkApproachDriftedIdempotent — calling twice with the
// same event id leaves the same field value. The second call is a
// no-op write (still touches updated_at, which is fine — the field
// is the audit point, not the timestamp).
func TestSpecMarkApproachDriftedIdempotent(t *testing.T) {
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		seedFeature(store, "feat-realtime-sync", "Realtime sync")
		seedApproach(t, store, "app-realtime-loader", "feat-realtime-sync")
	})

	for i := 0; i < 2; i++ {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "spec_mark_approach_drifted",
			Arguments: map[string]any{
				"approach_id": "app-realtime-loader",
				"event_id":    "spec_biased-evt-1",
			},
		})
		require.NoError(t, err)
		assert.False(t, res.IsError, "iteration %d", i)
	}

	got := store.GetSpec([]string{"app-realtime-loader"})
	approach := got.Results["app-realtime-loader"].Body.(spec.Approach)
	assert.Equal(t, "spec_biased-evt-1", approach.InvalidatedByEventID)
}

// TestSpecMarkApproachDriftedRejectsUnknownID — calling with an
// approach id that doesn't exist returns a tool-level error. The
// caller (the cascade playbook) is expected to discover approaches
// via the closure walk; an unknown id is a bug, not a soft error.
func TestSpecMarkApproachDriftedRejectsUnknownID(t *testing.T) {
	session, _ := newTestServer(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_mark_approach_drifted",
		Arguments: map[string]any{
			"approach_id": "app-not-here",
			"event_id":    "spec_biased-evt-1",
		},
	})
	require.NoError(t, err, "transport-level call should succeed")
	assert.True(t, res.IsError, "unknown approach id should surface as a tool-level error")
}

// TestSpecMarkApproachDriftedRejectsNonApproachTarget — calling
// with a decision / feature / strategy id (not an approach) is a
// kind-mismatch and surfaces as a tool-level error. Prevents the
// cascade from accidentally drift-marking a deliberation-layer
// node.
func TestSpecMarkApproachDriftedRejectsNonApproachTarget(t *testing.T) {
	session, _ := newTestServer(t, func(store *agent.SpecStore) {
		seedDecision(store, "dec-oltp-store", "Choose Postgres")
		seedFeature(store, "feat-realtime-sync", "Realtime sync", "dec-oltp-store")
	})

	cases := []string{"dec-oltp-store", "feat-realtime-sync"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "spec_mark_approach_drifted",
				Arguments: map[string]any{
					"approach_id": id,
					"event_id":    "spec_biased-evt-1",
				},
			})
			require.NoError(t, err)
			assert.True(t, res.IsError, "non-approach id %s should surface as a tool-level error", id)
		})
	}
}

// TestSpecMarkApproachDriftedRejectsEmptyEventID — an empty
// event id defeats the audit purpose of the field (the
// invalidated-by linkage). The tool rejects empty input upfront
// rather than silently writing an empty string.
func TestSpecMarkApproachDriftedRejectsEmptyEventID(t *testing.T) {
	session, _ := newTestServer(t, func(store *agent.SpecStore) {
		seedFeature(store, "feat-realtime-sync", "Realtime sync")
		seedApproach(t, store, "app-realtime-loader", "feat-realtime-sync")
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_mark_approach_drifted",
		Arguments: map[string]any{
			"approach_id": "app-realtime-loader",
			"event_id":    "",
		},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "empty event_id must surface as a tool-level error")
}
