# Plan: Verb-Set Consolidation — Phase C (Adopt / Reconciler)

## Context

`adopt` is the single verb that closes the loop between spec and code. It is the reconcile loop from DJ-068 with the pre-flight protocol from DJ-071 and the cascade from DJ-069 wired in. Today none of this is implemented end-to-end — the state store *schema* exists, the dispatcher exists, the council exists, but nothing reads drift status from the state store, plans workstreams accordingly, runs pre-flight, or writes results back.

This phase is the biggest and most architectural work in the consolidation. It is worth its own plan.

## The reconcile loop (target behavior)

```
┌─────────────────────────────────────────────────────────────────┐
│ 1. Scan spec graph — compute current spec_hash per Approach     │
│ 2. Scan state store — load stored spec_hash + artifact_hashes   │
│ 3. Diff & classify:                                             │
│    - spec_hash differs  → drifted (forward drift)               │
│    - artifact_hash differs → out_of_spec (backward drift)       │
│    - no state entry → unplanned (first reconcile)               │
│    - missing tests  → failed                                    │
│ 4. Cascade: if any Decision changed, rewrite parent             │
│    Feature/Strategy present-tense; mark child Approaches drifted│
│ 5. Plan: for each non-live Approach, walk transitive deps,      │
│    topo-sort into Workstreams. Critic flags file overlap.       │
│ 6. Prereq gate: run CheckPrereqs; fail fast if unmet.           │
│ 7. Pre-flight: per workstream, run clarify-only agent call;     │
│    record ambiguities as `assumed` Decisions; cascade again.    │
│ 8. Dispatch: Dispatcher runs workstreams in worktrees.          │
│ 9. Supervise: existing supervisor loop (streaming, churn, etc.) │
│ 10. Verify: run tests per Approach; set status live / failed.   │
│ 11. Update state store with new spec_hash + artifact_hashes.    │
│ 12. Record history events throughout.                           │
└─────────────────────────────────────────────────────────────────┘
```

## Scope

In scope for Phase C:

- **Spec-hash diff** — the reconciler's classification step
- **Cascade write-back** — Decision change → parent rewrite + child drift
- **Transitive dep walk** in planner — expand from a seed node to full plan set
- **Pre-flight protocol** — clarify-only agent call, assumed Decisions
- **`adopt` CLI + MCP command** — wires everything together
- **Prereq gate** inside `adopt`
- **State store write-back** after dispatch/verify

Out of scope (Phase D or later):

- Deleting `check` command (Phase D; this phase moves the logic but keeps the CLI surface)
- Rich UI for reconcile progress (streaming MCP progress works today via the supervisor)
- Concurrency-limit refinement (the existing scheduler is used as-is; tuning is a separate concern)

## Principle: The reconciler is a function, not a long-lived process

`adopt` is invoked, runs to completion, exits. It does not daemonize. This matches DJ-068's "in-repo state store" model and the user's expectation that each invocation does a fixed amount of work and reports what it did.

Long-running supervision of agents *within* a single `adopt` run still happens (via the streaming supervisor), but the reconciler itself is a single pass.

## Part C1: Spec-hash computation

C1a. Add `spec.ComputeSpecHash(approach Approach, parent FeatureOrStrategy, applicable []Decision) string` — SHA-256 of the canonical JSON representation of the Approach plus its resolved parent and Decisions at synthesis time. This is the hash stored in `.locutus/state/<approach>.yaml:spec_hash`.

C1b. Add `spec.ComputeArtifactHashes(fsys, approach Approach) map[string]string` — walks `approach.Artifacts` (paths), returns path → SHA-256 of current file content. Missing files are reported as empty-string hashes so they distinguish from present-but-changed.

C1c. Both functions live in `internal/spec/hash.go` to keep them colocated with the node types they operate on.

C1d. Tests: against `MemFS` with known content → known hash. Changing any field of the Approach or any parent/Decision must change the hash.

## Part C2: Reconciler classification

C2a. Add `internal/reconcile/` package with:

```go
type Classification struct {
    Approach     spec.Approach
    Status       state.Status   // computed: planned|drifted|out_of_spec|live|failed|unplanned
    StoredHash   string          // from state store; "" if no entry
    CurrentHash  string          // freshly computed
    ArtifactDiff map[string]string  // path → "added"/"removed"/"changed"/"ok"
}

func Classify(
    fsys specio.FS,
    spec spec.Graph,
    store *state.Store,
) ([]Classification, error)
```

