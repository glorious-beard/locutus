# Locutus Implementation Plan

## Context

Locutus is a Go CLI and MCP server that acts as an **autonomous project manager** for spec-driven software. It maintains a persistent spec graph (Feature → Decision → Strategy → Code), produces execution plans, delegates coding to external agents (Claude Code, Codex, etc.), and supervises their output to ensure correctness. The spec is the source of truth; code is a derived artifact.

The repo is brand new — no Go code exists yet. This plan covers the full end-to-end implementation: core infrastructure, CLI commands, decision graph, LLM-powered planning, agent dispatch/supervision, and MCP server.

## Package Layout

```
locutus/
  go.mod
  main.go                        # entry point, kong.Parse + Run
  cmd/
    cli.go                       # root CLI struct with --json and --verbose flags
    check.go                     # CheckCmd
    init.go                      # InitCmd
    status.go                    # StatusCmd
    version.go                   # VersionCmd
    update.go                    # UpdateCmd (self-update)
    diff.go                      # DiffCmd (blast radius preview)
    regen.go                     # RegenCmd (regenerate stale modules)
    revisit.go                   # RevisitCmd (update decision/strategy)
    triage.go                    # TriageCmd (evaluate issue against GOALS.md)
    import.go                    # ImportCmd (create feature/bug from issue)
    analyze.go                   # AnalyzeCmd (brownfield codebase analysis)
    mcp.go                       # McpCmd (MCP server, stdio default)
  internal/
    spec/
      types.go                   # Decision, Strategy, Entity, Feature, Manifest
      bug.go                     # Bug type
      plan.go                    # MasterPlan, Workstream, PlanStep, Assertion, InterfaceContract
      enums.go                   # all enums (DecisionStatus, StrategyKind, PlanAction, etc.)
      traces.go                  # TraceabilityIndex
      response.go                # MCPResponse, FileChange
    frontmatter/
      frontmatter.go             # parse/render markdown with YAML frontmatter
    specio/
      specio.go                  # FS interface, OSFS, SpecStore
      memfs.go                   # in-memory FS for tests
      pair.go                    # load/save .md + .json pairs atomically
      walk.go                    # walk spec dirs, discover pairs, detect orphans
    check/
      check.go                   # strategy-driven prerequisite validation
    scaffold/
      scaffold.go                # bare project scaffold (.borg/, council, skills, GOALS.md, CLAUDE.md)
      skills/                    # embedded SKILL.md files (via go:embed)
      council/                   # embedded council agent defs + workflow.yaml (via go:embed)
    render/
      render.go                  # pterm-based CLI rendering (stdout); slog for debug (stderr)
    graph/
      graph.go                   # DAG: Feature/Bug → Decision → Strategy → Files
      walk.go                    # forward walk, blast radius calculation
    agent/
      agent.go                   # Genkit AI init, provider registration
      council.go                 # council agent definition loader (YAML+markdown → Genkit config)
      workflow.go                # workflow DAG loader + round executor
      planner.go                 # greenfield planning pipeline (council orchestration)
      brownfield.go              # codebase scanning orchestrator
      collectors.go              # 7 scanning collectors (file inventory, lang, config, etc.)
      inference.go               # heuristic + LLM decision/strategy inference
      entities.go                # EntityExtractor interface + framework implementations
      revisit.go                 # decision/strategy update logic, conflict detection
      taskfile.go                # Taskfile.yml generation (deterministic)
      agentsmd.go                # CLAUDE.md + AGENTS.md symlink generation (deterministic)
    history/
      history.go                 # structured JSON events + LLM narrative generation
    dispatch/
      registry.go                # agent registry: capabilities, routing logic
      drivers/                   # AgentDriver interface + implementations
        driver.go                # AgentDriver interface
        claude.go                # ClaudeCodeDriver (wraps claude -p)
        codex.go                 # CodexDriver (wraps codex exec)
      runner.go                  # agent process forking, output collection
      scheduler.go               # concurrency scheduler (job queue, resource limits)
      worktree.go                # git worktree create/merge/cleanup
      supervisor.go              # supervision loop: validate, retry, escalate
      pr.go                      # PR creation, review, auto-merge to feature branch
```

## Libraries

- **CLI**: `github.com/alecthomas/kong`
- **YAML**: `gopkg.in/yaml.v3`
- **JSON**: `encoding/json` (stdlib)
- **Testing**: `github.com/stretchr/testify/assert`
- **Frontmatter**: Custom (~40 lines — split on `---` delimiters, yaml.Unmarshal)
- **Atomic writes**: Custom (write temp file + os.Rename)
- **Console output**: `github.com/pterm/pterm` — Go equivalent of Python's "rich" (tables, spinners, progress bars, tree views, colored text)
- **Self-update**: `github.com/creativeprojects/go-selfupdate` — downloads latest release from GitHub, handles multi-arch, supports rollback
- **MCP server**: `github.com/modelcontextprotocol/go-sdk` — official MCP SDK for Go, handles protocol/transports/tool registration
- **Logging**: `log/slog` (stdlib) — structured logging, zero external deps
- **Tracing/Metrics**: `go.opentelemetry.io/otel` — vendor-neutral, added incrementally as needed (not wired in Phase 1 beyond basic slog)
- **LLM framework**: `github.com/firebase/genkit/go` (Genkit Go 1.0) — unified multi-provider interface, tool calling, structured output. Model selected by config string at runtime (e.g., `anthropic/claude-sonnet-4-20250514`). Swap providers without recompiling.

## Key Design Decisions

1. **`--json` and `--verbose` flags on root CLI struct.** Every command produces a structured result. `--json` prints it as JSON; otherwise it goes through `internal/render/` (pterm, stdout) for human display. `--verbose` sets slog level to Debug (stderr), so debug messages don't interfere with structured output.

