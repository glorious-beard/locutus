## DJ-027: Hierarchical Plans (Plan of Plans) with Two-Level DAG

**Status:** shipped

**Decision:** Plans are hierarchical. A master plan decomposes into workstreams (sub-plans), each tailored to the agent executing it. Both levels form a DAG — workstreams can depend on other workstreams, steps can depend on other steps within a workstream.

**Why hierarchical:**
- Different agents need different plan granularity (Claude Code = high-level autonomy; weaker agents = detailed steps)
- Parallel execution across domains (backend + frontend simultaneously)
- Scope control — no single plan exceeds an agent's context window
- Matches real engineering: you don't give the frontend team the same plan as the backend team

**Interface contracts are the key enabler:** Shared types, proto definitions, and API shapes are produced by a dedicated workstream (typically first, no dependencies) and consumed by downstream workstreams. This is what enables parallel work — once the contract is defined, backend and frontend can build independently.

**Plan convergence criteria (what makes a good plan):**
- Makes decisions the agent shouldn't make (architecture); leaves decisions the agent should make (implementation details)
- Detail level calibrated to the executing agent's capability
- Every step has testable (not subjective) success criteria
- No workstream exceeds the agent's context window
- Outer DAG maximizes parallelism via interface contracts

**What replaced:** The previous flat `ExecutionPlan` with a single list of steps. Now `MasterPlan` contains `Workstream[]`, each containing its own `PlanStep[]` DAG.
