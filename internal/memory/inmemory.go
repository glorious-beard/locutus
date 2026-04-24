package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// InMemoryService keeps entries in process memory, keyed by namespace.
// Safe for concurrent use.
type InMemoryService struct {
	mu      sync.RWMutex
	entries map[string][]Entry
}

// NewInMemoryService returns an empty InMemoryService.
func NewInMemoryService() *InMemoryService {
	return &InMemoryService{entries: make(map[string][]Entry)}
}

// AddSessionToMemory appends prepared entries to the namespace's list.
func (s *InMemoryService) AddSessionToMemory(ctx context.Context, namespace string, entries []Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := prepareEntries(namespace, entries)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[namespace] = append(s.entries[namespace], prepared...)
	return nil
}

// SearchMemory returns matching entries newest first.
func (s *InMemoryService) SearchMemory(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("memory: nil search request")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []Entry
	if req.Namespace == "" {
		for _, list := range s.entries {
			matched = append(matched, filterEntries(list, req.Query)...)
		}
	} else {
		matched = filterEntries(s.entries[req.Namespace], req.Query)
	}
	sortNewestFirst(matched)
	if req.Limit > 0 && len(matched) > req.Limit {
		matched = matched[:req.Limit]
	}
	return &SearchResponse{Entries: matched}, nil
}

// prepareEntries validates and normalizes a batch. Returns a fresh slice so
// the caller retains ownership of the input.
func prepareEntries(namespace string, in []Entry) ([]Entry, error) {
	if namespace == "" {
		return nil, fmt.Errorf("memory: namespace required")
	}
	out := make([]Entry, 0, len(in))
	for i, e := range in {
		if e.Content == "" {
			return nil, fmt.Errorf("%w: entry %d has empty content", ErrInvalidEntry, i)
		}
		if e.Namespace != "" && e.Namespace != namespace {
			return nil, fmt.Errorf("%w: entry %d namespace %q differs from batch %q",
				ErrInvalidEntry, i, e.Namespace, namespace)
		}
		if e.ID == "" {
			e.ID = uuid.NewString()
		}
		if e.Timestamp.IsZero() {
			e.Timestamp = time.Now().UTC()
		}
		e.Namespace = namespace
		out = append(out, e)
	}
	return out, nil
}

func filterEntries(list []Entry, query string) []Entry {
	if query == "" {
		out := make([]Entry, len(list))
		copy(out, list)
		return out
	}
	q := strings.ToLower(query)
	var out []Entry
	for _, e := range list {
		if strings.Contains(strings.ToLower(e.Content), q) {
			out = append(out, e)
		}
	}
	return out
}

func sortNewestFirst(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
}
