package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/chetan/locutus/internal/executor"
	"github.com/chetan/locutus/internal/history"
)

// specLoopTemplateID is the TemplateID stamped on every step the
// spec-council convergence loop spawns. Surfaces in StepResult /
// RoundResult metadata for iteration attribution.
const specLoopTemplateID = "spec_loop"

// defaultSpecGateBudget is the per-gate iteration cap when neither the
// gate step's own Budget nor the workflow's DefaultGateBudget is set.
// Mirrors the plan: 5 iterations is roomy for a council that's making
// real progress and tight enough that runaway prompts surface as a
// failure rather than silently burning model time.
const defaultSpecGateBudget = 5

// SpecGateVerdict is the structured output of the spec_gate agent. The
// gate reads the assembled ProposedSpec, GOALS.md, and any open concerns;
// it returns Converged=true when the four-lifecycle-phases YES question
// holds for every deliverable, or Converged=false plus a list of
// OpenDimensions naming the specific axes still unresolved. Schema is
// registered in schemas.go so providers enforce structured output.
//
// Per CLAUDE.md: every enum-shaped field carries `jsonschema:"enum=..."`,
// every constrained field names its constraint in `description=...`, and
// the example payload registered in schemas.go uses descriptive prose,
// not placeholder tokens — placeholders prime the schema-skeleton
// failure (DJ-118 / agent-conventions).
//
// Distinct from the older ConvergenceVerdict in convergence.go, which
// drives the prose-parsed convergence-monitor path used by RunCouncil's
// plan/assimilation flow. DJ-122 supersedes that for the spec-gen
// council specifically; the older type stays for the remaining callers.
type SpecGateVerdict struct {
	Converged bool `json:"converged" jsonschema:"description=True only when the assembled ProposedSpec answers YES to the four-lifecycle-phases convergence question (define / develop / deploy / support) for every deliverable named in GOALS.md. False when at least one phase remains underspecified for at least one deliverable."`

	Reasoning string `json:"reasoning" jsonschema:"description=Two to three sentences naming which of define / develop / deploy / support are addressed and which (if any) remain underspecified. Names specific deliverables and axes by their domain vocabulary — 'iOS app deployment cadence is uncommitted'; 'firmware OTA channel is unspecified for the nRF52840 deliverable' — not generic claims like 'looks good' or 'needs more work'."`

	OpenDimensions []string `json:"open_dimensions,omitempty" jsonschema:"description=One entry per still-unresolved axis. Each entry names a specific dimension a deliverable hasn't committed to — e.g., 'deployment cadence for the iOS companion app'; 'OTA update channel for the firmware'; 'cost ceiling for the cloud backend'. Empty when Converged is true."`
}

