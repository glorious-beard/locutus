// DJ-125 Phase 3 — spec_list_manifest / spec_get backend swap.
//
// Generalises the DJ-123 SwappableSpecSearch pattern to the two other
// RAG tools (spec_list_manifest and spec_get) so the council can redirect
// them at run start. During the spec-generation council the agents read
// the in-flight RawProposal (and the loaded state.Existing snapshot)
// instead of the on-disk graph; outside the council the tools fall back
// to walking specio.FS. Outputs match the on-disk SpecManifest /
// LookupSpecNode shape verbatim so callers see one tool surface.
//
// The swap-and-restore lifecycle mirrors SwappableSpecSearch:
// generateSpecWithWorkflow pushes an in-flight provider in at council
// start and restores the previous (on-disk-backed) provider in the
// deferred teardown. Mock executors that don't wire the swappables
// degrade gracefully — the council just keeps the disk-backed default.

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// SpecManifestProvider is the read-side seam for spec_list_manifest.
// On-disk implementations walk specio.FS; in-flight implementations
// parse the council's RawProposal.
type SpecManifestProvider interface {
	ListManifest() (SpecManifest, error)
}

// SpecGetProvider is the read-side seam for spec_get. On-disk
// implementations walk specio.FS; in-flight implementations look the
// id up against the council's RawProposal (falling back to the loaded
// state.Existing snapshot when the in-flight proposal doesn't carry
// the id yet — that's still in-process data, never a fresh disk read).
type SpecGetProvider interface {
	GetSpec(id string) (json.RawMessage, error)
}

// fsSpecProvider is the on-disk default. ListManifest walks
// `.borg/spec/`; GetSpec resolves the id through LookupSpecNode.
type fsSpecProvider struct{ fsys specio.FS }

func (p *fsSpecProvider) ListManifest() (SpecManifest, error) {
	if p == nil || p.fsys == nil {
		return SpecManifest{}, nil
	}
	return BuildSpecManifest(p.fsys), nil
}

func (p *fsSpecProvider) GetSpec(id string) (json.RawMessage, error) {
	if p == nil || p.fsys == nil {
		return nil, fmt.Errorf("spec_get: no backing store wired")
	}
	return LookupSpecNode(p.fsys, id)
}

// InFlightSpecStore is the council-scoped overlay that backs the
// manifest/get tools while a spec-generation run is in flight. It
// carries the current RawProposal JSON plus the loaded state.Existing
// snapshot (already parsed into typed nodes) so the tools see both the
// emerging proposal and the persisted graph the run was kicked off
// against.
//
// Update is called by every merge function that mutates RawProposal
// (mergeDecisions, mergeNarrative, mergeReconciledProposal, …) so the
// next agent's tool call sees the freshest content. Concurrent reads
// against Updates are serialised via mu so a Tool dispatch in flight
// can't observe a torn proposal mid-write.
type InFlightSpecStore struct {
	mu          sync.RWMutex
	rawProposal string
	existing    *ExistingSpec
}

// NewInFlightSpecStore returns an empty store. SetState (or Update)
// populates it before any tool dispatch sees it.
func NewInFlightSpecStore() *InFlightSpecStore {
	return &InFlightSpecStore{}
}

// SetState captures the council's PlanningState references the
// in-flight tools need to satisfy reads. Call this at council start
// (once the state pointer is alive) and again on every merge that
// touches RawProposal. existing is captured by reference — its
// contents stay stable for the lifetime of one council run.
func (s *InFlightSpecStore) SetState(rawProposal string, existing *ExistingSpec) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.rawProposal = rawProposal
	s.existing = existing
	s.mu.Unlock()
}

// Update refreshes only the RawProposal pointer. Convenience entry
// point for the merge helpers (existing doesn't change mid-run).
func (s *InFlightSpecStore) Update(rawProposal string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.rawProposal = rawProposal
	s.mu.Unlock()
}

