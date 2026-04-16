package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
)

const workflowYAML = `rounds:
  - id: propose
    agent: planner
    parallel: false
  - id: challenge
    agents: [critic, stakeholder]
    parallel: true
    depends_on: [propose]
  - id: research
    agent: researcher
    parallel: false
    depends_on: [challenge]
    conditional: open_questions
  - id: revise
    agent: planner
    parallel: false
    depends_on: [research]
  - id: record
    agent: historian
    parallel: false
    depends_on: [revise]
max_rounds: 5
`

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
	return MockResponse{Response: &GenerateResponse{Content: content}}
}

func TestLoadWorkflow(t *testing.T) {
	fs := specio.NewMemFS()
	assert.NoError(t, fs.WriteFile("workflow.yaml", []byte(workflowYAML), 0o644))

	wf, err := LoadWorkflow(fs, "workflow.yaml")
	assert.NoError(t, err)
	assert.NotNil(t, wf)

	assert.Len(t, wf.Rounds, 5)
	assert.Equal(t, 5, wf.MaxRounds)

	assert.Equal(t, "propose", wf.Rounds[0].ID)
	assert.Equal(t, "planner", wf.Rounds[0].Agent)
	assert.False(t, wf.Rounds[0].Parallel)

	assert.Equal(t, "challenge", wf.Rounds[1].ID)
	assert.Equal(t, []string{"critic", "stakeholder"}, wf.Rounds[1].Agents)
	assert.True(t, wf.Rounds[1].Parallel)
	assert.Equal(t, []string{"propose"}, wf.Rounds[1].DependsOn)

	assert.Equal(t, "research", wf.Rounds[2].ID)
	assert.Equal(t, "open_questions", wf.Rounds[2].Conditional)

	assert.Equal(t, "revise", wf.Rounds[3].ID)
	assert.Equal(t, []string{"research"}, wf.Rounds[3].DependsOn)

	assert.Equal(t, "record", wf.Rounds[4].ID)
	assert.Equal(t, []string{"revise"}, wf.Rounds[4].DependsOn)
}

func TestLoadWorkflowMissing(t *testing.T) {
	fs := specio.NewMemFS()
	wf, err := LoadWorkflow(fs, "nonexistent.yaml")
	assert.Error(t, err)
	assert.Nil(t, wf)
}

func TestExecuteRoundSequential(t *testing.T) {
	mock := NewMockLLM(mockResp("planner proposal"))

	exec := &WorkflowExecutor{
		LLM:       mock,
		AgentDefs: map[string]AgentDef{"planner": {ID: "planner", SystemPrompt: "You are the planner."}},
		Workflow:  &Workflow{MaxRounds: 5},
	}

	state := &PlanningState{Prompt: "Design feature X."}
	step := WorkflowStep{ID: "propose", Agent: "planner"}

	results, err := exec.ExecuteRound(context.Background(), step, state)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "propose", results[0].StepID)
	assert.Equal(t, "planner", results[0].AgentID)
	assert.Equal(t, "planner proposal", results[0].Output)
	assert.NoError(t, results[0].Err)
}

func TestExecuteRoundParallel(t *testing.T) {
	mock := NewMockLLM(
		mockResp("critic concerns"),
		mockResp("stakeholder feedback"),
	)

	exec := &WorkflowExecutor{
		LLM: mock,
		AgentDefs: map[string]AgentDef{
			"critic":      {ID: "critic", SystemPrompt: "You are the critic."},
			"stakeholder": {ID: "stakeholder", SystemPrompt: "You are a stakeholder."},
		},
		Workflow: &Workflow{MaxRounds: 5},
	}

	state := &PlanningState{
		Prompt:       "Design feature X.",
		ProposedSpec: "Here is my proposal...",
	}
	step := WorkflowStep{
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
	mock := NewMockLLM(mockResp("should not be called"))

	exec := &WorkflowExecutor{
		LLM:       mock,
		AgentDefs: map[string]AgentDef{"researcher": {ID: "researcher", SystemPrompt: "You are the researcher."}},
		Workflow:  &Workflow{MaxRounds: 5},
	}

	// State has no mention of "open_questions" anywhere.
	state := &PlanningState{
		Prompt:       "Design feature X.",
		ProposedSpec: "The proposal is solid, no issues found.",
	}
	step := WorkflowStep{
		ID:          "research",
		Agent:       "researcher",
		Conditional: "open_questions",
	}

	results, err := exec.ExecuteRound(context.Background(), step, state)
	assert.NoError(t, err)
	assert.Empty(t, results)
	assert.Equal(t, 0, mock.CallCount())
}

func TestExecuteRoundConditionalFires(t *testing.T) {
	mock := NewMockLLM(mockResp("research findings"))

	exec := &WorkflowExecutor{
		LLM:       mock,
		AgentDefs: map[string]AgentDef{"researcher": {ID: "researcher", SystemPrompt: "You are the researcher."}},
		Workflow:  &Workflow{MaxRounds: 5},
	}

	// Concerns contain the trigger keyword.
	state := &PlanningState{
		Prompt:       "Design feature X.",
		ProposedSpec: "My proposal...",
		Concerns: []Concern{
			{AgentID: "critic", Severity: "high", Text: "There are open_questions about scalability."},
		},
	}
	step := WorkflowStep{
		ID:          "research",
		Agent:       "researcher",
		Conditional: "open_questions",
	}

	results, err := exec.ExecuteRound(context.Background(), step, state)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, 1, mock.CallCount())
}

