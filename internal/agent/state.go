package agent

import (
	"time"

	"github.com/chetan/locutus/internal/executor"
	"github.com/chetan/locutus/internal/search"
)

// ConcernStatus is the disposition of a Concern across iterations of
// the spec-generation council. DJ-125 promotes concerns from a flat
// append-only slice to a durable record carrying iteration metadata
// and a status that says whether the concern still blocks convergence.
//
// Lifecycle:
//   - ConcernStatusOpen: the default initial state. The critic raised
//     the concern this iteration (or a prior one) and nothing has
//     resolved it yet. Blocks convergence.
//   - ConcernStatusAddressed: the scout judged the current proposal
//     resolves the concern. Carries a one-sentence justification from
//     the scout grading pass (DJ-125 Phase 7). Does not block
//     convergence.
//   - ConcernStatusStale: the mechanical pre-pass detected that a
//     related axis is now settled or a related decision is now in the
//     graph. Does not block convergence. Cheap regex/lookup pass; no
//     LLM call.
//   - ConcernStatusWontfix: the scout judged the concern is a real
//     concern but represents a tradeoff the user accepts (e.g.
//     deliberate cost-vs-availability tradeoffs). Does not block
//     convergence.
type ConcernStatus string

const (
	ConcernStatusOpen      ConcernStatus = "open"
	ConcernStatusAddressed ConcernStatus = "addressed"
	ConcernStatusStale     ConcernStatus = "stale"
	ConcernStatusWontfix   ConcernStatus = "wontfix"
)

// Concern is a challenge raised by the critic or stakeholder.
//
// Kind groups concerns by the lens that produced them ("integrity",
// "architecture", "devops", "sre", "cost"). The revise projection
// renders concerns grouped by Kind so the architect can address each
// category specifically. Defaulted from AgentID at merge time when not
// set explicitly.
//
// DJ-125 fields (Status, IterationRaised, RelatedDecisionIDs,
// RelatedAxisIDs, Justification): Concerns are durable across
// iterations rather than cleared. Status communicates whether the
// concern currently blocks convergence; the related-id fields power
// mechanical staleness detection and (per DJ-126) decision-revision
// dispatch. Zero values match pre-DJ-125 persisted state so the
// council still loads older snapshots cleanly.
type Concern struct {
	AgentID  string `json:"agent_id" jsonschema:"description=The id of the agent that raised this concern (architect_critic; devops_critic; etc.). Defaulted from the calling agent at merge time when not set; the renderer groups concerns by agent."`
	Severity string `json:"severity" jsonschema:"enum=high,enum=medium,enum=low,description=Severity bucket. high blocks the spec from shipping; medium is a real concern worth addressing; low is a polish-pass note."`
	Kind     string `json:"kind,omitempty" jsonschema:"description=The lens that produced this concern (integrity; architecture; devops; sre; cost). The revise projection groups concerns by Kind so the architect addresses categories specifically. Defaulted from AgentID at merge time when not set."`
	Text     string `json:"text" jsonschema:"description=The concern itself — a complete sentence naming the specific finding. Cites the spec node id or GOALS.md clause when relevant. Not a generic complaint."`

	IterationRaised int `json:"iteration_raised,omitempty" jsonschema:"description=Iteration index when this concern was first raised. Zero for concerns raised before iteration tracking (legacy concerns from pre-DJ-125 sessions)."`

	Status ConcernStatus `json:"status,omitempty" jsonschema:"enum=open,enum=addressed,enum=stale,enum=wontfix,description=Current disposition. open blocks convergence; addressed/stale/wontfix do not. Mechanical pre-pass sets stale; scout grading sets addressed and wontfix."`

	RelatedDecisionIDs []string `json:"related_decision_ids,omitempty" jsonschema:"description=Decision IDs this concern references. Populated by mergeCriticIssues via regex match against the manifest plus optional structured surfacing by the critic. Powers DJ-126's decision-revision dispatch."`

	RelatedAxisIDs []string `json:"related_axis_ids,omitempty" jsonschema:"description=Axis IDs this concern references. Populated mechanically. Powers staleness checks against the manifest's axis-state field."`

	Justification string `json:"justification,omitempty" jsonschema:"description=One-sentence rationale from the scout's grading pass (DJ-125 Phase 7). Set when Status is addressed or wontfix to explain WHY the scout dispositioned the concern; empty otherwise."`

	// Counterproposals is the critic's enumerated alternative menu
	// (DJ-128). Carried verbatim from CriticIssue.Counterproposals at
	// merge time; consumed by projectReviseDecision so the revise
	// projection renders the full menu and the elaborator either picks
	// one or rejects all coherently. Each unpicked counterproposal lands
	// in the revised decision's alternatives slice with the critic's
	// Argument as the Rationale and the critic's Citations preserved.
	// Empty for concerns raised by the integrity critic or other
	// mechanical passes that have no counterproposal menu.
	Counterproposals []CriticCounterproposal `json:"counterproposals,omitempty" jsonschema:"description=The enumerated counterproposal menu the LLM critic surfaced for this concern (DJ-128). Each entry is one concrete option the elaborator can pick from in the revise pass; unpicked ones become alternatives in the deliberation log with the critic's argument + citations preserved verbatim. Empty for mechanical concerns (integrity violations, etc.) that have no counterproposal shape."`

	// Advisory marks concerns where every counterproposal is the
	// literal "needs investigation" sentinel (DJ-128). Advisory
	// concerns surface to the user but do NOT drive revise dispatch:
	// hasReviseableConcerns and fanoutReviseableConcerns skip them.
	// The sentinel is the one acceptable form for a critic that sees a
	// real problem but cannot name a concrete alternative; the
	// resulting concern is surfaced for human review rather than
	// forcing the elaborator to revise against a non-menu.
	Advisory bool `json:"advisory,omitempty" jsonschema:"description=True when every counterproposal in this concern is the literal 'needs investigation' sentinel — the critic saw a real problem but could not name a specific alternative (DJ-128). Advisory concerns surface for human review but never drive the revise-fanout dispatch."`
}

