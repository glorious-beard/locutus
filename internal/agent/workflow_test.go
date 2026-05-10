package agent

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAgentDefs() map[string]AgentDef {
	return map[string]AgentDef{
		"planner":     {ID: "planner", SystemPrompt: "You are the planner."},
		"critic":      {ID: "critic", SystemPrompt: "You are the critic."},
		"stakeholder": {ID: "stakeholder", SystemPrompt: "You are a stakeholder."},
		"researcher":  {ID: "researcher", SystemPrompt: "You are the researcher."},
		"historian":   {ID: "historian", SystemPrompt: "You are the historian."},
	}
}

func mockResp(content string) MockResponse {
	return MockResponse{Response: &AgentOutput{Content: content}}
}

func TestExecuteRoundSequential(t *testing.T) {
	mock := NewMockExecutor(mockResp("planner proposal"))

	exec := &WorkflowExecutor[PlanningState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"planner": {ID: "planner", SystemPrompt: "You are the planner."}},
		Workflow:  &Workflow[PlanningState]{MaxRounds: 5},
	}

	state := &PlanningState{Prompt: "Design feature X."}
	step := WorkflowStep[PlanningState]{ID: "propose", Agents: []string{"planner"}}

	results, err := exec.ExecuteRound(context.Background(), step, state)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "propose", results[0].StepID)
	assert.Equal(t, "planner", results[0].AgentID)
	assert.Equal(t, "planner proposal", results[0].Output)
	assert.NoError(t, results[0].Err)
}

func TestExecuteRoundParallel(t *testing.T) {
	mock := NewMockExecutor(
		mockResp("critic concerns"),
		mockResp("stakeholder feedback"),
	)

	exec := &WorkflowExecutor[PlanningState]{
		Executor: mock,
		AgentDefs: map[string]AgentDef{
			"critic":      {ID: "critic", SystemPrompt: "You are the critic."},
			"stakeholder": {ID: "stakeholder", SystemPrompt: "You are a stakeholder."},
		},
		Workflow: &Workflow[PlanningState]{MaxRounds: 5},
	}

	state := &PlanningState{
		Prompt:       "Design feature X.",
		ProposedSpec: "Here is my proposal...",
	}
	step := WorkflowStep[PlanningState]{
		ID:       "challenge",
		Agents:   []string{"critic", "stakeholder"},
		Parallel: true,
	}

	results, err := exec.ExecuteRound(context.Background(), step, state)
	assert.NoError(t, err)
	assert.Len(t, results, 2)

	agentIDs := map[string]bool{}
	for _, r := range results {
		assert.Equal(t, "challenge", r.StepID)
		assert.NoError(t, r.Err)
		agentIDs[r.AgentID] = true
	}
	assert.True(t, agentIDs["critic"])
	assert.True(t, agentIDs["stakeholder"])
	assert.Equal(t, 2, mock.CallCount())
}

func TestExecuteRoundConditionalSkipped(t *testing.T) {
	mock := NewMockExecutor(mockResp("should not be called"))

	exec := &WorkflowExecutor[PlanningState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"researcher": {ID: "researcher", SystemPrompt: "You are the researcher."}},
		Workflow:  &Workflow[PlanningState]{MaxRounds: 5},
	}

	state := &PlanningState{
		Prompt:       "Design feature X.",
		ProposedSpec: "The proposal is solid, no issues found.",
	}
	step := WorkflowStep[PlanningState]{
		ID:          "research",
		Agents:      []string{"researcher"},
		Conditional: hasOpenQuestions,
	}

	results, err := exec.ExecuteRound(context.Background(), step, state)
	assert.NoError(t, err)
	assert.Empty(t, results)
	assert.Equal(t, 0, mock.CallCount())
}

func TestExecuteRoundConditionalFires(t *testing.T) {
	mock := NewMockExecutor(mockResp("research findings"))

	exec := &WorkflowExecutor[PlanningState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"researcher": {ID: "researcher", SystemPrompt: "You are the researcher."}},
		Workflow:  &Workflow[PlanningState]{MaxRounds: 5},
	}

	state := &PlanningState{
		Prompt:       "Design feature X.",
		ProposedSpec: "My proposal...",
		OpenConcerns: []string{"scalability needs investigation"},
	}
	step := WorkflowStep[PlanningState]{
		ID:          "research",
		Agents:      []string{"researcher"},
		Conditional: hasOpenQuestions,
	}

	results, err := exec.ExecuteRound(context.Background(), step, state)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, 1, mock.CallCount())
}

