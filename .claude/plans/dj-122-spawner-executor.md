# DJ-122 Migration — Spawner-Node Executor Implementation Plan

> **Governing DJ:** [DJ-122: Graph-Mutation Workflow Executor with Spawner Nodes (Supersedes DJ-112 on Control-Flow Topology)](../../docs/DECISION_JOURNAL.md#dj-122-graph-mutation-workflow-executor-with-spawner-nodes-supersedes-dj-112-on-control-flow-topology). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes that don't belong in the DJ.
>
> **Status:** DONE — Phases 1–7 landed 2026-05-14. Manual smoke + Planning/Assimilation gate-spawner migration are follow-up work tracked elsewhere. DJ-122 status flipped to `shipping`.
> **Surface area:** `internal/executor/` (~440 LOC) + `internal/agent/workflow.go` wrapper + 3 council workflow definitions + ~25 references across agent / dispatch / cmd consumers.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Each phase verifies independently before moving on. CLI visibility rendering is deferred per direction in chat — get the engine working first.

## Why this plan exists

DJ-122 replaces `internal/executor`'s static-DAG model with a graph-mutation executor that supports spawner nodes. The three council workflows migrate together. Doing it as one big diff would be unreviewable and risky on partial-completion crash. The plan breaks the work into phases that each compose to a working state — the test suite stays green at every phase boundary, and the in-flight motivation (the spec council's convergence loop) materializes only in Phase 5 once the executor primitives are settled.

Planning and assimilation workflows stay static-shape under DJ-122 (no spawners needed); they migrate to the new executor in Phase 6 but their topology is unchanged. Dispatch-side spawner work (the supervisor-as-validation-gate-spawner generalization) is **out of scope for this plan** — it lives in DJ-123 once DJ-122 is shipping.

## Reference state (before adoption)

- **Executor:** [internal/executor/dag.go](../../internal/executor/dag.go) (~440 LOC). `Config[S]` carries static `Steps`, optional `Converged` + `MaxIterations` for "re-run whole DAG until convergence." Topological sort up-front via `buildStepGraph`; cycle prevention via `dgraph.PreventCycles()`; per-step `Conditional` closures; fanout expressed at the agent-wrapper layer, not the executor layer.
- **Workflow wrapper:** [internal/agent/workflow.go](../../internal/agent/workflow.go). `Workflow[S]` (line 66) carries `Snapshot`, `DefaultProject`, `Rounds []WorkflowStep[S]`, `MaxRounds`. `WorkflowStep[S]` (line 52) carries `ID`, `Agents`, `Parallel`, `DependsOn`, `Fanout`, `Project`, `Merge`, `Conditional`. `WorkflowExecutor[S].Run` (line 594) is the entry point that consumers (`GenerateSpec`, `Plan`, `Analyze`) call.
- **Spec council shape (today):** [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go). Nine rounds, single-pass (`MaxRounds: 1`). Critic findings route through `cluster_findings` → `revise` → `reconcile_revise` once, then done. **No actual convergence loop** — one revise pass and ship.
- **Coding-agent dispatch:** [internal/dispatch/dispatcher.go](../../internal/dispatch/dispatcher.go) uses `executor.Executor[dispatchState]` for the outer workstream DAG. Sequential-by-default per DJ-121. Stays static under DJ-122; spawner extension lands later in DJ-123.
- **Observability:** [internal/executor/dag.go](../../internal/executor/dag.go) `Event` with statuses `started / completed / skipped / error / waiting`. No `graph_mutated` kind yet. Per chat direction, the renderer is deferred to a later session.

## Resolved design questions

Three open questions were surfaced before drafting; resolved in chat 2026-05-14.

