// DJ-147 Task 5 — every mutation MCP tool, called from a dry-run
// session, must capture rather than persist. Verified end-to-end via
// the in-memory transport pair: call the tool, then assert the
// SpecStore's overlay has the entry (and base store / disk does not).
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/activity"
	"github.com/glorious-beard/locutus/internal/agent"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dryRunHarness wires a fresh server + client transport pair and flips
// dry-run on for the server-side session. Returns the (client session,
// server session, store, fsys) the caller exercises against.
func dryRunHarness(t *testing.T) (*mcp.ClientSession, *mcp.ServerSession, *agent.SpecStore, specio.FS) {
	t.Helper()
	clearSessionRuntimes()
	fsys := specio.NewMemFS()

	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)

	reg, err := activity.NewRegistry(fsys)
	require.NoError(t, err)

	server := NewSpecServer(store, fsys, reg, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	// Wait for InitializedHandler to land the runtime tuple.
	for d := time.Now().Add(time.Second); time.Now().Before(d); {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Flip dry-run on directly (the SDK client can't inject _meta in
	// tests on go-sdk v1.6.1; matches the DJ-143 Task 3 pattern).
	storeSessionContext(ss, "claude-code", "headless", true, "markdown")
	store.RegisterOverlay(ss)
	t.Cleanup(func() { store.UnregisterOverlay(ss) })
	return cs, ss, store, fsys
}

// TestDryRunCapturesProposeAndRevise drives every propose/revise/delete
// tool through the in-memory transport and asserts the would-be
// mutation lands in the overlay, not the base store / disk.
func TestDryRunCapturesProposeAndRevise(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]any
		kind agent.SpecKind
		id   string
	}{
		{"propose_decision", "spec_propose_decision",
			map[string]any{
				"id":         "dec-foo",
				"title":      "Foo",
				"status":     "proposed",
				"confidence": 1.0,
				"rationale":  "because reasons",
			},
			agent.KindDecision, "dec-foo"},
		{"propose_feature", "spec_propose_feature",
			map[string]any{
				"id":        "feat-foo",
				"title":     "Foo",
				"status":    "proposed",
				"decisions": []any{"dec-foo"},
			},
			agent.KindFeature, "feat-foo"},
		{"propose_strategy", "spec_propose_strategy",
			map[string]any{
				"id":        "strat-foo",
				"title":     "Foo",
				"kind":      "foundational",
				"status":    "proposed",
				"decisions": []any{"dec-foo"},
			},
			agent.KindStrategy, "strat-foo"},
		{"propose_goal", "spec_propose_goal",
			map[string]any{
				"id":            "goal-foo",
				"title":         "Foo",
				"body":          "in scope",
				"source_clause": "verbatim excerpt",
			},
			agent.KindGoal, "goal-foo"},
		{"propose_antigoal", "spec_propose_antigoal",
			map[string]any{
				"id":            "agoal-foo",
				"title":         "Foo",
				"body":          "out of scope",
				"source_clause": "verbatim excerpt",
			},
			agent.KindAntiGoal, "agoal-foo"},
		{"propose_approach", "spec_propose_approach",
			map[string]any{
				"id":           "app-feat-foo",
				"title":        "Foo approach",
				"parent_id":    "feat-foo",
				"body":         "## Implementation\n\nThe foo flow.",
				"source_files": []string{"internal/foo/foo.go"},
				"source_hash":  "sha256:deadbeef00",
			},
			agent.KindApproach, "app-feat-foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs, ss, store, fsys := dryRunHarness(t)

			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      tc.tool,
				Arguments: tc.args,
			})
			require.NoError(t, err)
			require.False(t, res.IsError, "tool result: %+v", res)

			caps := store.OverlayCaptured(ss)
			require.Len(t, caps, 1)
			assert.Equal(t, tc.tool, caps[0].Tool)
			assert.Equal(t, tc.kind, caps[0].Kind)
			assert.Equal(t, tc.id, caps[0].ID)

			// Overlay-view surfaces the captured entry.
			view := store.OverlayView(ss)
			entry, ok := view.Lookup(tc.kind, tc.id)
			require.True(t, ok, "overlay view must surface the captured entry")
			require.NotNil(t, entry)

			// Base store must NOT hold the entry.
			_, baseOK := store.OverlayView(nil).Lookup(tc.kind, tc.id)
			assert.False(t, baseOK, "base store must not hold a captured-only entry")

			// Disk must NOT hold the JSON file.
			diskPath := diskPathFor(tc.kind, tc.id)
			_, err = fsys.ReadFile(diskPath)
			assert.True(t, errors.Is(err, fs.ErrNotExist), "no JSON file should land on disk in dry-run; path=%s err=%v", diskPath, err)
		})
	}
}

