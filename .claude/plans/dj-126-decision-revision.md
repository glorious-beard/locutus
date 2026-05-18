# DJ-126 — Decision Re-Elaboration for Cross-Decision Contradictions

> **Governing DJ:** [DJ-126](../../docs/DECISION_JOURNAL.md#dj-126-decision-re-elaboration-for-cross-decision-contradictions-extends-dj-124-with-existing-decision-revision-depends-on-dj-125-concern-model). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** designed; implementation not started.
> **Prerequisite:** [DJ-125](../../docs/DECISION_JOURNAL.md#dj-125) must land first — DJ-126's dispatch is keyed on `Concern.RelatedDecisionIDs`, which DJ-125 introduces.
> **Surface area:** new `revise-decisions` step in the iteration template + dispatch closure + decision-elaborator revise-mode prompt + `mergeDecisions` replace-by-axis-ID logic + per-axis revision-count cap + DJ-103 `decision_revised` event.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Each phase verifies independently before moving on. **Prompt edits walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end before drafting** per [[feedback-agent-conventions-checklist-first]].

## Why this plan exists

DJ-124 dispatches four kinds of work (scout, decision-elaborator-first-author, narrative-elaborator, critics). It does not dispatch a fifth kind the second winplan re-run made indispensable: re-elaborating an existing decision when the critics flag a contradiction with another decision, a factual error in its rationale, or a hallucinated citation.

The iter-3 critic output of the second winplan run produced 6 substantive findings. Five of them required revising existing decisions:

1. `dec-datadog-observability-stack` adopts Datadog; `dec-aws-cloudwatch-logging` rejects Datadog as too expensive — **cross-decision contradiction**.
2. `dec-aurora-serverless-database-vendor` claims Aurora can scale to 0 ACU; actual minimum is 0.5 ACU — **factual error**.
3. `dec-150-dollar-off-cycle-ceiling` and `dec-tiered-seasonal-slo` cite GOALS.md excerpts that don't exist — **hallucinated citations**.
4. `dec-aws-ecs-fargate-deployment` (no EC2 management) contradicts `dec-fck-nat-egress` (commits to t4g.nano ARM EC2 instances) — **cross-decision inconsistency**.
5. Sum of committed baseline costs (Aurora storage + min ACU + NAT + Datadog) exceeds `dec-150-dollar-off-cycle-ceiling` — **financial incoherence**.

Only finding #6 (missing axis: shared schema location) had a closure path in the current workflow — scout surfaces it; decision-elaborator commits. The other five accumulate in `state.Concerns` and never resolve because the workflow has no decision-revision dispatch. The scout's only available move is to surface synthetic workaround axes ("observability-tool-coherence", "cost-runaway-protections", "peak-cost-ceiling") hoping new decisions paper over the contradictions; the original contradictory decisions stay in the graph.

DJ-125 closes the projection and concern-tracking gaps obscuring this issue. With DJ-125 in place, the residual failure is the missing decision-revision path. DJ-126 adds it.

## Reference state (assuming DJ-125 has landed)

- **`Concern`** carries `IterationRaised`, `Status`, `RelatedDecisionIDs`, `RelatedAxisIDs` (DJ-125).
- **`InFlightManifest`** is the projection surface for all agents (DJ-125).
- **Convergence rule** is `axes_open == [] AND no concerns with Status == open` (DJ-125).
- **Loop template** in [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go) currently runs `scout → decisions → narrative → reconcile → critique → scout`. DJ-126 adds a `revise-decisions` step between `narrative` and `critique`.
- **`spec_decision_elaborator`** ([internal/scaffold/agents/spec_decision_elaborator.md](../../internal/scaffold/agents/spec_decision_elaborator.md)) authors first-author decisions. The agent's prompt receives one `OpenAxis` and emits one `RawDecisionProposal`. DJ-126 extends the prompt with a revise mode.
- **`mergeDecisions`** ([internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go)) appends each `RawDecisionProposal` to `state.RawProposal.Decisions`. DJ-126 changes the append to a replace-by-axis-ID when the new decision's axes intersect with an existing decision's axes.
- **Cycle detection** in `scoutSpawnFor` uses `state.DecidedAxesByIter` to flag scout-side re-opens. DJ-126 leaves this in place but adds a per-axis revision-count cap for the revise-side path.

## Resolved design questions

Recorded in chat 2026-05-18; settled before this plan went to implementation.

1. **Single agent, two modes.** `spec_decision_elaborator` handles both first-author and revise modes. The mode switch is at the projection layer (per-fanout-item prompt content), not the agent's frontmatter or schema. Same `RawDecisionProposal` output schema for both.

2. **Replace-by-axis-ID.** When `mergeDecisions` sees a `RawDecisionProposal` whose `Axes` intersect with an existing decision's `Axes`, it REPLACES rather than appending. The existing decision's ID is preserved (so feature/strategy references don't break); body, rationale, alternatives, citations are overwritten by the revision.

3. **Revision-count cap per axis.** Default 3. Env var override `LOCUTUS_DECISION_REVISION_CAP`. Prevents the "revise dec-X → critic flags revised dec-X → revise again → ..." infinite-loop failure mode. When the cap fires, force-terminate with a `convergence_revision_capped` DJ-103 event naming the axes that exceeded the cap.

4. **Critic-driven dispatch, not scout-driven.** The decision-revision step's fanout closure walks `state.Concerns` looking for `Status == open` AND `len(RelatedDecisionIDs) > 0`. The scout doesn't surface revisions; the workflow controller dispatches them as a function of the critics' findings (via DJ-125's enriched `Concern`).

5. **Cycle detection separates scout-side and revise-side.** The existing `DecidedAxesByIter` flags scout-side cycles (scout re-emits a settled axis in `axes_open`). The new per-axis revision-count cap flags revise-side cycles. Two different failure modes; two different detection mechanisms.

6. **DJ-103 history events record each revision.** `decision_revised` event kind, carrying prior body + revised body + the driving concern. `locutus history` and the future `locutus explain` walk these events to show decision lineage.

7. **Revision dispatch fires AFTER narrative.** Order: `scout → decisions(first-author) → narrative → revise-decisions → critique`. Reasoning: the narrative step's output may resolve a concern by giving the feature better context (e.g., a feature body that explains the tradeoff makes a critic's "financial incoherence" finding `wontfix`-able). Running revisions after narrative gives the scout's grading pass (in the next iteration's scout call) a chance to mark those concerns `addressed` without needing a revision.

