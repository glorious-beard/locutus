## DJ-060: Dispatcher Uses Executor, Steps Within a Workstream Use a For-Loop

**Status:** shipped

**Decision:** The outer workstream DAG uses `executor.Executor[dispatchState]` for dependency ordering, parallel execution, and per-agent concurrency limits. The inner step iteration within a single workstream is a plain `for` loop in `Dispatcher.runWorkstream`.

**Why:** Workstream-level parallelism makes sense (different workstreams run in different worktrees, different agent sessions). Step-level parallelism within a workstream does not — all steps share one worktree and one agent session, so parallel execution would cause git state conflicts and session state chaos. The for-loop correctly models this sequential reality.

**What this means for `PlanStep.DependsOn`:** The field exists but its job is plan-time ordering validation (making sure `Order` is consistent with declared dependencies), not runtime parallelism enforcement.

**Resisted temptation:** Nesting a second executor inside `runWorkstream` for "symmetry." Rejected as over-abstraction — the executor adds value where it eliminates duplication, not where it just looks consistent.

---

Session date: 2026-04-17 to 2026-04-18 (streaming supervision build-out)
