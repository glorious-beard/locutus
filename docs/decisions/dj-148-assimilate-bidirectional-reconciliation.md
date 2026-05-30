## DJ-148: `assimilate` Closes the Code → Spec Gap; Bidirectional Reconciliation Produces State; Synthesizes Approaches Binding Inferred Specs to Source Files; Code-Is-Truth From This Direction; Preconditions GOALS.md + Goal Layer Populated (Operator Runs `refine goals` First); Reuses Refine's `scout` + Per-Domain Analyzers (`backend-analyzer` / `frontend-analyzer` / `infra-analyzer`) + `gap-analyst` Subagents With Prompts Rewritten for Current Conventions (Axis-Shaped IDs Per DJ-133, No Entity Persistence Per DJ-076); Adds `spec_propose_approach` / `spec_revise_approach` MCP Tools (Shared With DJ-149) + Per-Approach `source_hash` Body Field; Per-Runtime Convergence Drivers Inherited From DJ-144 (Workflow for Claude Code, Loop-State Tools for Codex/Gemini Interactive, OuterLoopRunner for Codex/Gemini Headless); DJ-147 Dry-Run Inheritance via `captureOnly` Wrapping; Defers Middle-Out Reconciliation, Orphan Handling, Self-Contained Goal-Layer Sync, `code-paths` Cache, and Iterative Hypothesis Cycle to DJ-150+ as Future Work — Closes the DJ-135 Phase 5 Checkpoint 3 Leak That Left `code_assimilation.md` as a 19-Line Placeholder

**Status:** settled (designed 2026-05-30; no code yet). Closes the long-deferred DJ-135 phase 5 checkpoint 3 work that was supposed to fill in the `code_assimilation.md` playbook body but never landed. The current playbook is a placeholder header (`> **Status:** placeholder. The detailed playbook lands alongside the cmd/assimilate.go rewrite in DJ-135 phase 5 checkpoint 3.`) that instructs the agent to "report `converged: true`" without doing any work. Caught during DJ-147 closeout when reviewing what `--dry-run` would even capture for the four mutating verbs — adopt and assimilate both fell out as structural no-ops. DJ-148 fixes assimilate; [DJ-149](dj-149-adopt-drift-and-synthesis.md) (sibling, designed alongside) fixes adopt.

**Context.** Locutus is a Kubernetes-like reconciler. The manifest (specs) declares desired state; the application (source code) is the realized state; the binding between them (per-approach `source_hash`) IS the state Locutus maintains. The principle is that **every verb's outcome must be coherent state** — specs without binding to code are floating hypotheses; code without spec binding is unmanaged. A verb that emits one side without closing the gap to the other violates the maintenance loop and shouldn't ship.

The post-DJ-135 verb decomposition assigns purposes:
- `refine` — declares manifest (specs) from `GOALS.md`. Deliberation; no code touched.
- `assimilate` — brownfield code → spec reconciliation. Reads code, infers/revises specs, synthesizes approaches binding them, establishes state. **DJ-148's scope.**
- `adopt` — ongoing spec → code reconciliation. Reads spec, synthesizes approaches for unbound features/strategies, dispatches the coding agent to write code that satisfies them, drift-marks when code diverges. DJ-149's scope.

`refine` and DJ-147's mutation surface ship and work today; assimilate and adopt have working CLI dispatch (per [DJ-135](dj-135-multi-runtime-pivot.md)) but stub playbooks that perform no work. The empirical evidence is a real `locutus adopt` run against `/Users/chetan/projects/winplan` on 2026-05-30 that correctly identified 0 approaches + 0 source files and exited cleanly — correct behavior for the inputs, but with no playbook content to advance the state of a project that did have source code.

