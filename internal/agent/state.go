package agent

import (
	"time"

	"github.com/chetan/locutus/internal/executor"
)

// Concern is a challenge raised by the critic or stakeholder.
//
// Kind groups concerns by the lens that produced them ("integrity",
// "architecture", "devops", "sre", "cost"). The revise projection
// renders concerns grouped by Kind so the architect can address each
// category specifically. Defaulted from AgentID at merge time when not
// set explicitly.
type Concern struct {
	AgentID  string `json:"agent_id" jsonschema:"description=The id of the agent that raised this concern (architect_critic; devops_critic; etc.). Defaulted from the calling agent at merge time when not set; the renderer groups concerns by agent."`
	Severity string `json:"severity" jsonschema:"enum=high,enum=medium,enum=low,description=Severity bucket. high blocks the spec from shipping; medium is a real concern worth addressing; low is a polish-pass note."`
	Kind     string `json:"kind,omitempty" jsonschema:"description=The lens that produced this concern (integrity; architecture; devops; sre; cost). The revise projection groups concerns by Kind so the architect addresses categories specifically. Defaulted from AgentID at merge time when not set."`
	Text     string `json:"text" jsonschema:"description=The concern itself — a complete sentence naming the specific finding. Cites the spec node id or GOALS.md clause when relevant. Not a generic complaint."`
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
	}
	if len(s.ResearchResults) > 0 {
		out.ResearchResults = make([]Finding, len(s.ResearchResults))
		copy(out.ResearchResults, s.ResearchResults)
	}
	if len(s.OpenConcerns) > 0 {
		out.OpenConcerns = make([]string, len(s.OpenConcerns))
		copy(out.OpenConcerns, s.OpenConcerns)
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
