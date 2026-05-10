package agent

import (
	"context"
	"fmt"
	"time"
)

// RunCouncil drives the multi-iteration convergence loop the council
// workflows depend on. The generic WorkflowExecutor[S].Run is a single
// DAG pass; RunCouncil wraps it with the iteration accounting,
// convergence-monitor check, and readiness gate the council needs.
//
// State ownership: the caller constructs the *PlanningState (with
// Prompt, Round=1, Existing populated) and passes it in. RunCouncil
// mutates it across iterations — bumping Round, setting OpenConcerns
// from convergence verdicts, and clearing per-iteration Concerns /
// ResearchResults between rounds. After Run returns, the caller can
// read the final state through the same pointer.
//
// The convergence loop is gated on whether an agent named "convergence"
// is registered. Workflows that don't need the loop (assimilation,
// spec-generation) can either omit the convergence agent and call
// Run() directly for one pass, or pass through here and exit after
// iteration 1.
func RunCouncil(ctx context.Context, exec *WorkflowExecutor[PlanningState], state *PlanningState) ([]RoundResult, error) {
	maxRounds := exec.Workflow.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 5
	}

	var allResults []RoundResult

	for iteration := 0; iteration < maxRounds; iteration++ {
		if exec.Events != nil {
			exec.Events <- WorkflowEvent{
				Status:    "started",
				Message:   fmt.Sprintf("iteration %d/%d", iteration+1, maxRounds),
				Timestamp: time.Now(),
			}
		}

		results, err := exec.Run(ctx, state)
		allResults = append(allResults, results...)
		if err != nil {
			return allResults, err
		}
		state.Round++

		monitorDef, hasMonitor := exec.AgentDefs["convergence"]
		if !hasMonitor {
			break
		}

		verdict, err := CheckConvergence(ctx, exec.Executor, monitorDef, state)
		if err != nil {
			return allResults, fmt.Errorf("convergence check: %w", err)
		}

		if exec.Events != nil {
			exec.Events <- WorkflowEvent{
				StepID:    "convergence",
				AgentID:   "convergence",
				Status:    "completed",
				Message:   verdict.Reasoning,
				Timestamp: time.Now(),
			}
		}

		if verdict.Converged {
			ready, err := CheckReadiness(ctx, exec.Executor, exec.AgentDefs, state)
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

	return allResults, nil
}