C2b. Classification rules (match DJ-068 exactly):

- No state entry → `unplanned`
- `StoredHash != CurrentHash` and artifacts unchanged → `drifted` (forward drift)
- `StoredHash == CurrentHash` and some artifact hash mismatched → `out_of_spec` (backward drift)
- `StoredHash == CurrentHash` and all artifacts match and tests passed last run → `live`
- Any other state (e.g., previously `failed`) is preserved from the state store

C2c. Tests: synthetic spec graph + synthetic state store; each classification branch covered.

## Part C3: Cascade write-back

C3a. Add `internal/cascade/` with one function:

```go
// Cascade rewrites parent Feature/Strategy present-tense statements and marks
// child Approaches drifted whenever a Decision is changed or removed. Returns
// the set of modified nodes for history recording.
func Cascade(
    ctx context.Context,
    llm agent.LLM,
    fsys specio.FS,
    changedDecisions []spec.Decision,
) (*CascadeResult, error)
```

C3b. Implementation:

1. For each changed Decision, walk `InfluencedBy` edges upward to find parent Feature/Strategy nodes.
2. For each parent: call the planner's "rewrite present-tense" prompt (a new prompt template in `internal/scaffold/agents/`), save the updated Feature/Strategy.
3. For each Approach under a rewritten parent: update its `spec_hash` against the new parent → mark `drifted` in the state store.
4. Record a `cascade` event in the historian per modified node.

C3c. The LLM call for present-tense rewriting is a narrow single-shot task (no council needed). One prompt, one response, confidence score, done.

C3d. Tests: MemFS spec with `Feature → Decision`, change Decision, assert Feature body was rewritten and Approach state flipped to `drifted`.

## Part C4: Transitive dep walk in planner

C4a. Extend `internal/spec/graph.go` with:

```go
// TransitiveDeps returns all dependency nodes reachable from seeds, filtered
// by a predicate (e.g., "non-live Approaches only"). Topologically sorted.
func TransitiveDeps(
    g Graph,
    seeds []string,
    predicate func(Node) bool,
) ([]Node, error)
```

C4b. Use it in the planner: when `adopt` hands a set of drifted/unplanned Approaches to the planner, the planner walks dependencies, collects non-`live` nodes, and feeds the full set into council deliberation.

C4c. The planner produces one or more Workstreams partitioning the dep set. Critic checks file overlap between Workstreams (DJ-030) — this logic exists but the planner doesn't currently call it; wire it up.

## Part C5: Pre-flight protocol

C5a. Add `pre_flight` as a new status in the state store lifecycle (Phase A / existing enum likely already has it; confirm).

C5b. Add `internal/preflight/` with:

```go
type Question struct {
    Text     string
    Location string  // Approach.ID or PlanStep.ID
}

type Resolution struct {
    Question    Question
    Answer      string
    Source      string  // "spec:<node-id>" or "assumed"
    DecisionID  string  // set if a new Decision node was created
}

func Preflight(
    ctx context.Context,
    llm agent.LLM,
    fsys specio.FS,
    ws spec.Workstream,
    maxRounds int,
) ([]Resolution, error)
```

C5c. Protocol:

1. Render the Workstream and its Approaches into a "clarify-only" prompt: the agent must respond with a JSON list of ambiguities or an empty list.
2. For each ambiguity:
   - Try to resolve against spec graph (Feature criteria, Decision rationale).
   - If no match, call a second agent to generate an assumption; create a Decision node with `status: assumed`, `confidence < 1.0`.
3. Append resolutions to Approach bodies before handing to the coding agent.
4. Bounded by `maxRounds` (default 3); if unresolved after the limit, remaining ambiguities become `assumed` Decisions automatically.
5. Every new Decision triggers a cascade (C3).

C5d. Tests: MemFS + MockLLM that returns canned ambiguities; assert `assumed` Decisions are created, Approach body is appended to, cascade runs.

## Part C6: The `adopt` command

C6a. `cmd/adopt.go`:

```go
type AdoptCmd struct {
    Scope  string `arg:"" optional:"" help:"Limit adoption to a specific spec node ID (default: all non-live)."`
    DryRun bool   `help:"Show the plan and prereq status; do not dispatch agents."`
    MaxConcurrent int `help:"Max concurrent agent sessions." default:"2"`
}

func (c *AdoptCmd) Run(cli *CLI) error
```

