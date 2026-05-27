## DJ-051: Flat Scaffold Layout

**Status:** shipped

**Decision:** Scaffold structure is flat: `internal/scaffold/agents/` holds all 15 agent definitions; `internal/scaffold/workflows/` holds `planning.yaml` and `assimilation.yaml`. On disk after `locutus init`: `.borg/agents/` and `.borg/workflows/`.

**Why:** The earlier nested hierarchy (`council/agents/`, `council/brownfield/agents/`, `council/supervision/agents/`) was organizational overhead with no functional benefit. Agents are loaded the same way regardless of category; workflows reference agents by ID. The flat layout is simpler, cleaner, and easier to navigate.

**Alternatives considered:** Keep nesting by category (planning/assimilation/supervision). Rejected because agent IDs are unique across categories and the loader doesn't care which subdirectory they came from.
