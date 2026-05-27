## DJ-122: Graph-Mutation Workflow Executor with Spawner Nodes (Supersedes DJ-112 on Control-Flow Topology)

**Status:** superseded by [DJ-135](dj-135-multi-runtime-pivot.md) on 2026-05-25 — the WorkflowExecutor + spawner-node design retires entirely; activity dispatch via ACP + per-project MCP singleton replaces the Go-encoded graph-mutation runtime. The `internal/agent/workflow*.go` files this DJ shipped were deleted in DJ-135 Phase 5. Prior status: shipping (Phases 1-7 landed 2026-05-14).

**Context.** [DJ-036](dj-036-council-agents-workflow-dag.md) committed externally-edited workflow YAMLs to support user customization without recompiling. [DJ-112](dj-112-workflows-move-from-external.md) walked that back when the customization affordance went unused in practice, moving workflows to Go values while preserving the DAG executor's separation of "graph topology" from "control flow." DJ-112 codified the explicit rule: *"sub-graph loops, when needed, can be expressed in Go control flow rather than as cyclic dependencies."*

That rule was honest at the time. With YAML in the picture, keeping the graph acyclic kept the file format intelligible to a human editor. With workflows now in Go (per DJ-112), the audience the acyclic-graph property protected is gone — there's no edit-time reader the constraint serves.

Three forces converge against the acyclic-only stance now:

1. **The spec council needs a real convergence loop.** Per the [spec_scout rewrite](../internal/scaffold/agents/spec_scout.md), the spec generation council is iterating toward a YES answer to "do we have enough committed-to information to *define*, *develop*, *deploy*, and *support* every deliverable while aligning with `GOALS.md`?" A gap-finder critic emits implicit-assumption gaps; the architect commits values; the gate re-checks. This is a first-class loop, not retry-around-a-DAG. Today it would have to be a Go `for` outside `executor.Run`, with the entire DAG re-running every iteration even though most early nodes (scout, outline) do not need to re-execute.

2. **Conditional routing wants to be in the graph.** The same gap-finder pattern: an infra gap should route to `devops_critic`; a support gap should route to `sre_critic`; no gap should route to nobody. With control flow outside the executor, each critic runs every iteration regardless, gated by per-step `Condition` predicates that fire in parallel and waste calls. With routing inside the graph, only the relevant critic spawns at all.

3. **The agent set carries draft-vs-refine duplication that the topology is masking.** The 40+ agent prompt files under `internal/scaffold/agents/` differ primarily in whether they produce an initial draft or refine a draft against critic concerns. In a DAG-only model, these have to be separate agents wired into different rounds. In a model where the loop is first-class, "refine" is the same agent invoked with `iteration_index > 0` and `concerns` populated — one agent file, one node type, two invocation modes selected by input state.

**Why generic workflow engines avoid cycles** (and why their reasoning does not fully apply to Locutus):

The strict-DAG industry norm exists for honest reasons: a DAG always terminates; topological sort gives parallel-scheduling for free; durable engines (Temporal, Cadence) replay event histories deterministically; DAG UIs (Airflow, Argo) visualize cleanly because every execution corresponds to exactly one graph node; state semantics ("what was the input when this node ran?") are well-defined per node. Cycles break all five. So engines that serve heterogeneous users push loops to the host language, where the user's domain knowledge fills in the semantics the engine can't validate.

Locutus's workflow is *domain-specific* and *in-process*: the loop condition (the YES test) is known to the engine; iteration state is well-defined (the spec proposal evolves, accumulated concerns thread through); we do not replay across process boundaries; the audience for graph visualization is the same operator who will reason about iteration. The reasons strict-DAG is the right default for generic engines do not all apply here. But the design must still respect the ones that do (termination, observability), via specific commitments rather than by structural avoidance.

**Survey of existing libraries.** None of the battle-tested Go workflow libraries solve this:

