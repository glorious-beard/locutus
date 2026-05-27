## DJ-083: Spec Generation Uses Externalized Agents + Dedicated Workflow YAML (Supersedes DJ-082)

**Status:** shipped

**Decision:** Spec generation runs through `WorkflowExecutor` against six agent definitions in `internal/scaffold/agents/` (`spec_scout.md`, `spec_architect.md`, `architect_critic.md`, `devops_critic.md`, `sre_critic.md`, `cost_critic.md`) and a workflow YAML in `internal/scaffold/workflows/spec_generation.yaml`. `locutus init` writes both into `.borg/agents/` and `.borg/workflows/`; the runtime loads from there on every invocation. Editing those files tunes the council without rebuilding.

**Workflow shape:**

- `survey` — `spec_scout` produces a `ScoutBrief` (domain read, technology options, implicit assumptions, watch-outs).
- `propose` — `spec_architect` produces a `SpecProposal`, with the scout brief folded into its user message via `projectPropose`.
- `critique` — four specialist critics (architect, DevOps, SRE, cost) run in parallel. Each emits `CriticIssues`; `merge_as: critic_issues` flattens each issue into a `Concern` attributed to the critic's role.
- `revise` — `spec_architect` again, conditional on `has_concerns`. Sees the proposal and the critic concerns via `projectRevise`.

**Why the four critics:** the original single-critic design caught dangling references (the most common proposer failure) but missed entire classes of weakness — deployment coherence ("this can't actually run on Vercel"), operational reality ("no on-call model named"), cost runaway ("BigQuery + Datadog + Vercel Pro will blow through the stated budget"). Each specialist has its own rule set; their union is a meaningfully tougher review than any single generalist.

**Why a scout pre-step:** the proposer working from goals + training distribution defaults to its priors. A scout brief that explicitly lists *implicit assumptions* (scale, cost, ops model, deployment posture, availability, compliance) and *technology options with tradeoffs* gives the proposer a concrete frame to react to. The proposer is then mandated to commit to each implicit assumption as a strategy + decision pair — turning unstated assumptions into first-class spec nodes.

**Schema enforcement:** `ScoutBrief`, `SpecProposal`, and `CriticIssues` are registered in `schemas.go` so `BuildGenerateRequest` wires them through Genkit's structured-output path. Each agent's response is JSON-by-construction at the API layer, not parsed out of free-form text.

**Cost envelope:** one full pass = 6 LLM calls (1 scout + 1 architect + 4 critics) when the proposal is clean, 7 when it isn't. Per-agent model tier comes from the agent's frontmatter (architect: strong, scout: balanced, critics: balanced) so the strong-tier cost is bounded to one (or two on revise) calls per invocation. Multi-round critique requires a `convergence` agent and `max_rounds > 1`; the default workflow ships with `max_rounds: 1`.

**PlanningState extensions to support this:** added `ScoutBrief string`, plus `merge_as: scout_brief` and `merge_as: critic_issues` cases in `mergeResults`. `projectPropose` was updated to fold the formatted scout brief into the proposer's user message; `projectChallenge` now also handles the `critique` step ID. These are minimal, additive changes — the existing planner workflow is unaffected.

**Reversal criteria:** DJ-083 stays unless either (a) a single-pass design with a stronger model demonstrably matches the four-critic output (would let us drop ~3 LLM calls per invocation), or (b) the council grows beyond what `WorkflowExecutor` + `PlanningState` can express cleanly (would need a generic state type or a parallel executor). Neither is currently in sight.
