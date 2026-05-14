package agent

// Phase 3 types for outline + per-node elaborate fanout. The outliner
// emits a slim Outline (titles + summaries only); per-node elaborators
// emit full RawFeatureProposal / RawStrategyProposal each. The
// reconciler from Phase 2 consumes the union of elaborated outputs
// unchanged — only the call topology upstream of it changes.

// Outline is the spec_outliner agent's output: feature and strategy
// titles + one-line summaries, no decisions, no detailed descriptions.
// Used to drive the elaborate-fanout step (one per item) and to give
// each elaborator situational awareness of the whole proposal's
// shape without dumping full sibling content into every prompt.
type Outline struct {
	Features   []OutlineFeature  `json:"features,omitempty" jsonschema:"description=Planned user-facing capabilities. Each entry seeds one elaborator call that produces a full RawFeatureProposal. Empty when the outline contributes only strategies."`
	Strategies []OutlineStrategy `json:"strategies,omitempty" jsonschema:"description=Planned cross-cutting commitments (storage; deployment; observability). Each entry seeds one elaborator call that produces a full RawStrategyProposal. Empty when the outline contributes only features."`
}

// OutlineFeature names a feature the architect intends to elaborate.
// ID is slug-derived from Title at outline time so downstream
// elaborators receive a stable identifier; the elaborator preserves
// it on the produced RawFeatureProposal.
type OutlineFeature struct {
	ID      string `json:"id" jsonschema:"description=Stable slug starting with 'feat-'; lowercase; hyphen-separated; three to five words derived from the title (e.g. 'feat-realtime-dashboard'). The downstream elaborator preserves this id verbatim on the produced RawFeatureProposal."`
	Title   string `json:"title" jsonschema:"description=Concise human-readable title in sentence case (e.g. 'Real-time dashboard'). A noun phrase; not a sentence."`
	Summary string `json:"summary" jsonschema:"description=One-sentence what-the-feature-does; ending with a period. Gives sibling elaborators situational awareness without inflating their prompt with full descriptions."`
}

// OutlineStrategy is the strategy counterpart. Kind is one of
// "foundational", "derived", "quality" — mirrors StrategyProposal.
type OutlineStrategy struct {
	ID      string `json:"id" jsonschema:"description=Stable slug starting with 'strat-'; lowercase; hyphen-separated; three to five words derived from the title (e.g. 'strat-compute-platform'). The downstream elaborator preserves this id verbatim on the produced RawStrategyProposal."`
	Title   string `json:"title" jsonschema:"description=Concise human-readable title in sentence case naming the cross-cutting concern (e.g. 'Compute platform'). A noun phrase."`
	Kind    string `json:"kind" jsonschema:"description=Category of cross-cutting concern: 'foundational' (commitments that everything else builds on; e.g. database engine); 'derived' (follows from a foundational choice; e.g. ORM choice given database); 'quality' (cross-cutting non-functional; e.g. observability stack)."`
	Summary string `json:"summary" jsonschema:"description=One-sentence what-the-strategy-adopts; ending with a period. Gives sibling elaborators situational awareness."`
}
