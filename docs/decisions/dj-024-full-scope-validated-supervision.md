## DJ-024: Full Scope Validated — Supervision Is Not Incremental

**Status:** shipped

**Context:** Mid-planning sanity check — is the full 18-step plan justified, or could this be a simpler MCP server that just manages the decision graph?

**Initial assessment:** ~60% of the plan (supervision, agent routing, Taskfile generation) seemed replicable with well-crafted skills in existing tools. Only the decision graph + historian + blast radius seemed genuinely novel.

**User pushback:** The shoe project (Atlas) demonstrated that even with 13 specialized agents, mandatory review gates, and a historian — all configured manually in Claude Code — the user still spent significant time:
- Detecting that Claude was solving the wrong problem and manually forcing step-backs
- Detecting code churn (cycling between approaches without converging)
- Catching failure to converge (same broken approach with different parameters)
- Missing silent decisions that became hardcoded and painful to change later

**Revised assessment:** The supervision loop is NOT incremental automation. It **replaces the human as the monitor** — the person who watches the agent, detects churn, forces step-backs, and catches missed decisions. A skill file can't do this because it provides instructions at session start but can't intervene mid-execution based on observed behavior. The full scope is justified:
- Decision graph + historian = long-term knowledge preservation
- Supervision loop = real-time waste prevention (replaces human monitoring)
- Blast radius = prevents cascading impact from decision changes
- Brownfield = recovers silently hardcoded decisions
- Feature layer = ensures the agent solves the right product problem

**Decision:** Keep the full 18-step plan. Every tier earns its place.
