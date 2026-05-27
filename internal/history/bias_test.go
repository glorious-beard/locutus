// DJ-138 phase 3 — spec_biased and approach_drifted event round-trips,
// plus the caused_by linkage that builds the cascade audit tree.

package history_test

import (
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/history"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecBiasedEventRoundTrip — record a spec_biased event with
// the full payload (bias text, blast radius, ACP session id) and
// read it back via Events().
func TestSpecBiasedEventRoundTrip(t *testing.T) {
	_, h := newHistFS(t)

	id, err := history.RecordBiased(h, "dec-oltp-store", "use postgres, the team owns ops", "sess-acp-abc123",
		history.BlastRadiusEstimate{
			DecisionsTouched:    1,
			FeaturesRewritten:   4,
			StrategiesRewritten: 2,
			ApproachesDrifted:   11,
		})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	events, err := h.Events()
	require.NoError(t, err)
	require.Len(t, events, 1)

	got := events[0]
	assert.Equal(t, id, got.ID)
	assert.Equal(t, history.EventKindBiased, got.Kind)
	assert.Equal(t, "dec-oltp-store", got.TargetID)
	require.NotNil(t, got.Biased)
	assert.Equal(t, "use postgres, the team owns ops", got.Biased.BiasText)
	assert.Equal(t, "sess-acp-abc123", got.Biased.ACPSessionID)
	assert.Equal(t, 4, got.Biased.BlastRadius.FeaturesRewritten)
	assert.Equal(t, 11, got.Biased.BlastRadius.ApproachesDrifted)
}

// TestApproachDriftedEventRoundTrip — record an approach_drifted
// event with caused_by + upstream_event_id and read it back.
func TestApproachDriftedEventRoundTrip(t *testing.T) {
	_, h := newHistFS(t)

	id, err := history.RecordApproachDrifted(h,
		"app-realtime-loader",
		"20260527T000000-001-dec-oltp-store-biased",
		"20260527T000001-001-feat-realtime-sync-refined",
	)
	require.NoError(t, err)

	events, err := h.Events()
	require.NoError(t, err)
	require.Len(t, events, 1)

	got := events[0]
	assert.Equal(t, id, got.ID)
	assert.Equal(t, history.EventKindApproachDrifted, got.Kind)
	assert.Equal(t, "app-realtime-loader", got.TargetID)
	assert.Equal(t, "20260527T000000-001-dec-oltp-store-biased", got.CausedBy)
	require.NotNil(t, got.ApproachDrifted)
	assert.Equal(t, "20260527T000001-001-feat-realtime-sync-refined", got.ApproachDrifted.UpstreamEventID)
}

// TestCausedByFieldOnExistingEvents — the new CausedBy field on
// the shared Event struct serializes correctly on existing event
// kinds too (spec_revised used by the cascade for the actual
// rewrites). Pre-DJ-138 events leave CausedBy empty; the
// omitempty tag means they round-trip without surprise.
func TestCausedByFieldOnExistingEvents(t *testing.T) {
	_, h := newHistFS(t)

	rootID := "20260527T000000-001-dec-oltp-store-biased"

	// Record a refine event with CausedBy set — emulates what the
	// cascade does when it rewrites a dependent feature.
	require.NoError(t, h.Record(history.Event{
		ID:        "20260527T000001-001-feat-realtime-sync-refined",
		Timestamp: time.Now(),
		Kind:      history.EventKindRefined,
		TargetID:  "feat-realtime-sync",
		CausedBy:  rootID,
		Rationale: "cascade rewrite",
	}))

	events, err := h.Events()
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, rootID, events[0].CausedBy)
}

// TestEventsCausedBy_ReturnsCascadeChildren — walk a cascade tree:
// one spec_biased root + two children (a refine and a drift).
// EventsCausedBy returns exactly the children, ignoring the root.
func TestEventsCausedBy_ReturnsCascadeChildren(t *testing.T) {
	_, h := newHistFS(t)

	rootID, err := history.RecordBiased(h, "dec-oltp-store", "bias text", "sess-1",
		history.BlastRadiusEstimate{DecisionsTouched: 1, FeaturesRewritten: 1, ApproachesDrifted: 1})
	require.NoError(t, err)

	time.Sleep(2 * time.Millisecond)
	require.NoError(t, h.Record(history.Event{
		ID:        "20260527T000001-001-feat-realtime-sync-refined",
		Timestamp: time.Now(),
		Kind:      history.EventKindRefined,
		TargetID:  "feat-realtime-sync",
		CausedBy:  rootID,
	}))

	time.Sleep(2 * time.Millisecond)
	_, err = history.RecordApproachDrifted(h, "app-realtime-loader", rootID, "20260527T000001-001-feat-realtime-sync-refined")
	require.NoError(t, err)

	children, err := h.EventsCausedBy(rootID)
	require.NoError(t, err)
	assert.Len(t, children, 2, "two children (refine + drift) should link back to the root")
	for _, c := range children {
		assert.Equal(t, rootID, c.CausedBy)
	}
}

// TestRecentBiased_ReturnsNewestFirst — record three spec_biased
// events at different timestamps and assert RecentBiased returns
// them newest-first, honoring the limit.
func TestRecentBiased_ReturnsNewestFirst(t *testing.T) {
	_, h := newHistFS(t)

	_, err := history.RecordBiased(h, "dec-a", "first", "sess-1",
		history.BlastRadiusEstimate{DecisionsTouched: 1})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = history.RecordBiased(h, "dec-b", "second", "sess-2",
		history.BlastRadiusEstimate{DecisionsTouched: 1})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = history.RecordBiased(h, "dec-c", "third", "sess-3",
		history.BlastRadiusEstimate{DecisionsTouched: 1})
	require.NoError(t, err)

	// All three.
	all, err := h.RecentBiased(10)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, "dec-c", all[0].TargetID, "newest first")
	assert.Equal(t, "dec-b", all[1].TargetID)
	assert.Equal(t, "dec-a", all[2].TargetID)

	// Limit to 2.
	top := all
	top, err = h.RecentBiased(2)
	require.NoError(t, err)
	require.Len(t, top, 2)
	assert.Equal(t, "dec-c", top[0].TargetID)
	assert.Equal(t, "dec-b", top[1].TargetID)
}

// TestRecentBiased_AbsentWhenNoBiases — graceful no-op return on
// an empty history log.
func TestRecentBiased_AbsentWhenNoBiases(t *testing.T) {
	_, h := newHistFS(t)
	out, err := h.RecentBiased(5)
	require.NoError(t, err)
	assert.Empty(t, out)
}
