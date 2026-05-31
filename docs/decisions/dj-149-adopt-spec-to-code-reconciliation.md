## DJ-149: `adopt` Closes the Spec → Code Gap; Bidirectional Reconciliation's Code-Producing Side; Runtime-Driven Master Plan via `.locutus/sessions/<sid>/plans/` + Stacked Worktrees (`adopt/<NNN>-<approach-id>`) with Phase-N+1-Branches-Off-Phase-N + Halt-On-First-Failure; Per-Approach State Re-Established at `.borg/state/` Per DJ-068/DJ-096 — Partially Supersedes DJ-148's Misplaced State Fields on `spec.Approach`; Migrates `ReconciliationState.SpecHash string` to `SpecHashes map[string]string` Keyed by Spec Id for Granular One-Hop Upstream Subgraph Drift Detection (Catches Cascade-Driven Spec Drift Per DJ-138 + Refine Revisions to the Approach Itself + Renames as Coincident Add+Remove); Per-File `Artifacts map[path]hash` Stays Per DJ-068 for Granular Code Drift; Adds `drift-classifier` Subagent for Semantic-vs-Trivial Code Drift Judgment (Trivial → `state_refresh_artifacts` Hash-Only Update; Semantic → `out_of_spec` Surfaced for Operator Decision); Honors DJ-068's Test-Asserted-Live-Status Principle (Runtime Runs Project Tests After Each Phase; Outcome Maps `passed`→`live` / `failed`→`failed`); New MCP Tools `state_record_reconciliation` / `state_refresh_artifacts` / `state_mark_status` / `state_delete_record` + Read Tools `state_list_records` / `state_get_record` — All CaptureOnly-Wrapped Per DJ-147; Overlay Extension Adds `stateOverrides` + `stateDeleted` Maps for In-Session Dry-Run Capture; Runtime Decides Parallelism + Branch Ordering (Locutus Stays Out of DAG Construction Per DJ-144's Trajectory of Trusting the Runtime); Closes the Other Half of the DJ-135 Phase 5 Checkpoint 3 Leak (DJ-148 Closed Assimilate's Half; This Closes Adopt's)

**Status:** settled (designed 2026-05-30; no code yet). Partially supersedes [DJ-148](dj-148-assimilate-bidirectional-reconciliation.md) — specifically the state-side fields placed on `spec.Approach` (`SourceFiles`, `SourceHash`, `SourceHashSyncedAt`). The DJ-148 contributions to the assimilate playbook + the rewritten subagent prompts + the bidirectional-reconciliation principle + the `spec_propose_approach` / `spec_revise_approach` MCP tools (for proposing/revising approach **bodies**, separate from state) stay. Closes the same DJ-135 phase 5 checkpoint 3 leak DJ-148 closed for assimilate, but on the adopt side. Caught during DJ-149 brainstorming when re-walking the journal surfaced [DJ-068](dj-068-manifest-state-separation-kubernetes-inspired.md) (manifest/state separation, shipped) and [DJ-096](dj-096-state-store-lives-under.md) (state path located at `.borg/state/`, shipped) — prior art that DJ-148 missed and inadvertently reinvented inside the spec graph.

**Context.** Locutus is a Kubernetes-like reconciler. The manifest (spec graph) declares desired state; the application (source code) is realized state; the binding between them is the **state Locutus maintains** — recorded per-approach as `(spec_hashes, artifacts, status, last_reconciled)`. The state IS the etcd equivalent: Locutus's record of what's reconciled, distinct from both the manifest and the code itself. DJ-068 established this separation and ratified `.locutus/state/` (later `.borg/state/` per DJ-096) as the storage location; `internal/state/` package exists with `FileStateStore`, `ReconciliationState`, and the eight `ReconcileStatus` values (`unplanned` / `planned` / `pre_flight` / `in_progress` / `live` / `failed` / `drifted` / `out_of_spec`).

