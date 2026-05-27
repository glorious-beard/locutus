// DJ-139 phase 3 — MCP write tools for spec.Goal nodes.
//
// Covers the propose, revise, and delete surface. Propose creates
// a goal-* node; revise preserves CreatedAt and bumps UpdatedAt;
// delete removes the node from the store AND from disk AND records
// a history event so the audit trail survives the node.

package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/history"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecProposeGoalCreatesNode — propose a goal-* via MCP; confirm
// the typed spec.Goal lands in the store with the supplied fields
// and server-managed timestamps.
func TestSpecProposeGoalCreatesNode(t *testing.T) {
	session, store := newTestServer(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_goal",
		Arguments: map[string]any{
			"id":            "goal-strategic-planning-tool",
			"title":         "Strategic planning tool",
			"body":          "Locutus is a strategic planning tool for solo founders running their own software projects.",
			"source_clause": "Locutus is a strategic planning tool",
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"goal-strategic-planning-tool"})
	entry, ok := got.Results["goal-strategic-planning-tool"]
	require.True(t, ok)
	assert.Equal(t, agent.SpecGetSettled, entry.Status, "auto-commit per call promotes to settled")
	goal, ok := entry.Body.(spec.Goal)
	require.True(t, ok, "body should be spec.Goal; got %T", entry.Body)
	assert.Equal(t, "Strategic planning tool", goal.Title)
	assert.Equal(t, "Locutus is a strategic planning tool", goal.SourceClause)
	assert.False(t, goal.CreatedAt.IsZero(), "server should fill created_at")
	assert.False(t, goal.UpdatedAt.IsZero(), "server should fill updated_at")
}

// TestSpecReviseGoalPreservesCreatedAt — pre-seed a goal-* with an
// older CreatedAt, call spec_revise_goal, confirm the body updated
// AND CreatedAt is preserved AND UpdatedAt advanced.
func TestSpecReviseGoalPreservesCreatedAt(t *testing.T) {
	originalCreatedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindGoal, "goal-strategic-planning-tool", spec.Goal{
			ID:           "goal-strategic-planning-tool",
			Title:        "Original title",
			Body:         "original body",
			SourceClause: "original clause",
			CreatedAt:    originalCreatedAt,
			UpdatedAt:    originalCreatedAt,
		}, agent.OriginSettled)
		_ = store.Commit()
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_goal",
		Arguments: map[string]any{
			"id":            "goal-strategic-planning-tool",
			"title":         "Strategic planning tool",
			"body":          "Locutus is a strategic planning tool for solo founders.",
			"source_clause": "Locutus is a strategic planning tool",
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"goal-strategic-planning-tool"})
	goal, ok := got.Results["goal-strategic-planning-tool"].Body.(spec.Goal)
	require.True(t, ok)
	assert.Equal(t, "Strategic planning tool", goal.Title)
	assert.Equal(t, "Locutus is a strategic planning tool", goal.SourceClause)
	assert.True(t, goal.CreatedAt.Equal(originalCreatedAt), "revise preserves created_at")
	assert.True(t, goal.UpdatedAt.After(originalCreatedAt), "revise bumps updated_at")
}

// TestSpecReviseGoalRejectsMissingID — revise on an unknown id is a
// tool-level error; the caller should use propose to create.
func TestSpecReviseGoalRejectsMissingID(t *testing.T) {
	session, _ := newTestServer(t, nil)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_goal",
		Arguments: map[string]any{
			"id":            "goal-never-existed",
			"title":         "Title",
			"body":          "body",
			"source_clause": "clause",
		},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for missing goal")
}