// SpecGenerationWorkflow drives `locutus refine goals` and `locutus
// import <doc>`'s post-admission planning pass.
//
// DJ-098 unified per-cluster revise. Critic findings route through:
//
//  1. survey      — senior-engineer scout brief.
//  2. outline     — feature/strategy titles + summaries.
//  3. elaborate_features  — fanout: one elaborator per outlined feature.
//  4. elaborate_strategies — fanout: one elaborator per outlined strategy.
//  5. reconcile   — clusters inline decisions across the assembled
//     proposal; emits canonical SpecProposal.
//  6. critique    — four LLM critics + the in-workflow integrity
//     critic, parallel.
//  7. cluster_findings — LLM clusterer groups any critic findings the
//     mechanical pre-pass couldn't id-match. Conditional on
//     hasUnmatchedFindings.
//  8. revise      — fanout (one call per FindingCluster). Each cluster
//     carries its own agent_id (set by the mechanical pre-pass from
//     id prefix or by the LLM clusterer's kind). Conditional on
//     hasFindingClusters.
//  9. reconcile_revise — same reconciler against the merged raw proposal
//     (original + revised + added). Conditional on hasFindingClusters.
//
// The mechanical pre-pass that populates state.FindingClusters from
// state.Concerns runs inside mergeCriticIssues — no explicit step. It
// groups findings that name an existing node id; the LLM clusterer
// step processes the rest.
//
// MaxRounds=1 because the DJ-122 spawner-driven convergence loop owns
// iteration; the executor's outer convergence pass is unused.
//
// Per-model concurrency caps live in models.yaml's `concurrent_requests`
// field. Even with Parallel=true on fanout steps, the actual
// concurrency is bounded so fanout never floods a model past its
// configured slot count.
//
// DJ-122: the static cluster_findings → revise → reconcile_revise tail
// is gone. A single `gate` step replaces it, running the spec_gate
// agent and using its WorkflowStep.Spawn to either terminate the
// workflow (verdict.Converged), spawn another revise/reconcile/critique/
// gate iteration (verdict.Converged == false; iter < Budget), or spawn
// a terminal convergence_failed step (budget exhausted) that writes a
// DJ-103 history event and fails the run.
//
// cluster_findings stays in the initial graph — it runs once before the
// first gate; subsequent iterations re-use the existing FindingClusters
// plus any new entries that mergeGateVerdict appends from the gate's
// OpenDimensions.
func NewSpecGenerationWorkflow(historian *history.Historian, budget int) *Workflow[PlanningState] {
	if budget <= 0 {
		budget = defaultSpecGateBudget
	}
	loopTemplate := convergenceLoopTemplate(historian, budget)
	return &Workflow[PlanningState]{
		Snapshot:          snapshotPlanningState,
		DefaultProject:    projectDefault,
		MaxRounds:         1,
		DefaultGateBudget: budget,
		Rounds: []WorkflowStep[PlanningState]{
			{
				ID:      "survey",
				Agents:  []string{"spec_scout"},
				Project: projectDefault,
				Merge:   mergeScoutBrief,
			},
			{
				ID:        "outline",
				Agents:    []string{"spec_outliner"},
				DependsOn: []string{"survey"},
				Project:   projectOutline,
				Merge:     mergeOutline,
			},
			{
				ID:        "elaborate_features",
				Agents:    []string{"spec_feature_elaborator"},
				Parallel:  true,
				DependsOn: []string{"outline"},
				Fanout:    fanoutOutlineFeatures,
				Project:   projectElaborateFeature,
				Merge:     mergeElaboratedFeatures,
			},
			{
				ID:        "elaborate_strategies",
				Agents:    []string{"spec_strategy_elaborator"},
				Parallel:  true,
				DependsOn: []string{"outline"},
				Fanout:    fanoutOutlineStrategies,
				Project:   projectElaborateStrategy,
				Merge:     mergeElaboratedStrategies,
			},
			{
				ID:        "reconcile",
				Agents:    []string{"spec_reconciler"},
				DependsOn: []string{"elaborate_features", "elaborate_strategies"},
				Project:   projectReconcile,
				Merge:     mergeReconciledProposal,
			},
			{
				ID:        "critique",
				Agents:    []string{"architect_critic", "devops_critic", "sre_critic", "cost_critic"},
				Parallel:  true,
				DependsOn: []string{"reconcile"},
				Project:   projectChallenge,
				Merge:     mergeCriticIssues,
			},
			{
				ID:          "cluster_findings",
				Agents:      []string{"spec_finding_clusterer"},
				DependsOn:   []string{"critique"},
				Conditional: hasUnmatchedFindings,
				Project:     projectClusterFindings,
				Merge:       mergeFindingClusters,
			},
			// DJ-122 gate: iteration 0 of the convergence loop. Its
			// Spawn either terminates (Converged), spawns the next
			// iteration (revise → reconcile → critique → gate), or
			// spawns the terminal convergence_failed step.
			newSpecGateStep("gate", 0, budget, loopTemplate, historian),
		},
	}
}

