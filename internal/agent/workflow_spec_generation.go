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

// recurrenceTerminationThreshold caps how many iterations the same
// (deliverable, axis) pair can appear in gate verdicts before the
// workflow force-terminates with a "convergence_stuck" history event.
// Three is the threshold the winplan smoke run validated: on-call
// ownership recurred in iter-1, iter-2, and iter-3 gates despite the
// elaborator committing PagerDuty + developer-led rotation in iter-1
// — by iter-3 it's clear the gate is goalpost-shifting, not finding
// real residual gaps. Force-termination produces a clear failure mode
// (and a forensic history event) rather than letting the run drift to
// budget exhaustion with no diagnosis.
const recurrenceTerminationThreshold = 3

// SpecGateVerdict is the structured output of the spec_gate agent. The
// gate reads the assembled ProposedSpec, GOALS.md, and any open concerns;
// it returns Converged=true when the four-lifecycle-phases YES question
// holds for every deliverable, or Converged=false plus a list of
// OpenDimensions naming each specific axis still unresolved. Schema is
// registered in schemas.go so providers enforce structured output.
//
// Field roles:
//
//   - Reasoning is a one-sentence headline naming the verdict — NOT the
//     gap list. Specific gaps belong on OpenDimensions entries, not in
//     prose. This split exists because the first DJ-122 smoke run had
//     every gap richly described in Reasoning and OpenDimensions empty,
//     so mergeGateVerdict had nothing to act on and the next iteration
//     couldn't address the gate's judgment. Splitting the field shape
//     removes the path of least resistance for the model to put gaps
//     where the workflow can't reach them.
//
//   - OpenDimensions is the canonical list. Each entry is a structured
//     OpenDimension with deliverable, phase, axis, and per-dimension
//     reasoning. mergeGateVerdict converts each into a Concern + a
//     FindingCluster so the next iteration's revise fanout addresses
//     the gate's specific judgment, not just the parallel critic loop's
//     findings.
//
// Per CLAUDE.md: enum-shaped fields carry `jsonschema:"enum=..."`,
// constrained fields name their constraint in `description=...`, and
// the example payload in schemas.go uses descriptive prose, not
// placeholder tokens.
//
// Distinct from the older ConvergenceVerdict in convergence.go, which
// drives the prose-parsed convergence-monitor path used by RunCouncil's
// plan/assimilation flow. DJ-122 supersedes that for the spec-gen
// council specifically; the older type stays for the remaining callers.
type SpecGateVerdict struct {
	Converged bool `json:"converged" jsonschema:"description=True only when the assembled ProposedSpec answers YES to the four-lifecycle-phases convergence question (define / develop / deploy / support) for every deliverable named in GOALS.md. False when at least one phase remains underspecified for at least one deliverable."`

	Reasoning string `json:"reasoning" jsonschema:"description=One sentence stating the verdict and pointing at the dominant pattern — e.g., 'Define and develop are committed across all deliverables; deploy and support carry the remaining gaps listed below.' This is a headline; not the gap list. Every specific gap lives on an OpenDimensions entry. Generic claims like 'looks good' or 'needs more work' are rejected."`

	OpenDimensions []OpenDimension `json:"open_dimensions" jsonschema:"description=One entry per still-unresolved axis. Empty array exactly when Converged is true. When Converged is false; every gap your reasoning identifies MUST appear here as a structured entry — putting gaps only in the reasoning sentence is rejected by the workflow."`
}

