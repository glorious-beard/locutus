package agent

import (
	"sync"
	"time"
)

// StoreEntry is the overlay's typed view of a would-be spec graph
// entry. Body is the same typed value (spec.Decision / spec.Feature /
// ...) that SpecStore.Put would have persisted under a normal write.
// Introduced by DJ-147 for the dry-run capture path; SpecStore
// integration (Task 2) reuses this shape on lookups that merge
// overlay-over-store.
type StoreEntry struct {
	Kind SpecKind
	ID   string
	Body any
	// Origin is always OriginProposed for overlay-held entries; the
	// field exists so the read-path merger in OverlayView.Lookup can
	// return a unified *StoreEntry shape across overlay and base-store
	// entries.
	Origin SpecManifestOrigin
}

// storeKey is the (kind, id) tuple the overlay's maps key on. Kept
// unexported because the overlay is the only consumer: callers reach
// in by kind+id, never by composite key.
type storeKey struct {
	Kind SpecKind
	ID   string
}

// CapturedMutation records one would-be mutation that a dry-run
// session emitted. The CLI's tools.jsonl-based render and the
// spec_dry_run_report MCP tool both surface these in capture order.
//
// Body is the typed entry (spec.Decision / spec.Feature / ...) the
// build*Body helper produced — same shape as what SpecStore.Put would
// have persisted under a normal write.
type CapturedMutation struct {
	Tool      string
	Kind      SpecKind
	ID        string
	Body      any
	Timestamp time.Time
}

// sessionOverlay holds a single dry-run MCP session's would-be
// mutations. Per DJ-147 §3: written by the captureOnly wrapper,
// consulted on every spec_list_manifest / spec_get / spec_search call
// from the same session via OverlayView, discarded at session close.
//
// The overlay is intentionally NOT a copy of the base store. It holds
// only what this session has written (entries + deleted), so memory
// scales with the captured mutation set, not the graph size.
//
// manifestOverride carries the would-be (goals_md_hash, goals_md_synced_at)
// pair from spec_update_goals_md_hash. The hash update doesn't fit the
// per-kind StoreEntry shape (it's a top-level manifest field, not a
// spec graph node) so it lands as a separate slot. nil means
// "no captured manifest mutation this session."
type sessionOverlay struct {
	mu               sync.RWMutex
	entries          map[storeKey]*StoreEntry
	deleted          map[storeKey]struct{}
	captured         []CapturedMutation
	manifestOverride *ManifestOverride
}

// ManifestOverride carries a per-session dry-run capture of a
// manifest-level write. Today only spec_update_goals_md_hash produces
// this; future manifest-mutation tools would land here too.
type ManifestOverride struct {
	GoalsMdHash     string
	GoalsMdSyncedAt time.Time
}

func newSessionOverlay() *sessionOverlay {
	return &sessionOverlay{
		entries: make(map[storeKey]*StoreEntry),
		deleted: make(map[storeKey]struct{}),
	}
}

func (o *sessionOverlay) put(tool string, kind SpecKind, id string, body any) {
	key := storeKey{Kind: kind, ID: id}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries[key] = &StoreEntry{Kind: kind, ID: id, Body: body, Origin: OriginProposed}
	delete(o.deleted, key) // a put after a delete un-masks
	o.captured = append(o.captured, CapturedMutation{
		Tool:      tool,
		Kind:      kind,
		ID:        id,
		Body:      body,
		Timestamp: time.Now().UTC(),
	})
}

func (o *sessionOverlay) delete(tool string, kind SpecKind, id string) {
	key := storeKey{Kind: kind, ID: id}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.deleted[key] = struct{}{}
	delete(o.entries, key) // a delete after a put removes the would-be entry
	o.captured = append(o.captured, CapturedMutation{
		Tool:      tool,
		Kind:      kind,
		ID:        id,
		Timestamp: time.Now().UTC(),
	})
}

