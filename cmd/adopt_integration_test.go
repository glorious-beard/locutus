package cmd

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent"
	"github.com/chetan/locutus/internal/dispatch"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/chetan/locutus/internal/state"
	"github.com/chetan/locutus/internal/workstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupIntegrationFixture writes a minimal project to t.TempDir so the
// assertion runner (which stats real files) has something to hit. Returns
// the repo dir + an OSFS rooted at it.
func setupIntegrationFixture(t *testing.T, assertions []spec.Assertion) (string, specio.FS, spec.Approach) {
	t.Helper()
	repoDir := t.TempDir()
	fs := specio.NewOSFS(repoDir)

	for _, d := range []string{".borg/spec/features", ".borg/spec/decisions", ".borg/spec/approaches", ".borg/history", ".borg/state"} {
		require.NoError(t, fs.MkdirAll(d, 0o755))
	}

	feat := spec.Feature{
		ID: "feat-auth", Title: "Authentication", Status: spec.FeatureStatusActive,
		Approaches: []string{"app-oauth"},
	}
	app := spec.Approach{
		ID: "app-oauth", Title: "OAuth", ParentID: "feat-auth",
		Body:          "Implement OAuth login.",
		ArtifactPaths: []string{"auth.go"},
		Assertions:    assertions,
		CreatedAt:     time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, specio.SavePair(fs, ".borg/spec/features/feat-auth", feat, "body"))
	require.NoError(t, specio.SaveMarkdown(fs, ".borg/spec/approaches/app-oauth.md", app, app.Body))
	// Pre-write the artifact so file_exists assertions pass.
	require.NoError(t, fs.WriteFile("auth.go", []byte("package auth\n"), 0o644))
	return repoDir, fs, app
}

// fakePlanOneWorkstream returns a PlanFunc that emits a MasterPlan with
// one workstream whose steps cover every Approach the caller references
// in the prompt. Deterministic — no LLM required.
func fakePlanOneWorkstream(planID, wsID, approachID string) PlanFunc {
	return func(ctx context.Context, req agent.PlanRequest) (*spec.MasterPlan, error) {
		return &spec.MasterPlan{
			ID:        planID,
			Version:   1,
			CreatedAt: time.Now(),
			Workstreams: []spec.Workstream{
				{
					ID:      wsID,
					AgentID: "claude-code",
					Steps: []spec.PlanStep{
						{ID: "step-1", Order: 1, ApproachID: approachID, Description: "Implement"},
					},
				},
			},
		}, nil
	}
}

// fakeDispatchSuccess returns a DispatchFunc that reports every workstream
// as having completed all its steps successfully.
func fakeDispatchSuccess() DispatchFunc {
	return func(ctx context.Context, plan *spec.MasterPlan, repoDir string, _ map[string]*dispatch.ResumePoint) ([]*dispatch.WorkstreamResult, error) {
		out := make([]*dispatch.WorkstreamResult, 0, len(plan.Workstreams))
		for _, ws := range plan.Workstreams {
			stepResults := make([]*dispatch.StepOutcome, len(ws.Steps))
			for i := range ws.Steps {
				stepResults[i] = &dispatch.StepOutcome{Success: true, Attempts: 1}
			}
			out = append(out, &dispatch.WorkstreamResult{
				WorkstreamID: ws.ID,
				Success:      true,
				StepResults:  stepResults,
			})
		}
		return out, nil
	}
}

// fakeDispatchFailure returns a DispatchFunc that reports every workstream
// as having dispatch-level failure (agent errored, couldn't complete).
func fakeDispatchFailure(message string) DispatchFunc {
	return func(ctx context.Context, plan *spec.MasterPlan, repoDir string, _ map[string]*dispatch.ResumePoint) ([]*dispatch.WorkstreamResult, error) {
		out := make([]*dispatch.WorkstreamResult, 0, len(plan.Workstreams))
		for _, ws := range plan.Workstreams {
			out = append(out, &dispatch.WorkstreamResult{
				WorkstreamID: ws.ID,
				Success:      false,
				Err:          assertError(message),
			})
		}
		return out, nil
	}
}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }
func assertError(msg string) error {
	return &fakeErr{msg: msg}
}

