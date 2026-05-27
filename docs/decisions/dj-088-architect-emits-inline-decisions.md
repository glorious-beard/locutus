## DJ-088: Architect Emits Inline Decisions; Reconciler Assigns IDs Post-Hoc

**Status:** shipped

**Decision:** The spec-generation council's architect (`spec_architect`) no longer emits a flat `decisions[]` array with shared IDs that features and strategies cross-reference. Instead, it emits a `RawSpecProposal`: features and strategies, each with their decisions **inline** as embedded objects with no IDs. A new reconciler agent (`spec_reconciler`) clusters duplicate or conflicting inline decisions across the proposal and emits a `ReconciliationVerdict` (action kinds: `dedupe`, `resolve_conflict`, `reuse_existing`); a deterministic Go function `ApplyReconciliation` consumes the verdict + raw proposal + existing-spec snapshot and produces the canonical `SpecProposal` with shared, slug-derived IDs that downstream agents and the persistence layer continue to expect.

**Why:** Phase 1 (DJ-087) dropped approaches and fixed half the dangling-ref problem. A post-Phase-1 winplan run on `googleai/gemini-3-flash-preview` confirmed the remaining failure mode: 23 dangling references in the integrity gate, all in `feature.decisions[]` and `strategy.decisions[]` cross-references between separate top-level arrays. The architect was juggling ~20 cross-array references in attention while generating prose — a load weaker models can't keep coherent.

The structural fix is to remove cross-references from the architect's output entirely. Each parent carries the decisions it requires inline. The reconciler's job is the cross-cutting view: where the architect has duplicated itself, dedupe; where it has contradicted itself, resolve. The architect's prompt collapses (half its mandates were referential-integrity rules); cognitive load drops without splitting the call into per-node fanout.

**Why this beats alternatives.** Two were considered and rejected:

- **Decisions-first decomposition** (one architect call for decisions, then one for structure that references them by id) — chicken-and-egg: the architect can't know which decisions to make until it knows what features and strategies need them.
- **Per-node fanout** (Phase 3 in the plan) — splits the architect call into one elaboration per feature and one per strategy, with a reconciler converging the output. Eliminates cross-call coordination but introduces a new workflow primitive (`fanout`) and a third agent (`spec_outliner`). Phase 2's inline-decisions design solves the cross-reference problem without the additional surgery; fanout becomes a clean escalation if a single architect call still degrades on big projects, since the reconciler doesn't change.

**The flow:**

```
survey → propose (raw) → reconcile → critique → revise (raw, conditional) → reconcile_revise (conditional)
```

The architect always emits `RawSpecProposal`; both `propose` and `revise` go through the same reconciler. The integrity-revise loop in `GenerateSpec` becomes a vestigial backstop — there are no cross-references in the architect's output to dangle, and `ApplyReconciliation` is deterministic and structurally cannot produce a malformed proposal.

**ID assignment.** Reconciler-assigned, slug-derived from the canonical decision title (`dec-use-postgres`, `dec-async-ingest`). Collisions across decisions whose titles slugify identically get a numeric suffix (`-2`, `-3`). Architects can't fabricate IDs because the architect contract has no ID field on `InlineDecisionProposal`.

**Existing-spec ID reuse.** When extending a spec, the reconciler sees `Existing.Decisions` and can mark a cluster `reuse_existing` with an existing decision's ID. `ApplyReconciliation` rewrites the parent's `decisions[]` to reference the existing ID without minting a new canonical decision.

**`InfluencedBy` dropped from the architect contract.** The field was an inter-decision reference — the same cross-reference problem inline decisions were designed to eliminate. Influence relationships, when they matter, are added during refine, not greenfield generation.

**Cascade rewrite on conflict.** When the reconciler resolves a conflict, the architect's prose for affected feature/strategy nodes was written under the loser. After persistence, `cmd/specgen.go::cascadeAfterReconcile` reloads each affected node and runs `cascade.InvokeRewriter` (a new exported variant of the rewriter that operates in-memory) to align the prose with the canonical decision set. Best-effort: a rewriter failure logs but doesn't roll back the spec.

**Migration:** the architect agent at `.borg/agents/spec_architect.md` and the workflow YAML at `.borg/workflows/spec_generation.yaml` ship via `locutus init`. Existing projects keep their old versions until they re-init or run `locutus update --offline --reset`. The on-disk spec shape under `.borg/spec/` is unchanged (DJ-085 stability).

**Reversal criteria:** revert if (a) the reconciler routinely over-merges (collapses compatible-but-distinct decisions into one) or under-merges (leaves obvious duplicates separate) at a rate that materially degrades spec quality on `gpt-class` and `claude-sonnet-class` models — at which point the design moves to fanout (Phase 3) where each call's clustering surface is bounded; or (b) the per-run cost of the extra reconcile call (one for clean runs, two when revise fires) outweighs the savings from dropped integrity-revise retries on the model spectrum we care about.

**Reference:** plan at [.claude/plans/council-resilience.md](../.claude/plans/council-resilience.md), Phase 2. Builds on DJ-087.