// TestDryRunCapturesRevise seeds a settled entry first, then revises
// it under dry-run and asserts the revision lands in the overlay while
// the on-disk JSON keeps its original body.
func TestDryRunCapturesRevise(t *testing.T) {
	cases := []struct {
		seedKind agent.SpecKind
		seedID   string
		seedBody any
		tool     string
		args     map[string]any
		kind     agent.SpecKind
		id       string
	}{
		{
			seedKind: agent.KindDecision,
			seedID:   "dec-foo",
			seedBody: spec.Decision{ID: "dec-foo", Title: "Old", Status: spec.DecisionStatus("active"), Confidence: 1.0, Rationale: "old", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			tool:     "spec_revise_decision",
			args: map[string]any{
				"id": "dec-foo", "title": "New", "status": "active", "confidence": 1.0, "rationale": "new",
			},
			kind: agent.KindDecision, id: "dec-foo",
		},
		{
			seedKind: agent.KindFeature,
			seedID:   "feat-foo",
			seedBody: spec.Feature{ID: "feat-foo", Title: "Old", Status: spec.FeatureStatus("active"), Decisions: []string{"dec-foo"}, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			tool:     "spec_revise_feature",
			args: map[string]any{
				"id": "feat-foo", "title": "New", "status": "active", "decisions": []any{"dec-foo"},
			},
			kind: agent.KindFeature, id: "feat-foo",
		},
		{
			seedKind: agent.KindStrategy,
			seedID:   "strat-foo",
			seedBody: spec.Strategy{ID: "strat-foo", Title: "Old", Kind: spec.StrategyKind("foundational"), Status: "active", Decisions: []string{"dec-foo"}},
			tool:     "spec_revise_strategy",
			args: map[string]any{
				"id": "strat-foo", "title": "New", "kind": "foundational", "status": "active", "decisions": []any{"dec-foo"},
			},
			kind: agent.KindStrategy, id: "strat-foo",
		},
		{
			seedKind: agent.KindGoal,
			seedID:   "goal-foo",
			seedBody: spec.Goal{ID: "goal-foo", Title: "Old", Body: "old", SourceClause: "old clause", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			tool:     "spec_revise_goal",
			args: map[string]any{
				"id": "goal-foo", "title": "New", "body": "new", "source_clause": "new clause",
			},
			kind: agent.KindGoal, id: "goal-foo",
		},
		{
			seedKind: agent.KindAntiGoal,
			seedID:   "agoal-foo",
			seedBody: spec.AntiGoal{ID: "agoal-foo", Title: "Old", Body: "old", SourceClause: "old clause", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			tool:     "spec_revise_antigoal",
			args: map[string]any{
				"id": "agoal-foo", "title": "New", "body": "new", "source_clause": "new clause",
			},
			kind: agent.KindAntiGoal, id: "agoal-foo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			cs, ss, store, fsys := dryRunHarness(t)

			// Seed the base store before flipping dry-run on the call
			// path. OriginProposed so persistLocked writes the seeded
			// body to disk; the dry-run revise must leave that file
			// untouched.
			require.NoError(t, store.Begin())
			require.NoError(t, store.Put(tc.seedKind, tc.seedID, tc.seedBody, agent.OriginProposed))
			require.NoError(t, store.Commit())

			// Capture the on-disk bytes for the seeded entry to compare
			// against post-call.
			before, err := fsys.ReadFile(diskPathFor(tc.kind, tc.id))
			require.NoError(t, err)

			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      tc.tool,
				Arguments: tc.args,
			})
			require.NoError(t, err)
			require.False(t, res.IsError, "tool result: %+v", res)

			caps := store.OverlayCaptured(ss)
			require.Len(t, caps, 1)
			assert.Equal(t, tc.tool, caps[0].Tool)
			assert.Equal(t, tc.kind, caps[0].Kind)
			assert.Equal(t, tc.id, caps[0].ID)

			// Overlay-view surfaces the revised body (not the seeded one).
			view := store.OverlayView(ss)
			entry, ok := view.Lookup(tc.kind, tc.id)
			require.True(t, ok)
			require.NotNil(t, entry)

			// On-disk bytes unchanged.
			after, err := fsys.ReadFile(diskPathFor(tc.kind, tc.id))
			require.NoError(t, err)
			assert.Equal(t, before, after, "dry-run revise must not touch disk")
		})
	}
}

// TestDryRunCapturesDeletes asserts the delete tools capture rather
// than remove the on-disk entry.
func TestDryRunCapturesDeletes(t *testing.T) {
	cases := []struct {
		seedKind agent.SpecKind
		seedID   string
		seedBody any
		tool     string
		args     map[string]any
		kind     agent.SpecKind
		id       string
	}{
		{
			seedKind: agent.KindGoal,
			seedID:   "goal-foo",
			seedBody: spec.Goal{ID: "goal-foo", Title: "Foo", Body: "in scope", SourceClause: "clause"},
			tool:     "spec_delete_goal",
			args:     map[string]any{"id": "goal-foo", "reason": "dropped from GOALS.md"},
			kind:     agent.KindGoal, id: "goal-foo",
		},
		{
			seedKind: agent.KindAntiGoal,
			seedID:   "agoal-foo",
			seedBody: spec.AntiGoal{ID: "agoal-foo", Title: "Foo", Body: "out", SourceClause: "clause"},
			tool:     "spec_delete_antigoal",
			args:     map[string]any{"id": "agoal-foo", "reason": "lifted from GOALS.md"},
			kind:     agent.KindAntiGoal, id: "agoal-foo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			cs, ss, store, fsys := dryRunHarness(t)

			require.NoError(t, store.Begin())
			require.NoError(t, store.Put(tc.seedKind, tc.seedID, tc.seedBody, agent.OriginProposed))
			require.NoError(t, store.Commit())

			_, err := fsys.ReadFile(diskPathFor(tc.kind, tc.id))
			require.NoError(t, err)

			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      tc.tool,
				Arguments: tc.args,
			})
			require.NoError(t, err)
			require.False(t, res.IsError, "tool result: %+v", res)

			caps := store.OverlayCaptured(ss)
			require.Len(t, caps, 1)
			assert.Equal(t, tc.tool, caps[0].Tool)
			assert.Equal(t, tc.kind, caps[0].Kind)
			assert.Equal(t, tc.id, caps[0].ID)

			// Overlay-view masks the entry — deleted in this session.
			view := store.OverlayView(ss)
			_, ok := view.Lookup(tc.kind, tc.id)
			assert.False(t, ok, "overlay view must mask the deleted entry")

			// On-disk file still exists.
			_, err = fsys.ReadFile(diskPathFor(tc.kind, tc.id))
			assert.NoError(t, err, "dry-run delete must not touch disk")
		})
	}
}

