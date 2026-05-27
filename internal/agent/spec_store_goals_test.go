// DJ-139 phase 1 tests — Goal and AntiGoal node kinds in SpecStore.
//
// These tests exercise the end-to-end shape: Put a typed spec.Goal /
// spec.AntiGoal via the kind-tagged Put surface; persist via the
// Begin/Commit transaction lifecycle; reload from disk into a fresh
// store; assert the round-trip is lossless including slice fields on
// AntiGoal (CededTo, KeptIn) and the new id-prefix routing.

package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecStorePutGoalRoundTrip writes a spec.Goal via Put(KindGoal,
// ...), persists through Commit, reads back via GetSpec, and asserts
// every field round-trips. The goal also re-loads cleanly into a
// fresh store reading from the same FS — the disk layout
// (.borg/spec/goals/<id>.json) and JSON encoding must match.
func TestSpecStorePutGoalRoundTrip(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	created := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 5, 27, 11, 0, 0, 0, time.UTC)
	g := spec.Goal{
		ID:           "goal-strategic-planning-tool",
		Title:        "Strategic planning tool",
		Body:         "Locutus is a strategic planning tool for solo founders running their own software projects.",
		SourceClause: "Locutus is a strategic planning tool",
		CreatedAt:    created,
		UpdatedAt:    updated,
	}

	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindGoal, g.ID, g, OriginProposed))
	require.NoError(t, store.Commit())

	// In-memory readback.
	res := store.GetSpec([]string{g.ID})
	require.Contains(t, res.Results, g.ID)
	assert.Equal(t, SpecGetSettled, res.Results[g.ID].Status)
	gotBody, ok := res.Results[g.ID].Body.(spec.Goal)
	require.True(t, ok, "GetSpec body is spec.Goal, got %T", res.Results[g.ID].Body)
	assert.Equal(t, g, gotBody)

	// On-disk file landed where loadFromFS expects it.
	data, err := fsys.ReadFile(".borg/spec/goals/goal-strategic-planning-tool.json")
	require.NoError(t, err, "Goal persisted to .borg/spec/goals/<id>.json")
	var decoded spec.Goal
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, g, decoded)

	// Fresh store re-loads from FS.
	store2, err := NewSpecStore(fsys)
	require.NoError(t, err)
	res2 := store2.GetSpec([]string{g.ID})
	require.Contains(t, res2.Results, g.ID)
	gotBody2, ok := res2.Results[g.ID].Body.(spec.Goal)
	require.True(t, ok)
	assert.Equal(t, g, gotBody2)
}

// TestSpecStorePutAntiGoalRoundTrip exercises the same round-trip for
// AntiGoal including the CededTo and KeptIn slice fields. omitempty
// must not drop populated slices.
func TestSpecStorePutAntiGoalRoundTrip(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	created := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 5, 27, 11, 0, 0, 0, time.UTC)
	ag := spec.AntiGoal{
		ID:           "agoal-fundraising",
		Title:        "Fundraising tracking",
		Body:         "Locutus does not track fundraising rounds, investor relationships, or cap table state.",
		SourceClause: "Fundraising tracking is out of scope",
		CededTo:      []string{"Carta", "AngelList"},
		KeptIn:       []string{"runway forecasting for product timeline planning"},
		CreatedAt:    created,
		UpdatedAt:    updated,
	}

	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindAntiGoal, ag.ID, ag, OriginProposed))
	require.NoError(t, store.Commit())

	res := store.GetSpec([]string{ag.ID})
	require.Contains(t, res.Results, ag.ID)
	gotBody, ok := res.Results[ag.ID].Body.(spec.AntiGoal)
	require.True(t, ok, "GetSpec body is spec.AntiGoal, got %T", res.Results[ag.ID].Body)
	assert.Equal(t, ag, gotBody)
	assert.Equal(t, []string{"Carta", "AngelList"}, gotBody.CededTo, "CededTo round-trips")
	assert.Equal(t, []string{"runway forecasting for product timeline planning"}, gotBody.KeptIn, "KeptIn round-trips")

	data, err := fsys.ReadFile(".borg/spec/antigoals/agoal-fundraising.json")
	require.NoError(t, err, "AntiGoal persisted to .borg/spec/antigoals/<id>.json")
	var decoded spec.AntiGoal
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, ag, decoded)

	// Fresh store re-loads from FS.
	store2, err := NewSpecStore(fsys)
	require.NoError(t, err)
	res2 := store2.GetSpec([]string{ag.ID})
	require.Contains(t, res2.Results, ag.ID)
	gotBody2, ok := res2.Results[ag.ID].Body.(spec.AntiGoal)
	require.True(t, ok)
	assert.Equal(t, ag, gotBody2)
}

