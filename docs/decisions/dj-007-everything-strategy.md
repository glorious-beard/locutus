## DJ-007: Everything Is a Strategy

**Status:** shipped

**Decision:** Build systems, test runners, linters, formatters, and deployment tools are all strategies — not hardcoded in Locutus.

**Implication:** Locutus never calls `go build` or `go test` directly. It reads the active strategy's `commands` map. Switching from Go to Rust or from `go build` to Bazel is a decision revisit that cascades through strategies.

**Extension — Taskfile.yml:** Generated deterministically from strategies' `commands` maps. Thin facade over real build tools. Avoids stochastic LLM generation for deterministic things like build commands.

**Extension — Strategy prerequisites:** Each strategy declares its prerequisites (tools, versions). `locutus check` is strategy-driven — adding/removing strategies changes what gets checked.