// SpecGenerationWorkflow is the default Workflow value used when no
// historian is needed (tests, code paths that don't drive convergence
// failure). Production callers should use NewSpecGenerationWorkflow so
// the gate's failure path can write a DJ-103 history event.
var SpecGenerationWorkflow = NewSpecGenerationWorkflow(nil, defaultSpecGateBudget)

// newSpecGateStep returns a WorkflowStep that runs the spec_gate agent
// and drives the convergence loop via its Spawn callback. Used for
// both the initial-graph gate (iter 0) and the gate stamped onto each
// loop-template iteration. ID is the *base* step id — the agent-layer
// AppendSubgraph will prefix it on iteration steps; the initial gate
// has ID "gate" verbatim.
//
// myIter and myStepID parameterise the Spawn closure: each gate
// instance knows its own iteration index and the prefixed id that
// next-iteration roots should depend on. budget is captured for the
// budget-exhaustion check.
func newSpecGateStep(baseID string, myIter, budget int, loopTemplate func(executor.IterationContext) []WorkflowStep[PlanningState], historian *history.Historian) WorkflowStep[PlanningState] {
	return WorkflowStep[PlanningState]{
		ID:             baseID,
		Agents:         []string{"spec_gate"},
		DependsOn:      []string{lastInitialStepBeforeGate(myIter)},
		Project:        projectSpecGate,
		Merge:          mergeGateVerdict,
		Budget:         budget,
		IterationIndex: myIter,
		Spawn:          gateSpawnFor(myIter, budget, loopTemplate, historian),
	}
}

// lastInitialStepBeforeGate returns the id of the step the initial-
// graph gate (iter 0) depends on. For iter > 0 the gate is part of an
// AppendSubgraph-spawned template and its DependsOn is rewritten by
// the helper, so the value here only matters for iter 0.
func lastInitialStepBeforeGate(iter int) string {
	if iter == 0 {
		return "cluster_findings"
	}
	// Stamped inside the loop template; AppendSubgraph rewrites the
	// "critique" sibling reference to the prefixed form.
	return "critique"
}

// gateSpawnFor builds the Spawn closure for a gate at iteration
// `myIter` with the given `budget`. The closure parses the gate's
// SpecGateVerdict from results and:
//
//   - Verdict.Converged == true: returns nil — workflow terminates as
//     the queue drains; existing post-workflow handler persists
//     ProposedSpec.
//   - Verdict.Converged == false AND myIter+1 < budget: expands the
//     loop template at iteration myIter+1 via AppendSubgraph with the
//     current gate's prefixed id as ParentNodeID.
//   - Otherwise (budget exhausted): returns a single terminal
//     convergence_failed step whose RunItem writes a DJ-103 history
//     event and returns a non-nil error; the executor propagates that
//     error through Run and on through GenerateSpec.
//
// myStepID is computed at expand time from myIter + the template id.
func gateSpawnFor(myIter, budget int, loopTemplate func(executor.IterationContext) []WorkflowStep[PlanningState], historian *history.Historian) func(ctx context.Context, snap StateSnapshot[PlanningState], results []RoundResult) ([]WorkflowStep[PlanningState], []executor.Edge, error) {
	return func(_ context.Context, snap StateSnapshot[PlanningState], results []RoundResult) ([]WorkflowStep[PlanningState], []executor.Edge, error) {
		verdict, err := parseSpecGateVerdict(results)
		if err != nil {
			return nil, nil, fmt.Errorf("spec_gate verdict at iter %d: %w", myIter, err)
		}
		if verdict.Converged {
			return nil, nil, nil
		}

		nextIter := myIter + 1
		if nextIter >= budget {
			// Budget exhausted: spawn a single terminal step that
			// writes the DJ-103 history event and errors out. Nothing
			// is persisted to `.borg/spec/`; failure means the
			// prompting or the problem itself needs investigation.
			terminal := convergenceFailedTerminal(historian, &snap.State, verdict, myIter, budget)
			return []WorkflowStep[PlanningState]{terminal}, nil, nil
		}

		// Expand the next iteration. ParentNodeID is the current
		// gate's prefixed id so AppendSubgraph wires the loop edge.
		var parentID string
		if myIter == 0 {
			parentID = "gate"
		} else {
			parentID = fmt.Sprintf("%s#iter:%d:gate", specLoopTemplateID, myIter)
		}
		steps, edges := AppendSubgraph(loopTemplate, executor.IterationContext{
			TemplateID:     specLoopTemplateID,
			IterationIndex: nextIter,
			ParentNodeID:   parentID,
		})
		return steps, edges, nil
	}
}

