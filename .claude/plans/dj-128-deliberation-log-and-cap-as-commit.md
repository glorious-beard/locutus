# DJ-128 — Decisions as Deliberation Logs, Structured Critic Counterproposals, Revision Cap as Commit

> **Governing DJ:** [DJ-128](../../docs/DECISION_JOURNAL.md#dj-128-decisions-as-deliberation-logs-structured-critic-counterproposals-revision-cap-as-commit-refines-dj-126-revise-loop-after-third-winplan-re-run). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** designed; implementation not started.
> **Prerequisite:** [DJ-126](../../docs/DECISION_JOURNAL.md#dj-126) must be landed (it is — Phases 1-6 in main as of 2026-05-20). DJ-128 reshapes how DJ-126's revise loop behaves; it does not replace the structural pieces (revise dispatch, replace-by-axis merge, per-axis cap counter).
> **Surface area:** schema changes on `CriticIssues` + `Alternative` + `Decision` + `Concern`; `mergeDecisions` revise-path rewrite for alternative monotonicity; `mergeCriticIssues` consumes new shape; cap terminal flipped from error to commit; 4 critic prompts + decision-elaborator revise prompt rewrites; new validators.
> **Discipline (per memory):** tests → design pause in chat → code; prompt edits walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end before drafting per [[feedback-agent-conventions-checklist-first]]. Schema changes follow [CLAUDE.md's jsonschema-tag rule](../../CLAUDE.md): every meaningful field carries a tag with enough detail to prevent degenerate outputs; the validator is the safety net, not the primary enforcement.

## Why this plan exists

DJ-128 reshapes three behaviors the third winplan re-run ([`/Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/)) exposed as a single coupled failure mode:

1. **Critics emit free-form objections.** No structured counterproposal. The critic can object indefinitely without committing to a specific alternative. The elaborator is left guessing what the critic wants.
2. **Revisions replace decisions wholesale.** The prior chosen option and the critic finding that demoted it vanish from the decision body. Next iteration's critic sees only the current snapshot and can re-litigate the same axis with no memory.
3. **The revision cap errors out.** When critic and elaborator can't agree, the workflow exits non-zero rather than committing the latest revision and shipping. The critic gets unilateral kill-the-loop power.

Together these three create the bullying dynamic: critic objects → elaborator picks something else (losing prior deliberation) → critic objects to the new thing → repeat until the cap kills the loop. None of the three behaviors individually is wrong; coupled, they prevent the type-2 two-way door commit-and-move-on discipline.

This plan implements the three coupled fixes from the DJ together. Shipping any one alone leaves the failure mode intact: deliberation logs without structured counterproposals lets the critic still re-raise without commitment; structured counterproposals without deliberation logs still loses memory across revisions; cap-as-commit without the other two ships specs with no record of what was contested.

## Reference state (assuming DJ-126 has landed and the dedup fix from the post-validation winplan re-run is in main)

- **`Concern`** carries `IterationRaised`, `Status`, `RelatedDecisionIDs`, `RelatedAxisIDs`, `Justification` (DJ-125 + DJ-126).
- **`CriticIssues`** is `Issues []string` (free-form objection list).
- **`Alternative`** is `{Name, Rationale, RejectedBecause, Citations}` (no iteration / concern provenance on rejection).
- **`Decision`** has no `Locked` field; the persisted on-disk schema is unchanged from DJ-085.
- **`mergeDecisions`** revise-path replaces the prior decision body in `state.RawProposal` and increments `AxisRevisionCount`. No demotion of prior chosen option into alternatives.
- **`scoutConvergenceRevisionCappedTerminal`** returns a non-nil error; the workflow exits non-zero with a `convergence_revision_capped` DJ-103 event.
- **Critic prompts** (architect_critic.md, devops_critic.md, sre_critic.md, cost_critic.md) all reference the `Issues []string` shape and don't ask for counterproposals.
- **Revise mode prompt** in spec_decision_elaborator.md handles aggregated concerns (post-DJ-126-dedup) but doesn't require demotion of prior chosen options into alternatives.

## Resolved design questions

Recorded in chat 2026-05-20 between the winplan re-run failure and this plan; settled before implementation.

1. **One DJ, three coupled changes.** The deliberation log, structured counterproposals, and cap-as-commit ship together. Shipping any one alone preserves the bullying dynamic (see "Why this plan exists" above). The plan's phases group by surface area but the system isn't useful until all three land.

2. **`Counterproposal` is required, not optional.** Empty / placeholder counterproposals are schema violations. When a critic sees a real problem but has no concrete alternative, the critic emits a `counterproposal: "needs investigation"` sentinel (mirrors the literal-sentinel pattern from `justify_researcher.md`) and the workflow surfaces those as advisory-only concerns rather than dispatching revisions. Sentinel does not drive `hasReviseableConcerns`.

3. **Alternative monotonicity is hard-enforced.** `mergeDecisions` rejects any revision where `len(revised.Alternatives) < len(prior.Alternatives) + 1`. Validator failure logs the rejection, leaves the prior decision in place, records an integrity-violation concern naming the elaborator's error. The elaborator should never shrink the alternatives set; if it tries, that's a prompt failure to catch and surface, not a silent overwrite to allow.

4. **`Locked: true` is a per-decision flag on the in-flight `RawSpecProposal.Decisions[]` entry.** The persisted on-disk decision also carries the flag (omitempty) so users inspecting `.borg/spec/decisions/` see which decisions reached cap-as-commit. The flag is the dispatch gate for `hasReviseableConcerns` — locked decisions never appear in fanout items.

5. **The cap-as-commit terminal commits the elaborator's LATEST revision.** No final "pick from history" extra call. The latest revision is presumably the elaborator's most-informed answer (it saw all prior alternatives in the revise prompt). The deliberation log captures every prior alternative; the latest revision is just the current "best answer" snapshot. If the elaborator's latest revision is wrong, that's a (c)-reversal-criteria signal in the DJ, not a design defect of the commit semantics.

6. **`decision_locked` is a separate DJ-103 event kind from `decision_revised`.** Locked decisions get one final `decision_revised` event (the latest revision) AND one `decision_locked` event (the cap-as-commit signal). The two events carry different content: `decision_revised` records the revision-as-revision; `decision_locked` records the cap fired and which concerns were flipped to wontfix. `locutus history` distinguishes them.

7. **Critic prompt changes are per-critic.** Architect, devops, sre, cost each get their own counterproposal-discipline framing fit to the lens. Shared scaffolding: "Don't raise a concern you wouldn't commit to a specific alternative for." Per-critic: what "specific alternative" means in their lens (a vendor swap for cost critic, a deployment-shape change for devops, an SLO adjustment for sre, an architectural pattern for architect).

## Phase 1 — Schema changes + validators

**Goal:** the `CriticIssue` / `Alternative` / `Decision` / `Concern` types pick up the new fields; the jsonschema tags carry the constraints the model will read; the validator catches degenerate outputs.

**Files expected to change:**

- [internal/spec/types.go](../../internal/spec/types.go):
  - `Alternative` gains `RejectedAtIteration int` (omitempty) and `RejectedByConcernText string` (omitempty). jsonschema tags describe both as deliberation-log provenance fields.
  - `Decision` gains `Locked bool` (omitempty) with a jsonschema description naming the cap-as-commit semantics.
- [internal/agent/specgen.go](../../internal/agent/specgen.go):
  - `CriticIssues.Issues` shape changes from `[]string` to `[]CriticIssue`.
  - New `CriticIssue` struct: `Weakness`, `Evidence`, `Counterproposal`, `RelatedDecisionIDs` — all with jsonschema tags. `Counterproposal` carries `minLength` (in description prose since invopop doesn't enforce it) and a description naming the literal-sentinel "needs investigation" value as the only acceptable empty case. `Weakness` and `Evidence` mirror `AdversarialConcern`'s field documentation.
  - New `degenerateCriticIssueValidator` (mirrors `degenerateChallengerBrief`) — rejects `Counterproposal: "dummy"` / empty / one-word / etc. unless the sentinel "needs investigation" is present. Wired into the critic adapter's degenerate-output retry path.
- [internal/agent/state.go](../../internal/agent/state.go):
  - `Concern` gains `Counterproposal string` (omitempty) carrying the critic's structured counterproposal text. jsonschema description names it as the as-flagged counterproposal driving the revision.
- [internal/agent/raw_proposal.go](../../internal/agent/raw_proposal.go):
  - `RawDecisionProposal` carries the same shape as today; `Alternative`'s new fields flow through because Alternatives is `[]spec.Alternative`.

**Process discipline:** every new field carries a `jsonschema` tag per CLAUDE.md. Walk the field list against the agent-conventions checklist before drafting; example payloads for the new shapes use descriptive prose.

**Tests:**

- `TestCriticIssueSchemaRejectsEmptyCounterproposal` — schema-layer rejection of empty Counterproposal at the structured-output API; the adapter retry path engages.
- `TestCriticIssueAllowsNeedsInvestigationSentinel` — the literal sentinel value is the one exception to the non-empty rule.
- `TestAlternativeCarriesIterationAndConcernText` — a `RejectedAtIteration` + `RejectedByConcernText` round-trip through marshal/unmarshal and pretty-print.
- `TestDecisionLockedFlagDefaultsFalse` — legacy on-disk decisions without the field load with `Locked: false`.

**Verification:** `go build ./... && go vet ./... && go test ./internal/spec/... ./internal/agent/... -count=1 -race`.

**Estimated:** 2-3 hours.

## Phase 2 — `mergeCriticIssues` consumes the new shape

**Goal:** the merge function reads the structured `CriticIssue` shape, populates the new `Concern.Counterproposal` field, merges critic-provided `RelatedDecisionIDs` with the regex-auto-extracted set (critic-provided wins on conflict).

**Files expected to change:**

- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go):
  - `mergeCriticIssues` rewritten: unmarshals into the new `CriticIssues` shape; for each `CriticIssue`, constructs a `Concern` carrying Text (from Weakness + Evidence), Counterproposal (verbatim from the issue), RelatedDecisionIDs (union of critic-provided + regex-extracted, dedup-preserving).
  - `newConcernFromCritic` extended to accept the structured `CriticIssue` parameter; the prior free-form-string overload is retired.

**Tests:**

- `TestMergeCriticIssuesPopulatesCounterproposal` — fixture with one critic emitting one structured issue; assert `Concern.Counterproposal` matches the critic's verbatim field.
- `TestMergeCriticIssuesUnionsRelatedDecisionIDs` — critic emits explicit RelatedDecisionIDs `[dec-x]`; the text mentions `dec-y`; the merged Concern carries both.
- `TestMergeCriticIssuesPreservesSentinelCounterproposals` — sentinel "needs investigation" issues flow into Concerns with the sentinel verbatim in Counterproposal; downstream `hasReviseableConcerns` filters them out.

**Verification:** `go build ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 2 hours.

## Phase 3 — Critic prompts rewritten

**Goal:** the four critic prompts (architect, devops, sre, cost) describe the new `CriticIssue` shape and the counterproposal discipline. Per-lens framing of what "specific alternative" means.

**Files expected to change:**

- [internal/scaffold/agents/architect_critic.md](../../internal/scaffold/agents/architect_critic.md): the `## Task` section's "Emit issues" paragraph rewrites to walk the `CriticIssue` field shape (Weakness, Evidence, Counterproposal, RelatedDecisionIDs) in schema order; the "Don't raise a concern you wouldn't commit to a specific alternative for" framing replaces the current "find what doesn't add up" output discipline. The 9-rule analysis framing stays — that's the analysis lens, not the output discipline. Architect-lens counterproposals name an architectural pattern (e.g. "swap RDS Multi-AZ for Aurora Serverless v2 for the OLTP store" rather than "rethink the data layer").
- [internal/scaffold/agents/devops_critic.md](../../internal/scaffold/agents/devops_critic.md): same shape; devops-lens counterproposals name a deployment-shape change (e.g. "add a separate staging environment with auto-promotion rules to the GitHub Actions workflow" rather than "improve CI/CD").
- [internal/scaffold/agents/sre_critic.md](../../internal/scaffold/agents/sre_critic.md): same shape; sre-lens counterproposals name an SLO / error-budget adjustment (e.g. "lower the availability SLO from 99.9% to 99.5% to fit the budget cap" rather than "the SLO seems too tight").
- [internal/scaffold/agents/cost_critic.md](../../internal/scaffold/agents/cost_critic.md): same shape; cost-lens counterproposals name a vendor swap or capacity adjustment (e.g. "switch from Datadog to CloudWatch + Sentry to fit the $150 cap" rather than "Datadog is too expensive").
- All four: the literal sentinel "needs investigation" value is documented as the one acceptable empty-counterproposal output for cases where the critic sees a problem but genuinely cannot name a specific alternative. Sentinel concerns are advisory; they do not drive revisions.

**Process discipline:** walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end before drafting per the memory checklist. The counterproposal-discipline framing is high-stakes; the "no specific alternative" sentinel parallels the search-failure sentinels in `justify_researcher.md` / `spec_decision_elaborator.md`.

**Tests:**

- `TestEveryCriticPromptRequiresCounterproposal` — for each of the 4 critic prompts, assert the prompt names "Counterproposal" as a field and describes the per-lens commitment discipline.
- `TestEveryCriticPromptDocumentsSentinel` — assert each prompt names the literal "needs investigation" sentinel verbatim.
- `TestCriticPromptDropsLegacyIssuesStringFraming` — assert none of the critic prompts retain the legacy `Issues []string` "list of objections" framing.

**Verification:** `go test ./internal/scaffold/... -count=1 -race`.

**Estimated:** 3-4 hours (four prompts, each requiring care per the agent-conventions checklist).

## Phase 4 — `mergeDecisions` alternative monotonicity

**Goal:** when `mergeDecisions` replaces a decision via the revise path, the prior chosen option is automatically demoted into `alternatives[]` with `rejected_because = <driving concern text>` and `rejected_at_iteration = <current iter>`. The validator hard-fails revisions where the elaborator shrunk the alternatives set.

**Files expected to change:**

- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - `mergeDecisions` revise-path (the `len(matches) == 1` and `len(existingMatches) == 1` branches) extended:
    - Construct a "prior-chosen-as-alternative" entry from the prior decision: `Name = prior.Title`, `Rationale = prior.ArchitectRationale`, `RejectedBecause = <first driving concern's Counterproposal-aware text>`, `Citations = prior.Citations`, `RejectedAtIteration = currentIter`, `RejectedByConcernText = <driving concern verbatim>`.
    - Validate the incoming revision: every prior alternative (by Name) appears in the revised alternatives; the prior chosen option is in the revised alternatives. Reject the revision if either invariant fails; record an integrity-violation concern naming the elaborator's error.
    - On valid revision: prepend (or append at the right position) the demoted prior-chosen entry into the revised alternatives slice — the elaborator should already have done this per the revise prompt, but the merge function enforces the invariant defensively.
  - New helper `validateAlternativeMonotonicity(prior, revised RawDecisionProposal, drivingConcern *Concern) error` returns nil on valid revisions, descriptive error on shrinkage. Called from both replace branches.

**Tests:**

- `TestMergeDecisionsDemotesPriorChosenOption` — fixture: prior dec-X with chosen option A, alternatives [B, C]. Revise to chosen option B, alternatives [A, C] (B promoted, A demoted). Assert the revised decision in state.RawProposal carries A as an alternative with RejectedAtIteration set; RejectedBecause carries the driving concern text.
- `TestMergeDecisionsRejectsAlternativeShrinking` — fixture: prior dec-X with alternatives [B, C]. Revise emits alternatives [D] (dropped B, C). Assert: revision rejected; prior decision unchanged; integrity_critic concern recorded naming the shrinkage.
- `TestMergeDecisionsAlternativeMonotonicityHonorsNewAlternatives` — fixture: revise emits prior alternatives [B, C] PLUS demoted prior chosen A PLUS new alternative D. Total: 4 alternatives. Accept.
- `TestMergeDecisionsDemotionPreservesAlternativeCitations` — fixture: prior chosen A has citations [c1, c2]. After demotion to alternative, the alternative entry carries [c1, c2].

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 4-5 hours.

## Phase 5 — Revise mode prompt rewrite

**Goal:** the revise mode section of `spec_decision_elaborator.md` walks the elaborator through the alternative-monotonicity discipline — every revision either flips a prior alternative to chosen (and demotes the prior chosen) or rejects the critic's counterproposal (and adds it as a new alternative with elaborator-side rejection reasoning). The new `Counterproposal` field on each rendered finding becomes the load-bearing input.

**Files expected to change:**

- [internal/scaffold/agents/spec_decision_elaborator.md](../../internal/scaffold/agents/spec_decision_elaborator.md):
  - The Revise mode section's pattern-list (Factual error / Cross-decision contradiction / Hallucinated citation) extends with a new top-level structural pattern:
    - **Flip:** the critic's counterproposal becomes the new chosen option; the prior chosen option moves to alternatives with `rejected_because` naming the critic's reasoning. Use when the critic's argument is correct and the counterproposal is genuinely better.
    - **Reject:** the critic's counterproposal becomes a new alternative entry with `rejected_because` naming the elaborator's reasoning. The chosen option stays; the alternatives list grows by one. Use when the critic's argument doesn't survive scrutiny.
  - The "Preserve `axes[]` verbatim" and "Preserve the prior `id`" mandates stay.
  - New mandate: the alternatives list strictly grows. Every prior alternative appears in the revised alternatives; the prior chosen option appears in the revised alternatives (demoted with the driving concern's text as `rejected_because`); the critic's counterproposal appears either as the new chosen option (flip) or as a new alternative (reject).
  - The "deliberation log" framing names the alternatives as the durable record of what was considered and why; reviewers + future iterations read it to avoid re-litigating settled rejections.

**Process discipline:** walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end before drafting. The flip/reject discipline is high-stakes; the failure mode "elaborator silently drops prior alternatives" is exactly what the validator catches but the prompt should make it unlikely in the first place.

**Tests:**

- `TestDecisionElaboratorReviseModeRequiresAlternativeMonotonicity` — scaffolded prompt mandates the alternative-monotonicity discipline by name; explicitly says the prior chosen option becomes an alternative on revise.
- `TestDecisionElaboratorReviseModeDescribesFlipAndReject` — prompt names both the flip and reject patterns and ties each to the critic's counterproposal field.
- `TestDecisionElaboratorReviseModeRetainsAxesPreservation` — existing DJ-126 axis-preservation mandate survives the prompt rewrite.

**Verification:** `go test ./internal/scaffold/... -count=1 -race`.

**Estimated:** 2-3 hours.

## Phase 6 — Cap-as-commit terminal

**Goal:** `scoutConvergenceRevisionCappedTerminal` reshapes from error-and-exit to commit-and-continue. The contested concerns flip to `wontfix`; the capped decisions get `Locked: true`; the terminal returns nil. The convergence rule fires naturally on the next scout call.

**Files expected to change:**

- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - `scoutConvergenceRevisionCappedTerminal` rewritten: writes the existing `convergence_revision_capped` DJ-103 event AND a new `decision_locked` event per capped decision. Marks every open concern whose RelatedDecisionIDs intersects the capped axes' decisions as `Status: wontfix` with `Justification = "axis hit revision cap of N at iter M; council could not resolve critic↔elaborator disagreement; current decision committed as best answer; alternatives carry the contested reasoning"`. Sets `Locked: true` on each capped decision in `state.RawProposal.Decisions`. Returns nil (no error).
  - `hasReviseableConcerns` extended: a concern naming only locked decisions is filtered out; a concern naming a mix of locked and unlocked decisions is reviseable only for the unlocked ones.
  - `fanoutReviseableConcerns` updated: skips locked decisions from the priorByID map.
  - The scout spawner's existing `axesExceedingRevisionCap` check stays — it still fires the terminal — but the terminal no longer errors. After the terminal runs, the spawner expands the next iteration template normally; the scout sees the wontfix dispositions in the manifest.

- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go):
  - New `decisionLockedEvent(decision *RawDecisionProposal, contestedConcerns []Concern, iter, cap int) history.Event` builder. TargetID = decision.ID; Rationale names the cap that fired + the wontfix concern texts.

**Tests:**

- `TestRevisionCapCommitsRatherThanErrors` — drives the trajectory from Phase 4's `TestRevisionCapTerminatesWhenExceeded` but asserts the loop exits cleanly (no error) and the proposal carries the latest revision with Locked: true; contested concerns are wontfix.
- `TestLockedDecisionsAreExcludedFromRevise` — fixture: dec-X is locked; concern names dec-X. Assert `hasReviseableConcerns` returns false; `fanoutReviseableConcerns` produces zero items.
- `TestLockedDecisionsSurviveSubsequentRevise` — mixed fixture: locked dec-X and unlocked dec-Y in the same concern's RelatedDecisionIDs. Assert fanout produces one item for dec-Y only.
- `TestDecisionLockedEventRecorded` — fixture with historian; after cap-as-commit, the event store carries a `decision_locked` event per capped decision.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 7 — End-to-end convergence test

**Goal:** a `MockExecutor`-driven test that drives the entire shape: critic emits counterproposals; elaborator demotes prior chosen options into alternatives; revision-cap fires on intractable disagreement; loop ships with the latest revision committed and concerns wontfix.

**Files expected to change:**

- [internal/agent/workflow_spec_generation_dj124_test.go](../../internal/agent/workflow_spec_generation_dj124_test.go):
  - `TestDJ128HappyPathConvergesViaFlipRevision`: a single revision flips a prior chosen option to the critic's counterproposal; the demoted prior is in alternatives; concerns are addressed; loop converges in 3 iterations. Asserts the final proposal's `alternatives[]` carries the deliberation chronology.
  - `TestDJ128RejectRevisionAddsCriticCounterproposalAsAlternative`: a single revision rejects the critic's counterproposal; the counterproposal becomes a new alternative with elaborator-side rejection reasoning; the chosen option stays; loop converges. Asserts the alternatives grew by one entry corresponding to the critic's counterproposal.
  - `TestDJ128CapAsCommitShipsWithLockedDecisions`: drives 3 oscillating revisions on the same axis; the cap fires; the workflow exits cleanly; the proposal carries the latest revision with Locked: true; the contested concerns are wontfix; the convergence rule fires naturally on the next scout pass.

**Verification:** `go test ./internal/agent/... -count=1 -race -skip TestCLISinkRendersAgentLifecycle`.

**Estimated:** 3-4 hours.

## Phase 8 — Validation against winplan re-run

**Goal:** the same winplan project that triggered DJ-128 converges within budget under the new architecture.

**Process:**

1. Build the DJ-128 binary: `go build -o ~/go/bin/locutus-dj128 .`.
2. Run `locutus-dj128 update --offline --reset` against winplan to refresh agent prompts.
3. Run `locutus-dj128 refine goals` against winplan with default 5-iteration budget.
4. Compare against the DJ-126 cap-trip run at [`/Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/):
   - Did the loop converge cleanly, hit cap-as-commit on some axes and ship, or hit budget exhaustion?
   - How many decisions are Locked at the end? Which axes?
   - How rich are the alternatives lists on revised decisions? (5+ entries per revised axis is healthy; 2 entries means the alternative-monotonicity validator isn't being hit and the model is shrinking the set silently.)
   - Are critic counterproposals concrete or sentinel-heavy?
   - Did `decision_revised` + `decision_locked` events appear in `.borg/history/` in the expected shape?

**What success looks like:** `locutus refine goals` exits zero in ≤4 iterations on the winplan project. The history log shows `decision_revised` events with the deliberation chronology and at most a handful of `decision_locked` events on genuinely contested axes. Critics emit concrete counterproposals (not sentinels) on most concerns. Final spec's decisions carry alternative-monotonic deliberation history.

**What partial success looks like:** convergence within budget with 20-30% of decisions locked. Indicates the critic-elaborator pair systematically disagree on a class of axis; lens-specific prompt tightening (reversal-criteria mitigation (d)) is the next iteration.

**What failure looks like:** convergence still doesn't happen, OR critic concerns degenerate to sentinel-only (the structured shape made critics under-report), OR shipped decisions are systematically wrong on inspection (cap-as-commit committed wrong revisions). Reversal-criteria paths apply.

**Verification:** the winplan session traces are durable evidence.

**Estimated:** 1 hour of compute + manual review.

## Phase 9 — DJ-128 status flip + plan marked DONE

**Goal:** DJ-128 flips from `proposed` to `shipping` once Phase 8 validation passes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-128 status `proposed` → `shipping (Phases 1-8 landed YYYY-MM-DD)`.
- DJ-126 status updated: `Phase 7 supplanted by DJ-128 validation` (DJ-126's Phase 7 was the failed run that motivated DJ-128; DJ-128's Phase 8 is the new validation).
- This plan file marked DONE.

**Verification:** `go test ./... -count=1 -race -skip TestCLISinkRendersAgentLifecycle` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 20-26 hours single-stranded across 4-5 sessions

## Pointers a fresh session should follow before resuming

1. Confirm DJ-126 is landed AND the dedup-by-decision-ID fix from the post-validation winplan re-run is in main. DJ-128 builds on both.
2. Read DJ-128 in full ([docs/DECISION_JOURNAL.md#dj-128](../../docs/DECISION_JOURNAL.md#dj-128-decisions-as-deliberation-logs-structured-critic-counterproposals-revision-cap-as-commit-refines-dj-126-revise-loop-after-third-winplan-re-run)). It's the authoritative design.
3. Read the third winplan trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/) for the empirical failure mode. The `hosting-platform (revised 3×)` cap-trip and the iter-4 critic concerns (c-46, c-48, c-49 about naming drift after revisions) are the concrete shape of the bullying dynamic.
4. Before the prompt edits in Phase 3 and Phase 5, **re-read [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end**. Four critic prompts + the decision-elaborator revise prompt = five high-stakes prompt edits; the agent-conventions checklist exists for exactly this kind of session.
5. Phase 8 is the validation step. Don't flip DJ-128 status to `shipping` until winplan re-runs cleanly. The reversal criteria in the DJ name the specific empirical signals to watch for.
6. The cap-as-commit semantics interact with the convergence rule. Don't try to short-circuit the post-cap scout call — the scout sees the wontfix dispositions in the manifest and arrives at `Converged: true` naturally. Bypassing the scout post-cap would break the DJ-125 "scout owns convergence" invariant.

## What is explicitly out of scope

- **Decision deletion / renaming.** DJ-126's exclusion stays: revise replaces in place; can't remove a decision or rename its ID. The `dec-supabase-…` ID surviving a revision to RDS (observed in the winplan trace) is an annoyance the deliberation log doesn't fix on its own. A future DJ may add a "supersede" action that creates a new decision with a new ID and links the old one to it; that's a separate design.
- **Critic counterproposal rebuttal via the elaborator's `Reject` pattern.** The elaborator can reject a counterproposal by adding it as an alternative with a reason, but it can't trigger a critic re-grade of the rejection within the same iteration. Critics see the rejection in the next iteration's manifest and either accept or argue with it via a new concern. The cross-talk loop is bounded by the cap.
- **Per-decision revision-count cap tuning by lens.** All axes share `LOCUTUS_DECISION_REVISION_CAP=3` today. Some axes (high-coupling architectural ones like hosting-platform) might genuinely need more iterations; others (low-coupling polish ones) might need fewer. A future DJ may add per-lens or per-axis cap tuning; this DJ keeps the global default.
- **Reversal-criteria (c) mitigation: extra "commit" call after cap fires.** The DJ lists it as the mitigation for cap-as-commit shipping wrong decisions; the plan does NOT implement it upfront. If Phase 8 validation surfaces (c) as a real signal, the next iteration adds the commit call.
- **DJ-127 (MCP write tools + ACP transport) integration.** DJ-127 is on the queue but separate. DJ-128 changes the agent surface (CriticIssue schema, revise prompt) but the dispatch primitive stays direct-SDK as today.
