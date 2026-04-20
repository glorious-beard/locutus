package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/state"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStore() *state.FileStateStore {
	return state.NewFileStateStore(specio.NewMemFS(), ".locutus/state")
}

func sampleState(approachID string) state.ReconciliationState {
	return state.ReconciliationState{
		ApproachID:     approachID,
		SpecHash:       "sha256:abc123",
		Status:         state.StatusLive,
		Message:        "all assertions passed",
		LastReconciled: time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
		WorkstreamID:   "ws-1",
		Artifacts: map[string]string{
			"src/auth/oauth.go":      "sha256:def456",
			"src/auth/oauth_test.go": "sha256:789abc",
		},
		AssertionResults: []state.AssertionResult{
			{
				Assertion: spec.Assertion{Kind: spec.AssertionKindTestPass, Target: "./..."},
				Passed:    true,
				Output:    "ok github.com/chetan/locutus",
				RunAt:     time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
			},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	store := newStore()
	s := sampleState("oauth-login")

	require.NoError(t, store.Save(s))

	got, err := store.Load("oauth-login")
	require.NoError(t, err)

	assert.Equal(t, s.ApproachID, got.ApproachID)
	assert.Equal(t, s.SpecHash, got.SpecHash)
	assert.Equal(t, s.Status, got.Status)
	assert.Equal(t, s.Message, got.Message)
	assert.Equal(t, s.WorkstreamID, got.WorkstreamID)
	assert.Equal(t, s.Artifacts, got.Artifacts)
	assert.Equal(t, s.LastReconciled.UTC(), got.LastReconciled.UTC())
	require.Len(t, got.AssertionResults, 1)
	assert.Equal(t, s.AssertionResults[0].Kind, got.AssertionResults[0].Kind)
	assert.Equal(t, s.AssertionResults[0].Passed, got.AssertionResults[0].Passed)
	assert.Equal(t, s.AssertionResults[0].Output, got.AssertionResults[0].Output)
}

func TestErrNotFound(t *testing.T) {
	store := newStore()
	_, err := store.Load("nonexistent")
	assert.True(t, errors.Is(err, state.ErrNotFound))
}

func TestOverwriteUpdatesStatus(t *testing.T) {
	store := newStore()
	s := sampleState("oauth-login")
	require.NoError(t, store.Save(s))

	s.Status = state.StatusFailed
	s.Message = "compilation error"
	require.NoError(t, store.Save(s))

	got, err := store.Load("oauth-login")
	require.NoError(t, err)
	assert.Equal(t, state.StatusFailed, got.Status)
	assert.Equal(t, "compilation error", got.Message)
}

func TestWalkReturnsSortedByApproachID(t *testing.T) {
	store := newStore()
	for _, id := range []string{"zebra-feature", "alpha-feature", "middle-feature"} {
		s := sampleState(id)
		require.NoError(t, store.Save(s))
	}

	results, err := store.Walk()
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, "alpha-feature", results[0].ApproachID)
	assert.Equal(t, "middle-feature", results[1].ApproachID)
	assert.Equal(t, "zebra-feature", results[2].ApproachID)
}

func TestDelete(t *testing.T) {
	store := newStore()
	s := sampleState("oauth-login")
	require.NoError(t, store.Save(s))

	require.NoError(t, store.Delete("oauth-login"))

	_, err := store.Load("oauth-login")
	assert.True(t, errors.Is(err, state.ErrNotFound))
}

func TestWalkEmptyStore(t *testing.T) {
	store := newStore()
	results, err := store.Walk()
	require.NoError(t, err)
	assert.Empty(t, results)
}