// TestSpecStoreLoadFromFSRecognizesGoalsAndAntiGoals seeds JSON files
// directly on disk (no Put through the API) and constructs a fresh
// store. Both kinds load into the in-memory maps without going
// through Begin/Commit.
func TestSpecStoreLoadFromFSRecognizesGoalsAndAntiGoals(t *testing.T) {
	fsys := specio.NewMemFS()
	g := spec.Goal{
		ID:           "goal-strategic-planning-tool",
		Title:        "Strategic planning tool",
		Body:         "Locutus is a strategic planning tool.",
		SourceClause: "Locutus is a strategic planning tool",
		CreatedAt:    time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC),
	}
	ag := spec.AntiGoal{
		ID:           "agoal-fundraising",
		Title:        "Fundraising tracking",
		Body:         "Fundraising is out of scope.",
		SourceClause: "Fundraising tracking is out of scope",
		CededTo:      []string{"Carta"},
		CreatedAt:    time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC),
	}
	gData, err := json.Marshal(g)
	require.NoError(t, err)
	agData, err := json.Marshal(ag)
	require.NoError(t, err)
	require.NoError(t, fsys.MkdirAll(".borg/spec/goals", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/goals/goal-strategic-planning-tool.json", gData, 0o644))
	require.NoError(t, fsys.MkdirAll(".borg/spec/antigoals", 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/antigoals/agoal-fundraising.json", agData, 0o644))

	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	res := store.GetSpec([]string{g.ID, ag.ID})
	require.Contains(t, res.Results, g.ID)
	require.Contains(t, res.Results, ag.ID)
	assert.Equal(t, SpecGetSettled, res.Results[g.ID].Status)
	assert.Equal(t, SpecGetSettled, res.Results[ag.ID].Status)
	gotG, ok := res.Results[g.ID].Body.(spec.Goal)
	require.True(t, ok)
	assert.Equal(t, g, gotG)
	gotAG, ok := res.Results[ag.ID].Body.(spec.AntiGoal)
	require.True(t, ok)
	assert.Equal(t, ag, gotAG)
}

// TestSpecStoreListManifestIncludesGoalsAndAntiGoals confirms the new
// kinds surface in the SpecManifest under dedicated Goals and
// AntiGoals slices so the agent-facing manifest tool catalogs them
// alongside the existing five kinds.
func TestSpecStoreListManifestIncludesGoalsAndAntiGoals(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindGoal, "goal-strategic-planning-tool",
		spec.Goal{
			ID:           "goal-strategic-planning-tool",
			Title:        "Strategic planning tool",
			Body:         "Locutus is a strategic planning tool.",
			SourceClause: "Locutus is a strategic planning tool",
		}, OriginProposed))
	require.NoError(t, store.Put(KindAntiGoal, "agoal-fundraising",
		spec.AntiGoal{
			ID:           "agoal-fundraising",
			Title:        "Fundraising tracking",
			Body:         "Fundraising is out of scope.",
			SourceClause: "Fundraising tracking is out of scope",
			CededTo:      []string{"Carta"},
		}, OriginProposed))
	require.NoError(t, store.Commit())

	m := store.ListManifest()
	require.Len(t, m.Goals, 1, "goal entry surfaces in manifest")
	require.Len(t, m.AntiGoals, 1, "antigoal entry surfaces in manifest")
	assert.Equal(t, "goal-strategic-planning-tool", m.Goals[0].ID)
	assert.Equal(t, "Strategic planning tool", m.Goals[0].Title)
	assert.Equal(t, OriginSettled, m.Goals[0].Origin, "post-commit entries are settled")
	assert.Equal(t, "agoal-fundraising", m.AntiGoals[0].ID)
	assert.Equal(t, "Fundraising tracking", m.AntiGoals[0].Title)
	assert.Equal(t, OriginSettled, m.AntiGoals[0].Origin)
}

// TestSpecStoreLookupLockedResolvesGoalIDs drives the lookup path
// directly: GetSpec routes goal-* and agoal-* prefixes to the right
// typed maps and returns the body in its typed form.
func TestSpecStoreLookupLockedResolvesGoalIDs(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindGoal, "goal-x",
		spec.Goal{ID: "goal-x", Title: "X", Body: "body", SourceClause: "clause"}, OriginProposed))
	require.NoError(t, store.Put(KindAntiGoal, "agoal-y",
		spec.AntiGoal{ID: "agoal-y", Title: "Y", Body: "body", SourceClause: "clause"}, OriginProposed))
	require.NoError(t, store.Commit())

	res := store.GetSpec([]string{"goal-x", "agoal-y"})
	require.Contains(t, res.Results, "goal-x")
	require.Contains(t, res.Results, "agoal-y")

	gBody, ok := res.Results["goal-x"].Body.(spec.Goal)
	require.True(t, ok, "goal-x resolves to spec.Goal")
	assert.Equal(t, "X", gBody.Title)

	agBody, ok := res.Results["agoal-y"].Body.(spec.AntiGoal)
	require.True(t, ok, "agoal-y resolves to spec.AntiGoal")
	assert.Equal(t, "Y", agBody.Title)
}

