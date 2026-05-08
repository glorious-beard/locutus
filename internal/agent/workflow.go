package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chetan/locutus/internal/executor"
)

// WorkflowStep defines a single step in the council workflow. Steps carry
// the data fields the DAG executor consumes (ID, Agents, Parallel,
// DependsOn) plus four optional closures that describe what the step
// does:
//
//   - Conditional: gates execution. If non-nil and returns false, the
//     step is skipped (no agent calls, no merge). nil means unconditional.
//   - Fanout: returns a slice of raw-JSON items. Each item drives one
//     agent call with StateSnapshot.FanoutItem populated so the projection
//     can render the per-element prompt. nil means the step runs the
//     configured agents once each.
//   - Project: builds the LLM messages for each agent call. Falls back
//     to projectDefault when nil.
//   - Merge: applies round results back into the planning state. nil is
//     equivalent to mergeNoop.
//
// Per-model concurrency caps live in models.yaml (`concurrent_requests`).
// Even with Parallel=true, fanout never floods a model past its
// configured slot count.
type WorkflowStep struct {
	ID          string
	Agents      []string
	Parallel    bool
	DependsOn   []string
	Conditional func(*PlanningState) bool
	Fanout      func(*PlanningState) ([]string, error)
	Project     func(StateSnapshot) []Message
	Merge       func(*PlanningState, []RoundResult)
}

