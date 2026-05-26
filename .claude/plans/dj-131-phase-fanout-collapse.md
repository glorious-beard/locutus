# DJ-131 — Phase-Fanout Collapse for ACP-Driven Council Steps

> **Governing DJ:** [DJ-131](../../docs/DECISION_JOURNAL.md#dj-131). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** SUPERSEDED by DJ-135 on 2026-05-26. The phase-fanout-collapse mechanism this DJ designed retires entirely. Under DJ-135 the coding agent runs ONE ACP session for the whole activity and dispatches its own subagents internally (via `Task` on Claude Code, equivalent on Codex / Gemini); Locutus no longer manages fanout. Historical: designed; implementation never started — blocked on DJ-127 which itself was subsumed by DJ-135.
> **Predecessors:** [DJ-127](../../docs/DECISION_JOURNAL.md#dj-127) (ACP-driven decision-elaboration + write tools — DJ-131 reuses its substrate); [DJ-122](../../docs/DECISION_JOURNAL.md#dj-122) (workflow executor + fanout primitive — DJ-131 adds a `FanoutMode` branch); [DJ-130](../../docs/DECISION_JOURNAL.md#dj-130) (per-step folder trace layout — phase-fanout dispatch reuses the same parent-step-with-N-children shape).
> **Surface area:** workflow step type extension + ACP phase-dispatch primitive + plan rendering + staging-area phase keying + capability set-binding + partial-result handling + trace renderer per-phase extraction.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Each phase verifies independently before moving on. **DJ-127 must ship before this plan starts** — every primitive DJ-131 extends is a primitive DJ-127 introduces.

## Why this plan exists

DJ-127 promises the council aligns with subscription-funded AI tooling (Claude Max, Codex Pro, Gemini Advanced) — every council invocation becomes one or more ACP coding-agent sessions, billed against the user's subscription rather than per-token. The funding-model argument is load-bearing: the DX win is the justification, not quality or speed.

But DJ-127's per-agent-per-axis dispatch shape doesn't fit the quota math. A moderate-complexity refine — 4–8 axes × 5 iterations + 3–5 critique dimensions × 5 iterations + 2–8 feature/strategy elaborations × 5 iterations — produces 80–200 ACP sessions per `locutus refine goals`. Claude Max's 5-hour rolling quota holds ~50 Sonnet sessions or ~10 Opus sessions per window. A single refine would burn the entire daily allocation; the user can't do their day-job coding-agent work for hours after.

The structural fix: dispatch fanout steps as **multi-phase plans handed to one ACP session**, not as N independent sessions. The coding agent's own parallelism (Claude Code's Task() primitive) handles within-session concurrency; locutus stops orchestrating fanout parallelism at the workflow layer for these steps. One session, N commits, native parallel research inside.

DJ-131 closes the quota-arithmetic gap DJ-127 inherits.

## Reference state (after DJ-127 ships; before DJ-131 starts)

- **DJ-127's substrate** lives at `internal/mcp/` (write tools: `spec_propose_decision`, `spec_revise_decision`, etc.; staging.go; capability.go) + `internal/dispatch/acp/` (council-side spawner) + `internal/dispatch/locking.go` + `internal/agent/workflow.go`'s `executeAgent` transport switch.
- **Agent transport** is per-agent frontmatter: `transport: direct_sdk | acp`. Decision-elaborator and similar research-heavy roles default to `acp`; critics, scout, narrative-elaborator default to `direct_sdk`.
- **Fanout dispatch** at [internal/agent/workflow.go:379+](../../internal/agent/workflow.go#L379) iterates fanout items and calls `executeAgent` per item. Under DJ-127, each call opens its own ACP session — N sessions for N items.
- **Session staging area** (DJ-127's `internal/mcp/staging.go`) keys commits by the bound session id and consolidates on `spec_commit_session()`. Capability scoping binds one session to one phase id (e.g., one `axis_id` for decision-elaborator).

## Resolved design questions

Recorded in chat 2026-05-21; settled before this plan went to implementation.

1. **Two `FanoutMode`s coexist; workflow author chooses.** `FanoutSlot` preserves DJ-122's per-item isolation; `FanoutPhase` collapses N items into one ACP session. Slot mode stays default; phase mode is per-step opt-in. Auto-detect-by-runtime-heuristic was considered and rejected — workflow authors have intent information the runtime doesn't.

2. **Coding agent owns local parallelism.** Claude Code's Task() runtime is what makes phase fanout cheap. Pushing per-phase dispatch back into locutus defeats the amortization. Plan template can suggest parallelism via prose ("phases are independent; use Task() to research them concurrently"); the agent's actual decision is its own.

3. **Per-phase attribution lives on tool-call payloads.** `spec_propose_decision.axis_id`, `spec_propose_new_node.id`, etc. are already self-attributing under DJ-127. The staging area keys by that field; the trace renderer pulls per-phase YAML views over the combined session transcript by phase id.

4. **Capability scoping binds to a set of phase ids, not one.** Coarser than DJ-127's per-session-per-axis binding; still rejects out-of-set commits at the MCP boundary. The agent could write to the wrong axis within the set; mitigation is tool-call validation comparing commit content against axis_id description (future hardening; not in initial landing).

5. **Per-step `MaxPhases` cap with partitioning fallback.** Bounded context: 6 axes per phase-fanout dispatch is comfortable for Claude Code; 15+ pushes past quality thresholds. When N exceeds the cap, locutus partitions into multiple phase-fanout sessions (12 axes → two 6-axis sessions). Per-step `MaxPhases` defaults: 6 for decision-elaboration, 6 for narrative-elaboration. Critique-dimension stays `FanoutSlot` (no phase mode at all).

6. **Workflow executor stays outer-orchestrator.** DAG, spawners, convergence, budget, conditional dispatch all at the workflow layer. Coding agent owns LOCAL orchestration within ONE phase-fanout step only. A phase-fanout dispatch IS one workflow step from the executor's perspective.

7. **Partial-result handling at the merge boundary.** Session staging preserves partial commits; merge function returns `[]RoundResult` of length N with explicit per-slot success/failure status; workflow falls through to slot-fanout retry on the next iteration for missing phases. Preserves DJ-122's per-slot failure isolation in spirit even though the underlying dispatch collapsed N slots into one session.

## Phase 1 — Workflow step type extension + plan-render scaffolding

**Goal:** add `FanoutMode` and `MaxPhases` to `WorkflowStep[S]`; build the plan-rendering primitive; route phase-fanout dispatches to a stub that errors clearly. Smallest, lowest-risk change; establishes the type surface before wiring the ACP session.

**Files expected to change:**

- [internal/agent/workflow.go](../../internal/agent/workflow.go):
    - `WorkflowStep[S]` gains `FanoutMode FanoutMode` and `MaxPhases int` fields. `FanoutMode` enum values: `FanoutSlot` (zero value; today's behavior) and `FanoutPhase`.
    - `ExecuteRound`'s fanout dispatch grows a branch on `FanoutMode`. `FanoutSlot` runs today's per-item dispatch path unchanged. `FanoutPhase` calls a new `e.dispatchPhaseFanout(ctx, step, items, snap)` method that initially returns an error ("DJ-131 Phase 2 not landed").
- [internal/agent/workflow_phase_fanout.go](../../internal/agent/workflow_phase_fanout.go) (new): scaffold for `dispatchPhaseFanout`; today just returns the stub error.
- [internal/dispatch/acp/plan_render.go](../../internal/dispatch/acp/plan_render.go) (new): markdown plan template engine; reads the agent's frontmatter `plan_template` block to know how per-item fields project into per-phase context blocks. Initial template format:

    ```
    # Plan: <step_id>

    You have <N> independent phases. Each one commits via <tool_name>. You may research phases in parallel via your Task() tool.

    ## Phase 1: <phase_id_1>

    <per-item context rendered from plan_template>

    ## Phase 2: <phase_id_2>
    ...
    ```

- [internal/agent/manifest.go](../../internal/agent/manifest.go) — `AgentDef` gains `PlanTemplate string` parsed from frontmatter. Parsed but not yet consumed (Phase 1 only validates the field exists).

**Tests:**

- `TestWorkflowStepFanoutModeDefault` — zero-value `FanoutMode` is `FanoutSlot`; existing fanout steps behave identically.
- `TestFanoutPhaseRoutesToPhasePrimitive` — set `FanoutMode: FanoutPhase` on a step; assert `dispatchPhaseFanout` is invoked (and returns the stub error for now).
- `TestPlanRenderProducesValidMarkdown` — drive plan-render with a 3-phase template + 3 fanout items; assert output has 3 `## Phase` sections in order.
- `TestAgentDefParsesPlanTemplate` — agent frontmatter with `plan_template:` block parses into `AgentDef.PlanTemplate`.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 2 — Phase-dispatch primitive against DJ-127's ACP spawner

**Goal:** implement `dispatchPhaseFanout` end-to-end: open one ACP session via DJ-127's spawner with capability scoped to N phase ids, render the plan as the session's initial message, wait for the session to close, extract per-phase results from the staging area, return as `[]RoundResult` of length N.

**Files expected to change:**

- [internal/agent/workflow_phase_fanout.go](../../internal/agent/workflow_phase_fanout.go):
    - `dispatchPhaseFanout(ctx, step, items, snap)`:
        1. Project each fanout item into a per-phase context block via the agent's `plan_template`.
        2. Extract phase ids (the field name varies per agent: `axis_id` for decision-elaborator, `id` for narrative-elaborator). Phase-id extraction is via a per-agent frontmatter field `phase_id_field: <json_field_name>`.
        3. Open an ACP session via `acp.SpawnPhaseSession(ctx, def, items, phaseIDs)` (DJ-127's spawner with a new constructor that binds capability to a SET of phase ids).
        4. Wait for session close; pull `[]StagedCommit` from staging via `staging.CommitsForSession(sessionID)`.
        5. Group commits by phase id; build `[]RoundResult` of length N; mark phases with no matching commit as failed (`RoundResult.Err = ErrPhaseMissing`).
- [internal/mcp/capability.go](../../internal/mcp/capability.go) (DJ-127 file) — extended to support set-binding: `Scope.AllowedPhaseIDs []string` instead of just `Scope.PhaseID string`. Tool-call validator rejects commits whose phase-id field isn't in the allowed set.
- [internal/mcp/staging.go](../../internal/mcp/staging.go) (DJ-127 file) — extended with `CommitsForSession(sessionID) []StagedCommit` that returns all commits the session produced, grouped by phase id at the caller's discretion.
- [internal/dispatch/acp/](../../internal/dispatch/acp/) — `SpawnPhaseSession` adds the set-binding constructor; session lifecycle otherwise identical to DJ-127's per-axis spawner.

**Tests:**

- `TestPhaseFanoutDispatchesOneSessionForNItems` — drive with a mock ACP client; assert one session opens, N tool calls commit, merge returns N results.
- `TestPhaseFanoutCapabilityRejectsOutOfSetPhaseID` — bind session to `{axis-a, axis-b}`; mock agent commits to `axis-c`; assert MCP boundary rejects, staging area receives no out-of-set commit.
- `TestPhaseFanoutPartialResultHandling` — mock ACP session that commits 3 of 5 phases then errors; merge returns 3 successes + 2 `ErrPhaseMissing` failures; workflow's existing per-slot failure handling absorbs them.
- `TestPhaseFanoutPlanRenderedIntoSessionInput` — assert the markdown plan with N phase sections is what the ACP session's initial message contains.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... ./internal/dispatch/acp/... ./internal/mcp/... -count=1 -race`.

**Empirical check:** with Phase 2 alone landed, manually run `locutus refine goals` on a tiny project with one elaborator step flipped to `FanoutMode: FanoutPhase`. Verify one ACP session opens (not N); verify the plan is correctly rendered into the session's input; verify per-phase commits land in the staging area; verify the merge function returns N results.

**Estimated:** 8-12 hours.

## Phase 3 — Per-phase trace extraction + MaxPhases partitioning

**Goal:** phase-fanout dispatches produce one DJ-130 step folder containing the combined session transcript plus N per-phase result YAMLs; per-step `MaxPhases` cap with partitioning when N exceeds it.

**Files expected to change:**

- [internal/agent/session.go](../../internal/agent/session.go) — `stepHandle` gains a sibling `phaseHandle` for phase-fanout dispatches. Layout: `calls/<step>-<agent>-phasefanout/{step.yaml, session-transcript.md, phase-01-<phase_id>.yaml, phase-02-<phase_id>.yaml, ...}`. `step.yaml` carries summed token counts across phases and the list of phase ids; per-phase YAMLs carry the tool-call payload for that phase plus extracted context windows.
- [internal/agent/workflow_phase_fanout.go](../../internal/agent/workflow_phase_fanout.go):
    - `dispatchPhaseFanout` partitions when `len(items) > step.MaxPhases`. Partition strategy: contiguous chunks of MaxPhases (axes 1-6 in session A, axes 7-12 in session B). Future iteration: semantic grouping (axes sharing source evidence go in the same session). Each partition produces its own DJ-130 step folder under a `<step>-<agent>-phasefanout-partition-<N>` subdirectory.
    - Per-partition results concatenate into the workflow's `[]RoundResult` in original order.
- [docs/debugging-traces.md](../../docs/debugging-traces.md) — new section on phase-fanout trace layout; how to navigate from a per-phase YAML back to the originating section of the session transcript via phase id; partitioning naming convention.

**Tests:**

- `TestPhaseFanoutTraceLayout` — drive a 3-phase dispatch; assert disk layout matches `calls/<step>-<agent>-phasefanout/{step.yaml, session-transcript.md, phase-01-axis-a.yaml, phase-02-axis-b.yaml, phase-03-axis-c.yaml}`.
- `TestPhaseFanoutStepYAMLAggregatesTokens` — after session closes, parent step.yaml sums token counts across N phases.
- `TestMaxPhasesPartitioning` — `MaxPhases: 3` with 8 items partitions into three sessions (3+3+2); each session's step folder uses the `partition-<N>` suffix; final `[]RoundResult` has 8 entries in order.
- `TestPerPhaseYAMLContainsToolCallPayload` — per-phase YAML's `tool_call_payload` field is the byte-identical staging-area entry for that phase.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Empirical check:** run `locutus refine goals` on a project with 7 open axes; `FanoutMode: FanoutPhase, MaxPhases: 4` on the decision-elaborator step. Verify two ACP sessions spawn (4 + 3); verify both step folders are written with correct partition naming; verify per-phase YAMLs extract cleanly from the session transcripts.

**Estimated:** 6-8 hours.

## Phase 4 — Spec-generation workflow opts in to phase fanout

**Goal:** flip decision-elaborator and narrative-elaborator steps to `FanoutMode: FanoutPhase` in the production spec-generation workflow; update the relevant agent `.md` files with plan templates and revised prompts.

**Files expected to change:**

- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) — decision-elaboration step gains `FanoutMode: FanoutPhase, MaxPhases: 6`. Narrative-elaboration steps (`elaborate_features`, `elaborate_strategies`) gain the same when fanout breadth > 2. Critique-dimension step stays `FanoutSlot` (lens independence).
- [internal/scaffold/agents/spec_decision_elaborator.md](../../internal/scaffold/agents/spec_decision_elaborator.md):
    - Frontmatter gains `phase_id_field: axis_id` and a `plan_template` block defining per-axis context projection (id, description, source_evidence, surfaced_by).
    - Prompt body updated to assume "you receive a multi-phase plan; commit each phase via `spec_propose_decision` with the matching `axis_id`." Walk `docs/agent-conventions.md` before drafting; the literal-sentinel pattern for grounded research carries verbatim.
- [internal/scaffold/agents/spec_feature_elaborator.md](../../internal/scaffold/agents/spec_feature_elaborator.md), [internal/scaffold/agents/spec_strategy_elaborator.md](../../internal/scaffold/agents/spec_strategy_elaborator.md) — analogous plan_template + prompt updates.

**Tests:**

- `TestSpecGenerationDecisionElaboratorFlipsToPhaseFanout` — assert the workflow definition carries `FanoutMode: FanoutPhase` on the decision-elaboration step.
- `TestSpecDecisionElaboratorPlanTemplateRendersAllFields` — render a 3-axis plan template; assert every per-axis field (id, description, source_evidence, surfaced_by) appears in the correct phase section.
- Existing spec-generation tests pass without change (mock executor path doesn't dispatch through ACP; `FanoutMode: FanoutPhase` falls through to `FanoutSlot` when transport is `direct_sdk`, which is the test mock's default).

**Verification:** `go test ./internal/agent/... -count=1 -race`.

**Empirical check:** run `locutus refine goals` on winplan with ACP transport configured. Verify the decision-elaboration dispatch in iter-0 opens one ACP session for all open axes (not N); verify each axis gets its decision committed; verify the trace folder shows per-phase YAMLs.

**Estimated:** 4-6 hours.

## Phase 5 — Validation against winplan quota arithmetic

**Goal:** measure the actual session count for a moderate-complexity refine; verify the quota math now closes.

**Process:**

1. Build the DJ-131 binary: `go build -o ~/go/bin/locutus-dj131 .`.
2. Run `locutus-dj131 update --offline --reset` against winplan.
3. Run `locutus-dj131 refine goals` against winplan with default budget; ACP transport configured for decision-elaborator + narrative-elaborator.
4. Count ACP sessions from the session traces under `winplan/.locutus/sessions/<sid>/calls/`. Compare against the pre-DJ-131 estimate (per-axis per-session would have been 80-200; DJ-131 target is 15-35).
5. Spot-check per-phase quality: pick three axes from the resulting spec; compare their decisions' rationale + alternatives + citations against what slot-fanout produced in prior runs. Look for cross-axis anchoring artifacts (e.g., AWS-everywhere bias if first phase picked AWS for compute).
6. Spot-check Claude Max quota consumption: did the refine fit in one 5-hour window?

**What success looks like:** session count drops 4-6× vs DJ-127 per-axis dispatch; resulting spec quality holds (no measurable degradation in critic findings vs slot-fanout baseline); refine fits comfortably in one Claude Max 5-hour window.

**What partial success looks like:** session count drops as expected but cross-axis anchoring shows up in 1-2 axes per refine — `MaxPhases` reduces to 4 to tighten context isolation; the per-axis-anchoring rate should drop accordingly.

**What failure looks like:** Claude Code processes phases serially despite plan-template suggestions (wall-clock per session = N × per-axis time, defeating the latency argument); OR per-phase quality systematically degrades from anchoring (critic findings spike on cross-axis coupling); OR partial-result handling produces inconsistent decision sets across iterations. Diagnose against the per-session traces; reversal criterion (a), (b), or (c) triggers depending on the failure mode.

**Verification:** the winplan session traces are durable evidence. No automated assertion here.

**Estimated:** 2-3 hours of compute + manual review.

## Phase 6 — DJ-131 status flip + plan marked DONE

**Goal:** DJ-131 flips from `proposed` to `shipping` once Phase 5 validation passes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-131 status `proposed` → `shipping (Phases 1-5 landed YYYY-MM-DD)`.
- This plan file marked DONE.

**Verification:** `go test ./... -count=1 -race` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 23-33 hours single-stranded across 4-6 sessions

(plus Phase 5 empirical compute time)

## Pointers a fresh session should follow before resuming

1. Confirm DJ-127 has shipped (`Status: shipping` on the DJ; write tools + staging + capability + locking + ACP spawner all in place). If DJ-127 is still proposed, this plan is blocked — DJ-131 reuses DJ-127's substrate end-to-end.
2. Read DJ-131 in full ([docs/DECISION_JOURNAL.md#dj-131](../../docs/DECISION_JOURNAL.md#dj-131)). It's the authoritative design; this plan is progress tracking.
3. Read DJ-127's reversal criteria — if any have triggered post-shipping, DJ-131's quota argument may not hold and the plan needs revisiting.
4. Read DJ-122 ([docs/DECISION_JOURNAL.md#dj-122](../../docs/DECISION_JOURNAL.md#dj-122)) for the workflow executor primitives DJ-131 extends.
5. Read DJ-130 ([docs/DECISION_JOURNAL.md#dj-130](../../docs/DECISION_JOURNAL.md#dj-130)) for the per-step folder trace layout DJ-131 reuses.
6. Each phase has an empirical check. **Run them.** The quota argument and the parallelism argument are both empirical; the static surface (build, vet, tests) is necessary but not sufficient evidence.

## What is explicitly out of scope

- **Semantic partition strategy** (grouping axes by shared source evidence when partitioning N > MaxPhases). Initial landing uses contiguous chunks; a future DJ refines partitioning when the failure mode "axes share evidence but were split across sessions" surfaces empirically.
- **Coding-agent-side Task() forcing.** The plan template suggests parallelism via prose; locutus doesn't enforce it. If reversal criterion (a) triggers, a future DJ may add explicit `<parallel>...</parallel>` directives or per-phase budget caps that force the agent's hand.
- **Phase fanout for direct-SDK transport.** Conceptually possible (one direct-SDK call with N structured-output sub-objects), rejected because direct-SDK's lack of tool-call loop makes the architectural argument inverted — there's no session-spawn cost to amortize.
- **Granular session checkpointing** (preserving state mid-tool-call). Partial-result handling at the dispatch boundary covers the common failure modes; finer-grained checkpointing is a future hardening if partial-failure rates prove too high.
- **Tool-call content validation** (cross-checking commit content against the bound `axis_id`'s description to catch wrong-axis commits). Initial landing relies on capability set-binding alone; a future DJ adds content validation if wrong-axis-commit failures surface empirically.
- **Phase fanout for critique-dimension steps.** Each lens is genuinely independent; the cross-axis coherence argument doesn't apply. Critique-dimension stays `FanoutSlot` indefinitely unless empirical evidence shows otherwise.
