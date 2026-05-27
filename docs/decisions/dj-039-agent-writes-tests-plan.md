## DJ-039: Agent Writes Tests, Plan Specifies Criteria (Reverses DJ-017)

**Status:** shipped

**Decision:** The coding agent writes both implementation AND tests. The plan specifies acceptance criteria (WHAT to test, pass/fail conditions). The supervisor validates that tests actually cover the criteria via `llm_review` assertion.

**Reverses DJ-017** ("Locutus writes tests, not the agent") because:
- Dictating test code is the same over-prescription problem as over-detailed plans
- The agent knows the codebase — it can augment existing test files, reuse test helpers, choose appropriate fixtures
- "Plan specifies WHAT, agent decides HOW" should apply to tests just as much as implementation

**Risk mitigation:** The original concern (agent writes tests that pass its own broken implementation) is mitigated by the `llm_review` assertion: "Do these tests actually cover the acceptance criteria specified in the plan?" This is an independent LLM review, not the agent reviewing its own work. Combined with coverage thresholds and deterministic checks, this catches self-serving tests without Locutus having to write them.