// Finding is a research result from the researcher.
type Finding struct {
	Query  string `json:"query" jsonschema:"description=The factual question this finding answers — a complete sentence pulled from a critic's concern or from a known gap in the spec. Phrasing the question concretely makes the research result concretely answerable."`
	Result string `json:"result" jsonschema:"description=Evidence-based answer to Query; citing retrieved sources. When grounding was on; the research result cites real URLs / vendor docs; when grounding is off the result still cites named principles ('the CAP theorem trade-off for AP systems'). Empty result or 'no information available' is acceptable when the question genuinely has no answer."`
}

// PlanningState is the typed blackboard for council workflow execution.
// It is owned exclusively by the workflow orchestrator goroutine — parallel
// agents receive read-only snapshots via StateSnapshot[PlanningState] (deep-
// copied by snapshotPlanningState), and their results are merged back by the
// orchestrator after all parallel agents complete.
type PlanningState struct {
	Round            int       `json:"round"`
	Prompt           string    `json:"prompt"`
	ProposedSpec     string    `json:"proposed_spec,omitempty"`
	Concerns         []Concern `json:"concerns,omitempty"`
	ResearchResults  []Finding `json:"research_results,omitempty"`
	Revisions        string    `json:"revisions,omitempty"`
	Record           string    `json:"record,omitempty"`
	OpenConcerns     []string  `json:"open_concerns,omitempty"`
	ResolvedConcerns []string  `json:"resolved_concerns,omitempty"`
	// ScoutBrief carries a survey-step output (e.g. the spec-generation
	// council's spec_scout) into downstream propose/revise rounds. It is
	// the raw JSON of an agent.ScoutBrief — projection.go formats it
	// for human-readable inclusion in the proposer's user message.
	ScoutBrief string `json:"scout_brief,omitempty"`
	// RawProposal carries the architect's pre-reconcile output (a
	// RawSpecProposal JSON) from propose/revise into the reconcile step.
	// The reconcile step's merge handler runs ApplyReconciliation and
	// stores the canonical SpecProposal back into ProposedSpec; downstream
	// agents (critics, reviser) read ProposedSpec as today.
	RawProposal string `json:"raw_proposal,omitempty"`
	// ConflictActions records reconciler verdicts that flipped a decision
	// (resolve_conflict). Surfaced to GenerateSpec so it can fire cascade
	// rewrites on affected feature/strategy nodes after persistence.
	ConflictActions []AppliedAction `json:"-"`
	// Existing is the spec snapshot the reconciler matches inline
	// decisions against for ID reuse. Set by GenerateSpec before the
	// workflow runs.
	Existing *ExistingSpec `json:"-"`
	// Phase 3: outline + per-node elaborate fanout state.
	//
	// Outline holds the spec_outliner's JSON output (used by the
	// fanout step to spawn per-element elaborator calls, and by the
	// elaborator's projection to give each call situational
	// awareness of sibling features/strategies).
	// ElaboratedFeatures and ElaboratedStrategies accumulate the
	// fanout outputs (raw JSON of RawFeatureProposal /
	// RawStrategyProposal). Once both are populated, the merge
	// handler assembles them into RawProposal for the reconciler.
	Outline              string   `json:"outline,omitempty"`
	ElaboratedFeatures   []string `json:"elaborated_features,omitempty"`
	ElaboratedStrategies []string `json:"elaborated_strategies,omitempty"`

	// Revise-fanout state (DJ-098 unified per-cluster design).
	//
	// OriginalRawProposal is the post-elaborate, pre-revise assembly of
	// the elaborate fanout outputs. Preserved separately from RawProposal
	// so the revise merge can swap in revised nodes by ID without losing
	// the untouched ones. RawProposal itself gets rewritten to the
	// merged version (original + revised + additions) before
	// reconcile_revise consumes it.
	//
	// FindingClusters is the unified list of clusters consumed by the
	// revise fanout. Populated in two passes: the mechanical pre-pass
	// (id-mention matching, runs in mergeResults after critique) emits
	// clusters for findings naming an existing node by id; the LLM
	// clusterer step processes UnmatchedFindings and appends topic-
	// grouped clusters for the rest. Each cluster carries the agent_id
	// to dispatch (spec_feature_elaborator vs spec_strategy_elaborator)
	// and an optional node_id (set when the cluster targets an existing
	// node, empty when it proposes a new one).
	//
	// UnmatchedFindings is the verbatim list of critic findings the
	// mechanical pre-pass couldn't id-match. Drives the LLM clusterer
	// step's input via the cluster_findings projection.
	//
	// RevisedNodes accumulates per-cluster elaborator outputs. Each
	// entry is one RawFeatureProposal or RawStrategyProposal JSON; the
	// assembleRevisedRawProposal merge sniffs the id prefix (feat- vs
	// strat-) and decides revise (id matches existing) vs addition
	// (fresh id) per entry.
	OriginalRawProposal string           `json:"original_raw_proposal,omitempty"`
	FindingClusters     []FindingCluster `json:"finding_clusters,omitempty"`
	UnmatchedFindings   []string         `json:"unmatched_findings,omitempty"`
	RevisedNodes        []string         `json:"revised_nodes,omitempty"`

	// GateAxisRecurrence tracks how many times each (deliverable, axis)
	// pair has been flagged as an OpenDimension across all gate
	// verdicts in this run. Populated by mergeGateVerdict. Consumed by
	// the gate spawner to detect non-progress: if any axis has been
	// raised RecurrenceTerminationThreshold or more iterations in a
	// row, the gate force-terminates with a "convergence_stuck" history
	// event rather than continuing to iterate against a moving target.
	//
	// Key format: strings.ToLower(deliverable)|strings.ToLower(axis)
	// with surrounding whitespace stripped. Lowercase-and-pipe lets
	// "WinPlan platform: On-call rotation owner" and
	// "winplan platform: on-call rotation owner" coalesce to the same
	// bucket even when the gate's casing drifts iteration-over-iteration.
	//
	// DJ-124 retires the gate role in favour of the scout-driven loop;
	// the field stays on the struct because the legacy gate test workflow
	// (runSpecGateTestWorkflow) still drives it and because removing it
	// would force a Stage C-only rewrite of a working test surface.
	GateAxisRecurrence map[string]int `json:"gate_axis_recurrence,omitempty"`

	// DJ-124: scout-driven convergence loop state.
	//
	// PriorScoutBrief carries the previous iteration's ScoutBrief output.
	// Captured by mergeScoutBrief BEFORE overwriting ScoutBrief; consumed
	// by the scout's own projection on the next iteration so the model
	// can detect stable vs reopened axes. Not persisted (json:"-").
	PriorScoutBrief string `json:"-"`

	// Imported carries content from `locutus import` admitted into the
	// workflow's input set. The scout reads these alongside GOALS.md and
	// the existing graph; the convergence loop handles the rest. Empty
	// for `locutus refine` runs. Populated by Phase 6.
	Imported []ImportedContent `json:"-"`

	// AxesOpen tracks the current iteration's open axes. Populated by
	// mergeScoutBrief from ScoutBrief.AxesOpen; consumed by the
	// decision-elaborator fanout's Fanout closure to spawn one call per
	// axis. Replaced on each scout call (axes that get closed drop off
	// naturally; new axes get added).
	AxesOpen []OpenAxis `json:"-"`

	// NewNodesFromScout tracks the current iteration's new feature /
	// strategy nodes from ScoutBrief.NewNodes. Consumed by the
	// narrative-elaborator fanout's Fanout closure. Replaced on each
	// scout call. mergeDecisions appends newly-minted decision IDs to
	// the relevant entries' Decisions[] before narrative dispatch fires.
	NewNodesFromScout []NewSpecNode `json:"-"`

	// DecidedAxesByIter tracks which axis IDs were decided in each
	// iteration. Used by the scout spawner's cycle-detection logic:
	// when the next scout call emits an axis that appears in
	// DecidedAxesByIter for a prior iteration, the loop is reopening a
	// decided axis — force-terminate with a cycle signal.
	//
	// Key format: axis ID. Value: iteration index when the axis was
	// first decided.
	DecidedAxesByIter map[string]int `json:"-"`

	// AxisRevisionCount tracks how many times each axis has been
	// revised across iterations. Incremented by mergeDecisions every
	// time a replace-by-axis-ID match fires on the axis. Consumed by
	// the scout spawner's per-axis revision-count cap: when any axis
	// reaches the cap (default 3; env override
	// LOCUTUS_DECISION_REVISION_CAP), the loop force-terminates with a
	// convergence_revision_capped DJ-103 event naming the capped axes.
	// Per-axis counting means revising dec-X three times and dec-Y
	// once doesn't terminate at cap=3 — the revisions are independent.
	//
	// Distinct from DecidedAxesByIter (which records first-author
	// commits): this map records revise-side activity. The two
	// failure modes (scout-side reopens vs revise-side oscillation)
	// are flagged by two separate mechanisms.
	AxisRevisionCount map[string]int `json:"-"`

	// PendingDecisionRevisedEvents accumulates one entry per
	// replace-by-axis-ID match within a single mergeDecisions call.
	// The wrapper Merge closure in convergenceLoopTemplate drains the
	// slice through the historian and clears it; mergeDecisions itself
	// stays historian-agnostic so its 2-arg signature works for both
	// production (wrapped) and unit-test (direct) callers.
	PendingDecisionRevisedEvents []PendingDecisionRevisedEvent `json:"-"`

	// LockedDecisionIDs is the set of decision IDs the cap-as-commit
	// terminal has flipped to Locked (DJ-128). Populated by the
	// revision-capped terminal; consumed by hasReviseableConcerns and
	// fanoutReviseableConcerns to skip locked decisions from
	// subsequent revise dispatches, AND by the SpecProposal projection
	// so persisted decisions carry the Locked flag downstream.
	// Set, not slice, so membership lookup is O(1) in the filter path.
	LockedDecisionIDs map[string]struct{} `json:"-"`

	// DanglingReferences accumulates integrity-violation findings from
	// ApplyReconciliation. Surfaced to the scout's next-iteration input
	// as concerns so the loop can self-correct (e.g. the scout iterates
	// the dispatch with a corrected new_nodes[].decisions[]).
	DanglingReferences []string `json:"-"`

	// InFlightIndex is the council-scoped Bluge index over the current
	// RawProposal (DJ-123 Phase 3). Set by GenerateSpec at council
	// start and torn down at council end; nil on non-council
	// PlanningState consumers (assimilation, refine, etc.). The merge
	// functions that mutate RawProposal call rebuildInFlightIndex(s)
	// after the write so the next agent that fires spec_search sees
	// the latest proposal.
	//
	// Pointer is shared across the deep-copied snapshots — Snapshot
	// copies the slices on PlanningState but the index handle itself
	// is concurrent-safe (Rebuild serialises against in-flight Search
	// under an RWMutex inside search.InFlightIndex).
	InFlightIndex *search.InFlightIndex `json:"-"`

	// InFlightSpecStore is the council-scoped overlay that backs the
	// DJ-125 spec_list_manifest and spec_get tools while a run is in
	// flight. Set by GenerateSpec at council start; torn down (via the
	// deferred swap back) at council end. The same merge helpers that
	// call rebuildInFlightIndex also call InFlightSpecStore.Update so
	// every RAG tool sees a consistent view of the in-flight proposal.
	// Pointer-shared across snapshots like InFlightIndex; its internal
	// RWMutex serialises tool reads against merge-side writes.
	InFlightSpecStore *InFlightSpecStore `json:"-"`
}

