## DJ-079: `refine goals` Generates the Spec Graph from GOALS.md

**Status:** shipped

**Decision:** `locutus refine goals` is the entry point for greenfield spec generation. It reads `GOALS.md`, calls a council-driven `agent.GenerateSpec` (proposer + critic + revise), and persists the resulting features, decisions, strategies, and approaches via the existing assimilation persistence layer. Re-running is incremental: matching IDs update in place; new IDs land as new files. The Goals root node ID is reserved as the literal string `"goals"` (not `"GOALS.md"` as before) so users can address it directly on the CLI.

**Why `refine` rather than a new verb:** `refine` already means "council-driven deliberation on any spec node" (DJ-069). Goals is a node (KindGoals); deliberating on it = deriving its children (features, strategies, decisions, approaches). Adding a `plan` verb would inflate the 8-verb surface. The semantic stretch is small: refine cascades changes through an existing graph, and `refine goals` is the same operation at the top of the tree — generate or update the children to reflect the parent.

**Why not fold into `assimilate`:** `assimilate` is for inferring spec from *code*. Its agents (backend_analyzer, frontend_analyzer, infra_analyzer) are tuned for code analysis. A docs-only or greenfield repo runs `assimilate` and produces thin, mis-shaped output. The two flows have different inputs and different LLM-side prompts; conflating them would dilute both.

**Composability with `import`:** `import <doc>` runs the same internal pipeline (`runSpecGeneration`) post-admission. The shared call site means a user can iterate: edit GOALS.md → `refine goals` to seed, then `import docs/feature-X.md` for each design doc → `import` extends the existing graph rather than re-introducing nodes. `--no-plan` opts out for admission-only.
