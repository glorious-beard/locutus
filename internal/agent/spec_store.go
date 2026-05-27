package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/glorious-beard/locutus/internal/search"
	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
)

// SpecKind discriminates entries by node kind. Mirrors the id-prefix
// routing (`feat-` → KindFeature, `dec-` → KindDecision, etc.) and is
// the key type used in SpecGetResult.AvailableIDs.
type SpecKind string

const (
	KindFeature  SpecKind = "feature"
	KindStrategy SpecKind = "strategy"
	KindDecision SpecKind = "decision"
	KindBug      SpecKind = "bug"
	KindApproach SpecKind = "approach"
	// KindGoal and KindAntiGoal route the goal-* and agoal-* id
	// prefixes to their typed maps (DJ-139). Goals and AntiGoals are
	// the persisted LLM interpretation of GOALS.md — leaves in the
	// cascade sense; nothing structurally depends on them, citations
	// from other kinds are informational dotted lines.
	KindGoal     SpecKind = "goal"
	KindAntiGoal SpecKind = "antigoal"
)

// SpecGetStatus discriminates the disposition of a SpecGetEntry.
// Settled bodies live on disk and are stable; in-flight bodies live
// in the council's in-memory proposed pool and may be rewritten;
// missing entries name an id the store couldn't resolve.
type SpecGetStatus string

const (
	SpecGetSettled  SpecGetStatus = "settled"
	SpecGetInFlight SpecGetStatus = "in_flight"
	SpecGetMissing  SpecGetStatus = "missing"
)

// SpecGetResult is the batched response shape for SpecStore.GetSpec.
// Every requested id appears in Results exactly once with a status
// discriminating found-and-stable vs found-but-in-flight vs missing.
// AvailableIDs surfaces the per-kind catalogue once per result for
// kinds that had at least one miss so the model can recover without
// the result repeating the same catalogue per missing id.
type SpecGetResult struct {
	Results      map[string]SpecGetEntry `json:"results"`
	AvailableIDs map[SpecKind][]string   `json:"available_ids,omitempty"`
	Working      []string                `json:"working,omitempty"`
}

// SpecGetEntry is the per-id result. Status discriminates whether
// Body or Reason is populated; Working flags entries the council is
// rewriting this iteration so the model knows the body may shift on
// the next pass.
type SpecGetEntry struct {
	Status  SpecGetStatus `json:"status"`
	Body    any           `json:"body,omitempty"`
	Working bool          `json:"working,omitempty"`
	Reason  string        `json:"reason,omitempty"`
}

// SpecStore is the single in-process source of truth for spec graph
// reads and writes during a session (DJ-134). It holds typed nodes
// in memory tagged by origin (settled / proposed) and working flag,
// serves every consumer (RAG tools, CLI verbs, MCP) through one
// mutex-protected surface, and persists proposed deltas to .borg/spec/
// at Commit time via a single FS write batch.
//
// Lifecycle: NewSpecStore loads all settled nodes from disk; reads
// run any time; writes (Put, MarkWorking, ClearWorking) accumulate
// in memory; Begin opens a council transaction; Commit promotes
// proposed→settled and persists; Rollback discards the proposed delta
// and restores pre-Begin state.
//
// Concurrency: one writer at a time (the council goroutine + future
// tool-call mutations serialize through the same Lock); many readers
// (tool dispatches) take RLock. The mutex covers both the typed maps
// AND the search index so a reader either sees pre-Put or post-Put
// state, never half. Empirically the existing per-swappable RWMutexes
// in the pre-DJ-134 stack carry no observed contention at council
// throughput; the unified store inherits the profile.
type SpecStore struct {
	mu   sync.RWMutex
	fsys specio.FS

	goals      map[string]*goalEntry
	antiGoals  map[string]*antiGoalEntry
	features   map[string]*featureEntry
	strategies map[string]*strategyEntry
	decisions  map[string]*decisionEntry
	bugs       map[string]*bugEntry
	approaches map[string]*approachEntry

	index *search.InFlightIndex

	// tx, when non-nil, holds the pre-Begin snapshot for Rollback to
	// restore. Begin captures it; Commit clears it; Rollback restores
	// from it.
	tx *txSnapshot
}

type goalEntry struct {
	body    spec.Goal
	origin  SpecManifestOrigin
	working bool
}
type antiGoalEntry struct {
	body    spec.AntiGoal
	origin  SpecManifestOrigin
	working bool
}
type featureEntry struct {
	body    spec.Feature
	origin  SpecManifestOrigin
	working bool
}
type strategyEntry struct {
	body    spec.Strategy
	origin  SpecManifestOrigin
	working bool
}
type decisionEntry struct {
	body    spec.Decision
	origin  SpecManifestOrigin
	working bool
}
type bugEntry struct {
	body    spec.Bug
	origin  SpecManifestOrigin
	working bool
}
type approachEntry struct {
	body    spec.Approach
	origin  SpecManifestOrigin
	working bool
}

