package agent

import "time"

// Concern is a challenge raised by the critic or stakeholder.
//
// Kind groups concerns by the lens that produced them ("integrity",
// "architecture", "devops", "sre", "cost"). The revise projection
// renders concerns grouped by Kind so the architect can address each
// category specifically. Defaulted from AgentID at merge time when not
// set explicitly.
type Concern struct {
	AgentID  string `json:"agent_id"`
	Severity string `json:"severity"` // "high", "medium", "low"
	Kind     string `json:"kind,omitempty"`
	Text     string `json:"text"`
}

// Finding is a research result from the researcher.
type Finding struct {
	Query  string `json:"query"`
	Result string `json:"result"`
}

// PlanningState is the typed blackboard for council workflow execution.
// It is owned exclusively by the workflow orchestrator goroutine — parallel
// agents receive read-only snapshots via StateSnapshot, and their results are
// merged back by the orchestrator after all parallel agents complete.
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

	// Revise-fanout state (Phase 1 of council-tools-and-revise-fanout).
	//
	// OriginalRawProposal is the post-elaborate, pre-revise assembly of
	// the elaborate fanout outputs. Preserved separately from RawProposal
	// so the revise merge can swap in revised nodes by ID without losing
	// the untouched ones. RawProposal itself gets rewritten to the
	// merged version (original + revised + additions) before
	// reconcile_revise consumes it.
	//
	// RevisionPlan is the spec_revision_triager agent's JSON output —
	// drives the revise_features / revise_strategies fanouts.
	//
	// RevisedFeatures and RevisedStrategies accumulate per-node
	// elaborator outputs from the revise fanouts (raw JSON of
	// RawFeatureProposal / RawStrategyProposal).
	//
	// AdditionProposals is the architect's revise_additions output —
	// a partial RawSpecProposal containing only the new features /
	// strategies that address triage-bucketed addition concerns.
	OriginalRawProposal string   `json:"original_raw_proposal,omitempty"`
	RevisionPlan        string   `json:"revision_plan,omitempty"`
	RevisedFeatures     []string `json:"revised_features,omitempty"`
	RevisedStrategies   []string `json:"revised_strategies,omitempty"`
	AdditionProposals   string   `json:"addition_proposals,omitempty"`
}

// StateSnapshot is a read-only copy of PlanningState fields relevant to a
// specific agent. Agents receive this instead of the full mutable state.
type StateSnapshot struct {
	Round           int
	Prompt          string
	ProposedSpec    string
	Concerns        []Concern
	ResearchResults []Finding
	Revisions       string
	OpenConcerns    []string
	ScoutBrief      string
	// RawProposal is the architect's pre-reconcile output, projected to
	// the spec_reconciler agent so it can cluster inline decisions.
	RawProposal string
	// Existing is the spec snapshot the reconciler matches inline
	// decisions against for ID reuse.
	Existing *ExistingSpec
	// Phase 3 fanout: Outline carries the slim feature/strategy
	// outline so per-node elaborators see sibling shape; FanoutItem
	// is the raw JSON of the specific outline element this elaborator
	// call is responsible for (only set on fanout-spawned snapshots).
	Outline    string
	FanoutItem string
	// OriginalRawProposal carries the pre-revise assembled raw
	// proposal so the revise-fanout projections can look up the
	// prior content of the node they're revising. The revise
	// elaborator's prompt cites the prior RawFeatureProposal /
	// RawStrategyProposal verbatim so the model has full context
	// for the requested change.
	OriginalRawProposal string
	// RevisionPlan is the spec_revision_triager's JSON output, threaded
	// to the revise_additions projection so the architect sees the
	// addition concerns directly (the additions step is not a fanout
	// and can't read them via FanoutItem).
	RevisionPlan string
}

// Snapshot creates a read-only copy of the current state. Slice fields are
// copied so the snapshot is safe for concurrent reads while the orchestrator
// continues to mutate the original.
func (s *PlanningState) Snapshot() StateSnapshot {
	snap := StateSnapshot{
		Round:               s.Round,
		Prompt:              s.Prompt,
		ProposedSpec:        s.ProposedSpec,
		Revisions:           s.Revisions,
		ScoutBrief:          s.ScoutBrief,
		RawProposal:         s.RawProposal,
		Existing:            s.Existing,
		Outline:             s.Outline,
		OriginalRawProposal: s.OriginalRawProposal,
		RevisionPlan:        s.RevisionPlan,
	}
	if len(s.Concerns) > 0 {
		snap.Concerns = make([]Concern, len(s.Concerns))
		copy(snap.Concerns, s.Concerns)
	}
	if len(s.ResearchResults) > 0 {
		snap.ResearchResults = make([]Finding, len(s.ResearchResults))
		copy(snap.ResearchResults, s.ResearchResults)
	}
	if len(s.OpenConcerns) > 0 {
		snap.OpenConcerns = make([]string, len(s.OpenConcerns))
		copy(snap.OpenConcerns, s.OpenConcerns)
	}
	return snap
}

// HasOpenConcerns returns true if there are unresolved concerns.
func (s *PlanningState) HasOpenConcerns() bool {
	return len(s.OpenConcerns) > 0
}

// WorkflowEvent reports progress during workflow execution.
type WorkflowEvent struct {
	StepID    string    `json:"step_id"`
	AgentID   string    `json:"agent_id,omitempty"`
	Status    string    `json:"status"` // "started", "completed", "retrying", "skipped", "error"
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