2. **`FS` interface for testability.** A narrow interface (`ReadFile`, `WriteFile`, `MkdirAll`, `Remove` + `fs.FS` for reading) lets all file-dependent tests use an in-memory implementation.

3. **Frontmatter = human subset, JSON = full data.** The `.md` YAML frontmatter contains only `id`, `title`, `status` (enough for human scanning). The `.json` sidecar holds the complete typed struct. The markdown body is prose.

4. **Write JSON first, then MD.** JSON is the machine source of truth. If a crash interrupts between the two writes, `status` detects the orphan.

5. **Genkit Go for LLM access.**
   - Uses `github.com/firebase/genkit/go` with Anthropic plugin (Claude default, others swappable).
   - Model selected by string at runtime from manifest config (e.g., `anthropic/claude-sonnet-4-20250514`). Changing providers = changing a config value, no rebuild.
   - Genkit provides: unified `ai.Generate()` API, tool calling with Go function registration, structured output with schema validation, streaming support.
   - Locutus builds a thin tool-use loop on top of Genkit's Generate for multi-turn decision conversations.
   - Genkit integration starts at Step 12; full planning pipeline at Step 14.

6. **`MCPResponse` as the universal structured return type.** All commands wrap their results this way, so the MCP server (Step 17) is just wiring.

7. **Observability from day one.** Use `slog` for structured logging throughout. OpenTelemetry tracing/metrics wired incrementally as needed.

8. **Self-update command.** `locutus update` uses `go-selfupdate` to pull the latest release from GitHub. Added in Phase 1 so it's available from the first binary distribution.

9. **Stdio-first MCP transport.** VS Code only supports stdio for MCP servers; Claude Code supports stdio too. The binary runs as `locutus mcp` for stdio MCP mode (spawned by the client). Optional `locutus mcp --http :8080` for remote/multi-client scenarios later. CLI mode (`locutus check`, `locutus init`, etc.) remains separate — same structured output, different presentation layer.

10. **Everything is a strategy — no hardcoded toolchain.** Build systems, test runners, linters, formatters, deployment tools are all strategies, not baked into Locutus. The "build" strategy might select `go build` today and `bazel` tomorrow. The "testing" strategy might select `go test` or `pytest` depending on the backend-language decision. Locutus never calls `go build` directly — it reads the active strategy's commands. This means switching from Go+React to C+++CMake or Python+Django is just a set of decision revisits that cascade through strategies.

    **No archetype selection at init.** `locutus init` creates a bare spec structure with no stack assumptions. The "archetype" emerges organically from the user's first prompt (greenfield) or codebase analysis (brownfield). There is no archetype enum or selection step.

11. **Taskfile.yml as derived command reference.** Generated by Locutus from active strategies' `commands` maps. Thin facade that delegates to the real build/test tools. Regenerated whenever strategies change. `task build`, `task test`, etc.

12. **Skills over templates.** No template engine. Strategies activate SKILL.md files that guide the LLM to generate correct code. The "archetype" is the emergent combination of active skills — e.g., "Go backend + Connect RPC" activates `go-project.skill.md` + `connect-rpc.skill.md` + `buf-protobuf.skill.md`. Skills live in `.agents/skills/` and are strategy-driven: when a strategy becomes active, its associated skills get loaded into agent context.

13. **Advisory delegation, not enforcement.** External agents (Claude Code, Copilot) can't be forced to use Locutus tools. AGENTS.md and skills provide strong guidance but are advisory. Locutus should be resilient to agents that bypass it — the brownfield path can recover spec from direct code changes. No drift detection or gatekeeper infrastructure.

14. **Unified `internal/agent/` package.** Genkit LLM setup, generation pipeline, brownfield analysis, revisit logic, and AGENTS.md generation all live in one package. Genkit does the LLM heavy lifting; the agent package is where the real orchestration logic lives.

15. **Autonomous planner + supervised delegator.** Locutus makes all decisions autonomously (status: `assumed`, with confidence score) and produces fully resolved execution plans. No `input_needed` during planning. The execution plan specifies: files to create/modify, governing strategy, patterns, skills, acceptance criteria. Locutus updates `traces.json` based on planned mappings. Spec-derived artifacts (Taskfile.yml, AGENTS.md, CLAUDE.md, proto definitions) are generated directly — deterministic, no delegation.

16. **Agent routing.** Locutus maintains a registry of coding agents with their strengths (languages, frameworks, domains). When dispatching plan steps, each step is routed to the best available agent based on the strategy's domain. The agent registry is itself a strategy — revisitable and extensible. Bootstrapped with sensible defaults.

17. **Agent supervision loop.** Locutus doesn't just delegate — it supervises:
    1. Generate acceptance tests from plan's success criteria (test-first)
    2. Delegate implementation to the routed agent
    3. Run tests via the testing strategy
    4. If pass → validate (no TODOs, no stubs, no dead code, no invented requirements) → commit
    5. If fail → provide test output + guidance to agent, request fix
    6. If still failing after N rounds → analyze: is the plan wrong? Is the agent stuck? Is this a decision-level issue? Adjust accordingly.
    7. Final: the result must WORK — does exactly what was intended, nothing more.

18. **Historian for decision archaeology.** Every decision/strategy change is recorded as a structured JSON event in `.borg/history/`. Events capture: what changed (ID, old/new values), why (user-stated rationale), alternatives considered and why they were ruled out, and timestamp. A human-readable markdown summary is derived from the JSON events. The historian is consulted during `revisit` — previously rejected alternatives may now be relevant ("We ruled out Bazel in March because of team experience; has that changed?").

