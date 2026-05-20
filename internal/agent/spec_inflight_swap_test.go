// DJ-125 Phase 3 — spec_list_manifest / spec_get swap tests.
//
// Covers the read-through swap pattern for the two new swappables, the
// in-flight store's merge behaviour (in-flight wins over existing on
// ID collision; existing fills in the rest), and the wiring assertion
// that mergeDecisions / mergeNarrative refresh the store via
// rebuildInFlightIndex.

package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// TestSwappableSpecListManifestRoutesToInFlight verifies that when a
// SwappableSpecListManifest is swapped to an InFlightSpecStore, the
// handler returns in-flight content rather than the on-disk default.
func TestSwappableSpecListManifestRoutesToInFlight(t *testing.T) {
	// Wire an fsys-only default first.
	memfs := specio.NewMemFS()
	defaultProvider := NewFSSpecProvider(memfs)
	swap := NewSwappableSpecListManifest(defaultProvider)

	// Disk is empty, so the default should return an empty manifest.
	m, err := swap.ListManifest()
	require.NoError(t, err)
	assert.Empty(t, m.Features)
	assert.Empty(t, m.Decisions)

	// Plant an in-flight proposal and swap the store in.
	raw := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-inflight-auth", Title: "Auth", Summary: "WorkOS auth.", Decisions: []string{"dec-workos"}},
		},
		Decisions: []RawDecisionProposal{
			{ID: "dec-workos", Title: "WorkOS", Summary: "Adopt WorkOS for identity."},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)

	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), nil)
	prev := swap.Swap(store)
	defer swap.Swap(prev)

	m, err = swap.ListManifest()
	require.NoError(t, err)
	require.Len(t, m.Features, 1)
	assert.Equal(t, "feat-inflight-auth", m.Features[0].ID)
	require.Len(t, m.Decisions, 1)
	assert.Equal(t, "dec-workos", m.Decisions[0].ID)
}

// TestSwappableSpecGetRoutesToInFlight verifies spec_get redirects to
// the in-flight content and falls back to state.Existing entries when
// the in-flight proposal doesn't carry the id.
func TestSwappableSpecGetRoutesToInFlight(t *testing.T) {
	memfs := specio.NewMemFS()
	defaultProvider := NewFSSpecProvider(memfs)
	swap := NewSwappableSpecGet(defaultProvider)

	// In-flight has dec-workos; existing snapshot has dec-prior.
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-workos", Title: "WorkOS", Summary: "Adopt WorkOS for identity."},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)

	existing := &ExistingSpec{
		Decisions: []spec.Decision{
			{ID: "dec-prior", Title: "Postgres", Summary: "Adopt Postgres."},
		},
	}
	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), existing)
	prev := swap.Swap(store)
	defer swap.Swap(prev)

	// In-flight first.
	data, err := swap.GetSpec("dec-workos")
	require.NoError(t, err)
	var found map[string]any
	require.NoError(t, json.Unmarshal(data, &found))
	assert.Equal(t, "dec-workos", found["id"])

	// Fall through to existing.
	data, err = swap.GetSpec("dec-prior")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &found))
	assert.Equal(t, "dec-prior", found["id"])

	// Unknown id returns an actionable not-found.
	_, err = swap.GetSpec("dec-missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no node with id")

	// Malformed id rejected before lookup.
	_, err = swap.GetSpec("dec-../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed")
}