// convergenceLoopTemplate returns the template closure used by
// AppendSubgraph to expand one iteration of the spec-council
// convergence loop. Each iteration is a four-step chain:
//
//	revise → reconcile → critique → gate
//
// Internal DependsOn entries name siblings by their base id and are
// rewritten by AppendSubgraph to the prefixed form. The template's
// gate carries the same Spawn closure pattern as the initial gate, so
// the loop drives itself one iteration at a time.
func convergenceLoopTemplate(historian *history.Historian, budget int) func(executor.IterationContext) []WorkflowStep[PlanningState] {
	var template func(executor.IterationContext) []WorkflowStep[PlanningState]
	template = func(ic executor.IterationContext) []WorkflowStep[PlanningState] {
		thisIter := ic.IterationIndex
		return []WorkflowStep[PlanningState]{
			{
				// revise: fanout per FindingCluster (existing clusters
				// plus those mergeGateVerdict added from the prior
				// gate's OpenDimensions). Step-level Agents is the
				// fallback; per-cluster AgentID overrides at dispatch.
				ID:          "revise",
				Agents:      []string{"spec_strategy_elaborator"},
				Parallel:    true,
				Conditional: hasFindingClusters,
				Fanout:      fanoutFindingClusters,
				Project:     projectFindingCluster,
				Merge:       mergeRevisedNodes,
			},
			{
				ID:          "reconcile",
				Agents:      []string{"spec_reconciler"},
				DependsOn:   []string{"revise"},
				Conditional: hasFindingClusters,
				Project:     projectReconcile,
				Merge:       mergeReconciledProposal,
			},
			{
				ID:        "critique",
				Agents:    []string{"architect_critic", "devops_critic", "sre_critic", "cost_critic"},
				Parallel:  true,
				DependsOn: []string{"reconcile"},
				Project:   projectChallenge,
				Merge:     mergeCriticIssues,
			},
			{
				ID:        "gate",
				Agents:    []string{"spec_gate"},
				DependsOn: []string{"critique"},
				Project:   projectSpecGate,
				Merge:     mergeGateVerdict,
				Budget:    budget,
				Spawn:     gateSpawnFor(thisIter, budget, template, historian),
			},
		}
	}
	return template
}

// parseSpecGateVerdict reads the SpecGateVerdict from the gate step's
// results. spec_gate uses structured output so the agent's Output is
// the verdict's JSON; we tolerate (and surface) any decode failure as
// an error so the spawner errors visibly rather than silently
// treating the verdict as non-converged.
func parseSpecGateVerdict(results []RoundResult) (*SpecGateVerdict, error) {
	for _, r := range results {
		if r.AgentID != "spec_gate" {
			continue
		}
		if r.Err != nil {
			return nil, fmt.Errorf("spec_gate run failed: %w", r.Err)
		}
		if strings.TrimSpace(r.Output) == "" {
			return nil, fmt.Errorf("spec_gate returned empty output")
		}
		var v SpecGateVerdict
		if err := json.Unmarshal([]byte(r.Output), &v); err != nil {
			return nil, fmt.Errorf("parse spec_gate verdict: %w (content=%q)", err, r.Output)
		}
		return &v, nil
	}
	return nil, fmt.Errorf("no spec_gate result in round")
}

