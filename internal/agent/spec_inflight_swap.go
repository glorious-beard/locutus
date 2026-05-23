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
	"errors"
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
	// workingSignals derive SpecManifestEntry.Working for tool callers
	// reading the in-flight manifest. Populated by the workflow's
	// merge helpers via SetWorkingSignals — captures the live
	// AxesOpen / NewNodesFromScout / open-concern related-decision-IDs
	// so the manifest renderer can flag entries the council is
	// actively rewriting in this iteration. Empty / nil signals → no
	// entry is flagged as Working (the field omits from JSON via
	// omitempty), preserving the pre-DJ-125 behaviour.
	workingSignals workingSignals
}

// workingSignals carries the minimal live state the in-flight
// manifest needs to derive SpecManifestEntry.Working without coupling
// to the full PlanningState. Updated by the workflow's merge
// closures after every state mutation that changes which nodes are
// candidates for revision this iteration.
//
// Decisions are working when:
//   - any axis in the decision's axes[] is in OpenAxisIDs (the scout
//     reopened the axis; an elaborator dispatch will revise the
//     decision), OR
//   - any open concern's related_decision_ids names this decision id
//     (a revise-decisions dispatch will fire on it this iteration).
//
// Features and strategies are working when:
//   - the node id is in NewNodeIDs (newly introduced by the scout
//     this iter; narrative dispatch hasn't landed yet), OR
//   - any open concern's text mentions this node id (a re-narrative
//     pass will fire on it through the affected-nodes computation).
type workingSignals struct {
	OpenAxisIDs            map[string]struct{}
	NewNodeIDs             map[string]struct{}
	OpenConcernDecisionIDs map[string]struct{}
	OpenConcernNodeMatches map[string]struct{}
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

// SetWorkingSignals updates the live working-signal capture used by
// ListManifest to derive SpecManifestEntry.Working. Called by the
// workflow's merge closures after any state mutation that changes
// the set of nodes the council is mid-rewriting. Passing zero-value
// signals (all maps nil) clears the working flag for every entry —
// useful at council teardown to leave the store in a clean state.
func (s *InFlightSpecStore) SetWorkingSignals(sig workingSignals) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.workingSignals = sig
	s.mu.Unlock()
}

// snapshot grabs the current rawProposal + existing + working
// signals under the read lock so the per-tool builders can release
// the lock before doing the (potentially slower) parse.
func (s *InFlightSpecStore) snapshot() (string, *ExistingSpec, workingSignals) {
	if s == nil {
		return "", nil, workingSignals{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rawProposal, s.existing, s.workingSignals
}

// ListManifest builds an on-disk-shape SpecManifest from the in-flight
// proposal plus the loaded existing snapshot. In-flight entries win
// when an ID collides with an existing entry (the council is rewriting
// that node). The shape matches BuildSpecManifest(fsys) so the tool
// surface stays uniform.
func (s *InFlightSpecStore) ListManifest() (SpecManifest, error) {
	raw, existing, sig := s.snapshot()
	manifest := SpecManifest{}

	var prop RawSpecProposal
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &prop); err != nil {
			return SpecManifest{}, fmt.Errorf("spec_list_manifest: parse in-flight proposal: %w", err)
		}
	}

	// In-flight entries are OriginProposed and inherit a per-id
	// Working flag computed from the live workingSignals.
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
			Origin:  OriginProposed,
			Working: sig.isNodeWorking(f.ID),
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
			Origin:  OriginProposed,
			Working: sig.isNodeWorking(st.ID),
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
			Origin:  OriginProposed,
			Working: sig.isDecisionWorking(d.ID, d.Axes),
		})
	}

	// On-disk entries that didn't dedupe against the in-flight set
	// land as OriginSettled. Working stays false unless an open
	// concern targets the id — a revise-decisions dispatch may still
	// pick up a settled-on-disk decision when its critic finding fires
	// against it.
	if existing != nil {
		for _, f := range existing.Features {
			if _, dup := seenFeatures[f.ID]; dup {
				continue
			}
			manifest.Features = append(manifest.Features, SpecManifestEntry{
				ID:      f.ID,
				Title:   f.Title,
				Summary: summaryOrFallback(f.Summary, f.Description),
				Origin:  OriginSettled,
				Working: sig.isNodeWorking(f.ID),
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
				Origin:  OriginSettled,
				Working: sig.isNodeWorking(st.ID),
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
				Origin:  OriginSettled,
				Working: sig.isDecisionWorking(d.ID, d.Axes),
			})
		}
		for _, a := range existing.Approaches {
			manifest.Approaches = append(manifest.Approaches, SpecManifestEntry{
				ID:      a.ID,
				Title:   a.Title,
				Summary: strings.TrimSpace(a.Summary),
				Origin:  OriginSettled,
			})
		}
	}

	return manifest, nil
}

