package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListManifest_OriginPerEntry verifies the Origin field that
// distinguishes settled (on-disk, from existing snapshot) from
// proposed (in-flight, council-authored this iteration). In-flight
// entries win on id collision and get OriginProposed; existing-only
// entries get OriginSettled.
func TestListManifest_OriginPerEntry(t *testing.T) {
	raw := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-inflight-new", Title: "Newly authored", Summary: "Just minted.", Decisions: []string{"dec-x"}},
			{ID: "feat-overlap", Title: "Overlap (inflight rev)", Summary: "Council rewrote this one.", Decisions: []string{"dec-x"}},
		},
		Decisions: []RawDecisionProposal{
			{ID: "dec-inflight-d", Title: "Council decision", Rationale: "r", Axes: []string{"some-axis"}},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)
	existing := &ExistingSpec{
		Features: []spec.Feature{
			{ID: "feat-overlap", Title: "Overlap (settled)", Summary: "The disk version."},
			{ID: "feat-only-on-disk", Title: "Only on disk", Summary: "Carried over."},
		},
		Decisions: []spec.Decision{
			{ID: "dec-only-on-disk", Title: "Disk decision", Rationale: "from disk"},
		},
	}

	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), existing)
	m, err := store.ListManifest()
	require.NoError(t, err)

	// Build an id → origin map for clear assertions
	originByID := map[string]SpecManifestOrigin{}
	for _, e := range m.Features {
		originByID[e.ID] = e.Origin
	}
	for _, e := range m.Decisions {
		originByID[e.ID] = e.Origin
	}

	assert.Equal(t, OriginProposed, originByID["feat-inflight-new"],
		"in-flight-only entry carries OriginProposed")
	assert.Equal(t, OriginProposed, originByID["feat-overlap"],
		"in-flight wins on id collision and the entry's Origin is Proposed (the council is rewriting the disk version)")
	assert.Equal(t, OriginSettled, originByID["feat-only-on-disk"],
		"existing-only entry (no in-flight match) carries OriginSettled")
	assert.Equal(t, OriginProposed, originByID["dec-inflight-d"], "in-flight decision is Proposed")
	assert.Equal(t, OriginSettled, originByID["dec-only-on-disk"], "existing-only decision is Settled")
}

// TestListManifest_WorkingFromOpenAxes verifies that a decision whose
// axes[] intersects state.AxesOpen lands with Working=true. The
// scout has reopened the axis; an elaborator dispatch is about to
// rewrite the in-flight decision.
func TestListManifest_WorkingFromOpenAxes(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-stable", Title: "Stable", Axes: []string{"axis-stable"}, Rationale: "r"},
			{ID: "dec-working", Title: "Being revised", Axes: []string{"axis-working"}, Rationale: "r"},
		},
	}
	rawJSON, err := json.Marshal(raw)
	require.NoError(t, err)

	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), nil)
	store.SetWorkingSignals(workingSignals{
		OpenAxisIDs: map[string]struct{}{
			"axis-working": {},
		},
	})

	m, err := store.ListManifest()
	require.NoError(t, err)

	workingByID := map[string]bool{}
	for _, e := range m.Decisions {
		workingByID[e.ID] = e.Working
	}
	assert.False(t, workingByID["dec-stable"],
		"decision whose axes are NOT in the scout's open set is not Working")
	assert.True(t, workingByID["dec-working"],
		"decision whose axes intersect the scout's open set is Working — the elaborator dispatch will revise it this iteration")
}

// TestListManifest_WorkingFromOpenConcerns verifies that a decision
// flagged by an open critic concern (via RelatedDecisionIDs) lands
// with Working=true. The revise-decisions dispatch will target it
// this iteration.
func TestListManifest_WorkingFromOpenConcerns(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-targeted", Title: "Has a concern", Axes: []string{"a"}, Rationale: "r"},
			{ID: "dec-untouched", Title: "No concerns", Axes: []string{"b"}, Rationale: "r"},
		},
	}
	rawJSON, _ := json.Marshal(raw)

	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), nil)
	store.SetWorkingSignals(workingSignals{
		OpenConcernDecisionIDs: map[string]struct{}{
			"dec-targeted": {},
		},
	})

	m, _ := store.ListManifest()
	workingByID := map[string]bool{}
	for _, e := range m.Decisions {
		workingByID[e.ID] = e.Working
	}
	assert.True(t, workingByID["dec-targeted"], "decision named in an open concern is Working")
	assert.False(t, workingByID["dec-untouched"], "decision not flagged stays stable")
}

// TestListManifest_WorkingFromNewNodes verifies that a feature /
// strategy newly introduced by the scout this iteration lands with
// Working=true even before its narrative dispatch fires.
func TestListManifest_WorkingFromNewNodes(t *testing.T) {
	raw := RawSpecProposal{
		Features: []RawFeatureProposal{
			{ID: "feat-just-introduced", Title: "Brand new", Summary: "scout surfaced this iter"},
			{ID: "feat-settled", Title: "Existing", Summary: "narrative landed already"},
		},
	}
	rawJSON, _ := json.Marshal(raw)

	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), nil)
	store.SetWorkingSignals(workingSignals{
		NewNodeIDs: map[string]struct{}{
			"feat-just-introduced": {},
		},
	})

	m, _ := store.ListManifest()
	workingByID := map[string]bool{}
	for _, e := range m.Features {
		workingByID[e.ID] = e.Working
	}
	assert.True(t, workingByID["feat-just-introduced"], "scout-introduced new feature is Working")
	assert.False(t, workingByID["feat-settled"], "existing feature with no pending narrative dispatch is not Working")
}