// mergeGateVerdict parses the SpecGateVerdict and appends its
// OpenDimensions into state — both as Concerns (record-keeping,
// tagged spec_gate / high) and as FindingClusters (so the next
// iteration's revise fanout picks them up). When the verdict is
// Converged or empty, this is a no-op aside from recording the
// verdict for diagnostics.
func mergeGateVerdict(s *PlanningState, results []RoundResult) {
	verdict, err := parseSpecGateVerdict(results)
	if err != nil {
		// Don't blow up the merge; the Spawn closure parses again
		// and surfaces the error there with iteration context.
		return
	}
	if verdict.Converged {
		return
	}
	for _, dim := range verdict.OpenDimensions {
		s.Concerns = append(s.Concerns, Concern{
			AgentID:  "spec_gate",
			Severity: "high",
			Kind:     "convergence",
			Text:     dim,
		})
		s.FindingClusters = append(s.FindingClusters, FindingCluster{
			Topic:    dim,
			Findings: []string{dim},
			AgentID:  "spec_strategy_elaborator",
		})
	}
}

// projectSpecGate builds the prompt for the spec_gate agent. The
// agent's system prompt (spec_gate.md) carries the YES-question
// framing; we hand it the assembled ProposedSpec and any open
// concerns to grade.
func projectSpecGate(snap StateSnapshot[PlanningState]) []Message {
	s := snap.State
	var b strings.Builder
	b.WriteString("## Assembled spec proposal\n\n")
	if s.ProposedSpec != "" {
		b.WriteString("```json\n")
		b.WriteString(s.ProposedSpec)
		b.WriteString("\n```\n")
	} else {
		b.WriteString("(no proposal — earlier steps produced nothing to grade)\n")
	}
	if len(s.Concerns) > 0 {
		b.WriteString("\n## Open concerns\n")
		for _, c := range s.Concerns {
			fmt.Fprintf(&b, "- [%s/%s] %s\n", c.AgentID, c.Severity, c.Text)
		}
	}
	b.WriteString("\n## GOALS.md (project goals)\n\n")
	b.WriteString(s.Prompt)
	return []Message{{Role: "user", Content: b.String()}}
}

// convergenceFailedTerminal returns a one-shot WorkflowStep whose
// RunItem writes a DJ-103 history event tagged convergence_failed and
// then returns a non-nil error. The executor propagates the error
// through the workflow Run; GenerateSpec returns it and the calling
// command exits non-zero. Nothing persists to `.borg/spec/`.
func convergenceFailedTerminal(historian *history.Historian, snapState *PlanningState, verdict *SpecGateVerdict, iter, budget int) WorkflowStep[PlanningState] {
	terminalID := fmt.Sprintf("convergence_failed_iter:%d", iter)
	// Snapshot the in-progress ProposedSpec at spawn time — by the
	// time RunItem fires the snapshot may have moved on (no other
	// merges should run after a terminal step, but be defensive).
	snapshotSpec := snapState.ProposedSpec
	concerns := append([]Concern(nil), snapState.Concerns...)
	return WorkflowStep[PlanningState]{
		ID:     terminalID,
		Agents: []string{"spec_gate"},
		RunItem: func(_ context.Context, _ StateSnapshot[PlanningState]) (string, error) {
			if historian != nil {
				evt := buildConvergenceFailedEvent(verdict, iter, budget, snapshotSpec, concerns)
				if err := historian.Record(evt); err != nil {
					slog.Warn("convergence_failed: failed to record DJ-103 event", "err", err)
				}
			}
			return "", fmt.Errorf(
				"spec-generation council convergence failed after %d iteration(s); %d unresolved concern(s); verdict: %s",
				iter+1, len(concerns), verdict.Reasoning,
			)
		},
	}
}

