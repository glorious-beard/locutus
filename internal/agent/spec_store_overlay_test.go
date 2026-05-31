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
	"github.com/glorious-beard/locutus/internal/state"
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

// TestSpecStore_OverlayRegisterIsIdempotent locks down the documented
// invariant on RegisterOverlay: re-registering the same session is a
// no-op — the first overlay survives, captured mutations carry over.
// A future change that swaps overlays on re-register would break this
// test.
func TestSpecStore_OverlayRegisterIsIdempotent(t *testing.T) {
	store, err := NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	require.NoError(t, store.OverlayPut(sess, "spec_propose_decision", KindDecision, "dec-foo", spec.Decision{ID: "dec-foo"}))
	require.Len(t, store.OverlayCaptured(sess), 1, "one capture present")

	// Re-register the same session. The first overlay must survive — captured stays.
	store.RegisterOverlay(sess)
	caps := store.OverlayCaptured(sess)
	assert.Len(t, caps, 1, "re-registering must NOT discard the existing overlay")
	assert.Equal(t, "dec-foo", caps[0].ID)
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

func TestSessionOverlay_StatePutAndLookup(t *testing.T) {
	o := newSessionOverlay()
	rs := state.ReconciliationState{
		ApproachID: "app-feat-foo",
		Status:     state.StatusLive,
		SpecHashes: map[string]string{"app-feat-foo": "sha256:abc"},
		Artifacts:  map[string]string{"f.go": "sha256:def"},
	}
	o.putState("state_record_reconciliation", "app-feat-foo", rs)

	got, ok := o.lookupState("app-feat-foo")
	require.True(t, ok)
	require.NotNil(t, got)
	assert.Equal(t, "app-feat-foo", got.ApproachID)
	assert.Equal(t, state.StatusLive, got.Status)

	caps := o.capturedList()
	require.Len(t, caps, 1)
	assert.Equal(t, "state_record_reconciliation", caps[0].Tool)
	assert.Equal(t, "app-feat-foo", caps[0].ID)
}

func TestSessionOverlay_StateDeleteMasksLookup(t *testing.T) {
	o := newSessionOverlay()
	o.deleteState("state_delete_record", "app-feat-foo")

	_, ok := o.lookupState("app-feat-foo")
	assert.False(t, ok, "deleted state record must report missing")
	assert.True(t, o.isStateDeleted("app-feat-foo"))

	caps := o.capturedList()
	require.Len(t, caps, 1)
	assert.Equal(t, "state_delete_record", caps[0].Tool)
}

func TestSessionOverlay_StatePutAfterDeleteUnmasks(t *testing.T) {
	o := newSessionOverlay()
	o.deleteState("state_delete_record", "app-x")
	o.putState("state_record_reconciliation", "app-x", state.ReconciliationState{ApproachID: "app-x"})
	_, ok := o.lookupState("app-x")
	assert.True(t, ok, "putState after deleteState must unmask")
	assert.False(t, o.isStateDeleted("app-x"), "isStateDeleted should be false after re-put")
}

func TestSessionOverlay_ConcurrentStatePutSafe(t *testing.T) {
	o := newSessionOverlay()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "app-" + string(rune('a'+i))
			o.putState("state_record_reconciliation", id, state.ReconciliationState{ApproachID: id})
		}(i)
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		id := "app-" + string(rune('a'+i))
		_, ok := o.lookupState(id)
		assert.True(t, ok, "missing %s after concurrent writes", id)
	}
	assert.Len(t, o.capturedList(), 8)
}

func TestSpecStore_OverlayStatePutAndView(t *testing.T) {
	store, err := NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	rs := state.ReconciliationState{
		ApproachID: "app-feat-foo",
		Status:     state.StatusLive,
	}
	err = store.OverlayPutState(sess, "state_record_reconciliation", "app-feat-foo", rs)
	require.NoError(t, err)

	view := store.OverlayView(sess)
	got, ok := view.GetState("app-feat-foo")
	require.True(t, ok)
	assert.Equal(t, state.StatusLive, got.Status)

	// Caps include the state capture
	caps := store.OverlayCaptured(sess)
	require.Len(t, caps, 1)
	assert.Equal(t, "state_record_reconciliation", caps[0].Tool)
}