// TestInFlightSpecStoreMergesExistingAndInFlight verifies the manifest
// shape: in-flight nodes appear first; existing nodes whose ids don't
// collide append; in-flight wins on collision.
func TestInFlightSpecStoreMergesExistingAndInFlight(t *testing.T) {
	raw := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-dashboard", Title: "Inflight Dashboard", Summary: "Inflight version.", Decisions: []string{"dec-x"}},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)
	existing := &ExistingSpec{
		Features: []spec.Feature{
			// Same id as in-flight: in-flight wins.
			{ID: "feat-dashboard", Title: "Existing Dashboard", Summary: "Existing version."},
			// Distinct id: appended.
			{ID: "feat-other", Title: "Other", Summary: "Other feature."},
		},
	}

	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), existing)
	m, err := store.ListManifest()
	require.NoError(t, err)

	require.Len(t, m.Features, 2)
	// In-flight version wins on collision.
	assert.Equal(t, "feat-dashboard", m.Features[0].ID)
	assert.Equal(t, "Inflight Dashboard", m.Features[0].Title)
	// Existing-only feature appended.
	assert.Equal(t, "feat-other", m.Features[1].ID)
}

// TestRebuildInFlightIndexUpdatesSpecStore verifies the wiring: when
// rebuildInFlightIndex fires (which mergeDecisions / mergeNarrative /
// mergeReconciledProposal all call), the in-flight store's underlying
// rawProposal is refreshed so subsequent tool calls see the new
// content.
func TestRebuildInFlightIndexUpdatesSpecStore(t *testing.T) {
	store := NewInFlightSpecStore()
	store.SetState("", nil) // starts empty

	state := &PlanningState{
		InFlightSpecStore: store,
		RawProposal:       `{"features":[{"id":"feat-from-merge","title":"From Merge","summary":"Set during a merge."}]}`,
	}

	// Pre-condition: store is empty.
	m, err := store.ListManifest()
	require.NoError(t, err)
	assert.Empty(t, m.Features)

	// Trigger the rebuild — this is exactly what mergeDecisions /
	// mergeNarrative call at the end.
	rebuildInFlightIndex(state)

	// Post-condition: store reflects the new RawProposal.
	m, err = store.ListManifest()
	require.NoError(t, err)
	require.Len(t, m.Features, 1)
	assert.Equal(t, "feat-from-merge", m.Features[0].ID)
}

// TestRegisterSpecToolsUsesSwappables wires both swappables into the
// tool registry and confirms a swap takes effect through the tool
// dispatch — i.e. the handler returns the new delegate's output, not
// the on-disk default's, after Swap fires.
func TestRegisterSpecToolsUsesSwappables(t *testing.T) {
	memfs := specio.NewMemFS()
	defaultProvider := NewFSSpecProvider(memfs)
	listSwap := NewSwappableSpecListManifest(defaultProvider)
	getSwap := NewSwappableSpecGet(defaultProvider)

	registry := NewToolRegistry()
	RegisterSpecTools(registry, memfs, nil, listSwap, getSwap)

	listDef, ok := registry.Resolve(ToolNameSpecListManifest)
	require.True(t, ok)
	getDef, ok := registry.Resolve(ToolNameSpecGet)
	require.True(t, ok)

	// Default (disk-empty) returns empty manifest.
	resJSON, err := listDef.Handler(context.Background(), []byte("{}"))
	require.NoError(t, err)
	var emptyManifest SpecManifest
	require.NoError(t, json.Unmarshal(resJSON, &emptyManifest))
	assert.Empty(t, emptyManifest.Features)

	// Swap in an in-flight store.
	raw := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-swapped", Title: "Swapped", Summary: "Swapped content."},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)
	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), nil)
	listSwap.Swap(store)
	getSwap.Swap(store)

	resJSON, err = listDef.Handler(context.Background(), []byte("{}"))
	require.NoError(t, err)
	var swappedManifest SpecManifest
	require.NoError(t, json.Unmarshal(resJSON, &swappedManifest))
	require.Len(t, swappedManifest.Features, 1)
	assert.Equal(t, "feat-swapped", swappedManifest.Features[0].ID)

	// spec_get should also route through.
	args, err := json.Marshal(SpecGetInput{ID: "feat-swapped"})
	require.NoError(t, err)
	resJSON, err = getDef.Handler(context.Background(), args)
	require.NoError(t, err)
	var node map[string]any
	require.NoError(t, json.Unmarshal(resJSON, &node))
	assert.Equal(t, "feat-swapped", node["id"])
}
