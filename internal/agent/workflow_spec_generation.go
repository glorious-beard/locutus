package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
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
// import <doc>`'s post-admission planning pass under DJ-124.
//
// The DJ-124 round shape is:
//
//	scout → decisions(per-axis fanout)
//	      → narrative(per-affected-node fanout)
//	      → reconcile → critique → scout(next iter)
//
// Concretely the initial graph is just the iter-0 scout. Its Spawn
// closure parses the ScoutBrief and either:
//
//   - exits (Converged: true);
//   - expands the iteration template at iter 1; or
//   - terminates with a convergence_failed history event (budget == 0
//     edge case where no iterations are allowed).
//
// Each iteration template adds five steps (decisions → narrative →
// reconcile → critique → scout). The scout at the tail of every
// iteration uses the same Spawn pattern as the initial scout, so the
// loop drives itself one iteration at a time.
//
// Decisions are dispatched once per ScoutBrief.AxesOpen entry; the
// scout's gap analyzer surfaces only uncovered axes so the loop
// converges by closing them. Narrative dispatch is conditional: it
// fires only for affected nodes (features / strategies referencing any
// just-minted decision, nodes named in critic findings, and new nodes
// the scout introduced this iteration). Unchanged nodes keep their
// prior body across iterations.
//
// MaxRounds=1 because the spawner-driven loop owns iteration; the
// executor's outer convergence pass is unused.
//
// Per-model concurrency caps live in models.yaml's `concurrent_requests`
// field. Even with Parallel=true on fanout steps, the actual
// concurrency is bounded so fanout never floods a model past its
// configured slot count.
//
// Cycle detection: when scout's axes_open contains an axis ID already
// recorded in DecidedAxesByIter (i.e. the same axis was decided in a
// prior iteration and is being re-opened), the workflow terminates
// with a convergence_stuck DJ-103 history event naming the recurring
// axes. Budget exhaustion produces a convergence_failed event.
func NewSpecGenerationWorkflow(historian *history.Historian, budget int) *Workflow[PlanningState] {
	if budget <= 0 {
		budget = defaultSpecGateBudget
	}
	loopTemplate := convergenceLoopTemplate(historian, budget)
	return &Workflow[PlanningState]{
		Snapshot:           snapshotPlanningState,
		DefaultProject:     projectDefault,
		MaxRounds:          1,
		DefaultGateBudget:  budget,
		MaxGraphMultiplier: 100,
		Rounds: []WorkflowStep[PlanningState]{
			{
				ID:      "scout",
				Agents:  []string{"spec_scout"},
				Project: projectScout,
				Merge:   mergeScoutBrief,
				Budget:  budget,
				Spawn:   scoutSpawnFor(0, budget, loopTemplate, historian),
			},
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

// decisionRevisedEvent constructs the DJ-103 history.Event for one
// replace-by-axis-ID match. TargetID is the decision id (preserved
// from the prior); OldValue is the prior body as JSON; NewValue is
// the revised body as JSON; Rationale names every concern that drove
// the revision with its severity, agent_id, and iteration. Empty
// prior body (legacy revisions of pre-DJ-124 decisions without a
// rendered RawDecisionProposal shape) is serialised as null.
func decisionRevisedEvent(ev PendingDecisionRevisedEvent) history.Event {
	now := time.Now()
	priorJSON, _ := json.MarshalIndent(ev.Prior, "", "  ")
	revisedJSON, _ := json.MarshalIndent(ev.Revised, "", "  ")
	var rationale strings.Builder
	fmt.Fprintf(&rationale, "Decision %s revised at iter %d.", ev.Revised.ID, ev.Iter+1)
	if len(ev.DrivingConcerns) > 0 {
		rationale.WriteString("\n\nDriving concerns:")
		for _, c := range ev.DrivingConcerns {
			fmt.Fprintf(&rationale, "\n- [%s/%s] %s", c.AgentID, c.Severity, c.Text)
		}
	}
	return history.Event{
		ID:        history.EventID("decision_revised", ev.Revised.ID, now),
		Timestamp: now,
		Kind:      "decision_revised",
		TargetID:  ev.Revised.ID,
		OldValue:  string(priorJSON),
		NewValue:  string(revisedJSON),
		Rationale: rationale.String(),
	}
}

// mergeDecisionsRecording wraps mergeDecisions: it performs the merge
// as usual, then drains state.PendingDecisionRevisedEvents through the
// historian (nil-safe). Used by convergenceLoopTemplate so the
// production loop emits decision_revised DJ-103 events for every
// replace-by-axis-ID match; unit tests that call mergeDecisions
// directly skip the recording side-effect (the pending slice
// accumulates on state and stays there harmlessly).
func mergeDecisionsRecording(historian *history.Historian) func(*PlanningState, []RoundResult) {
	return func(s *PlanningState, results []RoundResult) {
		mergeDecisions(s, results)
		if s == nil || len(s.PendingDecisionRevisedEvents) == 0 {
			return
		}
		if historian != nil {
			for _, ev := range s.PendingDecisionRevisedEvents {
				if err := historian.Record(decisionRevisedEvent(ev)); err != nil {
					slog.Warn("decision_revised: failed to record DJ-103 event", "err", err, "target_id", ev.Revised.ID)
				}
			}
		}
		s.PendingDecisionRevisedEvents = nil
	}
}

// convergenceLoopTemplate (DJ-124) returns the template closure
// AppendSubgraph uses to expand one iteration of the new scout-driven
// spec-council loop. Each iteration is a five-step chain:
//
//	decisions → narrative → reconcile → critique → scout
//
// Decisions fanout dispatches one spec_decision_elaborator per axis in
// state.AxesOpen. Narrative fanout dispatches one elaborator
// (feature or strategy, routed via the per-item agent_id) per affected
// node — the set computed by computeAffectedNodes from changed
// decisions, critic-named nodes, and scout-introduced new nodes.
// Reconcile field-maps the assembled RawSpecProposal into the canonical
// SpecProposal via ApplyReconciliation; the reconciler agent's verdict
// is parsed-but-ignored under DJ-124 Stage A. Critique runs the four
// LLM critics plus the mechanical integrity critic. The tail scout
// re-judges convergence; its Spawn closure either terminates the loop
// or expands the next iteration template.
//
// Internal DependsOn entries name siblings by their base id and are
// rewritten by AppendSubgraph to the prefixed form. The template's
// scout carries the same Spawn closure pattern as the initial-graph
// scout, so the loop drives itself one iteration at a time.
func convergenceLoopTemplate(historian *history.Historian, budget int) func(executor.IterationContext) []WorkflowStep[PlanningState] {
	// One recording wrapper shared by both decision-elaborator dispatch
	// sites — first-author (decisions) and revise (revise-decisions).
	// The wrapper drains state.PendingDecisionRevisedEvents into the
	// historian after each merge call, so DJ-103 decision_revised
	// events fire for every replace-by-axis-ID match. First-author
	// merges produce no pending events; the drain is a no-op there.
	decisionMerge := mergeDecisionsRecording(historian)

	var template func(executor.IterationContext) []WorkflowStep[PlanningState]
	template = func(ic executor.IterationContext) []WorkflowStep[PlanningState] {
		thisIter := ic.IterationIndex
		return []WorkflowStep[PlanningState]{
			{
				// candidate-survey (DJ-132): fanout per OpenAxis. Runs
				// BEFORE the decisions step on the initial-dispatch
				// path. Each call enumerates the candidate space for
				// one axis (fast tier, grounded); the merge stores the
				// CandidateList keyed by axis ID on
				// state.AxisSurveys. projectOpenAxis then renders the
				// per-axis surveyed candidate list as a section in the
				// elaborator's input so the elaborator's initial
				// alternatives slice starts pre-populated.
				//
				// Skipped naturally on iterations with no open axes
				// (hasOpenAxes is false) and on revise dispatches
				// (revises use the separate revise-decisions step
				// which dispatches by concern, not axis).
				ID:          "candidate-survey",
				Agents:      []string{"spec_candidate_survey"},
				Parallel:    true,
				Conditional: hasOpenAxes,
				Fanout:      fanoutOpenAxes,
				Project:     projectCandidateSurvey,
				Merge:       mergeCandidateSurveys,
			},
			{
				// decisions: fanout per OpenAxis. The decision-elaborator
				// researches and commits one decision per axis; the
				// merge appends to state.RawProposal.Decisions[],
				// records DecidedAxesByIter for cycle detection, and
				// appends newly-minted decision IDs to NewNodesFromScout
				// entries that surfaced the axis.
				//
				// DependsOn:candidate-survey so the survey's merge has
				// populated state.AxisSurveys before projectOpenAxis
				// reads it for the per-axis projection. The dependency
				// is at the step level (decisions waits for the entire
				// survey fanout to merge), so per-axis sequencing
				// (survey for A → elaborator for A) is enforced by
				// the merged AxisSurveys map being a complete
				// snapshot before any decisions call dispatches.
				ID:          "decisions",
				Agents:      []string{"spec_decision_elaborator"},
				Parallel:    true,
				DependsOn:   []string{"candidate-survey"},
				Conditional: hasOpenAxes,
				Fanout:      fanoutOpenAxes,
				Project:     projectOpenAxis,
				Merge:       decisionMerge,
			},
			{
				// narrative: fanout per affected node. The per-item
				// agent_id discriminator routes each item to either
				// spec_feature_elaborator or spec_strategy_elaborator;
				// the merge replaces (or appends) the matching entry in
				// state.RawProposal.Features / Strategies.
				ID:          "narrative",
				Agents:      []string{"spec_feature_elaborator"},
				Parallel:    true,
				DependsOn:   []string{"decisions"},
				Conditional: hasAffectedNodes,
				Fanout:      fanoutAffectedNodes,
				Project:     projectAffectedNode,
				Merge:       mergeNarrative,
			},
			{
				// revise-decisions (DJ-126): fanout per open concern
				// with RelatedDecisionIDs that name a decision in the
				// in-flight or existing graph. Each fanout item
				// dispatches the spec_decision_elaborator in revise
				// mode against one (concern, prior decision) pair; the
				// merge function replaces the prior decision in-place
				// by axis-ID intersection (Phase 3). Fires after
				// narrative so a feature/strategy body that resolves
				// the concern naturally can mark it addressed via the
				// scout's next-iteration grading pass instead of
				// triggering a revision.
				ID:          "revise-decisions",
				Agents:      []string{"spec_decision_elaborator"},
				Parallel:    true,
				DependsOn:   []string{"narrative"},
				Conditional: hasReviseableConcerns,
				Fanout:      fanoutReviseableConcerns,
				Project:     projectReviseDecision,
				Merge:       decisionMerge,
			},
			{
				// reconcile: spec_reconciler runs for API-layer schema
				// stability (the agent is still on disk and its strict-
				// mode verdict must be a valid ReconciliationVerdict),
				// but ApplyReconciliation ignores the verdict content
				// under DJ-124. The merge field-maps RawSpecProposal →
				// SpecProposal and surfaces dangling decision references
				// onto state.DanglingReferences so the next scout pass
				// can address them.
				//
				// DependsOn:revise-decisions so the field-map runs after
				// any revisions land in state.RawProposal; skipped
				// revise-decisions still counts as completed in the
				// executor so this dependency is safe on iterations
				// with no reviseable concerns.
				ID:        "reconcile",
				Agents:    []string{"spec_reconciler"},
				DependsOn: []string{"revise-decisions"},
				Project:   projectReconcile,
				Merge:     mergeReconciledProposal,
			},
			{
				// DJ-129: critique is a Fanout over CritiqueDimensions
				// the scout surfaces. One spec_critic_elaborator call
				// per dimension. When the scout surfaces zero
				// dimensions, the fanout fires zero items and the step
				// becomes a no-op.
				ID:        "critique",
				Agents:    []string{"spec_critic_elaborator"},
				DependsOn: []string{"reconcile"},
				Fanout:    fanoutCritiqueDimensions,
				Project:   projectCritiqueDimension,
				Merge:     mergeCriticIssues,
			},
			{
				// scout (tail of iteration): re-judges convergence. The
				// Spawn closure parses ScoutBrief.Converged, checks for
				// cycle (axes_open re-emitting a decided axis), and
				// either expands the next iteration template or spawns
				// a terminal step.
				ID:        "scout",
				Agents:    []string{"spec_scout"},
				DependsOn: []string{"critique"},
				Project:   projectScout,
				Merge:     mergeScoutBrief,
				Budget:    budget,
				Spawn:     scoutSpawnFor(thisIter, budget, template, historian),
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

// mergeScoutBrief stores the spec_scout's structured ScoutBrief output
// and (DJ-124) projects the gap-analyzer fields (AxesOpen, NewNodes)
// onto state for the dispatch closures the next iteration template
// consumes.
//
// Cross-iteration chaining: the prior ScoutBrief is captured into
// state.PriorScoutBrief BEFORE the new brief overwrites state.ScoutBrief,
// so the scout's own projection on the next iteration can compare what
// it said last time against the current state.
//
// Robust to non-DJ-124 input: when the result JSON lacks AxesOpen /
// NewNodes (e.g. tests that hand the merge a pre-DJ-124 brief), the
// fields parse as zero values and the dispatcher fanout closures
// behave as no-ops. The legacy ScoutBrief storage (state.ScoutBrief =
// raw JSON for projection.formatScoutBrief) is preserved verbatim.
func mergeScoutBrief(s *PlanningState, results []RoundResult) {
	v := firstNonEmpty(results)
	if v == "" {
		return
	}
	// Capture prior brief before overwriting so the scout's next-iter
	// projection can render the previous output as context.
	s.PriorScoutBrief = s.ScoutBrief
	s.ScoutBrief = v

	// Pick the iteration index off the first result that supplied
	// the brief content. RoundResult.IterationIndex is stamped by the
	// RunStep wrapper from the executor.Step.
	var iter int
	for _, r := range results {
		if r.Output == v {
			iter = r.IterationIndex
			break
		}
	}

	var brief ScoutBrief
	if err := json.Unmarshal([]byte(v), &brief); err != nil {
		// Brief is unparseable as a ScoutBrief — leave AxesOpen /
		// NewNodesFromScout cleared so the dispatcher fanouts fire no
		// items. The scout spawner will surface the parse failure when
		// it tries to read Converged.
		s.AxesOpen = nil
		s.NewNodesFromScout = nil
		s.CurrentCritiqueDimensions = nil
		return
	}
	// Replace (not append) the per-iteration slices. Axes closed in the
	// prior iteration naturally drop off because the scout only emits
	// uncovered axes; new axes get added. Same shape for NewNodes.
	if len(brief.AxesOpen) > 0 {
		s.AxesOpen = append([]OpenAxis(nil), brief.AxesOpen...)
	} else {
		s.AxesOpen = nil
	}
	if len(brief.NewNodes) > 0 {
		s.NewNodesFromScout = append([]NewSpecNode(nil), brief.NewNodes...)
	} else {
		s.NewNodesFromScout = nil
	}

	// DJ-129: absorb critique dimensions onto PlanningState. Compute
	// stability BEFORE folding new dims into CritiqueDimensionsByIter
	// (otherwise the new dims would already be "seen" by the time
	// scoutSpawnFor checks). Then record so the next iteration's
	// check sees the dims as historical. Per design decision #7,
	// recordDimensionStability is append-only so the historical
	// signal of "this dimension was considered" survives retirement.
	if len(brief.CritiqueDimensions) > 0 {
		s.CurrentCritiqueDimensions = append([]CritiqueDimension(nil), brief.CritiqueDimensions...)
	} else {
		s.CurrentCritiqueDimensions = nil
	}
	s.LastDimensionsStable = dimensionsAreStable(s)
	recordDimensionStability(s, brief.CritiqueDimensions, iter)

	// DJ-125 Phase 7: apply scout-graded concern dispositions onto
	// state.Concerns by id-match. The id is the manifest position
	// (c-<index>); unknown ids are logged and skipped — convergence
	// treats them as still-open per ConcernDisposition documentation.
	applyConcernDispositions(s, brief.ConcernDispositions)
}

// applyConcernDispositions writes scout-graded dispositions onto
// state.Concerns. Each disposition addresses the concern by its
// manifest position id (c-<index>). The scout's set of valid
// dispositions is addressed / wontfix / still_open; stale is owned by
// the mechanical pre-pass and the scout never emits it (the schema's
// enum tag forbids it). still_open is a no-op transition (leaves
// Status as ConcernStatusOpen).
func applyConcernDispositions(s *PlanningState, dispositions []ConcernDisposition) {
	if s == nil || len(dispositions) == 0 {
		return
	}
	for _, d := range dispositions {
		idx, ok := parseConcernIDIndex(d.ConcernID)
		if !ok || idx < 0 || idx >= len(s.Concerns) {
			slog.Warn("scout disposition references unknown concern id; skipping",
				"concern_id", d.ConcernID, "disposition", d.Disposition)
			continue
		}
		target := &s.Concerns[idx]
		// Do not overwrite an already-disposed concern. The scout sees
		// the manifest's open concerns; concurrent mechanical staleness
		// pre-pass dispositions are durable.
		if target.Status != ConcernStatusOpen && target.Status != "" {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(d.Disposition)) {
		case "addressed":
			target.Status = ConcernStatusAddressed
			target.Justification = d.Justification
		case "wontfix":
			target.Status = ConcernStatusWontfix
			target.Justification = d.Justification
		case "still_open":
			// Leave Status as open; record the justification so the
			// next iteration's scout can read why the prior pass
			// thought the concern was still open.
			target.Justification = d.Justification
		default:
			slog.Warn("scout disposition has unknown value; skipping",
				"concern_id", d.ConcernID, "disposition", d.Disposition)
		}
	}
}

// parseConcernIDIndex parses a manifest concern ID ("c-<index>") into
// the numeric index. Returns ok=false on malformed input so the caller
// skips the disposition.
func parseConcernIDIndex(id string) (int, bool) {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "c-") {
		return 0, false
	}
	n, err := strconv.Atoi(id[2:])
	if err != nil {
		return 0, false
	}
	return n, true
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
	rebuildInFlightIndex(s)
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
	rebuildInFlightIndex(s)
}

// mergeReconciledProposal turns the reconciler's verdict into the
// canonical SpecProposal via ApplyReconciliation. Errors are recorded as
// integrity-kind Concerns so revise can surface them; the workflow
// itself does not fail.
//
// DJ-124: integrity-violation entries from ApplyReconciliation (one per
// dangling decision reference) are also surfaced onto
// state.DanglingReferences so the next scout iteration sees them. The
// scout's projection threads them into the next-iter prompt as
// concerns; the model corrects new_nodes[].decisions[] to drop or
// replace the bad ref.
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
	// DJ-124: dangling decision references flagged by ApplyReconciliation
	// flow onto state.DanglingReferences so the next scout iteration's
	// projection renders them. Replace (not append) on each reconcile
	// pass so stale references from prior iterations don't accumulate.
	s.DanglingReferences = collectDanglingReferences(applied)
	rebuildInFlightIndex(s)
}

// collectDanglingReferences returns one human-readable line per
// integrity_violation AppliedAction. Format: "feature feat-x.decisions
// references unknown id 'dec-missing'". Consumed by projectScout to
// surface dangling refs to the next-iter scout.
func collectDanglingReferences(applied []AppliedAction) []string {
	var out []string
	for _, a := range applied {
		if a.Kind != "integrity_violation" {
			continue
		}
		if len(a.AffectedNodes) == 0 {
			out = append(out, fmt.Sprintf("decision reference %q is dangling (no source node recorded)", a.CanonicalID))
			continue
		}
		for _, ref := range a.AffectedNodes {
			out = append(out, fmt.Sprintf("%s %s.decisions references unknown decision %q", ref.ParentKind, ref.ParentID, a.CanonicalID))
		}
	}
	return out
}

// mergeCriticIssues parses each critic's CriticIssues output into
// per-issue Concerns tagged with the critic's lens (architecture,
// devops, sre, cost) for grouping in the revise prompt. After the LLM
// critics merge, runs the mechanical integrity critic and the
// mechanical cluster pre-pass (DJ-098).
//
// DJ-125 Phase 5: every newly-recorded Concern is enriched with
// iteration metadata (IterationRaised), an initial status
// (ConcernStatusOpen), and the related-id sets the mechanical
// disposition pre-pass and DJ-126's decision-revision dispatch will
// consume. Related decision IDs come from a regex match against the
// concern text (the long-standing idRefRegex); related axis IDs come
// from a manifest lookup against the axes currently known to the
// council (settled + open).
//
// DJ-128: critic output shape switched from `Issues []string` to
// structured CriticIssue carrying Weakness + Evidence + an enumerated
// Counterproposals menu + RelatedDecisionIDs surfacing. The merge
// populates Concern.Counterproposals verbatim from the critic's slice
// so the revise projection renders the menu; the merge marks the
// concern Advisory when every counterproposal is the "needs
// investigation" sentinel so hasReviseableConcerns can skip
// counterproposal-less surfaces. Critic-provided RelatedDecisionIDs
// are unioned with the regex-extracted set; critic-provided ids win
// on conflict (they're the structured surface). Outputs the validator
// classifies as degenerate are logged but still recorded as concerns
// against the raw output text, so a flaky critic doesn't make a real
// problem invisible to the operator.
func mergeCriticIssues(s *PlanningState, results []RoundResult) {
	knownAxisIDs := collectKnownAxisIDs(s)
	for _, r := range results {
		if r.Err != nil || r.Output == "" {
			continue
		}
		iter := r.IterationIndex
		// DJ-129: lens-first kind derivation from the fanout item;
		// legacy fallback to critiqueKindFor(AgentID) for pre-DJ-129
		// session data.
		kind := deriveCritiqueKind(r)
		var ci CriticIssues
		if err := json.Unmarshal([]byte(r.Output), &ci); err != nil {
			// JSON parse failure: fall back to the raw output text as
			// the concern body so the user still sees what the critic
			// emitted. Mechanical regex extraction handles related-id
			// hits; no counterproposal menu attaches.
			s.Concerns = append(s.Concerns, newConcernFromFreeformText(r.AgentID, kind, r.Output, iter, knownAxisIDs))
			continue
		}
		if reason, deg := degenerateCriticIssueValidator(&ci); deg {
			slog.Warn("critic emitted degenerate CriticIssues; recording raw output as concern",
				"agent", r.AgentID,
				"reason", reason)
			s.Concerns = append(s.Concerns, newConcernFromFreeformText(r.AgentID, kind, r.Output, iter, knownAxisIDs))
			continue
		}
		for _, issue := range ci.Issues {
			s.Concerns = append(s.Concerns, newConcernFromCriticIssue(r.AgentID, kind, issue, iter, knownAxisIDs))
		}
	}
	appendIntegrityFindings(s)
	runMechanicalCluster(s)
	// DJ-125 Phase 6: stale concerns that the just-recorded findings
	// + the in-flight graph already resolve. Cheap regex/lookup pass;
	// no LLM call. Runs after appendIntegrityFindings so integrity
	// concerns are eligible for staleness too.
	mechanicalDisposeConcerns(s)
}

// newConcernFromCriticIssue constructs a Concern from a structured
// DJ-128 CriticIssue. Text is synthesized as "Weakness — Evidence" so
// downstream consumers (the revise projection, the deliberation log)
// see the same conjoined narrative the model produced. The
// Counterproposals slice is carried verbatim; the Advisory flag is
// set when every counterproposal is the "needs investigation"
// sentinel. RelatedDecisionIDs is the union of the critic's
// structured surfacing and the regex-extracted hits on the text;
// duplicates are removed preserving order.
func newConcernFromCriticIssue(agentID, kind string, issue CriticIssue, iter int, knownAxisIDs []string) Concern {
	text := strings.TrimSpace(issue.Weakness)
	if ev := strings.TrimSpace(issue.Evidence); ev != "" {
		if text != "" {
			text = text + " — " + ev
		} else {
			text = ev
		}
	}
	related := unionDecisionIDs(issue.RelatedDecisionIDs, extractDecisionRefsFromText(text))
	return Concern{
		AgentID:            agentID,
		Severity:           "medium",
		Kind:               kind,
		Text:               text,
		IterationRaised:    iter,
		Status:             ConcernStatusOpen,
		RelatedDecisionIDs: related,
		RelatedAxisIDs:     extractAxisRefsFromText(text, knownAxisIDs),
		Counterproposals:   append([]CriticCounterproposal(nil), issue.Counterproposals...),
		Advisory:           isAdvisoryCounterproposalMenu(issue.Counterproposals),
	}
}

// newConcernFromFreeformText is the pre-DJ-128 fallback shape: a
// concern built straight from raw critic text with no counterproposal
// menu. Used when the critic emits invalid JSON or a degenerate
// structured output — the operator still sees the raw critique even
// when the structured surface fails.
func newConcernFromFreeformText(agentID, kind, text string, iter int, knownAxisIDs []string) Concern {
	return Concern{
		AgentID:            agentID,
		Severity:           "medium",
		Kind:               kind,
		Text:               text,
		IterationRaised:    iter,
		Status:             ConcernStatusOpen,
		RelatedDecisionIDs: extractDecisionRefsFromText(text),
		RelatedAxisIDs:     extractAxisRefsFromText(text, knownAxisIDs),
	}
}

// unionDecisionIDs combines two slices of decision IDs preserving
// order with the first slice's entries taking precedence. Used by
// mergeCriticIssues to merge the critic's structured
// RelatedDecisionIDs with the regex-extracted set from the concern
// text. Returns nil when both inputs are empty so the omitempty JSON
// tag on Concern.RelatedDecisionIDs drops the field.
func unionDecisionIDs(primary, secondary []string) []string {
	if len(primary) == 0 && len(secondary) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	out := make([]string, 0, len(primary)+len(secondary))
	for _, id := range primary {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range secondary {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// collectKnownAxisIDs returns the set of axis IDs currently known to
// the council — the union of (a) axes recorded in DecidedAxesByIter
// (settled this run) and (b) axes in state.AxesOpen (open this
// iteration). Used by mergeCriticIssues to populate
// Concern.RelatedAxisIDs without needing a separate regex.
func collectKnownAxisIDs(s *PlanningState) []string {
	if s == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(s.DecidedAxesByIter)+len(s.AxesOpen))
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range s.DecidedAxesByIter {
		add(id)
	}
	for _, axis := range s.AxesOpen {
		add(axis.ID)
	}
	return ids
}

// extractDecisionRefsFromText pulls out kebab-case dec-* references
// from concern text using decRefRegex. Returns nil when no decision
// refs are present so the omitempty JSON tag drops the field for
// legacy / no-match cases. The mechanical clusterer's
// feat-/strat-only contract is preserved by using decRefRegex here
// rather than the broader idRefRegex.
func extractDecisionRefsFromText(text string) []string {
	matches := decRefRegex.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	var out []string
	for _, m := range matches {
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out
}

// extractAxisRefsFromText scans concern text for any axis ID from the
// supplied known-axis set and returns the matches. Whole-word match —
// the axis ID must appear bounded by non-slug characters, so
// "auth-provider" in concern text matches the "auth-provider" axis
// but not "authentication-provider".
//
// Returns nil when no matches so the omitempty JSON tag drops the
// field.
func extractAxisRefsFromText(text string, knownAxisIDs []string) []string {
	if len(knownAxisIDs) == 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	var out []string
	seen := make(map[string]struct{}, len(knownAxisIDs))
	for _, axisID := range knownAxisIDs {
		if _, dup := seen[axisID]; dup {
			continue
		}
		if axisIDInText(text, axisID) {
			seen[axisID] = struct{}{}
			out = append(out, axisID)
		}
	}
	return out
}

// axisIDInText returns true when axisID appears as a whole-word match
// in text. Whole-word means bounded by non-slug characters on both
// sides — slug chars are lowercase letters, digits, and hyphens. The
// boundary check prevents "auth" from matching inside "authentication".
func axisIDInText(text, axisID string) bool {
	if axisID == "" {
		return false
	}
	for {
		idx := strings.Index(text, axisID)
		if idx < 0 {
			return false
		}
		left := idx == 0 || !isSlugChar(text[idx-1])
		end := idx + len(axisID)
		right := end >= len(text) || !isSlugChar(text[end])
		if left && right {
			return true
		}
		text = text[idx+1:]
	}
}

func isSlugChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-'
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
	rebuildInFlightIndex(s)
}

// rebuildInFlightIndex re-indexes the council's in-flight Bluge store
// from the current RawProposal. Called by each merge function that
// mutates RawProposal so the next agent that fires spec_search sees the
// freshest proposal. No-op when the index pointer is nil (the merge
// helpers are reused outside the council, e.g. tests, where the
// in-flight store is not wired) or when RawProposal is empty.
//
// Rebuild failures are logged and swallowed — a stale or empty in-flight
// index is strictly better than aborting the merge: the council can
// still proceed; the worst case is that one spec_search call returns
// no hits until the next merge succeeds. The disk index is not the
// fallback during the council (per DJ-123 resolved design question 3:
// projectCritiqueDimension builds the spec_critic_elaborator's user
// message for one fanout call (DJ-129). The prefix carries the
// project context (GOALS + scout brief + in-flight manifest); the
// suffix carries the dimension-specific framing (focus_question +
// source_evidence + applicable disciplines) plus the proposal block
// the critic-elaborator prompt keys on.
func projectCritiqueDimension(snap StateSnapshot[PlanningState]) []Message {
	st := snap.State
	var prefix strings.Builder
	prefix.WriteString(st.Prompt)
	if st.ScoutBrief != "" {
		if formatted := formatScoutBrief(st.ScoutBrief); formatted != "" {
			prefix.WriteString("\n\n## Scout brief\n\n")
			prefix.WriteString(formatted)
		}
	}
	if rendered := renderManifestForProjection(&st); rendered != "" {
		prefix.WriteString("\n\n## In-flight spec manifest (use spec_get to fetch full node bodies)\n\n")
		prefix.WriteString(rendered)
	}

	var item CritiqueDimensionItem
	if snap.FanoutItem != "" {
		_ = json.Unmarshal([]byte(snap.FanoutItem), &item)
	}

	var suffix strings.Builder
	suffix.WriteString("## Dimension to challenge\n\n")
	fmt.Fprintf(&suffix, "- **ID:** `%s`\n", item.Dimension.ID)
	fmt.Fprintf(&suffix, "- **Lens:** `%s`\n", item.Dimension.Lens)
	fmt.Fprintf(&suffix, "- **Severity floor:** `%s`\n", item.Dimension.SeverityFloor)
	suffix.WriteString("\n### Focus question\n\n")
	suffix.WriteString(item.Dimension.FocusQuestion)
	suffix.WriteString("\n\n### Source evidence\n\n")
	for _, e := range item.Dimension.SourceEvidence {
		fmt.Fprintf(&suffix, "- %s\n", e)
	}
	suffix.WriteString("\n### Apply these disciplines\n\n")
	for _, d := range item.Dimension.Disciplines {
		fmt.Fprintf(&suffix, "- `%s`\n", d)
	}
	suffix.WriteString("\nFollow the matching discipline sections in your system prompt.\n")

	suffix.WriteString("\n## Proposal under review\n\n```json\n")
	suffix.WriteString(st.ProposedSpec)
	suffix.WriteString("\n```\n")

	return []Message{
		{Role: "user", Content: prefix.String(), Cacheable: true},
		{Role: "user", Content: suffix.String()},
	}
}

// deriveCritiqueKind returns the Concern.Kind for a critic
// RoundResult. Under DJ-129, the kind comes from the CritiqueDimension
// the fanout dispatched against (the FanoutItem's Dimension.Lens).
// For pre-DJ-129 results (no FanoutItem present, or the FanoutItem
// doesn't decode as CritiqueDimensionItem), falls back to
// critiqueKindFor(AgentID) so loaded session data still resolves.
func deriveCritiqueKind(r RoundResult) string {
	if strings.TrimSpace(r.FanoutItem) != "" {
		var item CritiqueDimensionItem
		if err := json.Unmarshal([]byte(r.FanoutItem), &item); err == nil {
			if lens := strings.TrimSpace(item.Dimension.Lens); lens != "" {
				return lens
			}
		}
	}
	return critiqueKindFor(r.AgentID)
}

// agents see ONLY the in-flight proposal, never the persisted graph).
func rebuildInFlightIndex(s *PlanningState) {
	if s == nil {
		return
	}
	// DJ-125: refresh the RAG list/get overlay first — the store keeps
	// its own copy of RawProposal and serves the manifest/get tools off
	// it. Update is cheap (just a pointer swap under a write lock); the
	// parse happens lazily inside the tool handler.
	if s.InFlightSpecStore != nil {
		s.InFlightSpecStore.Update(s.RawProposal)
	}
	if s.InFlightIndex == nil || s.RawProposal == "" {
		return
	}
	if err := s.InFlightIndex.Rebuild(s.RawProposal); err != nil {
		slog.Warn("in-flight spec_search: rebuild failed; council continues with stale index",
			"error", err)
	}
}
