package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImportWorkflowPhaseOrdering verifies the intake step completes
// (and stores IntakeResult) before the plan step's PlanRunner closure
// fires. The closure asserts the intake output is observable when it
// runs, catching any DAG-ordering regression.
func TestImportWorkflowPhaseOrdering(t *testing.T) {
	intakeJSON := `{"id":"feat-x","title":"Feature X","accepted":true}`
	mock := NewMockExecutor(MockResponse{
		Response: &AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	var planSawIntake bool
	state := &ImportState{
		Data:      []byte("Feature X document body."),
		Kind:      "feature",
		GoalsBody: "Build something useful.",
		LLM:       mock,
		PlanRunner: func(s *ImportState) (any, error) {
			// The plan step's merge fires only after intake's merge has
			// stashed IntakeResult. If the DAG ordering inverted, this
			// would observe a nil result.
			planSawIntake = s.IntakeResult != nil && s.IntakeResult.ID == "feat-x"
			return "plan-output", nil
		},
	}

	require.NoError(t, RunImportWorkflow(context.Background(), state))
	require.NotNil(t, state.IntakeResult)
	assert.Equal(t, "feat-x", state.IntakeResult.ID)
	assert.True(t, state.IntakeResult.Accepted)
	assert.True(t, planSawIntake, "plan step should observe IntakeResult populated by intake step")
	assert.Equal(t, "plan-output", state.PlanResult)
}

// TestImportWorkflowSkipPlanSuppressesPlan verifies SkipPlan=true keeps
// the plan step from firing even when intake admitted the document.
func TestImportWorkflowSkipPlanSuppressesPlan(t *testing.T) {
	intakeJSON := `{"id":"feat-y","title":"Feature Y","accepted":true}`
	mock := NewMockExecutor(MockResponse{
		Response: &AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	var planFired bool
	state := &ImportState{
		Data:     []byte("Feature Y document body."),
		Kind:     "feature",
		SkipPlan: true,
		LLM:      mock,
		PlanRunner: func(*ImportState) (any, error) {
			planFired = true
			return "should-not-be-set", nil
		},
	}

	require.NoError(t, RunImportWorkflow(context.Background(), state))
	require.NotNil(t, state.IntakeResult)
	assert.False(t, planFired, "SkipPlan=true should keep PlanRunner from firing")
	assert.Nil(t, state.PlanResult, "PlanResult should stay nil when plan step is skipped")
}

// TestImportWorkflowRejectionSuppressesPlan verifies an intake verdict
// with Accepted=false suppresses the plan step. Mirrors the cmd-layer
// admission-gate rule (a rejected intake never triggers planning).
func TestImportWorkflowRejectionSuppressesPlan(t *testing.T) {
	intakeJSON := `{"id":"feat-mobile","title":"Mobile","accepted":false,"reason":"out of scope"}`
	mock := NewMockExecutor(MockResponse{
		Response: &AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	var planFired bool
	state := &ImportState{
		Data:      []byte("Add a mobile app."),
		Kind:      "feature",
		GoalsBody: "Build a CLI tool.",
		LLM:       mock,
		PlanRunner: func(*ImportState) (any, error) {
			planFired = true
			return "should-not-be-set", nil
		},
	}

	require.NoError(t, RunImportWorkflow(context.Background(), state))
	require.NotNil(t, state.IntakeResult)
	assert.False(t, state.IntakeResult.Accepted, "intake should produce a rejected verdict")
	assert.False(t, planFired, "rejected intake should suppress PlanRunner")
	assert.Nil(t, state.PlanResult)
}

// TestImportWorkflowDuplicateSuppressesPlan verifies an intake verdict
// with Duplicate=true suppresses the plan step (the cmd-layer treats
// duplicates as rejections).
func TestImportWorkflowDuplicateSuppressesPlan(t *testing.T) {
	intakeJSON := `{"id":"feat-status","title":"Status","accepted":false,"duplicate":true,"duplicate_of":"feat-status","reason":"duplicate"}`
	mock := NewMockExecutor(MockResponse{
		Response: &AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	var planFired bool
	state := &ImportState{
		Data:      []byte("Status command again."),
		Kind:      "feature",
		GoalsBody: "Build a CLI tool.",
		LLM:       mock,
		PlanRunner: func(*ImportState) (any, error) {
			planFired = true
			return nil, nil
		},
	}

	require.NoError(t, RunImportWorkflow(context.Background(), state))
	require.NotNil(t, state.IntakeResult)
	assert.True(t, state.IntakeResult.Duplicate)
	assert.False(t, planFired, "duplicate intake should suppress PlanRunner")
}

// TestImportWorkflowNilPlanRunnerSuppressesPlan verifies a nil
// PlanRunner suppresses the plan step (the cmd-layer leaves it nil
// when no planning is wanted).
func TestImportWorkflowNilPlanRunnerSuppressesPlan(t *testing.T) {
	intakeJSON := `{"id":"feat-z","title":"Feature Z","accepted":true}`
	mock := NewMockExecutor(MockResponse{
		Response: &AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	state := &ImportState{
		Data: []byte("Feature Z body."),
		Kind: "feature",
		LLM:  mock,
		// PlanRunner intentionally nil.
	}

	require.NoError(t, RunImportWorkflow(context.Background(), state))
	require.NotNil(t, state.IntakeResult)
	assert.Nil(t, state.PlanResult, "nil PlanRunner should leave PlanResult nil")
}

// TestImportWorkflowDryRunPlanRunnerCanInspectFlag verifies the
// PlanRunner closure sees DryRun on the state — used by the cmd-layer
// to short-circuit on-disk writes inside the planning callback.
// The workflow itself doesn't gate on DryRun; the closure does.
func TestImportWorkflowDryRunPlanRunnerCanInspectFlag(t *testing.T) {
	intakeJSON := `{"id":"feat-dry","title":"Dry","accepted":true}`
	mock := NewMockExecutor(MockResponse{
		Response: &AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	var observed bool
	var observedDryRun bool
	state := &ImportState{
		Data:   []byte("Body."),
		Kind:   "feature",
		DryRun: true,
		LLM:    mock,
		PlanRunner: func(s *ImportState) (any, error) {
			observed = true
			observedDryRun = s.DryRun
			// Closure short-circuits on DryRun in production.
			if s.DryRun {
				return nil, nil
			}
			return "wrote-stuff", nil
		},
	}

	require.NoError(t, RunImportWorkflow(context.Background(), state))
	assert.True(t, observed, "PlanRunner should fire even on DryRun (the workflow doesn't gate)")
	assert.True(t, observedDryRun, "PlanRunner should see DryRun=true on state")
	assert.Nil(t, state.PlanResult, "DryRun closure returned nil; PlanResult should be nil")
}

// TestImportWorkflowIntakeErrorSurfaces verifies a failure inside the
// intake LLM call surfaces as RunImportWorkflow's return error and
// suppresses the plan step (no IntakeResult means the conditional
// fails).
func TestImportWorkflowIntakeErrorSurfaces(t *testing.T) {
	llmErr := errors.New("intake LLM unavailable")
	mock := NewMockExecutor(MockResponse{Err: llmErr})

	var planFired bool
	state := &ImportState{
		Data: []byte("Body."),
		Kind: "feature",
		LLM:  mock,
		PlanRunner: func(*ImportState) (any, error) {
			planFired = true
			return nil, nil
		},
	}

	err := RunImportWorkflow(context.Background(), state)
	require.Error(t, err)
	assert.ErrorIs(t, err, llmErr, "intake error should propagate")
	assert.Nil(t, state.IntakeResult)
	assert.False(t, planFired, "intake failure should suppress plan step")
}

// TestImportWorkflowPlanRunnerErrorSurfaces verifies a failure inside
// PlanRunner surfaces as RunImportWorkflow's return error.
func TestImportWorkflowPlanRunnerErrorSurfaces(t *testing.T) {
	intakeJSON := `{"id":"feat-q","title":"Q","accepted":true}`
	mock := NewMockExecutor(MockResponse{
		Response: &AgentOutput{Content: intakeJSON, Model: "test-model"},
	})

	planErr := errors.New("planning pass failed")
	state := &ImportState{
		Data: []byte("Body."),
		Kind: "feature",
		LLM:  mock,
		PlanRunner: func(*ImportState) (any, error) {
			return nil, planErr
		},
	}

	err := RunImportWorkflow(context.Background(), state)
	require.Error(t, err)
	assert.ErrorIs(t, err, planErr, "PlanRunner error should propagate")
	require.NotNil(t, state.IntakeResult, "intake should still complete")
}
