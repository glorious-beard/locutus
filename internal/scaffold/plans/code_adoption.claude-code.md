# Code adoption (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives approach reconciliation to completion for the project named in your Run context. The workflow loops until every approach in the spec graph has been classified and drift has been surfaced, or until the iteration cap of {{max_iterations}} is reached. You own the loop; do not emit a `converged:` verdict line for an outer harness — this workflow is the harness.

> **Status:** The detailed playbook lands alongside the `cmd/adopt.go` rewrite in DJ-135 phase 5 checkpoint 3. Until that lands, this workflow runs a single best-effort reconcile pass and completes.

## Plan first

Your very first action is to call `TodoWrite` with the entries you intend to execute. Mark each entry `in_progress` when you start it and `completed` when it lands. A reasonable opening plan for this placeholder pass covers: Read scope (preamble), Classify approaches against codebase, Surface drift, Report. Update as work progresses, not in a batch at the end.

## Scope note

Read the `Scope:` line in your Run context, if present. A scope note limits the reconcile pass to a subset of the approach graph (e.g. a single module or service boundary). When no scope is provided, the pass covers the full set of approaches in the spec graph.

## Workflow shape (placeholder)

Until the detailed playbook lands, each iteration of this workflow runs one best-effort reconcile pass:

1. **Read the Run context.** Note any `Target:` or `Scope:` line that limits the pass.
2. **Inspect the codebase.** Use the `Read` and `Bash` tools to examine source files in the scope. For each approach the spec graph names, determine whether the code reflects it as written.
3. **Classify each approach.** Four possible states:
   - `live` — the code implements the approach exactly as specified.
   - `drifted` — the code diverges from the approach in ways that are consistent with the spec's intent but differ in detail.
   - `out_of_spec` — the code contradicts the approach.
   - `unplanned` — code exists in the area but no approach covers it.
4. **Surface the drift.** Emit a markdown table of approach ids and their classified state, with a short note for any non-`live` classification.
5. **Propose remediation.** For each `drifted` or `out_of_spec` approach, describe the change required to bring code and spec into alignment.

After step 5, the pass is complete for this iteration.

## Reporting

Produce a short operator-facing summary:

- **Scope** — what the pass covered (full graph or a named subset).
- **Classification table** — a markdown table of approach id, state, and note.
- **Remediation items** — a bulleted list of changes needed for non-live approaches.
- **Unplanned areas** — code areas that have no covering approach, if any were found.

The workflow owns the loop — no trailing `converged:` verdict line is needed because there is no outer harness to read it.
