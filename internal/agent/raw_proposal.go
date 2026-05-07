package agent

import "github.com/chetan/locutus/internal/spec"

// RawSpecProposal is the architect's output before reconciliation. Each
// feature and strategy carries its decisions inline as embedded objects
// with no IDs, eliminating the cross-array referential integrity that
// weaker models can't keep in attention.
//
// The reconciler step (spec_reconciler agent + ApplyReconciliation Go
// surgery) clusters inline decisions across the proposal and emits a
// canonical SpecProposal with shared decisions and assigned IDs that the
// persistence layer expects.
type RawSpecProposal struct {
	Features   []RawFeatureProposal  `json:"features,omitempty"`
	Strategies []RawStrategyProposal `json:"strategies,omitempty"`
}

// RawFeatureProposal mirrors FeatureProposal but carries InlineDecisionProposal
// objects directly under decisions[] instead of decision-ID references. The
// architect describes each decision the feature requires locally; the reconciler
// resolves cross-feature duplication and conflicts.
//
// Decisions is required and must contain at least one entry (DJ-105):
// strict-mode JSON schema on every supported provider rejects responses
// missing or empty in this field, so a flaky model cannot smuggle a
// decision-less feature past the API into the reconcile/validate pipeline.
// The retry loop kicks in instead, giving the model another chance to
// produce a conformant output.
type RawFeatureProposal struct {
	ID                 string                   `json:"id"`
	Title              string                   `json:"title"`
	Description        string                   `json:"description"`
	AcceptanceCriteria []string                 `json:"acceptance_criteria,omitempty"`
	Decisions          []InlineDecisionProposal `json:"decisions" jsonschema:"minItems=1"`
}

// RawStrategyProposal is the inline-decisions counterpart to StrategyProposal.
// Like RawFeatureProposal, Decisions is required with minItems=1 (DJ-105).
type RawStrategyProposal struct {
	ID        string                   `json:"id"`
	Title     string                   `json:"title"`
	Kind      string                   `json:"kind"`
	Body      string                   `json:"body"`
	Decisions []InlineDecisionProposal `json:"decisions" jsonschema:"minItems=1"`
}

// InlineDecisionProposal is a DecisionProposal without an ID and without
// InfluencedBy. The reconciler assigns canonical IDs at apply time.
//
// InfluencedBy was dropped from the architect's contract because it
// re-introduces inter-decision references — the same cross-reference
// problem inline decisions were designed to eliminate. Influence
// relationships, when they matter, are added during refine.
type InlineDecisionProposal struct {
	Title              string             `json:"title"`
	Rationale          string             `json:"rationale"`
	Confidence         float64            `json:"confidence"`
	Alternatives       []spec.Alternative `json:"alternatives,omitempty"`
	Citations          []spec.Citation    `json:"citations,omitempty"`
	ArchitectRationale string             `json:"architect_rationale,omitempty"`
}
