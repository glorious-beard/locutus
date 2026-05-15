package agent

import (
	"sync"
)

// SpecSearchCallRecord is one spec_search invocation captured by the
// council-scoped metrics collector (DJ-123 Phase 5). Operator-facing
// instrumentation only — never exported to an LLM schema; carries no
// jsonschema tags because the agent surface (SpecSearchResult) is the
// LLM-facing shape, not this one.
//
// Status values: "success" (HitCount > 0), "empty" (HitCount == 0), or
// "error" (the backend call failed). Empty + error are distinguished
// because the DJ-123 reversal criterion (a) — a >25% empty-result rate
// signalling BM25-only is no longer viable — counts only the "empty"
// status. An error means the query never reached the backend in a
// usable shape and so contributes neither to "empty" nor to
// "successful" coverage.
//
// HitCount mirrors len(SpecSearchResult.Hits) — what the agent
// actually sees in its tool response after the per-call Limit was
// applied. We deliberately do NOT use SpecSearchResult.TotalMatches
// here: the agent's interpretation of "I got nothing back" follows
// what landed in its turn, not the unbounded match count.
type SpecSearchCallRecord struct {
	Query     string
	HitCount  int
	TookMs    int64
	Status    string
	ErrorText string
}

// SpecSearchMetrics is the council-scoped collector wired by
// generateSpecWithWorkflow at council start and detached at council
// end (DJ-123 Phase 5). The spec_search tool handler records one
// SpecSearchCallRecord per invocation; the council teardown logs the
// aggregate (total calls, empty-result rate) via slog.Info so the
// reversal-criteria threshold can be measured from operator-side logs.
//
// Thread-safe under a mutex: spec_search fires from parallel
// elaborator goroutines under fanout, so concurrent Record calls are
// the common case.
type SpecSearchMetrics struct {
	mu    sync.Mutex
	calls []SpecSearchCallRecord
}

// Record appends one call to the collector. A nil receiver is a no-op
// so the spec_search handler can call this unconditionally — outside a
// council run the collector pointer is nil and recording is skipped.
func (m *SpecSearchMetrics) Record(r SpecSearchCallRecord) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, r)
}

// Snapshot returns a defensive copy of the recorded calls. Callers
// (the council teardown logger; tests asserting captured records) read
// through Snapshot rather than mutating the collector's slice
// directly. Nil receiver returns nil.
func (m *SpecSearchMetrics) Snapshot() []SpecSearchCallRecord {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SpecSearchCallRecord, len(m.calls))
	copy(out, m.calls)
	return out
}

// SpecSearchAggregate is the per-council summary computed from the
// collector at council teardown. EmptyRate is the fraction of calls
// that returned zero hits, in [0, 1] — feeds the DJ-123 reversal
// criterion (a) threshold of 0.25. Computed only over calls that
// completed (Status of "empty" or "success"); errors are excluded
// from the denominator so a backend that blew up on every call
// doesn't masquerade as a "100% empty" coverage failure.
type SpecSearchAggregate struct {
	TotalCalls     int
	CompletedCalls int
	EmptyCalls     int
	ErrorCalls     int
	EmptyRate      float64
}

// Aggregate computes the council-level summary over the recorded
// calls. Nil receiver returns a zero-value aggregate.
func (m *SpecSearchMetrics) Aggregate() SpecSearchAggregate {
	snap := m.Snapshot()
	agg := SpecSearchAggregate{TotalCalls: len(snap)}
	for _, c := range snap {
		switch c.Status {
		case "empty":
			agg.EmptyCalls++
			agg.CompletedCalls++
		case "success":
			agg.CompletedCalls++
		case "error":
			agg.ErrorCalls++
		}
	}
	if agg.CompletedCalls > 0 {
		agg.EmptyRate = float64(agg.EmptyCalls) / float64(agg.CompletedCalls)
	}
	return agg
}

// SpecSearchEmptyRateThreshold is the DJ-123 reversal-criterion (a)
// threshold: a council whose spec_search calls return zero hits on
// more than 25% of invocations signals that BM25-only is failing the
// in-flight surface and the in-flight design needs to be reconsidered.
// Surfaced via the slog.Info aggregate at council end so operators can
// compare measured rate to the threshold without parsing it from
// prose.
const SpecSearchEmptyRateThreshold = 0.25
