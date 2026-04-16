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
						StrategyID:  "strat-go",
						Description: "Set up Go project structure",
					},
					{
						ID:          "step-2",
						Order:       2,
						StrategyID:  "strat-go",
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

// setupPlannerFS creates a MemFS pre-populated with council agent definitions
// and a standard workflow.yaml, matching the scaffold layout under .borg/.
func setupPlannerFS(t *testing.T) *specio.MemFS {
	t.Helper()

	fs := specio.NewMemFS()

	// Create directory structure.
	assert.NoError(t, fs.MkdirAll(".borg/council/agents", 0o755))
	assert.NoError(t, fs.MkdirAll(".borg/council", 0o755))
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
		assert.NoError(t, fs.WriteFile(".borg/council/agents/"+name, []byte(content), 0o644))
	}

	// Standard council workflow.
	wfYAML := `rounds:
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
	assert.NoError(t, fs.WriteFile(".borg/council/workflow.yaml", []byte(wfYAML), 0o644))

	return fs
}

// setupPlannerFSWithSpecialists extends setupPlannerFS with specialist steps
// and agent definitions for test_architect and schema_designer.
func setupPlannerFSWithSpecialists(t *testing.T) *specio.MemFS {
	t.Helper()

	fs := setupPlannerFS(t)

	// Additional specialist agent definitions.
	specialists := map[string]string{
		"test_architect.md": `---
id: test_architect
role: specialist
temperature: 0.4
---
You are the test architect. Design test strategies and define acceptance
criteria for each workstream step.
`,
		"schema_designer.md": `---
id: schema_designer
role: specialist
temperature: 0.4
---
You are the schema designer. Define data models, API contracts, and
interface types that enable parallel workstreams.
`,
	}
	for name, content := range specialists {
		assert.NoError(t, fs.WriteFile(".borg/council/agents/"+name, []byte(content), 0o644))
	}

	// Workflow with specialist steps. Specialists run conditionally when the
	// proposed spec mentions "schema" or "test".
	wfYAML := `rounds:
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
  - id: specialist_schema
    agent: schema_designer
    parallel: false
    depends_on: [challenge]
    conditional: schema
  - id: specialist_test
    agent: test_architect
    parallel: false
    depends_on: [challenge]
    conditional: test
  - id: revise
    agent: planner
    parallel: false
    depends_on: [research, specialist_schema, specialist_test]
  - id: record
    agent: historian
    parallel: false
    depends_on: [revise]
max_rounds: 5
`
	assert.NoError(t, fs.WriteFile(".borg/council/workflow.yaml", []byte(wfYAML), 0o644))

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

	mock := NewMockLLM(
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
	assert.Equal(t, "strat-go", ws.Steps[0].StrategyID)
	assert.Equal(t, "Set up Go project structure", ws.Steps[0].Description)

	assert.Equal(t, "step-2", ws.Steps[1].ID)
	assert.Equal(t, 2, ws.Steps[1].Order)
	assert.Equal(t, "Implement auth handlers", ws.Steps[1].Description)
}

// TestPlanWithSpecialists verifies that specialist agents (test_architect,
// schema_designer) are invoked when their conditionals fire, and that their
// outputs feed into the revise step.
func TestPlanWithSpecialists(t *testing.T) {
	fs := setupPlannerFSWithSpecialists(t)
	planJSON := sampleMasterPlanJSON(t)

	// The propose output mentions "schema" and "test" to trigger both specialists.
	proposeOutput := planJSON[:len(planJSON)-1] + `,"notes":"need schema design and test strategy"}`

	mock := NewMockLLM(
		// propose: planner — mentions schema and test to trigger conditionals
		mockResp(proposeOutput),
		// challenge: critic + stakeholder (parallel)
		mockResp("CONVERGED: looks good"),
		mockResp("CONVERGED: aligned with goals"),
		// research: skipped (no "open_questions")
		// specialist_schema: fires because propose mentions "schema"
		mockResp("Schema: User(id, email, password_hash) with sessions table"),
		// specialist_test: fires because propose mentions "test"
		mockResp("Test strategy: integration tests for auth endpoints, unit tests for JWT"),
		// revise: planner revises with specialist input
		mockResp(planJSON),
		// record: historian
		mockResp("Decision recorded with schema and test notes"),
		// convergence check
		mockResp("CONVERGED"),
		// readiness: critic + stakeholder
		mockResp("APPROVED"),
		mockResp("APPROVED"),
	)

	req := PlanRequest{
		Prompt:    "Build user auth with schema and test coverage",
		GoalsBody: "# Goals\n\nFull auth system.",
	}

	plan, err := Plan(context.Background(), mock, fs, req)
	assert.NoError(t, err)
	assert.NotNil(t, plan)

	// Verify specialists actually ran by checking total call count.
	// Without specialists: 5 workflow + 3 orchestration = 8
	// With 2 specialists: 7 workflow + 3 orchestration = 10
	assert.Equal(t, 10, mock.CallCount())

	// Plan should still be valid.
	assert.Equal(t, "plan-001", plan.ID)
	assert.Len(t, plan.Workstreams, 1)
}

// TestPlanMissingCouncil verifies that Plan returns an error when the council
// agent definitions directory is missing from the filesystem.
func TestPlanMissingCouncil(t *testing.T) {
	// Empty MemFS — no .borg directory at all.
	fs := specio.NewMemFS()

	mock := NewMockLLM() // no responses needed — should fail before LLM calls

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

	mock := NewMockLLM(
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