The earlier council-era predecessors ([DJ-075](dj-075-assimilate-reads-existing-spec.md), [DJ-087](dj-087-approaches-are-synthesized-adopt.md)) shipped their design principles but were retired as implementation by DJ-135's multi-runtime pivot. The principles still hold; the plumbing needs to be rebuilt on the activity-playbook + MCP model. DJ-148 carries forward DJ-075's six invariants (ExistingSpec pre-load, `inferred` default status, matching-ID idempotency for re-runs) and DJ-087's "approaches synthesized after analysis, not during refine" rule, both translated to the post-DJ-135 footing.

**Decision.** Author the `code_assimilation.md` playbook body and per-runtime overlays; rewrite the surviving council-era assimilation subagent prompts for current conventions; add the approach mutation surface (two new MCP tools + one new body field); ship bidirectional reconciliation as one verb that produces coherent state. No new CLI verbs, no changes to refine, no new sidecar artifacts (those are deferred to DJ-150+). DJ-147 dry-run inherits automatically through the existing `captureOnly` wrapper.

1. **Verb scope: `assimilate` only.** DJ-149 is the parallel work for adopt. The CLI dispatch and Kong fields (including `--dry-run` + `--format` from DJ-147) already exist in `cmd/assimilate.go` — DJ-148 doesn't touch the Go-side cmd surface beyond a doc-comment refresh that drops the now-stale "Flags from the legacy verb (--dry-run) dropped" comment.

2. **Preconditions: `GOALS.md` exists + goal layer populated.** assimilate refuses to run with a clear actionable error when either is missing:
   - `GOALS.md` missing → "GOALS.md not found in working tree. Run `locutus init` to scaffold a template, then edit it with your project's mission statement before running assimilate."
   - Goal layer empty (manifest has no `goal-*` / `agoal-*` ids) → "Goal layer is empty. Run `locutus refine goals` first to materialize the goal layer from GOALS.md; assimilate uses the goal layer as context for grounding inferences."

   The refusals are emitted by the playbook itself in Step 0, not by the Go-side cmd surface — the precondition check requires consulting the manifest (so the daemon must be running). The playbook reads `mcp__locutus__spec_list_manifest`, checks the `Goals` / `AntiGoals` arrays, and emits the refusal as the verdict-line activity output if empty.

   Auto-dispatch of `refine goals` as a precondition step (the friendlier alternative) is deferred to DJ-150+ — keeps the verb boundary clean for v1; the operator's first-time workflow has one extra step but each step has one clear purpose.

3. **Bidirectional reconciliation; code-is-truth from this direction.** assimilate's job is to close the gap between code and spec by mutating BOTH sides:
   - **Code → spec mutations.** Where existing spec nodes (typically refine-emitted) describe a capability that code implements differently, the playbook revises them via `spec_revise_*` to match what the code shows. Where code implements a capability with no corresponding spec node, the playbook proposes a new one via `spec_propose_*`. The default conflict resolution direction is **code-is-truth** — assimilate is reading reality; reality wins. The agent surfaces the revision rationale in its closing report so the operator can spot intent-vs-reality divergence.
   - **Approach synthesis.** Every emitted feature/strategy gets an approach binding it to the source files that justified emitting it. Approaches carry a `source_hash` field (`sha256:<hex>`) computed from the bound files' content; that hash is what DJ-149's drift detection compares against on subsequent runs.
   - **State outcome.** A successful assimilate run leaves the project in a state where every spec node assimilate touched is either bound to code via an approach (with matching `source_hash`) or is an upstream-only node (goal / anti-goal / axis-question decision) that doesn't bind. No floating specs.

   Conflict resolution edge cases — operator's aspirational spec ("we WILL use JWT") with code that disagrees ("currently using session cookies") — surface as low-confidence revisions in the report. Operators reject by editing the spec back post-run; no special status field is added in this DJ. The report-and-revise loop is the surface. (A first-class "aspirational" status with conflict-deferral semantics is DJ-150+ future work.)