// txSnapshot captures enough state for Rollback to restore the store
// to its pre-Begin condition. Shallow copies of the maps suffice since
// entry pointers are replaced (not mutated) by Put.
type txSnapshot struct {
	goals      map[string]*goalEntry
	antiGoals  map[string]*antiGoalEntry
	features   map[string]*featureEntry
	strategies map[string]*strategyEntry
	decisions  map[string]*decisionEntry
	bugs       map[string]*bugEntry
	approaches map[string]*approachEntry
}

// NewSpecStore constructs a store seeded from the on-disk graph under
// `.borg/spec/`. Missing directories (greenfield) are not an error —
// the store starts empty. All loaded nodes are tagged OriginSettled.
func NewSpecStore(fsys specio.FS) (*SpecStore, error) {
	if fsys == nil {
		return nil, fmt.Errorf("NewSpecStore: fsys is required")
	}
	idx, err := search.NewInFlightIndex()
	if err != nil {
		return nil, fmt.Errorf("NewSpecStore: open search index: %w", err)
	}
	s := &SpecStore{
		fsys:       fsys,
		goals:      make(map[string]*goalEntry),
		antiGoals:  make(map[string]*antiGoalEntry),
		features:   make(map[string]*featureEntry),
		strategies: make(map[string]*strategyEntry),
		decisions:  make(map[string]*decisionEntry),
		bugs:       make(map[string]*bugEntry),
		approaches: make(map[string]*approachEntry),
		index:      idx,
	}
	if err := s.loadFromFS(); err != nil {
		return nil, err
	}
	if err := s.rebuildIndex(); err != nil {
		return nil, err
	}
	return s, nil
}

// loadFromFS walks `.borg/spec/{goals,antigoals,features,strategies,decisions,bugs}`
// for JSON nodes and `.borg/spec/approaches` for markdown approaches. Each
// loaded node lands in the corresponding map with OriginSettled. Missing
// directories are not an error.
func (s *SpecStore) loadFromFS() error {
	if pairs, err := specio.WalkPairs[spec.Goal](s.fsys, ".borg/spec/goals"); err == nil {
		for _, p := range pairs {
			if p.Err == nil && p.Object.ID != "" {
				s.goals[p.Object.ID] = &goalEntry{body: p.Object, origin: OriginSettled}
			}
		}
	}
	if pairs, err := specio.WalkPairs[spec.AntiGoal](s.fsys, ".borg/spec/antigoals"); err == nil {
		for _, p := range pairs {
			if p.Err == nil && p.Object.ID != "" {
				s.antiGoals[p.Object.ID] = &antiGoalEntry{body: p.Object, origin: OriginSettled}
			}
		}
	}
	if pairs, err := specio.WalkPairs[spec.Feature](s.fsys, ".borg/spec/features"); err == nil {
		for _, p := range pairs {
			if p.Err == nil && p.Object.ID != "" {
				s.features[p.Object.ID] = &featureEntry{body: p.Object, origin: OriginSettled}
			}
		}
	}
	if pairs, err := specio.WalkPairs[spec.Strategy](s.fsys, ".borg/spec/strategies"); err == nil {
		for _, p := range pairs {
			if p.Err == nil && p.Object.ID != "" {
				s.strategies[p.Object.ID] = &strategyEntry{body: p.Object, origin: OriginSettled}
			}
		}
	}
	if pairs, err := specio.WalkPairs[spec.Decision](s.fsys, ".borg/spec/decisions"); err == nil {
		for _, p := range pairs {
			if p.Err == nil && p.Object.ID != "" {
				s.decisions[p.Object.ID] = &decisionEntry{body: p.Object, origin: OriginSettled}
			}
		}
	}
	if pairs, err := specio.WalkPairs[spec.Bug](s.fsys, ".borg/spec/bugs"); err == nil {
		for _, p := range pairs {
			if p.Err == nil && p.Object.ID != "" {
				s.bugs[p.Object.ID] = &bugEntry{body: p.Object, origin: OriginSettled}
			}
		}
	}
	if paths, err := s.fsys.ListDir(".borg/spec/approaches"); err == nil {
		for _, p := range paths {
			if len(p) < 3 || p[len(p)-3:] != ".md" {
				continue
			}
			obj, _, err := specio.LoadMarkdown[spec.Approach](s.fsys, p)
			if err == nil && obj.ID != "" {
				s.approaches[obj.ID] = &approachEntry{body: obj, origin: OriginSettled}
			}
		}
	}
	return nil
}

