## DJ-047: Full Build Order Rewrite — 8 Tiers

**Status:** shipped

**Decision:** Rewrote the entire build order after a comprehensive gap analysis identified ~20 missing pieces across all tiers. Expanded from 6 tiers to 8.

**Key changes:**
- Tier 1: Added Bug type, plan types (MasterPlan, Workstream, PlanStep, Assertion), GOALS.md concept
- Tier 2: Added `triage`, `import` commands. Init now creates GOALS.md, council agents, workflow.yaml, AGENTS.md symlink
- Tier 3: Graph now includes Feature → Decision edges and supports diff on features
- Tier 4: Split into "LLM + Council Infrastructure" — council agent loader, workflow DAG loader/executor, historian. No longer includes planning or brownfield.
- Tier 5: NEW — "Planning Pipeline (Greenfield)" — council orchestration, specialist agents, spec-derived artifacts, GOALS.md evaluation
- Tier 6: NEW — "Brownfield Analysis" — its own tier with 7 collectors, heuristic/LLM inference, entity extraction, gap analysis, remediation, `locutus analyze` command
- Tier 7: Expanded dispatch — added AgentDriver implementations, git worktree management, concurrency scheduler, PR creation/review
- Tier 8: MCP server (was Tier 6) — added triage/import/analyze as MCP tools
- Package layout updated with ~15 new files across spec, agent, dispatch packages
