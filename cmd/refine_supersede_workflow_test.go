package cmd

import (
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/cascade"
	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupersedeWorkflow_KindRouting locks in the per-kind agent
// routing for phase 1's fanout-of-one. The workflow itself is a single
// var across all three kinds (Decision / Feature / Strategy) — kind
// dispatch happens via the fanout item's agent_id under DJ-098.
func TestSupersedeWorkflow_KindRouting(t *testing.T) {
	cases := []struct {
		kind     spec.NodeKind
		expected string
	}{
		{spec.KindDecision, "refiner-supersede-decision"},
		{spec.KindFeature, "refiner-supersede-feature"},
		{spec.KindStrategy, "refiner-supersede-strategy"},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			s := &SupersedeState{Kind: c.kind}
			items, err := fanoutSupersedeKind(s)
			require.NoError(t, err)
			require.Len(t, items, 1, "phase 1 emits exactly one fanout item")

			var item supersedeReplaceItem
			require.NoError(t, json.Unmarshal([]byte(items[0]), &item))
			assert.Equal(t, c.expected, item.AgentID,
				"kind %s must route to %s", c.kind, c.expected)
			assert.Equal(t, string(c.kind), item.Kind)
		})
	}
}

// TestSupersedeWorkflow_KindRouting_BugIsRejected covers the defensive
// branch: the cmd layer already rejects bug targets before invoking
// the workflow, but fanoutSupersedeKind must surface an error rather
// than silently emitting an empty fanout when invariants are
// violated.
func TestSupersedeWorkflow_KindRouting_BugIsRejected(t *testing.T) {
	s := &SupersedeState{Kind: spec.KindBug}
	items, err := fanoutSupersedeKind(s)
	require.Error(t, err, "bug targets must surface as a fanout error")
	assert.Contains(t, err.Error(), "unsupported kind")
	assert.Nil(t, items)
}

// TestSupersedeWorkflow_ProseFanoutAllParents drives phase 2's fanout
// against a plan with one feature, one strategy, and one bug to
// rewrite. The output must contain one item per parent with
// agent_id="refiner" and the right kind/id pair.
func TestSupersedeWorkflow_ProseFanoutAllParents(t *testing.T) {
	s := &SupersedeState{
		Plan: &cascade.SupersedePlan{
			InPlace:             false,
			FeaturesToRewrite:   []string{"feat-alpha"},
			StrategiesToRewrite: []string{"strat-go"},
			BugsToRewrite:       []string{"bug-1"},
		},
	}
	items, err := fanoutSupersedeProse(s)
	require.NoError(t, err)
	require.Len(t, items, 3, "one fanout item per parent")

	kinds := make([]string, 0, len(items))
	ids := make([]string, 0, len(items))
	for _, raw := range items {
		var item supersedeProseItem
		require.NoError(t, json.Unmarshal([]byte(raw), &item))
		assert.Equal(t, "refiner", item.AgentID,
			"phase 2 always routes to the refiner agent")
		kinds = append(kinds, item.Kind)
		ids = append(ids, item.ID)
	}
	assert.Equal(t, []string{"feature", "strategy", "bug"}, kinds,
		"emission order: features, then strategies, then bugs")
	assert.Equal(t, []string{"feat-alpha", "strat-go", "bug-1"}, ids)
}

// TestSupersedeWorkflow_ProseEmptyWhenInPlace covers the conditional
// firing the plan calls out: in-place supersedes (same old/new id)
// don't change downstream id references, so phase 2 has nothing to
// rewrite. Empty fanout short-circuits the phase entirely.
func TestSupersedeWorkflow_ProseEmptyWhenInPlace(t *testing.T) {
	s := &SupersedeState{
		Plan: &cascade.SupersedePlan{
			InPlace: true,
			// Populated to prove that InPlace overrides the parent
			// lists even when they're non-empty (defensive: an
			// in-place plan shouldn't carry rewrite lists, but if a
			// caller misbuilds one, the workflow still skips prose).
			FeaturesToRewrite: []string{"feat-alpha"},
		},
	}
	items, err := fanoutSupersedeProse(s)
	require.NoError(t, err)
	assert.Nil(t, items, "InPlace plan must produce zero fanout items")
}

// TestSupersedeWorkflow_ProseEmptyWhenNoPlan covers the defensive
// branch: a workflow that failed phase 1 (state.Plan == nil) must not
// crash phase 2's fanout. Returns nil cleanly so the executor
// short-circuits without dispatching.
func TestSupersedeWorkflow_ProseEmptyWhenNoPlan(t *testing.T) {
	s := &SupersedeState{Plan: nil}
	items, err := fanoutSupersedeProse(s)
	require.NoError(t, err)
	assert.Nil(t, items)
}
