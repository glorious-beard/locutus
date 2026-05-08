package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chetan/locutus/internal/history"
	"github.com/chetan/locutus/internal/spec"
	"github.com/chetan/locutus/internal/specio"
	"github.com/stretchr/testify/assert"
)

// sampleMasterPlan returns a MasterPlan suitable for JSON-encoding in mock
// LLM responses.
func sampleMasterPlan() spec.MasterPlan {
	return spec.MasterPlan{
		ID:          "plan-001",
		Version:     1,
		ProjectRoot: ".",
		Prompt:      "Build user auth",
		TriggerKind: spec.PlanActionInit,
		Workstreams: []spec.Workstream{
			{
				ID:             "ws-backend",
				StrategyDomain: "backend",
				DetailLevel:    spec.DetailLevelHigh,
				Steps: []spec.PlanStep{
					{
						ID:          "step-1",
						Order:       1,
						ApproachID:  "strat-go",
						Description: "Set up Go project structure",
					},
					{
						ID:          "step-2",
						Order:       2,
						ApproachID:  "strat-go",
						Description: "Implement auth handlers",
					},
				},
			},
		},
		Summary: "Authentication feature plan",
	}
}

// sampleMasterPlanJSON returns the JSON encoding of sampleMasterPlan.
func sampleMasterPlanJSON(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(sampleMasterPlan())
	assert.NoError(t, err)
	return string(data)
}

// setupPlannerFS creates a MemFS pre-populated with agent definitions,
// matching the scaffold layout under .borg/. The planning workflow is
// defined in code (PlanningWorkflow) and does not need on-disk seeding.
func setupPlannerFS(t *testing.T) *specio.MemFS {
	t.Helper()

	fs := specio.NewMemFS()

	// Create directory structure.
	assert.NoError(t, fs.MkdirAll(".borg/agents", 0o755))
	assert.NoError(t, fs.MkdirAll(".borg/history", 0o755))

	// Council agent definitions (YAML frontmatter + markdown body).
	agents := map[string]string{
		"planner.md": `---
id: planner
role: planner
temperature: 0.7
---
You are the planner agent. Produce a structured JSON MasterPlan from the project
prompt and spec context. Output valid JSON only.
`,
		"critic.md": `---
id: critic
role: critic
temperature: 0.3
---
You are the critic agent. Challenge proposals for risks, over-engineering,
and missing edge cases. Rate concerns as high/medium/low severity.
`,
		"stakeholder.md": `---
id: stakeholder
role: stakeholder
temperature: 0.3
---
You are the stakeholder agent. Validate that proposals align with project
goals, user needs, and business constraints.
`,
		"convergence.md": `---
id: convergence
role: convergence
temperature: 0.1
---
You are the convergence monitor. Assess whether the council has reached
agreement. Respond CONVERGED, NOT_CONVERGED, or CYCLING.
`,
		"historian.md": `---
id: historian
role: historian
temperature: 0.2
---
You are the historian agent. Record decisions made, alternatives considered,
and rationale for the decision journal.
`,
	}
	for name, content := range agents {
		assert.NoError(t, fs.WriteFile(".borg/agents/"+name, []byte(content), 0o644))
	}

	return fs
}

