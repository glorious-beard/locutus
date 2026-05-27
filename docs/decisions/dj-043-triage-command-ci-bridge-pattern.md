## DJ-043: Triage Command + CI-Bridge Pattern

**Status:** shipped

**Decision:** Add `locutus triage --input <file> --json` command that evaluates an issue against GOALS.md and outputs a structured JSON verdict (accepted/rejected/duplicate). A thin CI wrapper (GitHub Action) handles the external system interaction on both sides.

**The pattern:** CI fetches issue → pipes to `locutus triage` → reads JSON verdict → acts on external system (comment, label, close). Locutus never calls external APIs, never needs API keys.

**Why this approach:** Locutus stays local-only (DJ-042) but the triage capability is still usable in automated workflows. The CI wrapper is ~20 lines of YAML. Different platforms write their own wrappers. Locutus's structured JSON output is the universal interface — same pattern as MCP (Locutus produces structured output, something else presents/acts on it).