func TestMergeResults(t *testing.T) {
	state := &PlanningState{Prompt: "Design feature X."}

	// Propose → sets ProposedSpec.
	mergeResults(state, WorkflowStep{ID: "propose"}, []RoundResult{
		{StepID: "propose", AgentID: "planner", Output: "my proposal"},
	})
	assert.Equal(t, "my proposal", state.ProposedSpec)

	// Challenge → appends Concerns.
	mergeResults(state, WorkflowStep{ID: "challenge"}, []RoundResult{
		{StepID: "challenge", AgentID: "critic", Output: "concern 1"},
		{StepID: "challenge", AgentID: "stakeholder", Output: "concern 2"},
	})
	assert.Len(t, state.Concerns, 2)
	assert.Equal(t, "critic", state.Concerns[0].AgentID)
	assert.Equal(t, "stakeholder", state.Concerns[1].AgentID)

	// Research → appends Findings.
	mergeResults(state, WorkflowStep{ID: "research"}, []RoundResult{
		{StepID: "research", AgentID: "researcher", Output: "finding"},
	})
	assert.Len(t, state.ResearchResults, 1)

	// Revise → sets Revisions.
	mergeResults(state, WorkflowStep{ID: "revise"}, []RoundResult{
		{StepID: "revise", AgentID: "planner", Output: "revised proposal"},
	})
	assert.Equal(t, "revised proposal", state.Revisions)

	// Record → sets Record.
	mergeResults(state, WorkflowStep{ID: "record"}, []RoundResult{
		{StepID: "record", AgentID: "historian", Output: "decision journal entry"},
	})
	assert.Equal(t, "decision journal entry", state.Record)

	// Test MergeAs override.
	state2 := &PlanningState{Prompt: "Test merge_as."}
	mergeResults(state2, WorkflowStep{ID: "custom-step", MergeAs: "proposed_spec"}, []RoundResult{
		{StepID: "custom-step", AgentID: "planner", Output: "merged via merge_as"},
	})
	assert.Equal(t, "merged via merge_as", state2.ProposedSpec)
}

func TestWorkflowRunFullSequence(t *testing.T) {
	// 6 calls: propose(1) + challenge(2) + research(1) + revise(1) + record(1)
	// The critic response includes "open_questions" so research fires.
	mock := NewMockLLM(
		mockResp("planner proposal"),
		mockResp("critic: there are open_questions here"),
		mockResp("stakeholder: looks reasonable"),
		mockResp("researcher findings"),
		mockResp("revised proposal"),
		mockResp("historian record"),
	)

	fs := specio.NewMemFS()
	assert.NoError(t, fs.WriteFile("workflow.yaml", []byte(workflowYAML), 0o644))
	wf, err := LoadWorkflow(fs, "workflow.yaml")
	assert.NoError(t, err)

	exec := &WorkflowExecutor{
		LLM:       mock,
		AgentDefs: newTestAgentDefs(),
		Workflow:  wf,
	}

	results, err := exec.Run(context.Background(), "Design a feature for X.")
	assert.NoError(t, err)
	assert.Len(t, results, 6)

	// Verify dependency ordering.
	stepOrder := []string{}
	for _, r := range results {
		stepOrder = append(stepOrder, r.StepID)
	}
	assert.True(t, indexOf(stepOrder, "propose") < indexOf(stepOrder, "challenge"))
	assert.True(t, indexOf(stepOrder, "challenge") < indexOf(stepOrder, "research"))
	assert.True(t, indexOf(stepOrder, "research") < indexOf(stepOrder, "revise"))
	assert.True(t, indexOf(stepOrder, "revise") < indexOf(stepOrder, "record"))

	assert.Equal(t, 6, mock.CallCount())
}

