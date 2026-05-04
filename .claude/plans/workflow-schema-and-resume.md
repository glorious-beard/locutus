# Workflow Schema Cleanup + Resumable Sessions

Two related-but-separable changes to the council workflow. Both target
the discomfort the user surfaced 2026-05-04: the workflow YAML accumulates
machine-friendly cruft (`parallel`, `depends_on`, dotted `fanout` paths)
that buries the actual execution story, and a 25-minute spec-gen run that
dies on call 14 of 18 burns all completed work.

This plan rejects the Temporal/Restate alternative (architectural mismatch
for an in-process CLI tool, hard Docker prereq is a steep DX tax). Stays
on the existing executor; the changes are in the loader and the session
layer.

## Phase A — Phases-with-jobs YAML schema

### Discomfort being addressed

Reading [internal/scaffold/workflows/spec_generation.yaml](internal/scaffold/workflows/spec_generation.yaml)
top-to-bottom, the **execution story** is hidden behind three pieces of
machinery that don't belong in the user's mental model:

1. `parallel: true` is mostly noise. The executor already concurrently
   runs ready steps up to `MaxConcurrency` / `TypeLimits`. The YAML flag
   is redundant.
2. `depends_on: [previous_step]` repeats on 11 of 12 steps. It's a
   linked list dressed as a DAG.
3. `fanout: revision_plan.feature_revisions` is a dotted state path that
   reads like data binding. The user has to know which fields on
   PlanningState are slices.

### Target shape

A **phase** is a named step in the user's mental model. A phase contains
one or more **jobs** (agent calls or fanouts) that run concurrently
within the phase. Phases run sequentially top-to-bottom.

```yaml
phases:
  - id: survey
    job: spec_scout

  - id: outline
    job: spec_outliner

  - id: elaborate
    jobs:
      - {agent: spec_feature_elaborator, for_each: outline.features}
      - {agent: spec_strategy_elaborator, for_each: outline.strategies}

  - id: reconcile
    job: spec_reconciler

  - id: critique
    jobs: [architect_critic, devops_critic, sre_critic, cost_critic]

  - id: triage
    when: has_concerns
    job: spec_revision_triager

  - id: revise
    when: has_concerns
    jobs:
      - {agent: spec_feature_elaborator, for_each: revision_plan.feature_revisions}
      - {agent: spec_strategy_elaborator, for_each: revision_plan.strategy_revisions}
      - {agent: spec_feature_elaborator, for_each: revision_plan.additions.features, when: has_additions}
      - {agent: spec_strategy_elaborator, for_each: revision_plan.additions.strategies, when: has_additions}

  - id: reconcile_revise
    when: has_concerns
    job: spec_reconciler

max_iterations: 1
```

### What's gone, what's renamed, what stayed

- **Gone:** `parallel`. Inferred from being a sibling job in a phase.
- **Gone:** `depends_on`. Inferred from phase order.
- **Renamed:** `fanout` → `for_each`. Same semantics, more intuitive
  (matches GitHub Actions matrix, Ansible loops). The dotted path stays
  because the items genuinely come from runtime state.
- **Renamed:** `conditional` → `when`. Aligns with GitHub Actions / GitLab CI.
- **Renamed:** `rounds` → `phases`. The old name was a relic of when the
  whole workflow re-iterated each round; with sub-DAG iteration not yet a
  feature, "phases" reads cleaner.
- **Renamed:** `max_rounds` → `max_iterations`. Same.
- **Kept:** `merge_as` (still needed for state-merge dispatch).
- **Kept:** `id` per phase and per job.

### Singleton vs multi-job phases

- Singleton: `job: <agent_name>`. One agent, no fanout. Compiles to one
  executor.Step.
- Multi-job: `jobs: [...]`. Each entry is either a string (agent name)
  or a struct (agent + for_each + when). Compiles to N executor.Steps,
  all with the same DependsOn (the previous phase's join point) and
  Parallel=true.
- Phase-level `when:` applies to every job in the phase. Job-level
  `when:` overrides for finer control (used by the additions jobs in
  the revise phase).

### Loader — implementation strategy

Phase-shaped YAML compiles down to the existing `[]WorkflowStep`. No
executor change. The loader is where the sugar lives.

Compilation rules:

1. Each phase gets a synthetic **join step** — an empty step that all
   jobs in the next phase depend on. (Or: every job in phase N depends
   on every job in phase N-1. Simpler, no synthetic step.) Pick the
   simpler rule unless it explodes the DAG fanout.
2. Each job becomes one `WorkflowStep` with:
   - `ID = "<phase_id>.<job_index>"` for unnamed jobs, or `"<phase_id>"`
     for singleton phases.
   - `DependsOn = [all jobs in previous phase]`.
   - `Parallel = (len(phase.jobs) > 1)`. Inferred.
   - `Conditional = job.When ?? phase.When`.
   - `Fanout = job.ForEach`.
3. `merge_as` is auto-derived from the agent's role (architect →
   raw_proposal, reconciler → reconciled_proposal, etc.) **OR** declared
   explicitly per job when ambiguous. Keep this minimal; current YAML
   has 12 explicit `merge_as` entries that are mostly mechanical.

### Backwards compatibility

The user said earlier the project isn't even in alpha. **Do not write a
legacy fallback.** Migrate spec_generation.yaml to the new shape in the
same PR; delete the old WorkflowStep type or rename it to `Job`.

### Tests

- Loader test: parse the new YAML, compile to `[]Job`, assert the
  expected DAG shape (which jobs depend on which).
- Equivalence test: load the OLD spec_generation.yaml and the NEW one,
  assert they compile to the same DAG. (Then delete the old yaml.)
- Snapshot tests: re-run prompt snapshot tests; rendered prompts shouldn't
  change because projections key on stepID base, not on file shape.
