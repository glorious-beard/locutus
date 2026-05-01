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
	Features   []OutlineFeature  `json:"features,omitempty"`
	Strategies []OutlineStrategy `json:"strategies,omitempty"`
}

// OutlineFeature names a feature the architect intends to elaborate.
// ID is slug-derived from Title at outline time so downstream
// elaborators receive a stable identifier; the elaborator preserves
// it on the produced RawFeatureProposal.
type OutlineFeature struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// OutlineStrategy is the strategy counterpart. Kind is one of
// "foundational", "derived", "quality" — mirrors StrategyProposal.
type OutlineStrategy struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}