// TestMergeFunctionsCoverPlanningSteps exercises each planning workflow
// merge function so a regression in any state-projection rule surfaces
// here rather than only in the live workflow.
func TestMergeFunctionsCoverPlanningSteps(t *testing.T) {
	state := &PlanningState{Prompt: "Design feature X."}

	mergeProposedSpec(state, []RoundResult{
		{StepID: "propose", AgentID: "planner", Output: "my proposal"},
	})
	assert.Equal(t, "my proposal", state.ProposedSpec)

	mergeChallengeConcerns(state, []RoundResult{
		{StepID: "challenge", AgentID: "critic", Output: "concern 1"},
		{StepID: "challenge", AgentID: "stakeholder", Output: "concern 2"},
	})
	assert.Len(t, state.Concerns, 2)
	assert.Equal(t, "critic", state.Concerns[0].AgentID)
	assert.Equal(t, "stakeholder", state.Concerns[1].AgentID)

	mergeResearch(state, []RoundResult{
		{StepID: "research", AgentID: "researcher", Output: "finding"},
	})
	assert.Len(t, state.ResearchResults, 1)

	mergeRevisions(state, []RoundResult{
		{StepID: "revise", AgentID: "planner", Output: "revised proposal"},
	})
	assert.Equal(t, "revised proposal", state.Revisions)

	mergeRecord(state, []RoundResult{
		{StepID: "record", AgentID: "historian", Output: "decision journal entry"},
	})
	assert.Equal(t, "decision journal entry", state.Record)
}

func TestWorkflowRunFullSequence(t *testing.T) {
	// Planning council runs propose → challenge (parallel) → research
	// (conditional on hasOpenQuestions, false on iter 1) → revise →
	// record. Iter 1 skips research because OpenConcerns is empty until
	// the convergence check populates it; convergence is absent here so
	// the loop exits after one iteration. Expected calls: propose(1) +
	// challenge(2) + revise(1) + record(1) = 5.
	mock := NewMockExecutor(
		mockResp("planner proposal"),
		mockResp("critic concerns"),
		mockResp("stakeholder feedback"),
		mockResp("revised proposal"),
		mockResp("historian record"),
	)

	exec := &WorkflowExecutor[PlanningState]{
		Executor:  mock,
		AgentDefs: newTestAgentDefs(),
		Workflow:  PlanningWorkflow,
	}

	results, err := RunCouncil(context.Background(), exec, &PlanningState{Prompt: "Design a feature for X.", Round: 1})
	assert.NoError(t, err)
	assert.Len(t, results, 5)

	stepOrder := []string{}
	for _, r := range results {
		stepOrder = append(stepOrder, r.StepID)
	}
	assert.True(t, indexOf(stepOrder, "propose") < indexOf(stepOrder, "challenge"))
	assert.True(t, indexOf(stepOrder, "challenge") < indexOf(stepOrder, "revise"))
	assert.True(t, indexOf(stepOrder, "revise") < indexOf(stepOrder, "record"))
	assert.NotContains(t, stepOrder, "research", "research is gated on OpenConcerns; iter 1 has none")

	assert.Equal(t, 5, mock.CallCount())
}

func TestWorkflowRunWithRetryableError(t *testing.T) {
	mock := NewMockExecutor(
		MockResponse{Err: ErrRateLimit},
		mockResp("planner response after retry"),
	)

	wf := &Workflow[PlanningState]{
		Rounds:    []WorkflowStep[PlanningState]{{ID: "propose", Agents: []string{"planner"}, Merge: mergeProposedSpec}},
		MaxRounds: 5,
	}

	exec := &WorkflowExecutor[PlanningState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"planner": {ID: "planner", SystemPrompt: "You are the planner."}},
		Workflow:  wf,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, err := exec.Run(ctx, &PlanningState{Prompt: "Plan something.", Round: 1})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "propose", results[0].StepID)
	assert.Equal(t, 2, mock.CallCount())
}

