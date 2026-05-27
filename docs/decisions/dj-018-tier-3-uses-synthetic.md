## DJ-018: Tier 3 Uses Synthetic Fixtures

**Status:** shipped

**Decision:** Tier 3 (Decision Graph) tests use hand-crafted spec files as test fixtures, not data from the planner.

**Why:** Tier 3 is pure graph algorithms with no LLM dependency. The DAG construction and traversal code operates on typed structs loaded from JSON. Using synthetic fixtures keeps Tier 3 independent of Tier 4 (planner) and testable in isolation. Real data flows through the graph once Tier 4 is complete.
