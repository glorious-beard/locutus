# Decision Journal

This document captures the series of architectural decisions and pivots that shaped the Locutus implementation plan. Each entry records what was decided, what alternatives were considered, and why the final choice was made. This is the "historian for the historian" — a record of how Locutus itself was designed.

## Status legend

Every DJ carries a **Status:** line immediately after its heading. A DJ's status is what distinguishes "we've decided to do this" from "this is observable in code today." When citing a DJ, always read the status first.

- **shipped** — code matches the decision. Safe to rely on as current behavior.
- **shipping** — partially implemented. Some aspects of the DJ are live; others are gaps. The DJ body (or a linked note) should describe what's in vs. out. Citing a shipping DJ requires naming which part you rely on.
- **settled** — design agreed, no code yet. The DJ is a commitment, not a fact. Citing a settled DJ must flag that it isn't yet observable.
- **superseded by DJ-N** — a later decision replaced this one. Read DJ-N for current direction; keep the original entry for historical context.

Backfilled on 2026-04-23 after an audit surfaced a recurring "we keep discovering designed but unimplemented features" pattern — DJs were being read as state when they were really direction.

Session date: 2026-04-13 to 2026-04-14

## DJ-001: CLI Framework

**Status:** shipped

**Decision:** Use `alecthomas/kong` instead of `spf13/cobra`.

**Alternatives considered:**
- Cobra — industry standard, but more boilerplate
- urfave/cli — simpler API
- No framework (stdlib `flag`)

**Why Kong:** User preference. Kong's struct-based command definitions are a cleaner fit for the `--json` and `--verbose` flag pattern, where global flags live on a parent struct and are naturally inherited by subcommands.

## DJ-002: Console Output Library

**Status:** shipped

**Decision:** Use `pterm` for rich terminal output.

**Alternatives considered:**
- `text/tabwriter` (stdlib) — too basic for the UX we want
- `charmbracelet/lipgloss` + `bubbletea` — better for full TUI apps, overkill for CLI output
- Custom rendering

**Why pterm:** Closest Go equivalent to Python's "rich" library. Tables, spinners, progress bars, tree views, colored text — all without building a full TUI. The user explicitly asked for rich console output comparable to Python's ecosystem.

## DJ-003: LLM Access — The Claude CLI Pivot and Reversal

**Status:** shipped

**Decision:** Use Genkit Go with Anthropic API keys.

**Journey:**
1. **Initial approach:** Shell out to `claude -p --output-format json` to use the user's Claude Max subscription (no API token costs). Research confirmed this was the officially supported way to use Max programmatically.
2. **Problem discovered:** Shelling out to `claude` CLI sacrifices control over conversation flow. No good way to forward feedback requests from the `claude` process back to the user through Locutus. Too much complexity for a cost optimization.
3. **Pivot back to API:** A separate conversation confirmed that `claude -p` was "a cost optimization that was going to compromise the architecture." Building a thin tool loop against the Messages API is a better fit.
4. **Framework selection:** Chose Genkit Go over rolling our own or using Eino (ByteDance). Genkit provides config-over-code model selection — swap providers by changing a string, no recompile.

**Key finding:** The Anthropic Go SDK cannot authenticate with Claude Max subscription — it requires API keys. Anthropic banned OAuth tokens from third-party SDKs in Feb 2026. This was a significant factor in the initial push toward `claude` CLI, and its resolution.

## DJ-004: MCP Transport

**Status:** shipped

**Decision:** Stdio-first, optional HTTP.

**Research finding:** VS Code only supports stdio for MCP servers. Claude Code supports both stdio and HTTP. Stdio is the common denominator.

**Pattern:** `locutus mcp` starts stdio MCP server (spawned by client). `locutus mcp --http :8080` for remote/multi-client scenarios later.

## DJ-005: No Archetype Selection at Init

**Status:** shipped

**Decision:** `locutus init` creates a bare spec structure with no stack assumptions. No archetype enum.

**Journey:**
1. Initially planned opinionated defaults from PLAN.md D-008 (Go + TanStack + Connect RPC)
2. Then planned to generate a full Hello World SaaS app at init
3. User questioned whether we were biased toward traditional SaaS monoliths — what about CLIs, microservices, daemons, libraries?
4. Realized that archetypes should emerge organically from the user's first prompt (greenfield) or codebase analysis (brownfield)
5. Also realized that "asking for archetype" at init was wrong — brownfield should discover it automatically

**Why no archetype:** The "archetype" is just the emergent combination of active skills and strategies. There's no enum to select because the possibilities are unbounded.

## DJ-006: Skills Over Templates

**Status:** shipped

**Decision:** No template engine. Use SKILL.md files to guide LLM generation.

**Context:** The user is the author of `stamp` (github.com/glorious-beard/stamp), an MCP-based template rendering tool. They stopped development because well-written SKILL.md files provided equivalent DX without the template engine complexity.

**Why skills:** Templates are deterministic but rigid. Skills guide the LLM to produce correct code while allowing it to adapt to context. The skill is the expert knowledge; the LLM is the flexible executor.

## DJ-007: Everything Is a Strategy

**Status:** shipped

**Decision:** Build systems, test runners, linters, formatters, and deployment tools are all strategies — not hardcoded in Locutus.

**Implication:** Locutus never calls `go build` or `go test` directly. It reads the active strategy's `commands` map. Switching from Go to Rust or from `go build` to Bazel is a decision revisit that cascades through strategies.

**Extension — Taskfile.yml:** Generated deterministically from strategies' `commands` maps. Thin facade over real build tools. Avoids stochastic LLM generation for deterministic things like build commands.

**Extension — Strategy prerequisites:** Each strategy declares its prerequisites (tools, versions). `locutus check` is strategy-driven — adding/removing strategies changes what gets checked.

## DJ-008: Planner + Delegator, Not Coder

**Status:** shipped

**Decision:** Locutus produces execution plans for external coding agents. It does not generate application code itself.

**Journey:**
1. Initially planned Locutus as a code generator (generates code directly via LLM)
2. User noted that Claude Code, Codex, Charlie, Gemini etc. have billions in R&D behind them. Competing on code quality is a losing game.
3. Pivoted to planner model: Locutus focuses on decisions, strategies, and execution plans. External agents handle implementation.
4. Exception: spec-derived artifacts (Taskfile.yml, AGENTS.md, proto definitions) are generated directly — they're deterministic transforms, not creative coding.

**Why this works:** Locutus's unique value is architectural intelligence (decisions, strategies, history). Code generation is commodity. By delegating coding, Locutus is agent-agnostic — works with any coding agent, benefits from improvements in any of them.

## DJ-009: Autonomous Decisions During Planning

**Status:** shipped

**Decision:** Locutus makes all decisions autonomously during planning (status: `assumed`, with confidence score). No `input_needed` during planning.

**Journey:**
1. Initially planned multi-turn conversations with `input_needed` chains during planning
2. Problem: forwarding feedback from the planning LLM to the user and back is complex, especially through MCP
3. Solution: Locutus decides everything itself, documents rationale and alternatives via the historian, and the user reviews later via `locutus status` / `locutus revisit`
4. `input_needed` only occurs during explicit `revisit` — the user asked to change something, so clarifying questions are appropriate

**Why autonomous:** Simpler MCP contract. Plans are always fully resolved and self-contained. No mid-planning callbacks. Aligns with D-004 from PLAN.md (Passive Generation Model).

## DJ-010: Agent Routing and Supervision

**Status:** shipped

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

**Status:** shipped

**Decision:** Every decision/strategy change recorded as structured JSON with rationale and rejected alternatives.

**User insight:** "Git history isn't sufficient. We should have a historian agent that captures the motivations behind changes and alternatives considered."

**Value during revisit:** When revisiting a strategy, the historian surfaces previously rejected alternatives. "We considered Bazel in March but ruled it out because of team experience. Has that changed?"

**Format:** Structured JSON events (machine-queryable) + derived markdown summary (human-readable). Both in `.borg/history/`.

## DJ-012: Advisory Delegation

**Status:** shipped

**Decision:** External agents can't be forced to use Locutus. AGENTS.md provides strong guidance but is advisory only. No drift detection infrastructure.

**Context:** VS Code Copilot may not even read AGENTS.md. Claude Code generally follows it but can't be forced.

**Why no enforcement:** Building drift detection or gatekeeper infrastructure adds complexity without guarantees. Better to invest in brownfield recovery (which can reconcile spec after direct edits) than in prevention.

## DJ-013: Test-First Tier Implementation

**Status:** shipped

**Decision:** Each implementation tier starts with acceptance tests and ends with running them.

**Why:** Locutus's own supervision loop enforces test-first discipline on external agents. We should eat our own cooking. Writing tests first also forces us to define contracts before implementation, catching design issues early.

## DJ-014: Brownfield Self-Analysis

**Status:** shipped

**Decision:** Don't scaffold `.borg/` for the Locutus repo during implementation. Use brownfield analysis in a later session.

**Alternatives:** Could manually create spec files from PLAN.md now.

**Why later:** Brownfield recovery captures actual state, not planned state. Some decisions may shift during implementation. Also dogfoods the brownfield feature — if it can't recover Locutus's own architecture, we have a bug.

## DJ-015: Competitive Positioning

**Status:** shipped

**Conclusion from landscape research:** No open-source tool combines persistent decision graphs + spec-driven planning + agent supervision + historian. Closest is GitHub Spec Kit (spec-first philosophy but no decision persistence or supervision). Decision graph concept exists in theory but has no production implementation.

**User context:** Not competing with commercial offerings (Devin, Cursor). This is MIT-licensed open source. Goal is addressing what the user spends most time on when using coding agents — not building a business.

**Reference implementation:** User's Atlas shoe project (`/Users/chetan/projects/shoe`) is Locutus implemented manually: 13 specialized agents, historian, mandatory review gates, approach-auditor, dispatch protocol. Locutus automates this pattern.

## DJ-016: Execution Plan — One Strategy Per Step, Agent Self-Reports Files

**Status:** superseded by DJ-027

**Decision:** Each plan step is scoped to one strategy but can touch multiple files. The agent self-reports files modified; `git diff --name-only` is the source of truth.

**Alternatives considered:**
- a) Explicitly specify one or more files per step — too rigid; agent may need to create helpers or modify unexpected files
- b) Discover files after modification, constrained to one strategy — viable but doesn't capture agent's own understanding
- c) Agent self-reports files at end of coding cycle, constrained to one strategy — **chosen**

**Why Option C:** The real constraint is one strategy per step (preserves traceability). Within that boundary, the agent should have freedom to touch whatever files are needed. `git diff --name-only` verifies the self-report. All files in the diff map to the step's governing strategy in `traces.json`. This handles cases where agents create helper files, update go.mod, or modify files not anticipated in the plan.

**What changed:** The `PlanStep` struct no longer has `FilePath` and `Action` for a single file. Instead it has `ExpectedFiles` (guidance, not enforcement) and the supervisor uses git diff for the actual file list.

## DJ-017: Locutus Writes Tests, Not the Agent

**Status:** superseded by DJ-039

**Decision:** Acceptance tests are generated by Locutus's own LLM (Genkit), not by the coding agent being supervised.

**Why:** If the agent writes its own tests, it will write tests that pass its own implementation — defeating the purpose of test-first discipline. Locutus writes tests from the plan's acceptance criteria (which are independent of any implementation), writes them to the worktree before dispatching the agent, and the agent is told to make them pass without modifying the test files.

## DJ-018: Tier 3 Uses Synthetic Fixtures

**Status:** shipped

**Decision:** Tier 3 (Decision Graph) tests use hand-crafted spec files as test fixtures, not data from the planner.

**Why:** Tier 3 is pure graph algorithms with no LLM dependency. The DAG construction and traversal code operates on typed structs loaded from JSON. Using synthetic fixtures keeps Tier 3 independent of Tier 4 (planner) and testable in isolation. Real data flows through the graph once Tier 4 is complete.

## DJ-019: Brownfield — Heuristic First, LLM Second

**Status:** shipped

**Decision:** Brownfield analysis uses heuristics for everything deterministically derivable from parseable file content. LLM is reserved for understanding intent, meaning, and context beyond syntax.

**The line:**
- Heuristic: file inventory, config parsing, language/framework detection from dependency files, struct/type parsing, import graphs, FK detection from naming conventions
- LLM: architectural intent (monolith vs microservices), rationale recovery, cross-cutting concerns (auth patterns, error handling), entity significance, feature recovery

**Cost optimization:** LLM calls are batched (2-3 total), not one per decision. This keeps brownfield analysis fast and affordable.

## DJ-020: Retry Uses Session Resume, Not Cold Start

**Status:** shipped

**Decision:** When the supervisor retries a failed agent step, it resumes the agent's existing session (`claude -p --resume <session-id>`) rather than starting a fresh conversation.

**Why:** Session resume gives the agent full context of what it tried and what failed. This is far more token-efficient and produces better results than cold-starting with "here's the task again plus what went wrong." The session ID is controlled by Locutus, not the agent.

## DJ-021: Genkit Go — LLM Plumbing Only, Not Agent Orchestration

**Status:** shipped

**Decision:** Use Genkit Go strictly for LLM access (multi-provider Generate, tool registration, structured output). All agent orchestration, definition loading, and persistence is built by Locutus.

**Research finding:** Genkit Go cannot read agent definition files (AGENTS.md, SKILL.md) or memory files. It has no native agent support — agents are built manually using flows and tool definitions. The JS/TS version is significantly more mature for agent development, but we're in Go. Genkit Go's session system is in-memory only with no file-based persistence.

**What Genkit Go gives us:** Multi-provider model selection by config string, `ai.Generate()` with structured output, tool registration, system prompts, conversation history management.

**What Locutus builds on top:** SKILL.md loading and injection, agent registry and routing, supervision loop, historian, brownfield analysis, memory/persistence, all file-based spec I/O.

## DJ-022: Features as Product-Level Layer Above Decisions

**Status:** shipped

**Decision:** Features sit above decisions in the spec graph: Feature → Decision → Strategy → Source Files. Decisions can be feature-driven or standalone (foundational/project-wide). Same for strategies.

**Why:** Features are the product spec — what the user actually cares about. "User authentication" is a feature. "JWT vs sessions" is a decision driven by that feature. Without features at the top, decisions float without product-level motivation. Features also carry acceptance criteria that flow down into plan step assertions, giving the supervisor concrete product-level success criteria.

**What changed:**
- Feature type gets `acceptance_criteria []string` and `decisions []string` (IDs it drives)
- Planning pipeline starts with features when user describes product-level intent, decisions when describing implementation-level intent
- Blast radius now traverses Feature → Decision → Strategy → Files (more powerful)
- Historian records feature-level context ("this decision exists because of the auth feature")

**Standalone decisions:** "Use Go" is a project-wide foundational decision not tied to any feature. These exist at the decision level with no parent feature. The graph allows orphan decisions and strategies.

## DJ-023: Agent File Generation Strategy

**Status:** shipped

**Decision:** Locutus generates CLAUDE.md as the primary agent instruction file and symlinks AGENTS.md to it. SKILL.md files (open standard, agentskills.io) are generated per-strategy in `.agents/skills/` and referenced from CLAUDE.md.

**Research finding:** AGENTS.md is a Linux Foundation standard (60k+ repos) but Claude Code doesn't read it natively (feature request #6235). Claude Code reads CLAUDE.md and its own `.claude/` ecosystem. SKILL.md is an open standard supported by all major tools (Claude, Codex, Copilot, Cursor, Gemini).

**What we DON'T generate:** `.claude/agents/` definitions and `.claude/memory/` files. These are Claude Code internals for its own sub-agent orchestration and session recall. They're orthogonal to Locutus's spec management.

## DJ-024: Full Scope Validated — Supervision Is Not Incremental

**Status:** shipped

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

**Status:** shipping

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

**Status:** shipped

**Decision:** The historian has two layers. Layer 1 (deterministic): records structured JSON events (what changed, old/new values, alternatives). Layer 2 (LLM): writes a compelling human-readable narrative connecting decisions to the broader project arc.

**Why LLM for narrative:** Structured JSON events are queryable but not useful to a human reader. The shoe project's LOG.md reads as a story ("After five days attempting CT-scan-derived sock maps, the domain translator identified that the hosiery industry has standard pattern templates..."). That narrative quality — highlighting what's surprising, noting reversals, providing context — requires an LLM. A mechanical event log would never produce that.

**The two layers complement each other:** JSON events are the source of truth for blast radius, revisit queries, and machine consumption. The narrative summary in `.borg/history/summary.md` is a derived artifact for human reading — a rich project history that explains not just what happened but why it matters.

**Implementation notes (landed 2026-04-23 as Round 2 of the gap-closeout plan):**

- **Manifest + detail layout.** The narrative isn't a single monolithic file. `.borg/history/summary.md` is a manifest — timeline + index of targets with deeper history — and `.borg/history/details/<target-id>.md` hold the per-target narrative depth for any target with ≥ N events (configurable; default N=2). The manifest is cheap to regenerate and scannable in 30 seconds; the detail files are where the motivational narrative lives.

- **Two agents per DJ-036.** The manifest is written by the **archivist** (`internal/scaffold/agents/archivist.md`, fast tier) — terse, faithful, non-interpretive. The detail files are written by the **analyst** (`internal/scaffold/agents/analyst.md`, balanced tier) — causal reasoning, motivation analysis, honest when the record is sparse. The split matches the cost/value curve: fast-tier for the 80% case that's just structural index updates; balanced-tier for the 20% that needs narrative depth.

- **Debounce via embedded hash.** Both layers carry a `<!-- locutus-narrative-hash: ... -->` comment in their body. On re-run, `GenerateNarrative` recomputes the hash over the current event set (or per-target subset) and skips the LLM if it matches. `--force` bypasses. Per-target debounce is independent of the manifest-level debounce — a target whose events haven't changed isn't re-analysed even when other targets churn.

- **User-controlled scoping.** `locutus history --regenerate-narrative` is the trigger. `--since YYYY-MM-DD` and `--until YYYY-MM-DD` narrow the event window. The debounce hash is computed over the scoped set, so the same window produces the same skip-or-regen decision on repeat.

- **Package decoupling.** `internal/history` does not import `internal/agent` (the inverse dependency already exists for event records), so the LLM contract in `history` is a narrow `GenerateFn func(ctx, userPrompt) (string, error)`. The system-prompt identity of each agent is the caller's concern — cmd-layer closes over the loaded agent definition and passes the wrapped closure in. Helper at `agent.NamedAgentFn(fsys, llm, agentID)` loads the def and returns the callback; its shared home in the agent package is explicitly so `internal/cascade` and `internal/preflight` (which today hard-code system prompts against their own agent files) can adopt the same pattern in a future cleanup.

**Follow-up (not done in Round 2):** `internal/cascade/cascade.go` and `internal/preflight/preflight.go` still inline their system prompts instead of loading `rewriter.md` / `preflight.md` via `agent.NamedAgentFn`. The agent def files exist and carry the correct personas; the two packages just need the same wrapper call `cmd/history.go` uses now. Tracked as a refactor, not a DJ — the designed behaviour is unchanged.

## DJ-027: Hierarchical Plans (Plan of Plans) with Two-Level DAG

**Status:** shipped

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

**Status:** shipping

**Decision:** Plan readiness is determined by a collaborative gate: the convergence monitor triggers the check (mechanical: stable + complete), then the critic and stakeholder each do a final sign-off. Both must approve. No dedicated plan reviewer agent needed.

**Why collaborative, not a single reviewer:**
- The convergence monitor detects "council stopped debating" — necessary but not sufficient. A plan can stabilize and still be too vague.
- The critic evaluates technical soundness: "Are there gaps? Is this over-engineered?"
- The stakeholder evaluates user alignment: "Does this serve the user's goals? Is the scope proportional?"
- Both perspectives are needed. A technically perfect plan that doesn't serve the user is worthless. A user-aligned plan that's technically flawed will fail in execution.

**Why not a dedicated reviewer:** The critic and stakeholder already have the context from participating in the council rounds. A fresh-eyes reviewer would need to re-read everything, adding cost without proportional benefit. The collaborative gate reuses existing roles in a new capacity.

## DJ-029: Genkit Go + Custom Orchestration, Not LangGraphGo

**Status:** shipped

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

**Status:** shipped (plan-time prevention; rebase fallback deferred)

**Decision:** The critic flags file overlaps between parallel workstreams during planning. The planner restructures to eliminate them (merge workstreams, add dependency edges, or extract shared files into a dedicated workstream). If unanticipated overlaps occur at runtime (agent touches files not in ExpectedFiles), fall back to sequential rebase with conflict resolution.

**Why plan-time prevention over runtime merge:** Merge conflicts during agent execution are expensive — the agent may need to re-run steps, and automated conflict resolution is unreliable. Preventing overlaps at plan time is cheaper and more predictable. The rebase fallback handles edge cases where agents touch unexpected files.

**Implementation (Round 6, 2026-04-25):** [`internal/overlap/`](../internal/overlap/) ships a layout-agnostic Go detector — `overlap.Detect(plan, approachesByID)` returns inter-workstream conflicts as `Report{WorkstreamA, WorkstreamB, SharedFiles}`. The "files this workstream touches" set is the union of every step's `ExpectedFiles` and the referenced Approach's `ArtifactPaths`, so a planner that populates either field has its declared writes counted. Sequential workstreams (connected by transitive `DependsOn`) are exempt; intra-workstream sharing is allowed. `cmd/adopt.go:planWithOverlapRetry` wraps `cfg.Plan` in a retry loop: on overlap detection, the next call sees an "Overlap conflicts" prompt section listing conflicts and naming the two valid resolutions (merge the workstreams or add a `depends_on` edge). After 3 retries with persistent overlap, adopt errors out with the conflict surfaced.

**Detection layer choice:** despite the DJ's "critic flags" wording, the detector is Go code, not an LLM critic. File-set intersection is mechanical and deterministic; the qualitative call (how to resolve) is delegated to the planner on retry, which is already an LLM. No critic agent step.

**Rebase fallback:** explicitly out of scope for Round 6. Plan-time prevention is the priority per the DJ; runtime rebase is a separate future concern.

**Known followup:** the synthesizer agent (Round 3) and planner currently allocate file sets without bias toward agent-shaped granularity. Operating evidence may show that smaller, agent-shaped files reduce overlap-retry pressure. A separate DJ should consider whether the synthesizer prompt and the planner's `ArtifactPaths` allocation should prefer finer-grained file boundaries — and whether brownfield assimilation should *split* existing files toward that target as part of remediation. Tracked in [`.claude/plans/gap-closeout.md`](../.claude/plans/gap-closeout.md) Round 6 status.

## DJ-031: Concurrency Scheduler with Configurable Resource Limits

**Status:** shipping

**Decision:** A concurrency scheduler separates what CAN run in parallel (DAG topology) from what WILL run in parallel (resource availability). Configurable limits per-agent and globally.

**Why:** The DAG says "4 workstreams can run in parallel" but Claude Max might only support 2 concurrent sessions. Codex might have its own limits. The user's machine might not handle 5 worktrees. The scheduler is a standard job-queue pattern (ready queue → running slots → blocked) with configurable limits that the user sets based on their subscription and hardware.

## DJ-032: Commit-Per-Workstream on a Local Feature Branch (Reframed 2026-04-25)

**Status:** shipped (2026-04-25 reframe)

**Decision:** Each workstream produces commits on a scratch branch (`locutus-wt/<workstream-id>`), then merges into a local feature branch (`locutus/<workstream-id>`) — no PR objects, no automated review hop, no remote push. The human reviews the accumulated local state when work is done (git log, diff, run the app) and pushes when satisfied.

**Why no PR objects:** Locutus is a local, opinionated tool today. PRs are a team-facing artifact — they exist for review by other humans on a remote-hosted forge (GitHub, GitLab, Gitea). Locutus has only one operator. Creating PRs that no one will look at is waste, and an automated "Locutus reviews its own PR before merging" hop is just code obscuring what's already a pure local merge. The cleanest abstraction is what the dispatcher already does: commit, merge to feature branch, move on.

**Why not auto-push:** Pushing to remote is irreversible. The user needs a review point before changes leave the machine. Local auto-merge gives Locutus full autonomy during execution while keeping the user in control of what becomes visible elsewhere.

**The model (as implemented):**

- `dispatch.runWorkstream` creates a worktree on `locutus-wt/<ws-id>`, runs the agent, commits the result, then `MergeToFeatureBranch("locutus/<ws-id>")`.
- Multiple workstreams in a plan each produce their own feature branch.
- Work flows continuously — no per-workstream human halts.
- User reviews `git log`, `git diff`, runs the app, decides what to push.
- User pushes when satisfied, or resets / cherry-picks / amends when not.

