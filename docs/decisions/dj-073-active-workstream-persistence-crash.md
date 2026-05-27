## DJ-073: Active Workstream Persistence for Crash Recovery

**Status:** shipped

**Decision:** Refine DJ-069's "ephemeral PlanStep" claim: the full in-flight dispatch — MasterPlan plus all its ActiveWorkstreams — is **ephemeral at planning time** (not cached between `adopt` invocations; each plan run regenerates the plan tree from the current Approaches + codebase state, per DJ-069's original rationale) but **persistent during execution**. Once the planner hands a MasterPlan to the dispatcher, both the plan itself (for its InterfaceContracts, GlobalAssertions, and workstream dependencies per DJ-027) and each ActiveWorkstream (for its PlanStep DAG, agent session ID, per-step status) are written under `.locutus/workstreams/<plan-id>/` and kept until every Approach the plan covers reaches `live` (archive) or upstream drift invalidates the plan (delete + re-plan).

**Why the refinement is needed:**

DJ-069 framed PlanSteps as ephemeral to justify regenerating them against current codebase state on every plan run. That rationale holds for the **plan → dispatch** transition: the same Approach can produce different PlanSteps on different days because brownfield context changes. It does not address what happens once a workstream is dispatched and the coding agent is actively executing.

Without persistence, a crash — agent death, Locutus death, machine reboot — forces the next `adopt` invocation to choose between:

1. Treating an `in_progress` entry as drift and replanning from scratch (wastes agent work).
2. Treating it as live (wrong; nothing completed).
3. Resuming — but from what? The plan was never written down.

Option 1 wins by default and is fine for trivially small Approaches but unacceptable once an Approach spans multiple files and tests (which is the common case).

**Persistence mechanics:**

- **Location:** one subdirectory per active plan under `.locutus/workstreams/<plan-id>/`, containing `plan.yaml` (the MasterPlan as a `PlanRecord`) and one `<workstream-id>.yaml` (an `ActiveWorkstream`) per dispatched workstream. Whole-plan cleanup removes the subdirectory.
- **Gitignored, not committed.** The directory layout mirrors `.locutus/state/` (DJ-068 pattern) but the contents are transient coordination state — deleted on terminal transition, mutated on every PlanStep completion, invalidated on drift. Committing step-by-step progress would produce diff noise without adding anything a fresh clone could use; a new checkout correctly sees no active workstreams and proceeds as "nothing in flight." This is a narrow departure from DJ-068's "state is always in-repo" framing: durable reconciliation state is committed; execution coordination is not.
- **Shape of `ActiveWorkstream`:** embeds `spec.Workstream` verbatim, adds `PlanID string` (join back to the owning plan), `ApproachIDs []string` (which Approaches this workstream covers), `AgentSessionID string` (for coding-agent `--resume`), `PreFlightDone bool`, `StepStatus []StepProgress`, and `CreatedAt` / `UpdatedAt` timestamps.
- **Shape of `PlanRecord`:** wraps `spec.MasterPlan` (carrying `InterfaceContracts`, `GlobalAssertions`, workstream `DependsOn` graph, and the trigger prompt per DJ-027) plus its own `CreatedAt` / `UpdatedAt`. The MasterPlan must persist — those cross-workstream fields only live on the plan, not replicated onto individual workstream records, so losing the plan would forfeit the coordination data a resume run needs.
- **Write points:**
  - On dispatch: planner mints `PlanID` and one `WorkstreamID` per workstream; `SavePlan` writes the MasterPlan once, then one `Save` per workstream as each is handed to the dispatcher. `ReconciliationState.WorkstreamID` is stamped on each covered Approach's state entry.
  - On each PlanStep completion: dispatcher calls `Save` on the affected `ActiveWorkstream` (updated `StepStatus`).
  - On terminal transition (all Approaches the plan covers reach `live`, or the user aborts): `DeletePlan` removes the entire `<plan-id>/` subdirectory. `ReconciliationState.WorkstreamID` becomes historical (retained on state entries as audit trail, with no record to dereference).

**Resume protocol (next `adopt` invocation):**

1. `ListActivePlans` enumerates plan IDs that have an in-flight `<plan-id>/plan.yaml` marker.
2. For each active plan, load the `PlanRecord` and all its `ActiveWorkstream` records.
3. Classify every Approach across the plan's union of workstream coverage using DJ-068 rules.
4. Branch:
   - **All covered Approaches unchanged (`live`/`in_progress`, spec_hash matches):** resume. For each workstream, restart the coding agent with `--resume <AgentSessionID>` (streaming driver capability), skipping PlanSteps already marked `complete` in `StepStatus`. Inter-workstream dependencies come from the plan's `DependsOn` graph (now persisted).
   - **Any covered Approach has drifted:** invalidate the whole plan. `DeletePlan` removes the subdirectory; clear `WorkstreamID` on the affected state entries; proceed as a fresh plan run (new MasterPlan, new workstream IDs, fresh dispatches).
   - **All covered Approaches reached `live`:** archive. `DeletePlan` removes the subdirectory; `live` status on each `ReconciliationState` is the durable record.

**Why this does not contradict DJ-069:**