// TestDryRunCapturesMarkApproachDrifted seeds an approach, marks it
// drifted under dry-run, and asserts the would-be update lands in the
// overlay without touching disk.
func TestDryRunCapturesMarkApproachDrifted(t *testing.T) {
	cs, ss, store, _ := dryRunHarness(t)

	seed := spec.Approach{
		ID:        "app-foo",
		Title:     "Foo",
		ParentID:  "feat-foo",
		Body:      "approach body",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(agent.KindApproach, seed.ID, seed, agent.OriginProposed))
	require.NoError(t, store.Commit())

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_mark_approach_drifted",
		Arguments: map[string]any{
			"approach_id": "app-foo",
			"event_id":    "evt-123",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool result: %+v", res)

	caps := store.OverlayCaptured(ss)
	require.Len(t, caps, 1)
	assert.Equal(t, "spec_mark_approach_drifted", caps[0].Tool)
	assert.Equal(t, agent.KindApproach, caps[0].Kind)
	assert.Equal(t, "app-foo", caps[0].ID)

	// Overlay-view surfaces the updated body with invalidated_by_event_id set.
	view := store.OverlayView(ss)
	entry, ok := view.Lookup(agent.KindApproach, "app-foo")
	require.True(t, ok)
	approach, ok := entry.Body.(spec.Approach)
	require.True(t, ok)
	assert.Equal(t, "evt-123", approach.InvalidatedByEventID)

	// Disk surface for approaches lives elsewhere — what we care about
	// is that the base-store entry is untouched.
	baseEntry, _ := store.OverlayView(nil).Lookup(agent.KindApproach, "app-foo")
	require.NotNil(t, baseEntry)
	baseApproach, ok := baseEntry.Body.(spec.Approach)
	require.True(t, ok)
	assert.Empty(t, baseApproach.InvalidatedByEventID, "base store must not be mutated")
}

// TestDryRunPassthroughForNonDryRunSession confirms that a session
// with dry-run OFF still hits the production handler and persists.
func TestDryRunPassthroughForNonDryRunSession(t *testing.T) {
	clearSessionRuntimes()
	fsys := specio.NewMemFS()
	store, err := agent.NewSpecStore(fsys)
	require.NoError(t, err)
	reg, err := activity.NewRegistry(fsys)
	require.NoError(t, err)

	server := NewSpecServer(store, fsys, reg, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "t"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	// Wait for runtime registration; leave dry-run false.
	for d := time.Now().Add(time.Second); time.Now().Before(d); {
		if rt, _ := sessionRuntimeFor(ss); rt == "claude-code" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_decision",
		Arguments: map[string]any{
			"id":         "dec-foo",
			"title":      "Foo",
			"status":     "proposed",
			"confidence": 1.0,
			"rationale":  "because reasons",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool result: %+v", res)

	// Persisted to disk — dry-run was off, so the production path ran.
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-foo.json")
	assert.NoError(t, err, "non-dry-run sessions must still persist")

	// No overlay entries (no overlay registered).
	caps := store.OverlayCaptured(ss)
	assert.Empty(t, caps)
}

// TestDryRunCapturesUpdateGoalsMdHash asserts the manifest-level hash
// update captures rather than touching .borg/manifest.json under
// dry-run. The capture records a CapturedMutation with Tool set to
// spec_update_goals_md_hash and Body carrying the (hash, syncedAt) the
// agent would have persisted.
func TestDryRunCapturesUpdateGoalsMdHash(t *testing.T) {
	cs, ss, store, fsys := dryRunHarness(t)

	// Seed an initial manifest so the production path would have
	// something to read-modify-write.
	seeded := spec.Manifest{ProjectName: "test", GoalsMdHash: "sha256:original"}
	data, err := json.MarshalIndent(seeded, "", "  ")
	require.NoError(t, err)
	require.NoError(t, fsys.MkdirAll(".borg", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/manifest.json", data, 0o644))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_update_goals_md_hash",
		Arguments: map[string]any{"hash": "sha256:newhash"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool result: %+v", res)

	caps := store.OverlayCaptured(ss)
	require.Len(t, caps, 1)
	assert.Equal(t, "spec_update_goals_md_hash", caps[0].Tool)

	// On-disk manifest unchanged.
	after, err := fsys.ReadFile(".borg/manifest.json")
	require.NoError(t, err)
	var m spec.Manifest
	require.NoError(t, json.Unmarshal(after, &m))
	assert.Equal(t, "sha256:original", m.GoalsMdHash, "dry-run must not touch the on-disk manifest hash")
}

// TestDryRunReadAfterWrite verifies DJ-147 Task 6: dry-run reads consult
// the per-session overlay so a propose/revise lands as a would-be entry
// visible to the same session's subsequent spec_get / spec_list_manifest.
func TestDryRunReadAfterWrite(t *testing.T) {
	cs, _, store, _ := dryRunHarness(t)

	// Seed a base feature so we can test overlay-overrides-base.
	require.NoError(t, store.Put(agent.KindFeature, "feat-base", spec.Feature{ID: "feat-base", Title: "Base"}, agent.OriginSettled))

	// Capture two operations: revise the base feature + propose a new one.
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_revise_feature",
		Arguments: map[string]any{
			"id": "feat-base", "title": "Revised in overlay", "status": "active", "decisions": []any{"dec-x"},
		},
	})
	require.NoError(t, err)
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "spec_propose_feature",
		Arguments: map[string]any{
			"id": "feat-new", "title": "New", "status": "proposed", "decisions": []any{"dec-x"},
		},
	})
	require.NoError(t, err)

	// spec_get must return overlay versions to THIS session.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_get",
		Arguments: map[string]any{"ids": []string{"feat-base", "feat-new"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	body := callToolResultJSON(t, res)
	assert.Contains(t, body, "Revised in overlay", "overlay revision must surface on read in the same session")
	assert.Contains(t, body, "feat-new", "overlay-only entry must surface on read in the same session")

	// spec_list_manifest must include feat-new (overlay-only).
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "spec_list_manifest"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	assert.Contains(t, callToolResultJSON(t, res), "feat-new")
}

// TestDryRunDeleteMasksRead verifies DJ-147 Task 6: an overlay-deleted
// id is reported as missing on the same-session read path. The on-disk
// entry stays untouched.
func TestDryRunDeleteMasksRead(t *testing.T) {
	cs, _, store, _ := dryRunHarness(t)

	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(agent.KindGoal, "goal-x", spec.Goal{ID: "goal-x", Title: "X", Body: "scope", SourceClause: "src"}, agent.OriginProposed))
	require.NoError(t, store.Commit())

	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_delete_goal",
		Arguments: map[string]any{"id": "goal-x", "reason": "dropped from GOALS.md"},
	})
	require.NoError(t, err)

	// spec_get must report goal-x missing in the dry-run session view.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "spec_get",
		Arguments: map[string]any{"ids": []string{"goal-x"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	body := callToolResultJSON(t, res)
	assert.Contains(t, body, "missing", "deleted-in-overlay must be reported as missing")
}

// callToolResultJSON extracts the StructuredContent of a tool result as
// a JSON string for substring assertions. The MCP SDK auto-derives a
// JSON Content text block from StructuredContent, but going straight to
// the typed structure avoids depending on the SDK's text-rendering
// shape.
func callToolResultJSON(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "tool result must have StructuredContent")
	data, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	return string(data)
}

// diskPathFor returns the .borg/spec path a kind/id would persist to
// under the production write path. Used to assert dry-run leaves the
// file system untouched.
func diskPathFor(kind agent.SpecKind, id string) string {
	switch kind {
	case agent.KindDecision:
		return ".borg/spec/decisions/" + id + ".json"
	case agent.KindFeature:
		return ".borg/spec/features/" + id + ".json"
	case agent.KindStrategy:
		return ".borg/spec/strategies/" + id + ".json"
	case agent.KindGoal:
		return ".borg/spec/goals/" + id + ".json"
	case agent.KindAntiGoal:
		return ".borg/spec/antigoals/" + id + ".json"
	default:
		return ""
	}
}
