// DJ-147 — per-session overlay on the SpecStore. The overlay captures
// would-be mutations (proposes / revises / deletes) and serves them on
// read-after-write so a dry-run workflow runs faithfully end-to-end
// against the would-be graph.
package agent

import (
	"testing"
	"time"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionOverlay_PutAndLookup(t *testing.T) {
	o := newSessionOverlay()
	d := spec.Decision{ID: "dec-foo", Title: "Foo"}
	o.put("spec_propose_decision", KindDecision, "dec-foo", d)

	entry, ok := o.lookup(KindDecision, "dec-foo")
	require.True(t, ok)
	require.NotNil(t, entry)
	got, ok := entry.Body.(spec.Decision)
	require.True(t, ok)
	assert.Equal(t, "dec-foo", got.ID)

	assert.Len(t, o.capturedList(), 1)
	cap := o.capturedList()[0]
	assert.Equal(t, "spec_propose_decision", cap.Tool)
	assert.Equal(t, KindDecision, cap.Kind)
	assert.Equal(t, "dec-foo", cap.ID)
}

func TestSessionOverlay_DeleteMasksLookup(t *testing.T) {
	o := newSessionOverlay()
	o.delete("spec_delete_goal", KindGoal, "goal-x")

	_, ok := o.lookup(KindGoal, "goal-x")
	assert.False(t, ok, "lookup must report missing for deleted entries")

	assert.True(t, o.isDeleted(KindGoal, "goal-x"))
	assert.False(t, o.isDeleted(KindGoal, "goal-y"))

	assert.Len(t, o.capturedList(), 1)
	assert.Equal(t, "spec_delete_goal", o.capturedList()[0].Tool)
}

func TestSessionOverlay_RevisePreservesOrderedCapture(t *testing.T) {
	o := newSessionOverlay()
	o.put("spec_propose_feature", KindFeature, "feat-a", spec.Feature{ID: "feat-a"})
	o.put("spec_revise_feature", KindFeature, "feat-a", spec.Feature{ID: "feat-a", Title: "A revised"})

	entry, ok := o.lookup(KindFeature, "feat-a")
	require.True(t, ok)
	got := entry.Body.(spec.Feature)
	assert.Equal(t, "A revised", got.Title, "later writes overwrite earlier ones in the overlay")

	caps := o.capturedList()
	require.Len(t, caps, 2)
	assert.Equal(t, "spec_propose_feature", caps[0].Tool)
	assert.Equal(t, "spec_revise_feature", caps[1].Tool)
	assert.True(t, caps[0].Timestamp.Before(caps[1].Timestamp) || caps[0].Timestamp.Equal(caps[1].Timestamp))
}

func TestSessionOverlay_ConcurrentPutSafe(t *testing.T) {
	o := newSessionOverlay()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			id := "dec-" + string(rune('a'+i))
			o.put("spec_propose_decision", KindDecision, id, spec.Decision{ID: id})
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	// All 8 must be present.
	for i := 0; i < 8; i++ {
		id := "dec-" + string(rune('a'+i))
		_, ok := o.lookup(KindDecision, id)
		assert.True(t, ok, "missing %s after concurrent writes", id)
	}
	assert.Len(t, o.capturedList(), 8)
}

// Avoid the import-vs-used-time-noise: use time directly so the test file owns the import.
var _ = time.Now
