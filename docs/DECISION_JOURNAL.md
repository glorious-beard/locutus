# Decision Journal

This document captures the series of architectural decisions and pivots that shaped the Locutus implementation plan. Each entry records what was decided, what alternatives were considered, and why the final choice was made. This is the "historian for the historian" — a record of how Locutus itself was designed.

Session date: 2026-04-13 to 2026-04-14

## DJ-001: CLI Framework

**Decision:** Use `alecthomas/kong` instead of `spf13/cobra`.

**Alternatives considered:**
- Cobra — industry standard, but more boilerplate
- urfave/cli — simpler API
- No framework (stdlib `flag`)

**Why Kong:** User preference. Kong's struct-based command definitions are a cleaner fit for the `--json` and `--verbose` flag pattern, where global flags live on a parent struct and are naturally inherited by subcommands.

## DJ-002: Console Output Library

**Decision:** Use `pterm` for rich terminal output.

**Alternatives considered:**
- `text/tabwriter` (stdlib) — too basic for the UX we want
- `charmbracelet/lipgloss` + `bubbletea` — better for full TUI apps, overkill for CLI output
- Custom rendering

**Why pterm:** Closest Go equivalent to Python's "rich" library. Tables, spinners, progress bars, tree views, colored text — all without building a full TUI. The user explicitly asked for rich console output comparable to Python's ecosystem.

## DJ-003: LLM Access — The Claude CLI Pivot and Reversal

**Decision:** Use Genkit Go with Anthropic API keys.

**Journey:**
1. **Initial approach:** Shell out to `claude -p --output-format json` to use the user's Claude Max subscription (no API token costs). Research confirmed this was the officially supported way to use Max programmatically.
2. **Problem discovered:** Shelling out to `claude` CLI sacrifices control over conversation flow. No good way to forward feedback requests from the `claude` process back to the user through Locutus. Too much complexity for a cost optimization.
3. **Pivot back to API:** A separate conversation confirmed that `claude -p` was "a cost optimization that was going to compromise the architecture." Building a thin tool loop against the Messages API is a better fit.
4. **Framework selection:** Chose Genkit Go over rolling our own or using Eino (ByteDance). Genkit provides config-over-code model selection — swap providers by changing a string, no recompile.

**Key finding:** The Anthropic Go SDK cannot authenticate with Claude Max subscription — it requires API keys. Anthropic banned OAuth tokens from third-party SDKs in Feb 2026. This was a significant factor in the initial push toward `claude` CLI, and its resolution.

## DJ-004: MCP Transport

**Decision:** Stdio-first, optional HTTP.

**Research finding:** VS Code only supports stdio for MCP servers. Claude Code supports both stdio and HTTP. Stdio is the common denominator.

**Pattern:** `locutus mcp` starts stdio MCP server (spawned by client). `locutus mcp --http :8080` for remote/multi-client scenarios later.

## DJ-005: No Archetype Selection at Init

**Decision:** `locutus init` creates a bare spec structure with no stack assumptions. No archetype enum.

**Journey:**
1. Initially planned opinionated defaults from PLAN.md D-008 (Go + TanStack + Connect RPC)
2. Then planned to generate a full Hello World SaaS app at init
3. User questioned whether we were biased toward traditional SaaS monoliths — what about CLIs, microservices, daemons, libraries?
4. Realized that archetypes should emerge organically from the user's first prompt (greenfield) or codebase analysis (brownfield)
5. Also realized that "asking for archetype" at init was wrong — brownfield should discover it automatically

**Why no archetype:** The "archetype" is just the emergent combination of active skills and strategies. There's no enum to select because the possibilities are unbounded.

## DJ-006: Skills Over Templates

**Decision:** No template engine. Use SKILL.md files to guide LLM generation.

**Context:** The user is the author of `stamp` (github.com/glorious-beard/stamp), an MCP-based template rendering tool. They stopped development because well-written SKILL.md files provided equivalent DX without the template engine complexity.

**Why skills:** Templates are deterministic but rigid. Skills guide the LLM to produce correct code while allowing it to adapt to context. The skill is the expert knowledge; the LLM is the flexible executor.

## DJ-007: Everything Is a Strategy

**Decision:** Build systems, test runners, linters, formatters, and deployment tools are all strategies — not hardcoded in Locutus.

**Implication:** Locutus never calls `go build` or `go test` directly. It reads the active strategy's `commands` map. Switching from Go to Rust or from `go build` to Bazel is a decision revisit that cascades through strategies.

**Extension — Taskfile.yml:** Generated deterministically from strategies' `commands` maps. Thin facade over real build tools. Avoids stochastic LLM generation for deterministic things like build commands.

**Extension — Strategy prerequisites:** Each strategy declares its prerequisites (tools, versions). `locutus check` is strategy-driven — adding/removing strategies changes what gets checked.

## DJ-008: Planner + Delegator, Not Coder

**Decision:** Locutus produces execution plans for external coding agents. It does not generate application code itself.

**Journey:**
1. Initially planned Locutus as a code generator (generates code directly via LLM)
2. User noted that Claude Code, Codex, Charlie, Gemini etc. have billions in R&D behind them. Competing on code quality is a losing game.
3. Pivoted to planner model: Locutus focuses on decisions, strategies, and execution plans. External agents handle implementation.
4. Exception: spec-derived artifacts (Taskfile.yml, AGENTS.md, proto definitions) are generated directly — they're deterministic transforms, not creative coding.

**Why this works:** Locutus's unique value is architectural intelligence (decisions, strategies, history). Code generation is commodity. By delegating coding, Locutus is agent-agnostic — works with any coding agent, benefits from improvements in any of them.

## DJ-009: Autonomous Decisions During Planning

**Decision:** Locutus makes all decisions autonomously during planning (status: `assumed`, with confidence score). No `input_needed` during planning.

**Journey:**
1. Initially planned multi-turn conversations with `input_needed` chains during planning
2. Problem: forwarding feedback from the planning LLM to the user and back is complex, especially through MCP
3. Solution: Locutus decides everything itself, documents rationale and alternatives via the historian, and the user reviews later via `locutus status` / `locutus revisit`
4. `input_needed` only occurs during explicit `revisit` — the user asked to change something, so clarifying questions are appropriate

**Why autonomous:** Simpler MCP contract. Plans are always fully resolved and self-contained. No mid-planning callbacks. Aligns with D-004 from PLAN.md (Passive Generation Model).

## DJ-010: Agent Routing and Supervision

**Decision:** Locutus maintains a registry of coding agents with their strengths and supervises their output.

**User insight:** "Claude Code has a tendency to claim premature victory with stubbed out code and TODOs. Other agents invent requirements or implement dead code."

**Supervision loop:**
1. Generate acceptance tests first (test-first discipline)
2. Delegate to best-matched agent
3. Run tests
4. Validate: no stubs, no dead code, no invented requirements
5. If failing, retry with guidance; if stuck, escalate
6. Result must WORK — does exactly what was intended

**Agent routing:** Registry maps agents to strengths (languages/frameworks). Route plan steps to best available agent. Registry is itself a strategy — revisitable.

## DJ-011: Historian

**Decision:** Every decision/strategy change recorded as structured JSON with rationale and rejected alternatives.

**User insight:** "Git history isn't sufficient. We should have a historian agent that captures the motivations behind changes and alternatives considered."

**Value during revisit:** When revisiting a strategy, the historian surfaces previously rejected alternatives. "We considered Bazel in March but ruled it out because of team experience. Has that changed?"

**Format:** Structured JSON events (machine-queryable) + derived markdown summary (human-readable). Both in `.borg/history/`.

## DJ-012: Advisory Delegation

**Decision:** External agents can't be forced to use Locutus. AGENTS.md provides strong guidance but is advisory only. No drift detection infrastructure.

**Context:** VS Code Copilot may not even read AGENTS.md. Claude Code generally follows it but can't be forced.

**Why no enforcement:** Building drift detection or gatekeeper infrastructure adds complexity without guarantees. Better to invest in brownfield recovery (which can reconcile spec after direct edits) than in prevention.

## DJ-013: Test-First Tier Implementation

**Decision:** Each implementation tier starts with acceptance tests and ends with running them.

**Why:** Locutus's own supervision loop enforces test-first discipline on external agents. We should eat our own cooking. Writing tests first also forces us to define contracts before implementation, catching design issues early.

## DJ-014: Brownfield Self-Analysis

**Decision:** Don't scaffold `.borg/` for the Locutus repo during implementation. Use brownfield analysis in a later session.

**Alternatives:** Could manually create spec files from PLAN.md now.

**Why later:** Brownfield recovery captures actual state, not planned state. Some decisions may shift during implementation. Also dogfoods the brownfield feature — if it can't recover Locutus's own architecture, we have a bug.

## DJ-015: Competitive Positioning

**Conclusion from landscape research:** No open-source tool combines persistent decision graphs + spec-driven planning + agent supervision + historian. Closest is GitHub Spec Kit (spec-first philosophy but no decision persistence or supervision). Decision graph concept exists in theory but has no production implementation.

**User context:** Not competing with commercial offerings (Devin, Cursor). This is MIT-licensed open source. Goal is addressing what the user spends most time on when using coding agents — not building a business.

**Reference implementation:** User's Atlas shoe project (`/Users/chetan/projects/shoe`) is Locutus implemented manually: 13 specialized agents, historian, mandatory review gates, approach-auditor, dispatch protocol. Locutus automates this pattern.

## DJ-016: Execution Plan — One Strategy Per Step, Agent Self-Reports Files

**Decision:** Each plan step is scoped to one strategy but can touch multiple files. The agent self-reports files modified; `git diff --name-only` is the source of truth.

**Alternatives considered:**
- a) Explicitly specify one or more files per step — too rigid; agent may need to create helpers or modify unexpected files
- b) Discover files after modification, constrained to one strategy — viable but doesn't capture agent's own understanding
- c) Agent self-reports files at end of coding cycle, constrained to one strategy — **chosen**

**Why Option C:** The real constraint is one strategy per step (preserves traceability). Within that boundary, the agent should have freedom to touch whatever files are needed. `git diff --name-only` verifies the self-report. All files in the diff map to the step's governing strategy in `traces.json`. This handles cases where agents create helper files, update go.mod, or modify files not anticipated in the plan.

**What changed:** The `PlanStep` struct no longer has `FilePath` and `Action` for a single file. Instead it has `ExpectedFiles` (guidance, not enforcement) and the supervisor uses git diff for the actual file list.

## DJ-017: Locutus Writes Tests, Not the Agent

**Decision:** Acceptance tests are generated by Locutus's own LLM (Genkit), not by the coding agent being supervised.