## Phase 1 — Decision-elaborator revise-mode prompt

**Goal:** extend `spec_decision_elaborator.md` with a "Revise mode" section that activates when the projection includes a "Prior decision" + "Critic finding to address" block. Same output schema; same `RawDecisionProposal` shape.

**Files expected to change:**

- [internal/scaffold/agents/spec_decision_elaborator.md](../../internal/scaffold/agents/spec_decision_elaborator.md):
  - New section "Revise mode" placed after the existing per-field task body.
  - Activates when the user message includes a "Prior decision" block (full body of the existing decision) and a "Critic finding to address" block (the concern text + severity).
  - Required behaviors:
    - Preserve `axes` verbatim from the prior decision. (The axis IDs are the dispatch key; changing them breaks the replace-by-axis-ID match.)
    - Acknowledge in the rationale what the prior decision committed AND why it's wrong (per the critic finding). The revision must reason about the prior commitment, not emit a fresh take ignoring the prior.
    - If grounded research disagrees with the prior decision's factual claim, follow the literal-sentinel pattern from `justify_researcher.md` — explicit sentinel excerpts for search-failure modes; no fabricated facts.
    - If the critic finding is "contradicts dec-Y" and dec-Y is also being revised this iteration (both flagged), the elaborator commits to a choice that's coherent with the scout's manifest (which shows the in-flight state of all decisions). Cross-reference via `spec_get(dec-Y)` if needed.

**Process discipline:** walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end before drafting. The revise-mode section is high-stakes; the failure mode "revision contradicts the prior in a new way" is exactly the cycle-detection cap is meant to catch, but the prompt should make it unlikely in the first place.

**Tests:**

- `TestDecisionElaboratorReviseModeSectionPresent` — scaffolded prompt contains "Revise mode" and the "Prior decision" / "Critic finding" handlers.
- `TestDecisionElaboratorPromptPreservesAxesInRevise` — prompt mandates axis-ID preservation in revise mode.

**Verification:** `go test ./internal/scaffold/... -count=1 -race`.

**Estimated:** 2-3 hours.

## Phase 2 — `revise-decisions` workflow step + fanout closure + projection

**Goal:** add the new step to the iteration template. The step's `Fanout` closure walks `state.Concerns` for revisable concerns; the projection renders the manifest + prior decision + critic finding for each.

**Files expected to change:**

- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - `convergenceLoopTemplate` adds a `revise-decisions` step between `narrative` and `critique`:
    ```go
    {
        ID:          "revise-decisions",
        Agents:      []string{"spec_decision_elaborator"},
        Parallel:    true,
        Conditional: hasReviseableConcerns,
        DependsOn:   []string{"narrative"},
        Fanout:      fanoutReviseableConcerns,
        Project:     projectReviseDecision,
        Merge:       mergeDecisions, // same merge function; replace-by-axis-ID logic added in Phase 3
    },
    ```
  - New helpers:
    - `hasReviseableConcerns(state *PlanningState) bool` — true when `state.Concerns` has any entry with `Status == open` AND `len(RelatedDecisionIDs) > 0`.
    - `fanoutReviseableConcerns(state *PlanningState) ([]string, error)` — emits one fanout item per qualifying concern. Each item carries the concern + the prior `RawDecisionProposal` for each related decision + the manifest slice the projection needs.
    - `projectReviseDecision(snap StateSnapshot[PlanningState]) []Message` — renders manifest + prior decision (full body) + critic finding (text + severity) + a "Revise mode" header.

**Tests:**

- `TestHasReviseableConcernsRequiresOpenAndRelatedIDs` — assert the conditional fires only when both criteria hold.
- `TestFanoutReviseableConcernsEmitsOnePerConcern` — fixture with 3 reviseable concerns; assert 3 fanout items.
- `TestProjectReviseDecisionIncludesPriorDecisionFullBody` — projection contains the prior decision's body, not just a summary.
- `TestReviseDecisionsStepConditionalSkipsWhenNoOpenConcerns` — when all concerns are `stale`/`addressed`/`wontfix`, the step skips cleanly.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 3 — `mergeDecisions` replace-by-axis-ID

**Goal:** extend `mergeDecisions` to detect "this is a revision of an existing decision" via axis intersection and replace the existing entry in-place.

**Files expected to change:**

- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - `mergeDecisions` rewritten: for each incoming `RawDecisionProposal`:
    1. Compute the intersection of incoming `Axes` with each existing decision's `Axes` (both in `state.RawProposal.Decisions` and `state.Existing.Decisions`).
    2. If an intersection exists with exactly one existing decision: REPLACE that decision (preserve ID; overwrite body, rationale, alternatives, citations).
    3. If an intersection exists with multiple existing decisions: this is ambiguous; record an integrity-violation concern naming the ambiguity and skip the replacement. The scout/critics surface it next iteration.
    4. If no intersection: append as today (first-author dispatch path).
  - Mark the concern that drove the revision as `Status == addressed` once the replacement lands. The driving concern ID is carried on the `RawDecisionProposal`'s fanout-item metadata (or recorded separately on `state.RawProposal` and matched up).

**Design decision to lock in during implementation:** how to carry the driving concern ID from `fanoutReviseableConcerns` to `mergeDecisions`. Two options:
- (a) Embed it in the fanout-item JSON; the projection sees it but the agent's `RawDecisionProposal` schema doesn't change (the agent doesn't see the concern ID — it sees the concern's text). Merge function reads the fanout-item metadata from `RoundResult.Source` (or a similar metadata channel).
- (b) Add a `DrivingConcernID` field to `RawDecisionProposal` schema and require the agent to echo it back. Loses information (the agent could lie or get it wrong); rejected.

Pick (a) at implementation time.

**Tests:**

- `TestMergeDecisionsReplacesWhenAxesIntersectExactlyOne` — fixture: existing `dec-X` with axes=[a,b]; incoming proposal with axes=[a]. Assert `dec-X` is replaced (ID preserved, body overwritten).
- `TestMergeDecisionsAppendsWhenNoAxisIntersection` — fixture: incoming proposal with brand-new axis. Assert append.
- `TestMergeDecisionsRecordsAmbiguityWhenMultipleExistingMatch` — fixture: two existing decisions both have axis=[a]; incoming proposal with axes=[a]. Assert no replacement, integrity-violation concern recorded.
- `TestMergeDecisionsMarksDrivingConcernAddressed` — fixture: revise-mode dispatch driven by concern C; after merge, concern C's `Status == addressed`.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 4 — Per-axis revision-count cap

**Goal:** prevent revise-loop infinite recursion. Track per-axis revision count across iterations; force-terminate when an axis hits the cap.

**Files expected to change:**

- [internal/agent/state.go](../../internal/agent/state.go):
  - `PlanningState.AxisRevisionCount map[string]int` — keyed by axis ID; incremented each time `mergeDecisions` replaces a decision with that axis. Deep-copied in `snapshotPlanningState`.
- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - `mergeDecisions` increments `AxisRevisionCount[axisID]` for every axis in a replaced decision.
  - New helper `axesExceedingRevisionCap(state) []string` — returns axis IDs above the cap.
  - `scoutSpawnFor` (or a new revise-side check that fires after `mergeDecisions`) calls `axesExceedingRevisionCap`. If non-empty, spawn a `convergence_revision_capped` terminal step (analogous to `convergence_stuck_terminal` from the existing cycle-detection path).
- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go):
  - New `convergenceRevisionCappedTerminal(historian, state, cappedAxes)` builder, analogous to `convergenceStuckTerminal`. Writes a DJ-103 event with kind `convergence_revision_capped` naming the capped axes.
- Environment variable `LOCUTUS_DECISION_REVISION_CAP` (default 3). Read by `readDecisionRevisionCap()` analogous to `readSpecGateBudget`.

**Tests:**

- `TestAxisRevisionCountIncrementsOnReplace` — fixture: revise dec-X twice; assert count==2.
- `TestRevisionCapTerminatesWhenExceeded` — fixture: cap=2; revise dec-X 3 times. Assert workflow force-terminates with `convergence_revision_capped` event.
- `TestRevisionCapPerAxisIndependent` — revising dec-X 3 times and dec-Y once doesn't terminate when cap=3; counts are per-axis.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 2-3 hours.

## Phase 5 — DJ-103 `decision_revised` history events

**Goal:** record each revision as a structured event so `locutus history` can show decision lineage and the future `locutus explain` can walk concern→decision references.

**Files expected to change:**

- [internal/history/types.go](../../internal/history/types.go) (or wherever DJ-103 event kinds live):
  - New event kind `decision_revised`. Schema: `target_id` = the decision ID, `old_value` = prior `DecisionProposal` JSON, `new_value` = revised `DecisionProposal` JSON, `rationale` = the driving concern text + severity + iteration.
- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - `mergeDecisions` calls `historian.Record(decisionRevisedEvent(...))` after each replacement.
  - Historian is nil-safe (mirrors existing convergence_failed handling).
- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go):
  - New `decisionRevisedEvent(prior, revised *DecisionProposal, concern *Concern, iter int) history.Event` builder.
- `locutus history` consumes the new event kind transparently (assuming it lists all kinds; verify and update if hand-listed).

**Tests:**

- `TestDecisionRevisedEventRecordedOnReplace` — fixture with historian; after replace, the event store has a `decision_revised` event with the right target_id and bodies.
- `TestDecisionRevisedEventIncludesDrivingConcern` — the event's `rationale` field contains the concern text.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... ./internal/history/... -count=1 -race`.

**Estimated:** 2 hours.

## Phase 6 — End-to-end loop convergence test

**Goal:** a `MockExecutor`-driven test that forces a cross-decision contradiction and verifies the loop converges via the revise path.

**Files expected to change:**