// TestPlanProducesValidMasterPlan verifies that Plan() orchestrates the full
// council workflow and returns a correctly-typed MasterPlan parsed from the
// planner's structured JSON output.
//
// Mock call order for single-pass convergence (research conditional skipped):
//   propose(1) + challenge(2) + revise(1) + record(1) = 5 workflow calls
//   + convergence(1) + readiness(2) = 3 orchestration calls
//   Total: 8 calls
//
// Note: research is conditional on "open_questions" and skipped because the
// critic/stakeholder responses do not contain that keyword.
func TestPlanProducesValidMasterPlan(t *testing.T) {
	fs := setupPlannerFS(t)
	planJSON := sampleMasterPlanJSON(t)

	mock := NewMockExecutor(
		// propose: planner outputs the JSON master plan
		mockResp(planJSON),
		// challenge: critic and stakeholder (parallel, but consumed in order)
		mockResp("CONVERGED: looks good"),
		mockResp("CONVERGED: aligned with goals"),
		// research: skipped (conditional "open_questions" not present)
		// revise: planner revises (uses same JSON since no real concerns)
		mockResp(planJSON),
		// record: historian records
		mockResp("Decision recorded"),
		// convergence check (called by outer loop after DAG completes)
		mockResp("CONVERGED"),
		// readiness gate: critic then stakeholder
		mockResp("APPROVED"),
		mockResp("APPROVED"),
	)

	req := PlanRequest{
		Prompt:    "Build user auth",
		GoalsBody: "# Goals\n\nAuthentication and authorization for all endpoints.",
		Features: []spec.Feature{
			{ID: "feat-auth", Title: "User Authentication", Status: spec.FeatureStatusProposed},
		},
		Decisions: []spec.Decision{
			{ID: "dec-jwt", Title: "Use JWT tokens", Status: spec.DecisionStatusActive},
		},
		Strategies: []spec.Strategy{
			{ID: "strat-go", Title: "Go backend", Kind: spec.StrategyKindFoundational},
		},
	}

	plan, err := Plan(context.Background(), mock, fs, req)
	assert.NoError(t, err)
	assert.NotNil(t, plan)

	// Verify top-level plan fields.
	assert.Equal(t, "plan-001", plan.ID)
	assert.Equal(t, 1, plan.Version)
	assert.Equal(t, ".", plan.ProjectRoot)
	assert.Equal(t, "Build user auth", plan.Prompt)
	assert.Equal(t, spec.PlanActionInit, plan.TriggerKind)
	assert.Equal(t, "Authentication feature plan", plan.Summary)

	// Verify workstreams.
	assert.Len(t, plan.Workstreams, 1)
	ws := plan.Workstreams[0]
	assert.Equal(t, "ws-backend", ws.ID)
	assert.Equal(t, "backend", ws.StrategyDomain)
	assert.Equal(t, spec.DetailLevelHigh, ws.DetailLevel)

	// Verify steps within the workstream.
	assert.Len(t, ws.Steps, 2)
	assert.Equal(t, "step-1", ws.Steps[0].ID)
	assert.Equal(t, 1, ws.Steps[0].Order)
	assert.Equal(t, "strat-go", ws.Steps[0].ApproachID)
	assert.Equal(t, "Set up Go project structure", ws.Steps[0].Description)

	assert.Equal(t, "step-2", ws.Steps[1].ID)
	assert.Equal(t, 2, ws.Steps[1].Order)
	assert.Equal(t, "Implement auth handlers", ws.Steps[1].Description)
}

// TestPlanMissingCouncil verifies that Plan returns an error when the council
// agent definitions directory is missing from the filesystem.
func TestPlanMissingCouncil(t *testing.T) {
	// Empty MemFS — no .borg directory at all.
	fs := specio.NewMemFS()

	mock := NewMockExecutor() // no responses needed — should fail before LLM calls

	req := PlanRequest{
		Prompt:    "Build user auth",
		GoalsBody: "# Goals\n\nAuth system.",
	}

	plan, err := Plan(context.Background(), mock, fs, req)
	assert.Error(t, err)
	assert.Nil(t, plan)
	assert.Equal(t, 0, mock.CallCount(), "no LLM calls should be made when council is missing")
}

// TestPlanRecordsHistory verifies that the Plan function records at least one
// history event after completing the planning pipeline.
func TestPlanRecordsHistory(t *testing.T) {
	fs := setupPlannerFS(t)
	planJSON := sampleMasterPlanJSON(t)

	mock := NewMockExecutor(
		mockResp(planJSON),                          // propose
		mockResp("CONVERGED: looks good"),           // challenge: critic
		mockResp("CONVERGED: aligned with goals"),   // challenge: stakeholder
		mockResp(planJSON),                          // revise
		mockResp("Decision recorded"),               // record
		mockResp("CONVERGED"),                       // convergence
		mockResp("APPROVED"),                        // readiness: critic
		mockResp("APPROVED"),                        // readiness: stakeholder
	)

	req := PlanRequest{
		Prompt:    "Build user auth",
		GoalsBody: "# Goals\n\nAuth.",
	}

	plan, err := Plan(context.Background(), mock, fs, req)
	assert.NoError(t, err)
	assert.NotNil(t, plan)

	// Verify history was recorded. The Plan function should use a Historian
	// to persist at least one event under .borg/history/.
	historian := history.NewHistorian(fs, ".borg/history")
	events, err := historian.Events()
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(events), 1, "Plan should record at least one history event")

	// The event should reference the plan.
	found := false
	for _, evt := range events {
		if evt.TargetID == plan.ID || evt.Kind == "plan_created" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a history event referencing the plan ID or kind plan_created")
}