**Why:** If the agent writes its own tests, it will write tests that pass its own implementation — defeating the purpose of test-first discipline. Locutus writes tests from the plan's acceptance criteria (which are independent of any implementation), writes them to the worktree before dispatching the agent, and the agent is told to make them pass without modifying the test files.

## DJ-018: Tier 3 Uses Synthetic Fixtures

**Decision:** Tier 3 (Decision Graph) tests use hand-crafted spec files as test fixtures, not data from the planner.

**Why:** Tier 3 is pure graph algorithms with no LLM dependency. The DAG construction and traversal code operates on typed structs loaded from JSON. Using synthetic fixtures keeps Tier 3 independent of Tier 4 (planner) and testable in isolation. Real data flows through the graph once Tier 4 is complete.

## DJ-019: Brownfield — Heuristic First, LLM Second

**Decision:** Brownfield analysis uses heuristics for everything deterministically derivable from parseable file content. LLM is reserved for understanding intent, meaning, and context beyond syntax.

**The line:**
- Heuristic: file inventory, config parsing, language/framework detection from dependency files, struct/type parsing, import graphs, FK detection from naming conventions
- LLM: architectural intent (monolith vs microservices), rationale recovery, cross-cutting concerns (auth patterns, error handling), entity significance, feature recovery

**Cost optimization:** LLM calls are batched (2-3 total), not one per decision. This keeps brownfield analysis fast and affordable.

## DJ-020: Retry Uses Session Resume, Not Cold Start

**Decision:** When the supervisor retries a failed agent step, it resumes the agent's existing session (`claude -p --resume <session-id>`) rather than starting a fresh conversation.

**Why:** Session resume gives the agent full context of what it tried and what failed. This is far more token-efficient and produces better results than cold-starting with "here's the task again plus what went wrong." The session ID is controlled by Locutus, not the agent.

## DJ-021: Genkit Go — LLM Plumbing Only, Not Agent Orchestration

**Decision:** Use Genkit Go strictly for LLM access (multi-provider Generate, tool registration, structured output). All agent orchestration, definition loading, and persistence is built by Locutus.

**Research finding:** Genkit Go cannot read agent definition files (AGENTS.md, SKILL.md) or memory files. It has no native agent support — agents are built manually using flows and tool definitions. The JS/TS version is significantly more mature for agent development, but we're in Go. Genkit Go's session system is in-memory only with no file-based persistence.

**What Genkit Go gives us:** Multi-provider model selection by config string, `ai.Generate()` with structured output, tool registration, system prompts, conversation history management.

**What Locutus builds on top:** SKILL.md loading and injection, agent registry and routing, supervision loop, historian, brownfield analysis, memory/persistence, all file-based spec I/O.

## DJ-022: Features as Product-Level Layer Above Decisions

**Decision:** Features sit above decisions in the spec graph: Feature → Decision → Strategy → Source Files. Decisions can be feature-driven or standalone (foundational/project-wide). Same for strategies.

**Why:** Features are the product spec — what the user actually cares about. "User authentication" is a feature. "JWT vs sessions" is a decision driven by that feature. Without features at the top, decisions float without product-level motivation. Features also carry acceptance criteria that flow down into plan step assertions, giving the supervisor concrete product-level success criteria.

**What changed:**
- Feature type gets `acceptance_criteria []string` and `decisions []string` (IDs it drives)
- Planning pipeline starts with features when user describes product-level intent, decisions when describing implementation-level intent
- Blast radius now traverses Feature → Decision → Strategy → Files (more powerful)
- Historian records feature-level context ("this decision exists because of the auth feature")

**Standalone decisions:** "Use Go" is a project-wide foundational decision not tied to any feature. These exist at the decision level with no parent feature. The graph allows orphan decisions and strategies.

## DJ-023: Agent File Generation Strategy

**Decision:** Locutus generates CLAUDE.md as the primary agent instruction file and symlinks AGENTS.md to it. SKILL.md files (open standard, agentskills.io) are generated per-strategy in `.agents/skills/` and referenced from CLAUDE.md.

**Research finding:** AGENTS.md is a Linux Foundation standard (60k+ repos) but Claude Code doesn't read it natively (feature request #6235). Claude Code reads CLAUDE.md and its own `.claude/` ecosystem. SKILL.md is an open standard supported by all major tools (Claude, Codex, Copilot, Cursor, Gemini).

**What we DON'T generate:** `.claude/agents/` definitions and `.claude/memory/` files. These are Claude Code internals for its own sub-agent orchestration and session recall. They're orthogonal to Locutus's spec management.

## DJ-024: Full Scope Validated — Supervision Is Not Incremental

**Context:** Mid-planning sanity check — is the full 18-step plan justified, or could this be a simpler MCP server that just manages the decision graph?

**Initial assessment:** ~60% of the plan (supervision, agent routing, Taskfile generation) seemed replicable with well-crafted skills in existing tools. Only the decision graph + historian + blast radius seemed genuinely novel.

**User pushback:** The shoe project (Atlas) demonstrated that even with 13 specialized agents, mandatory review gates, and a historian — all configured manually in Claude Code — the user still spent significant time:
- Detecting that Claude was solving the wrong problem and manually forcing step-backs
- Detecting code churn (cycling between approaches without converging)
- Catching failure to converge (same broken approach with different parameters)
- Missing silent decisions that became hardcoded and painful to change later

**Revised assessment:** The supervision loop is NOT incremental automation. It **replaces the human as the monitor** — the person who watches the agent, detects churn, forces step-backs, and catches missed decisions. A skill file can't do this because it provides instructions at session start but can't intervene mid-execution based on observed behavior. The full scope is justified:
- Decision graph + historian = long-term knowledge preservation
- Supervision loop = real-time waste prevention (replaces human monitoring)
- Blast radius = prevents cascading impact from decision changes
- Brownfield = recovers silently hardcoded decisions
- Feature layer = ensures the agent solves the right product problem

**Decision:** Keep the full 18-step plan. Every tier earns its place.

## DJ-025: Planning as a Cooperative Council, Not a Single LLM Call

**Decision:** The planning pipeline uses a cooperative council of agents running iterative rounds, not a single LLM call.

**Insight:** The process used to design Locutus itself — proposing, challenging, researching, validating, recording over multiple rounds — IS the planning pipeline. The council replicates this process:
- **Planner** — proposes approach (the HOW)
- **Critic** — challenges: "Is this necessary? Simpler way?"
- **Researcher** — investigates alternatives, fills gaps
- **Stakeholder** — the user's advocate: "Does this solve the right problem? Is the scope proportional to the value?" Distinct from the critic — critic challenges HOW, stakeholder challenges WHAT and WHY.
- **Historian** — records decisions (deterministic, not LLM)
- **Convergence monitor** — detects cycling, forces decisions after 3+ rounds on same concern (deterministic, not LLM)

**Why a council, not a single call:** A single LLM call produces a plan but doesn't challenge it. The critic and stakeholder catch over-engineering, wrong-problem-solving, and scope creep that a planner alone would miss. This mirrors how the user challenged the plan repeatedly ("are we biased toward SaaS?", "is this meaningful?") — those challenges made the plan dramatically better.

**Budget:** Max 5 rounds, 4-5 LLM calls per round (planner + critic + stakeholder + historian narrative, optionally researcher). 16-25 LLM calls total per planning session. Convergence monitor prevents runaway costs.

## DJ-026: Historian Uses LLM for Narrative, Not Just Deterministic Recording

**Decision:** The historian has two layers. Layer 1 (deterministic): records structured JSON events (what changed, old/new values, alternatives). Layer 2 (LLM): writes a compelling human-readable narrative connecting decisions to the broader project arc.

**Why LLM for narrative:** Structured JSON events are queryable but not useful to a human reader. The shoe project's LOG.md reads as a story ("After five days attempting CT-scan-derived sock maps, the domain translator identified that the hosiery industry has standard pattern templates..."). That narrative quality — highlighting what's surprising, noting reversals, providing context — requires an LLM. A mechanical event log would never produce that.

**The two layers complement each other:** JSON events are the source of truth for blast radius, revisit queries, and machine consumption. The narrative summary in `.borg/history/summary.md` is a derived artifact for human reading — a rich project history that explains not just what happened but why it matters.

## DJ-027: Hierarchical Plans (Plan of Plans) with Two-Level DAG

**Decision:** Plans are hierarchical. A master plan decomposes into workstreams (sub-plans), each tailored to the agent executing it. Both levels form a DAG — workstreams can depend on other workstreams, steps can depend on other steps within a workstream.

**Why hierarchical:**
- Different agents need different plan granularity (Claude Code = high-level autonomy; weaker agents = detailed steps)
- Parallel execution across domains (backend + frontend simultaneously)
- Scope control — no single plan exceeds an agent's context window
- Matches real engineering: you don't give the frontend team the same plan as the backend team

**Interface contracts are the key enabler:** Shared types, proto definitions, and API shapes are produced by a dedicated workstream (typically first, no dependencies) and consumed by downstream workstreams. This is what enables parallel work — once the contract is defined, backend and frontend can build independently.

**Plan convergence criteria (what makes a good plan):**
- Makes decisions the agent shouldn't make (architecture); leaves decisions the agent should make (implementation details)
- Detail level calibrated to the executing agent's capability
- Every step has testable (not subjective) success criteria
- No workstream exceeds the agent's context window
- Outer DAG maximizes parallelism via interface contracts

**What replaced:** The previous flat `ExecutionPlan` with a single list of steps. Now `MasterPlan` contains `Workstream[]`, each containing its own `PlanStep[]` DAG.

## DJ-028: Plan Readiness Is a Collaborative Gate, Not a Single Agent

**Decision:** Plan readiness is determined by a collaborative gate: the convergence monitor triggers the check (mechanical: stable + complete), then the critic and stakeholder each do a final sign-off. Both must approve. No dedicated plan reviewer agent needed.

**Why collaborative, not a single reviewer:**
- The convergence monitor detects "council stopped debating" — necessary but not sufficient. A plan can stabilize and still be too vague.
- The critic evaluates technical soundness: "Are there gaps? Is this over-engineered?"
- The stakeholder evaluates user alignment: "Does this serve the user's goals? Is the scope proportional?"
- Both perspectives are needed. A technically perfect plan that doesn't serve the user is worthless. A user-aligned plan that's technically flawed will fail in execution.

**Why not a dedicated reviewer:** The critic and stakeholder already have the context from participating in the council rounds. A fresh-eyes reviewer would need to re-read everything, adding cost without proportional benefit. The collaborative gate reuses existing roles in a new capacity.

## DJ-029: Genkit Go + Custom Orchestration, Not LangGraphGo

**Decision:** Keep Genkit Go for LLM access. Build ~350 LOC of custom orchestration for Locutus's council and supervision patterns. Do not add LangGraphGo or LangChainGo.

