# Code Assimilation (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives the brownfield assimilation work for this project to convergence. The workflow loops until the gap-analyst reports no new mutations or the iteration cap of {{max_iterations}} is reached. You own the loop; do not emit a single `converged:` verdict line for an outer harness — this workflow is the harness.

## Plan first

Your very first action is to call `TodoWrite` with the entries you intend to execute. Mark each entry `in_progress` when you start and `completed` when it lands. A reasonable opening plan covers: Precondition check (preamble), then per-iteration: Discover code, Read manifest, Scout survey, Analyzer fan-out, Reconciliation, Coverage critic, Emit + approach synthesis — repeated up to {{max_iterations}} times — then Report.

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

Each iteration runs Steps 1-7 in order. The loop exits early when the gap-analyst's reconciliation plan is empty (no confirms, no revises, no proposes) — that's convergence. The iteration cap is the safety bound.

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

### Step 6 — Coverage critic fan-out (parallel per identified deliverable shape)

After the gap-analyst's reconciliation step returns its feature/strategy plan, dispatch `spec-coverage-critic` via `parallel()` — one dispatch per identified deliverable shape. Each dispatch receives:
- The shape entry (`{shape_id, shape_label, source_evidence}`) — a **category identifier** naming the kind of deliverable, not a component type. In assimilate, identify shapes by reading GOALS.md and the gap-analyst's output as a whole: emit one `shape_id` per distinct deliverable category the project produces. Component analysis (backend + frontend + infra) informs feature/strategy proposals but does not multiply shapes — a repo with backend service, frontend client, and infra IaC for a single web product is ONE shape (`hosted-code-with-users`). A strategy id (`strat-*`) is never a valid shape id.
- The current features array — `{id, title, summary, acceptance_criteria, body_excerpt}` for every feature in the manifest after the gap-analyst's reconciliation lands (confirmed-existing, revised, and newly-proposed). `acceptance_criteria` feed the critic's ownership judgment; `body_excerpt` is the first ~500 characters of each feature's body.
- The goal layer — `{id, title, description}` for every `goal-*` and `agoal-*` node in the current manifest.

Collect every dispatch's `CoverageReport`; concatenate the uncovered-obligation entries (those whose `covered_by` is empty); feed them into Step 7's input alongside the gap-analyst's feature/strategy plan. Step 7 addresses each uncovered obligation by either (a) extending an existing inferred feature's body via `spec_revise_feature` or (b) proposing a new feature via `spec_propose_feature`. The coverage critic itself writes nothing to the spec graph — that crosses the role boundary per DJ-150 §1.

For single-deliverable projects, the `parallel()` reduces to one dispatch but the workflow shape stays consistent. When the gap-analyst returned an empty action plan (no confirms, no revises, no proposes), skip this step and proceed to the convergence check.

See `code_assimilation.md` § "Step 6 — Coverage critic (per identified deliverable shape)" for the full prose on the shape-inference heuristic, role boundary, and how uncovered obligations get addressed downstream.

### Step 7 — Emit + approach synthesis + state record

For each gap-analyst action:
- **Confirm**: no MCP call.
- **Revise**: call `mcp__locutus__spec_revise_<kind>` with the revised body; status stays `inferred`.
- **Propose**: call `mcp__locutus__spec_propose_<kind>` with `status: inferred`.

For every feature or strategy confirmed, revised, or proposed, synthesize the approach body AND record its state — dispatch these in parallel within the iteration for throughput:

1. Call `mcp__locutus__spec_propose_approach` (or `spec_revise_approach`) with id `app-<parent-id>`, `parent_id`, and a brief markdown `body` naming what the code currently does. No `source_files` or `source_hash` on the body — see the default playbook's Step 7 for the full rationale.
2. Compute per-file artifact hashes for each cited source file via `shasum -a 256 <file> | cut -d' ' -f1` (prefix result with `sha256:`); assemble the path → hash map.
3. Call `mcp__locutus__state_record_reconciliation` with `approach_id`, `artifacts` (the path→hash map), `branch_name: "assimilate-derived"`, `test_outcome` (`"passed"` if analyzer evidence includes confirmed test coverage; `"failed"` otherwise so the operator's next `status` query flags it), `test_command`, and optional `test_output_excerpt`. Do not pass `spec_hashes`; the server fills it.

If the gap-analyst's action plan was empty AND no approaches were synthesized or recorded this iteration, emit `converged` and exit the loop. Otherwise loop to Step 1.

## Closing report

After the loop exits (either by convergence or by hitting the {{max_iterations}} cap), produce a closing summary:
- Iterations run.
- Net changes by kind (decisions revised, features proposed, etc.).
- Approaches synthesized with their state records (artifact file counts, test_outcome per approach).
- Any analyzer disagreements that landed as low-confidence revisions (so the operator can spot intent-vs-reality divergence).
- Convergence outcome: converged-cleanly OR hit-iteration-cap OR precondition-failed.