// isDecisionWorking reports whether a decision is being rewritten in
// the current iteration. True when any of the decision's axes is in
// the scout's open-axis set (an elaborator dispatch will revise the
// id-preserved decision on that axis) OR any open critic concern's
// related_decision_ids names this id (a revise-decisions dispatch
// will fire on it).
func (w workingSignals) isDecisionWorking(id string, axes []string) bool {
	if len(w.OpenAxisIDs) > 0 {
		for _, a := range axes {
			if _, hit := w.OpenAxisIDs[a]; hit {
				return true
			}
		}
	}
	if len(w.OpenConcernDecisionIDs) > 0 {
		if _, hit := w.OpenConcernDecisionIDs[id]; hit {
			return true
		}
	}
	return false
}

// isNodeWorking reports whether a feature / strategy / approach is
// being rewritten in the current iteration. True when the id is in
// NewNodeIDs (newly introduced by the scout this iter; narrative
// dispatch hasn't landed yet) OR any open critic concern's text
// mentions this id (a re-narrative pass on the affected-nodes set
// will fire on it).
func (w workingSignals) isNodeWorking(id string) bool {
	if len(w.NewNodeIDs) > 0 {
		if _, hit := w.NewNodeIDs[id]; hit {
			return true
		}
	}
	if len(w.OpenConcernNodeMatches) > 0 {
		if _, hit := w.OpenConcernNodeMatches[id]; hit {
			return true
		}
	}
	return false
}

// GetSpec looks the id up in the in-flight proposal first, then the
// loaded existing snapshot.
//
// Not-found recovery: rather than redirect the model to a separate
// spec_list_manifest call, the error message inlines the kind-matched
// manifest (every id of the requested prefix, with its title) so the
// model has the candidate set in one round. Inlining was a deliberate
// response to a Gemini 3.5 Flash tool-loop pathology where the model
// would confabulate plausibly-named ids and spin the tool-use loop
// chasing them; surfacing the real id space in the error message
// breaks the spiral by giving the model a bounded, authoritative
// pick list instead of an open-ended invitation to guess.
//
// The kind is inferred from the id prefix (feat-, strat-, dec-,
// bug-, app-). Malformed or empty ids produce an error with no
// manifest inlined since the kind is undetermined.
func (s *InFlightSpecStore) GetSpec(id string) (json.RawMessage, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("spec_get: empty id")
	}
	if !validSpecID.MatchString(id) {
		return nil, fmt.Errorf("spec_get: id %q is malformed (expected kebab-case with prefix feat-, strat-, dec-, bug-, or app-)", id)
	}

	raw, existing, _ := s.snapshot()

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

	// Not found: build the not-found error with the kind-matched
	// manifest inlined so the model can recover in one round.
	avail := s.availableIDsForKind(id)
	if len(avail) == 0 {
		return nil, fmt.Errorf("spec_get: no node with id %q (no nodes of this kind exist in the in-flight proposal or the loaded existing snapshot)", id)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "spec_get: no node with id %q. Available ids of this kind (%d):", id, len(avail))
	for _, e := range avail {
		b.WriteString("\n  - ")
		b.WriteString(e.ID)
		if t := strings.TrimSpace(e.Title); t != "" {
			b.WriteString(" — ")
			b.WriteString(t)
		}
	}
	b.WriteString("\nPick one of the ids above; do not guess at variant slugs.")
	return nil, errors.New(b.String())
}

// availableIDsForKind returns every entry in the in-flight + existing
// graph whose id shares the requested id's prefix. Used by GetSpec's
// not-found path to inline the candidate set in the error message.
// The returned slice carries ID + Title only — Summary / Origin /
// Working are deliberately omitted to keep the error message
// scannable; the model can call spec_list_manifest if it needs the
// richer view.
func (s *InFlightSpecStore) availableIDsForKind(requestedID string) []SpecManifestEntry {
	manifest, err := s.ListManifest()
	if err != nil {
		return nil
	}
	switch {
	case strings.HasPrefix(requestedID, "feat-"):
		return stripToIDTitle(manifest.Features)
	case strings.HasPrefix(requestedID, "strat-"):
		return stripToIDTitle(manifest.Strategies)
	case strings.HasPrefix(requestedID, "dec-"):
		return stripToIDTitle(manifest.Decisions)
	case strings.HasPrefix(requestedID, "bug-"):
		return stripToIDTitle(manifest.Bugs)
	case strings.HasPrefix(requestedID, "app-"):
		return stripToIDTitle(manifest.Approaches)
	}
	return nil
}

// stripToIDTitle returns a copy of the entries carrying only ID and
// Title — used by GetSpec's not-found error message to keep the
// inlined pick list compact.
func stripToIDTitle(in []SpecManifestEntry) []SpecManifestEntry {
	out := make([]SpecManifestEntry, len(in))
	for i, e := range in {
		out[i] = SpecManifestEntry{ID: e.ID, Title: e.Title}
	}
	return out
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
