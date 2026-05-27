## DJ-038: On-Demand Specialist Agents for Plan Fleshing-Out

**Status:** settled (deferred 2026-04-25 pending team-facing decision)

**Deferral note:** the specialist-agent layer is most valuable when Locutus is producing PRs for human review (test-architect proves tests cover the criteria; UI-designer / schema-designer flesh out detail before review). Locutus's current posture is local commit-per-workstream (DJ-032 reframed) with one operator. Specialists are overhead in that posture — the agent already writes tests (DJ-039) and the human reviews the merged feature branch directly. Reopen this DJ when/if Locutus pivots team-facing.

**Decision:** Implementation details (executable acceptance tests, UI descriptions, schema designs) are handled by on-demand specialist agents, not the core planner. Specialists are invoked after the core council converges on structure.

**Specialists:** Test architect (Playwright scripts, Go test skeletons), UI designer (component descriptions from feature specs), schema designer (migrations, proto definitions, API contracts). Users can add custom specialists (security reviewer, accessibility auditor, i18n specialist).

**Why not the planner:** The planner proposes architecture ("we need an auth service"). Writing a Playwright script or describing a UI component tree is a different skill. Overloading the planner degrades both its architectural reasoning and its implementation detail quality. Specialists can also use domain-specific models or prompts optimized for their task.

**How they fit:** Core council rounds converge on structure → readiness gate passes → specialist agents flesh out implementation details (1-3 additional LLM calls) → master plan is complete with both architecture and executable detail.