1. **Gate evaluation: hybrid — Go and LLM both supported.** A gate is a regular `WorkflowStep[S]` whose `Run` produces a verdict (via a Go-only evaluator step, or via an LLM `Agents:` call that returns a structured verdict), and whose `Spawn` reads the verdict and decides what to append. Some convergence checks are qualitative — *"is this spec coherent across all deliverables and aligned with GOALS.md?"* — and reduce poorly to static heuristics; the primitive must admit a generative evaluator. For Phase 5's spec council gate, evaluation is **LLM-driven** via a new `spec_gate` agent that grades the assembled ProposedSpec against the four-lifecycle-phases YES question and returns a structured `ConvergenceVerdict`. Future gates (e.g., the dispatch-side validation gates in DJ-123) pick the Go or LLM path per their workload.
2. **Iteration budget: per-gate, with a workflow-level fallback default.** Different gates need different budgets (a 5-iteration spec council loop is not the same as a 3-retry validation loop), so budget belongs on the gate's step config, not as a workflow-global constant. `WorkflowStep[S]` gains a `Budget int` field used by spawner closures as the iteration cap for whatever loop they drive; absent/zero falls back to `Workflow[S].DefaultGateBudget` (new optional field, default 5). The spec council gate sets `Budget: 5` with `LOCUTUS_SPEC_GEN_MAX_ITERATIONS` env override.
3. **Budget exhaustion is a hard failure, not soft convergence.** When a gate hits its budget with concerns still open, the workflow run fails. Reason: either the prompting is wrong (systems issue worth fixing) or the problem space is genuinely hard (signal worth learning from); writing a "force-converged" state to disk papers over both. Mechanics: the gate's `Spawn` appends a terminal `convergence_failed_iter:N` step whose `Run` writes a DJ-103 history event tagged `convergence_failed` (carrying unresolved concerns, the gate's last verdict reasoning, and a JSON snapshot of the in-progress ProposedSpec for forensic review) and then returns a non-nil error; the executor propagates; `GenerateSpec` returns the error; the calling command exits non-zero. **Nothing is persisted to `.borg/spec/`** — failure means "investigate the prompting or the problem; do not ship this spec."

## Phase 1 — Add Spawn capability to the executor

**Goal:** the executor admits graph mutation, but no workflows use it yet. Existing static-DAG behavior is preserved bit-for-bit.

**Files expected to change:**

- [internal/executor/dag.go](../../internal/executor/dag.go) — `Step` gains optional `Spawn func(ctx context.Context, snapshot S, output any) (newSteps []Step, newEdges []Edge, err error)`. New tiny `Edge` type `{From, To string}`. `executeOnce`'s wave loop is rewritten so that after a step's `RunStep` returns, if the step also has a `Spawn` (or returned spawns via the `RunStep` output — choose one path; the function-on-Step path is cleaner), the new vertices and edges are added to `g` via `AddVertex` / `AddEdge`, `predecessors` is recomputed via `g.PredecessorMap()`, and the wave loop continues until the queue drains AND no pending nodes remain.
- [internal/executor/dag.go](../../internal/executor/dag.go) — new `MaxGraphMultiplier int` field on `Config[S]` (default 1000 if zero) bounds total node count to `MaxGraphMultiplier × initialNodeCount`. Exceed during `executeOnce` returns a new `ErrGraphSizeExceeded` that names the most-recent spawner.
- [internal/executor/dag.go](../../internal/executor/dag.go) — the up-front `buildStepGraph` validation still runs against the initial step list; spawned-in steps and edges are validated incrementally as they're appended (re-running `dgraph.AddEdge` with `PreventCycles` catches any spawner that would form a cycle, returning `ErrEdgeCreatesCycle` wrapped with the spawner id).
- [internal/executor/dag_test.go](../../internal/executor/dag_test.go) — new tests:
  - `TestSpawnAppendsStepsAndEdges` — a step's Spawn returns one new step and one edge; the executor runs it after the parent.
  - `TestSpawnRespectsGraphSizeCap` — a runaway spawner that returns one new step each call hits the cap and errors with the spawner id in the message.
  - `TestSpawnRejectsCycle` — a spawner that returns an edge creating a cycle errors with `ErrEdgeCreatesCycle` wrapped.
  - All existing tests pass unmodified.

**Verification:** `go build ./... && go vet ./... && go test ./internal/executor/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 2 — Iteration metadata + spawn-from-template primitive

**Goal:** spawner nodes can be authored as `(template, IterationContext) -> []Step` closures with iteration metadata threaded through node IDs and `StepResult`s. The "convergence loop" idiom becomes expressible as a gate node that returns the next iteration's nodes from a template closure.

**Files expected to change:**

- [internal/executor/dag.go](../../internal/executor/dag.go) — `Step` gains optional `TemplateID string` and `IterationIndex int` fields (zero-valued on initial-graph nodes). New `IterationContext` struct `{IterationIndex int, ParentNodeID string, TemplateID string}`. New helper `executor.AppendSubgraph(template func(IterationContext) []Step, ctx IterationContext) ([]Step, []Edge)` that runs the template, sets `TemplateID` / `IterationIndex` on each returned step, prefixes each step's `ID` as `<template_id>#iter:<n>:<base_id>`, and produces edges from `ParentNodeID` to each template-root step.
- [internal/executor/dag.go](../../internal/executor/dag.go) — `StepResult` gains `TemplateID` and `IterationIndex` fields (populated by `runSingle` / `runParallel` from the step they ran).
- [internal/executor/dag_test.go](../../internal/executor/dag_test.go) — new tests:
  - `TestConvergenceLoopRunsToCompletion` — a 3-iteration loop with a gate that returns no spawns on iteration 3 terminates with exactly 3 iterations' worth of nodes executed.
  - `TestConvergenceLoopHitsBudget` — a loop with a gate that always spawns hits the executor's `MaxGraphMultiplier` and errors visibly.
  - `TestIterationMetadataInResults` — `StepResult.IterationIndex` and `TemplateID` are populated correctly across iterations.

