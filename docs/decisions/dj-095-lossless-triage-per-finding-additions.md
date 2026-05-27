## DJ-095: Lossless Triage + Per-Finding Additions Fanout

**Status:** shipped

**Decision:** Two coupled changes restore the council's signal pathway from critic findings to spec mutations:

1. **Triage routes everything.** The `spec_revision_triager` agent's prompt drops its rule 5 ("non-actionable → omit") in favour of a routing-completeness mandate: every input critic finding lands in exactly one of `feature_revisions`, `strategy_revisions`, or `additions`. There is no fourth bucket. The critic already did the actionability judgment by emitting the finding; the triager's only authority is routing. When uncertain, the prompt directs the triager to default to `additions` with `kind: "strategy"` — that's the recoverable failure mode (a strategy that turns out to be unnecessary is recoverable by the next refine pass; a finding silently dropped is not). Triager `capability` flips from `fast` to `balanced` since routing 32 findings is closer to a judgment call than the simple keyword-mapping fast tier handles cleanly.

2. **Additions becomes a per-finding fanout.** The `RevisionPlan.Additions` field changes from `[]string` to `[]AddedNode { kind: "feature"|"strategy", source_concern: string }`. The single `revise_additions` step (one architect call asked to invent N nodes from a list) is replaced by two fanout steps `revise_feature_additions` / `revise_strategy_additions` filtered by kind, each dispatching `spec_feature_elaborator` / `spec_strategy_elaborator` once per AddedNode. The elaborators gain an "addition mode" projection: the user message includes a "Node to propose (addition)" block with the verbatim critic finding, an "Existing nodes (do NOT re-emit)" list, and a directive to invent one new node (id, title, body, decisions) from the finding. `PlanningState.AdditionProposals` changes from `string` to `[]string` to accumulate per-finding outputs; `assembleRevisedRawProposal` sniffs the id prefix per entry to dispatch into the merged feature/strategy slices.

**Why:** A real winplan run on 2026-05-03 (trace `.locutus/sessions/20260503/0034/35-d9cdc4/`) — with Phases 1-3 shipped — surfaced both problems compounding on each other.

The four critics emitted ~32 findings: devops flagged missing CI/CD, environments, rollback, secrets, deps, build (6); SRE flagged missing observability tooling, SLO targets, on-call, capacity, circuit breakers, runbooks, error budget (7); architect flagged missing Multi-deliverable coordination, Documentation, Build Tooling, Distribution Channel, Backend connectivity protocol, AWS-vs-Vercel coherence, plus several decision-language violations (14); cost flagged missing cost ceiling, no caps/alarms, no cheap alternatives (5).

Of those ~32 findings, the triager (call 0024) routed exactly **3** — two cost/capacity concerns onto `feat-voter-file-management` and one SLO concern onto `feat-field-canvassing-interface`. The other 29 fell into the "non-actionable, omit" bucket. The result on disk: a spec missing IaC, CI/CD, secrets, observability, auth, build tooling — exactly the gaps the critics had identified. The triager was detecting them and discarding them in the same call.

Even if triage had routed correctly, the additions path remained a single architect call (DJ-092 `revise_additions`) — structurally identical to the pre-Phase-1 revise step that failed by emitting placeholder decisions under multi-node authoring pressure. With 29+ additions, that call is the same anti-pattern, just one round later. Fixing both together is what makes the signal pathway lossless: the critics surface a gap → the triager routes it → the elaborator authors a corrected node addressing it → the reconciler dedupes across cluster.

**Why no discard bucket.** The earlier "non-actionable → omit" rule was added so the triager could drop pure observations and already-addressed findings, but in practice it became the triager's escape hatch when a finding didn't obviously fit one of the three actionable buckets. Removing the rule trades occasional over-routing (one wasted elaborator call when an addition turns out unnecessary) for never-silently-dropped findings (a wasted call costs spend; a dropped finding costs a spec gap that ships to disk). Reconciler dedup catches the over-routing tail (5 critics flagging the same missing strategy → 5 elaborator outputs collapsed to 1 canonical).

**Why per-finding fanout.** Same structural reason DJ-092 made revise per-node: a single architect call asked to author N new nodes from a list short-circuits under multi-node authoring pressure (placeholder decisions, missing nodes). One bounded elaborator call per addition is structurally isomorphic to the elaborate path that already works at scale.

**What's new:**

