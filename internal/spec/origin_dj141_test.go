// DJ-141 — Origin records provenance for unanchored goal-layer nodes
// (claims with no verbatim GOALS.md excerpt). Anchored nodes carry a
// non-empty SourceClause and omit Origin; unanchored nodes carry a
// non-empty Origin and an empty SourceClause.
package spec_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/glorious-beard/locutus/internal/spec"
)

func TestGoalOriginRoundTrips(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	g := spec.Goal{
		ID: "goal-win-number-computation", Title: "Win-number computation",
		Body: "Computes the vote target.", Origin: "dec-product-scope-boundary",
		CreatedAt: now, UpdatedAt: now,
	}
	raw, err := json.Marshal(g)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"origin":"dec-product-scope-boundary"`)

	var back spec.Goal
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, "dec-product-scope-boundary", back.Origin)
	assert.Empty(t, back.SourceClause)
}

func TestGoalOriginOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(spec.Goal{ID: "goal-x", SourceClause: "verbatim clause"})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "origin", "anchored node must omit empty origin")
}

func TestAntiGoalOriginRoundTrips(t *testing.T) {
	raw, err := json.Marshal(spec.AntiGoal{ID: "agoal-x", Origin: "mission statement"})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"origin":"mission statement"`)
}
