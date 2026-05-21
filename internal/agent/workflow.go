package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/chetan/locutus/internal/executor"
)

// WorkflowStep defines a single step in a workflow. Steps carry the
// data fields the DAG executor consumes (ID, Agents, Parallel, DependsOn)
// plus optional closures that describe what the step does:
//
//   - Conditional: gates execution. If non-nil and returns false, the
//     step is skipped (no agent calls, no merge). nil means unconditional.
//   - Fanout: returns a slice of raw-JSON items. Each item drives one
//     agent call with StateSnapshot.FanoutItem populated so the projection
//     can render the per-element prompt. nil means the step runs the
//     configured agents once each.
//   - Project: builds the LLM messages for each agent call. Falls back
//     to a verb-supplied default when nil. Ignored when RunItem is set.
//   - Merge: applies round results back into the verb's state. nil is
//     equivalent to a no-op merge.
//   - RunItem: when non-nil, REPLACES the executor's default per-slot
//     agent-dispatch (build messages → RunWithRetry → return content).
//     The executor calls RunItem instead, threading through the same
//     ctx + snapshot + per-slot fanout context. Lets a single fanout
//     slot drive a multi-call sub-flow via the verb's own helpers
//     (e.g. justify's per_decision step running challenger → research
//     → advocate per fanout item). The returned content is stored on
//     RoundResult.Output exactly as a normal dispatch's content would
//     be; an error sets RoundResult.Err. Project and the AgentDef
//     lookup are skipped when RunItem fires — the closure owns
//     dispatch end-to-end. Step.Agents must still name exactly one
//     agent (used as the AgentID label on RoundResult so merge
//     handlers and the events sink see a consistent identity).
//
// The S type parameter is the verb-specific mutable state the closures
// see and mutate. Council workflows use WorkflowStep[PlanningState]; new
// verbs declare their own state types (JustifyState, RefineState, etc.).
//
// Per-model concurrency caps live in models.yaml (`concurrent_requests`).
// Even with Parallel=true, fanout never floods a model past its
// configured slot count.
type WorkflowStep[S any] struct {
	ID          string
	Agents      []string
	Parallel    bool
	DependsOn   []string
	Conditional func(*S) bool
	Fanout      func(*S) ([]string, error)
	Project     func(StateSnapshot[S]) []Message
	Merge       func(*S, []RoundResult)
	RunItem     func(ctx context.Context, snap StateSnapshot[S]) (string, error)

	// Spawn, when non-nil, is invoked after the step's merge and may
	// append additional WorkflowSteps to the running DAG. It receives a
	// post-merge snapshot and the step's RoundResults so the closure can
	// branch on what the step actually produced. The agent-layer
	// translator wraps this into the executor.Step.Spawn primitive
	// landed in DJ-122 Phase 1. Returned steps go through the same
	// translator as initial-graph steps, so they may declare Agents,
	// RunItem, Conditional, etc. and run via ExecuteRound on a future
	// wave. The Edge list is for cross-cutting edges that don't fit on
	// a new step's DependsOn (e.g., parent → template-root).
	Spawn func(ctx context.Context, snapshot StateSnapshot[S], results []RoundResult) (newSteps []WorkflowStep[S], newEdges []executor.Edge, err error)

	// Budget is read by spawner closures as the iteration cap for the
	// loop they drive. Zero falls back to Workflow.DefaultGateBudget.
	// Has no effect on non-spawner steps; the executor does not consume
	// this field directly.
	Budget int

	// TemplateID and IterationIndex are stamped on spawned steps by
	// callers that drive iteration-aware loops; they propagate through
	// to executor.Step (and onward to StepResult / RoundResult) so
	// downstream consumers can attribute output to a specific
	// iteration. Zero-valued on initial-graph steps.
	TemplateID     string
	IterationIndex int
}

