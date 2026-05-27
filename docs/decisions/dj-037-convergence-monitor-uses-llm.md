## DJ-037: Convergence Monitor Uses LLM, Not Just Deterministic Checks

**Status:** shipping

**Decision:** The convergence monitor is an LLM call using a cheap/fast model (Haiku-class), not purely deterministic code.

**Why LLM:** Deterministic convergence checks ("did the concerns list change?") can't distinguish between:
- Same concern raised three rounds in a row but planner's response evolved each time → progress, not cycling
- Two new concerns raised but they're minor refinements → plan is substantively ready
- Stakeholder approved but with low confidence → worth one more round

An LLM (even a cheap one) can make these nuanced judgments using its own criteria alongside the other agents' feedback. The cost is minimal — Haiku-class models are fast and cheap.

**What changed:** Convergence monitor moves from deterministic code to an LLM agent with its own definition file in `.borg/council/agents/`. Still configurable — user can set the model, adjust the convergence criteria. Round budget updated: 5-6 LLM calls per round (was 4-5).
