package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/chetan/locutus/internal/executor"
	"github.com/chetan/locutus/internal/specio"
)

// WorkflowStep defines a single step in the council workflow.
type WorkflowStep struct {
	ID          string   `yaml:"id"`
	Agent       string   `yaml:"agent,omitempty"`
	Agents      []string `yaml:"agents,omitempty"`
	Parallel    bool     `yaml:"parallel"`
	DependsOn   []string `yaml:"depends_on,omitempty"`
	Conditional string   `yaml:"conditional,omitempty"`
}

// Workflow defines the full council workflow DAG.
type Workflow struct {
	Rounds    []WorkflowStep `yaml:"rounds"`
	MaxRounds int            `yaml:"max_rounds"`
}

// LoadWorkflow reads and parses a workflow.yaml from the FS.
func LoadWorkflow(fsys specio.FS, path string) (*Workflow, error) {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading workflow %q: %w", path, err)
	}

	var wf Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parsing workflow %q: %w", path, err)
	}

	return &wf, nil
}

// RoundResult holds the output of executing one round.
type RoundResult struct {
	StepID  string
	AgentID string
	Output  string
	Err     error
}

// WorkflowExecutor runs the council workflow using the generic DAG executor
// with a typed PlanningState blackboard.
type WorkflowExecutor struct {
	LLM       LLM
	AgentDefs map[string]AgentDef
	Workflow  *Workflow
	Events    chan WorkflowEvent // optional; nil disables progress reporting
}

// executionRetryConfig returns a retry config for workflow agent calls.
func executionRetryConfig() RetryConfig {
	return RetryConfig{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    2 * time.Second,
		MaxAttempts: 3,
	}
}

// emitEvent sends a workflow event non-blocking. Safe for concurrent use.
func (e *WorkflowExecutor) emitEvent(stepID, agentID, status, message string) {
	if e.Events != nil {
		select {
		case e.Events <- WorkflowEvent{
			StepID:    stepID,
			AgentID:   agentID,
			Status:    status,
			Message:   message,
			Timestamp: time.Now(),
		}:
		default:
		}
	}
}

// executeAgent runs a single agent against a state snapshot. Safe for
// concurrent use — reads only from the snapshot and immutable AgentDef.
func (e *WorkflowExecutor) executeAgent(ctx context.Context, stepID, agentID string, snap StateSnapshot) RoundResult {
	def, ok := e.AgentDefs[agentID]
	if !ok {
		return RoundResult{StepID: stepID, AgentID: agentID, Err: fmt.Errorf("agent %q not found", agentID)}
	}

	e.emitEvent(stepID, agentID, "started", "")

	messages := ProjectState(stepID, snap)
	req := BuildGenerateRequest(def, messages)

	resp, err := GenerateWithRetry(ctx, e.LLM, req, executionRetryConfig())
	if err != nil {
		e.emitEvent(stepID, agentID, "error", err.Error())
		return RoundResult{StepID: stepID, AgentID: agentID, Err: err}
	}

	e.emitEvent(stepID, agentID, "completed", "")
	return RoundResult{StepID: stepID, AgentID: agentID, Output: resp.Content}
}

// ExecuteRound runs a single workflow step against the current state. For
// parallel multi-agent steps, agents run concurrently with the same snapshot.
func (e *WorkflowExecutor) ExecuteRound(ctx context.Context, step WorkflowStep, state *PlanningState) ([]RoundResult, error) {
	// Check conditional.
	if step.Conditional != "" {
		if !shouldRunConditional(step.Conditional, state) {
			return nil, nil
		}
	}

	agents := step.Agents
	if len(agents) == 0 && step.Agent != "" {
		agents = []string{step.Agent}
	}
	if len(agents) == 0 {
		return nil, nil
	}

	snap := state.Snapshot()

	// Parallel multi-agent execution.
	if step.Parallel && len(agents) > 1 {
		results := make([]RoundResult, len(agents))
		var wg sync.WaitGroup
		wg.Add(len(agents))
		for i, agentID := range agents {
			go func(idx int, aid string) {
				defer wg.Done()
				results[idx] = e.executeAgent(ctx, step.ID, aid, snap)
			}(i, agentID)
		}
		wg.Wait()

		for _, r := range results {
			if r.Err != nil {
				return results, r.Err
			}
		}
		return results, nil
	}

	// Sequential.
	var results []RoundResult
	for _, agentID := range agents {
		r := e.executeAgent(ctx, step.ID, agentID, snap)
		results = append(results, r)
		if r.Err != nil {
			return results, r.Err
		}
	}
	return results, nil
}

// shouldRunConditional checks whether a conditional step should execute by
// scanning the state for the keyword.
func shouldRunConditional(cond string, state *PlanningState) bool {
	lower := strings.ToLower(cond)

	if strings.Contains(strings.ToLower(state.ProposedSpec), lower) {
		return true
	}
	if strings.Contains(strings.ToLower(state.Revisions), lower) {
		return true
	}
	for _, c := range state.Concerns {
		if strings.Contains(strings.ToLower(c.Text), lower) {
			return true
		}
	}
	return false
}

