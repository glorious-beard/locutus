package agent

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/glorious-beard/locutus/internal/search"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSpecFile is a test helper that round-trips a typed spec value
// through JSON and writes it to the MemFS at the conventional
// `.borg/spec/<kind>/<id>.json` path. Mirrors what the production
// persistence layer does and what the SpecStore must read back at
// load time.
func writeSpecFile(t *testing.T, fsys *specio.MemFS, kind, id string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err, "marshal %s/%s", kind, id)
	require.NoError(t, fsys.MkdirAll(".borg/spec/"+kind, 0o755))
	require.NoError(t, fsys.WriteFile(".borg/spec/"+kind+"/"+id+".json", data, 0o644))
}

// TestSpecStore_LoadFromFSAllKinds confirms NewSpecStore reads every
// kind of node under `.borg/spec/` into memory and tags each entry
// `OriginSettled`. The store's ListManifest reflects the full graph;
// GetSpec resolves every loaded id.
func TestSpecStore_LoadFromFSAllKinds(t *testing.T) {
	fsys := specio.NewMemFS()
	writeSpecFile(t, fsys, "features", "feat-canvass",
		spec.Feature{ID: "feat-canvass", Title: "Canvassing", Summary: "Door-to-door."})
	writeSpecFile(t, fsys, "strategies", "strat-ingest",
		spec.Strategy{ID: "strat-ingest", Title: "Ingest", Kind: spec.StrategyKind("infra"), Summary: "ETL."})
	writeSpecFile(t, fsys, "decisions", "dec-oltp-store",
		spec.Decision{ID: "dec-oltp-store", Title: "Postgres", Summary: "Pick Postgres.", Axes: []string{"oltp-store"}})
	writeSpecFile(t, fsys, "bugs", "bug-rls-leak",
		spec.Bug{ID: "bug-rls-leak", Title: "RLS leak", Summary: "Cross-tenant read."})
	require.NoError(t, fsys.MkdirAll(".borg/spec/approaches", 0o755))
	require.NoError(t, specio.SaveMarkdown(fsys, ".borg/spec/approaches/app-canvass-mvp.md",
		spec.Approach{ID: "app-canvass-mvp", Title: "Canvass MVP", Summary: "Brief for the canvassing slice."},
		"# Canvass MVP\n\nBody of the brief.\n"))

	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	m := store.ListManifest()
	assert.Len(t, m.Features, 1, "feature loaded")
	assert.Len(t, m.Strategies, 1, "strategy loaded")
	assert.Len(t, m.Decisions, 1, "decision loaded")
	assert.Len(t, m.Bugs, 1, "bug loaded")
	assert.Len(t, m.Approaches, 1, "approach loaded")
	for _, e := range m.Features {
		assert.Equal(t, OriginSettled, e.Origin, "feature entry tagged settled")
	}
	for _, e := range m.Decisions {
		assert.Equal(t, OriginSettled, e.Origin, "decision entry tagged settled")
	}

	res := store.GetSpec([]string{"feat-canvass", "dec-oltp-store", "app-canvass-mvp"})
	assert.Len(t, res.Results, 3)
	assert.Equal(t, SpecGetSettled, res.Results["feat-canvass"].Status)
	assert.Equal(t, SpecGetSettled, res.Results["dec-oltp-store"].Status)
	assert.Equal(t, SpecGetSettled, res.Results["app-canvass-mvp"].Status)
	assert.Empty(t, res.AvailableIDs, "no missing ids → no available_ids surfaced")
}

// TestSpecStore_PutTagsAsProposed confirms a Put with OriginProposed
// inserts the body into the store with the right status. Subsequent
// reads (ListManifest, GetSpec, Search) see the new node.
func TestSpecStore_PutTagsAsProposed(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Put(KindDecision, "dec-cache",
		spec.Decision{ID: "dec-cache", Title: "Redis", Summary: "Cache layer.", Axes: []string{"cache"}}, OriginProposed))

	m := store.ListManifest()
	require.Len(t, m.Decisions, 1)
	assert.Equal(t, "dec-cache", m.Decisions[0].ID)
	assert.Equal(t, OriginProposed, m.Decisions[0].Origin)

	res := store.GetSpec([]string{"dec-cache"})
	require.Contains(t, res.Results, "dec-cache")
	assert.Equal(t, SpecGetInFlight, res.Results["dec-cache"].Status)
	assert.NotNil(t, res.Results["dec-cache"].Body)
}

