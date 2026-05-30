# Code Assimilation (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives the brownfield assimilation work for this project to convergence. The workflow loops until the gap-analyst reports no new mutations or the iteration cap of {{max_iterations}} is reached. You own the loop; do not emit a single `converged:` verdict line for an outer harness — this workflow is the harness.

## Plan first

Your very first action is to call `TodoWrite` with the entries you intend to execute. Mark each entry `in_progress` when you start and `completed` when it lands. A reasonable opening plan covers: Precondition check (preamble), then per-iteration: Discover code, Read manifest, Scout survey, Analyzer fan-out, Reconciliation, Emit + approach synthesis — repeated up to {{max_iterations}} times — then Report.

## Start here

After laying out your plan, call `mcp__locutus__spec_list_manifest` (no arguments) to read the current spec graph state. The manifest carries every node — including `goal-*` and `agoal-*` ids — and the `goals_md_hash` field. Then read `GOALS.md` once with the `Read` tool. Both are inputs to the preamble.

## Invariants

- **Spec mutations route exclusively through the `mcp__locutus__spec_*` MCP tools.** Never call `Write` or `Edit` on any file under `.borg/spec/` — those files are the SpecStore's persistence backing, not its source of truth (DJ-134). The daemon owns coherence.
- **`GOALS.md` is read-only for the duration of this run.** Read it once with the `Read` tool when the playbook says to. Never call `Write` or `Edit` on `GOALS.md`. The goal layer is operator-authored via `refine`.

## One-time preamble — Precondition check (runs once, before the loop)

assimilate requires (a) `GOALS.md` exists and (b) goal layer is populated. Check both. If either fails:
- Emit a single closing message naming the unmet precondition and the operator's fix.
- Do not enter the workflow loop.

If both preconditions hold, mark the preamble plan entry complete and enter the convergence loop.

## Convergence loop (up to {{max_iterations}} iterations)

Each iteration runs Steps 1-6 in order. The loop exits early when the gap-analyst's reconciliation plan is empty (no confirms, no revises, no proposes) — that's convergence. The iteration cap is the safety bound.

### Step 1 — Discover code

Run `git ls-files` via the `Bash` tool. Apply hardcoded excludes (`.borg/**`, `.locutus/**`, `GOALS.md`, `docs/**`, `**/README.md`, `**/CHANGELOG.md`). The remaining set is the source surface.

### Step 2 — Read manifest + goal layer for context

Call `mcp__locutus__spec_list_manifest`. Issue one batched `mcp__locutus__spec_get` with every `goal-*` / `agoal-*` / `feat-` / `dec-` / `strat-` / `app-` id.

### Step 3 — Scout survey (sequential)

Dispatch `scout` with the source file list + goal-layer bodies. Wait for it to return its ScoutSummary.

### Step 4 — Analyzer fan-out (parallel)

Spawn three concurrent subagent dispatches in the workflow:
- `backend-analyzer` with scout summary + backend files
- `frontend-analyzer` with scout summary + frontend files (early-exits if none)
- `infra-analyzer` with scout summary + infra files

Wait for all three to complete. Collect their structured responses.

### Step 5 — Reconciliation (sequential)

Dispatch `gap-analyst` with the three analyzer contributions + the existing manifest's nodes + the goal-layer bodies. It returns the per-id action plan.

### Step 6 — Emit + approach synthesis

For each gap-analyst action:
- **Confirm**: no MCP call.
- **Revise**: call `mcp__locutus__spec_revise_<kind>` with the revised body; status stays `inferred`.
- **Propose**: call `mcp__locutus__spec_propose_<kind>` with `status: inferred`.

For every feature or strategy confirmed, revised, or proposed, also synthesize the approach:
1. Compute `source_hash` for the cited files: `find <files> -type f | sort | xargs shasum -a 256 | shasum -a 256 | cut -d' ' -f1`, prefix with `sha256:`.
2. Call `mcp__locutus__spec_propose_approach` (or `spec_revise_approach`) with id `app-<parent-id>`, parent_id, source_files, source_hash, body.

If the gap-analyst's action plan was empty AND no approaches were synthesized this iteration, emit `converged` and exit the loop. Otherwise loop to Step 1.

## Closing report

After the loop exits (either by convergence or by hitting the {{max_iterations}} cap), produce a closing summary:
- Iterations run.
- Net changes by kind (decisions revised, features proposed, etc.).
- Approaches synthesized with their `source_hash`.
- Any analyzer disagreements that landed as low-confidence revisions (so the operator can spot intent-vs-reality divergence).
- Convergence outcome: converged-cleanly OR hit-iteration-cap OR precondition-failed.