19. **GOALS.md as the project root + issue-driven intake.** The full hierarchy is: GOALS.md → Feature/Bug → Decision → Strategy → Source Files. GOALS.md is a human-authored document at the project root that defines: what the project is, what's in scope, what's out of scope, and how to evaluate whether a proposed feature or bug fix belongs. It gives the stakeholder agent an **objective reference** instead of relying on LLM judgment alone.

    **Local-only with import and triage.** Locutus is local-only — it never calls external APIs. Input format is **markdown with YAML frontmatter** (not JSON) — rich enough for inline images, Figma links, discussion threads, and attachments. Two commands bridge to external systems:
    - `locutus triage --input <file.md> --json` — reads a markdown issue, evaluates against GOALS.md, writes a structured JSON verdict to stdout (accepted/rejected/duplicate, with reason, suggested labels, suggested action). Does NOT create a local artifact — dry-run for CI.
    - `locutus import --input <file.md>` — reads a markdown issue, creates a local feature or bug in `.borg/spec/`. The markdown body becomes the `.md` file; Locutus generates the `.json` sidecar from the frontmatter + council enrichment. If triage was not run first, import runs triage internally and rejects out-of-scope items.
    - **CI-bridge pattern:** A thin CI wrapper (e.g., GitHub Action) exports the issue as markdown+frontmatter (provider-specific exporter), pipes to `locutus triage`, reads the verdict, acts on the external system. Locutus never needs API keys. Different platforms write their own exporters and wrappers.
    - All enrichment, planning, and execution happen locally. Deep integration with GitHub/Jira/Linear is explicitly deferred.

    **Features are live capabilities, not tasks.** They have status: `proposed`, `active`, `removed` — never "resolved." A feature like "user authentication" is permanent.

    **Bugs are defects tied to features** (in `.borg/spec/bugs/`): id, title, severity (auto-triaged against GOALS.md), status (`reported` → `triaged` → `fixing` → `fixed`), related feature, reproduction steps, root cause (filled after analysis), fix plan, `source` link. When code changes pass tests, Locutus marks the bug locally as `fixed`.

    **Zero-issue as a quality strategy:** Locutus actively works toward resolving all in-scope bugs. Duplicate detection against existing spec.

20. **Features as the product-level layer below GOALS.md.** Features are the product spec ("user authentication"). Decisions are implementation choices ("JWT vs sessions"). Strategies are execution approaches ("use golang-jwt/v5"). Decisions can be feature-driven or standalone (foundational/project-wide). Same for strategies. Features have acceptance criteria that flow down into plan step assertions. Features are evaluated against GOALS.md for scope before entering the council.

    **Feature authoring is human-initiated, council-enriched.** The human writes the feature (any level of detail — from a one-liner to a full spec with Figma mockups and screenshots). Can also originate from a GitHub issue. The council enriches it: planner adds acceptance criteria and edge cases, stakeholder validates it represents user intent AND aligns with GOALS.md, critic checks for gaps, historian records origin. The human reviews the enriched spec before it drives decisions. The `.md` body is for humans (prose, mockups, links). The `.json` sidecar is for Locutus (structured acceptance criteria, entity refs, decision links).

21. **Single test suite, scoped runs via traceability.** Generated tests join the project's test suite (no shadow infrastructure). Test files are governed by strategies just like source files (both appear in `traces.json` and strategy `governs` fields). When code changes, Locutus uses the DAG to identify affected strategies → collects governed test files → invokes the testing strategy's scoped command (e.g., `go test ./pkg/auth/... ./pkg/cache/...`). Strategy commands support parameterization: `"test_scoped": "go test ${TARGETS}"`. `task test` always runs the full suite; scoped runs are Locutus-internal during generation. Full suite runs as a final gate before commit.

## Detailed Designs

### Execution Plan Format

Plans are **hierarchical** — a master plan decomposes into workstreams, each tailored to the agent executing it. Both levels form a DAG. Key types live in `internal/spec/plan.go`.

**Two-level DAG:**
- **Outer DAG:** workstreams and their dependencies (which workstreams can run in parallel)
- **Inner DAG:** steps within each workstream (which steps can run in parallel within that workstream)

**Top-level `MasterPlan` struct:**
- `ID`, `Version`, `CreatedAt`, `ProjectRoot` — plan identity and location
- `Prompt` — the original user input that triggered this plan (traceability)
- `TriggerKind` — enum: `init`, `revisit`, `regen`, `brownfield`
- `Features []FeatureRef` — snapshot of features this plan addresses
- `Decisions []DecisionRef` — snapshot of all relevant decisions at plan-creation time
- `Strategies []StrategyRef` — snapshot of all relevant strategies at plan-creation time
- `InterfaceContracts []InterfaceContract` — shared types, API shapes, proto definitions that enable parallel workstreams
- `Workstreams []Workstream` — the DAG of workstreams (each is a sub-plan)
- `GlobalAssertions` — plan-wide checks run after ALL workstreams complete
- `SpecDerivedArtifacts []string` — files Locutus generates directly (Taskfile, CLAUDE.md, etc.)
- `Summary` — human-readable description for logging and historian

**`InterfaceContract` struct:**
- `ID` — identifier (e.g., `auth-api-contract`)
- `Description` — what this contract defines
- `Artifacts []string` — files that define the contract (proto files, type definitions, OpenAPI specs)
- `ProducedBy` — workstream ID that creates the contract (often runs first)
- `ConsumedBy []string` — workstream IDs that depend on this contract