// emptyPreflightLLM returns a MockLLM scripted to respond with empty
// resolutions for every pre-flight call (no ambiguities found).
func emptyPreflightLLM(n int) *agent.MockLLM {
	resps := make([]agent.MockResponse, n)
	for i := range resps {
		resps[i] = agent.MockResponse{Response: &agent.GenerateResponse{Content: `{"resolutions": []}`}}
	}
	return agent.NewMockLLM(resps...)
}

func TestRunAdoptFullDispatchHappyPath(t *testing.T) {
	repoDir, fs, _ := setupIntegrationFixture(t, []spec.Assertion{
		{Kind: spec.AssertionKindFileExists, Target: "auth.go"},
	})

	cfg := AdoptConfig{
		FS:       fs,
		LLM:      emptyPreflightLLM(2),
		RepoDir:  repoDir,
		Plan:     fakePlanOneWorkstream("plan-auth-v1", "ws-auth", "app-oauth"),
		Dispatch: fakeDispatchSuccess(),
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, "plan-auth-v1", report.PlanID)
	require.Len(t, report.DispatchedWorkstreams, 1)
	outcome := report.DispatchedWorkstreams[0]
	assert.True(t, outcome.Dispatched)
	assert.Equal(t, []string{"app-oauth"}, outcome.LiveApproaches)
	assert.Empty(t, outcome.FailedApproaches)

	// Archived plan (all Approaches live) → workstream dir should be gone.
	planIDs, err := workstream.ListActivePlans(fs, ".locutus/workstreams")
	require.NoError(t, err)
	assert.Empty(t, planIDs)
	assert.Contains(t, report.Archived, "plan-auth-v1")

	// State store should say live.
	store := state.NewFileStateStore(fs, ".borg/state")
	entry, err := store.Load("app-oauth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusLive, entry.Status)
	assert.NotEmpty(t, entry.Artifacts, "artifact hashes must be refreshed post-dispatch")
	require.Len(t, entry.AssertionResults, 1)
	assert.True(t, entry.AssertionResults[0].Passed)
}

func TestRunAdoptAssertionFailurePreservesPlanAndFlipsFailed(t *testing.T) {
	// Assertion expects a file that doesn't exist → fail.
	repoDir, fs, _ := setupIntegrationFixture(t, []spec.Assertion{
		{Kind: spec.AssertionKindFileExists, Target: "missing.go"},
	})

	cfg := AdoptConfig{
		FS:       fs,
		LLM:      emptyPreflightLLM(2),
		RepoDir:  repoDir,
		Plan:     fakePlanOneWorkstream("plan-auth-v2", "ws-auth", "app-oauth"),
		Dispatch: fakeDispatchSuccess(),
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)

	outcome := report.DispatchedWorkstreams[0]
	assert.True(t, outcome.Dispatched, "dispatcher succeeded")
	assert.Equal(t, []string{"app-oauth"}, outcome.FailedApproaches, "assertion failure flips Approach to failed")
	assert.Empty(t, outcome.LiveApproaches)

	// Plan directory should NOT be archived — at least one Approach failed.
	_, err = fs.Stat(filepath.Join(".locutus/workstreams", "plan-auth-v2", "plan.yaml"))
	assert.NoError(t, err, "incomplete plan should remain on disk for the next adopt invocation")
	assert.Empty(t, report.Archived)

	store := state.NewFileStateStore(fs, ".borg/state")
	entry, err := store.Load("app-oauth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusFailed, entry.Status)
}

func TestRunAdoptDispatchFailureMarksAllCoveredFailed(t *testing.T) {
	repoDir, fs, _ := setupIntegrationFixture(t, nil)

	cfg := AdoptConfig{
		FS:       fs,
		LLM:      emptyPreflightLLM(2),
		RepoDir:  repoDir,
		Plan:     fakePlanOneWorkstream("plan-auth-v3", "ws-auth", "app-oauth"),
		Dispatch: fakeDispatchFailure("agent errored"),
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)

	outcome := report.DispatchedWorkstreams[0]
	assert.False(t, outcome.Dispatched)
	assert.Contains(t, outcome.Error, "agent errored")
	assert.Equal(t, []string{"app-oauth"}, outcome.FailedApproaches)

	store := state.NewFileStateStore(fs, ".borg/state")
	entry, err := store.Load("app-oauth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusFailed, entry.Status)
}

func TestRunAdoptDiscardInFlightForceInvalidates(t *testing.T) {
	// Pre-DJ-074 behavior: every leftover plan was invalidated. Now
	// auto-resume is the default; --discard-in-flight (DiscardInFlight=true)
	// forces the prior always-invalidate semantics for callers who know
	// the plan is stale.
	repoDir, fs, _ := setupIntegrationFixture(t, nil)

	wsStore := workstream.NewFileStore(fs, ".locutus/workstreams", "plan-old")
	require.NoError(t, wsStore.SavePlan(spec.MasterPlan{ID: "plan-old"}))
	require.NoError(t, wsStore.Save(workstream.ActiveWorkstream{WorkstreamID: "ws-old", PlanID: "plan-old"}))

	cfg := AdoptConfig{
		FS:              fs,
		LLM:             emptyPreflightLLM(2),
		RepoDir:         repoDir,
		Plan:            fakePlanOneWorkstream("plan-new", "ws-new", "app-oauth"),
		Dispatch:        fakeDispatchSuccess(),
		DiscardInFlight: true,
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)

	assert.Contains(t, report.ResumedInvalidated, "plan-old", "discard-in-flight invalidates regardless of drift")

	activeIDs, err := workstream.ListActivePlans(fs, ".locutus/workstreams")
	require.NoError(t, err)
	assert.NotContains(t, activeIDs, "plan-old")
	assert.NotContains(t, activeIDs, "plan-new")
	assert.Contains(t, report.Archived, "plan-new")
}

func TestRunAdoptArchivesAlreadyLivePlan(t *testing.T) {
	// A leftover plan whose covered Approaches are all StatusLive (and
	// SpecHash matches) should be archived without reinvoking the
	// planner — the work's already done.
	repoDir, fs, _ := setupIntegrationFixture(t, nil)

	// Look up app-oauth from the fixture's spec graph and stamp its
	// state as live with the current SpecHash so classifyPlanAction
	// returns "all live → archive".
	graph, err := loadSpecGraph(fs)
	require.NoError(t, err)
	approach := graph.Approach("app-oauth")
	require.NotNil(t, approach)
	specHash := spec.ComputeSpecHash(*approach)

	store := state.NewFileStateStore(fs, ".borg/state")
	require.NoError(t, store.Save(state.ReconciliationState{
		ApproachID:     "app-oauth",
		Status:         state.StatusLive,
		SpecHash:       specHash,
		LastReconciled: time.Now(),
	}))

	wsStore := workstream.NewFileStore(fs, ".locutus/workstreams", "plan-done")
	require.NoError(t, wsStore.SavePlan(spec.MasterPlan{ID: "plan-done"}))
	require.NoError(t, wsStore.Save(workstream.ActiveWorkstream{
		WorkstreamID: "ws-done",
		PlanID:       "plan-done",
		ApproachIDs:  []string{"app-oauth"},
	}))

	cfg := AdoptConfig{
		FS:       fs,
		LLM:      emptyPreflightLLM(2),
		RepoDir:  repoDir,
		Plan:     fakePlanOneWorkstream("plan-new", "ws-new", "app-oauth"),
		Dispatch: fakeDispatchSuccess(),
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)

	assert.Contains(t, report.Archived, "plan-done", "all-live plan archived in classifier")
	assert.NotContains(t, report.ResumedInvalidated, "plan-done")
}

func TestRunAdoptInvalidatesDriftedPlan(t *testing.T) {
	// A leftover plan whose covered Approach has a SpecHash mismatch
	// (drift) should be invalidated, not resumed.
	repoDir, fs, _ := setupIntegrationFixture(t, nil)

	store := state.NewFileStateStore(fs, ".borg/state")
	require.NoError(t, store.Save(state.ReconciliationState{
		ApproachID:     "app-oauth",
		Status:         state.StatusInProgress,
		SpecHash:       "stale-hash-from-prior-run",
		LastReconciled: time.Now(),
	}))

	wsStore := workstream.NewFileStore(fs, ".locutus/workstreams", "plan-drifted")
	require.NoError(t, wsStore.SavePlan(spec.MasterPlan{ID: "plan-drifted"}))
	require.NoError(t, wsStore.Save(workstream.ActiveWorkstream{
		WorkstreamID:   "ws-drifted",
		PlanID:         "plan-drifted",
		ApproachIDs:    []string{"app-oauth"},
		AgentSessionID: "session-from-prior",
	}))

	cfg := AdoptConfig{
		FS:       fs,
		LLM:      emptyPreflightLLM(2),
		RepoDir:  repoDir,
		Plan:     fakePlanOneWorkstream("plan-new", "ws-new", "app-oauth"),
		Dispatch: fakeDispatchSuccess(),
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)

	assert.Contains(t, report.ResumedInvalidated, "plan-drifted", "drift triggers invalidate")
	assert.NotContains(t, report.Resumed, "plan-drifted")
}

func TestRunAdoptResumesNonDriftedPlan(t *testing.T) {
	// A leftover plan whose covered Approaches are all SpecHash-clean
	// and not yet live, with a persisted AgentSessionID and at least
	// one not-yet-complete step, should be resumed: planner is NOT
	// invoked, dispatch is called with a populated resumeMap.
	repoDir, fs, _ := setupIntegrationFixture(t, nil)

	graph, err := loadSpecGraph(fs)
	require.NoError(t, err)
	approach := graph.Approach("app-oauth")
	require.NotNil(t, approach)
	specHash := spec.ComputeSpecHash(*approach)

	store := state.NewFileStateStore(fs, ".borg/state")
	require.NoError(t, store.Save(state.ReconciliationState{
		ApproachID:     "app-oauth",
		Status:         state.StatusInProgress,
		SpecHash:       specHash,
		LastReconciled: time.Now(),
	}))

	resumablePlan := spec.MasterPlan{
		ID: "plan-resumable",
		Workstreams: []spec.Workstream{
			{
				ID: "ws-resumable",
				Steps: []spec.PlanStep{
					{ID: "step-1", ApproachID: "app-oauth"},
					{ID: "step-2", ApproachID: "app-oauth"},
				},
			},
		},
	}
	wsStore := workstream.NewFileStore(fs, ".locutus/workstreams", "plan-resumable")
	require.NoError(t, wsStore.SavePlan(resumablePlan))
	require.NoError(t, wsStore.Save(workstream.ActiveWorkstream{
		WorkstreamID:   "ws-resumable",
		PlanID:         "plan-resumable",
		ApproachIDs:    []string{"app-oauth"},
		Plan:           resumablePlan.Workstreams[0],
		AgentSessionID: "session-from-prior",
		StepStatus: []workstream.StepProgress{
			{StepID: "step-1", Status: workstream.StepComplete},
		},
	}))

	// Capture the resumeMap the dispatcher sees so we can verify the
	// resume path was wired through with the correct ResumePoint.
	var capturedResume map[string]*dispatch.ResumePoint
	plannerCalled := false

	cfg := AdoptConfig{
		FS:      fs,
		LLM:     emptyPreflightLLM(2),
		RepoDir: repoDir,
		Plan: PlanFunc(func(context.Context, agent.PlanRequest) (*spec.MasterPlan, error) {
			plannerCalled = true
			return nil, nil
		}),
		Dispatch: DispatchFunc(func(_ context.Context, plan *spec.MasterPlan, _ string, resume map[string]*dispatch.ResumePoint) ([]*dispatch.WorkstreamResult, error) {
			capturedResume = resume
			out := make([]*dispatch.WorkstreamResult, 0, len(plan.Workstreams))
			for _, ws := range plan.Workstreams {
				out = append(out, &dispatch.WorkstreamResult{
					WorkstreamID: ws.ID,
					Success:      true,
					StepResults:  []*dispatch.StepOutcome{{Success: true}},
				})
			}
			return out, nil
		}),
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)

	assert.False(t, plannerCalled, "resume path must NOT invoke the planner")
	assert.Contains(t, report.Resumed, "plan-resumable")
	assert.Equal(t, "plan-resumable", report.PlanID)

	require.NotNil(t, capturedResume)
	rp, ok := capturedResume["ws-resumable"]
	require.True(t, ok, "resumeMap must carry ResumePoint for ws-resumable")
	assert.Equal(t, "step-2", rp.StepID, "resume targets first not-yet-complete step")
	assert.Equal(t, "session-from-prior", rp.SessionID)
}

func TestRunAdoptDryRunDoesNotDispatch(t *testing.T) {
	// Dry-run should not touch LLM, Plan, or Dispatch even if configured.
	repoDir, fs, _ := setupIntegrationFixture(t, nil)

	// Panic if anything downstream is invoked.
	panicPlan := PlanFunc(func(context.Context, agent.PlanRequest) (*spec.MasterPlan, error) {
		t.Fatalf("dry-run must not invoke planner")
		return nil, nil
	})
	panicDispatch := DispatchFunc(func(context.Context, *spec.MasterPlan, string, map[string]*dispatch.ResumePoint) ([]*dispatch.WorkstreamResult, error) {
		t.Fatalf("dry-run must not invoke dispatcher")
		return nil, nil
	})

	cfg := AdoptConfig{
		FS:       fs,
		RepoDir:  repoDir,
		Plan:     panicPlan,
		Dispatch: panicDispatch,
		DryRun:   true,
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	assert.True(t, report.DryRun)
	assert.Empty(t, report.PlanID)
	assert.Empty(t, report.DispatchedWorkstreams)
}

// TestRunAdoptRecordsStepProgress confirms the DJ-073 step status array
// gets populated after dispatch. The record is deleted on archive, so we
// run with a failing assertion to keep the plan around and inspect it.
func TestRunAdoptRecordsStepProgress(t *testing.T) {
	repoDir, fs, _ := setupIntegrationFixture(t, []spec.Assertion{
		{Kind: spec.AssertionKindFileExists, Target: "missing.go"},
	})

	cfg := AdoptConfig{
		FS:       fs,
		LLM:      emptyPreflightLLM(2),
		RepoDir:  repoDir,
		Plan:     fakePlanOneWorkstream("plan-steps", "ws-steps", "app-oauth"),
		Dispatch: fakeDispatchSuccess(),
	}

	_, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)

	wsStore := workstream.NewFileStore(fs, ".locutus/workstreams", "plan-steps")
	rec, err := wsStore.Load("ws-steps")
	require.NoError(t, err)
	require.Len(t, rec.StepStatus, 1)
	assert.Equal(t, "step-1", rec.StepStatus[0].StepID)
	assert.Equal(t, workstream.StepComplete, rec.StepStatus[0].Status,
		"dispatch success should record step as complete even when post-dispatch assertions then fail the Approach")
	assert.True(t, rec.PreFlightDone, "PreFlightDone should be set after preflight runs")
}