// Workflow defines a verb's DAG of steps. The S type parameter is the
// verb-specific state container threaded through every step's closures.
type Workflow[S any] struct {
	Rounds    []WorkflowStep[S]
	MaxRounds int

	// Snapshot returns a value-copy of the state safe for concurrent
	// reads by parallel agents. Verbs whose state contains slices or
	// maps must provide a closure that copies them; otherwise parallel
	// agents would observe in-flight mutations from other goroutines.
	// When nil, the executor passes a shallow value copy via *state.
	Snapshot func(*S) S

	// DefaultProject is the projection used when a step omits its own
	// Project closure. Council workflows wire this to projectDefault
	// (prompt verbatim + prior ProposedSpec). Verbs whose state has no
	// universally-projectable shape can leave it nil and require every
	// step to declare Project explicitly.
	DefaultProject func(StateSnapshot[S]) []Message

	// MaxGraphMultiplier forwards to executor.Config.MaxGraphMultiplier
	// for workflows that use spawner steps. Zero leaves the executor
	// at its own default (1000 × initial-step-count). Setting it to 1
	// effectively disables spawning. Has no effect when no step has a
	// Spawn callback.
	MaxGraphMultiplier int

	// DefaultGateBudget is the fallback iteration cap for gate steps
	// whose own WorkflowStep.Budget is zero. Spawner closures read
	// their own budget first and fall back to this. Zero leaves the
	// fallback at 5.
	DefaultGateBudget int
}

// RoundResult holds the output of executing one round.
type RoundResult struct {
	StepID  string
	AgentID string
	Output  string
	Err     error

	// TemplateID and IterationIndex echo the producing step's
	// iteration metadata (DJ-122 Phase 2). Zero-valued for
	// initial-graph and ad-hoc-spawned steps. Stamped by the RunStep
	// wrapper from the executor.Step, so per-agent code in
	// executeAgent doesn't need to know about iterations.
	TemplateID     string
	IterationIndex int

	// FanoutItem echoes the per-call fanout dispatch context (the
	// raw JSON of the per-iteration item the executor dispatched
	// against). Empty for non-fanout calls. Threaded through from
	// snap.FanoutItem so merge handlers can attribute results back
	// to the dimension/cluster/item that produced them (DJ-129).
	FanoutItem string
}

// WorkflowExecutor runs a workflow using the generic DAG executor with a
// caller-supplied state value.
type WorkflowExecutor[S any] struct {
	Executor  AgentExecutor
	AgentDefs map[string]AgentDef
	Workflow  *Workflow[S]
	Events    chan WorkflowEvent // optional; nil disables progress reporting
}

// BridgeToSink wires e.Events so workflow step lifecycle events
// (queued / started / completed / retrying / error) forward to
// sink.OnEvent. Returns a closer the caller must defer; the closer
// closes the events channel and waits for the bridging goroutine to
// drain. No-op when sink is nil (returns a no-op closer).
//
// Workflow steps emit per-step events via emitEvent, which silently
// drops them when Events is nil. They also suppress NotifyingExecutor's
// per-LLM-call events (WithSuppressLLMNotify) to avoid double-spinners
// when both are wired. So a WorkflowExecutor without an Events bridge
// is *completely silent* from the operator's perspective — the
// suppression fires but nothing replaces it. Every cmd-layer
// WorkflowExecutor construction that runs against a CLI must call
// BridgeToSink with the sink withProgressSink returns; sites that
// don't are progress-reporting regressions.
//
// Idempotent on double-close. Buffer sized at 64 to match the
// canonical bridging pattern in internal/agent/specgen.go and
// internal/prereqs/summaries.go — that's "every agent fires
// started+completed in tight succession without the consumer ever
// falling behind."
func (e *WorkflowExecutor[S]) BridgeToSink(sink EventSink) func() {
	if sink == nil {
		return func() {}
	}
	ch := make(chan WorkflowEvent, 64)
	e.Events = ch
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			sink.OnEvent(ev)
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(ch)
			<-done
		})
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