**`Workstream` struct (a sub-plan for one agent):**
- `ID` — identifier (e.g., `backend-auth`, `frontend-auth`, `db-migrations`)
- `StrategyDomain` — the strategy domain this workstream covers
- `AgentID` — which agent executes this workstream (from registry)
- `DetailLevel` — enum: `high` (strong agent, more autonomy), `medium`, `detailed` (weaker agent, more specificity). Determines how prescriptive the steps are.
- `DependsOn []WorkstreamDependency` — other workstreams that must complete first, with reason
- `Steps []PlanStep` — the inner DAG of steps
- `Assertions` — workstream-level checks run after all steps in this workstream complete

**Per-step `PlanStep` struct:**
- `ID`, `Order` — identity and execution sequence (same order = parallelizable within workstream)
- `StrategyID` — governing strategy (one strategy per step)
- `ExpectedFiles []string` — optional guidance; actual files discovered via `git diff --name-only`
- `DecisionIDs []string` — decisions that drove this step
- `Description` — instruction for the coding agent (detail level matches workstream's `DetailLevel`)
- `Skills []SkillRef` — resolved SKILL.md with **inlined content**
- `DependsOn []StepDependency` — steps that must complete first, with reason
- `Assertions []Assertion` — machine-verifiable acceptance criteria
- `Context map[string]string` — related file contents the agent needs

**`Assertion` struct — deterministic and LLM-based checks:**
- `Kind` — enum: `test_pass`, `file_exists`, `file_not_exists`, `contains`, `not_contains`, `command_exit_zero`, `compiles`, `lint_clean`, `llm_review`
- `Target` — kind-specific: file path, test pattern, command string, package path; for `llm_review`, the file(s) or diff to review
- `Pattern` — for contains/not_contains (literal or regex); for `llm_review`, unused
- `Prompt` — for `llm_review` only: the review question (e.g., "Does this code follow the separation of concerns described in the architecture strategy?")
- `Message` — human-readable description shown in failure output
- Deterministic assertions (`test_pass`, `contains`, etc.) run first — fast and cheap. `llm_review` assertions run last — slower and costlier, but catch semantic issues that heuristics miss (architectural consistency, design pattern adherence, naming quality, visual consistency with existing UI).

**Plan convergence criteria (what makes a good plan):**
- Each workstream is scoped to a single strategy domain
- Interface contracts are fully defined before dependent workstreams start
- Step detail level is calibrated to the executing agent's capability
- Every step has testable success criteria (not "make sure it looks right")
- The plan makes architectural decisions (agent shouldn't); leaves implementation details to the agent (agent should)
- No workstream exceeds the agent's context window when fully expanded
- The outer DAG maximizes parallelism — workstreams that share only interface contracts can run concurrently

**Design decisions:**
- **Hierarchical plans, not flat.** A master plan decomposes into workstreams by strategy domain. Each workstream is tailored to its executing agent. This enables parallel execution (backend + frontend simultaneously) and agent-appropriate detail levels.
- **Interface contracts as the parallelism enabler.** Shared types, proto definitions, and API shapes are produced by a dedicated workstream (typically first) and consumed by others. This is the "API contract" that lets teams (agents) work in parallel.
- **Detail level per workstream.** A strong agent (Claude Code) gets a high-level workstream with more autonomy. A weaker or more specialized agent gets more detailed steps. Same feature, different plan granularity.
- **One strategy per step.** Steps are scoped to a single strategy. Agent self-reports files via git diff.
- **Inlined skills.** `SkillRef.Content` carries the full SKILL.md text — plan is self-contained.
- **Frozen snapshots.** Decision/strategy snapshots frozen at plan-creation time.
- **Four-tier assertions.** Per-step (functional), per-workstream (domain integration), quality strategies (cross-cutting, applied to ALL workstreams), and global (whole project).
- **Quality strategies for best practices.** New strategy kind: `quality` (alongside `foundational` and `derived`). Cross-cutting strategies with machine-verifiable assertions that the supervisor enforces on every workstream's output — regardless of whether the agent "remembered" the instruction. Examples: DRY enforcement via duplication detectors, component library usage via grep, naming conventions via linters, import restrictions, test coverage thresholds. The skill tells the agent what to do (best effort); the quality strategy verifies it was done (enforcement).
- **Test-first workstream pattern.** A foundational quality strategy mandating that every workstream starts with defining acceptance tests and concludes with all tests passing. This is enforced structurally by the supervisor — a workstream cannot complete until its final test gate passes. The plan's acceptance criteria flow into the first step ("define tests"), and the supervisor won't mark the workstream as done until the last step ("all tests green") succeeds. This is a hard gate, not optional guidance.

### Supervision Loop Mechanics

Lives in `internal/dispatch/supervisor.go`. The supervisor manages the full lifecycle of plan execution.

**Agent interaction:**
- Agents are forked via `exec.Command` (e.g., `claude -p --output-format json --session-id <uuid>`)
- Each agent type implements an `AgentDriver` interface: `BuildCommand`, `BuildRetryCommand`, `ParseOutput`
- Retries use `--resume <session-id>` to continue the agent's conversation (retains full prior context)
- One-shot mode (not streaming) for supervision; streaming reserved for interactive CLI use

**Test strategy (plan specifies WHAT, agent decides HOW):**
- The plan specifies acceptance criteria for each step: what must be tested and how to determine pass/fail. These come from the feature spec (human-authored or council-enriched).
- The coding agent writes BOTH implementation AND tests. It can create new test files or augment existing ones, choose fixtures, reuse test helpers — it knows the codebase.
- The supervisor validates: (a) tests pass, (b) coverage threshold met, (c) `llm_review` assertion checks "do these tests actually cover the acceptance criteria in the plan?" — this catches self-serving tests without Locutus having to write the tests itself.

**Validation checks (layered, cheap to expensive):**
1. **Stub detection (regex):** Patterns: `TODO`, `FIXME`, `panic("not implemented")`, empty function bodies, trivial returns. If regex finds nothing, an LLM review catches semantic stubs.
2. **Dead code (toolchain):** Language linter (e.g., `staticcheck` for Go) + symbol reference counting (exported symbols defined vs referenced).
3. **Invented requirements (LLM):** Compare plan step description against git diff. Flag functions/endpoints/features not in the plan.
4. **Missing requirements (LLM):** Compare acceptance criteria against implementation. Flag unsatisfied criteria.
- Short-circuit: if stubs found, skip remaining checks. Fix stubs first.

**Retry protocol:**
- Max 3 attempts (1 initial + 2 retries). Hard limit.
- Feedback on retry includes: test output, validation issues, specific guidance, and rules (no TODOs, don't modify tests, don't add unrequested functionality).
- **Stuck detection:** Compare consecutive attempts. Stuck if: same test failure count AND same validation issues, OR diff is < 5 changed lines (cosmetic changes only). Progressing if: fewer test failures or fewer validation issues.

**Escalation cascade (cheapest to most expensive):**
1. `RefineStep` — regenerate tests and/or step description, reset retries, re-dispatch
2. `ExplicitGuide` — LLM generates detailed pseudocode/architecture guidance for the agent
3. `Replan` — return control to planner to decompose or change approach
4. `UserInput` — return `input_needed` MCPResponse with specific question (only for decision-level issues with low confidence)
5. `Abort` — report failure with full retry history and analysis

**Concurrency and resource management:**
- **Concurrency scheduler** separates what CAN run in parallel (DAG) from what WILL run in parallel (resource availability). Job-queue pattern: ready queue → running slots → blocked on dependencies.
- Configurable limits: `max_concurrent_total` (default 3) and per-agent limits (e.g., `claude-code: max_concurrent: 2`). Accounts for subscription bandwidth, machine resources, API rate limits.
- Priority: critical path first (longest dependency chain) or interface-contract producers first (unblock the most downstream work).
- Each parallel agent gets its own git worktree (`git worktree add`).
- On success: create PR from worktree branch, Locutus reviews (spec alignment, no stubs, tests pass), auto-merge to a local feature branch (e.g., `locutus/feature-auth`). No human halt per PR.
- **Push is human-only.** Locutus never pushes to remote. The user reviews the accumulated local state and pushes when satisfied. This avoids 30 halts for 30 workstreams while keeping the user in control of what goes upstream.
- Failed dependency: all downstream steps/workstreams skipped with reason.

**File conflict prevention:**
- **Plan-time prevention (default):** During planning, the critic flags file overlaps between parallel workstreams. The planner restructures: merge workstreams, add dependency edges, or extract shared files into their own workstream that runs first.
- **Runtime fallback (rebase):** If an unanticipated overlap occurs (agent touches files not in `ExpectedFiles`), the later-finishing workstream rebases onto the updated main. If rebase has conflicts, the supervisor either resolves via the coding agent or re-runs the conflicting steps against the new baseline (counts as a retry).

### Planning Council

The planning pipeline uses a cooperative council of agents, not a single LLM call. This mirrors the iterative refinement process used to design Locutus itself — proposing, challenging, researching, validating, recording.

**Council agents and workflow are fully externalizable — embedded then editable.**

Agent definitions live in `.borg/council/agents/` as YAML frontmatter + markdown body files (same pattern as spec files). The workflow DAG lives in `.borg/council/workflow.yaml`. Both are written from `embed.FS` defaults at `locutus init` and loaded at runtime. Users can customize model, temperature, system prompt per council role, add new roles, and reorder the workflow DAG — all without recompiling.

**Default council agents** (each in `.borg/council/agents/<id>.md`):

- **Planner** — Proposes features, decisions, strategies, and execution steps. Produces: proposed additions to the spec graph.
- **Critic** — Challenges the HOW: over-engineering, unnecessary complexity, simpler alternatives, technical risks. Produces: list of concerns with severity and suggested alternatives.
- **Researcher** — Investigates alternatives, fills knowledge gaps via web search and documentation. Produces: research findings mapped to specific concerns. Conditional: only runs when open questions exist.
- **Stakeholder** — The user's advocate. Validates the WHAT and WHY: does this accomplish user goals? Is scope proportional to value? Produces: alignment assessment, flagged mismatches.
- **Historian** — Two layers. Layer 1 (deterministic): records structured JSON events. Layer 2 (LLM): writes compelling narrative connecting decisions to the project arc. Produces: human-readable narrative in `.borg/history/summary.md`.
- **Convergence monitor** — LLM call using a fast/cheap model (e.g., Haiku, GPT-4o-mini). Assesses convergence with nuance: distinguishes "same concern but evolving responses" (progress) from "same debate, no new information" (cycling). Checks mechanical completeness (all workstreams have steps, all steps have assertions). Triggers readiness gate (critic + stakeholder sign-off). Can use its own criteria alongside other agents' feedback.

**Default workflow DAG** (`.borg/council/workflow.yaml`):
1. `propose` — planner (sequential)
2. `challenge` — critic + stakeholder (parallel), depends on propose
3. `research` — researcher (sequential), depends on challenge, conditional on open questions
4. `revise` — planner (sequential), depends on research
5. `record` — historian (sequential), depends on revise
6. Readiness gate: convergence monitor triggers, critic + stakeholder approve. Max 5 rounds. Force decision after 3 rounds on same concern.

**On-demand specialist agents** (called conditionally after core council rounds converge, before the plan is finalized):
- **Test architect** — Takes acceptance criteria from features/steps, produces executable test specifications (Playwright scripts, Go test skeletons, API integration tests). Knows the active testing strategy.
- **UI designer** — Takes feature descriptions (possibly with Figma links) and produces UI component descriptions detailed enough for a coding agent to implement. Knows the active frontend strategy and component library.
- **Schema designer** — Takes entity definitions and produces database migration specs, proto definitions, API contracts. Knows the active data and API strategies.
- Specialists are NOT permanent council members — they don't participate in every round. They're invoked after the core council converges on structure, to flesh out implementation details.
- Also defined as externalizable files in `.borg/council/agents/`. Users can add custom specialists (e.g., security reviewer, accessibility auditor, i18n specialist).

**Customization examples:**
- Change stakeholder prompt to represent a specific domain ("you represent a healthcare compliance officer")
- Add a security reviewer specialist for auth-related features
- Add an accessibility auditor specialist that produces WCAG compliance checks
- Skip the researcher for small changes
- Swap models per agent (use Opus for the planner, Haiku for the convergence monitor)

**Round budget:** Max 5 core council rounds per planning session. Each round involves 5-6 LLM calls (planner + critic + stakeholder + historian narrative + convergence monitor, optionally researcher). After convergence, specialist agents are invoked as needed (1-3 additional LLM calls). Total: 20-35 LLM calls per planning session.

**State between rounds:**
```
PlanningState {
  Round           int
  ProposedSpec    SpecDelta       // what the planner proposed this round
  Concerns        []Concern       // from critic + stakeholder
  ResearchResults []Finding       // from researcher
  Decisions       []Decision      // accumulated across rounds
  ResolvedConcerns []string       // concerns addressed in this round
  OpenConcerns    []string        // carried to next round
}
```

### Brownfield Analysis

Lives in `internal/agent/brownfield.go` (orchestrator) with supporting files: `collectors.go`, `inference.go`, `entities.go`.

**Pipeline:** Scan → Infer → Generate spec → Gap analysis → Fill gaps → Execute remediation → User reviews

**Scanning collectors (all heuristic, no LLM):**
1. **File inventory** — walk filesystem respecting .gitignore, extension frequencies
2. **Language detection** — definitive signals from config files (go.mod, package.json, Cargo.toml), fallback to extension frequency
3. **Config file parsing** — dedicated mini-parsers for go.mod, package.json, Dockerfile, CI configs, Makefile, buf.yaml, etc.
4. **Package/module structure** — detect Go cmd/internal/pkg layout, monorepo tools, frontend directory conventions
5. **Import graph** — parse imports using language-specific AST/regex to build internal dependency graph
6. **Test file patterns** — detect frameworks, coverage config, test directory conventions
7. **Entity/model extraction** — Go structs with DB tags (using `go/parser`), proto messages, TypeScript types, SQL migrations

**Decision inference (heuristic layer):**
- Config file presence → high-confidence decisions (e.g., go.mod → backend-language=Go, confidence 0.95)
- Dependency declarations → framework decisions (e.g., react in package.json → frontend=React, confidence 0.90)
- Multiple signals reinforce: combined confidence = 1 - (1-a)(1-b), capped at 0.98
- Conflicting signals: both candidates emitted with respective confidences + conflict annotation

**Decision inference (LLM layer — 2-3 batched calls total):**
1. Architecture + domain call: architectural style, domain classification, cross-cutting decisions, rationale for all heuristic decisions
2. Entity refinement call: classify significance (domain entity vs infrastructure type), infer relationship semantics, identify missing entities
3. Feature recovery call (optional): propose features this codebase implements

**Strategy inference:**
- Build/test commands extracted from Makefile/CI/package.json → strategy selection and commands map
- `governs` field populated by directory conventions + config file scope declarations
- `influenced_by` linked to inferred decisions
- LLM provides strategy naming, interface descriptions, and alternative suggestions

**Entity detection:**
- `EntityExtractor` interface with framework-specific implementations (GoStructExtractor, ProtoExtractor, etc.)
- FK detection from naming conventions, ORM tags, SQL foreign keys
- Cardinality from field types (singular = many-to-one, slice/repeated = one-to-many)
- Cross-source merge: same entity from Go struct + proto + SQL migration → unified with boosted confidence

**The heuristic/LLM line:**
- Heuristic: anything deterministically derivable from file content parseable with a grammar
- LLM: anything requiring understanding of intent, meaning, or context beyond syntax

**Gap analysis (after inference, before output):**
- Missing tests: files governed by strategies but with no corresponding test files
- Missing acceptance criteria: features without success criteria
- Undocumented decisions: code patterns implying a decision with no recorded decision
- Orphan code: files not governed by any strategy
- Missing quality strategies: no linter config? No CI? No coverage threshold?
- Stale documentation: README claims X but code does Y

**Gap filling — hybrid remediation model:** Cross-cutting gaps (missing CI, missing linter config, missing coverage thresholds) become a single consolidated "project-remediation" feature. Feature-specific gaps (missing auth tests, undocumented auth decisions) attach to their respective features. Both are filled with `assumed` decisions and strategies (not `inferred` — these are new, not recovered from code). The remediation plan goes through the council for validation, then executes via the normal dispatch/supervision pipeline. No pause for user input — autonomous, just like greenfield.

**Output:** Standard `.borg/` spec directory. Recovered decisions have `status: inferred`. Gap-fill decisions have `status: assumed`. Both converge to the same managed state. User reviews via `locutus status` and confirms/overrides via `locutus revisit`.

## Build Order

Each tier follows test-first discipline: **first step writes acceptance tests, last step runs them and confirms green.** Details for each component are in the Detailed Designs section above — build steps reference those designs rather than duplicating them.

### Tier 1: Core Infrastructure

**1a: Acceptance tests** — JSON round-trip for all types (Decision, Strategy, Entity, Feature, Bug, Manifest, TraceabilityIndex, MasterPlan, Workstream, PlanStep, Assertion). Frontmatter parse/render round-trip. Spec I/O load/save pairs, walk, orphan detection. CLI skeleton: `locutus version`, `--json`, `--verbose`.

**1b: Module init + CLI skeleton** — `go mod init`, `main.go` with Kong, `cmd/cli.go` with all command structs (check, init, status, version, update, diff, regen, revisit, mcp, triage, import).

**1c: Core types** — `internal/spec/types.go` (Decision, Strategy, Entity, Feature, Manifest), `bug.go` (Bug), `plan.go` (MasterPlan, Workstream, PlanStep, Assertion, InterfaceContract, SkillRef, all enums), `traces.go` (TraceabilityIndex), `response.go` (MCPResponse). Feature extended with `acceptance_criteria`, `decisions`. Strategy extended with `prerequisites`, `commands`, `skills`. Graph model: GOALS.md → Feature/Bug → Decision → Strategy → Source Files.

**1d: Frontmatter parser** — `internal/frontmatter/frontmatter.go`. Parse and render.

**1e: FS abstraction + Spec I/O** — `internal/specio/` (FS interface, OSFS, MemFS, SpecStore, pair load/save, walk, orphan detection).

**1f: Run Tier 1 acceptance tests — all green.**

### Tier 2: CLI Commands

**2a: Acceptance tests** — All CLI commands with mock data: check, init, status, update, triage, import. Init creates `.borg/`, `.borg/council/`, `.agents/skills/`, `GOALS.md` skeleton, `CLAUDE.md`, `AGENTS.md` symlink. Status renders correctly for empty and populated specs. Triage evaluates against GOALS.md and outputs JSON verdict. Import creates feature/bug from markdown input.

**2b: `locutus check`** — `internal/check/check.go`. Strategy-driven prerequisites. `Commander` interface for testability.

**2c: CLI rendering** — `internal/render/render.go`. Pterm-based rendering for all commands.

**2d: `locutus init`** — `internal/scaffold/scaffold.go`. Creates: `.borg/` (manifest, spec dirs, traces.json, history/, council/agents/, council/workflow.yaml), `.agents/skills/` (embedded generic SKILL.md files), `GOALS.md` skeleton, `CLAUDE.md` with Locutus usage notes, `AGENTS.md` symlinked to `CLAUDE.md`. Council agent definitions and workflow DAG written from `embed.FS`.

**2e: `locutus status`** — Walks spec, renders grouped summary (GOALS.md present?, features, bugs, decisions, strategies, entities, orphans).

**2f: `locutus update`** — Self-update via `go-selfupdate`. Version via `-ldflags`.

**2g: `locutus triage`** — `cmd/triage.go`. Reads markdown+frontmatter issue from file/stdin. Evaluates against GOALS.md (LLM call with GOALS.md as context). Outputs structured JSON verdict (accepted/rejected/duplicate).

**2h: `locutus import`** — `cmd/import.go`. Reads markdown issue, runs triage internally if not pre-triaged, creates feature or bug in `.borg/spec/`. Generates `.json` sidecar.

**2i: Run Tier 2 acceptance tests — all green.**

### Tier 3: Decision Graph

**3a: Acceptance tests** — DAG from synthetic fixtures: Feature → Decision → Strategy → Files. Forward walk from feature, decision, or strategy. Blast radius at each level. Cycle detection. `locutus diff` on features, decisions, and strategies.

**3b: DAG construction and traversal** — `internal/graph/graph.go`, `walk.go`. Loads all spec types (features, bugs, decisions, strategies). Builds full graph with Feature → Decision and Decision → Strategy edges. Forward walk, blast radius via traces.json, cycle detection.

**3c: `locutus diff`** — Accepts feature, decision, or strategy ID. Shows affected downstream nodes and governed source files.

**3d: Run Tier 3 acceptance tests — all green.**

### Tier 4: LLM + Council Infrastructure

**4a: Acceptance tests** — Genkit provider registration. Basic Generate call with structured output. Council agent definition loader (reads YAML+markdown, constructs Generate config). Workflow DAG loader and executor (runs rounds in correct order). Historian record/query/narrative.

**4b: Genkit LLM integration** — `internal/agent/agent.go`. Provider registration, model selection from manifest config, basic Generate with structured output.

**4c: Council agent loader** — `internal/agent/council.go`. Reads `.borg/council/agents/*.md` (YAML frontmatter: id, model, temperature, output_schema; markdown body: system prompt). Constructs Genkit Generate calls from the definitions.

**4d: Workflow DAG loader and executor** — `internal/agent/workflow.go`. Reads `.borg/council/workflow.yaml`. Executes rounds: sequential and parallel steps, conditional execution, readiness gate (convergence monitor triggers, critic + stakeholder sign-off). Max 5 rounds.

**4e: Historian** — `internal/history/history.go`. Structured JSON events + LLM-generated narrative. Record, query by ID, query alternatives, derived markdown summary.

**4f: Run Tier 4 acceptance tests — all green.**

### Tier 5: Planning Pipeline (Greenfield)

**5a: Acceptance tests** — Council produces a master plan from a prompt. Plan has correct hierarchy (workstreams, steps, assertions). Specialist agents flesh out details. GOALS.md evaluation for scope. Spec-derived artifacts generated (Taskfile, CLAUDE.md, AGENTS.md).

**5b: Greenfield planning pipeline** — `internal/agent/planner.go`. Orchestrates council rounds using workflow executor from 4d. Council agents loaded from 4c. Produces MasterPlan with workstreams. Records all decisions via historian.

**5c: Specialist agents** — Test architect, UI designer, schema designer. Invoked after council converges. Loaded from `.borg/council/agents/` like council agents.

**5d: Spec-derived artifact generation** — `internal/agent/taskfile.go` (Taskfile.yml from strategy commands), `internal/agent/agentsmd.go` (CLAUDE.md + AGENTS.md symlink from spec state), proto definitions if API strategy active.

**5e: GOALS.md evaluation** — LLM call with GOALS.md content as context. Used by triage command and by the stakeholder agent during planning.

**5f: Run Tier 5 acceptance tests — all green.**

### Tier 6: Brownfield Analysis

**6a: Acceptance tests** — Given a synthetic Go+React codebase: scanning produces correct CodebaseSnapshot. Heuristic inference produces correct `inferred` decisions. LLM inference adds rationale and cross-cutting decisions. Gap analysis identifies missing tests, orphan code. Remediation creates `assumed` decisions for gaps. Output is a standard `.borg/` spec directory.

**6b: Scanning collectors** — `internal/agent/collectors.go`. 7 parallel collectors: file inventory, language detection, config parsing, package structure, import graph, test patterns, entity extraction.

**6c: Heuristic inference** — `internal/agent/inference.go`. Rules that map signals to decisions (go.mod → backend=Go). Confidence scoring and conflict detection.

**6d: LLM inference** — Batched LLM calls (2-3 total): architecture + domain, entity refinement, feature recovery.

**6e: Entity extraction** — `internal/agent/entities.go`. `EntityExtractor` interface with framework-specific implementations (Go structs, proto, TypeScript, SQL migrations). FK detection, cardinality inference, cross-source merge.

**6f: Gap analysis** — Compares inferred spec against quality expectations: missing tests, undocumented decisions, orphan code, missing quality strategies, stale docs.

**6g: Remediation feature generation** — Cross-cutting gaps → consolidated "project-remediation" feature. Feature-specific gaps → attached to respective features. All gap-fill decisions marked `assumed`.

**6h: `locutus analyze` command** — `cmd/analyze.go`. Runs the full brownfield pipeline on the current directory. Produces `.borg/` spec. Can also be triggered by `locutus init` when it detects an existing codebase.

**6i: Run Tier 6 acceptance tests — all green.**

### Tier 7: Dispatch + Supervision

**7a: Acceptance tests** — Agent registry routing. AgentDriver implementations (ClaudeCode, Codex mock). Git worktree creation/merge/cleanup. Concurrency scheduler with configurable limits. Supervisor loop: pass, retry, stuck detection, escalation. PR creation and auto-merge to feature branch. Quality strategy enforcement. File conflict detection and rebase.

**7b: Agent registry** — `internal/dispatch/registry.go`. Agent definitions with strengths. Routing logic. Bootstrapped defaults.

**7c: Agent drivers** — `internal/dispatch/drivers/`. `AgentDriver` interface + `ClaudeCodeDriver` (wraps `claude -p`), `CodexDriver` (wraps `codex exec`). `BuildCommand`, `BuildRetryCommand` (uses `--resume`), `ParseOutput`.

**7d: Git worktree management** — `internal/dispatch/worktree.go`. Create worktree per agent, commit in worktree, merge to feature branch, cleanup. Handle merge conflicts via rebase.

**7e: Concurrency scheduler** — `internal/dispatch/scheduler.go`. Job-queue pattern. Configurable `max_concurrent_total` and per-agent limits. Priority: critical path or interface-contract producers first.

**7f: Supervisor** — `internal/dispatch/supervisor.go`. Full supervision loop per the Detailed Design: dispatch, validate (stub/dead code/invented/missing via layered checks), retry with session resume, stuck detection, escalation cascade. Quality strategy enforcement across all workstreams. Test-first workstream pattern enforced structurally.

**7g: PR creation and review** — Create PR from worktree branch. Locutus reviews (assertions, spec alignment). Auto-merge to local feature branch. Never push to remote.

**7h: `locutus regen` and `locutus revisit`** — `cmd/regen.go`, `cmd/revisit.go`. Regen: plan stale modules, dispatch, supervise. Revisit: consult historian, walk graph, re-plan, dispatch, supervise. Revisit is the one place `input_needed` can occur.

**7i: Run Tier 7 acceptance tests — all green.**

### Tier 8: MCP Server

**8a: Acceptance tests** — MCP server starts on stdio, responds to initialize. All tools registered (init, status, check, diff, regen, revisit, triage, import, analyze). Tool calls return MCPResponse. Progress notifications during long operations.

**8b: MCP server** — `cmd/mcp.go`. `modelcontextprotocol/go-sdk`. Stdio default, optional `--http`. All commands exposed as tools. Structured response contract. Progress notifications via MCP protocol.

**8c: Run Tier 8 acceptance tests — all green.**

## Final Verification

```bash
# Greenfield
./locutus init "my-app"                  # bare .borg/ scaffold + GOALS.md + council + skills
./locutus status                         # empty spec, GOALS.md present
# ... user describes a feature via MCP or CLI ...
./locutus check                          # validates prerequisites for active strategies
./locutus status                         # shows features, decisions, strategies, entities
./locutus diff auth-strategy             # preview blast radius
./locutus revisit auth-strategy          # update decision, cascade, re-plan, re-dispatch
./locutus regen                          # regenerate stale modules

# Brownfield
cd existing-project/
./locutus analyze                        # scan codebase, infer spec, gap analysis, remediate
./locutus status                         # shows inferred + assumed decisions

# Issue intake
./locutus triage --input issue.md --json # evaluate against GOALS.md
./locutus import --input issue.md        # create feature/bug from issue

# MCP
./locutus mcp                            # start MCP server for Claude Code / VS Code
```
