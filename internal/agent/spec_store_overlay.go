package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/state"
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
//
// JSON tags are explicit (lowercase) so spec_dry_run_report serializes
// to the documented response shape regardless of Go's default field-
// name lowercasing; for manifest captures (spec_update_goals_md_hash)
// Kind and ID are empty, so omitempty keeps the JSON compact.
type CapturedMutation struct {
	Tool      string    `json:"tool"`
	Kind      SpecKind  `json:"kind,omitempty"`
	ID        string    `json:"id,omitempty"`
	Body      any       `json:"body,omitempty"`
	Timestamp time.Time `json:"timestamp"`
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

	// stateOverrides holds would-be ReconciliationState records keyed
	// by approach id. Written by state_record_reconciliation +
	// state_refresh_artifacts + state_mark_status capture closures
	// under dry-run. Read by OverlayView.GetState. Per DJ-149 §10.
	stateOverrides map[string]*state.ReconciliationState

	// stateDeleted marks approach ids whose state records would be
	// removed by state_delete_record under dry-run. Lookup masks the
	// base FileStateStore the same way spec deleted entries mask the
	// base SpecStore.
	stateDeleted map[string]struct{}
}

// ManifestOverride carries a per-session dry-run capture of a
// manifest-level write. Today only spec_update_goals_md_hash produces
// this; future manifest-mutation tools would land here too.
type ManifestOverride struct {
	GoalsMdHash     string    `json:"goals_md_hash"`
	GoalsMdSyncedAt time.Time `json:"goals_md_synced_at"`
}

