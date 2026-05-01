package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/chetan/locutus/internal/executor"
	"github.com/chetan/locutus/internal/specio"
)

// WorkflowStep defines a single step in the council workflow.
//
// Fanout, when set, names a slice on PlanningState (currently
// "outline.features" or "outline.strategies") whose elements drive
// per-element parallel agent calls — Phase 3's per-node elaborate.
// Each element is exposed to the agent's projection function via
// StateSnapshot.FanoutItem (raw JSON) so the projection can render
// the element-specific prompt. Set together with Parallel: true
// when calls should run concurrently (subject to per-model
// concurrency caps configured in models.yaml).
type WorkflowStep struct {
	ID          string   `yaml:"id"`
	Agent       string   `yaml:"agent,omitempty"`
	Agents      []string `yaml:"agents,omitempty"`
	Parallel    bool     `yaml:"parallel"`
	DependsOn   []string `yaml:"depends_on,omitempty"`
	Conditional string   `yaml:"conditional,omitempty"` // condition tag: "has_concerns", "has_open_questions", or custom keyword
	MergeAs     string   `yaml:"merge_as,omitempty"`    // state field to merge into: "proposed_spec", "concerns", "research", "revisions", "record"
	Fanout      string   `yaml:"fanout,omitempty"`      // Phase 3: dotted state path to a slice of items; one agent call per item
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

	// Existing, when non-nil, is threaded onto the workflow's PlanningState
	// for the spec_reconciler agent to match inline-decision clusters
	// against existing-spec decisions for ID reuse.
	Existing *ExistingSpec

	// LastState captures the workflow's final PlanningState. Populated by
	// Run after the DAG completes so callers (e.g. GenerateSpec) can
	// inspect the canonical ProposedSpec and the reconciler's conflict
	// actions for post-workflow cascade rewrites.
	LastState *PlanningState
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
//
// Emits three lifecycle events to the workflow events channel:
//
//   - "queued"    — the goroutine has been scheduled. The actual LLM
//                   call may be sitting in the per-model concurrency
//                   queue (models.yaml's concurrent_requests cap).
//   - "started"   — the call left the queue and is hitting the
//                   provider. Driven by an acquired-callback the LLM
//                   wrapper invokes after its semaphore acquire.
//   - "completed" — the call returned (success or final retry failure
//                   reported separately as "error").
//
// The cliSink renders "queued" with a distinct visual ("queued" prefix)
// and updates the same spinner to "running" on the started event, so
// the operator can tell waiting items from in-flight ones.
func (e *WorkflowExecutor) executeAgent(ctx context.Context, stepID, agentID string, snap StateSnapshot) RoundResult {
	def, ok := e.AgentDefs[agentID]
	if !ok {
		return RoundResult{StepID: stepID, AgentID: agentID, Err: fmt.Errorf("agent %q not found", agentID)}
	}

	e.emitEvent(stepID, agentID, "queued", "")
	ctx = WithAgentID(ctx, agentID)
	ctx = WithAcquiredCallback(ctx, func() {
		e.emitEvent(stepID, agentID, "started", "")
	})

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

	// Phase 3 fanout: spawn one agent invocation per element of the
	// named state slice. Each invocation gets its own snapshot with
	// FanoutItem populated so the agent's projection can render the
	// per-element prompt. Per-model concurrency caps in
	// LLM (models.yaml's concurrent_requests) bound the actual
	// parallelism — even with `parallel: true`, fanout never floods
	// a model past its configured slot count.
	if step.Fanout != "" {
		if len(agents) != 1 {
			return nil, fmt.Errorf("fanout step %q must declare exactly one agent (got %d)", step.ID, len(agents))
		}
		items, err := extractFanoutItems(state, step.Fanout)
		if err != nil {
			return nil, fmt.Errorf("fanout %s: %w", step.ID, err)
		}
		if len(items) == 0 {
			// No items to elaborate — return cleanly. Subsequent steps
			// see an empty merged result and treat it as no-op.
			return nil, nil
		}
		results := make([]RoundResult, len(items))
		// Each fanout call gets a per-item stepID so progress sinks
		// (cliSink spinners, MCP progress) surface one entry per item
		// instead of collapsing N parallel goroutines into a single
		// spinner. The merge handler keys on step.MergeAs, not the
		// per-item stepID, so this rename only affects observability.
		fanoutStepID := func(item string) string {
			id := fanoutItemID(item)
			if id == "" {
				return step.ID
			}
			return step.ID + " (" + id + ")"
		}
		if step.Parallel {
			var wg sync.WaitGroup
			wg.Add(len(items))
			for i, item := range items {
				go func(idx int, raw string) {
					defer wg.Done()
					itemSnap := snap
					itemSnap.FanoutItem = raw
					results[idx] = e.executeAgent(ctx, fanoutStepID(raw), agents[0], itemSnap)
				}(i, item)
			}
			wg.Wait()
		} else {
			for i, item := range items {
				itemSnap := snap
				itemSnap.FanoutItem = item
				results[i] = e.executeAgent(ctx, fanoutStepID(item), agents[0], itemSnap)
				if results[i].Err != nil {
					return results, results[i].Err
				}
			}
		}
		for _, r := range results {
			if r.Err != nil {
				return results, r.Err
			}
		}
		return results, nil
	}

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

// fanoutItemID extracts the `id` field from a fanout item's raw JSON
// for per-item event labeling. Returns empty when the item shape has
// no `id` (or the JSON is malformed); callers fall back to the bare
// step ID, which preserves correctness — the dedup-key collision is a
// progress-rendering issue, not a correctness one.
func fanoutItemID(rawJSON string) string {
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &v); err != nil {
		return ""
	}
	return v.ID
}

// extractFanoutItems resolves a dotted state path (e.g. "outline.features")
// to a slice of raw-JSON strings, one per element. Each returned string is
// the JSON encoding of a single element so the elaborator's projection can
// re-parse it into the right typed shape.
//
// Supported paths today: "outline.features", "outline.strategies". Adding
// new fanout sources means parsing the corresponding state field here —
// kept narrow to avoid hand-rolling a generic JSON-path resolver against
// the typed PlanningState struct.
func extractFanoutItems(state *PlanningState, path string) ([]string, error) {
	if state == nil {
		return nil, nil
	}
	switch path {
	case "outline.features", "outline.strategies":
		if strings.TrimSpace(state.Outline) == "" {
			return nil, nil
		}
		var outline Outline
		if err := json.Unmarshal([]byte(state.Outline), &outline); err != nil {
			return nil, fmt.Errorf("parse outline: %w", err)
		}
		var items []any
		if path == "outline.features" {
			for _, f := range outline.Features {
				items = append(items, f)
			}
		} else {
			for _, s := range outline.Strategies {
				items = append(items, s)
			}
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			data, err := json.Marshal(it)
			if err != nil {
				return nil, fmt.Errorf("marshal fanout item: %w", err)
			}
			out = append(out, string(data))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported fanout path %q (want outline.features or outline.strategies)", path)
	}
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
		case "outline":
			// Phase 3: outliner emits an Outline JSON. Stashed for the
			// downstream fanout (extractFanoutItems reads it) and for
			// each elaborator's projection (sibling situational
			// awareness). Last writer wins on multi-call merges, but
			// outline is a single-agent step so this is benign.
			state.Outline = r.Output
		case "elaborated_features":
			// Phase 3 fanout: each elaborator emits one
			// RawFeatureProposal. Accumulate; assembly into RawProposal
			// happens after both fanouts complete (see post-loop hook).
			state.ElaboratedFeatures = append(state.ElaboratedFeatures, r.Output)
		case "elaborated_strategies":
			state.ElaboratedStrategies = append(state.ElaboratedStrategies, r.Output)
		case "raw_proposal":
			// Phase 2: architect emits a RawSpecProposal; the
			// reconcile step transforms it into canonical SpecProposal.
			// Stash the raw on state so the reconciler's projection
			// can see it; ProposedSpec is left for downstream agents
			// that expect the canonical shape.
			state.RawProposal = r.Output
		case "reconciled_proposal":
			// Reconcile output is a ReconciliationVerdict. Combine
			// with the upstream RawProposal via ApplyReconciliation
			// to produce the canonical SpecProposal. Errors here
			// (malformed verdict, source out of bounds) are recorded
			// as a Concern so revise can surface them; the workflow
			// itself doesn't fail.
			canonical, applied, err := mergeReconcile(state.RawProposal, r.Output, state.Existing)
			if err != nil {
				state.Concerns = append(state.Concerns, Concern{
					AgentID:  r.AgentID,
					Severity: "high",
					Text:     fmt.Sprintf("reconcile: %s", err.Error()),
				})
				continue
			}
			state.ProposedSpec = canonical
			state.ConflictActions = appendConflictActions(state.ConflictActions, applied)
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
			// that raised it, with Kind derived from the agent ID so
			// the revise projection can group findings by lens.
			kind := critiqueKindFor(r.AgentID)
			var ci CriticIssues
			if err := json.Unmarshal([]byte(r.Output), &ci); err != nil {
				// Fallback: store the raw output as one concern so we
				// don't lose the critic's contribution entirely.
				state.Concerns = append(state.Concerns, Concern{
					AgentID:  r.AgentID,
					Severity: "medium",
					Kind:     kind,
					Text:     r.Output,
				})
				continue
			}
			for _, issue := range ci.Issues {
				state.Concerns = append(state.Concerns, Concern{
					AgentID:  r.AgentID,
					Severity: "medium",
					Kind:     kind,
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

	// Phase 5: mechanical integrity critic. Runs once per critique step
	// (after all LLM critics merge), reads the post-reconcile ProposedSpec,
	// and appends any dangling-ref findings as Concerns with Kind="integrity".
	// Cheap (Go function, no LLM), and load-bearing only on regressions —
	// Phase 2's reconciler should produce a clean proposal in the common
	// case. When it doesn't, the integrity critic catches it in-workflow
	// so revise can address it, instead of falling all the way through to
	// the post-workflow integrity loop.
	if mergeKey == "critic_issues" || mergeKey == "critique" {
		appendIntegrityFindings(state)
	}

	// Phase 3 assembly: when either fanout merge completes, attempt
	// to assemble a full RawSpecProposal from whatever has accumulated
	// (the other fanout may have already finished or be empty). Order-
	// independent — both branches converge on the same RawProposal as
	// soon as the data is available. Reconcile reads state.RawProposal
	// for both its prompt and the ApplyReconciliation merge.
	if mergeKey == "elaborated_features" || mergeKey == "elaborated_strategies" {
		if assembled, ok := assembleRawProposal(state); ok {
			state.RawProposal = assembled
		}
	}
}

// assembleRawProposal stitches the per-element fanout outputs into a
// single RawSpecProposal JSON. Returns (assembled, true) when at least
// one of the elaborated slices has entries. Best-effort: items that
// fail to parse are skipped with a slog.Warn rather than failing the
// whole assembly — a single bad elaborator output shouldn't poison
// the rest. Safe to call repeatedly as fanouts merge incrementally.
func assembleRawProposal(state *PlanningState) (string, bool) {
	if state == nil {
		return "", false
	}
	if len(state.ElaboratedFeatures) == 0 && len(state.ElaboratedStrategies) == 0 {
		return "", false
	}
	out := RawSpecProposal{}
	for _, raw := range state.ElaboratedFeatures {
		var f RawFeatureProposal
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			slog.Warn("assemble raw proposal: skipping malformed feature elaborator output", "error", err)
			continue
		}
		out.Features = append(out.Features, f)
	}
	for _, raw := range state.ElaboratedStrategies {
		var s RawStrategyProposal
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			slog.Warn("assemble raw proposal: skipping malformed strategy elaborator output", "error", err)
			continue
		}
		out.Strategies = append(out.Strategies, s)
	}
	data, err := json.Marshal(out)
	if err != nil {
		slog.Warn("assemble raw proposal: marshal failed", "error", err)
		return "", false
	}
	return string(data), true
}

// critiqueKindFor maps a critic agent ID to its lens label for grouping
// in the revise prompt. Unknown agents fall back to "review" so concerns
// don't lose their kind tag entirely.
func critiqueKindFor(agentID string) string {
	switch agentID {
	case "architect_critic":
		return "architecture"
	case "devops_critic":
		return "devops"
	case "sre_critic":
		return "sre"
	case "cost_critic":
		return "cost"
	default:
		return "review"
	}
}

// appendIntegrityFindings runs Validate against the current canonical
// proposal and appends any warnings to state.Concerns as integrity-kind
// findings. The agent ID "integrity_critic" mirrors the LLM-critic naming
// convention; the revise prompt groups by Kind so the architect sees
// these alongside the LLM critics' concerns.
func appendIntegrityFindings(state *PlanningState) {
	if state == nil || state.ProposedSpec == "" {
		return
	}
	var p SpecProposal
	if err := json.Unmarshal([]byte(state.ProposedSpec), &p); err != nil {
		// Malformed canonical proposal — surface as an integrity finding
		// so revise can re-emit. This shouldn't happen post-Phase-2
		// because ApplyReconciliation produces structured output, but the
		// guard catches regressions.
		state.Concerns = append(state.Concerns, Concern{
			AgentID:  "integrity_critic",
			Severity: "high",
			Kind:     "integrity",
			Text:     fmt.Sprintf("post-reconcile proposal is malformed JSON: %s", err.Error()),
		})
		return
	}
	for _, w := range p.Validate(state.Existing) {
		state.Concerns = append(state.Concerns, Concern{
			AgentID:  "integrity_critic",
			Severity: "high",
			Kind:     "integrity",
			Text:     w.String(),
		})
	}
}

// Run executes the full council workflow using the generic DAG executor.
// The outer convergence loop and readiness gate are handled here; the inner
// DAG execution (dependency ordering, parallelism) is delegated to executor.Executor.
func (e *WorkflowExecutor) Run(ctx context.Context, initialPrompt string) ([]RoundResult, error) {
	state := &PlanningState{
		Prompt:   initialPrompt,
		Round:    1,
		Existing: e.Existing,
	}
	defer func() { e.LastState = state }()

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
