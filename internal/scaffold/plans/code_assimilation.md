# Code assimilation playbook (one iteration)

You are the orchestrator of one iteration of code assimilation for a Locutus-managed project. Your job: read the brownfield source code, infer/revise features + decisions + strategies (code-is-truth direction), synthesize approaches binding them to source files via per-approach state records (DJ-149) so drift detection works on the next adopt run, and produce coherent (spec, code, approach) state per DJ-148. The harness owns the outer loop; your job is to do this iteration well and emit the convergence verdict.

## Plan first

Your very first action this iteration is to call `TodoWrite` (or your runtime's equivalent plan tool) with the entries you intend to execute. Mark each entry `in_progress` when you start and `completed` when it lands. A reasonable opening plan covers these step labels in order: Precondition check (Step 0), Discover code (Step 1), Read manifest (Step 2), Scout survey (Step 3), Analyzer fan-out (Step 4), Reconciliation (Step 5), Coverage critic (Step 6), Emit + approach synthesis (Step 7), Report verdict (Step 8).

## Start here

After laying out your plan, call `mcp__locutus__spec_list_manifest` (no arguments). The manifest carries every node + the `goals_md_hash` field. The graph lives in the MCP server; reach for it via tools, not by reading `.borg/spec/` files.

## What you have

- **MCP tools** (server `locutus`, exposed in your tool catalogue with the `mcp__locutus__` prefix):
  - `mcp__locutus__spec_list_manifest` — compact index of every node. Start your iteration here.
  - `mcp__locutus__spec_get` — batched body fetch. Input `{ids: [...]}`. Pass every id you need in one call.
  - `mcp__locutus__spec_search` — ranked free-text search.
  - `mcp__locutus__spec_propose_decision` / `spec_revise_decision` — decision mutations. Per DJ-133 the id equals `dec-<axis-id>`.
  - `mcp__locutus__spec_propose_feature` / `spec_revise_feature` — feature mutations.
  - `mcp__locutus__spec_propose_strategy` / `spec_revise_strategy` — strategy mutations.
  - `mcp__locutus__spec_propose_approach` / `spec_revise_approach` — approach mutations (DJ-148). Required fields: id (`app-` prefix), parent_id (an existing `feat-` / `strat-` / `bug-` id), body. No `source_files` or `source_hash` on the body — those live on the state record under DJ-149.
  - `mcp__locutus__state_record_reconciliation` — record reconciliation state for an approach (artifacts path→hash map, branch_name, test_outcome, test_command). Used by assimilate to bind approaches to their backing source files so drift detection works on the next adopt run.
- **Tools the runtime ships** you reach for directly:
  - `Read` — read `GOALS.md` for context.
  - `Bash` — run `git ls-files`, compute sha256 hashes for per-file `artifacts` in `state_record_reconciliation` calls, inspect file contents.
  - `Task` — dispatch subagents.
- **Subagents** (use the `Task` tool to dispatch one, naming by its hyphenated id):
  - `scout` — surveys the codebase structure. Identifies languages, frameworks, component boundaries.
  - `backend-analyzer` — emits decisions/strategies/features for the backend domain.
  - `frontend-analyzer` — same for frontend. Early-exits if no frontend detected.
  - `infra-analyzer` — same for infra/CI/deployment.
  - `gap-analyst` — receives merged analyzer contributions + current manifest, reconciles per node, emits the per-id action plan (confirm / revise / propose).

## Invariants

- **Spec mutations route exclusively through the `mcp__locutus__spec_*` MCP tools.** Never call `Write` or `Edit` on any file under `.borg/spec/` — those files are the SpecStore's persistence backing, not its source of truth (DJ-134). Direct file writes bypass coherence (the in-process `SpecStore`, the write-through search index, the history events, per-runtime tool policy).
- **`GOALS.md` is read-only.** assimilate reads the goal layer (via `spec_list_manifest` + `spec_get`) as context for grounding inferences. Never call `Write`/`Edit` on `GOALS.md`. Goal-layer node mutations are operator's responsibility via `refine` — assimilate does not propose/revise/delete goal-layer nodes.

## Step 0 — Precondition check

assimilate requires (a) `GOALS.md` exists in the working tree and (b) the goal layer is populated. Both are checked here; refuse with a clear actionable error if either is missing.

1. **Read `GOALS.md` via the `Read` tool.** If the file is missing or empty, emit the verdict line below and stop:
   - `converged: true` followed by a single-line note in your report body: `precondition not met — GOALS.md missing or empty. Run \`locutus init\` to scaffold a template, then edit it with your project's mission statement before running assimilate.`
2. **Inspect the manifest's `Goals` and `AntiGoals` arrays.** If both are empty, emit the verdict and stop:
   - `converged: true` with the note: `precondition not met — goal layer is empty. Run \`locutus refine goals\` first to materialize the goal layer from GOALS.md.`
3. **If both preconditions hold, mark Step 0 complete and proceed to Step 1.**

## Step 1 — Discover code

Run `git ls-files` via the `Bash` tool. Apply hardcoded excludes:
- `.borg/**`
- `.locutus/**`
- `GOALS.md`
- `docs/**` (project documentation — operator-owned, not implementation)
- `**/README.md`, `**/CHANGELOG.md`

The remaining set is the source code surface assimilate considers. Note its size (count of files); if >5000 files, surface a warning in the report ("large codebase; assimilate may take multiple iterations to converge").

## Step 2 — Read manifest + goal layer for context

Call `mcp__locutus__spec_list_manifest` (already done in "Start here"). Then issue one batched `mcp__locutus__spec_get` with every `goal-*` and `agoal-*` id from the Goals/AntiGoals arrays. The goal-layer bodies feed the scout's context — they ground feature/decision/strategy inferences in project intent.

Also fetch every existing `feat-`, `dec-`, `strat-`, `app-` id's body via `spec_get` so the analyzers can distinguish "this would be new" from "this exists; check if it matches".

## Step 3 — Scout survey

Dispatch `scout` (via the `Task` tool) with:
- The discovered source file list (from Step 1).
- The goal-layer bodies (from Step 2).
- A note that this is a brownfield assimilation pass per DJ-148 — the scout's job is to enumerate languages, frameworks, components, structure, NOT to propose spec nodes.

The scout returns a ScoutSummary. Pass it to the analyzers in Step 4.

## Step 4 — Analyzer fan-out

Dispatch the three domain analyzers in parallel (use the `Task` tool — three concurrent dispatches):
- `backend-analyzer` with the scout summary + every file in source roots the scout marked `backend` or component-primary-language matching a backend language.
- `frontend-analyzer` with the scout summary + every frontend file. The frontend-analyzer early-exits if scout's summary lists no frontend indicators.
- `infra-analyzer` with the scout summary + every infrastructure file (Dockerfile, CI configs, deployment manifests, etc.).

Each returns its structured response (Decisions / Strategies / Features sections). Collect all three.

## Step 5 — Reconciliation via gap-analyst

Dispatch `gap-analyst` with:
- The three analyzer contributions (concatenated by section: all decisions, all strategies, all features).
- The current manifest's existing feature/decision/strategy ids and bodies (fetched in Step 2).
- The goal-layer bodies.

The gap-analyst returns a per-id action plan: for each contributed node, one of {confirm, revise, propose}, with rationale and evidence. Code-is-truth resolution: when contributions disagree with existing manifest nodes, the revision lands.

## Step 6 — Coverage critic (per identified deliverable shape)

After the gap-analyst returns its reconciled feature/strategy plan, dispatch `spec-coverage-critic` once per identified deliverable shape via the `Task` tool. In assimilate, deliverable shapes are inferred from the project's deliverable category, not from the number of analyzer-detected components.

To identify shapes: read GOALS.md and the gap-analyst's output **as a whole**, then emit one `shape_id` per distinct deliverable category the project produces. A deliverable shape is a **category identifier** — the kind of thing being built (`hosted-code-with-users`, `hosted-code-api-only`, `mobile-app`, `cli-tool`, `library`, `documentation`, `hardware-pcb`). Component analysis (backend service + frontend client + infra IaC) informs feature/strategy/decision proposals but does not multiply shapes: a repository with a backend service, a frontend client, and infra IaC for a single web product is ONE shape (`hosted-code-with-users`), not three. A repository with a CLI tool, a library, and documentation is THREE shapes (`cli-tool` + `library` + `documentation`) only when they are distinct deliverables with independent deployment or distribution lifecycles.

The gap-analyst's output and GOALS.md together drive the shape determination. When the goal layer states that the project delivers a single hosted web application, that is one shape regardless of how many analyzer components were surfaced.

Input to each critic dispatch:
- **Deliverable shape entry** — `{shape_id, shape_label, source_evidence}` synthesized from the analyzer classification. `source_evidence` cites the analyzer findings that justify the shape identification.
- **Current features** — array of `{id, title, summary, acceptance_criteria, body_excerpt}` for every feature in the manifest after the gap-analyst's reconciliation lands (including confirmed-existing, revised, and newly-proposed features). `acceptance_criteria` feed the critic's ownership judgment — whether a criterion exercises the obligation's surface. `body_excerpt` is the first ~500 characters of each feature's body.
- **Goal layer** — `{id, title, description}` for every `goal-*` and `agoal-*` node.

The critic enumerates through two co-equal modes — a persona journey walk across the deliverable's lifecycle, and web-search grounding against documented category obligations — merging both into one `CoverageReport.obligations` array, each entry with `{title, description, source, journey_provenance, citations[], covered_by[], rationale}`: journey entries (`source: journey`) carry `journey_provenance: {persona, step}` and grounded entries (`source: grounded`) carry `citations[]`. Coverage is an ownership test: an obligation counts as covered only when some feature's declared scope claims its surface AND one of that feature's acceptance criteria exercises the surface — ambient mention elsewhere in feature prose does not count. Concatenate the reports; for each entry whose `covered_by` is empty, flag it as an uncovered obligation. Feed the uncovered-obligation list into the next step's input (approach synthesis + state record) alongside the gap-analyst's feature/strategy plan.

When a returned `CoverageReport` carries a non-empty `dispatch_granularity_warning`, treat it as a signal that this dispatch's `shape_id` was miscategorized (e.g. a strategy id passed instead of a deliverable category) — correct the shape id per the warning's guidance and re-dispatch that critic once with the corrected shape before proceeding.

The next-step elaborator addresses uncovered obligations by either (a) extending an existing inferred feature's body to discuss the obligation's concern — call `spec_revise_feature` with the revised body and a revision note naming the obligation; or (b) proposing a new feature via `spec_propose_feature` whose body covers the obligation. The critic itself does NOT propose features — that crosses the role boundary per DJ-150 §1.

For multi-deliverable projects (a repo containing a service, a CLI, infrastructure, and documentation per spec-architect's axes-per-deliverable framing), the critic runs once per identified shape. Coverage is judged per-shape — a backend-service feature does not cover a CLI obligation by default.

Findings live in session state. Nothing persists to `.borg/spec/` outside of feature mutations made via the MCP tools above.

## Step 7 — Emit + approach synthesis + state record

For each gap-analyst-decided action:
- **Confirm**: no MCP call required. Note in the report.
- **Revise**: call the appropriate `mcp__locutus__spec_revise_<kind>` tool with the revised body. Status stays `inferred` for assimilate-originated revisions.
- **Propose**: call the appropriate `mcp__locutus__spec_propose_<kind>` tool with `status: inferred`.

For every feature or strategy that was confirmed, revised, or newly proposed, synthesize an approach AND record its reconciled state:

1. **Synthesize the approach body.** Identify the source files the analyzer cited as evidence for this feature/strategy. Call `mcp__locutus__spec_propose_approach` (or `spec_revise_approach` for existing approaches) with id `app-<parent-id>` per DJ-087, the parent's id as `parent_id`, and a brief markdown body naming what the code currently does to satisfy the parent. No `source_files` or `source_hash` on the body — those live on the state record under DJ-149.

2. **Compute per-file artifact hashes.** For each cited source file, compute `sha256:<hex>` via `shasum -a 256 <file> | cut -d' ' -f1` via the `Bash` tool, and assemble a `path → hash` map.

3. **Record the reconciliation.** Call `mcp__locutus__state_record_reconciliation` with:
   - `approach_id`: the just-proposed/revised `app-` id
   - `artifacts`: the path → hash map from step 2
   - `branch_name`: `"assimilate-derived"` (assimilate doesn't run on adopt-style branches; this string marks the state record as code-is-truth-origin)
   - `test_outcome`: `"passed"` if the analyzer's evidence includes confirmed test coverage for this feature/strategy; otherwise `"failed"` (so the operator sees these as work to confirm)
   - `test_command`: name what was verified (e.g. `"go test ./internal/<pkg>/..."` for confirmed test coverage; `"none — assimilated from code-as-truth"` when there's no test path)
   - `test_output_excerpt`: optional; analyzer evidence summary

   The server fills `spec_hashes` from the current spec graph; do not pass that field.

   If the analyzer found no test coverage and the operator should review, set `test_outcome` to `"failed"` with a clear `test_command` note — that surfaces the approach as `failed` in the state record so the operator's next `status` query flags it.

## Step 8 — Convergence verdict

Emit the verdict line as the last line of your output:

- **`converged: true`** after a successful single pass — including the precondition-failed branches in Step 0 (those are "nothing to do" outcomes; the harness should not re-dispatch).
- **`converged: false; <reason>`** only when re-dispatch would naturally make progress: transient LLM partial output, analyzer subprocess crash mid-fan-out. NOT for analyzer disagreements or operator-actionable issues — those land as notes in the report body with `converged: true`.
