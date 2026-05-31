# Code adoption (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives approach reconciliation and implementation to convergence for this project. The workflow loops until the worklist is empty or the iteration cap of {{max_iterations}} is reached. You own the loop — do not emit a `converged:` verdict line for an outer harness; this workflow is the harness.

## Plan first

Your very first action is to call `TodoWrite` with the entries you intend to execute. Mark each entry `in_progress` when you start and `completed` when it lands. A reasonable opening plan covers: Precondition check (preamble), then per-iteration: Read manifest + state (Step 1), Classify worklist (Step 2), Write plan files (Step 3), Implement in parallel (Step 4), Persist state (Step 5), Drift report (Step 6) — repeated up to {{max_iterations}} times — then Closing report.

## Start here

After laying out your plan, call `mcp__locutus__spec_list_manifest` and `mcp__locutus__state_list_records` (both, no arguments). Both are inputs to the preamble. The spec graph and state store live in the MCP server; reach for them via tools, not by reading `.borg/spec/` or `.borg/state/` files directly.

## Scope

Read the `Scope:` line in your Run context, if present. A scope note limits this run to a subset of the approach graph (for example, a single module or service boundary). When no scope is provided, the run covers the full set of approaches in the spec graph. The worklist classification in Step 2 is scoped accordingly.

## Invariants

- **All spec and state mutations route exclusively through the `mcp__locutus__spec_*` and `mcp__locutus__state_*` MCP tools.** Never call `Write` or `Edit` on any file under `.borg/spec/` or `.borg/state/` — the daemon owns coherence (in-process `SpecStore`, write-through search index, history events, per-runtime tool policy per DJ-143); those files are the persistence backing, not the source of truth.
- **`GOALS.md` is read-only.** adopt reads the goal layer via the manifest as context for plan files.
- **Failed phase halts the chain; failed branch retained.** When a phase fails (tests fail or the agent halts), that chain stops. The failed branch is kept for operator inspection; subsequent phases in the same chain are not attempted. Independent chains may continue.
- **The runtime decides phase parallelism within the workflow's structure.** The workflow's `parallel()` primitive partitions independent approach chains; within each chain, `pipeline()` sequences phase stages.

## One-time preamble — Precondition check (runs once, before the loop)

adopt requires (a) `GOALS.md` exists in the working tree, (b) the goal layer is populated, and (c) the worklist is non-empty. Check all three here.

1. **Read `GOALS.md` via the `Read` tool.** If the file is missing or empty, emit one closing message: `precondition not met — GOALS.md missing or empty. Run \`locutus init\` to scaffold a template, then edit it before running adopt.` Do not enter the loop.
2. **Inspect the manifest's `Goals` and `AntiGoals` arrays.** If both are empty, emit one closing message: `precondition not met — goal layer is empty. Run \`locutus refine goals\` first to materialize the goal layer from GOALS.md.` Do not enter the loop.
3. **Check the spec graph for `app-`, `feat-`, or `strat-` nodes.** If none exist, emit one closing message: `precondition not met — spec graph has no features, strategies, or approaches. Run \`locutus refine\` to build out the spec graph first.` Do not enter the loop.
4. **Run the Step 2 worklist analysis** (see below). If the worklist is empty after classification, emit one closing message: `no adopt work pending — all approaches are live with current SpecHashes and Artifacts.` Do not enter the loop.
5. If all preconditions hold, mark the preamble complete and enter the convergence loop.

## Convergence loop (up to {{max_iterations}} iterations)

Each iteration runs Steps 1–6 in order. The loop exits early when the worklist is empty (all approaches live + SpecHashes current + Artifacts current). The iteration cap is the safety bound.

### Step 1 — Read manifest + state

`spec_list_manifest` and `state_list_records` are already in hand from "Start here"; refresh them at the top of each subsequent iteration. Issue one batched `mcp__locutus__spec_get` with every `feat-`, `strat-`, `app-`, `dec-`, `goal-`, and `agoal-` id to fetch full bodies. Approach bodies provide `parent_id`, `decisions[]`, `advances[]`, and `respects[]` for SpecHash computation; goal/antigoal bodies provide plan file context.

### Step 2 — Identify worklist + drift classification (parallel)

For each approach id in scope, dispatch in parallel:

**Spec-side drift:** Call `mcp__locutus__state_compare_hashes` per approach. Any non-empty `added`, `removed`, or `changed` result marks the approach for `regenerate`. Orphan check: if `approach.parent_id` is absent from the manifest, call `mcp__locutus__spec_mark_approach_drifted` and categorize as `orphan-superseded`.

**Code-side drift:** For each `path → stored_hash` in the state record's `Artifacts`, stat the file. A missing file is `out_of_spec`. A hash mismatch triggers a `drift-classifier` subagent dispatch (via `parallel()`, one dispatch per mismatched file). Trivial results → call `mcp__locutus__state_refresh_artifacts`. Semantic results → call `mcp__locutus__state_mark_status` with `out_of_spec`.