// snapshot grabs the current rawProposal + existing under the read
// lock so the per-tool builders can release the lock before doing the
// (potentially slower) parse.
func (s *InFlightSpecStore) snapshot() (string, *ExistingSpec) {
	if s == nil {
		return "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rawProposal, s.existing
}

// ListManifest builds an on-disk-shape SpecManifest from the in-flight
// proposal plus the loaded existing snapshot. In-flight entries win
// when an ID collides with an existing entry (the council is rewriting
// that node). The shape matches BuildSpecManifest(fsys) so the tool
// surface stays uniform.
func (s *InFlightSpecStore) ListManifest() (SpecManifest, error) {
	raw, existing := s.snapshot()
	manifest := SpecManifest{}

	var prop RawSpecProposal
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &prop); err != nil {
			return SpecManifest{}, fmt.Errorf("spec_list_manifest: parse in-flight proposal: %w", err)
		}
	}

	seenFeatures := make(map[string]struct{}, len(prop.Features))
	for _, f := range prop.Features {
		if f.ID == "" {
			continue
		}
		seenFeatures[f.ID] = struct{}{}
		manifest.Features = append(manifest.Features, SpecManifestEntry{
			ID:      f.ID,
			Title:   f.Title,
			Summary: summaryOrFallback(f.Summary, f.Description),
		})
	}
	seenStrategies := make(map[string]struct{}, len(prop.Strategies))
	for _, st := range prop.Strategies {
		if st.ID == "" {
			continue
		}
		seenStrategies[st.ID] = struct{}{}
		manifest.Strategies = append(manifest.Strategies, SpecManifestEntry{
			ID:      st.ID,
			Title:   st.Title,
			Kind:    st.Kind,
			Summary: summaryOrFallback(st.Summary, st.Body),
		})
	}
	seenDecisions := make(map[string]struct{}, len(prop.Decisions))
	for _, d := range prop.Decisions {
		if d.ID == "" {
			continue
		}
		seenDecisions[d.ID] = struct{}{}
		manifest.Decisions = append(manifest.Decisions, SpecManifestEntry{
			ID:      d.ID,
			Title:   d.Title,
			Summary: summaryOrFallback(d.Summary, d.Rationale),
		})
	}

	if existing != nil {
		for _, f := range existing.Features {
			if _, dup := seenFeatures[f.ID]; dup {
				continue
			}
			manifest.Features = append(manifest.Features, SpecManifestEntry{
				ID:      f.ID,
				Title:   f.Title,
				Summary: summaryOrFallback(f.Summary, f.Description),
			})
		}
		for _, st := range existing.Strategies {
			if _, dup := seenStrategies[st.ID]; dup {
				continue
			}
			manifest.Strategies = append(manifest.Strategies, SpecManifestEntry{
				ID:      st.ID,
				Title:   st.Title,
				Kind:    string(st.Kind),
				Summary: summaryOrFallback(st.Summary, ""),
			})
		}
		for _, d := range existing.Decisions {
			if _, dup := seenDecisions[d.ID]; dup {
				continue
			}
			manifest.Decisions = append(manifest.Decisions, SpecManifestEntry{
				ID:      d.ID,
				Title:   d.Title,
				Summary: summaryOrFallback(d.Summary, d.Rationale),
			})
		}
		for _, a := range existing.Approaches {
			manifest.Approaches = append(manifest.Approaches, SpecManifestEntry{
				ID:      a.ID,
				Title:   a.Title,
				Summary: strings.TrimSpace(a.Summary),
			})
		}
	}

	return manifest, nil
}

// GetSpec looks the id up in the in-flight proposal first, then the
// loaded existing snapshot. Returns a not-found error pointing the
// model at spec_list_manifest when neither carries the id; mirrors
// LookupSpecNode's recovery shape so prompts that worked with the
// on-disk tool still work with the in-flight one.
func (s *InFlightSpecStore) GetSpec(id string) (json.RawMessage, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("spec_get: empty id")
	}
	if !validSpecID.MatchString(id) {
		return nil, fmt.Errorf("spec_get: id %q is malformed (expected kebab-case with prefix feat-, strat-, dec-, bug-, or app-)", id)
	}

	raw, existing := s.snapshot()

	if strings.TrimSpace(raw) != "" {
		var prop RawSpecProposal
		if err := json.Unmarshal([]byte(raw), &prop); err == nil {
			if data, ok := lookupInFlightByID(prop, id); ok {
				return data, nil
			}
		}
	}

	if existing != nil {
		if data, ok := lookupExistingByID(existing, id); ok {
			return data, nil
		}
	}

	return nil, fmt.Errorf("spec_get: no node with id %q in the in-flight proposal or the loaded existing snapshot (call spec_list_manifest to see available ids)", id)
}

// lookupInFlightByID returns the JSON payload for one raw-proposal
// node by id. The shape it returns mirrors what reading the on-disk
// .json file would return (an object with id/title/etc. fields). For
// features/strategies/decisions we marshal the typed Raw shape
// directly; bugs and approaches aren't part of the council's in-flight
// proposal yet.
func lookupInFlightByID(prop RawSpecProposal, id string) (json.RawMessage, bool) {
	switch {
	case strings.HasPrefix(id, "feat-"):
		for _, f := range prop.Features {
			if f.ID == id {
				data, err := json.Marshal(f)
				if err != nil {
					return nil, false
				}
				return data, true
			}
		}
	case strings.HasPrefix(id, "strat-"):
		for _, s := range prop.Strategies {
			if s.ID == id {
				data, err := json.Marshal(s)
				if err != nil {
					return nil, false
				}
				return data, true
			}
		}
	case strings.HasPrefix(id, "dec-"):
		for _, d := range prop.Decisions {
			if d.ID == id {
				data, err := json.Marshal(d)
				if err != nil {
					return nil, false
				}
				return data, true
			}
		}
	}
	return nil, false
}