DJ-148 placed state fields directly on `spec.Approach` — conflating desired state (the spec's description of what the approach should do) with observed state (the reconciliation record of what was actually written and when). The correct separation per DJ-068 is: spec.Approach describes WHAT (body, parent, citations); state record describes IS (per-file hashes, status, when reconciled). DJ-149 reverts the misplaced fields and re-routes assimilate's reconciliation writes through the proper state surface, while filling in the adopt verb itself.

The verb decomposition crystallizes:
- `refine` — declares manifest from `GOALS.md`. Deliberation; spec mutation only.
- `assimilate` — brownfield code → spec reconciliation. Reads code, infers/revises spec, synthesizes approaches, **establishes state via the state MCP tools** (post-DJ-149).
- `adopt` — spec → code reconciliation. Reads spec + state, identifies work (unbound / spec-drifted / code-drifted / orphan-parent), dispatches the runtime to implement in stacked worktrees, **maintains state via the same MCP tools**. DJ-149's primary scope.

Both verbs are bidirectional reconcilers that produce coherent (spec, code, state). The state surface is symmetric: the same MCP tools, the same `ReconciliationState` shape, the same drift semantics. The verbs differ only in which direction they're closing the gap from.

The state record's drift signal needed strengthening to be useful. DJ-068's original `SpecHash string` is the hash of the approach node alone — it doesn't catch upstream spec changes (cascade-revised features per [DJ-138](dj-138-refine-with-bias-cascade.md), refine revising the approach's cited decisions, renames of upstream features when refine restructures the spec). DJ-149 migrates that single field to `SpecHashes map[string]string` keyed by spec id, covering the approach's one-hop upstream subgraph: `approach.id` + `approach.parent_id` + every entry in `approach.decisions[]` + `approach.advances[]` + `approach.respects[]`. The map gives both **set-diff** drift signals (added / removed keys — catches renames as coincident add+remove) and **hash-diff** drift signals (body changed on a same-id key). Symmetric to the per-file `Artifacts map[path]hash` on the code side, which DJ-068 already had for the same granular-diagnostic reason.

The execution model trusts the runtime per DJ-144's trajectory. Locutus identifies the worklist and writes per-approach plan files to `.locutus/sessions/<sid>/plans/<approach-id>.md`; the runtime reads the plan folder and decides parallelism, branch ordering, worktree management, and test execution. Claude Code's dynamic workflow pipelines independent phases concurrently; Codex/Gemini serialize via their iteration primitives. The state outcome (per-approach reconciliation records via MCP) is identical regardless of how the runtime sequenced the work.

**Decision.** Author the `code_adoption.md` playbook body + per-runtime overlays; add the state MCP tool surface (4 write + 2 read tools); extend DJ-147's overlay for state captures; migrate the `ReconciliationState` schema; revert DJ-148's state-side fields from `spec.Approach`; ship runtime-driven master-plan execution with stacked worktrees. The state surface becomes the etcd equivalent for the reconciliation loop, accessible only via MCP tools (the same DJ-134 invariant that spec mutations follow).

1. **Verb scope: `adopt` only.** The CLI dispatch + Kong fields (`--scope`, `--dry-run`, `--format`) already exist in [`cmd/adopt.go`](../../cmd/adopt.go); DJ-149 doesn't touch the Go-side cmd surface beyond a doc-comment refresh.

2. **Partial supersession of DJ-148.** Revert the state-side additions DJ-148 made to `spec.Approach` (`SourceFiles`, `SourceHash`, `SourceHashSyncedAt`), their plumbing in `proposeApproachInput` / `buildApproachBody` / both MCP tool handlers, and the assimilate playbook's hash-computation block. The DJ-148 contributions that stay: assimilate playbook itself, rewritten subagent prompts (scout, backend-analyzer, frontend-analyzer, infra-analyzer, gap-analyst), bidirectional-reconciliation principle, `spec_propose_approach` / `spec_revise_approach` MCP tools (for proposing/revising approach **bodies**, separate from state). DJ-148's doc gets a status-header update naming the partial supersession; the body stays intact for historical context.

