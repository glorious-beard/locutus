// DJ-139 phase 2 — optional .advances / .respects citation fields on
// existing node kinds. These tests cover round-trip behavior through
// the MCP propose/revise surface:
//
//   - propose with advances + respects populates the typed body;
//   - propose without either field round-trips cleanly;
//   - revise updates citations on an existing node;
//   - the JSON encoding honors omitempty when both slices are empty
//     (defensive — keeps existing on-disk nodes byte-stable across a
//     read/write cycle when no citations are present).
//
// Polarity reminder for readers of this file (the canonical statement
// lives in the tool descriptions in tools_spec_write.go):
//
//   - .advances cites goal-* ids the node exists to advance (forward
//     direction);
//   - .respects cites agoal-* ids the node was checked against and
//     admitted (boundary-navigation direction).

package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecProposeFeatureAcceptsAdvancesAndRespects — propose a feature
// with both citation slices populated; read back via SpecStore.GetSpec
// and confirm the fields landed verbatim on the typed body.
func TestSpecProposeFeatureAcceptsAdvancesAndRespects(t *testing.T) {
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		seedDecision(store, "dec-storage", "Choose Postgres")
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_feature",
		Arguments: map[string]any{
			"id":        "feat-dashboard",
			"title":     "Realtime dashboard",
			"summary":   "Live tile updates over WebSocket",
			"status":    "active",
			"decisions": []string{"dec-storage"},
			"advances":  []string{"goal-multi-tenancy", "goal-realtime-visibility"},
			"respects":  []string{"agoal-fundraising"},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"feat-dashboard"})
	feature, ok := got.Results["feat-dashboard"].Body.(spec.Feature)
	require.True(t, ok, "body should be spec.Feature; got %T", got.Results["feat-dashboard"].Body)
	assert.Equal(t, []string{"goal-multi-tenancy", "goal-realtime-visibility"}, feature.Advances)
	assert.Equal(t, []string{"agoal-fundraising"}, feature.Respects)
}

// TestSpecProposeDecisionAcceptsAdvancesAndRespects — propose a
// decision with both citation slices populated; confirm round-trip.
func TestSpecProposeDecisionAcceptsAdvancesAndRespects(t *testing.T) {
	session, store := newTestServer(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{
			"id":          "dec-storage",
			"title":       "Choose Postgres for OLTP",
			"status":      "active",
			"confidence":  1.0,
			"rationale":   "Strong relational semantics and PostGIS for the geo features.",
			"axes":        []string{"storage"},
			"surfaced_by": []string{"goal-multi-tenancy"},
			"advances":    []string{"goal-multi-tenancy"},
			"respects":    []string{"agoal-fundraising"},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"dec-storage"})
	decision, ok := got.Results["dec-storage"].Body.(spec.Decision)
	require.True(t, ok)
	assert.Equal(t, []string{"goal-multi-tenancy"}, decision.Advances)
	assert.Equal(t, []string{"agoal-fundraising"}, decision.Respects)
}

// TestSpecProposeStrategyAcceptsAdvancesAndRespects — propose a
// strategy with both citation slices populated; confirm round-trip.
func TestSpecProposeStrategyAcceptsAdvancesAndRespects(t *testing.T) {
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		seedDecision(store, "dec-storage", "Choose Postgres")
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_strategy",
		Arguments: map[string]any{
			"id":        "strat-storage-platform",
			"title":     "Storage platform: Postgres + read replicas",
			"kind":      "foundational",
			"status":    "active",
			"decisions": []string{"dec-storage"},
			"advances":  []string{"goal-multi-tenancy"},
			"respects":  []string{"agoal-fundraising"},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"strat-storage-platform"})
	strategy, ok := got.Results["strat-storage-platform"].Body.(spec.Strategy)
	require.True(t, ok)
	assert.Equal(t, []string{"goal-multi-tenancy"}, strategy.Advances)
	assert.Equal(t, []string{"agoal-fundraising"}, strategy.Respects)
}

// TestSpecReviseFeatureUpdatesCitations — revise an existing feature
// to add citation fields that weren't on the original. Confirms the
// citations land and that revise still preserves CreatedAt while
// bumping UpdatedAt.
func TestSpecReviseFeatureUpdatesCitations(t *testing.T) {
	originalCreatedAt := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindDecision, "dec-storage", spec.Decision{
			ID:         "dec-storage",
			Title:      "Choose Postgres",
			Status:     spec.DecisionStatusActive,
			Confidence: 1.0,
			Rationale:  "Strong relational semantics.",
			Axes:       []string{"storage"},
			SurfacedBy: []string{"goal-multi-tenancy"},
			CreatedAt:  originalCreatedAt,
			UpdatedAt:  originalCreatedAt,
		}, agent.OriginSettled)
		_ = store.Put(agent.KindFeature, "feat-dashboard", spec.Feature{
			ID:        "feat-dashboard",
			Title:     "Original dashboard",
			Status:    spec.FeatureStatusActive,
			Decisions: []string{"dec-storage"},
			CreatedAt: originalCreatedAt,
			UpdatedAt: originalCreatedAt,
		}, agent.OriginSettled)
		_ = store.Commit()
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_feature",
		Arguments: map[string]any{
			"id":        "feat-dashboard",
			"title":     "Realtime dashboard",
			"status":    "active",
			"decisions": []string{"dec-storage"},
			"advances":  []string{"goal-realtime-visibility"},
			"respects":  []string{"agoal-fundraising"},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"feat-dashboard"})
	feature, ok := got.Results["feat-dashboard"].Body.(spec.Feature)
	require.True(t, ok)
	assert.Equal(t, []string{"goal-realtime-visibility"}, feature.Advances)
	assert.Equal(t, []string{"agoal-fundraising"}, feature.Respects)
	assert.True(t, feature.CreatedAt.Equal(originalCreatedAt), "revise preserves created_at")
	assert.True(t, feature.UpdatedAt.After(originalCreatedAt), "revise bumps updated_at")
}

