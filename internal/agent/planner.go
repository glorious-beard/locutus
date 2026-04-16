package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
)

// PlanRequest holds inputs for the greenfield planning pipeline.
type PlanRequest struct {
	Prompt     string
	GoalsBody  string
	Features   []spec.Feature
	Decisions  []spec.Decision
	Strategies []spec.Strategy
}

// Plan runs the full greenfield planning pipeline.
func Plan(ctx context.Context, llm LLM, fsys specio.FS, req PlanRequest) (*spec.MasterPlan, error) {
	// 1. Load agent definitions.
	defs, err := LoadAgentDefs(fsys, ".borg/agents")
	if err != nil {
		return nil, fmt.Errorf("loading agents: %w", err)
	}

	// 2. Load workflow.
	wf, err := LoadWorkflow(fsys, ".borg/workflows/planning.yaml")
	if err != nil {
		return nil, fmt.Errorf("loading workflow: %w", err)
	}

	// 3. Build agent defs map keyed by ID.
	agentDefs := make(map[string]AgentDef, len(defs))
	for _, d := range defs {
		agentDefs[d.ID] = d
	}

	// 4. Create workflow executor.
	executor := &WorkflowExecutor{
		LLM:       llm,
		AgentDefs: agentDefs,
		Workflow:  wf,
	}

	// 5. Run the workflow.
	results, err := executor.Run(ctx, req.Prompt)
	if err != nil {
		return nil, fmt.Errorf("workflow execution: %w", err)
	}

	// 6. Extract the MasterPlan from the last planner output.
	// Look for the last "propose" or "revise" step result from the planner agent.
	var planJSON string
	for i := len(results) - 1; i >= 0; i-- {
		r := results[i]
		if r.Err != nil || r.Output == "" {
			continue
		}
		if r.StepID == "revise" || r.StepID == "propose" {
			planJSON = r.Output
			break
		}
	}

	if planJSON == "" {
		return nil, fmt.Errorf("no planner output found in workflow results")
	}

	var plan spec.MasterPlan
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return nil, fmt.Errorf("parsing master plan JSON: %w", err)
	}

	// 7. Record history event.
	h := history.NewHistorian(fsys, ".borg/history")
	evt := history.Event{
		ID:        fmt.Sprintf("evt-%s-%d", plan.ID, time.Now().UnixNano()),
		Timestamp: time.Now(),
		Kind:      "plan_created",
		TargetID:  plan.ID,
		NewValue:  planJSON,
		Rationale: "Greenfield planning pipeline completed",
	}
	if err := h.Record(evt); err != nil {
		return nil, fmt.Errorf("recording history event: %w", err)
	}

	return &plan, nil
}
