# Code Adoption (interactive — Codex / Gemini)

This overlay applies to interactive Codex and Gemini sessions for the `code_adoption` activity. It wraps the default single-iteration playbook with the loop-state directive header so the agent self-drives iteration via the DJ-142 `spec_loop_*` MCP tools.

## Loop control

You are running this activity in **interactive mode**. The harness does NOT re-dispatch you between iterations — you drive iteration yourself via the daemon's loop-state tools:

- `mcp__locutus__spec_loop_begin` — call once at the start with `{activity: "code_adoption", target: ""}`. Returns the iteration cap and the current iteration number (starts at 1).
- `mcp__locutus__spec_loop_status` — call to check whether you should continue. Returns `should_continue: bool` (false when at cap or marked converged).
- `mcp__locutus__spec_advance_iteration` — call between iterations to increment the counter and (optionally) record a per-iteration note for the report.

Do NOT emit a `converged:` verdict line for an outer harness — there is no harness in interactive mode; you are the harness. The loop terminates when (a) you've emitted `spec_loop_status` and it returned `should_continue: false`, or (b) you've decided no further work remains and called the natural end of the workflow.

The iteration cap is set by the activity registry per DJ-138; this activity's default is `max_iterations: 10`.

---

(Below this header, the default `code_adoption.md` playbook body applies. Read it now and follow it for each iteration, calling `spec_advance_iteration` between iterations.)
