package agent

import (
	"fmt"
	"sync"

	"github.com/chetan/locutus/internal/search"
)

// SwappableSpecSearch is a search.Backend whose underlying delegate can
// be replaced at runtime. The agent-facing spec_search tool registers
// against a single SwappableSpecSearch instance per process; production
// wires the on-disk *search.Index as the initial delegate and the
// spec-generation council swaps in a *search.InFlightIndex for the
// duration of a run (DJ-123 Phase 3).
//
// Why a swap, not a fresh registration: the tool registry is shared
// across all agent calls and not re-wired per verb. Pushing the
// council's in-flight index in via Swap keeps every spec_search tool
// dispatch inside the council pointing at the in-flight corpus without
// re-registering or threading a per-call backend through the dispatch
// path.
//
// Safe for concurrent Search / Swap; Search is taken under an RLock so
// in-flight queries see a consistent delegate even when another
// goroutine is swapping. Swap returns the previous delegate so callers
// can defer-restore the production backend at council end.
type SwappableSpecSearch struct {
	mu      sync.RWMutex
	current search.Backend
	// metrics is the council-scoped SpecSearchMetrics collector (DJ-123
	// Phase 5). Set by generateSpecWithWorkflow at council start,
	// cleared by the same defer that restores the disk backend. The
	// spec_search tool handler reads it through Metrics() and records
	// per-call (query, hit_count, took_ms) into it; nil outside a
	// council run, which makes recording a no-op on the production
	// non-council path.
	metrics *SpecSearchMetrics
}

// NewSwappableSpecSearch returns a SwappableSpecSearch initialized to
// delegate at initial. Passing a nil initial is valid — Search on a
// nil-delegate swappable returns a "no backend wired" error, the same
// shape SearchSpecNodes already surfaces.
func NewSwappableSpecSearch(initial search.Backend) *SwappableSpecSearch {
	return &SwappableSpecSearch{current: initial}
}

// Search dispatches to the current delegate. Returns an error when no
// delegate is wired; mirrors the SearchSpecNodes guard shape so the
// surfaced error reads identically whether the wiring is missing at
// registration time or at swap time.
func (s *SwappableSpecSearch) Search(query string, opts search.Options) ([]search.Hit, int, error) {
	s.mu.RLock()
	cur := s.current
	s.mu.RUnlock()
	if cur == nil {
		return nil, 0, fmt.Errorf("spec_search: no backend wired")
	}
	return cur.Search(query, opts)
}

// Swap replaces the current delegate and returns the previous one. The
// council uses this at run start to push the in-flight index in and at
// run end (via defer) to restore the disk backend. Passing nil parks
// the swappable in the "no backend wired" state; subsequent Search
// calls error until Swap restores a real delegate.
func (s *SwappableSpecSearch) Swap(next search.Backend) search.Backend {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.current
	s.current = next
	return prev
}

// Current returns the live delegate. Test-only inspection helper; the
// production code path goes through Search or Swap.
func (s *SwappableSpecSearch) Current() search.Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// SetMetrics attaches (or, when m is nil, detaches) the council-scoped
// SpecSearchMetrics collector. generateSpecWithWorkflow installs a
// fresh collector at council start and clears it (via SetMetrics(nil))
// at council end so subsequent CLI verbs that reuse the same swappable
// don't see stale metrics. Outside a council run, Metrics() returns
// nil and the spec_search tool handler's recording becomes a no-op.
func (s *SwappableSpecSearch) SetMetrics(m *SpecSearchMetrics) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics = m
}

// Metrics returns the wired collector, or nil when none is installed.
// The spec_search tool handler reads through this on every call;
// SpecSearchMetrics.Record itself is nil-safe, so the handler can hand
// the result straight through without an extra guard.
func (s *SwappableSpecSearch) Metrics() *SpecSearchMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.metrics
}

// Compile-time guard that *SwappableSpecSearch satisfies the read-side
// Backend interface — RegisterSpecTools wires it the same way it wires
// a raw *search.Index.
var _ search.Backend = (*SwappableSpecSearch)(nil)
