# DJ-129 — Dimension-Driven Critics (Replace Fixed 4-Critic Surface)

> **Governing DJ:** to be added to `docs/DECISION_JOURNAL.md` as DJ-129 once the design is approved. This plan is the implementation tracker against that pending DJ entry.
>
> **Status:** SUPERSEDED by DJ-135 on 2026-05-26. The dimension-driven critic *discipline* (scout surfaces critique_dimensions; cohesion-critic dispatches per-dimension rather than running 4 fixed lenses) carries forward verbatim into the activity playbook. The Go-side dispatch loop that wired this together retires with the WorkflowExecutor. Historical: DONE — Phases 1-6 + 8 landed 2026-05-20.
>
> **Prerequisite:** [DJ-128](../../docs/DECISION_JOURNAL.md#dj-128-decisions-as-deliberation-logs-structured-critic-counterproposals-revision-cap-as-commit-refines-dj-126-revise-loop-after-third-winplan-re-run) Phases 1-7 in main as of 2026-05-20. DJ-129 retires DJ-128's Phase 3 work (the four critic prompt files) and replaces it with a parametric critic-elaborator + scout-driven dimension identification.
>
> **Surface area:** ScoutBrief schema extension; spec_scout prompt extension (dimension identification); new `spec_critic_elaborator` agent (one file, parametric); critique dispatcher (code-side); workflow `critique` step becomes a fanout; the four existing critic agent files are retired; minor `mergeCriticIssues` change to pull lens-tag from fanout item.
>
> **Discipline (per memory):** tests → design pause in chat → code; prompt edits walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end before drafting per [[feedback-agent-conventions-checklist-first]]; schema changes follow [CLAUDE.md's jsonschema-tag rule](../../CLAUDE.md).

## Why this plan exists

The current critique stage runs four hand-picked critic agents (architect, devops, sre, cost) in parallel. The choice of four lenses was historical accumulation, not design. Three problems result:

1. **Coverage gaps.** Real lenses are missing: security/privacy (a campaign-software project's voter-file privacy concerns; a fintech's PCI scope; a medical project's HIPAA boundary), compliance (state-level privacy regimes; election law), maintainability vs. team capacity (can the team you have actually build this in the time GOALS implies), data integrity, vendor lock-in / portability beyond pricing. Adding any of these means writing a new critic agent file plus prompt-engineering it — the N+1 trap.

2. **Forced critique noise.** A project whose GOALS.md explicitly de-prioritizes cost (research project; internal infra where the company eats the bill) still runs the cost critic. The dominant failure mode of an unforced critic is empty issues; the dominant failure mode of a *forced* critic is pattern-matching cost shapes onto decisions where cost isn't actually a constraint — producing specious findings that pollute the concerns set.

3. **Symmetry break with scout-driven dispatch.** The scout already identifies project-specific axes (AxesOpen) and dispatches one decision-elaborator per axis. The critique stage is special-cased: a fixed set of agents that don't know what's specific about the project. This is the same problem the scout-driven loop solved for first-author decisions.

DJ-129 closes the symmetry: the scout identifies critique dimensions (the same way it identifies axes); the critique stage fans out one parametric critic-elaborator per dimension. The four lenses become content (per-discipline sections in one elaborator prompt) rather than identity (separate agent files).

## Reference state (DJ-128 Phases 1-7 landed)

- **`CriticIssues` schema**: `Issues []CriticIssue` with `{Weakness, Evidence, Counterproposals[], RelatedDecisionIDs[]}`. DJ-129 does not change this.
- **`CriticCounterproposal` schema**: `{Option, Argument, Citations[]}` with sentinel discipline. DJ-129 does not change this.
- **`Concern` schema**: carries `Counterproposals []CriticCounterproposal` + `Advisory bool` from DJ-128. DJ-129 does not change this.
- **`degenerateCriticIssueValidator`**: lives in `internal/agent/critic_validator.go`. DJ-129 does not change this.
- **`mergeCriticIssues`**: consumes the DJ-128 structured shape. Today derives `Concern.Kind` from `critiqueKindFor(AgentID)` — under DJ-129 the AgentID is always `spec_critic_elaborator`, so the kind derivation moves to the fanout item's `lens` field.
- **`mergeDecisions` alternative monotonicity**: lives in `internal/agent/decision_monotonicity_dj128.go`. DJ-129 does not change this.
- **`spec.Decision.Locked` + cap-as-commit terminal**: DJ-128 Phase 6 work. DJ-129 does not change this.
- **`spec_decision_elaborator` revise mode prompt**: DJ-128 Phase 5 work. DJ-129 does not change this.
- **Four critic prompt files** at `internal/scaffold/agents/{architect,devops,sre,cost}_critic.md`: get retired in Phase 6 below; their lens-specific knowledge migrates to the per-discipline sections of `spec_critic_elaborator.md`.
- **`ScoutBrief` schema**: today carries `DomainRead`, `TechnologyOptions`, `ImplicitAssumptions`, `WatchOuts`, `AxesOpen`, `NewNodes`, `ConcernDispositions`, `Converged`. DJ-129 adds `CritiqueDimensions[]`. `WatchOuts` and `ImplicitAssumptions` stay (supporting context; not load-bearing for critique dispatch).
- **`PlanningState`**: today carries `DecidedAxesByIter`, `AxisRevisionCount`, `LockedDecisionIDs`. DJ-129 adds `CritiqueDimensionsByIter` (mirrors `DecidedAxesByIter` for dimension stability tracking).

## Resolved design questions

Recorded in chat 2026-05-20; settled before implementation.

1. **Critique dimensions come from the existing spec_scout, not a separate critique-scout.** The scout already surveys the project to identify gaps (AxesOpen) and concerns to honor (WatchOuts, ImplicitAssumptions today). Promoting that survey to also identify *critique dimensions* is a natural extension. One LLM call per iteration instead of two; the scout's project understanding is reused rather than re-derived.

2. **Dimensions are mostly stable across iterations with explicit add/retire signal.** Mirrors `DecidedAxesByIter` cycle detection. New decisions in a later iteration may surface new dimensions (e.g., adding a payments feature surfaces a PCI dimension); the scout can add them. Dimensions the spec no longer touches can be retired. Convergence requires the dimension set is stable for at least one iteration — same logic as the existing convergence rule.

3. **No LLM-lens floor; mechanical critics always run.** A project that doesn't surface a cost concern doesn't get a cost-lens critic. The mechanical `integrity_critic` (citation coverage, decision-per-feature, no-dangling-refs) stays code-side and runs unconditionally. Rationale: forcing an LLM critic to find a concern when none exists produces specious findings worse than no critique. The scout has five iterations to identify any real concern; if it under-identifies systematically, the mitigation is prompt enrichment (scout-side), not a code-side floor.

4. **Lens is a free-form grouping label; discipline is a bounded enum driving prompt content.** Lens (`"cost"`, `"sre"`, `"compliance"`, `"election-cycle-traffic"`, ...) is open-ended and project-defined; it groups concerns for the revise projection's `Concern.Kind` and drives nothing else in code. Discipline is a bounded enum (`web_grounded`, `spec_node_grounded`, `best_practice_grounded`, `goals_grounded`, `freeform`) that names *how to ground a claim*, not *what to look for*. The critic-elaborator's prompt has one section per discipline value; the dimension carries `disciplines []Discipline` (slice — a dimension can combine multiple disciplines). This avoids the lower-layer N+1 trap that a lens-fragment-per-file design would have created.

5. **DJ-128 winplan validation is deferred until DJ-129 lands.** Combined re-run validates both designs. Trade-off: loses causal attribution if validation surfaces a regression (was it DJ-128's deliberation log or DJ-129's dimension-driven critic that broke?), gains a single paid-LLM run instead of two. The reversal criteria below name the symptoms each design's failure modes would produce so the post-run review can attribute correctly.

6. **One critic-elaborator agent file, parametric across all dimensions.** Project's lens vocabulary is not constrained by what agent files exist. The dispatcher reads the scout's `CritiqueDimensions[]` and fans out one call per dimension. Each call's `FanoutItem` is the full `CritiqueDimension`; the projection renders the focus_question + source_evidence + the relevant discipline sections inline.

7. **Retirement is a positive signal, not a cleanup event.** Once a dimension is surfaced by the scout, its id is recorded in `CritiqueDimensionsByIter` permanently — even if subsequent iterations stop surfacing it. The semantic: a retired dimension was considered and concluded, not forgotten. The dimension may recur in a later iteration (new evidence surfaces the lens again); recurrence is OK because the id is already in the map. This matters for the stability check: stability is **monotonic-add** (true when no NEW dimension id appeared this iteration), not **set-equality** (which would force an extra iteration whenever the scout cleanly retired a resolved concern). Retirement therefore doesn't block convergence; new-dimension introduction does.

## Phase 1 — Schema changes + dispatcher scaffold

**Goal:** the `CritiqueDimension` type exists; the `ScoutBrief` schema carries `CritiqueDimensions[]`; the `PlanningState` carries `CritiqueDimensionsByIter` for stability tracking; the bounded discipline enum is registered with jsonschema enum tags so strict-mode providers reject invalid values.

**Files expected to change:**

- [internal/agent/specgen.go](../../internal/agent/specgen.go):
  - New `CritiqueDimension` struct: `{ID string, Lens string, FocusQuestion string, SourceEvidence []string, Disciplines []string, SeverityFloor string}`. All fields carry jsonschema tags. `Disciplines` carries `jsonschema:"enum=web_grounded,enum=spec_node_grounded,enum=best_practice_grounded,enum=goals_grounded,enum=freeform,minItems=1"`. `SeverityFloor` carries the existing severity enum (`high`/`medium`/`low`).
  - `ScoutBrief.CritiqueDimensions []CritiqueDimension` added with jsonschema description naming the scout's responsibility.
- [internal/agent/state.go](../../internal/agent/state.go):
  - `PlanningState.CritiqueDimensionsByIter map[string]int` (mirrors `DecidedAxesByIter`).
  - Optional `CurrentCritiqueDimensions []CritiqueDimension` (replaced per iteration; equivalent role to `AxesOpen` on PlanningState).
- [internal/agent/schemas.go](../../internal/agent/schemas.go): example payload for `CritiqueDimension` uses descriptive prose; no placeholder tokens. The example ScoutBrief gains a `CritiqueDimensions` entry to seed the model's understanding.

**Tests:**

- `TestCritiqueDimensionSchemaRejectsInvalidDiscipline` — schema-layer rejection of `Disciplines: ["bogus"]` at the structured-output API.
- `TestCritiqueDimensionRequiresMinItemsDisciplines` — empty disciplines slice is rejected at the schema layer.
- `TestScoutBriefRoundTripsCritiqueDimensions` — marshal/unmarshal preserves the slice.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 1-2 hours.

## Phase 2 — spec_scout prompt extension

**Goal:** the scout's prompt teaches it to identify critique dimensions from the project's GOALS + in-flight proposal. Walks the schema field order; uses positive phrasing; provides example dimensions with grounded source_evidence; names the discipline enum and how to pick.

**Files expected to change:**

- [internal/scaffold/agents/spec_scout.md](../../internal/scaffold/agents/spec_scout.md):
  - New `# Critique dimensions` section after the axes / new-nodes sections. Walks the `CritiqueDimension` fields in schema order. Names the discipline enum verbatim. Frames the conditional pattern: "if GOALS implies a cost ceiling, include a cost dimension with `disciplines: [web_grounded, goals_grounded]`; if not, don't." Includes 3-5 worked examples spanning different lenses (cost, sre, compliance, election-cycle-traffic, vendor-portability).
  - Dimension stability framing: "dimensions are mostly stable across iterations. Add a new dimension only when a new decision surfaces a concern the prior iteration's set didn't cover. Retire a dimension only when the spec no longer touches the area it covered."

**Process discipline:** walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end. The scout's prompt grows substantially; risk is anti-pattern priming (long sections of "don't do X" patterns) per convention §1-§3. Use positive phrasing throughout.

**Tests:**

- `TestScoutPromptNamesCritiqueDimensionsField` — the prompt mentions the field by name.
- `TestScoutPromptDocumentsDisciplineEnum` — the prompt names all five discipline values verbatim.
- `TestScoutPromptProvidesLensExamples` — the prompt carries example dimensions across at least three lenses (the diversity teaches the model that lens is open-ended).

**Verification:** `go test ./internal/scaffold/... -count=1`.

**Estimated:** 2-3 hours.

## Phase 3 — spec_critic_elaborator agent (parametric)

**Goal:** one new agent file at `internal/scaffold/agents/spec_critic_elaborator.md`. Generic critic identity, output schema `CriticIssues` (DJ-128 unchanged), `thinking: off` per the agent conventions. The prompt has one section per discipline enum value, each section teaching the grounding pattern for that discipline. The user-message projection renders the dimension's focus_question + source_evidence + a "Apply these disciplines: ..." block.

**Files expected to change:**

- [internal/scaffold/agents/spec_critic_elaborator.md](../../internal/scaffold/agents/spec_critic_elaborator.md): new file. Sections:
  - `# Identity` — generic critic; describes the role as "challenge the proposal on the dimension specified in the user message."
  - `# Spec-lookup tools` — same as the four existing critic prompts; `spec_search`, `spec_get`, `spec_list_manifest`.
  - `# Disciplines` — five subsections, one per discipline value:
    - `## web_grounded` — verify claims via web search; cite URLs with verbatim excerpts because external pages change. (Seeded from `cost_critic.md`'s web grounding section.)
    - `## spec_node_grounded` — cite other spec nodes by id via `spec_get`; verify the cited node says what your concern implies. (Seeded from `architect_critic.md`'s cross-decision integrity check.)
    - `## best_practice_grounded` — cite named principles ("12-factor app: stateless processes"; "Google SRE Book Ch.4: availability vs cost"). Just kind+reference; omit excerpt. (Seeded from `sre_critic.md`'s SLO framing.)
    - `## goals_grounded` — cite GOALS.md clauses with verbatim excerpts; honor GOALS as a hard constraint. (Seeded from all four critics' GOALS engagement.)
    - `## freeform` — when no specific discipline applies; the focus_question is the framing.
  - `# Task` — describes the CriticIssue output shape (Weakness, Evidence, Counterproposals, RelatedDecisionIDs); names the counterproposal-menu enumeration discipline (DJ-128); names the "needs investigation" sentinel as a last-resort. This section is roughly the unified version of the four existing critics' task sections.

**Process discipline:** prompts under `internal/scaffold/agents/` follow `docs/agent-conventions.md` end-to-end. Schema-skeleton risk is real here because the prompt has multiple subsections; example payloads in the schema registration must use descriptive prose.

**Tests:**

- `TestCriticElaboratorPromptDocumentsAllDisciplines` — the prompt has a section per discipline enum value.
- `TestCriticElaboratorPromptRequiresCounterproposalMenu` — DJ-128 carry-forward: enumeration discipline named.
- `TestCriticElaboratorPromptDocumentsSentinel` — `needs investigation` named as last-resort.
- `TestCriticElaboratorPromptDescribesGrounding` — each discipline section names the citation kind appropriate to it.

**Verification:** `go test ./internal/scaffold/agents/... -count=1`.

**Estimated:** 3-4 hours (the prompt grows from 4 small files to one larger file; care required per the conventions checklist).

## Phase 4 — Critique dispatcher + cycle detection

**Goal:** code-side dispatcher converts `state.CurrentCritiqueDimensions` into fanout items for `spec_critic_elaborator`. Cycle / stability check tracks dimension churn across iterations; convergence requires stability.

**Files expected to change:**

- [internal/agent/critique_dispatch_dj129.go](../../internal/agent/critique_dispatch_dj129.go): new file.
  - `fanoutCritiqueDimensions(s *PlanningState) ([]string, error)`: walks `s.CurrentCritiqueDimensions`, marshals one fanout item per dimension as JSON. Each item is a `CritiqueDimensionItem` carrying `{AgentID: "spec_critic_elaborator", ID: "crit:<dimension.id>", Dimension: CritiqueDimension}`. Returns `[]string` per the existing `fanoutReviseableConcerns` pattern (one marshaled item per slice entry).
  - `recordDimensionStability(s *PlanningState, currentSet []CritiqueDimension, iter int)`: walks the current iteration's dimensions; for each, if the id is not yet in `s.CritiqueDimensionsByIter`, records `dim.ID → iter` (first-seen). Existing entries are NOT overwritten — once recorded, a dimension's first-seen iteration stays as the historical signal that this dimension was considered. Per design decision #7, the map is append-only. Called from `mergeScoutBrief` after the brief is parsed.
  - `dimensionsAreStable(s *PlanningState) bool`: monotonic-add stability check. Returns true when every dimension in the current iteration's set already had its id recorded in `s.CritiqueDimensionsByIter` at an earlier iteration — i.e., the scout surfaced no NEW dimensions this turn. A scout that retired a previously-surfaced dimension still returns true (retirement is a positive signal that the concern was considered and concluded, not a churn signal). Convergence rule consumes this.
- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - `mergeScoutBrief` extended: pulls `CritiqueDimensions[]` from the brief onto `s.CurrentCritiqueDimensions`; calls `recordDimensionStability`.
  - `scoutSpawnFor`'s convergence rule extended: `Converged: true` AND `dimensionsAreStable(s)` together gate exit. Without dimension stability, the loop spawns another iteration (which the scout uses to grade dimensions just like it grades concerns).

The critique step itself uses `mergeCriticIssues` as its Merge (the existing DJ-128 implementation) — there's no separate dispatcher-merge function. The scout's `mergeScoutBrief` is where dimension state populates onto `PlanningState`.

**Tests:**

- `TestFanoutCritiqueDimensionsEmitsOneItemPerDimension` — fixture with 3 dimensions; assert 3 fanout items with correct id format.
- `TestDimensionStabilityRejectsNewDimensionAddition` — fixture where iter-N surfaces a dimension id not present in `CritiqueDimensionsByIter` yet; assert `dimensionsAreStable` returns false.
- `TestDimensionStabilityAllowsRetirement` — fixture where iter-N surfaces a SUBSET of the prior iteration's dimensions (one retired); assert `dimensionsAreStable` returns true (retirement is not churn per design decision #7).
- `TestDimensionStabilityAllowsRecurrence` — fixture where iter-N surfaces a dimension that was retired in iter-(N-1) but appeared in iter-(N-2); assert true (the id is already in `CritiqueDimensionsByIter`, so the recurrence is not new).
- `TestDimensionStabilityHoldsAcrossNoOpIterations` — fixture where consecutive iterations carry identical dimension sets; assert true.
- `TestRecordDimensionStabilityNeverOverwritesFirstSeen` — fixture where iter-3 surfaces a dimension whose id was first recorded in iter-1; assert `s.CritiqueDimensionsByIter["..."]` stays at 1 (the historical signal is preserved).
- `TestConvergenceRequiresDimensionStability` — full workflow fixture where scout claims `Converged: true` but a brand-new dimension was just introduced; assert loop spawns another iteration rather than exiting.

**Verification:** `go test ./internal/agent/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 5 — Workflow change: critique step becomes a fanout

**Goal:** the `critique` step in `convergenceLoopTemplate` switches from 4 parallel agents to a fanout dispatching `spec_critic_elaborator` over `state.CurrentCritiqueDimensions`. The merge handler (`mergeCriticIssues`) is unchanged structurally; one minor edit pulls the `Concern.Kind` lens-tag from the fanout item's `Dimension.Lens` instead of `critiqueKindFor(AgentID)`.

**Files expected to change:**

- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go):
  - `convergenceLoopTemplate`'s critique step rewritten: `Agents: []string{"spec_critic_elaborator"}`, `Fanout: fanoutCritiqueDimensions`, `Project: projectCritiqueDimension`, `Merge: mergeCriticIssues`.
  - New `projectCritiqueDimension(snap StateSnapshot[PlanningState]) []Message`: builds the user message from the prompt prefix + scout brief + in-flight manifest + the FanoutItem's CritiqueDimension (focus_question, source_evidence, disciplines).
  - `mergeCriticIssues` modified: derives `Concern.Kind` from the fanout item's `Lens` field rather than from `critiqueKindFor(AgentID)`. The legacy path (when AgentID matches one of the old four critic ids) is kept as a fallback for any persisted concerns that loaded from a pre-DJ-129 session.

**Tests:**

- `TestCritiqueStepFiresOneCallPerDimension` — fixture with 3 dimensions; assert 3 spec_critic_elaborator dispatches.
- `TestMergeCriticIssuesTagsKindFromLens` — fixture: fanout item carries `lens: "compliance"`; assert merged Concern.Kind == "compliance".
- `TestProjectCritiqueDimensionRendersFocusAndDisciplines` — user-message contains focus_question + every applicable discipline section header.

**Verification:** `go test ./internal/agent/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 6 — Retire the four critic prompt files

**Goal:** the four critic agent files are removed. Their lens-specific content has been migrated into the relevant discipline sections of `spec_critic_elaborator.md` during Phase 3. No code change references them after Phase 5.

**Files expected to change:**

- Delete `internal/scaffold/agents/architect_critic.md`.
- Delete `internal/scaffold/agents/devops_critic.md`.
- Delete `internal/scaffold/agents/sre_critic.md`.
- Delete `internal/scaffold/agents/cost_critic.md`.
- [internal/agent/workflow.go](../../internal/agent/workflow.go) `critiqueKindFor()`: kept as a legacy fallback for loaded session data, but no longer the primary path. Add a doc comment naming the DJ-129 transition.
- [internal/scaffold/agents/critic_prompts_dj128_test.go](../../internal/scaffold/agents/critic_prompts_dj128_test.go): delete; the assertions move into the new `spec_critic_elaborator.md` test file from Phase 3.

**Tests:**

- `TestRetiredCriticAgentsAbsentFromScaffold` — verify the four files are gone.
- The DJ-128 e2e tests (`dj128_e2e_test.go`) update their `MockResponse` setup: instead of fanning four `MockResponse{AgentID: "<x>_critic"}` per iteration, they fan one or more `MockResponse{AgentID: "spec_critic_elaborator"}` per iteration (count = number of dimensions the test fixture's scout surfaces).

**Verification:** `go test ./... -count=1 -race -skip TestCLISinkRendersAgentLifecycle`.

**Estimated:** 2-3 hours (the file deletions are trivial; the test updates require care).

## Phase 7 — End-to-end dimension-driven test

**Goal:** a `MockExecutor`-driven test demonstrating the full DJ-129 flow: scout identifies a project-specific dimension that ISN'T one of the legacy four lenses; the critic-elaborator dispatches against it; the critic emits a CriticIssue with counterproposals; the revise pass engages the menu; the loop converges.

**Files expected to change:**

- [internal/agent/dj129_e2e_test.go](../../internal/agent/dj129_e2e_test.go) new file:
  - `TestDJ129ScoutSurfacedComplianceDimensionDrivesCritic`: scout identifies a `compliance` dimension on a fixture project; the critic-elaborator dispatched against it emits a compliance-shaped concern; the elaborator's revise pass flips the dec to honor the compliance constraint; the loop converges with the compliance dimension stable.
  - `TestDJ129ProjectWithNoCostConcernRunsNoCostCritic`: fixture where GOALS.md does NOT imply a cost ceiling and the scout surfaces no cost dimension; assert zero cost-lens concerns in the final state.
  - `TestDJ129DimensionInstabilityBlocksConvergence`: fixture where scout iter-2 surfaces a new dimension not present in iter-1; assert convergence does NOT fire that iteration even with `Converged: true` on the brief.

**Verification:** `go test ./internal/agent/... -count=1 -race -run TestDJ129`.

**Estimated:** 3-4 hours.

## Phase 8 — Combined DJ-128 + DJ-129 validation against winplan

**Goal:** the same winplan project that triggered DJ-128 converges within budget under the combined DJ-128 + DJ-129 architecture. Reversal criteria attribute any regression to one or both designs.

**Process:**

1. Build the combined binary: `go build -o ~/go/bin/locutus-dj129 .`.
2. Run `locutus-dj129 update --offline --reset` against winplan to refresh agent prompts (this picks up the deleted critic files and the new `spec_critic_elaborator.md`).
3. Run `locutus-dj129 refine goals` against winplan with default 5-iteration budget.
4. Compare against [the DJ-126 cap-trip trace](file:///Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/) and any intermediate DJ-128-only validation runs (if any were performed before deferring).

**What success looks like:**

- `locutus refine goals` exits zero in ≤4 iterations on the winplan project.
- The history log shows `decision_revised` events with the DJ-128 deliberation chronology and at most a handful of `decision_locked` events.
- Critics emit grounded counterproposals; the dimension set surfaced by the scout is project-specific and includes lenses the old four-critic surface couldn't have produced (e.g. voter-file privacy regimes, election-cycle traffic).
- Final spec's decisions carry the DJ-128 alternative-monotonic deliberation history.

**What partial success looks like:**

- Convergence within budget with 10-20% of decisions locked. Indicates the critic-elaborator pair has bounded disagreements; lens-specific prompt tightening (reversal-criteria mitigation) is the next iteration.

**Reversal criteria — which design is responsible:**

- **(a) Scout surfaces too few dimensions** → real critic concerns vanish silently → mitigation: scout prompt enrichment with more lens examples. Attributable to DJ-129.
- **(b) Scout surfaces excessive dimensions (5+) on a small project** → noise + iteration cost → mitigation: scout-prompt tighten on retire-when-empty signal. DJ-129.
- **(c) Critic-elaborator under-cites under a discipline** → lens fragment-equivalent (discipline section) needs richer examples in the elaborator prompt. DJ-129.
- **(d) DJ-128 deliberation log accumulates "needs investigation" sentinels** → the critic-elaborator is less confident than the four fixed critics were because per-lens identity is lost → mitigation: enrich the focus_question shape so the critic has enough framing to commit to alternatives. Attributable to DJ-129's interaction with DJ-128's enumeration discipline.
- **(e) Cap-as-commit fires more often than under DJ-128 alone** → the critic-elaborator's disagreement profile is wider than the four fixed critics' was → mitigation: dimension-stability check tightens; OR scout learns to retire dimensions that have been challenged-and-rejected N times. DJ-129.

**Verification:** the winplan session traces under `.locutus/sessions/` are durable evidence.

**Estimated:** 1-2 hours of compute + manual review.

## Phase 9 — DJ-128 + DJ-129 status flips + plans marked DONE

**Goal:** both DJs flip from `proposed` to `shipping` once Phase 8 validation passes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md):
  - DJ-128 status: `proposed` → `shipping (Phases 1-7 landed YYYY-MM-DD; Phase 3 retired by DJ-129)`.
  - DJ-129 entry added (the full DJ text — currently captured in this plan's "Resolved design questions" + "Why this plan exists" sections); status `shipping (Phases 1-9 landed YYYY-MM-DD)`.
- [.claude/plans/dj-128-deliberation-log-and-cap-as-commit.md](dj-128-deliberation-log-and-cap-as-commit.md): mark DONE.
- This plan file: mark DONE.

**Verification:** `go test ./... -count=1 -race -skip TestCLISinkRendersAgentLifecycle` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 18-25 hours single-stranded across 4-5 sessions

## Pointers a fresh session should follow before resuming

1. Confirm DJ-128 Phases 1-7 are in main (Phase 8 was deferred under design decision #5; the binary at `~/go/bin/locutus-dj128` exists but the winplan re-run was not performed). The DJ-129 combined validation in Phase 8 is the next paid-LLM run against winplan.
2. Read this plan in full plus the DJ-128 plan ([dj-128-deliberation-log-and-cap-as-commit.md](dj-128-deliberation-log-and-cap-as-commit.md)). DJ-129 builds on DJ-128's interaction-shape work without changing it.
3. Re-read [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end before Phase 2 (scout prompt extension) and Phase 3 (critic-elaborator prompt creation). These are two high-stakes prompt edits; the conventions checklist exists for exactly this kind of session.
4. The discipline enum is the design's load-bearing simplification — a saturated set of grounding patterns rather than an open-ended lens taxonomy. Resist the temptation to add discipline values during implementation; if a project needs a grounding pattern that doesn't fit the five, that's a real signal to update the design, not a quiet enum addition.

## What is explicitly out of scope

- **Project-specific lens fragments at user-extension points.** Adding a new lens means the scout starts emitting dimensions with that lens label; no fragment file needs to be authored. If user-supplied per-lens prompts become a real need, that's a future DJ.
- **Per-dimension `severity_floor` enforcement.** The field is on the schema (so the scout can express a hint) but the workflow doesn't currently enforce it — concerns still default to `medium`. Tightening that's a future iteration.
- **Lens-driven discipline inference.** A natural extension would be "if lens=='cost', dispatcher auto-adds web_grounded to disciplines." Out of scope — the scout authors the disciplines field directly, full responsibility.
- **Retiring the integrity_critic into a dimension.** Today integrity is a mechanical concern emitter; it doesn't dispatch an LLM. Folding it into the dimension framework would mean an LLM call to do what regex + structural checks already do reliably. Out of scope; integrity stays mechanical.
- **Operator-level dimension veto.** A future DJ may add a CLI flag (`--skip-lens=cost`) so an operator can suppress a lens for a specific run. Out of scope.
