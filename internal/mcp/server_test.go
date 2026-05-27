package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

// newTestServer builds an in-memory SpecStore and connects it to an
// in-memory MCP client. Returns the connected client session and the
// underlying store so test bodies can either drive the server via
// tool calls (the agent-facing surface we're proving out) or assert
// against the store directly (the implementation we're protecting
// the contract for).
//
// The store is empty unless the seed callback populates it. Tests
// that want a populated graph supply a seed that puts entries; tests
// that exercise propose/revise tools leave the store empty and let
// the tool call do the writing.
func newTestServer(t *testing.T, seed func(*agent.SpecStore)) (*mcp.ClientSession, *agent.SpecStore) {
	t.Helper()
	ctx := context.Background()

	fsys := specio.NewMemFS()
	store, err := agent.NewSpecStore(fsys)
	assert.NoError(t, err)
	if seed != nil {
		seed(store)
	}

	server := NewSpecServer(store, nil, nil, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	_, err = server.Connect(ctx, serverTransport, nil)
	assert.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	assert.NoError(t, err)

	t.Cleanup(func() { session.Close() })
	return session, store
}

// seedDecision is a helper for seed callbacks. The MCP write tools
// build their own bodies; this helper exists so the read-tool tests
// can populate the store directly without going through the MCP
// surface.
func seedDecision(store *agent.SpecStore, id, title string) {
	now := time.Now().UTC()
	_ = store.Begin()
	_ = store.Put(agent.KindDecision, id, spec.Decision{
		ID:         id,
		Title:      title,
		Summary:    "summary of " + id,
		Status:     spec.DecisionStatusActive,
		Confidence: 1.0,
		Rationale:  "rationale for " + id,
		Axes:       []string{id[len("dec-"):]},
		SurfacedBy: []string{"goal-test"},
		CreatedAt:  now,
		UpdatedAt:  now,
	}, agent.OriginSettled)
	_ = store.Commit()
}

func seedFeature(store *agent.SpecStore, id, title string, deps ...string) {
	now := time.Now().UTC()
	_ = store.Begin()
	_ = store.Put(agent.KindFeature, id, spec.Feature{
		ID:        id,
		Title:     title,
		Summary:   "summary of " + id,
		Status:    spec.FeatureStatusActive,
		Decisions: deps,
		CreatedAt: now,
		UpdatedAt: now,
	}, agent.OriginSettled)
	_ = store.Commit()
}

func TestSpecServer_RegistersAllReadTools(t *testing.T) {
	session, _ := newTestServer(t, nil)
	res, err := session.ListTools(context.Background(), nil)
	assert.NoError(t, err)

	names := toolNames(res.Tools)
	assert.Contains(t, names, "spec_list_manifest")
	assert.Contains(t, names, "spec_get")
	assert.Contains(t, names, "spec_search")
}

func TestSpecServer_RegistersAllWriteTools(t *testing.T) {
	session, _ := newTestServer(t, nil)
	res, err := session.ListTools(context.Background(), nil)
	assert.NoError(t, err)

	names := toolNames(res.Tools)
	assert.Contains(t, names, "spec_propose_decision")
	assert.Contains(t, names, "spec_propose_feature")
	assert.Contains(t, names, "spec_propose_strategy")
	assert.Contains(t, names, "spec_revise_decision")
	assert.Contains(t, names, "spec_revise_feature")
	assert.Contains(t, names, "spec_revise_strategy")
	// DJ-139 phase 3: goal-layer write surface.
	assert.Contains(t, names, "spec_propose_goal")
	assert.Contains(t, names, "spec_revise_goal")
	assert.Contains(t, names, "spec_delete_goal")
	assert.Contains(t, names, "spec_propose_antigoal")
	assert.Contains(t, names, "spec_revise_antigoal")
	assert.Contains(t, names, "spec_delete_antigoal")
}

func TestSpecServer_SpecListManifest_ReturnsAllKinds(t *testing.T) {
	session, _ := newTestServer(t, func(store *agent.SpecStore) {
		seedDecision(store, "dec-storage", "Choose Postgres for OLTP")
		seedFeature(store, "feat-dashboard", "Realtime dashboard", "dec-storage")
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_list_manifest",
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError, "manifest call should not be an error")
	assert.NotNil(t, res.StructuredContent)

	manifestJSON, err := json.Marshal(res.StructuredContent)
	assert.NoError(t, err)

	var manifest agent.SpecManifest
	assert.NoError(t, json.Unmarshal(manifestJSON, &manifest))
	assert.Len(t, manifest.Decisions, 1)
	assert.Len(t, manifest.Features, 1)
	assert.Equal(t, "dec-storage", manifest.Decisions[0].ID)
	assert.Equal(t, "feat-dashboard", manifest.Features[0].ID)
	assert.Equal(t, agent.OriginSettled, manifest.Decisions[0].Origin)
}

// TestSpecListManifestToolReturnsGoalsMdHash — DJ-139 LB-1. The Phase 6
// refine-goals orchestrator reads the manifest via spec_list_manifest
// to discover the persisted hash + synced_at; without those fields on
// the wire the short-circuit signal is unreachable and the matcher
// dispatches every run even when GOALS.md is unchanged.
func TestSpecListManifestToolReturnsGoalsMdHash(t *testing.T) {
	ctx := context.Background()

	fsys := specio.NewMemFS()
	// Seed a manifest with the hash fields populated so the tool has
	// something to surface.
	manifest := spec.Manifest{
		ProjectName:     "locutus",
		Version:         "0.1.0",
		CreatedAt:       time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC),
		GoalsMdHash:     "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		GoalsMdSyncedAt: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(manifest)
	assert.NoError(t, err)
	assert.NoError(t, fsys.MkdirAll(".borg", 0o755))
	assert.NoError(t, fsys.WriteFile(".borg/manifest.json", data, 0o644))

	store, err := agent.NewSpecStore(fsys)
	assert.NoError(t, err)

	server := NewSpecServer(store, nil, nil, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err = server.Connect(ctx, serverTransport, nil)
	assert.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	assert.NoError(t, err)
	t.Cleanup(func() { session.Close() })

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "spec_list_manifest"})
	assert.NoError(t, err)
	assert.False(t, res.IsError)
	assert.NotNil(t, res.StructuredContent)

	manifestJSON, err := json.Marshal(res.StructuredContent)
	assert.NoError(t, err)

	var got agent.SpecManifest
	assert.NoError(t, json.Unmarshal(manifestJSON, &got))
	assert.Equal(t, manifest.GoalsMdHash, got.GoalsMdHash, "tool must surface goals_md_hash for the Phase 6 short-circuit signal")
	assert.True(t, got.GoalsMdSyncedAt.Equal(manifest.GoalsMdSyncedAt), "tool must surface goals_md_synced_at")
}

func TestSpecServer_SpecGet_BatchedResults(t *testing.T) {
	session, _ := newTestServer(t, func(store *agent.SpecStore) {
		seedDecision(store, "dec-storage", "Choose Postgres")
		seedDecision(store, "dec-auth", "Use Auth0")
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_get",
		Arguments: map[string]any{"ids": []string{"dec-storage", "dec-auth", "dec-missing"}},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	raw, err := json.Marshal(res.StructuredContent)
	assert.NoError(t, err)

	var got agent.SpecGetResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Len(t, got.Results, 3)
	assert.Equal(t, agent.SpecGetSettled, got.Results["dec-storage"].Status)
	assert.Equal(t, agent.SpecGetSettled, got.Results["dec-auth"].Status)
	assert.Equal(t, agent.SpecGetMissing, got.Results["dec-missing"].Status)
	assert.NotEmpty(t, got.AvailableIDs[agent.KindDecision], "miss should surface available decision ids")
}

func TestSpecServer_SpecGet_RejectsEmptyIDs(t *testing.T) {
	session, _ := newTestServer(t, nil)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_get",
		Arguments: map[string]any{"ids": []string{}},
	})
	// The handler returns an error; the SDK surfaces it as a non-nil
	// err on the client side OR as a tool-level error on the result.
	// Either is acceptable signaling — assert that the call did not
	// silently succeed.
	if err == nil {
		assert.True(t, res.IsError, "expected error response or non-nil err for empty ids")
	}
}

func TestSpecServer_SpecSearch_ReturnsHits(t *testing.T) {
	session, _ := newTestServer(t, func(store *agent.SpecStore) {
		seedDecision(store, "dec-storage", "Choose Postgres for OLTP")
		seedFeature(store, "feat-dashboard", "Realtime dashboard with live tiles", "dec-storage")
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_search",
		Arguments: map[string]any{"query": "dashboard"},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	raw, err := json.Marshal(res.StructuredContent)
	assert.NoError(t, err)
	var out specSearchOutput
	assert.NoError(t, json.Unmarshal(raw, &out))
	assert.NotEmpty(t, out.Hits, "search for 'dashboard' should match the feature")
	assert.Equal(t, "feat-dashboard", out.Hits[0].ID)
}

func TestSpecServer_SpecProposeDecision_WritesToSpecStore(t *testing.T) {
	session, store := newTestServer(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{
			"id":          "dec-storage",
			"title":       "Choose Postgres for OLTP",
			"summary":     "Postgres 16 with PostGIS",
			"status":      "active",
			"confidence":  1.0,
			"rationale":   "Strong relational semantics and PostGIS for the geo features.",
			"axes":        []string{"storage"},
			"surfaced_by": []string{"goal-multi-tenancy"},
		},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"dec-storage"})
	entry, ok := got.Results["dec-storage"]
	assert.True(t, ok)
	assert.Equal(t, agent.SpecGetSettled, entry.Status, "auto-commit per call promotes to settled")
	decision, ok := entry.Body.(spec.Decision)
	assert.True(t, ok, "body should be spec.Decision; got %T", entry.Body)
	assert.Equal(t, "Choose Postgres for OLTP", decision.Title)
	assert.Equal(t, spec.DecisionStatusActive, decision.Status)
	assert.False(t, decision.CreatedAt.IsZero(), "server should fill created_at")
	assert.False(t, decision.UpdatedAt.IsZero(), "server should fill updated_at")
}

func TestSpecServer_SpecProposeFeature_WritesToSpecStore(t *testing.T) {
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
		},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	got := store.GetSpec([]string{"feat-dashboard"})
	feature, ok := got.Results["feat-dashboard"].Body.(spec.Feature)
	assert.True(t, ok)
	assert.Equal(t, "Realtime dashboard", feature.Title)
	assert.Equal(t, []string{"dec-storage"}, feature.Decisions)
}

func TestSpecServer_SpecProposeStrategy_WritesToSpecStore(t *testing.T) {
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		seedDecision(store, "dec-storage", "Choose Postgres")
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_strategy",
		Arguments: map[string]any{
			"id":        "strat-storage-platform",
			"title":     "Storage platform: Postgres + read replicas",
			"summary":   "Single primary, two read replicas, no sharding for v1",
			"kind":      "foundational",
			"status":    "active",
			"decisions": []string{"dec-storage"},
		},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	got := store.GetSpec([]string{"strat-storage-platform"})
	strategy, ok := got.Results["strat-storage-platform"].Body.(spec.Strategy)
	assert.True(t, ok)
	assert.Equal(t, spec.StrategyKindFoundational, strategy.Kind)
}

func TestSpecServer_SpecReviseDecision_RewritesExisting(t *testing.T) {
	originalCreatedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindDecision, "dec-storage", spec.Decision{
			ID:         "dec-storage",
			Title:      "Original title",
			Summary:    "summary",
			Status:     spec.DecisionStatusActive,
			Confidence: 1.0,
			Rationale:  "original rationale",
			Axes:       []string{"storage"},
			SurfacedBy: []string{"goal-test"},
			CreatedAt:  originalCreatedAt,
			UpdatedAt:  originalCreatedAt,
		}, agent.OriginSettled)
		_ = store.Commit()
	})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_decision",
		Arguments: map[string]any{
			"id":          "dec-storage",
			"title":       "Revised title",
			"summary":     "revised summary",
			"status":      "active",
			"confidence":  0.9,
			"rationale":   "revised rationale citing new evidence",
			"axes":        []string{"storage"},
			"surfaced_by": []string{"goal-test"},
		},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError)

	got := store.GetSpec([]string{"dec-storage"})
	decision := got.Results["dec-storage"].Body.(spec.Decision)
	assert.Equal(t, "Revised title", decision.Title)
	assert.Equal(t, "revised rationale citing new evidence", decision.Rationale)
	assert.True(t, decision.CreatedAt.Equal(originalCreatedAt), "revise preserves created_at; got %v expected %v", decision.CreatedAt, originalCreatedAt)
	assert.True(t, decision.UpdatedAt.After(originalCreatedAt), "revise bumps updated_at to now")
}