// mergeResults applies round results back into the planning state. Called
// sequentially by the DAG executor — never concurrently.
func mergeResults(state *PlanningState, stepID string, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		switch stepID {
		case "propose":
			state.ProposedSpec = r.Output
		case "challenge":
			state.Concerns = append(state.Concerns, Concern{
				AgentID:  r.AgentID,
				Severity: "medium",
				Text:     r.Output,
			})
		case "research":
			state.ResearchResults = append(state.ResearchResults, Finding{
				Query:  "council concern investigation",
				Result: r.Output,
			})
		case "revise":
			state.Revisions = r.Output
		case "record":
			state.Record = r.Output
		}
	}
}

// Run executes the full council workflow using the generic DAG executor.
// The outer convergence loop and readiness gate are handled here; the inner
// DAG execution (dependency ordering, parallelism) is delegated to executor.Executor.
func (e *WorkflowExecutor) Run(ctx context.Context, initialPrompt string) ([]RoundResult, error) {
	state := &PlanningState{
		Prompt: initialPrompt,
		Round:  1,
	}

	maxRounds := e.Workflow.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 5
	}

	// Build executor.Steps from WorkflowSteps.
	dagSteps := make([]executor.Step, len(e.Workflow.Rounds))
	stepLookup := make(map[string]WorkflowStep, len(e.Workflow.Rounds))
	for i, ws := range e.Workflow.Rounds {
		stepLookup[ws.ID] = ws
		ds := executor.Step{
			ID:        ws.ID,
			DependsOn: ws.DependsOn,
			Parallel:  false, // parallelism is within a step (multi-agent), not between steps
		}
		if ws.Conditional != "" {
			cond := ws.Conditional // capture for closure
			ds.Conditional = func(s any) bool {
				return shouldRunConditional(cond, s.(*PlanningState))
			}
		}
		dagSteps[i] = ds
	}

	// Accumulate results across convergence iterations.
	var allResults []RoundResult

	// Bridge DAG events to WorkflowEvents.
	var dagEvents chan executor.Event
	var bridgeDone chan struct{}
	if e.Events != nil {
		dagEvents = make(chan executor.Event, 50)
		bridgeDone = make(chan struct{})
		go func() {
			defer close(bridgeDone)
			for evt := range dagEvents {
				select {
				case e.Events <- WorkflowEvent{
					StepID:    evt.StepID,
					Status:    evt.Status,
					Message:   evt.Message,
					Timestamp: evt.Timestamp,
				}:
				default:
				}
			}
		}()
	}

	for iteration := 0; iteration < maxRounds; iteration++ {
		if e.Events != nil {
			e.Events <- WorkflowEvent{
				Status:    "started",
				Message:   fmt.Sprintf("iteration %d/%d", iteration+1, maxRounds),
				Timestamp: time.Now(),
			}
		}

		cfg := executor.Config[PlanningState]{
			Steps: dagSteps,
			RunStep: func(ctx context.Context, step executor.Step, snap PlanningState) (executor.StepResult, error) {
				ws := stepLookup[step.ID]
				results, err := e.ExecuteRound(ctx, ws, &snap)
				return executor.StepResult{Output: results}, err
			},
			Merge: func(s *PlanningState, r executor.StepResult) {
				if results, ok := r.Output.([]RoundResult); ok {
					mergeResults(s, r.StepID, results)
				}
			},
			Snapshot: func(s *PlanningState) PlanningState { return *s },
			Events:   dagEvents,
		}

		executor := executor.NewExecutor(cfg)
		dagResults, err := executor.Run(ctx, state)
		if err != nil {
			return allResults, err
		}

		// Flatten dag results into RoundResults.
		for _, dr := range dagResults {
			if results, ok := dr.Output.([]RoundResult); ok {
				allResults = append(allResults, results...)
			}
		}
		state.Round++

		// Convergence check.
		monitorDef, hasMonitor := e.AgentDefs["convergence"]
		if !hasMonitor {
			break
		}

		verdict, err := CheckConvergence(ctx, e.LLM, monitorDef, state)
		if err != nil {
			return allResults, fmt.Errorf("convergence check: %w", err)
		}

		if e.Events != nil {
			e.Events <- WorkflowEvent{
				StepID:  "convergence",
				AgentID: "convergence",
				Status:  "completed",
				Message: verdict.Reasoning,
				Timestamp: time.Now(),
			}
		}

		if verdict.Converged {
			ready, err := CheckReadiness(ctx, e.LLM, e.AgentDefs, state)
			if err != nil {
				return allResults, fmt.Errorf("readiness gate: %w", err)
			}
			if ready {
				break
			}
			continue
		}

		state.OpenConcerns = verdict.OpenIssues

		if iteration >= maxRounds-2 {
			break
		}

		state.Concerns = nil
		state.ResearchResults = nil
	}

	if dagEvents != nil {
		close(dagEvents)
		<-bridgeDone // wait for bridge goroutine to drain
	}

	return allResults, nil
}