// OpenDimension is one structured gap the spec_gate identified. The
// fields are sized so downstream consumers (mergeGateVerdict, the next
// iteration's revise prompt, the eventual CLI renderer) can act on
// them without re-parsing prose:
//
//   - Deliverable names the artifact the gap applies to ("iOS companion
//     app", "nRF52840 firmware", "Vapor backend"). Use the same domain
//     vocabulary the spec proposal uses for ids.
//
//   - Phase is one of define / develop / deploy / support — the
//     lifecycle phase the gap belongs to. Enum-enforced.
//
//   - Axis is the specific dimension that's uncommitted — what the
//     architect needs to decide. A noun phrase, not a sentence.
//
//   - Reasoning is one sentence saying why this gap blocks the YES.
//     Becomes the Concern text the next iteration's revise sees.
type OpenDimension struct {
	Deliverable string `json:"deliverable" jsonschema:"description=The artifact this gap applies to; named in the proposal's own vocabulary — 'iOS companion app'; 'nRF52840 firmware'; 'Vapor backend'; 'campaign analytics dashboard'. Use the same name across iterations so the workflow can recognize when an axis has been raised before."`

	Phase string `json:"phase" jsonschema:"enum=define,enum=develop,enum=deploy,enum=support,description=The lifecycle phase this gap belongs to. define: who/what; success criteria; scope. develop: language/framework/testing/contracts. deploy: distribution; environments; rollout; secrets; signing; OTA; certification. support: observability; on-call; SLO; lifetime; compliance."`

	Axis string `json:"axis" jsonschema:"description=The specific dimension uncommitted — what the architect needs to decide. A noun phrase the next iteration's revise can act on: 'App Store / TestFlight rollout cadence'; 'OTA update channel'; 'cost ceiling'; 'on-call rotation owner'. Specific enough that the elaborator knows what to commit (a cadence; a channel; a number; a role); not a category ('deployment story'; 'observability')."`

	Reasoning string `json:"reasoning" jsonschema:"description=One sentence stating why leaving this axis uncommitted blocks define/develop/deploy/support for the named deliverable. Becomes the Concern text the next iteration's revise sees; write for that reader."`

	CurrentCommitmentQuoted string `json:"current_commitment_quoted" jsonschema:"description=Verbatim text from the current proposal that you judge insufficient on this axis — usually one or two sentences from a strategy body or a decision rationale. Populate when the proposal contains text on this axis but the commitment doesn't go far enough; the next iteration's elaborator sees this quoted passage alongside the finding so it strengthens the right text rather than rewriting the strategy from scratch. Leave empty (the empty string) when the proposal contains nothing on this axis at all; empty means truly absent. A role-class commitment ('developer-led on-call'; 'platform engineers carry the pager') counts as a commitment for an ownership axis; a vendor or tool ('PagerDuty'; 'Datadog') counts as a commitment for the corresponding axis; a numeric threshold ('99.9% availability') counts as a commitment for an SLO axis."`
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

		// Non-progress detection: if any (deliverable, axis) pair has
		// recurred at the termination threshold, the gate is not
		// making forward progress on that axis — either it's
		// goalpost-shifting against a present-but-elaborated
		// commitment, or the elaborator is genuinely unable to
		// strengthen the commitment further. Either way the loop is
		// not converging on the recurrent axis; force-terminate with
		// the stuck axes named so the failure mode is diagnosable
		// instead of letting the run drift to budget exhaustion.
		//
		// Check BEFORE budget — a stuck loop should fail with the
		// stuck-axes diagnosis, not budget exhaustion.
		if stuck := stuckAxes(&snap.State); len(stuck) > 0 {
			terminal := convergenceStuckTerminal(historian, &snap.State, verdict, myIter, stuck)
			return []WorkflowStep[PlanningState]{terminal}, nil, nil
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

// convergenceStuckTerminal returns a one-shot WorkflowStep whose
// RunItem writes a DJ-103 history event tagged convergence_stuck and
// errors out. Differs from convergenceFailedTerminal in the failure
// reason: budget exhaustion means the problem is hard or the
// prompting is wrong; stuck means a specific axis recurred without
// the elaborator producing a substantively different commitment, so
// the gate is most likely goalpost-shifting or the elaborator is at
// its ceiling for that axis. The history event names the stuck axes
// so the operator can see WHICH axes caused the stall.
func convergenceStuckTerminal(historian *history.Historian, snapState *PlanningState, verdict *SpecGateVerdict, iter int, stuck []string) WorkflowStep[PlanningState] {
	terminalID := fmt.Sprintf("convergence_stuck_iter:%d", iter)
	snapshotSpec := snapState.ProposedSpec
	concerns := append([]Concern(nil), snapState.Concerns...)
	stuckCopy := append([]string(nil), stuck...)
	return WorkflowStep[PlanningState]{
		ID:     terminalID,
		Agents: []string{"spec_gate"},
		RunItem: func(_ context.Context, _ StateSnapshot[PlanningState]) (string, error) {
			if historian != nil {
				evt := buildConvergenceStuckEvent(verdict, iter, snapshotSpec, concerns, stuckCopy)
				if err := historian.Record(evt); err != nil {
					slog.Warn("convergence_stuck: failed to record DJ-103 event", "err", err)
				}
			}
			return "", fmt.Errorf(
				"spec-generation council stuck at iter %d: %d axis/axes recurred %d+ times without convergence — likely gate goalpost-shifting or elaborator ceiling. Stuck axes: %s",
				iter+1, len(stuckCopy), recurrenceTerminationThreshold, strings.Join(stuckCopy, "; "),
			)
		},
	}
}

// buildConvergenceStuckEvent constructs the DJ-103 history.Event for
// a stuck-recurrence termination. NewValue carries the
// ProposedSpec snapshot; Rationale names the stuck axes and the
// last verdict reasoning so a forensic reader can see what the gate
// was saying when the loop stalled.
func buildConvergenceStuckEvent(verdict *SpecGateVerdict, iter int, snapshotSpec string, concerns []Concern, stuck []string) history.Event {
	now := time.Now()
	var rationale strings.Builder
	fmt.Fprintf(&rationale, "spec_gate stuck at iter %d: %d axis/axes recurred %d+ times.\n\nStuck axes:",
		iter+1, len(stuck), recurrenceTerminationThreshold)
	for _, s := range stuck {
		fmt.Fprintf(&rationale, "\n- %s", s)
	}
	fmt.Fprintf(&rationale, "\n\nLast verdict reasoning: %s", verdict.Reasoning)
	if len(verdict.OpenDimensions) > 0 {
		rationale.WriteString("\n\nLast iteration's open dimensions:")
		for _, d := range verdict.OpenDimensions {
			fmt.Fprintf(&rationale, "\n- [%s] %s :: %s", d.Phase, d.Deliverable, d.Axis)
		}
	}
	if len(concerns) > 0 {
		rationale.WriteString("\n\nUnresolved concerns at the time of stall:")
		for _, c := range concerns {
			fmt.Fprintf(&rationale, "\n- [%s/%s] %s", c.AgentID, c.Severity, c.Text)
		}
	}
	return history.Event{
		ID:        history.EventID("convergence_stuck", "", now),
		Timestamp: now,
		Kind:      "convergence_stuck",
		Rationale: rationale.String(),
		NewValue:  snapshotSpec,
	}
}

// convergenceLoopTemplate returns the template closure used by
// AppendSubgraph to expand one iteration of the spec-council
// convergence loop. Each iteration is a five-step chain:
//
//	revise → reconcile → critique → cluster_findings → gate
//
// cluster_findings runs in every iteration (not just the initial
// graph) so per-iteration critic free-text findings that don't name a
// specific spec-node id get LLM-clustered into the same FindingCluster
// shape mechanically-routed findings produce. Without it, iter-N's
// critic concerns leak into state.UnmatchedFindings and never reach
// iter-(N+1)'s revise — observed during the first DJ-122 smoke run.
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
				// cluster_findings: LLM-cluster any unmatched
				// free-text findings from this iteration's critics so
				// they reach the next iteration's revise as
				// FindingClusters. Skips when no unmatched findings
				// exist (the conditional matches the initial graph's
				// instance).
				ID:          "cluster_findings",
				Agents:      []string{"spec_finding_clusterer"},
				DependsOn:   []string{"critique"},
				Conditional: hasUnmatchedFindings,
				Project:     projectClusterFindings,
				Merge:       mergeFindingClusters,
			},
			{
				ID:        "gate",
				Agents:    []string{"spec_gate"},
				DependsOn: []string{"cluster_findings"},
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
//
// Defensive validation, beyond decode:
//
//   - Converged=false with len(OpenDimensions)==0 is rejected.
//     Surfaced as a real DJ-122 calibration failure: the gate produced
//     a verdict the workflow cannot act on. Without this guard, the
//     model's tendency to put gaps in the reasoning sentence and
//     leave OpenDimensions empty would leak silently as "no work to
//     do in the next iteration."
//
//   - Converged=true with len(OpenDimensions)>0 is also rejected.
//     Same workflow contract: a converged spec has no remaining gaps;
//     a verdict claiming both is contradictory and must not advance.
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
		if !v.Converged && len(v.OpenDimensions) == 0 {
			return nil, fmt.Errorf(
				"spec_gate verdict is degenerate: converged=false with open_dimensions empty. "+
					"The gate must list every gap as an OpenDimension entry; putting them only in the reasoning sentence is rejected "+
					"because the next iteration's revise has nothing to act on. Reasoning was: %s",
				truncateForError(v.Reasoning))
		}
		if v.Converged && len(v.OpenDimensions) > 0 {
			return nil, fmt.Errorf(
				"spec_gate verdict is contradictory: converged=true with %d open_dimensions. "+
					"A converged spec has no remaining gaps; either the gate should have returned converged=false, or the dimensions are not actual gaps",
				len(v.OpenDimensions))
		}
		return &v, nil
	}
	return nil, fmt.Errorf("no spec_gate result in round")
}