// emitEvent sends a workflow event. Blocks if the channel is full —
// dropping council events would silently desynchronise any UI built on
// top, and the channel is sized generously by the caller (see
// GenerateSpec). Safe for concurrent use.
func (e *WorkflowExecutor[S]) emitEvent(stepID, agentID, status, message string) {
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
func (e *WorkflowExecutor[S]) executeAgent(ctx context.Context, step WorkflowStep[S], stepID, agentID string, snap StateSnapshot[S]) RoundResult {
	// RunItem path: the step owns dispatch end-to-end. Skip AgentDefs
	// lookup, Project rendering, and RunWithRetry — the closure does
	// its own LLM calls (typically via the AgentDispatcher carried on
	// the verb's state). Lifecycle events still fire so the operator
	// sees the step queued/started/completed; the closure's own
	// per-call YAML traces give the leaf detail.
	if step.RunItem != nil {
		e.emitEvent(stepID, agentID, "queued", "")
		ctx = WithAgentID(ctx, agentID)
		ctx = WithSuppressLLMNotify(ctx)
		if tag := stepIDFanoutTag(stepID); tag != "" {
			ctx = WithCallTag(ctx, tag)
		}
		ctx = WithAcquiredCallback(ctx, func() {
			e.emitEvent(stepID, agentID, "started", "")
		})
		// Rate-limit wait happens inside the dispatcher's underlying
		// Executor.Run call. Surface it as the same "retrying" event
		// the non-RunItem path uses so the cliSink flips spinner text
		// to "rate-limited; waiting Ns" consistently across both
		// dispatch shapes.
		ctx = WithRateLimitWaitCallback(ctx, func(sleep time.Duration) {
			e.emitEvent(stepID, agentID, "retrying", fmt.Sprintf("rate-limited; waiting %s", sleep.Round(time.Second)))
		})
		// RunItem owns its sub-call dispatch, but the workflow sink
		// expects a queued → started → completed lifecycle for every
		// step slot. Emit "started" inline (mirror of the
		// AcquiredCallback the LLM wrapper fires for normal calls)
		// so the sink advances consistently.
		e.emitEvent(stepID, agentID, "started", "")
		out, err := step.RunItem(ctx, snap)
		if err != nil {
			e.emitEvent(stepID, agentID, "error", err.Error())
			return RoundResult{StepID: stepID, AgentID: agentID, Output: out, Err: err, FanoutItem: snap.FanoutItem}
		}
		e.emitEvent(stepID, agentID, "completed", "")
		return RoundResult{StepID: stepID, AgentID: agentID, Output: out, FanoutItem: snap.FanoutItem}
	}

	def, ok := e.AgentDefs[agentID]
	if !ok {
		return RoundResult{StepID: stepID, AgentID: agentID, Err: fmt.Errorf("agent %q not found", agentID), FanoutItem: snap.FanoutItem}
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
	// Rate-limit wait happens INSIDE Executor.Run (single attempt) —
	// not via RunWithRetry's full re-walk. Surface it as the same
	// "retrying" event so the cliSink flips the spinner text in
	// place; on resume the next concurrency Acquire fires the
	// AcquiredCallback above and flips the spinner back to running.
	ctx = WithRateLimitWaitCallback(ctx, func(sleep time.Duration) {
		e.emitEvent(stepID, agentID, "retrying", fmt.Sprintf("rate-limited; waiting %s", sleep.Round(time.Second)))
	})

	project := step.Project
	if project == nil && e.Workflow != nil {
		project = e.Workflow.DefaultProject
	}
	var messages []Message
	if project != nil {
		messages = project(snap)
	}
	input := AgentInput{Messages: messages}

	resp, err := RunWithRetry(ctx, e.Executor, def, input, executionRetryConfig())
	if err != nil {
		e.emitEvent(stepID, agentID, "error", err.Error())
		return RoundResult{StepID: stepID, AgentID: agentID, Err: err, FanoutItem: snap.FanoutItem}
	}

	e.emitEvent(stepID, agentID, "completed", "")
	return RoundResult{StepID: stepID, AgentID: agentID, Output: resp.Content, FanoutItem: snap.FanoutItem}
}

// ExecuteRound runs a single workflow step against the current state. For
// parallel multi-agent steps, agents run concurrently with the same snapshot.
//
// Opens a `workflow.phase` span tagged with the step ID and (when
// resolvable) the agent that runs it. The span ends when ExecuteRound
// returns, so every agent.dispatch / llm.attempt / provider.generate
// child sits beneath it in the trace. Steps short-circuited by their
// Conditional or empty-agent guard exit before opening the span — no
// trace entry for "this step decided not to fire" since fanout
// filtering already handles the same shape (an empty fanout produces
// no provider.generate spans, and that's the right "didn't fire"
// signal for downstream tools).
func (e *WorkflowExecutor[S]) ExecuteRound(ctx context.Context, step WorkflowStep[S], state *S) ([]RoundResult, error) {
	if step.Conditional != nil && !step.Conditional(state) {
		return nil, nil
	}

	agents := step.Agents
	if len(agents) == 0 {
		return nil, nil
	}

	phaseAttrs := []attribute.KeyValue{
		attribute.String("locutus.workflow.phase", step.ID),
	}
	// Single-agent step: stamp locutus.agent.id on the phase span so
	// trace filters can group by agent without descending. Fanout and
	// multi-agent steps emit per-call agent.dispatch children that
	// carry the per-call agent id; tagging the parent with one
	// "primary" id would be misleading there.
	if step.Fanout == nil && len(agents) == 1 {
		phaseAttrs = append(phaseAttrs, attribute.String("locutus.agent.id", agents[0]))
	}
	phaseCtx, phaseSpan := Tracer().Start(ctx, "workflow.phase",
		oteltrace.WithAttributes(phaseAttrs...))
	defer phaseSpan.End()
	ctx = phaseCtx

	snap := e.snapshot(state)

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
//
// DJ-125: integrity-critic concerns default to ConcernStatusOpen so the
// mechanical disposition pre-pass and scout grading both treat them
// uniformly with LLM-critic concerns. The text already names the
// dangling decision ID, so RelatedDecisionIDs is populated via the
// same extraction helper.
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
			Status:   ConcernStatusOpen,
		})
		return
	}
	knownAxes := collectKnownAxisIDs(state)
	for _, w := range p.Validate(state.Existing) {
		text := w.String()
		state.Concerns = append(state.Concerns, Concern{
			AgentID:            "integrity_critic",
			Severity:           "high",
			Kind:               "integrity",
			Text:               text,
			Status:             ConcernStatusOpen,
			RelatedDecisionIDs: extractDecisionRefsFromText(text),
			RelatedAxisIDs:     extractAxisRefsFromText(text, knownAxes),
		})
	}
}