**Reframe note (2026-04-25):** the original DJ called this "PR-Per-Workstream" with an implied auto-review hop. That language was aspirational and never reached implementation — the dispatcher always did local commit-and-merge. The new framing aligns vocabulary with reality. The team-facing pivot (real PR creation, automated reviewer agent against the PR diff, structural test-first enforcement at plan time) is **deferred** — see DJ-038, DJ-040, and the gap-closeout plan's Round 8 deferral.

**Scope of "shipped":** the local commit-and-merge mechanism. Quality gates that should fire on the merged feature branch (spec alignment vs. `traces.json`, no-stubs check, interface-contract satisfaction) are tracked separately and run in the verify phase of `adopt`, not as a PR review. If/when Locutus pivots team-facing, those checks become inputs to the PR-level reviewer.

## DJ-033: Features Are Human-Initiated, Council-Enriched

**Status:** shipped

**Decision:** The human writes the feature spec (any level of detail). The council enriches it with acceptance criteria, edge cases, entity links, and technical considerations. The human reviews the enriched spec before it drives decisions.

**Why not human-only:** A one-liner prompt ("add auth") should be enough to kick off work. The council can flesh out acceptance criteria and edge cases that the human might not think of. But the human always writes the initial intent.

**Why not LLM-generated:** Features can include rich artifacts — Figma mockups, screenshots, user stories from customer research — that an LLM can't produce. The `.md` body is the human's space (prose, links, images). The `.json` sidecar is Locutus's space (structured acceptance criteria, entity refs, decision links).

**The enrichment flow:** Human writes feature → planner adds acceptance criteria and edge cases → stakeholder validates it represents user intent → critic checks for gaps → human reviews enriched spec → spec drives decisions and strategies.

## DJ-034: Quality Strategies for Best Practice Enforcement

**Status:** shipping

**Decision:** Best practices are modeled as a new strategy kind (`quality`, alongside `foundational` and `derived`). Quality strategies are cross-cutting — applied to ALL workstreams by the supervisor, not just one. They carry machine-verifiable assertions (linters, duplication detectors, grep patterns) that the supervisor enforces regardless of whether the agent "remembered" the instruction.

**Why not rely on skills alone:** Claude Code (and other agents) demonstrably forget or ignore instructions as context grows — even with a 1M token window. Skills loaded into agent context are best-effort guidance. Quality strategies with machine-verifiable assertions are enforcement — the supervisor checks after the agent finishes, and fails the step if violations are found.

**The two-layer model:**
- **Skill (tell):** SKILL.md says "always use the `<Button>` component from our design system, never raw `<button>`". The agent will usually follow this. Best effort.
- **Quality strategy (verify):** Assertion `not_contains` on .tsx files for `<button`. The supervisor catches violations the agent missed. Enforcement.

**Examples:** DRY enforcement (duplication detector), component library usage (grep for raw elements), naming conventions (linter rules), import restrictions (grep for forbidden paths), test coverage thresholds, no console.log in production code, max function length.

**Four-tier assertion model:** Per-step (functional) → per-workstream (domain integration) → quality strategies (cross-cutting best practices) → global (whole project).

## DJ-035: LLM-Based Assertions Alongside Deterministic Checks

**Status:** settled

**Decision:** Assertions can be either deterministic (`test_pass`, `contains`, `compiles`, `lint_clean`, etc.) or LLM-based (`llm_review`). Deterministic assertions run first (fast, cheap). LLM review assertions run last (slower, costlier, but catch semantic issues).

**Why not deterministic-only:** Some quality checks require judgment that regex and linters can't provide: "Does this code follow the separation of concerns in the architecture strategy?", "Is the error handling consistent with patterns elsewhere?", "Does this UI match the visual language of the design system?" These are real concerns that agents routinely get wrong, and no heuristic can catch them.

**The `llm_review` assertion:** Carries a `Prompt` field with the specific review question. The supervisor sends the changed files (or diff) plus the prompt to an LLM and evaluates the response. This is a separate LLM call from the coding agent — an independent reviewer, not the agent reviewing its own work.

**Cost management:** Deterministic assertions short-circuit — if they fail, LLM reviews don't run (fix the cheap failures first). LLM reviews only run on passing code, keeping cost proportional to quality.

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

## DJ-037: Convergence Monitor Uses LLM, Not Just Deterministic Checks

**Status:** shipping

**Decision:** The convergence monitor is an LLM call using a cheap/fast model (Haiku-class), not purely deterministic code.

**Why LLM:** Deterministic convergence checks ("did the concerns list change?") can't distinguish between:
- Same concern raised three rounds in a row but planner's response evolved each time → progress, not cycling
- Two new concerns raised but they're minor refinements → plan is substantively ready
- Stakeholder approved but with low confidence → worth one more round

An LLM (even a cheap one) can make these nuanced judgments using its own criteria alongside the other agents' feedback. The cost is minimal — Haiku-class models are fast and cheap.

**What changed:** Convergence monitor moves from deterministic code to an LLM agent with its own definition file in `.borg/council/agents/`. Still configurable — user can set the model, adjust the convergence criteria. Round budget updated: 5-6 LLM calls per round (was 4-5).

## DJ-038: On-Demand Specialist Agents for Plan Fleshing-Out

**Status:** settled (deferred 2026-04-25 pending team-facing decision)

**Deferral note:** the specialist-agent layer is most valuable when Locutus is producing PRs for human review (test-architect proves tests cover the criteria; UI-designer / schema-designer flesh out detail before review). Locutus's current posture is local commit-per-workstream (DJ-032 reframed) with one operator. Specialists are overhead in that posture — the agent already writes tests (DJ-039) and the human reviews the merged feature branch directly. Reopen this DJ when/if Locutus pivots team-facing.

**Decision:** Implementation details (executable acceptance tests, UI descriptions, schema designs) are handled by on-demand specialist agents, not the core planner. Specialists are invoked after the core council converges on structure.

**Specialists:** Test architect (Playwright scripts, Go test skeletons), UI designer (component descriptions from feature specs), schema designer (migrations, proto definitions, API contracts). Users can add custom specialists (security reviewer, accessibility auditor, i18n specialist).

**Why not the planner:** The planner proposes architecture ("we need an auth service"). Writing a Playwright script or describing a UI component tree is a different skill. Overloading the planner degrades both its architectural reasoning and its implementation detail quality. Specialists can also use domain-specific models or prompts optimized for their task.

**How they fit:** Core council rounds converge on structure → readiness gate passes → specialist agents flesh out implementation details (1-3 additional LLM calls) → master plan is complete with both architecture and executable detail.

## DJ-039: Agent Writes Tests, Plan Specifies Criteria (Reverses DJ-017)

**Status:** shipped

**Decision:** The coding agent writes both implementation AND tests. The plan specifies acceptance criteria (WHAT to test, pass/fail conditions). The supervisor validates that tests actually cover the criteria via `llm_review` assertion.

**Reverses DJ-017** ("Locutus writes tests, not the agent") because:
- Dictating test code is the same over-prescription problem as over-detailed plans
- The agent knows the codebase — it can augment existing test files, reuse test helpers, choose appropriate fixtures
- "Plan specifies WHAT, agent decides HOW" should apply to tests just as much as implementation

**Risk mitigation:** The original concern (agent writes tests that pass its own broken implementation) is mitigated by the `llm_review` assertion: "Do these tests actually cover the acceptance criteria specified in the plan?" This is an independent LLM review, not the agent reviewing its own work. Combined with coverage thresholds and deterministic checks, this catches self-serving tests without Locutus having to write them.

## DJ-040: Test-First Workstream Pattern as a Quality Strategy

**Status:** settled (deferred 2026-04-25 pending team-facing decision)

