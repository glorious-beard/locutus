## DJ-034: Quality Strategies for Best Practice Enforcement

**Status:** shipping

**Decision:** Best practices are modeled as a new strategy kind (`quality`, alongside `foundational` and `derived`). Quality strategies are cross-cutting — applied to ALL workstreams by the supervisor, not just one. They carry machine-verifiable assertions (linters, duplication detectors, grep patterns) that the supervisor enforces regardless of whether the agent "remembered" the instruction.

**Why not rely on skills alone:** Claude Code (and other agents) demonstrably forget or ignore instructions as context grows — even with a 1M token window. Skills loaded into agent context are best-effort guidance. Quality strategies with machine-verifiable assertions are enforcement — the supervisor checks after the agent finishes, and fails the step if violations are found.

**The two-layer model:**
- **Skill (tell):** SKILL.md says "always use the `<Button>` component from our design system, never raw `<button>`". The agent will usually follow this. Best effort.
- **Quality strategy (verify):** Assertion `not_contains` on .tsx files for `<button`. The supervisor catches violations the agent missed. Enforcement.

**Examples:** DRY enforcement (duplication detector), component library usage (grep for raw elements), naming conventions (linter rules), import restrictions (grep for forbidden paths), test coverage thresholds, no console.log in production code, max function length.

**Four-tier assertion model:** Per-step (functional) → per-workstream (domain integration) → quality strategies (cross-cutting best practices) → global (whole project).
