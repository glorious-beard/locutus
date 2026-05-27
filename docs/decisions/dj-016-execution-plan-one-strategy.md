## DJ-016: Execution Plan — One Strategy Per Step, Agent Self-Reports Files

**Status:** superseded by DJ-027

**Decision:** Each plan step is scoped to one strategy but can touch multiple files. The agent self-reports files modified; `git diff --name-only` is the source of truth.

**Alternatives considered:**
- a) Explicitly specify one or more files per step — too rigid; agent may need to create helpers or modify unexpected files
- b) Discover files after modification, constrained to one strategy — viable but doesn't capture agent's own understanding
- c) Agent self-reports files at end of coding cycle, constrained to one strategy — **chosen**

**Why Option C:** The real constraint is one strategy per step (preserves traceability). Within that boundary, the agent should have freedom to touch whatever files are needed. `git diff --name-only` verifies the self-report. All files in the diff map to the step's governing strategy in `traces.json`. This handles cases where agents create helper files, update go.mod, or modify files not anticipated in the plan.

**What changed:** The `PlanStep` struct no longer has `FilePath` and `Action` for a single file. Instead it has `ExpectedFiles` (guidance, not enforcement) and the supervisor uses git diff for the actual file list.
