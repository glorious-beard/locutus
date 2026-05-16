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
	Strategies []RawStrategyProposal `json:"strategies,omitempty" jsonschema:"description=Cross-cutting engineering commitments — e.g. storage choice / deployment posture / auth model. Each strategy is independent of any single feature and applies system-wide. Empty when the proposal contributes only features."`
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
	ID string `json:"id" jsonschema:"description=Stable slug for the feature — starts with 'feat-' / lowercase / hyphen-separated / three to five words derived from the title (e.g. 'feat-realtime-dashboard'). When revising an existing feature the elaborator preserves the existing id verbatim; when proposing a new one the elaborator picks the slug."`
	// Summary is a one-sentence "what" description of the feature.
	// Optional in the JSON schema (some authoring agents may not yet
	// emit it); the SummariesPresent prereq backfills any missing
	// summaries via the fast-tier spec_summarizer agent. Producing
	// Summary inline here is strictly higher quality than the prereq
	// fallback because the authoring agent has the full context that
	// the persisted node will be summarising.
	Summary            string                   `json:"summary,omitempty" jsonschema:"description=One-sentence what-the-feature-does; ending with a period. Read by scanning agents via spec_list_manifest; distinct from Description (which is the full paragraph)."`
	Title              string                   `json:"title" jsonschema:"description=Concise human-readable title in sentence case naming the capability (e.g. 'Real-time dashboard'). A noun phrase rather than a sentence — what a top-level UI surface would display."`
	Description        string                   `json:"description" jsonschema:"description=What the feature does in one paragraph. Covers the user-visible behavior and the success criterion in prose; mention the actor / trigger / outcome. Acceptance criteria belong in AcceptanceCriteria rather than here."`
	AcceptanceCriteria []string                 `json:"acceptance_criteria,omitempty" jsonschema:"description=Testable assertions that gate the feature as shipped. Each entry is a single sentence in the form 'When X happens then Y is observable.' — concrete enough that a coding agent can write a test from it. Omit when the feature is too speculative to commit to acceptance bars."`
	Decisions          []InlineDecisionProposal `json:"decisions" jsonschema:"minItems=1,description=The inline architectural decisions this feature requires. Required; must contain at least one entry. Each decision is described locally without IDs or cross-references; the reconciler dedupes against sibling features/strategies at apply time."`
}

// RawStrategyProposal is the inline-decisions counterpart to StrategyProposal.
// Like RawFeatureProposal, Decisions is required with minItems=1 (DJ-105).
type RawStrategyProposal struct {
	ID string `json:"id" jsonschema:"description=Stable slug for the strategy — starts with 'strat-' / lowercase / hyphen-separated / three to five words (e.g. 'strat-compute-platform'). When revising an existing strategy the id is preserved verbatim; when proposing a new one it's picked from the title."`
	// Summary: see RawFeatureProposal.Summary.
	Summary   string                   `json:"summary,omitempty" jsonschema:"description=One-sentence what-the-strategy-adopts ending with a period. Read by scanning agents; distinct from Body (the full prose argument)."`
	Title     string                   `json:"title" jsonschema:"description=Concise human-readable title in sentence case naming the cross-cutting concern (e.g. 'Compute platform' or 'Observability stack'). A noun phrase rather than a sentence."`
	Kind      string                   `json:"kind" jsonschema:"description=The category of cross-cutting concern. Free-form short label (foundational / operational / security / data / etc.) used by the renderer to group strategies. Not user-visible behavior — that belongs in a feature."`
	Body      string                   `json:"body" jsonschema:"description=The prose argument for the strategy — what is being adopted; why; and what the system-wide consequences are. Multi-paragraph allowed. Decisions go into Decisions; do not duplicate decision rationale here."`
	Decisions []InlineDecisionProposal `json:"decisions" jsonschema:"minItems=1,description=The inline architectural decisions this strategy makes concrete. Required; must contain at least one entry. The reconciler dedupes against sibling features/strategies at apply time."`
}