4. **Subagent pool: 5 agents, prompts rewritten for current conventions.** The surviving council-era assimilation agents at `internal/scaffold/agents/*.md` get prompt rewrites:
   - **`scout`** — enumerates languages, frameworks, component boundaries, structure, config files. Emits a structured `ScoutSummary` for the analyzers to consume. Detects multi-component polyglot repos (multiple `go.mod` / `package.json` / `pyproject.toml` / `Cargo.toml` markers; per-component conventions).
   - **`backend-analyzer`** — reads backend source per scout's component breakdown. Emits a structured contribution of (decisions, strategies, features) for the backend domain.
   - **`frontend-analyzer`** — same for frontend. Early-exits when scout's summary doesn't list any frontend indicators.
   - **`infra-analyzer`** — reads CI configs, Dockerfiles, deployment manifests, etc. Emits an infra-domain contribution.
   - **`gap-analyst`** — receives the merged analyzer contributions + the current manifest's existing spec nodes. Reconciles: for each contribution, determines whether it confirms an existing node (no mutation), revises an existing node (`spec_revise_*` with code-is-truth conflict resolution), or proposes a new one (`spec_propose_*` with `status: inferred`). Returns the reconciled set ready for emission.

   Prompt rewrites strip council-era schema drift:
   - IDs use current conventions: decisions as `dec-<axis>` per [DJ-133](dj-133-decisions-identified-by-axis.md), features as `feat-<slug>`, strategies as `strat-<slug>`, approaches as `app-<parent-id>` per [DJ-087](dj-087-approaches-are-synthesized-adopt.md)'s id convention.
   - Entity emission dropped entirely. [DJ-076](dj-076-entity-in-memory-context-not.md) made entities in-memory in the council pipeline; the activity-playbook model has no in-memory pipeline, so the council-era `e-<name>` outputs are removed from analyzer prompts. (Future DJ can revisit if entities surface a real consumer.)
   - Council-era "strategies as build/test commands" (the `s-build-go` style with `commands` map and `governs` globs) is dropped; replaced with current `spec.Strategy` shape (forward-looking implementation approach: `strat-event-sourcing`, `strat-jwt-auth`, etc.).
   - Council-era `output_schema: AssimilationContribution` front-matter is dropped; the activity-playbook model doesn't enforce typed Go schemas on subagent outputs.

5. **Approach synthesis surface.** Adds two new MCP tools, registered in `internal/mcp/tools_spec_write.go` and shared with DJ-149:
   - **`spec_propose_approach`** — input `{id: string, parent_id: string, parent_kind: "feat"|"strat", body: string, source_files: [string], source_hash: string, status?: "inferred"|"settled"}`. Validates id has `app-` prefix, parent_id exists and matches parent_kind, source_files are non-empty paths, source_hash is `sha256:<hex>`. Commits the approach to the SpecStore + emits the manifest notification.
   - **`spec_revise_approach`** — same input shape; revises an existing approach. Preserves the original `created_at` per existing revise patterns.

   The `source_files: []string` carries the relative paths the approach binds to. `source_hash: string` is the sha256 of the sorted-paths-then-contents concatenation of those files — computed by the agent before calling the tool, opaque to the daemon (the daemon just stores it). DJ-149's drift detection re-computes the hash from current file content and compares.

   Both tools inherit DJ-147 dry-run via the existing `captureOnly` wrapper at registration. The wrapper pattern is identical to the 14 existing wrapped tools; no new dry-run plumbing required.