func TestWorkflowRunWithRetryableError(t *testing.T) {
	mock := NewMockLLM(
		MockResponse{Err: ErrRateLimit},
		mockResp("planner response after retry"),
	)

	wf := &Workflow{
		Rounds:    []WorkflowStep{{ID: "propose", Agent: "planner"}},
		MaxRounds: 5,
	}

	exec := &WorkflowExecutor{
		LLM:       mock,
		AgentDefs: map[string]AgentDef{"planner": {ID: "planner", SystemPrompt: "You are the planner."}},
		Workflow:  wf,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, err := exec.Run(ctx, "Plan something.")
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "propose", results[0].StepID)
	assert.Equal(t, 2, mock.CallCount())
}

func TestWorkflowEvents(t *testing.T) {
	mock := NewMockLLM(mockResp("planner output"))

	events := make(chan WorkflowEvent, 10)
	exec := &WorkflowExecutor{
		LLM:       mock,
		AgentDefs: map[string]AgentDef{"planner": {ID: "planner", SystemPrompt: "You are the planner."}},
		Workflow:  &Workflow{Rounds: []WorkflowStep{{ID: "propose", Agent: "planner"}}, MaxRounds: 5},
		Events:    events,
	}

	_, err := exec.Run(context.Background(), "Design X.")
	assert.NoError(t, err)

	// Drain events.
	close(events)
	var evts []WorkflowEvent
	for e := range events {
		evts = append(evts, e)
	}

	// Should have agent-level events (started/completed for planner)
	// and possibly iteration-level events.
	assert.GreaterOrEqual(t, len(evts), 2)

	// Filter to agent-specific events.
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

	snap := state.Snapshot()

	// Mutate the original — snapshot should be unaffected.
	state.ProposedSpec = "mutated"
	state.Concerns = append(state.Concerns, Concern{AgentID: "stakeholder", Text: "concern 2"})

	assert.Equal(t, "original", snap.ProposedSpec)
	assert.Len(t, snap.Concerns, 1, "snapshot should not see mutations to original state")
}

func TestConvergenceLoopConvergesFirstRound(t *testing.T) {
	// Single-step workflow + convergence monitor + readiness gate.
	// Iteration 1: propose → convergence says CONVERGED → critic APPROVED → stakeholder APPROVED → done.
	mock := NewMockLLM(
		mockResp("planner proposal"),           // propose
		mockResp("CONVERGED: all looks good"),  // convergence check
		mockResp("APPROVED"),                   // critic readiness
		mockResp("APPROVED"),                   // stakeholder readiness
	)

	wf := &Workflow{
		Rounds:    []WorkflowStep{{ID: "propose", Agent: "planner"}},
		MaxRounds: 5,
	}

	defs := map[string]AgentDef{
		"planner":     {ID: "planner", SystemPrompt: "You are the planner."},
		"convergence": {ID: "convergence", SystemPrompt: "Assess convergence."},
		"critic":      {ID: "critic", SystemPrompt: "You are the critic."},
		"stakeholder": {ID: "stakeholder", SystemPrompt: "You are a stakeholder."},
	}

	exec := &WorkflowExecutor{LLM: mock, AgentDefs: defs, Workflow: wf}
	results, err := exec.Run(context.Background(), "Design X.")
	assert.NoError(t, err)
	assert.Len(t, results, 1) // only the propose result
	assert.Equal(t, 4, mock.CallCount()) // propose + convergence + critic + stakeholder
}