Worklist categories — walk `spec_list_manifest`'s `Features` and `Strategies` arrays as well as `Approaches`; a feat/strat whose `approaches[]` is empty is also a worklist entry. Categories in priority order:
- **`synthesize_approach`** — a Feature or Strategy in scope has no Approach attached (its `approaches[]` is empty or the cited `app-<parent-id>` doesn't exist in the manifest). Per [DJ-087](../../docs/decisions/dj-087-approaches-are-synthesized-adopt.md), adopt owns approach synthesis. Dispatch `synthesizer` with the parent body + applicable decisions; call `spec_propose_approach` with the returned body and id `app-<parent-id>`; the new approach flows into the `synthesize_and_implement` chain in this same iteration. Idempotent: on re-run the parent's `approaches[]` is non-empty, so this category is naturally skipped.
- **`synthesize_and_implement`** — no state record (approach never implemented).
- **`implement`** — state record exists with status `planned` or `pre_flight`.
- **`regenerate`** — spec-drifted; the approach body needs revision before implementation.
- Skip approaches with status `live` whose SpecHashes and Artifacts show no drift.

When an approach is both spec-drifted and code is `out_of_spec`, categorize as `out_of_spec` (takes precedence). Surface both drift sources in Step 6.

If the worklist is empty, exit the loop.

### Step 3 — Write plan files

For each worklist entry, write a plan file to `.locutus/sessions/<sid>/plans/<NNN>-<approach-id>.md` where `<NNN>` is a zero-padded 3-digit ordinal that determines execution order. Use `Bash` to `mkdir -p` the plans directory; these are ephemeral session artifacts, not spec mutations.

For `regenerate` entries, first dispatch `approach-regenerator` with the current approach body and the revised parent spec bodies. Commit the revised body via `mcp__locutus__spec_revise_approach` before writing the plan file — the plan file's approach body section reflects the regenerated text.

Each plan file carries YAML frontmatter (`approach_id`, `parent_id`, `parent_kind`, `category`, `branch_name: adopt/<NNN>-<approach-id>`, `decisions[]`, `advances[]`, `respects[]`) followed by context sections (parent body, relevant decisions, goals, anti-goals, approach body, acceptance criteria) and a closing instruction to call `mcp__locutus__state_record_reconciliation` with `artifacts` (sha256 hash per written file) and `test_outcome: "passed"` or `"failed"`. See `code_adoption.md` § "Step 3 — Write master plan files" for the full plan file structure.

### Step 4 — Implementation fan-out

**`synthesize_approach` entries run first, in `parallel()`.** For each worklist entry with category `synthesize_approach`, dispatch the `synthesizer` subagent (one per orphan parent, all in parallel): `synthesizer` receives the parent body + applicable decisions and returns the approach body. For each completed synthesis, call `mcp__locutus__spec_propose_approach` with the returned body and id `app-<parent-id>`. The now-existing approaches immediately enter the `synthesize_and_implement` chain for this iteration — write their plan files (Step 3 template) and include them in the fan-out below.

**Partition approaches.** Approaches with no shared parent and no shared artifact paths are independent. Group them into independent chains. Within each chain, phases are ordered by `<NNN>` ordinal.

**Use `pipeline()` per chain.** Phase N+1 branches off phase N's branch (`adopt/<NNN+1>-...` off `adopt/<NNN>-...`) — stacked branches. Each stage in the pipeline runs the plan-file body in order.

**Use `parallel()` across independent chains.** Independent chains run concurrently. Use `isolation: 'worktree'` for each parallel agent dispatch that mutates source files — each worktree is the chain's own checkout. After a chain completes, its worktree branch is merged (or left as a standalone branch for operator review); the worktree itself is cleaned up by the workflow.

**Each implementation agent:** reads its plan file, writes and edits source files, runs the project's test suite, then calls `mcp__locutus__state_record_reconciliation` with the artifact map and `test_outcome: "passed"` or `"failed"`. If the project has no test suite, the agent scaffolds one with its native skills or halts the phase as operator-actionable rather than assuming `passed`.

**Halt on first failure within a chain.** The failed branch is retained for operator review. Other independent chains that are already running via `parallel()` may complete; new chains that have not yet started are not dispatched. The closing report records which chains completed, which halted, and which were never dispatched due to an upstream halt.

### Step 5 — Persist state

After this iteration's fan-out completes (or halts), call `mcp__locutus__state_list_records` to verify state coverage. For each worklist entry:
- State record with status `live` or `failed` — expected. Note the outcome.
- State record absent — halt-skipped (downstream of a failed chain). Note as "halt-skipped".
- State record absent with no upstream failure — surface a warning: `Expected state_record_reconciliation for <approach-id> but no record found.`

### Step 6 — Drift classification report

Summarize the Step 2 drift findings for this iteration:
- Trivial code drifts auto-accepted (approach id + file count updated per approach).
- Semantic code drifts flagged `out_of_spec` (require operator decision before next run).
- Spec-drifted approaches (approach id + which spec ids changed).
- Orphaned approaches (approach id + absent parent id).
- Out-of-spec precedence cases (both drift sources listed).

After Step 6, check the worklist: if empty and no failures, the loop converges. Otherwise begin the next iteration.

## Closing report

After the loop exits (convergence, iteration cap, or halt), produce the operator-facing summary:

- **Iterations run.**
- **Per-approach results table** — id, category, branch, test outcome, status.
- **Branch list** — so the operator can `git log adopt/...` to review work.
- **Drifts surfaced** — trivial accepted vs semantic regenerated vs `out_of_spec`.
- **Convergence outcome:** converged-cleanly | hit-iteration-cap | halt-on-failure.

No trailing `converged:` verdict line — this workflow is the harness. (`dispatchUsesOuterLoop("claude-code")` is false per DJ-144; the CC workflow does not go through the `OuterLoopRunner`.)

The `{{max_iterations}}` token in the loop title and the Plan-first `TodoWrite` entry is injected by the activity registry at dispatch time (Task 19 of DJ-149). The workflow enforces it as its own iteration counter — stopping after that many pass completions regardless of worklist state and noting "hit-iteration-cap" in the closing report.