// snapshot returns a verb-supplied value-copy of state safe for concurrent
// reads. When the workflow declares no Snapshot closure, the caller gets a
// shallow value-copy via *state — fine for verbs whose state has no slices
// or maps to deep-copy.
func (e *WorkflowExecutor[S]) snapshot(state *S) StateSnapshot[S] {
	if e.Workflow != nil && e.Workflow.Snapshot != nil {
		return StateSnapshot[S]{State: e.Workflow.Snapshot(state)}
	}
	return StateSnapshot[S]{State: *state}
}

// Run executes one DAG pass against the supplied state. The verb owns the
// state pointer — Run mutates it via per-step Merge closures and returns
// the round results. Multi-iteration concerns (convergence, readiness)
// belong in verb-specific wrappers (see RunCouncil for the council case).
func (e *WorkflowExecutor[S]) Run(ctx context.Context, state *S) ([]RoundResult, error) {
	stepLookup := make(map[string]WorkflowStep[S], len(e.Workflow.Rounds))
	// toExecutorStep is shared between the initial-graph build and the
	// spawn translator below so spawned WorkflowSteps go through the
	// same conversion path (Conditional wrapping, Spawn re-translation,
	// stepLookup registration) as initial-graph steps. Closes over
	// stepLookup, so each call registers ws.ID for later RunStep /
	// Merge lookups; safe to call sequentially from a Spawn callback
	// per executor's wave-then-spawn ordering.
	var toExecutorStep func(ws WorkflowStep[S]) executor.Step
	toExecutorStep = func(ws WorkflowStep[S]) executor.Step {
		stepLookup[ws.ID] = ws
		ds := executor.Step{
			ID:             ws.ID,
			DependsOn:      ws.DependsOn,
			Parallel:       ws.Parallel,
			TemplateID:     ws.TemplateID,
			IterationIndex: ws.IterationIndex,
		}
		if ws.Conditional != nil {
			cond := ws.Conditional // capture for closure
			ds.Conditional = func(s any) bool {
				return cond(s.(*S))
			}
		}
		if ws.Spawn != nil {
			spawn := ws.Spawn // capture for closure
			ds.Spawn = func(ctx context.Context, snapAny any, out any) ([]executor.Step, []executor.Edge, error) {
				snapState, _ := snapAny.(S)
				snap := StateSnapshot[S]{State: snapState}
				results, _ := out.([]RoundResult) // nil-output is fine; spawn handles len==0
				newWSs, newEdges, err := spawn(ctx, snap, results)
				if err != nil {
					return nil, nil, err
				}
				if len(newWSs) == 0 {
					return nil, newEdges, nil
				}
				execSteps := make([]executor.Step, len(newWSs))
				for i, ns := range newWSs {
					execSteps[i] = toExecutorStep(ns)
				}
				return execSteps, newEdges, nil
			}
		}
		return ds
	}

	dagSteps := make([]executor.Step, len(e.Workflow.Rounds))
	for i, ws := range e.Workflow.Rounds {
		dagSteps[i] = toExecutorStep(ws)
	}

	dagEvents, stopBridge := e.startEventBridge()
	defer stopBridge()

	cfg := executor.Config[S]{
		Steps: dagSteps,
		RunStep: func(ctx context.Context, step executor.Step, snap S) (executor.StepResult, error) {
			ws := stepLookup[step.ID]
			results, err := e.ExecuteRound(ctx, ws, &snap)
			// Stamp iteration metadata from the executor step onto
			// each RoundResult so callers see iteration attribution
			// without consulting events.
			if step.TemplateID != "" || step.IterationIndex != 0 {
				for j := range results {
					results[j].TemplateID = step.TemplateID
					results[j].IterationIndex = step.IterationIndex
				}
			}
			return executor.StepResult{Output: results}, err
		},
		Merge: func(s *S, r executor.StepResult) {
			results, ok := r.Output.([]RoundResult)
			if !ok {
				return
			}
			ws := stepLookup[r.StepID]
			if ws.Merge != nil {
				ws.Merge(s, results)
			}
		},
		Snapshot: func(s *S) S {
			if e.Workflow.Snapshot != nil {
				return e.Workflow.Snapshot(s)
			}
			return *s
		},
		Events:             dagEvents,
		MaxGraphMultiplier: e.Workflow.MaxGraphMultiplier,
	}

	dagResults, err := executor.NewExecutor(cfg).Run(ctx, state)

	var allResults []RoundResult
	for _, dr := range dagResults {
		if results, ok := dr.Output.([]RoundResult); ok {
			allResults = append(allResults, results...)
		}
	}
	return allResults, err
}