// lookup returns (entry, true) when the overlay holds a would-be entry
// for (kind, id), (nil, false) when it doesn't. A deleted key reports
// (nil, false) — masking the base store on the read path.
//
// The returned pointer references the overlay's live entry; callers
// must treat it as read-only — mutation outside the overlay's lock
// would race with concurrent put/delete.
func (o *sessionOverlay) lookup(kind SpecKind, id string) (*StoreEntry, bool) {
	key := storeKey{Kind: kind, ID: id}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if _, gone := o.deleted[key]; gone {
		return nil, false
	}
	entry, ok := o.entries[key]
	return entry, ok
}

// isDeleted reports whether (kind, id) is marked deleted by this
// session. Used by the read-path merge to mask the base store.
func (o *sessionOverlay) isDeleted(kind SpecKind, id string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	_, gone := o.deleted[storeKey{Kind: kind, ID: id}]
	return gone
}

// lookupWithMask is the atomic compound used by OverlayView.Lookup:
// returns (entry, found, deletedMask) under a single RLock acquisition
// so a concurrent put/delete cannot interleave between the deletion
// check and the entry lookup.
func (o *sessionOverlay) lookupWithMask(kind SpecKind, id string) (*StoreEntry, bool, bool) {
	key := storeKey{Kind: kind, ID: id}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if _, gone := o.deleted[key]; gone {
		return nil, false, true
	}
	entry, ok := o.entries[key]
	return entry, ok, false
}

// capturedList returns a defensive copy of the ordered capture so
// callers can iterate without holding the overlay's lock.
func (o *sessionOverlay) capturedList() []CapturedMutation {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]CapturedMutation, len(o.captured))
	copy(out, o.captured)
	return out
}

// setGoalsMdHash captures a would-be spec_update_goals_md_hash call.
// Last-write-wins on the override slot; each call also lands a fresh
// CapturedMutation in the ordered list so spec_dry_run_report (Task 7)
// can surface the sequence.
func (o *sessionOverlay) setGoalsMdHash(hash string, syncedAt time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.manifestOverride = &ManifestOverride{GoalsMdHash: hash, GoalsMdSyncedAt: syncedAt}
	o.captured = append(o.captured, CapturedMutation{
		Tool:      "spec_update_goals_md_hash",
		Body:      ManifestOverride{GoalsMdHash: hash, GoalsMdSyncedAt: syncedAt},
		Timestamp: time.Now().UTC(),
	})
}

// manifestOverrideOrNil returns the would-be manifest override, or
// nil when no spec_update_goals_md_hash call has been captured.
func (o *sessionOverlay) manifestOverrideOrNil() *ManifestOverride {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.manifestOverride == nil {
		return nil
	}
	c := *o.manifestOverride
	return &c
}

// OverlayView is the read merger consulted by spec_list_manifest /
// spec_get / spec_search. It consults the session's overlay first
// (deleted-key masks, overlay-held entries win), then falls back to the
// base SpecStore.
//
// Sessions without an overlay (the non-dry-run common case) get a view
// whose Lookup is a passthrough to the base store — safe to call
// unconditionally on every read.
type OverlayView struct {
	store   *SpecStore
	overlay *sessionOverlay // may be nil
}

// OverlayView returns the per-session read merger. sess may be a
// session handle with a registered overlay or any other value (in
// which case the view passes straight through to the base store).
func (s *SpecStore) OverlayView(sess any) *OverlayView {
	return &OverlayView{store: s, overlay: s.overlayFor(sess)}
}

// Lookup returns the entry for (kind, id), consulting the overlay
// first. A deleted overlay entry masks the base store.
//
// The returned pointer references either the overlay's live entry or
// a freshly-built adapter over the base-store's per-kind entry; in
// both cases callers must treat it as read-only.
func (v *OverlayView) Lookup(kind SpecKind, id string) (*StoreEntry, bool) {
	if v.overlay != nil {
		entry, found, deleted := v.overlay.lookupWithMask(kind, id)
		if deleted {
			return nil, false
		}
		if found {
			return entry, true
		}
	}
	return v.store.lookupEntry(kind, id)
}
