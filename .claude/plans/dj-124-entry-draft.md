# Drafts for DECISION_JOURNAL.md

> Two artifacts to review and append/insert:
>
> 1. **DJ-123 epilogue** — append to the end of DJ-123's existing entry (immediately after its current `**Reference.**` paragraph at line 3280).
> 2. **DJ-124 entry** — append to the end of `docs/DECISION_JOURNAL.md` after DJ-123's entry.
>
> Both are standalone Markdown blocks. After your review, either tell me to make the edits or paste them in directly.

---

## Artifact 1: DJ-123 Epilogue

> Append to DJ-123's existing entry (after the current `**Reference.**` paragraph).

```markdown
**Epilogue (2026-05-15).** The first real winplan re-run with DJ-123's elaborator-side in-flight search shipped (binary at `29a2ba7`) failed to converge in 5 iterations — trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/). Diagnosis: zero `spec_search` calls fired across 138 agent invocations. The Phase 4 prompt edits did not move Flash-tier elaborators toward tool-use; Pro-tier reconciler invoked `spec_list_manifest` reliably but never the search. The infrastructure works; the consumers don't engage. More fundamentally, the iter-3 → iter-4 explosion pattern (3 open dimensions → 5 entirely new ones) revealed a structural issue DJ-123 cannot solve: decisions and narrative are coupled in elaborator output, so revising one re-litigates the other. DJ-124 supersedes DJ-123 as the convergence mechanism by separating decision-making from narrative authoring into distinct phases. DJ-123's in-flight search infrastructure (Phases 1-3, 5 of its plan) remains as defense-in-depth in DJ-124's Phase 2 narrative elaborators. **Status update:** `proposed` → `landed; superseded as convergence mechanism by DJ-124; in-flight search infrastructure stays as defense-in-depth.`
```

---

## Artifact 2: DJ-124 Entry

> Append after the DJ-123 epilogue.

