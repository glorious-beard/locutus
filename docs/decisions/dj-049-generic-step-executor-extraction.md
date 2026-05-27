## DJ-049: Generic Step Executor Extraction

**Status:** shipped

**Decision:** Extract a generic `internal/executor` package that powers both the planning council workflow and workstream dispatch. Parameterized by a `State` type. Provides dependency-ordered execution, bounded parallelism via semaphores, per-type concurrency limits, snapshot isolation for parallel steps, optional convergence loop, and progress events via channel.

**Why:** The planning council DAG and the Tier 7 dispatch DAG are the same pattern with different payloads. Rather than duplicate coordination logic, extract it once and let callers provide typed state and a `RunStep` function. The planning workflow wraps it as `WorkflowExecutor[PlanningState]`; the dispatcher wraps it as `executor.Executor[dispatchState]`.

**Alternatives considered:** Keep two separate implementations (planning-specific + dispatch-specific), or adopt a larger agent-framework dependency (Eino, CrewAI-equivalent). Rejected the first as duplication. Rejected the second as overkill — the primitive is ~200 lines of Go with generics.
