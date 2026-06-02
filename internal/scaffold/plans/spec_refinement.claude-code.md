# Spec Refinement (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives the spec graph to convergence for the target named in your Run context (`Target:`, default `goals`). The workflow loops until the scout reports convergence or the iteration cap of {{max_iterations}} is reached. You own the loop; do not emit a single `converged:` verdict line for an outer harness — this workflow is the harness.

## Target scoping

Read the `Target:` line in your Run context. Two cases:

- `Target: goals` — the root. Survey the whole spec graph against `GOALS.md`; convergence is the scout's whole-graph verdict.
- `Target: <node-id>` (e.g. `Target: dec-oltp-store`) — a single spec node. Scope the survey to that node's subtree: the axes that resolve to it, the features and strategies that reference it, and the decisions it depends on. Convergence is the scout's verdict on that subtree.

Pass the target to `spec-scout` in its survey input so its `axes_open` / `new_nodes` / `critique_dimensions` are bounded to the scope.

## Plan first

Your very first action is to call `TodoWrite` with the entries you intend to execute. Mark each entry `in_progress` when you start it and `completed` when it lands. The harness renders your plan entries inline so the operator sees what you've scheduled and how far through it you are. Update as work progresses, not in a batch at the end.

A reasonable opening plan covers these step labels: Goal-layer sync (preamble), then per-iteration: Coverage critic, Survey, Decide open axes, Elaborate new nodes, Critique, Reconcile, Cascade revisions, Confirm landings — repeated up to {{max_iterations}} times — then Citation walk (post-loop), Report. You will add or split entries as the matcher returns its diff and the critic returns uncovered obligations and the survey returns axes and new-node lists.

## Start here

After laying out your plan, call `mcp__locutus__spec_list_manifest` (no arguments) to read the current spec graph state. The manifest carries every node — including `goal-*` and `agoal-*` ids — and the `goals_md_hash` field that drives the one-time goal-layer sync. Then read `GOALS.md` once with the `Read` tool. Both are inputs to the preamble.

## Invariants

- **Spec mutations route exclusively through the `mcp__locutus__spec_*` MCP tools.** Never call `Write` or `Edit` on any file under `.borg/spec/` — those files are the SpecStore's persistence backing, not its source of truth (DJ-134). The daemon owns coherence (in-process `SpecStore` + write-through search index + history events + per-runtime tool policy per DJ-143); direct file writes bypass all of it. When you need to mutate the graph, the right tool is one of `spec_propose_*` / `spec_revise_*` / `spec_delete_*` (or `spec_mark_approach_drifted` for DJ-138 drift marks).
- **`GOALS.md` is read-only for the duration of this run.** Read it once with the `Read` tool when the playbook says to. Never call `Write` or `Edit` on `GOALS.md` — the operator owns its content (DJ-139 RQ1: "humans only edit GOALS.md"). The matcher's `promoted` / `contradicted` moves apply to goal-layer *nodes* via the goal-layer MCP tools (`spec_propose_goal` / `spec_revise_goal` / `spec_delete_goal` and the AntiGoal variants), not to the `GOALS.md` file. If your iteration drafts a unified diff against `GOALS.md` (e.g. `feature_ingestion` Branch B), the diff is *output for operator review*, never a patch the workflow applies itself.

## One-time preamble — Goal-layer sync (runs once, before the loop)

Reconcile the persisted goal layer against the current text of `GOALS.md`. GOALS.md is the unchanging upstream input for this workflow run; sync it once here, not per iteration.

Walk in order:

