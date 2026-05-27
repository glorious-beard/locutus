## DJ-126: Decision Re-Elaboration for Cross-Decision Contradictions (Extends DJ-124 With Existing-Decision Revision; Depends on DJ-125 Concern Model)

**Status:** superseded by [DJ-135](dj-135-multi-runtime-pivot.md) on 2026-05-25 — decision re-elaboration for cross-decision contradictions retires as a Go-orchestrated mechanism. The discipline carries forward into the playbook (the orchestrator dispatches `spec_revise_decision` MCP tool calls when iter-N critics surface contradictions; the cascade-revisions playbook step covers the cross-node propagation). The Go-side revise-dispatch machinery in `internal/agent/workflow_spec_generation*.go` retires with the rest of the WorkflowExecutor. Prior status: proposed.

**Context.** DJ-124's workflow dispatches four kinds of work: scout (surfaces new axes and judges convergence), decision-elaborator (commits a new decision per axis in `axes_open`), narrative-elaborator (updates feature/strategy bodies referencing decisions), critics (produce findings). It does not dispatch a fifth kind that the second winplan re-run made indispensable: **re-elaborating an existing decision when the critics find it contradicts another decision, carries a factual error, or cites a hallucinated source.**

The second winplan trace ([`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/)) produced 6 substantive iter-3 critic findings:

1. `dec-datadog-observability-stack` adopts Datadog; `dec-aws-cloudwatch-logging` rejects Datadog as too expensive — cross-decision contradiction.
2. `dec-aurora-serverless-database-vendor` claims Aurora can scale to 0 ACU; actual minimum is 0.5 ACU — factual error.
3. `dec-150-dollar-off-cycle-ceiling` and `dec-tiered-seasonal-slo` cite GOALS.md excerpts that don't exist — hallucinated citations.
4. `dec-aws-ecs-fargate-deployment` (no EC2) contradicts `dec-fck-nat-egress` (commits to t4g.nano ARM EC2 instances) — cross-decision inconsistency.
5. Sum of decided baseline costs (Aurora storage + min ACU + NAT + Datadog) exceeds `dec-150-dollar-off-cycle-ceiling` — financial incoherence.
6. Missing axis: shared schema location between Next.js frontend and Go ingestion runtime.

Only finding #6 is something DJ-124's workflow can resolve: the scout surfaces it as `axes_open`, the decision-elaborator commits a new decision. The other five require modifying existing decisions. The workflow has no path for that. The scout's only available move is to surface a workaround axis ("observability-tool-coherence", "cost-runaway-protections", "peak-cost-ceiling") hoping the new decision papers over the contradiction, but the original contradictory decisions stay in the graph — observed in the iter-4 `axes_open` of the same trace, which fired three synthetic budget axes that never actually invalidated `dec-150-dollar-off-cycle-ceiling`'s commitment. Convergence never holds.

Under the pre-DJ-124 architecture (DJ-122), the revise step re-elaborated affected nodes; affected nodes were features and strategies with inline decisions; revising the node revised its decisions. DJ-124 separated decision-authoring from narrative-authoring; the loss of inline-decision-revision was unintentional — the workflow rewrite focused on "decisions first, narrative second" without preserving the "decisions can also be revised when wrong" path.

**Why this surfaced now.** DJ-124's flagship validation case (winplan re-run) is the first time critics flagged real contradictions between settled decisions on a non-trivial spec graph. The smoke-run-during-DJ-124-development used `MockExecutor` scripts that never produced contradictions, so the gap didn't appear in tests. [DJ-125](dj-125-in-flight-manifest.md) closes the projection and concern-tracking gaps that obscured this issue; with DJ-125 in place, the residual failure is the missing decision-revision dispatch. Depending on DJ-125's `Concern.RelatedDecisionIDs` substrate makes the dispatch tractable — without it, the workflow has no structural way to know which decisions the critic wants revised.

**Decision.** Add a `revise-decisions` step to the DJ-124 convergence loop, dispatched per concern with `Status: open` AND `len(RelatedDecisionIDs) > 0`. For each such concern, dispatch one `spec_decision_elaborator` call in **revise mode** with input:

- The full prior `RawDecisionProposal` for each related decision.
- The critic finding text and severity.
- The relevant slice of the in-flight manifest (the contradicting decisions, the cited GOALS.md, the surrounding strategy bodies).

Output: a corrected `RawDecisionProposal` for the same axis ID(s). `mergeDecisions` matches by axis-ID + decision-ID and replaces the entry in `state.RawProposal.Decisions` (rather than appending a new one).

The new iteration template:

```
scout → decisions(per axes_open fanout)
      → narrative(per affected_node fanout)
      → revise-decisions(per open concern with related decisions, fanout) ← NEW
      → critique
      → scout (next iter)
```

Three design commitments:

1. **`spec_decision_elaborator` handles both first-author and revise modes.** The agent's prompt receives a "Prior decision" block when in revise mode; absent in first-author mode. The agent's `RawDecisionProposal` output schema is unchanged — it always emits a complete decision. The mode-switch is at the projection layer (per-fanout-item) not the agent surface. Same agent, two prompts that share a body and diverge in the "context to react to" section.

2. **Replace-by-axis-ID, not append.** When `mergeDecisions` sees a `RawDecisionProposal` whose `Axes` intersect with an existing decision's `Axes`, it REPLACES rather than appending. Preserves the "one decision per axis at a time" invariant. The old decision's ID is preserved; rationale, alternatives, citations are all overwritten by the revision. History of revisions is captured in DJ-103 events (one event per revision).

3. **Cycle detection adapts.** Today's `DecidedAxesByIter` map flags re-opened axes as cycles. After DJ-126, an axis can be legitimately "re-decided" (the revise path), but the dispatch is concern-driven (a critic flagged it) not scout-driven (no axis appears in `axes_open` twice). Cycle detection stays the safety net for the scout-side path; the revise path is exempt. A new per-axis revision-count cap (default 3) prevents the alternate failure mode of "revise dec-X → critic flags revised dec-X → revise again → ..." infinite loops.

**Alternatives considered.**

- **Force user intervention.** Surface the contradiction to the user; require explicit `locutus refine dec-X --against "the critic finding"`. Defensible for a non-autonomous tool; defeats the whole point of an autonomous spec council that converges within budget. Held only as the fallback verb behind the autonomous path.

- **Scout surfaces a "resolution axis" for cross-decision contradictions.** Instead of revising, the scout emits a synthetic axis like "observability-tool-coherence" with `surfaced_by: [dec-datadog, dec-cloudwatch]`. The decision-elaborator picks one. This is exactly what the iter-3 scout actually did in the failing winplan run — emitted `peak-cost-ceiling` and `cost-runaway-protections` to paper over the financial incoherence. Two structural problems: (a) the original contradictory decisions stay in the graph (no way to remove `dec-Y` when `dec-X` resolves the axis); (b) the synthetic axis is rarely well-formed — the model invents an ill-defined axis rather than admitting the contradiction directly. Rejected.

- **Reconciler-level contradiction supersede actions.** Today's `ReconciliationVerdict` carries a no-op `actions` array (Stage A simplified the verdict). Extending it to allow "supersede dec-X with dec-Y" actions would let the reconciler resolve contradictions mechanically. Two problems: (a) the reconciler is sequential after the elaborators; under DJ-124's flow it can't introspect "which decision is right" — that's the decision-elaborator's job; (b) the reconciler is mid-retirement (DJ-125 makes its role mostly empty); building new capability into a step that's going away is bad scope. Rejected.

- **Re-elaborate every decision every iteration.** Naive: have the workflow re-fire `spec_decision_elaborator` for every existing decision every iteration. Token cost balloons (O(N × iterations) decisions of grounded research per session); most iterations don't need most decisions revised. Rejected as obvious over-cost.

- **Critic-driven dispatch with a separate `decision_reviser` agent.** Have the critic emit `decision_revision_requests[]` alongside `issues[]`, dispatching a new agent type to handle the revision. Adds a new agent surface and a new prompt to maintain; the decision-elaborator's revise mode is the simpler shape (one agent, two contexts). Rejected on surface-area grounds.

**Consequences.**

- **Code:**
    - `internal/agent/workflow_spec_generation_dj124.go` — new step in the iteration template: `revise-decisions` between `narrative` and `critique`. Fanout closure walks `state.Concerns` with `Status: open` and `len(RelatedDecisionIDs) > 0`. Conditional: skip when no such concerns exist.
    - `internal/agent/workflow_spec_generation_dj124.go` — `mergeDecisions` extended to detect "replacement vs append" by intersecting `RawDecisionProposal.Axes` with existing decisions' `Axes`. Replacement preserves the existing ID; the prior decision body is overwritten with the revised one.
    - New per-fanout projection `projectReviseDecision(snap)` that renders the manifest + the prior decision (full body) + the critic finding text + severity.
    - `internal/scaffold/agents/spec_decision_elaborator.md` — gains a "Revise mode" section: when the user message includes a "Prior decision" block AND a "Critic finding to address" block, emit a corrected `RawDecisionProposal` for the same axis (preserve `axes` verbatim). Walk `docs/agent-conventions.md` before drafting per the memory checklist; the literal-sentinel pattern (from `justify_researcher.md`) applies when grounded research disagrees with the prior decision's claim.
    - Cycle detection in `scoutSpawnFor` adjusts: only count axis appearances in `axes_open` toward the cycle threshold; the revise-decisions path doesn't count. A new per-axis revision-count cap (env var `LOCUTUS_DECISION_REVISION_CAP`, default 3) prevents revise-loop infinite recursion.
    - DJ-103 history events: `decision_revised` event kind, recording prior body + revised body + driving concern text. Surfaces in `locutus history`.
    - Tests: `TestReviseDecisionsDispatchPerOpenConcern`, `TestMergeDecisionsReplacesByAxisID`, `TestReviseDecisionsPreservesIDs`, `TestRevisionCapTerminatesRevolvingDoor`, end-to-end smoke test where the loop converges after a forced critic contradiction.

- **User-visible:**
    - Session traces show `spec_decision_elaborator-<axis-id>:revise` fanout items distinct from first-author calls.
    - `locutus history` records both the original decision and each revision (via DJ-103 events); the future `locutus explain` can show "this decision was revised in iter-N because of finding-X" with the full lineage.
    - Convergence on real projects with contradictions becomes achievable within the default 5-iteration budget.

- **Performance:**
    - Per-revision call: same wall-clock as a first-author decision-elaborator call (~30-90s with grounded research). Adds 1-3 calls per iteration on average (one per `open` concern with related decisions).
    - Net session latency: roughly +15-30% per session on contradiction-heavy projects; -50%+ vs current budget-exhaustion behavior because the loop actually terminates.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. Existing sessions terminated under `convergence_failed` before DJ-126 had no `revise-decisions` step; new sessions get the step. No persisted-state schema change beyond DJ-125's `Concern.RelatedDecisionIDs`.

**Reversal criteria.** Revert if:

- (a) the decision-elaborator's revise mode produces unstable decisions — each revision contradicts the prior, the loop never converges. Surfaces as the same axis being revised every iteration through budget exhaustion. Mitigation: tighten the revise-mode prompt to require the elaborator to acknowledge what the prior decision committed AND why it's wrong, not emit a fresh take. The revision-count cap is the safety net.
- (b) the revise dispatch fires too liberally — every iteration produces 10+ revisions, churning the graph. Mitigation: rank concerns by severity; revise only `severity: high`; lower-severity concerns get the scout's `wontfix` disposition.
- (c) revisions cycle through different decision IDs for the same axis ad infinitum (revise `dec-X` → axis appears in `axes_open` via scout → new decision `dec-Y` → critic flags `dec-Y` → revise `dec-Y` → ...). The per-axis revision-count cap catches this; if the cap fires frequently in real usage, the cap's threshold needs lowering or the dispatch logic needs to distinguish "revision of same decision id" from "new decision for same axis."

**Reference.** Extends [DJ-124](dj-124-decisions-before-narrative.md) with the missing decision-revision dispatch. Depends on [DJ-125](dj-125-in-flight-manifest.md) — specifically `Concern.RelatedDecisionIDs` is the dispatch key. Restores the decision-revision capability that pre-DJ-124's revise step provided implicitly (by revising features that carried inline decisions); makes the revision path explicit at the decisions layer where DJ-124 located decision-authoring. Honors [DJ-103](dj-103-history-narrative-cache-archivist.md) by recording each revision as a structured event. Motivated by the second winplan re-run trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/) which exposed the structural gap.