// ListManifest returns a SpecManifest view of every entry in the
// store. Held under RLock briefly to copy out the manifest entries
// before returning so the caller never sees a mid-write state.
func (s *SpecStore) ListManifest() SpecManifest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m := SpecManifest{}
	for _, e := range s.goals {
		m.Goals = append(m.Goals, SpecManifestEntry{
			ID:      e.body.ID,
			Title:   e.body.Title,
			Summary: summaryOrFallback("", e.body.Body),
			Origin:  e.origin,
			Working: e.working,
		})
	}
	for _, e := range s.antiGoals {
		m.AntiGoals = append(m.AntiGoals, SpecManifestEntry{
			ID:      e.body.ID,
			Title:   e.body.Title,
			Summary: summaryOrFallback("", e.body.Body),
			Origin:  e.origin,
			Working: e.working,
		})
	}
	for _, e := range s.features {
		m.Features = append(m.Features, SpecManifestEntry{
			ID:      e.body.ID,
			Title:   e.body.Title,
			Summary: summaryOrFallback(e.body.Summary, e.body.Description),
			Origin:  e.origin,
			Working: e.working,
		})
	}
	for _, e := range s.strategies {
		m.Strategies = append(m.Strategies, SpecManifestEntry{
			ID:      e.body.ID,
			Title:   e.body.Title,
			Kind:    string(e.body.Kind),
			Summary: summaryOrFallback(e.body.Summary, ""),
			Origin:  e.origin,
			Working: e.working,
		})
	}
	for _, e := range s.decisions {
		m.Decisions = append(m.Decisions, SpecManifestEntry{
			ID:      e.body.ID,
			Title:   e.body.Title,
			Summary: summaryOrFallback(e.body.Summary, e.body.Rationale),
			Origin:  e.origin,
			Working: e.working,
		})
	}
	for _, e := range s.bugs {
		m.Bugs = append(m.Bugs, SpecManifestEntry{
			ID:      e.body.ID,
			Title:   e.body.Title,
			Summary: summaryOrFallback(e.body.Summary, e.body.Description),
			Origin:  e.origin,
			Working: e.working,
		})
	}
	for _, e := range s.approaches {
		m.Approaches = append(m.Approaches, SpecManifestEntry{
			ID:      e.body.ID,
			Title:   e.body.Title,
			Summary: strings.TrimSpace(e.body.Summary),
			Origin:  e.origin,
			Working: e.working,
		})
	}
	return m
}

// GetSpec is the batched lookup. Every requested id appears in
// Results exactly once with a status discriminating found-and-stable
// (settled), found-but-in-flight (in_flight), or missing. For misses,
// AvailableIDs carries the per-kind id catalogue once per kind so a
// batch of N missing ids of the same kind doesn't duplicate the
// catalogue N times.
//
// Top-level error is reserved for unrecoverable store failures
// (none today — kept on the signature for forward compatibility
// with future write-through provider errors); per-id misses never
// bubble up.
func (s *SpecStore) GetSpec(ids []string) SpecGetResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := SpecGetResult{Results: make(map[string]SpecGetEntry, len(ids))}
	missedKinds := make(map[SpecKind]struct{})

	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if !validSpecID.MatchString(id) {
			result.Results[id] = SpecGetEntry{
				Status: SpecGetMissing,
				Reason: fmt.Sprintf("malformed id %q: expected kebab-case with prefix goal-, agoal-, feat-, strat-, dec-, bug-, or app-", id),
			}
			continue
		}
		entry, kind, found, working := s.lookupLocked(id)
		if !found {
			result.Results[id] = SpecGetEntry{
				Status: SpecGetMissing,
				Reason: fmt.Sprintf("no node with id %q", id),
			}
			missedKinds[kind] = struct{}{}
			continue
		}
		result.Results[id] = entry
		if working {
			result.Working = append(result.Working, id)
		}
	}

	if len(missedKinds) > 0 {
		result.AvailableIDs = make(map[SpecKind][]string, len(missedKinds))
		for kind := range missedKinds {
			result.AvailableIDs[kind] = s.idsForKindLocked(kind)
		}
	}
	return result
}

