# DJ-133 — Decisions Identified by Axis, Not Chosen Option

> **Governing DJ:** [DJ-133](../../docs/DECISION_JOURNAL.md#dj-133). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** DONE — axis-shaped decision IDs (`dec-<axis-id>`) shipped 2026-05-23 as `internal/migrate/MigrateDecisionIDs` (idempotent across runs); the convention is foundational and carries through DJ-134 and DJ-135. CLAUDE.md's "Sources of Truth" section names it authoritatively. Empirically verified on winplan refine 2026-05-26 — every decision in the spec graph uses `dec-<axis>` form.
> **Predecessors:** [DJ-124](../../docs/DECISION_JOURNAL.md#dj-124) (axis-driven workflow; the scout surfaces axes; the elaborator commits decisions — under DJ-133 the two share a key namespace); [DJ-126](../../docs/DECISION_JOURNAL.md#dj-126) (replace-by-axis-ID match — DJ-133 collapses it to ID-based lookup); [DJ-128](../../docs/DECISION_JOURNAL.md#dj-128) (cap-as-commit lock-by-axis-id — simplified under DJ-133); [DJ-088](../../docs/DECISION_JOURNAL.md#dj-088) (cascade-rewrite machinery — the migration leans on it); commit [`eb51a14`](https://github.com/chetan/locutus/commit/eb51a14) (the three immediate fixes that mitigate the failure mode at the prompt and tool-error-message surface — DJ-133 closes the deeper structural cause).
> **Surface area:** schema example payloads + decision-elaborator prompt; workflow mergeDecisions simplification; one-shot on-disk migration; documentation across CLAUDE.md / council.md / agent-conventions.md.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Walk `docs/agent-conventions.md` as a checklist before editing the elaborator prompt — it's a file under `internal/scaffold/agents/`. Cite DJ-133 + the precise constraint in chat before touching spec-layer code (per `[[feedback-cite-djs-before-spec-work]]`).

## Why this plan exists

The seventh winplan re-run (`/Users/chetan/projects/winplan/.locutus/sessions/20260522/1250/10-877ef1/`) surfaced a tool-loop pathology where Gemini 3.5 Flash spiraled through 10 spec_get / spec_search rounds chasing decision IDs that didn't exist. Round-by-round reasoning shows the model confabulating plausibly-named decision slugs (`dec-supabase-storage-tus-railway-ingestion`, `dec-supabase-postgres-persistence`, `dec-transaction-scoped-rls-context`, etc.) and treating them as real.

The three immediate fixes in commit [`eb51a14`](https://github.com/chetan/locutus/commit/eb51a14) — manifest origin/working tags, spec_get inline-recovery, prompt corrections — mitigate the proximal failure mode by bounding the model's recovery surface and correcting the agent-prompt misinformation. They're necessary regardless of what we do next.

But the trace exposed a deeper architectural issue that the fixes don't address: **the decision ID's job is to be a stable handle for the architectural question the decision answers; the current chosen-option-name-in-the-ID convention makes the ID describe the answer instead**, so the ID drifts whenever the answer changes (Flip). This creates four concrete symptoms:

- Stale chosen-option in id after a Flip (`dec-nextauth-identity-and-rbac` points at a Clerk-now decision because the workflow preserves the prior id).
- A whole subsystem (`axisIntersectionMatches`, `ambiguous` integrity violation, per-axis state maps) exists only to compensate for the chosen-option-ID drift.
- Backreferences advertise stale technology in the slug — `feat-X.decisions: [dec-nextauth-...]` keeps pointing right, but the slug name is misleading.
- The id namespace is unbounded for the model — variant slugs like `dec-supabase-postgres-persistence` are plausible-shaped, so confabulation is structurally feasible.

DJ-133 makes decision IDs identical to their primary axis ID, prefixed `dec-`. The ID becomes **the question**; the body's `title` / `chosen_option` / `rationale` answer it. Flips change the body, not the ID. Backreferences stay durably accurate. The model's id namespace is bounded by the scout's known-axis set, which makes confabulation structurally infeasible.

## Reference state (before DJ-133 starts)

- **Decision-ID minting today** in [internal/scaffold/agents/spec_decision_elaborator.md](../../internal/scaffold/agents/spec_decision_elaborator.md) `### id` section says *"A stable slug derived from the chosen option, prefixed `dec-`, lowercase, hyphen-separated, three to five words (e.g. `dec-postgres-oltp-store`, `dec-aws-cognito-auth`, `dec-stm32h743-mcu`). The reconciler may suffix with `-2` / `-3` if collisions occur; you pick the natural slug."*
- **Schema example payloads** in [internal/agent/schemas.go](../../internal/agent/schemas.go) — `RawDecisionProposal` and `RawSpecProposal` register examples with IDs like `dec-postgres-oltp-store`. The example is what the model treats as canonical via the cacheable user message that travels into every call.
- **mergeDecisions's replace-by-axis-ID match** at [internal/agent/workflow_spec_generation_dj124.go:1096](../../internal/agent/workflow_spec_generation_dj124.go#L1096) walks `axisIntersectionMatches` to find prior decisions whose `axes[]` intersects the incoming axes. The `ambiguous` integrity violation at [recordAmbiguousRevisionConcern](../../internal/agent/workflow_spec_generation_dj124.go) (line ~1340) fires when an incoming axis set intersects more than one prior decision.
- **State maps keyed by axis or by decision-id**: [DecidedAxesByIter](../../internal/agent/state.go#L228) keyed by axis ID; [AxisRevisionCount](../../internal/agent/state.go#L238) keyed by axis ID; [LockedDecisionIDs](../../internal/agent/state.go#L260) keyed by decision ID. Under DJ-133 axis-id == decision-id by construction, so all three maps' keys are interchangeable.
- **Cascade-rewrite machinery** in [internal/cascade/supersede.go](../../internal/cascade/supersede.go) + [supersede_apply.go](../../internal/cascade/supersede_apply.go) handles the "decision id changed, rewrite all incoming refs" pattern under DJ-088. The DJ-133 migration leans on the same machinery.
- **The three-fix commit** [`eb51a14`](https://github.com/chetan/locutus/commit/eb51a14) added manifest `Origin` / `Working` fields, spec_get inline-recovery, and corrected prompt wording. DJ-133's `working` flag retains its meaning (body may change) but no longer needs the "ID may also change" caveat — IDs are stable across Flips by construction post-DJ-133.

## Resolved design questions

Recorded in chat 2026-05-22, locked in DJ-133's text:

1. **Defer DJ-133 behind the three immediate fixes.** The fixes mitigate the hallucination failure mode at the prompt + tool-error-message surface; DJ-133 closes the deeper structural cause. Sequenced (not bundled) so empirical validation of the fixes is possible before committing to the bigger migration.

2. **Decisions don't carry chosen-option in the ID.** Title, chosen_option, and rationale are the human-readable answer surfaces. The ID is the durable handle.

3. **Composite axes use primary-axis-as-ID, not joined slugs.** Composite is rare; the secondary axes already live in `axes[]`. Avoids parsing ambiguity from join syntax (`dec-compute-platform--aws-region`).

4. **Existing on-disk decisions migrate as part of the landing.** One-shot migration walks `.borg/spec/decisions/`, renames each `dec-<chosen>` to `dec-<axes[0]>`, and rewrites all incoming refs via the cascade-rewrite machinery. Idempotent.

5. **The elaborator stops minting IDs.** Prompt rewrites the `### id` section to "Copy the axis ID verbatim, prefixed `dec-`" — no slug-from-chosen derivation. Example payloads update to match.

6. **`replace-by-axis-ID` collapses to `replace-by-ID`.** `axisIntersectionMatches` retires; the merge's match becomes a hash lookup. The `ambiguous` integrity violation retires too (axis-set overlap becomes ID-set overlap, which the existing duplicate-ID check already handles).

7. **`working` flag keeps its meaning but the ID-stability hedge goes away.** Post-DJ-133 the ID is stable across Flips by construction, so `working: true` cleanly means "the body is being rewritten right now" — no need for the "and possibly the ID" caveat the three-fix shape carried.

## Phase 1 — Migration tool

**Goal:** add a one-shot migration that renames every persisted `dec-<chosen-option>` to `dec-<primary-axis>` and rewrites all incoming refs in features / strategies / bugs / approaches. Idempotent (already-axis-shaped IDs are no-ops). Logged as a DJ-103 history event so `locutus history` surfaces the migration.

**Why Phase 1, not Phase 2 or later:** the migration is the activation point for the new convention. Until on-disk decisions are axis-shaped, the elaborator's axis-as-ID outputs get overwritten with chosen-option IDs by mergeDecisions's prior-ID-preservation path. Migration first, then prompt + workflow changes layer on top.

**Files expected to change:**

- [internal/migrate/](../../internal/migrate/) (new package, or `internal/cascade/` extension) — the migration helper. Walks `.borg/spec/decisions/`, reads each decision's `axes[]`, computes the new ID (`dec-<axes[0]>`), uses cascade-rewrite machinery to rename file + update all incoming refs in features/strategies/bugs/approaches.
- [cmd/update.go](../../cmd/update.go) — wires the migration into `locutus update` so it runs once per project on the first update after DJ-133 lands. Per `[[feedback-no-back-compat-until-self-hosting]]` the user runs `locutus update --offline --reset` before every operation, so this is the natural injection point.
- [internal/history/](../../internal/history/) — defines a `decision_id_migration` event kind so the rename + ref-rewrite per decision lands in the durable history. One event per decision migrated; payload records old id → new id + the affected refs.

**Edge cases the migration must handle:**

- **Empty `axes[]`** (legacy pre-DJ-124 decision): log a warning, leave the decision alone. The user can hand-fix or accept the stale id.
- **Composite axes** (`axes: [a, b, c]`): use `axes[0]` as primary. Documented in the operator-visible warning when the migration runs against composite decisions.
- **ID-collision after migration** (two decisions whose primary axes map to the same target id): surface as a hard error before any rename. The operator hand-resolves before retrying. Should be very rare; would indicate a pre-DJ-133 graph that's already in an inconsistent state.
- **Already-axis-shaped IDs** (the migration ran in a prior session, or the project was greenfielded post-DJ-133): no-op per decision.

**Tests:**

- `TestDecisionIDMigration_RenamesByPrimaryAxis` — `dec-supabase-postgresql` with `axes=["database-and-spatial-storage"]` → renamed to `dec-database-and-spatial-storage`. File is moved; sidecar (`.md` if any) follows; JSON contents have the new id.
- `TestDecisionIDMigration_RewritesIncomingRefs` — features and strategies referencing the old id are rewritten to the new id. Driven through the cascade-rewrite machinery for parity with DJ-088's id-rewrite pattern.
- `TestDecisionIDMigration_Idempotent` — second run produces the same final state; no spurious history events.
- `TestDecisionIDMigration_HandlesCompositeAxes` — `dec-fargate-compute-platform` with `axes=["compute-platform", "aws-region"]` → renamed to `dec-compute-platform`; warning logged naming the secondary axes.
- `TestDecisionIDMigration_SkipsEmptyAxes` — decision with `axes=[]` left untouched; warning logged.
- `TestDecisionIDMigration_DetectsConflicts` — two decisions whose primary axes both map to `dec-compute-platform` → migration aborts before any rename; error message names the conflicting decisions.
- `TestDecisionIDMigration_WritesHistoryEvent` — each successful rename produces a `decision_id_migration` event under `.borg/history/`.

**Verification:** `go build ./... && go vet ./... && go test ./internal/migrate/... ./internal/cascade/... -count=1 -race`.

**Estimated:** 5-8 hours.

## Phase 2 — Schema example payloads + decision-elaborator prompt

**Goal:** flip the elaborator's mental model from "derive a slug from the chosen option" to "copy the axis ID verbatim." Schema example payloads (visible to the model on every call via the cacheable user message) update to use axis-shaped IDs.

**Files expected to change:**

- [internal/agent/schemas.go](../../internal/agent/schemas.go) — `RawDecisionProposal`, `RawSpecProposal`, `SpecProposal`, `DecisionProposal` example payloads update. The canonical example axis-as-ID is `dec-database-and-spatial-storage` (matches what the iter-1 winplan trace produced for that axis); references to `dec-postgres-oltp-store` change to `dec-oltp-store` or similar axis-shape. Walk every RegisterSchema example payload that contains a `dec-*` string — there are a few across feature / strategy / decision shapes.
- [internal/scaffold/agents/spec_decision_elaborator.md](../../internal/scaffold/agents/spec_decision_elaborator.md):
  - `### id` section: rewrites to *"Copy the axis ID verbatim, prefixed `dec-`. The axis ID comes from the `OpenAxis.ID` field in the input; you mint nothing. Example: if the input axis is `database-and-spatial-storage`, the decision id is `dec-database-and-spatial-storage`."*
  - Composite-axis paragraph: *"For genuinely composite axes (rare — e.g. choosing AWS ECS Fargate as both compute-platform AND aws-region), use the primary axis as the id slug; list all axes in the `axes[]` field."*
  - Drop the "natural slug" + "reconciler may suffix with -2 / -3" language entirely. There's no slug to suffix; collisions are integrity violations the workflow catches.
- [internal/scaffold/agents/spec_scout.md](../../internal/scaffold/agents/spec_scout.md) — minor: if the scout's gap-analysis prose mentions "the decision-ID the elaborator will mint for this axis," update to "the decision id will be `dec-<axis-id>`, derived mechanically from the axis ID the scout surfaces."

**Walk the agent-conventions checklist:** per `[[feedback-agent-conventions-checklist-first]]`, before submitting the elaborator-prompt change, walk `docs/agent-conventions.md`'s six anti-patterns + four positive patterns as a checklist. The prompt change is short and structural so the checklist is quick, but it's mandatory before shipping.

**Tests:**

- `TestRawDecisionProposalExampleUsesAxisAsID` — `SchemaExample("RawDecisionProposal")`'s payload has `ID` matching its `Axes[0]` with the `dec-` prefix.
- `TestRawSpecProposalExampleUsesAxisAsID` — same check for the parent shape's nested decision examples.
- `TestDecisionElaboratorPromptDescribesAxisAsID` (scaffold-level) — agent .md contains "Copy the axis ID verbatim" and the canonical axis-as-ID example.
- `TestDecisionElaboratorPromptDropsSlugMinting` — the stale "natural slug" / "derived from the chosen option" / "suffix with -2 / -3" wording is gone.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... ./internal/scaffold/... -count=1 -race`.

**Estimated:** 2-3 hours.

## Phase 3 — Workflow simplification

**Goal:** collapse `axisIntersectionMatches` and the `ambiguous` integrity violation in `mergeDecisions`. Since axis-id == decision-id post-Phase-1, the match becomes a hash lookup on incoming.id against existing-decision IDs.

**Files expected to change:**

- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go) — `mergeDecisions` simplifies:
  - The "axis-intersection" match becomes an ID-based hash lookup: incoming.ID against the in-flight RawProposal.Decisions[] and the existing snapshot's decisions[].
  - `axisIntersectionMatches` helper retires.
  - `recordAmbiguousRevisionConcern` retires — under axis-as-ID, two incoming decisions with overlapping axes produce two incoming decisions with the same ID, which the existing duplicate-ID check already surfaces as an integrity violation.
- State map cleanup (lighter touch):
  - `DecidedAxesByIter`, `AxisRevisionCount`, `LockedDecisionIDs` all keep their interfaces; their keys are now equivalent to decision IDs but for forensic clarity the maps stay separate. Document the equivalence in each field's doc comment.
  - Consider whether `axisIDsExceedingRevisionCap` and `incrementAxisRevisionCounts` need rename — probably yes, to `decisionIDsExceedingRevisionCap` / `incrementDecisionRevisionCounts` for clarity. Code-mechanical rename.

**Mandate ordering with Phase 1:** Phase 3 depends on the migration having run on the operator's machine. Workflow simplification without prior migration would break legacy graphs (a `dec-supabase-postgresql` decision with `axes=["database-and-spatial-storage"]` wouldn't match an incoming `dec-database-and-spatial-storage` revision because they have different IDs). The migration is what restores the invariant id == axis-id; the workflow simplification then exploits the invariant.

In practice: Phase 1 ships first, runs on the user's machine via `locutus update`, then Phase 2 + 3 ship together (the elaborator emits axis-shaped IDs which match the migrated on-disk IDs).

**Tests:**

- `TestMergeDecisions_MatchesPriorByID` — new test for the simpler hash-lookup match. Replaces the existing `TestMergeDecisions_ReplacesWhenAxesIntersectExactlyOne`.
- `TestMergeDecisions_DuplicateIDIsIntegrityViolation` — incoming decision with id matching an existing prior produces a revision (replace-by-ID), not a duplicate; the prior shape (axis-intersection-with-N-priors → ambiguous) is impossible by construction.
- `TestAxisIntersectionMatchesRetired` (negative existence): grep guard that the function is not present. Optional; the build will fail without it if any caller remains.
- Existing tests update for the simpler match path. `TestMergeDecisionsRecordsAmbiguityWhenMultipleExistingMatch` retires (path no longer reachable post-DJ-133).

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 4-6 hours.

## Phase 4 — Documentation

**Goal:** CLAUDE.md / council.md / agent-conventions.md / decision-journal back-references all reflect the axis-as-ID convention.

**Files expected to change:**

- [CLAUDE.md](../../CLAUDE.md) — the existing "Project" paragraph mentions the spec graph; add a sentence under Sources of Truth noting decisions are identified by their primary axis (per DJ-133), with `title` / `chosen_option` carrying the human-readable answer.
- [docs/council.md](../../docs/council.md) — the per-agent reference for `spec_decision_elaborator` updates: the "Authors decisions per axis" entry's description of ID derivation switches to "copy axis ID verbatim, prefix `dec-`." Per `[[feedback-council-doc-maintenance]]` this is mandatory whenever a council-touching DJ ships.
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — add a new section "Stable identifiers vs. current content" describing the pattern: IDs name the question, content fields name the answer. The convention is general (applies to features and strategies too, where the id stays stable across title/body revisions); DJ-133 makes it explicit for decisions.

**Verification:** `go test ./... -count=1 -race` clean; `go vet ./...` clean. Manually verify Mermaid diagrams in council.md still render (no shape change expected; this is text-only updates to the per-agent reference block).

**Estimated:** 1-2 hours.

## Phase 5 — Empirical validation against winplan re-run

**Goal:** run a winplan refine after Phases 1-4 land. Verify the new IDs produce on-disk + the deeper hallucination failure mode is genuinely closed (not just mitigated by the eb51a14 three-fix surface change).

**Process:**

1. Build the DJ-133 binary: `go build -o ~/go/bin/locutus-dj133 .`.
2. Run `locutus-dj133 update --offline --reset` against winplan. The migration runs as a side effect of this command — verify the operator-visible log lists every decision renamed + the per-decision history events were written.
3. Inspect `.borg/spec/decisions/*.json` — every decision's id should be `dec-<axis-id>`, matching its `axes[0]` verbatim.
4. Inspect `.borg/spec/features/*.json` and `.borg/spec/strategies/*.json` — every `decisions[]` slice's entries should be axis-shaped.
5. Run `locutus-dj133 refine goals` with `LOCUTUS_SPEC_GEN_MAX_ITERATIONS=10`.
6. Measure:
    - **No tool-loop spirals.** Inspect every step folder's `01-reason.yaml`; no step should hit 10 rounds of `spec_get` / `spec_search`. The hallucination failure mode is closed iff the model can't confabulate axis-shaped IDs that don't exist in the scout's surfaced set.
    - **mergeDecisions success on revise dispatches.** Revise dispatches commit to the right prior decision by id-match (not axis-intersection-match). The trace's `decision_revised` history events should show id == axes[0].
    - **Backreferences stay stable.** When a Flip happens (chosen option changes), the decision id stays the same; the citation from features/strategies doesn't drift.

**What success looks like:** zero tool-loop-exhausted errors across the run; every decision id matches its axes[0]; backreferences stay byte-stable across Flips. Net wall-clock per refine drops further than the eb51a14 baseline because the workflow's simpler match path runs faster (sub-millisecond per axis vs O(N×M) intersection).

**What partial success looks like:** tool-loop errors drop to ~zero but a residual Gemini 3.5 Flash failure mode shows up in a different shape — e.g., the model invents a scout-shaped axis name that's adjacent to a real one (`dec-database-storage` instead of `dec-database-and-spatial-storage`). Reversal criterion (c) tracks this: the namespace bounding may not defeat all confabulation, just the unbounded variant. Mitigation: extend spec_get's inline recovery to use Levenshtein-distance on the requested id against the available set so suggestions explicitly point at the right axis name.

**What failure looks like:** the migration mangles backreferences (a feat-X cited a decision whose id changed but the migration missed the rewrite); the model still spirals on confabulated axis-names because the scout surfaces too few axes for the model to feel constrained; the composite-axis convention proves operationally awkward in practice. Reversal criteria (a), (c), (b) in DJ-133's text track these.

**Verification:** the winplan session traces are durable evidence. No automated assertion.

**Estimated:** 1-2 hours of compute + manual review.

## Phase 6 — DJ-133 status flip + plan marked DONE

**Goal:** DJ-133 flips from `proposed` to `shipping` once Phase 5 validation passes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-133 status `proposed` → `shipping (Phases 1-5 landed YYYY-MM-DD)`.
- This plan file marked DONE.

**Verification:** `go test ./... -count=1 -race` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 13-21 hours single-stranded across 3-4 sessions

(plus Phase 5 empirical compute time)

## Pointers a fresh session should follow before resuming

1. Read DJ-133 in full ([docs/DECISION_JOURNAL.md#dj-133](../../docs/DECISION_JOURNAL.md#dj-133)). It's the authoritative design; this plan is progress tracking.
2. Read commit [`eb51a14`](https://github.com/chetan/locutus/commit/eb51a14) (the three immediate fixes DJ-133 builds on). Without those fixes, DJ-133's prompt-side gains would be partially offset by the model still mis-believing spec_get can't see in-flight decisions; with them, DJ-133 closes the structural cause cleanly.
3. Read DJ-126 ([docs/DECISION_JOURNAL.md#dj-126](../../docs/DECISION_JOURNAL.md#dj-126)) for the replace-by-axis-ID workflow DJ-133 collapses.
4. Read DJ-088 ([docs/DECISION_JOURNAL.md#dj-088](../../docs/DECISION_JOURNAL.md#dj-088)) for the cascade-rewrite machinery the migration leans on.
5. Walk `docs/agent-conventions.md` checklist before editing `spec_decision_elaborator.md`. The user memory mandates this walk for any edit under `internal/scaffold/agents/`.
6. Read the seventh winplan re-run trace at `~/projects/winplan/.locutus/sessions/20260522/1250/10-877ef1/` end-to-end before Phase 5. The id-confabulation failure mode at step 0027 (the revise dispatch hitting the tool-loop cap) is the load-bearing motivation; the trace makes it concrete.

## What is explicitly out of scope

- **Renaming feature / strategy / bug / approach IDs.** Only decisions get the axis-as-ID treatment. Features and strategies already have stable conventions (slug-from-title-or-summary); their IDs don't drift on revision because they don't have a "chosen option" that changes Flip-by-Flip.
- **Permanent ID aliases.** The migration is the one-shot rename + cascade-rewrite; there's no permanent "this old id redirects to this new id" surface. Post-migration the old ids don't exist.
- **Updating prior DJ text that references chosen-option-style IDs.** Historical DJs are the durable design record; rewriting them retroactively would corrupt that record. References to `dec-postgres-oltp-store` in DJ-128 / DJ-129 / etc. stay as historical record; new DJs (134+) use axis-as-ID examples.
- **Splitting the per-axis state maps into one unified map.** `DecidedAxesByIter`, `AxisRevisionCount`, `LockedDecisionIDs` keep their separate identities for forensic clarity even though their keys are now equivalent. A future cleanup could merge them; not part of DJ-133.
- **Adding a Levenshtein-distance suggestion layer to spec_get's inline-recovery error.** The eb51a14 fix uses raw kind-matched id list; DJ-133's namespace bounding makes confabulation structurally infeasible so the simple list should suffice. If empirical evidence shows the model still mis-types axis-shaped IDs (`dec-database-storage` instead of `dec-database-and-spatial-storage`), the suggestion layer becomes a follow-up — not in DJ-133's scope.