// ImportedContent is one external document admitted into the spec
// generation workflow via `locutus import`. Populated by Phase 6
// (DJ-124); empty for refine runs. The scout reads these alongside
// GOALS.md and the existing spec graph.
type ImportedContent struct {
	Path string // filesystem path or label
	Body string // verbatim content
}

// PendingDecisionRevisedEvent captures the inputs the
// decision_revised DJ-103 history event needs (DJ-126 Phase 5). One
// entry is appended per replace-by-axis-ID match inside
// mergeDecisions; the wrapper Merge closure in convergenceLoopTemplate
// drains them through the historian and clears the slice so the
// state stays clean for the next iteration's merge.
//
// Prior is a copy of the existing decision body BEFORE the replace;
// Revised is the incoming RawDecisionProposal that overwrote it.
// DrivingConcerns snapshots every open concern whose
// RelatedDecisionIDs contained the prior id at the time the merge
// fired — captured BEFORE markConcernsAddressedByRevision so the
// event's rationale carries the as-flagged finding text rather than
// the post-merge addressed-status form.
type PendingDecisionRevisedEvent struct {
	Prior           RawDecisionProposal
	Revised         RawDecisionProposal
	DrivingConcerns []Concern
	Iter            int
}

// StateSnapshot wraps a verb's state value with fanout context. Projections
// receive this generic wrapper so they can read both the verb-specific state
// (snap.State.X) and the fanout dispatch context (snap.FanoutItem) without
// the executor needing to know what shape S has.
//
// The State field is a value-copy of the verb's state taken before any
// step in the wave runs. Verbs whose state contains slices or maps must
// supply a Workflow.Snapshot closure that deep-copies them; otherwise
// parallel agents would observe in-flight mutations from other goroutines.
//
// FanoutItem is empty on non-fanout calls. On fanout calls it carries the
// raw JSON of the per-iteration item the executor dispatched the agent
// against (e.g. the OutlineFeature being elaborated, the FindingCluster
// being revised). The projection unmarshals it as needed.
type StateSnapshot[S any] struct {
	State      S
	FanoutItem string
}