- Workflow execution test: smoke test against an in-memory LLM stub that
  the new shape executes phases in order and parallelizes jobs within a
  phase.

### Files

- [internal/agent/workflow.go](internal/agent/workflow.go): rename
  `WorkflowStep` → `Job`; rename `Workflow.Rounds` → `Workflow.Phases`;
  add `Phase` type; new `LoadWorkflow` parses phases shape.
- [internal/scaffold/workflows/spec_generation.yaml](internal/scaffold/workflows/spec_generation.yaml):
  rewrite in phase shape.
- [internal/agent/projection.go](internal/agent/projection.go): no
  changes (projection keys on stepID base, which the new compiler still
  emits as `<phase_id>` or `<phase_id> (<fanout_item>)`).

## Phase B — Resumable session traces

### Discomfort being addressed

A 25-minute spec-gen run dies on call 14 of 18 (LLM rate-limit, network
blip, OOM). Currently the next run starts from scratch — 13 completed
calls of work discarded. The trace files at
`.locutus/sessions/<sid>/calls/` already capture every result; all that's
missing is a resume path.

This is option (5) from the Temporal discussion. It buys ~80% of
Temporal's resume-from-failure value with zero new dependencies.

### Identity hash

A session is resumable iff:

1. The workflow YAML hash matches.
2. The agent .md hashes match.
3. The user-supplied input (GOALS.md path + content hash, or `import`'s
   document hash) matches.

If any of those drift, the session is no longer replayable — refuse to
resume; offer to start a new session. This avoids replaying stale results
under a changed agent definition.

Hash storage: `.locutus/sessions/<sid>/manifest.yaml` already exists for
trace metadata. Add `inputs_hash`, `workflow_hash`, `agents_hash` fields.

### Resume protocol

On `locutus refine goals` (or `import <doc>`):

1. Compute identity hash from current workflow + agents + input.
2. Search `.locutus/sessions/` for an unfinished session whose manifest
   hash matches.
3. If found, prompt: "Resume session 20260504/1345? (Y/n)". `--resume`
   forces yes; `--no-resume` forces fresh start.
4. On resume, replay completed call results into PlanningState by
   walking `calls/*.yaml` in order and dispatching each through the
   merge handler for its step.
5. Compute the first incomplete phase/job (any job in a phase whose
   trace file is missing).
6. Continue execution from that job.

### Per-call cache lookup

Inside `executeAgent`:

```
key := sessionKey(state.SessionID, step.ID, snap.FanoutItem)
if cached, ok := loadCachedCall(key); ok {
    emitEvent(stepID, agentID, "resumed", "...")
    return cached
}
// otherwise call LLM, write result, return
```

Idempotency key shape: `<step_id>:<sha256(fanout_item_or_empty)>`. The
fanout-item hash distinguishes per-feature elaborator calls from each
other within the same step.

### What does NOT get cached

- Pre-merge hooks that read live FS state outside `.locutus/sessions/`
  (e.g. a tool call to `spec_list_manifest` reads the live spec). If
  the spec changed between runs, replaying the cached tool result is
  wrong.
- For now, scope resumability to spec-generation council runs against a
  fresh greenfield project. Resume against an evolving spec graph is
  out of scope; the manifest hash already excludes that case via the
  inputs hash.

### CLI surface

```
locutus refine goals                 # auto-detects resumable session, prompts
locutus refine goals --resume        # force resume; error if no match
locutus refine goals --no-resume     # force fresh; archives the unfinished session
locutus refine goals --resume <sid>  # resume a specific session by id
```

### Tests

- Identity hash test: changing agent .md / workflow yaml / input
  invalidates the session.
- Replay test: write a fake completed-calls directory, run with resume,
  assert no LLM calls happen for replayed steps.
- Mid-flow restart test: kill workflow at call 5 of 10, resume, assert
  remaining 5 calls fire and the final state matches a clean run.
- Stale-session test: hash mismatch refuses to resume.

### Files

- [internal/agent/session.go](internal/agent/session.go): add identity
  hash to manifest; add `loadCachedCall(sid, stepID, fanoutItemHash)`;
  add `findResumableSession(fsys, hash)`.
- [internal/agent/workflow.go](internal/agent/workflow.go): pre-call
  cache lookup in `executeAgent`; emit `"resumed"` lifecycle event so
  the spinner UI distinguishes cached steps from live ones.
- [cmd/refine.go](cmd/refine.go), [cmd/import.go](cmd/import.go):
  add `--resume` / `--no-resume` flags; resume-prompt logic.

## Sequencing

Phase A and Phase B are independent. Phase A is the bigger structural
change but lands cleanly in one PR (loader + YAML rewrite + tests).
Phase B is more contained but threads through more files (session,
workflow, two CLI verbs).

Recommendation: **Phase A first.** The current YAML's verbosity is a
permanent papercut that gets re-encountered every time the user reads
or edits a workflow. Phase B is a recoverable failure (re-run the
whole flow); the user already has trace files, so the work isn't lost,
just the wall-clock time. Better to fix the perpetual annoyance before
the periodic one.

If both ship, the resumable-session manifest hash gets computed against
the new phases-shaped workflow file, so Phase A landing first means
Phase B doesn't carry a transient hash-shape concern.

## Out of scope (for this plan)

- Sub-DAG iteration ("iterate critique → triage → revise → reconcile
  until critics are clean"). Discussed earlier; tracked separately.
- Forks/joins beyond the within-phase pattern. Unused in any current
  workflow; add an escape hatch if a real diamond appears.
- Workflow versioning (Temporal-style). Not needed — the manifest hash
  invalidates resume on any workflow change.
- Distributed execution. Locutus is a single-process CLI; this is
  explicitly out of scope.
