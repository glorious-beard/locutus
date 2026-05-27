// DJ-141 — propose/revise goal tools accept an optional `origin` and
// enforce exactly-one-of {source_clause, origin}: an anchored node
// supplies source_clause, an unanchored node supplies origin, never
// both and never neither.
package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func timeZero() time.Time { return time.Time{} }

func TestBuildGoalBodyExactlyOneOf(t *testing.T) {
	_, err := buildGoalBody(proposeGoalInput{ID: "goal-x", Title: "X", Body: "b"}, timeZero())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one of")

	_, err = buildGoalBody(proposeGoalInput{ID: "goal-x", Title: "X", Body: "b", SourceClause: "c", Origin: "o"}, timeZero())
	require.Error(t, err)

	g, err := buildGoalBody(proposeGoalInput{ID: "goal-x", Title: "X", Body: "b", SourceClause: "c"}, timeZero())
	require.NoError(t, err)
	assert.Equal(t, "c", g.SourceClause)
	assert.Empty(t, g.Origin)

	g, err = buildGoalBody(proposeGoalInput{ID: "goal-x", Title: "X", Body: "b", Origin: "mission statement"}, timeZero())
	require.NoError(t, err)
	assert.Equal(t, "mission statement", g.Origin)
	assert.Empty(t, g.SourceClause)
}

func TestBuildAntiGoalBodyExactlyOneOf(t *testing.T) {
	_, err := buildAntiGoalBody(proposeAntiGoalInput{ID: "agoal-x", Title: "X", Body: "b"}, timeZero())
	require.Error(t, err)
	a, err := buildAntiGoalBody(proposeAntiGoalInput{ID: "agoal-x", Title: "X", Body: "b", Origin: "dec-product-scope-boundary"}, timeZero())
	require.NoError(t, err)
	assert.Equal(t, "dec-product-scope-boundary", a.Origin)
}

func TestUpdateGoalsMdHashInputHasNoSyncedAtField(t *testing.T) {
	// DJ-141: synced_at is server-stamped, never agent-supplied. The
	// input struct must not carry a SyncedAt field (the agent has no
	// clock; on the winplan run it fabricated a midnight timestamp).
	var in updateGoalsMdHashInput
	in.Hash = "sha256:abc"
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "synced_at")
}
