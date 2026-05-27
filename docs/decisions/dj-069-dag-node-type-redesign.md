## DJ-069: DAG Node Type Redesign — Goal / Feature / Strategy / Decision / Approach

**Status:** shipped

**Decision:** Replace the original `Feature → Decision → Strategy → Code` hierarchy with a redesigned DAG: `Goal → (Feature | Strategy) → Decision`, with a new `Approach` node as the synthesis layer handed to coding agents. The `Code` node type is removed entirely.

**The problem with the original hierarchy:** `Strategy` was doing two unrelated jobs — (1) high-level architectural and engineering excellence concerns (language choices, CI, observability, deployment), and (2) per-feature implementation approaches that refine a Feature against the active strategies. Conflating them made the graph ambiguous and artifact ownership unclear.

**Node roles:**

| Node | Role | Owns artifacts? | State derives from |
| --- | --- | --- | --- |
| `Goal` | High-level objective; anchor for Features and Strategies | No | Children |
| `Feature` | User-facing capability; present-tense statement | No (via Approach) | Its Approaches |
| `Strategy` | Architecture / engineering / production excellence; present-tense statement | No (via Approach) | Its Approaches |
| `Decision` | Assumption or architectural choice; leaf constraint; guides generation | No | N/A (active or removed) |
| `Approach` | Synthesis of a Feature or Strategy against applicable Decisions; implementation brief for the coding agent | Yes | Reconciler test results |

**Artifact ownership:** Only `Approach` nodes own artifact paths and participate in the `spec_hash` / `artifact_hash` reconciliation loop. Features and Strategies derive their state from their Approach children. Goals derive state from their Feature and Strategy children.

**Decisions are pure spec.** A Decision is a constraint that informs how a Feature or Strategy is stated and how its Approaches are generated. Decisions do not flow directly to the coding agent — they are already incorporated into the present-tense statements of the Feature or Strategy nodes above them. ADR documents generated from Decisions are part of the spec store, not the artifact store.

**Decision propagation:** When a Decision is revisited (updated or replaced), all parent Feature and Strategy nodes rewrite their present-tense statements to reflect the new decision. Any Approach nodes hanging off those parents are marked `drifted` and re-queued for reconciliation. There is no local-vs-global distinction: change the Decision, cascade to all parents, let the history agent record what changed. The spec graph always reflects current active intent.

**Decision lifecycle:** A Decision is either active (present in the graph) or it is removed. No `deprecated` status — a Decision that no longer applies simply ceases to exist. Its removal propagates to parents exactly as an update would.

**Feature deprecation:** When part of a Feature is deprecated, the deprecated portion is removed or marked `deprecated` with a pointer to its replacement nodes. Feature A rewrites its present-tense statement to drop the deprecated content. New Feature nodes (children of Feature A if the umbrella concept holds, or siblings under the same Goal if Feature A is being fundamentally replaced) are created and enter the normal `unplanned` lifecycle. The history agent records the deprecation rationale.

**Approach cardinality:** Each Approach has exactly one parent (a Feature or a Strategy). A Feature or Strategy may have multiple Approach nodes (one per distinct implementation concern). This keeps artifact ownership unambiguous and cascade behavior simple. Shared implementation concerns do not live in a shared Approach — they surface as a top-level Strategy with its own Approach.

**Graph shape (actual DAG, not a tree):**

```text
Goal
├── Feature A  (present-tense; rewrites when child Decisions change)
│   ├── Decision X  ◄──── also a child of Strategy B
│   ├── Decision Y
│   ├── Approach A1  (owns src/feature-a/part1.go + tests)
│   └── Approach A2  (owns src/feature-a/part2.go + tests)
└── Strategy B  (present-tense; rewrites when child Decisions change)
    ├── Decision X  (same node as above — true DAG edge)
    └── Approach B1  (owns .github/workflows/ci.yml, Dockerfile)
```

Decisions are shared DAG nodes (N parents). Approaches are owned leaf nodes (1 parent). Goals and Features/Strategies are interior nodes with derived state.

**Why remove the `Code` node type:** `Code` as a spec node added a layer of granularity (specific files or functions as reconciliation targets) that belongs to the artifact store, not the spec graph. The spec describes *what* and *why*; the artifact paths on an Approach describe *where* it lives. A separate `Code` node conflated spec intent with implementation detail.

**Rationale for the Approach node:** Without it, the coding agent would receive raw spec nodes (Feature/Strategy) and raw Decision constraints, and would have to perform the synthesis itself on every invocation. The Approach node externalizes that synthesis as a first-class spec artifact — it can be versioned, reviewed, and re-generated independently of its parent Feature or Strategy. It also provides a clean reconciliation target: the Approach is what drifts, not the Feature.

**Approaches are denormalized by design.** The council/planner agents read the full spec graph (parent Feature or Strategy, all applicable Strategies, all relevant Decisions) and produce a self-contained markdown synthesis stored in the Approach's `Body` field. The coding agent receives the Approach and needs nothing else from the spec graph — it does not resolve the parent Feature, look up Decisions, or consult Strategy commands at runtime. The `Decisions []string` field on the Approach is an audit trail of which decisions were consulted during synthesis, not a pointer the agent follows.

The execution layers map as follows:

- **Approach** (spec layer) — durable, versioned synthesis of "what to build and why"; owned by the spec graph; participates in reconciliation lifecycle
- **PlanStep** (plan layer) — ephemeral execution instruction generated from an Approach at plan time; includes inlined file context, skills, and assertions; consumed directly by the coding agent
- **Workstream** (session layer) — groups PlanSteps for a single agent session; supervised by Locutus; one workstream covers N Approaches

The same Approach generates different PlanSteps on successive planning runs (brownfield context, existing files, and current codebase state all affect what the PlanStep instructs). The Approach itself only changes when the spec drifts.

**Philosophical grounding:** Locutus is built on the premise that requirements are vague and will remain so — especially in startup contexts where a week-old requirement may already be stale. The node design reflects this: Features and Strategies are living present-tense statements that absorb Decision revisions rather than accumulating stale history. The spec graph always represents current intent. Assumptions are captured explicitly as Decisions, not buried in prose. The freedom to revisit any Decision at any time, with automatic cascade, is the mechanism that keeps the spec honest without requiring perfect requirements upfront.
