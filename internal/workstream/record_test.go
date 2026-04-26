package workstream_test

import (
	"errors"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/workstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const planID = "plan-auth-2026-04-22"

func fixturePlan() spec.MasterPlan {
	return spec.MasterPlan{
		ID:          planID,
		Version:     1,
		CreatedAt:   time.Now(),
		ProjectRoot: "/tmp/test-repo",
		Prompt:      "Add OAuth login",
		InterfaceContracts: []spec.InterfaceContract{
			{ID: "auth-types", Description: "Shared Session struct", ProducedBy: "ws-contracts", ConsumedBy: []string{"ws-auth"}},
		},
		GlobalAssertions: []spec.Assertion{
			{Kind: spec.AssertionKindTestPass, Target: "./..."},
		},
	}
}

func fixtureWorkstream() workstream.ActiveWorkstream {
	return workstream.ActiveWorkstream{
		WorkstreamID:   "ws-auth",
		PlanID:         planID,
		ApproachIDs:    []string{"app-oauth", "app-session"},
		AgentSessionID: "claude-session-abc",
		Plan: spec.Workstream{
			ID:             "ws-auth",
			StrategyDomain: "auth",
			AgentID:        "claude-code",
			Steps: []spec.PlanStep{
				{ID: "step-1", Order: 1, ApproachID: "app-oauth", Description: "OAuth client"},
				{ID: "step-2", Order: 2, ApproachID: "app-session", Description: "Session store"},
			},
		},
		StepStatus: []workstream.StepProgress{
			{StepID: "step-1", Status: workstream.StepComplete},
		},
	}
}

func newStore(t *testing.T) (specio.FS, *workstream.FileStore) {
	t.Helper()
	fs := specio.NewMemFS()
	return fs, workstream.NewFileStore(fs, ".locutus/workstreams", planID)
}

func TestSaveAndLoadWorkstreamRoundTrip(t *testing.T) {
	_, store := newStore(t)

	rec := fixtureWorkstream()
	require.NoError(t, store.Save(rec))

	got, err := store.Load("ws-auth")
	require.NoError(t, err)

	assert.Equal(t, rec.WorkstreamID, got.WorkstreamID)
	assert.Equal(t, rec.PlanID, got.PlanID)
	assert.Equal(t, rec.ApproachIDs, got.ApproachIDs)
	assert.Equal(t, rec.AgentSessionID, got.AgentSessionID)
	assert.Equal(t, rec.Plan.ID, got.Plan.ID)
	assert.Len(t, got.Plan.Steps, 2)
	assert.Len(t, got.StepStatus, 1)
	assert.Equal(t, workstream.StepComplete, got.StepStatus[0].Status)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestSavePlanAndLoadPlanRoundTrip(t *testing.T) {
	_, store := newStore(t)

	plan := fixturePlan()
	require.NoError(t, store.SavePlan(plan))

	rec, err := store.LoadPlan()
	require.NoError(t, err)
	assert.Equal(t, plan.ID, rec.Plan.ID)
	assert.Equal(t, plan.Prompt, rec.Plan.Prompt)
	require.Len(t, rec.Plan.InterfaceContracts, 1)
	assert.Equal(t, "auth-types", rec.Plan.InterfaceContracts[0].ID)
	require.Len(t, rec.Plan.GlobalAssertions, 1)
	assert.False(t, rec.CreatedAt.IsZero())
	assert.False(t, rec.UpdatedAt.IsZero())
}

func TestSavePlanPreservesCreatedAtAcrossUpdates(t *testing.T) {
	_, store := newStore(t)

	require.NoError(t, store.SavePlan(fixturePlan()))
	first, err := store.LoadPlan()
	require.NoError(t, err)

	time.Sleep(5 * time.Millisecond)
	// Save a modified plan (same ID) and verify CreatedAt survives.
	modified := first.Plan
	modified.Summary = "updated mid-flight"
	require.NoError(t, store.SavePlan(modified))

	second, err := store.LoadPlan()
	require.NoError(t, err)
	assert.Equal(t, first.CreatedAt, second.CreatedAt, "CreatedAt must survive re-save")
	assert.True(t, second.UpdatedAt.After(first.UpdatedAt), "UpdatedAt must advance")
}

func TestSaveRequiresWorkstreamID(t *testing.T) {
	_, store := newStore(t)
	err := store.Save(workstream.ActiveWorkstream{})
	assert.Error(t, err)
}

func TestSavePlanRequiresID(t *testing.T) {
	_, store := newStore(t)
	err := store.SavePlan(spec.MasterPlan{})
	assert.Error(t, err)
}

func TestLoadMissingReturnsErrNotFound(t *testing.T) {
	_, store := newStore(t)

	_, err := store.Load("nonexistent")
	assert.ErrorIs(t, err, workstream.ErrNotFound)

	_, err = store.LoadPlan()
	assert.ErrorIs(t, err, workstream.ErrNotFound)
}

// failingReadFS forces ReadFile to return a caller-specified error so we can
// drive Load/LoadPlan down their non-NotExist branches. Per DJ-073 the
// classifier's resume-decide branch needs to distinguish "no plan on disk"
// (treat as nothing in flight) from "plan exists but unreadable" (surface
// the disk problem) — collapsing both to ErrNotFound silently invalidates
// recoverable plans on transient IO failure. Same bug class as the
// state.Load fix in commit 74b3104.
type failingReadFS struct {
	specio.FS
	err error
}

func (f *failingReadFS) ReadFile(string) ([]byte, error) { return nil, f.err }

func TestLoadDoesNotMaskIOErrorAsNotFound(t *testing.T) {
	fsys := &failingReadFS{
		FS:  specio.NewMemFS(),
		err: errors.New("permission denied"),
	}
	store := workstream.NewFileStore(fsys, ".locutus/workstreams", planID)

	_, err := store.Load("ws-a")
	require.Error(t, err)
	assert.False(t, errors.Is(err, workstream.ErrNotFound),
		"non-NotExist errors must propagate, not collapse to ErrNotFound")
	assert.Contains(t, err.Error(), "permission denied")
}

func TestLoadPlanDoesNotMaskIOErrorAsNotFound(t *testing.T) {
	fsys := &failingReadFS{
		FS:  specio.NewMemFS(),
		err: errors.New("disk read failure"),
	}
	store := workstream.NewFileStore(fsys, ".locutus/workstreams", planID)

	_, err := store.LoadPlan()
	require.Error(t, err)
	assert.False(t, errors.Is(err, workstream.ErrNotFound),
		"non-NotExist errors must propagate, not collapse to ErrNotFound")
	assert.Contains(t, err.Error(), "disk read failure")
}

func TestLoadCorruptYAMLPropagatesUnmarshalError(t *testing.T) {
	mem := specio.NewMemFS()
	require.NoError(t, mem.MkdirAll(".locutus/workstreams/"+planID, 0o755))
	// Truncated YAML — simulates a SIGKILL mid-Save.
	require.NoError(t, mem.WriteFile(
		".locutus/workstreams/"+planID+"/ws-a.yaml",
		[]byte("workstream_id: ws-a\nplan_id: ["),
		0o644,
	))
	store := workstream.NewFileStore(mem, ".locutus/workstreams", planID)

	_, err := store.Load("ws-a")
	require.Error(t, err)
	assert.False(t, errors.Is(err, workstream.ErrNotFound))
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestWalkSortedByID(t *testing.T) {
	_, store := newStore(t)

	require.NoError(t, store.Save(workstream.ActiveWorkstream{WorkstreamID: "ws-z"}))
	require.NoError(t, store.Save(workstream.ActiveWorkstream{WorkstreamID: "ws-a"}))
	require.NoError(t, store.Save(workstream.ActiveWorkstream{WorkstreamID: "ws-m"}))

	all, err := store.Walk()
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, "ws-a", all[0].WorkstreamID)
	assert.Equal(t, "ws-m", all[1].WorkstreamID)
	assert.Equal(t, "ws-z", all[2].WorkstreamID)
}

func TestWalkSkipsPlanYAML(t *testing.T) {
	_, store := newStore(t)

	require.NoError(t, store.SavePlan(fixturePlan()))
	require.NoError(t, store.Save(fixtureWorkstream()))

	all, err := store.Walk()
	require.NoError(t, err)
	require.Len(t, all, 1, "plan.yaml must not appear in Walk output")
	assert.Equal(t, "ws-auth", all[0].WorkstreamID)
}

func TestWalkEmptyStore(t *testing.T) {
	_, store := newStore(t)

	all, err := store.Walk()
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestDeleteWorkstreamRemoves(t *testing.T) {
	_, store := newStore(t)

	require.NoError(t, store.Save(workstream.ActiveWorkstream{WorkstreamID: "ws-gone"}))
	require.NoError(t, store.Delete("ws-gone"))

	_, err := store.Load("ws-gone")
	assert.ErrorIs(t, err, workstream.ErrNotFound)
}

func TestDeleteMissingIsNoOp(t *testing.T) {
	_, store := newStore(t)
	// Idempotent cleanup.
	assert.NoError(t, store.Delete("never-existed"))
}

// TestDeletePlanClearsEverything covers terminal cleanup: when every
// Approach reaches live, the whole plan directory should be wiped so a
// fresh clone (or a later adopt run) sees nothing in flight.
func TestDeletePlanClearsEverything(t *testing.T) {
	fs, store := newStore(t)

	require.NoError(t, store.SavePlan(fixturePlan()))
	require.NoError(t, store.Save(fixtureWorkstream()))
	require.NoError(t, store.Save(workstream.ActiveWorkstream{WorkstreamID: "ws-other"}))

	require.NoError(t, store.DeletePlan())

	_, err := store.LoadPlan()
	assert.ErrorIs(t, err, workstream.ErrNotFound)
	_, err = store.Load("ws-auth")
	assert.ErrorIs(t, err, workstream.ErrNotFound)
	_, err = store.Load("ws-other")
	assert.ErrorIs(t, err, workstream.ErrNotFound)

	// ListActivePlans must not surface the deleted plan.
	plans, err := workstream.ListActivePlans(fs, ".locutus/workstreams")
	require.NoError(t, err)
	assert.NotContains(t, plans, planID)
}

func TestDeletePlanIsIdempotent(t *testing.T) {
	_, store := newStore(t)
	assert.NoError(t, store.DeletePlan())
	assert.NoError(t, store.DeletePlan())
}

// TestListActivePlans is the resume-path entry point — the dispatcher
// calls this first to discover which plans are in-flight.
func TestListActivePlans(t *testing.T) {
	fs := specio.NewMemFS()

	for _, id := range []string{"plan-b", "plan-a", "plan-c"} {
		store := workstream.NewFileStore(fs, ".locutus/workstreams", id)
		require.NoError(t, store.SavePlan(spec.MasterPlan{ID: id}))
	}
	// Put a stray file at the top level that must be ignored.
	require.NoError(t, fs.WriteFile(".locutus/workstreams/stray.yaml", []byte("noise"), 0o644))
	// And a directory without plan.yaml, which must be ignored.
	require.NoError(t, fs.MkdirAll(".locutus/workstreams/orphan-dir", 0o755))

	plans, err := workstream.ListActivePlans(fs, ".locutus/workstreams")
	require.NoError(t, err)
	assert.Equal(t, []string{"plan-a", "plan-b", "plan-c"}, plans)
}

func TestListActivePlansMissingBaseDir(t *testing.T) {
	fs := specio.NewMemFS()
	plans, err := workstream.ListActivePlans(fs, ".locutus/workstreams")
	require.NoError(t, err)
	assert.Empty(t, plans)
}

func TestRecordProgressUpdatesOrInserts(t *testing.T) {
	rec := fixtureWorkstream()
	startingLen := len(rec.StepStatus)

	rec.RecordProgress(workstream.StepProgress{StepID: "step-2", Status: workstream.StepInProgress})
	assert.Len(t, rec.StepStatus, startingLen+1)

	rec.RecordProgress(workstream.StepProgress{StepID: "step-2", Status: workstream.StepComplete})
	assert.Len(t, rec.StepStatus, startingLen+1)

	assert.Equal(t, workstream.StepComplete, rec.StepByID("step-2").Status)
}

func TestStepByIDDefaultsToPending(t *testing.T) {
	rec := workstream.ActiveWorkstream{}
	got := rec.StepByID("step-never-seen")
	assert.Equal(t, "step-never-seen", got.StepID)
	assert.Equal(t, workstream.StepPending, got.Status)
}

func TestUpdatedAtAdvancesOnResave(t *testing.T) {
	_, store := newStore(t)

	require.NoError(t, store.Save(fixtureWorkstream()))
	first, err := store.Load("ws-auth")
	require.NoError(t, err)

	time.Sleep(5 * time.Millisecond)
	require.NoError(t, store.Save(first))
	second, err := store.Load("ws-auth")
	require.NoError(t, err)

	assert.True(t, second.UpdatedAt.After(first.UpdatedAt))
}
