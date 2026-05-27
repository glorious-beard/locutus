## DJ-011: Historian

**Status:** shipped

**Decision:** Every decision/strategy change recorded as structured JSON with rationale and rejected alternatives.

**User insight:** "Git history isn't sufficient. We should have a historian agent that captures the motivations behind changes and alternatives considered."

**Value during revisit:** When revisiting a strategy, the historian surfaces previously rejected alternatives. "We considered Bazel in March but ruled it out because of team experience. Has that changed?"

**Format:** Structured JSON events (machine-queryable) + derived markdown summary (human-readable). Both in `.borg/history/`.
