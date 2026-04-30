package agent

import (
	"context"
	"encoding/json"
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
	Conditional string   `yaml:"conditional,omitempty"` // condition tag: "has_concerns", "has_open_questions", or custom keyword
	MergeAs     string   `yaml:"merge_as,omitempty"`    // state field to merge into: "proposed_spec", "concerns", "research", "revisions", "record"
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

// emitEvent sends a workflow event. Blocks if the channel is full —
// dropping council events would silently desynchronise any UI built on
// top, and the channel is sized generously by the caller (see
// GenerateSpec). Safe for concurrent use.
func (e *WorkflowExecutor) emitEvent(stepID, agentID, status, message string) {
	if e.Events == nil {
		return
	}
	e.Events <- WorkflowEvent{
		StepID:    stepID,
		AgentID:   agentID,
		Status:    status,
		Message:   message,
		Timestamp: time.Now(),
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

// shouldRunConditional checks whether a conditional step should execute.
// Supports typed condition tags and falls back to keyword presence in state.
func shouldRunConditional(cond string, state *PlanningState) bool {
	// Typed conditions checked first.
	switch cond {
	case "has_concerns":
		return len(state.Concerns) > 0
	case "has_open_questions":
		return state.HasOpenConcerns()
	case "has_proposed_spec":
		return state.ProposedSpec != ""
	case "has_revisions":
		return state.Revisions != ""
	}

	// Fallback: keyword presence scan for custom/legacy conditionals.
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

// mergeResults applies round results back into the planning state using the
// step's MergeAs field. Falls back to stepID-based matching for backward
// compatibility with workflows that don't declare merge_as.
func mergeResults(state *PlanningState, step WorkflowStep, results []RoundResult) {
	mergeKey := step.MergeAs
	if mergeKey == "" {
		mergeKey = step.ID // fallback: use step ID
	}

	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		switch mergeKey {
		case "proposed_spec", "propose":
			state.ProposedSpec = r.Output
		case "concerns", "challenge":
			state.Concerns = append(state.Concerns, Concern{
				AgentID:  r.AgentID,
				Severity: "medium",
				Text:     r.Output,
			})
		case "critic_issues", "critique":
			// Each critic emits CriticIssues JSON. Parse and flatten:
			// one Concern per issue string, attributed to the critic
			// that raised it. This makes the downstream revise prompt
			// readable instead of dumping raw JSON.
			var ci CriticIssues
			if err := json.Unmarshal([]byte(r.Output), &ci); err != nil {
				// Fallback: store the raw output as one concern so we
				// don't lose the critic's contribution entirely.
				state.Concerns = append(state.Concerns, Concern{
					AgentID:  r.AgentID,
					Severity: "medium",
					Text:     r.Output,
				})
				continue
			}
			for _, issue := range ci.Issues {
				state.Concerns = append(state.Concerns, Concern{
					AgentID:  r.AgentID,
					Severity: "medium",
					Text:     issue,
				})
			}
		case "research":
			state.ResearchResults = append(state.ResearchResults, Finding{
				Query:  "investigation",
				Result: r.Output,
			})
		case "revisions", "revise":
			state.Revisions = r.Output
		case "record":
			state.Record = r.Output
		case "scout_brief", "survey":
			state.ScoutBrief = r.Output
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
			// Steps are sequential at the DAG level — the executor resolves
			// dependency ordering. Multi-agent parallelism (e.g. critic +
			// stakeholder running concurrently) is handled inside ExecuteRound,
			// which fans out goroutines per agent within a single step.
			// These are distinct concerns: step ordering vs. agent fan-out.
			//
			// TODO: if step-level parallelism is needed (e.g. two independent
			// council rounds running in parallel), set Parallel based on the
			// workflow step and ensure ExecuteRound is reentrant-safe.
			Parallel: false,
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

	// Bridge DAG events to WorkflowEvents. The cleanup func is deferred so
	// every return path joins the goroutine — earlier hand-written cleanup
	// only ran on the success path and leaked on convergence/readiness errors.
	dagEvents, stopBridge := e.startEventBridge()
	defer stopBridge()

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
					ws := stepLookup[r.StepID]
					mergeResults(s, ws, results)
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

	return allResults, nil
}

// startEventBridge spawns a goroutine that forwards executor events as
// WorkflowEvents to e.Events. Returns the channel the executor should write
// to and a cleanup func that closes the channel and waits for the goroutine
// to drain. When e.Events is nil, both returns are no-ops.
func (e *WorkflowExecutor) startEventBridge() (chan executor.Event, func()) {
	if e.Events == nil {
		return nil, func() {}
	}
	dagEvents := make(chan executor.Event, 50)
	done := make(chan struct{})
	go func() {
		defer close(done)
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
	return dagEvents, func() {
		close(dagEvents)
		<-done
	}
}