- **Strict-DAG in-process libraries** ([Azure/go-workflow](https://github.com/Azure/go-workflow), [rhosocial/go-dag](https://github.com/rhosocial/go-dag), [d-tsuji/flower](https://github.com/d-tsuji/flower), [floxy](https://github.com/floxy-project/floxy)): all return cycle-detection errors at preflight. Their guidance to users is exactly DJ-112's: write loops as Go control flow outside the workflow.
- **Rule engines with DSL** ([rulego](https://github.com/rulego/rulego), [dagucloud/dagu](https://github.com/dagucloud/dagu)): re-introduce the DSL-as-config problem DJ-112 walked away from; dagu is actually a daemon (web server + scheduler + persistence). Wrong shape.
- **Distributed engines** (Temporal, Cadence, Hatchet, Argo): require their server. Violates Locutus's local-only posture. Notable that Temporal's *programming model* is the closest in spirit — workflow functions with native loops, materializing per-iteration activities as fresh execution-graph nodes — but the durable-replay machinery is the load-bearing reason it needs a server.

The build is small enough that maintaining our own beats integrating any of these.

**Decision.** Replace `internal/executor` with a graph-mutation executor on top of [`dominikbraun/graph`](https://github.com/dominikbraun/graph) (already a dependency per [DJ-084](dj-084-canonical-graph-library-spec.md)). Loops, fanouts, and conditional gates are all expressed as **spawner nodes** that mutate the graph during execution. The topology remains acyclic at any point in time — loops materialize as fresh subgraph copies depending on prior iterations' outputs. The cycle-avoidance reasons that genuinely apply to us (termination, observability) are addressed by explicit commitments below, not by structural prohibition.

The model has two node kinds:

- **Work nodes.** Execute a step; return output. Same shape as today's `executor.Step.Run`.
- **Spawner nodes.** Execute a step; *and* mutate the graph by appending new nodes/edges before returning. The new nodes flow into the executor's ready frontier on the next iteration of its scheduler loop.

The executor's main loop:

```
while ready_queue not empty or pending nodes exist:
  pop ready node
  run it
  record output
  if it appended nodes, recompute ready frontier
```

All higher-level constructs collapse to spawners:

- **Fanout** is a spawner that appends N work-node copies depending on itself, plus a merge node depending on all N. Topological sort returns the N at level X for free; `dominikbraun/graph`'s level traversal handles the rest.
- **Conditional gate** is a spawner that reads its predecessors' outputs and appends one of several alternative subgraphs.
- **Convergence loop** is a spawner that reads accumulated state, checks the test, and either appends nothing (workflow terminates as the queue drains) or appends the next iteration's subgraph plus a fresh convergence-gate node, with `iteration_index` baked into the new nodes' IDs.
- **Iteration limit** is enforced inside the loop spawner: `if iteration_index >= budget, append a terminal "budget-exhausted" node instead of iteration_{n+1}`. Same mechanism, no separate concept.

**Three design commitments worth calling out explicitly:**

1. **Hard graph-size cap in the executor itself, not just in spawner logic.** A buggy spawner cannot OOM the process. Belt-and-suspenders: the loop spawner enforces its iteration budget; the executor enforces a global node-count cap (default: 1000× initial graph size) and errors out if exceeded. A `WorkflowExecutor[State].MaxGraphMultiplier` knob lets per-workflow callers tune this. The error message names the most recently spawning node so operators have one place to look instead of an opaque hang.

2. **Iteration metadata threaded through node IDs and event records.** Spawned nodes carry `template_id` and `iteration_index` in their IDs (and in `Step.Metadata`, which already exists for the event sink). `locutus status` and the workflow event stream render "spec-gen council, iteration 3 of 5" rather than an opaque chain. Without this, observability degrades fast as loops grow — and observability is one of the cycle-avoidance reasons that genuinely applies to us, so the metadata pattern is non-optional.

3. **Spawn-from-template as a small primitive.** The spawner receives a `func(state State) []*Step` template closure and an updated state value; it calls `executor.AppendSubgraph(template, state)` to materialize. This keeps the "what gets spawned" logic adjacent to the workflow declaration (preserving DJ-112's Go-values pattern) rather than scattered across a registry.

**The convergence loop, end-to-end:**

```
Initial graph:
  scout -> outline -> elaborate -> reconcile -> critique -> gate

When `gate` runs:
  - Reads state.OpenConcerns (collected from critique step)
  - If empty (YES on define/develop/deploy/support) -> append nothing, terminate
  - Else if iteration_index >= budget -> append terminal "force-converged" node
  - Else -> append:
      revise{iter:n+1} -> reconcile{iter:n+1} -> critique{iter:n+1} -> gate{iter:n+1}
    with revise depending on the prior gate's predecessors so state threads correctly
```

`revise` is a node, not a separate agent type. Its `Run` invokes the same architect agent today's `revise` step uses, but as one node in the graph rather than a separate workflow phase.

**The draft-vs-refine collapse as a consequence.** Today's `spec_feature_elaborator` and the architect's revise pass are separate agents because the workflow phases force them to be. Under DJ-122, the elaborator is one node type. First-iteration nodes have empty `concerns` input and produce drafts; subsequent-iteration nodes have populated `concerns` input and produce refines. The agent prompt branches on this internally (one prompt with conditional rendering on `iteration_index > 0`, or two prompt sections selected by mode — design decision deferred to the agent-set consolidation DJ that DJ-122 enables).

**Alternatives considered:**

- **Status quo (DJ-112's "loops live outside the DAG").** Keeps the executor simple but pays for it everywhere else: every loop is a Go `for` somewhere, conditional routing is per-step `Condition` closures with no graph visibility, the draft-vs-refine agent duplication has no path to collapse. Acceptable when YAML was the constraint; not acceptable now. The constraint that justified DJ-112's choice has lapsed.
- **Native cyclic topology (back-edges as first-class graph constructs).** Honest about what we want, but loses topological scheduling for the subgraph inside a loop, needs iteration-aware semantics throughout the executor, and complicates visualization. Hits the cycle-avoidance reasons that genuinely apply to us. Spawner-nodes get the same expressiveness while keeping the per-instant topology acyclic — same outcome, fewer pain points.
- **Import [Azure/go-workflow](https://github.com/Azure/go-workflow) and layer loops on top.** Smallest dep surface among the in-process candidates (3 direct deps, MIT, Microsoft-maintained, active May 2026), with typed Input/Output, retry/timeout, sub-workflows, interceptors. But it is strict-DAG by design (`ErrCycleDependency` from preflight), and we would have to wrap it with an outer iteration loop — the same shape as today. Worth borrowing as ideas (typed Input/Output, retry/timeout/condition shape, interceptor pattern) but not as a dependency.
- **State-machine library** ([looplab/fsm](https://github.com/looplab/fsm), [qmuntal/stateless](https://github.com/qmuntal/stateless)). Models the convergence loop cleanly but loses the parallel-execution and fanout primitives that today's executor handles well. State machines and DAGs answer different questions; gluing them together is more work than this design.
- **Move to a durable workflow engine (Temporal).** Temporal's programming model is the closest fit (workflow functions with native Go loops, durable per-activity execution). But it requires the Temporal server process, which violates Locutus's local-only posture. Worth revisiting only if Locutus moves to a server-resident architecture for unrelated reasons.

**Consequences.**

- **Code:**
  - `internal/executor/executor.go` rewrites against the spawner-node model. The `Step` type gains an optional `Spawn func(ctx, state) ([]*Step, []Edge, error)` returned alongside `Run`'s output. The scheduler's ready-queue loop is reworked to recompute after each step that spawns.
  - `internal/agent/workflows.go`: the three council workflows (`PlanningWorkflow`, `AssimilationWorkflow`, `SpecGenerationWorkflow`) are rewritten as initial node sets + spawner-closure templates. The spec council in particular collapses the per-round phases into a single loop spawner.
  - `internal/dispatch/dispatcher.go`: the outer workstream DAG continues to use the executor; no spawners needed there yet (workstream dependencies are static, per [DJ-027](dj-027-hierarchical-plans-plan-plans.md) / [DJ-121](dj-121-adoption.md)). The dispatcher's interaction with the executor API changes when `executor.Step` does; otherwise unchanged.
  - `internal/scaffold/agents/`: the agent-collapse work (draft/refine unification) is a follow-up DJ, not done here. DJ-122 enables it; the actual prompt-file consolidation is its own design pass once we see what falls out.
- **User-visible:**
  - `locutus status` gains iteration awareness — "spec-gen council, iteration 3 of 5" — derived from the iteration metadata threaded through node IDs.
  - The workflow event stream emits a new `WorkflowEventKind` for `graph_mutated` so MCP clients and the CLI spinner can render progress in loops correctly. Existing `step_started` / `step_completed` events continue per node.
  - No CLI-level verb surface changes. Convergence loops and conditional routing are workflow-internal.
- **Migration:** per the no-back-compat-until-self-hosting posture, this is a flag-day rewrite. The three council workflows migrate together with the executor. Tests built against `executor.Step` literals are rewritten to the new shape. The DJ-112 reversal-criterion clause "(a) we ship a genuine third-party-workflow plugin model where users author topologies without recompiling" is not satisfied by DJ-122 — topologies remain Go values; what changes is whether the executor's *runtime semantics* admit graph mutation. DJ-112's no-YAML stance is preserved.
- **Observability:** the bounded-cap safety net errors visibly with a "graph size exceeded N nodes, runaway spawner suspected: <node-id>" message. Operators get one specific point to look at instead of an opaque hang or OOM.

**Reversal criteria.** Revert to DJ-112's "loops outside the DAG" model if:

- (a) iteration-metadata observability proves insufficient — operators report they cannot tell what iteration of which loop is running, and threading more metadata does not fix it. At that point the spawner abstraction has failed at the observability obligation that justified it, and the simpler model (one Go `for` per workflow, easier to reason about end-to-end) wins back.
- (b) the bounded-cap safety net produces false positives in real workloads (legitimate workflows hitting the cap repeatedly) and tuning the multiplier does not fix it. Indicates that loop bodies are larger than the spawner model anticipated; either the cap design or the topology choice is wrong.
- (c) durable replay becomes a requirement — at that point we adopt Temporal's programming model and accept the server dependency, and the in-process spawner executor becomes redundant.

**Reference.** Supersedes [DJ-112](dj-112-workflows-move-from-external.md) on the control-flow-topology axis (preserves DJ-112 on the no-YAML axis). Depends on [DJ-084](dj-084-canonical-graph-library-spec.md) for the graph library. Enables a follow-up DJ on agent-set consolidation (draft/refine collapse) and a follow-up gap-finder critic that uses conditional spawning to route concerns. Motivated by the [spec_scout](../internal/scaffold/agents/spec_scout.md) rewrite and its convergence-test framing.
