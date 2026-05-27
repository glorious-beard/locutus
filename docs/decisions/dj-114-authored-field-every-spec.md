## DJ-114: Authored `Summary` Field on Every Spec Node

**Status:** shipped

**Decision:** Every persisted spec node — Feature, Strategy, Decision, Bug, Approach — carries a `Summary` field: one or two sentences describing **what** the node is, distinct from the longer prose (`Description` / `Rationale` / `Body`) and from the one-line "why" already on Decision (`ArchitectRationale`). The authoring/refining agents emit it as part of their existing output schema; the persistence layer threads it from the proposal types onto disk. `BuildSpecManifest` reads `Summary` verbatim when present and falls back to derived truncation when absent. Nodes that pre-date the field (legacy projects) are filled by the `SummariesPresent` prereq via a fast-tier `spec_summarizer` agent. See DJ-115 for the prereq mechanics.

**Why this lives on the node, not in a derived index.** DJ-094 introduced `spec_list_manifest` as a tool the council uses to navigate the spec graph without inlining every node's full content. The manifest's per-entry `Summary` field was derived at tool-call time by truncating the first 200 runes of `Description` / `Rationale` / `Body`. For long-form rationale that opens with framing ("This is a foundational choice…") before stating the substance, the truncation routinely captures the lead-in instead of the conclusion — the manifest becomes a misleading index. Persisting an authored one-liner on the node fixes the signal without introducing a separate sync surface. The spec directory remains the manifest (DJ-068); `Summary` is a field on the existing files, not a new index file.

**Why this is distinct from `Decision.ArchitectRationale`.** ArchitectRationale (DJ-085) is the one-sentence **why** — "Postgres won because of mature replication tooling and operational maturity." Summary is the one-sentence **what** — "Adopt Postgres with logical replication for the OLTP store." Both exist; they answer different questions and would conflate badly if merged. The "why" is queried in a follow-up when needed (via `justify` or by reading the full Rationale); the "what" is the field a scanning agent reads to decide whether the node is relevant at all.

**Why authored, not derived.** The model that wrote the rationale is best-positioned to summarise it — it has the full context the persisted node will be summarising, and it produces the Summary in the same LLM call that produces the rest of the node. A separate post-hoc summariser (the prereq path; see DJ-115) is strictly the fallback for legacy nodes the authoring path didn't cover. The authoring path adds zero new LLM calls in the steady state; the fallback path runs at most once per legacy node per project.

**Soft-validated, not hard-rejected.** `IsWellFormedSummary` checks length ≤ 600 chars + sentence-terminal punctuation (`.`, `!`, `?`). Violations are logged but accepted. The LLM call's cost is already paid by the time the value reaches us; rejecting a cosmetically off-by-one summary forces another round-trip to fix what is almost always a stray period. Hard-required is "non-empty after whitespace trim" (`HasSummary`), enforced by the SummariesPresent prereq.

**What's new:**