func TestConvergenceLoopRequiresMultipleIterations(t *testing.T) {
	// Iteration 1: propose → NOT_CONVERGED
	// Iteration 2: propose (again) → CONVERGED → APPROVED × 2
	mock := NewMockLLM(
		// Iteration 1
		mockResp("initial proposal"),
		mockResp("NOT_CONVERGED\n- need more detail on auth"),
		// Iteration 2
		mockResp("revised proposal with auth details"),
		mockResp("CONVERGED"),
		mockResp("APPROVED"), // critic
		mockResp("APPROVED"), // stakeholder
	)

	wf := &Workflow{
		Rounds:    []WorkflowStep{{ID: "propose", Agent: "planner"}},
		MaxRounds: 5,
	}

	defs := map[string]AgentDef{
		"planner":     {ID: "planner", SystemPrompt: "You are the planner."},
		"convergence": {ID: "convergence", SystemPrompt: "Assess convergence."},
		"critic":      {ID: "critic", SystemPrompt: "You are the critic."},
		"stakeholder": {ID: "stakeholder", SystemPrompt: "You are a stakeholder."},
	}

	exec := &WorkflowExecutor{LLM: mock, AgentDefs: defs, Workflow: wf}
	results, err := exec.Run(context.Background(), "Design X.")
	assert.NoError(t, err)
	assert.Len(t, results, 2) // propose from each iteration
	assert.Equal(t, 6, mock.CallCount())
}

func TestConvergenceLoopReadinessBlocked(t *testing.T) {
	// Iteration 1: propose → CONVERGED → critic BLOCKED → loop
	// Iteration 2: propose → CONVERGED → critic APPROVED → stakeholder APPROVED
	mock := NewMockLLM(
		// Iteration 1
		mockResp("proposal v1"),
		mockResp("CONVERGED"),
		mockResp("BLOCKED: missing error handling"),
		// Iteration 2
		mockResp("proposal v2 with error handling"),
		mockResp("CONVERGED"),
		mockResp("APPROVED"),
		mockResp("APPROVED"),
	)

	wf := &Workflow{
		Rounds:    []WorkflowStep{{ID: "propose", Agent: "planner"}},
		MaxRounds: 5,
	}

	defs := map[string]AgentDef{
		"planner":     {ID: "planner", SystemPrompt: "You are the planner."},
		"convergence": {ID: "convergence", SystemPrompt: "Assess convergence."},
		"critic":      {ID: "critic", SystemPrompt: "You are the critic."},
		"stakeholder": {ID: "stakeholder", SystemPrompt: "You are a stakeholder."},
	}

	exec := &WorkflowExecutor{LLM: mock, AgentDefs: defs, Workflow: wf}
	results, err := exec.Run(context.Background(), "Design X.")
	assert.NoError(t, err)
	assert.Len(t, results, 2) // one propose per iteration
	assert.Equal(t, 7, mock.CallCount())
}

func TestConvergenceLoopForcedAfterMaxRounds(t *testing.T) {
	// 5 iterations of NOT_CONVERGED — should force-exit at iteration 4 (maxRounds-2=3).
	var responses []MockResponse
	for i := 0; i < 5; i++ {
		responses = append(responses,
			mockResp(fmt.Sprintf("proposal iteration %d", i+1)),
			mockResp("NOT_CONVERGED\n- still issues"),
		)
	}
	mock := NewMockLLM(responses...)

	wf := &Workflow{
		Rounds:    []WorkflowStep{{ID: "propose", Agent: "planner"}},
		MaxRounds: 5,
	}

	defs := map[string]AgentDef{
		"planner":     {ID: "planner", SystemPrompt: "You are the planner."},
		"convergence": {ID: "convergence", SystemPrompt: "Assess convergence."},
	}

	exec := &WorkflowExecutor{LLM: mock, AgentDefs: defs, Workflow: wf}
	results, err := exec.Run(context.Background(), "Design X.")
	assert.NoError(t, err)
	// Should have forced exit, not run all 5 full iterations.
	assert.LessOrEqual(t, len(results), 5)
}

func TestParseConvergenceResponse(t *testing.T) {
	// CONVERGED
	v := parseConvergenceResponse("CONVERGED: everything looks good", 2)
	assert.True(t, v.Converged)

	// NOT_CONVERGED with issues
	v = parseConvergenceResponse("NOT_CONVERGED\n- auth is missing\n- no tests", 1)
	assert.False(t, v.Converged)
	assert.Len(t, v.OpenIssues, 2)
	assert.Equal(t, "auth is missing", v.OpenIssues[0])

	// CYCLING
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