3. **State surface from DJ-068 with DJ-149 schema migration.** `internal/state/` package's `ReconciliationState` struct migrates `SpecHash string` → `SpecHashes map[string]string` keyed by spec id covering the approach's one-hop upstream subgraph (`approach.id` + `approach.parent_id` + each entry in `approach.decisions[]` + `approach.advances[]` + `approach.respects[]`). The map gives both set-diff drift (added/removed keys — catches renames as coincident add+remove) and hash-diff drift (body changed). The flat map is inspectable at a glance (`diff stored.spec_hashes current.spec_hashes` is the whole drift diagnostic); Merkle would compose better hierarchically but adds opacity. Per-file `Artifacts map[path]hash` from DJ-068 stays — symmetric granularity on the code side. The eight `ReconcileStatus` values stay unchanged.

4. **Drift classification: set diff + hash diff on both sides.** Step 2 of the adopt playbook computes drift per approach across both spec and code dimensions:
   - **Spec side**: `current_keys = upstream subgraph from spec graph`; compare to `stored.SpecHashes.keys`. Added keys = new upstream dependencies; removed keys = no-longer-referenced (or rename's other half); same-key hash mismatch = body changed. Any spec drift → status `drifted` → schedule regeneration.
   - **Code side**: for each `stored.Artifacts` path, stat the file. Missing → `removed_file`. Hash mismatch → dispatch `drift-classifier` subagent with the diff; subagent judges trivial (formatting/imports/comments) vs semantic. Trivial → `state_refresh_artifacts` (hash-only update; status stays `live`). Semantic → status `out_of_spec` → surface for operator decision. Removed files → status `out_of_spec` → surface.
   - **Added code files NOT auto-detected on the adopt side** — that's assimilate's surface (code → spec direction). adopt only reconciles spec-side gaps + previously-bound files; surprise additions get surfaced via assimilate's `unplanned`-code detection in a future enhancement.
   - **Combined drift** (spec drifted AND code is out_of_spec) → status `out_of_spec` takes precedence (auto-regenerating would clobber operator's manual code changes); report shows BOTH drift sources so operator decides next move.
   - **Orphan parent** (`approach.parent_id` no longer in manifest) → call `spec_mark_approach_drifted` (DJ-138) + classify as orphan-superseded + surface.

5. **Master plan as runtime-decided DAG; Locutus writes plan files; runtime owns execution.** Step 3 of the playbook writes per-approach plan files to `.locutus/sessions/<sid>/plans/<approach-id>.md` (ephemeral, per-session, under `.locutus/` not `.borg/`, ignored by git). Each file: structured frontmatter (id, parent_id, parent_kind, decisions[], advances[], respects[], worklist_category, prior_artifacts + drift_reason if applicable) + markdown body (parent + decisions + goals verbatim, approach body, acceptance criteria, implementation hints). The runtime reads the plan folder and decides execution shape — Claude Code's dynamic workflow inspects the (implicit) DAG and pipelines independent phases concurrently; Codex/Gemini serialize via their iteration primitives. Locutus stays out of DAG construction: the runtime sees richer code-level dependencies than spec citations capture (shared utility files, common imports, identical file targets), and runtimes get better over time without Locutus needing to track their improvements.

6. **Stacked worktrees per phase; branch naming `adopt/<NNN>-<approach-id>`; phase-N+1-branches-off-phase-N.** Per-phase worktree (`git worktree add ../<project>-adopt-<NNN>-<approach-id>` or equivalent). Branch name `adopt/<NNN>-<approach-id>` where `<NNN>` is the runtime's chosen lexicographic ordinal (zero-padded to 3 digits). Parallel siblings at the same ordinal suffix with a letter (`003a`, `003b`); the rightmost parallel sibling is the base for the next serial phase (convention; runtime free to deviate). Stacked branches accept operator-review burden honestly: phase-by-phase reviewable units instead of giant auto-merged PRs. The user accepted the rebasing burden on phase N+1 after phase N merges as unavoidable pain for v1.

7. **Halt on first failure; failed branch retained; no auto-resume.** Per-phase failure (test failed, agent halted) → runtime halts the master plan; failed phase's worktree + branch retained for operator inspection; subsequent phases not attempted (they may depend on the failed phase per the stacked-branches convention). Operator addresses the failure + re-runs adopt; Step 2 recomputes the worklist from current state; previously-successful phases are skipped (their state records show `live`); the failed phase reappears in the worklist (its state shows `failed`) along with any halt-skipped predecessors. No "resume from where left off" semantic needed — state IS the resume mechanism.

8. **Test-asserted live status per DJ-068's honest-state principle.** The runtime runs the project's test suite after each phase implementation; the suite's exit status is the live-vs-failed signal. `state_record_reconciliation`'s `test_outcome` field is a strict enum (`passed` | `failed`) — daemon maps `passed` → status `live`, `failed` → status `failed`. If a project genuinely lacks a test suite, the runtime is instructed (per the playbook's dispatch prose) to either scaffold tests using its native skills or explicitly halt the phase as operator-actionable rather than fudging `live`. No `untested` status added in v1; the honest-state principle is preserved.

9. **Per-runtime convergence drivers inherited from [DJ-144](dj-144-cc-workflow-convergence.md).** Three playbook files:
   - `code_adoption.claude-code.md` (provider overlay) — dynamic workflow shape; runtime drives per-phase fan-out with concurrency primitives.
   - `code_adoption.interactive.md` (mode overlay) — `spec_loop_*` directive header for Codex/Gemini interactive self-loop.
   - `code_adoption.md` (default fallback) — single-iteration shape with verdict line; codex/gemini headless via OuterLoopRunner.
   
   Activity registry entry already exists; DJ-149 sets `max_iterations: 10` (higher than assimilate's 3 — adopt is multi-phase, and the cap is for transient-failure retry headroom, not for normal-flow deliberation passes).

10. **DJ-147 dry-run inheritance via captureOnly + overlay extension.** All four new write MCP tools (`state_record_reconciliation`, `state_refresh_artifacts`, `state_mark_status`, `state_delete_record`) wrap via `captureOnly` at registration, identical to the 14+2 spec tools DJ-147 + DJ-148 established. Read tools (`state_list_records`, `state_get_record`) route through `OverlayView` for in-session visibility. `sessionOverlay` extends with `stateOverrides map[approach_id]*ReconciliationState` and `stateDeleted map[approach_id]struct{}` — symmetric to the spec-side `entries` + `deleted` maps. Under `--dry-run`, the playbook adds `LOCUTUS_DRY_RUN` guards around the non-MCP side effects (writing plan files, dispatching the runtime for code generation, creating worktrees). `spec_dry_run_report` returns captured state mutations alongside captured spec mutations.

## Resolved design questions

1. **Why partial supersession of DJ-148 rather than amendment in place?** The journal's established convention for "we tried X and it was wrong" is supersession (see DJ-121 → DJ-135), not amendment in place. Supersession preserves each DJ's original intent + reasoning (so future-us reconstructing history sees what was tried and why) while the successor names exactly what changes. Amendment blocks tend to grow stale; supersession produces cleaner legibility. DJ-148's doc gets a status-header update; the body stays intact.

2. **Why map-based `SpecHashes` vs single composite hash vs Merkle tree?** Single composite (DJ-068's original) loses diagnostic granularity — when something drifts you can't tell what. Merkle composes better hierarchically but adds opacity (you can't tell which spec changed without unwinding the tree). Flat map keyed by spec id gives both set-diff and hash-diff signals with per-key precision — operator sees exactly which spec drifted. Same diagnostic granularity as `Artifacts map[path]hash` on the code side; consistent shape across both halves of the state record. Maps are simpler to understand at a glance.

3. **Why runtime-driven execution vs Locutus-computed DAG?** Locutus can compute static dependencies from spec citations, but the runtime sees richer code-level dependencies once it starts implementing (shared utility files, common imports, identical file targets). Building DAG construction into Locutus duplicates intelligence the runtimes already have and creates maintenance burden when runtimes improve. The worst case of trusting the runtime is suboptimal parallelism (the run took longer than it could have), not incorrect outcomes — worktree isolation guarantees state coherence regardless of execution order. Consistent with DJ-144's trajectory of trusting the runtime with convergence judgment + parallel orchestration.

4. **Why halt-on-failure vs continue-on-failure?** Continue-on-failure produces incoherent state (subsequent phases may depend on the failed phase's code; running them anyway hides the gap between "this works" and "this is broken"). Halt-on-failure respects state outcome + the stacked-branches dependency convention. The operator-actionable nature of phase failures is the right escape hatch: fix the underlying issue, re-run adopt, Step 2 recomputes from current state. State IS the resume mechanism.

5. **Why stacked branches vs auto-merge vs separate review branches?** Auto-merge produces giant unreviewable PRs that get rejected from sheer size. Separate (non-stacked) review branches can't share code between phases in the same adopt run. Stacked branches give per-phase reviewable units while allowing phase N+1 to build on phase N. The rebase burden on phase N+1 after phase N merges is unavoidable pain for v1 — accepted as worth the review-friendliness trade-off.

6. **Why include `state_mark_status` + `state_delete_record` in v1 vs deferring to YAGNI?** Excluding them would force operators to edit `.borg/state/<id>.yaml` directly for manual status manipulation or approach retirement. Direct file edits bypass the captureOnly wrapper (no dry-run capture), the overlay (other sessions miss the edit), and the tools.jsonl audit trail (no headless trace). DJ-134's "MCP-only mutation" principle applies to state the same way it applies to spec; the state surface needs the same completeness.

7. **Why no separate `untested` status?** The eight DJ-068 statuses are sufficient. The runtime is instructed to either run tests or scaffold them; if neither is possible, the phase halts as operator-actionable rather than landing in an ambiguous status. Adding an `untested` status would soften the honest-state principle DJ-068 established (`live` means "tests passed", not "code was written"). v1 keeps the model honest; if real workflows surface the need for an `untested` middle ground, add later.

8. **Why test-asserted live vs file-presence checks?** DJ-068's explicit principle: "The reconciler asserts `live` or `failed` by running tests — not by checking that code was written. This is the mechanism that makes the state store an honest account of the system's actual condition." File-presence checks would let unverified code claim `live` status, eroding the state store's trustworthiness. Tests are the verification surface.

9. **Why no schema-migration unit tests?** No production projects have state records yet. The "migration" from `SpecHash string` to `SpecHashes map[string]string` is from-empty (no records to migrate). The Go struct definition is the source of truth; the YAML library handles encoding correctness. Testing schema migration against a hypothetical scenario is over-testing.

10. **Why is added source code assimilate's territory, not adopt's?** adopt is spec → code direction: it reconciles spec-side gaps (unbound approaches, drifted hashes) by generating code. Code that appears without being adopt-generated is the inverse: code → spec, which is assimilate's surface (assimilate surfaces it as `unplanned` code for operator review). The two-verb division respects which direction of the manifest-vs-reality gap each verb is closing. Auto-detecting added files in adopt would require heuristic file-to-approach attribution; cleaner to leave that to assimilate's structured analysis.

## Alternatives considered

- **Amending DJ-148 in place (rejected).** Would conflate the original DJ's intent with later corrections; amendment blocks tend to grow stale. Supersession is the journal's established convention.
- **State fields stay on `spec.Approach`; defer migration to a future DJ (rejected).** Pragmatic but creates ongoing technical debt with two state surfaces (`spec.Approach` fields + `.borg/state/` records) that operators would have to reason about. Migration cost is small (no production projects yet); deferral cost compounds.
- **Single composite `SpecHash` over the upstream subgraph (rejected).** Captures drift but loses the per-key diagnostic that map-based hashing provides. Operators want to know WHICH spec changed.
- **Merkle tree for spec hashes (rejected).** Hierarchical composition is theoretically cleaner but adds opacity. Flat map is simpler to understand and inspect at a glance.
- **Locutus computes the DAG; runtime executes it (rejected).** Duplicates intelligence runtimes already have; creates maintenance burden when runtimes improve. Inconsistent with DJ-144's trajectory.
- **Auto-merge to base on phase success (rejected).** Produces giant PRs that get rejected from sheer size. Per-phase review-friendliness wins over end-state cleanliness.
- **Continue-on-failure (rejected).** Produces incoherent state when subsequent phases depend on the failed phase. The state-outcome principle requires halt.
- **Separate (non-stacked) review branches per phase (rejected).** Loses the ability for phase N+1 to build on phase N within the same adopt run.
- **Defer `state_mark_status` + `state_delete_record` to YAGNI (rejected).** Forces operators to bypass the MCP boundary; violates DJ-134's mutation-discipline principle.
- **Add `untested` status to ReconcileStatus (deferred).** Would soften DJ-068's honest-state principle. v1 expects the runtime to scaffold tests or halt; if real workflows surface the need, revisit.
- **AST-based deterministic drift classifier (deferred).** Requires per-language AST parsers. v1 uses LLM judgment via the `drift-classifier` subagent — non-determinism is bounded (only fires on hash mismatch; rare in steady state) and improves with frontier model evolution.
- **Auto-detect added source files in adopt (deferred).** Heuristic file-to-approach attribution is risky. Leaving added code to assimilate's structured analysis (where it surfaces as `unplanned`) is the correct division of labor.
- **Strict status-transition validation in `state_mark_status` (deferred).** Some transitions (e.g., `live` → `planned` without going through `drifted`/`out_of_spec`) are unusual but not invalid. v1 trusts the operator/runtime; if invalid transitions become a real problem, add validation later.
- **Concurrent adopt run mutex enforced at the daemon level (deferred).** v1 uses a CLI-side guard (sidecar lock file in `.locutus/adopt.lock`) + documents "don't run concurrent adopt against the same project." Daemon-level enforcement adds machinery; the CLI guard catches the common case.
- **First-class approach retirement workflow (deferred).** When a parent feat/strat is deleted/superseded, the approach's state record becomes orphaned. v1: operator calls `state_delete_record` manually after `spec_mark_approach_drifted` surfaces the orphan. A future DJ could add automatic cleanup or a dedicated `adopt --retire-orphans` mode.

## Consequences

**Cmd / API surface:**
- `cmd/adopt.go` doc-comment refresh — name the DJ-149 contract (master plan, runtime-driven execution, state via MCP); preserve existing flags (`--scope`, `--dry-run`, `--format`)
- CLI-side concurrent-run guard via `.locutus/adopt.lock` sidecar file (small addition)

**MCP tool surface:**
- New write tools (4): `state_record_reconciliation`, `state_refresh_artifacts`, `state_mark_status`, `state_delete_record` — all captureOnly-wrapped per DJ-147
- New read tools (3): `state_list_records`, `state_get_record`, `state_compare_hashes` — overlay-aware; `state_compare_hashes` does server-side SpecHashes diff (current subgraph vs stored record) so the agent doesn't need to reproduce server-side hash bytes
- Tool count goes from 14+2 mutation tools (DJ-148 baseline minus DJ-149 reverts) to ~16 mutation tools (4 new state + 2 existing approach) + 3 new read tools

**Spec data model:**
- `spec.Approach` reverts: drop `SourceFiles`, `SourceHash`, `SourceHashSyncedAt` and their `yaml:` tags; restore the pre-DJ-148 struct shape

**State data model:**
- `state.ReconciliationState`: `SpecHash string` → `SpecHashes map[string]string`; update `internal/state/state.go` + the package's tests
- `FileStateStore` exposed to the daemon (alongside `SpecStore`) via `NewSpecServer`'s constructor

**Overlay extension:**
- `sessionOverlay` gains `stateOverrides map[string]*state.ReconciliationState` + `stateDeleted map[string]struct{}` + four new entry points (`OverlayPutState`, `OverlayDeleteState`, `OverlayGetState`, `OverlayListStateRecords`)

**Daemon-side helper:**
- New `internal/state/spec_hashes.go`: given an approach id + current spec graph, compute the one-hop SpecHashes map deterministically

**Subagent prompts (`internal/scaffold/agents/`):**
- `approach-regenerator` rewrite — drop `output_schema:`, update for DJ-148 conventions (axis-shaped ids, no entity persistence), reference the new state surface for drift signals
- New `drift-classifier` agent — small, judgmental, language-agnostic; reads a file diff and returns `trivial` or `semantic`

**Activity playbook (`internal/scaffold/plans/`):**
- Rewritten: `code_adoption.md` (default fallback — single-iteration shape with verdict line; preconditions → worklist → plan-files → dispatch → state-persist → report → verdict)
- New: `code_adoption.claude-code.md` (provider overlay — dynamic-workflow shape; runtime drives per-phase fan-out)
- New: `code_adoption.interactive.md` (mode overlay — codex/gemini interactive `spec_loop_*` directive header)

**Activity registry (`internal/activity/agents-default.yaml`):**
- Update `code_adoption.max_iterations` to 10 (transient-failure retry headroom; adopt converges in 1 in normal flow)

**Assimilate playbook re-routing (consequence of the DJ-148 revert):**
- `internal/scaffold/plans/code_assimilation.md` Step 6's hash-computation block: replace the `spec_propose_approach` / `spec_revise_approach` invocations (which carry source_files + source_hash) with the proper state recording via `state_record_reconciliation`. The approach body is committed via the existing approach tools (without state fields); the state record is created via the new state tool.
- The assimilate playbook's `.claude-code.md` overlay gets the same edit.

**Documentation:**
- `docs/runtime-affordances.md` — add an Adopt paragraph after the existing Assimilate paragraph (DJ-148); naming the worktree-per-phase + stacked-branches + halt-on-failure + test-asserted-live contract
- `CLAUDE.md` — add a DJ-149 bullet in "Sources of Truth" after the DJ-148 bullet; naming the state separation + the supersession of DJ-148's state-side fields
- `docs/council.md` — add an Adopt subsection naming the subagent flow (drift-classifier for code diff judgments; approach-regenerator for parent-supersession regeneration) and the runtime-driven master plan
- DJ-148's doc gets a status-header update: `**Status:** shipped 2026-05-30; state-side fields partially superseded by DJ-149` — body unchanged for historical context

## Future Work

The following enhancements are deferred — each has a defensible v1 alternative + a clear trigger for when to revisit:

- **DJ-150 (provisional): AST-based deterministic drift classifier.** Per-language parsers (`go fmt`-canonical hash for Go, prettier-canonical for JS/TS, etc.) replace LLM judgment for trivial-vs-semantic drift. Triggers: v1's drift-classifier produces unacceptable false positives/negatives in real adopt runs.
- **DJ-151 (provisional): Auto-detect added source files in adopt.** Heuristic file-to-approach attribution (e.g., new files in directories the approach already binds to). Triggers: operators repeatedly complain that adopt misses new files they expect adopt to bind.
- **DJ-152 (provisional): Strict status-transition validation.** `state_mark_status` enforces the DJ-068 lifecycle graph (e.g., `live` → `planned` requires going through `drifted` or `out_of_spec` first). Triggers: invalid transitions create real state-coherence problems in audit logs.
- **DJ-153 (provisional): Daemon-level concurrent adopt run enforcement.** Replace the CLI-side `.locutus/adopt.lock` with daemon-side mutex via the per-session map. Triggers: CLI-side guard proves insufficient (operators bypass via direct daemon dispatch).
- **DJ-154 (provisional): First-class approach retirement workflow.** Dedicated `adopt --retire-orphans` mode that auto-calls `state_delete_record` for approaches whose parent was deleted, after operator confirmation. Triggers: orphan accumulation becomes a real operator burden.
- **DJ-155 (provisional): Per-language test-suite scaffolding baseline.** A registry of "default test command per language" the runtime falls back to when project markers don't surface one (Go: `go test ./...`; Node: `npm test`; Python: `pytest`). Triggers: runtime ambiguity about test commands halts phases unnecessarily.
- **DJ-156 (provisional): Transitive dependency walk for SpecHashes.** Extend the one-hop subgraph to a full transitive closure (parent's decisions, parent's parent if a hierarchy emerges, etc.). Triggers: one-hop misses meaningful upstream changes that compound through indirection.
- **DJ-157 (provisional): State versioning for spec graph evolution.** If the SpecHashes computation algorithm changes (e.g., switching from sha256 to blake3), existing state records become uncomparable. A `state_format_version` field would let the daemon detect + migrate. Triggers: a hash-algorithm change becomes operationally necessary.

## References

- [DJ-068](dj-068-manifest-state-separation-kubernetes-inspired.md) — Manifest/state separation; `.locutus/state/` (now `.borg/state/`); per-approach `ReconciliationState` with `SpecHash` (now `SpecHashes` per DJ-149) + `Artifacts map[path]hash` + 7 statuses
- [DJ-071](dj-071-pre-flight-clarification-protocol-coding.md) — Added `pre_flight` status (8th); honored as-is in DJ-149
- [DJ-072](dj-072-cli-surface-consolidated-8-verb.md) — 8-verb CLI surface (the verb adopt sits within)
- [DJ-075](dj-075-assimilate-reads-existing-spec.md) — Assimilate's idempotent spec-write principle (carried forward to DJ-149's state-write idempotency)
- [DJ-087](dj-087-approaches-are-synthesized-adopt.md) — Approaches synthesized at adopt time, not refine time (DJ-149's master plan includes synthesis for unbound feat/strat)
- [DJ-096](dj-096-state-store-lives-under.md) — State path `.locutus/state/` → `.borg/state/`
- [DJ-121](dj-121-adoption.md) — MasterPlan concept (superseded by DJ-135; concept survives as in-session ephemeral artifact in DJ-149)
- [DJ-134](dj-134-unified-spec-store.md) — `SpecStore` as source of truth during a daemon session; "MCP-only mutation" principle DJ-149 extends to the state surface
- [DJ-135](dj-135-multi-runtime-pivot.md) — Multi-runtime pivot; daemon-per-project; activity-playbook + ACP dispatch model
- [DJ-138](dj-138-refine-with-bias-cascade.md) — `refine --with` bias cascade + `spec_mark_approach_drifted` (DJ-149's spec-drift detection complements this — implicit via SpecHashes mismatch + explicit via the existing mark tool)
- [DJ-141](dj-141-unanchored-goal-provenance.md) — Goal-layer provenance (DJ-149 reads goal layer as context for approach plan files)
- [DJ-143](dj-143-per-runtime-tool-policy.md) — Per-runtime tool restriction (DJ-149's state tools don't need restriction; available to all runtimes)
- [DJ-144](dj-144-cc-workflow-convergence.md) — Per-runtime convergence drivers; runtime-driven execution principle DJ-149 inherits + extends to per-phase parallelism
- [DJ-147](dj-147-dry-run-mutation-capture.md) — `captureOnly` wrapper + overlay; DJ-149's state tools inherit dry-run via the same registration-site pattern; overlay extension is symmetric to spec overrides
- [DJ-148](dj-148-assimilate-bidirectional-reconciliation.md) — Assimilate's playbook + agent rewrites + approach mutation tools (DJ-149 partially supersedes the state-side fields; the rest stays)
