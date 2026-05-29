// DJ-147 — per-session overlay on the SpecStore. The overlay captures
// would-be mutations (proposes / revises / deletes) and serves them on
// read-after-write so a dry-run workflow runs faithfully end-to-end
// against the would-be graph.
package agent

import (
	"sync"
	"testing"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionOverlay_PutAndLookup(t *testing.T) {
	o := newSessionOverlay()
	d := spec.Decision{ID: "dec-foo", Title: "Foo"}
	o.put("spec_propose_decision", KindDecision, "dec-foo", d)

	entry, ok := o.lookup(KindDecision, "dec-foo")
	require.True(t, ok)
	require.NotNil(t, entry)
	got, ok := entry.Body.(spec.Decision)
	require.True(t, ok)
	assert.Equal(t, "dec-foo", got.ID)

	assert.Len(t, o.capturedList(), 1)
	cap := o.capturedList()[0]
	assert.Equal(t, "spec_propose_decision", cap.Tool)
	assert.Equal(t, KindDecision, cap.Kind)
	assert.Equal(t, "dec-foo", cap.ID)
}

func TestSessionOverlay_DeleteMasksLookup(t *testing.T) {
	o := newSessionOverlay()
	o.delete("spec_delete_goal", KindGoal, "goal-x")

	_, ok := o.lookup(KindGoal, "goal-x")
	assert.False(t, ok, "lookup must report missing for deleted entries")

	assert.True(t, o.isDeleted(KindGoal, "goal-x"))
	assert.False(t, o.isDeleted(KindGoal, "goal-y"))

	assert.Len(t, o.capturedList(), 1)
	assert.Equal(t, "spec_delete_goal", o.capturedList()[0].Tool)
}

func TestSessionOverlay_RevisePreservesOrderedCapture(t *testing.T) {
	o := newSessionOverlay()
	o.put("spec_propose_feature", KindFeature, "feat-a", spec.Feature{ID: "feat-a"})
	o.put("spec_revise_feature", KindFeature, "feat-a", spec.Feature{ID: "feat-a", Title: "A revised"})

	entry, ok := o.lookup(KindFeature, "feat-a")
	require.True(t, ok)
	got := entry.Body.(spec.Feature)
	assert.Equal(t, "A revised", got.Title, "later writes overwrite earlier ones in the overlay")

	caps := o.capturedList()
	require.Len(t, caps, 2)
	assert.Equal(t, "spec_propose_feature", caps[0].Tool)
	assert.Equal(t, "spec_revise_feature", caps[1].Tool)
	assert.True(t, caps[0].Timestamp.Before(caps[1].Timestamp) || caps[0].Timestamp.Equal(caps[1].Timestamp))
}

func TestSessionOverlay_ConcurrentPutSafe(t *testing.T) {
	o := newSessionOverlay()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "dec-" + string(rune('a'+i))
			o.put("spec_propose_decision", KindDecision, id, spec.Decision{ID: id})
		}(i)
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		id := "dec-" + string(rune('a'+i))
		_, ok := o.lookup(KindDecision, id)
		assert.True(t, ok, "missing %s after concurrent writes", id)
	}
	assert.Len(t, o.capturedList(), 8)
}

// fakeSess is a tiny stand-in for *mcp.ServerSession — the SpecStore
// keys overlays on `any` (DJ-134 keeps the agent package mcp-import-free),
// so any addressable value works as a session handle in tests.
type fakeSess struct{ id string }

// TestSpecStore_OverlayRegisterUnregister covers the lifecycle: a
// freshly-constructed store has no overlay for any session; Register
// installs one; Unregister discards it. overlayFor returns nil before
// register and after unregister.
func TestSpecStore_OverlayRegisterUnregister(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	sess := &fakeSess{id: "s1"}

	// Before register, no overlay.
	assert.Nil(t, store.overlayFor(sess))

	store.RegisterOverlay(sess)
	assert.NotNil(t, store.overlayFor(sess))

	store.UnregisterOverlay(sess)
	assert.Nil(t, store.overlayFor(sess))

	// Unregister is idempotent — repeating the call on a session that
	// no longer has an overlay is a no-op.
	assert.NotPanics(t, func() { store.UnregisterOverlay(sess) })
}

