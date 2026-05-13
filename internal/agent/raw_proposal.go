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
	Features   []RawFeatureProposal  `json:"features,omitempty" jsonschema:"description=User-facing capabilities the system delivers. Each feature is a coherent slice of behavior end-users (or operators) interact with. Empty when the proposal contributes only strategies."`
	Strategies []RawStrategyProposal `json:"strategies,omitempty" jsonschema:"description=Cross-cutting engineering commitments (storage choice, deployment posture, auth model). Each strategy is independent of any single feature and applies system-wide. Empty when the proposal contributes only features."`
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
	ID string `json:"id" jsonschema:"description=Stable slug for the feature, starting with 'feat-', lowercase, hyphen-separated, three to five words derived from the title (e.g. 'feat-realtime-dashboard'). When revising an existing feature, the elaborator preserves the existing id verbatim; when proposing a new one, the elaborator picks the slug."`
	// Summary is a one-sentence "what" description of the feature.
	// Optional in the JSON schema (some authoring agents may not yet
	// emit it); the SummariesPresent prereq backfills any missing
	// summaries via the fast-tier spec_summarizer agent. Producing
	// Summary inline here is strictly higher quality than the prereq
	// fallback because the authoring agent has the full context that
	// the persisted node will be summarising.
	Summary            string                   `json:"summary,omitempty" jsonschema:"description=One-sentence what-the-feature-does, ending with a period. Read by scanning agents via spec_list_manifest; distinct from Description (which is the full paragraph)."`
	Title              string                   `json:"title" jsonschema:"description=Concise human-readable title in sentence case naming the capability (e.g. 'Real-time dashboard'). Not a sentence; a noun phrase that a top-level UI surface would display."`
	Description        string                   `json:"description" jsonschema:"description=What the feature does in one paragraph. Covers the user-visible behavior and the success criterion in prose; mention the actor, the trigger, and the outcome. Acceptance criteria belong in AcceptanceCriteria, not here."`
	AcceptanceCriteria []string                 `json:"acceptance_criteria,omitempty" jsonschema:"description=Testable assertions that gate the feature as shipped. Each entry is a single sentence in the form 'When X happens, Y is observable.' — concrete enough that a coding agent can write a test from it. Omit when the feature is too speculative to commit to acceptance bars."`
	Decisions          []InlineDecisionProposal `json:"decisions" jsonschema:"minItems=1,description=The inline architectural decisions this feature requires. Required; must contain at least one entry. Each decision is described locally (no IDs, no cross-references); the reconciler dedupes against sibling features/strategies at apply time."`
}

// RawStrategyProposal is the inline-decisions counterpart to StrategyProposal.
// Like RawFeatureProposal, Decisions is required with minItems=1 (DJ-105).
type RawStrategyProposal struct {
	ID string `json:"id" jsonschema:"description=Stable slug for the strategy, starting with 'strat-', lowercase, hyphen-separated, three to five words (e.g. 'strat-compute-platform'). When revising an existing strategy, preserved verbatim; when proposing a new one, picked from the title."`
	// Summary: see RawFeatureProposal.Summary.
	Summary   string                   `json:"summary,omitempty" jsonschema:"description=One-sentence what-the-strategy-adopts, ending with a period. Read by scanning agents; distinct from Body (the full prose argument)."`
	Title     string                   `json:"title" jsonschema:"description=Concise human-readable title in sentence case naming the cross-cutting concern (e.g. 'Compute platform' or 'Observability stack'). A noun phrase, not a sentence."`
	Kind      string                   `json:"kind" jsonschema:"description=The category of cross-cutting concern. Free-form short label ('foundational', 'operational', 'security', 'data', etc.) used by the renderer to group strategies. Not user-visible behavior — that belongs in a feature."`
	Body      string                   `json:"body" jsonschema:"description=The prose argument for the strategy: what is being adopted, why, and what the system-wide consequences are. Multi-paragraph allowed. Decisions go into Decisions; do not duplicate decision rationale here."`
	Decisions []InlineDecisionProposal `json:"decisions" jsonschema:"minItems=1,description=The inline architectural decisions this strategy makes concrete. Required; must contain at least one entry. The reconciler dedupes against sibling features/strategies at apply time."`
}

// InlineDecisionProposal is a DecisionProposal without an ID and without
// InfluencedBy. The reconciler assigns canonical IDs at apply time.
//
// InfluencedBy was dropped from the architect's contract because it
// re-introduces inter-decision references — the same cross-reference
// problem inline decisions were designed to eliminate. Influence
// relationships, when they matter, are added during refine.
type InlineDecisionProposal struct {
	// Summary: see RawFeatureProposal.Summary.
	Summary            string             `json:"summary,omitempty" jsonschema:"description=One-sentence what-was-decided, ending with a period (e.g. 'Adopt Postgres over MySQL for the OLTP store.'). Distinct from Rationale (the why) and Title (the noun phrase)."`
	Title              string             `json:"title" jsonschema:"description=Concise human-readable title naming the decision (e.g. 'Database engine choice'). A noun phrase, not a sentence; the persistence layer uses this as the decision's heading."`
	Rationale          string             `json:"rationale" jsonschema:"description=The reasoning behind the choice. Multi-sentence prose. Cites trade-offs accepted and constraints satisfied. Distinct from ArchitectRationale (which is one-sentence) and Summary (which is the what)."`
	Confidence         float64            `json:"confidence" jsonschema:"description=Confidence in the decision on a 0.0 to 1.0 scale. 1.0 = fully committed, no reservation; 0.5 = leaning but reversible; 0.0 = forced choice under uncertainty. Used by reviewers to spot decisions that warrant deeper deliberation."`
	Alternatives       []spec.Alternative `json:"alternatives,omitempty" jsonschema:"description=The other options considered with their rationale and rejection reasons. Including thoughtful alternatives is what distinguishes a decision from a fiat; populate when at least one real alternative was weighed."`
	Citations          []spec.Citation    `json:"citations,omitempty" jsonschema:"description=Sources backing the decision: GOALS.md clauses, vendor docs, prior decisions, search results. Verbatim excerpts so reviewers can audit the evidence without retrieving the source."`
	ArchitectRationale string             `json:"architect_rationale,omitempty" jsonschema:"description=One-sentence summary of why this choice fits the architecture, distinct from the longer Rationale. Read by the reconciler when deciding whether two inline decisions across features describe the same canonical decision."`
}