- [internal/agent/workflow_spec_generation_dj124_test.go](../../internal/agent/workflow_spec_generation_dj124_test.go):
  - `TestLoopConvergesAfterForcedContradictionViaRevision`:
    1. MockExecutor scripted to:
       - Iter-0 scout: emit 2 open axes (auth-provider, observability-stack).
       - Decision-elaborator: commit `dec-cognito-auth` and `dec-datadog-observability` (the contradiction setup: budget mismatch).
       - Narrative: author one feature referencing both decisions.
       - Critics: flag the cost contradiction with `RelatedDecisionIDs: [dec-datadog-observability]`.
       - Iter-1 scout: no new `axes_open`; concern marked `still_open` (the scout can't resolve the contradiction itself).
       - Iter-1 revise-decisions: dispatched for `dec-datadog-observability`; mock returns a revised decision committing to CloudWatch instead.
       - Iter-2 critics: no findings.
       - Iter-2 scout: `Converged: true`.
    2. Assert workflow exits with `converged: true` in 3 iterations (or fewer).
    3. Assert `decision_revised` event recorded for `dec-datadog-observability`.
    4. Assert the final `state.ProposedSpec` has the revised decision (cloudwatch, not datadog).

- `TestRevisionLoopHitsCapForUnstableRevision` — similar setup but the mock revision keeps oscillating between datadog and cloudwatch each iteration. Assert the loop terminates via `convergence_revision_capped` at iter-3 (default cap).

**Verification:** `go test ./internal/agent/... -count=1 -race -skip TestCLISinkRendersAgentLifecycle`.

**Estimated:** 3-4 hours.

## Phase 7 — Validation against winplan re-run

**Goal:** the same winplan project that triggered DJ-125 + DJ-126 converges within budget under the new architecture.

**Process:**

1. Build the DJ-126 binary (DJ-125 must already be landed and validated): `go build -o ~/go/bin/locutus-dj126 .`.
2. Run `locutus-dj126 update --offline --reset` against winplan to refresh agent prompts.
3. Run `locutus-dj126 refine goals` against winplan with default 5-iteration budget.
4. Compare against the prior failing runs and the DJ-125-only result:
   - Did the loop converge?
   - How many revisions fired? On which axes?
   - Did the revision-count cap fire? On which axes?
   - Did `decision_revised` events appear in `.borg/history/` in the expected shape?
   - Are the final decisions internally coherent (no remaining contradictions)?

**What success looks like:** `locutus refine goals` exits with `converged: true` in ≤4 iterations on the winplan project. The history log shows 2-5 `decision_revised` events corresponding to the contradictions / factual errors / hallucinated citations the prior runs surfaced. No `convergence_revision_capped` event fires.

**What partial success looks like:** convergence within budget with the revision-count cap firing on 1-2 axes. Indicates those specific axes are under-constrained or the revise-mode prompt isn't strong enough; not an architectural failure but a tuning need.

**What failure looks like:** convergence still doesn't happen, OR the revision-count cap fires on many axes (loop oscillates without making progress). Diagnose against the trace: either the revise-mode prompt needs tightening (cap fires too easily), the cap is too low, or there's a deeper coordination problem the architecture doesn't address.

**Verification:** the winplan session traces are durable evidence.

**Estimated:** 1 hour of compute + manual review.

## Phase 8 — DJ-126 status flip + plan marked DONE

**Goal:** DJ-126 flips from `proposed` to `shipping` once Phase 7 validation passes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-126 status `proposed` → `shipping (Phases 1-7 landed YYYY-MM-DD)`.
- This plan file marked DONE.

**Verification:** `go test ./... -count=1 -race -skip TestCLISinkRendersAgentLifecycle` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 16-21 hours single-stranded across 3-4 sessions

## Pointers a fresh session should follow before resuming

1. Confirm DJ-125 is landed and the new `Concern` shape carries `RelatedDecisionIDs`. DJ-126 depends on this substrate.
2. Read DJ-126 in full ([docs/DECISION_JOURNAL.md#dj-126](../../docs/DECISION_JOURNAL.md#dj-126-decision-re-elaboration-for-cross-decision-contradictions-extends-dj-124-with-existing-decision-revision-depends-on-dj-125-concern-model)). It's the authoritative design.
3. Read DJ-124 ([docs/DECISION_JOURNAL.md#dj-124](../../docs/DECISION_JOURNAL.md#dj-124)) for the workflow this extends and the spawner-node pattern used.
4. Read the second winplan trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/) for the empirical failure modes the revise dispatch addresses.
5. Before the prompt edit in Phase 1, **re-read [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end**. The revise-mode section is the highest-stakes prompt edit in this plan; cycling-revision failure modes trace back to weak revise-mode prompts.
6. Phase 7 is the validation step. Don't flip DJ-126 status to `shipping` until winplan re-runs cleanly.

## What is explicitly out of scope

- **Decision deletion.** When critics flag a decision as "this axis shouldn't have been decided at all," the revise dispatch can't remove it; it can only replace it with a different decision on the same axis. Decision deletion would need a different dispatch (scout-driven, probably) and is held for a follow-up DJ if real cases demand it.
- **Cross-decision dependency tracking.** When `dec-X` is revised in a way that invalidates `dec-Y`'s rationale, `dec-Y` isn't automatically queued for revision. Critics surface the new contradiction next iteration; the revise dispatch handles it in iter-(N+1). Lazy invalidation by design — eager dependency tracking is held for a follow-up if it surfaces as a convergence-speed bottleneck.
- **Revision authored by the user.** The dispatch is autonomous-only. `locutus refine dec-X --against "<concern text>"` would be a manual-intervention verb mirroring this path; held for a follow-up if users ask for it.
- **Cycle detection refactor.** The DJ-125 plan notes that cycle detection could move from `DecidedAxesByIter` to manifest axis-state. DJ-126 inherits whatever DJ-125 ships; doesn't refactor.
- **Reconciler step retirement.** DJ-125 makes the reconciler agent's verdict mostly empty; DJ-126 doesn't remove the step but doesn't add new work to it either. The retirement is a follow-up cleanup.