6. **Approach body schema extension.** `spec.Approach` (`internal/spec/types.go` or equivalent) gains:
   - `SourceFiles []string` — relative paths the approach binds to
   - `SourceHash string` — `sha256:<hex>` over the sorted-paths-then-contents
   - `SourceHashSyncedAt time.Time` — server-stamped on each propose/revise

   The existing approach fields (id, parent_id, parent_kind, body, status, etc.) are preserved. The on-disk YAML-frontmatter shape under `.borg/spec/approaches/<id>.md` (per `internal/spec/approach.go`'s `yaml:` tags) gains the three new fields; existing approaches without them load with zero values (no migration needed — DJ-148 lands before any approaches exist in any project).

7. **Per-runtime convergence drivers inherited from [DJ-144](dj-144-cc-workflow-convergence.md).** assimilate ships with three playbook files; resolution per DJ-136 walks most-specific to least-specific:
   - `code_assimilation.claude-code.md` (provider overlay) — references the dynamic-workflow-driven shape from `spec_refinement.claude-code.md`, parallel fan-out of the four content-emitting agents (scout sequentially, then backend/frontend/infra in parallel, then gap-analyst sequentially), `{{max_iterations}}` injection point preserved for the workflow's loop counter.
   - `code_assimilation.interactive.md` (mode overlay) — same shape as the default fallback but with the leading `## Loop control` block from `spec_refinement.interactive.md` reused verbatim, instructing the agent to drive iteration via `spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration`. Used by codex/gemini interactive.
   - `code_assimilation.md` (default fallback) — single-iteration shape, emits `converged: true` after the single pass. Used by codex/gemini headless via `OuterLoopRunner`.

   `max_iterations` is added to the activity registry entry; defaults to **3** for assimilate (lower than spec_refinement's 20 because assimilate is single-pass-shaped — iteration only fires on transient partial-output failures).

   Activity registry entry (`internal/activity/agents-default.yaml`):
   ```yaml
   code_assimilation:
     prompt: code_assimilation
     description: Reconcile spec graph with source code; infer features/decisions/strategies; synthesize approaches binding them; establish state.
     max_iterations: 3
   ```

8. **Single-pass convergence; idempotent re-runs.** assimilate's playbook is single-iteration-shaped (one survey + analyze + reconcile + emit pass; emit `converged: true`). DJ-075's matching-ID invariant makes re-runs safe: existing ids cause `spec_revise_*` calls; new ids cause `spec_propose_*`. Re-running on unchanged code produces a no-op (analyzers see existing nodes match what they'd infer; gap-analyst surfaces no changes).

   `converged: false; <reason>` is emitted only when re-dispatch would naturally make progress: transient LLM partial output, analyzer subprocess crash mid-fan-out. Operator-actionable issues (analyzer disagreement, conflict needing human judgment) land as notes in the report body with `converged: true` — re-dispatch won't help.

9. **Status defaults to `inferred` for all emitted nodes.** Per DJ-075's invariant. Distinguished from refine's `proposed` status (preflight guess) and operator-edited `active`/`settled` status. Operators reviewing inferred-status nodes know the provenance: read from code, evidence-based, low-confidence-by-default.

10. **DJ-147 dry-run inheritance.** Both new tools (`spec_propose_approach`, `spec_revise_approach`) get the `captureOnly` wrapper at registration. The capture closure builds the approach body via `buildApproachBody(in, time.Time{})`, calls `store.OverlayPut(sess, "spec_propose_approach", agent.KindApproach, in.ID, body)`. DJ-147's existing test pattern in `internal/mcp/tools_spec_write_dry_run_test.go` extends with one new subtest per new tool.

    Under `assimilate --dry-run`:
    - All spec mutations (feature/decision/strategy revisions + approach proposals) capture in the overlay
    - The `tools.jsonl` post-dispatch render and the `spec_dry_run_report` MCP tool both surface them
    - No source file writes attempted (assimilate doesn't write code; only adopt does via DJ-149)
    - Exit code 0; CLI renders the markdown or JSON report

## Resolved design questions

1. **Why not "minimal" spec-only emission (the retracted version)?** Considered, retracted. Emitting features/decisions/strategies without binding them to source files via approaches violates the state-outcome principle — the result is floating specs that aren't reconciled to code. The maintenance loop has nothing to operate on. Approach synthesis is what makes assimilate's output coherent state rather than ghost hypotheses.

2. **Why not full middle-out reconciliation (refine cascades hypotheses; assimilate validates)?** Deferred to DJ-150+. Middle-out is theoretically cleaner and matches how a human engineer would approach brownfield onboarding, but it requires sophisticated infrastructure (hypothesis tracking, iterative convergence semantics, orphan classification, refine-vs-assimilate workflow integration) that we'd be designing without ever having run a simple version. Ship the simpler "code-is-truth" reconciliation first; revisit middle-out when real assimilate runs surface the friction.

3. **Why operator-driven preconditions instead of self-contained `refine goals`?** Cleaner verb boundary — each verb has one clear purpose. The friendlier auto-dispatch alternative is DJ-150+ future work. v1 ships with one extra operator step (`refine goals` → `assimilate`) and a helpful error message when the precondition isn't met.

4. **Why single-pass instead of iterative hypothesis-then-validate?** Same reason as #2 — iterative convergence requires hypothesis tracking infrastructure we'd be designing speculatively. Single-pass with re-run idempotency (DJ-075) is sufficient for v1. Operators can re-run assimilate after editing code or spec; the matching-ID invariant handles it. Iterative-with-orphan-feedback is DJ-150+.

5. **Why code-is-truth for conflict resolution (not spec-is-truth or human-arbitrated)?** assimilate is the verb that READS code. Code is the realized state; the question this verb is answering is "what does the code actually do?" If the spec disagrees, the spec is stale. Operators who want spec-is-truth resolution use adopt (which reads spec and updates code to match). The two verbs respect different sides of the manifest-vs-reality split; that's why they're separate verbs. Edge cases (aspirational spec) surface in the report and operators can reject by editing post-run.

6. **Why drop entity emission?** Council-era assimilate emitted `e-<name>` entity nodes per [DJ-076](dj-076-entity-in-memory-context-not.md), which kept them in-memory in the pipeline rather than persisted to `.borg/spec/`. The post-DJ-135 activity-playbook model has no in-memory pipeline — the agent's output goes directly to MCP tool calls. There's no "in-memory entity" surface to land in. Dropping entities entirely is cleaner than persisting them as a new node kind for no current consumer. Future DJ can revisit if a specific consumer emerges.

7. **Why no `code-paths` cache file or `codebase_hash` in this DJ?** Both are deferred to DJ-150+ as the "run-level no-op short-circuit" mechanism. v1's no-op behavior comes from DJ-075's matching-ID idempotency at the per-node level — re-running on unchanged code produces zero net mutations (analyzers see existing nodes that match what they'd infer). The run-level cache that skips even the analyzer work entirely is a performance optimization; it's not required for correctness. Adds complexity (new file format, new MCP tools, hash composition logic, dirty-tree handling) that we'd be shipping without evidence the analyzer work is actually expensive enough to need caching.

8. **Why the new approach mutation tools instead of folding into a generic `spec_propose_*` / `spec_revise_*`?** Consistency with the existing per-kind tool surface (`spec_propose_decision` / `spec_propose_feature` / `spec_propose_strategy` / `spec_propose_goal` / `spec_propose_antigoal` etc.). Per-kind tools have validated input shapes specific to that kind's body — easier for the agent to call correctly (the tool description names the field set). A generic spec mutation tool would push validation onto the agent's prompt, which is fragile.

9. **Why share `spec_propose_approach` / `spec_revise_approach` between DJ-148 and DJ-149 instead of each DJ shipping its own approach surface?** Approaches have one shape regardless of which verb synthesizes them. assimilate synthesizes approaches binding code-discovered specs; adopt synthesizes approaches for refine-added features without coverage. Same `app-` id convention, same body shape (id, parent_id, parent_kind, body, source_files, source_hash). One shared mutation surface keeps the SpecStore's approach handling uniform.

10. **Why `max_iterations: 3` instead of 20 like refine?** assimilate is single-pass-shaped. Iteration only fires on transient failures (LLM partial output, analyzer crash). Three iterations is enough buffer for retry-after-flake without being so high that a genuinely runaway loop wastes operator tokens. refine's 20 is for genuine multi-iteration deliberation (axes resolve over multiple critique passes); assimilate doesn't have that loop.

## Alternatives considered

- **Minimal spec-only emission (rejected).** "Just emit features/decisions/strategies; defer approach synthesis to DJ-149." Violates the state-outcome principle — the result is floating specs that aren't reconciled to code. The maintenance loop has nothing to operate on. See *Resolved design questions* #1.

- **Full middle-out reconciliation (deferred, not rejected).** "Refine cascades hypotheses; assimilate validates and revises." Theoretically cleaner. Requires hypothesis tracking, iterative convergence, orphan classification, refine-vs-assimilate workflow integration. Deferred to DJ-150+ as future work — see *Resolved design questions* #2.

- **Self-contained goal-layer sync inside assimilate (deferred, not rejected).** "assimilate dispatches `spec-goal-diff-matcher` as a Step 0 sub-step, so the operator doesn't need to run `refine goals` first." Friendlier UX, blurs the verb boundary. Deferred to DJ-150+; v1 operator workflow is `init → edit GOALS.md → refine goals → assimilate → adopt` with one verb per concern.

- **Council-era schemas verbatim (rejected).** Keep the existing `backend-analyzer.md` / `frontend-analyzer.md` / `infra-analyzer.md` prompts as-is — same agent names, same shapes. Rejected: their schemas have drifted from current conventions (council-era `d-` / `s-` / `e-` id prefixes vs. current `dec-` / `strat-` per DJ-133; entity emission per DJ-076 no longer applies; council-era strategies-as-build-commands don't match current `spec.Strategy`). The prompt rewrites are mandatory for the agents to produce usable output.

- **Spec-is-truth conflict resolution (rejected).** When code disagrees with existing spec, leave the spec untouched and mark the code as drifted. Rejected: that's adopt's direction (spec → code), not assimilate's. assimilate is the verb that reads code; its purpose is to update spec to reflect reality. The two-verb decomposition specifically separates these directions.

- **`code-paths` cache file + `codebase_hash` for run-level no-op (deferred, not rejected).** A `.borg/code-paths` file declares which paths to walk + cache the discovery; `codebase_hash` on the manifest enables run-level no-op short-circuit. Adds complexity (new file format with `.dockerignore`-inverted syntax, new MCP tools `spec_update_code_paths` + `spec_update_codebase_hash`, hash composition over per-file content, dirty-working-tree handling) without evidence the analyzer work is expensive enough to require it. Deferred to DJ-150+ if real assimilate runs surface the friction.

- **Orphan handling with `.borg/orphan-files` + `.borg/orphan-acceptances` sidecars (deferred, not rejected).** First-class tracking of source files that don't bind to any approach, with an operator-edited acceptance file for legitimately-orphan code (vendored, generated, third-party). Real concern (some code legitimately doesn't fit any spec node), but the v1 surface — reporting orphans in the agent's closing message without sidecar files — is sufficient until a real assimilate run shows the operator-visible report is insufficient.

- **Iterative hypothesis-then-validate cycle (deferred, not rejected).** Each assimilate run hypothesizes new spec nodes for unexplained code; subsequent runs validate or refute. Convergence over multiple runs as orphans get explained. Requires bidirectional convergence semantics, hypothesis tracking infrastructure. Deferred to DJ-150+; v1 single-pass with idempotent re-runs is sufficient for most projects.

- **Auto-dispatch `refine goals` as a precondition step (deferred, not rejected).** When goal layer is empty, assimilate dispatches `refine goals` inline before proceeding rather than refusing. Friendlier UX, but adds a nested dispatch (sub-activity-in-activity) that doesn't fit the current activity-playbook model cleanly. Deferred — v1 refuses with a helpful error.

## Consequences

**Cmd / API surface:**
- `cmd/assimilate.go` doc-comment refresh — drop the now-stale "Flags from the legacy verb (--dry-run) dropped" comment; refer to DJ-148 for the playbook contract.
- No changes to CLI flag surface (Kong fields already exist from DJ-147).
- No changes to Kong field shape.

**MCP tool surface:**
- New: `spec_propose_approach`, `spec_revise_approach` (shared with DJ-149). Both wrapped via DJ-147 `captureOnly`.
- Tool count goes from 14 wrapped mutation tools (DJ-147 baseline) to 16.
- DJ-147 dry-run tests extend with subtests for the two new tools.

**Spec data model:**
- `spec.Approach` gains `SourceFiles []string`, `SourceHash string`, `SourceHashSyncedAt time.Time`.
- On-disk YAML-frontmatter shape (per `internal/spec/approach.go`) extended with three new `yaml:` tagged fields; existing approaches (zero today) load with zero values.

**Subagent prompts (`internal/scaffold/agents/`):**
- Rewritten: `scout.md`, `backend-analyzer.md`, `frontend-analyzer.md`, `infra-analyzer.md`, `gap-analyst.md`.
- Schema drift fixed (axis-shaped IDs per DJ-133, entity emission dropped per DJ-076, strategy shape aligned with current `spec.Strategy`).
- `output_schema:` front-matter removed (council-era artifact, not enforced in activity-playbook model).

**Activity playbook (`internal/scaffold/plans/`):**
- Rewritten: `code_assimilation.md` (default fallback — single-iteration shape with goal-layer precondition check + scout dispatch + analyzer fan-out + gap-analyst reconciliation + emit).
- New: `code_assimilation.claude-code.md` (provider overlay — dynamic-workflow shape mirroring `spec_refinement.claude-code.md`).
- New: `code_assimilation.interactive.md` (mode overlay — codex/gemini interactive overlay with loop-state tool directive header).

**Activity registry (`internal/activity/agents-default.yaml`):**
- New entry `code_assimilation` with `max_iterations: 3`.

**Documentation:**
- `docs/runtime-affordances.md` — add a brief assimilate paragraph after the existing DJ-144 driver-matrix section, naming the precondition contract.
- `CLAUDE.md` — add a bullet in "Sources of Truth" naming DJ-148's bidirectional reconciliation principle.
- `docs/council.md` — update to mention the assimilate playbook's subagent flow alongside the existing refinement / ingestion flows.

**Downstream impact:**
- DJ-149 (sibling, designed alongside) ships its drift detection + on-demand new approach synthesis on top of the `spec.Approach` schema extension + `spec_propose_approach` / `spec_revise_approach` tools that DJ-148 establishes.
- DJ-147 dry-run automatically covers `assimilate --dry-run` end-to-end once the playbook body lands — no DJ-147 changes required.
- DJ-150+ future work (middle-out, orphan handling, self-contained goal-layer sync, `code-paths` cache, iterative hypothesis cycle) can be added without revisiting DJ-148's surface.

## Future Work

The following enhancements are deferred — each has a defensible v1 alternative (idempotency, operator-driven workflow, single-pass shape) so deferring doesn't violate state-outcome. They land as separate DJs when real assimilate runs surface the friction:

- **DJ-150 (provisional): Middle-out reconciliation.** Refine cascades hypotheses from GOALS.md → features/strategies/decisions → axes; assimilate validates each hypothesis against code, revises spec where code disagrees, and tracks orphan code through iterative re-runs. Required infrastructure: hypothesis tracking on spec nodes (a status or confidence field that distinguishes hypothesis-from-refine vs. confirmed-by-assimilate), bidirectional convergence semantics in the playbook (forward convergence: no new hypotheses generated; orphan convergence: the orphan set didn't shrink), refine-vs-assimilate workflow integration.

- **DJ-151 (provisional): Orphan handling with sidecar acceptance files.** First-class tracking of source files that don't bind to any approach. `.borg/orphan-files` lists current orphans (manifest field or sidecar); `.borg/orphan-acceptances` lets operators declare which orphans are accepted (vendored / generated / third-party / legitimately-out-of-scope). assimilate's playbook reads acceptances, skips hypothesis attempts for accepted orphans, and re-iterates over unaccepted ones each run until stability.

- **DJ-152 (provisional): Self-contained goal-layer sync inside assimilate.** Auto-dispatch `spec-goal-diff-matcher` as a Step 0 sub-step when goal layer is empty, removing the operator's "run refine goals first" requirement. Requires nested dispatch (sub-activity-in-activity) or a thin convenience function the playbook can call.

- **DJ-153 (provisional): `code-paths` cache file + `codebase_hash`.** `.borg/code-paths` file with `.dockerignore`-inverted syntax (include patterns; `!`-prefixed excludes) declares which paths to walk. Manifest's `codebase_hash` field enables run-level no-op short-circuit (`(code-paths content + sorted-path:content per included file) hash`). Two new MCP tools: `spec_update_code_paths` (line-aware mutation preserving comments), `spec_update_codebase_hash`. Hard-coded baseline excludes (`.borg/**`, `.locutus/**`, `GOALS.md`) apply after operator's include/exclude resolution.

- **DJ-154 (provisional): Iterative hypothesis cycle with multi-run convergence.** assimilate hypothesizes new spec nodes for unexplained code on each run; subsequent runs validate or refute. Convergence over multiple runs as orphans get explained. Requires hypothesis tracking + bidirectional convergence semantics. Composes naturally with DJ-150 (middle-out) and DJ-151 (orphan handling).

- **DJ-155 (provisional): Spec-revision conflict handling with aspirational-status escape hatch.** First-class spec node status that says "this is intentionally aspirational; don't auto-revise to match current code." assimilate respects the marker, surfaces the gap in the report, doesn't mutate. v1 surfaces this through report-and-revise (operator rejects unwanted revisions by editing back); DJ-155 promotes it to a typed marker.

## References

- [DJ-075](dj-075-assimilate-reads-existing-spec.md) — Assimilate Reads Existing Spec, Writes Back Atomically (council-era; principles carry forward, plumbing replaced).
- [DJ-076](dj-076-entity-in-memory-context-not.md) — Entity In-Memory Context Not Persisted (DJ-148 drops entity emission entirely on post-DJ-135 footing).
- [DJ-087](dj-087-approaches-are-synthesized-adopt.md) — Approaches Are Synthesized at Adopt Time, Not Refine Time (DJ-148 extends: approaches are also synthesized at assimilate time, when code provides the binding context).
- [DJ-133](dj-133-decisions-identified-by-axis.md) — Decisions identified by axis (DJ-148's analyzer prompts emit `dec-<axis>` ids per this convention).
- [DJ-134](dj-134-unified-spec-store.md) — Unified SpecStore (DJ-148's approach mutations write through the same in-process store).
- [DJ-135](dj-135-multi-runtime-pivot.md) — Multi-runtime pivot (DJ-148 lands the long-deferred phase 5 checkpoint 3 work for assimilate).
- [DJ-138](dj-138-refine-with-bias-cascade.md) — `max_iterations` per activity in `agents-default.yaml` (DJ-148 adds `code_assimilation: 3`).
- [DJ-139](dj-139-goal-layer-node-kinds.md) — Goal layer (DJ-148's precondition requires the goal layer populated).
- [DJ-141](dj-141-unanchored-goal-provenance.md) — Unanchored goal provenance (DJ-148 reads the goal layer; doesn't mutate it).
- [DJ-143](dj-143-per-runtime-tool-policy.md) — Per-runtime tool policy (DJ-148 inherits the `requireRuntime` composition pattern for any future runtime-scoping of the approach tools).
- [DJ-144](dj-144-cc-workflow-convergence.md) — Claude Code workflow convergence (DJ-148 reuses the four convergence-driver pattern; the provider overlay and mode overlay mirror `spec_refinement.claude-code.md` and `spec_refinement.interactive.md`).
- [DJ-147](dj-147-dry-run-mutation-capture.md) — Dry-run mutation capture (DJ-148's two new approach tools inherit the `captureOnly` wrapper at registration; no new dry-run plumbing required).
- [DJ-149](dj-149-adopt-drift-and-synthesis.md) — Adopt: drift detection + on-demand new approach synthesis (sibling DJ; shares the approach mutation surface DJ-148 establishes).