**Deferral note:** the test-first **structural gate** (a hard plan-time enforcement that every workstream's first step has an assertion describing a failing test) is most valuable when Locutus is producing PRs for human review — it makes review faster and catches "agent skipped tests" failure modes before they ship. In Locutus's current local posture (DJ-032 reframed), test-first is **practiced** (DJ-039: agent writes tests, supervisor's `llm_review` validates coverage) but not **structurally gated** at plan time. The user reviews the merged feature branch directly and can re-run with stricter assertions if a workstream skipped tests. Reopen this DJ when/if Locutus pivots team-facing — the hard gate then earns its keep.

**Decision:** Every workstream must start with defining acceptance tests and conclude with all tests passing. This is a foundational quality strategy enforced structurally by the supervisor — a hard gate, not optional guidance.

**The pattern:** Plan acceptance criteria → first step: agent defines/writes tests → middle steps: agent implements → final step: all tests pass. The supervisor won't mark a workstream as complete until the test gate passes.

**Why a quality strategy, not just an instruction:** Instructions get forgotten. A quality strategy is enforced by the supervisor on every workstream regardless of what the agent does. The test-first pattern is too important to be advisory — it's the primary mechanism for ensuring the result actually works.

## DJ-041: GOALS.md as Project Root + Issue-Driven Intake

**Status:** shipped

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

**Status:** shipped

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

**Status:** shipped

**Decision:** Add `locutus triage --input <file> --json` command that evaluates an issue against GOALS.md and outputs a structured JSON verdict (accepted/rejected/duplicate). A thin CI wrapper (GitHub Action) handles the external system interaction on both sides.

**The pattern:** CI fetches issue → pipes to `locutus triage` → reads JSON verdict → acts on external system (comment, label, close). Locutus never calls external APIs, never needs API keys.

**Why this approach:** Locutus stays local-only (DJ-042) but the triage capability is still usable in automated workflows. The CI wrapper is ~20 lines of YAML. Different platforms write their own wrappers. Locutus's structured JSON output is the universal interface — same pattern as MCP (Locutus produces structured output, something else presents/acts on it).

## DJ-044: Markdown Input for Triage/Import, Not JSON

**Status:** shipped

**Decision:** The input format for `locutus triage` and `locutus import` is markdown with YAML frontmatter, not JSON. The CI exporter (provider-specific) converts from the external system's format to markdown.

**Why markdown:** Issues are already written in markdown. Markdown carries inline images, Figma links, code blocks, discussion threads — rich content that JSON can't naturally represent. Locutus already has a frontmatter parser. The markdown body becomes the feature/bug `.md` file directly.

**The flow:** External system → provider-specific exporter → markdown+frontmatter → `locutus triage`/`locutus import` → structured JSON verdict (for triage) or local spec artifact (for import). If import is called without prior triage, it runs triage internally and rejects out-of-scope items.

## DJ-045: Brownfield Includes Gap Analysis and Autonomous Remediation

**Status:** shipped (2026-04-25)

**Decision:** After inferring the spec from existing code, brownfield runs a gap analysis (missing tests, undocumented decisions, orphan code, missing quality strategies, stale docs) and fills the gaps autonomously with `assumed` decisions and strategies. Same pattern as greenfield — no pause for user input.

**Why autonomous, not pause:** Greenfield doesn't pause to ask the user about every decision — it assumes and the user reviews later. Brownfield should be the same. The only difference is the starting point: brownfield starts with `inferred` decisions (from code), greenfield starts empty. Gap-fill decisions are `assumed` (new, not recovered from code). Both converge to the same fully managed state.

**Gap categories:** Missing tests, missing acceptance criteria, undocumented decisions (code implies a choice but no decision is recorded), orphan code (files not traced to any strategy), missing quality strategies (no linter, no CI, no coverage), stale documentation.

**Implementation (Round 5, 2026-04-25):** [`internal/remediate/`](../internal/remediate/) ships `Plan` (the remediator agent's structured output with Decisions, Strategies, Features, FeatureUpdates), `Remediate(ctx, llm, gaps, existing) → *Result`, and `ApplyToAssimilation(plan, result, existing)` which merges remediation output into the AssimilationResult before persistence so the existing DJ-075 atomic-write pass writes everything in one go. The remediate pass runs **outside** the workflow YAML — `agent.Analyze`'s `parseAssimilationResults` previously merged the workflow's `remediate` round output blindly, with no consolidation, no attachment, and no opt-out; that round was removed from [`internal/scaffold/workflows/assimilation.yaml`](../internal/scaffold/workflows/assimilation.yaml). `cmd/assimilate.go` calls `remediate.Remediate` after `agent.Analyze` returns, gated by the new `--no-remediate` opt-out flag (default ON per the autonomy posture).

## DJ-046: Hybrid Remediation — Cross-Cutting + Feature-Specific

**Status:** shipped (2026-04-25)

**Decision:** Cross-cutting gaps (missing CI, linter config, coverage thresholds) become a single consolidated "project-remediation" feature. Feature-specific gaps (missing auth tests, undocumented auth decisions) attach to their respective features.

**Why hybrid:** Pure consolidation loses the feature-level context ("these missing tests are for auth"). Pure per-feature loses the cross-cutting view ("the project has no CI at all"). Hybrid gives both: the consolidated feature handles infrastructure gaps, individual features handle their own quality gaps.

**Implementation (Round 5, 2026-04-25):** Consolidation and attachment rules live in the [`remediator` agent prompt](../internal/scaffold/agents/remediator.md), not in `internal/remediate/`. The agent is told: cross-cutting quality gaps go under one `f-project-remediation` Feature with separate Decision+Strategy pairs; feature-specific gaps emit a `FeatureUpdate{FeatureID, AddedDecisions}` against the existing Feature. The package code faithfully threads the agent's structured output into the AssimilationResult, pulling existing-spec Features into the result when a `FeatureUpdate` references one, so the persistence pass writes them back with the new Decision references.

**Cascade-skip caveat:** Round 5 ships without firing `cascade.Cascade` after remediation — the remediator writes new Decisions and updates parent Features in coordination, so the resulting prose is consistent by construction. If empirical drift emerges between remediator-authored prose and the rewriter's voice in later runs, revisit; a follow-up DJ would document the trigger.

## DJ-047: Full Build Order Rewrite — 8 Tiers

**Status:** shipped

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

**Status:** shipped

**Decision:** CLI is minimal interaction (stdin prompts for revisit, pterm spinners for progress, text output). MCP is the primary interactive interface — supported by VS Code, Claude Code, JetBrains, Cursor, Windsurf, Zed, Gemini CLI, and likely Antigravity. Headless mode via `--json` flag on every command. Rich TUI is a future feature if demanded.

**Why not rich CLI now:** MCP covers all major IDEs via stdio transport. Most users will access Locutus through whatever AI assistant their IDE provides. A rich bubbletea-based TUI would cost 500-1000 LOC, delay shipping, and serve a narrow audience (power terminal users). Start minimal, add later.

**Three modes, same core:** Every command produces structured data (MCPResponse). MCP returns JSON to the client. CLI renders via pterm. Headless outputs raw JSON. The difference is presentation only — all three share the same engine.

---

Session date: 2026-04-16 to 2026-04-17 — post-Tier-8 refinements

## DJ-049: Generic Step Executor Extraction

**Status:** shipped

**Decision:** Extract a generic `internal/executor` package that powers both the planning council workflow and workstream dispatch. Parameterized by a `State` type. Provides dependency-ordered execution, bounded parallelism via semaphores, per-type concurrency limits, snapshot isolation for parallel steps, optional convergence loop, and progress events via channel.

**Why:** The planning council DAG and the Tier 7 dispatch DAG are the same pattern with different payloads. Rather than duplicate coordination logic, extract it once and let callers provide typed state and a `RunStep` function. The planning workflow wraps it as `WorkflowExecutor[PlanningState]`; the dispatcher wraps it as `executor.Executor[dispatchState]`.

**Alternatives considered:** Keep two separate implementations (planning-specific + dispatch-specific), or adopt a larger agent-framework dependency (Eino, CrewAI-equivalent). Rejected the first as duplication. Rejected the second as overkill — the primitive is ~200 lines of Go with generics.

## DJ-050: brownfield → assimilation Rename

**Status:** shipped

**Decision:** Rename "brownfield" to "assimilation" throughout the codebase: package names, types (`BrownfieldRequest` → `AssimilationRequest`), enum values (`PlanActionBrownfield` → `PlanActionAssimilation`), comments, and agent definitions.

**Why:** "Brownfield" is enterprise jargon that doesn't fit the Borg theme. "Assimilation" matches the project's naming convention and is more descriptive of what the pipeline actually does — it absorbs an existing codebase into the spec graph.

## DJ-051: Flat Scaffold Layout

**Status:** shipped

**Decision:** Scaffold structure is flat: `internal/scaffold/agents/` holds all 15 agent definitions; `internal/scaffold/workflows/` holds `planning.yaml` and `assimilation.yaml`. On disk after `locutus init`: `.borg/agents/` and `.borg/workflows/`.

**Why:** The earlier nested hierarchy (`council/agents/`, `council/brownfield/agents/`, `council/supervision/agents/`) was organizational overhead with no functional benefit. Agents are loaded the same way regardless of category; workflows reference agents by ID. The flat layout is simpler, cleaner, and easier to navigate.

**Alternatives considered:** Keep nesting by category (planning/assimilation/supervision). Rejected because agent IDs are unique across categories and the loader doesn't care which subdirectory they came from.

## DJ-052: Agent Definitions Are the Prompt Source of Truth

**Status:** shipped

**Decision:** Each agent `.md` file contains the full prompt: identity, context, task, output format, quality criteria, and anti-patterns. Go code in `projection.go`, `convergence.go`, and `supervisor.go` only injects dynamic context (state snapshots, event data) as user messages.

**Why:** Scattered prompt engineering across Go code is hard to iterate on, review, and version. Consolidating prompts in `.md` files makes them: editable by non-developers, diffable in PRs, isolatable for A/B testing, and loadable at runtime (users can customize per-project after `locutus init`).

**Alternatives considered:** Keep prompt fragments in Go code for compile-time safety. Rejected because prompt engineering is iterative content authoring, not programming — locking it in Go tightens feedback loops unnecessarily.

## DJ-053: Capability Tiers with Multi-Provider Resolution

**Status:** superseded by DJ-067

**Decision:** Agent frontmatter specifies `capability: fast|balanced|strong` instead of a specific model. The capability tier resolves to an actual model at `BuildGenerateRequest` time via configurable mapping. Default mapping uses Anthropic models (Haiku/Sonnet/Opus). Future: discover available providers from env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`) and map tiers to the best available model per provider, possibly via LLM-powered routing for task-specific provider selection.

**Why:** Hardcoding model names in agent defs couples content authoring to specific providers. Capability tiers let users: swap providers without rewriting agents, pay Haiku prices for bounded-judgment tasks (convergence, historian, watchdog), reserve Opus for complex architectural work.

**Implementation today:** Static tier → default Anthropic model mapping. Multi-provider LLM routing deferred to future plan.

## DJ-054: JSON Schema via Struct Tags and Registry

**Status:** shipped

**Decision:** Agent frontmatter can specify `output_schema: MasterPlan` (or other registered type name). At `BuildGenerateRequest` time, Go reflects the corresponding type and appends a JSON schema to the system prompt. Struct tags (`jsonschema:"description=..."`) provide field-level documentation. A `schemaRegistry` maps type names to example instances.

**Why:** LLMs produce more reliable structured output when given an explicit schema, and descriptions next to fields keep the schema in sync with Go code. The alternative — inlining schemas as Markdown in agent `.md` files — drifts from the Go types over time.

**Pattern:** `github.com/google/jsonschema-go` (already a transitive MCP SDK dependency) handles reflection. Equivalent to Pydantic's `Field(description="...")` in Python.

## DJ-055: Executor Uses func(any) bool for Step.Conditional, Accepting Generics Leak

**Status:** shipped

**Decision:** `executor.Step.Conditional` has signature `func(state any) bool` even though the executor is generic on `State`. Callers type-assert `state.(*MyState)` in their closures.

**Why:** `Step` is not generic — making it so would require `Step[S]` everywhere and significantly complicate the API. The `any`-typed conditional is a pragmatic leak of the generic contract. Callers handle it with a small type assertion at the closure boundary. Accepted as a Go generics limitation rather than a design flaw.

**Alternatives considered:** Make `Step` generic (too invasive), use an interface with a type parameter (awkward), remove conditional from `Step` (would push conditionality into `RunStep` itself, losing the optimization of skipping before resource allocation).

## DJ-056: Fast-Tier LLM Monitor Replaces Go Heuristic Watchdog

**Status:** shipped

**Decision:** For fuzzy supervision decisions (churn detection, scope drift, stalled progress, invented requirements), use a fast-tier LLM ("Haiku-class") invoked periodically over a sliding event window. Go code handles only mechanical bookkeeping: ring buffer of recent events, cooldown clock between invocations, circuit breaker for repeated errors. No pattern-detection heuristics in Go.

**Why:** Heuristics for "what counts as churn" would always chase edge cases. Coding agents evolve, emit new event patterns, interleave legitimate retries with actual cycles. An LLM observes the pattern in context and adapts without code changes. Tuning happens in the `monitor.md` agent's prompt, not in Go. Cost is bounded by a cooldown (≥10 events between invocations) and a cheap model tier.

**Alternatives considered:** Pure Go heuristics (fragile, high maintenance). Pure LLM on every event (prohibitive cost). Tiered — Go watchdog triggers LLM judgment (still has the heuristic fragility problem). Picked pure periodic LLM because it shifts all judgment to the prompt, which is the right surface for this kind of decision.

## DJ-057: Permission/Question Routing via Tool-Name Registry, Not Heuristics

**Status:** superseded by DJ-062

**Decision:** `EventPermissionRequest` and `EventClarifyQuestion` are identified by matching the event's tool name against a per-driver registry:

- Permission tool: the tool name we registered via `claude -p --permission-prompt-tool <name>`. Because we configured the name, match is definitional, not inferred.
- Question tool: the provider's documented SDK tool name (e.g., Claude's `AskUserQuestion`).

If a driver doesn't support either mechanism, those events simply don't fire for that provider — acknowledged limitation, not papered over.

**Why:** Heuristic detection ("is this a Bash command that looks dangerous?") would be fragile and lead to false positives/negatives. The tool-name match is structural because the identification is either by configuration we chose or by documented provider convention.

## DJ-058: Churn and Retry Are Distinct

**Status:** shipped

**Decision:** Churn and retry are separate supervision phenomena:

- **Retry** is vertical — a new attempt after a failure signal (validation rejected, test failed, timeout). Lives in the outer `Supervisor.Supervise` loop.
- **Churn** is horizontal — repeating action cycles within a single attempt detected from the event stream. Causes the current attempt to abort early to save tokens.

Churn-aborted attempts feed the retry loop with pattern-specific feedback. Two consecutive churn-aborts on the same step escalate to `RefineStep` because the step itself is likely the problem.

**Why:** Conflating them leads to wrong responses. A step that churns and then recovers (cycle, then validation failure normally) isn't the same as a step that consistently cycles. The distinct counter (`consecutiveChurns`) separates these modes cleanly.

## DJ-059: Streaming Supervision Deferred to Follow-Up Plan

**Status:** superseded by DJ-061

**Decision:** Supervisor currently runs coding agents in batch mode (`CommandRunner` returns `[]byte`). Streaming supervision — NDJSON event loop with mid-attempt churn detection, permission/question routing, MCP progress forwarding — is captured in `.claude/plans/streaming-supervision.md` for execution in a future session.

**Why:** The current batch supervisor works for all existing tests and the 8 tiers as originally specified. Streaming requires ~10 new files, touches every driver, and significantly expands the supervision surface. Better to keep it as a coherent follow-up plan than jam it into the already-large tier sequence.

**Plan scope includes:** normalized `AgentEvent`, pull-based stream parser per driver, sliding-window LLM monitor, permission/question tool-name registry, MCP progress notifications, heartbeat and size-bomb timeouts for mid-stream detection (belt-and-suspenders with reassembly-based monitor), context-cancellation propagation to kill forked processes.

## DJ-060: Dispatcher Uses Executor, Steps Within a Workstream Use a For-Loop

**Status:** shipped

**Decision:** The outer workstream DAG uses `executor.Executor[dispatchState]` for dependency ordering, parallel execution, and per-agent concurrency limits. The inner step iteration within a single workstream is a plain `for` loop in `Dispatcher.runWorkstream`.

**Why:** Workstream-level parallelism makes sense (different workstreams run in different worktrees, different agent sessions). Step-level parallelism within a workstream does not — all steps share one worktree and one agent session, so parallel execution would cause git state conflicts and session state chaos. The for-loop correctly models this sequential reality.

**What this means for `PlanStep.DependsOn`:** The field exists but its job is plan-time ordering validation (making sure `Order` is consistent with declared dependencies), not runtime parallelism enforcement.

**Resisted temptation:** Nesting a second executor inside `runWorkstream` for "symmetry." Rejected as over-abstraction — the executor adds value where it eliminates duplication, not where it just looks consistent.

---

Session date: 2026-04-17 to 2026-04-18 (streaming supervision build-out)

## DJ-061: Streaming Supervision Plan Executed End-to-End (Closes DJ-059)

**Status:** shipped

**Decision:** The streaming-supervision plan deferred in DJ-059 shipped across 13 commits. Batch `CommandRunner` signature replaced with `io.ReadCloser`, supervisor's outer loop rewritten around `runAttempt`, NDJSON parser + delta reassembler for Claude Code, fast-tier LLM monitor with ring buffer + cooldown + circuit breaker, MCP permission bridge via a `locutus mcp-perm-bridge` subcommand + Unix socket, MCP progress forwarding through a session-wrapped notifier.

**What actually shipped vs what the plan specified:** All 9 parts closed with real assertions and no `t.Skip`. 61 new tests, entire repo clean under `go test -race -count=1`. Live smoke test against real `claude --output-format stream-json` green end-to-end (~9s via Claude Max OAuth, zero API tokens). The pre-existing batch `Supervise` is removed; streaming is the only supervisory path.

**Deferred from the plan:** Codex and Gemini CLI driver fixtures/parsers (need real captures once each provider's auth is configured). The `locutus mcp-perm-bridge` subcommand is built and tested but not yet wired into `ClaudeCodeDriver.BuildCommand` — the supervisor exposes a `permBridge` hook that tests set directly; production wire-up happens when the Dispatcher gets a CLI entry point.

## DJ-062: Permission Bridge via In-Process MCP Server, Not Stream Parsing (Reverses DJ-057)

**Status:** shipped

**Decision:** Permission events surface via a Unix-socket bridge from an in-process MCP server (`locutus mcp-perm-bridge` subcommand), not by tool-name matching on the agent's public event stream.

**Reverses DJ-057** (which proposed identifying permission events by matching the configured permission-prompt tool name in the stream). That premise was factually wrong for Claude Code. Verified experimentally against a running `claude --print --permission-prompt-tool mcp__perm__locutus_permission` with a stub MCP server: when the agent wants a restricted tool, Claude invokes the permission-prompt tool as a **separate MCP RPC** on a side channel, not as a `tool_use` event in the public stream. The stream only shows the original restricted tool (e.g., `Bash`) followed by a `tool_result` reflecting our allow/deny. So the stream parser can never see the permission request — it's invisible to stdout-based observation.

**What's correct now:** the supervisor opens a Unix socket per supervision session, spawns `locutus mcp-perm-bridge --socket <path>` as Claude's MCP server, and reads `PermRequest{id, tool, input}` off the socket as `AgentEvent{Kind: EventPermissionRequest, InteractionID: id, ...}`. `handleInteraction` asks the validator/guardian LLM for an allow/deny verdict, then routes back through `PermBridge.Respond`. No claude resume is needed — the blocked MCP RPC returns, Claude continues.

**`ClassifyToolName` survives as a utility:** DJ-057's intended mechanism is still sound for providers that *do* surface these events as tool calls in-stream (hypothetical). The function is kept and tested, just not wired into the Claude parser.

**AskUserQuestion visibility is unverified.** Claude's SDK docs describe an `AskUserQuestion` tool but we haven't confirmed whether it appears as a `tool_use` in `--print --output-format stream-json` mode. Treated as future extension; `EventClarifyQuestion` exists in the taxonomy and plumbs through the same bridge architecture if the fixture capture confirms it.

## DJ-063: Sliding-Window Churn Rule over Consecutive Counter (Refines DJ-058)

**Status:** shipped

**Decision:** Escalate to `RefineStep` when ≥2 of the last 3 attempt outcomes are `churnDetected`. Validation-only failures occupy slots in the window without counting as churn.

**Refines DJ-058** (which described a simple `consecutiveChurns` counter incremented on churn and reset on any non-churn outcome). That rule fails on alternating patterns — churn → validation-fail → churn — because the reset on the middle attempt clears the counter even though the step is clearly stuck in a loop.

**Regression test:** `TestSupervise_AlternatingChurnFailChurn_Escalates` exercises exactly that pattern and would fail the consecutive-counter implementation. Added as the guard against any future revert.

**Non-churn outcomes:** stay in the window but don't contribute to the count. They push old churn out once the window fills (after the 4th attempt, the oldest slot is dropped). This preserves the "N-of-last-M" semantics without letting validation failures pile up as evidence of churn.

## DJ-064: FastLLM Field Bounds Monitor Cost (Extends DJ-056)

**Status:** shipped

**Decision:** `SupervisorConfig` gains a `FastLLM agent.LLM` field distinct from the strong-tier `LLM`. `Supervisor.monitorCycle` uses `FastLLM`; `Supervisor.validate` and `handleInteraction` use `LLM`. When the monitor agent is configured but `FastLLM` is nil, the supervisor surfaces a clear "FastLLM is nil" error at call time rather than routing monitor prompts through the strong tier.

**Extends DJ-056.** The original plan had the monitor calling `s.cfg.LLM`, which in production would send every monitor cycle through the strong tier — defeating the "bounded cost" property that was DJ-056's whole point. The separate field makes the cost envelope explicit: monitors burn fast-tier tokens, validators/guardians burn strong-tier tokens, and callers who care (most importantly, `Dispatcher`) plumb both through explicitly.

**Missing monitor agent behavior:** when `AgentDefs["monitor"]` is unset, `monitorCycle` logs an INFO notice exactly once per supervisor (via `sync.Once`) and returns `IsCycle=false`. Silent disable — validation at attempt end still catches bad outcomes; a one-time log means misconfiguration is discoverable without noise.

## DJ-065: End-to-End Smoke Test Caught Three Production Bugs That Mock-Only Unit Tests Had Hidden

**Status:** shipped

**Decision:** Between Parts 6 and 7 of the streaming supervision build, paused feature work and wrote a hand-rolled integration test (`internal/dispatch/live_integration_test.go`, gated behind `LOCUTUS_INTEGRATION_TEST=1`) that runs the batch dispatcher against a real Claude Code subprocess on a trivial "create hello.txt" step. The test surfaced three production bugs in the pre-existing Dispatcher path that unit tests had never run into:

1. **`ClaudeCodeDriver.BuildCommand` didn't set `--permission-mode`.** In `-p` mode Claude can't prompt, so the default `default` permission mode auto-denies any tool call that would require approval. Claude would *claim* to have created the file in its response text but never actually touch the filesystem. Fixed by adding `--permission-mode acceptEdits` (allows file edits, still gates shell/network).

2. **Worktree branch and feature branch shared the same name.** `CreateWorktree` created `locutus/<id>`; `Dispatcher.runWorkstream` later tried to merge into `locutus/<id>` — but git won't check out a branch already used by a worktree. Merge failed with `'locutus/hello' is already used by worktree at '...'`. Fixed by splitting into `locutus-wt/<id>` (scratch) and `locutus/<id>` (feature target).

3. **`WorkstreamResult.BranchName` pointed at the transient scratch branch.** That branch is deleted in `Cleanup()` after merge, so callers saw a BranchName that no longer existed. Fixed by overwriting `BranchName` with the feature branch name after successful merge.

**Why this matters as a decision:** all three bugs were catchable by a ~50-line integration test. None were catchable by the existing extensive unit-test suite because mocks substituted for the real external behavior. The lesson — "validate assumptions about external systems with a real run before stacking more layers" — is now policy: the live integration test is kept in-repo, runs via `LOCUTUS_INTEGRATION_TEST=1`, and every time the streaming path changes it's re-run against real Claude to confirm no regression.

**Alternatives considered:** continuing the per-part unit work and validating end-to-end at the end. Rejected because by the time we'd reached Part 9 with the old bugs unfixed, we'd have been debugging a four-layer interaction instead of three one-line fixes.

## DJ-066: Genkit Wired with Env-Driven Plugin Auto-Detection (Completes DJ-003)

**Status:** shipped

**Decision:** `internal/agent/genkit.go` no longer stubs. `NewGenKitLLM()` inspects the environment via `DetectProviders()`, registers `github.com/firebase/genkit/go/plugins/anthropic` when `ANTHROPIC_API_KEY` is present and `github.com/firebase/genkit/go/plugins/googlegenai` when `GEMINI_API_KEY` or `GOOGLE_API_KEY` is present, and exposes an `agent.LLM` backed by `genkit.GenerateText`.

**Completes DJ-003.** The original decision committed to Genkit Go as the LLM abstraction layer but left `Generate()` returning `"GenKit LLM provider not yet wired"`. That stub is replaced; the live smoke test (`TestGenKitLLM_LiveSmoke` with `LOCUTUS_INTEGRATION_TEST=1`) hits a real provider through a real API key loaded from `.env`.

**Why env-driven auto-detection:** the Genkit plugins `panic` during `Init` if their API-key env var is missing. Registering a plugin unconditionally would brick the whole process for any user who hasn't set up *every* provider. `DetectProviders()` inspects env first, registers only the matching plugins — a Gemini-only user never pulls in the Anthropic plugin (and vice versa). `sync.Once` guards `genkit.Init` against the plugins' second-initialization panic.

**Claude Max subscription caveat:** Anthropic's OAuth (used by the `claude` CLI, zero-cost for Max subscribers) can't be used by the Go SDK — the Anthropic Go SDK requires an API key. So a user with Claude Max who sets `ANTHROPIC_API_KEY` burns API tokens, not Max credits. This is surfaced in the Genkit wire-up commit's rationale and mentioned when users face the choice.

**Provider prefix required:** Genkit requires `anthropic/...` or `googleai/...` on every model string — it routes by prefix. Model strings without a prefix fall back to the configured default via `GenKitLLM.resolveModel`. Callers can override at three layers: `LOCUTUS_MODEL` env var (global), per-`AgentDef.Model` field (per agent), or `LOCUTUS_MODELS_CONFIG` YAML override (per-project or per-user — see DJ-067).

## DJ-067: Model Tier Config via Embedded YAML, List-per-Tier Runtime Resolution (Supersedes DJ-053)

**Status:** shipped

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

**Status:** shipped

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

**Status:** shipped

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

**Status:** shipped

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

**Status:** shipped

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

**Status:** shipped

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

## DJ-073: Active Workstream Persistence for Crash Recovery

**Status:** shipped

**Decision:** Refine DJ-069's "ephemeral PlanStep" claim: the full in-flight dispatch — MasterPlan plus all its ActiveWorkstreams — is **ephemeral at planning time** (not cached between `adopt` invocations; each plan run regenerates the plan tree from the current Approaches + codebase state, per DJ-069's original rationale) but **persistent during execution**. Once the planner hands a MasterPlan to the dispatcher, both the plan itself (for its InterfaceContracts, GlobalAssertions, and workstream dependencies per DJ-027) and each ActiveWorkstream (for its PlanStep DAG, agent session ID, per-step status) are written under `.locutus/workstreams/<plan-id>/` and kept until every Approach the plan covers reaches `live` (archive) or upstream drift invalidates the plan (delete + re-plan).

**Why the refinement is needed:**

DJ-069 framed PlanSteps as ephemeral to justify regenerating them against current codebase state on every plan run. That rationale holds for the **plan → dispatch** transition: the same Approach can produce different PlanSteps on different days because brownfield context changes. It does not address what happens once a workstream is dispatched and the coding agent is actively executing.

Without persistence, a crash — agent death, Locutus death, machine reboot — forces the next `adopt` invocation to choose between:

1. Treating an `in_progress` entry as drift and replanning from scratch (wastes agent work).
2. Treating it as live (wrong; nothing completed).
3. Resuming — but from what? The plan was never written down.

Option 1 wins by default and is fine for trivially small Approaches but unacceptable once an Approach spans multiple files and tests (which is the common case).

**Persistence mechanics:**

- **Location:** one subdirectory per active plan under `.locutus/workstreams/<plan-id>/`, containing `plan.yaml` (the MasterPlan as a `PlanRecord`) and one `<workstream-id>.yaml` (an `ActiveWorkstream`) per dispatched workstream. Whole-plan cleanup removes the subdirectory.
- **Gitignored, not committed.** The directory layout mirrors `.locutus/state/` (DJ-068 pattern) but the contents are transient coordination state — deleted on terminal transition, mutated on every PlanStep completion, invalidated on drift. Committing step-by-step progress would produce diff noise without adding anything a fresh clone could use; a new checkout correctly sees no active workstreams and proceeds as "nothing in flight." This is a narrow departure from DJ-068's "state is always in-repo" framing: durable reconciliation state is committed; execution coordination is not.
- **Shape of `ActiveWorkstream`:** embeds `spec.Workstream` verbatim, adds `PlanID string` (join back to the owning plan), `ApproachIDs []string` (which Approaches this workstream covers), `AgentSessionID string` (for coding-agent `--resume`), `PreFlightDone bool`, `StepStatus []StepProgress`, and `CreatedAt` / `UpdatedAt` timestamps.
- **Shape of `PlanRecord`:** wraps `spec.MasterPlan` (carrying `InterfaceContracts`, `GlobalAssertions`, workstream `DependsOn` graph, and the trigger prompt per DJ-027) plus its own `CreatedAt` / `UpdatedAt`. The MasterPlan must persist — those cross-workstream fields only live on the plan, not replicated onto individual workstream records, so losing the plan would forfeit the coordination data a resume run needs.
- **Write points:**
  - On dispatch: planner mints `PlanID` and one `WorkstreamID` per workstream; `SavePlan` writes the MasterPlan once, then one `Save` per workstream as each is handed to the dispatcher. `ReconciliationState.WorkstreamID` is stamped on each covered Approach's state entry.
  - On each PlanStep completion: dispatcher calls `Save` on the affected `ActiveWorkstream` (updated `StepStatus`).
  - On terminal transition (all Approaches the plan covers reach `live`, or the user aborts): `DeletePlan` removes the entire `<plan-id>/` subdirectory. `ReconciliationState.WorkstreamID` becomes historical (retained on state entries as audit trail, with no record to dereference).

**Resume protocol (next `adopt` invocation):**

1. `ListActivePlans` enumerates plan IDs that have an in-flight `<plan-id>/plan.yaml` marker.
2. For each active plan, load the `PlanRecord` and all its `ActiveWorkstream` records.
3. Classify every Approach across the plan's union of workstream coverage using DJ-068 rules.
4. Branch:
   - **All covered Approaches unchanged (`live`/`in_progress`, spec_hash matches):** resume. For each workstream, restart the coding agent with `--resume <AgentSessionID>` (streaming driver capability), skipping PlanSteps already marked `complete` in `StepStatus`. Inter-workstream dependencies come from the plan's `DependsOn` graph (now persisted).
   - **Any covered Approach has drifted:** invalidate the whole plan. `DeletePlan` removes the subdirectory; clear `WorkstreamID` on the affected state entries; proceed as a fresh plan run (new MasterPlan, new workstream IDs, fresh dispatches).
   - **All covered Approaches reached `live`:** archive. `DeletePlan` removes the subdirectory; `live` status on each `ReconciliationState` is the durable record.

**Why this does not contradict DJ-069:**

DJ-069's concern was that PlanSteps must be regenerated to reflect current codebase context during planning. That rationale still holds: the planner never reads `.locutus/workstreams/*` as input; nothing in the plan pipeline consults persisted PlanSteps to short-circuit regeneration. The persistence is of an **execution contract** — locked when the agent is told "here are your steps," invalidated the moment upstream drift is detected. DJ-069 is preserved; "ephemeral" is narrowed to "not cached across planning runs."

**Alternatives considered:**

- **Coarse resume (Approach-level only).** Treat the whole Approach as redo-on-crash; no persistent PlanSteps. Simple, no new file kind, but wastes the work of every completed step inside the crashed Approach. Rejected once we accept that Approaches are often multi-file / multi-test.
- **Ephemeral everything; rely on agent session resume alone.** Claude Code's `--resume` reopens a session by ID, so maybe persisting just the session ID on `ReconciliationState` is enough. Rejected: session resume brings the *conversation* back, not the *plan* — the agent wouldn't know which PlanSteps were already complete without the persisted step status map.
- **Persist PlanSteps on the Approach itself.** Rejected for the same DJ-068 reasons rehearsed in DJ-072's design discussion: mixes intent (Approach) with execution telemetry (step progress), produces spec diffs on every dispatch, and breaks `git revert` semantics on the spec.
- **Persist only the DAG, not per-step status.** Rejected: without step status, we cannot skip completed steps on resume, which is the whole point of persistence.

**Impact on other decisions:**

- **DJ-068 (manifest/state separation):** unchanged. `.locutus/workstreams/` is a new state-store directory alongside `.locutus/state/`, following the same in-repo-YAML pattern.
- **DJ-069 (node redesign):** clarified. "Ephemeral PlanStep" is now precisely "regenerated every plan run; persisted once dispatched; invalidated on upstream drift." The layer model (Approach / PlanStep / Workstream) and the denormalisation principle are preserved.
- **DJ-071 (pre-flight):** complementary. Pre-flight resolutions continue to land as `assumed` Decisions in the spec graph (durable regardless of workstream fate). A workstream record captures whether pre-flight has already run for that dispatch so resume doesn't re-run it.
- **DJ-027 (hierarchical plans):** unchanged. MasterPlan → Workstream → PlanStep remains the planner's output shape. MasterPlans themselves remain ephemeral (no file kind introduced for them yet) — only the subset of Workstreams actually being executed land on disk.
- **Streaming supervision plan** (`.claude/plans/streaming-supervision.md`): complementary. Intra-attempt retry (`Supervisor.Supervise` outer loop) and churn detection remain Locutus-internal; crash recovery across process deaths is what DJ-073 adds.

**Impact on code:**

- **New package `internal/workstream`:** `ActiveWorkstream` and `PlanRecord` types; `FileStore` constructed per plan with `SavePlan` / `LoadPlan` / `Save` / `Load` / `Walk` / `Delete` / `DeletePlan`; package-level `ListActivePlans(fsys, baseDir)` for the resume entry point. Mirrors `internal/state`'s shape for per-entity YAML but nested per plan.
- **New status enum `StepExecutionStatus`:** pending / in_progress / complete / failed, tracked per PlanStep inside `ActiveWorkstream.StepStatus` via `StepProgress` entries.
- **New FS primitive `ListSubdirs`:** added to `specio.FS` (and implemented for `OSFS` and `MemFS`) so `ListActivePlans` can enumerate plan subdirectories without relying on file-naming conventions. Mirrors the non-recursive shape of `ListDir`.
- **`.gitignore`:** `/.locutus/workstreams/` excluded. `.locutus/state/` (DJ-068) remains tracked.
- **`cmd/adopt.go`:** when dispatch is wired (deferred Phase C round), the resume protocol above runs *before* the current classification pass. The clear-on-drift invariant from DJ-072's follow-up already prepared the state entries: when `WorkstreamID` is cleared on drift, `DeletePlan` removes the corresponding `.locutus/workstreams/<plan-id>/` subdirectory in the same step.
- **Dispatcher integration:** writes `plan.yaml` once on planner completion, writes one `<workstream-id>.yaml` per workstream on dispatch, updates on each PlanStep completion, deletes the plan subdirectory on terminal transition. Agent session ID plumbed in from the driver.

**Cleanup / garbage collection:**

Orphaned workstream records (Approaches removed from the graph while a record still references them) are detected on the next `adopt` run — if none of a record's `ApproachIDs` resolve in the current spec graph, the record is deleted with a log line. No background GC needed; the reconciler is the GC.

**Not in scope for DJ-073:**

- Post-completion MasterPlan archival. The `PlanRecord` exists only while a plan is in flight; once every Approach reaches `live`, the subdirectory is deleted. If we later want historical plan archival (for post-hoc "how did we build this" analysis), that's a separate decision — either pipe plan summaries through the historian or add a committed `docs/plans/archive/` location.
- Cross-machine resume. Everything here assumes single-machine execution; distributed resume is a future problem.
- Concurrent plans. Nothing forbids two overlapping plans in the layout (subdirectories don't collide), but `adopt` doesn't today take a write-lock against another in-flight `adopt`. If that becomes a concern, a lockfile under `.locutus/` is the natural next step.

## DJ-074: True `--resume` for Interrupted Adoption

**Status:** shipped (2026-04-25)

The current `adopt` invalidates any leftover plan subdirectory from `.locutus/workstreams/` and replans from scratch, even when nothing has drifted. DJ-073's resume-path contract explicitly specifies per-session resume ("Restart the coding agent with `--resume <AgentSessionID>`, skipping PlanSteps already marked complete") but landing that cleanly requires two pieces of plumbing the Phase C MVP skipped. This DJ captures the design so future work can execute it without re-deriving the shape.

**Decision:** Implement true resume for the DJ-073 "no drift detected" branch with the following components:

1. **Session-ID capture at dispatch.** `dispatch.WorkstreamResult` gains an `AgentSessionID string` field; the supervisor already observes session IDs in the streaming event feed (cf. `internal/dispatch/streaming.go::attemptResult.sessionID`) and just needs to surface the final one. `cmd/adopt.go` then writes `AgentSessionID` onto the `ActiveWorkstream` record so it persists alongside `StepStatus`.

2. **Skip-to-step mode in the dispatcher.** `runWorkstream` currently iterates `ws.Steps` from index 0 unconditionally. Add an input shape — either a `resumeFrom` parameter or an overload — that accepts the step ID (or index) to start from and a session ID to pass through to the driver. The worktree must be derived from the existing `locutus/<ws-id>` feature branch so the already-completed steps' merged work forms the starting state, not a fresh `main`.

3. **Driver `--resume` support.** `StreamingDriver.BuildCommand` gains a `SessionID string` field on its request struct; `ClaudeCodeDriver` translates it to `--resume <id>` on the `claude -p` invocation, and `CodexDriver` to `codex exec --session <id>`. Drivers without `--resume` capability reject with a clear error — the caller then falls back to invalidate-and-replan.

4. **`adopt` resume branch fleshed out.** Replace the current `resumeOrInvalidateActivePlans` (which always wipes) with a classifier-driven dispatcher:
   - **All covered Approaches unchanged:** for each `ActiveWorkstream`, find the first `StepProgress` whose Status is not `complete`. Dispatch with `resumeFrom=<step-id>` and `AgentSessionID=<rec.AgentSessionID>`. Steps already complete are skipped.
   - **Any covered Approach drifted:** invalidate as today.
   - **All covered Approaches live:** archive (`DeletePlan`) as today.

5. **User flag shape.** Default behaviour becomes *auto-resume when possible*, matching DJ-073's spec. An explicit `adopt --discard-in-flight` flag forces invalidation for the "I know this plan is wrong, start over" case. No `--resume <session-id>` argument — the ID is always read from persisted state, per the user's observation that a human-supplied session ID is an anti-pattern (the record is the source of truth).

**Why separated from DJ-073:** DJ-073's Phase C MVP shipped correct persistence and correct invalidate-and-replan. Shipping a half-finished resume (session-id captured but dispatcher can't skip steps; or skip-to-step works but no session-id reuse so a fresh conversation restarts and re-does prior agent work) would burn tokens and create spurious file churn. The clean increment is either *all three* plumbing pieces (capture + skip + driver flag) or none. DJ-074 gates the feature on that.

**Discovery in the meantime:** `locutus status --in-flight` lists every leftover plan with its `AgentSessionID`, per-workstream step progress, and next-pending step. That's enough to decide whether a run should be resumed (nothing drifted, sessions valid) or discarded (new spec coming, sessions stale) before invoking `adopt`.

**Alternatives considered:**

- **Fresh-session replay.** Skip driver `--resume` support; use a new session each resume, but still skip already-complete steps. Rejected: the agent loses conversation context from the original session, and the MVP can't reliably model "this step is already done" to a fresh agent without re-writing prompts. Partial credit for the tokens saved on skipped steps, but the agent-context loss dominates.
- **Prompt-driven checkpoint.** Rather than driver `--resume`, serialize the conversation state into the Approach body and re-inject it on replay. Rejected as brittle — the conversation state is the agent's private model; trying to externalize it via prose reliably has failed in practice (see DJ-025's "council rounds are the conversation" note).
- **Interactive prompt on leftover detection.** When `adopt` detects a leftover plan, stop and ask the user `[r]esume, [d]iscard?`. Rejected: `adopt` must remain scriptable. Flags (`--discard-in-flight`) carry the same signal without blocking automation.

**Dependencies & next steps:** Implementing DJ-074 touches `internal/dispatch/supervisor.go` (session-id surfacing), `internal/dispatch/dispatcher.go` (resume-from-step mode), `internal/dispatch/drivers/*` (driver flag), and `cmd/adopt.go` (branching replace). Estimated one focused session if the driver flag work is scoped to Claude Code first and Codex lands as a follow-up.

**Implementation (Round 7, 2026-04-25):** landed in three commits.

- **Phase A** (`c20604e`, dispatch layer): `StepOutcome.SessionID`, `WorkstreamResult.AgentSessionID`, `ResumePoint{StepID, SessionID}`, `Supervisor.SuperviseFrom` (sibling that pre-seeds sessionID), `runWorkstream` accepts `*ResumePoint` (skip-to-step + worktree-from-base via `CreateWorktreeFromBase`), `workstreamHasStep` validates the step ID before any side effects.
- **Phase B** (`672b33f`, plumbing): `DispatchFunc` and `Dispatcher.Dispatch` signatures grew `resume map[string]*dispatch.ResumePoint`; `AdoptCmd.DiscardInFlight` + `--discard-in-flight` CLI flag; `recordStepProgress` persists `AgentSessionID` on `ActiveWorkstream`.
- **Phase C** (this commit, policy): `classifyActivePlans` does drift-aware classification — for each leftover plan, walks records, computes current `ComputeSpecHash` for each covered Approach and compares against the persisted state's `SpecHash`. Verdicts: any drift → invalidate; all live → archive; otherwise resume. `RunAdoptWithConfig` now short-circuits to a new `runAdoptDispatchAndVerify` helper when `PlanToResume` is non-nil — the planner is **not** invoked, the persisted plan is used directly, pre-flight is skipped (it ran on the prior invocation). `buildResumePoint` walks each ActiveWorkstream's `StepStatus` and points `ResumePoint.StepID` at the first non-`StepComplete` step with the workstream's persisted `AgentSessionID`. At most one resumable plan per invocation; multiple leftover resumable plans → first wins, rest invalidate.

Driver `--resume` support landed implicitly: Claude Code's existing `BuildRetryCommand` already issues `--resume <id>`, and Phase A wired `SuperviseFrom` to call `BuildRetryCommand` on the resumed step's first attempt. Codex / Gemini support is still deferred per the original DJ scoping.

20 tests across `internal/dispatch/resume_test.go` and `cmd/adopt_integration_test.go` cover: surfacing, skip-to-step, sessionID pre-seed, unknown-step error, sessionID propagation, `--discard-in-flight` force invalidate, archive-when-all-live, invalidate-on-drift, resume-when-clean (planner not called, dispatch sees correct ResumeMap).

## DJ-075: Assimilate Reads Existing Spec, Writes Back Atomically

**Status:** shipped

**Decision:** `locutus assimilate` is an idempotent spec-writer, not just a spec-proposer. Before running inference, it loads the current `.borg/spec/` into an `ExistingSpec` snapshot and passes that to the scout prompt so the LLM can distinguish new nodes from updates to existing ones (matching IDs = update, new IDs = new). After the pipeline returns, the inferred nodes are persisted back to `.borg/spec/` with per-file atomicity and a top-level sentinel that surfaces crashed prior runs. Nodes without an explicit status default to the `inferred` lifecycle state.

**Why this is a DJ rather than an implementation note:**

Until 2026-04-23 the assimilation pipeline returned an `AssimilationResult` and threw it away — the command printed a count and nothing landed on disk. That made `assimilate` effectively a one-shot inference demo: running it a second time re-inferred the same spec without any awareness of what already existed, and nothing a caller could commit. Resolving that is a real architectural decision (input shape, conflict policy, crash safety, status defaults), not a mechanical fix. Capturing it here so future rounds can build on the invariants instead of re-deriving them.

**Key invariants:**

1. **`ExistingSpec` is loaded before inference.** `RunAssimilate` calls `loadExistingSpec` and attaches the snapshot to `AssimilationRequest.ExistingSpec`. The scout prompt is extended with an `## Existing spec` section listing every current Feature, Decision, Strategy, Approach, and Entity so the LLM emits updates (matching IDs) alongside new nodes rather than duplicating concepts under fresh IDs.
2. **Per-file atomic writes.** Each node persists via `specio.SavePair` / `SaveMarkdown`, which use write-temp-and-rename semantics internally. Any single file is either fully written or not touched. A crash mid-loop may leave some new files present and others absent, but never half-written content — `git status` surfaces exactly what landed, and `git restore .` reverts cleanly.
3. **Sentinel file for crash detection.** `.borg/spec/.assimilating` is written at the start of the persistence loop and removed on success. If a later run finds it present, the prior run crashed; the caller can warn before proceeding. No new verb or flag required.
4. **Conflict resolution via merge-aware LLM.** No policy baked into the persistence layer. The LLM received the existing spec and already made the merge decision; the persistence layer just writes whatever the LLM produced. Matching IDs overwrite existing files; new IDs create new ones. Hand-authored spec that the LLM correctly identifies as `active` gets preserved because the LLM emits it unchanged.
5. **`inferred` as the default status.** Features and Decisions landed via assimilate without an explicit status default to `FeatureStatusInferred` / `DecisionStatusInferred`. This distinguishes inferred-from-code from assumed-during-pre-flight (DJ-071) — both have `confidence < 1.0` and want refinement, but provenance differs: `inferred` means "this decision is implicit in existing code," `assumed` means "Locutus guessed because the spec was silent."
6. **Dry-run preserved for free.** The existing `readOnlyFS` wrapper drops every write, including the sentinel. `--dry-run` still runs inference end-to-end and reports what would land without touching disk. A minor wrinkle (`WalkInventory` type-asserted to concrete FS types) was fixed by an `Unwrap()` escape hatch on `readOnlyFS`.

**Alternatives considered:**

- **Staging to `.borg/spec/.pending/` + explicit `--commit`.** Safer (human reviews diff before promoting) but adds a verb/flag for a workflow `git` already provides. `git diff` before committing *is* the staging step. Rejected.
- **Skip-existing conflict policy.** Pre-load was dismissed initially; the fallback was "don't overwrite hand edits." Rejected once we realized the LLM can't correctly enhance an existing feature without knowing it exists — the skip policy hid the input the LLM needed to make the right call.
- **Error on conflict.** Would block re-running `assimilate` after any code change that touched an already-inferred area. Rejected as hostile to iteration.
- **`inferred` status reused from `assumed`.** Semantically subtle but real: assumed = pre-flight guess with no evidence; inferred = read from code with evidence. Keeping them separate gives human reviewers the right prompt ("verify a guess" vs. "verify I read this right").

**Impact on other DJs:**

- **DJ-014 (shipped):** brownfield self-analysis now produces committable spec, not just a console summary. No change to DJ-014 itself.
- **DJ-019 (shipped):** "Heuristic first, LLM second" still governs the inference shape; DJ-075 extends it with a persistence contract.
- **DJ-045 (shipping):** Remediation (Round 5 of the gap-closeout plan) now has a reliable dependency — the spec is on disk before the remediator runs, so cross-cutting vs. feature-specific gap attribution has a real graph to attach to.
- **DJ-068 (shipped):** `.borg/spec/` remains the authoritative manifest; nothing in DJ-075 moves state into or out of the state store.
- **DJ-069 (shipped):** Node kinds preserved; only the write-back path is new.

**Not in scope for DJ-075:**

- Entity persistence. Resolved separately in DJ-076: Entity stays as in-memory context on `AssimilationResult`, never persisted to `.borg/spec/entities/`.
- Deleting spec nodes that no longer match the code. Reverse drift ("feature is in spec, code has removed it") is a different problem — belongs in `adopt`'s `out_of_spec` surfacing, not in `assimilate`.
- Remediation of detected gaps (that's Round 5 of the gap-closeout plan, governed by DJ-045).

## DJ-076: Entity Is In-Memory Context, Not a Persisted Spec Node

**Status:** shipped

**Decision:** `spec.Entity` (plus `EntityField` and `Relationship`) remains a live type in the codebase, populated by the assimilation pipeline and attached to `AssimilationResult.Entities`. Downstream agents (planner, supervisor, remediator) receive it as structured context so they can reason about existing data models without re-parsing Go/proto/SQL on every call. But Entities are **never persisted** — there is no `.borg/spec/entities/` directory, no `Entity` marshals to disk, and `loadExistingSpec` does not populate `ExistingSpec.Entities` from storage. A fresh assimilate run reconstructs the projection from code. The `KindEntity` enum value is retained so agent JSON output can be tagged as entity-kind for routing, but no graph edge, cascade path, or lifecycle treats entities as first-class spec nodes.

**Why this distinction matters:**

The confusion that surfaced during Round 1 was treating "formal extraction of a domain model" as equivalent to "persisted spec node." They're separate concerns. The extraction is valuable — an LLM planning a new Feature against an existing codebase benefits from seeing `User{id, email, password_hash} has_many Order` rather than being asked to infer the data model from scratch every call. That value is real at *inference time*, in one run. The value of *persisting* that structure across runs is much weaker: the code is the authoritative schema; a serialized entity file is a cache that drifts the moment someone renames a field.

In short: formal extraction earns its keep as agent context; formal persistence doesn't, because the code never stopped being the source of truth.

**What remains in code:**

- `spec.Entity`, `spec.EntityField`, `spec.Relationship` — structured types for passing between agents.
- `spec.KindEntity` — enum value (marked with a comment noting the DJ-076 semantics) so typed output from analyzer agents can be routed.
- `AssimilationResult.Entities` — populated by the pipeline; consumed in the same process.
- `ExistingSpec.Entities` — optional field the caller can populate when re-running against a previous projection, explicitly nullable.
- `backend_analyzer.md` prompt — still extracts entities; the output just doesn't get persisted.

**What's deliberately NOT there:**

- `.borg/spec/entities/` directory.
- Entity handling in `persistAssimilationResult` (the persistence loop skips entities with a code comment pointing at this DJ).
- Graph edges involving `KindEntity` nodes (`SpecGraph.BuildGraph` never adds them).
- Cascade or reconcile logic for entities.

**Why code, not spec, is authoritative for entity structure:**

1. **Correctness.** `grep 'type User struct'` is always right. A persisted entity file is right only until the next code change; it's a cache with unavoidable drift.
2. **Coding agents can read code.** That's the premise. If an agent needs to know User's fields, it opens the file. What an agent *can't* read from code is intent — "why bcrypt" or "what this feature does for users" — and that goes in Feature/Decision/Strategy prose, where it already belongs.
3. **Lifecycle has no obvious meaning.** Features have `proposed → active → removed`; Decisions have `proposed → assumed → inferred → active`; both map to user-visible judgements. What's `status: proposed` for a User struct that already exists in code? The lifecycle concept doesn't transfer.
4. **Drift detection doesn't need an Entity abstraction.** Feature/Approach hash-drift already captures "this file changed underneath the spec node that governs it." That mechanism catches field renames by surfacing the Approach that owned the file, which is more useful than abstract "entity changed."

**Alternatives considered and rejected:**

- **Delete Entity entirely.** Considered during the same conversation. Rejected because the in-memory extraction has real value as agent context — dropping the type would lose that. The mistake was conflating "keep the type" with "keep the persistence."
- **Persist to `.borg/spec/domain.md` as a flat doc.** Rejected: even a single doc is a cache that drifts against code, with all the same problems as per-entity files. If the value is agent context, the LLM can reconstruct it fresh on each assimilate run in less time than it takes to decide whether the on-disk doc is current.
- **Persist entities with `status: inferred` and cascade on field rename.** Rejected as overkill. The cascade model exists to keep spec prose consistent with Decisions; it doesn't buy anything when the "spec" in question is already machine-generated from code.

**Impact on adjacent DJs:**

- **DJ-069 (shipped):** node redesign DAG is unchanged. Entities were already not in `Goal → Feature/Strategy → Decision/Approach`; DJ-076 makes that exclusion explicit and rationalised.
- **DJ-075 (shipped):** updated in-place — "Entity persistence" removed from the "not in scope" deferrals list; DJ-076 is the resolution.
- **Round 5 of the gap-closeout plan:** the remediator uses in-memory entities to frame gap descriptions ("no tests for User"), still works exactly as intended.
- **Original IMPLEMENTATION_PLAN.md Tier 6e** (deleted): `EntityExtractor` framework-specific implementations (Go structs, proto, TypeScript, SQL migrations) remain a future extension — the *extraction* has room to grow even though the *persistence* is frozen as "none."

**Invariant to preserve going forward:** when future work wants to give Entity behaviour that feels lifecycle-adjacent (cascade, refine, etc.), first ask whether the same behaviour attached to the Feature/Strategy/Decision that *uses* the entity would be cleaner. In every case examined so far, it was.

**Forward-looking note — the extraction itself is provisional.** The persistence decision ("never write entities to `.borg/spec/`") is firm, because the argument against it is purely mechanical (the code is the source of truth; a serialised file is always a drifting cache). The *extraction* decision — keeping `AssimilationResult.Entities` as in-memory context for downstream agents — is conditional. We're carrying the cost of parsing and emitting entity structure on every assimilate run as an open bet that feeding structured domain data to the planner / supervisor / remediator produces better Approaches than reconstructing that structure from code in each prompt. Whether that bet pays off isn't decidable in the abstract; it will be answered by operational history once enough real adopt-and-refine cycles accumulate.

Until then, the framing holds: **usage and motivation are the valuable outputs of assimilation; structure reconstruction is a provisional helper.** When an Entity shows up in agent context, it should be in service of guiding what the agent should DO with the data (a Feature's behaviour, a Decision's rationale, a Strategy's pattern) — not re-explaining what a struct looks like when the agent can just read the file. If a later audit shows the formal extraction is burning tokens without measurably improving agent output, dropping the extraction entirely is the next step and this DJ gets superseded.

## DJ-077: Selective Adoption from Google ADK, Not Wholesale (Narrows DJ-029)

**Status:** settled

**Decision:** DJ-029 rejected wholesale adoption of `LangGraphGo` / `LangChainGo` on maturity and pattern-fit grounds and adopted a "custom orchestration" posture. That posture holds today but has narrowed: Locutus's custom orchestration has grown past the "~350 LOC" footprint cited in DJ-029 to roughly 2k+ LOC, and Google's Agent Development Kit ([adk-python](https://github.com/google/adk-python), [adk-go](https://github.com/google/adk-go)) shipped with first-party maintenance, active releases, and abstractions that map onto real gaps in Locutus (memory, evaluation). DJ-077 permits **selective, attribution-preserving adoption of ADK patterns and code at specific integration points**, while preserving DJ-029's rejection of wholesale adoption.

**Why not wholesale adoption:**

1. **adk-go is Gemini/Apigee-only at the model layer.** `model/gemini/` and `model/apigee/` are the only implementations; the `LLM` interface uses `genai.Content` as the content type, which is Gemini-flavored. Adopting wholesale would require a Genkit-adk-go adapter that translates Anthropic/OpenAI content into `genai.Content` shape — non-trivial, ongoing maintenance cost, lossy in places (tool-use formats, thinking blocks).
2. **adk-python is the canonical surface.** adk-go has a ~70% port: missing `evaluation/`, `code_executors/`, standalone `planners/`, `a2a/`. Anything beyond adk-go's current surface would require porting from Python. Wholesale adoption means committing to that ongoing port or depending on abstractions we only half-have.
3. **Session/runtime models don't map cleanly as a blanket.** adk-go's `session.Service` is conversation + user-scoped. Locutus is single-user, so the user-scoping is dead weight; adopting the whole runtime wholesale would complicate, not clarify. **Partial revisit (2026-04-23):** this bullet was too broad as originally written. The *event-log-per-session* shape does map onto `refine`-with-council, where a workstream behaves like a session and drafter/challenger turns behave like chat turns. Challenger and analyst agents cannot do their jobs against memory alone — they need the raw reasoning trace. A workstream-scoped session-event store lands as a Round 3 prerequisite, tracked in [`.claude/plans/gap-closeout-pre-round3-session-history.md`](../.claude/plans/gap-closeout-pre-round3-session-history.md). This is still not wholesale adoption of `session.Service` — we take the event-log shape, drop the user scoping, key by workstream.

**Where selective adoption IS appropriate:**

1. **Memory abstraction.** `memory.Service` is a tiny interface (two methods: `AddSessionToMemory`, `SearchMemory`), and the shape maps onto the agent-memory gap audited on 2026-04-23. Adapted (Content → plain string; user/app scoping → project scoping), it becomes our shared memory primitive. Copy-with-attribution.
2. **MCP toolset pattern.** `tool/mcptoolset/` demonstrates a clean way to expose MCP-server tools as agent tools with filter + confirmation. We already have MCP plumbing; this is pattern inspiration for when the planner or supervisor wants to invoke external MCP tools. Reference, not code copy.
3. **Evaluation framework (from adk-python).** Not present in adk-go. Port from adk-python as the basis for Round 4's `llm_review` assertion — rubric + LLM-as-judge shape beats writing a reviewer agent from scratch. Translation effort is real (~2 sessions) but delivers more than what we'd build.
4. **Workflow agents (sequential / parallel / loop) as reference.** `agent/workflowagents/` formalizes patterns our council executor does ad-hoc. Not code-copy today (our executor works), but good reference for future executor refactors.

**Licensing posture:**

- APL v2 → MIT is compatible. Derived files carry a top-of-file comment: `// Portions adapted from github.com/google/adk-go, Copyright 2025 Google LLC, Licensed under the Apache License 2.0.`
- A `NOTICE` file at repo root enumerates ADK-derived portions.
- If we copy verbatim chunks larger than incidental, `third_party/adk/LICENSE-APACHE` holds the license text.
- Locutus-written code remains MIT; ADK-derived portions remain APL v2. Mixed-license is standard practice.

**Implications for the gap-closeout plan:**

- **Pre-Round-3 increment — memory (shipped 2026-04-24):** `internal/memory/` ships the two-method `Service` interface (`AddSessionToMemory` + `SearchMemory`), an `Entry` shape adapted from `adk-go/memory` (string content instead of `genai.Content`, namespace scoping instead of user+app), and two implementations: `NewInMemoryService()` and `NewFileStoreService(fsys, root)` keyed by UUIDv4. Search is case-insensitive substring over `Content` (embedding-backed impl deferred). File layout: `<root>/<namespace>/<uuid>.yaml`, gitignored per DJ-073's transient-learned-state posture. Attribution lives in top-of-file comments and `NOTICE` at the repo root. Tracked in [`.claude/plans/gap-closeout-pre-round3-memory.md`](../.claude/plans/gap-closeout-pre-round3-memory.md).
- **Pre-Round-3 increment — session history (shipped 2026-04-24):** `internal/session/` ships a workstream-scoped, append-only event log: `Store` interface (`Append` + `Read`), a `SessionEvent` shape with roles that mirror the LLM provider API (`system`|`user`|`assistant`|`tool`) so events replay into a provider's messages array without translation, plus `InMemoryStore` and `FileStore`. File layout: `<root>/<workstream-id>/events.yaml` as a multi-document YAML stream, appended via `O_APPEND` + `fsync` on the OS-backed FS (`specio.OSFS.AppendFile` and `specio.MemFS.AppendFile` added). Corrupt trailing documents are logged and skipped. The design is Locutus-native enough that no `NOTICE` attribution is required — ADK's event-log-per-session shape was inspiration, not code. Tracked in [`.claude/plans/gap-closeout-pre-round3-session-history.md`](../.claude/plans/gap-closeout-pre-round3-session-history.md).
- **Round 4 — `llm_review` via eval framework (shipped 2026-04-24):** `internal/eval/` ships an `Evaluator` interface, an `EvalCase` / `EvalMetric` pair, and a `Runner` keyed by `spec.AssertionKind` — adapted from adk-python's evaluation framework, dropping its `EvalSet` / `Invocation` abstractions which assume a chat-turn runtime Locutus does not have. The MVP evaluator is `LLMJudge`, registered for `AssertionKindLLMReview`; future evaluators (safety, latency, multi-judge consensus) bind against new `AssertionKind`s without a central switch. New agent: [`internal/scaffold/agents/llm_judge.md`](../internal/scaffold/agents/llm_judge.md), narrow per-assertion judge with structured JSON output (passed / reasoning / confidence). `cmd/adopt_assertions.go`'s pass-with-note stub is gone; `runAssertions` now takes `(ctx, approach, repoDir, *eval.Runner, specio.FS)` and routes `llm_review` through the runner. Per-file size cap (64 KiB default, configurable per evaluator) prevents large artifacts from blowing context. Tracked in [`.claude/plans/gap-closeout.md`](../.claude/plans/gap-closeout.md) Round 4.
- **Rounds 3, 5–8:** unchanged in scope; memory and session-history primitives become available to later rounds as optional inputs.

**Supersession scope relative to DJ-029:**

DJ-029 remains in force for wholesale-framework adoption of LangGraphGo/LangChainGo — those specific rejections still apply. DJ-077 narrows DJ-029's "custom orchestration" implication: *selective* adoption of individual patterns/packages from well-maintained, license-compatible external frameworks is permitted when the alternative is reinventing the same abstraction. Future decisions about adopting other parts of ADK (or alternative frameworks) re-evaluate under this posture.

**Not in scope for DJ-077:**

- Wholesale migration to ADK runtime (executor, runner, session).
- Any commitment to port beyond the memory + evaluation surfaces. Each future adoption is its own decision.
- Multi-provider translation adapter for `genai.Content`. If we ever want adk-go's runtime, that adapter is a prerequisite; today we don't.

## DJ-078: Agent Definition and Prompt Templating Policy (Refines DJ-077)

**Status:** settled

**Decision:** DJ-077 permits selective adoption from Google ADK but leaves open *how* adopted patterns are surfaced. DJ-078 pins the two most load-bearing choices: serialization format and prompt templating syntax.

1. **Serialization: Locutus keeps its own format.** Agent definitions remain markdown files in `.borg/agents/` (per DJ-036), loaded via `internal/agent.NamedAgentFn`. We do **not** adopt ADK's YAML `LlmAgentConfig` shape or its 4600-line JSON-Schema artifact. Markdown agent defs evolve freely; YAML would freeze a surface we'd inherit maintenance on.
2. **Prompt templating: Go `text/template` syntax.** When an agent prompt needs variable interpolation, use `{{.Var}}` / `{{.Scope.Var}}` resolved by `text/template` (stdlib), not ADK's custom `{var}` regex. A prompt file *may* carry a companion data-dictionary block (YAML frontmatter or sibling `<name>.vars.yaml`) documenting each variable's name, type, source, and whether it's required — optional for MVP, promotable to a schema later.
3. **ADK adoption conforms to (1) and (2).** When a capability ADK already figured out (state-injection semantics, optional variables, static/dynamic split for caching, transfer-prompt generation, evaluation rubrics, etc.) earns its way into Locutus, we **copy the design** — not the code — and rewrite it against our markdown + `text/template` surface. Attribution still applies per DJ-077 (top-of-file comment + NOTICE entry) when the translation is faithful enough that ADK's design is recognizably load-bearing.

**Why `text/template` over ADK's `{var}`:**

- Go stdlib. Zero dependency, zero port cost. Already used by Go authors across the ecosystem; no new syntax to teach.
- `{{ }}` delimiters collide less with natural prose in agent instructions than `{ }`. ADK mitigates the collision with a non-identifier passthrough; we avoid the class of problem entirely.
- Templates compile once and validate variable references at parse time, surfacing typos before the first LLM call. ADK's regex substitutes silently (`{typo}` → empty string with `{var?}`, else `KeyError`).
- Richer constructs when we need them (`{{if}}`, `{{range}}`, `{{with}}`) without reaching back to ADK.

**Why keep markdown agent defs:**

- Every existing agent def in `internal/scaffold/agents/` and `.borg/agents/` is a markdown file today. Migrating to YAML would be a forced churn with no operational payoff.
- Markdown is legible to humans reviewing agent behavior in PRs. YAML with quoted instruction strings is not.
- Frontmatter (YAML block at top of a markdown file) already gives us a structured-metadata slot when needed — for the optional data dictionary, or future fields like `model:`, `description:`, `tags:` — without committing to ADK's full schema.

**What "copy the design, not the code" looks like:**

- If we later want ADK's static/dynamic instruction split for prompt caching, we implement it in Go against our markdown files: a frontmatter field `static_instruction: true` routes the body to the cacheable slot; dynamic bodies flow through `text/template`. We do **not** port `flows/llm_flows/instructions.py` line-for-line.
- If we later want ADK's `{var?}` optional-variable semantics, we implement it as a `text/template` FuncMap helper (`{{optional .var}}`), not by swapping template engines.
- If we later want sub-agent transfer prompts, we generate them in Go from our own `sub_agents:` frontmatter field — ADK's `_build_transfer_instruction_body` is the *shape* we match, not the source we ship.

**Interaction with existing DJs:**

- **DJ-036 (shipped):** unchanged. Agent defs remain external markdown.
- **DJ-077 (settled):** refined. Selective adoption still permitted; DJ-078 adds the constraint that adopted designs translate to Locutus's markdown + `text/template` surface rather than bringing ADK's serialization and templating conventions with them.
- **DJ-029 (shipped):** unchanged. Custom orchestration posture holds.

**Not in scope for DJ-078:**

- Immediate port of `inject_session_state`. Whether to land a `text/template`-based interpolation helper as a second pre-Round-3 increment (alongside memory) is a separate scheduling call, not a policy question. Today, no agent prompt in `internal/scaffold/agents/` uses variables; we ship templating when the first real use case lands.
- A committed data-dictionary schema. The optional companion block is permitted but unspecified until a concrete agent needs it.
- Any constraint on adopting ADK patterns *outside* agent definition and prompt formation (memory, evaluation, etc.) — those fall under DJ-077's general adoption posture.

**Reversal criteria:**

DJ-078 gets revisited if: (a) an ADK capability we need cannot be faithfully expressed against markdown + `text/template` without significant contortion; or (b) we discover operational need for a declarative agent config format that multiple tools (Locutus + external) must parse identically — at that point a shared YAML surface may earn its way in.

## DJ-079: `refine goals` Generates the Spec Graph from GOALS.md

**Status:** shipped

**Decision:** `locutus refine goals` is the entry point for greenfield spec generation. It reads `GOALS.md`, calls a council-driven `agent.GenerateSpec` (proposer + critic + revise), and persists the resulting features, decisions, strategies, and approaches via the existing assimilation persistence layer. Re-running is incremental: matching IDs update in place; new IDs land as new files. The Goals root node ID is reserved as the literal string `"goals"` (not `"GOALS.md"` as before) so users can address it directly on the CLI.

**Why `refine` rather than a new verb:** `refine` already means "council-driven deliberation on any spec node" (DJ-069). Goals is a node (KindGoals); deliberating on it = deriving its children (features, strategies, decisions, approaches). Adding a `plan` verb would inflate the 8-verb surface. The semantic stretch is small: refine cascades changes through an existing graph, and `refine goals` is the same operation at the top of the tree — generate or update the children to reflect the parent.

**Why not fold into `assimilate`:** `assimilate` is for inferring spec from *code*. Its agents (backend_analyzer, frontend_analyzer, infra_analyzer) are tuned for code analysis. A docs-only or greenfield repo runs `assimilate` and produces thin, mis-shaped output. The two flows have different inputs and different LLM-side prompts; conflating them would dilute both.

**Composability with `import`:** `import <doc>` runs the same internal pipeline (`runSpecGeneration`) post-admission. The shared call site means a user can iterate: edit GOALS.md → `refine goals` to seed, then `import docs/feature-X.md` for each design doc → `import` extends the existing graph rather than re-introducing nodes. `--no-plan` opts out for admission-only.

## DJ-080: `models.yaml` Follows the Embedded-Then-Editable Pattern

**Status:** shipped

**Decision:** `internal/agent/models.yaml` is `//go:embed`-ed into the binary as the source of truth, but `locutus init` writes a copy to `.borg/models.yaml` so users can edit per-project model preferences. `LoadModelConfig` reads with this precedence: (1) `LOCUTUS_MODELS_CONFIG` env var, (2) `.borg/models.yaml` walked up from cwd, (3) embedded defaults.

**Why scaffold instead of env-var-only:** consistent with DJ-036's "embedded-then-editable pattern" already used for council agents and workflow YAML. A user who wants to flip Anthropic-first for the strong tier should be able to edit a file in the repo, not set an environment variable. `locutus update` (the analogue refresh path for embedded artifacts) is the seam for picking up upstream changes.

**Why precedence puts the project file ABOVE the env var:** it doesn't — env var wins. Rationale: env-var override is the explicit signal ("I want this specific path"), the project file is the implicit default. CI / shared environments / power users get the env var; everyone else gets the project file.

## DJ-081: Project-Root Walk-Up for All Subcommands Except `init`

**Status:** shipped

**Decision:** Every subcommand except `init` resolves its filesystem root by walking up from the current working directory until it finds `.borg/manifest.json` (the marker scaffolded by `init`). Reaching the filesystem root without finding it returns `ErrNotInProject` with a friendly "run `locutus init` here, or cd into an existing project" message. `init` deliberately stays cwd-rooted because that's the bootstrap step.

**Why walk-up, not cwd-only:** before this, running any subcommand from a subdirectory would either error (no `.borg/`) or write a fresh `.borg/` in the wrong place. Standard tools (git, cargo, npm) walk up; users expect Locutus to do the same.

**Why `.borg/manifest.json` as the marker:** it's persistent (lifetime of the project), already written by `init`, and the JSON content carries authoritative metadata so a misplaced empty `.borg/` directory doesn't masquerade as a project root.

## DJ-082: Spec Generation Uses Single-Pass Council, Not the Planner Workflow

**Status:** superseded by DJ-083

**Decision (historical):** `agent.GenerateSpec` runs a lightweight council inline — proposer LLM call, then 0..N critic-and-revise rounds — rather than going through the existing `agent.Plan` planning workflow (which uses `WorkflowExecutor` + `planning.yaml`). Default critique rounds = 1 from the cmd-layer entry point.

**Reversal:** DJ-083 supersedes this. Spec generation now uses externalized agent definitions and a dedicated workflow YAML, the same model the planning council uses. The triggering observation was multi-agent expansion — once the council grew to six members (scout + architect + four specialist critics), the inline approach turned every prompt into a Go string constant and every tuning knob into a recompile. The reversal criteria DJ-082 set out ("when a graph-generator workflow is added to planning.yaml") were met as soon as it was cheaper to externalize than to keep maintaining the inline shape.

## DJ-083: Spec Generation Uses Externalized Agents + Dedicated Workflow YAML (Supersedes DJ-082)

**Status:** shipped

**Decision:** Spec generation runs through `WorkflowExecutor` against six agent definitions in `internal/scaffold/agents/` (`spec_scout.md`, `spec_architect.md`, `architect_critic.md`, `devops_critic.md`, `sre_critic.md`, `cost_critic.md`) and a workflow YAML in `internal/scaffold/workflows/spec_generation.yaml`. `locutus init` writes both into `.borg/agents/` and `.borg/workflows/`; the runtime loads from there on every invocation. Editing those files tunes the council without rebuilding.

**Workflow shape:**

- `survey` — `spec_scout` produces a `ScoutBrief` (domain read, technology options, implicit assumptions, watch-outs).
- `propose` — `spec_architect` produces a `SpecProposal`, with the scout brief folded into its user message via `projectPropose`.
- `critique` — four specialist critics (architect, DevOps, SRE, cost) run in parallel. Each emits `CriticIssues`; `merge_as: critic_issues` flattens each issue into a `Concern` attributed to the critic's role.
- `revise` — `spec_architect` again, conditional on `has_concerns`. Sees the proposal and the critic concerns via `projectRevise`.

**Why the four critics:** the original single-critic design caught dangling references (the most common proposer failure) but missed entire classes of weakness — deployment coherence ("this can't actually run on Vercel"), operational reality ("no on-call model named"), cost runaway ("BigQuery + Datadog + Vercel Pro will blow through the stated budget"). Each specialist has its own rule set; their union is a meaningfully tougher review than any single generalist.

**Why a scout pre-step:** the proposer working from goals + training distribution defaults to its priors. A scout brief that explicitly lists *implicit assumptions* (scale, cost, ops model, deployment posture, availability, compliance) and *technology options with tradeoffs* gives the proposer a concrete frame to react to. The proposer is then mandated to commit to each implicit assumption as a strategy + decision pair — turning unstated assumptions into first-class spec nodes.

**Schema enforcement:** `ScoutBrief`, `SpecProposal`, and `CriticIssues` are registered in `schemas.go` so `BuildGenerateRequest` wires them through Genkit's structured-output path. Each agent's response is JSON-by-construction at the API layer, not parsed out of free-form text.

**Cost envelope:** one full pass = 6 LLM calls (1 scout + 1 architect + 4 critics) when the proposal is clean, 7 when it isn't. Per-agent model tier comes from the agent's frontmatter (architect: strong, scout: balanced, critics: balanced) so the strong-tier cost is bounded to one (or two on revise) calls per invocation. Multi-round critique requires a `convergence` agent and `max_rounds > 1`; the default workflow ships with `max_rounds: 1`.

**PlanningState extensions to support this:** added `ScoutBrief string`, plus `merge_as: scout_brief` and `merge_as: critic_issues` cases in `mergeResults`. `projectPropose` was updated to fold the formatted scout brief into the proposer's user message; `projectChallenge` now also handles the `critique` step ID. These are minimal, additive changes — the existing planner workflow is unaffected.

**Reversal criteria:** DJ-083 stays unless either (a) a single-pass design with a stronger model demonstrably matches the four-critic output (would let us drop ~3 LLM calls per invocation), or (b) the council grows beyond what `WorkflowExecutor` + `PlanningState` can express cleanly (would need a generic state type or a parallel executor). Neither is currently in sight.

## DJ-084: `dominikbraun/graph` Is the Canonical Graph Library; Spec and Executor Share It

**Status:** shipped

**Decision:** Both `internal/spec` (the spec dependency graph) and `internal/executor` (the runtime DAG) hold their graph structures in [`github.com/dominikbraun/graph`](https://github.com/dominikbraun/graph). Cycle detection, predecessor and adjacency lookups, and topological sort all delegate to the library. Hand-rolled equivalents are not allowed.

**What runs in our code instead:**

- Runtime semantics — `RunStep`, `Merge`, `Snapshot` callbacks; the convergence loop; conditional steps; the parallel/sequential split per wave; `MaxConcurrency` and `TypeLimits` enforcement; the events channel.
- Domain semantics — what a vertex *means* (a `Step`, a `Feature`, a `Decision`), what an edge *means* (dependency, parent-child), how nodes are persisted.

The graph library is an implementation detail of the storage and traversal layer, not a leaky abstraction the runtime is built around.

**Why one library, not two (or one library + a hand-rolled twin):** before this refactor, `internal/spec/graph.go` already used the library, but `internal/executor/dag.go` had its own ~150 lines of "track a `completed` map, scan for steps whose `DependsOn` are all in `completed`" wave scheduling. That code worked, but it duplicated graph machinery the spec graph wasn't reinventing — and crucially, it surfaced cycles mid-run as a generic "deadlock: N incomplete" error rather than at config time with the cycle's vertices named.

The previous defense ("the executor is execute-and-mutate, not a query structure, so a graph library doesn't help") conflated two layers. The graph itself is a passive structure either way; what makes the executor a runtime is the callback layer, the scheduling policy, and the convergence loop — none of which the library is asked to provide.

**Concrete wins from sharing the library:**

- Cycle detection at config time, not as a runtime symptom. The error surface goes from "deadlock: 3 incomplete" to "dependency cycle reaches step X via Y."
- Duplicate step IDs and edges to undeclared steps are caught at the same checkpoint, courtesy of `dgraph.PreventCycles` + a small upfront validation pass.
- Future improvements to the library (faster topological sort, deterministic ordering via `StableTopologicalSort`, memory layout work) land in both the spec graph and the executor without further effort.
- Less hand-coded graph bookkeeping to audit. The wave-selection function (`readySteps`) is now ~10 lines that filter `PredecessorMap()` against a `completed` set.

**What the library is NOT asked to do:**

- Express runtime parallelism or scheduling policy. Both stay in our code.
- Hold typed state. The `State[S]` parameterization is independent of the graph.
- Drive the convergence loop. That's a callback in `Config[S]`.

**Reversal criteria:** DJ-084 reverses only if `dominikbraun/graph` makes a breaking change we can't follow, or if a graph-shape requirement emerges that the library can't express (e.g., weighted edges for prioritization, or hyperedges for grouped dependencies). Neither is currently in sight; the library has been stable and our usage is mainstream.

**Note on DJ-029:** DJ-029's "custom orchestration, ~350 LOC, not a generic framework" framing remains correct for the *runtime semantics* layer — that's still the executor's identity. DJ-084 specifies that within that boundary, graph storage and traversal are library-backed; "custom orchestration" never meant "custom graph data structure."

## DJ-085: Decisions Denormalize Their Justification; Session Transcripts Are Debug-Only

**Status:** shipped

**Decision:** A council-generated `spec.Decision` carries its own justification record on the persisted node. New types `spec.Citation` and `spec.DecisionProvenance` are populated by the architect at proposal time and survive on disk under `.borg/spec/decisions/<id>.{json,md}`. Each citation is `{kind: "goals" | "doc" | "best_practice" | "spec_node", reference, span?, excerpt?}` with the verbatim excerpt persisted alongside the reference, so a citation survives the cited file moving or being rewritten. Every council-generated decision MUST carry at least one citation and a one-sentence `architect_rationale`; the architect critic flags violations.

**Why denormalize, not point at the session file:** session transcripts live under `.locutus/sessions/<date>/<time>/<sid>.yaml`, which is gitignored and explicitly ephemeral debug context. An earlier sketch of this feature stored a `SessionID` on each Decision so a future tool could load the full council exchange. That made the Decision's justification load-bearing on a file the user is encouraged to delete — exactly the wrong durability story for the spec graph, which is supposed to be the project's authoritative record.

The denormalized shape solves it cleanly: the citations + the architect's own reason are persisted on the spec node. The session file remains useful for full-fidelity debug (the verbatim prompts, the critic exchange, the revise round) but its absence costs nothing structural. The same posture as `models.yaml` (embedded source of truth + editable `.borg/` copy) and like git's commit object versus the working tree.

**What lands on each decision:**

- `Citations []Citation` — at least one entry. Each citation grounds the decision in something traceable: a span of GOALS.md, a doc the user imported, a named precise best practice ("12-factor app: stateless processes" — not "industry best practices"), or another spec node. Excerpts are persisted verbatim.
- `ArchitectRationale string` — one short sentence summary, distinct from the longer prose `Rationale` field. The audit-scan version of "why."
- `SourceSession string` — non-load-bearing pointer at the transcript file. Empty when the decision was not council-generated. The `justify` verb (when added) reads it as a hint; nothing breaks when the file is gone.
- `GeneratedAt time.Time` — stamped by `normalizeDecision` at persist time so future audits know how stale the provenance record is.

**What the architect's prompt requires:** every decision MUST emit at least one citation. Vague rationale without a citation is a critic flag (architect_critic rule 6). Best-practice citations must name something precise — vague appeals to "good engineering" don't satisfy the rule.

**Carve-outs:** decisions that did NOT come from the council (hand-authored by the user, inferred by `assimilate` from existing code, etc.) leave `Provenance` nil rather than carrying a hollow `Provenance{}`. Distinguishable from "council ran and returned nothing." Future `assimilate` work can populate Provenance with `kind: "spec_node"` self-references where appropriate, but the current path is to leave it empty.

**Reversal criteria:** DJ-085 reverses only if (a) we move sessions into source control (would make the pointer durable, but bloats the spec with multi-KB transcripts per refine — not on the table), or (b) the citation field set proves insufficient (would extend the schema, not abandon denormalization).

**Note for `justify` verb (forthcoming):** The verb reads `Provenance.Citations` directly to produce a defense report. When `SourceSession` resolves to an existing file, it can pull the full council exchange as supplementary context. When it doesn't, the durable Citations + ArchitectRationale + Alternatives + Rationale already in the spec are sufficient — the decision defends itself.

## DJ-086: `update` Has Two Orthogonal Flags — `--reset` and `--offline`

**Status:** shipped

**Decision:** `locutus update` grows two independent flags that compose:

- `--reset` overwrites the project's scaffolded artifacts (`.borg/agents/*.md`, `.borg/workflows/*.yaml`, `.borg/models.yaml`) with the running binary's embedded versions. User content (`GOALS.md`, `.borg/spec/`, `.borg/history/`, `.borg/manifest.json`, `.locutus/`) is never modified.
- `--offline` skips the GitHub release check and download.

The four meaningful combinations:

| Command | Behavior |
| --- | --- |
| `update` | Check, download newer binary if available. Local files untouched. |
| `update --reset` | Check + download. If a download happened, refuse to reset (the running process still has old embedded artifacts) and tell the user to re-run `update --offline --reset` against the new binary. Otherwise, reset using the current binary. |
| `update --offline` | No-op with a friendly message — paired with `--reset` is the useful form. |
| `update --offline --reset` | Reset only; no network. The canonical "I just upgraded the binary, refresh my project files" command. |

**Why `--reset` is opt-in, not the default:** users edit `.borg/agents/*.md`, `.borg/workflows/*.yaml`, and `.borg/models.yaml` to tune their council and model preferences (DJ-036, DJ-080). Silently overwriting those edits on a casual binary update would surprise people. Default `update` has the narrow, predictable scope of "make the binary current"; refreshing local files requires explicit consent.

**Why two flags rather than one combined verb:** the verb-level question is "are you trying to upgrade the install?" The answer is yes either way; the flags scope what "upgrade" means in this invocation. Keeping the two operations independently togglable also covers the offline-and-reset case (which is the most common follow-up to a binary download) without inventing a third flag for it.

**Why we don't re-exec after a download to apply `--reset` immediately:** technically possible (`syscall.Exec` would replace the running process with the freshly-downloaded binary), but it's a real behavioral surprise — environment, signal handlers, and stdout/stderr buffering all behave differently across an exec. A clear "download succeeded; run `update --offline --reset` to refresh project files" message is less clever and less surprising. We can revisit if the two-step pattern becomes friction in practice.

**What `Reset` does NOT do:**

- It doesn't delete files. An agent that was in the embed in v1.0 but removed in v1.1 stays on disk after a v1.0 → v1.1 upgrade unless the user removes it manually. A future "prune" mode is its own decision.
- It doesn't touch agent files the user added that aren't in the embed (custom agents survive).
- It doesn't touch user-content directories (specs, history, manifest, runtime state, GOALS.md).

**Reversal criteria:** DJ-086 reverses only if either (a) the friction of "download finished; run --offline --reset" becomes a real complaint and re-exec ergonomics improve, or (b) we decide reset should be the default (would only happen if we had a strong story for preserving user edits across overwrite — e.g., a per-file "user-modified" flag the runtime tracks).

## DJ-087: Approaches Are Synthesized at Adopt Time, Not Refine Time

**Status:** shipped

**Decision:** The spec-generation council (`refine goals`, `import`) no longer emits Approach nodes. The `SpecProposal` JSON contract drops `approaches[]` entirely, along with `Feature.Approaches` and `Strategy.Approaches` cross-reference arrays. Approach synthesis moves to `adopt`: when the reconciler encounters a Feature or Strategy in scope that has no Approach attached, it invokes the existing single-approach synthesizer to produce one on demand, persists it as `app-<parent-id>.md`, and updates the parent's `approaches[]` slice on disk.

**Why:** A real `refine goals` run on `winplan` (GOALS.md ~8 lines) with `googleai/gemini-3.1-pro-preview` failed three Pro Preview calls in a row to produce a referentially-clean `SpecProposal` even with mechanical, prescriptive integrity-revise prompts. The hard-fail behaviour from `5a42eb2` correctly surfaced the failure but didn't address the root cause: the architect was being asked to emit a single 2k-token JSON blob with ~30 cross-references that JSON Schema cannot enforce, all maintained by attention alone. Stronger models tolerate this; the open-source Gemini Flash / Claude Haiku tier we want to support does not.

Approaches were the worst offenders in that load: every approach needs a `parent_id` resolving to a feature or strategy, and every feature/strategy carries an `approaches[]` cross-ref array. CLAUDE.md already framed approaches as "the synthesis layer for coding agents" — implementation sketches that bridge spec and code. They need code context. During refine that context doesn't exist; the architect invents the sketch, and those invented sketches drive a substantial fraction of the dangling-ref problem.

**How approaches reach disk:** `adopt` already classifies approaches (live/drifted/unplanned/failed). The single-approach synthesizer at [cmd/refine.go](../cmd/refine.go)'s `invokeSynthesizer` already takes a parent's prose plus applicable decisions and returns a `RewriteResult.RevisedBody`. The new path in [cmd/adopt_synthesize.go](../cmd/adopt_synthesize.go) walks the spec graph for parents in scope with empty `Approaches`, calls the synthesizer per parent, persists the result via `specio.SaveMarkdown`, and updates the parent JSON. Re-runs are idempotent: the deterministic ID `app-<parent-id>` collides on re-run and we skip parents whose `Approaches[]` already names the new ID.

**Out of scope:** the on-disk shape under `.borg/spec/` is unchanged (DJ-085 stability). `spec.Feature.Approaches` and `spec.Strategy.Approaches` stay; only the LLM-facing `SpecProposal` types lose them.

**Migration:** existing projects with persisted approaches keep them — `adopt` only synthesizes when `Approaches` is empty for a parent. The architect agent file at `.borg/agents/spec_architect.md` ships via `locutus init` (scaffold). Existing projects keep their old version until they re-init or run `locutus update --offline --reset`.

**Reversal criteria:** revert if (a) per-parent synthesis at adopt time has materially worse cost or wall-clock than the single-call architect path it replaces *and* the architect path becomes reliable on weak models (unlikely without Phase 2's outline → fanout decomposition), or (b) the deterministic `app-<parent-id>` ID scheme collides with user-authored approach IDs in practice — at which point we add a numeric suffix or move ID assignment into the synthesizer agent.

**Reference:** plan at [.claude/plans/council-resilience.md](../.claude/plans/council-resilience.md), Phase 1.

## DJ-088: Architect Emits Inline Decisions; Reconciler Assigns IDs Post-Hoc

**Status:** shipped

**Decision:** The spec-generation council's architect (`spec_architect`) no longer emits a flat `decisions[]` array with shared IDs that features and strategies cross-reference. Instead, it emits a `RawSpecProposal`: features and strategies, each with their decisions **inline** as embedded objects with no IDs. A new reconciler agent (`spec_reconciler`) clusters duplicate or conflicting inline decisions across the proposal and emits a `ReconciliationVerdict` (action kinds: `dedupe`, `resolve_conflict`, `reuse_existing`); a deterministic Go function `ApplyReconciliation` consumes the verdict + raw proposal + existing-spec snapshot and produces the canonical `SpecProposal` with shared, slug-derived IDs that downstream agents and the persistence layer continue to expect.

**Why:** Phase 1 (DJ-087) dropped approaches and fixed half the dangling-ref problem. A post-Phase-1 winplan run on `googleai/gemini-3-flash-preview` confirmed the remaining failure mode: 23 dangling references in the integrity gate, all in `feature.decisions[]` and `strategy.decisions[]` cross-references between separate top-level arrays. The architect was juggling ~20 cross-array references in attention while generating prose — a load weaker models can't keep coherent.

The structural fix is to remove cross-references from the architect's output entirely. Each parent carries the decisions it requires inline. The reconciler's job is the cross-cutting view: where the architect has duplicated itself, dedupe; where it has contradicted itself, resolve. The architect's prompt collapses (half its mandates were referential-integrity rules); cognitive load drops without splitting the call into per-node fanout.

**Why this beats alternatives.** Two were considered and rejected:

- **Decisions-first decomposition** (one architect call for decisions, then one for structure that references them by id) — chicken-and-egg: the architect can't know which decisions to make until it knows what features and strategies need them.
- **Per-node fanout** (Phase 3 in the plan) — splits the architect call into one elaboration per feature and one per strategy, with a reconciler converging the output. Eliminates cross-call coordination but introduces a new workflow primitive (`fanout`) and a third agent (`spec_outliner`). Phase 2's inline-decisions design solves the cross-reference problem without the additional surgery; fanout becomes a clean escalation if a single architect call still degrades on big projects, since the reconciler doesn't change.

**The flow:**

```
survey → propose (raw) → reconcile → critique → revise (raw, conditional) → reconcile_revise (conditional)
```

The architect always emits `RawSpecProposal`; both `propose` and `revise` go through the same reconciler. The integrity-revise loop in `GenerateSpec` becomes a vestigial backstop — there are no cross-references in the architect's output to dangle, and `ApplyReconciliation` is deterministic and structurally cannot produce a malformed proposal.

**ID assignment.** Reconciler-assigned, slug-derived from the canonical decision title (`dec-use-postgres`, `dec-async-ingest`). Collisions across decisions whose titles slugify identically get a numeric suffix (`-2`, `-3`). Architects can't fabricate IDs because the architect contract has no ID field on `InlineDecisionProposal`.

**Existing-spec ID reuse.** When extending a spec, the reconciler sees `Existing.Decisions` and can mark a cluster `reuse_existing` with an existing decision's ID. `ApplyReconciliation` rewrites the parent's `decisions[]` to reference the existing ID without minting a new canonical decision.

**`InfluencedBy` dropped from the architect contract.** The field was an inter-decision reference — the same cross-reference problem inline decisions were designed to eliminate. Influence relationships, when they matter, are added during refine, not greenfield generation.

**Cascade rewrite on conflict.** When the reconciler resolves a conflict, the architect's prose for affected feature/strategy nodes was written under the loser. After persistence, `cmd/specgen.go::cascadeAfterReconcile` reloads each affected node and runs `cascade.InvokeRewriter` (a new exported variant of the rewriter that operates in-memory) to align the prose with the canonical decision set. Best-effort: a rewriter failure logs but doesn't roll back the spec.

**Migration:** the architect agent at `.borg/agents/spec_architect.md` and the workflow YAML at `.borg/workflows/spec_generation.yaml` ship via `locutus init`. Existing projects keep their old versions until they re-init or run `locutus update --offline --reset`. The on-disk spec shape under `.borg/spec/` is unchanged (DJ-085 stability).

**Reversal criteria:** revert if (a) the reconciler routinely over-merges (collapses compatible-but-distinct decisions into one) or under-merges (leaves obvious duplicates separate) at a rate that materially degrades spec quality on `gpt-class` and `claude-sonnet-class` models — at which point the design moves to fanout (Phase 3) where each call's clustering surface is bounded; or (b) the per-run cost of the extra reconcile call (one for clean runs, two when revise fires) outweighs the savings from dropped integrity-revise retries on the model spectrum we care about.

**Reference:** plan at [.claude/plans/council-resilience.md](../.claude/plans/council-resilience.md), Phase 2. Builds on DJ-087.

## DJ-089: Mechanical Integrity Critic + Directive Revise Prompt

**Status:** shipped

**Decision:** The spec-generation council's critique step gains a non-LLM integrity critic (a Go function that runs `SpecProposal.Validate` against the post-reconcile proposal and emits findings as Concerns with `Kind="integrity"`). The revise projection (`projectRevise` in [internal/agent/projection.go](../internal/agent/projection.go)) is rewritten in the directive shape used by the post-workflow integrity-revise prompt: explicit rejection ("STOP. Your previous RawSpecProposal is rejected"), findings grouped by Kind (architecture / cost / devops / integrity / sre), prescriptive per-kind action lists, explicit don'ts, and a directive to re-emit the COMPLETE corrected RawSpecProposal. Critic concerns now carry a `Kind` field on `Concern`, defaulted from the agent ID at merge time (`architect_critic` → "architecture", etc.).

**Why:** Even with Phase 2 (DJ-088) eliminating the structural cause of dangling references, two failure modes remained on the in-workflow critique → revise hop:

1. Free-form prose findings made the architect's job to interpret. The original winplan run had `architect_critic` flag the integrity issue verbatim, and revise ignored it — the prompt was diluted enough that the architect didn't act on it. The directive revise prompt fixes the dilution problem the same way `78da6b5` ("sharper integrity-revise prompt") fixed it for the post-workflow loop: make rejection explicit, enumerate the violations, name the actions.
2. Integrity violations were only caught after the workflow finished, by the post-workflow integrity loop. That loop runs synchronously, costs LLM tokens for the architect retry, and (per the user's earlier complaint) renders as a silent multi-second pause in the CLI. Catching integrity issues during critique means revise addresses them in the same flow that revise addresses architecture/devops/SRE/cost concerns.

After Phase 2, integrity issues should be rare in the common case — `ApplyReconciliation` produces structurally clean output by construction. The integrity critic is then load-bearing only on regressions: a malformed reconciler verdict, a future code change that re-introduces cross-references, etc. Cheap enough (one Go function call per critique merge) that running it always costs nothing in the common case.

**Why a Go function, not an LLM critic.** Validate is mechanical. An LLM can't do it more accurately than `Validate` can. Spending LLM tokens to re-derive a fact already encoded in code would be wasteful. The integrity critic is a peer to the LLM critics in the workflow's mental model (it appears as a Concern with AgentID="integrity_critic" alongside architect_critic etc.), but its implementation is `appendIntegrityFindings` in [internal/agent/workflow.go](../internal/agent/workflow.go) — invoked from `mergeResults` after the LLM critic results merge.

**Why the architect sees its prior RawProposal in revise, not the canonical SpecProposal.** Phase 2 made the architect's output a `RawSpecProposal` (inline decisions, no IDs). The reconciler transforms that into the canonical `SpecProposal` with assigned IDs. Critics see the canonical. The previous `projectRevise` showed the architect the canonical via the assistant message — implying "you produced this", which the architect did not. The new projection surfaces `state.RawProposal` as the assistant message instead, so the rejection language is unambiguous: "you said X; critics flagged Y; emit X' addressing Y." The architect then emits a corrected RawSpecProposal which goes through `reconcile_revise`.

**Reversal criteria:** revert the integrity critic if it produces noise (false positives) on clean Phase 2 output — that would suggest a bug in `Validate` or `ApplyReconciliation`, not a reason to remove the critic. Revert the directive revise prompt if it materially degrades architect compliance on Pro-class models (unlikely; the directive shape was already proven on `reviseForIntegrity`).

**Reference:** plan at [.claude/plans/council-resilience.md](../.claude/plans/council-resilience.md), Phase 5. Independent of Phases 1–4; lands alongside Phase 2 to keep the in-workflow path tight.

## DJ-090: Outline + Per-Node Elaborate Fanout (Council Resilience Phase 3)

**Status:** shipped

**Decision:** The spec-generation council's single architect call is replaced by a three-step shape:

1. **Outline** (1 LLM call) — `spec_outliner` emits an `Outline` JSON: feature and strategy titles + one-line summaries only. No decisions, no detailed descriptions. The outline IS the spec's structural skeleton.
2. **Elaborate fanout** (N+M LLM calls in parallel) — for each outlined feature and strategy, a per-node elaborator (`spec_feature_elaborator`, `spec_strategy_elaborator`) emits the full `RawFeatureProposal` / `RawStrategyProposal` with inline decisions for that one node. Per-call output is bounded by node complexity, not project size.
3. **Assemble + Reconcile** — the merge handler stitches the per-element outputs into a full `RawSpecProposal`; the existing Phase-2 reconciler (DJ-088) consumes it unchanged and produces the canonical `SpecProposal`.

The reconciler doesn't change. Only the upstream call topology changes. Critique and revise downstream are also unchanged.

A new `fanout` field on `WorkflowStep` names a state path (`outline.features` / `outline.strategies`); `WorkflowExecutor.ExecuteRound` spawns one agent invocation per element, threading the element JSON through `StateSnapshot.FanoutItem` for the projection function to render. Per-model concurrency caps (new `concurrent_requests` knob in `models.yaml`) bound actual parallelism so fanout never floods a model past its rate-limit window.

**Why:** A real winplan run on Pro Preview (April 30 2026) truncated mid-JSON at 64k output tokens during the revise step — i.e., the model couldn't fit a corrected RawSpecProposal for a moderately-sized multi-deliverable spec into its output budget. That's not a knob-tuning problem; it's a structural one. The architect was being asked to emit too much per call.

Phase 3's per-node fanout collapses each call's output to ~one node's worth of JSON (4–8k tokens regardless of project size). Truncation stops being a recurring failure mode; parallelism makes wall-clock better; one bad elaborator output retries one node instead of the whole proposal. The plan considered this in advance — Phase 3 was written ahead of Phase 2 with this exact failure mode in mind.

**Why we waited.** The plan's sequencing recommended Phases 1+2 first, then Phase 3 only if measured failure said so. We measured: Phase 1+2 alone weren't enough on Pro Preview at 64k cap. Phase 3 was the next move, not a hedge.

**Why concurrency caps belong in models.yaml.** Without per-model throttling, a 10-feature project would fire 10+ concurrent calls and trip free-tier or preview-model rate limits, then stall on backoff retries. The cap belongs in YAML (the user can tune per project, per quota tier) rather than as a compiled-in constant. Embedded defaults: 2 for preview models (3-flash-preview, 3.1-pro-preview), 4 for stable Gemini Flash, 5 for Claude Haiku, 3–4 for Sonnet/Opus. Generous on paid tiers; conservative on previews.

**What stays the same:**

- Reconciler agent and `ApplyReconciliation` logic (DJ-088).
- Critique + revise + reconcile_revise flow (DJ-088, DJ-089).
- Cascade rewrite on conflict-resolution actions.
- Integrity critic in the critique merge pass (DJ-089).
- `RawSpecProposal` shape (just emitted incrementally instead of all-at-once).

**What's new:**

- `Outline`, `OutlineFeature`, `OutlineStrategy` types in [internal/agent/elaboration.go](../internal/agent/elaboration.go).
- Three new agents: `spec_outliner.md`, `spec_feature_elaborator.md`, `spec_strategy_elaborator.md`.
- `WorkflowStep.Fanout` field and `extractFanoutItems` resolver.
- `StateSnapshot.FanoutItem` and `StateSnapshot.Outline` for per-element projection.
- `assembleRawProposal` Go function — stitches fanout outputs into `state.RawProposal` (best-effort; malformed elaborator outputs are dropped with a slog warning rather than aborting the assembly).
- `ModelKnobs.ConcurrentRequests` and the per-model semaphore in `GenKitLLM`.

**Reversal criteria:** revert if (a) the outliner's per-item summaries are so thin that elaborators systematically fail to commit on coherent decisions — at which point the outline schema needs richer per-item context, not abandonment of fanout; or (b) the per-model concurrency caps are a meaningful bottleneck for paid-tier users on big projects — at which point we make the caps tier-aware rather than hard-coding defaults. Neither failure mode is structural; both are tunable.

**Reference:** plan at [.claude/plans/council-resilience.md](../.claude/plans/council-resilience.md), Phase 3. Builds on DJ-088 (Phase 2's reconciler is reused unchanged) and DJ-089 (Phase 5's critic sharpening still applies to the post-reconcile critique).

## DJ-091: Session Trace Storage Is a Per-Call File Layout

**Status:** shipped

**Decision:** A session is a directory, not a file. `SessionRecorder` writes:

```text
.locutus/sessions/<YYYYMMDD>/<HHMM>/<SS>-<short>/
├── session.yaml          # manifest: session_id, started_at, completed_at, command, project_root
└── calls/
    ├── 0001-spec_scout.yaml
    ├── 0002-spec_outliner.yaml
    └── …                # one YAML file per LLM call
```

`Begin` writes the per-call file with `status: in_progress` and the input messages; `Finish` rewrites the same file with response/error/tokens/raw_message and drops the in-memory handle. Every flush is bounded to one per-call file (atomic via tmp + rename). The recorder no longer holds the cumulative `session.Calls[]` slice in memory — the directory listing IS the calls list. A new optional `Close` stamps `completed_at` on the manifest and flips any still-in-flight calls to `status: interrupted` for clean shutdown; sessions that crash without Close leave `completed_at` absent on disk, which itself is diagnostic.

**Why:** Three shipped changes turned a once-fine single-file format into a structural problem:

1. **Phase 3 fanout (DJ-090)** produces 15–25+ calls per session as a baseline. Adopt with dozens of workstreams could push this much higher. Single-file rewrite is O(N) per flush, total work O(N²) over a session.
2. **`raw_message` capture** added per-call YAML payloads of ~5–50KB each (truncated/looped Gemini outputs are the largest offenders). 17 calls × 10KB avg meant rewriting ~170KB on every state transition.
3. **Crash-mid-call ergonomics.** The session trace exists so an operator can debug LLM activity — prompt issues, tool-call traces, degenerate loops. A SIGKILL between Begin (input flushed to memory) and Finish (output captured) used to lose *exactly* the in-flight call most worth debugging. The atomic-rewrite property protected against partial-file corruption but not against process death between flushes. Now the input messages land on disk before Begin returns, so the prompt that hung is preserved.

The shape is the smallest change that fixes all three: per-call files give bounded flush size (one call's content), bounded memory (in-flight working set only — realistically ≤10 calls under per-model concurrency caps), and crash-survivable inputs (one fsync per Begin).

**Why not streaming.** True mid-call durability — capturing chunks as the LLM emits them — would require switching to genkit's streaming mode and a per-call append-log sidecar. That's real engineering work for a contingent benefit. Today's middleware-after-return capture already records `raw_message` for the truncation/loop cases we've actually hit. The streaming-aware path is scoped as Phase 2 of the persistence plan and deferred until measured failure says otherwise.

**Why not migrate old sessions.** Nothing reads single-file sessions programmatically; existing files on disk stay readable by hand. The path shape change is observable to users who tail trace files — the CLI banner now reads `Session: <dir>/ (per-call YAML under calls/)` instead of `Session: <file>` to make the layout discoverable.

**What's new:**

- `SessionRecorder` is directory-rooted (`dir`, not `path`); `manifest` and `inFlight map[int]*callHandle` replace `session sessionFile`.
- `callHandle.flush()` writes one per-call file atomically; `Begin`/`Finish` no longer touch sibling calls.
- `Close()` marks the manifest complete and flushes interrupted stragglers; `import` and `refine` CLI paths call it before printing the session banner.
- `CallStatusInterrupted` joins the existing `in_progress`/`completed`/`error` set.
- Per-call file naming: `<NNNN>-<agent_id>.yaml` (4-digit zero-padded; sorts lexically; agent_id makes `ls calls/` an at-a-glance summary). When `agent_id` is empty (ad-hoc call sites like the synthesizer), the filename is just `<NNNN>.yaml`.
- New tests cover the load-bearing properties: crash-mid-call preserves input on disk, in-flight count returns to zero after each Finish, per-call writes don't touch sibling files, Close stamps the manifest and interrupted stragglers.

**Reversal criteria:** revert if (a) per-directory file counts hit a filesystem ceiling on real workflows — not a current concern at 25 calls per session, but worth watching if adopt sessions push past several hundred calls; or (b) the loss of a single-file `cat` UX hurts more than the per-call discoverability helps — the directory layout is `find/grep` friendly, so this would surprise.

**Reference:** plan at [.claude/plans/session-trace-persistence.md](../.claude/plans/session-trace-persistence.md), Phase 1. Phase 2 (streaming-aware mid-call capture) is deferred.

## DJ-092: Revise Step Is a Per-Node Fanout, Not a Single Architect Call

**Status:** shipped

**Decision:** The spec-generation council's `revise` step is replaced by a four-step shape:

1. **`triage`** (1 LLM call, fast tier) — `spec_revision_triager` consumes the critic findings + the proposal's existing node IDs and emits a `RevisionPlan` routing each finding into one of three buckets: `feature_revisions[]` (concerns targeting an existing feature), `strategy_revisions[]` (concerns targeting an existing strategy), or `additions[]` (concerns proposing a missing node). Non-actionable findings are silently omitted; the trace records both the input concerns and the output buckets so an operator can see what got dropped without a separate `discarded[]` field.
2. **`revise_features`** (fanout, parallel) — one `spec_feature_elaborator` call per `feature_revisions[]` entry. Reuses the Phase-3 elaborator agent in revise mode: the projection feeds it the prior `RawFeatureProposal` plus the targeted concerns and asks for a corrected re-emission of that one node.
3. **`revise_strategies`** (fanout, parallel) — strategy counterpart, same shape.
4. **`revise_additions`** (1 architect call, conditional `has_additions`) — emits a partial `RawSpecProposal` containing ONLY the new features/strategies that address each addition concern. Existing nodes are explicitly listed as "do NOT re-emit."

The merge handler stitches the original `RawSpecProposal` (preserved in a new `state.OriginalRawProposal` field after elaborate completes) with the per-node revisions (swap by ID) and the additions (append, dropping ID collisions). `reconcile_revise` consumes the merged proposal unchanged.

**Why:** A real winplan run on Pro Preview (2026-05-02, trace `.locutus/sessions/20260502/1216/35-ef2f20.yaml`) revealed the architect short-circuiting under critic-finding pressure. Pre-revise (line 3686+) every strategy carried rich inline decisions — `strat-web-application-framework` had "Adopt Next.js on Vercel" and "Use React Server Components" with full rationale, alternatives, and citations. Post-revise (line 4605+) the architect emitted `decisions: [{}]` placeholders on every single strategy. The reconciler's `isEmptyInlineDecision` correctly drops the placeholders, but with revise replacing the whole proposal there is no fallback — all 8 persisted strategies ended up with zero decisions on disk.

This is exactly the failure mode Phase 3's elaborate fanout (DJ-090) was designed to prevent: too much input + too much output + the model short-circuits by stubbing entire sections. Phase 3 fixed it for elaborate; revise was still a single architect call carrying the full RawSpecProposal. The fix is the same pattern — bound the per-call output to one node's worth of JSON.

**Per-node fanout makes the failure structurally absent.** A revise call that touches `strat-web-application-framework` only ever produces a `RawStrategyProposal` for that one strategy. There is no "every other strategy" to short-circuit on, because every other strategy is handled by a sibling call (or untouched and passed through the merge verbatim). The empty-placeholder failure mode requires the architect to be authoring multiple strategies in one call; the fanout prevents it.

**Why a triage step.** Critics today emit free-form `{agent_id, severity, kind, text}` findings that mention node IDs or titles in prose. Without triage, every elaborator call would have to filter the global concerns list to find what applies to its node — duplicated work, inconsistent judgment across siblings. Triage is a single bounded call (small input: concerns; small output: routing plan) that maps each finding to the right bucket once. Critics keep their existing free-form output; the routing logic is one new agent, not a critic-prompt rewrite.

**Why no `discarded[]` field.** An earlier draft of `RevisionPlan` included `discarded: []string` so non-actionable findings were explicitly accounted for. No code consumes the field — the workflow's three downstream steps read `feature_revisions`, `strategy_revisions`, and `additions` only. Aspirational fields in LLM output schemas are degenerate-loop bait on weaker models (per the Span citation removal in DJ-090's follow-up). Dropping `discarded[]` keeps `RevisionPlan` to exactly the fields downstream code consumes; the trace already captures both input and output so an operator can compute what got dropped by diffing.

**Executor bug surfaced and fixed.** The new workflow has `parallel: true` + `conditional` on the same step (`revise_features` and `revise_strategies` both fanout-parallel and gated by `has_concerns`). The DAG executor's `runParallel` filtered out conditional-skipped steps but never marked them completed — the wave loop infinite-looped with skipped steps stuck in `ready` forever. The sequential branch already handled this via `runSingle`'s `skip` return; the parallel branch now returns a `skipped []string` alongside the results so the caller can mark them. Pre-existing bug exposed by Phase 1; fixed in [internal/executor/dag.go](../internal/executor/dag.go).

**What's new:**

- New types: `RevisionPlan`, `NodeRevision` in [internal/agent/revision.go](../internal/agent/revision.go).
- New agent: [internal/scaffold/agents/spec_revision_triager.md](../internal/scaffold/agents/spec_revision_triager.md), fast-tier router.
- `extractFanoutItems` extended for `revision_plan.feature_revisions` and `revision_plan.strategy_revisions`.
- `fanoutItemID` falls back from `id` to `node_id` so revise-fanout per-item event labels render.
- `has_additions` conditional gates `revise_additions`.
- `assembleRevisedRawProposal` Go function — merges original + revisions + additions for `reconcile_revise`.
- `PlanningState`: `OriginalRawProposal`, `RevisionPlan`, `RevisedFeatures[]`, `RevisedStrategies[]`, `AdditionProposals` fields.
- Three new projections: `projectTriage`, `projectReviseNode` (parameterized for feature/strategy), `projectReviseAdditions`.
- The architect's "On revise rounds" section is removed; replaced with an "On revise_additions calls" section scoped to its new responsibility (additions only).
- The two elaborator prompts gain a small "If invoked in revise mode" addendum.

**What stays the same:**

- The reconciler agent and `ApplyReconciliation` logic (DJ-088).
- The integrity critic in the critique merge pass (DJ-089).
- Cascade rewrites on conflict-resolution actions.
- Critic findings shape and the four critic agents.

**Reversal criteria:** revert if (a) triage misroutes concerns at a high enough rate that the wrong elaborator addresses them — at which point the triage prompt needs sharper rules, not abandonment of the structure; or (b) per-node revise calls produce thinner content than the prior single-call architect did, suggesting the elaborator agent isn't a good fit for revise mode — at which point we'd add a dedicated revise-elaborator agent rather than reusing the elaborate one.

**Reference:** plan at [.claude/plans/council-tools-and-revise-fanout.md](../.claude/plans/council-tools-and-revise-fanout.md), Phase 1. Phases 2 (scout grounding) and 3 (spec_lookup tool for the reconciler) are scoped in the same plan and follow.

## DJ-093: Scout Grounding via `grounding:` Frontmatter Field

**Status:** shipped

**Decision:** Agent frontmatter gains a `grounding: bool` field. When `true`, the LLM call is wired with the provider's native search-grounding capability:

- **Gemini routes** (`googleai/gemini-*`): the `genai.GoogleSearch` tool is appended to the request's `GenerateContentConfig.Tools` via Genkit's `ai.WithConfig` option. The model can search the live web during the call to verify claims against current material.
- **Anthropic routes**: the Genkit Go anthropic plugin doesn't yet expose `web_search`. The runtime logs a `slog.Warn("grounding requested but unsupported on Anthropic; proceeding ungrounded")` and produces a normal ungrounded request — the call still succeeds; only the search capability is dropped. Wire `web_search` through here on the same flag when upstream lands it.

The scout (`spec_scout.md`) is the first agent opted in. Frontmatter:

```yaml
grounding: true
```

The scout prompt gains a `# Use Search to Verify Current State of Practice` section instructing the agent to use search as a sanity check (verify version numbers, recent best-practice shifts, vendor status) — explicitly NOT as an enumeration tool, and explicitly NOT a license to add output schema fields. The scout's responsibilities and output shape are unchanged; grounding raises the floor on what `domain_read` and `implicit_assumptions` can ground themselves against.

**Why:** Foundational gaps like "explicit cloud-platform commitment" and "infrastructure-as-code tool" never surfaced in real winplan runs because the scout's `implicit_assumptions[]` was bounded by training-cutoff intuition. Adding axes to the outliner's prompt is the wrong fix — it ages badly as practice evolves. The right fix is to give the scout the ability to verify what it commits on against current material.

**Why grounding lives on the agent, not on the request.** Per-call Grounding flags push the decision into every callsite. Frontmatter scope is per-agent, which matches how the council reasons about responsibilities — the scout *is* the agent that surveys current state of practice; other agents *aren't* and shouldn't pay for grounded calls. The threading: `AgentDef.Grounding` → `BuildGenerateRequest` → `GenerateRequest.Grounding` → `buildProviderConfig` (attaches `GoogleSearch` or logs the Anthropic warning).

**Hard provider constraint.** Per Genkit's googlegenai live test (`plugins/googlegenai/googleai_live_test.go:241`): "The Gemini API does not support combining GoogleSearch with function calling." An agent with `grounding: true` cannot also have custom Genkit function-call tools attached. For our council that's not a collision — the scout uses grounding (no other tools); the reconciler will use spec_lookup tools (no grounding). For users who configure agents differently, this constraint will surface as an `INVALID_ARGUMENT` from Gemini.

`output_schema` (responseSchema) coexistence with GoogleSearch on Gemini: the plugin's "JSON mode is not compatible with tools" check (`gemini.go:311`) only excludes Genkit `input.Tools` (function calling), not `gcc.Tools` (the GoogleSearch attachment), so the scout's `output_schema: ScoutBrief` should still apply at the same time as grounding. If Gemini's API ever rejects this combination at runtime, drop `output_schema` for the scout and parse JSON from prose.

**Cost note.** Grounded Gemini calls are billed differently from ungrounded calls (search results count toward usage). First runs on real projects will tell us in real numbers; if the cost-per-refine becomes uncomfortable, gate grounding behind an env var (`LOCUTUS_GROUNDING=off`).

**What's new:**

- `AgentDef.Grounding bool` field with frontmatter tag `yaml:"grounding,omitempty"`.
- `GenerateRequest.Grounding bool` field.
- `BuildGenerateRequest` threads `def.Grounding` into the request.
- `buildProviderConfig` attaches `GoogleSearch` for Gemini routes when `req.Grounding`; logs a structured warning for Anthropic routes.
- The googleai branch's "no config needed" early-return is gated on `!req.Grounding` so the GoogleSearch attachment always materializes a config.
- `spec_scout.md` frontmatter sets `grounding: true` and the prompt body documents the search-as-sanity-check role.
- Tests: `TestBuildProviderConfig` gains four grounding subtests (Gemini attach, default-off, materializes-config, Anthropic non-fatal). `TestLoadAgentDefsParsesGrounding` confirms frontmatter round-trips. `TestBuildGenerateRequestThreadsGrounding` confirms the AgentDef → GenerateRequest path.

**What stays the same:**

- The scout's responsibilities, output schema (`ScoutBrief`), and prompt structure (Identity / Context / Task / Quality Criteria).
- All other agents — the reconciler, elaborators, critics, architect, triager — leave grounding off.
- The model-tier resolution; grounding is orthogonal to capability tier.

**Reversal criteria:** revert if (a) the scout-with-grounding produces noticeably worse briefs than ungrounded (e.g. search-result-aggregation displacing engineering judgment) — at which point the prompt's "search is a sanity check" framing needs sharpening; or (b) per-call costs become a meaningful operating concern — at which point we add an env-var gate or capability-tier-based opt-in. Neither failure mode is structural.

**Reference:** plan at [.claude/plans/council-tools-and-revise-fanout.md](../.claude/plans/council-tools-and-revise-fanout.md), Phase 2. Phase 3 (spec_lookup tool for the reconciler) follows.

## DJ-094: Spec-Lookup Tools for the Reconciler + Per-Round Tool-Use Capture

**Status:** shipped

**Decision:** Two changes ship together:

1. **`spec_list_manifest` and `spec_get` tools** are registered against the Genkit runtime so the `spec_reconciler` agent can navigate the persisted spec lazily instead of receiving the entire `ExistingSpec` snapshot inlined into its prompt. The reconciler's frontmatter declares `tools: [spec_list_manifest, spec_get]`; the GenKit Generate path passes them via `ai.WithTools` when `req.Tools` is non-empty.

2. **Per-round tool-use capture** in the session trace. The middleware accumulates one `GenerateRound` snapshot per model invocation inside Genkit's tool-dispatch loop and surfaces them as `Rounds []recordedRound` on the per-call YAML. Single-round calls leave `Rounds` empty (the top-level `Reasoning`/`Response`/`RawMessage` carry that round's data); multi-round calls record every round so an operator can see what the model asked the tools to do, not just the final response after the loop completed.

**Tools surface:**

- `spec_list_manifest()` → returns a compact index grouped by kind (features, strategies, decisions, bugs, approaches). Each entry has `id`, `title`, `kind` (strategies only), and a `summary` collapsed to one line and truncated to 200 runes. Computed on-demand from `.borg/spec/<kind>/*.json` directory listings — no persisted manifest file. The spec directory IS the manifest per DJ-068.
- `spec_get(id)` → returns the raw JSON of one spec node by id. Kind is inferred from the id prefix (`feat-`, `strat-`, `dec-`, `bug-`, `app-`); approaches return their markdown body wrapped as a JSON string. Unknown prefix errors with a clear message; missing id surfaces the underlying read error.

Both are pure reads against `specio.FS`. Greenfield runs (no `.borg/spec/`) return empty manifests rather than errors — tools must be safe to call when the reconciler has nothing to look up.

**Why no persisted manifest file.** The user asked whether `.borg/manifest.json` should be populated since it's empty in real projects. DJ-081 already pins that file as the project-root marker (`{project_name, version, created_at}`); DJ-068 establishes that `.borg/spec/` IS the manifest. Adding a derived `.borg/spec/manifest.json` that we have to keep in sync with disk creates a drift surface for no benefit — `ListDir` is fast, the JSON files are small, and reading them at tool-call time is bounded by the reconciler's actual lookup pattern rather than total spec size.

**Why no persisted file is the right answer for now.** If we ever ship a `Genkit` runtime that talks to remote MCP servers, a persisted manifest becomes load-bearing (the remote can't `ListDir` cheaply). Today's runtime is in-process; the on-demand path is correct.

**Hard provider constraint.** On Gemini, attaching tools silently disables API-level JSON mode (`plugins/googlegenai/gemini.go:311` — `if hasOutput && len(input.Tools) == 0`). The reconciler's `output_schema: ReconciliationVerdict` still injects the schema as system-prompt documentation, but the API doesn't enforce conformance when tools are attached. The defensive mitigation is `stripJSONFences` in `mergeReconcile` — Gemini wraps its output in ```` ```json ... ``` ```` out of training-distribution habit when JSON mode is off, and the parser would reject the wrapping. The fence-stripper trims it before `json.Unmarshal`. If the model produces malformed JSON beyond a fence wrap, the reconciler's verdict parse fails and surfaces as a workflow error — same path as today.

The Gemini constraint also means tools and `grounding: true` (DJ-093) cannot coexist on the same agent. Not a collision in our council: the scout uses grounding (no tools); the reconciler uses tools (no grounding).

**Why per-round capture ships in this DJ.** Without per-round capture, today's middleware overwrites `capturedText`/`capturedReasoning`/`capturedMessageRaw` on each model invocation. In a multi-round tool-use loop, only the **final round** survives in the trace — the model's actual `tool_request` blocks from earlier rounds are silently lost. The reconciler is the first and only consumer of multi-round tool-use today; without per-round capture, the first real tool-using run would produce a trace that hides exactly the calls an operator would want to debug. The fix is small (~30 lines) and keeps the trace's debugging value intact as the council's tool surface grows.

The capture shape: `recordedRound { Index, Reasoning, Text, Message, InputTokens, OutputTokens, ThoughtsTokens }`. `Message` is the JSON-serialised `*ai.Message` for that round, containing every part (text, reasoning, `tool_request`). Tool **response** payloads (what the runtime returned to the model) appear as input messages on the **next** round; an operator can reconstruct the conversation by reading rounds in order. Capturing tool-response payloads explicitly is a follow-up if it proves load-bearing for debugging.

**What's new:**

- `internal/agent/spec_tools.go`: `SpecManifest`, `SpecManifestEntry`, `BuildSpecManifest(fsys)`, `LookupSpecNode(fsys, id)`, `RegisterSpecTools(g, fsys)`. Constants `ToolNameSpecListManifest`, `ToolNameSpecGet`. The pure functions are testable with MemFS without going through Genkit registration.
- `AgentDef.Tools []string` field with frontmatter tag `yaml:"tools,omitempty"`.
- `GenerateRequest.Tools []string` and `GenerateRequest.Rounds []GenerateRound`. `BuildGenerateRequest` threads `def.Tools` through.
- `GenKitLLM.Genkit()` accessor exposes the runtime so tool registration can run after `NewGenKitLLM`.
- `cmd/llm.go.recordingLLM` calls `registerSpecToolsOnce` after `getLLM()` so tools are bound to the same `fsys` the rest of the command operates on.
- `GenKitLLM.Generate` attaches `ai.WithTools(toolRefs...)` when `req.Tools` is non-empty (using `ai.ToolName` to satisfy `ToolRef`).
- `captureMW` accumulates `GenerateRound` per invocation; the result is assigned to `out.Rounds` only when `len > 1`.
- `recordedCall.Rounds []recordedRound` field; `callHandle.finishAt` copies `resp.Rounds` into the per-call YAML.
- `mergeReconcile` strips markdown fences from the verdict via `stripJSONFences` before `json.Unmarshal`.
- `spec_reconciler.md` frontmatter declares `tools: [spec_list_manifest, spec_get]`; the prompt's "Existing spec snapshot" context section is replaced with a tool-usage hint pointing at the same lookup surface.
- `projectReconcile` no longer inlines `ExistingSpec.Decisions` into the prompt; emits a tool-usage hint sentence when an existing spec is present and nothing extra otherwise.
- New tests for: greenfield manifest, populated manifest, summary truncation, prefix routing, threading through AgentDef → GenerateRequest, frontmatter tools parsing, projection no-inline behavior, fence-stripper variants, end-to-end fenced-verdict tolerance, per-round persistence, single-round-omits-Rounds.

**What stays the same:**

- The reconciler's task (cluster inline decisions, emit verdict). Output schema unchanged.
- The cascade rewrite path on `resolve_conflict` actions.
- The session-trace per-call file layout (DJ-091); `Rounds` is a new optional field within the existing `recordedCall` shape.
- All other agents (scout, outliner, elaborators, critics, triager, architect) — none gain tool wiring. Only the reconciler.

**Reversal criteria:** revert if (a) Gemini's API rejects the responseSchema + tools combination at runtime in a way the fence-stripper can't accommodate (e.g. truncated mid-JSON before a closing fence) — at which point we'd drop `output_schema` for the reconciler entirely and parse loosely; or (b) the per-round capture inflates trace files past comfort on long tool-use loops — at which point we'd cap `Message` size per round or move multi-round captures to a sibling sidecar. Neither failure mode is structural.

**Reference:** plan at [.claude/plans/council-tools-and-revise-fanout.md](../.claude/plans/council-tools-and-revise-fanout.md), Phase 3. Per-round capture was folded in during implementation after the user flagged the trace-visibility gap.

## DJ-095: Lossless Triage + Per-Finding Additions Fanout

**Status:** shipped

**Decision:** Two coupled changes restore the council's signal pathway from critic findings to spec mutations:

1. **Triage routes everything.** The `spec_revision_triager` agent's prompt drops its rule 5 ("non-actionable → omit") in favour of a routing-completeness mandate: every input critic finding lands in exactly one of `feature_revisions`, `strategy_revisions`, or `additions`. There is no fourth bucket. The critic already did the actionability judgment by emitting the finding; the triager's only authority is routing. When uncertain, the prompt directs the triager to default to `additions` with `kind: "strategy"` — that's the recoverable failure mode (a strategy that turns out to be unnecessary is recoverable by the next refine pass; a finding silently dropped is not). Triager `capability` flips from `fast` to `balanced` since routing 32 findings is closer to a judgment call than the simple keyword-mapping fast tier handles cleanly.

2. **Additions becomes a per-finding fanout.** The `RevisionPlan.Additions` field changes from `[]string` to `[]AddedNode { kind: "feature"|"strategy", source_concern: string }`. The single `revise_additions` step (one architect call asked to invent N nodes from a list) is replaced by two fanout steps `revise_feature_additions` / `revise_strategy_additions` filtered by kind, each dispatching `spec_feature_elaborator` / `spec_strategy_elaborator` once per AddedNode. The elaborators gain an "addition mode" projection: the user message includes a "Node to propose (addition)" block with the verbatim critic finding, an "Existing nodes (do NOT re-emit)" list, and a directive to invent one new node (id, title, body, decisions) from the finding. `PlanningState.AdditionProposals` changes from `string` to `[]string` to accumulate per-finding outputs; `assembleRevisedRawProposal` sniffs the id prefix per entry to dispatch into the merged feature/strategy slices.

**Why:** A real winplan run on 2026-05-03 (trace `.locutus/sessions/20260503/0034/35-d9cdc4/`) — with Phases 1-3 shipped — surfaced both problems compounding on each other.

The four critics emitted ~32 findings: devops flagged missing CI/CD, environments, rollback, secrets, deps, build (6); SRE flagged missing observability tooling, SLO targets, on-call, capacity, circuit breakers, runbooks, error budget (7); architect flagged missing Multi-deliverable coordination, Documentation, Build Tooling, Distribution Channel, Backend connectivity protocol, AWS-vs-Vercel coherence, plus several decision-language violations (14); cost flagged missing cost ceiling, no caps/alarms, no cheap alternatives (5).

Of those ~32 findings, the triager (call 0024) routed exactly **3** — two cost/capacity concerns onto `feat-voter-file-management` and one SLO concern onto `feat-field-canvassing-interface`. The other 29 fell into the "non-actionable, omit" bucket. The result on disk: a spec missing IaC, CI/CD, secrets, observability, auth, build tooling — exactly the gaps the critics had identified. The triager was detecting them and discarding them in the same call.

Even if triage had routed correctly, the additions path remained a single architect call (DJ-092 `revise_additions`) — structurally identical to the pre-Phase-1 revise step that failed by emitting placeholder decisions under multi-node authoring pressure. With 29+ additions, that call is the same anti-pattern, just one round later. Fixing both together is what makes the signal pathway lossless: the critics surface a gap → the triager routes it → the elaborator authors a corrected node addressing it → the reconciler dedupes across cluster.

**Why no discard bucket.** The earlier "non-actionable → omit" rule was added so the triager could drop pure observations and already-addressed findings, but in practice it became the triager's escape hatch when a finding didn't obviously fit one of the three actionable buckets. Removing the rule trades occasional over-routing (one wasted elaborator call when an addition turns out unnecessary) for never-silently-dropped findings (a wasted call costs spend; a dropped finding costs a spec gap that ships to disk). Reconciler dedup catches the over-routing tail (5 critics flagging the same missing strategy → 5 elaborator outputs collapsed to 1 canonical).

**Why per-finding fanout.** Same structural reason DJ-092 made revise per-node: a single architect call asked to author N new nodes from a list short-circuits under multi-node authoring pressure (placeholder decisions, missing nodes). One bounded elaborator call per addition is structurally isomorphic to the elaborate path that already works at scale.

**What's new:**

- `AddedNode { Kind, SourceConcern }` struct in [internal/agent/revision.go](../internal/agent/revision.go); `RevisionPlan.Additions` retyped from `[]string` to `[]AddedNode`.
- `PlanningState.AdditionProposals` retyped from `string` to `[]string`. Merge handler appends instead of overwrites; `assembleRevisedRawProposal` iterates the slice, sniffs id prefix per entry (`extractRawID` helper), dispatches into merged feature/strategy slices with collision-drop semantics.
- `extractFanoutItems` gains paths `revision_plan.additions.features` and `revision_plan.additions.strategies` that filter the AddedNode list by kind.
- `projectAdditionElaborate(snap, kind)` projection in [internal/agent/projection.go](../internal/agent/projection.go); `ProjectState` routes `revise_feature_additions` / `revise_strategy_additions` step IDs to it. The old single-call `projectReviseAdditions` is removed.
- Workflow YAML: single `revise_additions` step replaced by two fanout steps `revise_feature_additions` (`spec_feature_elaborator`, `parallel: true`) and `revise_strategy_additions` (`spec_strategy_elaborator`, `parallel: true`), both gated on `has_additions`. `reconcile_revise` depends on both.
- [spec_revision_triager.md](../internal/scaffold/agents/spec_revision_triager.md) prompt rewrite: rule 5 dropped, routing-completeness mandate added, additions output shape changed to `AddedNode`, capability `fast` → `balanced`. The "Bias toward routing, not discarding" mandate is now load-bearing rather than a soft hint.
- Elaborator prompts ([spec_feature_elaborator.md](../internal/scaffold/agents/spec_feature_elaborator.md), [spec_strategy_elaborator.md](../internal/scaffold/agents/spec_strategy_elaborator.md)) gain an "addition mode" addendum describing how to invent a new node from a single critic finding, including id-prefix conventions and the "do NOT re-emit existing nodes" directive.
- [spec_architect.md](../internal/scaffold/agents/spec_architect.md) drops its "On revise_additions calls" section — the architect is no longer the additions author; the elaborators are.
- New tests: `TestExtractFanoutItemsAdditions` (kind-filter + empty-default), `TestMergeResultsAdditionProposalsAccumulates`, `TestAssembleRevisedRawProposalAppendsAdditions` updated for slice shape, `TestAssembleRevisedRawProposalAdditionsDedupOnExistingID`, `TestAssembleRevisedRawProposalAdditionsUnknownPrefixDropped`, `TestProjectAdditionElaborateRendersConcernAndExistingNodes`, plus `TestProjectStateRoutesReviseStepsCorrectly` updated for the new step IDs.

**What stays the same:**

- The reconciler's task and ApplyReconciliation logic. Cross-cluster dedup is exactly its job; multiple critics flagging the same missing strategy collapse to one canonical via existing semantics.
- Per-node revise fanouts (DJ-092) — same shape, same elaborator agents.
- Critique step and the four critic agents.
- `has_additions` conditional — unchanged because `len(plan.Additions) > 0` works whether Additions is `[]string` or `[]AddedNode`.

**Reversal criteria:** revert if (a) per-finding fanout cost becomes prohibitive in real runs (29+ strong-tier elaborator calls per refine; per-call 5m timeout bounds runaway, but aggregate spend may bite) — at which point the mitigation is capping additions per critic in the triager prompt or moving the addition elaborator to balanced tier; or (b) the reconciler's cross-cluster dedup turns out to under-collapse, leaving the spec with multiple near-identical strategies after every run — at which point we'd add a triager-side pre-cluster pass before fanout dispatch.

**Open questions:**

- **Multi-round convergence.** New nodes added in this pass aren't themselves criticised. If single-pass output still has meaningful gaps the same critics would flag on a second round, we'd bump `max_rounds` to 2-3 — that's the natural Phase 5 follow-up. Defer until measured.
- **Addition kind misclassification.** A finding routed as `feature` but really a `strategy` (or vice versa) goes to the wrong elaborator agent. Recoverable: the strategy elaborator can produce feature-shaped output and vice versa under the right system prompt. Costs accuracy, not correctness.

**Reference:** plan at [.claude/plans/council-tools-and-revise-fanout.md](../.claude/plans/council-tools-and-revise-fanout.md), Phase 4. Sequencing notes Phases 1-3 (DJ-092, DJ-093, DJ-094) ship before this one. Phase 5 (multi-round convergence) is the natural follow-up if Phase 4's single-pass output isn't comprehensive enough.

## DJ-096: State Store Lives Under `.borg/state/`, Not `.locutus/state/`

**Status:** shipped

**Decision:** The reconciliation state store relocates from `.locutus/state/` to `.borg/state/`. The DJ-068 substantive decision is unchanged — state IS committed, observed-vs-desired separation, Kubernetes-style reconciliation loop — only the on-disk path moves. A new constant `state.DefaultStateDir = ".borg/state"` is introduced; production callers reference it instead of the legacy literal.

**Why the path was wrong:** DJ-068 explicitly committed to "in-repo YAML, version-controlled, diffable, auditable via `git log`," but the implementation landed under `.locutus/state/` and `cmd/init.go` writes `.locutus/` to `.gitignore` wholesale. Net effect: state was treated as ephemeral when the design said durable. The mistake came by analogy from `.locutus/workstreams/` (DJ-073) and `.locutus/sessions/` (DJ-091), both of which genuinely are per-machine/per-run; DJ-073 even calls out the inconsistency as "a narrow departure from DJ-068's 'state is always in-repo' framing." The departure was accidentally promoted to the default.

**The clean line:**

| Directory | Lifecycle | Committed? |
| --- | --- | --- |
| `.borg/spec/` | Desired state — what should be | Yes |
| `.borg/state/` | Observed state — what currently is, hash-linked to spec | Yes (this DJ) |
| `.borg/history/` | Past-tense narrative record | Yes |
| `.borg/agents/` | Council agent prompts | Yes |
| `.borg/workflows/` | Workflow YAML | Yes |
| `.borg/models.yaml` | Model tier configuration | Yes |
| `.borg/manifest.json` | Project-root marker | Yes |
| `.locutus/sessions/` | LLM call traces (per-run debug) | No |
| `.locutus/workstreams/` | In-flight execution coordination (deleted on terminal) | No |

`.borg/` = "what the project knows about itself." `.locutus/` = "what this run / this machine is doing right now." Naming actually fits the Star Trek allusion — `.borg/` is the Collective's accumulated knowledge; `.locutus/` is the assimilated speaker's working memory.

**Why state belongs alongside spec:** state is the observed counterpart to `.borg/spec/`'s desired state — they're sibling project-truths. A teammate cloning the repo immediately sees what's `live`, what's `drifted`, what's `unplanned`. `git blame .borg/state/app-oauth-login.yaml` gives the audit trail of when reconciliation last passed and what spec_hash it asserted against. CI can compare committed claims against a fresh reconcile and flag stale state as a build failure.

**Why per-machine concerns don't change the answer:** artifact hashes target source files (`src/auth/oauth.go`), which are deterministic across environments. Built binaries are NEVER part of artifact hashes — only the source paths the spec asserts.

**Migration:** none. The legacy `.locutus/state/` path was gitignored, so existing projects (winplan being the only one) have nothing committed to preserve. The next adopt run regenerates entries at the new path. Pre-alpha; no consumer relies on the old layout.

**What's new:**

- `state.DefaultStateDir = ".borg/state"` constant in [internal/state/store.go](../internal/state/store.go).
- Production callers ([cmd/refine.go](../cmd/refine.go), [cmd/adopt.go](../cmd/adopt.go)) reference `state.DefaultStateDir` instead of the literal.
- Test fixtures use `.borg/state` literals (test-isolated, no need to touch the production constant).
- Scaffolder ([internal/scaffold/scaffold.go](../internal/scaffold/scaffold.go)) creates `.borg/state` instead of `.locutus/state`.
- Comment in [internal/state/state.go](../internal/state/state.go) corrected from `.locutus/state` to `.borg/state`.

**What stays the same:**

- DJ-068's substantive decisions (state is committed; observed-vs-desired separation; Kubernetes-style loop; per-Approach state entries; per-file artifact hashes; lifecycle states).
- DJ-073's gitignored treatment of `.locutus/workstreams/` (genuinely transient execution coordination).
- DJ-091's gitignored treatment of `.locutus/sessions/` (per-run debug traces).
- DJ-081's `.borg/manifest.json` as the project-root marker (unchanged content; just a marker).
- The `cmd/init.go` `.gitignore` writer continues to add `.locutus/` whole — now correct without exceptions, since nothing committed lives under that path.

**Reversal criteria:** revert if the spec/state co-location creates a category of merge conflict on a real team workflow we haven't anticipated. Pre-alpha; no measurement yet, but the failure mode is bounded — state files are small, deterministic on source artifacts, and `git merge` handles them as ordinary YAML.

**Reference:** supersedes the path detail in DJ-068. Substantive decisions in DJ-068 stand unchanged. Followup to DJ-095's general Phase-4 work, but logically independent.

## DJ-097: Projections Are Data-Only; Rules Belong in the Agent's `.md`

**Status:** shipped

**Decision:** Projection functions in [internal/agent/projection.go](../internal/agent/projection.go) emit data only — context, fanout items, concerns to address. They do NOT carry directives ("Emit a RevisionPlan…", "Produce one Raw…Proposal…", "Findings you judge non-actionable are simply omitted"). All rules of behavior live in the agent's `.md` system prompt under `# Identity / # Context / # Task / # Mandates`.

**Why:** the system prompt and the projection's user-message tail are two surfaces that BOTH render into the model's context, but they're maintained in two different files with two different mental models. When rules drift between them, the user message wins at inference time and silently overrides the system prompt. DJ-095's Phase 4 broke for one run (May-4 winplan trace) because the triager's system prompt was rewritten to mandate "every finding routes to one of three buckets" but the projection tail still said "findings you judge non-actionable are simply omitted." The user-message instruction won; 22 of 32 critic findings got dropped — exactly the behavior the system-prompt rewrite was supposed to eliminate.

The structural fix: never let projections carry directives. With that constraint, drift between the two surfaces becomes structurally impossible because there's only one surface for rules.

**Concrete changes in [projection.go](../internal/agent/projection.go):**

- `projectElaborateOne` — dropped "Produce the full Raw…Proposal for the item above. Preserve its id and title verbatim. Decisions are inline; the reconciler downstream dedupes across siblings." That directive lives in [spec_feature_elaborator.md](../internal/scaffold/agents/spec_feature_elaborator.md) and [spec_strategy_elaborator.md](../internal/scaffold/agents/spec_strategy_elaborator.md) Task sections.
- `projectTriage` — dropped the routing-completeness reminder at the tail. The mandate lives in [spec_revision_triager.md](../internal/scaffold/agents/spec_revision_triager.md), now the only surface that owns it. Added an explicit "emit empty arrays as `[]`, never `[{}]`" rule to the .md to defuse the secondary regression.
- `projectReviseNode` — dropped "Produce the corrected Raw*Proposal… Address every concern… Re-emit the FULL node." The "revise mode" addendum in each elaborator's .md owns this.
- `projectAdditionElaborate` — dropped "Produce one Raw*Proposal that addresses the finding above. Invent the id…" The "addition mode" addendum in each elaborator's .md owns this.
- `projectReconcile` — dropped "Emit a ReconciliationVerdict naming the clusters that need dedupe / resolve_conflict / reuse_existing." Lives in [spec_reconciler.md](../internal/scaffold/agents/spec_reconciler.md). Kept the data-conditional flag noting whether an existing spec is present (data, not directive — different per call).

What remains in projections is strictly data: section headers, fanout items as plain key-value pairs, concern lists grouped by kind, prior content blocks, existing-nodes lists.

**Snapshot tests for rendered prompts** ([prompt_snapshot_test.go](../internal/agent/prompt_snapshot_test.go) + `testdata/golden/<step>.txt`):

- A test renders the FULL system + user message for each council step against a fixed fixture, diffs against a golden file, and fails on any change.
- Update mode: `LOCUTUS_UPDATE_GOLDEN=1 go test ./internal/agent -run TestRenderedPrompt`. Rebuilding goldens forces the diff into the PR review surface.
- Eight scenarios covered: triage, revise_features, revise_strategies, revise_feature_additions, revise_strategy_additions, elaborate_features, elaborate_strategies, reconcile_greenfield. The high-leverage ones — i.e. the steps where the DJ-095 contradiction bug actually bit.
- A second test (`TestRenderedPromptHasNoContradictions`) is a permanent invariant: the triager's rendered prompt must NOT contain "simply omitted" / "non-actionable" alongside the routing-completeness mandate. Reintroducing either by accident fails the test loudly.

What this catches:

1. Drift between system prompt and projection (the actual DJ-095 bug). A rule change in either surface produces a golden diff that the reviewer either accepts (refresh golden) or rejects (the change wasn't supposed to land).
2. Schema-example regressions — `RegisterSchema` changes flow into the system prompt via `BuildGenerateRequest`, so any schema edit shows up in the snapshots.
3. Subtle restructurings — context reordering, header changes, accidental whitespace shifts — that wouldn't fail behaviorally but would change what the model actually sees.

What this doesn't catch:

- Semantic correctness of the rules themselves. A coherent prompt that's just *wrong* will produce wrong-but-consistent output and snapshot diffs won't help. That's a different test layer (real LLM runs).
- Behavior changes that don't show up in the prompt — model config, tool availability, capability tier resolution. Those need their own tests.

**What stays the same:**

- `BuildGenerateRequest` continues to assemble system prompt (agent .md body + appended schema) + user messages (from projection). No structural change to the rendering pipeline.
- `buildRevisePrompt` (used by the legacy single-call revise path on non-spec-generation workflows) still carries directive content. Out of scope; the spec-generation council no longer uses that path. If that path ever comes back into active use, the same DJ-097 rule applies and it should be moved into the corresponding agent's .md.
- `projectChallenge` / `projectResearch` / `projectRecord` keep small directive fragments ("Review the proposal above", "Investigate these concerns:", "Record the council session above"). These are border-line — short data-labels that double as one-line task statements — and the agents that use them aren't the council agents currently exercised by `refine goals`. Defer until those paths come into active maintenance.

**Process implication:** when modifying an agent's behavior, the agent's `.md` is the single source of truth. The projection's job is to format the data that .md tells the agent to consume. Anyone reviewing a prompt change should ask: "did this rule change show up in the snapshot diff?" If yes, the golden has to refresh and the change is visible to reviewers. If no, the change is in code that doesn't render into the prompt and shouldn't affect agent behavior.

**Reversal criteria:** revert if data-only projections turn out to lack the localization that prompt experimentation needed (e.g., A/B testing wording for one step without rebuilding the whole .md). At that point a structured "directive-fragments" mechanism — first-class, not free-form — would be the right abstraction. Today that's premature.

**Reference:** corrects the regression that broke DJ-095's Phase 4 in the May-4 winplan run. The Phase 4 design itself stands; this DJ corrects an implementation drift surface that DJ-095 didn't anticipate.

## DJ-100: Comprehensive Spec Snapshot Lives Under `status --full`

**Status:** shipped

**Decision:** A full read-and-render view of the spec graph is exposed as `locutus status --full [--format markdown|json] [--kind …] [--status …]`, not as a new top-level verb. The structured shape behind it is a new in-memory type `spec.Loaded` (typed nodes paired with markdown bodies + inverse-reference indexes built once at load time), produced by `spec.LoadSpec(fsys)`. A separate `spec.DeriveStages(loaded, fsys)` walks `.locutus/workstreams/` activity to classify every node into one of five canonical implementation stages (`drafted`, `planned`, `implementing`, `done`, `drifted`), with `done` and `drifted` deferred until the SpecHash field on Approach lands. Markdown is the default render; JSON emits the same `SnapshotData` struct so tooling can diff snapshots directly.

**Why under `status` and not a new verb:** the canonical surface is locked at 8 mutating + read verbs ([CLAUDE.md](../CLAUDE.md)). `status` was already the read-shape covering counts and in-flight plans; extending it to a comprehensive view is a continuation, not a new concept. A user typing `locutus status --full` is asking the same question (`what's the state?`) at a different fidelity. A separate `snapshot` verb would split the read surface for no semantic gain.

**Why `Loaded` is distinct from `SpecGraph`:** the existing [internal/spec/graph.go](../internal/spec/graph.go) is built for *traversal* — blast-radius, transitive deps, topological sort. Read-and-render paths (snapshot, explain, justify, refine `--diff`) need typed nodes paired with their markdown bodies plus inverse-reference lookups (which features point at this decision? which strategies depend on this one?). Cramming both concerns into one type forced either the renderers to walk graph-internals (leaky) or the traversal layer to carry rendering details (heavyweight). Two shapes, one for each access pattern, with no duplication of *content* — the underlying typed structs (`Feature`, `Strategy`, `Decision`, `Approach`, `Bug`) are shared. Inverse indexes are computed at load time so per-call lookups are O(1); rebuilding them per query would be wasteful given snapshot/explain/justify all want the same indexes.

**Why workstream activity is read by line-grep, not import:** classifying a node as `implementing` requires checking whether any in-flight workstream YAML names one of the node's approach ids. The natural call would be `internal/workstream.LoadPlan` — but `internal/workstream` already imports `internal/spec` (for the typed approach references it persists), so that direction would cycle. A small parse-free helper `approachIDsInWorkstream` in [internal/spec/stage.go](../internal/spec/stage.go) line-greps `approach_ids:` from each YAML. Tolerates malformed files (best-effort; the stage map is informational, not load-bearing for any state transition). Future fix: extract the workstream YAML's typed shape into a leaf package both can import.

**Why per-node renderers ship now (in [internal/render/spec.go](../internal/render/spec.go)):** `RenderFeature`, `RenderStrategy`, `RenderDecision`, `RenderApproach`, `RenderBug` were authored for the snapshot but composed into the single `SnapshotMarkdown` only at the section level. Extracting them as exported functions costs nothing — the code is already factored — and explicitly seeds `explain` and `refine --diff` (DJ-101 and a follow-up plan) with the rendering primitive. The alternative — inlining the renderers into `SnapshotMarkdown` and re-extracting them when needed — would be a refactor pretending to be three commits.

**Why narrative ordering for strategies:** strategies sort foundational → quality → derived inside the snapshot. The natural reading order: foundational commitments (the named tech stack) → quality commitments (testing, observability) → derived strategies that build on those. Lexicographic ordering put "Quality" before "Foundational" alphabetically — wrong narrative orientation. Decisions and features sort by id (alphabetical) because there's no narrative gradient on those axes.

**What `--full` exposes that `--in-flight` doesn't:** `status --in-flight` (DJ-074 territory) lists leftover workstream YAMLs to surface adopt-resume candidates — operational visibility, not spec content. `--full` walks `.borg/spec/`, classifies stages, flags dangling references and orphans (decisions with no parent feature/strategy; approaches with no parent), and emits goals. Different reads of different state. They co-exist on `status` because both answer "what's true right now?".

**MCP parity:** the same `status` MCP tool gains the `full`, `format`, `kind`, `status` parameters. Identical handler shape; the IDE-side agent's "summarize my spec" call becomes a single tool invocation rather than reading the directory file by file.

**Reversal criteria:** revert if read-and-render paths grow traversal needs (transitive blast radius, topological queries) that force them to consult `SpecGraph` anyway — at that point fold `Loaded` and `SpecGraph` into one type rather than maintain both. Or revert the snapshot's narrative ordering if user feedback signals that strategies should sort alphabetically for diff stability (the two goals trade off).

**Reference:** depends on the typed structs DJ-068 settled. Foundation for DJ-101 (explain + justify) and the spec-refine-brief-diff plan (deferred). The five canonical stages are forward-looking: `done` and `drifted` need DJ-072's SpecHash on Approach to fire, which doesn't exist yet.

## DJ-101: Explain Is Pure-Render; Justify Is Active Defense

**Status:** shipped

**Decision:** Two new read-only verbs land alongside the canonical 8 — `locutus explain <id>` and `locutus justify <id> [--against "..."]`. They expand the verb surface from 8 to 10. Both are MCP tools.

`explain` composes the per-node renderers from DJ-100 with the inverse-index lookups from `spec.Loaded` to render any single spec node — rationale, alternatives, citations, lineage, back-references. No LLM. Markdown by default; `--format=json` for tooling. The id prefix selects kind (`dec-`, `feat-`, `strat-`, `app-`, `bug-`).

`justify` dispatches the inlined `spec_advocate` agent against the same rendered explain output plus GOALS.md plus the user's challenge (when given). With no `--against`, it writes a 2-4 paragraph defense citing the goal-clauses being satisfied and the conditions under which the node would NOT hold. With `--against`, it first runs the inlined `spec_challenger` agent (grounding-eligible) to formulate the strongest version of the user's critique, then the advocate addresses each concern point-by-point and emits a verdict (`held_up` | `partially_held_up` | `broke_down`). Non-`held_up` verdicts surface a suggested `locutus refine <id> --brief "..."` invocation pre-populated with the breaking points.

**Why two verbs and not one:** `explain` is reproducible, free, offline; `justify` invokes the LLM and costs tokens. Folding them under a flag (`explain --justify`) would conflate two different cost models. Users who just want to read what's already there shouldn't pay for an LLM call by default; users who want an active defense should opt in by typing the verb that does it. The verb name carries the cost contract.

**Why the verb surface grew from 8 to 10:** the locked 8-verb count (CLAUDE.md) covered the *mutating* and *operational* verbs — init, update, import, refine, assimilate, adopt, status, history. Read-only deliberation aids didn't fit any of them naturally:

- `explain` could have ridden under `status --node <id>`, but `status` is a project-level summary; per-node read-and-render is a different shape.
- `justify` could have ridden under `refine --justify` (no mutation), but `refine` is canonically the deliberation-and-rewrite verb; gating its non-mutating mode behind a flag obscures the fact that justify never touches spec.

The cleaner line: 8 mutating/operational verbs + 2 read-and-render deliberation verbs. CLAUDE.md is updated to reflect this. Future read-only deliberation aids (e.g., a graph traversal viewer) can land alongside without inflating the mutating set.

**Why advocate and challenger prompts are inlined in Go, not scaffold .md files:** the existing council agents (architect_critic, spec_reconciler, etc.) live as `.md` files under [internal/scaffold/agents/](../internal/scaffold/agents/) and are emitted to `.borg/agents/` by `init` / `update --reset`. Project agents are user-customizable per-project. But `justify` is a one-shot helper — same shape as `intake.go` — and shipping new scaffold files would force every existing project to run `update --reset` before the verb worked. Inlining the prompts in [internal/agent/justify.go](../internal/agent/justify.go) gives the verb a stable contract regardless of when the project was bootstrapped. Tradeoff: not customizable per-project. Revisit if real demand surfaces — at that point, a `.borg/agents/spec_advocate.md` override could shadow the inlined default, mirroring how the assimilation council handles user-supplied overrides.

**Why `AdversarialDefense` is structured, not free prose:** the adversarial path has to bridge into the next deliberation step. A free-prose verdict ("the rationale mostly holds up but…") forces the user to pattern-match what the next action should be. The structured shape — `verdict ∈ {held_up, partially_held_up, broke_down}`, `breaking_points[]`, per-concern `still_stands` flag — lets the renderer auto-emit the suggested refine command when the verdict isn't clean. The deliberation aid becomes a deliberation *bridge*: justify's broken-down verdict tells the user exactly what to feed `refine --brief` (a follow-up plan, deferred). Strict-mode schema enforcement at the API level (verdict enum, `oneOf` discrimination) prevents the LLM from inventing fourth-option verdicts that downstream rendering would have to special-case.

**Why suggest the next-step command but don't auto-fire it:** `refine` mutates spec. Auto-firing on a model-emitted verdict would couple two LLM calls' worth of confidence-decay into one irreversible action. Showing the command keeps the user as the gating authority. The cost of one extra paste is negligible; the cost of an unintended cascade isn't.

**Why drop `--against-finding <session>:<idx>` from the plan:** the original plan included sugar to load a recorded critic finding as the challenge prompt. No real consumer today (no one is iterating from a stored review). Per the no-aspirational-fields rule, shipping a flag that returns "not yet implemented" adds surface without value. If the workflow (review → recall finding → re-challenge) shows up in real use, the flag costs ~10 LoC to implement at that point.

**MCP exposure:** both verbs land as MCP tools (`explain`, `justify`) alongside the existing CLI ↔ MCP parity pattern. An IDE-side agent answering "why did we choose Datadog?" calls `explain dec-adopt-datadog-…` — cheap, instant. For "we're considering switching — does this still hold?" it calls `justify dec-postgres-… --against "Cockroach for cross-region writes"` and surfaces the verdict. Both flows happen inside the agent's existing conversation; no terminal context-switch. The justify tool's description explicitly frames it as "ask a specialist" not "filter a list" — calling it in a loop nests LLM activity, expensive if the consumer doesn't realize.

**Why this is read-only and never persisted:** justifications are deliberation aids, not state. The existing decision JSON already carries `rationale`, `architect_rationale`, `alternatives[].rejected_because`, `provenance.citations` — those ARE the persisted justification. Adding a generated-prose field next to them would muddy "what the architect committed to" with "what the LLM said in defense of it during one ad-hoc challenge." Stay separate. If a recorded justification turns out to be valuable for subsequent runs (e.g., training data, drift detection), revisit with a `JustificationProvenance` field that tracks generated defenses as audit-trail-only — explicitly not authoritative.

**Reversal criteria:** revert `justify` if real users discover that LLM-emitted defenses systematically drift from the persisted rationale (the model fabricates rationale not in the JSON). At that point `explain` stays as the canonical read; `justify` becomes a debugging aid behind a flag rather than a first-class verb. Revert the verb-surface expansion if the deliberation reads turn out to fit naturally under existing verbs after a few months of real use — the worst case is two aliases pointing at the same code path during a deprecation window.

**Reference:** depends on DJ-100 (per-node renderers + `Loaded` shape). Bridges to a deferred refine `--brief` / `--diff` plan via the suggested-next-step output. The inlined-prompt pattern matches `IntakeDocument` ([internal/agent/intake.go](../internal/agent/intake.go)).

## DJ-102: Refine Becomes a Deliberate-Evolution Loop

**Status:** shipped

**Decision:** `locutus refine` grows three new flags — `--brief "..."`, `--diff`, `--rollback` — backed by automatic history capture of pre/post bytes on every refine that mutates a node. The verb stays singular but its semantics shift from "regenerate the node" to "deliberately evolve the node."

**The three flags and what they do:**

| Flag | Effect | Cost |
| --- | --- | --- |
| `--brief "..."` | Threads a focused refinement intent into the rewriter prompt as a directive. Substantive rewrite expected. | One LLM call (existing). |
| `--diff` | After the refine writes, prints a unified diff over the per-node Markdown render of prior vs. new bytes. | Free; no extra LLM call. |
| `--rollback` | Restores the spec sidecar to the OldValue captured on the most recent `spec_refined` event. Records a `spec_rolled_back` event so subsequent rollbacks walk past it. | Free; no LLM call. Mutually exclusive with `--brief`, `--diff`, `--dry-run`. |

**Why automatic history capture, not opt-in:** the rollback path needs the prior bytes preserved verbatim, and `--diff` benefits from the same data. Once the side-effect is automatic, it also feeds `locutus history <id>` (existing surface) with structured refine events for free. Per-node history was previously emitted as `feature_refined` / `strategy_refined` / etc. without `OldValue` / `NewValue` payloads — useful for audit, not enough for rollback. The new `spec_refined` event carries both, replacing the per-kind labels for refine writes. Decision-path cascade keeps its existing operational events because the cascade rewrites *parents*, not the decision itself; the user-edited decision JSON has no pre-edit state to preserve.

**Why brief is threaded via context, not a parameter:** `cascade.WithBrief(ctx, brief)` and `cascade.BriefFromContext(ctx)` follow the same pattern `agent.WithRole` already uses for per-call metadata. Adding a `brief string` parameter to `RewriteFeature` / `RewriteStrategy` / `RewriteBug` / `InvokeRewriter` would touch every council-pipeline caller (assimilate, post-reconcile cascade, intake's planning pass) — most of which never carry an intent. Context keeps signatures stable for the no-brief path; the rewriter pulls the value via one helper at the prompt-assembly point.

**Why two agents (rewriter + refiner) instead of one:** the cascade rewriter is conservative ("minimum diff", don't paraphrase for stylistic preference), the refine path is deliberate (the user explicitly asked for change; deliver it). A single prompt with a "if Refinement intent is present, embrace it; else hew to minimum diff" branch arbitrates the conditional at inference time and can under-edit in refine mode because "minimum diff" is on the page. Two purpose-built agents — `rewriter.md` (cascade) + `refiner.md` (refine) — produce more predictable output. Dispatch is deterministic: `cascade.invokeRewriter` checks `BriefFromContext` and loads the matching scaffold .md.

DJ-097's lesson stays applicable: rules belong in the agent's `.md`, not in code or projection layers. Splitting reduces the conditional surface inside the .md to zero on either side.

The synthesizer (Approach refines) currently keeps a single agent with conditional brief handling. The cost of splitting is duplication; the trigger to revisit is observed conservativeness in refine-mode resyntheses. Symmetric with rewriter if it shows up.

**Why diff renders Markdown via per-node renderers, not raw JSON:** JSON-level diff is mechanically correct but full of noise from `updated_at` / `created_at` timestamp churn, `alternatives` reordering, and other artifacts that don't represent semantic change. Markdown rendering normalizes those out and shows the human-meaningful change. Both sides of the diff render through the same stub-Loaded path so back-reference resolution from the live graph (which moves around as other nodes change) doesn't surface as spurious diff lines for THIS refine.

**Why diff helper is in-tree, not `sergi/go-diff`:** ~140 LoC of LCS over lines covers the use case. Refine-sized inputs (a single node's rendered Markdown) are hundreds of lines max; the O(n*m) memory of classic LCS is fine. Adding a dependency for a feature this size is heavier than maintaining the helper.

**Why scaffold-loaded prompts via `scaffold.LoadAgent`:** previously `cascade.invokeRewriter` and `cmd/refine.go invokeSynthesizer` carried inline `agent.AgentDef{...}` literals while well-authored `internal/scaffold/agents/{rewriter,synthesizer}.md` files existed for the same agents. The inline path won at runtime; the scaffold .md was dead code from the cascade's perspective. Editing `.borg/agents/rewriter.md` did nothing today — the user couldn't tune behavior even though the surface looked customizable.

`scaffold.LoadAgent(fsys, id)` reads `.borg/agents/<id>.md` first (per-project user override) and falls back to the embedded scaffold copy when the project file is absent (fresh installs, tests on uninitialized FSes). The scaffold .md is the source of truth for the initial prompt; the project copy is a user-owned override. Helpers like `intake.go` (no scaffold .md exists) inline because there's nothing to override; the inline pattern stays correct for *that* shape, just wrong for rewriter/synthesizer.

**Pre-existing bug fixed while here:** the rewriter and synthesizer agents had no `OutputSchema` declared on the inline `AgentDef`, so the API request had no strict-mode JSON enforcement. Gemini 3 occasionally returned `{"prose":"..."}` instead of `{"revised_body":"..."}` — silently wiping the parent Description because `cascade.RewriteFeature` treats an unmatched RevisedBody as "empty body, save it." Registering `RewriteResult` in `cascade`'s `init()` and setting `output_schema: RewriteResult` in the scaffold .md frontmatter forces strict-mode matching against the actual struct. The schema lives in cascade (not internal/agent) so the registered example stays the source of truth alongside the consumer.

**Cuts from the original plan, both for the no-aspirational-fields rule (mirrors the `--against-finding` cut from DJ-101's justify):**

- `--address-finding <session>:<idx>`: sugar to load a recorded critic finding as the brief. No real consumer today; the workflow (review → recall finding → re-challenge) hasn't surfaced. ~10 LoC if it does.
- `--dry-run --diff` combined mode: requires plumbing "LLM-without-save" through every cascade rewriter so the diff renders against a proposed-but-not-persisted node. Invasive for marginal gain over the existing `--dry-run`-as-cascade-blast-radius preview. Add when the use case appears.

**MCP parity:** the existing `refine` MCP tool gains `brief`, `diff`, `rollback` parameters with the same handler shape. Mutual-exclusion validation lives in the shared dispatcher so CLI and MCP enforce the same invariants. An IDE-side agent driving "the SRE critic flagged X; address it" issues `refine({id, brief: "..."})` and gets the diff back as the tool result — recoverable via `refine({id, rollback: true})` if the agent's interpretation was off.

**Reversal criteria:** revert `--brief` if observed refine-mode rewrites drift from the user's intent more often than they hit it (the refiner becomes a noisy paraphrase). Revert `--rollback` if real users discover that decision-path refines (which don't auto-record `spec_refined` events because the user-edited decision has no pre-state) leave them confused about what's rollback-able. The rollback gap is documented in the result Note; if the doc is insufficient, the cascade path needs its own pre-edit capture.

**Reference:** depends on DJ-100's per-node renderers (consumed by `--diff`). Bridges from DJ-101's `justify --against` adversarial verdict, which suggests a `refine --brief "..."` invocation pre-populated with the breaking points — closes the deliberation loop. DJ-097 ("rules belong in the agent's `.md`") is the precedent that justified moving prompts off inline literals to scaffold .md files.

## DJ-103: History Narrative Is a Cache, and the Archivist Should Tell a Story

**Status:** shipped

**Decision:** Three coupled changes to the narrative-history surface (DJ-026 layer 2):

1. **Auto-regenerate on staleness.** `locutus history --narrative` is now self-sufficient. It hashes the current event set, compares against the embedded hash in the cached `summary.md`, and regenerates transparently when they diverge — printing a one-line `Regenerating narrative…` notice to stderr and the refreshed summary to stdout. `--regenerate-narrative` is preserved for the bounded-window (`--since`/`--until`) and `--force` cases but is no longer a prerequisite for reading the narrative.

2. **Relocate the cache to `.locutus/history/`.** The events themselves stay committed in `.borg/history/evt-*.json` (source of truth). The rendered `summary.md` and `details/<id>.md` move under `.locutus/history/` (gitignored, machine-local). The narrative is a regenerable cache, and treating it as such cleans up the layout: `.borg/` = "what the project knows about itself" (versioned), `.locutus/` = "what this run / this machine derived" (transient). Mirrors DJ-091 (`.locutus/sessions/`) and DJ-073 (`.locutus/workstreams/`).

3. **Unstarve the archivist.** The archivist agent used to receive only `id, timestamp, kind, target, rationale` and a directive that "one line per event is plenty for the timeline." Result: a summary worse than `git log --oneline`. Now the archivist receives the full event payload (Old/New values, alternatives, rationale) and a substantive prompt asking for narrative prose that captures **what changed, why, and what the current state is** for each affected node. Smoke test against winplan turned a 7-line list into a multi-paragraph narrative that names the description-wipe bug, walks the three refine→rollback cycles for `feat-campaign-performance`, and identifies the current "reset" state. The mechanical timeline survives as a `--narrative` fallback when no LLM provider is configured.

**Why these three move together:** they're the same UX critique seen from three angles. The user pointed out the summary "is actually worse than `git log`" because it lists events without telling a story; that's (3). The same session asked why the user has to run `--regenerate-narrative` first; that's (1). And it surfaced that the artifact is committed when it's plainly derived state; that's (2). Each fix on its own would feel half-done; together they reframe the narrative as "the archivist's interpretation of the committed events, regenerated on demand from a machine-local cache."

**Why agent-prompt updates ship in the binary, not via `update --reset`:** prior commits revealed that `cascade.invokeRewriter` and `cmd/refine.go invokeSynthesizer` carried inline `agent.AgentDef` literals that masked their scaffold .md files (DJ-102 fixed that to load via `scaffold.LoadAgent`). The history narrative path went through `agent.NamedAgentFn`, which loads only from `.borg/agents/<id>.md` — meaning a stale per-project copy from an old `init` masked binary upgrades to the archivist prompt. The history paths now use `scaffold.LoadAgent` (project-FS-first, embedded-fallback). A user with a customized `.borg/agents/archivist.md` keeps their override; a user who never edited it gets the new prompt the moment they upgrade the binary. `agent.NamedAgentFn` stays in the codebase pending broader migration of other call sites (decoupling `internal/agent` from `internal/scaffold` for the cycle remaining is out of scope here).

**The deeper tension:** scaffolds copied to the project on `init` (`.borg/agents/`, `.borg/workflows/`) become silent staleness traps when the binary ships updates. The `scaffold.LoadAgent` pattern (project FS first, embedded fallback) addresses the stale-copy case for callers that opt in, but doesn't fix the underlying issue: `init` copies files unprompted. A future cleanup could (a) stop pre-copying the scaffolds and treat `.borg/agents/<id>.md` purely as opt-in overrides, or (b) version-stamp the scaffold copies and detect "unchanged from copy" to prefer the embedded version on upgrade. Out of scope for this DJ; flagged for follow-up.

**Backwards compatibility:** existing projects with `.borg/history/summary.md` and `.borg/history/details/` from prior runs are not migrated automatically. Those files become orphaned the next time a user runs `--narrative` (which will write to `.locutus/history/` instead). Users can `git rm` the legacy paths once they've validated the new surface. Pre-alpha; no operator depends on the old path.

**Reversal criteria:** revert (1) auto-regen if stale-cache regeneration takes long enough that users prefer the explicit two-step (regenerate, then read) flow; the LLM call cost is real. Revert (2) location move if a workflow surfaces where teammates need the rendered narrative committed (e.g., reading on GitHub without running locutus). Revert (3) the archivist substantive rewrite if the longer summary turns out to drift from the events more often than the terse one did — the structural data inputs (Old/New values) make hallucination less likely than the rationale-only diet, but it's a measurement question, not a guaranteed win.

**Reference:** depends on DJ-026 (layer-2 narrative pipeline), DJ-091 / DJ-073 (`.locutus/` for derived state), DJ-097 (rules belong in `.md`), DJ-102's `scaffold.LoadAgent` helper. Bridges from DJ-102's auto-history (`spec_refined` events with Old/New values) — without that structured payload, the substantive archivist would have nothing to chew on.