C6b. Execution:

1. Load spec graph, state store, LLM.
2. Run cascade for any pending Decision changes (recomputed from historian since last run).
3. Classify all Approaches (C2).
4. If `Scope` is set, filter to that subtree.
5. Run `CheckPrereqs` (B3a). Fail fast if unmet.
6. Plan Workstreams (C4).
7. If `--dry-run`: print plan + prereq status + pre-flight question count, exit.
8. Run pre-flight per Workstream (C5).
9. Dispatch (existing `dispatch.Dispatcher`).
10. After each Approach finishes: compute new artifact hashes, run tests, set status `live` or `failed`, write state store.
11. Record history events.
12. Print final summary.

C6c. MCP `adopt` tool with the same shape. Streams progress via existing MCP progress machinery (`internal/dispatch/monitor.go`).

C6d. Output format for CLI:

```
Reconciling 4 Approaches across 2 Workstreams:

Workstream ws-auth:
  ✓ login-oauth-approach        (drifted → live, 3 files)
  ✓ login-session-approach      (drifted → live, 2 files)
Workstream ws-ui:
  ✗ login-button-approach       (failed: test assertion failed)
  ⚠ login-error-banner-approach (out_of_spec: artifact diverged)

3/4 live, 1 failed. State store updated.
```

## Part C7: Handling `out_of_spec`

C7a. `out_of_spec` Approaches need human choice (DJ-068 lists three paths). `adopt` surfaces them via:

- Print a summary in the final report.
- Exit non-zero if any Approach is `out_of_spec` (adoption is incomplete).
- Provide hint: `locutus refine <approach-id>` to update the spec to match, or manual revert to re-adopt.

C7b. No auto-resolution. This is deliberately a human gate.

## Final Verification

```bash
go build ./...
go test ./...

# Integration smoke test (requires a test fixture repo):
cd /tmp/locutus-fixture
./locutus init
./locutus import --input feature.md
./locutus refine some-decision-id  # changes a decision, cascade fires
./locutus adopt --dry-run           # expect: drifted Approach, planned workstream
ANTHROPIC_API_KEY=$KEY ./locutus adopt  # dispatches, runs tests, updates state
./locutus status                    # expect: 1 live, 0 drifted
```

## Files Touched (expected)

**New:**
- `internal/spec/hash.go` — spec_hash, artifact_hashes
- `internal/reconcile/classify.go` — classification
- `internal/cascade/cascade.go` — Decision change propagation
- `internal/preflight/preflight.go` — clarify-only protocol
- `cmd/adopt.go` — command
- `internal/scaffold/agents/rewriter.md` — new agent prompt for present-tense rewriting
- `internal/scaffold/agents/preflight.md` — new agent prompt for ambiguity extraction

**Modified:**
- `internal/spec/graph.go` — add `TransitiveDeps`
- `internal/agent/planner.go` — consume dep-filtered seed set; call critic for file-overlap
- `cmd/cli.go` — register `AdoptCmd`
- `cmd/mcp.go` — register `adopt` tool
- `internal/state/state.go` — verify `pre_flight` status enum present (if not, add)

## Dependencies

- Depends on Phase A (LLM wiring, `refine` as a consumer of cascade).
- Depends on Phase B (`CheckPrereqs` factoring, `--dry-run` convention).
- Phase D deletes `check` and `diff` commands after this phase is proven stable.
- This is the phase that determines whether Locutus "works" end-to-end as designed.

## Risk / Scope Reality Check

The listed scope is ambitious. Realistic landing:

- C1 (hash) — 1 session
- C2 (classify) — 1 session
- C3 (cascade) — 1 session
- C4 (dep walk) — 0.5 session
- C5 (pre-flight) — 1 session
- C6 (adopt command) — 1 session
- C7 (out_of_spec surface) — 0.25 session
- Integration + smoke tests — 1 session

Total: ~6 focused sessions. Do not attempt in one sitting unless a part is cut.

A viable minimum viable Phase C: C1 + C2 + C4 + C6 without cascade, pre-flight, or out_of_spec. That gives you `adopt` that handles forward drift (spec changed, code needs regeneration) in the common case, missing the subtler cases. Decide whether to scope down before starting.