// lookupLocked resolves a well-formed id against the typed maps and
// returns the matching SpecGetEntry, the kind the id maps to, whether
// it was found, and whether the entry's working flag is set. The
// caller holds the store's RLock (or Lock) for the duration of the
// lookup. Status discriminates settled (on-disk-loaded) vs in_flight
// (council-proposed).
func (s *SpecStore) lookupLocked(id string) (entry SpecGetEntry, kind SpecKind, found bool, working bool) {
	switch {
	case strings.HasPrefix(id, "goal-"):
		kind = KindGoal
		if e, ok := s.goals[id]; ok {
			return SpecGetEntry{Status: statusFor(e.origin), Body: e.body, Working: e.working}, kind, true, e.working
		}
	case strings.HasPrefix(id, "agoal-"):
		kind = KindAntiGoal
		if e, ok := s.antiGoals[id]; ok {
			return SpecGetEntry{Status: statusFor(e.origin), Body: e.body, Working: e.working}, kind, true, e.working
		}
	case strings.HasPrefix(id, "feat-"):
		kind = KindFeature
		if e, ok := s.features[id]; ok {
			return SpecGetEntry{Status: statusFor(e.origin), Body: e.body, Working: e.working}, kind, true, e.working
		}
	case strings.HasPrefix(id, "strat-"):
		kind = KindStrategy
		if e, ok := s.strategies[id]; ok {
			return SpecGetEntry{Status: statusFor(e.origin), Body: e.body, Working: e.working}, kind, true, e.working
		}
	case strings.HasPrefix(id, "dec-"):
		kind = KindDecision
		if e, ok := s.decisions[id]; ok {
			return SpecGetEntry{Status: statusFor(e.origin), Body: e.body, Working: e.working}, kind, true, e.working
		}
	case strings.HasPrefix(id, "bug-"):
		kind = KindBug
		if e, ok := s.bugs[id]; ok {
			return SpecGetEntry{Status: statusFor(e.origin), Body: e.body, Working: e.working}, kind, true, e.working
		}
	case strings.HasPrefix(id, "app-"):
		kind = KindApproach
		if e, ok := s.approaches[id]; ok {
			return SpecGetEntry{Status: statusFor(e.origin), Body: e.body, Working: e.working}, kind, true, e.working
		}
	}
	return SpecGetEntry{}, kind, false, false
}

// statusFor maps a SpecManifestOrigin to its SpecGetStatus equivalent.
// Settled origin → settled status; proposed → in_flight.
func statusFor(o SpecManifestOrigin) SpecGetStatus {
	if o == OriginProposed {
		return SpecGetInFlight
	}
	return SpecGetSettled
}

// idsForKindLocked returns every id of the given kind in lexical
// order. Caller holds the store's RLock or Lock.
func (s *SpecStore) idsForKindLocked(kind SpecKind) []string {
	var ids []string
	switch kind {
	case KindGoal:
		for id := range s.goals {
			ids = append(ids, id)
		}
	case KindAntiGoal:
		for id := range s.antiGoals {
			ids = append(ids, id)
		}
	case KindFeature:
		for id := range s.features {
			ids = append(ids, id)
		}
	case KindStrategy:
		for id := range s.strategies {
			ids = append(ids, id)
		}
	case KindDecision:
		for id := range s.decisions {
			ids = append(ids, id)
		}
	case KindBug:
		for id := range s.bugs {
			ids = append(ids, id)
		}
	case KindApproach:
		for id := range s.approaches {
			ids = append(ids, id)
		}
	}
	sortStrings(ids)
	return ids
}

// sortStrings sorts in place — used for the deterministic
// AvailableIDs ordering the tests assert against.
func sortStrings(s []string) {
	// Tiny insertion sort: id slices are <100 entries, the import
	// trade-off vs sort.Strings isn't worth pulling sort in for one
	// helper.
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j-1] > s[j] {
			s[j-1], s[j] = s[j], s[j-1]
			j--
		}
	}
}