// truncateForError returns s truncated to ~200 chars with an ellipsis
// suffix. Used inside parseSpecGateVerdict's error message so a long
// reasoning paragraph doesn't dominate the error output.
func truncateForError(s string) string {
	const max = 200
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// mergeGateVerdict parses the SpecGateVerdict and threads each
// OpenDimension into the state the next iteration's revise reads:
//
//   - As a Concern (record-keeping). AgentID is the gate, Severity
//     "high", Kind tagged with the dimension's lifecycle phase so the
//     revise prompt can group by phase the same way critic concerns
//     are grouped.
//
//   - As a FindingCluster (so revise fanout picks it up). Topic names
//     the deliverable + axis so the elaborator's prompt has the gap
//     specifically; Findings carries the per-dimension reasoning text
//     so the elaborator sees WHY the axis matters.
//
// When the verdict is degenerate (covered by parseSpecGateVerdict's
// validation), this merge is a no-op — the error path in the
// spawner surfaces it.
func mergeGateVerdict(s *PlanningState, results []RoundResult) {
	verdict, err := parseSpecGateVerdict(results)
	if err != nil {
		// The Spawn closure parses again and surfaces the error
		// with iteration context. Merge is the wrong place to
		// fail loudly.
		return
	}
	if verdict.Converged {
		return
	}
	if s.GateAxisRecurrence == nil {
		s.GateAxisRecurrence = make(map[string]int, len(verdict.OpenDimensions))
	}
	for _, dim := range verdict.OpenDimensions {
		topic := dim.Axis
		if dim.Deliverable != "" {
			topic = dim.Deliverable + ": " + dim.Axis
		}
		s.Concerns = append(s.Concerns, Concern{
			AgentID:  "spec_gate",
			Severity: "high",
			Kind:     dim.Phase, // define / develop / deploy / support
			Text:     dim.Reasoning,
		})
		s.FindingClusters = append(s.FindingClusters, FindingCluster{
			Topic:                   topic,
			Findings:                []string{dim.Reasoning},
			AgentID:                 "spec_strategy_elaborator",
			CurrentCommitmentQuoted: dim.CurrentCommitmentQuoted,
		})
		s.GateAxisRecurrence[gateAxisKey(dim)]++
	}
}

// gateAxisKey builds the canonical key the GateAxisRecurrence map
// uses. Lowercased + pipe-joined so casing drift across iterations
// ("On-call rotation owner" vs "on-call rotation owner") doesn't fork
// the bucket. Whitespace is trimmed but interior punctuation is left
// alone — slight wording variation ("on-call owner" vs "on-call
// rotation owner") will fork; that's acceptable because such variation
// is itself a signal the gate isn't being stable about axis naming.
func gateAxisKey(dim OpenDimension) string {
	return strings.ToLower(strings.TrimSpace(dim.Deliverable)) + "|" + strings.ToLower(strings.TrimSpace(dim.Axis))
}

// stuckAxes returns the (deliverable, axis) pairs that have recurred
// at or above the termination threshold. Empty when the loop is still
// making progress.
func stuckAxes(s *PlanningState) []string {
	if s == nil || len(s.GateAxisRecurrence) == 0 {
		return nil
	}
	var stuck []string
	for key, count := range s.GateAxisRecurrence {
		if count >= recurrenceTerminationThreshold {
			stuck = append(stuck, fmt.Sprintf("%s (raised %d×)", key, count))
		}
	}
	return stuck
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