// TestSpecDeleteGoalRemovesFromStoreAndDisk — seed a goal, call
// spec_delete_goal, confirm the node disappears from both the
// in-memory store and the on-disk JSON file.
func TestSpecDeleteGoalRemovesFromStoreAndDisk(t *testing.T) {
	session, store, fsys, _ := newTestServerWithHistory(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindGoal, "goal-strategic-planning-tool", spec.Goal{
			ID:           "goal-strategic-planning-tool",
			Title:        "Strategic planning tool",
			Body:         "Locutus is a strategic planning tool.",
			SourceClause: "Locutus is a strategic planning tool",
		}, agent.OriginProposed)
		_ = store.Commit()
	})

	// Sanity-check the seed landed.
	_, err := fsys.ReadFile(".borg/spec/goals/goal-strategic-planning-tool.json")
	require.NoError(t, err, "seed wrote the goal to disk")

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_delete_goal",
		Arguments: map[string]any{
			"id":     "goal-strategic-planning-tool",
			"reason": "dropped from GOALS.md in 2026-05-27 edit",
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"goal-strategic-planning-tool"})
	assert.Equal(t, agent.SpecGetMissing, got.Results["goal-strategic-planning-tool"].Status, "goal removed from store")

	_, err = fsys.ReadFile(".borg/spec/goals/goal-strategic-planning-tool.json")
	assert.Error(t, err, "goal removed from disk")
}

// TestSpecDeleteGoalRejectsUnknownID — delete on an id that doesn't
// exist returns a tool-level error so the caller learns the id was
// missing rather than treating the no-op as success.
func TestSpecDeleteGoalRejectsUnknownID(t *testing.T) {
	session, _, _, _ := newTestServerWithHistory(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_delete_goal",
		Arguments: map[string]any{
			"id":     "goal-never-existed",
			"reason": "doesn't matter",
		},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for missing goal")
}

// TestSpecDeleteGoalRecordsHistoryEvent — the delete handler writes
// a goal_deleted event carrying the supplied reason as the
// rationale. The event is the audit trail that survives the deleted
// node.
func TestSpecDeleteGoalRecordsHistoryEvent(t *testing.T) {
	session, _, _, h := newTestServerWithHistory(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindGoal, "goal-strategic-planning-tool", spec.Goal{
			ID:           "goal-strategic-planning-tool",
			Title:        "Strategic planning tool",
			Body:         "Locutus is a strategic planning tool.",
			SourceClause: "Locutus is a strategic planning tool",
		}, agent.OriginProposed)
		_ = store.Commit()
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_delete_goal",
		Arguments: map[string]any{
			"id":     "goal-strategic-planning-tool",
			"reason": "dropped from GOALS.md in 2026-05-27 edit",
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError)

	events, err := h.EventsForTarget("goal-strategic-planning-tool")
	require.NoError(t, err)
	require.Len(t, events, 1, "exactly one goal_deleted event recorded")
	assert.Equal(t, history.EventKindGoalDeleted, events[0].Kind)
	assert.Equal(t, "dropped from GOALS.md in 2026-05-27 edit", events[0].Rationale)
}

// TestSpecDeleteGoalRequiresReason — the reason field is required;
// an empty reason is rejected so the audit trail is meaningful.
func TestSpecDeleteGoalRequiresReason(t *testing.T) {
	session, _, _, _ := newTestServerWithHistory(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindGoal, "goal-x", spec.Goal{ID: "goal-x", Title: "X", Body: "b", SourceClause: "c"}, agent.OriginProposed)
		_ = store.Commit()
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_delete_goal",
		Arguments: map[string]any{
			"id":     "goal-x",
			"reason": "",
		},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "empty reason rejected")
}

// TestSpecProposeGoalRejectsMalformedID — propose with a non-goal-
// prefixed id is rejected before it reaches the store.
func TestSpecProposeGoalRejectsMalformedID(t *testing.T) {
	session, _ := newTestServer(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_goal",
		Arguments: map[string]any{
			"id":            "feat-wrong-prefix",
			"title":         "Title",
			"body":          "body",
			"source_clause": "clause",
		},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for wrong id prefix")
}

// newTestServerWithHistory mirrors newTestServer but constructs a
// Historian alongside the SpecStore so tests can assert against the
// recorded history events that the delete tools land.
func newTestServerWithHistory(t *testing.T, seed func(*agent.SpecStore)) (*mcp.ClientSession, *agent.SpecStore, specio.FS, *history.Historian) {
	t.Helper()
	ctx := context.Background()

	fsys := specio.NewMemFS()
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)
	if seed != nil {
		seed(store)
	}
	hist := history.NewHistorian(fsys, ".borg/history")

	server := NewSpecServer(store, nil, nil, hist)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	_, err = server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })

	return session, store, fsys, hist
}