// TestSpecStore_PutPromotesAndDeduplicates confirms Put against an
// existing id replaces the body and updates the status — one entry
// remains, not two. Mirrors what the council's revise dispatch does
// when an iter-N elaborator re-emits an axis that already had a
// committed decision.
func TestSpecStore_PutPromotesAndDeduplicates(t *testing.T) {
	fsys := specio.NewMemFS()
	writeSpecFile(t, fsys, "decisions", "dec-cache",
		spec.Decision{ID: "dec-cache", Title: "Memcached", Axes: []string{"cache"}})
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	// Sanity: starts as one settled entry.
	require.Len(t, store.ListManifest().Decisions, 1)
	assert.Equal(t, OriginSettled, store.ListManifest().Decisions[0].Origin)

	// Revise it as proposed.
	require.NoError(t, store.Put(KindDecision, "dec-cache",
		spec.Decision{ID: "dec-cache", Title: "Redis", Axes: []string{"cache"}}, OriginProposed))

	m := store.ListManifest()
	require.Len(t, m.Decisions, 1, "still one entry after Put against existing id")
	assert.Equal(t, "Redis", m.Decisions[0].Title, "body replaced")
	assert.Equal(t, OriginProposed, m.Decisions[0].Origin, "status flipped to proposed")
}

// TestSpecStore_GetSpecBatchedReturnsPartialResult drives the load-
// bearing API change: GetSpec accepts a slice of ids and returns one
// SpecGetEntry per requested id, with status discriminating found vs
// missing. No top-level Go error for per-id misses.
func TestSpecStore_GetSpecBatchedReturnsPartialResult(t *testing.T) {
	fsys := specio.NewMemFS()
	writeSpecFile(t, fsys, "decisions", "dec-known",
		spec.Decision{ID: "dec-known", Title: "Known"})
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	res := store.GetSpec([]string{"dec-known", "dec-unknown"})

	require.Contains(t, res.Results, "dec-known")
	assert.Equal(t, SpecGetSettled, res.Results["dec-known"].Status)
	assert.NotNil(t, res.Results["dec-known"].Body)

	require.Contains(t, res.Results, "dec-unknown")
	assert.Equal(t, SpecGetMissing, res.Results["dec-unknown"].Status)
	assert.NotEmpty(t, res.Results["dec-unknown"].Reason)
	assert.Nil(t, res.Results["dec-unknown"].Body)

	// AvailableIDs[KindDecision] populated because at least one dec-*
	// was missing; carries every known dec- id once, deduped at the
	// result level rather than repeated per missing id.
	require.Contains(t, res.AvailableIDs, KindDecision)
	assert.Equal(t, []string{"dec-known"}, res.AvailableIDs[KindDecision])
}

// TestSpecStore_GetSpecBatchedEmptyIdsIsEmptyResult exercises the
// defensive paths: empty slice, nil slice, and whitespace-only ids
// all produce an empty Results map without error.
func TestSpecStore_GetSpecBatchedEmptyIdsIsEmptyResult(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	for _, ids := range [][]string{nil, {}, {"", "   "}} {
		res := store.GetSpec(ids)
		assert.Empty(t, res.Results, "empty/whitespace ids produce no entries")
		assert.Empty(t, res.AvailableIDs, "no kinds to surface")
	}
}