func newSessionOverlay() *sessionOverlay {
	return &sessionOverlay{
		entries:        make(map[storeKey]*StoreEntry),
		deleted:        make(map[storeKey]struct{}),
		stateOverrides: make(map[string]*state.ReconciliationState),
		stateDeleted:   make(map[string]struct{}),
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

// putState captures a would-be state record write under dry-run.
// Mirrors put() for spec entries. Appends a CapturedMutation entry
// to surface in spec_dry_run_report's output. Per DJ-149.
func (o *sessionOverlay) putState(tool, approachID string, rs state.ReconciliationState) {
	o.mu.Lock()
	defer o.mu.Unlock()
	rsCopy := rs
	o.stateOverrides[approachID] = &rsCopy
	delete(o.stateDeleted, approachID) // put after delete unmasks
	o.captured = append(o.captured, CapturedMutation{
		Tool:      tool,
		Kind:      KindApproach, // state records are per-approach; reuse the approach kind for report consistency
		ID:        approachID,
		Body:      rsCopy,
		Timestamp: time.Now().UTC(),
	})
}

// deleteState marks an approach's state record as would-be deleted.
// Mirrors delete() for spec entries.
func (o *sessionOverlay) deleteState(tool, approachID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stateDeleted[approachID] = struct{}{}
	delete(o.stateOverrides, approachID) // delete after put removes the would-be record
	o.captured = append(o.captured, CapturedMutation{
		Tool:      tool,
		Kind:      KindApproach,
		ID:        approachID,
		Timestamp: time.Now().UTC(),
	})
}

// lookupState returns (record, true) when the overlay holds a
// would-be state record for approachID; (nil, false) when it
// doesn't OR when stateDeleted masks the lookup. Returned pointer
// is the overlay's live record; callers must treat as read-only.
func (o *sessionOverlay) lookupState(approachID string) (*state.ReconciliationState, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if _, gone := o.stateDeleted[approachID]; gone {
		return nil, false
	}
	rs, ok := o.stateOverrides[approachID]
	return rs, ok
}

// isStateDeleted reports whether this session has marked the
// approach's state record for deletion. Used by OverlayView read
// merging (Task 7) to mask the base FileStateStore.
func (o *sessionOverlay) isStateDeleted(approachID string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	_, gone := o.stateDeleted[approachID]
	return gone
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

// Manifest returns the spec graph manifest with overlay entries
// layered on the base store: overlay-only entries appear (with
// origin=proposed), overlay-deleted entries are masked, overlay-
// revised entries replace the base row with the new title/summary.
// The ManifestOverride (DJ-147) replaces GoalsMdHash + GoalsMdSyncedAt
// for the dry-run view.
//
// Sessions without an overlay get the base manifest unchanged.
func (v *OverlayView) Manifest() SpecManifest {
	base := v.store.ListManifest()
	if v.overlay == nil {
		return base
	}
	v.overlay.mu.RLock()
	defer v.overlay.mu.RUnlock()
	return mergeOverlayIntoManifest(base, v.overlay)
}

// Captured returns the session's ordered capture list, or nil for
// non-dry-run sessions.
func (v *OverlayView) Captured() []CapturedMutation {
	if v.overlay == nil {
		return nil
	}
	return v.overlay.capturedList()
}

// GetSpec returns the batched lookup result for the given ids,
// consulting the overlay first for each id (overlay revisions win;
// overlay-only entries surface as in_flight; overlay-deleted entries
// report missing). Ids not held by the overlay fall through to the
// base store via SpecStore.GetSpec, and the two result sets are merged.
//
// For non-dry-run sessions (overlay==nil) this is a direct passthrough
// to SpecStore.GetSpec, preserving byte-compatible output for read
// tools wired through OverlayView unconditionally.
func (v *OverlayView) GetSpec(ids []string) SpecGetResult {
	if v.overlay == nil {
		return v.store.GetSpec(ids)
	}

	// Partition: ids the overlay holds (entry or delete-mask) vs ids to
	// forward to the base store. The base store handles malformed-id
	// validation and AvailableIDs population, so we only short-circuit
	// when the overlay has a definitive answer.
	result := SpecGetResult{Results: make(map[string]SpecGetEntry, len(ids))}
	var passthroughIDs []string
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		kind, ok := specKindOfID(id)
		if !ok {
			// Malformed id — defer to base store's validation message so
			// the error text stays consistent across sessions.
			passthroughIDs = append(passthroughIDs, raw)
			continue
		}
		entry, found, deleted := v.overlay.lookupWithMask(kind, id)
		if deleted {
			result.Results[id] = SpecGetEntry{
				Status: SpecGetMissing,
				Reason: fmt.Sprintf("no node with id %q", id),
			}
			continue
		}
		if found {
			result.Results[id] = SpecGetEntry{
				Status: SpecGetInFlight,
				Body:   entry.Body,
			}
			continue
		}
		passthroughIDs = append(passthroughIDs, raw)
	}

	if len(passthroughIDs) > 0 {
		base := v.store.GetSpec(passthroughIDs)
		for k, v := range base.Results {
			result.Results[k] = v
		}
		if len(base.AvailableIDs) > 0 {
			result.AvailableIDs = base.AvailableIDs
		}
		if len(base.Working) > 0 {
			result.Working = append(result.Working, base.Working...)
		}
	}
	return result
}

// mergeOverlayIntoManifest layers the overlay's entries / deletes /
// manifest-override onto a base SpecManifest. Called under the
// overlay's RLock. The base SpecManifest was produced under
// SpecStore's RLock and is safe to mutate (it's already a copy).
func mergeOverlayIntoManifest(base SpecManifest, o *sessionOverlay) SpecManifest {
	// Mask deletes: drop rows whose (kind, id) is in o.deleted.
	base.Goals = filterManifestEntries(base.Goals, KindGoal, o.deleted)
	base.AntiGoals = filterManifestEntries(base.AntiGoals, KindAntiGoal, o.deleted)
	base.Features = filterManifestEntries(base.Features, KindFeature, o.deleted)
	base.Strategies = filterManifestEntries(base.Strategies, KindStrategy, o.deleted)
	base.Decisions = filterManifestEntries(base.Decisions, KindDecision, o.deleted)
	base.Bugs = filterManifestEntries(base.Bugs, KindBug, o.deleted)
	base.Approaches = filterManifestEntries(base.Approaches, KindApproach, o.deleted)

	// Layer entries: overlay revisions replace base rows in place;
	// overlay-only entries append (origin=proposed).
	for key, entry := range o.entries {
		row := manifestEntryFor(entry)
		base = upsertManifestRow(base, key.Kind, row)
	}

	// ManifestOverride: replace GoalsMdHash + GoalsMdSyncedAt for the
	// dry-run view. The base manifest already carries the persisted
	// hash; the override wins.
	if o.manifestOverride != nil {
		base.GoalsMdHash = o.manifestOverride.GoalsMdHash
		base.GoalsMdSyncedAt = o.manifestOverride.GoalsMdSyncedAt
	}
	return base
}

// filterManifestEntries drops rows whose (kind, id) is in the
// deleted set. Returns the original slice when no rows are masked.
func filterManifestEntries(entries []SpecManifestEntry, kind SpecKind, deleted map[storeKey]struct{}) []SpecManifestEntry {
	if len(deleted) == 0 || len(entries) == 0 {
		return entries
	}
	out := entries[:0:0]
	for _, e := range entries {
		if _, gone := deleted[storeKey{Kind: kind, ID: e.ID}]; gone {
			continue
		}
		out = append(out, e)
	}
	return out
}

// upsertManifestRow replaces a base manifest row with the same id, or
// appends if no matching base row exists. Routes by kind to the
// correct per-kind slice.
func upsertManifestRow(m SpecManifest, kind SpecKind, row SpecManifestEntry) SpecManifest {
	switch kind {
	case KindGoal:
		m.Goals = replaceOrAppendManifestRow(m.Goals, row)
	case KindAntiGoal:
		m.AntiGoals = replaceOrAppendManifestRow(m.AntiGoals, row)
	case KindFeature:
		m.Features = replaceOrAppendManifestRow(m.Features, row)
	case KindStrategy:
		m.Strategies = replaceOrAppendManifestRow(m.Strategies, row)
	case KindDecision:
		m.Decisions = replaceOrAppendManifestRow(m.Decisions, row)
	case KindBug:
		m.Bugs = replaceOrAppendManifestRow(m.Bugs, row)
	case KindApproach:
		m.Approaches = replaceOrAppendManifestRow(m.Approaches, row)
	}
	return m
}

// replaceOrAppendManifestRow replaces the row in entries whose ID
// matches row.ID, or appends row if no match exists.
func replaceOrAppendManifestRow(entries []SpecManifestEntry, row SpecManifestEntry) []SpecManifestEntry {
	for i, e := range entries {
		if e.ID == row.ID {
			entries[i] = row
			return entries
		}
	}
	return append(entries, row)
}

// manifestEntryFor produces a SpecManifestEntry from a captured
// overlay *StoreEntry. Mirrors the per-kind summary-field selection
// SpecStore.ListManifest uses on the base maps.
func manifestEntryFor(e *StoreEntry) SpecManifestEntry {
	row := SpecManifestEntry{
		ID:     e.ID,
		Origin: OriginProposed,
	}
	switch b := e.Body.(type) {
	case spec.Goal:
		row.Title = b.Title
		row.Summary = summaryOrFallback("", b.Body)
	case spec.AntiGoal:
		row.Title = b.Title
		row.Summary = summaryOrFallback("", b.Body)
	case spec.Feature:
		row.Title = b.Title
		row.Summary = summaryOrFallback(b.Summary, b.Description)
	case spec.Strategy:
		row.Title = b.Title
		row.Kind = string(b.Kind)
		row.Summary = summaryOrFallback(b.Summary, "")
	case spec.Decision:
		row.Title = b.Title
		row.Summary = summaryOrFallback(b.Summary, b.Rationale)
	case spec.Bug:
		row.Title = b.Title
		row.Summary = summaryOrFallback(b.Summary, b.Description)
	case spec.Approach:
		row.Title = b.Title
		row.Summary = strings.TrimSpace(b.Summary)
	}
	return row
}

// specKindOfID routes an id to its SpecKind via the kebab-prefix
// convention. The MCP read tools accept already-validated ids — this
// helper is a cheap dispatch, not a validator (malformed ids fall
// through to the base store for the canonical error message).
func specKindOfID(id string) (SpecKind, bool) {
	switch {
	case strings.HasPrefix(id, "goal-"):
		return KindGoal, true
	case strings.HasPrefix(id, "agoal-"):
		return KindAntiGoal, true
	case strings.HasPrefix(id, "feat-"):
		return KindFeature, true
	case strings.HasPrefix(id, "strat-"):
		return KindStrategy, true
	case strings.HasPrefix(id, "dec-"):
		return KindDecision, true
	case strings.HasPrefix(id, "bug-"):
		return KindBug, true
	case strings.HasPrefix(id, "app-"):
		return KindApproach, true
	}
	return "", false
}