**Alternatives considered:**
- LangChainGo + LangGraphGo (drop Genkit) — Three LangGraphGo implementations exist in Go (`tmc`, `dshills`, `smallnest`), all immature (alpha/early-stage). Python LangGraph hit 1.0 but Go ports haven't caught up. Would add alpha dependencies and require untested integration.
- Genkit Go + LangGraphGo (both) — Two frameworks to integrate, nobody has tested this combination, architectural mismatch between LangGraph's ReAct-loop model and Locutus's council deliberation pattern.

**Why custom orchestration:**
- Locutus's patterns are specific: council rounds (sequential with parallel LLM calls), workstream DAG (topological sort + goroutines), supervision loop (retry with state tracking). These are ~350 LOC total, not a generic framework.
- LangGraph is designed for stateful agent conversation loops (ReAct, tool-use). Locutus's council is a deliberation among specialized roles — a different pattern that doesn't cleanly map to LangGraph's graph nodes.
- We already have state persistence (specio + historian) and don't need LangGraph's checkpointing.
- Genkit Go's config-string model selection (`anthropic/claude-sonnet-4-20250514`) directly supports manifest-driven provider switching, which LangChainGo doesn't offer as cleanly.

**What we build:** DAG executor (~150 LOC), council round manager (~200 LOC). What Genkit Go provides: multi-provider LLM access, structured output, tool registration.

## DJ-030: File Conflict Prevention at Plan Time, Rebase as Fallback

**Decision:** The critic flags file overlaps between parallel workstreams during planning. The planner restructures to eliminate them (merge workstreams, add dependency edges, or extract shared files into a dedicated workstream). If unanticipated overlaps occur at runtime (agent touches files not in ExpectedFiles), fall back to sequential rebase with conflict resolution.

**Why plan-time prevention over runtime merge:** Merge conflicts during agent execution are expensive — the agent may need to re-run steps, and automated conflict resolution is unreliable. Preventing overlaps at plan time is cheaper and more predictable. The rebase fallback handles edge cases where agents touch unexpected files.

## DJ-031: Concurrency Scheduler with Configurable Resource Limits

**Decision:** A concurrency scheduler separates what CAN run in parallel (DAG topology) from what WILL run in parallel (resource availability). Configurable limits per-agent and globally.

**Why:** The DAG says "4 workstreams can run in parallel" but Claude Max might only support 2 concurrent sessions. Codex might have its own limits. The user's machine might not handle 5 worktrees. The scheduler is a standard job-queue pattern (ready queue → running slots → blocked) with configurable limits that the user sets based on their subscription and hardware.

## DJ-032: PR-Per-Workstream, Auto-Merge Locally, Human Pushes

**Decision:** Each workstream produces a PR. Locutus reviews and auto-merges PRs to a local feature branch (e.g., `locutus/feature-auth`) without human intervention. Locutus never pushes to remote — the human reviews the accumulated local state and pushes when satisfied.

**Why not halt per PR:** A plan with 30 workstreams would require 30 human approvals, killing the DX. The user doesn't want to be interrupted for each workstream — they want the work to flow and to review the final result.

**Why not auto-push:** Pushing to remote is irreversible (in a team context). The user needs a review point before changes are visible to others. Local auto-merge gives Locutus full autonomy during execution while keeping the user in control of what goes upstream.

**The model:**
- Locutus creates PRs per workstream, reviews them (spec alignment, tests, no stubs), auto-merges to a local branch
- Work flows continuously — no halts
- User reviews the accumulated state on the local branch (git log, diff, run the app)
- User pushes when satisfied, or resets/cherry-picks if something is wrong

**PR review checks:** Acceptance tests pass, spec alignment (files match traces.json), no stubs/TODOs, interface contracts satisfied, auto-generated PR description (which feature, which decisions/strategies, what acceptance criteria).

## DJ-033: Features Are Human-Initiated, Council-Enriched

**Decision:** The human writes the feature spec (any level of detail). The council enriches it with acceptance criteria, edge cases, entity links, and technical considerations. The human reviews the enriched spec before it drives decisions.

**Why not human-only:** A one-liner prompt ("add auth") should be enough to kick off work. The council can flesh out acceptance criteria and edge cases that the human might not think of. But the human always writes the initial intent.

**Why not LLM-generated:** Features can include rich artifacts — Figma mockups, screenshots, user stories from customer research — that an LLM can't produce. The `.md` body is the human's space (prose, links, images). The `.json` sidecar is Locutus's space (structured acceptance criteria, entity refs, decision links).

**The enrichment flow:** Human writes feature → planner adds acceptance criteria and edge cases → stakeholder validates it represents user intent → critic checks for gaps → human reviews enriched spec → spec drives decisions and strategies.

## DJ-034: Quality Strategies for Best Practice Enforcement

**Decision:** Best practices are modeled as a new strategy kind (`quality`, alongside `foundational` and `derived`). Quality strategies are cross-cutting — applied to ALL workstreams by the supervisor, not just one. They carry machine-verifiable assertions (linters, duplication detectors, grep patterns) that the supervisor enforces regardless of whether the agent "remembered" the instruction.

**Why not rely on skills alone:** Claude Code (and other agents) demonstrably forget or ignore instructions as context grows — even with a 1M token window. Skills loaded into agent context are best-effort guidance. Quality strategies with machine-verifiable assertions are enforcement — the supervisor checks after the agent finishes, and fails the step if violations are found.

**The two-layer model:**
- **Skill (tell):** SKILL.md says "always use the `<Button>` component from our design system, never raw `<button>`". The agent will usually follow this. Best effort.
- **Quality strategy (verify):** Assertion `not_contains` on .tsx files for `<button`. The supervisor catches violations the agent missed. Enforcement.

**Examples:** DRY enforcement (duplication detector), component library usage (grep for raw elements), naming conventions (linter rules), import restrictions (grep for forbidden paths), test coverage thresholds, no console.log in production code, max function length.

**Four-tier assertion model:** Per-step (functional) → per-workstream (domain integration) → quality strategies (cross-cutting best practices) → global (whole project).

## DJ-035: LLM-Based Assertions Alongside Deterministic Checks

**Decision:** Assertions can be either deterministic (`test_pass`, `contains`, `compiles`, `lint_clean`, etc.) or LLM-based (`llm_review`). Deterministic assertions run first (fast, cheap). LLM review assertions run last (slower, costlier, but catch semantic issues).

**Why not deterministic-only:** Some quality checks require judgment that regex and linters can't provide: "Does this code follow the separation of concerns in the architecture strategy?", "Is the error handling consistent with patterns elsewhere?", "Does this UI match the visual language of the design system?" These are real concerns that agents routinely get wrong, and no heuristic can catch them.

**The `llm_review` assertion:** Carries a `Prompt` field with the specific review question. The supervisor sends the changed files (or diff) plus the prompt to an LLM and evaluates the response. This is a separate LLM call from the coding agent — an independent reviewer, not the agent reviewing its own work.

**Cost management:** Deterministic assertions short-circuit — if they fail, LLM reviews don't run (fix the cheap failures first). LLM reviews only run on passing code, keeping cost proportional to quality.

## DJ-036: Council Agents and Workflow DAG Are Externalizable Files

**Decision:** Council agent definitions are YAML frontmatter + markdown body files in `.borg/council/agents/`. The council workflow DAG is `.borg/council/workflow.yaml`. Both are written from embedded defaults at `locutus init` and loaded at runtime. Users can customize without recompiling.

**Why externalizable, not code-only:**
- Advanced users can tune the council: change a model, adjust temperature, rewrite a system prompt
- The stakeholder's prompt can be project-specific ("you represent a healthcare compliance officer")
- New council roles can be added without recompiling (e.g., a security reviewer for auth features)
- The workflow DAG can be reordered, steps can be made conditional, parallelism can be adjusted
- Council definitions are versioned in git alongside the spec

