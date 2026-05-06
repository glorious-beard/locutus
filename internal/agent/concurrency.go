package agent

import (
	"context"
	"sync"
)

// ConcurrencyManager throttles in-flight calls per (provider, model)
// using a buffered-channel semaphore per key. Tuned to bound fanout-
// shaped workloads (one elaborator per outline node) below provider
// RPM ceilings without coordination across the workflow. Keys are
// "<provider>/<model>" so multiple providers serving similar model
// strings (e.g. "gemini-2.5-flash") cap independently.
//
// Caps are taken from models.yaml's per-tier `concurrent_requests`
// knob and threaded through ResolvedModel.ConcurrentRequests; a
// zero or negative cap means "unbounded" and Acquire returns a
// no-op release.
type ConcurrencyManager struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

// NewConcurrencyManager returns an empty manager. Per-key
// semaphores are created lazily on first Acquire so unconfigured
// (provider, model) keys incur zero overhead.
func NewConcurrencyManager() *ConcurrencyManager {
	return &ConcurrencyManager{slots: map[string]chan struct{}{}}
}

// Acquire claims one slot in the (provider, model) semaphore.
// Returns a release closure that the caller must invoke (deferring
// is fine) when the call returns; failing to release leaks a slot.
//
// cap <= 0 means no throttling — release is a no-op. Honors ctx
// cancellation so a stuck queue surfaces as the caller's timeout
// rather than a hung goroutine.
func (m *ConcurrencyManager) Acquire(ctx context.Context, provider, model string, cap int) (func(), error) {
	if cap <= 0 {
		return func() {}, nil
	}
	sem := m.semaphoreFor(provider+"/"+model, cap)
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
}

// semaphoreFor returns the channel for a key, creating it on first
// use. The cap is fixed at the first Acquire — subsequent calls with
// a different cap reuse the original channel. Process-wide knobs
// like models.yaml are stable across a run; this matches that
// invariant without re-locking on every acquire.
func (m *ConcurrencyManager) semaphoreFor(key string, cap int) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	sem, ok := m.slots[key]
	if !ok {
		sem = make(chan struct{}, cap)
		m.slots[key] = sem
	}
	return sem
}