// Workflow defines the full council workflow DAG.
type Workflow struct {
	Rounds    []WorkflowStep
	MaxRounds int
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
	Executor  AgentExecutor
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
//     call may be sitting in the per-model concurrency
//     queue (models.yaml's concurrent_requests cap).
//   - "started"   — the call left the queue and is hitting the
//     provider. Driven by an acquired-callback the LLM
//     wrapper invokes after its semaphore acquire.
//   - "completed" — the call returned (success or final retry failure
//     reported separately as "error").
//
// The cliSink renders "queued" with a distinct visual ("queued" prefix)
// and updates the same spinner to "running" on the started event, so
// the operator can tell waiting items from in-flight ones.
func (e *WorkflowExecutor) executeAgent(ctx context.Context, step WorkflowStep, stepID, agentID string, snap StateSnapshot) RoundResult {
	def, ok := e.AgentDefs[agentID]
	if !ok {
		return RoundResult{StepID: stepID, AgentID: agentID, Err: fmt.Errorf("agent %q not found", agentID)}
	}

	e.emitEvent(stepID, agentID, "queued", "")
	ctx = WithAgentID(ctx, agentID)
	// Workflow steps already emit their own queued/started/completed
	// events on the sink; tell any wrapping NotifyingExecutor to stay
	// silent so direct-call LLM events don't double up with workflow
	// per-step events on the same sink.
	ctx = WithSuppressLLMNotify(ctx)
	if tag := stepIDFanoutTag(stepID); tag != "" {
		ctx = WithCallTag(ctx, tag)
	}
	ctx = WithAcquiredCallback(ctx, func() {
		e.emitEvent(stepID, agentID, "started", "")
	})
	ctx = WithRetryCallback(ctx, func(attempt int, retryErr error) {
		e.emitEvent(stepID, agentID, "retrying", fmt.Sprintf("attempt %d failed: %s", attempt, retryErr))
	})

	project := step.Project
	if project == nil {
		project = projectDefault
	}
	messages := project(snap)
	input := AgentInput{Messages: messages}

	resp, err := RunWithRetry(ctx, e.Executor, def, input, executionRetryConfig())
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
	if step.Conditional != nil && !step.Conditional(state) {
		return nil, nil
	}

	agents := step.Agents
	if len(agents) == 0 {
		return nil, nil
	}

	snap := state.Snapshot()

	// Fanout: spawn one agent invocation per element returned by the
	// step's Fanout function. Each invocation gets its own snapshot with
	// FanoutItem populated so the projection can render the per-element
	// prompt. Per-model concurrency caps in LLM (models.yaml's
	// concurrent_requests) bound the actual parallelism — even with
	// Parallel=true, fanout never floods a model past its configured
	// slot count.
	if step.Fanout != nil {
		if len(agents) != 1 {
			return nil, fmt.Errorf("fanout step %q must declare exactly one agent (got %d)", step.ID, len(agents))
		}
		items, err := step.Fanout(state)
		if err != nil {
			return nil, fmt.Errorf("fanout %s: %w", step.ID, err)
		}
		if len(items) == 0 {
			return nil, nil
		}
		results := make([]RoundResult, len(items))
		fanoutStepID := func(item string) string {
			id := fanoutItemID(item)
			if id == "" {
				return step.ID
			}
			return step.ID + " (" + id + ")"
		}
		// DJ-098: per-item agent dispatch. When a fanout item carries
		// its own `agent_id` field (FindingCluster does), it overrides
		// the step's declared agent — different clusters in the same
		// fanout step can dispatch to different elaborator agents.
		// Items without an agent_id field fall back to the step's
		// agent (preserves elaborate-fanout behavior).
		itemAgent := func(raw string) string {
			var v struct {
				AgentID string `json:"agent_id"`
			}
			if err := json.Unmarshal([]byte(raw), &v); err == nil && strings.TrimSpace(v.AgentID) != "" {
				return v.AgentID
			}
			return agents[0]
		}
		if step.Parallel {
			var wg sync.WaitGroup
			wg.Add(len(items))
			for i, item := range items {
				go func(idx int, raw string) {
					defer wg.Done()
					itemSnap := snap
					itemSnap.FanoutItem = raw
					results[idx] = e.executeAgent(ctx, step, fanoutStepID(raw), itemAgent(raw), itemSnap)
				}(i, item)
			}
			wg.Wait()
		} else {
			for i, item := range items {
				itemSnap := snap
				itemSnap.FanoutItem = item
				results[i] = e.executeAgent(ctx, step, fanoutStepID(item), itemAgent(item), itemSnap)
			}
		}
		// Per-node failure isolation: a fanout step is the *only*
		// place where partial success is meaningful. One bad
		// elaborator should not abort the proposal — the merge
		// handler accepts whatever results came back, the assembler
		// stitches the surviving outputs into the RawSpecProposal,
		// and the reconciler runs against what we have. A single
		// failure used to short-circuit the whole pipeline (16 of
		// 17 elaborators succeeding, all discarded), which defeated
		// the failure-isolation promise.
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
				results[idx] = e.executeAgent(ctx, step, step.ID, aid, snap)
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
		r := e.executeAgent(ctx, step, step.ID, agentID, snap)
		results = append(results, r)
		if r.Err != nil {
			return results, r.Err
		}
	}
	return results, nil
}

// stepIDFanoutTag extracts the per-item id from a fanout step's
// composite stepID. The dispatcher names fanout invocations as
// `"elaborate_features (feat-dashboard)"`; this returns
// `"feat-dashboard"`. Returns empty for non-fanout step IDs (which
// have no parenthetical suffix), in which case the recorder writes
// the per-call filename without a tag suffix.
func stepIDFanoutTag(stepID string) string {
	open := strings.Index(stepID, " (")
	if open < 0 {
		return ""
	}
	close := strings.LastIndex(stepID, ")")
	if close <= open+2 {
		return ""
	}
	return stepID[open+2 : close]
}

// fanoutItemID extracts an identifier from a fanout item's raw JSON
// for per-item event labeling. Tries `id` first (OutlineFeature /
// OutlineStrategy shape), then `node_id` (FindingCluster shape when
// the cluster targets an existing node), then `topic` (FindingCluster
// shape for new-node clusters). Returns empty when none are present;
// callers fall back to the bare step ID. Dedup-key collisions are a
// progress-rendering issue, not a correctness one.
func fanoutItemID(rawJSON string) string {
	var v struct {
		ID     string `json:"id"`
		NodeID string `json:"node_id"`
		Topic  string `json:"topic"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &v); err != nil {
		return ""
	}
	if v.ID != "" {
		return v.ID
	}
	if v.NodeID != "" {
		return v.NodeID
	}
	return v.Topic
}

func marshalFanoutItems(items []any) ([]string, error) {
	out := make([]string, 0, len(items))
	for _, it := range items {
		data, err := json.Marshal(it)
		if err != nil {
			return nil, fmt.Errorf("marshal fanout item: %w", err)
		}
		out = append(out, string(data))
	}
	return out, nil
}

// runMechanicalCluster (DJ-098) partitions state.Concerns into
// per-node FindingClusters (when a finding mentions an existing node
// id) and UnmatchedFindings (everything else). Idempotent; runs after
// every critique merge so late-arriving critic outputs and the
// integrity critic's findings get clustered too.
//
// Replaces state.FindingClusters and state.UnmatchedFindings on each
// invocation rather than appending; the LLM clusterer's output later
// appends back in via the finding_clusters merge handler.
func runMechanicalCluster(state *PlanningState) {
	if state == nil {
		return
	}
	featureIDs, strategyIDs := proposalIDLists(state.RawProposal)
	clusters, unmatched := MechanicalCluster(state.Concerns, featureIDs, strategyIDs)
	state.FindingClusters = clusters
	state.UnmatchedFindings = unmatched
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
			continue
		}
		out.Features = append(out.Features, f)
	}
	for _, raw := range state.ElaboratedStrategies {
		var s RawStrategyProposal
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			continue
		}
		out.Strategies = append(out.Strategies, s)
	}
	data, err := json.Marshal(out)
	if err != nil {
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
			Parallel:  ws.Parallel,
		}
		if ws.Conditional != nil {
			cond := ws.Conditional // capture for closure
			ds.Conditional = func(s any) bool {
				return cond(s.(*PlanningState))
			}
		}
		dagSteps[i] = ds
	}

	var allResults []RoundResult

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
				results, ok := r.Output.([]RoundResult)
				if !ok {
					return
				}
				ws := stepLookup[r.StepID]
				if ws.Merge != nil {
					ws.Merge(s, results)
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

		verdict, err := CheckConvergence(ctx, e.Executor, monitorDef, state)
		if err != nil {
			return allResults, fmt.Errorf("convergence check: %w", err)
		}

		if e.Events != nil {
			e.Events <- WorkflowEvent{
				StepID:    "convergence",
				AgentID:   "convergence",
				Status:    "completed",
				Message:   verdict.Reasoning,
				Timestamp: time.Now(),
			}
		}

		if verdict.Converged {
			ready, err := CheckReadiness(ctx, e.Executor, e.AgentDefs, state)
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