**Verification:** new tests green; existing tests still green.

**Estimated:** 2-3 hours.

## Phase 3 — `graph_mutated` event kind

**Goal:** the event sink receives a new event whenever a spawner appends nodes, so downstream consumers (MCP progress, the eventual CLI renderer, tests) can track graph growth.

**Files expected to change:**

- [internal/executor/dag.go](../../internal/executor/dag.go) — `Event.Status` accepts new value `"graph_mutated"`. New `Event.Mutation *MutationDetails` field with `{SpawnerID string, AppendedNodeCount int, TotalNodeCount int}`. Emitted from `executeOnce` immediately after a spawn-handling branch successfully appends nodes.
- [internal/executor/dag_test.go](../../internal/executor/dag_test.go) — `TestGraphMutatedEventEmitted` verifies the event arrives on the events channel with correct `SpawnerID` and count fields.
- [internal/agent/workflow.go](../../internal/agent/workflow.go) — `BridgeToSink` (line 123) and `emitEvent` (line 158) updated to recognize the new status and propagate the `Mutation` payload through `WorkflowEvent`. `WorkflowEvent` (line 159 in state.go — confirm location at implementation time) gains an optional `Mutation` field.
- [internal/agent/workflow_test.go](../../internal/agent/workflow_test.go) — verify the event survives the bridge round-trip.
- MCP progress consumer in `cmd/` and the existing CLI spinner — at minimum, **do not crash** on the new event kind. Rendering work is explicitly deferred.

**Verification:** event-emission and bridge tests green; existing event tests still green.

**Estimated:** 1-2 hours.

## Phase 4 — Reshape `WorkflowStep[S]` to admit spawner steps

**Goal:** `WorkflowStep[S]` gains a `Spawn` field analogous to its existing `Fanout`. The agent-layer `WorkflowExecutor` translates this into the executor's `Step.Spawn`. Static workflows continue to compile and run unchanged.

**Files expected to change:**

