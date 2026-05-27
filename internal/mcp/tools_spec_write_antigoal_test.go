// DJ-139 phase 3 — MCP write tools for spec.AntiGoal nodes.
//
// Mirrors tools_spec_write_goal_test.go for the agoal- surface.
// AntiGoals additionally carry CededTo and KeptIn slice fields; the
// propose/revise tests cover round-trip on those alongside the
// common id/title/body/source_clause set.

package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/history"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecProposeAntiGoalCreatesNode — propose an agoal-* via MCP;
// confirm the typed spec.AntiGoal lands with CededTo and KeptIn
// round-tripped alongside the common fields.
func TestSpecProposeAntiGoalCreatesNode(t *testing.T) {
	session, store := newTestServer(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_antigoal",
		Arguments: map[string]any{
			"id":            "agoal-fundraising",
			"title":         "Fundraising tracking",
			"body":          "Locutus does not track fundraising rounds, investor relationships, or cap table state.",
			"source_clause": "Fundraising tracking is out of scope",
			"ceded_to":      []string{"Carta", "AngelList"},
			"kept_in":       []string{"runway forecasting for product timeline planning"},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"agoal-fundraising"})
	entry, ok := got.Results["agoal-fundraising"]
	require.True(t, ok)
	assert.Equal(t, agent.SpecGetSettled, entry.Status)
	ag, ok := entry.Body.(spec.AntiGoal)
	require.True(t, ok, "body should be spec.AntiGoal; got %T", entry.Body)
	assert.Equal(t, "Fundraising tracking", ag.Title)
	assert.Equal(t, "Fundraising tracking is out of scope", ag.SourceClause)
	assert.Equal(t, []string{"Carta", "AngelList"}, ag.CededTo)
	assert.Equal(t, []string{"runway forecasting for product timeline planning"}, ag.KeptIn)
	assert.False(t, ag.CreatedAt.IsZero(), "server should fill created_at")
	assert.False(t, ag.UpdatedAt.IsZero(), "server should fill updated_at")
}

// TestSpecReviseAntiGoalPreservesCreatedAt — pre-seed an agoal- with
// older timestamps, revise it, confirm CreatedAt preserved and
// UpdatedAt advanced; CededTo and KeptIn replace wholesale.
func TestSpecReviseAntiGoalPreservesCreatedAt(t *testing.T) {
	originalCreatedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindAntiGoal, "agoal-fundraising", spec.AntiGoal{
			ID:           "agoal-fundraising",
			Title:        "Original title",
			Body:         "original body",
			SourceClause: "original clause",
			CededTo:      []string{"Carta"},
			CreatedAt:    originalCreatedAt,
			UpdatedAt:    originalCreatedAt,
		}, agent.OriginSettled)
		_ = store.Commit()
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_antigoal",
		Arguments: map[string]any{
			"id":            "agoal-fundraising",
			"title":         "Fundraising tracking",
			"body":          "Locutus does not track fundraising rounds.",
			"source_clause": "Fundraising tracking is out of scope",
			"ceded_to":      []string{"Carta", "AngelList"},
			"kept_in":       []string{"runway forecasting for product timeline planning"},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"agoal-fundraising"})
	ag, ok := got.Results["agoal-fundraising"].Body.(spec.AntiGoal)
	require.True(t, ok)
	assert.Equal(t, "Fundraising tracking", ag.Title)
	assert.Equal(t, []string{"Carta", "AngelList"}, ag.CededTo)
	assert.Equal(t, []string{"runway forecasting for product timeline planning"}, ag.KeptIn)
	assert.True(t, ag.CreatedAt.Equal(originalCreatedAt), "revise preserves created_at")
	assert.True(t, ag.UpdatedAt.After(originalCreatedAt), "revise bumps updated_at")
}

// TestSpecReviseAntiGoalRejectsMissingID — revise on an unknown id
// surfaces a tool-level error.
func TestSpecReviseAntiGoalRejectsMissingID(t *testing.T) {
	session, _ := newTestServer(t, nil)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_antigoal",
		Arguments: map[string]any{
			"id":            "agoal-never-existed",
			"title":         "Title",
			"body":          "body",
			"source_clause": "clause",
		},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for missing antigoal")
}

// TestSpecDeleteAntiGoalRemovesFromStoreAndDisk — seed an agoal-,
// call spec_delete_antigoal, confirm the node disappears from both
// the in-memory store and the on-disk JSON file.
func TestSpecDeleteAntiGoalRemovesFromStoreAndDisk(t *testing.T) {
	session, store, fsys, _ := newTestServerWithHistory(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindAntiGoal, "agoal-fundraising", spec.AntiGoal{
			ID:           "agoal-fundraising",
			Title:        "Fundraising tracking",
			Body:         "Out of scope.",
			SourceClause: "Fundraising tracking is out of scope",
		}, agent.OriginProposed)
		_ = store.Commit()
	})

	_, err := fsys.ReadFile(".borg/spec/antigoals/agoal-fundraising.json")
	require.NoError(t, err, "seed wrote the antigoal to disk")

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_delete_antigoal",
		Arguments: map[string]any{
			"id":     "agoal-fundraising",
			"reason": "the fundraising carve-out was lifted; no longer out-of-scope",
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"agoal-fundraising"})
	assert.Equal(t, agent.SpecGetMissing, got.Results["agoal-fundraising"].Status)

	_, err = fsys.ReadFile(".borg/spec/antigoals/agoal-fundraising.json")
	assert.Error(t, err, "antigoal removed from disk")
}

// TestSpecDeleteAntiGoalRejectsUnknownID — delete on an unknown id
// returns a tool-level error.
func TestSpecDeleteAntiGoalRejectsUnknownID(t *testing.T) {
	session, _, _, _ := newTestServerWithHistory(t, nil)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_delete_antigoal",
		Arguments: map[string]any{
			"id":     "agoal-never-existed",
			"reason": "doesn't matter",
		},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for missing antigoal")
}

// TestSpecDeleteAntiGoalRecordsHistoryEvent — the delete handler
// writes an antigoal_deleted event with the supplied reason. The
// event is the audit trail that survives the deleted node.
func TestSpecDeleteAntiGoalRecordsHistoryEvent(t *testing.T) {
	session, _, _, h := newTestServerWithHistory(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindAntiGoal, "agoal-fundraising", spec.AntiGoal{
			ID:           "agoal-fundraising",
			Title:        "Fundraising tracking",
			Body:         "Out of scope.",
			SourceClause: "Fundraising tracking is out of scope",
		}, agent.OriginProposed)
		_ = store.Commit()
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_delete_antigoal",
		Arguments: map[string]any{
			"id":     "agoal-fundraising",
			"reason": "the fundraising carve-out was lifted; no longer out-of-scope",
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError)

	events, err := h.EventsForTarget("agoal-fundraising")
	require.NoError(t, err)
	require.Len(t, events, 1, "exactly one antigoal_deleted event recorded")
	assert.Equal(t, history.EventKindAntiGoalDeleted, events[0].Kind)
	assert.Equal(t, "the fundraising carve-out was lifted; no longer out-of-scope", events[0].Rationale)
}

// TestSpecProposeAntiGoalRejectsMalformedID — propose with a
// non-agoal- prefixed id is rejected before it reaches the store.
func TestSpecProposeAntiGoalRejectsMalformedID(t *testing.T) {
	session, _ := newTestServer(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_antigoal",
		Arguments: map[string]any{
			"id":            "goal-wrong-prefix",
			"title":         "Title",
			"body":          "body",
			"source_clause": "clause",
		},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for wrong id prefix")
}