// TestSpecStoreGetSpecMissPopulatesGoalAndAntiGoalAvailableIDs
// confirms the per-kind miss-recovery catalogue extends to the new
// kinds: requesting a missing goal-* surfaces every known goal id
// under AvailableIDs[KindGoal] (and same for agoal-*).
func TestSpecStoreGetSpecMissPopulatesGoalAndAntiGoalAvailableIDs(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindGoal, "goal-a",
		spec.Goal{ID: "goal-a", Title: "A"}, OriginProposed))
	require.NoError(t, store.Put(KindGoal, "goal-b",
		spec.Goal{ID: "goal-b", Title: "B"}, OriginProposed))
	require.NoError(t, store.Put(KindAntiGoal, "agoal-c",
		spec.AntiGoal{ID: "agoal-c", Title: "C"}, OriginProposed))
	require.NoError(t, store.Commit())

	res := store.GetSpec([]string{"goal-missing", "agoal-missing"})
	assert.Equal(t, SpecGetMissing, res.Results["goal-missing"].Status)
	assert.Equal(t, SpecGetMissing, res.Results["agoal-missing"].Status)
	require.Contains(t, res.AvailableIDs, KindGoal)
	require.Contains(t, res.AvailableIDs, KindAntiGoal)
	assert.ElementsMatch(t, []string{"goal-a", "goal-b"}, res.AvailableIDs[KindGoal])
	assert.ElementsMatch(t, []string{"agoal-c"}, res.AvailableIDs[KindAntiGoal])
}

// TestSpecStoreRollbackRevertsProposedGoalAndAntiGoal confirms the
// transaction snapshot machinery covers the new kinds: a proposed
// goal added during a Begin → Put → Rollback cycle does not survive
// rollback and never reaches disk.
func TestSpecStoreRollbackRevertsProposedGoalAndAntiGoal(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindGoal, "goal-new",
		spec.Goal{ID: "goal-new", Title: "Discard me"}, OriginProposed))
	require.NoError(t, store.Put(KindAntiGoal, "agoal-new",
		spec.AntiGoal{ID: "agoal-new", Title: "Discard me too"}, OriginProposed))

	require.NoError(t, store.Rollback())

	m := store.ListManifest()
	assert.Empty(t, m.Goals, "rolled-back goal removed from store")
	assert.Empty(t, m.AntiGoals, "rolled-back antigoal removed from store")
	_, err = fsys.ReadFile(".borg/spec/goals/goal-new.json")
	assert.Error(t, err, "rolled-back goal never reaches disk")
	_, err = fsys.ReadFile(".borg/spec/antigoals/agoal-new.json")
	assert.Error(t, err, "rolled-back antigoal never reaches disk")
}

// TestSpecStorePutGoalRejectsMalformedID confirms the id-prefix
// invariant: a non-goal-prefixed id paired with KindGoal is rejected
// before it touches the maps. Same for KindAntiGoal.
func TestSpecStorePutGoalRejectsMalformedID(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	err = store.Put(KindGoal, "feat-wrong",
		spec.Goal{ID: "feat-wrong", Title: "wrong prefix"}, OriginProposed)
	assert.Error(t, err, "feat- id rejected for KindGoal")

	err = store.Put(KindAntiGoal, "goal-wrong",
		spec.AntiGoal{ID: "goal-wrong", Title: "wrong prefix"}, OriginProposed)
	assert.Error(t, err, "goal- id rejected for KindAntiGoal")
}

// TestValidSpecIDAcceptsGoalAndAntiGoalPrefixes is a table test that
// covers the validator regex. Confirms goal- and agoal- prefixes are
// accepted alongside the existing five, and malformed shapes are
// rejected.
func TestValidSpecIDAcceptsGoalAndAntiGoalPrefixes(t *testing.T) {
	cases := []struct {
		id    string
		valid bool
	}{
		// Positive: well-formed new prefixes.
		{"goal-strategic-planning-tool", true},
		{"goal-foo", true},
		{"agoal-fundraising", true},
		{"agoal-bar", true},
		// Positive: the existing five still work.
		{"feat-canvass", true},
		{"strat-ingest", true},
		{"dec-oltp-store", true},
		{"bug-rls-leak", true},
		{"app-canvass-mvp", true},
		// Negative: missing slug body.
		{"goal-", false},
		{"agoal-", false},
		// Negative: uppercase.
		{"goal-Foo", false},
		{"AGoal-foo", false},
		// Negative: leading/trailing hyphens in slug body.
		{"goal--double", false},
		{"goal-foo-", false},
		// Negative: unknown prefix.
		{"gol-foo", false},
		{"antigoal-foo", false},
		// Negative: path traversal.
		{"goal-../etc/passwd", false},
		{"agoal-../etc/passwd", false},
		// Negative: empty.
		{"", false},
	}
	for _, c := range cases {
		got := validSpecID.MatchString(c.id)
		assert.Equal(t, c.valid, got, "validSpecID(%q) = %v, want %v", c.id, got, c.valid)
	}
}