// snapshotPlanningState produces a deep-copy of PlanningState for the
// council workflows. Slice fields are copied so parallel agents reading
// the snapshot don't observe in-flight mutations from other goroutines.
// Wired into Workflow.Snapshot for PlanningWorkflow / SpecGenerationWorkflow
// / AssimilationWorkflow; verbs with simpler state can omit the closure
// and accept the executor's default shallow copy.
func snapshotPlanningState(s *PlanningState) PlanningState {
	out := *s
	if len(s.UnmatchedFindings) > 0 {
		out.UnmatchedFindings = make([]string, len(s.UnmatchedFindings))
		copy(out.UnmatchedFindings, s.UnmatchedFindings)
	}
	if len(s.Concerns) > 0 {
		out.Concerns = make([]Concern, len(s.Concerns))
		copy(out.Concerns, s.Concerns)
		// DJ-125: Concern carries slice fields (RelatedDecisionIDs,
		// RelatedAxisIDs); the per-element copy above only duplicated
		// the slice headers, so deep-copy each entry's slice payload to
		// keep snapshots independent from the orchestrator's mutable
		// state.
		for i := range out.Concerns {
			if len(s.Concerns[i].RelatedDecisionIDs) > 0 {
				out.Concerns[i].RelatedDecisionIDs = append([]string(nil), s.Concerns[i].RelatedDecisionIDs...)
			}
			if len(s.Concerns[i].RelatedAxisIDs) > 0 {
				out.Concerns[i].RelatedAxisIDs = append([]string(nil), s.Concerns[i].RelatedAxisIDs...)
			}
		}
	}
	if len(s.ResearchResults) > 0 {
		out.ResearchResults = make([]Finding, len(s.ResearchResults))
		copy(out.ResearchResults, s.ResearchResults)
	}
	if len(s.OpenConcerns) > 0 {
		out.OpenConcerns = make([]string, len(s.OpenConcerns))
		copy(out.OpenConcerns, s.OpenConcerns)
	}
	// DJ-124 scout-driven loop state.
	if len(s.AxesOpen) > 0 {
		out.AxesOpen = make([]OpenAxis, len(s.AxesOpen))
		copy(out.AxesOpen, s.AxesOpen)
	}
	if len(s.NewNodesFromScout) > 0 {
		out.NewNodesFromScout = make([]NewSpecNode, len(s.NewNodesFromScout))
		copy(out.NewNodesFromScout, s.NewNodesFromScout)
	}
	if len(s.Imported) > 0 {
		out.Imported = make([]ImportedContent, len(s.Imported))
		copy(out.Imported, s.Imported)
	}
	if len(s.DanglingReferences) > 0 {
		out.DanglingReferences = make([]string, len(s.DanglingReferences))
		copy(out.DanglingReferences, s.DanglingReferences)
	}
	if len(s.AxisRevisionCount) > 0 {
		out.AxisRevisionCount = make(map[string]int, len(s.AxisRevisionCount))
		for k, v := range s.AxisRevisionCount {
			out.AxisRevisionCount[k] = v
		}
	}
	if len(s.DecidedAxesByIter) > 0 {
		out.DecidedAxesByIter = make(map[string]int, len(s.DecidedAxesByIter))
		for k, v := range s.DecidedAxesByIter {
			out.DecidedAxesByIter[k] = v
		}
	}
	if len(s.LockedDecisionIDs) > 0 {
		out.LockedDecisionIDs = make(map[string]struct{}, len(s.LockedDecisionIDs))
		for k := range s.LockedDecisionIDs {
			out.LockedDecisionIDs[k] = struct{}{}
		}
	}
	return out
}

// HasOpenConcerns returns true if there are unresolved concerns.
func (s *PlanningState) HasOpenConcerns() bool {
	return len(s.OpenConcerns) > 0
}

// WorkflowEvent reports progress during workflow execution.
type WorkflowEvent struct {
	StepID    string    `json:"step_id"`
	AgentID   string    `json:"agent_id,omitempty"`
	Status    string    `json:"status"` // "started", "completed", "retrying", "skipped", "error", "graph_mutated"
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`

	// Mutation is populated on "graph_mutated" events forwarded from
	// the underlying executor (DJ-122 spawner support). Nil on every
	// other event kind.
	Mutation *executor.MutationDetails `json:"mutation,omitempty"`
}
