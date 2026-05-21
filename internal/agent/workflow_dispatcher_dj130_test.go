package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureDispatcher records every Dispatch call for assertions while
// delegating execution to an inner AgentDispatcher (typically a
// Dispatcher wrapping a MockExecutor).
type captureDispatcher struct {
	inner AgentDispatcher

	mu    sync.Mutex
	calls []capturedDispatch
}

type capturedDispatch struct {
	AgentID string
	Role    string
	Input   AgentInput
}

func (c *captureDispatcher) Dispatch(ctx context.Context, def AgentDef, input AgentInput, opts DispatchOptions) (*AgentOutput, error) {
	c.mu.Lock()
	c.calls = append(c.calls, capturedDispatch{
		AgentID: def.ID,
		Role:    opts.Role,
		Input:   input,
	})
	c.mu.Unlock()
	return c.inner.Dispatch(ctx, def, input, opts)
}

// TestWorkflowExecutorRoutesThroughDispatcher (DJ-130 Phase 1)
// verifies WorkflowExecutor.executeAgent dispatches through the
// AgentDispatcher carried on the executor — not RunWithRetry directly —
// and stamps DispatchOptions.Role with `workflow:<step-id>` so per-call
// telemetry attributes the work cleanly. Closes the wiring gap DJ-122
// introduced by routing workflow LLM calls through the same dispatcher
// surface every other call site uses.
func TestWorkflowExecutorRoutesThroughDispatcher(t *testing.T) {
	mock := NewMockExecutor(mockResp("planner output"))
	captured := &captureDispatcher{inner: NewDispatcher(mock)}

	exec := &WorkflowExecutor[PlanningState]{
		Executor:   mock,
		Dispatcher: captured,
		AgentDefs:  map[string]AgentDef{"planner": {ID: "planner", SystemPrompt: "planner"}},
		Workflow:   &Workflow[PlanningState]{MaxRounds: 5},
	}

	state := &PlanningState{Prompt: "design feature X"}
	step := WorkflowStep[PlanningState]{ID: "propose", Agents: []string{"planner"}}

	results, err := exec.ExecuteRound(context.Background(), step, state)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "planner output", results[0].Output)

	require.Len(t, captured.calls, 1, "step routes through Dispatcher.Dispatch exactly once")
	assert.Equal(t, "planner", captured.calls[0].AgentID)
	assert.Equal(t, "workflow:propose", captured.calls[0].Role,
		"DispatchOptions.Role carries the workflow step id so per-call telemetry attributes the work")
}

// TestWorkflowExecutorFallsBackToDefaultDispatcher confirms the lazy
// default fires when Dispatcher is left nil — production constructors
// that didn't opt into a custom dispatcher still route through Dispatch.
func TestWorkflowExecutorFallsBackToDefaultDispatcher(t *testing.T) {
	mock := NewMockExecutor(mockResp("planner output"))

	exec := &WorkflowExecutor[PlanningState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"planner": {ID: "planner", SystemPrompt: "planner"}},
		Workflow:  &Workflow[PlanningState]{MaxRounds: 5},
	}

	state := &PlanningState{Prompt: "design feature X"}
	step := WorkflowStep[PlanningState]{ID: "propose", Agents: []string{"planner"}}

	results, err := exec.ExecuteRound(context.Background(), step, state)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "planner output", results[0].Output)
	assert.Equal(t, 1, mock.CallCount(),
		"lazy default dispatcher routes the call through to the underlying executor")
}

// TestWorkflowExecutorDispatcherInvokedOncePerFanoutItem verifies the
// fanout path issues one Dispatch call per fanout item, each tagged
// with the fanout-shaped step id (`<step> (<item-id>)`). Confirms the
// Phase 1 wiring fans out cleanly with the rest of executeAgent's
// per-item context plumbing intact.
func TestWorkflowExecutorDispatcherInvokedOncePerFanoutItem(t *testing.T) {
	mock := NewMockExecutor(
		mockResp("elaborated 1"),
		mockResp("elaborated 2"),
	)
	captured := &captureDispatcher{inner: NewDispatcher(mock)}

	exec := &WorkflowExecutor[PlanningState]{
		Executor:   mock,
		Dispatcher: captured,
		AgentDefs:  map[string]AgentDef{"elab": {ID: "elab", SystemPrompt: "elab"}},
		Workflow:   &Workflow[PlanningState]{MaxRounds: 5},
	}

	var fanoutCalls atomic.Int32
	step := WorkflowStep[PlanningState]{
		ID:     "elaborate",
		Agents: []string{"elab"},
		Fanout: func(_ *PlanningState) ([]string, error) {
			fanoutCalls.Add(1)
			return []string{`{"id":"item-a"}`, `{"id":"item-b"}`}, nil
		},
	}

	state := &PlanningState{Prompt: "design feature X"}
	results, err := exec.ExecuteRound(context.Background(), step, state)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Len(t, captured.calls, 2)
	// Both items dispatched through the same agent; per-item step id
	// carries the fanout tag.
	assert.Equal(t, "workflow:elaborate (item-a)", captured.calls[0].Role)
	assert.Equal(t, "workflow:elaborate (item-b)", captured.calls[1].Role)
}