```markdown
## DJ-124: Spec Generation Re-Architecture — Decisions Before Narrative, Scout-as-Judge Convergence, Unified Import Flow (Refines DJ-068 Spec Graph Topology, Replaces DJ-105 Inline-Decisions Schema, Re-Scopes DJ-123 In-Flight Search)

**Status:** proposed

**Context.** DJ-122 shipped a convergence loop for the spec-generation council. DJ-123 attempted to close cross-iteration recurrence in that loop by giving elaborators an in-flight `spec_search` against the working `RawProposal`. The first real winplan re-run (trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/)) failed to converge inside the 5-iteration budget. The empirical signature was an **iter-3 → iter-4 explosion**: iter-3 had 3 open dimensions (multi-tenant isolation, rollout strategy, runbook structure); iter-4 had 5 entirely *new* dimensions (Redis hosting, auth provider, container orchestration, DB engine, multi-tenant isolation re-named); iter-5 saw the same 5 with subtle renames. The DJ-123 in-flight search did not fire — zero `spec_search` invocations across 138 agent calls — but the deeper problem is structural.

Three coupled structural issues surfaced from the diagnosis:

First, **decisions and narrative are authored together by elaborators**. Each elaborator emits a feature/strategy body AND the inline decisions justifying it in a single output. When the reconciler picks a winning decision among siblings, the narrative on the losing side is now wrong and must be re-elaborated; that re-elaboration produces new inline decisions on adjacent axes that may conflict with other siblings; iterate. Convergence requires breaking this coupling.

Second, **today's workflow has a Phase-0-style scout that runs once at session start and a convergence gate that runs after each iteration** — these are the same role asked at different times ("given what we know now, is the spec complete?") and treating them as distinct agents creates artifacts (axis-name drift between the two, separate prompt maintenance, separate convergence vocabularies). Unifying them removes the duplication.

Third, **the spec graph was misdescribed**. CLAUDE.md and several DJs read `Goal → (Feature | Strategy) → Decision`, implying decisions are children of features/strategies. The intended graph is the linear chain `Goal → Decision → (Feature | Strategy) → Approach`. Reference direction (solid arrows below) is `Approach → Feature/Strategy → Decision → Goal` — each kind cites the kind upstream of it. Authoring/flow direction is the inverse. Axes that need decisions are surfaced from goals, features, AND strategies (dotted arrows below) — meaning new imported content and existing nodes can both contribute to the axis set the decisions phase resolves.

In Mermaid form:

    flowchart TD
        A[Approaches] --> F[Features] & S[Strategies] --> D[Decisions] --> G[Goals]
        G & F & S -.->|axes| D

Correcting the graph is what enables the workflow re-architecture, because today's inline-decisions pattern is what made the misdescription practical.

**Why this surfaced now.** Pre-DJ-122 the council had no convergence loop — single-pass elaborate→reconcile→ship absorbed inline decisions into the persisted spec without re-detection. DJ-122's gate exposed cross-strategy contradictions explicitly; DJ-123 tried to close them with in-flight search. The winplan re-run showed both that elaborators don't reliably engage tools when the prompt directs them to (Flash-tier specifically), and that the underlying coupling between decisions and narrative would defeat the search even if it did fire. The data forces a structural rather than tooling fix.

**Decision.** Re-architect the spec-generation workflow around four coupled changes:

1. **Decisions move out of the elaborator's output and become first-class outputs of their own phase.** `RawFeatureProposal.Decisions` and `RawStrategyProposal.Decisions` change type from `[]InlineDecisionProposal` to `[]string` (references to decision IDs). A new agent — the *decision-elaborator* — authors decisions one-per-axis in Phase 1. Feature/strategy elaborators (renamed *narrative-elaborators* in this DJ) author narrative in Phase 2 and reference settled decisions by ID. The `spec.Feature` and `spec.Strategy` persisted shapes gain a `Decisions []string` field with `minItems=1` — every feature/strategy must be anchored in at least one decision.

2. **The scout becomes the unified gap analyzer, convergence judge, and decision-mapper.** A single agent role runs every iteration. Its job: read goals + existing graph + any imported content; surface axes that need decisions; for each surfaced axis, check whether an existing decision covers it; emit only the uncovered ones as `axes_open[]` and emit any new feature/strategy node it identified with `decisions[]` pre-populated from existing decisions on covered axes. The standalone gate role goes away. Convergence is `axes_open == [] AND no critic findings`.

3. **Axes are scout-determined and assigned at decision-creation time.** Decisions gain `Axes []string` (the axis IDs the decision answers, plural to support intentional shared-axis cases like staff-vs-end-user-auth) and `SurfacedBy []string` (the goal / feature / strategy IDs that surfaced the axis, set by the scout's dispatch). Open axes stay ephemeral (no separate spec kind); decided axes ride on the decisions covering them. Cross-run continuity comes for free via set membership on `Decision.Axes`.

4. **`locutus import` becomes a thin entry point into the standard workflow.** Today's import has its own admission/triage flow; DJ-124 dissolves that into the unified loop. The imported content (PRD markdown, etc.) joins goals + existing graph as input to the scout. The scout surfaces any new axes the imported content implies, identifies the new feature/strategy node, maps covered axes to existing decisions, and emits the work-to-do. Phase 1 commits any new decisions; Phase 2 authors the new node with its full reference set. Same workflow handles refine and import.

The workflow becomes a single loop with one entry shape:

    (loop until scout.converged)
      scout(state, findings, imported_content, prior_scout_output) → {
        converged?,
        axes_open[],          // only uncovered axes (need Phase 1)
        new_nodes[]            // new features/strategies; decisions[] pre-populated from covered axes
      }
      if converged: exit
      Phase 1 (decision-elaborators, parallel, one per open axis):
        decision-elaborator(axis, surfacing-nodes, findings) →
          grounded research → pick → justify → tag with axis ID + surfaced-by
      controller: affected = features_referencing(changed_decisions) ∪ nodes_named_in(findings) ∪ new_nodes
      Phase 2 (narrative-elaborators, parallel, only for affected):
        narrative-elaborator(node, decisions-it-references, findings) → updated body
      Phase 3 (critics, parallel):
        critics(state) → findings[]

Six design commitments worth calling out:

1. **Open vs closed axis terminology is strict.** An axis is *closed* once at least one decision tagged with its ID exists in the graph. An axis is *open* only when no covering decision exists yet. The scout's `axes_open[]` surfaces only the uncovered set; closed axes are implicit via the decisions covering them. Convergence is literally "every axis the scout could identify is covered."

2. **Scout names axes; does not enumerate options.** Per-axis option research lives inside the decision-elaborator that owns the axis. Scout's job is domain pattern-matching with light grounded research for project shape ("what does a web-hosted electoral campaign app entail?"), surfacing axes like `auth-provider`, `tenancy`, `tech-stack`, `ci-cd-pipeline`, `dev-inner-loop`. The decision-elaborator then researches options for `auth-provider`, evaluates them, picks one, and emits a Decision tagged with that axis. Separating these keeps each role focused and avoids the chicken-and-egg of "scout researches options for axes it hasn't named yet."

3. **Decision-elaborators are grounded; cite chosen path AND rejected alternatives.** `spec.Alternative` gains `Citations []Citation` with `minItems=1`. `spec.Citation.Kind` enum extends with `web` for grounded-research evidence the decision-elaborator gathered itself. Every commitment carries its evidence chain — both why the chosen option was picked and why each alternative was rejected. The auditability claim of the spec graph extends to alternative-rejection reasoning.

4. **`minItems=1` on `Feature.Decisions` and `Strategy.Decisions` enforces the linear chain structurally.** The reference list is scout-determined — not mechanically every-foundational-decision. The scout analyzes which decisions a feature actually depends on (auth and DB for a behavioral feature; charting library and real-time strategy for a dashboard) and pre-populates the reference list. The integrity validator catches dangling references; the schema constraint catches features ungrounded in any decision. There is NO "defer architectural commitment" escape pattern from DJ-105 — if an axis can't be decided yet, it stays in `axes_open` across iterations and the loop doesn't converge; user intervention closes it (more context, goal change, explicit `locutus refine`).

5. **Tier pinning by role.** Scout: strong + grounded (axis identification needs domain breadth; grounded for novel domains). Decision-elaborator: strong + grounded (research + commitment). Narrative-elaborator: balanced + ungrounded (consumes settled decisions; mechanical writing). Critics: balanced + ungrounded (unchanged). Cost concentrates where commitments are made.

6. **Conditional Phase 2 dispatch.** Workflow controller computes `affected = features_referencing(changed_decisions) ∪ nodes_named_in(findings) ∪ new_nodes_from_scout` and dispatches narrative-elaborators only for the affected set. Most iterations touch only a handful of features/strategies, not the full N. This bounds per-iteration cost and prevents the mass-rewrite that was the iter-4 explosion source.

**The workflow's information flow, end-to-end.** Iter-1 (greenfield refine): scout reads GOALS.md, surfaces axes (frontend, hosting, auth, tenancy, tech-stack, CI/CD, dev-loop, ...). All axes are open. Phase 1 dispatches one decision-elaborator per axis; each researches options and commits with citations on chosen + rejected paths. Phase 2 dispatches narrative-elaborators for the features/strategies named in the outline. Critics run; produce findings. Iter-2: scout consumes prior axes_open + new findings + state; most axes stay decided; maybe one new axis surfaces from a critic finding. Phase 1 dispatches only the new axis's decision-elaborator. Phase 2 dispatches only affected nodes. Critics run again. Iter-3: scout sees no open axes and no findings → `converged: true`. Exit.

Brownfield refine collapses identically: iter-1 scout sees existing decisions, surfaces only axes not yet covered, Phase 1 + Phase 2 do incremental work.

Import collapses identically: imported PRD joins the scout's input set; scout identifies the new feature node from it, maps the feature's axes to existing decisions (most covered → references pre-populated), surfaces any genuinely new axes, Phase 1 commits new decisions for uncovered axes, Phase 2 authors the new feature with the full reference list.

**Alternatives considered.**

- **Pre-fanout coordinator (DJ-123's out-of-scope follow-up candidate).** A single strong-tier agent reads scout brief + outline, commits foundational decisions before Phase 2 narrative fanout. Cleaner than DJ-123's mid-fanout coordination, but still leaves the decisions/narrative coupling in place — Phase 2 elaborators authoring inline decisions can still re-pick on already-settled axes. DJ-124's structural separation (decisions are their own first-class output of their own phase) subsumes this approach and addresses the coupling at its source.
- **Sequential revise instead of parallel.** Solves within-iteration parallel-fanout contradictions by serializing. Pays in wall-clock. Doesn't solve the decision/narrative coupling — sequential elaborators still author both at once. Held in alternatives stack only as a fallback if Phase 9 validation surfaces within-Phase-1 coordination problems (parallel decision-elaborators on different axes committing to mutually-incompatible choices).
- **Persisted axes as a first-class spec kind.** Adds lifecycle complexity (proposed → answered → superseded → deprecated) with no consumer demanding it. Decided-axis identity comes for free via `Decision.Axes`. Held for a follow-up DJ if a future verb needs cross-session known-unknowns tracking.
- **Embedding-cosine search to fix DJ-123's tool-engagement problem.** The diagnosis was that the LLM doesn't call `spec_search` at all, not that the BM25 results were poor. Adding embedding similarity doesn't fix non-engagement. DJ-124 retires search-as-convergence-mechanism; embedding search remains a candidate optimization for `spec_search` more generally.
- **Detect cross-strategy contradictions in the reconciler.** Catches contradictions after the fact, doesn't prevent them. The decision-elaborator + narrative-elaborator separation prevents the contradiction from being authored in the first place; reconciler retains a smaller scope (cross-decision dedupe) which is mechanical.
- **Keep `locutus import` as a separate flow with its own admission logic.** Today's import has its own triage agent and its own admission semantics; preserving that meant maintaining two parallel workflows that did similar work. Unifying under the scout-driven loop is cleaner and means improvements to the workflow benefit both verbs uniformly.
- **Make `Feature.Decisions` optional (`minItems=0`).** Considered seriously in chat (2026-05-15). The argument for: features that don't surface new axes shouldn't be forced to cite transitive foundational decisions just to satisfy the schema. The argument against (which won): if the scout is the analyst that determines a feature's decision references, the reference list is scout-determined work, not transitive bookkeeping. `minItems=1` makes the linear chain a structural invariant rather than a convention prose maintains. Brownfield imports satisfy `minItems=1` trivially (the scout references existing decisions); greenfield imports satisfy it because the loop commits decisions before authoring features.

**Consequences.**

- **Code:**
  - `internal/spec/types.go` — `Decision.Axes []string` (`minItems=1`), `Decision.SurfacedBy []string`, `Alternative.Citations []Citation` (`minItems=1`), `Citation.Kind` enum extended with `web`, `Feature.Decisions []string` (`minItems=1`), `Strategy.Decisions []string` (`minItems=1`).
  - `internal/agent/raw_proposal.go` — `RawFeatureProposal.Decisions` and `RawStrategyProposal.Decisions` change type to `[]string`; `InlineDecisionProposal` struct removed; new `RawDecisionProposal` struct for the per-axis Phase 1 output.
  - `internal/agent/workflow_spec_generation.go` — `NewSpecGenerationWorkflow` rewritten around the new shape; `mergeElaborated{Features,Strategies}` and `mergeRevisedNodes` replaced by `mergeDecisions` + `mergeNarrative`.
  - `internal/agent/state.go` — `PlanningState` extended with `priorScoutOutput`, `imported`, `axesOpen` fields.
  - New agent: `internal/scaffold/agents/spec_decision_elaborator.md` (strong tier, grounded, per-axis commit-and-justify).
  - Rewrites: `internal/scaffold/agents/spec_scout.md` (gap analyzer + judge + decision-mapper role); `spec_strategy_elaborator.md` and `spec_feature_elaborator.md` (narrative-only).
  - `internal/scaffold/agents/spec_reconciler.md` and `spec_gate.md` — scope reduced (reconciler) or retired (gate).
  - `cmd/import.go` — thin entry that admits content into `state.Imported` and runs the standard workflow.
  - Test rewrites across `internal/agent/workflow_spec_generation_test.go`, `internal/scaffold/scaffold_test.go`, `internal/agent/raw_proposal_schema_test.go`, `cmd/import_test.go`. New tests cover scout's mapping-to-existing-decisions, conditional Phase 2 dispatch, unified import flow.

- **Documentation:**
  - CLAUDE.md spec-graph line corrected to `Goal → Decision → (Feature | Strategy) → Approach` with the explicit note that decisions inform features/strategies and axes are surfaced from goals + features + strategies.
  - DJ-068, DJ-094, DJ-105, DJ-122 audited for old graph-framing prose; corrected or cross-referenced.

- **User-visible:**
  - `locutus refine goals` exits in fewer iterations on greenfield projects; brownfield runs touch only the changed slice.
  - `locutus import` produces a feature with structurally meaningful decision references rather than inline decisions admitted to the graph.
  - Decisions in the persisted spec carry axis tags AND back-references to surfacing nodes, supporting future explain/justify verbs that walk the decision-axis-feature graph both directions.
  - Session traces show per-phase fanout structure, making workflow progress easier to read than today's monolithic council loop.

- **Performance:**
  - Per-iteration cost lower for incremental work (conditional Phase 2 dispatch); higher for first iteration of greenfield (scout + N decision-elaborators + M narrative-elaborators all fire). Net: greenfield converges in 1-3 iterations vs today's 5+ at budget; brownfield converges in 1-2.
  - Scout grounded research is bounded — most iterations carry forward prior scout output verbatim; fresh research fires only for genuinely new axes.
  - Decision-elaborator grounded research fires per-axis-being-decided, not per-iteration. Total grounded calls per session is roughly the number of distinct axes the project has, not iterations × axes.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. `RawFeatureProposal.Decisions` and `RawStrategyProposal.Decisions` change type; old council prompts on disk are overwritten by `update --reset`. Existing persisted decisions in `.borg/spec/decisions/` continue to load — their `Axes` and `SurfacedBy` fields stay empty until a future refine pass populates them via re-scouting. The integrity validator handles empty-Axes decisions as legacy without complaint. Existing persisted features/strategies with no `Decisions []string` field continue to load as legacy; new authoring goes through the schema-enforced shape.

**Reversal criteria.** Revert if:

- (a) the scout systematically misses critical axes — surfaces as critic findings repeatedly flagging "missing axis X" across iterations. Indicates the scout's domain understanding isn't strong enough; consider per-domain scout specialization or moving to per-axis grounded research at every iteration rather than amortized.
- (b) decision-elaborator grounded research produces unreliable citations (web kind citations that don't actually back the claim). Surfaces as critic findings or human review flagging hallucinated evidence. Mitigation: tighten the citation discipline in the prompt, mirroring `justify_researcher.md`'s literal-sentinel pattern.
- (c) conditional Phase 2 dispatch creates incorrect "affected" sets that miss real downstream effects — surfaces as features/strategies whose narrative becomes inconsistent with current decisions. Mitigation: expand the affected-set computation; worst case, fall back to dispatching all narrative-elaborators every iteration.
- (d) the scout-as-judge fails to converge in 5 iterations on a project that should converge — indicates the convergence criterion is wrong or scout is missing axes the critics keep surfacing. Reversal would re-introduce a separate convergence gate role.
- (e) the `minItems=1` constraint on `Feature.Decisions` / `Strategy.Decisions` creates legitimate authoring deadlocks (features that the scout determines have no decision dependency but the schema rejects). Mitigation: loosen the constraint to `minItems=0` and rely on the integrity validator + critic findings to surface ungrounded features instead.
- (f) `locutus import` unified through the workflow becomes too slow for the "single feature admission" UX — surfaces as users complaining that `locutus import dashboard.md` takes minutes when today's import takes seconds. Mitigation: short-circuit the scout when the imported content surfaces no new axes (skip Phase 1 entirely); only convergence-cost when decisions are actually needed.

**Reference.** Refines [DJ-068](#dj-068-manifeststate-separation--kubernetes-inspired-reconciliation-model) by clarifying the spec graph topology (decisions are upstream of features/strategies; the chain is linear). Replaces [DJ-105](#dj-105-elaborator-decisions-is-api-layer-required-not-prompt-layer-required) by changing the inline-decisions schema from `[]InlineDecisionProposal` to `[]string` references and retiring the "Defer architectural commitment" escape pattern. Re-scopes [DJ-123](#dj-123-in-flight-spec-search-for-council-agents-extends-dj-094--dj-116-to-mid-council-state) — the in-flight search infrastructure remains as defense-in-depth in Phase 2 narrative elaborators but is no longer the convergence mechanism. Builds on [DJ-122](#dj-122-graph-mutation-workflow-executor-with-spawner-nodes-supersedes-dj-112-on-control-flow-topology)'s graph-mutation executor; uses the spawner-node pattern for both Phase 1 (per-axis decision dispatch) and Phase 2 (per-affected-node narrative dispatch). Honors [DJ-085](#dj-085-decisions-denormalize-their-justification-session-transcripts-are-debug-only) (decisions denormalize their justification on the persisted node) and extends it to alternatives. Unifies the `locutus import` admission flow into the same workflow, retiring its separate triage logic. Motivated by the DJ-123 winplan re-run trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/).
```