func TestSpecServer_SpecReviseDecision_RejectsMissingID(t *testing.T) {
	session, _ := newTestServer(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_decision",
		Arguments: map[string]any{
			"id":          "dec-nonexistent",
			"title":       "Title",
			"status":      "active",
			"confidence":  1.0,
			"rationale":   "rationale",
			"axes":        []string{"nonexistent"},
			"surfaced_by": []string{"goal-test"},
		},
	})
	assert.NoError(t, err, "tool should respond, not fail at the transport level")
	assert.True(t, res.IsError, "expected tool-level error for missing decision")
}

func TestSpecServer_SpecReviseFeature_RewritesExisting(t *testing.T) {
	// Mirror the revise_decision test: pre-seed a feature with a known
	// created_at, call spec_revise_feature, confirm the body changed
	// AND created_at is preserved AND updated_at advanced.
	originalCreatedAt := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindDecision, "dec-storage", spec.Decision{
			ID:         "dec-storage",
			Title:      "Choose Postgres",
			Summary:    "summary",
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
			Title:     "Original title",
			Summary:   "original summary",
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
			"id":          "feat-dashboard",
			"title":       "Revised title",
			"summary":     "revised summary",
			"status":      "active",
			"description": "Description now reflects the revised storage decision body.",
			"decisions":   []string{"dec-storage"},
		},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"feat-dashboard"})
	feature, ok := got.Results["feat-dashboard"].Body.(spec.Feature)
	assert.True(t, ok)
	assert.Equal(t, "Revised title", feature.Title)
	assert.Equal(t, "Description now reflects the revised storage decision body.", feature.Description)
	assert.True(t, feature.CreatedAt.Equal(originalCreatedAt), "revise preserves created_at")
	assert.True(t, feature.UpdatedAt.After(originalCreatedAt), "revise bumps updated_at")
}