// Put inserts or replaces an entry. The kind argument is explicit
// (rather than reflected from body) because the merge helpers know
// the kind they're producing — no need for runtime type dispatch.
// The id and body are validated against the kind's id-prefix.
//
// Origin tags the entry; the search index is rebuilt synchronously
// inside the Lock so a concurrent reader either sees the pre-Put or
// post-Put state for both ListManifest/GetSpec AND Search.
func (s *SpecStore) Put(kind SpecKind, id string, body any, origin SpecManifestOrigin) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("SpecStore.Put: empty id")
	}
	if !validSpecID.MatchString(id) {
		return fmt.Errorf("SpecStore.Put: id %q is malformed", id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch kind {
	case KindGoal:
		g, ok := body.(spec.Goal)
		if !ok {
			return fmt.Errorf("SpecStore.Put: body is %T, expected spec.Goal", body)
		}
		if !strings.HasPrefix(id, "goal-") {
			return fmt.Errorf("SpecStore.Put: id %q lacks goal- prefix for KindGoal", id)
		}
		s.goals[id] = &goalEntry{body: g, origin: origin, working: s.workingPriorLocked(KindGoal, id)}
	case KindAntiGoal:
		ag, ok := body.(spec.AntiGoal)
		if !ok {
			return fmt.Errorf("SpecStore.Put: body is %T, expected spec.AntiGoal", body)
		}
		if !strings.HasPrefix(id, "agoal-") {
			return fmt.Errorf("SpecStore.Put: id %q lacks agoal- prefix for KindAntiGoal", id)
		}
		s.antiGoals[id] = &antiGoalEntry{body: ag, origin: origin, working: s.workingPriorLocked(KindAntiGoal, id)}
	case KindFeature:
		f, ok := body.(spec.Feature)
		if !ok {
			return fmt.Errorf("SpecStore.Put: body is %T, expected spec.Feature", body)
		}
		if !strings.HasPrefix(id, "feat-") {
			return fmt.Errorf("SpecStore.Put: id %q lacks feat- prefix for KindFeature", id)
		}
		s.features[id] = &featureEntry{body: f, origin: origin, working: s.workingPriorLocked(KindFeature, id)}
	case KindStrategy:
		st, ok := body.(spec.Strategy)
		if !ok {
			return fmt.Errorf("SpecStore.Put: body is %T, expected spec.Strategy", body)
		}
		if !strings.HasPrefix(id, "strat-") {
			return fmt.Errorf("SpecStore.Put: id %q lacks strat- prefix for KindStrategy", id)
		}
		s.strategies[id] = &strategyEntry{body: st, origin: origin, working: s.workingPriorLocked(KindStrategy, id)}
	case KindDecision:
		d, ok := body.(spec.Decision)
		if !ok {
			return fmt.Errorf("SpecStore.Put: body is %T, expected spec.Decision", body)
		}
		if !strings.HasPrefix(id, "dec-") {
			return fmt.Errorf("SpecStore.Put: id %q lacks dec- prefix for KindDecision", id)
		}
		s.decisions[id] = &decisionEntry{body: d, origin: origin, working: s.workingPriorLocked(KindDecision, id)}
	case KindBug:
		b, ok := body.(spec.Bug)
		if !ok {
			return fmt.Errorf("SpecStore.Put: body is %T, expected spec.Bug", body)
		}
		if !strings.HasPrefix(id, "bug-") {
			return fmt.Errorf("SpecStore.Put: id %q lacks bug- prefix for KindBug", id)
		}
		s.bugs[id] = &bugEntry{body: b, origin: origin, working: s.workingPriorLocked(KindBug, id)}
	case KindApproach:
		a, ok := body.(spec.Approach)
		if !ok {
			return fmt.Errorf("SpecStore.Put: body is %T, expected spec.Approach", body)
		}
		if !strings.HasPrefix(id, "app-") {
			return fmt.Errorf("SpecStore.Put: id %q lacks app- prefix for KindApproach", id)
		}
		s.approaches[id] = &approachEntry{body: a, origin: origin, working: s.workingPriorLocked(KindApproach, id)}
	default:
		return fmt.Errorf("SpecStore.Put: unknown kind %q", kind)
	}
	return s.rebuildIndex()
}

// DeleteGoal removes a goal-* node from the in-memory map AND from
// the on-disk JSON file under .borg/spec/goals/<id>.json. Pre-DJ-139
// the spec model was append-only; deletion arrives with the goal
// layer because GOALS.md edits can drop scope claims, and the
// persisted interpretation has to follow. Callers that need an audit
// trail of the deletion record a goal_deleted history event
// separately via history.RecordGoalDeleted — DeleteGoal itself stays
// concerned only with the store + filesystem state.
//
// Returns an error if the id doesn't have the goal- prefix, if no
// entry exists for the id, or if the on-disk file removal fails. On
// disk-removal failure the in-memory entry is restored so the store
// stays consistent with disk.
//
// DeleteGoal is self-contained (no Begin/Commit dance required) —
// the operation is a single atomic mutation of one map + one file.
func (s *SpecStore) DeleteGoal(id string) error {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "goal-") {
		return fmt.Errorf("SpecStore.DeleteGoal: id %q lacks goal- prefix", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.goals[id]
	if !ok {
		return fmt.Errorf("SpecStore.DeleteGoal: goal %q not in store", id)
	}
	delete(s.goals, id)
	path := ".borg/spec/goals/" + id + ".json"
	if err := s.fsys.Remove(path); err != nil && !isNotExistErr(err) {
		// Restore the in-memory entry so the store reflects what's on
		// disk. The caller sees the error and can retry or surface it.
		s.goals[id] = entry
		return fmt.Errorf("SpecStore.DeleteGoal: remove %s: %w", path, err)
	}
	return s.rebuildIndex()
}

// DeleteAntiGoal mirrors DeleteGoal for the agoal- prefix and the
// .borg/spec/antigoals/ disk path. Same semantics: in-memory + on-
// disk removal, error on unknown id, restore-on-disk-failure.
func (s *SpecStore) DeleteAntiGoal(id string) error {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "agoal-") {
		return fmt.Errorf("SpecStore.DeleteAntiGoal: id %q lacks agoal- prefix", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.antiGoals[id]
	if !ok {
		return fmt.Errorf("SpecStore.DeleteAntiGoal: antigoal %q not in store", id)
	}
	delete(s.antiGoals, id)
	path := ".borg/spec/antigoals/" + id + ".json"
	if err := s.fsys.Remove(path); err != nil && !isNotExistErr(err) {
		s.antiGoals[id] = entry
		return fmt.Errorf("SpecStore.DeleteAntiGoal: remove %s: %w", path, err)
	}
	return s.rebuildIndex()
}

// isNotExistErr returns true when err is the not-exist sentinel from
// the underlying FS implementation. DeleteGoal / DeleteAntiGoal
// tolerate a missing file (the in-memory entry was the source of
// truth; the file simply hadn't persisted yet or was hand-removed)
// so the operation is idempotent against a divergent on-disk state.
// Both MemFS and OSFS wrap fs.ErrNotExist via fs.PathError, so the
// stdlib errors.Is check catches both paths without coupling to the
// implementation.
func isNotExistErr(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// workingPriorLocked preserves the working flag across a Put against
// an existing id — the workflow may have marked an id as working
// before the elaborator's revised body lands. Without this preserve,
// the Put would silently clear the flag. Caller holds Lock.
func (s *SpecStore) workingPriorLocked(kind SpecKind, id string) bool {
	switch kind {
	case KindGoal:
		if e, ok := s.goals[id]; ok {
			return e.working
		}
	case KindAntiGoal:
		if e, ok := s.antiGoals[id]; ok {
			return e.working
		}
	case KindFeature:
		if e, ok := s.features[id]; ok {
			return e.working
		}
	case KindStrategy:
		if e, ok := s.strategies[id]; ok {
			return e.working
		}
	case KindDecision:
		if e, ok := s.decisions[id]; ok {
			return e.working
		}
	case KindBug:
		if e, ok := s.bugs[id]; ok {
			return e.working
		}
	case KindApproach:
		if e, ok := s.approaches[id]; ok {
			return e.working
		}
	}
	return false
}

// MarkWorking flips the working flag on the given ids. Unknown ids
// are silently ignored (the workflow may pre-mark ids that haven't
// been Put yet — the flag will apply when the body lands).
func (s *SpecStore) MarkWorking(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		switch {
		case strings.HasPrefix(id, "goal-"):
			if e, ok := s.goals[id]; ok {
				e.working = true
			}
		case strings.HasPrefix(id, "agoal-"):
			if e, ok := s.antiGoals[id]; ok {
				e.working = true
			}
		case strings.HasPrefix(id, "feat-"):
			if e, ok := s.features[id]; ok {
				e.working = true
			}
		case strings.HasPrefix(id, "strat-"):
			if e, ok := s.strategies[id]; ok {
				e.working = true
			}
		case strings.HasPrefix(id, "dec-"):
			if e, ok := s.decisions[id]; ok {
				e.working = true
			}
		case strings.HasPrefix(id, "bug-"):
			if e, ok := s.bugs[id]; ok {
				e.working = true
			}
		case strings.HasPrefix(id, "app-"):
			if e, ok := s.approaches[id]; ok {
				e.working = true
			}
		}
	}
}

// ClearWorking resets every entry's working flag to false. Called at
// iteration boundaries when the council reassesses which nodes are
// being rewritten this round.
func (s *SpecStore) ClearWorking() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.goals {
		e.working = false
	}
	for _, e := range s.antiGoals {
		e.working = false
	}
	for _, e := range s.features {
		e.working = false
	}
	for _, e := range s.strategies {
		e.working = false
	}
	for _, e := range s.decisions {
		e.working = false
	}
	for _, e := range s.bugs {
		e.working = false
	}
	for _, e := range s.approaches {
		e.working = false
	}
}

// Search runs a full-text query against the in-memory Bluge index.
// The index is rebuilt synchronously on every Put so the result
// reflects the current store state without a separate reindex call.
func (s *SpecStore) Search(query string, opts search.Options) ([]search.Hit, int, error) {
	s.mu.RLock()
	idx := s.index
	s.mu.RUnlock()
	if idx == nil {
		return nil, 0, fmt.Errorf("SpecStore.Search: index not initialized")
	}
	return idx.Search(query, opts)
}

// Begin opens a council transaction: captures a snapshot of the
// current maps so Rollback can restore the pre-Begin state. The
// snapshot is shallow (map copies, pointing at the same entry
// values) — Put replaces entries by pointer, so the snapshot's
// references stay byte-stable across in-transaction mutations.
//
// Begin returns an error if a transaction is already open; nesting
// is not supported (council runs aren't nested).
func (s *SpecStore) Begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx != nil {
		return fmt.Errorf("SpecStore.Begin: transaction already open")
	}
	s.tx = &txSnapshot{
		goals:      cloneGoalMap(s.goals),
		antiGoals:  cloneAntiGoalMap(s.antiGoals),
		features:   cloneFeatureMap(s.features),
		strategies: cloneStrategyMap(s.strategies),
		decisions:  cloneDecisionMap(s.decisions),
		bugs:       cloneBugMap(s.bugs),
		approaches: cloneApproachMap(s.approaches),
	}
	return nil
}

// Commit promotes every OriginProposed entry to OriginSettled and
// persists the proposed-side delta to `.borg/spec/`. Clears the
// working flag on every entry (the iteration's rewrites are
// complete) and closes the transaction.
func (s *SpecStore) Commit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx == nil {
		return fmt.Errorf("SpecStore.Commit: no transaction open")
	}
	if err := s.persistLocked(); err != nil {
		return fmt.Errorf("SpecStore.Commit: persist: %w", err)
	}
	s.promoteAndClearWorkingLocked()
	s.tx = nil
	return nil
}