**The embedded-then-editable pattern:** `locutus init` writes defaults from `embed.FS`. At runtime, Locutus reads from `.borg/council/`. User edits are picked up automatically. `locutus update` can refresh defaults without overwriting user customizations (only update files the user hasn't modified).

**Genkit Go integration:** Genkit Go doesn't support loading agent definitions from files (DJ-021). Locutus reads the YAML (model, temperature, output schema) and markdown (system prompt) and constructs the Genkit `ai.Generate()` call programmatically. The file format is Locutus's own, not Genkit's.

## DJ-037: Convergence Monitor Uses LLM, Not Just Deterministic Checks

**Decision:** The convergence monitor is an LLM call using a cheap/fast model (Haiku-class), not purely deterministic code.

**Why LLM:** Deterministic convergence checks ("did the concerns list change?") can't distinguish between:
- Same concern raised three rounds in a row but planner's response evolved each time → progress, not cycling
- Two new concerns raised but they're minor refinements → plan is substantively ready
- Stakeholder approved but with low confidence → worth one more round

An LLM (even a cheap one) can make these nuanced judgments using its own criteria alongside the other agents' feedback. The cost is minimal — Haiku-class models are fast and cheap.

**What changed:** Convergence monitor moves from deterministic code to an LLM agent with its own definition file in `.borg/council/agents/`. Still configurable — user can set the model, adjust the convergence criteria. Round budget updated: 5-6 LLM calls per round (was 4-5).

## DJ-038: On-Demand Specialist Agents for Plan Fleshing-Out

**Decision:** Implementation details (executable acceptance tests, UI descriptions, schema designs) are handled by on-demand specialist agents, not the core planner. Specialists are invoked after the core council converges on structure.

**Specialists:** Test architect (Playwright scripts, Go test skeletons), UI designer (component descriptions from feature specs), schema designer (migrations, proto definitions, API contracts). Users can add custom specialists (security reviewer, accessibility auditor, i18n specialist).

**Why not the planner:** The planner proposes architecture ("we need an auth service"). Writing a Playwright script or describing a UI component tree is a different skill. Overloading the planner degrades both its architectural reasoning and its implementation detail quality. Specialists can also use domain-specific models or prompts optimized for their task.

**How they fit:** Core council rounds converge on structure → readiness gate passes → specialist agents flesh out implementation details (1-3 additional LLM calls) → master plan is complete with both architecture and executable detail.

## DJ-039: Agent Writes Tests, Plan Specifies Criteria (Reverses DJ-017)

**Decision:** The coding agent writes both implementation AND tests. The plan specifies acceptance criteria (WHAT to test, pass/fail conditions). The supervisor validates that tests actually cover the criteria via `llm_review` assertion.

**Reverses DJ-017** ("Locutus writes tests, not the agent") because:
- Dictating test code is the same over-prescription problem as over-detailed plans
- The agent knows the codebase — it can augment existing test files, reuse test helpers, choose appropriate fixtures
- "Plan specifies WHAT, agent decides HOW" should apply to tests just as much as implementation

**Risk mitigation:** The original concern (agent writes tests that pass its own broken implementation) is mitigated by the `llm_review` assertion: "Do these tests actually cover the acceptance criteria specified in the plan?" This is an independent LLM review, not the agent reviewing its own work. Combined with coverage thresholds and deterministic checks, this catches self-serving tests without Locutus having to write them.

## DJ-040: Test-First Workstream Pattern as a Quality Strategy

**Decision:** Every workstream must start with defining acceptance tests and conclude with all tests passing. This is a foundational quality strategy enforced structurally by the supervisor — a hard gate, not optional guidance.

**The pattern:** Plan acceptance criteria → first step: agent defines/writes tests → middle steps: agent implements → final step: all tests pass. The supervisor won't mark a workstream as complete until the test gate passes.

**Why a quality strategy, not just an instruction:** Instructions get forgotten. A quality strategy is enforced by the supervisor on every workstream regardless of what the agent does. The test-first pattern is too important to be advisory — it's the primary mechanism for ensuring the result actually works.

## DJ-041: GOALS.md as Project Root + Issue-Driven Intake

**Decision:** GOALS.md is a human-authored document at the project root that defines project scope, success criteria, and in/out-of-scope boundaries. GitHub issues are automatically evaluated against GOALS.md for intake. Features and bugs are spec artifacts.

**The hierarchy:** GOALS.md → Feature/Bug → Decision → Strategy → Source Files

**Why GOALS.md:**
- Gives the stakeholder agent an objective reference for scope evaluation instead of relying on LLM judgment
- Automatic scope filtering: "add blockchain support" to a medical device project → rejected
- Automatic bug triage: security bugs auto-escalated if GOALS.md says "security is critical"
- Duplicate detection: new issue matches existing spec → closed with link

**Issue-driven intake:** GitHub issues → evaluated against GOALS.md → in-scope features enter the planning council, out-of-scope rejected with explanation, bugs triaged by severity. Zero-issue count as a quality strategy.

**Bug as a spec artifact:** Lives in `.borg/spec/bugs/`. Has: id, title, severity (auto-triaged), status, reproduction steps, related feature/decision, root cause (filled after analysis), fix plan. Simpler lifecycle than features but follows the same markdown+JSON sidecar pattern.

**Motivation:** The .NET/5000-issues problem. Open-source projects drown in untriaged issues. Locutus as an autonomous triage + resolution engine is genuinely novel and addresses a real pain point. The goal is 90% autonomous improvement, driving toward zero open issues.

## DJ-042: Local-Only, No Write-Back to External Issue Trackers

**Decision:** Locutus is local-only. `locutus import <source>` reads an external issue once and creates a local spec artifact. Locutus never writes back. Deep integration with GitHub/Jira/Linear is explicitly deferred.

**Why local-only:**
- Deep integration is a maintenance nightmare (GitHub + GitLab + Jira + Linear + Azure DevOps — each with different APIs, auth, data models)
- Write-back requires OAuth scopes, webhook handling, conflict resolution, permission management — disproportionate complexity for the value
- Local-only doesn't prevent adoption: `locutus import github#123` is a one-liner, the user already has the GitHub CLI
- The shoe project managed 26 phases of complex hardware design without issue tracker integration
- It's open source — if someone wants Jira integration, they build it

**Features are live capabilities, not tasks.** Status: `proposed`, `active`, `removed`. Never "resolved." Features represent what the product does, not work to be completed.

**Bugs tie to features and have a lifecycle:** `reported` → `triaged` → `fixing` → `fixed`. Fixed when code changes pass tests. User closes the external issue manually.

## DJ-043: Triage Command + CI-Bridge Pattern

**Decision:** Add `locutus triage --input <file> --json` command that evaluates an issue against GOALS.md and outputs a structured JSON verdict (accepted/rejected/duplicate). A thin CI wrapper (GitHub Action) handles the external system interaction on both sides.

**The pattern:** CI fetches issue → pipes to `locutus triage` → reads JSON verdict → acts on external system (comment, label, close). Locutus never calls external APIs, never needs API keys.

**Why this approach:** Locutus stays local-only (DJ-042) but the triage capability is still usable in automated workflows. The CI wrapper is ~20 lines of YAML. Different platforms write their own wrappers. Locutus's structured JSON output is the universal interface — same pattern as MCP (Locutus produces structured output, something else presents/acts on it).

## DJ-044: Markdown Input for Triage/Import, Not JSON

**Decision:** The input format for `locutus triage` and `locutus import` is markdown with YAML frontmatter, not JSON. The CI exporter (provider-specific) converts from the external system's format to markdown.

**Why markdown:** Issues are already written in markdown. Markdown carries inline images, Figma links, code blocks, discussion threads — rich content that JSON can't naturally represent. Locutus already has a frontmatter parser. The markdown body becomes the feature/bug `.md` file directly.

**The flow:** External system → provider-specific exporter → markdown+frontmatter → `locutus triage`/`locutus import` → structured JSON verdict (for triage) or local spec artifact (for import). If import is called without prior triage, it runs triage internally and rejects out-of-scope items.

## DJ-045: Brownfield Includes Gap Analysis and Autonomous Remediation

**Decision:** After inferring the spec from existing code, brownfield runs a gap analysis (missing tests, undocumented decisions, orphan code, missing quality strategies, stale docs) and fills the gaps autonomously with `assumed` decisions and strategies. Same pattern as greenfield — no pause for user input.

**Why autonomous, not pause:** Greenfield doesn't pause to ask the user about every decision — it assumes and the user reviews later. Brownfield should be the same. The only difference is the starting point: brownfield starts with `inferred` decisions (from code), greenfield starts empty. Gap-fill decisions are `assumed` (new, not recovered from code). Both converge to the same fully managed state.

**Gap categories:** Missing tests, missing acceptance criteria, undocumented decisions (code implies a choice but no decision is recorded), orphan code (files not traced to any strategy), missing quality strategies (no linter, no CI, no coverage), stale documentation.

**The remediation plan** goes through the council for validation and executes via the normal dispatch/supervision pipeline.

## DJ-046: Hybrid Remediation — Cross-Cutting + Feature-Specific

**Decision:** Cross-cutting gaps (missing CI, linter config, coverage thresholds) become a single consolidated "project-remediation" feature. Feature-specific gaps (missing auth tests, undocumented auth decisions) attach to their respective features.

**Why hybrid:** Pure consolidation loses the feature-level context ("these missing tests are for auth"). Pure per-feature loses the cross-cutting view ("the project has no CI at all"). Hybrid gives both: the consolidated feature handles infrastructure gaps, individual features handle their own quality gaps.

## DJ-047: Full Build Order Rewrite — 8 Tiers

**Decision:** Rewrote the entire build order after a comprehensive gap analysis identified ~20 missing pieces across all tiers. Expanded from 6 tiers to 8.

**Key changes:**
- Tier 1: Added Bug type, plan types (MasterPlan, Workstream, PlanStep, Assertion), GOALS.md concept
- Tier 2: Added `triage`, `import` commands. Init now creates GOALS.md, council agents, workflow.yaml, AGENTS.md symlink
- Tier 3: Graph now includes Feature → Decision edges and supports diff on features
- Tier 4: Split into "LLM + Council Infrastructure" — council agent loader, workflow DAG loader/executor, historian. No longer includes planning or brownfield.
- Tier 5: NEW — "Planning Pipeline (Greenfield)" — council orchestration, specialist agents, spec-derived artifacts, GOALS.md evaluation
- Tier 6: NEW — "Brownfield Analysis" — its own tier with 7 collectors, heuristic/LLM inference, entity extraction, gap analysis, remediation, `locutus analyze` command
- Tier 7: Expanded dispatch — added AgentDriver implementations, git worktree management, concurrency scheduler, PR creation/review
- Tier 8: MCP server (was Tier 6) — added triage/import/analyze as MCP tools
- Package layout updated with ~15 new files across spec, agent, dispatch packages

## DJ-048: Minimal CLI, MCP as Primary Interface, Headless via --json

**Decision:** CLI is minimal interaction (stdin prompts for revisit, pterm spinners for progress, text output). MCP is the primary interactive interface — supported by VS Code, Claude Code, JetBrains, Cursor, Windsurf, Zed, Gemini CLI, and likely Antigravity. Headless mode via `--json` flag on every command. Rich TUI is a future feature if demanded.

**Why not rich CLI now:** MCP covers all major IDEs via stdio transport. Most users will access Locutus through whatever AI assistant their IDE provides. A rich bubbletea-based TUI would cost 500-1000 LOC, delay shipping, and serve a narrow audience (power terminal users). Start minimal, add later.

**Three modes, same core:** Every command produces structured data (MCPResponse). MCP returns JSON to the client. CLI renders via pterm. Headless outputs raw JSON. The difference is presentation only — all three share the same engine.

---

Session date: 2026-04-16 to 2026-04-17 — post-Tier-8 refinements

## DJ-049: Generic Step Executor Extraction

**Decision:** Extract a generic `internal/executor` package that powers both the planning council workflow and workstream dispatch. Parameterized by a `State` type. Provides dependency-ordered execution, bounded parallelism via semaphores, per-type concurrency limits, snapshot isolation for parallel steps, optional convergence loop, and progress events via channel.

**Why:** The planning council DAG and the Tier 7 dispatch DAG are the same pattern with different payloads. Rather than duplicate coordination logic, extract it once and let callers provide typed state and a `RunStep` function. The planning workflow wraps it as `WorkflowExecutor[PlanningState]`; the dispatcher wraps it as `executor.Executor[dispatchState]`.

**Alternatives considered:** Keep two separate implementations (planning-specific + dispatch-specific), or adopt a larger agent-framework dependency (Eino, CrewAI-equivalent). Rejected the first as duplication. Rejected the second as overkill — the primitive is ~200 lines of Go with generics.

## DJ-050: brownfield → assimilation Rename

**Decision:** Rename "brownfield" to "assimilation" throughout the codebase: package names, types (`BrownfieldRequest` → `AssimilationRequest`), enum values (`PlanActionBrownfield` → `PlanActionAssimilation`), comments, and agent definitions.

**Why:** "Brownfield" is enterprise jargon that doesn't fit the Borg theme. "Assimilation" matches the project's naming convention and is more descriptive of what the pipeline actually does — it absorbs an existing codebase into the spec graph.

## DJ-051: Flat Scaffold Layout

**Decision:** Scaffold structure is flat: `internal/scaffold/agents/` holds all 15 agent definitions; `internal/scaffold/workflows/` holds `planning.yaml` and `assimilation.yaml`. On disk after `locutus init`: `.borg/agents/` and `.borg/workflows/`.

**Why:** The earlier nested hierarchy (`council/agents/`, `council/brownfield/agents/`, `council/supervision/agents/`) was organizational overhead with no functional benefit. Agents are loaded the same way regardless of category; workflows reference agents by ID. The flat layout is simpler, cleaner, and easier to navigate.

**Alternatives considered:** Keep nesting by category (planning/assimilation/supervision). Rejected because agent IDs are unique across categories and the loader doesn't care which subdirectory they came from.

## DJ-052: Agent Definitions Are the Prompt Source of Truth

**Decision:** Each agent `.md` file contains the full prompt: identity, context, task, output format, quality criteria, and anti-patterns. Go code in `projection.go`, `convergence.go`, and `supervisor.go` only injects dynamic context (state snapshots, event data) as user messages.

**Why:** Scattered prompt engineering across Go code is hard to iterate on, review, and version. Consolidating prompts in `.md` files makes them: editable by non-developers, diffable in PRs, isolatable for A/B testing, and loadable at runtime (users can customize per-project after `locutus init`).

**Alternatives considered:** Keep prompt fragments in Go code for compile-time safety. Rejected because prompt engineering is iterative content authoring, not programming — locking it in Go tightens feedback loops unnecessarily.

## DJ-053: Capability Tiers with Multi-Provider Resolution

**Decision:** Agent frontmatter specifies `capability: fast|balanced|strong` instead of a specific model. The capability tier resolves to an actual model at `BuildGenerateRequest` time via configurable mapping. Default mapping uses Anthropic models (Haiku/Sonnet/Opus). Future: discover available providers from env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`) and map tiers to the best available model per provider, possibly via LLM-powered routing for task-specific provider selection.

**Why:** Hardcoding model names in agent defs couples content authoring to specific providers. Capability tiers let users: swap providers without rewriting agents, pay Haiku prices for bounded-judgment tasks (convergence, historian, watchdog), reserve Opus for complex architectural work.

**Implementation today:** Static tier → default Anthropic model mapping. Multi-provider LLM routing deferred to future plan.

## DJ-054: JSON Schema via Struct Tags and Registry

**Decision:** Agent frontmatter can specify `output_schema: MasterPlan` (or other registered type name). At `BuildGenerateRequest` time, Go reflects the corresponding type and appends a JSON schema to the system prompt. Struct tags (`jsonschema:"description=..."`) provide field-level documentation. A `schemaRegistry` maps type names to example instances.

**Why:** LLMs produce more reliable structured output when given an explicit schema, and descriptions next to fields keep the schema in sync with Go code. The alternative — inlining schemas as Markdown in agent `.md` files — drifts from the Go types over time.

**Pattern:** `github.com/google/jsonschema-go` (already a transitive MCP SDK dependency) handles reflection. Equivalent to Pydantic's `Field(description="...")` in Python.

## DJ-055: Executor Uses func(any) bool for Step.Conditional, Accepting Generics Leak

**Decision:** `executor.Step.Conditional` has signature `func(state any) bool` even though the executor is generic on `State`. Callers type-assert `state.(*MyState)` in their closures.

**Why:** `Step` is not generic — making it so would require `Step[S]` everywhere and significantly complicate the API. The `any`-typed conditional is a pragmatic leak of the generic contract. Callers handle it with a small type assertion at the closure boundary. Accepted as a Go generics limitation rather than a design flaw.

**Alternatives considered:** Make `Step` generic (too invasive), use an interface with a type parameter (awkward), remove conditional from `Step` (would push conditionality into `RunStep` itself, losing the optimization of skipping before resource allocation).

## DJ-056: Fast-Tier LLM Monitor Replaces Go Heuristic Watchdog

**Decision:** For fuzzy supervision decisions (churn detection, scope drift, stalled progress, invented requirements), use a fast-tier LLM ("Haiku-class") invoked periodically over a sliding event window. Go code handles only mechanical bookkeeping: ring buffer of recent events, cooldown clock between invocations, circuit breaker for repeated errors. No pattern-detection heuristics in Go.

**Why:** Heuristics for "what counts as churn" would always chase edge cases. Coding agents evolve, emit new event patterns, interleave legitimate retries with actual cycles. An LLM observes the pattern in context and adapts without code changes. Tuning happens in the `monitor.md` agent's prompt, not in Go. Cost is bounded by a cooldown (≥10 events between invocations) and a cheap model tier.

**Alternatives considered:** Pure Go heuristics (fragile, high maintenance). Pure LLM on every event (prohibitive cost). Tiered — Go watchdog triggers LLM judgment (still has the heuristic fragility problem). Picked pure periodic LLM because it shifts all judgment to the prompt, which is the right surface for this kind of decision.

## DJ-057: Permission/Question Routing via Tool-Name Registry, Not Heuristics

**Decision:** `EventPermissionRequest` and `EventClarifyQuestion` are identified by matching the event's tool name against a per-driver registry:

- Permission tool: the tool name we registered via `claude -p --permission-prompt-tool <name>`. Because we configured the name, match is definitional, not inferred.
- Question tool: the provider's documented SDK tool name (e.g., Claude's `AskUserQuestion`).

If a driver doesn't support either mechanism, those events simply don't fire for that provider — acknowledged limitation, not papered over.

**Why:** Heuristic detection ("is this a Bash command that looks dangerous?") would be fragile and lead to false positives/negatives. The tool-name match is structural because the identification is either by configuration we chose or by documented provider convention.

## DJ-058: Churn and Retry Are Distinct

**Decision:** Churn and retry are separate supervision phenomena:

- **Retry** is vertical — a new attempt after a failure signal (validation rejected, test failed, timeout). Lives in the outer `Supervisor.Supervise` loop.
- **Churn** is horizontal — repeating action cycles within a single attempt detected from the event stream. Causes the current attempt to abort early to save tokens.

Churn-aborted attempts feed the retry loop with pattern-specific feedback. Two consecutive churn-aborts on the same step escalate to `RefineStep` because the step itself is likely the problem.

**Why:** Conflating them leads to wrong responses. A step that churns and then recovers (cycle, then validation failure normally) isn't the same as a step that consistently cycles. The distinct counter (`consecutiveChurns`) separates these modes cleanly.

## DJ-059: Streaming Supervision Deferred to Follow-Up Plan

**Decision:** Supervisor currently runs coding agents in batch mode (`CommandRunner` returns `[]byte`). Streaming supervision — NDJSON event loop with mid-attempt churn detection, permission/question routing, MCP progress forwarding — is captured in `.claude/plans/streaming-supervision.md` for execution in a future session.

**Why:** The current batch supervisor works for all existing tests and the 8 tiers as originally specified. Streaming requires ~10 new files, touches every driver, and significantly expands the supervision surface. Better to keep it as a coherent follow-up plan than jam it into the already-large tier sequence.

**Plan scope includes:** normalized `AgentEvent`, pull-based stream parser per driver, sliding-window LLM monitor, permission/question tool-name registry, MCP progress notifications, heartbeat and size-bomb timeouts for mid-stream detection (belt-and-suspenders with reassembly-based monitor), context-cancellation propagation to kill forked processes.

## DJ-060: Dispatcher Uses Executor, Steps Within a Workstream Use a For-Loop

**Decision:** The outer workstream DAG uses `executor.Executor[dispatchState]` for dependency ordering, parallel execution, and per-agent concurrency limits. The inner step iteration within a single workstream is a plain `for` loop in `Dispatcher.runWorkstream`.

**Why:** Workstream-level parallelism makes sense (different workstreams run in different worktrees, different agent sessions). Step-level parallelism within a workstream does not — all steps share one worktree and one agent session, so parallel execution would cause git state conflicts and session state chaos. The for-loop correctly models this sequential reality.

**What this means for `PlanStep.DependsOn`:** The field exists but its job is plan-time ordering validation (making sure `Order` is consistent with declared dependencies), not runtime parallelism enforcement.

**Resisted temptation:** Nesting a second executor inside `runWorkstream` for "symmetry." Rejected as over-abstraction — the executor adds value where it eliminates duplication, not where it just looks consistent.

---

Session date: 2026-04-17 to 2026-04-18 (streaming supervision build-out)

## DJ-061: Streaming Supervision Plan Executed End-to-End (Closes DJ-059)

**Decision:** The streaming-supervision plan deferred in DJ-059 shipped across 13 commits. Batch `CommandRunner` signature replaced with `io.ReadCloser`, supervisor's outer loop rewritten around `runAttempt`, NDJSON parser + delta reassembler for Claude Code, fast-tier LLM monitor with ring buffer + cooldown + circuit breaker, MCP permission bridge via a `locutus mcp-perm-bridge` subcommand + Unix socket, MCP progress forwarding through a session-wrapped notifier.

**What actually shipped vs what the plan specified:** All 9 parts closed with real assertions and no `t.Skip`. 61 new tests, entire repo clean under `go test -race -count=1`. Live smoke test against real `claude --output-format stream-json` green end-to-end (~9s via Claude Max OAuth, zero API tokens). The pre-existing batch `Supervise` is removed; streaming is the only supervisory path.

**Deferred from the plan:** Codex and Gemini CLI driver fixtures/parsers (need real captures once each provider's auth is configured). The `locutus mcp-perm-bridge` subcommand is built and tested but not yet wired into `ClaudeCodeDriver.BuildCommand` — the supervisor exposes a `permBridge` hook that tests set directly; production wire-up happens when the Dispatcher gets a CLI entry point.

## DJ-062: Permission Bridge via In-Process MCP Server, Not Stream Parsing (Reverses DJ-057)

**Decision:** Permission events surface via a Unix-socket bridge from an in-process MCP server (`locutus mcp-perm-bridge` subcommand), not by tool-name matching on the agent's public event stream.

**Reverses DJ-057** (which proposed identifying permission events by matching the configured permission-prompt tool name in the stream). That premise was factually wrong for Claude Code. Verified experimentally against a running `claude --print --permission-prompt-tool mcp__perm__locutus_permission` with a stub MCP server: when the agent wants a restricted tool, Claude invokes the permission-prompt tool as a **separate MCP RPC** on a side channel, not as a `tool_use` event in the public stream. The stream only shows the original restricted tool (e.g., `Bash`) followed by a `tool_result` reflecting our allow/deny. So the stream parser can never see the permission request — it's invisible to stdout-based observation.

**What's correct now:** the supervisor opens a Unix socket per supervision session, spawns `locutus mcp-perm-bridge --socket <path>` as Claude's MCP server, and reads `PermRequest{id, tool, input}` off the socket as `AgentEvent{Kind: EventPermissionRequest, InteractionID: id, ...}`. `handleInteraction` asks the validator/guardian LLM for an allow/deny verdict, then routes back through `PermBridge.Respond`. No claude resume is needed — the blocked MCP RPC returns, Claude continues.

**`ClassifyToolName` survives as a utility:** DJ-057's intended mechanism is still sound for providers that *do* surface these events as tool calls in-stream (hypothetical). The function is kept and tested, just not wired into the Claude parser.

**AskUserQuestion visibility is unverified.** Claude's SDK docs describe an `AskUserQuestion` tool but we haven't confirmed whether it appears as a `tool_use` in `--print --output-format stream-json` mode. Treated as future extension; `EventClarifyQuestion` exists in the taxonomy and plumbs through the same bridge architecture if the fixture capture confirms it.

## DJ-063: Sliding-Window Churn Rule over Consecutive Counter (Refines DJ-058)

**Decision:** Escalate to `RefineStep` when ≥2 of the last 3 attempt outcomes are `churnDetected`. Validation-only failures occupy slots in the window without counting as churn.

**Refines DJ-058** (which described a simple `consecutiveChurns` counter incremented on churn and reset on any non-churn outcome). That rule fails on alternating patterns — churn → validation-fail → churn — because the reset on the middle attempt clears the counter even though the step is clearly stuck in a loop.

**Regression test:** `TestSupervise_AlternatingChurnFailChurn_Escalates` exercises exactly that pattern and would fail the consecutive-counter implementation. Added as the guard against any future revert.

**Non-churn outcomes:** stay in the window but don't contribute to the count. They push old churn out once the window fills (after the 4th attempt, the oldest slot is dropped). This preserves the "N-of-last-M" semantics without letting validation failures pile up as evidence of churn.

## DJ-064: FastLLM Field Bounds Monitor Cost (Extends DJ-056)

**Decision:** `SupervisorConfig` gains a `FastLLM agent.LLM` field distinct from the strong-tier `LLM`. `Supervisor.monitorCycle` uses `FastLLM`; `Supervisor.validate` and `handleInteraction` use `LLM`. When the monitor agent is configured but `FastLLM` is nil, the supervisor surfaces a clear "FastLLM is nil" error at call time rather than routing monitor prompts through the strong tier.

**Extends DJ-056.** The original plan had the monitor calling `s.cfg.LLM`, which in production would send every monitor cycle through the strong tier — defeating the "bounded cost" property that was DJ-056's whole point. The separate field makes the cost envelope explicit: monitors burn fast-tier tokens, validators/guardians burn strong-tier tokens, and callers who care (most importantly, `Dispatcher`) plumb both through explicitly.

**Missing monitor agent behavior:** when `AgentDefs["monitor"]` is unset, `monitorCycle` logs an INFO notice exactly once per supervisor (via `sync.Once`) and returns `IsCycle=false`. Silent disable — validation at attempt end still catches bad outcomes; a one-time log means misconfiguration is discoverable without noise.

## DJ-065: End-to-End Smoke Test Caught Three Production Bugs That Mock-Only Unit Tests Had Hidden

**Decision:** Between Parts 6 and 7 of the streaming supervision build, paused feature work and wrote a hand-rolled integration test (`internal/dispatch/live_integration_test.go`, gated behind `LOCUTUS_INTEGRATION_TEST=1`) that runs the batch dispatcher against a real Claude Code subprocess on a trivial "create hello.txt" step. The test surfaced three production bugs in the pre-existing Dispatcher path that unit tests had never run into:

1. **`ClaudeCodeDriver.BuildCommand` didn't set `--permission-mode`.** In `-p` mode Claude can't prompt, so the default `default` permission mode auto-denies any tool call that would require approval. Claude would *claim* to have created the file in its response text but never actually touch the filesystem. Fixed by adding `--permission-mode acceptEdits` (allows file edits, still gates shell/network).

2. **Worktree branch and feature branch shared the same name.** `CreateWorktree` created `locutus/<id>`; `Dispatcher.runWorkstream` later tried to merge into `locutus/<id>` — but git won't check out a branch already used by a worktree. Merge failed with `'locutus/hello' is already used by worktree at '...'`. Fixed by splitting into `locutus-wt/<id>` (scratch) and `locutus/<id>` (feature target).

3. **`WorkstreamResult.BranchName` pointed at the transient scratch branch.** That branch is deleted in `Cleanup()` after merge, so callers saw a BranchName that no longer existed. Fixed by overwriting `BranchName` with the feature branch name after successful merge.

**Why this matters as a decision:** all three bugs were catchable by a ~50-line integration test. None were catchable by the existing extensive unit-test suite because mocks substituted for the real external behavior. The lesson — "validate assumptions about external systems with a real run before stacking more layers" — is now policy: the live integration test is kept in-repo, runs via `LOCUTUS_INTEGRATION_TEST=1`, and every time the streaming path changes it's re-run against real Claude to confirm no regression.

**Alternatives considered:** continuing the per-part unit work and validating end-to-end at the end. Rejected because by the time we'd reached Part 9 with the old bugs unfixed, we'd have been debugging a four-layer interaction instead of three one-line fixes.

## DJ-066: Genkit Wired with Env-Driven Plugin Auto-Detection (Completes DJ-003)

**Decision:** `internal/agent/genkit.go` no longer stubs. `NewGenKitLLM()` inspects the environment via `DetectProviders()`, registers `github.com/firebase/genkit/go/plugins/anthropic` when `ANTHROPIC_API_KEY` is present and `github.com/firebase/genkit/go/plugins/googlegenai` when `GEMINI_API_KEY` or `GOOGLE_API_KEY` is present, and exposes an `agent.LLM` backed by `genkit.GenerateText`.

**Completes DJ-003.** The original decision committed to Genkit Go as the LLM abstraction layer but left `Generate()` returning `"GenKit LLM provider not yet wired"`. That stub is replaced; the live smoke test (`TestGenKitLLM_LiveSmoke` with `LOCUTUS_INTEGRATION_TEST=1`) hits a real provider through a real API key loaded from `.env`.

**Why env-driven auto-detection:** the Genkit plugins `panic` during `Init` if their API-key env var is missing. Registering a plugin unconditionally would brick the whole process for any user who hasn't set up *every* provider. `DetectProviders()` inspects env first, registers only the matching plugins — a Gemini-only user never pulls in the Anthropic plugin (and vice versa). `sync.Once` guards `genkit.Init` against the plugins' second-initialization panic.

**Claude Max subscription caveat:** Anthropic's OAuth (used by the `claude` CLI, zero-cost for Max subscribers) can't be used by the Go SDK — the Anthropic Go SDK requires an API key. So a user with Claude Max who sets `ANTHROPIC_API_KEY` burns API tokens, not Max credits. This is surfaced in the Genkit wire-up commit's rationale and mentioned when users face the choice.

**Provider prefix required:** Genkit requires `anthropic/...` or `googleai/...` on every model string — it routes by prefix. Model strings without a prefix fall back to the configured default via `GenKitLLM.resolveModel`. Callers can override at three layers: `LOCUTUS_MODEL` env var (global), per-`AgentDef.Model` field (per agent), or `LOCUTUS_MODELS_CONFIG` YAML override (per-project or per-user — see DJ-067).

## DJ-067: Model Tier Config via Embedded YAML, List-per-Tier Runtime Resolution (Supersedes DJ-053)

**Decision:** Tier → model mapping moves from hardcoded Go maps into an embedded `internal/agent/models.yaml`. Each `CapabilityTier` holds an ordered list of candidate model strings; `ModelConfig.ResolveTier(tier, providers)` walks the list and returns the first entry whose provider prefix is enabled in `DetectedProviders`. List order is the user's preference when multiple providers match.

**Supersedes DJ-053** (which established three-tier capability routing with hardcoded `DefaultModels` + `GoogleAIDefaultModels` maps). Those maps are removed. The problem with two parallel maps was that provider availability is a runtime fact — a Gemini-only user needs a `googleai/` entry for every tier at *call* time, not at compile time. The new list-per-tier form collapses the two maps into one config and picks at resolution time.

**File format:**

```yaml
tiers:
  fast:
    - googleai/gemini-2.5-flash-lite
    - anthropic/claude-haiku-4-5-20251001
  balanced:
    - googleai/gemini-2.5-flash
    - anthropic/claude-sonnet-4-6
  strong:
    - anthropic/claude-opus-4-7
    - googleai/gemini-2.5-pro
```

**Override path:** set `LOCUTUS_MODELS_CONFIG` to a YAML file with the same shape. Missing file errors loudly (user asked for it — silent fallback would hide typos). Env unset = embedded defaults.

**Refresh on `locutus update`:** deferred, not dropped. The plan is that `locutus update` refreshes the user's local override (when they have one) by keeping their provider-order preference per tier and updating the model names themselves to whatever ships in the newly-embedded defaults. `--freeze-models` opts out. The config loader and resolver are shaped to support this; the merge logic is a small follow-up whose primary design cost was the file format we now have.

**Alternatives considered:** (a) LLM-based periodic classification of model names into tiers — rejected as overkill for current scope (circular: "need a fast model to classify fast models"; nondeterministic; tokens cost). (b) Name-heuristic substring matching (`opus`/`pro` → strong, `haiku`/`flash-lite` → fast) — rejected for V1 because it breaks on naming-convention changes (what is Gemini 3 "Ultra"?). The embedded+override approach wins on simplicity and on matching the natural update cadence: Genkit plugins become aware of new models when the SDK version bumps, which is also when we'd refresh the YAML.

---

Session date: 2026-04-20

## DJ-068: Manifest/State Separation — Kubernetes-Inspired Reconciliation Model

**Decision:** The spec graph (desired state / manifest) and the runtime state store (observed state) are separate concerns. The spec graph is immutable-ish desired state; the state store is a mutable record of what has actually been reconciled.

**State store entry shape:**

```yaml
approach_id: oauth-login           # always an Approach node ID — only Approaches own artifacts
spec_hash: sha256:abc123           # hash of the Approach spec node at last reconcile
artifacts:                         # path → sha256; per-file for granular drift detection
  src/auth/oauth.go: sha256:def456
  src/auth/oauth_test.go: sha256:789abc
status: live
last_reconciled: 2026-04-20T14:32:00Z
workstream_id: ws-2                # last workstream that planned this Approach; N Approaches share one WorkstreamID
```

Only `Approach` nodes have state store entries. `Feature` and `Strategy` nodes derive their status from their Approach children — if all Approaches under a Feature are `live`, the Feature is implicitly live. Goals derive from their Feature and Strategy children. No state store entries are created for Features, Strategies, Decisions, or Goals.

`artifacts` is a `map[string]string` (path → SHA-256) rather than a single aggregate hash. A single hash would require re-hashing every file on every reconciliation check with no way to report which specific file drifted. Per-path hashes let the reconciler identify exactly which artifact changed, report it precisely in `out_of_spec` output, and enable future optimisations (mtime pre-check before re-hashing unchanged files). Paths double as the artifact list, so no separate `artifact_paths` field is needed.

**State store lives in-repo** (`.locutus/state/*.yaml`). Version-controlled, diffable, auditable via `git log`. Consistent with the spec-as-source-of-truth principle.

**Spec node lifecycle:**

- `unplanned` — spec exists; not covered by any workstream. Valid long-term resting state.
- `planned` — included in the master plan; topologically sorted; agent not yet dispatched.
- `in_progress` — agent dispatched and working.
- `live` — reconciler ran tests and they passed. The asserting of actual state.
- `failed` — reconciler ran tests and they failed, or agent errored. Routes back to `planned` for retry.
- `drifted` — `spec_hash` changed since last reconcile (spec is newer than artifacts; forward drift). Routes to `planned`.
- `out_of_spec` — `artifact_hash` changed outside Locutus (code edited manually; backward drift). Surfaces for human review with three resolution paths: (1) update or create a spec node to cover the change, then re-plan; (2) accept the change as a fix and mark `live`; (3) revert the artifact and re-reconcile from spec.

**The reconciler asserts `live` or `failed`** by running tests — not by checking that code was written. This is the mechanism that makes the state store an honest account of the system's actual condition.

**Dependency resolution at plan creation time:** When a spec node is added to the master plan, the planner walks its full transitive dependency subgraph, collects all non-`live` nodes, and topologically sorts the resulting set into the workstream. The user never needs to manually include dependencies; the planner discovers them. `unplanned` is not a warning state — nodes sit there until they become reachable from an active workstream.

**History is a separate concern.** The spec graph reflects only active desired state. The history agent captures what changed, when, and why. There is no `superseded` or `deprecated` state in the state store — outdated nodes are removed or replaced, and the history agent holds the record. This matches the Kubernetes model: the manifest shows current desired state; audit history lives elsewhere.

**Why this separation:** Without a distinct state store, the spec graph does double duty as both desired state and operational status. This conflation makes drift detection, reconciliation targeting, and workstream planning harder than it needs to be. The separation gives the reconciler a clean loop: diff spec_hash vs. stored spec_hash, diff artifact_hash vs. stored artifact_hash, act on the result.

**Alternatives considered:**

- Encode status directly on spec nodes — rejected because it mixes desired state with observed state, making the spec graph both harder to read and harder to version cleanly.
- Out-of-repo state store (local SQLite or similar) — rejected in favor of in-repo YAML. In-repo state is diffable, survives repo clones, and participates in the same version control as the spec.

## DJ-069: DAG Node Type Redesign — Goal / Feature / Strategy / Decision / Approach

**Decision:** Replace the original `Feature → Decision → Strategy → Code` hierarchy with a redesigned DAG: `Goal → (Feature | Strategy) → Decision`, with a new `Approach` node as the synthesis layer handed to coding agents. The `Code` node type is removed entirely.

**The problem with the original hierarchy:** `Strategy` was doing two unrelated jobs — (1) high-level architectural and engineering excellence concerns (language choices, CI, observability, deployment), and (2) per-feature implementation approaches that refine a Feature against the active strategies. Conflating them made the graph ambiguous and artifact ownership unclear.

**Node roles:**

| Node | Role | Owns artifacts? | State derives from |
| --- | --- | --- | --- |
| `Goal` | High-level objective; anchor for Features and Strategies | No | Children |
| `Feature` | User-facing capability; present-tense statement | No (via Approach) | Its Approaches |
| `Strategy` | Architecture / engineering / production excellence; present-tense statement | No (via Approach) | Its Approaches |
| `Decision` | Assumption or architectural choice; leaf constraint; guides generation | No | N/A (active or removed) |
| `Approach` | Synthesis of a Feature or Strategy against applicable Decisions; implementation brief for the coding agent | Yes | Reconciler test results |

**Artifact ownership:** Only `Approach` nodes own artifact paths and participate in the `spec_hash` / `artifact_hash` reconciliation loop. Features and Strategies derive their state from their Approach children. Goals derive state from their Feature and Strategy children.

**Decisions are pure spec.** A Decision is a constraint that informs how a Feature or Strategy is stated and how its Approaches are generated. Decisions do not flow directly to the coding agent — they are already incorporated into the present-tense statements of the Feature or Strategy nodes above them. ADR documents generated from Decisions are part of the spec store, not the artifact store.

**Decision propagation:** When a Decision is revisited (updated or replaced), all parent Feature and Strategy nodes rewrite their present-tense statements to reflect the new decision. Any Approach nodes hanging off those parents are marked `drifted` and re-queued for reconciliation. There is no local-vs-global distinction: change the Decision, cascade to all parents, let the history agent record what changed. The spec graph always reflects current active intent.

**Decision lifecycle:** A Decision is either active (present in the graph) or it is removed. No `deprecated` status — a Decision that no longer applies simply ceases to exist. Its removal propagates to parents exactly as an update would.

**Feature deprecation:** When part of a Feature is deprecated, the deprecated portion is removed or marked `deprecated` with a pointer to its replacement nodes. Feature A rewrites its present-tense statement to drop the deprecated content. New Feature nodes (children of Feature A if the umbrella concept holds, or siblings under the same Goal if Feature A is being fundamentally replaced) are created and enter the normal `unplanned` lifecycle. The history agent records the deprecation rationale.

**Approach cardinality:** Each Approach has exactly one parent (a Feature or a Strategy). A Feature or Strategy may have multiple Approach nodes (one per distinct implementation concern). This keeps artifact ownership unambiguous and cascade behavior simple. Shared implementation concerns do not live in a shared Approach — they surface as a top-level Strategy with its own Approach.

**Graph shape (actual DAG, not a tree):**

```text
Goal
├── Feature A  (present-tense; rewrites when child Decisions change)
│   ├── Decision X  ◄──── also a child of Strategy B
│   ├── Decision Y
│   ├── Approach A1  (owns src/feature-a/part1.go + tests)
│   └── Approach A2  (owns src/feature-a/part2.go + tests)
└── Strategy B  (present-tense; rewrites when child Decisions change)
    ├── Decision X  (same node as above — true DAG edge)
    └── Approach B1  (owns .github/workflows/ci.yml, Dockerfile)
```

Decisions are shared DAG nodes (N parents). Approaches are owned leaf nodes (1 parent). Goals and Features/Strategies are interior nodes with derived state.

**Why remove the `Code` node type:** `Code` as a spec node added a layer of granularity (specific files or functions as reconciliation targets) that belongs to the artifact store, not the spec graph. The spec describes *what* and *why*; the artifact paths on an Approach describe *where* it lives. A separate `Code` node conflated spec intent with implementation detail.

**Rationale for the Approach node:** Without it, the coding agent would receive raw spec nodes (Feature/Strategy) and raw Decision constraints, and would have to perform the synthesis itself on every invocation. The Approach node externalizes that synthesis as a first-class spec artifact — it can be versioned, reviewed, and re-generated independently of its parent Feature or Strategy. It also provides a clean reconciliation target: the Approach is what drifts, not the Feature.

**Approaches are denormalized by design.** The council/planner agents read the full spec graph (parent Feature or Strategy, all applicable Strategies, all relevant Decisions) and produce a self-contained markdown synthesis stored in the Approach's `Body` field. The coding agent receives the Approach and needs nothing else from the spec graph — it does not resolve the parent Feature, look up Decisions, or consult Strategy commands at runtime. The `Decisions []string` field on the Approach is an audit trail of which decisions were consulted during synthesis, not a pointer the agent follows.

The execution layers map as follows:

- **Approach** (spec layer) — durable, versioned synthesis of "what to build and why"; owned by the spec graph; participates in reconciliation lifecycle
- **PlanStep** (plan layer) — ephemeral execution instruction generated from an Approach at plan time; includes inlined file context, skills, and assertions; consumed directly by the coding agent
- **Workstream** (session layer) — groups PlanSteps for a single agent session; supervised by Locutus; one workstream covers N Approaches

The same Approach generates different PlanSteps on successive planning runs (brownfield context, existing files, and current codebase state all affect what the PlanStep instructs). The Approach itself only changes when the spec drifts.

**Philosophical grounding:** Locutus is built on the premise that requirements are vague and will remain so — especially in startup contexts where a week-old requirement may already be stale. The node design reflects this: Features and Strategies are living present-tense statements that absorb Decision revisions rather than accumulating stale history. The spec graph always represents current intent. Assumptions are captured explicitly as Decisions, not buried in prose. The freedom to revisit any Decision at any time, with automatic cascade, is the mechanism that keeps the spec honest without requiring perfect requirements upfront.

## DJ-070: Node ID Generation — LLM-Derived Kebab-Case Slugs

**Decision:** All spec node IDs (Feature, Strategy, Decision, Approach, Bug, Entity) are kebab-case slugs derived from the node's title by the generating agent at creation time. Sequential counter IDs (e.g., `DEC-001`, `FEAT-003`) are retired for new nodes. Existing nodes with legacy IDs are left untouched — the ID field is a plain string and any value is valid.

**Slug format:**

- Lowercase; spaces and underscores become hyphens; non-alphanumeric characters removed
- Truncated at a word boundary to ~50 characters
- Example: `"OAuth Login via Google"` → `oauth-login-via-google`
- Example: `"Implement OAuth token exchange"` → `implement-oauth-token-exchange`

**Collision resolution (handled by `specio`, not the generating agent):**

At save time, `specio` checks whether the ID already exists in the store. On collision, it appends `-` plus the first 6 hex characters of `SHA-256(title + ISO-8601 creation timestamp)`. The agent never sees this — it proposes a title; `specio` handles uniqueness.

Example: if `oauth-login-via-google` exists, the new node becomes `oauth-login-via-google-a3f912`.

**Why LLM-generated rather than author-assigned:**
The primary author of spec nodes is the council of LLM agents, not the human. Having the LLM derive a slug from the title it just wrote is the natural equivalent of a human naming a Kubernetes manifest. No user input required; the name is always meaningful because it reflects the node's title at the moment of creation.

**Alternatives considered:**

- **Sequential IDs (`DEC-001`)** — rejected. Require a central atomic counter. In a filesystem-based spec store with concurrent agents or multiple users, two sessions can independently emit the same counter value. No safe distributed increment exists without a coordinator.
- **UUIDs / ULIDs** — rejected. Globally unique and collision-free, but opaque. A Decision ID like `01HZQK7R3P...` cannot be referenced meaningfully in prose specs, in Decision.InfluencedBy lists, or in agent prompts. Human-readability is load-bearing in Locutus.
- **Pure content hash** (SHA-256 of title alone) — rejected. Two agents independently generating a Decision with the same title produce the same ID, silently aliasing two distinct nodes. The timestamp component in the collision suffix prevents this.
- **Kubernetes-style author naming** — considered but deprioritised. Works well when humans author manifests directly; less natural when the council is the primary author. The LLM slug approach gives the same DX (readable names, no counter) without requiring a human naming step.

**Applies to Approach nodes (DJ-069):** Approach IDs are derived from the Approach's own title — the implementation brief title the generating agent writes — not from the parent node's ID. The parent relationship is expressed through `Feature.Approaches []string` and `Strategy.Approaches []string`, not encoded in the child's ID.

**CLAUDE.md count update:** this entry brings the total to 70.

## DJ-071: Pre-Flight Clarification Protocol — Coding Agent Ambiguity Resolution Before Implementation

**Decision:** Introduce a `pre_flight` phase in the workstream execution lifecycle, sitting between `planned` and `in_progress`. During pre-flight, Locutus presents the Approach Body and PlanSteps to the coding agent in a constrained "clarify only" mode (no implementation). The agent returns a list of ambiguities. Locutus resolves each by consulting the spec graph or making an explicit assumption. All assumptions are recorded as new Decision nodes, which cascade through the spec graph exactly as any other Decision revision would (parent Feature/Strategy rewrites its present-tense statement; dependent Approaches are marked `drifted`). Once all ambiguities are resolved or the round limit is reached, the Approach transitions to `in_progress` and implementation begins.

**Why emulate rather than delegate to native agent planning:**
No coding agent (Claude Code, OpenAI Codex, Gemini Code, or others) exposes a programmatic planning API that Locutus can participate in. Claude Code has `--plan` mode but only interactively; there is no subprocess hook. Emulating the planning phase at the Locutus level is therefore required for consistency across all agents — and it is superior to native agent planning because the outputs (resolved ambiguities, captured assumptions) become durable spec graph artifacts rather than ephemeral internal agent state.

**Updated reconciliation lifecycle:**

```text
unplanned → planned → pre_flight → in_progress → live / failed
                                                        ↓
                                             drifted / out_of_spec → planned
```

- `pre_flight` — Workstream presented to coding agent; agent returns ambiguities; Locutus resolves and records assumptions as Decisions; bounded to a configurable maximum number of rounds (default: 3)

- If the round limit is reached with unresolved ambiguities, the remaining ambiguities are recorded as `assumed`-status Decisions (best-effort assumption) and execution proceeds

**Protocol detail:**

1. Locutus presents: Approach Body, PlanStep descriptions, relevant file context
2. Coding agent responds with: a structured list of questions or ambiguities (not code)
3. For each question, Locutus:
   a. Checks if the answer exists in the spec graph (Feature acceptance criteria, Decision rationale, Strategy constraints)
   b. If yes: returns the answer with a reference to the spec node
   c. If no: generates an assumption, creates a new Decision node (`status: assumed`, `confidence < 1.0`), cascades through spec graph, returns the assumption as the answer
4. Updated context (with resolved ambiguities) is appended to the Approach Body before handing off to the coding agent for implementation
5. The Approach's `UpdatedAt` timestamp is bumped; `spec_hash` in the state store is recomputed

**Why capture assumptions as Decisions rather than inline answers:**
Inline answers live in the agent session and disappear after execution. A Decision node persists, participates in the spec graph, can be revisited, and will cascade to all other Approaches that depend on the same parent if it's later revised. This is the mechanism that keeps the spec honest over successive workstream executions — the spec graph accumulates the team's actual decisions, not just the ones made at planning time.

**Relationship to existing escalation cascade:**
Pre-flight is distinct from the existing `RefineStep → ExplicitGuide → Replan → UserInput → Abort` escalation cascade, which handles failures *during* implementation. Pre-flight runs *before* implementation and cannot fail in the same way — unresolved questions are assumed, not escalated. If an assumption later proves wrong, `out_of_spec` drift surfaces it for correction.

**Impact on spec types:**
- `ReconcileStatus` gains `pre_flight` as a new status between `planned` and `in_progress`
- No other type changes required; new Decisions created during pre-flight follow the slug ID scheme (DJ-070) and the standard Decision lifecycle (DJ-069)

---

Session date: 2026-04-21

## DJ-072: CLI Surface Consolidated to 8-Verb Lifecycle Shape

**Decision:** Replace the 13 enumerative commands that accumulated through Tiers 1–8 (plus post-Tier-8 streaming work) with an 8-verb lifecycle that maps one-to-one to the phases a user actually moves through when operating on a Locutus project.

**The 8 verbs:**

| # | Command | Lifecycle phase | Purpose |
| --- | --- | --- | --- |
| 1 | `locutus init` | Bootstrap | Create the `.borg/` scaffold. |
| 2 | `locutus update` | Bootstrap | Refresh the binary and embedded defaults. |
| 3 | `locutus import <source>` | Admit | Bring a new feature/bug into the spec, gated by GOALS.md triage. |
| 4 | `locutus refine <node>` | Deliberate | Council-driven deliberation on any spec node (Goal, Feature, Strategy, Decision, Approach, Bug). |
| 5 | `locutus assimilate` | Infer | Infer or update the spec from an existing codebase (was `analyze`). |
| 6 | `locutus adopt` | Execute | Run the DJ-068 reconcile loop — bring code into alignment with the current spec. |
| 7 | `locutus status` | Observe | Snapshot of current state, drift, and validation errors. |
| 8 | `locutus history` | Recall | Query the historian's past-tense record (events, alternatives, narrative). |

**`--version` is a global flag** rather than a subcommand. `locutus mcp` remains the transport entry point for MCP clients; `locutus mcp-perm-bridge` remains a hidden internal subprocess.

**Deleted from the surface:** `version` (→ flag), `check` (→ gated inside `adopt`), `diff` (→ `refine --dry-run`), `regen` (→ `adopt`), `revisit` (→ `refine`, widened to all node kinds), `triage` (→ `import` admission gate), `analyze` (renamed to `assimilate`).

**Principle: one verb per lifecycle phase.** The old surface was organised around implementation deliverables (one subcommand per tier). The new surface is organised around user intent. A user never wants to "diff" in isolation — they want to preview the impact of a refinement, which is `refine --dry-run`. A user never wants to "triage" in isolation — they want to import something, and triage is the gate the admission passes through. Collapsing the verbs exposes the actual workflow.

**Every mutating verb supports `--dry-run`.** Consistent across `import`, `refine`, `assimilate`, `adopt`. Read-only verbs (`status`, `history`) do not. The dry-run implementation uses a `readOnlyFS` wrapper ([cmd/readonly_fs.go](../cmd/readonly_fs.go)) that drops writes while still serving reads — the pipeline runs to completion and the report reflects what would have been written.

**Why consolidate now rather than after more features land:**
The 13-command surface had already begun to produce rot: stubbed CLI handlers (`check`, `import` CLI), "LLM not configured" errors on four commands even though Genkit was wired end-to-end, duplicated logic between the CLI and MCP tool registrations, and a stale `docs/IMPLEMENTATION_PLAN.md` pointer in CLAUDE.md. Every new feature had to pick between fitting into the old shape or breaking from it. Consolidating before the reconciler matures means the reconciler is built against the final shape, not reworked into it later.

**Alternatives considered:**

- **Leave the 13 commands, add `adopt` alongside.** Rejected. The old commands weren't all real — four were LLM-blocked stubs, two were pure aliases-in-waiting. Shipping `adopt` into a crowded surface where half the verbs don't work would have been misleading to users and confusing to the next Claude session.
- **Keep compatibility aliases indefinitely.** Considered and rejected. Aliases were used during the multi-phase transition but deleted in Phase D the same session the consolidation landed. The project has exactly one user (the author); leaving aliases in place would have produced rot rather than reduced risk.
- **Expose `diff` and `check` as top-level verbs alongside the 8-verb set.** Rejected. `diff` is a dry-run of `refine` — its blast-radius output is exactly the cascade preview that `refine <id> --dry-run` emits. `check` is a pre-condition gate that only matters when `adopt` is about to dispatch — surfacing it standalone added a concept that didn't correspond to a user decision.

**Implementation in four phases (all landed 2026-04-21):**

- **Phase A — wiring + renames + history.** Real Genkit LLM construction via `cmd/llm.go::getLLM()` (lazy, env-var-driven); `analyze` → `assimilate`, `revisit` → `refine` (generalised to dispatch on node kind via `resolveNodeKind`); new `history` command with narrative/alternatives/events queries; `--version` flag replaces the `version` subcommand.
- **Phase B — fold-ins + dry-run.** `triage` folded into `import` as the admission gate; `diff` folded into `refine --dry-run`; `--dry-run` added on all mutating verbs; `check.CheckPrereqs` factored so it can be consumed by both the CLI and `adopt` without duplication.
- **Phase C — minimum viable `adopt` (DJ-068 partial implementation).** `spec.ComputeSpecHash` / `ComputeArtifactHashes` (with a `ReadFunc` indirection to avoid a spec→specio import cycle); `internal/reconcile` package implementing the five DJ-068 classification branches (`unplanned`, `drifted`, `out_of_spec`, `live`, preserved-prior-terminal); `SpecGraph.ApproachesUnder` for scope filtering; `adopt` command that classifies, runs the prereq gate, persists `planned` status, and surfaces `out_of_spec` drift with non-zero exit codes.
- **Phase D — cleanup.** Aliases removed, legacy commands deleted, duplicate MCP tool registrations dropped, `CLAUDE.md` rewritten to drop the stale `IMPLEMENTATION_PLAN.md` pointer and name the canonical surface.

**Deferred from Phase C (next rounds):**

- **Cascade write-back (DJ-069).** When a Decision is revised, parent Feature/Strategy present-tense statements need to be rewritten and child Approaches marked `drifted`. Today the classifier detects the drift via `spec_hash` diff, but no code actually rewrites parents or mutates status — that requires a dedicated rewriter agent prompt and remains TODO.
- **Pre-flight clarification (DJ-071).** The `pre_flight` enum value exists and the status machine recognises it, but the clarify-only agent call, ambiguity resolution, and `assumed`-Decision creation are not wired. `adopt` currently transitions drifted Approaches directly from `drifted` → `planned`.
- **Actual agent dispatch inside `adopt`.** Phase C wires classification and plan generation but stops short of invoking the dispatcher. The dispatcher package is fully built; connecting it to `adopt`'s planned workstreams is the next round of work.

The current `adopt` is honest about this scope — it classifies, gates on prereqs, persists planning intent, and reports what would happen. It does not claim to reconcile end-to-end yet. Future DJs (or a follow-up DJ-073/074) will cover the remaining rounds of Phase C.

**Phase plans live at `.claude/plans/verb-set-phase-{a,b,c,d}.md`** for reference on the detailed implementation choices in each phase.

**Impact on DJ-068 and DJ-069:**
Both DJs remain authoritative on the design of the state store, node types, cascade, and pre-flight. Phase C's minimum viable `adopt` is the first implementation increment against them — it validates the schema and classification model but leaves the cascade/pre-flight mechanics for later. Neither DJ is superseded.
