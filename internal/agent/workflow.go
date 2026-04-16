package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

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

// WorkflowExecutor runs the council workflow using a typed PlanningState
// blackboard. The orchestrator goroutine owns the state exclusively; parallel
// agents receive read-only snapshots and return results via channels.
type WorkflowExecutor struct {
	LLM       LLM
	AgentDefs map[string]AgentDef
	Workflow  *Workflow
	Events    chan WorkflowEvent // optional; nil disables progress reporting
}

// emit sends a workflow event if the Events channel is set.
func (e *WorkflowExecutor) emit(stepID, agentID, status, message string) {
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
			// Non-blocking: drop event if channel is full.
		}
	}
}

// executionRetryConfig returns a retry config for workflow agent calls.
func executionRetryConfig() RetryConfig {
	return RetryConfig{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    2 * time.Second,
		MaxAttempts: 3,
	}
}

// executeAgent runs a single agent against a state snapshot and returns the result.
// This is safe to call from multiple goroutines — it reads only from the snapshot
// and the immutable AgentDef, and writes only to its own local RoundResult.
func (e *WorkflowExecutor) executeAgent(ctx context.Context, stepID, agentID string, snap StateSnapshot) RoundResult {
	def, ok := e.AgentDefs[agentID]
	if !ok {
		return RoundResult{StepID: stepID, AgentID: agentID, Err: fmt.Errorf("agent %q not found", agentID)}
	}

	e.emit(stepID, agentID, "started", "")

	// Project state to agent-relevant messages, then build the full request.
	messages := ProjectState(stepID, snap)
	req := BuildGenerateRequest(def, messages)

	resp, err := GenerateWithRetry(ctx, e.LLM, req, executionRetryConfig())
	if err != nil {
		e.emit(stepID, agentID, "error", err.Error())
		return RoundResult{StepID: stepID, AgentID: agentID, Err: err}
	}

	e.emit(stepID, agentID, "completed", "")
	return RoundResult{StepID: stepID, AgentID: agentID, Output: resp.Content}
}

// shouldRunConditional checks whether a conditional step should execute.
// It scans the state for the conditional keyword.
func shouldRunConditional(cond string, state *PlanningState) bool {
	lower := strings.ToLower(cond)

	// Check in proposed spec, revisions, and concerns.
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

// ExecuteRound runs a single workflow step against the current state.
// For parallel steps, agents receive read-only snapshots and execute
// concurrently; results are collected and returned to the caller for merging.
func (e *WorkflowExecutor) ExecuteRound(ctx context.Context, step WorkflowStep, state *PlanningState) ([]RoundResult, error) {
	// Check conditional.
	if step.Conditional != "" {
		if !shouldRunConditional(step.Conditional, state) {
			e.emit(step.ID, "", "skipped", fmt.Sprintf("conditional %q not met", step.Conditional))
			return nil, nil
		}
	}

	// Determine agents to run.
	agents := step.Agents
	if len(agents) == 0 && step.Agent != "" {
		agents = []string{step.Agent}
	}
	if len(agents) == 0 {
		return nil, nil
	}

	// Take a snapshot — this is the read-only view agents will use.
	snap := state.Snapshot()

	// Parallel execution: goroutines per agent, results collected via slice.
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

	// Sequential execution.
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

// mergeResults applies round results back into the planning state based on
// the step ID. This is the only place state is mutated, and it runs
// sequentially in the orchestrator goroutine — no concurrent access.
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
				Severity: "medium", // TODO: parse from structured output
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

// Run executes the full workflow as an iterative convergence loop. Each
// iteration runs the workflow rounds (propose → challenge → research → revise
// → record), then the convergence monitor decides whether to loop or stop.
// After convergence, a readiness gate (critic + stakeholder approval) must
// pass. Max iterations is Workflow.MaxRounds; forced after 3 rounds on the
// same concern.
//
// The orchestrator owns PlanningState exclusively — parallel agents receive
// snapshots, and results are merged back sequentially.
func (e *WorkflowExecutor) Run(ctx context.Context, initialPrompt string) ([]RoundResult, error) {
	state := &PlanningState{
		Prompt: initialPrompt,
		Round:  1,
	}

	maxRounds := e.Workflow.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 5
	}

	var allResults []RoundResult

	for iteration := 0; iteration < maxRounds; iteration++ {
		e.emit("", "", "started", fmt.Sprintf("iteration %d/%d", iteration+1, maxRounds))

		// Execute all workflow rounds for this iteration.
		for _, step := range e.Workflow.Rounds {
			results, err := e.ExecuteRound(ctx, step, state)
			if err != nil {
				return allResults, err
			}

			mergeResults(state, step.ID, results)
			allResults = append(allResults, results...)
		}
		state.Round++

		// Convergence check — requires the "convergence" agent def.
		monitorDef, hasMonitor := e.AgentDefs["convergence"]
		if !hasMonitor {
			// No convergence monitor configured — single pass, done.
			break
		}

		verdict, err := CheckConvergence(ctx, e.LLM, monitorDef, state)
		if err != nil {
			return allResults, fmt.Errorf("convergence check: %w", err)
		}

		e.emit("convergence", "convergence", "completed", verdict.Reasoning)

		if verdict.Converged {
			// Readiness gate: critic + stakeholder approve.
			ready, err := CheckReadiness(ctx, e.LLM, e.AgentDefs, state)
			if err != nil {
				return allResults, fmt.Errorf("readiness gate: %w", err)
			}
			if ready {
				break
			}
			// Blocked — carry open issues into next iteration.
			e.emit("readiness", "", "blocked", "readiness gate rejected; looping")
			continue
		}

		// Not converged — carry open issues forward.
		state.OpenConcerns = verdict.OpenIssues

		// Force decision after MaxRounds-2 rounds on the same concern (plan: 3 rounds).
		if iteration >= maxRounds-2 {
			e.emit("convergence", "convergence", "forced", "forcing decision after repeated rounds")
			break
		}

		// Clear per-round state for next iteration (keep cumulative state).
		state.Concerns = nil
		state.ResearchResults = nil
	}

	return allResults, nil
}