// buildConvergenceFailedEvent constructs the DJ-103 history.Event
// payload. NewValue carries a JSON snapshot of the in-progress
// ProposedSpec for forensic review; Rationale carries the gate's last
// verdict reasoning plus the unresolved-concerns list.
func buildConvergenceFailedEvent(verdict *SpecGateVerdict, iter, budget int, snapshotSpec string, concerns []Concern) history.Event {
	now := time.Now()
	var rationale strings.Builder
	fmt.Fprintf(&rationale, "spec_gate verdict (iter %d/%d): %s", iter+1, budget, verdict.Reasoning)
	if len(verdict.OpenDimensions) > 0 {
		rationale.WriteString("\n\nOpen dimensions:")
		for _, d := range verdict.OpenDimensions {
			fmt.Fprintf(&rationale, "\n- %s", d)
		}
	}
	if len(concerns) > 0 {
		rationale.WriteString("\n\nUnresolved concerns at the time of failure:")
		for _, c := range concerns {
			fmt.Fprintf(&rationale, "\n- [%s/%s] %s", c.AgentID, c.Severity, c.Text)
		}
	}
	return history.Event{
		ID:        history.EventID("convergence_failed", "", now),
		Timestamp: now,
		Kind:      "convergence_failed",
		Rationale: rationale.String(),
		NewValue:  snapshotSpec,
	}
}

// hasUnmatchedFindings (DJ-098) gates the LLM clusterer step. True when
// the mechanical pre-pass left critic findings that name no existing
// node id. False when every finding has already been routed to a
// per-node cluster.
func hasUnmatchedFindings(s *PlanningState) bool { return len(s.UnmatchedFindings) > 0 }

// hasFindingClusters (DJ-098) gates the revise fanout and
// reconcile_revise. True when at least one FindingCluster (mechanical
// or LLM) is present. Skipped on runs where critics produced no
// findings.
func hasFindingClusters(s *PlanningState) bool { return len(s.FindingClusters) > 0 }

// fanoutOutlineFeatures returns one raw-JSON OutlineFeature per outlined
// feature. Each entry drives a per-element spec_feature_elaborator call.
func fanoutOutlineFeatures(state *PlanningState) ([]string, error) {
	if state == nil || state.Outline == "" {
		return nil, nil
	}
	var outline Outline
	if err := json.Unmarshal([]byte(state.Outline), &outline); err != nil {
		return nil, fmt.Errorf("parse outline: %w", err)
	}
	items := make([]any, 0, len(outline.Features))
	for _, f := range outline.Features {
		items = append(items, f)
	}
	return marshalFanoutItems(items)
}

// fanoutOutlineStrategies is the strategy counterpart to
// fanoutOutlineFeatures.
func fanoutOutlineStrategies(state *PlanningState) ([]string, error) {
	if state == nil || state.Outline == "" {
		return nil, nil
	}
	var outline Outline
	if err := json.Unmarshal([]byte(state.Outline), &outline); err != nil {
		return nil, fmt.Errorf("parse outline: %w", err)
	}
	items := make([]any, 0, len(outline.Strategies))
	for _, s := range outline.Strategies {
		items = append(items, s)
	}
	return marshalFanoutItems(items)
}

// fanoutFindingClusters (DJ-098 unified revise) returns one raw-JSON
// FindingCluster per non-empty cluster. AgentID is sniffed off each item
// by ExecuteRound so the dispatcher routes to the right elaborator per
// cluster. Empty-findings clusters are dropped here so the downstream
// dispatch never sees them.
func fanoutFindingClusters(state *PlanningState) ([]string, error) {
	if state == nil {
		return nil, nil
	}
	items := make([]any, 0, len(state.FindingClusters))
	for _, c := range state.FindingClusters {
		if len(c.Findings) == 0 {
			slog.Warn("fanout: dropping FindingCluster with no findings", "topic", c.Topic)
			continue
		}
		items = append(items, c)
	}
	return marshalFanoutItems(items)
}

// mergeScoutBrief stores the spec_scout's structured ScoutBrief output.
func mergeScoutBrief(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.ScoutBrief = v
	}
}

// mergeOutline stores the spec_outliner's Outline JSON. Stashed for the
// downstream fanout (fanoutOutlineFeatures/Strategies reads it) and for
// each elaborator's projection (sibling situational awareness).
func mergeOutline(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.Outline = v
	}
}

