package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// InMemoryStore keeps events in process memory, keyed by workstream ID.
// Safe for concurrent use.
type InMemoryStore struct {
	mu     sync.RWMutex
	events map[string][]SessionEvent
}

// NewInMemoryStore returns an empty InMemoryStore.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{events: make(map[string][]SessionEvent)}
}

// Append writes a single event to its workstream's list.
func (s *InMemoryStore) Append(ctx context.Context, ev SessionEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := prepareEvent(ev)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[prepared.WorkstreamID] = append(s.events[prepared.WorkstreamID], prepared)
	return nil
}

// Read returns matching events, oldest first.
func (s *InMemoryStore) Read(ctx context.Context, workstreamID string, opts ReadOpts) ([]SessionEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return filterByRange(s.events[workstreamID], opts), nil
}

// prepareEvent validates and normalizes an event before storage.
func prepareEvent(ev SessionEvent) (SessionEvent, error) {
	if ev.WorkstreamID == "" {
		return SessionEvent{}, fmt.Errorf("%w: missing workstream_id", ErrInvalidEvent)
	}
	if ev.Role == "" {
		return SessionEvent{}, fmt.Errorf("%w: missing role", ErrInvalidEvent)
	}
	if ev.Content == "" {
		return SessionEvent{}, fmt.Errorf("%w: missing content", ErrInvalidEvent)
	}
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	return ev, nil
}

// filterByRange applies Since / Until / Limit to an ordered event slice.
// Bounds are inclusive. A fresh slice is returned so callers can't mutate
// the store's state.
func filterByRange(list []SessionEvent, opts ReadOpts) []SessionEvent {
	out := make([]SessionEvent, 0, len(list))
	for _, ev := range list {
		if !opts.Since.IsZero() && ev.Timestamp.Before(opts.Since) {
			continue
		}
		if !opts.Until.IsZero() && ev.Timestamp.After(opts.Until) {
			continue
		}
		out = append(out, ev)
	}
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out
}