// TestSpecStore_GetSpecMissPopulatesAvailableIDs confirms a single
// miss produces a Missing entry plus the per-kind id catalogue —
// surfaced once per kind at the result level, with every known id
// of that kind enumerated. Mirrors the existing InFlightSpecStore
// not-found inline-recovery contract but as structured data.
func TestSpecStore_GetSpecMissPopulatesAvailableIDs(t *testing.T) {
	fsys := specio.NewMemFS()
	writeSpecFile(t, fsys, "decisions", "dec-a", spec.Decision{ID: "dec-a", Title: "A"})
	writeSpecFile(t, fsys, "decisions", "dec-b", spec.Decision{ID: "dec-b", Title: "B"})
	writeSpecFile(t, fsys, "features", "feat-x", spec.Feature{ID: "feat-x", Title: "X"})
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	res := store.GetSpec([]string{"dec-missing-1", "dec-missing-2"})

	// Both misses are recorded but the kind catalogue is one slice,
	// not duplicated per miss.
	assert.Equal(t, SpecGetMissing, res.Results["dec-missing-1"].Status)
	assert.Equal(t, SpecGetMissing, res.Results["dec-missing-2"].Status)
	require.Contains(t, res.AvailableIDs, KindDecision)
	assert.ElementsMatch(t, []string{"dec-a", "dec-b"}, res.AvailableIDs[KindDecision])
	assert.NotContains(t, res.AvailableIDs, KindFeature,
		"feat- catalogue not surfaced — no feat- ids were missing")
}

// TestSpecStore_GetSpecMalformedIDLandsInMissing confirms malformed
// ids surface as Missing entries (Reason names the regex violation)
// rather than as a top-level Go error. Lets the model parse one
// result shape regardless of how an id failed.
func TestSpecStore_GetSpecMalformedIDLandsInMissing(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	res := store.GetSpec([]string{"bad id", "feat-OK"})

	require.Contains(t, res.Results, "bad id")
	assert.Equal(t, SpecGetMissing, res.Results["bad id"].Status)
	assert.Contains(t, res.Results["bad id"].Reason, "malformed",
		"reason names the malformed-id failure mode")
}

// TestSpecStore_MarkWorkingFlagsEntries confirms MarkWorking propagates
// to ListManifest entries and to GetSpec.Results entries. ClearWorking
// resets the flag.
func TestSpecStore_MarkWorkingFlagsEntries(t *testing.T) {
	fsys := specio.NewMemFS()
	writeSpecFile(t, fsys, "decisions", "dec-foo",
		spec.Decision{ID: "dec-foo", Title: "Foo"})
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	store.MarkWorking([]string{"dec-foo"})

	m := store.ListManifest()
	require.Len(t, m.Decisions, 1)
	assert.True(t, m.Decisions[0].Working, "manifest entry shows working")

	res := store.GetSpec([]string{"dec-foo"})
	assert.True(t, res.Results["dec-foo"].Working, "GetSpec entry shows working")
	assert.Contains(t, res.Working, "dec-foo", "result-level Working slice lists the id")

	store.ClearWorking()
	assert.False(t, store.ListManifest().Decisions[0].Working, "ClearWorking resets")
}

// TestSpecStore_SearchIsLiveAfterPut confirms Put updates the search
// index synchronously: a Put with content matching a fresh query
// returns the new id in the hit list without an explicit reindex
// call.
func TestSpecStore_SearchIsLiveAfterPut(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Put(KindDecision, "dec-cache",
		spec.Decision{ID: "dec-cache", Title: "Cache", Summary: "redis cache layer",
			Rationale: "redis fits the latency profile"},
		OriginProposed))

	hits, _, err := store.Search("redis", search.Options{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, hits, "expected at least one hit for 'redis'")
	found := false
	for _, h := range hits {
		if h.ID == "dec-cache" {
			found = true
			break
		}
	}
	assert.True(t, found, "dec-cache appears in search results immediately after Put")
}

// TestSpecStore_CommitFlushesProposedToFS confirms Begin → Put →
// Commit lifecycle: proposed nodes land on disk under `.borg/spec/`
// at Commit, then transition to OriginSettled in memory.
func TestSpecStore_CommitFlushesProposedToFS(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindDecision, "dec-commit",
		spec.Decision{ID: "dec-commit", Title: "Commit me", Axes: []string{"commit-axis"}},
		OriginProposed))

	// Before Commit: nothing on disk.
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-commit.json")
	assert.Error(t, err, "FS write happens at Commit, not Put")

	require.NoError(t, store.Commit())

	data, err := fsys.ReadFile(".borg/spec/decisions/dec-commit.json")
	require.NoError(t, err, "Commit persists proposed → disk")
	var d spec.Decision
	require.NoError(t, json.Unmarshal(data, &d))
	assert.Equal(t, "Commit me", d.Title)

	// In memory: status is now settled.
	m := store.ListManifest()
	require.Len(t, m.Decisions, 1)
	assert.Equal(t, OriginSettled, m.Decisions[0].Origin,
		"proposed → settled after Commit")
}