// Rollback discards the in-memory delta accumulated since Begin and
// restores the pre-Begin state. The on-disk graph is untouched (the
// invariant: nothing reaches disk until Commit).
func (s *SpecStore) Rollback() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx == nil {
		return fmt.Errorf("SpecStore.Rollback: no transaction open")
	}
	s.goals = s.tx.goals
	s.antiGoals = s.tx.antiGoals
	s.features = s.tx.features
	s.strategies = s.tx.strategies
	s.decisions = s.tx.decisions
	s.bugs = s.tx.bugs
	s.approaches = s.tx.approaches
	s.tx = nil
	return s.rebuildIndex()
}

// persistLocked writes every OriginProposed entry to `.borg/spec/`.
// Caller holds Lock. Failures abort the persist; the store's
// in-memory state is left unchanged so a retry is meaningful.
func (s *SpecStore) persistLocked() error {
	for id, e := range s.goals {
		if e.origin != OriginProposed {
			continue
		}
		if err := writeJSONNode(s.fsys, ".borg/spec/goals/"+id+".json", e.body); err != nil {
			return err
		}
	}
	for id, e := range s.antiGoals {
		if e.origin != OriginProposed {
			continue
		}
		if err := writeJSONNode(s.fsys, ".borg/spec/antigoals/"+id+".json", e.body); err != nil {
			return err
		}
	}
	for id, e := range s.features {
		if e.origin != OriginProposed {
			continue
		}
		if err := writeJSONNode(s.fsys, ".borg/spec/features/"+id+".json", e.body); err != nil {
			return err
		}
	}
	for id, e := range s.strategies {
		if e.origin != OriginProposed {
			continue
		}
		if err := writeJSONNode(s.fsys, ".borg/spec/strategies/"+id+".json", e.body); err != nil {
			return err
		}
	}
	for id, e := range s.decisions {
		if e.origin != OriginProposed {
			continue
		}
		if err := writeJSONNode(s.fsys, ".borg/spec/decisions/"+id+".json", e.body); err != nil {
			return err
		}
	}
	for id, e := range s.bugs {
		if e.origin != OriginProposed {
			continue
		}
		if err := writeJSONNode(s.fsys, ".borg/spec/bugs/"+id+".json", e.body); err != nil {
			return err
		}
	}
	// Approaches persist as YAML frontmatter + markdown body via
	// specio.SaveMarkdown — the same path the cascade-rewrite and
	// fill-summaries workflows already use. spec.Approach's Body field
	// carries the brief content the coding agent reads.
	for id, e := range s.approaches {
		if e.origin != OriginProposed {
			continue
		}
		if err := s.fsys.MkdirAll(".borg/spec/approaches", 0o755); err != nil {
			return err
		}
		if err := specio.SaveMarkdown(s.fsys, ".borg/spec/approaches/"+id+".md", e.body, e.body.Body); err != nil {
			return err
		}
	}
	return nil
}