- [internal/agent/workflow.go](../../internal/agent/workflow.go) — `WorkflowStep[S]` (line 52) gains `Spawn func(ctx context.Context, snapshot StateSnapshot[S], results []RoundResult) (newSteps []WorkflowStep[S], newEdges []executor.Edge, err error)`. `ExecuteRound` (line 290) checks for `Spawn` and threads it into the `executor.Step.Spawn` closure built during round assembly.
- [internal/agent/workflow.go](../../internal/agent/workflow.go) — `Workflow[S]` (line 66) gains optional `MaxGraphMultiplier int` mirrored from executor config (default matches the executor's default) and optional `DefaultGateBudget int` (default 5) used as the fallback when a gate step's own `Budget` is zero.
- [internal/agent/workflow.go](../../internal/agent/workflow.go) — `WorkflowStep[S]` gains `Budget int` field. Read by spawner closures as the iteration cap for the loop they drive; zero falls back to the workflow's `DefaultGateBudget`. Has no effect on non-spawner steps.
- [internal/agent/workflow_test.go](../../internal/agent/workflow_test.go) — new test `TestSpawnerStepWiringEndToEnd`: a tiny workflow whose final step spawns one additional step on its first invocation and nothing on the second; verifies the agent-layer wiring threads the spawn through, the spawned step runs, iteration metadata is preserved.
- Existing tests for `SpecGenerationWorkflow`, `PlanningWorkflow`, `AssimilationWorkflow` continue passing without modification — no spawners used yet.

**Verification:** new wiring test green; existing tests green.

**Estimated:** 2 hours.

## Phase 5 — Migrate spec council to the convergence loop

**Goal:** the spec council's `critique → cluster_findings → revise → reconcile_revise` static tail becomes a convergence loop driven by a `gate` spawner. The gate appends the next iteration's `revise / reconcile / critique / gate` quartet when concerns remain; appends nothing when concerns are clean (loop terminates as the queue drains); appends a single terminal `force_converged` node when the iteration budget hits.

**Files expected to change:**

- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) — `SpecGenerationWorkflow.Rounds` ends with a new `gate` step backed by the LLM-driven `spec_gate` agent (`Agents: []string{"spec_gate"}`, `Budget: 5`). The gate's `Run` invokes the agent and parses a `ConvergenceVerdict`. The gate's `Spawn` reads the verdict and decides:
  - `verdict.Converged == true`: returns `(nil, nil, nil)` — workflow terminates as the queue drains. ProposedSpec is persisted to `.borg/spec/` via the existing post-workflow handler.
  - `verdict.Converged == false` AND `iteration_index < budget`: returns the next iteration's quartet via a new `convergenceLoopTemplate` helper, with edges threading from the gate's output to each new step's expected predecessor. The verdict's `open_dimensions` are merged into `state.Concerns` so the next revise pass has them.
  - `verdict.Converged == false` AND `iteration_index >= budget`: returns a single terminal `convergence_failed_iter:N` step whose `Run` writes a DJ-103 history event tagged `convergence_failed` (carrying the unresolved-concerns list, the gate's verdict reasoning, and a JSON snapshot of the in-progress ProposedSpec) and then returns a non-nil error. The error propagates through the executor; `GenerateSpec` returns it; the calling command exits non-zero. **Nothing is persisted to `.borg/spec/`.**
- New [internal/scaffold/agents/spec_gate.md](../../internal/scaffold/agents/spec_gate.md) — LLM agent definition. Reads the assembled ProposedSpec, GOALS.md, the four-lifecycle-phases convergence framing inherited from [spec_scout.md](../../internal/scaffold/agents/spec_scout.md), and any open concerns from `state.Concerns`. Returns a structured `ConvergenceVerdict`. Follows the conventions in [docs/agent-conventions.md](../../docs/agent-conventions.md): positive phrasing, schema-tag constraints, no anti-pattern lists; `thinking: on` because the YES judgment is qualitative.
- [internal/agent/schemas.go](../../internal/agent/schemas.go) — new `ConvergenceVerdict` Go struct + `RegisterSchema` call with descriptive (non-placeholder) example payload. Field tags per project CLAUDE.md rule: `Converged bool` with description naming the YES question; `Reasoning string` with `jsonschema:"description=Two to three sentences naming which of define/develop/deploy/support are addressed and which remain underspecified. Names specific deliverables and axes, not generic claims."`; `OpenDimensions []string` with `jsonschema:"description=Each entry names a specific axis still unresolved — e.g., 'deployment cadence for the iOS app'; 'OTA update channel for firmware'; 'cost ceiling for the cloud backend'. Empty when Converged is true."`.
- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) — new helper `convergenceLoopTemplate(state *PlanningState) func(executor.IterationContext) []executor.Step` returning the four nodes (`revise`, `reconcile`, `critique`, `gate`) with iteration-aware IDs and the gate carrying its `Budget` forward to the next iteration.
- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) — new merge helper `mergeGateVerdict(s *PlanningState, results []RoundResult)` parses the `ConvergenceVerdict` and merges `OpenDimensions` into `state.Concerns` (tagged with `AgentID: "spec_gate"`, `Severity: "high"`) so the next revise pass sees them.
- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) — the original `cluster_findings` step stays in the initial graph (it runs once before the first gate; subsequent iterations don't need to re-cluster from scratch — the iteration template's `revise` step uses the existing `state.FindingClusters` plus any new entries merged in from the gate's verdict).
- [internal/agent/specgen.go](../../internal/agent/specgen.go) — env override `LOCUTUS_SPEC_GEN_MAX_ITERATIONS` parsed at workflow construction; overrides the gate step's `Budget` when set. Default budget stays on the gate step literal (5).
- [internal/agent/workflow_spec_generation_test.go](../../internal/agent/workflow_spec_generation_test.go) — new tests:
  - `TestSpecGenConvergesViaGateVerdict` — fake `spec_gate` returns `Converged: true` on iteration 1; workflow terminates after one critique pass; ProposedSpec written to `.borg/spec/`.
  - `TestSpecGenConvergesAfterRevise` — fake `spec_gate` returns `Converged: false` with one `open_dimensions` entry on iteration 1, `Converged: true` on iteration 2; workflow terminates with the iter-2 ProposedSpec; the open dimension surfaced as a Concern feeding the iter-2 revise.
  - `TestSpecGenFailsOnBudgetExhaustion` — fake `spec_gate` always returns `Converged: false`; workflow run **errors out** after `Budget` iterations; the error message names the iteration count and unresolved-concern count; DJ-103 history event tagged `convergence_failed` is written with verdict reasoning and ProposedSpec snapshot; `.borg/spec/` is **not** written.
  - `TestSpecGenIterationEventsAreObservable` — events for at least one mid-loop iteration carry the correct `IterationIndex` and `TemplateID`.
- Golden-file updates in any existing spec-generation tests whose trace structure changes (the new `gate` step will appear at the end of the round list).

**Verification:** spec council tests green; manual smoke run `locutus refine goals` against a real `.borg/` produces a sensible iteration count, the spinner output (even pre-renderer) shows the loop happening, and a goal that should converge does (or hits budget visibly when it shouldn't).

**Estimated:** 4-5 hours. This is the largest phase and the proving ground for DJ-122's motivation.

## Phase 6 — Static workflows migrate to the new executor

**Goal:** `PlanningWorkflow` and `AssimilationWorkflow` run on the new executor unchanged. No spawners. This is the verification phase — confirms the new executor preserves behavior for static workflows.

**Files expected to change:**

- [internal/agent/planner.go](../../internal/agent/planner.go) — verify the workflow value compiles and runs against the new executor. No semantic changes expected.
- [internal/agent/assimilation.go](../../internal/agent/assimilation.go) — same.
- `cmd/plan_test.go`, `cmd/assimilate_test.go` — continue passing without modification.

**Verification:** `go test ./... -count=1 -race` — full suite green.

**Estimated:** 1 hour (mostly verification).

## Phase 7 — Documentation + cleanup

**Goal:** README, CLAUDE.md, and DJ-122's status are updated. Plan marked DONE.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-122 status field flips from `designed` to `shipping`.
- [CLAUDE.md](../../CLAUDE.md) — references to the executor / workflow shape updated if any are now stale.
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — if any prompt-side guidance shifted (e.g., a future agent-collapse DJ will need this, but for now the existing prompts don't change), note. Likely no change in this phase.
- This plan file marked DONE.

**Verification:** full `go test ./... -count=1 -race` green; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 2-3 days single-stranded.

## Pointers a fresh session should follow before resuming

1. Read [DJ-122](../../docs/DECISION_JOURNAL.md#dj-122-graph-mutation-workflow-executor-with-spawner-nodes-supersedes-dj-112-on-control-flow-topology) in full. It's the authoritative design; this plan is progress tracking.
2. Read the predecessor chain: [DJ-112](../../docs/DECISION_JOURNAL.md#dj-112-workflows-move-from-external-yaml-to-go-values-supersedes-dj-036-on-workflows) (the topology decision DJ-122 supersedes) and [DJ-084](../../docs/DECISION_JOURNAL.md#dj-084-dominikbraungraph-is-the-canonical-graph-library-spec-and-executor-share-it) (the graph library both layers share).
3. Read [internal/executor/dag.go](../../internal/executor/dag.go) end-to-end before touching it. The up-front DAG validation in `buildStepGraph` is load-bearing and stays — the change is that `executeOnce` re-runs `g.PredecessorMap()` after each step that emits spawns, and `dgraph.AddVertex` / `AddEdge` enforce uniqueness and cycle-prevention incrementally.
4. Read [internal/agent/workflow.go](../../internal/agent/workflow.go) and [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) — Phase 4 touches the wrapper and Phase 5 is the first real consumer.
5. Don't relitigate DJ-122's design decisions unless surfacing a reason in chat first. The spawner-node model (no native back-edges), the bounded graph-size cap, the iteration-metadata-in-node-IDs convention, and the deferred CLI visibility work are committed. The three open questions named at the top of this plan (Go-only vs LLM gate, budget default, force-converged behavior) are flagged for chat-level resolution before Phase 5, not for ad-hoc decision during implementation.
6. Per the no-back-compat-until-self-hosting posture, this is a flag-day rewrite. Don't add migration shims for the old `Config[S].Converged` / `MaxIterations` fields — they're removed in Phase 1 along with their consumers' migration to the new shape. Existing tests rewrite together with the executor.
7. Phase 5's `spec_gate` agent prompt is a deliberate design — it must read the four-lifecycle-phases YES question (define / develop / deploy / support, aligned with GOALS.md) and grade qualitatively, not mechanically. Read [internal/scaffold/agents/spec_scout.md](../../internal/scaffold/agents/spec_scout.md) before authoring the gate prompt to keep the framing consistent. The gate is the back-half complement to the scout: scout surfaces what needs committing-to; gate checks whether the architect did so. Both prompts use the same lifecycle vocabulary.