// mergeElaboratedFeatures appends each fanout call's output to
// ElaboratedFeatures and re-runs the assembler. Idempotent and order-
// independent — both elaborate-fanout merges converge on the same
// RawProposal as soon as the data is available.
func mergeElaboratedFeatures(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		s.ElaboratedFeatures = append(s.ElaboratedFeatures, r.Output)
	}
	if assembled, ok := assembleRawProposal(s); ok {
		s.RawProposal = assembled
		s.OriginalRawProposal = assembled
	}
}

// mergeElaboratedStrategies is the strategy counterpart.
func mergeElaboratedStrategies(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		s.ElaboratedStrategies = append(s.ElaboratedStrategies, r.Output)
	}
	if assembled, ok := assembleRawProposal(s); ok {
		s.RawProposal = assembled
		s.OriginalRawProposal = assembled
	}
}

// mergeReconciledProposal turns the reconciler's verdict into the
// canonical SpecProposal via ApplyReconciliation. Errors are recorded as
// integrity-kind Concerns so revise can surface them; the workflow
// itself does not fail.
func mergeReconciledProposal(s *PlanningState, results []RoundResult) {
	verdict := firstNonEmpty(results)
	if verdict == "" {
		return
	}
	canonical, applied, err := mergeReconcile(s.RawProposal, verdict, s.Existing)
	if err != nil {
		// First non-empty result's agent is the reconciler; surface
		// per-result errors via the reconcile-attribution path.
		for _, r := range results {
			if r.Err == nil && r.Output != "" {
				s.Concerns = append(s.Concerns, Concern{
					AgentID:  r.AgentID,
					Severity: "high",
					Text:     fmt.Sprintf("reconcile: %s", err.Error()),
				})
				return
			}
		}
		return
	}
	s.ProposedSpec = canonical
	s.ConflictActions = appendConflictActions(s.ConflictActions, applied)
}

// mergeCriticIssues parses each critic's CriticIssues output into
// per-issue Concerns tagged with the critic's lens (architecture,
// devops, sre, cost) for grouping in the revise prompt. After the LLM
// critics merge, runs the mechanical integrity critic and the
// mechanical cluster pre-pass (DJ-098).
func mergeCriticIssues(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		kind := critiqueKindFor(r.AgentID)
		var ci CriticIssues
		if err := json.Unmarshal([]byte(r.Output), &ci); err != nil {
			s.Concerns = append(s.Concerns, Concern{
				AgentID:  r.AgentID,
				Severity: "medium",
				Kind:     kind,
				Text:     r.Output,
			})
			continue
		}
		for _, issue := range ci.Issues {
			s.Concerns = append(s.Concerns, Concern{
				AgentID:  r.AgentID,
				Severity: "medium",
				Kind:     kind,
				Text:     issue,
			})
		}
	}
	appendIntegrityFindings(s)
	runMechanicalCluster(s)
}

// mergeFindingClusters (DJ-098) promotes the LLM clusterer's output into
// FindingCluster entries with AgentID set from kind, appending to the
// mechanical pre-pass output.
func mergeFindingClusters(s *PlanningState, results []RoundResult) {
	if v := firstNonEmpty(results); v != "" {
		s.FindingClusters = append(s.FindingClusters, PromoteLLMClusters(v)...)
	}
}

// mergeRevisedNodes (DJ-098) accumulates per-cluster elaborator outputs
// and rebuilds RawProposal. Each entry is one RawFeatureProposal or
// RawStrategyProposal; the assembler sniffs id prefix and decides
// revise (id matches existing) vs add (fresh id) per entry.
// Idempotent.
func mergeRevisedNodes(s *PlanningState, results []RoundResult) {
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		s.RevisedNodes = append(s.RevisedNodes, r.Output)
	}
	if merged, ok := assembleRevisedRawProposal(s); ok {
		s.RawProposal = merged
	}
}