// TestWorkflowCleansUpBridgeOnConvergenceError verifies that when Run takes
// the convergence-error early-return path, the events-bridge goroutine still
// exits — i.e., the cleanup is structurally guaranteed, not dependent on
// reaching the trailing close. Regression guard for the leak that escaped
// audit on 2026-04-25.
func TestWorkflowCleansUpBridgeOnConvergenceError(t *testing.T) {
	mock := NewMockExecutor(
		mockResp("planner output"),
		MockResponse{Err: errors.New("convergence model unavailable")},
	)

	events := make(chan WorkflowEvent, 100)
	exec := &WorkflowExecutor[PlanningState]{
		Executor: mock,
		AgentDefs: map[string]AgentDef{
			"planner":     {ID: "planner", SystemPrompt: "plan."},
			"convergence": {ID: "convergence", SystemPrompt: "judge."},
		},
		Workflow: &Workflow[PlanningState]{
			Rounds:    []WorkflowStep[PlanningState]{{ID: "propose", Agents: []string{"planner"}, Merge: mergeProposedSpec}},
			MaxRounds: 5,
		},
		Events: events,
	}

	before := runtime.NumGoroutine()
	_, err := RunCouncil(context.Background(), exec, &PlanningState{Prompt: "Plan something.", Round: 1})
	require.Error(t, err, "convergence model failure should error out")

	assert.LessOrEqual(t, runtime.NumGoroutine(), before,
		"bridge goroutine leaked after early-return")
}

func TestWorkflowEvents(t *testing.T) {
	mock := NewMockExecutor(mockResp("planner output"))

	events := make(chan WorkflowEvent, 10)
	exec := &WorkflowExecutor[PlanningState]{
		Executor:  mock,
		AgentDefs: map[string]AgentDef{"planner": {ID: "planner", SystemPrompt: "You are the planner."}},
		Workflow:  &Workflow[PlanningState]{Rounds: []WorkflowStep[PlanningState]{{ID: "propose", Agents: []string{"planner"}, Merge: mergeProposedSpec}}, MaxRounds: 5},
		Events:    events,
	}

	_, err := exec.Run(context.Background(), &PlanningState{Prompt: "Design X.", Round: 1})
	assert.NoError(t, err)

	close(events)
	var evts []WorkflowEvent
	for e := range events {
		evts = append(evts, e)
	}

	assert.GreaterOrEqual(t, len(evts), 2)

	agentStatuses := map[string]bool{}
	for _, e := range evts {
		if e.AgentID == "planner" {
			agentStatuses[e.Status] = true
			assert.Equal(t, "propose", e.StepID)
		}
	}
	assert.True(t, agentStatuses["started"])
	assert.True(t, agentStatuses["completed"])
}

func TestSnapshotIsolation(t *testing.T) {
	state := &PlanningState{
		Prompt:       "Design X.",
		ProposedSpec: "original",
		Concerns:     []Concern{{AgentID: "critic", Text: "concern 1"}},
	}

	snap := snapshotPlanningState(state)

	state.ProposedSpec = "mutated"
	state.Concerns = append(state.Concerns, Concern{AgentID: "stakeholder", Text: "concern 2"})

	assert.Equal(t, "original", snap.ProposedSpec)
	assert.Len(t, snap.Concerns, 1, "snapshot should not see mutations to original state")
}

func TestConvergenceLoopConvergesFirstRound(t *testing.T) {
	mock := NewMockExecutor(
		mockResp("planner proposal"),
		mockResp("CONVERGED: all looks good"),
		mockResp("APPROVED"),
		mockResp("APPROVED"),
	)

	wf := &Workflow[PlanningState]{
		Rounds:    []WorkflowStep[PlanningState]{{ID: "propose", Agents: []string{"planner"}, Merge: mergeProposedSpec}},
		MaxRounds: 5,
	}

	defs := map[string]AgentDef{
		"planner":     {ID: "planner", SystemPrompt: "You are the planner."},
		"convergence": {ID: "convergence", SystemPrompt: "Assess convergence."},
		"critic":      {ID: "critic", SystemPrompt: "You are the critic."},
		"stakeholder": {ID: "stakeholder", SystemPrompt: "You are a stakeholder."},
	}

	exec := &WorkflowExecutor[PlanningState]{Executor: mock, AgentDefs: defs, Workflow: wf}
	results, err := RunCouncil(context.Background(), exec, &PlanningState{Prompt: "Design X.", Round: 1})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, 4, mock.CallCount())
}