func TestSpecServer_SpecReviseFeature_RejectsMissingID(t *testing.T) {
	session, _ := newTestServer(t, nil)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_feature",
		Arguments: map[string]any{
			"id":        "feat-nonexistent",
			"title":     "Title",
			"status":    "active",
			"decisions": []string{"dec-x"},
		},
	})
	assert.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for missing feature")
}

func TestSpecServer_SpecReviseStrategy_RewritesExisting(t *testing.T) {
	session, store := newTestServer(t, func(store *agent.SpecStore) {
		_ = store.Begin()
		_ = store.Put(agent.KindDecision, "dec-storage", spec.Decision{
			ID:         "dec-storage",
			Title:      "Choose Postgres",
			Status:     spec.DecisionStatusActive,
			Confidence: 1.0,
			Rationale:  "Strong relational semantics.",
			Axes:       []string{"storage"},
			SurfacedBy: []string{"goal-test"},
		}, agent.OriginSettled)
		_ = store.Put(agent.KindStrategy, "strat-storage-platform", spec.Strategy{
			ID:        "strat-storage-platform",
			Title:     "Original storage platform",
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
			"title":     "Revised storage platform",
			"summary":   "Now with read replicas",
			"kind":      "foundational",
			"status":    "active",
			"decisions": []string{"dec-storage"},
		},
	})
	assert.NoError(t, err)
	assert.False(t, res.IsError, "expected success; got: %+v", res)

	got := store.GetSpec([]string{"strat-storage-platform"})
	strategy, ok := got.Results["strat-storage-platform"].Body.(spec.Strategy)
	assert.True(t, ok)
	assert.Equal(t, "Revised storage platform", strategy.Title)
	assert.Equal(t, "Now with read replicas", strategy.Summary)
}

func TestSpecServer_SpecReviseStrategy_RejectsMissingID(t *testing.T) {
	session, _ := newTestServer(t, nil)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_strategy",
		Arguments: map[string]any{
			"id":        "strat-nonexistent",
			"title":     "Title",
			"kind":      "foundational",
			"status":    "active",
			"decisions": []string{"dec-x"},
		},
	})
	assert.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for missing strategy")
}

func TestSpecServer_SpecProposeDecision_RejectsWrongIDPrefix(t *testing.T) {
	session, _ := newTestServer(t, nil)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{
			"id":          "feat-wrong-prefix", // should be dec-
			"title":       "Title",
			"status":      "active",
			"confidence":  1.0,
			"rationale":   "rationale",
			"axes":        []string{"wrong"},
			"surfaced_by": []string{"goal-test"},
		},
	})
	assert.NoError(t, err)
	assert.True(t, res.IsError, "expected tool-level error for wrong id prefix")
}

func toolNames(tools []*mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}
