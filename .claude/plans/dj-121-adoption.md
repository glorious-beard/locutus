# DJ-121 Adoption — Implementation Plan

> **Governing DJ:** [DJ-121: Coarsen Pre-Planning to Workstream Grain; Agent Owns Step Decomposition via Worktree Checklist](../../docs/DECISION_JOURNAL.md#dj-121-coarsen-pre-planning-to-workstream-grain-agent-owns-step-decomposition-via-worktree-checklist-refines-dj-010-dj-074-dj-120) in `docs/DECISION_JOURNAL.md`. The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes that don't belong in the DJ.
>
> **Status:** Phases 1-8 complete 2026-05-14. Phase 9 (final `PlanStep` removal) pending until all consumers have been observed on the new contract in practice.
> **Surface area:** ~110 references to `PlanStep` / `StepProgress` / `.Steps` across 25 files at the start; soft-deprecate path leaves most parseable while routing new behaviour through workstream-grain APIs.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Each phase verifies independently before moving on.

## Why this plan exists

DJ-121 commits to several entangled changes — removing `spec.PlanStep`, reshaping the supervisor, coarsening validation, sequential-by-default scheduling, agent-owned checklists, persistence schema collapse. Doing them as one big diff would be unreviewable and unrecoverable on crash. The plan breaks the work into atomic, verifiable phases that compose to the DJ-121 end state.

## Reference state (before adoption)

Today's pipeline (post-DJ-119, post-DJ-120):

- **Planner** (`internal/agent/planner_test.go`, `schemas.go`) emits `spec.MasterPlan` containing `Workstream`s, each with `Steps []PlanStep` and per-step `Assertion`s.
- **Dispatcher** (`internal/dispatch/dispatcher.go`) runs workstreams via `executor` with `Parallel: true`, `MaxConcurrency: d.MaxTotal`, `TypeLimits: d.MaxPerAgent`. `runWorkstream` opens an ACP connection per workstream, creates a session, then iterates `ws.Steps` calling `Supervise` per step.
- **Supervisor** (`internal/dispatch/supervisor.go`) `Supervise(ctx, step, conn, sessionID)` runs the retry/validate loop per step. Validator grades against `step.Assertions`. Churn detection at step grain.
- **Workstream persistence** (`internal/workstream/record.go`) tracks `StepProgress []StepProgress` per workstream.
- **Overlap detection** (`internal/overlap/`) operates on per-step file claims.
- **Preflight** (`internal/preflight/`) gates per-step prerequisites.

## Phase 1 — Sequential execution + parallelism scaffold removal: NEXT

**Goal:** sequential workstream scheduling becomes the default; parallel-execution knobs are removed. Smallest, safest change first.

**Files expected to change:**

- `internal/dispatch/dispatcher.go` — drop `MaxTotal` / `MaxPerAgent` fields; set `executor.Step.Parallel = false`; remove `MaxConcurrency` and `TypeLimits` from the `executor.Config`.
- `cmd/adopt.go` — `realDispatch` no longer sets `MaxTotal` / `MaxPerAgent` (already nil-default, just verify).
- `internal/dispatch/acp_supervise_test.go` — verify nothing tests parallel scheduling directly. Existing tests should keep passing.

**Verification:** `go build ./... && go vet ./... && go test ./internal/dispatch/... ./cmd/ -count=1` — all green.

**Estimated:** 30 minutes.

## Phase 2 — `_locutus/plan.md` write + agent checklist instruction

**Goal:** the worktree-resident plan/checklist mechanism lands as additive behavior. PlanStep iteration still happens; we're just adding the new artifacts on top so the next phase has them to work with.

**Files expected to change:**

- `internal/dispatch/dispatcher.go` — `runWorkstream` writes `_locutus/plan.md` after `CreateWorktree` and before `conn.NewSession`. Plan content: the Approach node, its acceptance criteria (derived from union of step assertions today, lifted up later), spec DAG subtree pointers, and the agent's instruction to maintain `_locutus/checklist.md`.
- New helper `internal/dispatch/worktree_plan.go` (or similar) — renders the plan.md content from `spec.Workstream` + relevant spec subtree.
- `internal/scaffold/agents/` — the coding-agent system prompt gains a section instructing the agent to maintain `_locutus/checklist.md`. (Locutus's scaffold defaults — these get embedded into `.borg/agents/` via `locutus init`.)

**Verification:** new test exercising the `_locutus/plan.md` write; integration test in cmd/ confirms a workstream run leaves the artifact behind.

**Estimated:** 1 hour.

## Phase 3 — Supervise reshape: one supervised unit per workstream

**Goal:** `Supervise` operates on the Approach / Workstream, not on individual PlanSteps. The retry/validate loop runs once per workstream. PlanSteps still exist in the spec model (Phase 4 removes them), but the dispatcher no longer iterates them.

**Files expected to change:**

- `internal/dispatch/supervisor.go` — new shape `Supervise(ctx, approach spec.Approach /* or workstream view */, conn, sessionID) (*StepOutcome, error)`. The retry loop's validator-feedback string is derived from approach-level criteria, not step assertions.
- `internal/dispatch/streaming.go` — `runAttempt` now sends one prompt per attempt (the whole approach's worth of work), not one prompt per step. Monitor's churn detection runs at workstream/attempt grain.
- `internal/dispatch/dispatcher.go` — `runWorkstream` calls `Supervise` once per workstream, not in a loop over steps.
- `internal/dispatch/judge.go` — validator prompt template grades against workstream-level criteria.
- `internal/dispatch/acp_supervise_test.go` — tests reshape to the new signature; the four scenarios (happy / retry / churn / cancel) still apply, just at workstream grain.

**Verification:** the four core supervisor scenarios pass with the new shape. Existing integration tests in `cmd/adopt_*_test.go` still pass (PlanStep model still exists for them).

**Estimated:** 2-3 hours.

## Phase 4 — Soft-deprecate `PlanStep`; lift primary contract to `Workstream.Assertions`

**Goal (revised — soft-deprecate path):** `spec.PlanStep` stays parseable. The primary acceptance-criteria contract moves to `Workstream.Assertions` (which the spec model already has — it was added for "workstream-level validation gates"). The validator prefers `ws.Assertions` when populated; falls back to the step-assertion union when empty. Plan-md rendering surfaces `ws.Assertions` as the primary section. `PlanStep.Assertions` is documented as deprecated. **Phase 9 (new tail phase) is the actual `PlanStep` removal**, once all consumers have migrated.

**Files expected to change:**

- `internal/spec/plan.go` — `PlanStep` struct removed; `Workstream.Steps` field removed; `Workstream.AcceptanceCriteria` field added.
- `internal/spec/spec_test.go` — fixture updates.
- `internal/agent/schemas.go` — planner output schema no longer includes PlanSteps.
- `internal/agent/planner_test.go` — golden plan fixtures updated to new shape.
- `internal/overlap/overlap.go` — overlap detection operates on workstream-level file claims (lifted from step claims).
- `internal/preflight/preflight.go` — preflight runs per workstream, not per step.

**Verification:** all spec/planner/overlap/preflight tests pass. The planner agent prompt may need a quick eval to confirm it still produces sensible workstream-level criteria (Phase 6's deeper agent-prompt work).

**Estimated:** 3-4 hours. This is the largest phase.

## Phase 5 — Persistence: collapse `StepProgress`

**Goal:** the workstream record drops per-step bookkeeping. Just workstream status + `AgentSessionID`. Migration helper for existing `.borg/` data.

**Files expected to change:**

- `internal/workstream/record.go` — `StepProgress` removed; `ActiveWorkstream` simplifies.
- `internal/workstream/record_test.go` — fixture updates.
- `cmd/adopt_step_progress_test.go` — likely renamed and reshaped, or deleted if the coverage migrates to `cmd/adopt_integration_test.go`.
- `cmd/adopt.go` — `persistStepProgress` becomes `persistWorkstreamProgress`; the `StepCompleteHandler` shape collapses.
- One-shot migration: when `locutus update` runs against a `.borg/` with old-format workstream records, derive the workstream-level acceptance criteria from the union of the old PlanSteps' assertions and rewrite the records. Plus a `LOCUTUS_DJ121_MIGRATION_DONE` marker or similar guard.

**Verification:** unit tests for the migration. Existing workstream-record round-trip tests pass.

**Estimated:** 2 hours.

## Phase 6 — Planner agent updates

**Goal:** the planner agent stops decomposing into PlanSteps. It produces Workstreams with workstream-level acceptance criteria and a DependsOn set.

**Files expected to change:**

- `internal/scaffold/agents/planner.md` (or similar; the actual planner agent def location) — prompt updated to instruct workstream-level output.
- `internal/agent/schemas.go` — planner output JSON schema reflects the new shape (already touched in Phase 4; this phase finalizes the prompt alignment).
- An eval check to confirm the planner's output quality at the new grain isn't worse than the old per-step output.

**Verification:** planner output golden tests pass; quick manual run against a real spec confirms the planner emits sensible workstream-level criteria.

**Estimated:** 1-2 hours.

## Phase 7 — Validator + agent scaffold finalization

**Goal:** validator agent grades against workstream acceptance criteria; coding-agent scaffold default carries the checklist instruction in its system prompt.

**Files expected to change:**

- `internal/scaffold/agents/validator.md` (or wherever the validator agent def lives) — prompt updated to workstream-level grading.
- `internal/scaffold/agents/<coding-agent>.md` — system prompt section about maintaining `_locutus/checklist.md`. Probably one shared scaffold across all three (claude-code, codex, gemini).
- Re-run any existing prompt-quality evals.

**Verification:** validator and coding-agent prompts pass the existing guards (`cmd/TestEveryAgentDeclaresThinking`, anti-pattern priming guard, etc.).

**Estimated:** 1 hour.

## Phase 8 — Documentation + cleanup

**Goal:** README, agent-conventions, status output, and any stale references are updated.

**Files expected to change:**

- `README.md` — supported-workflows section updated (workstream-level resume, no step decomposition).
- `docs/agent-conventions.md` — checklist instruction documented.
- `cmd/status.go` — output simplifies (no "step N/M of workstream X").
- This plan file marked DONE.

**Verification:** full `go test ./... -count=1` green.

**Estimated:** 30 minutes.

---

## Total estimate: 1-2 days single-stranded

## Pointers a fresh session should follow before resuming

1. Read [DJ-121](../../docs/DECISION_JOURNAL.md#dj-121-coarsen-pre-planning-to-workstream-grain-agent-owns-step-decomposition-via-worktree-checklist-refines-dj-010-dj-074-dj-120) in full. It's the authoritative design; this plan is progress tracking.
2. Read the chain: DJ-010 (the original supervision design, partially superseded), DJ-074 (resume contract, refined twice), DJ-120 (first resume narrowing), DJ-119 (ACP transport, prerequisite for DJ-121's lifecycle premise).
3. Read `internal/dispatch/supervisor.go` and `internal/dispatch/dispatcher.go` — these are the bulk of Phase 3 surgery.
4. Read `internal/spec/plan.go` — Phase 4's main target.
5. Don't relitigate DJ-121's design decisions unless surfacing a reason in chat first. The sequential-by-default execution, one-Approach-one-Workstream decomposition, agent-owned checklist, workstream-level validation, and `_locutus/` worktree artifacts are committed.