- `AddedNode { Kind, SourceConcern }` struct in [internal/agent/revision.go](../internal/agent/revision.go); `RevisionPlan.Additions` retyped from `[]string` to `[]AddedNode`.
- `PlanningState.AdditionProposals` retyped from `string` to `[]string`. Merge handler appends instead of overwrites; `assembleRevisedRawProposal` iterates the slice, sniffs id prefix per entry (`extractRawID` helper), dispatches into merged feature/strategy slices with collision-drop semantics.
- `extractFanoutItems` gains paths `revision_plan.additions.features` and `revision_plan.additions.strategies` that filter the AddedNode list by kind.
- `projectAdditionElaborate(snap, kind)` projection in [internal/agent/projection.go](../internal/agent/projection.go); `ProjectState` routes `revise_feature_additions` / `revise_strategy_additions` step IDs to it. The old single-call `projectReviseAdditions` is removed.
- Workflow YAML: single `revise_additions` step replaced by two fanout steps `revise_feature_additions` (`spec_feature_elaborator`, `parallel: true`) and `revise_strategy_additions` (`spec_strategy_elaborator`, `parallel: true`), both gated on `has_additions`. `reconcile_revise` depends on both.
- [spec_revision_triager.md](../internal/scaffold/agents/spec_revision_triager.md) prompt rewrite: rule 5 dropped, routing-completeness mandate added, additions output shape changed to `AddedNode`, capability `fast` → `balanced`. The "Bias toward routing, not discarding" mandate is now load-bearing rather than a soft hint.
- Elaborator prompts ([spec_feature_elaborator.md](../internal/scaffold/agents/spec_feature_elaborator.md), [spec_strategy_elaborator.md](../internal/scaffold/agents/spec_strategy_elaborator.md)) gain an "addition mode" addendum describing how to invent a new node from a single critic finding, including id-prefix conventions and the "do NOT re-emit existing nodes" directive.
- [spec_architect.md](../internal/scaffold/agents/spec_architect.md) drops its "On revise_additions calls" section — the architect is no longer the additions author; the elaborators are.
- New tests: `TestExtractFanoutItemsAdditions` (kind-filter + empty-default), `TestMergeResultsAdditionProposalsAccumulates`, `TestAssembleRevisedRawProposalAppendsAdditions` updated for slice shape, `TestAssembleRevisedRawProposalAdditionsDedupOnExistingID`, `TestAssembleRevisedRawProposalAdditionsUnknownPrefixDropped`, `TestProjectAdditionElaborateRendersConcernAndExistingNodes`, plus `TestProjectStateRoutesReviseStepsCorrectly` updated for the new step IDs.

**What stays the same:**

- The reconciler's task and ApplyReconciliation logic. Cross-cluster dedup is exactly its job; multiple critics flagging the same missing strategy collapse to one canonical via existing semantics.
- Per-node revise fanouts (DJ-092) — same shape, same elaborator agents.
- Critique step and the four critic agents.
- `has_additions` conditional — unchanged because `len(plan.Additions) > 0` works whether Additions is `[]string` or `[]AddedNode`.

**Reversal criteria:** revert if (a) per-finding fanout cost becomes prohibitive in real runs (29+ strong-tier elaborator calls per refine; per-call 5m timeout bounds runaway, but aggregate spend may bite) — at which point the mitigation is capping additions per critic in the triager prompt or moving the addition elaborator to balanced tier; or (b) the reconciler's cross-cluster dedup turns out to under-collapse, leaving the spec with multiple near-identical strategies after every run — at which point we'd add a triager-side pre-cluster pass before fanout dispatch.

**Open questions:**

- **Multi-round convergence.** New nodes added in this pass aren't themselves criticised. If single-pass output still has meaningful gaps the same critics would flag on a second round, we'd bump `max_rounds` to 2-3 — that's the natural Phase 5 follow-up. Defer until measured.
- **Addition kind misclassification.** A finding routed as `feature` but really a `strategy` (or vice versa) goes to the wrong elaborator agent. Recoverable: the strategy elaborator can produce feature-shaped output and vice versa under the right system prompt. Costs accuracy, not correctness.

**Reference:** plan at [.claude/plans/council-tools-and-revise-fanout.md](../.claude/plans/council-tools-and-revise-fanout.md), Phase 4. Sequencing notes Phases 1-3 (DJ-092, DJ-093, DJ-094) ship before this one. Phase 5 (multi-round convergence) is the natural follow-up if Phase 4's single-pass output isn't comprehensive enough.