DJ-069's concern was that PlanSteps must be regenerated to reflect current codebase context during planning. That rationale still holds: the planner never reads `.locutus/workstreams/*` as input; nothing in the plan pipeline consults persisted PlanSteps to short-circuit regeneration. The persistence is of an **execution contract** — locked when the agent is told "here are your steps," invalidated the moment upstream drift is detected. DJ-069 is preserved; "ephemeral" is narrowed to "not cached across planning runs."

**Alternatives considered:**

- **Coarse resume (Approach-level only).** Treat the whole Approach as redo-on-crash; no persistent PlanSteps. Simple, no new file kind, but wastes the work of every completed step inside the crashed Approach. Rejected once we accept that Approaches are often multi-file / multi-test.
- **Ephemeral everything; rely on agent session resume alone.** Claude Code's `--resume` reopens a session by ID, so maybe persisting just the session ID on `ReconciliationState` is enough. Rejected: session resume brings the *conversation* back, not the *plan* — the agent wouldn't know which PlanSteps were already complete without the persisted step status map.
- **Persist PlanSteps on the Approach itself.** Rejected for the same DJ-068 reasons rehearsed in DJ-072's design discussion: mixes intent (Approach) with execution telemetry (step progress), produces spec diffs on every dispatch, and breaks `git revert` semantics on the spec.
- **Persist only the DAG, not per-step status.** Rejected: without step status, we cannot skip completed steps on resume, which is the whole point of persistence.

**Impact on other decisions:**

- **DJ-068 (manifest/state separation):** unchanged. `.locutus/workstreams/` is a new state-store directory alongside `.locutus/state/`, following the same in-repo-YAML pattern.
- **DJ-069 (node redesign):** clarified. "Ephemeral PlanStep" is now precisely "regenerated every plan run; persisted once dispatched; invalidated on upstream drift." The layer model (Approach / PlanStep / Workstream) and the denormalisation principle are preserved.
- **DJ-071 (pre-flight):** complementary. Pre-flight resolutions continue to land as `assumed` Decisions in the spec graph (durable regardless of workstream fate). A workstream record captures whether pre-flight has already run for that dispatch so resume doesn't re-run it.
- **DJ-027 (hierarchical plans):** unchanged. MasterPlan → Workstream → PlanStep remains the planner's output shape. MasterPlans themselves remain ephemeral (no file kind introduced for them yet) — only the subset of Workstreams actually being executed land on disk.
- **Streaming supervision plan** (`.claude/plans/streaming-supervision.md`): complementary. Intra-attempt retry (`Supervisor.Supervise` outer loop) and churn detection remain Locutus-internal; crash recovery across process deaths is what DJ-073 adds.

**Impact on code:**

- **New package `internal/workstream`:** `ActiveWorkstream` and `PlanRecord` types; `FileStore` constructed per plan with `SavePlan` / `LoadPlan` / `Save` / `Load` / `Walk` / `Delete` / `DeletePlan`; package-level `ListActivePlans(fsys, baseDir)` for the resume entry point. Mirrors `internal/state`'s shape for per-entity YAML but nested per plan.
- **New status enum `StepExecutionStatus`:** pending / in_progress / complete / failed, tracked per PlanStep inside `ActiveWorkstream.StepStatus` via `StepProgress` entries.
- **New FS primitive `ListSubdirs`:** added to `specio.FS` (and implemented for `OSFS` and `MemFS`) so `ListActivePlans` can enumerate plan subdirectories without relying on file-naming conventions. Mirrors the non-recursive shape of `ListDir`.
- **`.gitignore`:** `/.locutus/workstreams/` excluded. `.locutus/state/` (DJ-068) remains tracked.
- **`cmd/adopt.go`:** when dispatch is wired (deferred Phase C round), the resume protocol above runs *before* the current classification pass. The clear-on-drift invariant from DJ-072's follow-up already prepared the state entries: when `WorkstreamID` is cleared on drift, `DeletePlan` removes the corresponding `.locutus/workstreams/<plan-id>/` subdirectory in the same step.
- **Dispatcher integration:** writes `plan.yaml` once on planner completion, writes one `<workstream-id>.yaml` per workstream on dispatch, updates on each PlanStep completion, deletes the plan subdirectory on terminal transition. Agent session ID plumbed in from the driver.

**Cleanup / garbage collection:**

Orphaned workstream records (Approaches removed from the graph while a record still references them) are detected on the next `adopt` run — if none of a record's `ApproachIDs` resolve in the current spec graph, the record is deleted with a log line. No background GC needed; the reconciler is the GC.

**Not in scope for DJ-073:**

- Post-completion MasterPlan archival. The `PlanRecord` exists only while a plan is in flight; once every Approach reaches `live`, the subdirectory is deleted. If we later want historical plan archival (for post-hoc "how did we build this" analysis), that's a separate decision — either pipe plan summaries through the historian or add a committed `docs/plans/archive/` location.
- Cross-machine resume. Everything here assumes single-machine execution; distributed resume is a future problem.
- Concurrent plans. Nothing forbids two overlapping plans in the layout (subdirectories don't collide), but `adopt` doesn't today take a write-lock against another in-flight `adopt`. If that becomes a concern, a lockfile under `.locutus/` is the natural next step.