// TestSpecReviseDecisionUpdatesCitations — parallel to the feature
// revise test; the citation surface is symmetric across kinds.
func TestSpecReviseDecisionUpdatesCitations(t *testing.T) {
	originalCreatedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindDecision, "dec-storage", spec.Decision{
			ID:         "dec-storage",
			Title:      "Choose Postgres",
			Status:     spec.DecisionStatusActive,
			Confidence: 1.0,
			Rationale:  "Strong relational semantics.",
			Axes:       []string{"storage"},
			SurfacedBy: []string{"goal-multi-tenancy"},
			CreatedAt:  originalCreatedAt,
			UpdatedAt:  originalCreatedAt,
		}, agent.OriginSettled)
		_ = store.Commit()
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_decision",
		Arguments: map[string]any{
			"id":          "dec-storage",
			"title":       "Choose Postgres for OLTP",
			"status":      "active",
			"confidence":  1.0,
			"rationale":   "Strong relational semantics; PostGIS for geo features.",
			"axes":        []string{"storage"},
			"surfaced_by": []string{"goal-multi-tenancy"},
			"advances":    []string{"goal-multi-tenancy"},
			"respects":    []string{"agoal-fundraising"},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"dec-storage"})
	decision, ok := got.Results["dec-storage"].Body.(spec.Decision)
	require.True(t, ok)
	assert.Equal(t, []string{"goal-multi-tenancy"}, decision.Advances)
	assert.Equal(t, []string{"agoal-fundraising"}, decision.Respects)
	assert.True(t, decision.CreatedAt.Equal(originalCreatedAt), "revise preserves created_at")
}

// TestSpecReviseStrategyUpdatesCitations — parallel to feature /
// decision revise tests.
func TestSpecReviseStrategyUpdatesCitations(t *testing.T) {
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindDecision, "dec-storage", spec.Decision{
			ID:         "dec-storage",
			Title:      "Choose Postgres",
			Status:     spec.DecisionStatusActive,
			Confidence: 1.0,
			Rationale:  "Strong relational semantics.",
			Axes:       []string{"storage"},
			SurfacedBy: []string{"goal-multi-tenancy"},
		}, agent.OriginSettled)
		_ = store.Put(agent.KindStrategy, "strat-storage-platform", spec.Strategy{
			ID:        "strat-storage-platform",
			Title:     "Storage platform",
			Kind:      spec.StrategyKindFoundational,
			Status:    "active",
			Decisions: []string{"dec-storage"},
		}, agent.OriginSettled)
		_ = store.Commit()
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_strategy",
		Arguments: map[string]any{
			"id":        "strat-storage-platform",
			"title":     "Storage platform with read replicas",
			"kind":      "foundational",
			"status":    "active",
			"decisions": []string{"dec-storage"},
			"advances":  []string{"goal-multi-tenancy"},
			"respects":  []string{"agoal-fundraising"},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"strat-storage-platform"})
	strategy, ok := got.Results["strat-storage-platform"].Body.(spec.Strategy)
	require.True(t, ok)
	assert.Equal(t, []string{"goal-multi-tenancy"}, strategy.Advances)
	assert.Equal(t, []string{"agoal-fundraising"}, strategy.Respects)
}

// TestExistingNodesWithoutCitationsRoundTripCleanly — defensive
// assertion that omitempty does what we need: a feature proposed with
// no advances/respects fields produces a JSON encoding that does NOT
// include the "advances" or "respects" keys. Existing on-disk nodes
// authored before DJ-139 stay byte-stable across a propose/read cycle.
func TestExistingNodesWithoutCitationsRoundTripCleanly(t *testing.T) {
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		seedDecision(store, "dec-storage", "Choose Postgres")
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_feature",
		Arguments: map[string]any{
			"id":        "feat-dashboard",
			"title":     "Realtime dashboard",
			"status":    "active",
			"decisions": []string{"dec-storage"},
		},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError)

	got := store.GetSpec([]string{"feat-dashboard"})
	feature, ok := got.Results["feat-dashboard"].Body.(spec.Feature)
	require.True(t, ok)
	assert.Nil(t, feature.Advances, "Advances should be nil when not supplied")
	assert.Nil(t, feature.Respects, "Respects should be nil when not supplied")

	// Confirm the JSON encoding honors omitempty so the on-disk
	// representation doesn't gain garbage fields.
	encoded, err := json.Marshal(feature)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"advances"`, "JSON should omit advances when empty; got %s", encoded)
	assert.NotContains(t, string(encoded), `"respects"`, "JSON should omit respects when empty; got %s", encoded)
}