// lookupExistingByID returns the JSON payload for one persisted node
// by id from the in-memory ExistingSpec snapshot.
func lookupExistingByID(existing *ExistingSpec, id string) (json.RawMessage, bool) {
	switch {
	case strings.HasPrefix(id, "feat-"):
		for _, f := range existing.Features {
			if f.ID == id {
				return marshalForLookup(f)
			}
		}
	case strings.HasPrefix(id, "strat-"):
		for _, s := range existing.Strategies {
			if s.ID == id {
				return marshalForLookup(s)
			}
		}
	case strings.HasPrefix(id, "dec-"):
		for _, d := range existing.Decisions {
			if d.ID == id {
				return marshalForLookup(d)
			}
		}
	case strings.HasPrefix(id, "app-"):
		for _, a := range existing.Approaches {
			if a.ID == id {
				return marshalForLookup(a)
			}
		}
	}
	return nil, false
}

func marshalForLookup(v any) (json.RawMessage, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Compile-time guard: InFlightSpecStore satisfies both provider
// interfaces, so a single store backs both swappables during a council
// run. Helpful for keeping the wiring symmetrical with SwappableSpecSearch.
var (
	_ SpecManifestProvider = (*InFlightSpecStore)(nil)
	_ SpecGetProvider      = (*InFlightSpecStore)(nil)
)

// SwappableSpecListManifest is a SpecManifestProvider whose underlying
// delegate can be replaced at runtime. Mirrors SwappableSpecSearch:
// production wires an fsSpecProvider at registration time; the council
// pushes the InFlightSpecStore in via Swap for the duration of a run.
type SwappableSpecListManifest struct {
	mu      sync.RWMutex
	current SpecManifestProvider
}

func NewSwappableSpecListManifest(initial SpecManifestProvider) *SwappableSpecListManifest {
	return &SwappableSpecListManifest{current: initial}
}

func (s *SwappableSpecListManifest) ListManifest() (SpecManifest, error) {
	s.mu.RLock()
	cur := s.current
	s.mu.RUnlock()
	if cur == nil {
		return SpecManifest{}, fmt.Errorf("spec_list_manifest: no backend wired")
	}
	return cur.ListManifest()
}

// Swap replaces the current delegate and returns the previous one for
// defer-restore at council end.
func (s *SwappableSpecListManifest) Swap(next SpecManifestProvider) SpecManifestProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.current
	s.current = next
	return prev
}

// Current returns the live delegate. Test-only inspection helper.
func (s *SwappableSpecListManifest) Current() SpecManifestProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// SwappableSpecGet is the spec_get counterpart to
// SwappableSpecListManifest. Same lifecycle: production registers an
// fsSpecProvider; the council swaps in the in-flight store.
type SwappableSpecGet struct {
	mu      sync.RWMutex
	current SpecGetProvider
}

func NewSwappableSpecGet(initial SpecGetProvider) *SwappableSpecGet {
	return &SwappableSpecGet{current: initial}
}

func (s *SwappableSpecGet) GetSpec(id string) (json.RawMessage, error) {
	s.mu.RLock()
	cur := s.current
	s.mu.RUnlock()
	if cur == nil {
		return nil, fmt.Errorf("spec_get: no backend wired")
	}
	return cur.GetSpec(id)
}

func (s *SwappableSpecGet) Swap(next SpecGetProvider) SpecGetProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.current
	s.current = next
	return prev
}

func (s *SwappableSpecGet) Current() SpecGetProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// NewFSSpecProvider returns the default on-disk provider that satisfies
// both SpecManifestProvider and SpecGetProvider — convenience for
// production wiring and tests that don't go through the swappables.
func NewFSSpecProvider(fsys specio.FS) SpecManifestAndGetProvider {
	return &fsSpecProvider{fsys: fsys}
}

// SpecManifestAndGetProvider is the union interface fsSpecProvider
// satisfies — useful in tests that want one handle for both surfaces.
type SpecManifestAndGetProvider interface {
	SpecManifestProvider
	SpecGetProvider
}

// Compile-time guard that the on-disk provider satisfies both
// surfaces; one struct, two roles.
var _ SpecManifestAndGetProvider = (*fsSpecProvider)(nil)

// Compile-time hint: ensure spec package's Approach type is still
// available without escaping its import here — keeps the file's
// imports honest about what it relies on.
var _ = spec.Approach{}
