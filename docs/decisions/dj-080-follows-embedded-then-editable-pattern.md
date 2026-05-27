## DJ-080: `models.yaml` Follows the Embedded-Then-Editable Pattern

**Status:** shipped

**Decision:** `internal/agent/models.yaml` is `//go:embed`-ed into the binary as the source of truth, but `locutus init` writes a copy to `.borg/models.yaml` so users can edit per-project model preferences. `LoadModelConfig` reads with this precedence: (1) `LOCUTUS_MODELS_CONFIG` env var, (2) `.borg/models.yaml` walked up from cwd, (3) embedded defaults.

**Why scaffold instead of env-var-only:** consistent with DJ-036's "embedded-then-editable pattern" already used for council agents and workflow YAML. A user who wants to flip Anthropic-first for the strong tier should be able to edit a file in the repo, not set an environment variable. `locutus update` (the analogue refresh path for embedded artifacts) is the seam for picking up upstream changes.

**Why precedence puts the project file ABOVE the env var:** it doesn't — env var wins. Rationale: env-var override is the explicit signal ("I want this specific path"), the project file is the implicit default. CI / shared environments / power users get the env var; everyone else gets the project file.
