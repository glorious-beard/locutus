# Feature ingestion (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives feature admission to completion for the content named in your Run context. The workflow runs until the admission reaches a terminal branch (admitted, diff drafted, or blocked), or until the iteration cap of {{max_iterations}} is reached. You own the loop; do not emit a `converged:` verdict line for an outer harness — this workflow is the harness.

## Plan first

Your very first action is to call `TodoWrite` with the entries you intend to execute. Mark each entry `in_progress` when you start it and `completed` when it lands. A reasonable opening plan covers: Goal-layer read (preamble), then per-iteration: Read feature content, Identify domain, Structural conflict test, Branch (admit / draft diff / stop), Scout new axes (if admitted), Report. Update as work progresses, not in a batch at the end.

## Start here

After laying out your plan, call `mcp__locutus__spec_list_manifest` (no arguments). The manifest returns every node in the graph; the `Goals` and `AntiGoals` arrays carry the `goal-*` and `agoal-*` ids you need for structural conflict detection. Read the manifest once here and keep it in context.

## Invariants

- **Spec mutations route exclusively through the `mcp__locutus__spec_*` MCP tools.** Never call `Write` or `Edit` on any file under `.borg/spec/` — those files are the SpecStore's persistence backing, not its source of truth (DJ-134). The daemon owns coherence (in-process `SpecStore` + write-through search index + history events + per-runtime tool policy per DJ-143); direct file writes bypass all of it. When you need to mutate the graph, the right tool is one of `spec_propose_*` / `spec_revise_*` / `spec_delete_*` (or `spec_mark_approach_drifted` for DJ-138 drift marks).
- **`GOALS.md` is read-only for the duration of this run.** Read it once with the `Read` tool when the playbook says to. Never call `Write` or `Edit` on `GOALS.md` — the operator owns its content (DJ-139 RQ1: "humans only edit GOALS.md"). The matcher's `promoted` / `contradicted` moves apply to goal-layer *nodes* via the goal-layer MCP tools (`spec_propose_goal` / `spec_revise_goal` / `spec_delete_goal` and the AntiGoal variants), not to the `GOALS.md` file. If your iteration drafts a unified diff against `GOALS.md` (e.g. `feature_ingestion` Branch B), the diff is *output for operator review*, never a patch the workflow applies itself.

## One-time preamble — Goal-layer read (runs once, before the loop)

Fetch the full goal layer once so it is available for every iteration's structural conflict test.

Issue one batched `mcp__locutus__spec_get` with every `goal-*` and `agoal-*` id from the manifest's `Goals` and `AntiGoals` arrays. The response carries each node's `body` (the LLM-interpretation prose), `source_clause` (the verbatim excerpt from `GOALS.md`), and for `agoal-*` nodes the `ceded_to` and `kept_in` arrays. Keep this payload in context — each iteration's conflict test reads from it without re-fetching.

When the manifest's `Goals` and `AntiGoals` arrays are both empty, note that the goal layer has not yet been populated and proceed to the admission loop; Step 3 finds no conflicts and Step 4 Branch A admits without citations.

## Admission loop (repeat until a terminal branch or {{max_iterations}} reached)

Each iteration runs the following steps in order.

**Step 1 — Read the feature content.**
Read the user-supplied content from your Run context. Identify the feature's domain in one or two short phrases: what surface does it touch, what user-visible behavior does it commit to, what data does it consume or produce.

**Step 2 — Fetch any updated goal layer (if needed).**
Use the goal-layer payload fetched in the preamble. On a re-iteration (e.g. the operator edited GOALS.md between runs and restarted), call `mcp__locutus__spec_list_manifest` again and re-fetch with `mcp__locutus__spec_get` to get the latest state.

**Step 3 — Test the feature structurally against the `agoal-*` nodes.**
For each `agoal-*` node, judge whether the feature's domain overlaps the carve-out the anti-goal establishes. Use both the `body` (the LLM-interpretation prose) and the `kept_in` array (carve-out qualifiers that stay in scope).

Three outcomes:
- **No overlap.** Carry the empty conflict list into Step 4.
- **Overlap, fits under an existing `kept_in` entry.** Record which `agoal-*` ids the feature navigates — these become `respects` entries on admission.
- **Overlap, no existing `kept_in` entry fits.** Carry the conflict list (which `agoal-*` ids, and whether a plausible carve-out extension would resolve each) into Step 4.

For each `goal-*` node, note whether the feature materially advances it — these become `advances` entries on admission.

**Step 4 — Branch on the structural outcome.**

*Branch A — Admit (no conflict, or conflict resolved by existing `kept_in`):*
Author the feature body (or dispatch `spec-feature-elaborator` when the operator's input is sparse and a richer elaboration pass would land a more complete body). Mint the feature id as `feat-<slug>`. Call `mcp__locutus__spec_propose_feature` once with the full body and the goal-layer citation fields (`advances`, `respects`). Proceed to Step 5. Branch A is terminal for this iteration.

*Branch B — Draft a GOALS.md diff (conflict exists, plausible carve-out fits):*
Use the `mcp__locutus__spec_search` tool to locate nodes whose bodies name the conflicting carve-out domain. Load `GOALS.md` once with the `Read` tool. Draft a unified diff proposing the carve-out language — edit only the clause anchoring the conflicting `agoal-*` (find it via the agoal's `source_clause` field) and leave surrounding prose untouched. Emit the diff in a fenced code block tagged `diff`. Emit the report with the diff inline. Branch B is terminal for this iteration.

*Branch C — Stop and report (conflict is irreducible):*
Emit a report naming each conflicting `agoal-*` id, quoting its `source_clause`, and stating that admission requires either revising the feature to fit within scope or a `locutus refine goals` pass with explicit GOALS.md edits. Branch C is terminal for this iteration.

**Step 5 — On admission, surface axes the feature exposes (optional).**
When Branch A admitted the feature, dispatch `spec-scout` once to survey the graph for axes the new feature opens. Skip this step when the feature's `decisions` array referenced only existing settled nodes and no new axes are plausibly exposed.

## Reporting

After any terminal branch, produce a short operator-facing summary:

- **Domain** — the one-or-two-phrase summary of the feature's domain.
- **Goal-layer test** — a summary of which `goal-*` ids the feature advances and which `agoal-*` ids it overlaps (carve-out status for each).
- **Outcome** — names the branch and what landed (the admitted feature id, the drafted diff inline, or the irreducible-conflict explanation).
- **GOALS.md diff** — present only on Branch B. The fenced unified-diff block.

The workflow owns the loop — no trailing `converged:` verdict line is needed because there is no outer harness to read it.

## The manifest is your source of truth

Throughout this workflow, the MCP server's manifest is the authoritative record of what's been committed. When you need to know what landed, call `mcp__locutus__spec_list_manifest`. When you need a body, call `mcp__locutus__spec_get`. Use `mcp__locutus__spec_search` for topic-scoped lookups when you need to find related existing nodes by domain. The orchestrator's job is dispatch and coordination — leave authoring to the subagents.

Your tool surface is the `Task` tool (to dispatch subagents), the `Read` tool (to load `GOALS.md` when drafting a diff), and the MCP spec tools (to query and mutate state). Subagents do their own grounding, file reading, and code inspection.
