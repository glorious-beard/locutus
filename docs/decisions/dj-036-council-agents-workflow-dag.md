## DJ-036: Council Agents and Workflow DAG Are Externalizable Files

**Status:** shipped

**Decision:** Council agent definitions are YAML frontmatter + markdown body files in `.borg/council/agents/`. The council workflow DAG is `.borg/council/workflow.yaml`. Both are written from embedded defaults at `locutus init` and loaded at runtime. Users can customize without recompiling.

**Why externalizable, not code-only:**
- Advanced users can tune the council: change a model, adjust temperature, rewrite a system prompt
- The stakeholder's prompt can be project-specific ("you represent a healthcare compliance officer")
- New council roles can be added without recompiling (e.g., a security reviewer for auth features)
- The workflow DAG can be reordered, steps can be made conditional, parallelism can be adjusted
- Council definitions are versioned in git alongside the spec

**The embedded-then-editable pattern:** `locutus init` writes defaults from `embed.FS`. At runtime, Locutus reads from `.borg/council/`. User edits are picked up automatically. `locutus update` can refresh defaults without overwriting user customizations (only update files the user hasn't modified).

**Genkit Go integration:** Genkit Go doesn't support loading agent definitions from files (DJ-021). Locutus reads the YAML (model, temperature, output schema) and markdown (system prompt) and constructs the Genkit `ai.Generate()` call programmatically. The file format is Locutus's own, not Genkit's.
