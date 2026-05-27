## DJ-052: Agent Definitions Are the Prompt Source of Truth

**Status:** shipped

**Decision:** Each agent `.md` file contains the full prompt: identity, context, task, output format, quality criteria, and anti-patterns. Go code in `projection.go`, `convergence.go`, and `supervisor.go` only injects dynamic context (state snapshots, event data) as user messages.

**Why:** Scattered prompt engineering across Go code is hard to iterate on, review, and version. Consolidating prompts in `.md` files makes them: editable by non-developers, diffable in PRs, isolatable for A/B testing, and loadable at runtime (users can customize per-project after `locutus init`).

**Alternatives considered:** Keep prompt fragments in Go code for compile-time safety. Rejected because prompt engineering is iterative content authoring, not programming — locking it in Go tightens feedback loops unnecessarily.
