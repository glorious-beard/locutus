package mcp

import (
	"fmt"
	"sync"
	"time"
)

// loopRecord tracks the deterministic iteration state of one interactive
// self-loop run, keyed by (sessionToken, activity, target). It is returned to
// callers by value so stored state cannot be mutated outside the store.
type loopRecord struct {
	iteration   int       // completed iterations; fresh = 0
	maxIter     int       // iteration cap for this run
	converged   bool      // last reported convergence verdict
	lastVerdict string    // last reported reason text
	runID       string    // server-internal, logging/correlation only
	updatedAt   time.Time // for TTL GC
}

// loopKey identifies a self-loop run. sessionToken is supplied by the tool
// layer; the store never imports MCP types.
type loopKey struct{ sessionToken, activity, target string }

// loopStore is the daemon-side in-memory store backing the loop-tracking MCP
// tools. It is safe for concurrent use.
type loopStore struct {
	mu      sync.Mutex
	records map[loopKey]*loopRecord
	seq     uint64 // for runID minting
}

// newLoopStore returns a store with an initialized records map.
func newLoopStore() *loopStore {
	return &loopStore{records: make(map[loopKey]*loopRecord)}
}

// Begin starts (or recovers) a run for key. If a live record already exists it
// is returned unchanged (recovery preserves the in-flight iteration and its
// original maxIter; the passed maxIter is ignored) with updatedAt refreshed.
// Otherwise a fresh record is allocated at iteration 0 with a newly minted
// runID. The returned record is a copy.
func (s *loopStore) Begin(key loopKey, maxIter int) loopRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if rec, ok := s.records[key]; ok {
		rec.updatedAt = now
		return *rec
	}

	s.seq++
	rec := &loopRecord{
		iteration:   0,
		maxIter:     maxIter,
		converged:   false,
		lastVerdict: "",
		runID:       fmt.Sprintf("run-%d", s.seq),
		updatedAt:   now,
	}
	s.records[key] = rec
	return *rec
}

// Status returns a copy of the record for key and true if present, else the
// zero record and false.
func (s *loopStore) Status(key loopKey) (loopRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec, ok := s.records[key]; ok {
		return *rec, true
	}
	return loopRecord{}, false
}

// Advance records an iteration result for key. Advancing a missing key is a
// defensive no-op returning (zero, false). Otherwise it records the verdict,
// increments the iteration count, and computes cont = !converged && iteration <
// maxIter. When the run is terminal (cont == false) the record is deleted so a
// later Begin starts fresh; the incremented record is still returned by copy.
func (s *loopStore) Advance(key loopKey, converged bool, reason string) (rec loopRecord, cont bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, ok := s.records[key]
	if !ok {
		return loopRecord{}, false
	}

	stored.lastVerdict = reason
	stored.converged = converged
	stored.iteration++
	stored.updatedAt = time.Now()

	cont = !converged && stored.iteration < stored.maxIter
	out := *stored
	if !cont {
		delete(s.records, key)
	}
	return out, cont
}

// GC deletes every record whose age strictly exceeds ttl. A non-positive ttl
// collects nothing: with ttl == 0 a record is "older than now - 0" only if it
// predates the GC instant, but a just-touched record shares that instant for
// our purposes, so it survives (matching "updatedAt == now is not older than
// now - 0"). Defensive cleanup for abandoned loops (an agent that quits mid-run
// without converging or hitting the cap); terminal records are already deleted
// by Advance. NOT yet wired to a scheduler (DJ-142 Future Work) — abandoned
// records currently clear on daemon restart, acceptable for a per-project
// singleton daemon.
func (s *loopStore) GC(ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ttl <= 0 {
		return
	}
	now := time.Now()
	for key, rec := range s.records {
		if now.Sub(rec.updatedAt) > ttl {
			delete(s.records, key)
		}
	}
}
