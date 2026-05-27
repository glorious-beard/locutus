/goal Drive the target node named in your Run context (the `Target:` line, default `goals` — the root) to convergence by repeatedly invoking the `/locutus-refine` slash command. Each invocation runs one iteration of the cross-runtime playbook (published at `.claude/commands/locutus-refine.md`); the orchestrator focuses that iteration on the same target.

Inspect each run's final line — it carries a plain-text verdict in the form `converged: true` or `converged: false; <reason>`.

Termination condition (either stops this goal cleanly):

1. The most recent run's final line begins with `converged: true`, OR
2. 20 runs have been dispatched against this goal.

Pass the target through to each `/locutus-refine` invocation — when the slash command's run sees `Target: <node-id>` in its context, the scout scopes to that subtree. Without an explicit target, the orchestrator defaults to `goals` (whole-graph refinement).

The MCP server preserves the spec graph across iterations — each run sees the committed state from prior runs and either commits new work or reports convergence. Between runs, the goal evaluator decides whether termination is reached. Do not loop manually; the evaluator owns the iteration cadence. Surface each run's full report verbatim so the evaluator sees both the operator paragraph and the verdict line.

If the iteration cap fires before `converged: true`, the final report should name the axes and concerns still open for the target. The graph remains in its current state; the operator can re-invoke this goal to continue, or revise `GOALS.md` if the open axes reveal scope gaps that need resolving before further dispatch.