func TestSpecStore_OverlayStateDeleteMasks(t *testing.T) {
	store, err := NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	err = store.OverlayDeleteState(sess, "state_delete_record", "app-feat-foo")
	require.NoError(t, err)

	view := store.OverlayView(sess)
	_, ok := view.GetState("app-feat-foo")
	assert.False(t, ok, "deleted record masked from view")
}

func TestSpecStore_OverlayPutStateNoOverlayErrors(t *testing.T) {
	store, err := NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	sess := &fakeSess{id: "unregistered"}
	// Don't register.
	err = store.OverlayPutState(sess, "state_record_reconciliation", "app-x", state.ReconciliationState{})
	require.Error(t, err, "OverlayPutState should error if session has no registered overlay")
}

func TestOverlayView_StateAccessors_FallThroughToBase(t *testing.T) {
	store, err := NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	// Wire stub base-state accessors that return one canned record.
	baseRecords := map[string]state.ReconciliationState{
		"app-base": {ApproachID: "app-base", Status: state.StatusLive},
	}
	store.SetStateAccessors(
		func(id string) (*state.ReconciliationState, bool) {
			rs, ok := baseRecords[id]
			if !ok {
				return nil, false
			}
			return &rs, true
		},
		func() []string {
			out := make([]string, 0, len(baseRecords))
			for id := range baseRecords {
				out = append(out, id)
			}
			return out
		},
	)

	// View for a session without an overlay (no RegisterOverlay) — should pass through to base.
	sess := &fakeSess{id: "passthrough"}
	view := store.OverlayView(sess)
	rs, ok := view.GetState("app-base")
	require.True(t, ok, "base record should be visible without overlay")
	assert.Equal(t, "app-base", rs.ApproachID)

	ids := view.ListStateRecords()
	assert.ElementsMatch(t, []string{"app-base"}, ids)
}

func TestOverlayView_StateAccessors_OverlayOverridesBase(t *testing.T) {
	store, err := NewSpecStore(specio.NewMemFS())
	require.NoError(t, err)

	baseRecords := map[string]state.ReconciliationState{
		"app-foo": {ApproachID: "app-foo", Status: state.StatusLive},
	}
	store.SetStateAccessors(
		func(id string) (*state.ReconciliationState, bool) {
			rs, ok := baseRecords[id]
			if !ok {
				return nil, false
			}
			return &rs, true
		},
		func() []string {
			out := make([]string, 0, len(baseRecords))
			for id := range baseRecords {
				out = append(out, id)
			}
			return out
		},
	)

	sess := &fakeSess{id: "s1"}
	store.RegisterOverlay(sess)
	t.Cleanup(func() { store.UnregisterOverlay(sess) })

	// Overlay-revise app-foo to drifted status.
	require.NoError(t, store.OverlayPutState(sess, "state_mark_status", "app-foo",
		state.ReconciliationState{ApproachID: "app-foo", Status: state.StatusDrifted}))

	view := store.OverlayView(sess)
	rs, ok := view.GetState("app-foo")
	require.True(t, ok)
	assert.Equal(t, state.StatusDrifted, rs.Status, "overlay should win over base on read")

	// Overlay add app-new that isn't in base.
	require.NoError(t, store.OverlayPutState(sess, "state_record_reconciliation", "app-new",
		state.ReconciliationState{ApproachID: "app-new", Status: state.StatusLive}))

	ids := view.ListStateRecords()
	assert.ElementsMatch(t, []string{"app-foo", "app-new"}, ids)

	// Overlay delete app-foo masks the base record.
	require.NoError(t, store.OverlayDeleteState(sess, "state_delete_record", "app-foo"))
	_, ok = view.GetState("app-foo")
	assert.False(t, ok, "deleted record masked from view")

	ids = view.ListStateRecords()
	assert.ElementsMatch(t, []string{"app-new"}, ids, "deleted base record excluded from list")
}