// TestSpecStore_OverlayPutDoesNotPersist confirms OverlayPut writes to
// the session's overlay only — nothing reaches disk, nothing lands in
// the base store's per-kind maps. The capture list reflects the mutation.
func TestSpecStore_OverlayPutDoesNotPersist(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	d := spec.Decision{ID: "dec-foo", Title: "Foo"}
	err = store.OverlayPut(sess, "spec_propose_decision", KindDecision, "dec-foo", d)
	require.NoError(t, err)

	// Disk: nothing written.
	_, err = fsys.ReadFile(".borg/spec/decisions/dec-foo.json")
	assert.Error(t, err, "OverlayPut must NOT write to disk")

	// Base store: nothing landed in the per-kind map either — GetSpec
	// reports the id missing.
	res := store.GetSpec([]string{"dec-foo"})
	assert.Equal(t, SpecGetMissing, res.Results["dec-foo"].Status,
		"OverlayPut must NOT mutate the base SpecStore")

	// Captured list: one entry.
	caps := store.OverlayCaptured(sess)
	require.Len(t, caps, 1)
	assert.Equal(t, "dec-foo", caps[0].ID)
	assert.Equal(t, "spec_propose_decision", caps[0].Tool)
	assert.Equal(t, KindDecision, caps[0].Kind)

	// OverlayPut against a session with no registered overlay errors.
	otherSess := &fakeSess{id: "s2"}
	err = store.OverlayPut(otherSess, "spec_propose_decision", KindDecision, "dec-bar", spec.Decision{ID: "dec-bar"})
	assert.Error(t, err, "OverlayPut must reject sessions with no registered overlay")
}

// TestSpecStore_OverlayViewReadsOverlayThenBase exercises the read
// merger: a session's overlay wins for entries it holds; overlay-only
// entries appear; the base store fills in for keys the overlay doesn't
// hold. A session with no overlay sees the base store alone.
func TestSpecStore_OverlayViewReadsOverlayThenBase(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/decisions", 0o755))
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	// Seed the base store with dec-a via Put — the canonical settled-
	// origin install path. Same pattern other agent tests use.
	require.NoError(t, store.Put(KindDecision, "dec-a",
		spec.Decision{ID: "dec-a", Title: "From base"}, OriginSettled))

	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	// Overlay revises dec-a and adds dec-b.
	require.NoError(t, store.OverlayPut(sess, "spec_revise_decision", KindDecision, "dec-a",
		spec.Decision{ID: "dec-a", Title: "From overlay"}))
	require.NoError(t, store.OverlayPut(sess, "spec_propose_decision", KindDecision, "dec-b",
		spec.Decision{ID: "dec-b"}))

	view := store.OverlayView(sess)
	a, ok := view.Lookup(KindDecision, "dec-a")
	require.True(t, ok)
	assert.Equal(t, "From overlay", a.Body.(spec.Decision).Title,
		"overlay must win over base on read")
	b, ok := view.Lookup(KindDecision, "dec-b")
	require.True(t, ok)
	assert.Equal(t, "dec-b", b.Body.(spec.Decision).ID,
		"overlay-only entries must be visible to this session")

	// A session without an overlay sees only the base — this also
	// exercises the base-store fallback path through OverlayView.
	other := &fakeSess{id: "s2"}
	viewOther := store.OverlayView(other)
	a2, ok := viewOther.Lookup(KindDecision, "dec-a")
	require.True(t, ok)
	assert.Equal(t, "From base", a2.Body.(spec.Decision).Title,
		"non-dry-run session sees base only")
	_, ok = viewOther.Lookup(KindDecision, "dec-b")
	assert.False(t, ok, "overlay-only entries invisible to other sessions")
}

// TestSpecStore_OverlayDeleteMasks confirms OverlayDelete masks a
// settled base-store entry from this session's view only — other
// sessions still see the entry.
func TestSpecStore_OverlayDeleteMasks(t *testing.T) {
	fsys := specio.NewMemFS()
	require.NoError(t, fsys.MkdirAll(".borg/spec/goals", 0o755))
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	require.NoError(t, store.Put(KindGoal, "goal-x",
		spec.Goal{ID: "goal-x"}, OriginSettled))

	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	require.NoError(t, store.OverlayDelete(sess, "spec_delete_goal", KindGoal, "goal-x"))

	view := store.OverlayView(sess)
	_, ok := view.Lookup(KindGoal, "goal-x")
	assert.False(t, ok, "deleted entry must be masked from this session's view")

	// Other sessions still see it.
	other := &fakeSess{id: "s2"}
	_, ok = store.OverlayView(other).Lookup(KindGoal, "goal-x")
	assert.True(t, ok, "deletion is per-session, not global")

	// OverlayDelete against an unregistered session errors.
	err = store.OverlayDelete(other, "spec_delete_goal", KindGoal, "goal-x")
	assert.Error(t, err, "OverlayDelete must reject sessions with no registered overlay")
}
