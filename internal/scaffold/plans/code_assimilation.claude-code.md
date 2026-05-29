# Code assimilation (Claude Code — dynamic workflow)

Run this as a **workflow**: author an orchestration that drives spec inference from the existing codebase to completion for the project named in your Run context. The workflow loops until the inference pass has extracted entities, inferred decisions, and proposed features that name the user-visible capabilities the code implements, or until the iteration cap of {{max_iterations}} is reached. You own the loop; do not emit a `converged:` verdict line for an outer harness — this workflow is the harness.

> **Status:** The detailed playbook lands alongside the `cmd/assimilate.go` rewrite in DJ-135 phase 5 checkpoint 3. Until that lands, this workflow runs a single best-effort inference pass and completes.

## Plan first

Your very first action is to call `TodoWrite` with the entries you intend to execute. Mark each entry `in_progress` when you start it and `completed` when it lands. A reasonable opening plan for this placeholder pass covers: Read scope (preamble), Explore codebase structure, Extract entities, Infer decisions, Propose features, Report. Update as work progresses, not in a batch at the end.

## Scope note

Read the `Target:` or `Scope:` line in your Run context, if present. When no scope is provided, the pass covers the full project source tree.

## Workflow shape (placeholder)

Until the detailed playbook lands, each iteration of this workflow runs one best-effort inference pass:

1. **Read the Run context.** Note any `Target:` or `Scope:` line that limits the pass.
2. **Explore the codebase.** Use the `Read` and `Bash` tools to walk the project structure. Identify the main modules, packages, or service boundaries; note the primary data models and API surfaces.
3. **Extract entities.** For each identified module or boundary, name the core entity it manages and the user-visible behavior it exposes.
4. **Infer decisions.** For each architectural pattern you observe (e.g. the choice of database, the API protocol, the authentication scheme), formulate a decision body that names the axis (the question the pattern answers) and the chosen approach (the pattern the code implements). Keep the axis name as a question the project resolved, not a description of the code.
5. **Propose features.** For each user-visible capability the code implements, draft a feature body with a slug-based id (`feat-<slug>`), a title, a description, and acceptance criteria derived from the observable behavior.
6. **Emit proposed entities.** Produce the inferred decisions and features as a structured markdown report. This pass does not call MCP write tools directly — the operator reviews the inferred entities and decides which to admit via `locutus import` or `locutus refine`.

After step 6, the pass is complete for this iteration.

## Reporting

Produce a short operator-facing summary:

- **Scope** — what the pass covered (full project or a named subset).
- **Entities found** — a count of modules / boundaries identified.
- **Inferred decisions** — a bulleted list of decision axes inferred, each with the chosen approach the code exhibits.
- **Proposed features** — a bulleted list of feature candidates with slug-id and one-line description.
- **Gaps** — code areas where behavior is observable but the architectural intent is unclear, if any.

The workflow owns the loop — no trailing `converged:` verdict line is needed because there is no outer harness to read it.
