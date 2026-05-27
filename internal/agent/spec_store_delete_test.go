// DJ-139 phase 3 — SpecStore deletion methods for goal-* and agoal-*.
//
// Pre-DJ-139 the spec model was append-only; deletion arrives with
// the goal layer because GOALS.md edits can drop scope claims, and
// the persisted interpretation has to follow. DeleteGoal and
// DeleteAntiGoal remove the entry from the in-memory map AND remove
// the JSON file from disk; the historian (separately) preserves the
// audit trail.

package agent

import (
	"testing"

	"github.com/glorious-beard/locutus/internal/spec"
	"github.com/glorious-beard/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecStoreDeleteGoalRemovesFromStoreAndDisk seeds a goal,
// commits it through the persist path, then calls DeleteGoal and
// confirms it disappears from both the in-memory manifest and the
// on-disk JSON file.
func TestSpecStoreDeleteGoalRemovesFromStoreAndDisk(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	g := spec.Goal{
		ID:           "goal-strategic-planning-tool",
		Title:        "Strategic planning tool",
		Body:         "Locutus is a strategic planning tool for solo founders.",
		SourceClause: "Locutus is a strategic planning tool",
	}
	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindGoal, g.ID, g, OriginProposed))
	require.NoError(t, store.Commit())

	// Confirm the seed worked: on disk and in the manifest.
	_, err = fsys.ReadFile(".borg/spec/goals/goal-strategic-planning-tool.json")
	require.NoError(t, err, "goal file present after seed commit")
	require.Len(t, store.ListManifest().Goals, 1)

	require.NoError(t, store.DeleteGoal(g.ID))

	// In-memory: gone.
	res := store.GetSpec([]string{g.ID})
	assert.Equal(t, SpecGetMissing, res.Results[g.ID].Status, "deleted goal no longer in store")
	assert.Empty(t, store.ListManifest().Goals, "manifest no longer surfaces the deleted goal")

	// On disk: gone.
	_, err = fsys.ReadFile(".borg/spec/goals/goal-strategic-planning-tool.json")
	assert.Error(t, err, "goal file removed from disk")

	// Fresh store re-loads cleanly without the deleted goal.
	store2, err := NewSpecStore(fsys)
	require.NoError(t, err)
	assert.Empty(t, store2.ListManifest().Goals, "fresh store has no record of the deleted goal")
}

// TestSpecStoreDeleteAntiGoalRemovesFromStoreAndDisk — parallel to
// the goal test for the agoal- prefix.
func TestSpecStoreDeleteAntiGoalRemovesFromStoreAndDisk(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	ag := spec.AntiGoal{
		ID:           "agoal-fundraising",
		Title:        "Fundraising tracking",
		Body:         "Locutus does not track fundraising rounds.",
		SourceClause: "Fundraising tracking is out of scope",
		CededTo:      []string{"Carta", "AngelList"},
	}
	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindAntiGoal, ag.ID, ag, OriginProposed))
	require.NoError(t, store.Commit())

	_, err = fsys.ReadFile(".borg/spec/antigoals/agoal-fundraising.json")
	require.NoError(t, err)

	require.NoError(t, store.DeleteAntiGoal(ag.ID))

	res := store.GetSpec([]string{ag.ID})
	assert.Equal(t, SpecGetMissing, res.Results[ag.ID].Status)
	assert.Empty(t, store.ListManifest().AntiGoals)

	_, err = fsys.ReadFile(".borg/spec/antigoals/agoal-fundraising.json")
	assert.Error(t, err)
}

// TestSpecStoreDeleteGoalRejectsUnknownID — calling DeleteGoal on an
// id that isn't in the store returns an error rather than silently
// no-op'ing. The plan-mandated semantics: deletes are auditable
// (each lands a history event); a delete that didn't happen because
// the id wasn't found should surface to the caller, not silently
// vanish.
func TestSpecStoreDeleteGoalRejectsUnknownID(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	err = store.DeleteGoal("goal-never-existed")
	assert.Error(t, err, "DeleteGoal on unknown id returns error")
}

// TestSpecStoreDeleteAntiGoalRejectsUnknownID — same for agoal-.
func TestSpecStoreDeleteAntiGoalRejectsUnknownID(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	err = store.DeleteAntiGoal("agoal-never-existed")
	assert.Error(t, err, "DeleteAntiGoal on unknown id returns error")
}

// TestSpecStoreDeleteGoalRejectsWrongPrefix — DeleteGoal only handles
// goal- ids. An agoal- (or any other prefix) is a caller bug.
func TestSpecStoreDeleteGoalRejectsWrongPrefix(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	err = store.DeleteGoal("agoal-fundraising")
	assert.Error(t, err, "DeleteGoal on agoal- id is rejected")

	err = store.DeleteAntiGoal("goal-strategic-planning-tool")
	assert.Error(t, err, "DeleteAntiGoal on goal- id is rejected")
}

// TestDeleteGoalRefusesDuringOpenTransaction confirms the consistency
// invariant: DeleteGoal cannot run between Begin and Commit/Rollback
// because rollback would restore the in-memory entry while the disk
// file would remain gone (delete writes through to disk immediately).
// Refusing the operation at the boundary turns a silent invariant into
// a loud error.
func TestDeleteGoalRefusesDuringOpenTransaction(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	// Seed a goal so the delete would otherwise succeed.
	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindGoal, "goal-test", spec.Goal{
		ID:           "goal-test",
		Title:        "Test",
		Body:         "body",
		SourceClause: "clause",
	}, OriginSettled))
	require.NoError(t, store.Commit())

	// Open a transaction, then try to delete.
	require.NoError(t, store.Begin())
	err = store.DeleteGoal("goal-test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction")
}

// TestDeleteAntiGoalRefusesDuringOpenTransaction — parallel to the
// goal case for the agoal- prefix.
func TestDeleteAntiGoalRefusesDuringOpenTransaction(t *testing.T) {
	fsys := specio.NewMemFS()
	store, err := NewSpecStore(fsys)
	require.NoError(t, err)

	// Seed an antigoal so the delete would otherwise succeed.
	require.NoError(t, store.Begin())
	require.NoError(t, store.Put(KindAntiGoal, "agoal-test", spec.AntiGoal{
		ID:           "agoal-test",
		Title:        "Test",
		Body:         "body",
		SourceClause: "clause",
	}, OriginSettled))
	require.NoError(t, store.Commit())

	// Open a transaction, then try to delete.
	require.NoError(t, store.Begin())
	err = store.DeleteAntiGoal("agoal-test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction")
}