// promoteAndClearWorkingLocked flips every proposed entry to settled
// and clears the working flag everywhere. Called from Commit after a
// successful persist; the in-memory state then matches what was just
// written to disk.
func (s *SpecStore) promoteAndClearWorkingLocked() {
	for _, e := range s.goals {
		if e.origin == OriginProposed {
			e.origin = OriginSettled
		}
		e.working = false
	}
	for _, e := range s.antiGoals {
		if e.origin == OriginProposed {
			e.origin = OriginSettled
		}
		e.working = false
	}
	for _, e := range s.features {
		if e.origin == OriginProposed {
			e.origin = OriginSettled
		}
		e.working = false
	}
	for _, e := range s.strategies {
		if e.origin == OriginProposed {
			e.origin = OriginSettled
		}
		e.working = false
	}
	for _, e := range s.decisions {
		if e.origin == OriginProposed {
			e.origin = OriginSettled
		}
		e.working = false
	}
	for _, e := range s.bugs {
		if e.origin == OriginProposed {
			e.origin = OriginSettled
		}
		e.working = false
	}
	for _, e := range s.approaches {
		if e.origin == OriginProposed {
			e.origin = OriginSettled
		}
		e.working = false
	}
}

// rebuildIndex serializes the current feature+strategy+decision
// content into the InFlightIndex's RawProposal-shaped JSON and
// triggers a re-index. Bugs and approaches are not indexed (matches
// the pre-DJ-134 in-flight semantics; future work can extend the
// index to cover them). Caller holds Lock.
func (s *SpecStore) rebuildIndex() error {
	if s.index == nil {
		return nil
	}
	type inFlightFeature struct {
		ID                 string   `json:"id"`
		Summary            string   `json:"summary"`
		Title              string   `json:"title"`
		Description        string   `json:"description"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}
	type inFlightStrategy struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
		Title   string `json:"title"`
		Kind    string `json:"kind"`
		Body    string `json:"body"`
	}
	type inFlightDecision struct {
		ID        string `json:"id"`
		Summary   string `json:"summary"`
		Title     string `json:"title"`
		Rationale string `json:"rationale"`
	}
	type inFlightProposal struct {
		Features   []inFlightFeature  `json:"features"`
		Strategies []inFlightStrategy `json:"strategies"`
		Decisions  []inFlightDecision `json:"decisions"`
	}
	var prop inFlightProposal
	for _, e := range s.features {
		prop.Features = append(prop.Features, inFlightFeature{
			ID:                 e.body.ID,
			Summary:            e.body.Summary,
			Title:              e.body.Title,
			Description:        e.body.Description,
			AcceptanceCriteria: e.body.AcceptanceCriteria,
		})
	}
	for _, e := range s.strategies {
		prop.Strategies = append(prop.Strategies, inFlightStrategy{
			ID:      e.body.ID,
			Summary: e.body.Summary,
			Title:   e.body.Title,
			Kind:    string(e.body.Kind),
		})
	}
	for _, e := range s.decisions {
		prop.Decisions = append(prop.Decisions, inFlightDecision{
			ID:        e.body.ID,
			Summary:   e.body.Summary,
			Title:     e.body.Title,
			Rationale: e.body.Rationale,
		})
	}
	data, err := json.Marshal(prop)
	if err != nil {
		return fmt.Errorf("rebuildIndex: marshal: %w", err)
	}
	return s.index.Rebuild(string(data))
}

// writeJSONNode marshals a typed spec body and writes it to the given
// path under `.borg/spec/`. Creates the parent directory if absent.
func writeJSONNode(fsys specio.FS, path string, body any) error {
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return fmt.Errorf("writeJSONNode: marshal %s: %w", path, err)
	}
	dir := path
	if i := strings.LastIndex(path, "/"); i > 0 {
		dir = path[:i]
	}
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("writeJSONNode: mkdir %s: %w", dir, err)
	}
	return fsys.WriteFile(path, data, 0o644)
}

// cloneGoalMap and friends produce shallow copies of the typed
// maps — sufficient for Rollback because Put replaces entries by
// pointer rather than mutating them in place.
func cloneGoalMap(in map[string]*goalEntry) map[string]*goalEntry {
	out := make(map[string]*goalEntry, len(in))
	for k, v := range in {
		c := *v
		out[k] = &c
	}
	return out
}
func cloneAntiGoalMap(in map[string]*antiGoalEntry) map[string]*antiGoalEntry {
	out := make(map[string]*antiGoalEntry, len(in))
	for k, v := range in {
		c := *v
		out[k] = &c
	}
	return out
}
func cloneFeatureMap(in map[string]*featureEntry) map[string]*featureEntry {
	out := make(map[string]*featureEntry, len(in))
	for k, v := range in {
		c := *v
		out[k] = &c
	}
	return out
}
func cloneStrategyMap(in map[string]*strategyEntry) map[string]*strategyEntry {
	out := make(map[string]*strategyEntry, len(in))
	for k, v := range in {
		c := *v
		out[k] = &c
	}
	return out
}
func cloneDecisionMap(in map[string]*decisionEntry) map[string]*decisionEntry {
	out := make(map[string]*decisionEntry, len(in))
	for k, v := range in {
		c := *v
		out[k] = &c
	}
	return out
}
func cloneBugMap(in map[string]*bugEntry) map[string]*bugEntry {
	out := make(map[string]*bugEntry, len(in))
	for k, v := range in {
		c := *v
		out[k] = &c
	}
	return out
}
func cloneApproachMap(in map[string]*approachEntry) map[string]*approachEntry {
	out := make(map[string]*approachEntry, len(in))
	for k, v := range in {
		c := *v
		out[k] = &c
	}
	return out
}
