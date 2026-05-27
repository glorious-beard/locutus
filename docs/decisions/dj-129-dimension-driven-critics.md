## DJ-129: Dimension-Driven Critics (Scout-Surfaced Critique Surfaces Replace Fixed 4-Critic Lens Set; Builds on DJ-128 Structured Counterproposal Discipline)

**Status:** superseded by [DJ-135](dj-135-multi-runtime-pivot.md) on 2026-05-25 — the dimension-driven critic *discipline* (scout surfaces critique_dimensions; the cohesion-critic dispatches per-dimension rather than running 4 fixed lenses) carries forward into the activity playbook verbatim. The Go-side dispatch loop that wired this together retires with the WorkflowExecutor. Prior status: shipping (Phases 1-6 landed 2026-05-20).

**Context.** DJ-128 landed the deliberation log + structured counterproposal discipline that restored type-2 two-way door semantics to the council loop. The four critic agents it rewrote (architect, devops, sre, cost) — historically the fixed lens set chosen by accumulation rather than design — surfaced three structural problems on inspection:

1. **Coverage gaps.** Real lenses are missing: security/privacy (a campaign-software project's voter-file privacy concerns; a fintech's PCI scope; a medical project's HIPAA boundary), compliance (state-level privacy regimes; election law), maintainability vs. team capacity, data integrity, vendor lock-in / portability beyond pricing. Adding any of these means writing a new critic agent file plus prompt-engineering it — the N+1 trap.

2. **Forced critique noise.** A project whose GOALS.md explicitly de-prioritizes cost (research project; internal infra where the company eats the bill) still runs the cost critic. The dominant failure mode of an unforced critic is empty issues; the dominant failure mode of a *forced* critic is pattern-matching cost shapes onto decisions where cost isn't actually a constraint — producing specious findings that pollute the concerns set.

3. **Symmetry break with scout-driven dispatch.** The scout already identifies project-specific axes (AxesOpen) and dispatches one decision-elaborator per axis. The critique stage is special-cased: a fixed set of agents that don't know what's specific about the project. This is the same shape problem the DJ-124 scout-driven loop solved for first-author decisions.

**Decision.** Close the symmetry: the scout identifies *critique dimensions* (the same way it identifies axes); the critique stage fans out one parametric `spec_critic_elaborator` per dimension. The four lenses become content (per-discipline sections in one elaborator prompt) rather than identity (separate agent files).

Five coupled changes:

1. **`CritiqueDimension` schema on `ScoutBrief`.** New `ScoutBrief.CritiqueDimensions[]` field carrying one `CritiqueDimension{ID, Lens, FocusQuestion, SourceEvidence[], Disciplines[], SeverityFloor}` per critique surface. `Lens` is free-form (`cost`, `sre`, `compliance`, `election-cycle-traffic`, …); drives `Concern.Kind` for grouping and nothing else in code. `Disciplines` is a bounded enum (`web_grounded`, `spec_node_grounded`, `best_practice_grounded`, `goals_grounded`, `freeform`) that names *how to ground a claim* — one prompt section per discipline value; the elaborator applies the sections the dimension names.

2. **One parametric critic agent.** `spec_critic_elaborator.md` (DJ-129 Phase 3) replaces `architect_critic.md`, `devops_critic.md`, `sre_critic.md`, `cost_critic.md`. The prompt has one Identity section + five Discipline sections; the dimension's `disciplines[]` field selects which sections apply. Output schema is unchanged (`CriticIssues` from DJ-128).

3. **Critique step becomes a Fanout.** The `convergenceLoopTemplate`'s critique step changes from `Agents: []string{4 critics}, Parallel: true` to `Agents: []string{"spec_critic_elaborator"}, Fanout: fanoutCritiqueDimensions`. Empty CritiqueDimensions → zero items → critique is a no-op for that iteration (the no-floor design decision).

4. **Convergence requires dimension stability.** Mirrors the existing axis-cycle detection in `scoutSpawnFor`. A scout claiming `Converged: true` while introducing a brand-new dimension this iteration forces another iteration (the new dimension's critic-elaborator gets at least one chance to surface concerns). Stability is monotonic-add: new-dimension introduction blocks; retirement and recurrence do not (design decision #7).

5. **Lens-from-fanout-item Concern.Kind derivation.** `mergeCriticIssues` reads the dimension's Lens off the fanout item (the new `deriveCritiqueKind` helper) rather than off the AgentID. Legacy `critiqueKindFor` stays as a fallback for any pre-DJ-129 loaded session data.

**Alternatives considered.**

- **Add a separate critique-scout agent.** A dedicated agent that surveys for critique surfaces only (separate from spec_scout's axis-survey duty). Costs one extra LLM call per iteration; the spec_scout's project-understanding context (DomainRead, ImplicitAssumptions, WatchOuts) is already exactly what informs dimension identification, so a separate agent would re-derive it. Rejected.

- **Keep a fixed lens floor.** Always run a core critic set (e.g. always-on cost + always-on architecture) plus scout-driven extras. Preserves coverage at the cost of the forced-noise problem on projects where the floor doesn't apply. Rejected per design decision #3: forcing an LLM critic to find a concern when none exists produces specious findings worse than no critique. The mechanical `integrity_critic` (citation coverage, decision-per-feature, no-dangling-refs) stays code-side and runs unconditionally as a non-LLM floor.

- **One critic agent file per discipline (not per lens).** A `web_grounded_critic.md`, `goals_grounded_critic.md`, etc. Trades the lens-N+1 trap for a discipline-N+1 trap; loses the composition story (a cost dimension often needs both web_grounded and goals_grounded). Rejected.

- **Make Lens a bounded enum like Disciplines.** Forces lens vocabulary into code; defeats the project-specific-lens goal (`election-cycle-traffic`, `pci-scope` are project-named). Rejected.

- **Defer dimension identification to the user.** The scout could enumerate candidate dimensions; the user picks which ones apply via a config file or CLI flag. Reintroduces human-in-the-loop into a workflow whose explicit design goal is autonomous spec generation. Rejected.

**Consequences.**

- **Code:**
    - `internal/agent/specgen.go` — new `CritiqueDimension` struct with bounded discipline enum jsonschema tags; `ScoutBrief` gains `CritiqueDimensions []CritiqueDimension`.
    - `internal/agent/state.go` — `PlanningState` gains `CurrentCritiqueDimensions []CritiqueDimension`, `CritiqueDimensionsByIter map[string]int` (append-only first-seen tracking), `LastDimensionsStable bool` (computed in mergeScoutBrief before recording so scoutSpawnFor sees the prior map shape). `snapshotPlanningState` deep-copies the new fields.
    - `internal/agent/critique_dispatch_dj129.go` — new file: `fanoutCritiqueDimensions`, `recordDimensionStability`, `dimensionsAreStable`, `CritiqueDimensionItem` (fanout-item shape).
    - `internal/agent/workflow_spec_generation.go` — `mergeScoutBrief` populates dimensions and computes stability; `mergeCriticIssues` derives kind from the fanout item's Lens via new `deriveCritiqueKind` helper; new `projectCritiqueDimension` projection renders focus_question + source_evidence + applicable disciplines plus the proposal block; the critique step in `convergenceLoopTemplate` becomes a Fanout dispatching `spec_critic_elaborator`.
    - `internal/agent/workflow_spec_generation_dj124.go` — `scoutSpawnFor`'s convergence rule gates exit on `brief.Converged AND openCount==0 AND LastDimensionsStable`.
    - `internal/agent/workflow.go` — `RoundResult` gains a `FanoutItem` field; `executeAgent` threads `snap.FanoutItem` through so merge handlers can attribute results back to the dispatching dimension. `critiqueKindFor` doc-commented as the legacy fallback path.
    - `internal/scaffold/agents/spec_critic_elaborator.md` — new parametric critic agent file. One Identity section, five Discipline sections, the DJ-128 Counterproposals-menu task discipline carried forward unchanged.
    - `internal/scaffold/agents/spec_scout.md` — new `critique_dimensions` section teaching dimension identification: focus_question framing, discipline-enum coverage, lens open-endedness, the add/retain/retire lifecycle.
    - Deleted: `internal/scaffold/agents/{architect,devops,sre,cost}_critic.md` and the DJ-128-era `critic_prompts_dj128_test.go` (replaced by `elaborator_critic_dj129_test.go`).
    - Tests: `TestCritiqueDimensionRoundTrip`, `TestScoutBriefRoundTripsCritiqueDimensions`, `TestPlanningStateCarriesDimensionFields`, `TestCritiqueDimensionSchemaRegistered`, `TestFanoutCritiqueDimensionsEmitsOneItemPerDimension`, `TestFanoutCritiqueDimensionsHandlesEmptySet`, `TestRecordDimensionStability*`, `TestDimensionStability*`, `TestMergeScoutBriefPopulatesCritiqueDimensions`, `TestProjectCritiqueDimensionRendersFocusAndDisciplines`, `TestCritiqueStepIsAFanoutOverCritiqueDimensions`, `TestMergeCriticIssuesTagsKindFromDimensionLens`, `TestRetiredCriticAgentsAbsentFromScaffold`, `TestScoutPrompt*`, `TestCriticElaboratorPrompt*`, plus the three end-to-end `TestDJ129*` tests covering the compliance-dimension happy path, the no-cost-concern skip path, and the dimension-instability-blocks-convergence path.

- **User-visible:**
    - Critics now run only when the scout has identified a critique dimension. A research project with no cost concern won't see specious cost findings polluting its concerns set.
    - The lens vocabulary in `Concern.Kind` is project-defined: a campaign-software project's concerns may carry `Kind: compliance` or `Kind: election-cycle-traffic`; a fintech project may surface `Kind: pci-scope`. The revise projection's grouping reflects what the project actually cares about.
    - `spec_scout` is now responsible for two scout-driven dispatches per iteration: AxesOpen → decision-elaborators (DJ-124), and CritiqueDimensions → critic-elaborators (DJ-129). The symmetry makes the council's per-project tailoring legible at the scout layer.
    - The scout brief in `.locutus/sessions/.../scout-iter-N.yaml` now carries a `critique_dimensions` block alongside `axes_open`; users reading session traces see exactly which lenses the council applied to each iteration and why (the focus_question + source_evidence make it auditable).

- **Performance:**
    - Critic LLM calls per iteration scale with the number of dimensions the scout surfaces (1-5 typical) rather than a fixed 4. A research project with no critique dimensions → zero critic calls (net cost reduction). A campaign-software project with cost + compliance + election-cycle-traffic + vendor-portability dimensions → 4 critic calls (parity). The dominant cost change is shifting toward the project's actual surface area.
    - The scout's LLM call grows ~20-30% on tokens (the new `critique_dimensions` field with focus_question + source_evidence prose) — one larger scout call vs. potentially fewer critic calls.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. Existing sessions persisted with pre-DJ-129 critic AgentIDs (`architect_critic`, etc.) load cleanly through the `critiqueKindFor` legacy fallback. `locutus update --offline --reset` refreshes agent prompts to the DJ-129 set (the four `*_critic.md` files deleted, `spec_critic_elaborator.md` added, `spec_scout.md` updated).

**Reversal criteria.** Revert if:

- (a) the scout systematically under-identifies dimensions — fixtures that obviously have a cost concern produce briefs with no cost-lens dimension, hiding real issues. Surfaces as final specs that miss obvious lens-specific concerns the prior 4-critic surface would have caught. Mitigation: enrich the scout prompt's lens-diversity examples; if even with enriched examples the scout under-identifies, reintroduce a thin code-side floor that injects a default cost dimension when GOALS contains a budget clause and the scout didn't surface one.

- (b) the discipline enum proves too coarse — projects need a discipline pattern not covered by web/spec_node/best_practice/goals/freeform. Surfaces as scout briefs that pick `freeform` when a more specific discipline would have grounded the critic better; or critic-elaborator outputs that cite the wrong shape of evidence under `freeform` because the prompt doesn't have a section for the discipline they needed. Mitigation: add a discipline value to the enum + a section to the elaborator prompt (the design's whole point is this is cheap; adding a new lens is now a one-line schema change plus a prompt section, not a new agent file).

- (c) the dimension-stability convergence rule causes infinite loops — a scout keeps surfacing a new dimension every iteration, blocking convergence indefinitely. Surfaces as runs that hit budget exhaustion with the same scout repeatedly introducing one new dimension per iteration. Mitigation: per-iteration cap on newly-introduced dimensions (e.g. at most 2 new dimensions per iteration; scout must defer the rest). The cap fires the budget-exhaustion terminal with a diagnostic naming the rapidly-introduced dimensions for the user to see.

- (d) the parametric critic-elaborator produces shallower findings than the per-lens critics did — losing lens-specific depth because the discipline sections are general rather than lens-specific. Surfaces as comparison runs (DJ-128 4-critic vs. DJ-129 parametric on the same fixture) where the DJ-128 outputs cite more specific evidence per concern. Mitigation: enrich the per-discipline sections with lens-aware examples; if the depth gap is structural rather than promptable, the elaborator gains optional lens-specific guidance sections (e.g. "### When lens=cost") to bridge.

**Reference.** Builds on [DJ-128](dj-128-deliberation-log-and-cap-as-commit.md) — `CriticIssue`/`CriticCounterproposal` shape unchanged; the cap-as-commit and deliberation-log discipline carries forward. Closes the symmetry opened by [DJ-124](dj-124-decisions-before-narrative.md) — scout-driven dispatch for axes generalizes to scout-driven dispatch for critique dimensions. Honors [DJ-118](dj-118-json-schema-generation-uses.md) by tagging the new `CritiqueDimension` fields with invopop enum/description tags per the [CLAUDE.md jsonschema rule](../CLAUDE.md). Honors [DJ-103](dj-103-history-narrative-cache-archivist.md) — no new history event kinds; dimension lifecycle is observable via the scout brief in session traces. The motivating chat session is 2026-05-20; the implementation plan is at `.claude/plans/dj-129-implementation-tasks.md` and the design rationale at `.claude/plans/dj-129-dimension-driven-critics.md`.