// AppendSubgraph is the WorkflowStep[S] analogue of
// executor.AppendSubgraph. A Spawn closure that drives a convergence
// loop calls this to expand one iteration of a template into a list of
// fully-stamped WorkflowStep[S]s plus the parent edges connecting them
// to the spawning gate.
//
// For each step the template returns:
//   - ID is rewritten as "<TemplateID>#iter:<IterationIndex>:<base_id>"
//   - TemplateID and IterationIndex fields are populated
//   - DependsOn entries that name a sibling base id are rewritten to
//     the prefixed form; external references pass through unchanged
//   - Spawn closures the template installs on iteration steps are
//     preserved verbatim
//
// A "template root" is any returned step whose DependsOn names no
// sibling. Each root receives an auto-edge from ctx.ParentNodeID. When
// ParentNodeID is empty, no parent edges are produced.
//
// The helper is pure: it does not touch the workflow or executor state
// and returns nil slices on an empty template, so callers can hand the
// result straight back from a Spawn return without additional guarding.
func AppendSubgraph[S any](template func(executor.IterationContext) []WorkflowStep[S], ctx executor.IterationContext) ([]WorkflowStep[S], []executor.Edge) {
	raw := template(ctx)
	if len(raw) == 0 {
		return nil, nil
	}

	prefix := func(base string) string {
		return fmt.Sprintf("%s#iter:%d:%s", ctx.TemplateID, ctx.IterationIndex, base)
	}

	siblings := make(map[string]bool, len(raw))
	for _, s := range raw {
		siblings[s.ID] = true
	}

	steps := make([]WorkflowStep[S], len(raw))
	var rootIDs []string
	for i, s := range raw {
		baseID := s.ID
		s.ID = prefix(baseID)
		s.TemplateID = ctx.TemplateID
		s.IterationIndex = ctx.IterationIndex

		hasInternalDep := false
		if len(s.DependsOn) > 0 {
			rewritten := make([]string, len(s.DependsOn))
			for j, dep := range s.DependsOn {
				if siblings[dep] {
					rewritten[j] = prefix(dep)
					hasInternalDep = true
				} else {
					rewritten[j] = dep
				}
			}
			s.DependsOn = rewritten
		}
		steps[i] = s

		if !hasInternalDep {
			rootIDs = append(rootIDs, s.ID)
		}
	}

	var edges []executor.Edge
	if ctx.ParentNodeID != "" && len(rootIDs) > 0 {
		edges = make([]executor.Edge, 0, len(rootIDs))
		for _, id := range rootIDs {
			edges = append(edges, executor.Edge{From: ctx.ParentNodeID, To: id})
		}
	}

	return steps, edges
}

// startEventBridge spawns a goroutine that forwards executor events as
// WorkflowEvents to e.Events. Returns the channel the executor should write
// to and a cleanup func that closes the channel and waits for the goroutine
// to drain. When e.Events is nil, both returns are no-ops.
func (e *WorkflowExecutor[S]) startEventBridge() (chan executor.Event, func()) {
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
				Mutation:  evt.Mutation,
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