1. **Compute the current GOALS.md hash.** Run `shasum -a 256 GOALS.md` via the `Bash` tool. The output is `<hex>  GOALS.md`; the canonical hash form is `sha256:<hex>` (prefix the hex with `sha256:`).
2. **Compare against the manifest's `goals_md_hash`.** Two paths:
   - **Hashes match.** Mark the Goal-layer-sync plan entry `completed` with the note "hash matches; sync skipped" and proceed to the convergence loop.
   - **Hashes differ (or the manifest's hash is empty).** Continue to step 3.
3. **Gather the existing goal layer.** Read the manifest's `Goals` and `AntiGoals` arrays. Issue one batched `mcp__locutus__spec_get` with every id in those arrays — the matcher needs each node's full body (`source_clause`, `body`, `ceded_to`, `kept_in`) to do its matching work.
4. **Dispatch `spec-goal-diff-matcher`.** Pass it the current `GOALS.md` content (from your earlier `Read` call) plus the array of existing nodes from step 3. The matcher returns a structured diff with six arrays: `unchanged`, `modified`, `deleted`, `added`, `promoted`, `contradicted`.
5. **First run bootstrap affordance (conditional).** When the manifest carries zero existing `goal-*` and `agoal-*` ids AND at least one settled `dec-*` body enumerates scope claims, include a one-time directive in the matcher's input: "first run bootstrap — treat decisions whose body enumerates scope claims as secondary sources alongside `GOALS.md` for this one-time seeding." Pass the bodies of those scope-encoding decisions in the matcher's payload. Claims sourced from a decision body have no verbatim `GOALS.md` excerpt — instruct the matcher to emit them as **unanchored** (`origin` set to the decision id or `"mission statement"`, `source_clause` omitted).
6. **Apply the diff.** For each entry the matcher returned:
   - `unchanged` — no action.
   - `modified` (`goal-*`) — call `mcp__locutus__spec_revise_goal` with the matcher's `id`, `new_source_clause`, and `new_body`. AntiGoal variants land via `mcp__locutus__spec_revise_antigoal`, carrying `new_ceded_to` and `new_kept_in` when present.
   - `deleted` — call the matching `mcp__locutus__spec_delete_goal` or `mcp__locutus__spec_delete_antigoal` with the `id` and `reason` the matcher supplied. Only anchored nodes appear here; unanchored nodes are never deleted by absence.
   - `added` — mint the new id as `goal-<proposed_slug>` or `agoal-<proposed_slug>`. Call `mcp__locutus__spec_propose_goal` or `mcp__locutus__spec_propose_antigoal` with the full body.
   - `promoted` — call `mcp__locutus__spec_revise_goal` / `mcp__locutus__spec_revise_antigoal` with the node's `id`, the matcher's `new_source_clause` as `source_clause`, and no `origin` — anchors the previously-inferred node in place, preserving the id and its incoming citations.
   - `contradicted` — auto-resolve toward GOALS.md: call `mcp__locutus__spec_delete_goal` / `mcp__locutus__spec_delete_antigoal` on the matcher's `retire_id`, then `mcp__locutus__spec_propose_goal` / `mcp__locutus__spec_propose_antigoal` for the matcher's `new_node`. Record the matcher's `citing_ids` — those nodes are at-risk and surface in the final report.
7. **Persist the new hash.** Call `mcp__locutus__spec_update_goals_md_hash` with the `sha256:<hex>` value from step 1. This completes the preamble.

Keep track of which `goal-*` / `agoal-*` ids changed (anything in `modified`, `deleted`, `added`, `promoted`, or `contradicted`). The post-loop citation walk uses this set to decide which existing nodes need re-judgement.

## Convergence loop (repeat until converged or {{max_iterations}} reached)

Each iteration runs the following phases in order. Phase 0 (Coverage critic) must complete before Phase 1 (Survey); the scout receives the critic's output as input. Phase 1 must complete before Phase 2 fan-out begins. Maintain a phase barrier between Phase 2 fan-out and the serialized write phases (Phase 3 onward) — do not proceed to Phase 3 until all parallel subagents from Phase 2 have returned.

**Phase 0 — Coverage critic fan-out (parallel per deliverable shape).**
Before dispatching the scout, dispatch `spec-coverage-critic` via `parallel()` — one dispatch per identified deliverable shape. Each dispatch receives:

- The deliverable shape entry (`{shape_id, shape_label, source_evidence}`) — a **category identifier** naming the kind of thing being built. Multiple foundational strategies typically share one shape: a single hosted web application has strategies for capacity, isolation, observability, build pipeline, and more, but it is still ONE shape (`hosted-code-with-users`). To identify shapes, read GOALS.md and the foundational strategies as a whole, then emit one `shape_id` per distinct deliverable category. A strategy id (`strat-*`) is never a valid shape id.
- The current features array — `{id, title, summary, body_excerpt}` for every feature in the current manifest (`body_excerpt` is the first ~500 characters).
- The goal layer — `{id, title, description}` for every `goal-*` and `agoal-*` node in the current manifest.

On iteration 1, read the current manifest for any existing foundational strategies from prior refine invocations. When the manifest carries no foundational strategies at all (a greenfield project on its very first refine), skip Phase 0 — emit an empty `uncovered_obligations` list and proceed to Phase 1. The first iteration's scout and elaborators will author foundational strategies; the next iteration's Phase 0 will have them to work from.

Collect every dispatch's `CoverageReport`; concatenate the uncovered-obligation entries (those whose `covered_by` is empty); carry the resulting `uncovered_obligations` list into Phase 1 (Survey) as additional convergence-blocking input for the scout. The coverage critic itself writes nothing to the spec graph — that crosses the role boundary per [DJ-150](../../docs/decisions/dj-150-spec-coverage-critic.md) §1.

For single-deliverable projects, the `parallel()` reduces to one dispatch but the workflow shape stays consistent. For multi-deliverable projects (e.g. a wearable spanning hardware + firmware + mobile companion + cloud backend + documentation), dispatch one critic per identified shape.

See `spec_refinement.md` § "1. Coverage critic (per identified deliverable shape)" for the full prose on input shape, role boundary, and how uncovered obligations get addressed downstream.

**Phase 1 — Survey (serial, after Phase 0 completes).**
Dispatch `spec-scout`. Pass it `GOALS.md`, the current manifest, and the `uncovered_obligations` list from Phase 0. Read its output: `axes_open`, `new_nodes`, `critique_dimensions`, `concern_dispositions`, `converged`. The scout treats uncovered obligations as a convergence-blocking signal alongside `axes_open` and `critique_dimensions` — it cannot return `converged: true` while uncovered obligations remain. If `converged: true`, exit the loop and proceed to the post-loop citation walk.

**Phase 2 — Fan-out over disjoint units (parallel where your runtime allows).**
For each entry in `axes_open`:

1. Dispatch `spec-candidate-survey` for the axis. The survey self-completes; it returns a candidate list.
2. Dispatch `spec-decision-elaborator` with the axis id and the survey output. The elaborator authors the decision body AND commits it via `mcp__locutus__spec_propose_decision` itself; it returns only the committed id. Do not commit on the elaborator's behalf.

For each entry in `new_nodes` (including obligation-driven entries the scout synthesized from uncovered obligations):

- If kind = `feature`: dispatch `spec-feature-elaborator`. The elaborator authors AND commits via `mcp__locutus__spec_propose_feature`; returns the committed id.
- If kind = `strategy`: dispatch `spec-strategy-elaborator`. Same self-commit pattern via `mcp__locutus__spec_propose_strategy`.

For each entry in `critique_dimensions`: dispatch `spec-critic-elaborator`. Critics' concerns become inputs to the next scout; you do not act on them directly here.

Each axis and each new-node entry is an independent unit — dispatch them in parallel. Never let two parallel branches write the same node; when two axes or new-node entries would write to the same id, serialize those two and leave the rest parallel.

**Phase 3 — Reconcile (barrier — join all Phase 2 subagents first).**
Dispatch `spec-reconciler` once with the uncovered-obligation list from Phase 0 included as a special-class finding. It walks the graph for cross-decision integrity issues — including obligations that lack feature coverage as a class of integrity gap — and applies revisions via `mcp__locutus__spec_revise_decision` itself. The reconciler returns the list of revised decision ids in its summary so Phase 4 can cascade, and surfaces which uncovered obligations remain unaddressed so the cascade phase can act on them.

**Phase 4 — Cascade revisions (serial across shared nodes).**
For each decision id the reconciler revised (and for any decision that this iteration's `spec-decision-elaborator` calls produced a meaningful body change): find features and strategies whose `decisions[]` array references that decision. Call `mcp__locutus__spec_list_manifest` once, then `mcp__locutus__spec_get` on the candidate features/strategies in one batched call to read their bodies. Dispatch `spec-feature-elaborator` or `spec-strategy-elaborator` in revise mode for each affected node — the elaborator's input includes the revised decision context plus the existing feature/strategy body. The elaborator self-commits via `mcp__locutus__spec_revise_feature` or `mcp__locutus__spec_revise_strategy`. Additionally, for each uncovered obligation the reconciler surfaced (from Phase 0's critic output), dispatch `spec-feature-elaborator` in revise mode to address it: either extend an existing feature's body via `mcp__locutus__spec_revise_feature` (with a revision note naming the obligation) or propose a new feature via `mcp__locutus__spec_propose_feature`. When no decisions were revised and no uncovered obligations were surfaced this iteration, Phase 4 is a no-op; skip it.

**Phase 5 — Confirm landings.**
Call `mcp__locutus__spec_list_manifest` once. The manifest is the source of truth for what landed this iteration; keep the response in your working context — the citation walk uses it to enumerate the nodes touched.

After Phase 5, check the iteration count. If the loop has run {{max_iterations}} times, exit the loop regardless of the scout's verdict and proceed to the post-loop citation walk.

## Convergence by construction

Commit, don't defer. If an axis appears in `axes_open` and the candidate-survey and elaborator produced a defensible answer, commit it. If a concern recurs across iterations with no new evidence, treat it as "won't fix" and have the next scout grade it accordingly. Recurring concerns without new evidence are a smell that the critic dimension is mis-scoped, not that the decision is wrong.

## Post-loop — Citation walk (runs once, after the loop)

Walk the `.advances` / `.respects` citation arrays on every node touched by the workflow run. Walk it inline — no subagent dispatch.

1. **Enumerate candidate nodes.** A node needs re-judgement when either:
   - Its body was touched during the workflow (by any elaborator or reconciler commit), OR
   - Its existing `.advances` or `.respects` arrays reference a `goal-*` / `agoal-*` id that changed in the goal-layer sync preamble.
   Skip every other node.
2. **Fetch candidate bodies.** One batched `mcp__locutus__spec_get` with every candidate id, plus the full set of current `goal-*` and `agoal-*` ids and bodies (re-use the manifest's arrays).
3. **Judge each candidate against the final goal layer.** For each candidate:
   - Identify which current goals the node materially advances. List those ids in `advances`.
   - Identify which current anti-goals the node respects. List those ids in `respects`.
   - When a previously-cited id was deleted in the preamble, drop it from the list.
4. **Commit citation updates.** For each candidate whose `.advances` or `.respects` array changed, call the matching `mcp__locutus__spec_revise_decision` / `mcp__locutus__spec_revise_feature` / `mcp__locutus__spec_revise_strategy`. Pass every existing body field verbatim and set the citation arrays to the final values — the tool replaces both citation slices wholesale.

The citation walk is a single pass. When every candidate has been judged and the updates committed, the citation walk is done.

## Reporting

After the citation walk completes, produce a short operator-facing summary:

- **Goal-layer sync** — a one-line note on the preamble outcome (short-circuit fired, or N goal-layer commits).
- **Iterations run** — how many iterations the loop executed before the scout reported convergence or the cap fired.
- **Final iteration** — how many axes were decided, how many features/strategies were committed, what concerns landed.
- **Citation walk** — a one-line count of how many citation updates landed.
- **Features without goal anchors** — when the citation walk leaves any feature whose final `.advances` array is empty, list those feature ids under this heading. Omit the heading entirely when every feature has at least one entry in `.advances`.

The workflow owns the loop — no trailing `converged:` verdict line is needed because there is no outer harness to read it.

## The manifest is your source of truth

Throughout this workflow, the MCP server's manifest is the authoritative record of what's been committed. When you need to know what landed, call `mcp__locutus__spec_list_manifest`. When you need a body, call `mcp__locutus__spec_get`. The orchestrator's job is dispatch and coordination — leave authoring, research, and writing to the subagents.

Your tool surface is the `Task` tool (to dispatch subagents), the `Bash` tool (to compute the `GOALS.md` hash in the preamble), the `Read` tool (to read `GOALS.md` once), and the `mcp__locutus__spec_*` tools (to query and mutate state). Subagents do their own grounding, file reading, and code inspection.
