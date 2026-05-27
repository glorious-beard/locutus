## DJ-025: Planning as a Cooperative Council, Not a Single LLM Call

**Status:** shipping

**Decision:** The planning pipeline uses a cooperative council of agents running iterative rounds, not a single LLM call.

**Insight:** The process used to design Locutus itself — proposing, challenging, researching, validating, recording over multiple rounds — IS the planning pipeline. The council replicates this process:
- **Planner** — proposes approach (the HOW)
- **Critic** — challenges: "Is this necessary? Simpler way?"
- **Researcher** — investigates alternatives, fills gaps
- **Stakeholder** — the user's advocate: "Does this solve the right problem? Is the scope proportional to the value?" Distinct from the critic — critic challenges HOW, stakeholder challenges WHAT and WHY.
- **Historian** — records decisions (deterministic, not LLM)
- **Convergence monitor** — detects cycling, forces decisions after 3+ rounds on same concern (deterministic, not LLM)

**Why a council, not a single call:** A single LLM call produces a plan but doesn't challenge it. The critic and stakeholder catch over-engineering, wrong-problem-solving, and scope creep that a planner alone would miss. This mirrors how the user challenged the plan repeatedly ("are we biased toward SaaS?", "is this meaningful?") — those challenges made the plan dramatically better.

**Budget:** Max 5 rounds, 4-5 LLM calls per round (planner + critic + stakeholder + historian narrative, optionally researcher). 16-25 LLM calls total per planning session. Convergence monitor prevents runaway costs.
