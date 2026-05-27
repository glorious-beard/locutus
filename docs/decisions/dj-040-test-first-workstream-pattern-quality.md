## DJ-040: Test-First Workstream Pattern as a Quality Strategy

**Status:** settled (deferred 2026-04-25 pending team-facing decision)

**Deferral note:** the test-first **structural gate** (a hard plan-time enforcement that every workstream's first step has an assertion describing a failing test) is most valuable when Locutus is producing PRs for human review — it makes review faster and catches "agent skipped tests" failure modes before they ship. In Locutus's current local posture (DJ-032 reframed), test-first is **practiced** (DJ-039: agent writes tests, supervisor's `llm_review` validates coverage) but not **structurally gated** at plan time. The user reviews the merged feature branch directly and can re-run with stricter assertions if a workstream skipped tests. Reopen this DJ when/if Locutus pivots team-facing — the hard gate then earns its keep.

**Decision:** Every workstream must start with defining acceptance tests and conclude with all tests passing. This is a foundational quality strategy enforced structurally by the supervisor — a hard gate, not optional guidance.

**The pattern:** Plan acceptance criteria → first step: agent defines/writes tests → middle steps: agent implements → final step: all tests pass. The supervisor won't mark a workstream as complete until the test gate passes.

**Why a quality strategy, not just an instruction:** Instructions get forgotten. A quality strategy is enforced by the supervisor on every workstream regardless of what the agent does. The test-first pattern is too important to be advisory — it's the primary mechanism for ensuring the result actually works.
