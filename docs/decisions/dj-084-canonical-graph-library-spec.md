## DJ-084: `dominikbraun/graph` Is the Canonical Graph Library; Spec and Executor Share It

**Status:** shipped

**Decision:** Both `internal/spec` (the spec dependency graph) and `internal/executor` (the runtime DAG) hold their graph structures in [`github.com/dominikbraun/graph`](https://github.com/dominikbraun/graph). Cycle detection, predecessor and adjacency lookups, and topological sort all delegate to the library. Hand-rolled equivalents are not allowed.

**What runs in our code instead:**

- Runtime semantics — `RunStep`, `Merge`, `Snapshot` callbacks; the convergence loop; conditional steps; the parallel/sequential split per wave; `MaxConcurrency` and `TypeLimits` enforcement; the events channel.
- Domain semantics — what a vertex *means* (a `Step`, a `Feature`, a `Decision`), what an edge *means* (dependency, parent-child), how nodes are persisted.

The graph library is an implementation detail of the storage and traversal layer, not a leaky abstraction the runtime is built around.

**Why one library, not two (or one library + a hand-rolled twin):** before this refactor, `internal/spec/graph.go` already used the library, but `internal/executor/dag.go` had its own ~150 lines of "track a `completed` map, scan for steps whose `DependsOn` are all in `completed`" wave scheduling. That code worked, but it duplicated graph machinery the spec graph wasn't reinventing — and crucially, it surfaced cycles mid-run as a generic "deadlock: N incomplete" error rather than at config time with the cycle's vertices named.

The previous defense ("the executor is execute-and-mutate, not a query structure, so a graph library doesn't help") conflated two layers. The graph itself is a passive structure either way; what makes the executor a runtime is the callback layer, the scheduling policy, and the convergence loop — none of which the library is asked to provide.

**Concrete wins from sharing the library:**

- Cycle detection at config time, not as a runtime symptom. The error surface goes from "deadlock: 3 incomplete" to "dependency cycle reaches step X via Y."
- Duplicate step IDs and edges to undeclared steps are caught at the same checkpoint, courtesy of `dgraph.PreventCycles` + a small upfront validation pass.
- Future improvements to the library (faster topological sort, deterministic ordering via `StableTopologicalSort`, memory layout work) land in both the spec graph and the executor without further effort.
- Less hand-coded graph bookkeeping to audit. The wave-selection function (`readySteps`) is now ~10 lines that filter `PredecessorMap()` against a `completed` set.

**What the library is NOT asked to do:**

- Express runtime parallelism or scheduling policy. Both stay in our code.
- Hold typed state. The `State[S]` parameterization is independent of the graph.
- Drive the convergence loop. That's a callback in `Config[S]`.

**Reversal criteria:** DJ-084 reverses only if `dominikbraun/graph` makes a breaking change we can't follow, or if a graph-shape requirement emerges that the library can't express (e.g., weighted edges for prioritization, or hyperedges for grouped dependencies). Neither is currently in sight; the library has been stable and our usage is mainstream.

**Note on DJ-029:** DJ-029's "custom orchestration, ~350 LOC, not a generic framework" framing remains correct for the *runtime semantics* layer — that's still the executor's identity. DJ-084 specifies that within that boundary, graph storage and traversal are library-backed; "custom orchestration" never meant "custom graph data structure."