func TestConvergenceLoopRequiresMultipleIterations(t *testing.T) {
	mock := NewMockExecutor(
		mockResp("initial proposal"),
		mockResp("NOT_CONVERGED\n- need more detail on auth"),
		mockResp("revised proposal with auth details"),
		mockResp("CONVERGED"),
		mockResp("APPROVED"),
		mockResp("APPROVED"),
	)

	wf := &Workflow[PlanningState]{
		Rounds:    []WorkflowStep[PlanningState]{{ID: "propose", Agents: []string{"planner"}, Merge: mergeProposedSpec}},
		MaxRounds: 5,
	}

	defs := map[string]AgentDef{
		"planner":     {ID: "planner", SystemPrompt: "You are the planner."},
		"convergence": {ID: "convergence", SystemPrompt: "Assess convergence."},
		"critic":      {ID: "critic", SystemPrompt: "You are the critic."},
		"stakeholder": {ID: "stakeholder", SystemPrompt: "You are a stakeholder."},
	}

	exec := &WorkflowExecutor[PlanningState]{Executor: mock, AgentDefs: defs, Workflow: wf}
	results, err := RunCouncil(context.Background(), exec, &PlanningState{Prompt: "Design X.", Round: 1})
	assert.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, 6, mock.CallCount())
}

func TestConvergenceLoopReadinessBlocked(t *testing.T) {
	mock := NewMockExecutor(
		mockResp("proposal v1"),
		mockResp("CONVERGED"),
		mockResp("BLOCKED: missing error handling"),
		mockResp("proposal v2 with error handling"),
		mockResp("CONVERGED"),
		mockResp("APPROVED"),
		mockResp("APPROVED"),
	)

	wf := &Workflow[PlanningState]{
		Rounds:    []WorkflowStep[PlanningState]{{ID: "propose", Agents: []string{"planner"}, Merge: mergeProposedSpec}},
		MaxRounds: 5,
	}

	defs := map[string]AgentDef{
		"planner":     {ID: "planner", SystemPrompt: "You are the planner."},
		"convergence": {ID: "convergence", SystemPrompt: "Assess convergence."},
		"critic":      {ID: "critic", SystemPrompt: "You are the critic."},
		"stakeholder": {ID: "stakeholder", SystemPrompt: "You are a stakeholder."},
	}

	exec := &WorkflowExecutor[PlanningState]{Executor: mock, AgentDefs: defs, Workflow: wf}
	results, err := RunCouncil(context.Background(), exec, &PlanningState{Prompt: "Design X.", Round: 1})
	assert.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, 7, mock.CallCount())
}

func TestConvergenceLoopForcedAfterMaxRounds(t *testing.T) {
	var responses []MockResponse
	for i := 0; i < 5; i++ {
		responses = append(responses,
			mockResp(fmt.Sprintf("proposal iteration %d", i+1)),
			mockResp("NOT_CONVERGED\n- still issues"),
		)
	}
	mock := NewMockExecutor(responses...)

	wf := &Workflow[PlanningState]{
		Rounds:    []WorkflowStep[PlanningState]{{ID: "propose", Agents: []string{"planner"}, Merge: mergeProposedSpec}},
		MaxRounds: 5,
	}

	defs := map[string]AgentDef{
		"planner":     {ID: "planner", SystemPrompt: "You are the planner."},
		"convergence": {ID: "convergence", SystemPrompt: "Assess convergence."},
	}

	exec := &WorkflowExecutor[PlanningState]{Executor: mock, AgentDefs: defs, Workflow: wf}
	results, err := RunCouncil(context.Background(), exec, &PlanningState{Prompt: "Design X.", Round: 1})
	assert.NoError(t, err)
	assert.LessOrEqual(t, len(results), 5)
}

func TestParseConvergenceResponse(t *testing.T) {
	v := parseConvergenceResponse("CONVERGED: everything looks good", 2)
	assert.True(t, v.Converged)

	v = parseConvergenceResponse("NOT_CONVERGED\n- auth is missing\n- no tests", 1)
	assert.False(t, v.Converged)
	assert.Len(t, v.OpenIssues, 2)
	assert.Equal(t, "auth is missing", v.OpenIssues[0])

	v = parseConvergenceResponse("CYCLING: same debate for 3 rounds", 3)
	assert.True(t, v.Converged)
	assert.Equal(t, 3, v.ForcedAfter)
}

func indexOf(haystack []string, needle string) int {
	for i, v := range haystack {
		if v == needle {
			return i
		}
	}
	return -1
}