- `Summary string` field on `spec.Feature`, `spec.Strategy`, `spec.Decision`, `spec.Bug`, `spec.Approach` ([internal/spec/types.go](../internal/spec/types.go), [internal/spec/bug.go](../internal/spec/bug.go), [internal/spec/approach.go](../internal/spec/approach.go)). JSON-tagged `summary,omitempty` so legacy files deserialise cleanly with the field empty.
- `spec.HasSummary(s string) bool` and `spec.IsWellFormedSummary(s string) bool` helpers ([internal/spec/summary.go](../internal/spec/summary.go)) for the hard and soft checks respectively. `SummaryMaxChars = 600`.
- `Summary` field added to the authoring agents' output schemas: `RawFeatureProposal`, `RawStrategyProposal`, `InlineDecisionProposal`, `FeatureProposal`, `StrategyProposal`, `DecisionProposal`. Optional in the schema (`,omitempty`) so models that don't yet emit it still produce conformant responses; the prereq backfills.
- Example payloads in `RegisterSchema` calls ([internal/agent/schemas.go](../internal/agent/schemas.go)) demonstrate the `Summary` field so the schema-prompt-doc renderer surfaces it to the model.
- Authoring agent prompts ([spec_architect.md](../internal/scaffold/agents/spec_architect.md), [spec_feature_elaborator.md](../internal/scaffold/agents/spec_feature_elaborator.md), [spec_strategy_elaborator.md](../internal/scaffold/agents/spec_strategy_elaborator.md)) instruct the model to emit `summary` with the soft-validation criteria.
- `ApplyReconciliation` threads `Summary` from `Raw*Proposal` through the reconcile step onto `*Proposal`, and `SpecProposal.ToAssimilationResult()` copies it onto the persisted `spec.*` ([internal/agent/reconcile.go](../internal/agent/reconcile.go), [internal/agent/specgen.go](../internal/agent/specgen.go)).
- `BuildSpecManifest` reads `Summary` verbatim when non-empty; `summaryOrFallback` collapses the prior derived-truncation path into a defensive fallback ([internal/agent/spec_tools.go](../internal/agent/spec_tools.go)).

**What stays the same:**

- `.borg/manifest.json` remains the project-root marker (DJ-081). No new manifest file.
- DJ-094's `spec_list_manifest` and `spec_get` tool contract is unchanged; the only behavioral difference is summary quality.
- ArchitectRationale (DJ-085) stays on Decision as the durable one-line "why."
- Existing `Description` / `Rationale` / `Body` fields are untouched — Summary sits beside them.
- Refine-cascade rewriters (`RewriteFeatureResult` / `RewriteDecisionResult` / `RewriteStrategyResult`) and the approach regenerator are NOT updated in this pass. The prereq path covers them — when refine rewrites a node, the new content lacks an authored Summary; the next prereq run fills it. Adding Summary to the refine output schemas is a clean follow-on that further reduces prereq dependence; it's not load-bearing for correctness today.

**Rejected alternatives:**

- **Derive `Summary` at read time, no field on the node.** The DJ-094 path. Misleading on every node whose primary prose opens with framing instead of conclusion (most decisions in practice). Rejected because the cure — manually adjusting prose to lead with the conclusion — would distort the documents the prose serves elsewhere (explain, justify, refine cascade).
- **A separate persisted index file (`.borg/spec/manifest.json`).** Considered and rejected in DJ-094; rejected again here for the same reason. The field-on-the-node approach has zero drift surface — every refine that rewrites the JSON rewrites the Summary too.
- **One unified `Summary` shared between "what" and "why."** Conflates the two concerns. The two questions ("what is this node?" and "why was this chosen?") have different scanning audiences. Decision-only nodes would get the "why" version; non-decision nodes would have only the "what." Asymmetric.
- **Required at the schema level (`minItems`-style hard reject).** Forces every authoring agent to emit it in one synchronized release. Today some agents are updated; some are not. A required field would mean every refine path fails until every agent ships with Summary. The prereq backfill makes this graceful — the prereq IS the enforcement, just lazy.

**Reversal criteria:** revert if (a) operators routinely override authored summaries because the council's one-liners are misleading often enough to be worse than derived truncation — at which point we either tighten the prompt or switch back to derived; or (b) embedding-based semantic search supplants the manifest-scan workflow (the agent fetches by semantic similarity, never reading the index), at which point the authored summary becomes documentation rather than load-bearing search signal. Neither failure mode invalidates the data model — the field on the node is the right shape; only the consumer changes.

**Reference:** depends on DJ-094 (spec-lookup tools the manifest serves), DJ-085 (DecisionProvenance / ArchitectRationale — the "why" sibling), DJ-068 (`.borg/spec/` IS the manifest; no derived index file). Companion to DJ-115 (the prereq mechanics that backfill missing summaries on legacy nodes).
