package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
func Plan(ctx context.Context, exec AgentExecutor, fsys specio.FS, req PlanRequest) (*spec.MasterPlan, error) {
	// 1. Load agent definitions.
	defs, err := LoadAgentDefs(fsys, ".borg/agents")
	if err != nil {
		return nil, fmt.Errorf("loading agents: %w", err)
	}

	// 2. Build agent defs map keyed by ID.
	agentDefs := make(map[string]AgentDef, len(defs))
	for _, d := range defs {
		agentDefs[d.ID] = d
	}

	// 3. Create workflow executor.
	executor := &WorkflowExecutor[PlanningState]{
		Executor:  exec,
		AgentDefs: agentDefs,
		Workflow:  PlanningWorkflow,
	}

	// 5. Build a contextualized prompt that includes spec state.
	prompt := buildPlanPrompt(req)

	// 6. Run the workflow with the council convergence wrapper.
	state := &PlanningState{Prompt: prompt, Round: 1}
	results, err := RunCouncil(ctx, executor, state)
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
	now := time.Now()
	evt := history.Event{
		ID:        history.EventID("plan-created", plan.ID, now),
		Timestamp: now,
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

// buildPlanPrompt constructs the planner's user prompt. Existing-spec
// context flows through the `spec_list_manifest` / `spec_get` tools
// (DJ-094, DJ-115) rather than inline bullet lists — the v2 approach
// the prior inline comment called for. The planner's prompt body
// covers tool usage; this builder emits a one-line data-state flag so
// the agent knows the tools will return non-empty results, and omits
// the flag on greenfield runs.
func buildPlanPrompt(req PlanRequest) string {
	var b strings.Builder

	b.WriteString(req.Prompt)
	b.WriteString("\n")

	if req.GoalsBody != "" {
		b.WriteString("\n## GOALS.md\n")
		b.WriteString(req.GoalsBody)
		b.WriteString("\n")
	}

	if len(req.Features)+len(req.Decisions)+len(req.Strategies) > 0 {
		b.WriteString("\n## Existing spec is present\n\nA persisted spec snapshot exists at `.borg/spec/`; the `spec_list_manifest` and `spec_get` tools will return non-empty results. Call `spec_list_manifest` first to scan ids + summaries; call `spec_get(id)` only for nodes whose detail you need. (On greenfield runs this section is omitted.)\n")
	}

	return b.String()
}