// TestComputeWorkingSignals_FromPlanningState locks in the wiring
// between live PlanningState and the in-flight store's working
// signals. Each input source (AxesOpen, NewNodesFromScout, open
// Concerns with RelatedDecisionIDs, open Concerns mentioning node ids
// in their text) contributes to the resulting signal set.
func TestComputeWorkingSignals_FromPlanningState(t *testing.T) {
	s := &PlanningState{
		AxesOpen: []OpenAxis{
			{ID: "axis-A"},
			{ID: "axis-B"},
		},
		NewNodesFromScout: []NewSpecNode{
			{ID: "feat-new", Title: "new feat"},
			{ID: "strat-new", Title: "new strat"},
		},
		Concerns: []Concern{
			{
				AgentID: "critic",
				Status:  ConcernStatusOpen,
				Text:    "feat-some-feature has a contradiction with feat-other-feature",
				RelatedDecisionIDs: []string{"dec-targeted-1", "dec-targeted-2"},
			},
			{
				// Closed concern — should not contribute
				AgentID: "critic",
				Status:  ConcernStatusAddressed,
				Text:    "feat-resolved had an issue",
				RelatedDecisionIDs: []string{"dec-resolved"},
			},
		},
	}
	sig := computeWorkingSignals(s)

	// AxesOpen
	require.Len(t, sig.OpenAxisIDs, 2)
	assert.Contains(t, sig.OpenAxisIDs, "axis-A")
	assert.Contains(t, sig.OpenAxisIDs, "axis-B")

	// NewNodesFromScout
	require.Len(t, sig.NewNodeIDs, 2)
	assert.Contains(t, sig.NewNodeIDs, "feat-new")
	assert.Contains(t, sig.NewNodeIDs, "strat-new")

	// Open concern's RelatedDecisionIDs
	require.Len(t, sig.OpenConcernDecisionIDs, 2)
	assert.Contains(t, sig.OpenConcernDecisionIDs, "dec-targeted-1")
	assert.Contains(t, sig.OpenConcernDecisionIDs, "dec-targeted-2")
	assert.NotContains(t, sig.OpenConcernDecisionIDs, "dec-resolved",
		"addressed concern's decision-ids do not contribute to the working set")

	// Open concern's text-mentioned feat-/strat- ids
	require.Len(t, sig.OpenConcernNodeMatches, 2)
	assert.Contains(t, sig.OpenConcernNodeMatches, "feat-some-feature")
	assert.Contains(t, sig.OpenConcernNodeMatches, "feat-other-feature")
}

// TestGetSpec_NotFoundInlinesKindMatchedManifest verifies that
// GetSpec's not-found error message inlines every id of the same
// kind as the requested id — the cheap defense against the Gemini
// 3.5 Flash tool-loop pathology where the model confabulates
// plausibly-named ids and spirals trying to look them up. Once the
// error response carries the real candidate set, the next round's
// best play is to pick one of those ids rather than guess another
// variant.
func TestGetSpec_NotFoundInlinesKindMatchedManifest(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-postgres-oltp-store", Title: "OLTP store", Rationale: "r"},
			{ID: "dec-render-compute-platform", Title: "Compute platform", Rationale: "r"},
		},
		Features: []RawFeatureProposal{
			{ID: "feat-dashboard", Title: "Dashboard"},
		},
	}
	rawJSON, _ := json.Marshal(raw)

	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), nil)

	// Confabulated id with the right prefix; the error should list
	// the real dec-* ids.
	_, err := store.GetSpec("dec-supabase-storage-tus-railway-ingestion")
	require.Error(t, err)
	msg := err.Error()

	assert.Contains(t, msg, "dec-supabase-storage-tus-railway-ingestion",
		"error message names the missing id so the model sees what failed")
	assert.Contains(t, msg, "dec-postgres-oltp-store",
		"error inlines real dec-* ids of the same kind so the model can recover")
	assert.Contains(t, msg, "dec-render-compute-platform",
		"all dec-* ids land in the recovery list")
	assert.NotContains(t, msg, "feat-dashboard",
		"feature ids are NOT inlined — they're a different kind, surfacing them would invite the model to mismatch kinds")
	assert.Contains(t, msg, "do not guess at variant slugs",
		"the error explicitly tells the model not to try further variants — addresses the spiral failure mode")
}

// TestGetSpec_NotFoundWithEmptyManifest verifies a graceful message
// when no entries of the requested kind exist. Without an empty
// fallback, the error would emit a heading like "Available ids of
// this kind (0):" with no list — readable but slightly weird.
func TestGetSpec_NotFoundWithEmptyManifest(t *testing.T) {
	store := NewInFlightSpecStore()
	store.SetState("", nil) // no in-flight, no existing

	_, err := store.GetSpec("dec-anything")
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "dec-anything")
	assert.Contains(t, msg, "no nodes of this kind exist",
		"empty-graph case produces a distinct message rather than an empty pick list")
}

// TestGetSpec_FindsIDStillReturnsBody verifies that the new
// not-found error path doesn't accidentally regress the success
// path — a real id still returns the node body.
func TestGetSpec_FindsIDStillReturnsBody(t *testing.T) {
	raw := RawSpecProposal{
		Decisions: []RawDecisionProposal{
			{ID: "dec-supabase-postgresql", Title: "Postgres", Rationale: "JSONB plus PostGIS."},
		},
	}
	rawJSON, _ := json.Marshal(raw)
	store := NewInFlightSpecStore()
	store.SetState(string(rawJSON), nil)

	body, err := store.GetSpec("dec-supabase-postgresql")
	require.NoError(t, err)
	require.NotNil(t, body)
	assert.True(t, strings.Contains(string(body), "dec-supabase-postgresql"),
		"successful lookup returns the node's JSON body")
}
