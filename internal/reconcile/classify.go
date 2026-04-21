// Package reconcile implements the DJ-068 reconciliation loop: diff the
// spec graph (desired state) against the state store (observed state) to
// produce a plan-ready set of Approaches for `adopt` to dispatch.
//
// The reconciler is a function, not a daemon — each invocation runs to
// completion and exits. Long-running supervision of agents within an
// invocation lives in the dispatch package; the reconciler only decides
// what to dispatch and updates state.
package reconcile

import (
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
)

// Classification is the reconciler's verdict on a single Approach: what
// status it should have, what changed since the last reconcile, and the
// hashes that led to that verdict.
type Classification struct {
	Approach     spec.Approach
	Status       state.ReconcileStatus
	StoredHash   string            // from the state store; "" if no prior entry
	CurrentHash  string            // freshly computed from current spec
	StoredFiles  map[string]string // stored artifact hashes
	CurrentFiles map[string]string // current artifact hashes
	StateEntry   *state.ReconciliationState // the raw state entry, if any
}

// DriftedSpec returns true when the spec has changed since the last
// reconcile (forward drift).
func (c Classification) DriftedSpec() bool {
	return c.StoredHash != "" && c.StoredHash != c.CurrentHash
}

// DriftedArtifacts returns true when at least one artifact file has changed
// outside Locutus (backward drift).
func (c Classification) DriftedArtifacts() bool {
	return !spec.ArtifactsEqual(c.StoredFiles, c.CurrentFiles)
}

// Classify loads the spec graph and state store, pairs Approaches with
// their state entries, and assigns each a status based on the DJ-068
// classification rules:
//
//   - No state entry                       → unplanned
//   - stored_hash != current_hash           → drifted (forward drift)
//   - artifacts changed outside Locutus     → out_of_spec (backward drift)
//   - prior status live, no change observed → live
//   - any prior terminal state (failed, in_progress) → preserved
//
// Note: forward drift (spec changed) takes priority over backward drift so
// that a simultaneous spec+artifact change surfaces as `drifted` — the
// reconciler will regenerate from spec, which is authoritative.
func Classify(
	fsys specio.FS,
	graph *spec.SpecGraph,
	store *state.FileStateStore,
	decisionsByID map[string]spec.Decision,
) ([]Classification, error) {
	entries, err := store.Walk()
	if err != nil {
		return nil, err
	}
	byApproach := make(map[string]state.ReconciliationState, len(entries))
	for _, e := range entries {
		byApproach[e.ApproachID] = e
	}

	var out []Classification
	for id, node := range graph.Nodes() {
		if node.Kind != spec.KindApproach {
			continue
		}
		a := graph.Approach(id)
		if a == nil {
			continue
		}
		c := classifyOne(fsys, *a, byApproach, decisionsByID)
		out = append(out, c)
	}
	return out, nil
}

func classifyOne(
	fsys specio.FS,
	a spec.Approach,
	store map[string]state.ReconciliationState,
	decisionsByID map[string]spec.Decision,
) Classification {
	applicable := applicableDecisions(a, decisionsByID)
	currentSpec := spec.ComputeSpecHash(a, applicable)
	currentFiles := spec.ComputeArtifactHashes(fsys.ReadFile, a)

	c := Classification{
		Approach:     a,
		CurrentHash:  currentSpec,
		CurrentFiles: currentFiles,
	}

	entry, ok := store[a.ID]
	if !ok {
		c.Status = state.StatusUnplanned
		return c
	}
	c.StateEntry = &entry
	c.StoredHash = entry.SpecHash
	c.StoredFiles = entry.Artifacts

	// Forward drift (spec changed) takes priority.
	if c.DriftedSpec() {
		c.Status = state.StatusDrifted
		return c
	}
	// Backward drift — artifacts edited outside Locutus.
	if len(currentFiles) > 0 && c.DriftedArtifacts() {
		c.Status = state.StatusOutOfSpec
		return c
	}

	// No drift observed. Preserve the prior status unless it's one of the
	// transient states that should now be considered live.
	switch entry.Status {
	case state.StatusLive, state.StatusFailed, state.StatusInProgress,
		state.StatusPlanned, state.StatusPreFlight:
		c.Status = entry.Status
	default:
		c.Status = state.StatusUnplanned
	}
	return c
}

// applicableDecisions looks up decisions referenced by an Approach. Missing
// IDs are silently skipped — they'd already be caught by validation
// elsewhere and the hash should still be stable against what exists now.
func applicableDecisions(a spec.Approach, byID map[string]spec.Decision) []spec.Decision {
	if len(a.Decisions) == 0 {
		return nil
	}
	out := make([]spec.Decision, 0, len(a.Decisions))
	for _, id := range a.Decisions {
		if d, ok := byID[id]; ok {
			out = append(out, d)
		}
	}
	return out
}

// PlanCandidates returns classifications whose status indicates the
// Approach is ready to be planned and dispatched (drifted, unplanned,
// failed, or previously planned/in_progress). Live and out_of_spec are
// excluded: live needs nothing, out_of_spec needs human action first.
func PlanCandidates(cs []Classification) []Classification {
	var out []Classification
	for _, c := range cs {
		switch c.Status {
		case state.StatusDrifted, state.StatusUnplanned, state.StatusFailed,
			state.StatusPlanned, state.StatusPreFlight, state.StatusInProgress:
			out = append(out, c)
		}
	}
	return out
}

// OutOfSpec returns classifications flagged as backward drift. These need
// human resolution before adopt can proceed for those Approaches.
func OutOfSpec(cs []Classification) []Classification {
	var out []Classification
	for _, c := range cs {
		if c.Status == state.StatusOutOfSpec {
			out = append(out, c)
		}
	}
	return out
}
