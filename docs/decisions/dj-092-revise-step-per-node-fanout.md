## DJ-092: Revise Step Is a Per-Node Fanout, Not a Single Architect Call

**Status:** shipped

**Decision:** The spec-generation council's `revise` step is replaced by a four-step shape:

1. **`triage`** (1 LLM call, fast tier) — `spec_revision_triager` consumes the critic findings + the proposal's existing node IDs and emits a `RevisionPlan` routing each finding into one of three buckets: `feature_revisions[]` (concerns targeting an existing feature), `strategy_revisions[]` (concerns targeting an existing strategy), or `additions[]` (concerns proposing a missing node). Non-actionable findings are silently omitted; the trace records both the input concerns and the output buckets so an operator can see what got dropped without a separate `discarded[]` field.
2. **`revise_features`** (fanout, parallel) — one `spec_feature_elaborator` call per `feature_revisions[]` entry. Reuses the Phase-3 elaborator agent in revise mode: the projection feeds it the prior `RawFeatureProposal` plus the targeted concerns and asks for a corrected re-emission of that one node.
3. **`revise_strategies`** (fanout, parallel) — strategy counterpart, same shape.
4. **`revise_additions`** (1 architect call, conditional `has_additions`) — emits a partial `RawSpecProposal` containing ONLY the new features/strategies that address each addition concern. Existing nodes are explicitly listed as "do NOT re-emit."

The merge handler stitches the original `RawSpecProposal` (preserved in a new `state.OriginalRawProposal` field after elaborate completes) with the per-node revisions (swap by ID) and the additions (append, dropping ID collisions). `reconcile_revise` consumes the merged proposal unchanged.

**Why:** A real winplan run on Pro Preview (2026-05-02, trace `.locutus/sessions/20260502/1216/35-ef2f20.yaml`) revealed the architect short-circuiting under critic-finding pressure. Pre-revise (line 3686+) every strategy carried rich inline decisions — `strat-web-application-framework` had "Adopt Next.js on Vercel" and "Use React Server Components" with full rationale, alternatives, and citations. Post-revise (line 4605+) the architect emitted `decisions: [{}]` placeholders on every single strategy. The reconciler's `isEmptyInlineDecision` correctly drops the placeholders, but with revise replacing the whole proposal there is no fallback — all 8 persisted strategies ended up with zero decisions on disk.

This is exactly the failure mode Phase 3's elaborate fanout (DJ-090) was designed to prevent: too much input + too much output + the model short-circuits by stubbing entire sections. Phase 3 fixed it for elaborate; revise was still a single architect call carrying the full RawSpecProposal. The fix is the same pattern — bound the per-call output to one node's worth of JSON.

**Per-node fanout makes the failure structurally absent.** A revise call that touches `strat-web-application-framework` only ever produces a `RawStrategyProposal` for that one strategy. There is no "every other strategy" to short-circuit on, because every other strategy is handled by a sibling call (or untouched and passed through the merge verbatim). The empty-placeholder failure mode requires the architect to be authoring multiple strategies in one call; the fanout prevents it.

**Why a triage step.** Critics today emit free-form `{agent_id, severity, kind, text}` findings that mention node IDs or titles in prose. Without triage, every elaborator call would have to filter the global concerns list to find what applies to its node — duplicated work, inconsistent judgment across siblings. Triage is a single bounded call (small input: concerns; small output: routing plan) that maps each finding to the right bucket once. Critics keep their existing free-form output; the routing logic is one new agent, not a critic-prompt rewrite.

**Why no `discarded[]` field.** An earlier draft of `RevisionPlan` included `discarded: []string` so non-actionable findings were explicitly accounted for. No code consumes the field — the workflow's three downstream steps read `feature_revisions`, `strategy_revisions`, and `additions` only. Aspirational fields in LLM output schemas are degenerate-loop bait on weaker models (per the Span citation removal in DJ-090's follow-up). Dropping `discarded[]` keeps `RevisionPlan` to exactly the fields downstream code consumes; the trace already captures both input and output so an operator can compute what got dropped by diffing.

**Executor bug surfaced and fixed.** The new workflow has `parallel: true` + `conditional` on the same step (`revise_features` and `revise_strategies` both fanout-parallel and gated by `has_concerns`). The DAG executor's `runParallel` filtered out conditional-skipped steps but never marked them completed — the wave loop infinite-looped with skipped steps stuck in `ready` forever. The sequential branch already handled this via `runSingle`'s `skip` return; the parallel branch now returns a `skipped []string` alongside the results so the caller can mark them. Pre-existing bug exposed by Phase 1; fixed in [internal/executor/dag.go](../internal/executor/dag.go).

**What's new:**

- New types: `RevisionPlan`, `NodeRevision` in [internal/agent/revision.go](../internal/agent/revision.go).
- New agent: [internal/scaffold/agents/spec_revision_triager.md](../internal/scaffold/agents/spec_revision_triager.md), fast-tier router.
- `extractFanoutItems` extended for `revision_plan.feature_revisions` and `revision_plan.strategy_revisions`.
- `fanoutItemID` falls back from `id` to `node_id` so revise-fanout per-item event labels render.
- `has_additions` conditional gates `revise_additions`.
- `assembleRevisedRawProposal` Go function — merges original + revisions + additions for `reconcile_revise`.
- `PlanningState`: `OriginalRawProposal`, `RevisionPlan`, `RevisedFeatures[]`, `RevisedStrategies[]`, `AdditionProposals` fields.
- Three new projections: `projectTriage`, `projectReviseNode` (parameterized for feature/strategy), `projectReviseAdditions`.
- The architect's "On revise rounds" section is removed; replaced with an "On revise_additions calls" section scoped to its new responsibility (additions only).
- The two elaborator prompts gain a small "If invoked in revise mode" addendum.

**What stays the same:**

- The reconciler agent and `ApplyReconciliation` logic (DJ-088).
- The integrity critic in the critique merge pass (DJ-089).
- Cascade rewrites on conflict-resolution actions.
- Critic findings shape and the four critic agents.

**Reversal criteria:** revert if (a) triage misroutes concerns at a high enough rate that the wrong elaborator addresses them — at which point the triage prompt needs sharper rules, not abandonment of the structure; or (b) per-node revise calls produce thinner content than the prior single-call architect did, suggesting the elaborator agent isn't a good fit for revise mode — at which point we'd add a dedicated revise-elaborator agent rather than reusing the elaborate one.

**Reference:** plan at [.claude/plans/council-tools-and-revise-fanout.md](../.claude/plans/council-tools-and-revise-fanout.md), Phase 1. Phases 2 (scout grounding) and 3 (spec_lookup tool for the reconciler) are scoped in the same plan and follow.
