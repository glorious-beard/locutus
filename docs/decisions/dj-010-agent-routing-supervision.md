## DJ-010: Agent Routing and Supervision

**Status:** shipped (partially superseded by DJ-119 and DJ-121)

**Superseded in part by [DJ-119](dj-119-agent-client-protocol-replaces.md) (2026-05):** the wire layer between supervisor and coding agent — the per-CLI NDJSON driver model originally described here — has been replaced with an Agent Client Protocol (ACP) client. The transport layer changed; what remains of the original wire layer is gone. Read DJ-119 for the current story on how Locutus talks to coding agents.

**Refined further by [DJ-121](dj-121-adoption.md) (2026-05):** the supervision *grain* described here was per-`PlanStep`: pre-plan each Approach into ordered steps with assertions, validate each step, retry each step with feedback. DJ-121 coarsens that grain to per-Workstream and removes `PlanStep` from the spec model. The retry-and-validate loop and the supervisor's dual function (validation + HIL against the spec DAG) are preserved unchanged; the agent now owns step decomposition via a worktree-resident `_locutus/checklist.md`. Workstream execution also becomes sequential-by-default (parallel is opt-in) per DJ-121's correctness-over-throughput stance. Read DJ-121 for the current planning-and-execution shape.

**Decision:** Locutus maintains a registry of coding agents with their strengths and supervises their output.

**User insight:** "Claude Code has a tendency to claim premature victory with stubbed out code and TODOs. Other agents invent requirements or implement dead code."

**Supervision loop:**
1. Generate acceptance tests first (test-first discipline)
2. Delegate to best-matched agent
3. Run tests
4. Validate: no stubs, no dead code, no invented requirements
5. If failing, retry with guidance; if stuck, escalate
6. Result must WORK — does exactly what was intended

**Agent routing:** Registry maps agents to strengths (languages/frameworks). Route plan steps to best available agent. Registry is itself a strategy — revisitable.