// RawDecisionProposal is the per-axis output shape of DJ-124's Phase 1
// decision-elaborator. Each invocation of the elaborator takes one axis
// (plus its surfacing-node context) and produces one RawDecisionProposal
// with the full Decision content: title, summary, rationale, alternatives
// (each grounded with citations on the rejection reasoning), provenance
// citations on the chosen path, the axis IDs it answers, and the back-
// references to the spec nodes that surfaced those axes.
//
// Unlike the soon-to-be-retired InlineDecisionProposal, this struct
// carries an ID (the elaborator picks the slug from the title) and the
// DJ-124 back-reference set. Phase 5 wires it through the workflow as
// the load-bearing output of the new decisions-before-narrative phase
// split; Phase 1 only ships the schema shape so downstream phases have
// a stable contract to author against.
type RawDecisionProposal struct {
	ID                 string             `json:"id" jsonschema:"description=Stable slug for the decision — starts with 'dec-' / lowercase / hyphen-separated / three to five words derived from the title (e.g. 'dec-postgres-oltp-store'). The reconciler may suffix with -2/-3 if collisions occur; the elaborator picks the slug from the title."`
	Summary            string             `json:"summary,omitempty" jsonschema:"description=One-sentence what-was-decided ending with a period (e.g. 'Adopt Postgres over MySQL for the OLTP store.'). Distinct from Rationale (the why) and Title (the noun phrase)."`
	Title              string             `json:"title" jsonschema:"description=Concise human-readable title naming the decision (e.g. 'Database engine choice'). A noun phrase rather than a sentence — the persistence layer uses this as the decision's heading."`
	Rationale          string             `json:"rationale" jsonschema:"description=The reasoning behind the choice. Multi-sentence prose. Cites trade-offs accepted and constraints satisfied. Distinct from ArchitectRationale (which is one-sentence) and Summary (which is the what)."`
	ArchitectRationale string             `json:"architect_rationale,omitempty" jsonschema:"description=One-sentence summary of why this choice fits the architecture — distinct from the longer Rationale. Read by downstream consumers (renderer; refiner; supervisor) for a quick why-glance without expanding the full rationale."`
	Confidence         float64            `json:"confidence" jsonschema:"description=Confidence in the decision on a 0.0 to 1.0 scale. 1.0 means fully committed with no reservation; 0.5 means leaning but reversible; 0.0 means forced choice under uncertainty. Used by reviewers to spot decisions that warrant deeper deliberation."`
	Alternatives       []spec.Alternative `json:"alternatives" jsonschema:"description=The other options considered with their rationale and rejection reasons. Required because per DJ-124 every decision must show the alternatives that were weighed; absence is a tell that the elaborator hit fiat rather than deliberation. Every entry must carry citations on its rejected_because reasoning so the rejection is grounded evidence rather than fabricated trade-off prose.,minItems=1"`
	Citations          []spec.Citation    `json:"citations" jsonschema:"description=Sources backing the chosen path — GOALS.md clauses; vendor docs; web-fetched research results; prior decisions. minItems=1 because per DJ-124 decisions must be grounded; an uncited decision is the failure mode the new architecture exists to eliminate.,minItems=1"`
	Axes               []string           `json:"axes" jsonschema:"description=Slug-IDs of the foundational axes this decision answers — mirrors the axis IDs the scout dispatched on. Multiple entries when one decision spans multiple axes (e.g. choosing managed Postgres covers both 'oltp-store' and 'backup-strategy').,minItems=1"`
	SurfacedBy         []string           `json:"surfaced_by" jsonschema:"description=Spec node IDs (goal / feature / strategy) that surfaced the axis this decision answers. Mirrors the input surfacing-node set from the scout's dispatch — preserved so the persistence layer carries the back-reference without a separate scout round-trip.,minItems=1"`
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
	Summary            string             `json:"summary,omitempty" jsonschema:"description=One-sentence what-was-decided ending with a period (e.g. 'Adopt Postgres over MySQL for the OLTP store.'). Distinct from Rationale (the why) and Title (the noun phrase)."`
	Title              string             `json:"title" jsonschema:"description=Concise human-readable title naming the decision (e.g. 'Database engine choice'). A noun phrase rather than a sentence — the persistence layer uses this as the decision's heading."`
	Rationale          string             `json:"rationale" jsonschema:"description=The reasoning behind the choice. Multi-sentence prose. Cites trade-offs accepted and constraints satisfied. Distinct from ArchitectRationale (which is one-sentence) and Summary (which is the what)."`
	Confidence         float64            `json:"confidence" jsonschema:"description=Confidence in the decision on a 0.0 to 1.0 scale. 1.0 means fully committed with no reservation; 0.5 means leaning but reversible; 0.0 means forced choice under uncertainty. Used by reviewers to spot decisions that warrant deeper deliberation."`
	Alternatives       []spec.Alternative `json:"alternatives,omitempty" jsonschema:"description=The other options considered with their rationale and rejection reasons. Including thoughtful alternatives is what distinguishes a decision from a fiat; populate when at least one real alternative was weighed."`
	Citations          []spec.Citation    `json:"citations,omitempty" jsonschema:"description=Sources backing the decision — GOALS.md clauses / vendor docs / prior decisions / search results. Verbatim excerpts so reviewers can audit the evidence without retrieving the source."`
	ArchitectRationale string             `json:"architect_rationale,omitempty" jsonschema:"description=One-sentence summary of why this choice fits the architecture — distinct from the longer Rationale. Read by the reconciler when deciding whether two inline decisions across features describe the same canonical decision."`
}