// TestSpecStore_RollbackRevertsProposed confirms Begin → Put →
// Rollback discards the in-memory proposed delta and leaves
// `.borg/spec/` untouched. Preserves DJ-088's all-or-nothing council
// output semantics.
func TestSpecStore_RollbackRevertsProposed(t *testing.T) {
	fsys := specio.NewMemFS()
	writeSpecFile(t, fsys, "decisions", "dec-settled",
		spec.Decision{ID: "dec-settled", Title: "Stays"})
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindDecision, "dec-new",
		spec.Decision{ID: "dec-new", Title: "Discard me"}, OriginProposed))
	// Working mark on a settled node during the tx — should be cleared by Rollback.
	store.MarkWorking([]string{"dec-settled"})

	require.NoError(t, store.Rollback())

	m := store.ListManifest()
	require.Len(t, m.Decisions, 1, "proposed entry dropped")
	assert.Equal(t, "dec-settled", m.Decisions[0].ID)
	assert.False(t, m.Decisions[0].Working, "Working flag cleared by Rollback")

	_, err = fsys.ReadFile(".borg/spec/decisions/dec-new.json")
	assert.Error(t, err, "discarded entry never reaches disk")
}

// TestSpecStore_ConcurrentReadsAndOneWriter exercises the RWMutex
// discipline: one writer goroutine doing N Puts while M reader
// goroutines run ListManifest / GetSpec / Search. The test passes if
// `go test -race` doesn't fire and no reader observes a torn state
// (every observed manifest is a consistent snapshot).
func TestSpecStore_ConcurrentReadsAndOneWriter(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	const writes = 100
	const readers = 8

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < writes; i++ {
			id := "dec-" + decIDSuffix(i)
			_ = store.Put(KindDecision, id,
				spec.Decision{ID: id, Title: "writer"}, OriginProposed)
		}
	}()
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writes; i++ {
				_ = store.ListManifest()
				_ = store.GetSpec([]string{"dec-zero"})
				_, _, _ = store.Search("writer", search.Options{Limit: 5})
			}
		}()
	}
	wg.Wait()

	m := store.ListManifest()
	assert.Equal(t, writes, len(m.Decisions), "every write landed in the store")
}

// decIDSuffix returns a stable single-character suffix for the
// concurrency test's writer loop. Keeps ids inside the validSpecID
// regex (kebab-case, alnum) without dragging in fmt.Sprintf overhead.
func decIDSuffix(i int) string {
	const alpha = "abcdefghijklmnopqrstuvwxyz0123456789"
	base := len(alpha)
	if i < base {
		return string(alpha[i])
	}
	return string(alpha[i/base]) + string(alpha[i%base])
}

// TestSpecStore_OriginPrefixDispatch confirms GetSpec routes by id
// prefix (feat-, strat-, dec-, bug-, app-) and surfaces unknown
// prefixes as malformed-id misses.
func TestSpecStore_OriginPrefixDispatch(t *testing.T) {
	fsys := specio.NewMemFS()
	writeSpecFile(t, fsys, "features", "feat-x", spec.Feature{ID: "feat-x", Title: "X"})
	writeSpecFile(t, fsys, "strategies", "strat-y", spec.Strategy{ID: "strat-y", Title: "Y"})
	writeSpecFile(t, fsys, "decisions", "dec-z", spec.Decision{ID: "dec-z", Title: "Z"})
	writeSpecFile(t, fsys, "bugs", "bug-w", spec.Bug{ID: "bug-w", Title: "W"})
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	res := store.GetSpec([]string{"feat-x", "strat-y", "dec-z", "bug-w", "wrong-prefix-id"})

	assert.Equal(t, SpecGetSettled, res.Results["feat-x"].Status)
	assert.Equal(t, SpecGetSettled, res.Results["strat-y"].Status)
	assert.Equal(t, SpecGetSettled, res.Results["dec-z"].Status)
	assert.Equal(t, SpecGetSettled, res.Results["bug-w"].Status)
	assert.Equal(t, SpecGetMissing, res.Results["wrong-prefix-id"].Status)
	assert.Contains(t, res.Results["wrong-prefix-id"].Reason, "malformed")
}
