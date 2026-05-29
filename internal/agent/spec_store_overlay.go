package agent

import (
	"sync"
	"time"
)

// StoreEntry is the overlay's typed view of a would-be spec graph
// entry. Body is the same typed value (spec.Decision / spec.Feature /
// ...) that SpecStore.Put would have persisted under a normal write;
// Origin tags it as proposed so the read-path merge can present it
// alongside settled entries with the right disposition. Introduced by
// DJ-147 for the dry-run capture path; SpecStore integration (Task 2)
// reuses this shape on lookups that merge overlay-over-store.
type StoreEntry struct {
	Kind   SpecKind
	ID     string
	Body   any
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
type sessionOverlay struct {
	mu       sync.RWMutex
	entries  map[storeKey]*StoreEntry
	deleted  map[storeKey]struct{}
	captured []CapturedMutation
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

// capturedList returns a defensive copy of the ordered capture so
// callers can iterate without holding the overlay's lock.
func (o *sessionOverlay) capturedList() []CapturedMutation {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]CapturedMutation, len(o.captured))
	copy(out, o.captured)
	return out
}
