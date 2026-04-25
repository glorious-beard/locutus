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

	for _, d := range []string{".borg/spec/features", ".borg/spec/decisions", ".borg/spec/approaches", ".borg/history", ".locutus/state"} {
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
	store := state.NewFileStateStore(fs, ".locutus/state")
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

	store := state.NewFileStateStore(fs, ".locutus/state")
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

	store := state.NewFileStateStore(fs, ".locutus/state")
	entry, err := store.Load("app-oauth")
	require.NoError(t, err)
	assert.Equal(t, state.StatusFailed, entry.Status)
}

func TestRunAdoptInvalidatesStalePlanOnResume(t *testing.T) {
	repoDir, fs, _ := setupIntegrationFixture(t, nil)

	// Pre-seed a stale plan directory as if a previous Locutus died
	// mid-dispatch.
	wsStore := workstream.NewFileStore(fs, ".locutus/workstreams", "plan-old")
	require.NoError(t, wsStore.SavePlan(spec.MasterPlan{ID: "plan-old"}))
	require.NoError(t, wsStore.Save(workstream.ActiveWorkstream{WorkstreamID: "ws-old", PlanID: "plan-old"}))

	cfg := AdoptConfig{
		FS:       fs,
		LLM:      emptyPreflightLLM(2),
		RepoDir:  repoDir,
		Plan:     fakePlanOneWorkstream("plan-new", "ws-new", "app-oauth"),
		Dispatch: fakeDispatchSuccess(),
	}

	report, err := RunAdoptWithConfig(context.Background(), cfg)
	require.NoError(t, err)

	assert.Contains(t, report.ResumedInvalidated, "plan-old")

	// plan-old subdir must be gone.
	activeIDs, err := workstream.ListActivePlans(fs, ".locutus/workstreams")
	require.NoError(t, err)
	assert.NotContains(t, activeIDs, "plan-old")
	// plan-new completed and got archived.
	assert.NotContains(t, activeIDs, "plan-new")
	assert.Contains(t, report.Archived, "plan-new")
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
