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

**Status:** shipped (partially superseded by DJ-119 and DJ-121)

**Superseded in part by [DJ-119](#dj-119-agent-client-protocol-replaces-the-coding-agent-driver-layer) (2026-05):** the wire layer between supervisor and coding agent — the per-CLI NDJSON driver model originally described here — has been replaced with an Agent Client Protocol (ACP) client. The transport layer changed; what remains of the original wire layer is gone. Read DJ-119 for the current story on how Locutus talks to coding agents.

**Refined further by [DJ-121](#dj-121-coarsen-pre-planning-to-workstream-grain-agent-owns-step-decomposition-via-worktree-checklist-refines-dj-010-dj-074-dj-120) (2026-05):** the supervision *grain* described here was per-`PlanStep`: pre-plan each Approach into ordered steps with assertions, validate each step, retry each step with feedback. DJ-121 coarsens that grain to per-Workstream and removes `PlanStep` from the spec model. The retry-and-validate loop and the supervisor's dual function (validation + HIL against the spec DAG) are preserved unchanged; the agent now owns step decomposition via a worktree-resident `_locutus/checklist.md`. Workstream execution also becomes sequential-by-default (parallel is opt-in) per DJ-121's correctness-over-throughput stance. Read DJ-121 for the current planning-and-execution shape.

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

**Status:** shipped (library claim below superseded by DJ-118; struct-tag + registry pattern remains valid)

**Decision:** Agent frontmatter can specify `output_schema: MasterPlan` (or other registered type name). At `BuildGenerateRequest` time, Go reflects the corresponding type and appends a JSON schema to the system prompt. Struct tags (`jsonschema:"description=..."`) provide field-level documentation. A `schemaRegistry` maps type names to example instances.

**Why:** LLMs produce more reliable structured output when given an explicit schema, and descriptions next to fields keep the schema in sync with Go code. The alternative — inlining schemas as Markdown in agent `.md` files — drifts from the Go types over time.

**Pattern:** `github.com/google/jsonschema-go` (already a transitive MCP SDK dependency) handles reflection. Equivalent to Pydantic's `Field(description="...")` in Python.

> ⚠ **Library claim superseded by DJ-118.** Implementation chose `github.com/invopop/jsonschema` (not google) for its richer struct-tag vocabulary (`enum=`, `minItems=`, `description=` as key=value pairs vs google's single-description-string model). See DJ-118 for the technical reasoning and reversal criteria.

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

**Status:** shipped (2026-04-25); refined by DJ-120 then DJ-121

**Refined by [DJ-120](#dj-120-adopt-resume-narrows-to-step-level-under-the-acp-lifecycle-refines-dj-074) (2026-05) and [DJ-121](#dj-121-coarsen-pre-planning-to-workstream-grain-agent-owns-step-decomposition-via-worktree-checklist-refines-dj-010-dj-074-dj-120) (2026-05):** the resume-grain promise has narrowed twice. DJ-074 (below) committed two layers — *step-level* resume (worktree rebuilt from feature branch, completed PlanSteps skipped) and *conversation-level* resume (`--resume <AgentSessionID>` on the coding-agent CLI). DJ-120 dropped conversation-level resume when the ACP lifecycle (DJ-119) replaced the driver model — the agent subprocess no longer survives a Locutus restart, so there's no conversation to revive. DJ-121 dropped step-level resume on the Locutus side when `PlanStep` was removed from the spec model; step continuity is now preserved at the *agent's* level via a worktree-resident `_locutus/checklist.md`. The feature-branch durability guarantee below — completed workstreams' work persists on `locutus/<ws-id>` — is unchanged. Read DJ-121 for the current resume contract.

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

## DJ-104: `scout_brief` Is a First-Class Citation Kind

**Status:** shipped

**Decision:** Add `scout_brief` to the `Citation.Kind` enum alongside `goals`, `doc`, `best_practice`, `spec_node`. Both spec elaborators (`spec_feature_elaborator.md`, `spec_strategy_elaborator.md`) drop the legacy rule that forbade citing the scout brief — *"if a fact came from the scout brief … do not fabricate a citation kind for it — find a `best_practice` or `goals` anchor that justifies the same conclusion"* — and gain explicit guidance to cite `scout_brief` when a decision rests on a fact the (grounded) scout retrieved. The new kind requires `excerpt` verbatim, mirroring `goals` and `doc`, so grounded provenance survives the survey artifact being gone.

**Why:** the prior rule actively *severed* grounded provenance at the boundary between the scout (DJ-093, grounded) and the elaborators (ungrounded). When the scout's `technology_options` flagged a current major version or a vendor lifecycle shift, the elaborator was instructed to recast that grounded fact as a `best_practice` claim — which DJ-085 explicitly defines as a *"named precise best practice ('12-factor app: stateless processes' — not 'industry best practices')."* A version pin or vendor status is neither. The result was either (a) a vague best_practice citation that an architect_critic flag would catch under DJ-085 rule 6, or (b) the elaborator skipping the decision entirely. Either way, the system was discarding grounded provenance the scout had paid (in search-tool budget) to retrieve.

**Why this isn't a re-introduction of the failures DJ-090's follow-up warned about:** that warning targeted *aspirational fields in LLM output schemas* — fields downstream code didn't consume, which created shape pressure on weaker models to fill them with degenerate content (the named precedent was the Span citation removal). DJ-104 does not add a field. It adds an enum value to an existing field that elaborators already populate. The shape pressure on the model is unchanged: the same `Citations []Citation` slot is filled with one more allowed kind. Models that have nothing scout-brief-derived to cite continue to cite `goals` / `doc` / `best_practice` / `spec_node` exactly as before — the new kind is opt-in by the model's content, not mandated by structure.

**Why this doesn't trip DJ-093's reversal criterion (a):** DJ-093 reserved the right to revert grounding if scout output started "search-result-aggregation displacing engineering judgment," and was explicit that grounding is *"not a license to add output schema fields."* DJ-104 doesn't touch the scout's prompt, schema, or output shape — the scout brief is unchanged. What changes is downstream: an elaborator that *consumes* the brief is now allowed to cite it instead of laundering it. That puts more weight on the brief's existing fields (`technology_options`, `watch_outs`, `implicit_assumptions`) but doesn't ask the scout to produce more of them. Reversal criterion (a) was about scout *production* drift; this is consumption.

**The strict-form choice (excerpt required):** `scout_brief` joins `goals` and `doc` in the excerpt-required tier rather than `best_practice` and `spec_node` in the excerpt-optional tier. Reasoning: an excerpt-optional `scout_brief` re-opens the laundering loophole — a model could cite "scout_brief: technology_options" as bare reference and lose the grounded text it was supposed to preserve. The whole point of the kind is that the verbatim scout claim travels with the decision durably; making excerpt optional defeats that. The cost is one extra rule the elaborator must follow, which is cheap relative to the laundering it prevents.

**What lands:**

- `spec.Citation` doc comment lists `scout_brief` and notes the excerpt requirement. The struct is unchanged — `Kind` is a string, `Excerpt` already exists — so no schema migration is needed for prior decisions.
- Both elaborator scaffolds add the `scout_brief` row to the per-kind requirements list and replace the legacy "do not fabricate a citation kind" paragraph with positive guidance ("Prefer the most specific kind that fits…").
- Render (`internal/render/spec.go`) needs no change — citation rendering is generic over `Kind`.
- Reconciler (`internal/agent/reconcile.go`) needs no change — citations pass through untouched.
- `TestElaboratorPromptsAllowScoutBriefCitations` in `internal/scaffold/scaffold_test.go` locks in the prompt-level change against accidental regression.

**What stays the same:**

- The scout's prompt, output schema (`ScoutBrief`), and grounding posture (DJ-093). The brief is an unchanged input to elaborators.
- The other four citation kinds and their existing rules. `best_practice` still requires "named precise principle"; `goals` and `doc` still require excerpts.
- DJ-085's denormalization model — citations live on the decision; they do not become pointers.
- Existing decisions in `.borg/spec/` with `goals`/`doc`/`best_practice`/`spec_node` citations. Nothing about prior-authored content needs to change.

**Reversal criteria:** revert if (a) elaborators in real council runs cite `scout_brief` for facts that aren't actually scout-derived (laundering in the other direction — using `scout_brief` as a synonym for "I don't have a real citation"), at which point the architect_critic's rule 6 needs sharpening to validate the cited excerpt actually appears in the scout brief; or (b) the strict excerpt requirement turns out to dramatically reduce `scout_brief` use in practice (model finds it easier to skip than to copy the verbatim text), suggesting either the prompt needs more instruction or the requirement should soften. Neither is structural; both are tunable in the existing files.

**Reference:** extends DJ-085 (citation kinds), governed by DJ-090's follow-up (no aspirational fields) and DJ-093 (scout grounding's "shape unchanged" mandate). The architecture lesson — *grant grounding to discovery agents, propagate grounded provenance forward via citation rather than re-grounding every consumer* — is the same one that produced DJ-093 itself. DJ-104 closes the propagation step that DJ-093 left implicit.

## DJ-105: Elaborator `decisions` Is API-Layer Required, Not Prompt-Layer Required

**Status:** shipped

**Decision:** Tighten both `RawFeatureProposal` and `RawStrategyProposal` so the strict-mode JSON schema requires `decisions` with `minItems=1`, and remove the contradicting "or omit `decisions` entirely" escape hatch from both elaborator scaffolds. Strict-mode-conformant providers (Gemini responseSchema, OpenAI json_schema strict, Anthropic forced tool-use) will now reject decision-less responses at the API layer; the executor's retry loop kicks in instead of the malformed proposal flowing through to reconcile and dying at the spec validator.

**Why:** a `refine goals` run on the winplan project failed with six dangling-reference errors after two revise rounds — every error was "feature has no decisions; every feature must commit to at least one architectural choice." The session traces revealed two distinct pathologies that converged:

1. *Decision-omitting outputs* (e.g., session call `0004-spec_feature_elaborator-feat-campaign-dashboard.yaml`): the model's thinking transcript planned two real decisions, then the final JSON emitted only `id`/`title`/`description` with no `decisions` field. `finishReason: STOP`, 14-second duration, 102 output tokens — the model voluntarily ended.
2. *Mid-stream-redo-into-string-field* (e.g., session call `0035-spec_feature_elaborator-feat-win-calculator.yaml`): the model started emitting valid JSON, mid-stream realized it had spilled prose into the title field, wrote `"Wait, I shouldn't add prose. Let me start over"` and then a complete corrected JSON document — all captured *as part of the title string value*, with no escape from the structural commitment. Top-level object ended without `decisions`.

Two contributing factors made the pathologies survivable in the pipeline:

- The struct tag `Decisions []InlineDecisionProposal json:"decisions,omitempty"` made the field optional in the strict-mode schema. The reflector at [`internal/agent/schema.go`](../internal/agent/schema.go) computes the `required` array from `omitempty` tags via `RequiredFromJSONSchemaTags: false`, so the API contract didn't enforce presence.
- The elaborator prompts carried mutually contradictory rules four lines apart: *"Every feature MUST have at least one inline decision"* and *"Emit real, complete inline decisions or omit `decisions` entirely (and reconsider whether the feature belongs)."* The second rule gave the model a permission slip the first rule denied. Under uncertainty, `gemini-3.1-pro-preview` repeatedly took the easier path.

**Why API-layer enforcement, not post-receive validation:** strict-mode JSON schema rejection happens server-side at the provider, *before* a non-conformant response is returned to us. The executor's retry loop re-issues the call, and the model has to produce a conformant output to satisfy the API. Post-receive validation in the elaborator dispatch path would also work but is downstream of where the cost is paid: the model has already burned thinking tokens and output tokens on a malformed response, and the operator sees the failure as a soft error rather than an automatic retry. Schema enforcement is structural; post-receive validation is reactive.

**Why this isn't a re-introduction of the failures DJ-090's follow-up warned about:** that warning targeted *aspirational fields in LLM output schemas* — fields downstream code didn't consume, which created shape pressure on weaker models to fill them with degenerate content (the named precedent was the Span citation removal). DJ-105 does not add a field. It tightens the constraint on a field that downstream code (reconciler, validator, render) already requires for the spec to be well-formed. The shape pressure is unchanged in cardinality; the API just stops accepting empty arrays where empty arrays were never going to survive the rest of the pipeline anyway.

**What lands:**

- `RawFeatureProposal.Decisions` and `RawStrategyProposal.Decisions` lose `omitempty` and gain `jsonschema:"minItems=1"`. The reflector emits `required: ["...", "decisions"]` and `decisions.minItems: 1` in the generated schema.
- Both elaborator scaffolds delete the "or omit `decisions` entirely" clause and replace it with positive guidance: when a complete decision genuinely cannot be authored, emit a minimal "Defer architectural commitment" decision so the critic can route the feature/strategy for removal — but always emit a conformant response.
- `TestRawProposalSchemasRequireDecisions` in [`internal/agent/raw_proposal_schema_test.go`](../internal/agent/raw_proposal_schema_test.go) locks in the schema requirement against accidental regression (e.g., someone re-introducing `omitempty`).
- `TestElaboratorPromptsForbidDecisionsOmission` in [`internal/scaffold/scaffold_test.go`](../internal/scaffold/scaffold_test.go) locks in the prompt-level change.

**What stays the same:**

- The reconciler ([internal/agent/reconcile.go](../internal/agent/reconcile.go)) still passes `Citations` and `Decisions` through unchanged. The integrity validator catches dangling references as a defense-in-depth backstop, but the API-layer enforcement makes that path much rarer.
- The architect's own `RawSpecProposal` shape (different from the elaborator's per-node shapes). The architect operates pre-fanout; this DJ scopes to the per-node elaborators only.
- The acceptance_criteria field on RawFeatureProposal stays optional (`omitempty`). It's a quality-of-life field, not load-bearing for spec integrity.

**Reversal criteria:** revert if (a) the strict schema causes legitimate retries to spiral on edge cases where a model genuinely cannot produce a decision (would surface as repeated retry failures on the same node — at which point the prompt needs better guidance on the "Defer architectural commitment" escape pattern, not relaxed schema); or (b) a future architecture genuinely needs decision-less proposal nodes (would mean the elaborator's contract has changed and this DJ should be replaced rather than relaxed). Neither is structural; both are addressable in the same files.

**Reference:** governed by DJ-085 (decision-citation completeness), DJ-090's follow-up (no aspirational fields — and what that rule actually targets vs. what it doesn't). Companion to DJ-104 (citation-kind expansion). Distinct from the timeout / fallback bugs surfaced in the same session — `gemini`-side 504 misclassification and per-call timeout wrapping the entire fallback walk are real but separate concerns; the symptom user-visible from the failed run was DJ-105's pathology, not those.

## DJ-106: User-Message Prompt Caching for Anthropic via `Cacheable` Flag

**Status:** shipped

**Decision:** The neutral `Message` type carried by `AgentInput` and `adapters.Request` gains a `Cacheable bool` field. Projection layers that build the user-side conversation emit the static prefix shared across council fanout (GOALS body, scout brief, outline) as a `Cacheable=true` message, followed by a `Cacheable=false` message carrying the per-call variation. The Anthropic adapter merges adjacent same-role messages into a single `MessageParam` with one `TextBlockParam` per source `Message`, attaching `CacheControl: NewCacheControlEphemeralParam()` to the block whose source was flagged `Cacheable`. Other adapters ignore the flag — Gemini caching uses a separate `cachedContent` resource API; OpenAI's Responses API caches identical prefixes server-side automatically without explicit markers.

The cache_control marker on the system prompt was already in place ([`anthropic.go`](../internal/agent/adapters/anthropic.go#L82-L87) since DJ-099); DJ-106 extends caching to the much larger user-message prefix.

**Why:** the spec-generation council's elaborator fanout (DJ-090) dispatches 15-25 calls per `refine goals` invocation, every one carrying the same ~3-5k tokens of GOALS + scout brief + outline as the prefix of its user message. With no user-message caching, the fanout pays input-token cost for that prefix on every call AND consumes TPM budget proportional to (prefix tokens × N calls). Anthropic's ephemeral prompt cache (5-minute TTL, ~10% input-token cost on cache reads) is a near-perfect match for fanout-shaped workloads where N parallel calls fire within seconds of each other:

- **Cost:** the second-through-Nth call's static prefix bills at cache-read rates instead of full-input rates. For a 25-call fanout with a 4k-token static prefix, that's roughly 96k tokens shifted from full-cost to ~10% cost per refine.
- **TPM pressure:** cached input tokens count differently against rate limits (per Anthropic docs as of May 2026), so the same fanout consumes less TPM budget — meaningful on lower-tier accounts where the council can otherwise saturate the bucket.
- **Latency:** cache reads are faster than full prefix reprocessing, especially noticeable when the parallel batch hits the same node simultaneously.

**Why a `Cacheable bool` per-Message field, not a richer cache-region API:** the projection layer already emits `[]Message` and the boundary between "static across fanout" and "varies per call" is a single break — for the elaborator, between the outline section and the per-target header. A boolean per message is the smallest API surface that captures the boundary. Richer designs (multiple cache regions, TTL hints, tool/system markers as separate fields) would lock in design choices that haven't been measured yet. The flag is additive and cheap to extend later if a future projection needs more than two regions.

**Why grouping adjacent same-role messages, not preserving 1:1 Message→MessageParam:** Anthropic's API rejects consecutive same-role messages — alternation is required. More importantly, the cache marker's positional semantics ("everything in the request up to and including this block is the cacheable prefix") only work within a single message that holds multiple TextBlocks. A 1:1 mapping would split the prefix and the variation across two separate API messages, which makes the cache marker apply to a fragment that doesn't include the system prompt or any earlier content — defeating the purpose. Grouping is mandatory, not optional.

The grouping path activates only when at least one input Message carries `Cacheable=true`; otherwise the per-Message MessageParam shape is preserved so callers that don't care about caching see no behavioral change.

**Why now, not earlier:** the council fanout, the direct-SDK migration (DJ-099), and explicit cache markers on the system prompt all landed before today, but the user-message prefix wasn't getting cached because the projection layer concatenated GOALS + scout + outline + per-call target into a single `Message` content string. The boundary information was lost at the projection layer; the adapter had nothing to mark. DJ-106 plumbs the boundary through as a `Cacheable bool` on `Message` and lets the adapter act on it.

**What lands:**

- `agent.Message` and `adapters.Message`: new `Cacheable bool` field with comments explaining the cross-adapter behavior.
- `executor.go` `buildAdapterRequest`: passes the flag from `AgentInput.Messages` through to `adapters.Request.Messages`.
- `adapters/anthropic.go` `buildAnthropicMessages`: new grouping path triggered by `anyCacheable`; same-role runs merge into one `MessageParam` with multiple `TextBlockParam` blocks; cache_control set on Cacheable blocks via `NewCacheControlEphemeralParam`. Helpers `textBlockFromMessage`, `oneBlockMessageParam`, `anthropicMessageRole` factor the construction.
- `projection.go` `projectElaborateOne`: now emits two `Message`s — `Cacheable=true` prefix (GOALS + scout brief + outline) and `Cacheable=false` suffix (per-call fanout target). The semantic content of the user message is unchanged; only its segmentation differs.
- `TestBuildAnthropicMessages_CacheableMarksBlock` and `TestBuildAnthropicMessages_NoCacheableUnchanged` lock in the adapter behavior. `TestProjectElaborateOne_SplitsCacheableFromVariable` locks in the projection split.

**What stays the same:**

- The Gemini and OpenAI adapters. Gemini's `cachedContent` API is a separate workstream — it requires explicit cache resource creation/destruction with TTL management, which doesn't fit the interactive `refine goals` UX as cleanly. Defer until measurement justifies it. OpenAI's Responses API already does automatic prefix caching server-side; explicit markers would add complexity without clear benefit.
- All other projections. Only `projectElaborateOne` (the per-feature/per-strategy elaborator) splits today. The cluster-finding projection ([`projection.go:226`](../internal/agent/projection.go#L226)) is a candidate for the same treatment when fanout-shape behavior shows up there in real runs; it's not free and shouldn't ship pre-emptively.
- The system-prompt cache_control already in place since DJ-099. DJ-106 adds a second cache marker; Anthropic's API allows up to 4 per request, so we're well within budget.

**Reversal criteria:** revert if (a) cached prefix invalidation patterns produce *worse* aggregate latency than the uncached path (would suggest the 5-minute TTL is mismatched to actual fanout cadence — possible if a single `refine goals` run takes >5 minutes between scout and elaborator fanout, in which case the cache expires before the fanout dispatches, but the second-through-Nth elaborator call within the fanout would still hit the cache, so net positive); or (b) a future projection needs to mark non-contiguous cacheable regions and the simple boolean shape becomes a structural blocker (in which case the field evolves to a richer cache-region API rather than disappearing). Neither is structural; both can be addressed in the same files.

**Reference:** governed by the 2026-05 best-practice guidance (Anthropic ephemeral prompt cache: 5-min TTL, 1024-token minimum prefix, up to 4 markers per request, ~10% input cost on cache reads, billing/TPM accounting per the Anthropic Build documentation). Builds on DJ-099 (direct-SDK migration that exposed cache_control as a first-class API surface), DJ-090 (the per-node fanout that creates the caching opportunity), and DJ-099-era system-prompt caching (which added the first cache marker; DJ-106 adds the second). Distinct from the planned Anthropic Sonnet/Opus tier pricing review and from any future Gemini `cachedContent` integration — those are separate workstreams.

## DJ-107: Council Model-Tier Audit + Anthropic-First Provider Order

**Status:** shipped

**Decision:** Three coordinated changes to agent frontmatter across the 32-agent council:

1. **Tier downgrades on four agents.** `spec_feature_elaborator`, `spec_strategy_elaborator`, `spec_scout`, and `guide` move from strong → balanced. Strong tier is now reserved for `spec_architect` and `spec_reconciler` only.
2. **Provider order flipped to anthropic-first** on every agent whose primary work is structured-output generation or critique (~25 of 32 agents). Cost-first ordering (`googleai → anthropic → openai`) is retained only for mechanical / cost-sensitive agents — `historian`, `monitor`, `convergence` — and for `spec_reconciler` which was already anthropic-first.
3. **`cost_critic` gains `grounding: true`** with prompt guidance to use search for verifying current pricing/free-tier limits/vendor lifecycle, since training-data pricing ages quickly. Brings the count of grounded agents to three (`spec_scout`, `researcher`, `cost_critic`).

**Why the tier downgrades:**

The two elaborators are the council's biggest fanout (15-25 calls per `refine goals`). Per-feature / per-strategy elaboration is a medium-complexity strict-JSON task — committing on architectural shape with rationale, alternatives, and citations — not deep multi-step reasoning. Sonnet 4.6 (balanced) handles this shape well; Opus 4.7 / o3-pro / `gemini-3.1-pro-preview` (strong) are paying a substantial cost premium for a marginal capability premium that doesn't materially change output quality on this task. Combined with DJ-106's prompt caching, the cost compression on the elaborator fanout is the single biggest operational win in this set.

`spec_scout` runs once per refine and produces a `ScoutBrief` (domain read + technology options + implicit assumptions + watch-outs). It needs grounding (per DJ-093) but doesn't need strong-tier reasoning — `gemini-3-flash-preview` supports grounding+schema together and is more stable under strict JSON than `gemini-3.1-pro-preview` (which exhibited the title-stuffing pathology in the winplan run that produced DJ-105). Downgrade is both a quality and cost win.

`guide` is a conversational/narrative role; strong tier is paying for reasoning depth the agent doesn't use.

**Why anthropic-first provider order on most agents:**

Three observed-evidence reasons:

1. The strict-JSON pathology that killed the winplan `refine goals` run (DJ-105) was on `gemini-3.1-pro-preview`. Putting Anthropic first for any agent with a non-trivial `output_schema` reduces exposure to that failure mode without removing Gemini as a fallback.
2. With DJ-106 prompt caching on Anthropic, the cost differential between Anthropic and Gemini on cached fanout calls compresses significantly — the historical cost-first argument for `googleai-first` was load-bearing in 2023-2024 but is much weaker in 2026 with caching.
3. With DJ-093/DJ-104 putting all grounded agents (`spec_scout`, `researcher`, now `cost_critic`) on grounding-capable models, the "Gemini first because Google has the strongest search index" argument also weakens — Anthropic and OpenAI both have native grounding now, and Sonnet's reasoning over retrieved content compensates for any modest search-quality gap.

**Bias acknowledgment.** This audit was authored by Claude (Anthropic's model) and the recommendation to put Anthropic first carries a real bias surface that the operator (Chetan) flagged explicitly during review. Self-reported claims about Sonnet's strict-JSON reliability, reasoning quality on retrieved content, and synthesis prose quality are model-mediated and lack independent benchmark evidence in this conversation. The defensible evidence is narrower:

- Concrete observation: `gemini-3.1-pro-preview` produced pathological JSON on specific elaborator calls in the winplan run (DJ-105).
- Concrete observation: the `gemini-3-flash-preview` swap is a viable mitigation per the user's grounding+schema constraint analysis.
- Conjecture (model-mediated, not benchmarked): Anthropic generally handles strict JSON more reliably than Gemini on the same workload.

The third claim is the load-bearing one for "anthropic-first across the council," and it is the weakest link in the chain. The reversal criteria below treat it as such: if measurement contradicts the conjecture in real runs, the order flips back without ceremony.

**What lands:**

- 22 agents reordered to `anthropic-first` at balanced tier (analyst, architect_critic, archivist, backend_analyzer, cost_critic, critic, devops_critic, frontend_analyzer, gap_analyst, infra_analyzer, planner, preflight, refiner, remediator, reviewer, scout, spec_finding_clusterer, spec_outliner, sre_critic, stakeholder, synthesizer, validator).
- `researcher` reordered to anthropic-first balanced (was googleai-first), grounding flag retained.
- `rewriter` and `llm_judge` reordered to anthropic-first fast.
- `spec_architect` reordered to anthropic → openai → googleai at strong tier (avoid `gemini-3.1-pro-preview` second on the largest schema in the system).
- `spec_feature_elaborator`, `spec_strategy_elaborator` downgraded to anthropic-first balanced (S → B).
- `spec_scout`, `guide` downgraded to anthropic-first balanced.
- `cost_critic` gains `grounding: true` and a "Use Search to Verify Current Pricing" section in its prompt mirroring `spec_scout`'s sanity-check framing.

**What stays the same:**

- `historian`, `monitor`, `convergence` keep `googleai-first fast` ordering. These are mechanical / cost-sensitive roles where the bias-corrected logic still favors cost over quality, and the failure modes aren't strict-JSON-pathology-shaped.
- `spec_reconciler` keeps its existing `anthropic → openai → googleai strong` ordering. Already correct.

**Reversal criteria:**

- Revert tier downgrades if balanced-tier elaborator output proves substantively shallower than strong-tier in real runs (would surface as repeat critic findings on rationale depth, named-principle imprecision, or alternative-list thinness). Concrete signal: `architect_critic` rule 6 firings increase materially after the downgrade.
- Revert anthropic-first ordering if Anthropic-side rate-limit cascades become the dominant failure mode in production runs, suggesting the Anthropic Build tier ceiling is the binding constraint and `googleai-first` was load-balancing better than I credited.
- Revert `cost_critic` grounding if the agent starts citing tangential pricing data (overfilling against DJ-093's reversal criterion (a)) — would suggest the prompt's "sanity check, not enumeration" framing needs sharpening rather than the flag being wrong.

None of these are structural; all are addressable in agent frontmatter without code changes.

**Reference:** governed by DJ-093 (grounding mandate) and its reversal criterion (a) for the cost_critic addition; DJ-099 (direct-SDK migration that made tier choice meaningful per-provider); DJ-105 (the strict-JSON pathology that motivated the anthropic-first lean); DJ-106 (prompt caching that compresses the cost-of-anthropic argument). Bias caveat: the operator flagged the model's self-favoring lean during the audit conversation — the reversal criteria above are written to honor measurement-driven correction rather than defending the recommendation against revision.

## DJ-108: Anthropic Native Structured Output + Adaptive Thinking

**Status:** shipped

**Decision:** Migrate the Anthropic adapter from synthetic-tool-plus-forced-`tool_choice` strict-mode JSON enforcement to native `MessageNewParams.OutputConfig.Format.Schema`, and from the deprecated `thinking: {type: enabled, budget_tokens: N}` API to `thinking: {type: adaptive}` paired with `OutputConfig.Effort` ("low" | "medium" | "high" | "max"). Both changes ship together because they're entangled: the deprecated thinking-budget API is no longer supported on Opus 4.7+, and the strict-mode JSON enforcement via forced `tool_choice` was incompatible with extended thinking even on prior models.

The migration removes ~80 lines of adapter code (synthetic schema tool, forced-tool dispatch branch, "schema tool not invoked" error path, the thinking-allowed-when-not-forced gate from the prior hotfix) and replaces them with a single `OutputConfig.Format` assignment plus adaptive thinking.

**Why now:** the user's first attempt to run `refine goals` after DJ-107's anthropic-first reorder hit a 400 from Anthropic — *"Thinking may not be enabled when tool_choice forces tool use."* A short-lived hotfix suppressed thinking on forced-tool calls; that fix landed and worked, but it was the wrong shape. Investigating the SDK revealed `MessageNewParams.OutputConfig` and `ThinkingConfigAdaptiveParam` — the canonical APIs for structured output and thinking budget on current Claude models — neither of which the adapter was using. The adapter was essentially running on the pre-2026 implementation pattern, which still works on legacy models but not on Opus 4.7+.

**Why this works across all three pinned models:**

- **Opus 4.7 (strong):** the migration guide explicitly says the enabled-budget API is *no longer supported*; adaptive + Effort is the required path.
- **Sonnet 4.6 (balanced):** Anthropic's own prompt-engineering best-practices doc shows `thinking: {type: "adaptive"}` + `output_config: {effort: "high"}` as the canonical pattern for this model.
- **Haiku 4.5 (fast):** structured outputs were enabled in the December 4, 2025 release, expanding parity across the family.

So the migration is uniform — no per-model branching. The Models API exposes per-model capabilities (`structuredOutputs.supported`, `thinking.types.adaptive.supported`, `effort.high.supported`) for runtime introspection if a future need arises to vary behavior, but for the embedded models.yaml, all three pins support the unified path.

**Mapping `ThinkingLevel` to the new API:**

| `ThinkingLevel` | `Thinking` config                | `OutputConfig.Effort` (when schema set) |
| --------------- | -------------------------------- | --------------------------------------- |
| `ThinkingOff`   | unset (default: no thinking)     | unset                                   |
| `ThinkingOn`    | `ThinkingConfigAdaptiveParam{}`  | `medium`                                |
| `ThinkingHigh`  | `ThinkingConfigAdaptiveParam{}`  | `high`                                  |

`Effort` lives on `OutputConfig`, so it only attaches when `req.OutputSchema != nil`. Free-form-output calls get adaptive thinking without an Effort hint and the model self-paces.

**Why this composes cleanly with `Grounding`:** `OutputConfig` and `Tools` are independent fields on `MessageNewParams` — there's no `tool_choice` constraint forcing a specific tool, so the model is free to call `web_search` (or any other tool) before producing the structured response. Per the Anthropic docs example: *"Claude may call the tool first (tool_use) or respond with JSON (text)."* This was the architectural question that motivated the migration: with the old forced-`tool_choice` path, it was unclear whether server tools could fire intermediately. The new path makes it explicit and uncomplicated.

**Code structure changes:**

- New helper: `buildAnthropicMessageNewParams(req Request) MessageNewParams` — pure function, encapsulates all the param-building logic. Unit-testable in isolation; existing `buildAnthropicMessages` and `buildAnthropicTools` are called from inside it.
- `dispatch` loses the `forcedSchema bool` parameter and the entire forced-schema-tool extraction branch. The model's response is now uniformly read from text content blocks regardless of whether a schema was set.
- `buildAnthropicTools` loses the `includeSchemaTool` parameter — it never has reason to emit the synthetic tool now.
- Constants `schemaToolName` ("submit_response") and helper `thinkingAllowed` are deleted.
- Imports clean up (`log/slog` no longer needed in this file since the suppression Warn is gone).

**What lands:**

- 5 new tests in `anthropic_native_output_test.go` covering: `OutputConfig.Format.Schema` is set when schema present; no forced `tool_choice`; no synthetic tool; adaptive thinking + `Effort` mapping for `ThinkingOn`/`High`; thinking off clears both fields; adaptive without schema gets no `Effort`; grounding composes with structured output.
- The hotfix test (`anthropic_thinking_test.go`) is deleted — `thinkingAllowed` no longer exists; the constraint it tested no longer applies.

**What stays the same:**

- The Gemini and OpenAI adapters. Gemini 3+ already composes schema + tools + grounding cleanly; OpenAI's Responses API has had native `json_schema` strict mode since 2024.
- DJ-099-era system-prompt cache_control marker. Anthropic's caching mechanism is independent of the structured-output API.
- DJ-106 user-message prompt caching. Same — independent.
- The retry / fallback / per-pick concurrency / Retry-After honoring layers all remain.
- Spec generation, council shape, agent frontmatter, all remain.

**What gets simpler:**

- Anthropic dispatch is now uniform across schema and free-form calls — both paths read from text content blocks.
- The "Anthropic API rejects extended thinking + forced tool_choice" failure mode is no longer reachable; the gate that suppressed thinking is gone.
- Strict-mode JSON enforcement is server-side without the synthetic-tool intermediary.

**Reversal criteria:** revert if (a) the Anthropic API removes or substantially changes the `OutputConfig` API in a way that affects current models — would surface as a regression on a working refine run after a backend change, in which case fall back to whatever pattern the migration guide of the day prescribes; or (b) some current Anthropic model is found to NOT support `OutputConfig` despite the docs claim (would need per-model capability detection via the Models API at startup, which is a larger change). Neither is structurally hard; both are bounded to this adapter file.

**Reference:** Anthropic structured-outputs documentation (`platform.claude.com/docs/en/build-with-claude/structured-outputs`), Anthropic migration guide for thinking config (`platform.claude.com/docs/en/about-claude/models/migration-guide`), and the December 4, 2025 release note ("Structured outputs now support Claude Haiku 4.5") that confirmed cross-tier availability. Builds on DJ-099 (direct-SDK migration), DJ-105 (the strict-JSON enforcement principle this preserves), DJ-107 (the anthropic-first reorder that surfaced the latent thinking conflict). Supersedes the short-lived `thinkingAllowed` hotfix in commit 5dc0a3a — that fix worked but addressed the symptom; DJ-108 addresses the cause.

## DJ-109: Bypass Anthropic SDK Non-Streaming Preflight via Explicit Per-Request Timeout

**Status:** shipped

**Decision:** Pass `option.WithRequestTimeout(9m45s)` when constructing the Anthropic client. This bypasses the SDK's `CalculateNonStreamingTimeout` preflight check ("streaming is required for operations that may take longer than 10 minutes") which was rejecting any strong-tier request (Opus 4.7 with `max_output_tokens: 32768`) upfront — the SDK's heuristic estimates wall-clock as `(3600s × max_tokens / 128000)`, which is 921s = 15.4 min for those parameters and exceeds the 10-min default ceiling.

**Why an explicit timeout works:** the SDK's preflight at [`client.go:140-142`](https://github.com/anthropics/anthropic-sdk-go/blob/v1.23.0/client.go#L140-L142) returns immediately when `RequestTimeout` is set: *"if the user has set a specific request timeout, use that"*. Setting any non-zero value skips the heuristic entirely. The per-model `ModelNonStreamingTokens` cap doesn't apply to us — our pinned models (`claude-opus-4-7`, `claude-sonnet-4-6`, `claude-haiku-4-5-20251001`) aren't in that map; only older opus-4 / opus-4-1 variants are.

**Why 9m45s and not 30m or 1h:** observed Anthropic call durations on the strong tier top out at 1-3 minutes for the council's reconciler (the most demanding agent). 9m45s gives 3-10× headroom over realistic worst cases while staying just under the SDK's nominal "you should probably stream" mark. Setting it materially higher would say *"we're routinely doing single calls over 10 minutes"* — which would be a real signal to implement streaming, not to keep raising the number. The constant carries that contract in its comment.

**Why not implement streaming now:** streaming is the canonical path for long-running calls and is the right structural fix for genuinely-long workloads. But we don't have evidence yet that real calls exceed 9m45s. Implementing streaming is a substantial change to the dispatch loop (chunk accumulation, partial message handling, citation extraction across stream events) — not worth the lift before measurement justifies it. If a single call ever exceeds the 9m45s ceiling, that's the trigger to migrate.

**What happens if a call does exceed the cap:** the SDK returns a per-attempt timeout error, classified as `ErrTimeout` per Bug A. The retry loop kicks in (with the new Retry-After honoring per DJ-mid-this-session), and the executor's preference-walk fallback fires if Anthropic keeps timing out. None of those paths regress; the cap is a graceful-degradation boundary, not a hard stop.

**What lands:**

- New constant `anthropicRequestTimeout = 9*time.Minute + 45*time.Second` with the load-bearing rationale in its comment.
- `NewAnthropicAdapter` constructs the client with `option.WithRequestTimeout(anthropicRequestTimeout)`.

**What stays the same:**

- The locutus per-call context timeout (`DefaultLLMCallTimeout = 15m`, override via `LOCUTUS_LLM_TIMEOUT`). This still bounds total wall-clock above the SDK timeout.
- Per-agent frontmatter `timeout` field (e.g., `spec_feature_elaborator` has `timeout: 5m`). Those override the default for specific agents.
- Gemini and OpenAI adapters. Their SDKs don't have the same heuristic preflight.

**Reversal criteria:** revert the constant downward if a real call legitimately needs more than 9m45s (signal: session traces show successful calls approaching the cap regularly), at which point streaming is the right migration. Or revert upward if the cap proves too low for a specific agent (in which case prefer per-agent timeout overrides over raising the global cap). The constant is a clear contract; both directions are addressable in the same file.

**Reference:** triggered by a `refine goals` failure on the `spec_reconciler` step in winplan after DJ-107 routed strong-tier agents to anthropic-first. SDK source verified at `anthropic-sdk-go@v1.23.0/client.go:132-157` (`CalculateNonStreamingTimeout`). Anthropic SDK long-requests guidance at `platform.claude.com/docs/en/api/sdks/typescript`. Companion to DJ-108 (which moved Anthropic strict-mode to native `OutputConfig`); each fixed a different latent issue surfaced by the anthropic-first reorder.

## DJ-110: Per-Pick Timeout in `Executor.Run` (Bug B)

**Status:** shipped

**Decision:** The per-call timeout (`AgentDef.Timeout` from frontmatter, or `LOCUTUS_LLM_TIMEOUT`, or the 15-minute default) now applies **per-pick** during the model preference walk, not as a single budget that wraps the entire walk. Each provider attempt gets its own fresh `context.WithTimeout` in `Executor.Run`'s loop body; the previous code wrapped the loop with a single `WithTimeout` that all picks shared.

**Why:** when the first provider in a fallback chain hung the full timeout — a real failure mode observed in winplan's `0024-spec_strategy_elaborator-strat-auth-provider.yaml` (300041ms duration, `model: ""`, `error: context deadline exceeded`) — the per-call context was already dead by the time `Executor.Run` advanced to the next pick. Every subsequent pick failed instantly with `context.DeadlineExceeded`, and the agent's `models:` preference list became theatrical: it documented a fallback chain that only worked when the first provider failed *fast*.

The user-visible failure shape is distinctive: a duration that exactly matches the agent's frontmatter timeout, an empty `model` field (no provider's response ever surfaced its name to the recorder because none completed), and a `context deadline exceeded` error message. Bug A's classifier extension would have routed this case correctly *if there had been budget left* — the issue isn't classification, it's that the budget was already consumed by the time fallback was attempted.

**Why not a per-pick budget split (timeout / N picks):** considered and rejected. Splitting the budget across picks would mean every fallback walk ran with shorter individual budgets, regressing single-pick latency for the common case to subsidize the rare case. Cleaner to give each pick the full configured budget and let the parent context bound total wall clock when that matters (workflow-level deadlines, user Ctrl-C).

**Trade-off:** worst-case wall clock for an N-pick preference list with N timeouts is now N × per-pick timeout instead of a single per-pick timeout. For the council's typical 3-pick lists (anthropic → googleai → openai), that means a triple-failure walk could run up to 3× the configured per-call duration. In practice this is acceptable because:

1. The common case (first pick succeeds) is unchanged.
2. The fallback case (first pick fails fast, second succeeds) gets a fresh budget instead of inheriting a near-empty one.
3. The pathological case (all three pick attempts time out at the wall) was *already broken* before this change — it just failed fast with `context canceled` instead of failing slow with `context deadline exceeded`. The per-pick path actually *succeeds* if any later pick is healthy.

If a deployer needs to bound total walk time for a specific agent, the right tool is a workflow-level context with timeout, not the per-call timeout.

**What lands:**

- [`Executor.Run`](../internal/agent/executor.go) drops the wrap-the-walk `context.WithTimeout` and instead applies `WithTimeout(ctx, perPickTimeout)` inside the per-pick loop, with `cancel()` after each `runOne` call.
- The doc comment on `Run` is rewritten to describe the per-pick semantics and the worst-case wall-clock implication.
- No tests added at this layer — the timeout behavior is hard to unit-test without slow mocks that respect context cancellation. The change is mechanically clear (move `WithTimeout` inside the loop) and verified by the existing test suite + manual verification on the next refine run.

**What stays the same:**

- `AgentDef.Timeout` semantics, env var precedence (`LOCUTUS_LLM_TIMEOUT`), and the 15-minute default (`DefaultLLMCallTimeout`). Operators don't need to retune anything.
- `RunWithRetry`'s exponential backoff + Retry-After handling unchanged. It still re-walks the whole preference list on retry-eligible failures.
- The classifier extensions from Bug A. Server-side 504s still classify as ErrTimeout and now get genuine fallback budget.

**Reversal criteria:** revert if a deployer reports total-wall-clock surprises in production (a 5m timeout becoming a 15m total walk on a triple-failure path). Mitigation in that case is workflow-level context-with-timeout, not reverting per-pick — but the option is open if the change proves operationally surprising.

**Reference:** the deferred fix from the post-DJ-105 / DJ-106 / DJ-107 / DJ-108 / DJ-109 sequence. Bug A and Bug C were prerequisites; without 504 classification (Bug A), the fallback walk wasn't fallback-eligible at all, and without schema enforcement (Bug C), the council couldn't survive even the success case. Bug B closes the chain. Triggered visibly in winplan session 20260507/1522/28-5d9ba2/calls/0024.

## DJ-111: Flatten `ReconciliationVerdict` Schema (Drop `oneOf`)

**Status:** shipped

**Decision:** Replace the `actions[]` discriminated-union (`oneOf` of three variants — `dedupe`, `resolve_conflict`, `reuse_existing` — each with its own required-fields set) with a single flat object schema. The flat shape has `kind` (enum of the three variants) and `sources` as the only required fields; `canonical`, `loser`, `rejected_because`, and `existing_id` are all optional at the schema layer. Per-kind required fields move to prompt guidance (`spec_reconciler.md` already documents them) and apply-time validation (the switch in `reconcile.go:243+` already validates per-kind).

**Why:** Anthropic's native `output_config.format.schema` API rejects `oneOf` constructs. The spec_reconciler dispatch produced a 400 — *"output_config.format.schema: Schema type 'oneOf' is not supported"* — every time it routed to Anthropic (which is now first per DJ-107). The DJ-098-era comment that said *"Strict-mode adapters (Anthropic forced tool-use, Gemini responseJsonSchema, OpenAI json_schema strict) all honor oneOf with enum discriminants"* was true under the legacy forced-tool path; the migration to native output_config (DJ-108) lost the oneOf support specifically on Anthropic, while Gemini and OpenAI still accept it.

**Why flat instead of provider-specific schema:** considered and rejected. A per-provider schema would mean maintaining two shapes (oneOf for Gemini/OpenAI, flat for Anthropic), choosing per-dispatch which to send, and doubling the schema-test surface. The cost-benefit is wrong: the Go-side `ReconciliationAction` is already a flat struct ([reconcile.go:127-134](../internal/agent/reconcile.go#L127-L134)) with all variant-specific fields tagged `omitempty`, the apply switch already does per-kind validation, and the spec_reconciler prompt already documents per-kind requirements. The "loss of strict per-kind required fields at the API layer" is purely on the LLM input side; consumer code never relied on it.

**Why this isn't a regression to "the model emits dedupe without canonical" (the bug DJ-098-era oneOf was preventing):** the original failure was strict-mode JSON enforcement gone permissive — `omitempty` on every Go field made the reflected schema treat everything as optional, so the model was free to produce empty actions. That issue is now addressed at three layers stacked together:

1. The schema's `kind` enum still constrains the discriminator: the model can't emit a typo.
2. The spec_reconciler prompt explicitly documents per-kind requirements (lines 36-38: *"emit it as `canonical`"*, *"emit the rejected decision as `loser`"*, etc.).
3. The apply switch in `reconcile.go` validates per-kind and warns on mismatches (currently warns + skips; could promote to hard error if needed).

So the structural defense against malformed actions is preserved, just reshuffled across layers.

**Provider compatibility matrix:**

| Provider | `oneOf` in JSON Schema |
| --- | --- |
| Anthropic forced tool-use input_schema | accepted (legacy) |
| **Anthropic native `output_config.format`** | **rejected (current)** |
| Gemini `responseJsonSchema` | accepted |
| OpenAI `json_schema` strict | accepted |

The flat shape works uniformly across all three providers.

**What lands:**

- [`buildReconciliationVerdictSchema`](../internal/agent/schemas.go) emits a single object schema for `actions[].items` instead of an `oneOf` of three variants.
- [`TestReconciliationVerdictFlatSchema`](../internal/agent/schemas_reconcile_test.go) replaces the previous `TestReconciliationVerdictDiscriminatedSchema` — same shape of test, different assertions matching the new schema.
- [`TestReconciliationVerdictSchema_NoOneOf`](../internal/agent/reconciliation_schema_test.go) walks the schema tree and asserts `oneOf` is absent at any nesting depth, locking in the cross-provider compatibility.

**What stays the same:**

- `ReconciliationAction` Go struct unchanged.
- `reconcile.go` apply switch unchanged. Per-kind validation already lived there.
- `spec_reconciler.md` prompt unchanged — already documents per-kind requirements.

**Reversal criteria:** revert if (a) Anthropic adds `oneOf` support to `output_config.format.schema` AND (b) measurement shows the flat schema is producing more malformed actions than the discriminated-union shape did. Both conditions would have to hold; (a) without (b) means flatness is gratuitous but harmless, (b) without (a) means we have no provider-uniform alternative. Currently neither is true.

**Reference:** triggered by `spec_reconciler` 400 in winplan session 20260507/1522/28-5d9ba2. Reverses the DJ-098-era oneOf decision specifically for Anthropic compatibility under DJ-108's native structured-output path. The "per-kind enforcement migrates to prompt + apply-time" posture matches the rest of the codebase's general pattern (most agent outputs aren't discriminated-union-validated at the schema layer either).

## DJ-112: Workflows Move from External YAML to Go Values; Agents Stay External (Supersedes DJ-036 on Workflows)

**Status:** shipping

**Decision:** The three council workflows (`PlanningWorkflow`, `AssimilationWorkflow`, `SpecGenerationWorkflow`) live as Go values in [`internal/agent/workflows.go`](../internal/agent/workflows.go). `WorkflowStep` carries closures (`Conditional`, `Fanout`, `Project`, `Merge`) instead of string tags. The YAML loader, the embedded `workflows/*.yaml` files, the scaffold seed/reset paths for them, and the four parallel string-keyed switches (`shouldRunConditional`, `mergeResults`'s big switch, `extractFanoutItems`, `ProjectState`'s stepID dispatch) are deleted.

**Agents remain external.** `.borg/agents/<id>.md` is unchanged — DJ-036's reasoning ("advanced users tune prompts, change models per agent, version per project") still holds for agent files. Workflows were the part of DJ-036 that didn't pay off in practice: every novel step needs Go-side wiring (a new merge handler, conditional, projection, possibly fanout extractor), so a user could only reorder or toggle steps from YAML — a customization affordance no one used. The YAML was implicitly author-only; making that explicit removes the fiction without losing user customization that ever existed.

**Why a single source per step beats parallel string-keyed switches.** Defining a step under the YAML+switches design required edits in 4–5 files: the YAML entry plus a case in each of `shouldRunConditional`, `mergeResults`, `extractFanoutItems`, and `ProjectState`. Behavior was scattered across files keyed only by step IDs, so a typo or stale switch case failed silently at runtime. Closure fields put a step's full behavior in one struct literal next to the workflow declaration. The four switches are gone.

**The DAG executor stays a DAG executor.** `internal/executor` still does dependency ordering, bounded parallelism, and snapshot isolation — those earn their keep on fanout and parallel critic dispatch. What's gone is the conflation of "graph topology" with "control flow." The convergence loop that wraps `Run` was always *outside* the DAG (it re-runs the whole DAG per iteration), and it stays there as a Go `for`, not a graph back-edge. Sub-graph loops, when needed, can be expressed in Go control flow rather than as cyclic dependencies.

**Migration impact:**

- `LoadWorkflow` removed; callers (`Plan`, `Analyze`, `GenerateSpec`) reference the package-level workflow values directly.
- `update --reset` no longer refreshes workflow files (there are none on disk). It still refreshes agents and `models.yaml`. `ResetReport.WorkflowsReset` is dropped.
- `locutus init` no longer creates `.borg/workflows/`. Existing project copies of the YAMLs are now dead files — harmless but stale.
- The legacy substring fallback in conditionals (`shouldRunConditional` checking `state.ProposedSpec` for the condition string) is gone. Tests that depended on it (`TestPlanWithSpecialists`, the `open_questions` substring path) have been removed; their typed equivalents (`hasOpenQuestions` against `OpenConcerns`) are what production now uses.
- Tests that injected custom workflow YAMLs now build `&Workflow{Rounds: []WorkflowStep{...}}` literals. `GenerateSpec`'s tests inject a simpler `testSpecGenWorkflow` via a new package-private `generateSpecWithWorkflow` helper so each assertion can exercise a specific behavior without the full DJ-098 outline+fanout pipeline.

**Reversal criteria:** revert if (a) we ship a genuine third-party-workflow plugin model where users author topologies without recompiling — at that point a serialization format (YAML or otherwise) earns its way back in. The serialization format is the easy part; the harder constraint (Go-side handlers per step) would also have to be addressable from the plugin surface. (b) is not anticipated.

## DJ-113: Spec Node Supersession Deletes the Old Node; History Is the Durable Record

**Status:** shipped

**Decision:** `locutus refine <id> --supersede "<motivation>"` replaces a Decision, Feature, or Strategy with a new node, cascading id rewrites and invalidating affected approaches. The old node file is deleted. The motivation is required; `--supersede` is mutually exclusive with `--brief`. Bugs are rejected with a hint at the existing status-transition path.

**Why this verb existed to be added:** discovered 2026-05-09 in winplan. A `justify --against` returned **BROKE DOWN** with three breaking points against `dec-adopt-auth-js-nextauth-with-google-oidc-and`; the verdict's "Suggested next step" pointed at `refine --brief`; the refiner ran, rewrote the downstream feature prose, and left the decision file untouched at confidence 0.9 with the original alternatives. The cascade event then asserted "All applicable Decisions remain accurately represented" in the same session where the verdict said the opposite.

The cause was structural. The refiner agent ([internal/scaffold/agents/refiner.md](../internal/scaffold/agents/refiner.md)) is constitutionally forbidden from touching Decisions — "while keeping every applicable Decision accurately represented." On a decision target, `refine --brief` is a cascade-to-features operation. There was no path in the verb set from a BROKE DOWN verdict to actually mutating the decision. The same gap applied to Features and Strategies: a feature can be wrong-shaped (framed against a stale goal, scoped wrong) in ways prose-rewriting can't fix; a strategy can shift wholesale (microservices → modular monolith) in a way that's not a textual edit.

**Why a flag on `refine` rather than a new verb:** stays inside DJ-101's 8 mutating + 2 read-only cap. `--supersede` is sibling to `--brief` / `--diff` / `--rollback`. The user mental model is "refine this node," not "invoke a different operation type." A separate `supersede` verb would have broken the cap and added a concept to the user's surface for a single specific kind of refinement.

**Why `--supersede` and `--brief` are mutually exclusive:** they describe alternative invocation modes. `--brief` rewrites downstream prose to incorporate a focused intent; `--supersede` replaces the target wholesale and cascades. Allowing both would force the runtime to pick one, which means hiding intent from the user. Picking explicitly at the flag level keeps the user in control.

**Why bugs are excluded:** the existing status lifecycle (`reported → triaged → fixing → fixed`) plus filing a fresh bug against the right root cause already covers the rename-the-mistake case for bugs. Adding supersede-cascade weight to bugs without a real workflow benefit fails the no-aspirational-fields rule. A smarter justify suggestion for bug-shaped BROKE DOWN verdicts (e.g., a status transition) is deferred.

**Why the superseded node is deleted, not flagged `status: superseded`:** a tombstoned node duplicates infrastructure that already exists. `git log .borg/spec/<kind>/<id>.md` is the durable record of prior content. `.borg/history/evt-*.json` (DJ-103) captures the supersession event with motivation and full cascade scope. After the cascade, no spec-graph node references the old id. A `status: superseded` flag on a node nobody references is graph noise that pollutes `locutus list` discovery without serving any purpose.

**Why approaches are invalidated rather than deleted:** an Approach in `done` stage carries a SpecHash that ties it to the artifacts the coding agent produced (DJ-072). That linkage is what `status` reads to detect drift, and what an `adopt` re-run uses to know "this code already matches this spec." Deleting the approach would throw away the linkage and orphan the artifact files in the repo — the next adopt would synthesize a fresh approach with no awareness that prior code already exists. Invalidation preserves the linkage as the "blast radius" input the next adopt run consumes via the new `approach-regenerator` branch.

The mechanics: `Approach.InvalidatedByEventID` (added in this DJ) points at the supersession history event. Empty when the approach is valid. `Approach.IsInvalidated()` is the helper that callers (cascade engine, adopt routing, list/explain/status renderers) gate on. Validator behaviour: an invalidated Approach is not flagged as a dangling-reference target — the file exists, just in an intentionally stale state.

**Why eager regeneration was rejected (regenerate-inside-supersede):** it would cross the spec-mutation / reconcile-loop boundary the verb set is organised around. `refine --supersede` is pure spec mutation; `adopt` is the controller that brings code into alignment with spec. Adding LLM calls into supersede would force N synthesis calls into a flow that should stay fast and synchronous. At winplan scale a heavily-cited decision could trigger 10+ regenerations per supersede; lazy regeneration via the adopt loop scales linearly with adopt's existing async cadence rather than blocking the user during supersede.

**Cascade rules.** Different reference shapes are affected depending on which kind is superseded:

| Reference shape                         | Decision superseded                                                 | Feature superseded                                | Strategy superseded         |
|-----------------------------------------|---------------------------------------------------------------------|---------------------------------------------------|-----------------------------|
| `Feature.Decisions[]`                   | rewrite old → new                                                   | unchanged                                         | unchanged                   |
| `Strategy.Decisions[]`                  | rewrite old → new                                                   | unchanged                                         | unchanged                   |
| `Decision.InfluencedBy[]`               | rewrite old → new                                                   | unchanged                                         | unchanged                   |
| `Bug.FeatureID`                         | unchanged                                                           | rewrite old → new                                 | unchanged                   |
| `Approach` with target in `Decisions[]` | invalidate + rewrite                                                | n/a                                               | n/a                         |
| `Approach` with `ParentID == old id`    | n/a                                                                 | invalidate + rewrite                              | invalidate + rewrite        |
| Downstream prose                        | refiner re-pass on Features/Strategies that referenced the decision | refiner re-pass on Bugs filed against the feature | no automated prose cascade  |

In the in-place case (same slug), only the Approach invalidations apply. No id rewrites are needed because the id didn't change, but Approaches are still invalidated because the structured fields changed materially and the SpecHash linkage is suspect.

**Why no prose cascade for strategy supersede:** Strategies are stand-alone in the typed model — nothing in Feature/Decision/Bug has an explicit `strategy_id` reference. Soft prose mentions in unrelated nodes are unverifiable; chasing them is fuzzy work the refiner can't do reliably. The user can spot-check via `locutus list` if needed.

**Why the same-slug case is in-place rather than dedupe-suffixed:** when the new title's slug matches the existing slug, the supersede is treated as a content revision (alternatives section expanded, rationale revised) rather than a replacement. No new id, no cross-node cascade rewrites. This is the cheap escape from naming-collision mechanics — we don't synthesize `dec-foo-v2` ids; same slug just means "this is a revision."

**Per-kind agent flow:** three new agents, one per replaceable kind (`refiner-supersede-decision`, `refiner-supersede-feature`, `refiner-supersede-strategy`), each with kind-specific output schemas (`RewriteDecisionResult`, `RewriteFeatureResult`, `RewriteStrategyResult`). Each agent emits the full replacement struct plus an `architect_rationale`. Per-kind constraints: the decision agent must preserve every alternative from the old decision plus add the new option that prompted supersession; the feature agent must carry forward acceptance criteria unless the motivation explicitly retires them; the strategy agent must carry forward prerequisites/skills/kind unless the motivation explicitly drops or changes them. Mirrors the council's `spec_feature_elaborator` / `spec_strategy_elaborator` separation pattern.

**Adopt's regeneration branch closes the loop:** Phase 0b in `RunAdoptWithConfig` finds Approaches with `InvalidatedByEventID` populated, reads the referenced supersede event from `.borg/history/`, and invokes the new `approach-regenerator` agent with the prior approach + the current parent + the supersede motivation. The agent emits a fresh Body that addresses both forward (what the coding agent must build to satisfy the new spec) and backward (what to do with each prior artifact: keep / modify / replace / delete). Structured fields (ArtifactPaths, Decisions, Skills, Prerequisites, Assertions) carry forward unchanged — the cascade engine already rewrote any id references the supersede affected. On success the InvalidatedByEventID marker is cleared.

Soft-degrades when the supersede event referenced by an approach is missing from disk: logs a warning and skips. Tighter coupling between approach state and on-disk event integrity would run counter to the two-way-door DX the verb set is organised around — a hand-edit or a `git rm` shouldn't fail wholesale on the rest of the adopt run.

**`justify` integration:** the suggested-next-step renderer ([internal/render/justify.go](../internal/render/justify.go)::suggestedNextStep) routes by verdict + node kind. BROKE DOWN against decision/feature/strategy → `--supersede`. BROKE DOWN against bug → `--brief` (existing path; bugs are out of supersede scope). Held / partially_held_up / any other verdict → `--brief` (existing path). The breaking-points string flows through to whichever flag is suggested. This closes the loop the original winplan bug exposed.

**Visible reconcile-pending state:** invalidated approaches show up in `list` with an `[invalidated]` badge after the kind tag, in `explain` with a `> ⚠ Invalidated by event ...` banner above the rendered section pointing the operator at `locutus adopt`, and in `status` under a new `## Pending reconcile` section distinct from `## Drifted`. Drifted means code drifted from a valid spec; invalidated means spec drifted under valid code. Both need adopt; the headline tells the user which is which.

**Why the suggested-next-step routing isn't auto-firing:** Locutus does not auto-mutate on agent verdicts. The user invokes `--supersede` explicitly. Auto-firing on BROKE DOWN would couple two LLM calls' worth of confidence-decay into one irreversible action — same reasoning DJ-101 used for the original `--brief` suggestion.

**Rejected alternatives:**

- **New top-level `supersede` verb.** Breaks DJ-101's 8-verb cap. The user mental model is "refine this node," not "invoke a different operation type."
- **`status: superseded` flag on the old node.** Duplicates the durable record git + history already carry. Pollutes `locutus list` output. No node references it after cascade, so it serves no graph-traversal purpose either.
- **Eager approach regeneration inside `refine --supersede`.** Crosses the spec-mutation / reconcile-loop boundary. Forces N synthesis LLM calls into a flow that should stay fast and synchronous.
- **Deleting approaches and orphaning artifact code.** Loses the SpecHash → code-hash linkage that `status` and `adopt` rely on, and leaves the next adopt synthesizing fresh approaches with no awareness that prior code exists.
- **Auto-fire on BROKE DOWN verdicts.** User-driven mutation only.
- **Including bugs in scope.** Existing status lifecycle plus a fresh filing for wrong-root-cause covers the rename-the-mistake case without the supersede cascade weight.

**Reversal criteria:** revert if (a) operators systematically prefer the soft-cascade path (refine prose with caveats) even on BROKE DOWN verdicts — at that point the suggested-next-step routing reverts to `--brief` and `--supersede` becomes an opt-in for power users who explicitly want id replacement. (b) approach regeneration via the adopt branch produces consistently lower-quality briefs than fresh synthesis, indicating the blast-radius input is more confusing than helpful — at that point the regenerator falls back to fresh synthesis and the InvalidatedByEventID becomes purely a marker for the operator (no automated regen).

**Reference:** depends on DJ-101 (verb-set cap; justify as active defense; suggested-next-step pattern), DJ-072 (SpecHash on Approach — the load-bearing reason for invalidating rather than deleting approaches), DJ-085 (DecisionProvenance denormalised onto Decision — the durable-record-stays-with-the-node pattern this DJ extends to supersede events), DJ-103 (history events as durable past-tense record).

## DJ-114: Authored `Summary` Field on Every Spec Node

**Status:** shipped

**Decision:** Every persisted spec node — Feature, Strategy, Decision, Bug, Approach — carries a `Summary` field: one or two sentences describing **what** the node is, distinct from the longer prose (`Description` / `Rationale` / `Body`) and from the one-line "why" already on Decision (`ArchitectRationale`). The authoring/refining agents emit it as part of their existing output schema; the persistence layer threads it from the proposal types onto disk. `BuildSpecManifest` reads `Summary` verbatim when present and falls back to derived truncation when absent. Nodes that pre-date the field (legacy projects) are filled by the `SummariesPresent` prereq via a fast-tier `spec_summarizer` agent. See DJ-115 for the prereq mechanics.

**Why this lives on the node, not in a derived index.** DJ-094 introduced `spec_list_manifest` as a tool the council uses to navigate the spec graph without inlining every node's full content. The manifest's per-entry `Summary` field was derived at tool-call time by truncating the first 200 runes of `Description` / `Rationale` / `Body`. For long-form rationale that opens with framing ("This is a foundational choice…") before stating the substance, the truncation routinely captures the lead-in instead of the conclusion — the manifest becomes a misleading index. Persisting an authored one-liner on the node fixes the signal without introducing a separate sync surface. The spec directory remains the manifest (DJ-068); `Summary` is a field on the existing files, not a new index file.

**Why this is distinct from `Decision.ArchitectRationale`.** ArchitectRationale (DJ-085) is the one-sentence **why** — "Postgres won because of mature replication tooling and operational maturity." Summary is the one-sentence **what** — "Adopt Postgres with logical replication for the OLTP store." Both exist; they answer different questions and would conflate badly if merged. The "why" is queried in a follow-up when needed (via `justify` or by reading the full Rationale); the "what" is the field a scanning agent reads to decide whether the node is relevant at all.

**Why authored, not derived.** The model that wrote the rationale is best-positioned to summarise it — it has the full context the persisted node will be summarising, and it produces the Summary in the same LLM call that produces the rest of the node. A separate post-hoc summariser (the prereq path; see DJ-115) is strictly the fallback for legacy nodes the authoring path didn't cover. The authoring path adds zero new LLM calls in the steady state; the fallback path runs at most once per legacy node per project.

**Soft-validated, not hard-rejected.** `IsWellFormedSummary` checks length ≤ 600 chars + sentence-terminal punctuation (`.`, `!`, `?`). Violations are logged but accepted. The LLM call's cost is already paid by the time the value reaches us; rejecting a cosmetically off-by-one summary forces another round-trip to fix what is almost always a stray period. Hard-required is "non-empty after whitespace trim" (`HasSummary`), enforced by the SummariesPresent prereq.

**What's new:**

- `Summary string` field on `spec.Feature`, `spec.Strategy`, `spec.Decision`, `spec.Bug`, `spec.Approach` ([internal/spec/types.go](../internal/spec/types.go), [internal/spec/bug.go](../internal/spec/bug.go), [internal/spec/approach.go](../internal/spec/approach.go)). JSON-tagged `summary,omitempty` so legacy files deserialise cleanly with the field empty.
- `spec.HasSummary(s string) bool` and `spec.IsWellFormedSummary(s string) bool` helpers ([internal/spec/summary.go](../internal/spec/summary.go)) for the hard and soft checks respectively. `SummaryMaxChars = 600`.
- `Summary` field added to the authoring agents' output schemas: `RawFeatureProposal`, `RawStrategyProposal`, `InlineDecisionProposal`, `FeatureProposal`, `StrategyProposal`, `DecisionProposal`. Optional in the schema (`,omitempty`) so models that don't yet emit it still produce conformant responses; the prereq backfills.
- Example payloads in `RegisterSchema` calls ([internal/agent/schemas.go](../internal/agent/schemas.go)) demonstrate the `Summary` field so the schema-prompt-doc renderer surfaces it to the model.
- Authoring agent prompts ([spec_architect.md](../internal/scaffold/agents/spec_architect.md), [spec_feature_elaborator.md](../internal/scaffold/agents/spec_feature_elaborator.md), [spec_strategy_elaborator.md](../internal/scaffold/agents/spec_strategy_elaborator.md)) instruct the model to emit `summary` with the soft-validation criteria.
- `ApplyReconciliation` threads `Summary` from `Raw*Proposal` through the reconcile step onto `*Proposal`, and `SpecProposal.ToAssimilationResult()` copies it onto the persisted `spec.*` ([internal/agent/reconcile.go](../internal/agent/reconcile.go), [internal/agent/specgen.go](../internal/agent/specgen.go)).
- `BuildSpecManifest` reads `Summary` verbatim when non-empty; `summaryOrFallback` collapses the prior derived-truncation path into a defensive fallback ([internal/agent/spec_tools.go](../internal/agent/spec_tools.go)).

**What stays the same:**

- `.borg/manifest.json` remains the project-root marker (DJ-081). No new manifest file.
- DJ-094's `spec_list_manifest` and `spec_get` tool contract is unchanged; the only behavioral difference is summary quality.
- ArchitectRationale (DJ-085) stays on Decision as the durable one-line "why."
- Existing `Description` / `Rationale` / `Body` fields are untouched — Summary sits beside them.
- Refine-cascade rewriters (`RewriteFeatureResult` / `RewriteDecisionResult` / `RewriteStrategyResult`) and the approach regenerator are NOT updated in this pass. The prereq path covers them — when refine rewrites a node, the new content lacks an authored Summary; the next prereq run fills it. Adding Summary to the refine output schemas is a clean follow-on that further reduces prereq dependence; it's not load-bearing for correctness today.

**Rejected alternatives:**

- **Derive `Summary` at read time, no field on the node.** The DJ-094 path. Misleading on every node whose primary prose opens with framing instead of conclusion (most decisions in practice). Rejected because the cure — manually adjusting prose to lead with the conclusion — would distort the documents the prose serves elsewhere (explain, justify, refine cascade).
- **A separate persisted index file (`.borg/spec/manifest.json`).** Considered and rejected in DJ-094; rejected again here for the same reason. The field-on-the-node approach has zero drift surface — every refine that rewrites the JSON rewrites the Summary too.
- **One unified `Summary` shared between "what" and "why."** Conflates the two concerns. The two questions ("what is this node?" and "why was this chosen?") have different scanning audiences. Decision-only nodes would get the "why" version; non-decision nodes would have only the "what." Asymmetric.
- **Required at the schema level (`minItems`-style hard reject).** Forces every authoring agent to emit it in one synchronized release. Today some agents are updated; some are not. A required field would mean every refine path fails until every agent ships with Summary. The prereq backfill makes this graceful — the prereq IS the enforcement, just lazy.

**Reversal criteria:** revert if (a) operators routinely override authored summaries because the council's one-liners are misleading often enough to be worse than derived truncation — at which point we either tighten the prompt or switch back to derived; or (b) embedding-based semantic search supplants the manifest-scan workflow (the agent fetches by semantic similarity, never reading the index), at which point the authored summary becomes documentation rather than load-bearing search signal. Neither failure mode invalidates the data model — the field on the node is the right shape; only the consumer changes.

**Reference:** depends on DJ-094 (spec-lookup tools the manifest serves), DJ-085 (DecisionProvenance / ArchitectRationale — the "why" sibling), DJ-068 (`.borg/spec/` IS the manifest; no derived index file). Companion to DJ-115 (the prereq mechanics that backfill missing summaries on legacy nodes).

## DJ-115: Prerequisite Layer Is a Category of Functions, Not an Interface

**Status:** shipped

**Decision:** Operations declare their prerequisites as direct function calls — `prereqs.EnsureSpecsContainSummaries(ctx, sctx, regen bool) error` is the first, with more to follow as the surface grows. There is no `Precondition` interface, no registry, no abstraction layer. Each prereq is a concrete assertion paired with an optional resolution path; the caller controls the resolution mode via the `regen bool` argument. `update` runs the full prereq set as part of its normal flow; individual verbs invoke the specific prereqs they depend on. The `--check-pre-reqs` flag on `update` invokes the prereq pass standalone (the dev compile-and-run loop), composing with `--offline` to skip the network/binary-fetch parts while still satisfying prereqs.

**The assertion-with-optional-resolution shape.** A prereq function takes `regen bool`:

- `regen=false` → assertion only. The function walks the on-disk shape, counts violations, returns a typed error (e.g. `*SummariesError`) listing the offending node ids. No LLM calls, no mutations. Used by `--dry-run` paths because dry-run promises not to mutate anything — including prereq-driven fills.
- `regen=true` → assertion + resolution. The function walks, and if any violations exist, dispatches the resolution workflow (for SummariesPresent: `FillSummariesWorkflow`) to bring the on-disk shape into conformance. Returns `*SummariesError` only when the workflow itself leaves nodes unfilled (e.g. provider exhausted retry budget on a transient).

The caller — a CLI verb — sets `regen = !c.DryRun`. Dry-run that hits an unfilled-summaries project fails with `N spec nodes missing Summary; rerun 'locutus update --check-pre-reqs' to fill`. This is the honest report — dry-run cannot predict the post-resolution state without mutating, and reporting truncation fallbacks would lie about what the actual run would see.

**The flag matrix on `update`.** Three flags compose:

| Combination | Prereq behavior |
| --- | --- |
| `update` | run (regen=true) along with all other update work |
| `update --reset` | run (regen=true) after the reset refresh |
| `update --offline` | skip (no LLM calls in offline mode by default) |
| `update --offline --reset` | skip prereqs; refresh local files only |
| `update --offline --check-pre-reqs` | run prereqs (regen=true); skip the binary-fetch but still satisfy prereqs |
| `update --check-pre-reqs` | equivalent to bare `update` for the prereq pass |

The rule is `shouldRunPrereqs = !c.Offline || c.CheckPreReqs`. `--check-pre-reqs` is the dev-loop primitive — compile a new binary locally, run `update --offline --check-pre-reqs`, and the on-disk shape gets verified against what the new binary expects without any network round-trip for defaults refresh.

**Why no interface.** A `Precondition` interface or registry slice would be premature abstraction for a single concrete implementation. Concrete functions read clearly at the call site, compose naturally with verb-specific dependencies (some verbs already have a dispatcher; the prereq reuses it instead of constructing its own), and don't pretend the prereq surface is uniform — `EnsureSpecsContainSummaries` needs `SummariesContext{FSys, Executor, Dispatcher}`; a hypothetical `EnsureTracesPresent` would need different inputs (assimilation pipeline, maybe a code-walker). Forcing them through a common interface would either flatten the signature into `any` or invent a context struct nobody reads. When a second prereq lands and the call-site composition pattern becomes obvious, the planned refactor is config-driven `WithXxx` builders rather than method-bag interfaces — but that's not warranted until the second concrete prereq exists.

**The `fill-summaries` workflow shape.** Resolution for `SummariesPresent` is a one-step parallel fanout workflow. The discovery phase happens upstream in the prereq layer (walks `.borg/spec/<kind>/` directories, returns `[]MissingSummaryNode{Kind, ID, Path, Content}`). The workflow's single step fans out one item per missing node, runs `spec_summarizer` (fast-tier) against the node's content, writes the produced `Summary` back to disk via `specio.SavePair` / `SaveMarkdown`. The merge handler accumulates per-id success / failure in the orchestrator goroutine so there's no shared-state contention between parallel `RunItem` slots. Concurrency is bounded by `models.yaml`'s per-model `concurrent_requests` cap — same envelope every other council workflow uses.

Per-item failures (parse failures, empty summaries, provider exhausted) land on `state.Failed[id]`. The prereq returns `*SummariesError{Failed: ...}` when the count is non-zero; the operator reruns. The retry budget within a single workflow run is 3 (the standard `executionRetryConfig`); beyond that the prereq accumulates failures rather than continuing-on-partial-success because the verb that invoked the prereq needs to know its inputs are complete before proceeding.

**Concurrency and contention.** Sequential within a single invocation — the prereq fills, the workflow returns, then the calling verb runs. No race surface between the prereq and the verb because they run in order. Cross-invocation (two parallel `locutus` runs) is bounded by file-level atomic writes in specio (tmp + rename): last writer wins, both Summary values are valid, and a refine racing a summarizer fill has its rewrite-of-the-whole-node naturally supersede a summarizer-only Summary write.

**What's new:**

- `internal/prereqs/` package ([doc.go](../internal/prereqs/doc.go), [summaries.go](../internal/prereqs/summaries.go)). Single function `EnsureSpecsContainSummaries(ctx, sctx, regen) error` plus a typed `*SummariesError`.
- `FillSummariesWorkflow` ([internal/agent/workflow_fill_summaries.go](../internal/agent/workflow_fill_summaries.go)) with `FillSummariesState`, `MissingSummaryNode`, `runSummarizeOne`, `mergeSummarizeResults`. Per-kind write-back (`writeBackJSONSummary[T]` for Feature/Strategy/Decision/Bug, `writeBackApproachSummary` for the markdown-only Approach).
- `spec_summarizer` fast-tier agent ([internal/scaffold/agents/spec_summarizer.md](../internal/scaffold/agents/spec_summarizer.md)) with `SpecSummaryResult` schema ([internal/agent/spec_summarizer.go](../internal/agent/spec_summarizer.go)).
- `UpdateCmd.CheckPreReqs` flag + the `shouldRunPrereqs` matrix ([cmd/update.go](../cmd/update.go)). `--check-pre-reqs` and `--offline` compose orthogonally.
- `cmd/prereqs.go` helper (`runSpecPrereqs(ctx, fsys, llm, regen)`) so individual verbs (`import`, `refine`, `adopt`) invoke prereqs at their LLM-acquisition point without each one repeating the dispatcher construction.

**What stays the same:**

- `.borg/manifest.json` content (DJ-081).
- The verb set's 8-verb cap (DJ-101) — no new `prereq` verb. The flag on `update` is the surgical surface; the implicit pass in every other operation is the natural one.
- DJ-094's tools (`spec_list_manifest`, `spec_get`) — the prereq fills the data they read; no change to the tool contract.

**Rejected alternatives:**

- **`Precondition` interface with a registry.** Premature abstraction for one impl; flattens distinct input signatures into a `any`-typed bag. Reconsider when the second concrete prereq lands.
- **Trigger prereq resolution from `spec_list_manifest` (a read tool that secretly writes).** The original draft idea, rejected after the contention analysis. Even though file-level atomicity made it safe, the tool semantics — a tool documented as a pure read suddenly mutating spec JSON, with N LLM calls of latency inside what looks like a single tool call — would break the trace's interpretability and the agent's mental model of cost. Resolution happens at the verb boundary, where the cost is visible to the caller.
- **Continue-on-partial-failure in `FillSummariesWorkflow`.** Leaves the calling verb running against a partially-conformant on-disk shape. The invariant "prereqs satisfied at op start" only holds if the prereq fails when it can't reach satisfaction; soft-fail breaks the invariant.
- **Sequential summarize loop instead of parallel fanout.** Linear in node count; for a 270-node legacy project that's ~270× the wall time. Per-model concurrency caps already bound the actual parallelism, so the fanout is bounded by what the provider tolerates, not by goroutine count.
- **Always-required `Summary` at the JSON-schema level (no prereq, just rejection).** Would force every authoring agent into one synchronized release. The prereq path is the graceful enforcement — the field is required at the system level, with a lazy fill, instead of required at every individual write call.

**Reversal criteria:** revert if (a) the prereq's wall-time cost on routine `import` / `refine` invocations becomes painful enough that operators want to disable it per-verb — at which point we'd add per-verb opt-out flags or move resolution to a background-only path (lazy fill on a timer). (b) A second prereq fails to fit the `Ensure...(regen)` shape — at which point the planned `WithXxx`-builder refactor accelerates. Neither failure mode invalidates the operation-prerequisite framing; only the surface area changes.

**Reference:** depends on DJ-094 (spec-lookup tools the filled summaries feed), DJ-081 (`.borg/manifest.json` as project-root marker — unchanged), DJ-068 (`.borg/spec/` IS the manifest — the prereq updates the manifest in place), DJ-112 (workflows in Go, agents in `.borg/agents/` — the `fill-summaries` workflow follows this pattern). Companion to DJ-114 (the field this prereq fills).

## DJ-116: Persistent BM25 Spec Index Backing `list` and `spec_search`

**Status:** shipped

**Decision:** The spec graph gets a persistent full-text index living at `.locutus/spec_index/`, built on top of Bluge (pure-Go, CGo-free, BM25-first). Two consumers share the index: the operator-facing `list` verb (which replaces its hand-rolled token-counting scorer) and a new agent-facing MCP tool `spec_search(query, kind?, limit?)` registered alongside `spec_list_manifest` / `spec_get`. The index is gitignored and regenerable; correctness is preserved on every invocation by a (schema-version, file-tree-hash) fingerprint check that triggers a full rebuild on mismatch. Incremental updates are wired through `specio`'s atomic write paths via a process-global `SpecWriteCallback` so the index stays current within a long-lived process (MCP) without forcing each mutating verb to manage the writer itself.

**Why Bluge, not Bleve.** Bluge is built by Bleve's principal author with hindsight from Bleve's evolution: BM25 is the default similarity (Bleve had to be coaxed there), the vendored footprint is roughly half, and the segment format is pure-Go end-to-end. No CGo preserves Locutus's static-binary property. The on-disk format's churn risk is bounded by the fingerprint's schema-version tag: any breaking change to the field mapping or analyzer set bumps `search.SchemaVersion`, the next Open sees the mismatch, and the rebuild is automatic.

**Why a persistent disk cache at `.locutus/spec_index/`.** Matches DJ-103's pattern for the narrative summary cache: gitignored, regenerable, with a single `rm -rf .locutus/spec_index/` operator escape hatch. At winplan's current ~270 nodes the in-memory rebuild on every invocation is ~50–100ms — fine. At the realistic target scale (1500–3000 nodes for a moderately-developed project) the per-invocation tax becomes meaningful and the operator-facing `list` and MCP-server-hosted `spec_search` both pay it. The fingerprint validation cost (`sha256` over `<path, mtime, size>` tuples for every spec file) is single-digit milliseconds for 3000 files — orders of magnitude below a rebuild. Mtime-preserving edits (`cp -p`, restore-from-backup) slip past the fingerprint; the `rm -rf` escape hatch is the documented recovery path rather than a more elaborate checksum-everywhere scheme.

**Why a separate read path vs. write path.** Reads — `list`, `spec_search`, future `find` — open a `bluge.Reader` directly from disk via `bluge.OpenReader`. No writer lock, concurrent reads coexist freely, and the bounded staleness (whatever the most recent writer hasn't committed yet) is acceptable for query workflows. Writes go through `OpenWriterWithRetry` with retry schedule `[0, 250ms, 500ms, 1s]` (worst-case 1.75s across 4 attempts). On retry exhaustion the diagnostic names the holder PID read from Bluge's own `bluge.pid` file inside the index directory — paste-able context the operator can act on. The MCP server uses the same writer acquisition path on its mutating verbs rather than holding the writer continuously, so CLI verbs aren't starved while a long-running MCP session is up.

**Why ship the agent-facing tool with the foundation.** DJ-094 introduced `spec_list_manifest` and `spec_get` and accepted a propagation lag — the tools landed but the prompts that documented them took follow-on PRs to spread. The lag turned out to be expensive: the `Summary` field needed in DJ-114 fell out of *exactly that* gap, where the manifest's truncation-based summaries were being read by agents whose prompts hadn't caught up to the tool's real shape. For `spec_search` the foundation cost and the agent-benefit cost are paid once, on the same plumbing, so the tool registers and the prompts update in the same shipment. The wording in each of the 19 council prompts tells the agent when to reach for `spec_search` (topic-scoped) vs. `spec_list_manifest` (full-graph enumeration) — both tools coexist deliberately. The plan considered removing the manifest in favour of search-only; rejected because enumeration is a legitimate need for small projects and for agents that genuinely want the structural overview.

**Why a single shared index, not per-kind.** The unified shape simplifies cross-kind queries (which dominate operator use) at a negligible per-kind-speedup cost at the target scale. The `kind` field is indexed as a keyword filter (never free-text searched) so a kind-scoped query becomes a Bluge `BooleanQuery` with one Must term — cheap, fast, and the agent surface naturally exposes it as the `kind` parameter.

**Why a mutation callback in `specio`, not in each verb.** Every spec write goes through `specio.SavePair` / `SaveMarkdown` / the new `RemovePair`. Centralising the index hook at that chokepoint means a verb can't accidentally bypass indexing by routing through a different write path. The callback is process-global, nil-safe (no-op when unset — the right state for `locutus init` and for tests that don't go through `cmd`), and set once at `CLI.AfterApply`. The production callback opens the writer via `OpenWriterWithRetry`, calls `writer.Update` with a freshly-built document (or `writer.Delete` on remove), and logs-and-swallows any error so spec writes never fail because the index is unavailable. Failure converges on the next `Open`'s fingerprint check — eventual consistency, not best-effort silence.

**Why the field weights mirror the heuristic scorer.** Title=3, Summary=2, ID-tokens=2, Body=1 — the same boost profile the pre-FTS scorer used, applied at query-construction time via per-field `MatchQuery.SetBoost`. BM25's length normalisation and IDF handle the rest. Picking the same weights means the BM25 ranking starts in the same neighbourhood operators are used to before improving on it — an upgrade, not a tuning-from-scratch event. The slug-body tokens (`dec-postgres-with-pgvector` → `postgres with pgvector`) get indexed via the English analyzer too, so `spec_search('postgres')` finds the node even when the title doesn't say "Postgres" verbatim.

**Plan deviations worth recording.** Two corrections to the original plan (`.claude/plans/spec-search-index.md`):

- **Bluge v0.2.2 doesn't ship a `NewQueryStringQuery`** the plan assumed. Phase 1 ships with three hand-detected forms (free text, `"phrase"`, trailing-`*` prefix) plus an explicit `Options.Kind` filter. The richer syntax the plan listed (`kind:foo AND bar`, `+postgres -mysql`, `auth~2` fuzzy with edit distance) is deferred until a real consumer needs it; the call will be between pulling `github.com/blugelabs/query_string` as a second dep or hand-rolling a small parser when that day comes.
- **Phrase queries require fields indexed with `SearchTermPositions`.** Enabled on `title` / `summary` / `body`; both the bulk-build path and the per-node `BuildDocument` path used by the mutation callback share `newDoc` / `addTextField` helpers so the flag can't drift between them.

**What's new:**

- `internal/search/` package ([search.go](../internal/search/search.go), [index.go](../internal/search/index.go), [build.go](../internal/search/build.go), [fingerprint.go](../internal/search/fingerprint.go), [writer.go](../internal/search/writer.go)). Public API: `Open(fsys, projectRoot)`, `OpenInMemory(fsys)`, `(*Index).Search(query, opts)`, `BuildDocument(fsys, kind, id)`, `OpenWriterWithRetry(indexPath)`. `SchemaVersion` is a manually-bumped integer constant; fingerprint format is `"<schema>:<sha256-hex>"`.
- `internal/specio.SpecWriteCallback` and `SetSpecWriteCallback` ([callback.go](../internal/specio/callback.go)), wired into `SavePair` / `SaveMarkdown` and a new `RemovePair` helper. The callback fires after each successful write and carries `(kind, id, deleted)` — kind is the singular canonical form (`"decision"`, etc.) per the cleanup that followed Phase 2.
- `cmd/searchhook.go` — `registerSearchHook()` called from `CLI.AfterApply`. Project-root-gated (no-op when not inside a project), uses `OpenWriterWithRetry` + `BuildDocument` per callback fire, logs-and-swallows.
- `cmd/list.go` rewritten on top of `search.Open` / `search.Search`. The hand-rolled scorers (`scoreDecision`, `scoreFeature`, `scoreStrategy`, `scoreApproach`, `scoreBug`, `scoreField`, `scanLoaded`, `tokenizeQuery`, `weight*` constants) deleted. `ListHit.Score` widened from `int` to `float64` to match Bluge's BM25 scores — order is the API, magnitude is opaque.
- `spec_search` MCP tool registration ([internal/agent/spec_tools.go](../internal/agent/spec_tools.go)). Tool input: `SpecSearchInput{Query, Kind?, Limit?}`. Tool output: `SpecSearchResult{Hits []SpecManifestEntry, TotalMatches int}`. Agent-surface limit defaults to 20 (smaller than the CLI's 100 because agent contexts prefer compact result sets) and clamps to 100. `RegisterSpecTools` signature gains a `projectRoot string` parameter; empty `projectRoot` skips the `spec_search` registration so MemFS-backed tests keep working (Bluge is OS-bound).
- 19 council prompts under `internal/scaffold/agents/` document `spec_search` with role-tailored examples (verification critics, authoring agents, mutation-time agents, scout + the three `refiner-supersede-*` agents). `spec_list_manifest` and `spec_get` remain documented in every one — both tools coexist.

**What stays the same:**

- `.borg/spec/` IS the manifest (DJ-068). The index is a derived cache; the spec directory remains authoritative. No new manifest file.
- `.borg/manifest.json` content (DJ-081).
- The 8-verb / 2-deliberation-aid surface (DJ-101). No new verb. `spec_search` is an MCP tool, not a CLI verb — `list` is the CLI consumer.
- DJ-094's `spec_list_manifest` and `spec_get` tool contract. Both tools register through the same `RegisterSpecTools` block.
- DJ-114's authored `Summary` field. The index leverages summary text; the field's authority remains on the node.
- The `.locutus/` gitignored-cache convention from DJ-103.

**Rejected alternatives:**

- **Bleve instead of Bluge.** Heavier vendored footprint, CGo for some segment types, default similarity that has to be reconfigured to BM25. Bluge is the same author's successor with these specific complaints addressed.
- **Embedded `sqlite-fts5`.** CGo dependency, breaks the static-binary property. The Bluge-vs-sqlite tradeoff is mostly about query expressiveness; for our shape (BM25 over a ~3000-node corpus) Bluge is sufficient and lighter.
- **In-memory only, rebuild on every invocation.** Fine at 270 nodes, painful at 3000. Pays the rebuild cost on every `list` and every cold MCP server start; a long-running MCP host pays it once on boot and then nothing, but cold CLI invocations are the dominant interactive case and they remain expensive without the cache.
- **A separately-persisted manifest index file (e.g. `.borg/spec/index.json`).** Considered and rejected for the same reason DJ-094 rejected it — the spec directory is the manifest; introducing a parallel index file adds a sync surface to maintain. A gitignored cache outside `.borg/` is a different shape: it's *not* the manifest, it's a regenerable derivative, and the fingerprint check makes the cache self-healing.
- **Hold a `bluge.Writer` continuously in the MCP server.** Starves CLI mutations: any CLI verb that needs to update the index would contend with the MCP writer for the entire MCP session. The single-acquisition-per-mutation model keeps CLI and MCP on the same lock-hold profile.
- **Deprecate `spec_list_manifest` in favour of search-only.** Enumeration is a legitimate need for small projects and for agents that genuinely want the structural overview. Removing the manifest would force agents into search-shaped workflows even when scanning the full set is the right move. Both tools coexist; the prompts make the choice obvious.
- **An fsnotify watcher in the MCP server to refresh the on-disk reader on cross-process writes.** Deferred. The single-writer-per-mutation model means each MCP-issued mutation produces a fresh index update; cross-process changes (CLI writing while MCP is up) only become a real workflow once both surfaces are in routine concurrent use. Adds polling/inotify state for a problem we don't have yet.
- **Fuzzy "did you mean" on empty-result queries.** Bluge supports it (`SetFuzziness`); the user-facing affordance is a nice-to-have, not load-bearing. Deferred until operator feedback says misspellings are a real frustration.
- **Full Bluge query-string parser via `github.com/blugelabs/query_string` as a second dep.** Adds a transitive dep and a chunk of generated parser code for syntax (`kind:foo AND bar`, `+x -y`, `auth~2`) that has no in-tree caller yet. When `list` or `spec_search` actually needs it, the call between adding the dep or hand-rolling a tighter parser will be informed by what's actually being typed.

**Reversal criteria:** revert if (a) Bluge's index format or query API stability becomes a treadmill — version bumps consistently require code changes to mapping or query construction beyond what the schema-version-triggered rebuild absorbs. Mitigation in hand: the heuristic scorer plus DJ-114's authored summaries was the pre-FTS baseline and remains a clean fallback. (b) Operators routinely edit `.borg/spec/` by hand and don't understand why `list` shows stale results despite the `rm -rf .locutus/spec_index/` escape hatch — at which point the diagnostic gets sharpened or the cache moves in-process-only with the per-invocation rebuild tax accepted. (c) The 1.75s cross-process retry budget is insufficient in practice, or the PID-bearing diagnostic isn't actionable — at which point retry budget grows or MCP and CLI mutation coordination moves to a control channel rather than file-level flock contention. None of (a)–(c) invalidates the shape; only the engine, the cache location, or the lock arbitration changes.

**Reference:** depends on DJ-094 (spec-lookup tools — the LLM-facing precursor whose propagation gap motivated shipping `spec_search` alongside the foundation), DJ-103 (`.locutus/` cache pattern this index extends), DJ-114 (authored `Summary` — the curated text the index leverages and the field weights honour), DJ-068 (`.borg/spec/` IS the manifest — the on-disk index is derived from, not parallel to, the spec directory). Companion to DJ-115 (the prereq layer the on-disk index may eventually plug into for index-staleness detection if fingerprint mismatches become a routine signal worth a typed prereq).

## DJ-117: Explainable Search Ranking via Per-Field Scoring Scans

**Status:** shipped

**Decision:** Hits returned by `list` and `spec_search` carry per-field match diagnostics — for each field that contributed to the score (title, summary, id_tokens, body, description, rationale, alternative, acceptance, provenance, bug_detail), the matched query terms, the term-frequency Count, and the BM25 Contribution to the total Score. Sum of contributions equals Score within float rounding. The signal lets an LLM (or operator) apply context-sensitive judgment to a ranked list — discount a hit whose score is dominated by a rejected-alternative's prose when the question is about the chosen direction, weight a hit whose match is in the title higher than the same numeric score from a body-only match, etc. The diagnostic is opt-in via `Options.Explain`; cost is one targeted MatchQuery per scored field per Search call.

Two structural changes compose to make the diagnostic meaningful:

1. The body bucket (one mega-field in DJ-116) is split into seven purpose-specific fields: `body` (markdown body), `description` (feature / bug), `rationale` (decision), `alternative` (decision.Alternatives.*), `acceptance` (feature criteria), `provenance` (architect_rationale + citations), `bug_detail` (root_cause / fix_plan / reproduction_steps). All share `boostBody=1.0`; the split is for explainability, not ranking. `SchemaVersion` bumps from 1 to 2; on-disk indexes rebuild on next `Open` via the fingerprint check.

2. The kind-filter clause now has `boost=0`. It's a filter, not a ranking signal. Without this, kind-IDF leaked into the score — rare-kind docs (a single bug in a corpus of decisions) outranked common-kind docs by an accident of corpus shape. Documented as "what stays the same" because the ranking improvement is incidental; the primary motivation is keeping the kind clause out of the per-field Matches map.

**Why per-field scoring scans, not parsing Bluge's Explanation tree.** The original design captured during the brainstorm in chat called for parsing the `Explanation` tree's leaf messages with a regex on the assumed Lucene-style format (`weight(field:term in doc):`). That format **does not exist in Bluge.** Bluge's BM25 scorer in [search/similarity/bm25.go:135](file:///Users/chetan/go/pkg/mod/github.com/blugelabs/bluge%40v0.2.2/search/similarity/bm25.go) emits `"score(freq=N), computed as boost * idf * tf from:"` — no field name anywhere. The fallback option discussed (positional correlation of disjunction children to should-clause order) also fails: Bluge's `DisjunctionSliceSearcher` at [search/searcher/search_disjunction_slice.go:49](file:///Users/chetan/go/pkg/mod/github.com/blugelabs/bluge%40v0.2.2/search/searcher/search_disjunction_slice.go) calls `sort.Sort(sort.Reverse(searchers))` before iterating, so the children come back in selectivity order, not query-construction order. Both options collapsed on contact with the source.

The replacement: when `Options.Explain` is set, run one targeted single-field `MatchQuery` (or `MatchPhraseQuery` / `PrefixQuery`, mirroring the inner query type) per scored field via `bluge.NewAllMatches`. Collect `(docID → score)` per field into a `perFieldContributions` map. At hit-construction time, look each hit's ID up across the per-field maps; the score recorded for `(field, hit.ID)` IS the field's contribution to the disjunction's total — Bluge's `CompositeSumScorer` is a literal sum across constituents (see [search/similarity/composite.go](file:///Users/chetan/go/pkg/mod/github.com/blugelabs/bluge%40v0.2.2/search/similarity/composite.go)). No parsing, no fragile ordering assumption, just one independent invariant from Bluge's scoring semantics.

**The invariant lockdown.** Replacing the regex-format test with a test that pins the load-bearing property: a single-field `MatchQuery` and the same query as one clause of a multi-field disjunction must produce identical per-doc Score values. `TestBluge_PerFieldScoreEqualsDisjunctionContribution` asserts this with a fresh fixture. If a future Bluge release introduces cross-clause normalization in `CompositeSumScorer`, the test fails red and the per-field-scan approach has to be re-evaluated (the mitigation options are: a) reimplement BM25 outside Bluge, b) accept opaque per-disjunction scores and surface only Locations / Counts, neither great). The narrower-but-correct invariant is a stronger guarantee than the original regex shape would have been.

**Why opt-in, not always on.** The per-field scan path costs ~num_scored_fields extra Bluge searches per `Search` call (10 today). At our corpus size that's well under the main disjunction's cost — fast enough that `list` and `spec_search` both enable it unconditionally. But the cost is bounded by the consumer's actual need: tests that don't care about diagnostics leave `Explain` off and pay nothing. The cost-flag pattern matches how `bluge.IncludeLocations` and `bluge.ExplainScores` are opt-in upstream — the per-call decision stays with the caller.

**Why not update agent prompts in this pass.** Phase 5 (DJ-094, DJ-116 reference) updated 19 council prompts to recommend `spec_search` for topic-scoped lookups. The new `matches` map on `SpecSearchHit` is additive: existing prompts still work; the LLM encountering the new field reasons about it from the tool description, which is sent on every API call as part of the tool list. Updating per-agent prompts to repeat the interpretation pattern would be redundant against the per-call description, increase total prompt bytes, and create a 20-file drift surface for every future tweak to the response shape. Adoption-shaped follow-up was deferred to a "Phase 7b" pass *if* usage data showed mid-tier agents underweighting the diagnostic.

**Follow-up shipped same-day:** the per-call tool description is the only documentation surface and was extended (commit `c455d70`) to carry per-field semantics inline — what `title` / `summary` / `rationale` / `alternative` / `provenance` / `description` / `acceptance` / `bug_detail` / `body` / `id_tokens` each mean for relevance judgment, plus the interpretation hint "a hit whose contribution_pct is >60% in alternative or provenance is usually incidental." `FieldMatch` also gained a `ContributionPct float64` field — the same signal as `Contribution` expressed as a fraction of `Score` in [0, 1], easier for mid-tier model consumers to threshold against than absolute BM25 numbers (which vary with corpus size and query shape). Threaded through `internal/search.FieldMatch`, `cmd/list.ListFieldMatch` (with `contribution_pct` JSON tag), and `internal/agent.SpecFieldMatch`.

**Empirical validation (2026-05-12).** Built a build-tagged eval (`go test -tags=eval ./internal/agent/ -run SpecSearchInterpretation`) that runs a fixture corpus mimicking the winplan auth pattern — one clean-signal authentication decision, two `author`-stem polysemy traps, one alternative-only pgbouncer-style hit, one body-only incidental — through the production `spec_search` path, hands the JSON output to nine models across three providers and three tiers (strong / balanced / fast), and grades each model on whether it correctly classifies the five fixture hits as relevant or incidental. Result: **9/9 perfect score**.

| Provider | Tier | Model | Score | Latency |
|---|---|---|---|---|
| anthropic | strong | claude-opus-4-7 | 5/5 | 3.9s |
| anthropic | balanced | claude-sonnet-4-6 | 5/5 | 3.5s |
| anthropic | fast | claude-haiku-4-5 | 5/5 | 2.0s |
| googleai | strong | gemini-3.1-pro-preview | 5/5 | 18.6s |
| googleai | balanced | gemini-3-flash-preview | 5/5 | 3.7s |
| googleai | fast | gemini-3.1-flash-lite-preview | 5/5 | **1.4s** |
| openai | strong | gpt-5 | 5/5 | 5.2s |
| openai | balanced | gpt-5 | 5/5 | 4.6s |
| openai | fast | gpt-5-mini | 5/5 | 7.8s |

Every model's reasoning explicitly cited at least one of: (a) the `author` vs `authent` stem distinction, (b) the contribution_pct or per-field contribution magnitudes, (c) the field semantics from the tool description — direct evidence that the description is being read and applied rather than ignored. Three independent providers × three tiers × a corpus designed to trap models that don't read the diagnostic = the strongest portability evidence available short of production traffic. The "Phase 7b" prompt-update follow-up is no longer warranted by the data; the tool description alone carries the load.

**What's new:**

- `SchemaVersion` bumped to 2 (then to 3 in [1f93da0](https://github.com/) when `id_tokens` gained `SearchTermPositions` so prefix matches surface `Terms` / `Count`, not just `Contribution`).
- Seven new field constants (`fieldDescription`, `fieldRationale`, `fieldAlternative`, `fieldAcceptance`, `fieldProvenance`, `fieldBugDetail`, plus the existing `fieldBody`) and a `proseFields` slice that drives the query disjunction ([internal/search/build.go](../internal/search/build.go)). Per-kind document builders updated to route content to the appropriate purpose-specific field.
- `Options.Explain bool` and `FieldMatch{Terms, Count, Contribution, ContributionPct}` types; `Hit.Matches map[string]FieldMatch` ([internal/search/search.go](../internal/search/search.go)). `ContributionPct` is the field's share of `Score` as a fraction — added in the same-day follow-up so mid-tier consumers reason against percentages instead of absolute BM25 numbers (sum across fields ≈ 1.0).
- `scanPerFieldContributions` and `singleFieldScoreQuery` ([internal/search/index.go](../internal/search/index.go)) implement the per-field scan; `buildMatchDiagnostics` composes them with the Bluge-supplied Locations data into the public `Matches` map and computes the per-field percentage.
- Kind-filter clause: `bluge.NewTermQuery(kind).SetField(fieldKind).SetBoost(0)` in `buildQuery`.
- `cmd/list.go`: `RunList` sets `Explain: true`; `ListHit.Matches` (JSON-tagged with `contribution_pct`) carries the diagnostic; `renderListMarkdown` renders `[matched: alternative ×7, rationale ×1]` inline, sorted by contribution descending.
- `internal/agent/spec_tools.go`: `SpecSearchResult.Hits` type widened from `[]SpecManifestEntry` to `[]SpecSearchHit` (id, title, kind, summary, score, matches). `SpecFieldMatch` carries `contribution_pct`. Schema-additive — agents that parsed the old shape see every existing field unchanged plus the new ones.
- `SpecSearchToolDescription` exported and rewritten (commit `c455d70`) to document per-field semantics inline (what `alternative` / `provenance` / etc. mean for relevance) plus the threshold hint "concentration >60% in `alternative` or `provenance` is usually incidental." The description is the single source of truth for response interpretation; it ships on every API call as part of the tool list, so no per-prompt fan-out is needed.
- `TestBluge_PerFieldScoreEqualsDisjunctionContribution` plus the four Phase 7 tests (`TestSearch_MatchesNilWhenExplainOff`, `TestSearch_MatchesContributionSum`, `TestSearch_MatchesPerFieldSeparation`, `TestSearch_KindFilterNotInMatches`) and the regression test `TestSearch_IDTokensCarriesLocations` that guards the SchemaVersion 3 bump.
- `internal/agent/spec_search_eval_test.go` (build-tagged `//go:build eval`): runs the (provider × tier) interpretation eval against Anthropic / OpenAI / Google AI; opt-in via `go test -tags=eval`, gated on per-provider API keys, loads `.env` via godotenv. First-run result: 9/9 perfect — see Empirical validation section above.

**What stays the same:**

- `.borg/spec/` IS the manifest (DJ-068). No new manifest file.
- DJ-116's engine choice (Bluge), persistence model (`.locutus/spec_index/`), fingerprint invalidation, read-vs-write concurrency split, and `OpenWriterWithRetry` all unchanged.
- DJ-094's `spec_list_manifest` and `spec_get` tool contracts. Both tools register through the same `RegisterSpecTools` block.
- BM25 ranking: title=3, summary=2, id_tokens=2, prose=1.0 boost. The body-field split doesn't change boost weights; the kind-filter zeroing arguably *improves* ranking by removing accidental kind-IDF signal, but kind was already declared a filter in intent (DJ-116) so this is fixing-an-oversight rather than redesigning.
- DJ-114's authored Summary field. The index continues to leverage Summary at boost=2; the new `summary` Matches entry exposes how often it contributed.

**Rejected alternatives:**

- **Regex on Bluge's `Explanation` message strings.** Premised on Lucene's `weight(field:term in doc)` format. Bluge's BM25 scorer emits `score(freq=N), computed as boost * idf * tf` with no field name. Even with a stable upgrade pin and a snapshot test, the parser would have nothing to parse.
- **Positional correlation of disjunction children to the order shoulds were added.** Bluge's `DisjunctionSliceSearcher` sorts its searchers by selectivity (reverse order). Original add-order is destroyed before children are produced. A format-lockdown test on this would have been more brittle than the regex one — silently produces wrong field labels rather than failing loudly.
- **Reimplement BM25 outside Bluge to compute contributions independently.** Possible but brittle; would have to track every internal scoring decision Bluge makes (k1, b, length normalization, dictionary stats). Disagreements between our reimplementation and Bluge's actual scoring become indistinguishable from bugs. Reserved as the fallback if `TestBluge_PerFieldScoreEqualsDisjunctionContribution` ever fails.
- **One targeted query per top-N hit per field.** `O(top_N × num_fields)` extra queries — for top-100 with 10 fields, 1000 queries per `Search` call. The chosen approach is `O(num_fields)` total — one per-field scan covers the whole corpus, then per-hit lookups are O(1).
- **Always compute Matches (no opt-in flag).** Costs add up across tests and any future consumer that doesn't surface diagnostics. Opt-in keeps the floor at zero for callers that don't care.
- **Surface `kind` as a Matches entry when a Kind filter is set.** Constant noise — every hit would carry the same `kind` entry — and confuses the consumer's mental model of what "matched" means.
- **Skip the body-field split and rely on Locations alone for diagnostic granularity.** Counts and matched terms would still surface, but Contribution would lump all body-class signal under one `body` entry. The discriminating signal — "this match is dominated by rejected alternatives, not the main rationale" — is exactly what the operator-facing pgbouncer-vs-auth example needed. The split is what makes Contribution per-field meaningful at all.
- **Update agent prompts in this pass.** Out of scope. The agent-facing schema is additive; the new field is self-descriptive. Adoption follow-up if usage data shows agents ignoring the diagnostic.

**Reversal criteria:** revert if (a) `TestBluge_PerFieldScoreEqualsDisjunctionContribution` starts failing on a Bluge upgrade — at which point the design choice is between reimplementing BM25 contributions outside Bluge (heavy) or removing the Contribution field from `FieldMatch` and surfacing only Locations / Counts (lighter, loses the dominant-field-disambiguation use case). (b) The per-field scan cost grows unworkable at corpus scale — mitigations in hand: skip per-field scans for `Explain=false` callers (already done), or cache per-call scan results across hits (already done — the `perFieldContributions` map *is* the cache). (c) ~~Agents systematically ignore the `matches` affordance~~ — empirically excluded for the current frontier-model lineup by the 9/9 eval result above; re-evaluate only if production traces show agents underweighting the diagnostic despite citing it correctly in the eval, or if a new fast-tier model joins the lineup and a re-run drops below acceptable scores. The plumbing escape hatch (move per-field scans behind a separate MCP-surface flag) remains available if it ever does become inert overhead.

**Reference:** depends on DJ-116 (the FTS foundation this builds on — engine choice, field set, fingerprint invalidation, concurrency model), DJ-094 (`spec_list_manifest` / `spec_get` the agent-facing precursor whose ranked counterpart `spec_search` now serves with diagnostics), DJ-114 (authored Summary — the curated text whose match contribution this surface exposes per field). The build-tagged eval at [internal/agent/spec_search_eval_test.go](../internal/agent/spec_search_eval_test.go) is the durable artifact backing the empirical-validation claim above; re-running it on any future change to the tool description or `FieldMatch` shape is the cheapest check that mid-tier portability hasn't regressed.

## DJ-118: JSON Schema Generation Uses invopop/jsonschema, Not google/jsonschema-go

**Status:** shipped (supersedes the library-choice claim in DJ-054; the rest of DJ-054 — struct tags + registry pattern — remains valid)

**Decision:** All output-schema reflection in [internal/agent/schema.go](../internal/agent/schema.go) and [internal/agent/schemas.go](../internal/agent/schemas.go) uses `github.com/invopop/jsonschema` v0.13.0. DJ-054 named `github.com/google/jsonschema-go` as "the pattern" but the implementation chose invopop without amending the DJ; this entry ratifies actual practice and explains the technical reason invopop wins for this codebase's use case.

**Why invopop, not google.** The two libraries treat the `jsonschema:"..."` struct tag fundamentally differently. invopop parses key=value pairs — `enum=a,enum=b,enum=c`, `description=...`, `minItems=1`, `format=email` — mapping each pair to a JSON Schema keyword. google/jsonschema-go treats the entire tag value as a single description string and **explicitly rejects tags starting with `WORD=`** ([infer.go:80,334](https://github.com/google/jsonschema-go/blob/main/jsonschema/infer.go) — "For future compatibility, descriptions must not start with 'WORD='"). The result: with google's library, expressing an `enum` or `minItems` constraint requires post-reflection imperative Go code; with invopop it lives on the struct field directly.

Locutus's pattern is to express constraints declaratively at the struct definition site — the canonical authoring location for the shape every adapter then projects into Anthropic's `tool input_schema`, Gemini's `responseSchema`, and OpenAI's `json_schema` strict mode. Each provider's strict-mode validator server-side-enforces those constraints; the model's structured-output decoder is grammar-constrained to the schema. A field tagged `enum=held_up,enum=partially_held_up,enum=broke_down` cannot emit non-enum tokens. A field tagged with only a prose description "must be one of held_up, partially_held_up, broke_down" depends on the model's instruction-following, which we have empirical evidence (see commit `abb9119`) is unreliable on mid-tier models like claude-sonnet-4-6 — the `Verdict` field emitted `"partially_held_up ✗\n\nActually: partially_held_up"` and similar self-correction prose until the `enum=` tag landed.

**Why this isn't an indictment of google/jsonschema-go.** The library is the canonical schema dependency for the MCP go-sdk and serves that ecosystem well: MCP tool schemas are usually composed imperatively (tool servers declaring their tools in code), so a rich tag layer is less useful there. Tag-as-description is a clean minimal API for that use case. The library's design isn't asserting that simple descriptions outperform formal constraints for LLMs — provider docs (OpenAI Structured Outputs, Anthropic tool use, Gemini responseSchema) all recommend the opposite. google/jsonschema-go is simply leaving constraint composition to the caller while invopop bakes it into the tag vocabulary, and Locutus's use case is the latter.

**Forward-compat note.** This decision is revisitable. If `google/jsonschema-go` grows a rich tag vocabulary in a future minor — or if MCP becomes the dominant LLM-tooling protocol such that schema interop with the MCP go-sdk is load-bearing — we can swap. The migration would touch every `jsonschema:"key=value"` tag we've written and add post-reflection helpers for enum / minItems / etc. that the new library wouldn't parse from tags. Not painful at our current scale (≈20 enriched structs) but not free.

**What's new:** Nothing — this DJ ratifies code that has been in place since the schema infrastructure shipped. The `jsonschema:"enum=...,description=...,minItems=..."` tags landed on [SynthesisVerdict.Verdict](../internal/agent/justify_synthesizer.go), [AdversarialDefense.Verdict](../internal/agent/justify_schemas.go), [AdversarialConcern](../internal/agent/justify_schemas.go), [ChallengeSplit](../internal/agent/justify_splitter.go), and others in recent commits (`c455d70`, `abb9119`, `b547f29`). [docs/agent-conventions.md](agent-conventions.md) now documents the invopop tag syntax under "Patterns to prefer: Push constraints into the schema, not the prompt."

**What stays the same:**

- DJ-054's other claims: per-agent `output_schema:` frontmatter, package-level `RegisterSchema` registry, JSON-schema-doc appended to system prompts. All correct and load-bearing.
- The reflection pipeline in [internal/agent/schema.go:81](../internal/agent/schema.go) — `jsonschema.Reflector{...}.Reflect(example)` then `stripJSONSchemaArtifacts` + `enforceStrict`. Each adapter takes the resulting `map[string]any` and projects it into its provider-native form.
- `go.mod` carries both: `invopop/jsonschema` as a direct dep, `google/jsonschema-go` as an indirect dep via the MCP go-sdk. They serve different surfaces — Locutus's own schemas (invopop) vs the MCP-protocol surface the SDK manages internally (google).

**Rejected alternatives:**

- **Migrate to `google/jsonschema-go` to match DJ-054 verbatim.** Considered; rejected for the technical reason above. The DJ-054 library claim was a plan that diverged from the implementation; this DJ documents the implementation's actual choice rather than churning to match the plan.
- **Maintain hand-authored JSON Schema maps instead of struct reflection.** Higher drift risk (Go types and schemas drift independently), more boilerplate, no clear benefit. The reflection-plus-tags pattern works.
- **Keep using invopop without a corrective DJ.** Leaves DJ-054's library claim contradicted by the code, which violates the "DJs win when in conflict with the repo" rule from CLAUDE.md. The contradiction is fixable cheaply with this DJ; leaving it would be load-bearing drift in the design record.

**Reversal criteria:** revert if (a) `google/jsonschema-go` ships a rich struct-tag vocabulary at API parity with invopop and the MCP-SDK interop story becomes load-bearing for our schemas, OR (b) invopop's tag parser develops a stability or maintenance issue (it's been v0.x for years and that's normal in Go ecosystem; only a real regression would trigger this), OR (c) a future Locutus surface — MCP server tools, declarative schema authoring outside Go code — needs the constraint composition to happen elsewhere, in which case the imperative post-reflection model google's library encourages might fit better.

**Reference:** supersedes DJ-054 on the library-choice claim only. The DJ-054 entry above this one has been amended with a "Library claim superseded by DJ-118" note pointing here so a reader landing on DJ-054 sees the divergence without scrolling.

## DJ-119: Agent Client Protocol Replaces the Coding-Agent Driver Layer

**Status:** proposed

**Decision:** Replace [internal/dispatch/drivers/](../internal/dispatch/drivers/), the per-CLI NDJSON parsers in [internal/dispatch/streaming.go](../internal/dispatch/streaming.go), and the permission bridge in [internal/dispatch/bridge.go](../internal/dispatch/bridge.go) with a single Agent Client Protocol (ACP) client implementation under `internal/dispatch/acp/`. The supervisor, monitor, judge, registry, worktree, validators, traces.json, and OTel trace.jsonl pipeline above the driver layer stay unchanged in shape — they are fed by ACP `session/update` notifications and JSON-RPC responses instead of provider-specific NDJSON.

Each coding agent we drive becomes an ACP server addressable by command-line invocation, registered as `{name, command, args}` rather than as a Go-side `StreamingDriver` implementation:

- `claude-code` → spawn `claude-agent-acp` (bridge maintained under the `agentclientprotocol/` org)
- `codex` → spawn `codex-acp` (bridge maintained by `zed-industries/`)
- `gemini` → spawn `gemini --acp` (native support; the Gemini CLI accepts `--acp` and routes its policy engine through ACP)

Any other agent listed in the [ACP registry](https://agentclientprotocol.com/get-started/registry) — Cursor CLI, GitHub Copilot CLI, Goose, Cline, Auggie, Junie, Qwen Code, Kimi CLI, Mistral Vibe, GLM, OpenCode, and the long tail — becomes reachable without writing a new driver. This is the proximate motivation: Locutus's stated value is agent-portable spec-driven planning (DJ-010, DJ-012), and the driver-per-CLI model caps that portability at whatever subset we manually implement.

**Why now.** The "wait until adoption matures" hedge no longer holds. As of 2026-05-13: `coder/acp-go-sdk` v0.13.0 is the canonical Go SDK (163 stars, Apache-2.0, actively maintained); Gemini CLI ships native `--acp` (flag listed in [packages/cli/src/config/config.ts](https://github.com/google-gemini/gemini-cli)); `zed-industries/codex-acp` (741 stars) is the registry-canonical Codex bridge with full feature coverage (permissions, slash commands, multiple auth methods); `agentclientprotocol/claude-agent-acp` is the org-canonical Claude Code bridge (preferred over the more-starred community `Xuanwo/acp-claude-code` for the same reason we prefer DJ-114's `invopop` over a community fork — provenance over star count). Established orchestrator projects already ride this surface (`agentic.nvim` 457★, `obsidian-agent-client` 2016★, `agentrove`, `agentpool`), confirming the pattern is sound at our scale.

**What stays.** The supervision design from DJ-010 is intact. ACP affects only the wire layer between supervisor and coding agent — the layer that produces `AgentEvent`s today. Specifically preserved:

- `Supervisor` retry/validate/judge loop in [supervisor.go](../internal/dispatch/supervisor.go).
- Sliding-window churn monitor in [monitor.go](../internal/dispatch/monitor.go); the LLM cycle-detection prompt sees the same event sequence (one-to-one mapped from ACP `session/update`s to our `AgentEvent` shape).
- Agent registry, worktree management, and per-step file-touch source-of-truth via `git diff --name-only`.
- `traces.json` per-step records (DJ-091 layout unchanged).
- OTel `trace.jsonl` per session ([otel.go:99](../internal/agent/otel.go#L99)) — and *strictly upgraded* by ACP's W3C trace context propagation (see "Strict upgrades" below).
- The supervision validators, the planner verbs, and every command surface above the dispatch layer. None of them know they're talking to ACP.

**What's replaced.** Subtractive scope. The implementation deletes:

- `internal/dispatch/drivers/driver.go`, `claude_stream.go`, and the per-CLI command-construction / NDJSON-parse code (~600 LOC + tests).
- `internal/dispatch/bridge.go` — the unix-socket permission bridge. ACP's `session/request_permission` is a client-implemented method; we host it directly on the ACP `Client` rather than running `mcp-perm-bridge` as a sibling subprocess.
- `cmd mcp-perm-bridge` subcommand and its serialization protocol.
- The `--permission-prompt-tool=locutus_permission` flag wired into Claude Code; the embedded MCP tool config that names that tool; the `DriverConfig.PermissionToolName` / `QuestionToolName` per-driver string-match registry in [events.go:64-81](../internal/dispatch/events.go#L64-L81).

**What's added.** A single `internal/dispatch/acp/` package built on `github.com/coder/acp-go-sdk` v0.13.0. It implements `acp.Client` and exposes a thin adapter that emits the existing `AgentEvent` shape so the supervisor's event loop is unchanged. One ACP `Client` instance per workstream; one ACP server subprocess per workstream (chosen from the agent registry by name).

**Protocol coverage audit.** Every `AgentEvent` kind has an ACP equivalent or a documented mitigation:

| Locutus event | ACP source | Notes |
| --- | --- | --- |
| `EventInit` | `session/new` response → `sessionId` | Direct |
| `EventText` | `session/update agent_message_chunk` | Direct |
| `EventToolCall` | `session/update tool_call` | Typed `kind`, typed `locations[]`, `rawInput`, `rawOutput` — strictly richer than parsing NDJSON tool inputs |
| `EventToolResult` | `tool_call_update status: completed/failed` + `content[]` | Direct; `content` can include typed `diff` blocks |
| `EventResult` | `session/prompt` response with `stopReason` | Richer enum (end_turn / max_tokens / max_turn_requests / refusal / cancelled) than today's binary success/failure |
| `EventError` | JSON-RPC error response or `tool_call_update status: failed` | Direct |
| `EventPermissionRequest` | `session/request_permission` method (we implement on Client) | Deletes the entire bridge subsystem |
| `EventClarifyQuestion` | No first-class equivalent | **Gap A** — extension method `_locutus/clarify_question`, advertised via `_meta` capability. Bridges that don't implement it degrade to plain agent text, which is the same failure mode as a non-cooperating agent today. |
| `EventRetry` | Not surfaced by ACP | **Gap B** — agent-internal LLM retries become invisible. Not load-bearing — today's event is purely informational verbose logging, and supervisor-side OTel spans on our own adapter calls still capture the layer we control. |

**Gap C — session resume model.** The load-bearing question. Today's retry-with-feedback flow spawns a fresh agent CLI per attempt with `--resume <session-id>` + a new prompt. ACP offers two paths: `session/load` (replays the entire conversation via `session/update` notifications — expensive for long sessions) or `session/resume` (restores context without replay, gated by the `sessionCapabilities.resume` capability). The cleaner ACP-native shape is *neither*: keep one long-lived agent subprocess per workstream and send multiple `session/prompt` calls inside one session, each delivering a turn. The supervisor's lifecycle model changes from "spawn per attempt" to "spawn per workstream, prompt per attempt"; the retry-with-feedback semantics still work, the resume capability becomes irrelevant on the happy path, and the agent stays warm for cycle-monitor and validator interaction.

**Phase 0 verification — outcome (2026-05-13):** confirmed positively against all three ACP servers. The probe is at `/tmp/acp-verify/cmd/phase0/` — a 220-line Go program using `coder/acp-go-sdk` that initializes, dumps capabilities, then runs two consecutive `session/prompt` calls inside one `session/new`. Result matrix:

| Agent | Protocol | `loadSession` | `sessionCapabilities` | Two prompts in one session | Notes |
| --- | --- | --- | --- | --- | --- |
| Gemini `--acp` (native) | v1 | true | `{}` (none) | both `end_turn` | minimum capability set; no `close` means stdin-close-to-terminate. http+sse MCP. Prompt/image/audio/embedded-context accepted. |
| Claude Code (`@agentclientprotocol/claude-agent-acp@0.33.1`) | v1 | true | close, fork, list, **resume** | both `end_turn` | richest. Extension `_meta.claudeCode.promptQueueing: true` — Claude-specific signal we can opportunistically exploit for retry-with-feedback. |
| Codex (`zed-industries/codex-acp@0.14.0`, GitHub release tarball) | v1 | true | close, list | both `end_turn` | middle ground. `auth.logout` advertised. http-only MCP (no SSE). |

Three implications fold back into this DJ:

1. **Capability degradation strategy.** The three servers do *not* expose a uniform capability surface, contrary to the earlier "ACP carries everything we need" framing. The ACP client wrapper must tolerate missing-capability cases: no `sessionCapabilities.close` → terminate by closing stdin or SIGTERM (Gemini); no `sessionCapabilities.list` → keep our own session registry (Gemini); no `sessionCapabilities.resume` → never call `session/resume` (Gemini + Codex). The spawn-per-workstream lifecycle is what makes this safe — the resume capability becomes irrelevant on the happy path because we keep the agent alive.

2. **Phase 7 ops detail correction.** `codex-acp` is not on crates.io despite the Cargo.toml's `[package]` declaration; it ships only as a GitHub release tarball with prebuilt binaries per (arch, os) target. The init-time preflight check that warns operators about missing bridges needs to know this — the install hint for Codex is "download from GitHub releases", not "cargo install". The npm path for `claude-agent-acp` works as documented.

3. **`promptQueueing` is opportunistic.** Claude Code's extension advertises that the agent can accept queued prompts (presumably without losing context between them). Not load-bearing for Phase 1 — the basic single-prompt-per-attempt model satisfies the retry-with-feedback flow on its own. Worth a separate, smaller follow-up DJ if we find a use for it later.

The original "testable in an afternoon" framing held: from clean checkout to verified-all-three was under an hour including bridge installs. None of the three required `session/resume`, none needed `session/load`-with-replay. Gap C is closed.

**Strict upgrades.** Enumerated because they materially shift the cost/benefit, especially for the observability / traceability concern raised in the design review:

- **W3C trace context across the protocol boundary.** ACP's `_meta` field [explicitly reserves](https://agentclientprotocol.com/protocol/extensibility) `traceparent`, `tracestate`, and `baggage` for OpenTelemetry interop. Supervisor-side spans become parents of agent-side spans, and our existing OTel pipeline (DJ-091's `trace.jsonl` + optional OTLP HTTP exporter) sees a coherent end-to-end trace. Today the agent's internal trace context is opaque to us; under ACP it stitches.
- **Typed `tool_call.locations[].path`.** First-class file-tracking field replaces our heuristic extraction from raw tool inputs in [events.go:130-152](../internal/dispatch/events.go#L130-L152).
- **First-class `diff` content blocks on tool calls.** The agent reports file modifications with `oldText` + `newText` inline; today we rely on the worktree's `git diff` and the agent's own self-report.
- **First-class `plan` notifications.** Structural progress signal for cycle detection — plan-entry status transitions ("step 2 of 4 → in_progress") are a richer cycle-detection input than text-pattern inference over the tool-call stream.
- **Typed `stopReason` enum.** Replaces our binary EventResult-or-error judgment with an explicit signal of why the agent stopped (refusal, max-turns, end-of-turn, cancelled).
- **JSON-RPC frame archive.** Bidirectional framed messages mean our raw event archive captures both directions, including the permission decisions and `fs/*` responses we send — today we only capture the agent's stdout NDJSON.

**Reversal criteria.** Revert if (a) the verification step shows none of the three ACP servers we depend on supports a long-lived session model and `session/load`'s full replay is prohibitively expensive at our session lengths, OR (b) the bridge dependencies (`claude-agent-acp`, `codex-acp`) stop tracking upstream CLI changes such that the version-pinning + CI-integration cost exceeds the per-driver maintenance cost we're deleting, OR (c) ACP undergoes a breaking version bump and the Go SDK lags long enough to block routine upgrades — at which point we'd weigh maintaining a vendored SDK fork against reverting. None of these are likely; (a) is the only one we can falsify before committing, which is why it's the verification step.

**Rejected alternatives:**

- **Gemini-only ACP spike behind a `--driver acp` flag, keeping the existing drivers for Claude Code and Codex.** Considered and rejected (the design review's correction): a parallel-implementations approach leaves us maintaining two abstractions and gains nothing structural. The point of ACP is that the dispatcher's interface *becomes* ACP; the per-CLI knowledge collapses to "which command launches the ACP server." Half-migrating gives us the cost of both worlds and the deletion of neither.
- **Keep custom NDJSON drivers, add ACP only for net-new agents.** Same parallel-implementations problem with a different framing. Doesn't address the observability upgrade (W3C trace context propagation requires ACP on every channel).
- **Wait for native ACP support across all three CLIs.** Defers indefinitely on a hypothetical — Gemini is already native; `claude-agent-acp` and `codex-acp` are the canonical adapters under registry-listed orgs and treating them as production-quality dependencies is what the ecosystem expects. The risk transfer is real but bounded (version-pin + CI-test against the pinned versions), and it's the same kind of dependency risk we already accept for the underlying CLIs themselves.

**Reference:** preserves DJ-010 (Agent Routing and Supervision) and DJ-012 (Advisory Delegation) unchanged at the supervisor layer; preserves DJ-091 (Session Trace Storage) unchanged at the artifact layer. The protocol audit informing this entry consulted the [ACP specification source](https://github.com/agentclientprotocol/agent-client-protocol/tree/main/docs/protocol) (overview, session-setup, prompt-turn, tool-calls, file-system, agent-plan, terminals, slash-commands, extensibility) and verified per-agent support via the registry. SDK pin: `github.com/coder/acp-go-sdk@v0.13.0`.

## DJ-120: Adopt Resume Narrows to Step-Level Under the ACP Lifecycle (Refines DJ-074)

**Status:** settled (further refined by DJ-121)

**Refined further by [DJ-121](#dj-121-coarsen-pre-planning-to-workstream-grain-agent-owns-step-decomposition-via-worktree-checklist-refines-dj-010-dj-074-dj-120) (2026-05):** DJ-120 narrowed DJ-074's resume contract from conversation-level to step-level; DJ-121 narrows it further from step-level to workstream-level on Locutus's side, with the agent's `_locutus/checklist.md` preserving step continuity *at the agent's level*. The future-direction note below — `session/load`-based revival as a possible next move — still applies: it would re-introduce conversation-level resume on a different mechanism, and would supersede DJ-120 (not DJ-074, which has by then been further refined twice). Read DJ-121 for the current resume contract.

**Context.** [DJ-074](#dj-074-true---resume-for-interrupted-adoption) committed a two-layer resume contract for interrupted `adopt` runs: (a) step-level resume, where the worktree is rebuilt from the workstream's feature branch and steps already marked complete are skipped, and (b) conversation-level resume, where the dispatcher reissued `--resume <AgentSessionID>` to the coding-agent CLI so the next prompt landed inside the *same* agent conversation. Both layers shipped on the driver model: `runWorkstream` took a `*ResumePoint{StepID, SessionID}`; `ClaudeCodeDriver.BuildCommand` translated `SessionID` into `--resume <id>` on the spawned CLI; the resumed step continued with the agent's prior chain-of-thought intact.

[DJ-119](#dj-119-agent-client-protocol-replaces-the-coding-agent-driver-layer) replaces that wire layer with ACP. The new lifecycle (settled during Phase 3) is *spawn-per-workstream*: a single ACP subprocess is opened per workstream, a single `session/new` runs for its duration, and every step + every retry attempt sends a fresh `session/prompt` into that one warm session. The per-attempt subprocess spawn is gone — and so is the per-attempt `--resume <id>` re-entry. When Locutus exits, the agent subprocess exits with it; the conversation does not survive the process boundary.

Phase 0 verification of DJ-119 confirmed that all three target agents (Gemini `--acp`, Claude Code via `claude-agent-acp`, Codex via `codex-acp`) advertise `loadSession: true`, which means `session/load`-based revival of a prior conversation is theoretically possible. It is not implemented today, and the spawn-per-workstream lifecycle does not require it on the happy path. Cross-process conversation-level resume becomes implementable, not implemented.

**Decision:** DJ-074's durable contract narrows from "conversation-level resume" to "step-level resume." Concretely:

- **`AgentSessionID` on `WorkstreamResult` is preserved on disk.** The field continues to be captured from the ACP `session/new` response and persisted alongside `StepStatus` on `ActiveWorkstream`. Its load-bearing role under the driver model — being replayed via `--resume <id>` on restart — is gone. It is retained for two reasons: telemetry (operators inspecting `.locutus/workstreams/*.yaml` after a crash still see *which* agent session ran which steps) and as a future hook for `session/load`-based revival if that path is ever wired up.
- **`runWorkstream` no longer replays `AgentSessionID` into a new ACP session.** Under Phase 3's spawn-per-workstream lifecycle, the dispatcher opens a fresh `Connection`, calls `session/new` to get a brand-new session ID, and uses that for the resumed run. Step skipping by `resumeFrom.StepID` still runs — already-complete steps are bypassed exactly as DJ-074 specified — but the resumed step's first `session/prompt` lands in a fresh conversation, not a continuation of the prior one.
- **The feature-branch durability guarantee is untouched.** [DJ-032](#dj-032-commit-per-workstream-on-a-local-feature-branch-reframed-2026-04-25)'s commit-per-workstream model means each completed step has already been merged onto `locutus/<ws-id>`. The git history is the durable record of what was done; the resumed run starts from that branch's tip and the agent observes the merged work as the prevailing state of the worktree.

**Alternatives considered:**

- **Cross-process conversation revival via `session/load`.** Phase 0 confirmed every target agent advertises `loadSession: true`. A future iteration could, on restart, spawn the agent, call `session/load <prior-id>`, and resume `session/prompt` into the reconstituted conversation. Deferred because (a) we have no measured pain point — feature-branch + fresh-conversation is operationally sufficient for the interruption cases we've actually hit, and (b) it's unclear how much state each agent's `session/load` actually replays. Two open questions a follow-up would have to answer first: does the agent re-establish enough chain-of-thought to be useful, and does the replay cost dominate the marginal benefit for typical Locutus workstream lengths. Promotable to its own DJ when either condition changes.
- **Daemonize the agent subprocess across Locutus invocations.** A long-running `locutus-agent-daemon` per workstream that survives Locutus exits, addressable on restart. Rejected: it would invert the spawn-per-workstream lifecycle DJ-119 just committed to, reintroduce the cleanup-on-crash problem the new lifecycle dispatches with, and add a daemon-lifecycle concern that no other part of Locutus needs.
- **Detach the ACP subprocess on Locutus exit (don't kill the agent).** Variant of the above. Rejected for the same reasons plus the practical one: process detachment is OS-specific, hard to reason about under SIGKILL, and the agent has no way to know it should keep running when the controller dies.
- **Tighten the resume window: only refuse resume if the workstream record is older than N minutes since last step.** A different framing — narrow when resume is offered, not what it does. Rejected as orthogonal; the user-visible regression is "resumed step starts a fresh conversation," which a TTL would not change.

**Consequences.**

- **User-visible:** mostly invisible. Resume after interruption still works; the worktree picks up from the feature branch's tip; the agent runs the next pending step. The single observable difference is that the resumed agent has no recall of the prior conversation — context-without-history. For multi-step workstreams where the prior step established conventions, naming choices, or domain understanding in agent memory, the resumed run starts fresh and may make different micro-decisions. The merged work on the feature branch acts as the agent's grounding instead.
- **Tokens:** the resumed step replays its full prompt context from the spec, not from agent memory. This is closer to a cold start on that single step than the warm continuation DJ-074's driver model offered. Workstreams that are interrupted near completion see this cost most visibly; workstreams interrupted near their start barely notice (the agent had little context to lose).
- **Code:** `cmd/adopt.go`'s `buildResumePoint` and `classifyActivePlans` keep their existing shape — they still read `AgentSessionID` off the workstream record and pass it through the dispatch layer. The dispatch layer ignores the field for ACP transport (the adapter has no `SessionID` parameter to receive it on the wire). `internal/dispatch/resume_test.go` was deleted during Phase 3 because it exercised the `--resume <id>` codepath specifically; the StepID-skip contract is exercised by `cmd/adopt_integration_test.go`.
- **Future direction:** if the context-loss-on-resume cost becomes measured pain, the `session/load` revival path is the natural next move. The `AgentSessionID` persistence we kept in place is the hook for it. A new DJ would supersede this one and re-introduce conversation-level resume on a different mechanism. Until then, this is where the contract sits.

**Reference:** refines DJ-074 (the original resume design); preserves DJ-032's feature-branch durability semantics unchanged; depends on DJ-119's spawn-per-workstream lifecycle for the lifecycle premise. The Phase 0 capability matrix lives in [.claude/plans/acp-migration.md](../.claude/plans/acp-migration.md).

## DJ-121: Coarsen Pre-Planning to Workstream Grain; Agent Owns Step Decomposition via Worktree Checklist (Refines DJ-010, DJ-074, DJ-120)

**Status:** shipping (Phases 1-8 landed 2026-05-14; Phase 9 — final `spec.PlanStep` removal — pending observation of the soft-deprecate path in practice)

**Context.** Locutus's planner today decomposes each [Approach](#dj-038-spec-graph-shape-and-derivation-rules) into a `MasterPlan` of `Workstream`s, each `Workstream` into a sequence of `PlanStep`s with per-step `Assertion`s. The supervisor in `internal/dispatch/supervisor.go` runs a retry-and-validate loop *per step*: the validator LLM grades each step's output against its assertions; failed steps retry with the validator's feedback as the agent's next user message; the monitor's churn detection runs at step granularity. [DJ-010](#dj-010-supervisor-implementation-design) committed this design; [DJ-074](#dj-074-true---resume-for-interrupted-adoption) and [DJ-120](#dj-120-adopt-resume-narrows-to-step-level-under-the-acp-lifecycle-refines-dj-074) both ground their resume contracts in `StepID`-level persistence.

The original rationale for this fine-grained pre-planning was load-bearing in 2024: coding agents had limited attention span and tool-following discipline, so Locutus pre-chunked the work into bite-sized assertions the agent could reliably attempt and a validator could reliably grade. Pre-planning was scaffolding for the agent's limitations.

May 2026 reality is different. Top-tier coding agents (Claude Code via `claude-agent-acp`, Codex via `codex-acp`, Gemini `--acp`) handle long-horizon multi-file changes competently. They plan internally, decompose their own work, spawn their own subagents when needed, and reflect on completeness mid-task. The attention-span justification for per-step pre-planning has substantially eroded.

What hasn't eroded is the agents' tendency to stub, forget, or silently compromise on requirements — the failure mode the per-step validator was *secondarily* defending against. But that defense doesn't require step-grained pre-planning. A validator grading a workstream's full acceptance criteria catches the same failures (just later); the marginal benefit of per-step is earlier detection and a smaller retry blast radius, not categorical catch coverage. Weighed against the planner / supervisor / persistence complexity that per-step decomposition costs, the benefit is incremental, not categorical.

The supervisor's role is also dual, not single. Per the user's framing in chat: (a) **validation** — confirm the workstream is actually complete, tests cover the spec, no functionality was left out; (b) **human-in-the-loop** — when the agent needs a decision, the supervisor consults the spec DAG, produces an answer, and persists new Decision nodes for what was decided. Function (a) was the step-grained piece; function (b) operates per-permission-request (already true under [DJ-119](#dj-119-agent-client-protocol-replaces-the-coding-agent-driver-layer)'s Phase 4 `Policy`) and is independent of step granularity entirely.

Step granularity is still useful — for the *agent's* own progress tracking, not Locutus's. If the agent maintains a checklist file in the worktree as it works, step-level resume continuity is preserved without Locutus needing a step model: on resume, the agent reads its prior checklist alongside the worktree state and continues from where it stopped.

**Decision.** Coarsen Locutus's pre-planning to the workstream grain. The decomposition unit for the planner is the Approach: one Approach → one Workstream. `PlanStep` is removed from the spec model.

Concretely:

- **The planner outputs Workstreams, not Workstreams-of-Steps.** Each Workstream carries the Approach's acceptance criteria (lifted up from where they used to live on `PlanStep.Assertion`s) and a `DependsOn` set derived from the spec DAG. No internal decomposition into steps; the agent does that work itself.
- **`Supervise(ctx, approach, conn, sessionID)` is the new shape.** One retry-and-validate loop per workstream. The validator grades against the Approach's full acceptance criteria at workstream completion. Churn detection runs at workstream grain. Retry feedback ("your auth middleware is missing JWT signature verification on `auth.go:Verify`") is specific enough for agent repair without needing to name a step.
- **The agent owns step decomposition via the worktree.** Each workstream's worktree gains a `_locutus/` directory:
  - `_locutus/plan.md` — written once by Locutus when the workstream starts. Contains the Approach node, acceptance criteria, pointers into the spec DAG subtree relevant to the workstream, and the agent's instruction to maintain `checklist.md`.
  - `_locutus/checklist.md` — written and updated by the agent as it works. The agent's internal step decomposition lives here, not in Locutus's persistence layer.
- **The HIL function is unchanged.** Phase 4's `Policy` infrastructure (under `internal/dispatch/policy/` + `internal/dispatch/guardian/`) already operates per-permission-request and consults the spec DAG. It needs no step model. Decisions surfaced during a workstream are attributed to that Approach's Feature subtree (the existing pattern).
- **Resume narrows again.** DJ-074 → DJ-120 → DJ-121: Locutus-side resume is now workstream-grained. On `adopt` restart, completed workstreams are skipped; the in-flight workstream is restarted from its feature-branch tip. Step-level continuity *within* the resumed workstream comes from the agent reading its own `checklist.md`, not from Locutus tracking step state.
- **The decomposition rule is explicit: one Approach → one Workstream.** Not phase, not architectural layer, not risk tier. For greenfield projects the natural Approaches happen to be roughly phase-aligned (infra, CI/CD, scaffolding, domain models, features, deployment glue), but the unit of decomposition is the Approach; "phase" is not a concept Locutus knows about. Dependencies fall out of the spec DAG.
- **Sequential workstream execution is the default; parallel is opt-in.** Even when the spec DAG marks two Approaches as independent (no `DependsOn` edge between them), running them concurrently against a shared codebase produces inferior results. A downstream workstream sees only a half-built state of an upstream workstream's work-in-progress, misses opportunities to mirror conventions / naming / library choices the upstream agent established, and may re-implement utilities the upstream agent introduced. Correctness — a coherent, consistent codebase where each workstream's output benefits from the *complete* output of every prior workstream — outweighs parallel throughput in every Locutus use case we have. The `Dispatcher`'s parallel-execution scaffolding (`MaxTotal`, `MaxPerAgent`, `executor.Step.Parallel: true`) is removed or defaulted to sequential. Concurrency becomes opt-in for the rare future case where a user explicitly trades consistency for speed (e.g., via a `--parallel` flag); it is not the default Locutus posture.

**Alternatives considered:**

- **Status quo (keep per-step decomposition).** Marginal validation-earliness benefit, but the cost is real: `spec.PlanStep` + planner output complexity + supervisor's step-grained retry loop + per-step persistence in `workstream.StepProgress` + step-grained churn detection + step-level resume bookkeeping. The user's "start simple, add" framing applies — the bar for keeping the complex shape should be "demonstrably catches things the simpler shape can't," and the failure modes we actually see (stubs, forgetting, silent compromises) are catchable at workstream grain too. Rejected.
- **One master plan → one agent run, no workstreams.** Maximum simplicity but rejected for concrete reasons: context-window collapse on greenfield projects (too much work in one run), no concurrency (truly independent work serializes), single point of failure (one crash redoes everything), validation arrives too late to be actionable, no cost-tier mixing across workstreams, HIL traceability breaks down (decisions don't map to spec subtrees naturally). The cons dominate beyond toy projects.
- **Phase-based workstream decomposition (infra → CI/CD → app code).** Rejected because phases are sequential by nature, so phase-based decomposition doesn't unlock the concurrency that workstreams exist to enable. "Phase" also isn't a spec-graph concept; introducing it would force pre-architectural decisions Locutus has no business making.
- **Layer-based decomposition (frontend / backend / db / infra).** Same objection — forces premature architectural categorization. The agent should decide what the layers are by reading the actual codebase, not have Locutus impose them.
- **Keep `PlanStep` as an internal scratch concept (planner outputs them, supervisor ignores them).** Rejected because dead-weight abstractions accumulate cost — they show up in code, in tests, in the workstream record, in the LLM prompts — and "the field exists but isn't load-bearing" is exactly the aspirational-shape failure mode `feedback_no_aspirational_fields` warns against.
- **Move step tracking to the agent's CLI memory only (no `checklist.md` file).** Rejected because under DJ-119's spawn-per-workstream lifecycle, the agent subprocess dies with Locutus. The CLI's in-memory step state is lost on restart. A file on disk is the persistence layer; the checklist living in the worktree under git's purview is the right shape (it survives crashes, it's diff-able for debugging, it's contextually adjacent to the work it tracks).
- **Default to parallel workstream execution when the DAG permits it (the pre-DJ-121 default).** Rejected because DAG-independence does not imply codebase-independence. Two workstreams the DAG marks as independent still produce a single merged codebase, and parallel execution against a shared codebase yields inconsistent conventions, duplicated utilities, and missed mirroring opportunities (a downstream workstream can't learn from an upstream workstream's complete output if it's running at the same time). Locutus's value is project-management quality — coherent, validated output — not throughput. The user's framing: "it's way more important to be correct than it is to be fast." Parallel execution stays implementable as opt-in (the dispatcher's executor still supports it under the hood), but the default flips to sequential. A downstream workstream now reads the complete merged state of every upstream workstream's feature branch before its first `Prompt`, exactly mirroring what a human running the same tasks one at a time would observe.

**Consequences.**

- **User-visible:**
  - `locutus status` reports workstream-grained progress, not step-grained. The "step 3/7 of workstream X" output goes away; what remains is "workstream X in progress, started at T, agent: claude-code". A user wanting finer detail reads the worktree's `_locutus/checklist.md` directly.
  - `locutus adopt` resume is workstream-grained. An interrupted workstream restarts from its feature-branch tip; the agent reads its own checklist to pick up internal step continuity. Completed workstreams skip exactly as before.
  - The validator's feedback arrives once per workstream rather than once per step. Marginally later detection of partial-completion failures.
- **Spec model:**
  - `spec.PlanStep` is removed. Acceptance criteria move up to live on `spec.Approach` (or its execution-time analog). Persistence migrations needed for any project with existing PlanSteps in `.borg/` — likely a one-shot `locutus update` flow that re-derives workstream-level acceptance criteria from the union of the old PlanSteps' assertions.
  - The planner's output shape simplifies. Prompts for the planner agent simplify too (no "decompose this Approach into ordered steps with assertions" instruction; just "scope this Approach into a workstream with acceptance criteria and a DependsOn set").
- **Code:**
  - `internal/dispatch/supervisor.go`: `Supervise` takes the Approach (or a workstream view of it), not a `PlanStep`. The retry loop's "feedback" string is now derived from a workstream-level validator pass, not a step-level one.
  - `internal/dispatch/dispatcher.go`: `runWorkstream` no longer iterates `ws.Steps`. The workstream is one supervised unit. The `_locutus/plan.md` write happens here after the worktree is created and before the agent's first `Prompt`. The `Dispatcher`'s `MaxTotal` and `MaxPerAgent` parallelism controls are removed; workstreams are scheduled sequentially in DAG-topological order. `executor.Step.Parallel` is set to `false` for every workstream.
  - `internal/workstream/`: `StepProgress` collapses. The workstream record retains status (running / complete / failed) and `AgentSessionID` (per DJ-120's telemetry rationale), but per-step bookkeeping goes away.
  - The validator prompt template changes — grade against approach acceptance criteria, not against a specific step's assertions. The validator agent definition under `internal/scaffold/agents/` needs an update.
  - The agent's system prompt for coding runs gains a section instructing it to maintain `_locutus/checklist.md`. The instruction goes in the scaffold-default coding-agent prompt.
- **Tokens:**
  - Per-workstream validation runs once per workstream instead of once per step; total validator tokens drop substantially (today's run does N validator calls for N steps).
  - The coding agent's run is longer per invocation (no per-step retry cycle), which means more tokens-per-prompt at the top end but fewer total prompts. Net is workstream-dependent.
- **DJ-074 / DJ-120 reference chain:** DJ-074 originally committed conversation-level resume; DJ-120 narrowed that to step-level under the ACP lifecycle; DJ-121 narrows further to workstream-level on Locutus's side, with the `checklist.md` mechanism preserving step continuity *at the agent's level*. The cumulative effect is that Locutus's persistence contract becomes simpler at each iteration while the user-visible resume promise stays roughly constant (the agent picks up where it left off).
- **Future direction:** if `session/load`-based conversation revival ever ships (the speculative path DJ-120 mentioned), the per-workstream Locutus contract still holds — only the agent's own context recovery improves. The shape DJ-121 commits to is forward-compatible with that.

**Reference:** refines DJ-010 (the supervision-implementation design — the orchestration model is preserved, only the planning grain changes); refines DJ-074 and DJ-120 (the resume contract narrows again, see chain above); depends on DJ-119's Phase 4 `Policy` for the unchanged HIL surface; preserves DJ-032's feature-branch commit-per-workstream durability.

## DJ-122: Graph-Mutation Workflow Executor with Spawner Nodes (Supersedes DJ-112 on Control-Flow Topology)

**Status:** shipping (Phases 1–7 landed 2026-05-14; manual smoke + Planning/Assimilation gate-spawner migration are follow-up work tracked elsewhere)

**Context.** [DJ-036](#dj-036-council-agents-and-workflow-dag-are-externalizable-files) committed externally-edited workflow YAMLs to support user customization without recompiling. [DJ-112](#dj-112-workflows-move-from-external-yaml-to-go-values-supersedes-dj-036-on-workflows) walked that back when the customization affordance went unused in practice, moving workflows to Go values while preserving the DAG executor's separation of "graph topology" from "control flow." DJ-112 codified the explicit rule: *"sub-graph loops, when needed, can be expressed in Go control flow rather than as cyclic dependencies."*

That rule was honest at the time. With YAML in the picture, keeping the graph acyclic kept the file format intelligible to a human editor. With workflows now in Go (per DJ-112), the audience the acyclic-graph property protected is gone — there's no edit-time reader the constraint serves.

Three forces converge against the acyclic-only stance now:

1. **The spec council needs a real convergence loop.** Per the [spec_scout rewrite](../internal/scaffold/agents/spec_scout.md), the spec generation council is iterating toward a YES answer to "do we have enough committed-to information to *define*, *develop*, *deploy*, and *support* every deliverable while aligning with `GOALS.md`?" A gap-finder critic emits implicit-assumption gaps; the architect commits values; the gate re-checks. This is a first-class loop, not retry-around-a-DAG. Today it would have to be a Go `for` outside `executor.Run`, with the entire DAG re-running every iteration even though most early nodes (scout, outline) do not need to re-execute.

2. **Conditional routing wants to be in the graph.** The same gap-finder pattern: an infra gap should route to `devops_critic`; a support gap should route to `sre_critic`; no gap should route to nobody. With control flow outside the executor, each critic runs every iteration regardless, gated by per-step `Condition` predicates that fire in parallel and waste calls. With routing inside the graph, only the relevant critic spawns at all.

3. **The agent set carries draft-vs-refine duplication that the topology is masking.** The 40+ agent prompt files under `internal/scaffold/agents/` differ primarily in whether they produce an initial draft or refine a draft against critic concerns. In a DAG-only model, these have to be separate agents wired into different rounds. In a model where the loop is first-class, "refine" is the same agent invoked with `iteration_index > 0` and `concerns` populated — one agent file, one node type, two invocation modes selected by input state.

**Why generic workflow engines avoid cycles** (and why their reasoning does not fully apply to Locutus):

The strict-DAG industry norm exists for honest reasons: a DAG always terminates; topological sort gives parallel-scheduling for free; durable engines (Temporal, Cadence) replay event histories deterministically; DAG UIs (Airflow, Argo) visualize cleanly because every execution corresponds to exactly one graph node; state semantics ("what was the input when this node ran?") are well-defined per node. Cycles break all five. So engines that serve heterogeneous users push loops to the host language, where the user's domain knowledge fills in the semantics the engine can't validate.

Locutus's workflow is *domain-specific* and *in-process*: the loop condition (the YES test) is known to the engine; iteration state is well-defined (the spec proposal evolves, accumulated concerns thread through); we do not replay across process boundaries; the audience for graph visualization is the same operator who will reason about iteration. The reasons strict-DAG is the right default for generic engines do not all apply here. But the design must still respect the ones that do (termination, observability), via specific commitments rather than by structural avoidance.

**Survey of existing libraries.** None of the battle-tested Go workflow libraries solve this:

- **Strict-DAG in-process libraries** ([Azure/go-workflow](https://github.com/Azure/go-workflow), [rhosocial/go-dag](https://github.com/rhosocial/go-dag), [d-tsuji/flower](https://github.com/d-tsuji/flower), [floxy](https://github.com/floxy-project/floxy)): all return cycle-detection errors at preflight. Their guidance to users is exactly DJ-112's: write loops as Go control flow outside the workflow.
- **Rule engines with DSL** ([rulego](https://github.com/rulego/rulego), [dagucloud/dagu](https://github.com/dagucloud/dagu)): re-introduce the DSL-as-config problem DJ-112 walked away from; dagu is actually a daemon (web server + scheduler + persistence). Wrong shape.
- **Distributed engines** (Temporal, Cadence, Hatchet, Argo): require their server. Violates Locutus's local-only posture. Notable that Temporal's *programming model* is the closest in spirit — workflow functions with native loops, materializing per-iteration activities as fresh execution-graph nodes — but the durable-replay machinery is the load-bearing reason it needs a server.

The build is small enough that maintaining our own beats integrating any of these.

**Decision.** Replace `internal/executor` with a graph-mutation executor on top of [`dominikbraun/graph`](https://github.com/dominikbraun/graph) (already a dependency per [DJ-084](#dj-084-dominikbraungraph-is-the-canonical-graph-library-spec-and-executor-share-it)). Loops, fanouts, and conditional gates are all expressed as **spawner nodes** that mutate the graph during execution. The topology remains acyclic at any point in time — loops materialize as fresh subgraph copies depending on prior iterations' outputs. The cycle-avoidance reasons that genuinely apply to us (termination, observability) are addressed by explicit commitments below, not by structural prohibition.

The model has two node kinds:

- **Work nodes.** Execute a step; return output. Same shape as today's `executor.Step.Run`.
- **Spawner nodes.** Execute a step; *and* mutate the graph by appending new nodes/edges before returning. The new nodes flow into the executor's ready frontier on the next iteration of its scheduler loop.

The executor's main loop:

```
while ready_queue not empty or pending nodes exist:
  pop ready node
  run it
  record output
  if it appended nodes, recompute ready frontier
```

All higher-level constructs collapse to spawners:

- **Fanout** is a spawner that appends N work-node copies depending on itself, plus a merge node depending on all N. Topological sort returns the N at level X for free; `dominikbraun/graph`'s level traversal handles the rest.
- **Conditional gate** is a spawner that reads its predecessors' outputs and appends one of several alternative subgraphs.
- **Convergence loop** is a spawner that reads accumulated state, checks the test, and either appends nothing (workflow terminates as the queue drains) or appends the next iteration's subgraph plus a fresh convergence-gate node, with `iteration_index` baked into the new nodes' IDs.
- **Iteration limit** is enforced inside the loop spawner: `if iteration_index >= budget, append a terminal "budget-exhausted" node instead of iteration_{n+1}`. Same mechanism, no separate concept.

**Three design commitments worth calling out explicitly:**

1. **Hard graph-size cap in the executor itself, not just in spawner logic.** A buggy spawner cannot OOM the process. Belt-and-suspenders: the loop spawner enforces its iteration budget; the executor enforces a global node-count cap (default: 1000× initial graph size) and errors out if exceeded. A `WorkflowExecutor[State].MaxGraphMultiplier` knob lets per-workflow callers tune this. The error message names the most recently spawning node so operators have one place to look instead of an opaque hang.

2. **Iteration metadata threaded through node IDs and event records.** Spawned nodes carry `template_id` and `iteration_index` in their IDs (and in `Step.Metadata`, which already exists for the event sink). `locutus status` and the workflow event stream render "spec-gen council, iteration 3 of 5" rather than an opaque chain. Without this, observability degrades fast as loops grow — and observability is one of the cycle-avoidance reasons that genuinely applies to us, so the metadata pattern is non-optional.

3. **Spawn-from-template as a small primitive.** The spawner receives a `func(state State) []*Step` template closure and an updated state value; it calls `executor.AppendSubgraph(template, state)` to materialize. This keeps the "what gets spawned" logic adjacent to the workflow declaration (preserving DJ-112's Go-values pattern) rather than scattered across a registry.

**The convergence loop, end-to-end:**

```
Initial graph:
  scout -> outline -> elaborate -> reconcile -> critique -> gate

When `gate` runs:
  - Reads state.OpenConcerns (collected from critique step)
  - If empty (YES on define/develop/deploy/support) -> append nothing, terminate
  - Else if iteration_index >= budget -> append terminal "force-converged" node
  - Else -> append:
      revise{iter:n+1} -> reconcile{iter:n+1} -> critique{iter:n+1} -> gate{iter:n+1}
    with revise depending on the prior gate's predecessors so state threads correctly
```

`revise` is a node, not a separate agent type. Its `Run` invokes the same architect agent today's `revise` step uses, but as one node in the graph rather than a separate workflow phase.

**The draft-vs-refine collapse as a consequence.** Today's `spec_feature_elaborator` and the architect's revise pass are separate agents because the workflow phases force them to be. Under DJ-122, the elaborator is one node type. First-iteration nodes have empty `concerns` input and produce drafts; subsequent-iteration nodes have populated `concerns` input and produce refines. The agent prompt branches on this internally (one prompt with conditional rendering on `iteration_index > 0`, or two prompt sections selected by mode — design decision deferred to the agent-set consolidation DJ that DJ-122 enables).

**Alternatives considered:**

- **Status quo (DJ-112's "loops live outside the DAG").** Keeps the executor simple but pays for it everywhere else: every loop is a Go `for` somewhere, conditional routing is per-step `Condition` closures with no graph visibility, the draft-vs-refine agent duplication has no path to collapse. Acceptable when YAML was the constraint; not acceptable now. The constraint that justified DJ-112's choice has lapsed.
- **Native cyclic topology (back-edges as first-class graph constructs).** Honest about what we want, but loses topological scheduling for the subgraph inside a loop, needs iteration-aware semantics throughout the executor, and complicates visualization. Hits the cycle-avoidance reasons that genuinely apply to us. Spawner-nodes get the same expressiveness while keeping the per-instant topology acyclic — same outcome, fewer pain points.
- **Import [Azure/go-workflow](https://github.com/Azure/go-workflow) and layer loops on top.** Smallest dep surface among the in-process candidates (3 direct deps, MIT, Microsoft-maintained, active May 2026), with typed Input/Output, retry/timeout, sub-workflows, interceptors. But it is strict-DAG by design (`ErrCycleDependency` from preflight), and we would have to wrap it with an outer iteration loop — the same shape as today. Worth borrowing as ideas (typed Input/Output, retry/timeout/condition shape, interceptor pattern) but not as a dependency.
- **State-machine library** ([looplab/fsm](https://github.com/looplab/fsm), [qmuntal/stateless](https://github.com/qmuntal/stateless)). Models the convergence loop cleanly but loses the parallel-execution and fanout primitives that today's executor handles well. State machines and DAGs answer different questions; gluing them together is more work than this design.
- **Move to a durable workflow engine (Temporal).** Temporal's programming model is the closest fit (workflow functions with native Go loops, durable per-activity execution). But it requires the Temporal server process, which violates Locutus's local-only posture. Worth revisiting only if Locutus moves to a server-resident architecture for unrelated reasons.

**Consequences.**

- **Code:**
  - `internal/executor/executor.go` rewrites against the spawner-node model. The `Step` type gains an optional `Spawn func(ctx, state) ([]*Step, []Edge, error)` returned alongside `Run`'s output. The scheduler's ready-queue loop is reworked to recompute after each step that spawns.
  - `internal/agent/workflows.go`: the three council workflows (`PlanningWorkflow`, `AssimilationWorkflow`, `SpecGenerationWorkflow`) are rewritten as initial node sets + spawner-closure templates. The spec council in particular collapses the per-round phases into a single loop spawner.
  - `internal/dispatch/dispatcher.go`: the outer workstream DAG continues to use the executor; no spawners needed there yet (workstream dependencies are static, per [DJ-027](#dj-027-hierarchical-plans-plan-of-plans-with-two-level-dag) / [DJ-121](#dj-121-coarsen-pre-planning-to-workstream-grain-agent-owns-step-decomposition-via-worktree-checklist-refines-dj-010-dj-074-dj-120)). The dispatcher's interaction with the executor API changes when `executor.Step` does; otherwise unchanged.
  - `internal/scaffold/agents/`: the agent-collapse work (draft/refine unification) is a follow-up DJ, not done here. DJ-122 enables it; the actual prompt-file consolidation is its own design pass once we see what falls out.
- **User-visible:**
  - `locutus status` gains iteration awareness — "spec-gen council, iteration 3 of 5" — derived from the iteration metadata threaded through node IDs.
  - The workflow event stream emits a new `WorkflowEventKind` for `graph_mutated` so MCP clients and the CLI spinner can render progress in loops correctly. Existing `step_started` / `step_completed` events continue per node.
  - No CLI-level verb surface changes. Convergence loops and conditional routing are workflow-internal.
- **Migration:** per the no-back-compat-until-self-hosting posture, this is a flag-day rewrite. The three council workflows migrate together with the executor. Tests built against `executor.Step` literals are rewritten to the new shape. The DJ-112 reversal-criterion clause "(a) we ship a genuine third-party-workflow plugin model where users author topologies without recompiling" is not satisfied by DJ-122 — topologies remain Go values; what changes is whether the executor's *runtime semantics* admit graph mutation. DJ-112's no-YAML stance is preserved.
- **Observability:** the bounded-cap safety net errors visibly with a "graph size exceeded N nodes, runaway spawner suspected: <node-id>" message. Operators get one specific point to look at instead of an opaque hang or OOM.

**Reversal criteria.** Revert to DJ-112's "loops outside the DAG" model if:

- (a) iteration-metadata observability proves insufficient — operators report they cannot tell what iteration of which loop is running, and threading more metadata does not fix it. At that point the spawner abstraction has failed at the observability obligation that justified it, and the simpler model (one Go `for` per workflow, easier to reason about end-to-end) wins back.
- (b) the bounded-cap safety net produces false positives in real workloads (legitimate workflows hitting the cap repeatedly) and tuning the multiplier does not fix it. Indicates that loop bodies are larger than the spawner model anticipated; either the cap design or the topology choice is wrong.
- (c) durable replay becomes a requirement — at that point we adopt Temporal's programming model and accept the server dependency, and the in-process spawner executor becomes redundant.

**Reference.** Supersedes [DJ-112](#dj-112-workflows-move-from-external-yaml-to-go-values-supersedes-dj-036-on-workflows) on the control-flow-topology axis (preserves DJ-112 on the no-YAML axis). Depends on [DJ-084](#dj-084-dominikbraungraph-is-the-canonical-graph-library-spec-and-executor-share-it) for the graph library. Enables a follow-up DJ on agent-set consolidation (draft/refine collapse) and a follow-up gap-finder critic that uses conditional spawning to route concerns. Motivated by the [spec_scout](../internal/scaffold/agents/spec_scout.md) rewrite and its convergence-test framing.

## DJ-123: In-Flight Spec Search for Council Agents (Extends DJ-094 / DJ-116 to Mid-Council State)

**Status:** proposed

**Context.** DJ-122 shipped the spec council's convergence loop and the winplan smoke run (2026-05-14) exercised it through five iterations to budget-exhaustion. Iter-0 → iter-1 was a clean convergence; iter-1 → iter-2 onward surfaced a different failure mode the loop machinery cannot solve: **cross-strategy contradictions introduced by parallel revise fanout.** Two parallel elaborators committed to incompatible foundational choices (Auth0 vs Clerk for IdP; Schema-per-tenant vs RLS for tenancy; Node.js vs Python for the ingest runtime; Stadia Maps vs MapTiler for vector tiles; $5,000 vs $15,000 for the surge cost ceiling), the reconciler did not catch them (it dedupes inline decisions, not foundational strategy commitments), and the gate caught each pair after the fact and re-spawned them as `OpenDimensions`. Iter-3's elaborator addressing the contradiction resolved its quoted axis, but parallel iter-3 elaborators on adjacent strategies re-introduced the same contradiction at the next layer of detail, so iter-4 saw the gap back. Recurrence-based termination did not fire because the gate's axis names drift slightly between iterations (`"RLS vs Schema-per-tenant"` → `"Schema vs RLS"` → `"database tenancy isolation model"`).

The structural source: each elaborator is asked to "address this cluster" and sees the cluster's topic, findings, prior content of the targeted node, and (per the DJ-122 follow-up) the gate's `current_commitment_quoted` when present. What the elaborator does *not* see is **what every other strategy in the in-flight proposal currently says** — so an elaborator committing the cost-ceiling strategy has no idea another strategy committed to a vector-tile provider with its own cost implications, and an elaborator committing tenancy has no idea another strategy already named the rejected option in its hosting body. Cross-strategy alignment is an axis the council cannot reason about because no agent has a view of the whole proposal at fanout time.

[DJ-094](#dj-094-spec-lookup-tools-spec_list_manifest--spec_get-are-agent-facing-mcp-tools) shipped `spec_list_manifest` / `spec_get` against the persisted spec at `.borg/spec/`. [DJ-116](#dj-116-spec-graph-full-text-index-bluge-backed-on-disk-shared-by-list-and-spec_search) shipped a Bluge full-text index and the agent-facing `spec_search` tool, also against the persisted spec. Both serve the case where a verb runs against an existing spec snapshot. Neither helps the council, which is *generating* the spec — at fanout time the proposal lives only in `state.RawProposal` (JSON in memory) and `state.ProposedSpec` (the post-reconcile canonical shape); nothing is persisted yet.

**Why this surfaced now.** The pre-DJ-122 spec council had no convergence loop — the static `revise → reconcile_revise` tail ran once and shipped. Cross-strategy contradictions still existed in those single-pass outputs, but they were absorbed into the persisted spec without re-detection. DJ-122's gate is the first agent in the council that grades the *assembled* proposal; it sees the contradictions explicitly and emits them as `OpenDimensions`. The loop then asks elaborators to fix them — but the elaborators have the same per-cluster blindness that produced the contradictions in the first place. DJ-122 exposed the gap by giving the system a feedback signal; DJ-123 gives the elaborators the information they need to act on that signal.

**Decision.** Extend the agent-facing `spec_search` tool (DJ-116) so it operates over the in-flight `state.RawProposal` during council runs, exposed to the elaborator, gate, critic, and reconciler agents as the same tool surface they already know. A Bluge in-memory index is rebuilt at each `Merge` callback that mutates `state.RawProposal`; the existing DJ-117 per-field match diagnostics work unchanged; the tool's input/output schema is unchanged. From the agent's perspective there is one `spec_search` tool whose backing store depends on context — the persisted index when running against `.borg/spec/`, the in-flight index during a council run. The shape stays the same on either side of the boundary.

Three design commitments worth calling out:

1. **Title-weighted BM25, no embeddings in v1.** The elaborator's natural queries are foundational-axis discovery — `multi-tenant isolation`, `vector tile provider`, `cost ceiling`, `secrets management` — and the contradicting strategies tend to share vocabulary because they're addressing the same concept. BM25 with title-weighting (existing field-boost pattern from DJ-116) catches them. The LLM reading the hits is already the semantic layer; layering embedding cosine similarity on top would add per-call API latency, embedding-model-version non-determinism, and indexing overhead for marginal gain. Bluge supports vector search natively, so adding an embedding pass later is additive (no re-architecture). Empty-result instrumentation surfaces whether the BM25-only stack misses real cross-vocabulary cases worth fixing; if the rate exceeds a measured threshold in practice, hybrid scoring becomes a follow-up DJ rather than a v1 burden.

2. **Re-index on `RawProposal` mutation, not on every state change.** `mergeReconciledProposal`, `mergeRevisedNodes`, `mergeElaboratedFeatures`, and `mergeElaboratedStrategies` all touch `state.RawProposal`. A re-index runs after each. Smaller mutations (e.g., `Concerns` appends) do not trigger one. For a ~50-strategy proposal this is sub-millisecond work per merge; not worth a debounce or dirty-tracking layer. The index lives on the council's `WorkflowExecutor` instance, scoped to the run, garbage-collected when the run ends. No on-disk artifact — the persisted index stays at `.locutus/spec_index/` for the post-run case, untouched.

3. **The tool surface stays singular.** The elaborator's `.md` knows about `spec_search`. The MCP runtime registers it. Whether the backing store is the persisted Bluge index or an in-memory one during a council run is a configuration decision made by the agent dispatcher at runtime — the agent does not branch its prompt on which store is live. This preserves the DJ-094 contract that agents see a stable tool surface across council and post-council contexts; it also means the existing `spec_search_eval` build-tagged test continues to cover the prompt's mid-tier portability without modification.

**The elaborator's information flow, end-to-end.** Iter-N revise fans out one elaborator per `FindingCluster`. Each elaborator's `RunStep` begins with a small fixed query budget (2-4 search calls) before producing its strategy/feature output. The elaborator's prompt directive: *"before committing to a foundational choice (vendor, runtime, framework, tenancy model, cost tier), search the proposal for existing commitments on the same axis using both the domain term and the technical term. Cite hits in your rationale and state whether you align or supersede."* The elaborator now produces a strategy body whose decisions cite `strat-hosting-deployment` (or whichever existing strategy made the call) rather than picking blind. Result: iter-N elaborators converge on cross-iteration consistency without coordination.

The within-iteration parallel-fanout race remains — two iter-N elaborators querying simultaneously each get the iter-(N-1) snapshot and may still pick opposite values on a freshly-contested axis. This is acknowledged explicitly here as **out of scope for DJ-123**; the fix lives in a follow-up entry that either makes the loop-template's revise step sequential, introduces a pre-revise foundational-axis coordinator, or extends the reconciler to detect cross-strategy contradictions on the assembled output. The data from DJ-123's first real run determines which of those is the load-bearing follow-up. Closing cross-iteration recurrence first is the higher-leverage move: the winplan trace shows three of six iter-2 contradictions resolved cleanly to iter-3 and re-emerged at iter-4 because elaborators were blind to prior commitments; an in-flight search would have let them align, and the loop would have terminated inside the existing budget without the within-iteration fix.

**Alternatives considered:**

- **Embedding-cosine-only or hybrid search from the start.** Real benefit for semantic rephrasings ("on-call ownership" ↔ "the engineering team carries the pager"), but the LLM is already the semantic interpreter — it filters BM25 hits by meaning. The cost (per-call embedding latency, model-version drift across runs, council-run reproducibility breaking) is real; the gain over BM25-plus-title-weighting is uncertain until measured. Bluge supports vector fields if the BM25 v1 ships with empty-result instrumentation showing a real miss rate that hybrid would close.

- **Make the elaborator prompt include the entire current `RawProposal` inline.** The prompt already approaches the context-window limit on large projects (winplan's iter-3 critique prompt was ~50k input tokens). Inlining the whole proposal in every fanout elaborator's prompt scales the wrong way: N elaborators × full-proposal-size per elaborator = O(N²) tokens for a wave. Search returns just the matching slices, scaled to the cluster's needs.

- **Sequential elaboration (`Parallel: false` on the loop-template revise step).** Solves the within-iteration race directly but pays in wall-clock: a 12-cluster revise wave at 30-90s per elaborator turns into 6-15 minutes of serial work per iteration. The cross-iteration recurrence problem (the bigger source of divergence) still needs solving on top. This stays in the alternatives stack for the within-iteration follow-up.

- **A pre-revise foundational-axis coordinator agent that picks defaults on contested axes before the fanout fires.** Cleanest semantic answer for within-iteration contradictions but adds a new agent and a new workflow step, and the contested-axis set is itself emergent from the critic outputs — picking defaults requires reading the same proposal the elaborators would read. The coordinator and the in-flight-search-equipped elaborator are doing similar work; the elaborator's per-cluster path is the simpler shape. Held for the within-iteration follow-up.

- **Cross-strategy contradiction detection inside the reconciler.** The reconciler currently dedupes inline decisions across features and strategies. Extending it to detect "two strategies committed to incompatible foundational choices" would catch contradictions after assembly but before the critic / gate. Doesn't help the elaborator avoid producing them — fixes the symptom downstream rather than the cause upstream. Complementary to DJ-123 (catches what slips through in-flight search) but not a substitute. Worth doing as the follow-up that closes the within-iteration parallel-fanout gap.

**Consequences.**

- **Code:**
  - New `internal/search/inflight.go` — an in-memory Bluge index keyed off `state.RawProposal` JSON. Reuses the field set + analyzer configuration from `internal/search/build.go` (the existing on-disk index code) so the agent sees the same field-match diagnostics either way.
  - `internal/agent/spec_tools.go` — `spec_search` registration takes a backing store at construction time; the existing on-disk store becomes one implementation, the new in-flight one becomes another. The tool descriptor (input shape, output shape, MCP wire format) is unchanged.
  - `internal/agent/workflow_spec_generation.go` — `WorkflowExecutor[PlanningState]` setup wires the in-flight backing store; merge callbacks that touch `state.RawProposal` trigger the re-index. Council runs that don't carry `RawProposal` (none today) opt out implicitly.
  - `internal/scaffold/agents/spec_strategy_elaborator.md` and `spec_feature_elaborator.md` — system-prompt updates per agent-conventions: positive framing of "search the proposal before committing to a foundational choice; cite hits in your rationale; align with existing commitments or explicitly supersede." Walk [`docs/agent-conventions.md`](agent-conventions.md) at edit time per the memory checklist.
  - `internal/scaffold/agents/spec_reconciler.md` — minor prompt addition acknowledging the tool's availability; reconciler use is exploratory in v1 (the main consumer is the elaborator).
  - `internal/agent/spec_search_eval_test.go` — extend the build-tagged eval to include an in-flight-index fixture so mid-tier portability is verified for both backing stores.

- **User-visible:**
  - Council runs against larger projects converge in fewer iterations and stay inside the default budget more reliably. Workflow event stream gains `tool_call` events for spec_search invocations during council steps (already emitted for the persisted-store case per DJ-094).
  - No verb-level surface changes; `locutus refine`, `locutus import`, `locutus assimilate` all benefit transparently when their underlying council uses an elaborator that searches.
  - `--explain` output on `locutus search` is unchanged; in-flight search is council-internal.

- **Performance:**
  - Per-merge re-index cost on a ~50-strategy proposal: sub-millisecond (Bluge in-memory writes are fast; the corpus is small).
  - Per-elaborator wall-clock: increases by ~1-2 search-tool round-trips (~200-500ms each on a local Bluge query) plus the model's tool-use generation overhead (one additional generate-and-evaluate turn). Net: a strategy elaborator that used to take 30-60s takes 45-90s with 2-3 searches.
  - Total council-run latency: somewhat higher per iteration, lower across iterations (fewer iterations to converge). Empirical question; the winplan re-run is the validation case.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. The elaborator prompts gain the directive; council runs against pre-DJ-123 binaries continue to ignore the tool and operate as before. New binaries write a fresh `.borg/agents/spec_*.md` on `update --reset`. No persisted-state schema change.

**Reversal criteria.** Revert if:

- (a) the BM25 miss rate (empty-result responses on queries where the proposal demonstrably contains relevant text) exceeds 25% in a real workload, AND the title-weighting + multi-framing-query mitigations do not bring it below that threshold. At that point the LLM-on-BM25 stack is leaking the very contradictions DJ-123 set out to prevent, and either the hybrid embedding path or a more structured "axis registry" intermediate model becomes the right next move.
- (b) per-elaborator latency growth makes council runs unviable in interactive use — `locutus refine goals` taking >30 minutes on a small project because every elaborator burns 4-5 search round-trips. Solvable by capping the search budget per elaborator, but if the cap reduces hit rate sharply enough that contradictions return, the cost-benefit has flipped.
- (c) the within-iteration parallel-fanout problem turns out to dominate convergence behavior to the point that closing cross-iteration recurrence is irrelevant. The winplan trace argues against this — the persistent contradictions across iter-2/3/4 were cross-iteration, not within — but a second project with different shape could surface the inverse.

**Reference.** Extends [DJ-094](#dj-094-spec-lookup-tools-spec_list_manifest--spec_get-are-agent-facing-mcp-tools) (the spec-lookup tool surface this builds on) and [DJ-116](#dj-116-spec-graph-full-text-index-bluge-backed-on-disk-shared-by-list-and-spec_search) (the on-disk Bluge index this mirrors in-memory). Honors [DJ-117](#dj-117-spec-search-per-field-match-diagnostics-on-list-and-spec_search-hits) by sharing the per-field diagnostics path. Depends on [DJ-122](#dj-122-graph-mutation-workflow-executor-with-spawner-nodes-supersedes-dj-112-on-control-flow-topology)'s convergence loop — the loop is what makes mid-council search useful (single-pass runs see the whole proposal in the next agent's prompt anyway). Motivated by the winplan smoke run trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260514/1753/54-c319a3/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260514/1753/54-c319a3/) and the `convergence_failed` event it produced. The within-iteration parallel-fanout follow-up (sequential revise, pre-revise coordinator, or cross-strategy reconciler check) lives in a future DJ pending data from DJ-123's first real run.

**Epilogue (2026-05-15).** The first real winplan re-run with DJ-123's elaborator-side in-flight search shipped (binary at `29a2ba7`) failed to converge in 5 iterations — trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/). Diagnosis: zero `spec_search` calls fired across 138 agent invocations. The Phase 4 prompt edits did not move Flash-tier elaborators toward tool-use; Pro-tier reconciler invoked `spec_list_manifest` reliably but never the search. The infrastructure works; the consumers don't engage. More fundamentally, the iter-3 → iter-4 explosion pattern (3 open dimensions → 5 entirely new ones) revealed a structural issue DJ-123 cannot solve: decisions and narrative are coupled in elaborator output, so revising one re-litigates the other. DJ-124 supersedes DJ-123 as the convergence mechanism by separating decision-making from narrative authoring into distinct phases. DJ-123's in-flight search infrastructure (Phases 1-3, 5 of its plan) remains as defense-in-depth in DJ-124's Phase 2 narrative elaborators. **Status update:** `proposed` → `landed; superseded as convergence mechanism by DJ-124; in-flight search infrastructure stays as defense-in-depth.`

## DJ-124: Spec Generation Re-Architecture — Decisions Before Narrative, Scout-as-Judge Convergence, Unified Import Flow (Refines DJ-068 Spec Graph Topology, Replaces DJ-105 Inline-Decisions Schema, Re-Scopes DJ-123 In-Flight Search)

**Status:** shipping (Phases 1-8 landed 2026-05-16 on branch `worktree-dj-124-decisions-before-narrative`; Phase 9 — winplan re-run validation — pending live model execution; Phase 10 status-flip to `shipped` conditional on Phase 9)

**Context.** DJ-122 shipped a convergence loop for the spec-generation council. DJ-123 attempted to close cross-iteration recurrence in that loop by giving elaborators an in-flight `spec_search` against the working `RawProposal`. The first real winplan re-run (trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/)) failed to converge inside the 5-iteration budget. The empirical signature was an **iter-3 → iter-4 explosion**: iter-3 had 3 open dimensions (multi-tenant isolation, rollout strategy, runbook structure); iter-4 had 5 entirely *new* dimensions (Redis hosting, auth provider, container orchestration, DB engine, multi-tenant isolation re-named); iter-5 saw the same 5 with subtle renames. The DJ-123 in-flight search did not fire — zero `spec_search` invocations across 138 agent calls — but the deeper problem is structural.

Three coupled structural issues surfaced from the diagnosis:

First, **decisions and narrative are authored together by elaborators**. Each elaborator emits a feature/strategy body AND the inline decisions justifying it in a single output. When the reconciler picks a winning decision among siblings, the narrative on the losing side is now wrong and must be re-elaborated; that re-elaboration produces new inline decisions on adjacent axes that may conflict with other siblings; iterate. Convergence requires breaking this coupling.

Second, **today's workflow has a Phase-0-style scout that runs once at session start and a convergence gate that runs after each iteration** — these are the same role asked at different times ("given what we know now, is the spec complete?") and treating them as distinct agents creates artifacts (axis-name drift between the two, separate prompt maintenance, separate convergence vocabularies). Unifying them removes the duplication.

Third, **the spec graph was misdescribed**. CLAUDE.md and several DJs read `Goal → (Feature | Strategy) → Decision`, implying decisions are children of features/strategies. The intended graph is the linear chain `Goal → Decision → (Feature | Strategy) → Approach`. Reference direction (solid arrows below) is `Approach → Feature/Strategy → Decision → Goal` — each kind cites the kind upstream of it. Authoring/flow direction is the inverse. Axes that need decisions are surfaced from goals, features, AND strategies (dotted arrows below) — meaning new imported content and existing nodes can both contribute to the axis set the decisions phase resolves.

In Mermaid form:

    flowchart TD
        A[Approaches] --> F[Features] & S[Strategies] --> D[Decisions] --> G[Goals]
        G & F & S -.->|axes| D

Correcting the graph is what enables the workflow re-architecture, because today's inline-decisions pattern is what made the misdescription practical.

**Why this surfaced now.** Pre-DJ-122 the council had no convergence loop — single-pass elaborate→reconcile→ship absorbed inline decisions into the persisted spec without re-detection. DJ-122's gate exposed cross-strategy contradictions explicitly; DJ-123 tried to close them with in-flight search. The winplan re-run showed both that elaborators don't reliably engage tools when the prompt directs them to (Flash-tier specifically), and that the underlying coupling between decisions and narrative would defeat the search even if it did fire. The data forces a structural rather than tooling fix.

**Decision.** Re-architect the spec-generation workflow around four coupled changes:

1. **Decisions move out of the elaborator's output and become first-class outputs of their own phase.** `RawFeatureProposal.Decisions` and `RawStrategyProposal.Decisions` change type from `[]InlineDecisionProposal` to `[]string` (references to decision IDs). A new agent — the *decision-elaborator* — authors decisions one-per-axis in Phase 1. Feature/strategy elaborators (renamed *narrative-elaborators* in this DJ) author narrative in Phase 2 and reference settled decisions by ID. The `spec.Feature` and `spec.Strategy` persisted shapes gain a `Decisions []string` field with `minItems=1` — every feature/strategy must be anchored in at least one decision.

2. **The scout becomes the unified gap analyzer, convergence judge, and decision-mapper.** A single agent role runs every iteration. Its job: read goals + existing graph + any imported content; surface axes that need decisions; for each surfaced axis, check whether an existing decision covers it; emit only the uncovered ones as `axes_open[]` and emit any new feature/strategy node it identified with `decisions[]` pre-populated from existing decisions on covered axes. The standalone gate role goes away. Convergence is `axes_open == [] AND no critic findings`.

3. **Axes are scout-determined and assigned at decision-creation time.** Decisions gain `Axes []string` (the axis IDs the decision answers, plural to support intentional shared-axis cases like staff-vs-end-user-auth) and `SurfacedBy []string` (the goal / feature / strategy IDs that surfaced the axis, set by the scout's dispatch). Open axes stay ephemeral (no separate spec kind); decided axes ride on the decisions covering them. Cross-run continuity comes for free via set membership on `Decision.Axes`.

4. **`locutus import` becomes a thin entry point into the standard workflow.** Today's import has its own admission/triage flow; DJ-124 dissolves that into the unified loop. The imported content (PRD markdown, etc.) joins goals + existing graph as input to the scout. The scout surfaces any new axes the imported content implies, identifies the new feature/strategy node, maps covered axes to existing decisions, and emits the work-to-do. Phase 1 commits any new decisions; Phase 2 authors the new node with its full reference set. Same workflow handles refine and import.

The workflow becomes a single loop with one entry shape:

    (loop until scout.converged)
      scout(state, findings, imported_content, prior_scout_output) → {
        converged?,
        axes_open[],          // only uncovered axes (need Phase 1)
        new_nodes[]            // new features/strategies; decisions[] pre-populated from covered axes
      }
      if converged: exit
      Phase 1 (decision-elaborators, parallel, one per open axis):
        decision-elaborator(axis, surfacing-nodes, findings) →
          grounded research → pick → justify → tag with axis ID + surfaced-by
      controller: affected = features_referencing(changed_decisions) ∪ nodes_named_in(findings) ∪ new_nodes
      Phase 2 (narrative-elaborators, parallel, only for affected):
        narrative-elaborator(node, decisions-it-references, findings) → updated body
      Phase 3 (critics, parallel):
        critics(state) → findings[]

Six design commitments worth calling out:

1. **Open vs closed axis terminology is strict.** An axis is *closed* once at least one decision tagged with its ID exists in the graph. An axis is *open* only when no covering decision exists yet. The scout's `axes_open[]` surfaces only the uncovered set; closed axes are implicit via the decisions covering them. Convergence is literally "every axis the scout could identify is covered."

2. **Scout names axes; does not enumerate options.** Per-axis option research lives inside the decision-elaborator that owns the axis. Scout's job is domain pattern-matching with light grounded research for project shape ("what does a web-hosted electoral campaign app entail?"), surfacing axes like `auth-provider`, `tenancy`, `tech-stack`, `ci-cd-pipeline`, `dev-inner-loop`. The decision-elaborator then researches options for `auth-provider`, evaluates them, picks one, and emits a Decision tagged with that axis. Separating these keeps each role focused and avoids the chicken-and-egg of "scout researches options for axes it hasn't named yet."

3. **Decision-elaborators are grounded; cite chosen path AND rejected alternatives.** `spec.Alternative` gains `Citations []Citation` with `minItems=1`. `spec.Citation.Kind` enum extends with `web` for grounded-research evidence the decision-elaborator gathered itself. Every commitment carries its evidence chain — both why the chosen option was picked and why each alternative was rejected. The auditability claim of the spec graph extends to alternative-rejection reasoning.

4. **`minItems=1` on `Feature.Decisions` and `Strategy.Decisions` enforces the linear chain structurally.** The reference list is scout-determined — not mechanically every-foundational-decision. The scout analyzes which decisions a feature actually depends on (auth and DB for a behavioral feature; charting library and real-time strategy for a dashboard) and pre-populates the reference list. The integrity validator catches dangling references; the schema constraint catches features ungrounded in any decision. There is NO "defer architectural commitment" escape pattern from DJ-105 — if an axis can't be decided yet, it stays in `axes_open` across iterations and the loop doesn't converge; user intervention closes it (more context, goal change, explicit `locutus refine`).

5. **Tier pinning by role.** Scout: strong + grounded (axis identification needs domain breadth; grounded for novel domains). Decision-elaborator: strong + grounded (research + commitment). Narrative-elaborator: balanced + ungrounded (consumes settled decisions; mechanical writing). Critics: balanced + ungrounded (unchanged). Cost concentrates where commitments are made.

6. **Conditional Phase 2 dispatch.** Workflow controller computes `affected = features_referencing(changed_decisions) ∪ nodes_named_in(findings) ∪ new_nodes_from_scout` and dispatches narrative-elaborators only for the affected set. Most iterations touch only a handful of features/strategies, not the full N. This bounds per-iteration cost and prevents the mass-rewrite that was the iter-4 explosion source.

**The workflow's information flow, end-to-end.** Iter-1 (greenfield refine): scout reads GOALS.md, surfaces axes (frontend, hosting, auth, tenancy, tech-stack, CI/CD, dev-loop, ...). All axes are open. Phase 1 dispatches one decision-elaborator per axis; each researches options and commits with citations on chosen + rejected paths. Phase 2 dispatches narrative-elaborators for the features/strategies named in the outline. Critics run; produce findings. Iter-2: scout consumes prior axes_open + new findings + state; most axes stay decided; maybe one new axis surfaces from a critic finding. Phase 1 dispatches only the new axis's decision-elaborator. Phase 2 dispatches only affected nodes. Critics run again. Iter-3: scout sees no open axes and no findings → `converged: true`. Exit.

Brownfield refine collapses identically: iter-1 scout sees existing decisions, surfaces only axes not yet covered, Phase 1 + Phase 2 do incremental work.

Import collapses identically: imported PRD joins the scout's input set; scout identifies the new feature node from it, maps the feature's axes to existing decisions (most covered → references pre-populated), surfaces any genuinely new axes, Phase 1 commits new decisions for uncovered axes, Phase 2 authors the new feature with the full reference list.

**Alternatives considered.**

- **Pre-fanout coordinator (DJ-123's out-of-scope follow-up candidate).** A single strong-tier agent reads scout brief + outline, commits foundational decisions before Phase 2 narrative fanout. Cleaner than DJ-123's mid-fanout coordination, but still leaves the decisions/narrative coupling in place — Phase 2 elaborators authoring inline decisions can still re-pick on already-settled axes. DJ-124's structural separation (decisions are their own first-class output of their own phase) subsumes this approach and addresses the coupling at its source.
- **Sequential revise instead of parallel.** Solves within-iteration parallel-fanout contradictions by serializing. Pays in wall-clock. Doesn't solve the decision/narrative coupling — sequential elaborators still author both at once. Held in alternatives stack only as a fallback if Phase 9 validation surfaces within-Phase-1 coordination problems (parallel decision-elaborators on different axes committing to mutually-incompatible choices).
- **Persisted axes as a first-class spec kind.** Adds lifecycle complexity (proposed → answered → superseded → deprecated) with no consumer demanding it. Decided-axis identity comes for free via `Decision.Axes`. Held for a follow-up DJ if a future verb needs cross-session known-unknowns tracking.
- **Embedding-cosine search to fix DJ-123's tool-engagement problem.** The diagnosis was that the LLM doesn't call `spec_search` at all, not that the BM25 results were poor. Adding embedding similarity doesn't fix non-engagement. DJ-124 retires search-as-convergence-mechanism; embedding search remains a candidate optimization for `spec_search` more generally.
- **Detect cross-strategy contradictions in the reconciler.** Catches contradictions after the fact, doesn't prevent them. The decision-elaborator + narrative-elaborator separation prevents the contradiction from being authored in the first place; reconciler retains a smaller scope (cross-decision dedupe) which is mechanical.
- **Keep `locutus import` as a separate flow with its own admission logic.** Today's import has its own triage agent and its own admission semantics; preserving that meant maintaining two parallel workflows that did similar work. Unifying under the scout-driven loop is cleaner and means improvements to the workflow benefit both verbs uniformly.
- **Make `Feature.Decisions` optional (`minItems=0`).** Considered seriously in chat (2026-05-15). The argument for: features that don't surface new axes shouldn't be forced to cite transitive foundational decisions just to satisfy the schema. The argument against (which won): if the scout is the analyst that determines a feature's decision references, the reference list is scout-determined work, not transitive bookkeeping. `minItems=1` makes the linear chain a structural invariant rather than a convention prose maintains. Brownfield imports satisfy `minItems=1` trivially (the scout references existing decisions); greenfield imports satisfy it because the loop commits decisions before authoring features.

**Consequences.**

- **Code:**
  - `internal/spec/types.go` — `Decision.Axes []string` (`minItems=1`), `Decision.SurfacedBy []string`, `Alternative.Citations []Citation` (`minItems=1`), `Citation.Kind` enum extended with `web`, `Feature.Decisions []string` (`minItems=1`), `Strategy.Decisions []string` (`minItems=1`).
  - `internal/agent/raw_proposal.go` — `RawFeatureProposal.Decisions` and `RawStrategyProposal.Decisions` change type to `[]string`; `InlineDecisionProposal` struct removed; new `RawDecisionProposal` struct for the per-axis Phase 1 output.
  - `internal/agent/workflow_spec_generation.go` — `NewSpecGenerationWorkflow` rewritten around the new shape; `mergeElaborated{Features,Strategies}` and `mergeRevisedNodes` replaced by `mergeDecisions` + `mergeNarrative`.
  - `internal/agent/state.go` — `PlanningState` extended with `priorScoutOutput`, `imported`, `axesOpen` fields.
  - New agent: `internal/scaffold/agents/spec_decision_elaborator.md` (strong tier, grounded, per-axis commit-and-justify).
  - Rewrites: `internal/scaffold/agents/spec_scout.md` (gap analyzer + judge + decision-mapper role); `spec_strategy_elaborator.md` and `spec_feature_elaborator.md` (narrative-only).
  - `internal/scaffold/agents/spec_reconciler.md` and `spec_gate.md` — scope reduced (reconciler) or retired (gate).
  - `cmd/import.go` — thin entry that admits content into `state.Imported` and runs the standard workflow.
  - Test rewrites across `internal/agent/workflow_spec_generation_test.go`, `internal/scaffold/scaffold_test.go`, `internal/agent/raw_proposal_schema_test.go`, `cmd/import_test.go`. New tests cover scout's mapping-to-existing-decisions, conditional Phase 2 dispatch, unified import flow.

- **Documentation:**
  - CLAUDE.md spec-graph line corrected to `Goal → Decision → (Feature | Strategy) → Approach` with the explicit note that decisions inform features/strategies and axes are surfaced from goals + features + strategies.
  - DJ-068, DJ-094, DJ-105, DJ-122 audited for old graph-framing prose; corrected or cross-referenced.

- **User-visible:**
  - `locutus refine goals` exits in fewer iterations on greenfield projects; brownfield runs touch only the changed slice.
  - `locutus import` produces a feature with structurally meaningful decision references rather than inline decisions admitted to the graph.
  - Decisions in the persisted spec carry axis tags AND back-references to surfacing nodes, supporting future explain/justify verbs that walk the decision-axis-feature graph both directions.
  - Session traces show per-phase fanout structure, making workflow progress easier to read than today's monolithic council loop.

- **Performance:**
  - Per-iteration cost lower for incremental work (conditional Phase 2 dispatch); higher for first iteration of greenfield (scout + N decision-elaborators + M narrative-elaborators all fire). Net: greenfield converges in 1-3 iterations vs today's 5+ at budget; brownfield converges in 1-2.
  - Scout grounded research is bounded — most iterations carry forward prior scout output verbatim; fresh research fires only for genuinely new axes.
  - Decision-elaborator grounded research fires per-axis-being-decided, not per-iteration. Total grounded calls per session is roughly the number of distinct axes the project has, not iterations × axes.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. `RawFeatureProposal.Decisions` and `RawStrategyProposal.Decisions` change type; old council prompts on disk are overwritten by `update --reset`. Existing persisted decisions in `.borg/spec/decisions/` continue to load — their `Axes` and `SurfacedBy` fields stay empty until a future refine pass populates them via re-scouting. The integrity validator handles empty-Axes decisions as legacy without complaint. Existing persisted features/strategies with no `Decisions []string` field continue to load as legacy; new authoring goes through the schema-enforced shape.

**Reversal criteria.** Revert if:

- (a) the scout systematically misses critical axes — surfaces as critic findings repeatedly flagging "missing axis X" across iterations. Indicates the scout's domain understanding isn't strong enough; consider per-domain scout specialization or moving to per-axis grounded research at every iteration rather than amortized.
- (b) decision-elaborator grounded research produces unreliable citations (web kind citations that don't actually back the claim). Surfaces as critic findings or human review flagging hallucinated evidence. Mitigation: tighten the citation discipline in the prompt, mirroring `justify_researcher.md`'s literal-sentinel pattern.
- (c) conditional Phase 2 dispatch creates incorrect "affected" sets that miss real downstream effects — surfaces as features/strategies whose narrative becomes inconsistent with current decisions. Mitigation: expand the affected-set computation; worst case, fall back to dispatching all narrative-elaborators every iteration.
- (d) the scout-as-judge fails to converge in 5 iterations on a project that should converge — indicates the convergence criterion is wrong or scout is missing axes the critics keep surfacing. Reversal would re-introduce a separate convergence gate role.
- (e) the `minItems=1` constraint on `Feature.Decisions` / `Strategy.Decisions` creates legitimate authoring deadlocks (features that the scout determines have no decision dependency but the schema rejects). Mitigation: loosen the constraint to `minItems=0` and rely on the integrity validator + critic findings to surface ungrounded features instead.
- (f) `locutus import` unified through the workflow becomes too slow for the "single feature admission" UX — surfaces as users complaining that `locutus import dashboard.md` takes minutes when today's import takes seconds. Mitigation: short-circuit the scout when the imported content surfaces no new axes (skip Phase 1 entirely); only convergence-cost when decisions are actually needed.

**Reference.** Refines [DJ-068](#dj-068-manifeststate-separation--kubernetes-inspired-reconciliation-model) by clarifying the spec graph topology (decisions are upstream of features/strategies; the chain is linear). Replaces [DJ-105](#dj-105-elaborator-decisions-is-api-layer-required-not-prompt-layer-required) by changing the inline-decisions schema from `[]InlineDecisionProposal` to `[]string` references and retiring the "Defer architectural commitment" escape pattern. Re-scopes [DJ-123](#dj-123-in-flight-spec-search-for-council-agents-extends-dj-094--dj-116-to-mid-council-state) — the in-flight search infrastructure remains as defense-in-depth in Phase 2 narrative elaborators but is no longer the convergence mechanism. Builds on [DJ-122](#dj-122-graph-mutation-workflow-executor-with-spawner-nodes-supersedes-dj-112-on-control-flow-topology)'s graph-mutation executor; uses the spawner-node pattern for both Phase 1 (per-axis decision dispatch) and Phase 2 (per-affected-node narrative dispatch). Honors [DJ-085](#dj-085-decisions-denormalize-their-justification-session-transcripts-are-debug-only) (decisions denormalize their justification on the persisted node) and extends it to alternatives. Unifies the `locutus import` admission flow into the same workflow, retiring its separate triage logic. Motivated by the DJ-123 winplan re-run trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260515/0006/29-5135bd/).

## DJ-125: In-Flight Manifest + Enriched Concern Model (Refines DJ-094 / DJ-123 In-Flight Surface; Closes DJ-124's Concern-Disposition Gap)

**Status:** proposed

**Context.** DJ-124's scout-driven convergence loop exposed two coupled limitations in the council's state model that the incremental architecture from DJ-098 → DJ-122 → DJ-124 assumed away:

1. **`state.Concerns` is append-only.** `mergeCriticIssues` appends to the slice; nothing ever clears it. Under DJ-122 the gate was the convergence judge and didn't gate on `len(Concerns)==0` — it judged the assembled proposal directly. DJ-124 made the scout the judge and the rule became `axes_open == [] AND len(Concerns) == 0`. With concerns accumulating monotonically, the "no concerns" gate never holds, and the loop never converges even when the council is making real progress.

2. **Projection-by-blob.** `projectChallenge`, `projectScout`, `projectReconcile`, and the per-fanout projections dump the entire `state.RawProposal` or `state.ProposedSpec` into every agent's prompt. The first winplan re-run with DJ-124 failed because `compactContext`'s 8K cap truncated the critic's view to ~17% of the assembled decisions, producing spurious "missing X" findings against decisions that existed past the cliff. The cap was bumped to 200K chars as an immediate unblock (commit `be883a0`); that's a tactical fix on an architectural problem. As spec graphs grow past 200K chars on real-world projects (the goal of dogfooding Locutus), the same regression returns at the new cap.

The deeper structural inconsistency: the persisted spec graph already uses a manifest-detail RAG pattern via `spec_list_manifest` / `spec_get` / `spec_search` (DJ-094, DJ-116, DJ-117), and the in-flight `spec_search` was redirected to council-time state by DJ-123. But `spec_list_manifest` and `spec_get` still target only the on-disk spec, and the projections still blob-dump in-flight state. Agents have RAG tools available to them but the projections pre-load everything anyway. The primary axis stays blob-first with search as a supplementary tool; the architecturally honest pattern is manifest-first with detail-fetch on demand, the way the on-disk graph already works.

The second winplan re-run with the truncation fix in place (trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/)) made the failure modes legible:

- Iter-3 critics produced 6 substantive findings — cross-decision contradictions, factual errors, hallucinated citations, financial incoherence, integration gaps. These are real issues a critic loop should surface.
- Iter-3's `convergence_failed` event listed 30+ "unresolved concerns" — most of which were iter-0 / iter-1 spurious "missing X" findings that no longer reflected the current proposal. The scout correctly stopped re-surfacing those axes in `axes_open` (it saw the decisions in `state.RawProposal`), but the old concerns persisted in `state.Concerns` and blocked `Converged: true`.

**Why this surfaced now.** DJ-124 is the first architecture to make the scout responsible for convergence judgment. The append-only Concerns model was inherited from DJ-122 where it served as a per-iteration record consumed by the revise step — that step re-processed concerns each iteration and didn't gate on their absence. DJ-124 changed the consumer's contract without changing the producer's: `mergeCriticIssues` still appends, but now the scout reads cumulatively-accumulated state. The bug is a coupling break the workflow rewrite didn't notice.

The projection-blob issue surfaced concurrently. DJ-124's scout-driven flow accumulates decisions monotonically across iterations (where DJ-122's flow re-assembled the proposal per iteration), so the assembled proposal grows much faster, exceeding any fixed truncation cap within 2-3 iterations on a real project.

**Decision.** Two coupled changes shipped together as DJ-125:

1. **Promote concerns to a first-class state model.** `Concern` gains:
    - `IterationRaised int` — when the critic first surfaced this finding. Lets the scout reason about staleness.
    - `Status` enum (`open`, `addressed`, `stale`, `wontfix`) — the scout's grade of whether the concern still blocks convergence given the current proposal.
    - `RelatedDecisionIDs []string` — decision IDs the concern references. Parsed mechanically from the text via `idRefRegex`; optionally surfaced by the critic in a structured `referenced_decisions` field. Powers DJ-126's decision-revision dispatch.
    - `RelatedAxisIDs []string` — axis IDs the concern references. Powers the scout's "is this axis still open?" judgment.

    State transitions: created `open` by `mergeCriticIssues`; transitioned to `stale` mechanically when a related axis is settled or a related decision exists; transitioned to `addressed` by the scout when judgment is required (contradictions, factual claims); never deleted (durable for forensics).

2. **In-flight manifest as the primary projection surface.** Add `InFlightManifest` — a structured view over `state.RawProposal` carrying:
    - `axes` — every axis the loop has seen, with state (`settled-by-dec-X` / `open` / `under-evaluation`) and surfacing-node references.
    - `decisions` — id, title, summary, state (`settled-prior` / `settled-this-iter` / `flagged`), axes covered, surfaced-by references.
    - `features` / `strategies` — id, title, summary, decision-reference set.
    - `concerns` — id, iteration_raised, status, related decisions and axes (cross-links into the rest of the manifest).

    `spec_list_manifest` and `spec_get` are redirected to the in-flight manifest during council runs (mirror DJ-123's `spec_search` pattern via the existing `SwappableSpecSearch` adapter). Projections replace blob dumps with the manifest plus the agent's specific working item in full (the axis being decided, the feature being elaborated, the critic's lens). Each agent sees the structural shape of the graph plus its own focus; details are fetched on demand via the tools.

3. **Mechanical concern-disposition pre-pass.** Before the scout sees concerns, a Go pre-pass walks each concern's `RelatedAxisIDs` / `RelatedDecisionIDs` against the manifest. Concerns whose named axis is `settled` or whose named decision exists in the graph are marked `Status: stale`. The mechanical pass handles ~80% of "missing X" false positives automatically; the scout grades the remaining 20% (judgment calls about contradictions, factual claims, integration gaps).

4. **Scout grades remaining open concerns.** `ScoutBrief` extends with `concern_dispositions: [{concern_id, disposition, justification}]`. `mergeScoutBrief` applies the dispositions onto `state.Concerns`. The scout's convergence rule becomes:

    `Converged: true ⟺ axes_open == [] AND no concerns with Status == open`

    `addressed`, `stale`, and `wontfix` concerns don't gate convergence but stay in the manifest for `locutus history` / `locutus explain` and the eventual operator surfaces. The disposition's `justification` field gives the scout authority to explain its grading — load-bearing for `wontfix` ("real concern but acceptable tradeoff given goals X") so the user can audit the scout's judgment.

**Alternatives considered.**

- **Just bump `defaultMaxChars` higher.** The 8K → 200K bump worked tactically for the second winplan run. Specs grow to millions of characters on real projects (the goal of Locutus); bumping the cap perpetually is whack-a-mole; manifests are the architecturally honest answer. The bump becomes obsolete once DJ-125 ships.

- **Clear `state.Concerns` at iteration boundaries.** The "Fix 1" candidate from chat (2026-05-18). Loses history (no way to ask "when did this issue first surface?"), forces critics to re-discover every issue every iteration, and leaves no path to "concern was `wontfix`-marked because it's a real-but-acceptable tradeoff." Rejected as a tactical patch on a structural problem; disposition is the right primitive.

- **Per-iteration concern stamps without disposition.** Stamp each concern with `IterationRaised`; scout's convergence rule reads only current-iteration concerns. Simpler than full disposition tracking but loses the explicit `wontfix` / `addressed` distinction the scout needs to communicate. The single-bit "current-iter or not" doesn't carry enough state for the architecture DJ-126 builds on.

- **ACP-style coding-agent concern review.** Spawn an ACP reviewer agent that grades each concern. Rejected per the "don't unify ACP with structured-output council" decision (chat 2026-05-18) — grading is structured output; the scout already produces structured output; making the scout the grader keeps the role unified and the schema enforcement intact.

- **Manifest-only without enriched concerns.** Promote the proposal to a manifest but leave concerns as-is. Doesn't solve the stale-concerns blocker; convergence still fails. The two changes are coupled — the manifest provides the substrate the concern grading queries against, and the concern enrichment is what makes the convergence rule reliable.

- **Use the LLM clusterer (DJ-098) for staleness grading.** The existing `spec_finding_clusterer` agent could be repurposed to grade concerns. Rejected: clusterer is for grouping unrelated findings into topical clusters before revise dispatch; staleness grading is a different shape of judgment (per-concern, against current state, with structured disposition output). Two different agents serving two different needs.

**Consequences.**

- **Code:**
    - `internal/agent/state.go` — `Concern` gains `IterationRaised`, `Status`, `RelatedDecisionIDs`, `RelatedAxisIDs`; `ConcernStatus` enum.
    - `internal/agent/manifest.go` (new) — `InFlightManifest` data type + `BuildManifest(state)` builder. Includes axes, decisions, features, strategies, concerns with cross-references and per-item state markers.
    - `internal/search/inflight.go` — extend the in-flight Bluge index path with manifest-shaped accessors used by `spec_list_manifest` and `spec_get`.
    - `internal/agent/spec_tools.go` — tool backing stores swap from "on-disk only" to "in-flight during council; on-disk otherwise" (same pattern DJ-123 used for `spec_search`).
    - `internal/agent/projection.go` and `internal/agent/workflow_spec_generation_dj124.go` — `projectChallenge`, `projectScout`, `projectReconcile`, `projectOpenAxis`, `projectAffectedNode` all replace blob dumps with `RenderManifest(state)` + the per-agent working item.
    - `internal/agent/workflow_spec_generation_dj124.go` — `mergeCriticIssues` extracts `RelatedDecisionIDs` / `RelatedAxisIDs` mechanically from finding text (regex match against the current manifest); a new `mechanicalDisposeConcerns(state)` pass runs after the critic merge; `mergeScoutBrief` applies the scout's `concern_dispositions` array.
    - `internal/agent/specgen.go` — `ScoutBrief` schema gains `concern_dispositions []ConcernDisposition` with `jsonschema` tags per CLAUDE.md (enum for `disposition`, description on `justification`).
    - `internal/scaffold/agents/spec_scout.md` — section added describing concern grading. Walk `docs/agent-conventions.md` end-to-end before drafting per `feedback_agent_conventions_checklist_first`.
    - `internal/agent/compact.go` — `defaultMaxChars` revisited; manifest-rendered projections are bounded by structural shape rather than character count, so the cap can drop back closer to a sane working size or stay at 200K as defense-in-depth.
    - Tests across `internal/agent/`, `internal/search/`, `internal/scaffold/`, `cmd/` covering manifest building, concern enrichment, mechanical disposition, scout grading, manifest-based projections. Existing workflow tests update to assert manifest-rendered prompts and concern-status reasoning.

- **Documentation:**
    - CLAUDE.md gets a short note on the manifest-detail pattern for in-flight state, paralleling the existing persisted-graph paragraph.

- **User-visible:**
    - Session traces carry richer concern history — operators see when a concern was first raised, its current status, what (if anything) addressed it.
    - The `convergence_failed` terminal's rationale becomes legible: instead of dumping 30+ concerns from across all iterations, it shows only `Status: open` concerns at exhaustion with iteration-raised metadata; `stale` / `addressed` / `wontfix` concerns appear in an appendix for forensic context.
    - `locutus history` and the future `locutus explain` benefit transparently — they walk concern→decision references to show "this decision was the response to that critic finding."
    - Per-call prompts shrink substantially (manifest is ~10% the size of the blob proposal at iter-3 scale), so wall-clock per iteration drops.

- **Performance:**
    - Manifest build per merge: O(N) over decision/feature/strategy count. Sub-millisecond for ~50-node graphs; scales linearly to thousands.
    - Mechanical disposition pre-pass: O(M × K) where M is concern count and K is per-concern regex matches against axis/decision IDs. Negligible.
    - Prompt token counts drop substantially for critic / reconciler / per-fanout projections — the manifest is a fraction of the full proposal JSON.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. `Concern` gains fields; old persisted state loads with zero-value defaults (`IterationRaised: 0`, `Status: open`, empty related-id slices) and the mechanical disposition pass handles them on first re-iteration. The manifest infrastructure is council-internal; the persisted spec graph's schema doesn't change.

**Reversal criteria.** Revert if:

- (a) the mechanical disposition pre-pass marks concerns `stale` too aggressively, causing the loop to converge with unresolved real issues. Surfaces as users complaining `refine goals` declared convergence on a proposal with obvious gaps. Mitigation: tighten the regex matching (require exact id match, not substring); fall back to "always require scout grading" if matching is too loose.
- (b) the scout's concern grading is unreliable — marks `open` concerns `addressed` without real justification, or `addressed` concerns `open` (loop never converges). Surfaces as either premature convergence or budget exhaustion despite manifest correctness. Mitigation: tighten the scout's prompt around grading discipline; add a critic-style "did the scout grade correctly?" agent in a follow-up.
- (c) the manifest-rendered projection loses information the agents need. Surfaces as critic findings about subtle issues that the manifest's per-item summary doesn't capture but the full proposal would. Mitigation: extend the manifest's per-item summary fields; in the worst case, agents can `spec_get(id)` to fetch full detail — that's the RAG escape valve.

**Reference.** Extends [DJ-094](#dj-094-spec-lookup-tools-spec_list_manifest--spec_get-are-agent-facing-mcp-tools) by redirecting `spec_list_manifest` / `spec_get` to in-flight state during council runs. Extends [DJ-123](#dj-123-in-flight-spec-search-for-council-agents-extends-dj-094--dj-116-to-mid-council-state) by generalizing the in-flight-redirection pattern from search to the full RAG tool surface. Closes a structural gap in [DJ-124](#dj-124-spec-generation-re-architecture--decisions-before-narrative-scout-as-judge-convergence-unified-import-flow-refines-dj-068-spec-graph-topology-replaces-dj-105-inline-decisions-schema-re-scopes-dj-123-in-flight-search) (the scout's `len(Concerns)==0` convergence rule). Motivated by the second winplan re-run trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/) which exposed the stale-concerns failure mode and the projection-blob scaling limit. Powers [DJ-126](#dj-126-decision-re-elaboration-for-cross-decision-contradictions-extends-dj-124-with-existing-decision-revision-depends-on-dj-125-concern-model) by providing the `Concern.RelatedDecisionIDs` substrate the decision-revision dispatch reads.

## DJ-126: Decision Re-Elaboration for Cross-Decision Contradictions (Extends DJ-124 With Existing-Decision Revision; Depends on DJ-125 Concern Model)

**Status:** proposed

**Context.** DJ-124's workflow dispatches four kinds of work: scout (surfaces new axes and judges convergence), decision-elaborator (commits a new decision per axis in `axes_open`), narrative-elaborator (updates feature/strategy bodies referencing decisions), critics (produce findings). It does not dispatch a fifth kind that the second winplan re-run made indispensable: **re-elaborating an existing decision when the critics find it contradicts another decision, carries a factual error, or cites a hallucinated source.**

The second winplan trace ([`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/)) produced 6 substantive iter-3 critic findings:

1. `dec-datadog-observability-stack` adopts Datadog; `dec-aws-cloudwatch-logging` rejects Datadog as too expensive — cross-decision contradiction.
2. `dec-aurora-serverless-database-vendor` claims Aurora can scale to 0 ACU; actual minimum is 0.5 ACU — factual error.
3. `dec-150-dollar-off-cycle-ceiling` and `dec-tiered-seasonal-slo` cite GOALS.md excerpts that don't exist — hallucinated citations.
4. `dec-aws-ecs-fargate-deployment` (no EC2) contradicts `dec-fck-nat-egress` (commits to t4g.nano ARM EC2 instances) — cross-decision inconsistency.
5. Sum of decided baseline costs (Aurora storage + min ACU + NAT + Datadog) exceeds `dec-150-dollar-off-cycle-ceiling` — financial incoherence.
6. Missing axis: shared schema location between Next.js frontend and Go ingestion runtime.

Only finding #6 is something DJ-124's workflow can resolve: the scout surfaces it as `axes_open`, the decision-elaborator commits a new decision. The other five require modifying existing decisions. The workflow has no path for that. The scout's only available move is to surface a workaround axis ("observability-tool-coherence", "cost-runaway-protections", "peak-cost-ceiling") hoping the new decision papers over the contradiction, but the original contradictory decisions stay in the graph — observed in the iter-4 `axes_open` of the same trace, which fired three synthetic budget axes that never actually invalidated `dec-150-dollar-off-cycle-ceiling`'s commitment. Convergence never holds.

Under the pre-DJ-124 architecture (DJ-122), the revise step re-elaborated affected nodes; affected nodes were features and strategies with inline decisions; revising the node revised its decisions. DJ-124 separated decision-authoring from narrative-authoring; the loss of inline-decision-revision was unintentional — the workflow rewrite focused on "decisions first, narrative second" without preserving the "decisions can also be revised when wrong" path.

**Why this surfaced now.** DJ-124's flagship validation case (winplan re-run) is the first time critics flagged real contradictions between settled decisions on a non-trivial spec graph. The smoke-run-during-DJ-124-development used `MockExecutor` scripts that never produced contradictions, so the gap didn't appear in tests. [DJ-125](#dj-125-in-flight-manifest--enriched-concern-model-refines-dj-094--dj-123-in-flight-surface-closes-dj-124s-concern-disposition-gap) closes the projection and concern-tracking gaps that obscured this issue; with DJ-125 in place, the residual failure is the missing decision-revision dispatch. Depending on DJ-125's `Concern.RelatedDecisionIDs` substrate makes the dispatch tractable — without it, the workflow has no structural way to know which decisions the critic wants revised.

**Decision.** Add a `revise-decisions` step to the DJ-124 convergence loop, dispatched per concern with `Status: open` AND `len(RelatedDecisionIDs) > 0`. For each such concern, dispatch one `spec_decision_elaborator` call in **revise mode** with input:

- The full prior `RawDecisionProposal` for each related decision.
- The critic finding text and severity.
- The relevant slice of the in-flight manifest (the contradicting decisions, the cited GOALS.md, the surrounding strategy bodies).

Output: a corrected `RawDecisionProposal` for the same axis ID(s). `mergeDecisions` matches by axis-ID + decision-ID and replaces the entry in `state.RawProposal.Decisions` (rather than appending a new one).

The new iteration template:

```
scout → decisions(per axes_open fanout)
      → narrative(per affected_node fanout)
      → revise-decisions(per open concern with related decisions, fanout) ← NEW
      → critique
      → scout (next iter)
```

Three design commitments:

1. **`spec_decision_elaborator` handles both first-author and revise modes.** The agent's prompt receives a "Prior decision" block when in revise mode; absent in first-author mode. The agent's `RawDecisionProposal` output schema is unchanged — it always emits a complete decision. The mode-switch is at the projection layer (per-fanout-item) not the agent surface. Same agent, two prompts that share a body and diverge in the "context to react to" section.

2. **Replace-by-axis-ID, not append.** When `mergeDecisions` sees a `RawDecisionProposal` whose `Axes` intersect with an existing decision's `Axes`, it REPLACES rather than appending. Preserves the "one decision per axis at a time" invariant. The old decision's ID is preserved; rationale, alternatives, citations are all overwritten by the revision. History of revisions is captured in DJ-103 events (one event per revision).

3. **Cycle detection adapts.** Today's `DecidedAxesByIter` map flags re-opened axes as cycles. After DJ-126, an axis can be legitimately "re-decided" (the revise path), but the dispatch is concern-driven (a critic flagged it) not scout-driven (no axis appears in `axes_open` twice). Cycle detection stays the safety net for the scout-side path; the revise path is exempt. A new per-axis revision-count cap (default 3) prevents the alternate failure mode of "revise dec-X → critic flags revised dec-X → revise again → ..." infinite loops.

**Alternatives considered.**

- **Force user intervention.** Surface the contradiction to the user; require explicit `locutus refine dec-X --against "the critic finding"`. Defensible for a non-autonomous tool; defeats the whole point of an autonomous spec council that converges within budget. Held only as the fallback verb behind the autonomous path.

- **Scout surfaces a "resolution axis" for cross-decision contradictions.** Instead of revising, the scout emits a synthetic axis like "observability-tool-coherence" with `surfaced_by: [dec-datadog, dec-cloudwatch]`. The decision-elaborator picks one. This is exactly what the iter-3 scout actually did in the failing winplan run — emitted `peak-cost-ceiling` and `cost-runaway-protections` to paper over the financial incoherence. Two structural problems: (a) the original contradictory decisions stay in the graph (no way to remove `dec-Y` when `dec-X` resolves the axis); (b) the synthetic axis is rarely well-formed — the model invents an ill-defined axis rather than admitting the contradiction directly. Rejected.

- **Reconciler-level contradiction supersede actions.** Today's `ReconciliationVerdict` carries a no-op `actions` array (Stage A simplified the verdict). Extending it to allow "supersede dec-X with dec-Y" actions would let the reconciler resolve contradictions mechanically. Two problems: (a) the reconciler is sequential after the elaborators; under DJ-124's flow it can't introspect "which decision is right" — that's the decision-elaborator's job; (b) the reconciler is mid-retirement (DJ-125 makes its role mostly empty); building new capability into a step that's going away is bad scope. Rejected.

- **Re-elaborate every decision every iteration.** Naive: have the workflow re-fire `spec_decision_elaborator` for every existing decision every iteration. Token cost balloons (O(N × iterations) decisions of grounded research per session); most iterations don't need most decisions revised. Rejected as obvious over-cost.

- **Critic-driven dispatch with a separate `decision_reviser` agent.** Have the critic emit `decision_revision_requests[]` alongside `issues[]`, dispatching a new agent type to handle the revision. Adds a new agent surface and a new prompt to maintain; the decision-elaborator's revise mode is the simpler shape (one agent, two contexts). Rejected on surface-area grounds.

**Consequences.**

- **Code:**
    - `internal/agent/workflow_spec_generation_dj124.go` — new step in the iteration template: `revise-decisions` between `narrative` and `critique`. Fanout closure walks `state.Concerns` with `Status: open` and `len(RelatedDecisionIDs) > 0`. Conditional: skip when no such concerns exist.
    - `internal/agent/workflow_spec_generation_dj124.go` — `mergeDecisions` extended to detect "replacement vs append" by intersecting `RawDecisionProposal.Axes` with existing decisions' `Axes`. Replacement preserves the existing ID; the prior decision body is overwritten with the revised one.
    - New per-fanout projection `projectReviseDecision(snap)` that renders the manifest + the prior decision (full body) + the critic finding text + severity.
    - `internal/scaffold/agents/spec_decision_elaborator.md` — gains a "Revise mode" section: when the user message includes a "Prior decision" block AND a "Critic finding to address" block, emit a corrected `RawDecisionProposal` for the same axis (preserve `axes` verbatim). Walk `docs/agent-conventions.md` before drafting per the memory checklist; the literal-sentinel pattern (from `justify_researcher.md`) applies when grounded research disagrees with the prior decision's claim.
    - Cycle detection in `scoutSpawnFor` adjusts: only count axis appearances in `axes_open` toward the cycle threshold; the revise-decisions path doesn't count. A new per-axis revision-count cap (env var `LOCUTUS_DECISION_REVISION_CAP`, default 3) prevents revise-loop infinite recursion.
    - DJ-103 history events: `decision_revised` event kind, recording prior body + revised body + driving concern text. Surfaces in `locutus history`.
    - Tests: `TestReviseDecisionsDispatchPerOpenConcern`, `TestMergeDecisionsReplacesByAxisID`, `TestReviseDecisionsPreservesIDs`, `TestRevisionCapTerminatesRevolvingDoor`, end-to-end smoke test where the loop converges after a forced critic contradiction.

- **User-visible:**
    - Session traces show `spec_decision_elaborator-<axis-id>:revise` fanout items distinct from first-author calls.
    - `locutus history` records both the original decision and each revision (via DJ-103 events); the future `locutus explain` can show "this decision was revised in iter-N because of finding-X" with the full lineage.
    - Convergence on real projects with contradictions becomes achievable within the default 5-iteration budget.

- **Performance:**
    - Per-revision call: same wall-clock as a first-author decision-elaborator call (~30-90s with grounded research). Adds 1-3 calls per iteration on average (one per `open` concern with related decisions).
    - Net session latency: roughly +15-30% per session on contradiction-heavy projects; -50%+ vs current budget-exhaustion behavior because the loop actually terminates.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. Existing sessions terminated under `convergence_failed` before DJ-126 had no `revise-decisions` step; new sessions get the step. No persisted-state schema change beyond DJ-125's `Concern.RelatedDecisionIDs`.

**Reversal criteria.** Revert if:

- (a) the decision-elaborator's revise mode produces unstable decisions — each revision contradicts the prior, the loop never converges. Surfaces as the same axis being revised every iteration through budget exhaustion. Mitigation: tighten the revise-mode prompt to require the elaborator to acknowledge what the prior decision committed AND why it's wrong, not emit a fresh take. The revision-count cap is the safety net.
- (b) the revise dispatch fires too liberally — every iteration produces 10+ revisions, churning the graph. Mitigation: rank concerns by severity; revise only `severity: high`; lower-severity concerns get the scout's `wontfix` disposition.
- (c) revisions cycle through different decision IDs for the same axis ad infinitum (revise `dec-X` → axis appears in `axes_open` via scout → new decision `dec-Y` → critic flags `dec-Y` → revise `dec-Y` → ...). The per-axis revision-count cap catches this; if the cap fires frequently in real usage, the cap's threshold needs lowering or the dispatch logic needs to distinguish "revision of same decision id" from "new decision for same axis."

**Reference.** Extends [DJ-124](#dj-124-spec-generation-re-architecture--decisions-before-narrative-scout-as-judge-convergence-unified-import-flow-refines-dj-068-spec-graph-topology-replaces-dj-105-inline-decisions-schema-re-scopes-dj-123-in-flight-search) with the missing decision-revision dispatch. Depends on [DJ-125](#dj-125-in-flight-manifest--enriched-concern-model-refines-dj-094--dj-123-in-flight-surface-closes-dj-124s-concern-disposition-gap) — specifically `Concern.RelatedDecisionIDs` is the dispatch key. Restores the decision-revision capability that pre-DJ-124's revise step provided implicitly (by revising features that carried inline decisions); makes the revision path explicit at the decisions layer where DJ-124 located decision-authoring. Honors [DJ-103](#dj-103-history-events-are-the-narrative-source-recorded-events-plus-llm-summary) by recording each revision as a structured event. Motivated by the second winplan re-run trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/) which exposed the structural gap.

## DJ-127: Spec Mutation Tools as MCP Write Surface; ACP-Driven Decision Elaboration (Aligns Council Transport With Subscription-Based AI Funding Model)

**Status:** proposed

**Context.** Locutus's council is API-first by construction: direct-SDK adapters ([DJ-099](#dj-099-direct-sdk-llm-adapters-per-provider-supersede-genkit)) issue per-call requests against Anthropic / Google / OpenAI APIs, billed per-token. A non-trivial `locutus refine goals` session today costs $0.50–$30 depending on tier (cheapest Gemini Flash to premium Claude Opus). The visible cost matters less than the *invisible* friction it creates: every invocation is a billable event the user mentally accounts for. The deliberate user posture documented in `feedback_no_back_compat_until_self_hosting` and the `user_profile.md` memory — "Claude Max subscriber, avoids API token costs" — means the effective architecture constraint today is "use the cheapest free-tier Gemini path," which is exactly what shows up in the winplan validation traces (`gemini-3-flash-preview` throughout).

The developer ecosystem has shifted to subscription-based AI tooling. Most users running a serious AI workflow already pay for Claude Max ($200/month, Claude Code unlimited-ish), Codex Pro/Plus, or Gemini Advanced. These subscriptions cover *unlimited* (within reasonable quotas) coding-agent sessions. The marginal cost of one more `claude-code` invocation is zero up to the daily/weekly cap.

Locutus today fights this funding model. Direct-SDK adapters require explicit API keys, bill per call, and force users into a "is this refine worth $X?" decision before every invocation. The architecture is correct for an era when API access was the only way to drive LLMs; it's structurally misaligned with how developers actually pay for AI tooling today.

The transport infrastructure to bridge this gap already exists. [DJ-119](#dj-119-adopt-the-agent-client-protocol-acp-as-the-coding-agent-transport-supersedes-dj-006--dj-085s-shell-wrapper-claims) adopted ACP as the uniform transport for coding agents on the implementation side. [DJ-125](#dj-125-in-flight-manifest--enriched-concern-model-refines-dj-094--dj-123-in-flight-surface-closes-dj-124s-concern-disposition-gap) introduces the `InFlightManifest` typed-state surface plus the redirection of `spec_list_manifest` / `spec_get` / `spec_search` to in-flight state during council runs — the read-side of locutus's MCP server already serves both internal council agents and (in principle) any external MCP client. [DJ-126](#dj-126-decision-re-elaboration-for-cross-decision-contradictions-extends-dj-124-with-existing-decision-revision-depends-on-dj-125-concern-model) validates that the council architecture is stable enough to refactor without compounding debugging surface.

The honest framing of the tradeoff (settled in chat 2026-05-18 across three rounds of analysis):

- **Quality:** marginal. Coding-agent tooling could plausibly catch the occasional factual error (e.g., the Aurora Serverless v2 minimum-ACU mistake from the second winplan trace) that direct-SDK grounded search missed. But the dominant failure modes are structural (cross-decision contradictions, stale concerns) and not solved by richer per-agent tooling.
- **Convergence speed:** worse per agent invocation. ACP coding-agent sessions take 2-10 minutes vs direct-SDK's 30-90 seconds. The convergence count itself doesn't improve — that's a DJ-125 / DJ-126 concern.
- **Cost / DX:** transformative for subscription users. Per-session billing decisions evaporate; better models (Opus) become economically viable at flat subscription cost; the architecture stops fighting the user's funding model.

The cost/DX dimension is the load-bearing argument. Quality and speed are not.

**Why this surfaced now.** Three threads converged at the same time:

1. The second winplan validation run after DJ-124 (trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/)) made the council's structural failure modes legible — DJ-125 and DJ-126 are the immediate fixes. With those underway, the question shifts from "does the council architecture work?" to "is the council architecture aligned with how users want to invoke it?"
2. The MCP write-tools idea (raised in chat 2026-05-18) reframed the schema-enforcement objection. The argument "ACP loses structured-output enforcement" was based on assuming the contract lives at the *output* layer; moving it to the *tool-input* layer preserves the same `jsonschema`-tagged invariants that DJ-105 / DJ-124 rely on.
3. The DX dimension surfaced explicitly as a counterweight to the convergence-speed concern (chat 2026-05-18, final round). Subscription users price latency vs cost very differently from API-billed users; the architecture should align with the user's funding model, not just the engineering-optimal model.

**Decision.** Three coupled changes shipped together as DJ-127:

1. **Locutus's MCP server gains write tools** for spec mutation:
    - `spec_propose_decision(axis_id, title, summary, rationale, alternatives, citations, ...)` — first-author a decision for an open axis.
    - `spec_revise_decision(decision_id, ...)` — revise an existing decision (DJ-126's dispatch target).
    - `spec_propose_new_node(kind, id, title, summary, decisions[])` — emit a new feature or strategy from scout-driven analysis.
    - `spec_update_node_narrative(id, description|body, acceptance_criteria[])` — narrative-elaborator's commit path.
    - `spec_raise_concern(text, severity, kind, related_decision_ids[], related_axis_ids[])` — critic's commit path.
    - `spec_grade_concern(concern_id, disposition, justification)` — scout's concern-grading commit path (DJ-125).
    - `spec_commit_session()` / `spec_rollback_session()` — explicit transaction primitives the session staging area uses for atomicity.

    Each tool's input schema mirrors the equivalent `Raw*` / structured-output schema today, with the same `jsonschema` tags, descriptions, enums, and `minItems` constraints. The MCP boundary enforces the schema; agents that send malformed tool calls get rejected and retried.

2. **Council's agent transport becomes pluggable per role.** Each agent's `.md` frontmatter gains a `transport` field:
    - `transport: direct_sdk` — today's path; one structured-output call against Anthropic / Google / OpenAI. Default for backwards compatibility; used for fast/deterministic roles (scout judgment, critics, narrative-elaborator, reconciler).
    - `transport: acp` — spawns a coding-agent session via ACP. The session is given the agent's prompt + scoped MCP tools. Tool calls accumulate in a session staging area; the session ends; the staging area is consolidated into typed state by the merge function. Used for research-heavy roles (decision-elaborator first-author + revise; possibly a future deep-critic).

    The workflow executor (DJ-122 graph-mutation) is unchanged structurally. Per-step Budget, conditional dispatch, snapshot isolation, fanout, spawner callbacks all stay. Only the dispatch primitive inside `executeAgent` routes between direct-SDK and ACP based on the agent's declared transport.

3. **Three composable recursion guards.** Necessary regardless of transport choice but newly load-bearing under ACP integration:
    - **Filesystem lock on `.borg/`.** First locutus process to enter a council acquires an exclusive flock. Nested invocations (whether shell-driven from inside a coding agent or from a sibling user terminal) fail fast with a clear error. Useful today even without ACP — concurrent `locutus refine` + `locutus import` has undefined behavior in current code.
    - **Session-ID env propagation.** Parent locutus process exports `LOCUTUS_SESSION_ID=<sid>`. Child processes (spawned via the coding agent's shell tool) detect the env and refuse to start a council. Catches the deadlock case where the lock would block a child process descended from the holder.
    - **Per-session capability scoping at the MCP boundary.** When the council spawns an ACP session for "decide axis auth-provider," the MCP tool surface exposed to that session is filtered: read tools fully available, `spec_propose_decision` bound to `axis_id=auth-provider` only, other write tools hidden, shell-level escape tools (filesystem traversal outside the project, arbitrary shell commands invoking locutus subcommands) denied. Per-session capability scoping is the right primitive regardless — agent sessions should always have minimum-necessary capability.

**Resolved design questions** (settled in chat 2026-05-18):

1. **DX is the load-bearing argument; quality and speed are secondary.** Quality improvement from ACP is marginal (one fewer factual error per session at best, maybe). Latency is strictly worse per agent invocation. The justification is the subscription-funding alignment, not architectural elegance and not better specs.

2. **Pure-ACP for cost coherence; hybrid for cost/latency tradeoff.** Under a subscription-only user model (Max + Codex covers everything), pure-ACP wins because every agent invocation is subscription-amortized. Under a mixed model (some API budget available, want some calls to be faster), hybrid wins because critics and narrative-elaborator burn ~$1 of API per session for a much faster wall-clock. The transport choice is per-agent frontmatter, so users can flip individual roles based on their funding model. Default ships hybrid: ACP for decision-elaborator (highest research value), direct-SDK for everything else. Power users override per-role.

3. **Tool granularity: one tool per commit-kind, not per field.** `spec_propose_decision` takes a full decision in one call rather than splitting into `spec_set_decision_title`, `spec_add_decision_alternative`, etc. Matches today's `RawDecisionProposal` shape; preserves the all-or-nothing commit semantics; keeps the tool surface small. Finer granularity considered and rejected (lots more tools to define; the agent has to track partial state across calls; atomicity becomes harder).

4. **Session staging area is the atomicity boundary.** Tool calls accumulate in a session-scoped staging area; `spec_commit_session()` consolidates into committed state; mid-session failures roll back. The session boundary maps 1:1 to a workflow-executor agent dispatch — every dispatch opens a session; every successful dispatch closes with `spec_commit_session`; failures roll back. Mid-session partial commits are not exposed to other agents.

5. **Write tools never trigger workflow re-entry.** Tool calls mutate the staging area and return. They do not dispatch other agents, do not trigger critic rounds, do not invoke the workflow executor. The workflow executor remains the sole orchestrator of dispatch; tools are pure data-shape mutations within their dispatch scope. This is the property the merge functions already have today; preserving it under tools-as-MCP-write is a discipline matter, not a new mechanism.

6. **`spec_decision_elaborator` agent is bilingual.** The same `.md` agent definition produces a direct-SDK call (when `transport: direct_sdk`) and an ACP session (when `transport: acp`). Prompt body is largely unchanged; the structured-output schema becomes the equivalent tool-call shape; the literal-sentinel pattern from `justify_researcher.md` carries over verbatim (sentinel text becomes a tool-call payload field). Same agent, two transports.

7. **Subscription quota management as a first-class concern.** A `--budget` flag exposes the user's intent: `--budget=$0` means "subscription-only; refuse to start if no ACP transport configured"; `--budget=$5` means "OK with up to $5 of API spend; fall through to direct-SDK if subscription quotas exhausted"; default is "subscription preferred, API fallback OK." Progress UI surfaces per-session subscription consumption when ACP transport is in use.

8. **Workflow executor stays load-bearing.** The DAG, spawners, merge functions, budgets, cycle detection, conditional dispatch — all unchanged. Only the dispatch primitive (`executeAgent`) gains a transport switch. "Workflow as a single coding-agent prompt" was considered briefly and rejected: structural guardrails (budget, cycle detection, conditional dispatch, terminal events) live in the workflow executor and don't reduce to prose for a coding agent to enforce. The executor is what makes the council inspectable, replayable, and bounded.

**Alternatives considered.**

- **Stay pure direct-SDK.** Today's architecture. Better latency, clear billing, established. Loses the DX alignment with subscription users; perpetuates the "is this refine worth $X?" friction. Considered as the conservative path; rejected on funding-model alignment grounds.

- **Pure ACP, no direct-SDK fallback.** Eliminates the transport choice — every agent runs as a coding-agent session. Simpler architecturally; consistent funding model. Rejected because some roles (fast critics, narrative-elaborator synthesis) have no good reason to pay the latency multiplier, and users without subscriptions get no fallback path. Hybrid as the default preserves both options.

- **External-MCP-only.** Locutus's council stays pure direct-SDK; write tools exist only for external consumers (third-party agents, plugins, custom Claude Code workflows). Rejected because the internal council is the dominant consumer and gets none of the DX benefit; also creates a maintenance dichotomy where internal agents and external clients use different commit paths.

- **Bring-your-own-key with API-key vault.** Side-step the subscription argument by letting users configure cheaper API keys (BYOK with cheaper tiers, OpenRouter routing, etc.). Marginal improvement on cost; doesn't address the per-call mental-overhead friction that's the real DX cost.

- **One write tool, structured-output-shaped payload.** Collapse all write tools into a single `spec_commit(payload)` tool that takes any structured commit. Loses the per-commit-kind type safety; the agent has to pick the right shape inside one tool. Rejected on schema-enforcement clarity.

- **Coding agent as the supervisor; workflow executor retired.** Make the entire council a single Claude Code session driving everything end-to-end. Loses the executor's structural guardrails (budget, cycle detection, terminal events, deterministic spawn). Coding agents are bad at structural guardrails by training; failure mode shifts from "convergence_failed with 7 open axes" to "session ran 45 minutes, produced inconsistent spec." Rejected definitively.

- **Per-tool capability scoping at the call layer instead of the session layer.** Every tool call checks "is this caller authorized for this axis?" on each invocation. Equivalent enforcement but higher per-call overhead and harder to reason about. Session-level scoping (bind capabilities at session-spawn time, MCP boundary enforces) is the cleaner shape.

**Consequences.**

- **Code:**
    - `internal/mcp/` — new write-tool implementations: `spec_propose_decision`, `spec_revise_decision`, `spec_propose_new_node`, `spec_update_node_narrative`, `spec_raise_concern`, `spec_grade_concern`, `spec_commit_session`, `spec_rollback_session`. Each carries a jsonschema-tagged input schema mirroring its structured-output sibling.
    - `internal/mcp/staging.go` (new) — session-scoped staging area data type; consolidation logic that maps staged tool calls into typed state mutations.
    - `internal/mcp/capability.go` (new) — per-session capability scoping; the MCP server enforces tool visibility and tool-input constraints based on the session's bound scope.
    - `internal/dispatch/locking.go` (new) — filesystem lock on `.borg/.locutus.lock` + session-ID env propagation. Acquired in `cmd/` entry points; released in deferred cleanup.
    - `internal/agent/workflow.go` — `executeAgent` gains transport routing. Reads agent frontmatter's `transport` field; dispatches to direct-SDK adapter or ACP session spawner.
    - `internal/dispatch/acp/` — extended to handle council-side spawns (today's ACP integration is implementation-side; add the supervisor-side equivalent for spawning ACP sessions on behalf of a workflow step).
    - `internal/scaffold/agents/spec_decision_elaborator.md` — frontmatter gains `transport: acp` by default; prompt body updated to express the agent's work as tool calls rather than structured output. Walk `docs/agent-conventions.md` before drafting; the literal-sentinel pattern translates from "set excerpt to literally: <sentinel>" to "call `spec_propose_decision` with citation excerpt set to literally: <sentinel>."
    - `cmd/` — new `--budget` flag on `refine`, `import`, `assimilate`; budget plumbed through to dispatch transport-selection logic.
    - Tests across `internal/mcp/`, `internal/agent/`, `internal/dispatch/`, `cmd/` covering write-tool input validation, session staging atomicity, capability scoping (denied calls produce structured rejections), filesystem locking, env-detection nesting refusal, mixed-transport dispatch.

- **Documentation:**
    - CLAUDE.md gains a section on the transport-choice convention: which roles default to which transport and why. The funding-model-alignment argument lives there too.
    - User-facing docs for `--budget` flag and subscription-quota management.

- **User-visible:**
    - `locutus refine goals` on a Claude Max + Codex subscription footprint produces specs at $0 marginal cost (within subscription quotas).
    - Per-session subscription consumption surfaced in the progress UI when ACP transport is active.
    - Quota-exhaustion errors fall through to API-backed direct-SDK transport (subject to `--budget`) or fail with an actionable message naming the exhausted subscription.
    - External MCP clients (Claude Code in user's own workflow, custom tools, plugins) can use locutus's write tools for spec management outside the council. Locutus becomes a canonical MCP-accessible spec management surface, not just a CLI.
    - Per-session latency on roles that flipped to ACP: 3-10× slower wall-clock. Critics, scout, narrative-elaborator stay direct-SDK by default so per-iteration wall-clock stays in the same ballpark; only decision-elaborator slows substantially.

- **Performance:**
    - ACP-routed agents: 2-10 minutes per session vs 30-90 seconds for direct-SDK. Decision-elaborator (default ACP) drives most of the latency multiplier; other roles unaffected unless explicitly opted into ACP.
    - Per-session total wall-clock: roughly 2-3× current on typical projects (decision-elaborator fanout dominates). Trade-off explicit: the subscription-cost win is paid for in wall-clock.
    - Cost per session for a Claude Max + Codex + Gemini Pro subscription user: ~$0 marginal (within quotas). Cost per session for API-only users with default hybrid: roughly today's cost minus the decision-elaborator's share, plus zero. Power users with `transport: direct_sdk` everywhere keep today's cost/latency profile.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. Default transport per role lands in scaffold prompts on `locutus update --reset`. Existing council-internal direct-SDK callers continue to work; new external MCP clients can opt in immediately. No persisted-state schema change.

**Reversal criteria.** Revert if:

- (a) the DX win doesn't materialize. Subscription quotas turn out to be too tight for sustained council use — `locutus refine goals` exhausts the daily Claude Max quota in one session, blocking the user's other work for the rest of the day. Mitigation: queue council work to off-hours; surface quota in progress UI; cap per-session ACP usage. If even with these guards the quota math doesn't work for real users, the cost/DX argument fails and the architecture should revert to direct-SDK as the default with ACP as opt-in.
- (b) quality degrades. Coding agents under ACP produce systematically worse decisions than direct-SDK (more fabricated citations, less coherent rationale, weaker alternative-evaluation). Surfaces as critic findings post-DJ-126 showing higher rates of decision-elaborator-introduced errors after the transport flip. Mitigation: tighten the revise-mode prompt (DJ-126) + the literal-sentinel pattern. If quality stays below direct-SDK after prompt iteration, decision-elaborator reverts to direct-SDK as default.
- (c) operational complexity overwhelms benefit. Recursion guards, capability scoping, session staging, transport routing — substantial new surface area. If the bug rate from these systems exceeds the bug rate of the council's logic itself for sustained periods, the architecture has crossed a complexity cliff. Mitigation: aggressive test coverage on the dispatch / staging / capability paths; integration test suite that exercises the recursion guards. If despite this the operational cost is too high, scope reduces to "write tools as external MCP surface; internal council stays direct-SDK."
- (d) subscription-tier latency makes interactive use unviable. `locutus refine goals` taking 40+ minutes on a moderate-sized project, vs the 5-10 minute expectation set by today's direct-SDK runs. Mitigation: more aggressive default-to-direct-SDK for non-research roles; per-role budget caps; or a `--fast` mode that pins everything to direct-SDK. If interactive use is fundamentally infeasible under ACP, this becomes a batch-mode-only feature.

**Reference.** Builds on [DJ-119](#dj-119-adopt-the-agent-client-protocol-acp-as-the-coding-agent-transport-supersedes-dj-006--dj-085s-shell-wrapper-claims) (the ACP transport infrastructure this extends to the council). Requires [DJ-125](#dj-125-in-flight-manifest--enriched-concern-model-refines-dj-094--dj-123-in-flight-surface-closes-dj-124s-concern-disposition-gap) (the typed manifest surface the write tools mutate) and validation of [DJ-126](#dj-126-decision-re-elaboration-for-cross-decision-contradictions-extends-dj-124-with-existing-decision-revision-depends-on-dj-125-concern-model) (council architecture stable before transport refactor) as prerequisites. Complements [DJ-099](#dj-099-direct-sdk-llm-adapters-per-provider-supersede-genkit) — direct-SDK adapters remain the default for fast/judgment roles; ACP adds a parallel transport for research-heavy roles. Preserves [DJ-122](#dj-122-graph-mutation-workflow-executor-with-spawner-nodes-supersedes-dj-112-on-control-flow-topology)'s workflow executor unchanged structurally; only the dispatch primitive inside `executeAgent` gains a transport switch. Preserves [DJ-105](#dj-105-elaborator-decisions-is-api-layer-required-not-prompt-layer-required) / DJ-124's schema-enforcement guarantees by moving the contract from output schemas to tool-input schemas — same `jsonschema` library, same per-field tags, same API-layer rejection of malformed input. Motivated by the cost/DX alignment argument from chat 2026-05-18 (Claude Max subscription user model vs API-billed-per-call) and the third winplan validation trace (pending DJ-125 + DJ-126).

## DJ-128: Decisions as Deliberation Logs; Structured Critic Counterproposals; Revision Cap as Commit (Refines DJ-126 Revise Loop After Third Winplan Re-Run)

**Status:** shipping (Phases 1-7 landed 2026-05-20; Phase 3 critic prompt files retired by DJ-129; Phase 8 winplan validation combined with DJ-129's)

**Context.** DJ-126 added the missing decision-revision dispatch so contradictions surfaced by critics could be addressed inside the convergence loop. Its first full validation run on winplan ([`/Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/)) terminated with `convergence_revision_capped` on `hosting-platform (revised 3×)`. The cap fired correctly — there was real oscillation — but the diagnosis isn't "the elaborator failed to converge." It's three structural gaps in how the council deliberates:

1. **Decisions don't carry their own deliberation history.** A revise dispatch fully replaces the prior decision body. The new revision shows the new chosen option and a new set of alternatives, but the prior chosen option — and the critic finding that demoted it — vanish from the decision. Next iteration's critic sees only the current snapshot and can re-litigate the same axis with no memory that the prior choice was already considered and rejected. The deliberation log is at the wrong layer (DJ-103 history events are session-level, not embedded in the decision).

2. **Critics have no structured counterproposal menu.** `CriticIssues` is `Issues []string` — a list of free-form objections. A critic says "dec-X is wrong because Y" but has no schema slot for "and here are the alternatives I'd commit to in its place, each with reasoning and grounded evidence." The elaborator is left guessing what the critic actually wants. The critic, having committed to nothing, can re-raise the same objection (or a refactored version) every iteration with no skin in the game. Compare to `justify_challenger`, which emits a structured `AdversarialConcern{Weakness, Evidence, Counterproposal}` — the challenger has to commit to a specific alternative or retract the concern; DJ-128 generalizes this to an enumerated menu with per-option grounding.

3. **The revision cap is treated as a failure mode rather than a forcing function.** `scoutConvergenceRevisionCappedTerminal` returns a non-nil error; the workflow exits non-zero; `locutus refine goals` fails with vague guidance about human intervention. This lets the critic unilaterally terminate the council by repeating itself: the elaborator keeps capitulating, the critic keeps objecting, and at iteration N the loop dies. The critic, structurally an objector with no commitment, gets veto power over the entire spec.

Together these three create the bullying dynamic observed in the trace: critics object → elaborator picks something else (losing prior deliberation) → critics object again (with no engagement with the prior reasoning) → repeat until the cap kills the loop. None of the three behaviors individually is wrong; together they prevent the type-2 two-way door commit-and-move-on discipline the rest of the system relies on.

**Why this surfaced now.** DJ-126's winplan validation is the first run where the cap actually fired on a real spec graph. The end-to-end test in DJ-126 Phase 6 used a `MockExecutor` that converged after one revision; the live run with a real critic-elaborator pair produced the oscillation the cap was designed to catch — and exposed that catching it as an error is the wrong response.

**Decision.** Three coupled changes that together restore type-2 two-way door semantics to the council loop:

1. **Decisions become deliberation logs (alternative monotonicity).** When `mergeDecisions` processes a revise replacement, the prior chosen option is automatically demoted into `alternatives[]` with `rejected_because = <driving concern text + iteration tag>`. The revised decision's `alternatives[]` must strictly include every prior alternative; nothing is dropped on revise. The decision's `alternatives[]` becomes a chronological record of what was considered and why each was rejected. New `Alternative.RejectedAtIteration` field records which iteration demoted each option. The revise-mode prompt explicitly instructs the elaborator to either (a) flip to a prior alternative (in which case the prior chosen option is demoted with the critic's reasoning) or (b) reject the critic's counterproposal by adding it to alternatives with the elaborator's reasoning. The validator hard-fails any revision that shrinks the alternatives set.

2. **Critics emit grounded counterproposal menus.** Replace `Issues []string` with `Issues []CriticIssue{Weakness, Evidence, Counterproposals []CriticCounterproposal, RelatedDecisionIDs}`. Each `CriticCounterproposal` carries `{Option, Argument, Citations []spec.Citation}`:
    - **Option** — the concrete alternative (named vendor / config / behavior, not "use something else").
    - **Argument** — a complete-sentence statement of why this option is superior to the current decision on the dimension the Weakness identifies. The positive-framing form of `Alternative.RejectedBecause` (which is the negative framing); when the elaborator promotes the counterproposal to chosen, the Argument folds into the new decision's rationale; when the elaborator rejects, the Argument lands verbatim on the alternatives entry so the rejection reasoning has something concrete to engage with.
    - **Citations** — at least one citation grounding the Argument in evidence the elaborator can verify. Reuses the existing `spec.Citation` kind enum (`goals`, `doc`, `best_practice`, `spec_node`, `scout_brief`, `web`) and the literal-sentinel discipline from `justify_researcher.md` / `spec_decision_elaborator.md` for failed searches.

    The shape is **enumeration**, not single-pick: a critic that sees three viable alternatives lists all three, each with its argument and citations. The elaborator's revise call evaluates the full menu — pick one as the new chosen option (the picked counterproposal's Argument + Citations carry into the new decision's rationale + citations; the prior chosen demotes to alternatives) and fold the rejected counterproposals into alternatives (each rejected counterproposal becomes an Alternative entry with `Rationale = critic.Argument`, `RejectedBecause = elaborator's reasoning`, `Citations = critic.Citations`). Re-raising the same concern next iteration requires either new evidence or genuinely new counterproposals not already in the alternatives — substantively different concern, not a restate.

    The critic prompt is rewritten: when no concrete counterproposal with grounded citations exists, the critic does not raise the concern — the literal `"needs investigation"` sentinel from the search-failure pattern is the one exception, emitted as a single counterproposal with that string as Option and an empty Citations slice. The workflow surfaces those as advisory-only concerns (no revise dispatch). Empty / placeholder Option or Argument is rejected at the schema layer; degenerate citation arrays on non-sentinel counterproposals are caught by the per-call validator.

    Two structural properties this gives:

    - **A single revise call exhausts the critic's argument budget on that concern.** The critic enumerated their option-space upfront; the elaborator engaged with every one. Re-raising requires substantively new content. The bullying loop dies at the schema layer, not the cap.
    - **Counterproposals can't be fabricated.** Same anti-hallucination discipline as `Alternative.Citations` — the citation requirement forces the critic to ground each option in evidence (a GOALS.md clause, a vendor doc, a named principle, the scout brief, a retrieved URL). A critic that wants to suggest "use Postgres" has to either cite the GOALS clause that supports it, a doc that names it, or a retrieved page; the prompt-level "name a counterproposal" framing isn't a license for ungrounded suggestions.

3. **The revision cap commits rather than fails.** `scoutConvergenceRevisionCappedTerminal` is reshaped: it writes the DJ-103 diagnostic event as today, then marks every still-open concern on the capped axes as `Status: wontfix` with `Justification = "axis hit revision cap of N at iter M; council could not resolve critic↔elaborator disagreement; current decision committed as best answer; alternatives carry the contested reasoning"`. The decisions on capped axes get a new `Locked: true` flag (or equivalent) so `hasReviseableConcerns` excludes them from future revise dispatch. The terminal returns `nil` (no error). The convergence rule fires normally; the loop ships.

Combined, the three changes give a coherent type-2 two-way door council:

- Critic raises a real concern with a counterproposal → elaborator either flips (prior demoted) or rejects (counterproposal demoted) → decision body extends → concern resolved.
- Critic re-raises the same concern → elaborator points at the recorded rejection reasoning in alternatives; to push back, the critic must argue with the rejection (substantively different concern, not a restate).
- Genuine intractable disagreement → cap fires after 3 rounds → council commits the elaborator's latest revision with full deliberation log, marks the contested concerns wontfix, ships → no more bullying.

**Alternatives considered.**

- **Trust the critic to self-regulate.** Keep `Issues []string` but tell the critic in prose "only raise a concern if you'd commit to specific alternatives with grounded reasoning." Prose alone is not load-bearing — the same kind of failure the DJ-118 invopop schema-tag migration was designed to eliminate. The structured `Counterproposals []CriticCounterproposal{Option, Argument, Citations}` shape travels into the structured-output mode every adapter uses; prose-only enforcement doesn't. Rejected.

- **Filter critic concerns by severity.** Only fire revise for `severity: high`; let the elaborator ignore medium / low. Treats the symptom (volume) not the cause (no counterproposal). A high-severity objection without a counterproposal is just as bullying as a low-severity one. Rejected.

- **Add a new "arbitrator" agent.** When critic and elaborator disagree N times, dispatch a third agent to pick a winner. Adds surface area; the arbitrator has no privileged knowledge over either party; just defers the disagreement by one layer. The deliberation log + cap-as-commit gives the same outcome (someone has to pick) with no new agent. Rejected.

- **Defer all critique to a final post-convergence pass.** The "run critics once at the end" idea. Removes the per-iteration pressure that catches contradictions early; just relocates the loop (if final critique finds problems, re-enter). The current per-iteration shape is correct; the fix is in HOW the critique→revise interaction works, not WHEN it runs. Rejected for the same reasons argued in the chat re: DJ-126's per-iteration design.

- **Track deliberation in a side-channel (history events only).** Keep decisions as current-state snapshots; rely on DJ-103 `decision_revised` events to record the lineage. This is the current state of the system — and it's the failure mode. The critic doesn't read history events; it reads the in-flight manifest. Deliberation needs to live in the decision body to be load-bearing for the next critic's decision to raise or not raise a concern. Rejected.

- **Cap-as-commit without the deliberation log.** Just stop erroring; commit whatever the latest revision says. Solves the immediate bullying but leaves the underlying issue (no deliberation memory) intact — the spec ships with decisions whose contested reasoning isn't captured. The user has no way to see "this was contested 3 times; here's what was weighed." Rejected — the three changes need to ship together to be coherent.

**Consequences.**

- **Code:**
    - `internal/spec/types.go` — `Alternative` gains `RejectedAtIteration int` (omitempty for legacy load compatibility). Optional `RejectedByConcernText string` field carrying the verbatim critic finding that demoted the option. `Decision` gains `Locked bool` (omitempty) set by the cap-as-commit terminal.
    - `internal/agent/specgen.go` — `CriticIssues.Issues` shape changes from `[]string` to `[]CriticIssue{Weakness, Evidence, Counterproposals []CriticCounterproposal, RelatedDecisionIDs}`. New `CriticCounterproposal{Option, Argument, Citations []spec.Citation}` type. jsonschema tags enforce non-empty Weakness / Evidence / Option / Argument and `minItems=1` on Counterproposals and on each counterproposal's Citations. `degenerateCriticIssueValidator` rejects empty / placeholder Options or Arguments and ungrounded citation arrays (mirrors `degenerateChallengerBrief` + the alternative-citation discipline on `spec_decision_elaborator`).
    - `internal/agent/workflow_spec_generation.go` — `mergeCriticIssues` consumes the new shape; auto-extracts RelatedDecisionIDs via regex as today AND merges with the critic's structured field (critic-provided ids win on conflict). The full `Counterproposals` slice is carried through to `Concern.Counterproposals []CriticCounterproposal` (new `Concern` field); the elaborator's revise projection renders the whole menu.
    - `internal/agent/workflow_spec_generation_dj124.go` — `mergeDecisions` rewrites the revise replacement path: before overwriting the prior decision, demote the prior chosen option into `alternatives[]` with the driving concern as `rejected_because`. Validator hard-fails revisions where `len(revised.Alternatives) < len(prior.Alternatives) + 1` (post-demotion expected count). The revise projection renders prior `alternatives[]` and the critic's counterproposal as required input the elaborator must engage with.
    - `internal/agent/workflow_spec_generation_dj124.go` — `scoutConvergenceRevisionCappedTerminal` reshapes: writes diagnostic event, flips contested concerns to `wontfix`, marks capped decisions `Locked: true`, returns nil. The convergence rule (axes_open==[] AND no concerns open) now fires naturally; the loop ships.
    - `internal/agent/workflow_spec_generation_dj124.go` — `hasReviseableConcerns` skips concerns whose related decisions are all locked.
    - `internal/scaffold/agents/architect_critic.md`, `cost_critic.md`, `devops_critic.md`, `sre_critic.md` — rewritten to require the `Counterproposals` enumeration per concern, each entry carrying Option + Argument + grounded Citations. "Don't raise a concern you wouldn't commit to specific alternatives for, and enumerate every option you'd accept rather than picking one arbitrarily" replaces the current "find what doesn't add up" framing for the action-output side; the analysis framing stays as-is.
    - `internal/scaffold/agents/spec_decision_elaborator.md` — Revise mode section rewritten: walks the critic's counterproposal menu, picks one (flip) or rejects all (reject), folds the prior chosen and every rejected counterproposal into alternatives with the critic-provided Argument + Citations preserved verbatim. Walk `docs/agent-conventions.md` end-to-end before drafting per the memory checklist.
    - Tests: `TestMergeDecisionsDemotesPriorChosenOption`, `TestMergeDecisionsRejectsAlternativeShrinking`, `TestCriticIssueRequiresCounterproposalMenu`, `TestMergeDecisionsFoldsRejectedCounterproposalsAsAlternatives`, `TestRevisionCapCommitsRatherThanErrors`, `TestLockedDecisionsAreExcludedFromRevise`, end-to-end smoke test where the loop ships via cap-as-commit on an intractable disagreement.

- **User-visible:**
    - Decisions in `.borg/spec/decisions/` carry richer `alternatives[]` with `rejected_at_iteration` and `rejected_by_concern_text` metadata, showing the full deliberation chronology.
    - `locutus history` distinguishes "decision_revised" (extends deliberation) from "decision_locked" (cap-as-commit terminal write).
    - `locutus refine goals` exits zero on intractable critic↔elaborator disagreement — the spec ships with `wontfix` concerns recorded; the user can re-engage via the manual revision verb if desired.
    - The `wontfix` justification in the manifest tells the user exactly why a concern was set aside: "axis hit revision cap; current decision committed as best answer; see alternatives[] for contested reasoning."

- **Performance:**
    - Critic LLM calls produce ~50-80% more tokens (the Counterproposals enumeration with per-option Argument + Citations adds substantial prose vs the prior free-form string list). Net session cost roughly +15-20% on critique phases; balanced against the typical -100% from the loop no longer dying at the cap on contradiction-heavy projects and the substantial reduction in re-raise iterations now that critics can structurally see what's been considered.
    - Revise dispatches per iteration unchanged from DJ-126; the dedup-by-decision fix from the DJ-126 winplan re-run remains intact.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. Existing sessions persisted with `Issues []string` critic outputs aren't replayable under the new schema — running `locutus update --offline --reset` refreshes agent prompts. Persisted decisions without `RejectedAtIteration` on alternatives load cleanly (omitempty); the field is only populated for revisions authored under DJ-128.

**Reversal criteria.** Revert if:

- (a) the structured Counterproposals field causes critics to under-report — they decline to raise concerns whose counterproposals are genuinely uncertain (or whose Citations they can't ground), hiding real issues. Surfaces as critic Issues arrays smaller than under the old prose-only schema on the same fixture inputs. Mitigation: the `Option: "needs investigation"` sentinel value the critic emits when it sees a real problem but has no concrete alternative; the workflow surfaces those to the user as advisory-only concerns rather than dispatching revisions. If even with the sentinel the critic systematically swallows concerns, the structured shape isn't worth the bullying mitigation and the critic returns to free-form Issues.

- (b) the deliberation-log alternatives grow unboundedly. After N revisions on the same decision, `alternatives[]` carries N+1 entries; the decision body becomes too long for downstream consumers to skim. Mitigation: collapse functionally-equivalent alternatives (same product, different reasoning) during persistence; show only the top 3-5 most recently considered in `spec_list_manifest`'s summary view. If alternatives bloat outpaces these mitigations, the deliberation log moves to a side-channel (a per-decision `deliberation.md` file) with the decision body carrying only the current state and a count.

- (c) cap-as-commit ships systematically wrong decisions because the elaborator's latest revision is reflexive (the elaborator capitulated on iter-N-1 to a critic objection the elaborator should have rejected). Surfaces as user feedback after several sessions that the shipped specs contain decisions the user disagrees with on inspection. Mitigation: in the cap-fired path, run one last "commit" call to the elaborator with all revisions in context, asking it to pick the one it most stands behind. Adds one LLM call per capped axis; preserves the cap-as-commit semantics. If even with the commit call the shipped decisions are wrong too often, the cap returns to the error-and-require-human path for the specific axes where the elaborator's confidence on revisions trended downward — heuristic, not a structural rollback.

- (d) the count of locked decisions grows unboundedly across sessions, signaling the critic and elaborator are categorically incompatible on some class of axis. Mitigation: track lock rate per session in DJ-103 events; alert when a session ships with >25% of decisions locked. If a project consistently hits high lock rates, the critic prompts need lens-specific revision (the cost critic and architecture critic may need different counterproposal disciplines).

- (e) the "argue with the recorded rejection" framing becomes a backdoor re-litigation vector. Critics use the engagement-with-rejection clause to keep raising functionally identical concerns dressed up as "engaging" with the prior rejected_because — same bullying loop, two extra schema fields. Surfaces as: a Counterproposal.Option that matches an existing alternative's Name at >X% rate across sessions; Argument fields that paraphrase the alternative's prior critic-side Argument without naming a specific weakness in the recorded RejectedBecause; revise-decisions iterations on locked-and-then-unlocked decisions (where the critic's "engagement" was strong enough to reopen a previously contested decision but then re-traveled the same ground). Mitigation: tighten the validator to require Argument to literally reference a substring of the existing RejectedBecause when the Option matches an existing alternative — forces structural engagement, not just rhetorical claim of engagement; promote "engagement is judged by the elaborator in the revise prompt, not asserted by the critic in the CriticIssue" — the elaborator decides in the revise call whether the critic's Argument substantively engages with the recorded rejection, and rejects the concern as a re-raise if not. The Phase 8 winplan validation should explicitly check critic Counterproposal.Option strings against the per-decision alternatives history; if the rate of Option-reuse is above 20% on iter-2+ critique passes, this reversal criterion is firing.

**Reference.** Refines [DJ-126](#dj-126-decision-re-elaboration-for-cross-decision-contradictions-extends-dj-124-with-existing-decision-revision-depends-on-dj-125-concern-model) — the revise loop ships intact but the failure mode the cap was designed to catch is reshaped from "error and exit" to "commit and continue." Extends [DJ-125](#dj-125-in-flight-manifest--enriched-concern-model-refines-dj-094--dj-123-in-flight-surface-closes-dj-124s-concern-disposition-gap)'s `Concern` model with `Counterproposals []CriticCounterproposal` and reuses `Status: wontfix` (already in the enum) as the cap-as-commit terminal disposition — no new status. Honors [DJ-085](#dj-085-decision-provenance-is-denormalized-into-the-decision-itself) by keeping deliberation history inside the decision body rather than a separate file. Mirrors [`justify_challenger`'s `AdversarialConcern{Weakness, Evidence, Counterproposal}` shape](#dj-099-direct-sdk-llm-adapters-per-provider-supersede-genkit) — the challenger / counterproposal discipline that the council loop's critics structurally lacked. Honors [DJ-103](#dj-103-history-events-are-the-narrative-source-recorded-events-plus-llm-summary) by adding `decision_locked` as a sibling event kind to `decision_revised`. Motivated by the third winplan re-run trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260520/1224/39-81eadc/) and the chat 2026-05-20 about decision oscillation and the bullying dynamic.

## DJ-129: Dimension-Driven Critics (Scout-Surfaced Critique Surfaces Replace Fixed 4-Critic Lens Set; Builds on DJ-128 Structured Counterproposal Discipline)

**Status:** shipping (Phases 1-6 landed 2026-05-20; Phase 7 winplan validation deferred to user; Phase 8 status flips landed 2026-05-20)

**Context.** DJ-128 landed the deliberation log + structured counterproposal discipline that restored type-2 two-way door semantics to the council loop. The four critic agents it rewrote (architect, devops, sre, cost) — historically the fixed lens set chosen by accumulation rather than design — surfaced three structural problems on inspection:

1. **Coverage gaps.** Real lenses are missing: security/privacy (a campaign-software project's voter-file privacy concerns; a fintech's PCI scope; a medical project's HIPAA boundary), compliance (state-level privacy regimes; election law), maintainability vs. team capacity, data integrity, vendor lock-in / portability beyond pricing. Adding any of these means writing a new critic agent file plus prompt-engineering it — the N+1 trap.

2. **Forced critique noise.** A project whose GOALS.md explicitly de-prioritizes cost (research project; internal infra where the company eats the bill) still runs the cost critic. The dominant failure mode of an unforced critic is empty issues; the dominant failure mode of a *forced* critic is pattern-matching cost shapes onto decisions where cost isn't actually a constraint — producing specious findings that pollute the concerns set.

3. **Symmetry break with scout-driven dispatch.** The scout already identifies project-specific axes (AxesOpen) and dispatches one decision-elaborator per axis. The critique stage is special-cased: a fixed set of agents that don't know what's specific about the project. This is the same shape problem the DJ-124 scout-driven loop solved for first-author decisions.

**Decision.** Close the symmetry: the scout identifies *critique dimensions* (the same way it identifies axes); the critique stage fans out one parametric `spec_critic_elaborator` per dimension. The four lenses become content (per-discipline sections in one elaborator prompt) rather than identity (separate agent files).

Five coupled changes:

1. **`CritiqueDimension` schema on `ScoutBrief`.** New `ScoutBrief.CritiqueDimensions[]` field carrying one `CritiqueDimension{ID, Lens, FocusQuestion, SourceEvidence[], Disciplines[], SeverityFloor}` per critique surface. `Lens` is free-form (`cost`, `sre`, `compliance`, `election-cycle-traffic`, …); drives `Concern.Kind` for grouping and nothing else in code. `Disciplines` is a bounded enum (`web_grounded`, `spec_node_grounded`, `best_practice_grounded`, `goals_grounded`, `freeform`) that names *how to ground a claim* — one prompt section per discipline value; the elaborator applies the sections the dimension names.

2. **One parametric critic agent.** `spec_critic_elaborator.md` (DJ-129 Phase 3) replaces `architect_critic.md`, `devops_critic.md`, `sre_critic.md`, `cost_critic.md`. The prompt has one Identity section + five Discipline sections; the dimension's `disciplines[]` field selects which sections apply. Output schema is unchanged (`CriticIssues` from DJ-128).

3. **Critique step becomes a Fanout.** The `convergenceLoopTemplate`'s critique step changes from `Agents: []string{4 critics}, Parallel: true` to `Agents: []string{"spec_critic_elaborator"}, Fanout: fanoutCritiqueDimensions`. Empty CritiqueDimensions → zero items → critique is a no-op for that iteration (the no-floor design decision).

4. **Convergence requires dimension stability.** Mirrors the existing axis-cycle detection in `scoutSpawnFor`. A scout claiming `Converged: true` while introducing a brand-new dimension this iteration forces another iteration (the new dimension's critic-elaborator gets at least one chance to surface concerns). Stability is monotonic-add: new-dimension introduction blocks; retirement and recurrence do not (design decision #7).

5. **Lens-from-fanout-item Concern.Kind derivation.** `mergeCriticIssues` reads the dimension's Lens off the fanout item (the new `deriveCritiqueKind` helper) rather than off the AgentID. Legacy `critiqueKindFor` stays as a fallback for any pre-DJ-129 loaded session data.

**Alternatives considered.**

- **Add a separate critique-scout agent.** A dedicated agent that surveys for critique surfaces only (separate from spec_scout's axis-survey duty). Costs one extra LLM call per iteration; the spec_scout's project-understanding context (DomainRead, ImplicitAssumptions, WatchOuts) is already exactly what informs dimension identification, so a separate agent would re-derive it. Rejected.

- **Keep a fixed lens floor.** Always run a core critic set (e.g. always-on cost + always-on architecture) plus scout-driven extras. Preserves coverage at the cost of the forced-noise problem on projects where the floor doesn't apply. Rejected per design decision #3: forcing an LLM critic to find a concern when none exists produces specious findings worse than no critique. The mechanical `integrity_critic` (citation coverage, decision-per-feature, no-dangling-refs) stays code-side and runs unconditionally as a non-LLM floor.

- **One critic agent file per discipline (not per lens).** A `web_grounded_critic.md`, `goals_grounded_critic.md`, etc. Trades the lens-N+1 trap for a discipline-N+1 trap; loses the composition story (a cost dimension often needs both web_grounded and goals_grounded). Rejected.

- **Make Lens a bounded enum like Disciplines.** Forces lens vocabulary into code; defeats the project-specific-lens goal (`election-cycle-traffic`, `pci-scope` are project-named). Rejected.

- **Defer dimension identification to the user.** The scout could enumerate candidate dimensions; the user picks which ones apply via a config file or CLI flag. Reintroduces human-in-the-loop into a workflow whose explicit design goal is autonomous spec generation. Rejected.

**Consequences.**

- **Code:**
    - `internal/agent/specgen.go` — new `CritiqueDimension` struct with bounded discipline enum jsonschema tags; `ScoutBrief` gains `CritiqueDimensions []CritiqueDimension`.
    - `internal/agent/state.go` — `PlanningState` gains `CurrentCritiqueDimensions []CritiqueDimension`, `CritiqueDimensionsByIter map[string]int` (append-only first-seen tracking), `LastDimensionsStable bool` (computed in mergeScoutBrief before recording so scoutSpawnFor sees the prior map shape). `snapshotPlanningState` deep-copies the new fields.
    - `internal/agent/critique_dispatch_dj129.go` — new file: `fanoutCritiqueDimensions`, `recordDimensionStability`, `dimensionsAreStable`, `CritiqueDimensionItem` (fanout-item shape).
    - `internal/agent/workflow_spec_generation.go` — `mergeScoutBrief` populates dimensions and computes stability; `mergeCriticIssues` derives kind from the fanout item's Lens via new `deriveCritiqueKind` helper; new `projectCritiqueDimension` projection renders focus_question + source_evidence + applicable disciplines plus the proposal block; the critique step in `convergenceLoopTemplate` becomes a Fanout dispatching `spec_critic_elaborator`.
    - `internal/agent/workflow_spec_generation_dj124.go` — `scoutSpawnFor`'s convergence rule gates exit on `brief.Converged AND openCount==0 AND LastDimensionsStable`.
    - `internal/agent/workflow.go` — `RoundResult` gains a `FanoutItem` field; `executeAgent` threads `snap.FanoutItem` through so merge handlers can attribute results back to the dispatching dimension. `critiqueKindFor` doc-commented as the legacy fallback path.
    - `internal/scaffold/agents/spec_critic_elaborator.md` — new parametric critic agent file. One Identity section, five Discipline sections, the DJ-128 Counterproposals-menu task discipline carried forward unchanged.
    - `internal/scaffold/agents/spec_scout.md` — new `critique_dimensions` section teaching dimension identification: focus_question framing, discipline-enum coverage, lens open-endedness, the add/retain/retire lifecycle.
    - Deleted: `internal/scaffold/agents/{architect,devops,sre,cost}_critic.md` and the DJ-128-era `critic_prompts_dj128_test.go` (replaced by `elaborator_critic_dj129_test.go`).
    - Tests: `TestCritiqueDimensionRoundTrip`, `TestScoutBriefRoundTripsCritiqueDimensions`, `TestPlanningStateCarriesDimensionFields`, `TestCritiqueDimensionSchemaRegistered`, `TestFanoutCritiqueDimensionsEmitsOneItemPerDimension`, `TestFanoutCritiqueDimensionsHandlesEmptySet`, `TestRecordDimensionStability*`, `TestDimensionStability*`, `TestMergeScoutBriefPopulatesCritiqueDimensions`, `TestProjectCritiqueDimensionRendersFocusAndDisciplines`, `TestCritiqueStepIsAFanoutOverCritiqueDimensions`, `TestMergeCriticIssuesTagsKindFromDimensionLens`, `TestRetiredCriticAgentsAbsentFromScaffold`, `TestScoutPrompt*`, `TestCriticElaboratorPrompt*`, plus the three end-to-end `TestDJ129*` tests covering the compliance-dimension happy path, the no-cost-concern skip path, and the dimension-instability-blocks-convergence path.

- **User-visible:**
    - Critics now run only when the scout has identified a critique dimension. A research project with no cost concern won't see specious cost findings polluting its concerns set.
    - The lens vocabulary in `Concern.Kind` is project-defined: a campaign-software project's concerns may carry `Kind: compliance` or `Kind: election-cycle-traffic`; a fintech project may surface `Kind: pci-scope`. The revise projection's grouping reflects what the project actually cares about.
    - `spec_scout` is now responsible for two scout-driven dispatches per iteration: AxesOpen → decision-elaborators (DJ-124), and CritiqueDimensions → critic-elaborators (DJ-129). The symmetry makes the council's per-project tailoring legible at the scout layer.
    - The scout brief in `.locutus/sessions/.../scout-iter-N.yaml` now carries a `critique_dimensions` block alongside `axes_open`; users reading session traces see exactly which lenses the council applied to each iteration and why (the focus_question + source_evidence make it auditable).

- **Performance:**
    - Critic LLM calls per iteration scale with the number of dimensions the scout surfaces (1-5 typical) rather than a fixed 4. A research project with no critique dimensions → zero critic calls (net cost reduction). A campaign-software project with cost + compliance + election-cycle-traffic + vendor-portability dimensions → 4 critic calls (parity). The dominant cost change is shifting toward the project's actual surface area.
    - The scout's LLM call grows ~20-30% on tokens (the new `critique_dimensions` field with focus_question + source_evidence prose) — one larger scout call vs. potentially fewer critic calls.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. Existing sessions persisted with pre-DJ-129 critic AgentIDs (`architect_critic`, etc.) load cleanly through the `critiqueKindFor` legacy fallback. `locutus update --offline --reset` refreshes agent prompts to the DJ-129 set (the four `*_critic.md` files deleted, `spec_critic_elaborator.md` added, `spec_scout.md` updated).

**Reversal criteria.** Revert if:

- (a) the scout systematically under-identifies dimensions — fixtures that obviously have a cost concern produce briefs with no cost-lens dimension, hiding real issues. Surfaces as final specs that miss obvious lens-specific concerns the prior 4-critic surface would have caught. Mitigation: enrich the scout prompt's lens-diversity examples; if even with enriched examples the scout under-identifies, reintroduce a thin code-side floor that injects a default cost dimension when GOALS contains a budget clause and the scout didn't surface one.

- (b) the discipline enum proves too coarse — projects need a discipline pattern not covered by web/spec_node/best_practice/goals/freeform. Surfaces as scout briefs that pick `freeform` when a more specific discipline would have grounded the critic better; or critic-elaborator outputs that cite the wrong shape of evidence under `freeform` because the prompt doesn't have a section for the discipline they needed. Mitigation: add a discipline value to the enum + a section to the elaborator prompt (the design's whole point is this is cheap; adding a new lens is now a one-line schema change plus a prompt section, not a new agent file).

- (c) the dimension-stability convergence rule causes infinite loops — a scout keeps surfacing a new dimension every iteration, blocking convergence indefinitely. Surfaces as runs that hit budget exhaustion with the same scout repeatedly introducing one new dimension per iteration. Mitigation: per-iteration cap on newly-introduced dimensions (e.g. at most 2 new dimensions per iteration; scout must defer the rest). The cap fires the budget-exhaustion terminal with a diagnostic naming the rapidly-introduced dimensions for the user to see.

- (d) the parametric critic-elaborator produces shallower findings than the per-lens critics did — losing lens-specific depth because the discipline sections are general rather than lens-specific. Surfaces as comparison runs (DJ-128 4-critic vs. DJ-129 parametric on the same fixture) where the DJ-128 outputs cite more specific evidence per concern. Mitigation: enrich the per-discipline sections with lens-aware examples; if the depth gap is structural rather than promptable, the elaborator gains optional lens-specific guidance sections (e.g. "### When lens=cost") to bridge.

**Reference.** Builds on [DJ-128](#dj-128-decisions-as-deliberation-logs-structured-critic-counterproposals-revision-cap-as-commit-refines-dj-126-revise-loop-after-third-winplan-re-run) — `CriticIssue`/`CriticCounterproposal` shape unchanged; the cap-as-commit and deliberation-log discipline carries forward. Closes the symmetry opened by [DJ-124](#dj-124-scout-driven-convergence-loop-replaces-spec-gate-with-axis-driven-dispatch-supersedes-dj-094-phase-iterations) — scout-driven dispatch for axes generalizes to scout-driven dispatch for critique dimensions. Honors [DJ-118](#dj-118-invopop-jsonschema-replaces-google-jsonschema-go-throughout) by tagging the new `CritiqueDimension` fields with invopop enum/description tags per the [CLAUDE.md jsonschema rule](../CLAUDE.md). Honors [DJ-103](#dj-103-history-events-are-the-narrative-source-recorded-events-plus-llm-summary) — no new history event kinds; dimension lifecycle is observable via the scout brief in session traces. The motivating chat session is 2026-05-20; the implementation plan is at `.claude/plans/dj-129-implementation-tasks.md` and the design rationale at `.claude/plans/dj-129-dimension-driven-critics.md`.

## DJ-130: Provider Mechanics Encapsulated in Adapters; Trace Recording Follows the Provider-Call Boundary (Supersedes Unrecorded `5d15e7b` Split-in-Dispatcher Pattern)

**Status:** shipping (Phases 1-4 landed 2026-05-21; Phase 5 winplan empirical verification pending live-API run)

**Context.** The fifth winplan re-run ([`/Users/chetan/projects/winplan/.locutus/sessions/20260521/0055/07-b8e485/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260521/0055/07-b8e485/)) converged for the first time but produced a spec with 13 decisions, 2 features, and **zero strategies**. The architecture's only path to author strategies is `scout.new_nodes[kind=strategy] → narrative-elaborator → strat-` body; in the failing trace the scout's iter-0 *thinking* drafted two strategies (`strat-deployment`, `strat-data-store`) but the final structured-output JSON emitted only one feature. The strategies were silently dropped between extended thinking and structured-output serialization. Iter-1 through iter-4 emitted zero `new_nodes` at all. This is the failure mode `docs/agent-conventions.md` §6 names ("extended thinking + structured + short output = lossy serialization"), now observed end-to-end on Gemini 3 Pro Preview after prior identification on Claude (the `dummy` placeholder regime) and OpenAI gpt-5-nano (empty `{}` tool args under low reasoning_effort).

The workaround for this failure exists in code as of [`5d15e7b`](https://github.com/chetan/locutus/commit/5d15e7b) (2026-05-13, "feat(council): reason-then-format split for thinking-on schema agents"): when an agent has `thinking != off` and `output_schema != ""`, `Dispatcher.dispatchSplit` runs two LLM calls — one for reasoning (no schema) and one for format extraction (no thinking, no tools, schema set, different provider's fast tier from `format_providers:` rotation). The eval at 6/6 on Haiku and gpt-5-mini validated the format pass. **But three structural inconsistencies prevent this from helping the spec-generation council:**

1. **The split shipped without a Decision Journal entry.** The architectural choice — "the layer that decides whether to split is the Dispatcher" — was made tactically and never recorded. No DJ documents the rationale, the reversal criteria, or the layering invariant.

2. **The spec-generation `WorkflowExecutor` bypasses the Dispatcher.** [`internal/agent/workflow.go:322`](../internal/agent/workflow.go#L322) calls `RunWithRetry(ctx, e.Executor, def, input, ...)` directly, skipping `Dispatcher.Dispatch` entirely. The split path is never even checked for spec_scout, spec_decision_elaborator, spec_feature_elaborator, spec_strategy_elaborator, spec_reconciler, or spec_critic_elaborator — the agents that actually produce specs. Workflow-driven calls have been getting the broken thinking+schema single-call behavior for ~7 days while the split sat unused for that pathway. This wiring gap is what made the winplan re-run fail invisibly.

3. **Per-call YAML recording is at the `LoggingExecutor` boundary, not the provider-call boundary.** [`session.go:744-765`](../internal/agent/session.go#L744-L765) records one YAML per `AgentExecutor.Run`. OTel spans (per the comment at line 737-743) are already at the *provider-call* boundary (`provider.generate` span opened by each adapter's `Run`). When the dispatcher splits, OTel sees two spans but the YAML records one merged call. The two observability surfaces drift apart precisely when the split is firing — which is exactly when an operator most needs to see both calls.

Beyond the three implementation gaps, the layering principle they reveal needs to be settled. The current Dispatcher-decides-split shape encodes provider-specific knowledge ("Gemini can't do thinking+schema in one call"; "Claude emits `dummy`"; "OpenAI emits `{}`") at the Dispatcher layer, which has no other reason to know about provider mechanics. The adapters — which *do* know which provider they're talking to — sit one layer below and are passive: they take whatever request the dispatcher hands them and submit it, even when the combination is known-broken on their provider. DJ-108 ([Anthropic Native Structured Output](#dj-108-anthropic-native-structured-output--adaptive-thinking)) established the opposite direction explicitly — provider-specific structured-output mechanics belong inside the adapter, not in a dispatcher-layer workaround. The split-in-Dispatcher pattern shipped the day after DJ-108 and quietly walked back from DJ-108's principle.

**Why this surfaced now.** The DJ-125 in-flight manifest + DJ-126 decision-revision + DJ-128 deliberation-log + DJ-129 dimension-driven critics combined enabled the loop to converge — exposing what convergence on a thin spec actually looks like, which made the silent strategy-drop visible for the first time. Prior runs failed at convergence and the missing strategies were masked by the larger failure. The lossy-serialization failure mode was always present; convergence made it observable.

The lowest-common-denominator pattern (Genkit / LangChain / Vercel AI SDK / BAML) considered as an alternative: uniform `provider.Generate(req)` interface across providers; the caller is responsible for knowing which combinations work and which don't. Rejected — *the failure is universal across all three providers we use and silent*, so LCD doesn't actually give callers a portable contract, just three different undocumented failure modes to know about. Locutus is opinionated software with a single internal caller (the workflow engine), not an SDK with third-party consumers; the portability cost of adapter encapsulation is zero. The chat conversation (2026-05-21) walked through the LCD case in detail and committed to the minority pattern. The deeper rationale: when the workaround is universal across the provider set, when the failure is silent and corrupting (not just degraded), and when the caller is internal/coupled, adapter encapsulation is the right tradeoff. The HTTP client / database driver / gRPC client analogues all encapsulate provider-specific mechanics for the same reasons.

**Decision.** Three coupled changes shipped together as DJ-130. The architectural principle they instantiate: **the workflow expresses intent (one `def + input → output meeting contract` call); the dispatcher orchestrates retry/rotation/observability; the adapter handles provider-specific mechanics including any internal multi-call workaround. Observability follows the provider-call boundary on both surfaces (OTel and YAML).**

1. **Workflow LLM calls route through `Dispatcher.Dispatch`.** [`workflow.go:322`](../internal/agent/workflow.go#L322) replaces `RunWithRetry(ctx, e.Executor, def, input, ...)` with `dispatcher.Dispatch(ctx, def, input, opts)`. `dispatchOnce` already invokes `RunWithRetry` internally (line 398), so retry semantics survive. `WorkflowExecutor[S]` gains a `Dispatcher AgentDispatcher` field; constructors that don't pass one fall back to `NewDispatcher(executor)`. `DispatchOptions.Role` carries the step ID so per-call telemetry attributes the work cleanly.

2. **The thinking+schema split moves into each provider adapter.** `Dispatcher.dispatchSplit`, `Dispatcher.shouldSplitForFormat`, the `FormatProvider` interface, `canonicalFormatterPrompt`, and the `format_providers:` config block in `models.yaml` are retired. Each adapter ([anthropic.go](../internal/agent/adapters/anthropic.go), [gemini.go](../internal/agent/adapters/gemini.go), [openai_responses.go](../internal/agent/adapters/openai_responses.go)) gains internal logic: when the request asks for thinking + structured output and the adapter's provider is known to handle that combination poorly, the adapter makes two SDK calls itself (reasoning first, then format) and returns one merged `AgentOutput`. Format pass uses the *same provider's fast tier* (Anthropic Haiku for Claude reasoning; Gemini Flash-Lite for Gemini reasoning; gpt-5-mini for gpt-5 reasoning), declared in each provider's `providers:` block in `models.yaml` — no separate `format_providers:` rotation. Each adapter exposes a `requiresThinkingSchemaSplit(req Request) bool` function with a doc-comment naming the failure mode, the provider/model affected, and the commit that surfaced it; future provider improvements (e.g., a hypothetical Gemini 4 that handles the combination cleanly) flip the function to return false for that model without touching the dispatcher or workflow.

3. **`SessionRecorder.Begin`/`Finish` moves into the adapter layer.** Each adapter's `Run` opens its own recorder handle per *actual* SDK call. When an adapter splits internally, it produces two handles → two YAMLs. `LoggingExecutor` reshapes from a per-`Run` recorder into a per-agent-step *grouper*: it opens a parent step record (no provider call) when `inner.Run` starts, and child provider-call records emitted by the adapter link to it via a `parent_call_id` field carried on `context.Context` (mirrors the existing `WithRole` / `WithAgentID` / `WithCallTag` pattern at [session.go:746-748](../internal/agent/session.go#L746-L748)). On-disk layout changes from flat `0001-spec_scout.yaml` to a per-step folder:
    ```
    calls/0002-spec_scout/
      step.yaml                    # parent: role, agent_id, total tokens, duration, child call list
      01-reason.yaml               # provider call 1 (thinking on, no schema)
      02-format.yaml               # provider call 2 (no thinking, schema set)
    ```
    Single-call agents (thinking off, or no schema, or a future provider that handles the combination natively) produce one child YAML; the structure is uniform regardless of split status.

Together: the workflow sees one `Dispatch` per step; the dispatcher sees one `Run` per adapter invocation; the adapter handles the SDK-call shape internally and emits one trace record per actual SDK call. OTel and YAML now agree on what constitutes a "call." Operators debugging a slow / expensive / failing step see exactly the SDK calls that happened.

**Alternatives considered.**

- **Lowest-common-denominator interface (Genkit / LangChain / Vercel AI SDK / BAML pattern).** Uniform `Adapter.Run` across providers; caller decides whether to split. Discussed at length in chat 2026-05-21. Rejected because (a) the failure is universal across all three providers we use — there's no `Run(thinking_on, schema_set)` shape that "just works" across the provider set, so the LCD doesn't actually give callers a portable contract; (b) the failure is silent and corrupting (winplan strategies dropped invisibly), making caller-managed avoidance unreliable in practice; (c) Locutus is opinionated software with a single internal caller, so the portability cost LCD optimizes for doesn't apply; (d) DJ-108 already set the precedent that provider mechanics encapsulate into the adapter. The HTTP-client / DB-driver / gRPC-client analogies all transparently handle pooling / retries / chunking; the same reasoning applies here. The LCD argument's strongest point — capability drift goes invisible inside adapters — is addressed by the regression-test discipline in the reversal criteria below.

- **Keep the split in the Dispatcher, just wire it into the workflow.** Wire `workflow.go:322` through `Dispatcher.Dispatch`; leave `dispatchSplit` / `FormatProvider` / `format_providers:` in place. The narrow fix for the winplan strategy-drop. Rejected because it perpetuates the layering violation that `5d15e7b` introduced — Dispatcher knows about provider quirks it has no other reason to know about; observability remains misaligned (one YAML per dispatched call, two OTel spans when the split fires); the wiring fix would have to be re-done if/when a future provider's structured-output handling requires different mechanics than today's split.

- **Move the split into the adapter but leave the recorder at the LoggingExecutor boundary.** Adapter-encapsulated split; YAML keeps the per-`Run` aggregation. Rejected — operator debugging a slow / failing step *cannot see* which sub-call failed without trace correlation against OTel; the per-call YAML's role as the human-readable surface is broken if the unit of recording doesn't match the unit of work. The chat 2026-05-21 explicitly raised this and committed to fixing both surfaces together.

- **Per-call YAMLs as a flat list with sub-suffixes (`0002a-spec_scout-reason.yaml`, `0002b-spec_scout-format.yaml`) instead of per-step folders.** Less migration churn. Rejected for the medium term — flat-with-suffixes scales poorly to N>2 calls (future retry / fallback / streaming reassembly cases all involve N child calls), and the lack of a parent `step.yaml` means the per-step summary view requires walking all children. The per-step folder pattern degrades gracefully (single-call agents produce one child YAML; the folder shape is the same).

- **Capability detection at startup via provider APIs.** Anthropic's Models API exposes `structuredOutputs.supported`, `thinking.types.adaptive.supported`, etc. (named in DJ-108). Use those to drive `requiresThinkingSchemaSplit` dynamically rather than hard-coding per-model knowledge in each adapter. Rejected for the initial implementation because the failure mode is empirical (the model claims the combination is supported; in practice the output is degenerate) — capability claims don't capture the actual reliability profile. Static per-model overrides in the adapter are the source of truth; the Models API can inform future model additions but doesn't replace the empirical knowledge.

- **Sentinel "force single call" opt-out for callers that explicitly want the raw provider behavior** (e.g., for testing "what does Gemini actually do?"). `AgentDef.SplitMode: auto | always | never`. Rejected for the initial implementation — adds surface area for a need that hasn't materialized. The adapter's behavior is deterministic given (def, input), so tests that want to see raw provider behavior can set `Thinking: off` to bypass the split path explicitly. Revisit if a real consumer needs the opt-out.

**Consequences.**

- **Code:**
    - [`internal/agent/dispatcher.go`](../internal/agent/dispatcher.go) — retires `dispatchSplit`, `shouldSplitForFormat`, `mergeReasoningAndFormat`, the `FormatProvider` interface, and `canonicalFormatterPrompt`. `Dispatch` becomes single-path (`dispatchOnce` only); the ReAct branch (`dispatchReAct`) stays.
    - [`internal/agent/workflow.go:322`](../internal/agent/workflow.go#L322) — `RunWithRetry` call replaced with `dispatcher.Dispatch`. `WorkflowExecutor[S]` gains a `Dispatcher AgentDispatcher` field; nil falls back to `NewDispatcher(e.Executor)`.
    - [`internal/agent/adapters/anthropic.go`](../internal/agent/adapters/anthropic.go) — new `requiresThinkingSchemaSplit(req Request) bool` returning true for Opus 4.7 / Sonnet 4.6 with thinking-on + schema; new internal helper `runSplit(ctx, req, recorder)` that calls the SDK twice (reasoning + format) under separate recorder handles. The native `OutputConfig.Format.Schema` path from DJ-108 stays as the *single-call* path for thinking-off + schema runs and the eventual no-split case.
    - [`internal/agent/adapters/gemini.go`](../internal/agent/adapters/gemini.go) — same shape: `requiresThinkingSchemaSplit` returning true for Gemini 3 Pro Preview / Gemini 3 Flash with thinking-on + schema; `runSplit` that calls `Models.GenerateContent` twice. Today's combined `ThinkingConfig` + `ResponseJsonSchema` request becomes the single-call path used when split isn't needed.
    - [`internal/agent/adapters/openai_responses.go`](../internal/agent/adapters/openai_responses.go) — same shape; covers gpt-5 / gpt-5-mini / gpt-5-nano with thinking-on + structured-output emitting empty `{}`.
    - [`internal/agent/models.yaml`](../internal/agent/models.yaml) — `format_providers:` block removed; each `providers:` entry's existing `fast:` tier is what the adapter's format pass uses.
    - [`internal/agent/session.go`](../internal/agent/session.go) — `LoggingExecutor.Run` reshapes from "record this call" to "open a parent step record; child calls link via context." New `WithParentCallID(ctx, id) context.Context` and `ParentCallIDFromContext(ctx) string` helpers mirror the existing role/agentID/callTag pattern. `SessionRecorder.Begin` takes the parent id; on-disk layout becomes per-step folders. The parent `step.yaml` carries aggregate metadata (role, agent_id, total tokens summed across children, duration from first start to last finish, child call list).
    - Each adapter's `Run` calls `recorder.Begin` / `Finish` around each SDK invocation. Recorder access via `SessionRecorderFromContext(ctx)` — a new context value plumbed by `LoggingExecutor.Run` before delegating.
    - [`internal/agent/spec_search_metrics.go`](../internal/agent/spec_search_metrics.go) and other consumers reading per-call YAMLs (eval tools, replay debuggers) update for the new on-disk layout.
    - Tests: per-adapter `Test<Provider>SplitsForThinkingPlusSchema`, `Test<Provider>NoSplitForThinkingOff`, `Test<Provider>NoSplitForNoSchema`, `Test<Provider>SplitFailurePropagates`. Existing `dispatcher_split_test.go` retires (its assertions move to the per-adapter tests). New `TestSessionRecorderRecordsPerProviderCall` verifies the per-step folder layout. New `TestWorkflowRoutesThroughDispatcher` verifies workflow calls hit `Dispatcher.Dispatch`.
    - The fast-tier formatter reliability eval at [`internal/agent/output_formatter_eval_test.go`](../internal/agent/output_formatter_eval_test.go) updates: each provider's adapter is now exercised through its own format pass; the eval becomes a per-provider matrix rather than a cross-provider rotation.

- **User-visible:**
    - Per-call YAMLs live under per-step folders. Operators reading session traces see the parent `step.yaml` summary first; drill into child call YAMLs for SDK-call detail. `locutus history` and replay tools handle the new layout natively.
    - Token cost surfaces accurately: each child YAML carries its own input/output/thoughts tokens; parent `step.yaml` shows the sum so per-step budgeting is straightforward.
    - OTel traces unchanged in shape; both surfaces (YAML and OTel) now agree on what constitutes a "call."
    - The winplan strategy-drop bug resolves as a direct consequence — Gemini's adapter splits thinking from format, the format pass extracts the full `new_nodes` list including strategies, and the council's narrative-elaborator fires for both feature and strategy nodes.

- **Performance:**
    - Council agents with thinking-on + schema (scout, decision-elaborator, narrative-elaborators, critic-elaborator, reconciler) now make two SDK calls per step instead of one. The naive read is "token cost rises ~25-35%." The honest read is the opposite: the *baseline* of one call was paying full token cost for outputs that silently dropped content — the fifth winplan trace ([`20260521/0055/07-b8e485/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260521/0055/07-b8e485/)) produced a 2-feature 0-strategy spec from agents whose thinking traces drafted strategies that single-call serialization discarded. Tokens spent on that pass produced zero usable output. The split converts ~30% additional spend into the difference between a thin / wrong spec and a complete one; per-token efficiency improves dramatically because the denominator (useful output) goes from near-zero to the actual response. Dollar cost rises less than token count because the format pass uses each provider's fast tier (Haiku / Flash-Lite / gpt-5-mini are 5-10× cheaper per token than the strong tier carrying the reasoning pass).
    - Wall-clock per agent step rises ~20-30% (the format pass adds 1-3 seconds on the fast tier for small responses). Net session time on a 5-iteration winplan run rises ~15-20% — measured against the new baseline of a session that actually produces a complete spec, not against the prior baseline that was failing to converge or shipping incomplete output.
    - Provider rotation for the format pass is no longer cross-provider (no more "Gemini 503 → fall back to Anthropic Haiku"). Each adapter handles its provider's fast-tier transient failures with the same retry shape `RunWithRetry` already applies to single-call paths. The output_formatter_eval saw Gemini Flash-Lite at 3/6 due to 503s; per-adapter retry catches those without crossing providers. If Gemini Flash-Lite reliability proves insufficient under per-adapter retry, the reversal criteria below trigger.

- **Migration:** per [[feedback-no-back-compat-until-self-hosting]], no shim. Per-call YAMLs in existing session directories under the flat naming (`0001-spec_scout.yaml`) are not migrated. Replay tools that walk historical sessions handle both layouts during the transition; new sessions write the per-step folder layout exclusively. `format_providers:` config in users' edited `models.yaml` is silently ignored (no error) for one release, then removed from the parser; the deprecation note lands on the `locutus update` output for that release.

- **Documentation:**
    - `CLAUDE.md`'s LLM section gains a paragraph naming the layering invariant (workflow expresses intent; dispatcher orchestrates; adapter handles provider mechanics including split). Mirrors how the `jsonschema` tag invariant is documented today.
    - `docs/agent-conventions.md` §6 ("Extended thinking on for agents whose output is structured + short") gains a note: the adapter handles this case automatically now; the convention is informational rather than load-bearing for agent authors. Authors still see the failure mode in the convention doc as forensic context.

**Reversal criteria.** Revert if:

- (a) per-provider format reliability under same-provider retry is insufficient. The output_formatter_eval surfaced Gemini Flash-Lite at 3/6 due to transient 503s; today's `format_providers:` rotation crossed to Anthropic Haiku to handle them. If per-adapter retry can't recover from the same shape of transient failure, the format pass effectively fails on Gemini sessions and the council can't ship outputs. Mitigation, in order: extend `RunWithRetry` budget on the format-pass path; raise per-adapter timeout; fall back to a *cross-provider format-pass escape hatch* (a single retry that crosses to another provider, behind an env flag, opt-in) that preserves the layering principle in the common case but restores the rotation in the edge case. Hard reversal — restoring the dispatcher-side rotation — is the last resort.

- (b) provider capability evolution outpaces the static per-adapter overrides. If a new model is released that handles thinking + schema cleanly and `requiresThinkingSchemaSplit` returns true for it by default, the adapter unnecessarily splits and costs token+latency budget. Mitigation: keep the function explicitly per-model rather than per-provider; add a release-cadence check (cron / regression test against `requiresThinkingSchemaSplit` returning the *needed* answer per model). Hard reversal not necessary — flip the function's return for the affected model.

- (c) the recorder restructure introduces observability regressions. Per-step folder layout breaks downstream replay tools, log shippers, or operator scripts in ways we don't catch pre-merge. Mitigation: ship the layout change behind a feature flag (`LOCUTUS_TRACE_LAYOUT=per-step|flat`, default `per-step` after a one-release transition) so a regression can be reverted per-session without redeploying. Hard reversal — flat naming with parent_call_id on a metadata field — is the fallback if per-step folders prove materially worse.

- (d) the workflow routing through `Dispatcher.Dispatch` introduces latency or cost regressions we didn't anticipate. The dispatcher's provider rotation and corrective retry on degenerate output were previously bypassed by `RunWithRetry`; once wired in, they may fire more often than expected and inflate per-step latency / cost. Mitigation: cap `DispatchOptions.MaxAttempts` and `CorrectiveRetries` from the workflow side at known-safe values; observe in real session traces; tune.

- (e) the adapter-encapsulated split obscures a real debug case. Operators investigating a council failure can't determine from per-call YAMLs alone whether a split fired and which sub-call produced what content. Mitigation: parent `step.yaml` lists child call ids with their roles (`reason`, `format`); each child carries `parent_call_id` and `role`; the trace renderer (any) walks them as a tree. If this proves insufficient in practice, the parent `step.yaml` gains a `narrative` field summarizing what happened in prose for operator readability.

**Reference.** Aligns with [DJ-108](#dj-108-anthropic-native-structured-output--adaptive-thinking) — provider-specific mechanics belong in the adapter; this DJ extends the principle from structured-output handling to thinking+schema split handling. Supersedes the unrecorded shipping behavior of commit [`5d15e7b`](https://github.com/chetan/locutus/commit/5d15e7b) (2026-05-13, "feat(council): reason-then-format split for thinking-on schema agents") which placed the split at the Dispatcher layer without a DJ entry. Closes the wiring gap that DJ-122 ([Graph-Mutation Workflow Executor](#dj-122-graph-mutation-workflow-executor-with-spawner-nodes-supersedes-dj-112-on-control-flow-topology)) introduced — the spawner-driven WorkflowExecutor was authored the day after `5d15e7b` and bypassed `Dispatcher.Dispatch` for ~7 days while the split sat unused for the spec-generation pathway. Honors [DJ-099](#dj-099-direct-sdk-llm-adapters-per-provider-supersede-genkit) — the direct-SDK adapter substrate is the layer where provider-specific knowledge belongs. Honors [DJ-118](#dj-118-invopop-jsonschema-replaces-google-jsonschema-go-throughout) — no schema changes; jsonschema discipline carries forward. Motivated by the fifth winplan re-run trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260521/0055/07-b8e485/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260521/0055/07-b8e485/) and the chat 2026-05-21 discussion of LCD vs. adapter-encapsulation tradeoffs.

## DJ-131: Phase-Fanout Collapse for ACP-Driven Council Steps (Extends DJ-127 With N-Per-Session Coding-Agent Orchestration; Aligns Subscription-Quota Math With Per-Refine Fanout Volume)

**Status:** proposed (depends on [DJ-127](#dj-127-spec-mutation-tools-as-mcp-write-surface-acp-driven-decision-elaboration-aligns-council-transport-with-subscription-based-ai-funding-model) shipping first)

**Context.** [DJ-127](#dj-127-spec-mutation-tools-as-mcp-write-surface-acp-driven-decision-elaboration-aligns-council-transport-with-subscription-based-ai-funding-model) reframes council transport around subscription-funded coding-agent sessions: decision-elaboration becomes ACP-driven, with the spec mutation tools as the write surface. The funding-model argument is the load-bearing rationale — Claude Max and similar subscriptions cover unlimited (within quota) coding-agent invocations, so the cheap unit of work is "one session," not "one API call." DJ-127 as drafted dispatches one ACP session per agent fanout slot — one session per axis for decision-elaboration, one per critique dimension for the critic, one per feature/strategy for narrative elaboration.

That session arithmetic blows the quota math on real refines. A moderate-complexity council runs 4–8 axes per iteration × 5 iterations + 3–5 critique dimensions × 5 iterations + 2–8 feature/strategy elaborations × 5 iterations = **80–200 sessions per `locutus refine goals`**. Claude Max's 5-hour rolling quota sizes for human-scale workflows (~50 Sonnet sessions or ~10 Opus sessions per window); a single refine would exhaust the daily allocation and block the user's other coding-agent work for hours. The cost/DX win DJ-127 promises evaporates exactly when the user invokes the council seriously.

The structural reason is that DJ-127 treats per-agent-per-axis dispatch as load-bearing — a faithful translation of today's direct-SDK fanout pattern into coding-agent sessions. But coding agents are themselves *parallelism runtimes*: Claude Code's Task() primitive spawns parallel sub-agents that share the parent's context, runs them concurrently, and aggregates results back into the parent's flow. Telling Claude Code "decide these 5 independent axes" causes it to launch 5 Task() research sub-agents in parallel and commit 5 decisions from one session. Telling Claude Code "decide axis 1" five separate times burns five session-spawn costs while leaving the agent's native parallelism unused. DJ-127's per-axis-per-session shape pays for orchestration the coding agent already does internally.

The reframe: **what locutus dispatches under ACP isn't an agent invocation, it's a plan with N independent phases.** The coding agent reads the plan, decides whether to parallelize via Task() or process serially, commits N results via N MCP tool calls (each carrying its phase id), and closes the session. One session, N commits, native parallelism inside.

**Why this surfaced now.** Drafting DJ-127's plan exposed the quota math. The first-cut numbers (one session per axis × typical fanout breadth × iteration count) put a single council run past the daily Claude Max quota on first invocation. The funding-model argument that drives DJ-127 specifically fails under DJ-127's per-axis dispatch shape — the user gets the worst of both worlds (subscription-quota friction *and* per-session latency multiplier). The phase-fanout collapse pattern was discussed in chat 2026-05-21 as the structural fix: keep DJ-127's transport, change its dispatch granularity to match the coding agent's natural unit of work.

**Decision.** Three coupled changes shipped together as DJ-131, on top of DJ-127's substrate.

1. **`FanoutMode` step-level flag on `WorkflowStep`.** Two modes:
    - `FanoutSlot` (default; today's behavior): N items dispatch as N independent agent calls; per-item snapshot isolation, per-item retry, per-item trace YAML. Used when items have no cross-axis coherence value (e.g., parallel critique-dimension dispatch where each lens is genuinely independent) or when per-item failure isolation is load-bearing.
    - `FanoutPhase` (new; ACP-only): N items render into one markdown plan; one ACP session opens; the coding agent commits N results via per-phase tool calls. Used when items share substantial research context, when cross-axis coherence improves the result, or when subscription-quota arithmetic demands the amortization.

    The mode is workflow-author choice, not a runtime heuristic. Step authors know whether the N items are coherent-better-together or independent-better-isolated; encoding that intent on the step is cleaner than per-call auto-detection.

2. **Plan-rendering + per-phase staging at the dispatch boundary.** The agent-layer dispatch primitive for `FanoutPhase` steps:
    - Renders all N items into a structured markdown plan: `## Phase N: <id>\n<per-item context>\n<commit instruction>`. The render template lives in the agent's `.md` frontmatter alongside the existing `transport: acp` setting; agents declare which fields project into the phase template.
    - Opens one ACP session against the bound coding agent. Session capability scopes to `phase_id ∈ {N phase ids}` on the relevant write tool (e.g., `spec_propose_decision.axis_id` constrained to the N axis ids surfaced this iteration). The MCP boundary rejects tool calls targeting phase ids outside the bound set.
    - Session staging area (introduced by DJ-127) keys tool calls by their phase id. When the session closes, the merge function pulls N typed results out of staging by phase id and returns them as a `[]RoundResult` of length N — byte-identical shape to today's slot-fanout result.

    The coding agent is free to use Task() for parallel per-phase research, or serial, or any mix. Locutus doesn't see (or care about) the internal orchestration; it sees one ACP session yielding N tool calls.

3. **Partial-result handling preserves per-phase failure isolation in spirit.** A session-level failure (timeout, quota exhaust, MCP rejection on one tool call, coding-agent error) doesn't necessarily mean all N phases are lost. The session staging area preserves whatever was committed before the failure. The merge function returns `[]RoundResult` of length N with explicit per-slot success/failure status. The workflow handles the partial-failure case by:
    - **Slot-failure retry** for missing phases: fall through to a follow-up `FanoutSlot` dispatch (one ACP session per missing axis) on the next iteration.
    - **Convergence-loop natural retry**: missing phases re-surface as unresolved axes on the next scout iteration; the workflow's existing convergence mechanism re-dispatches them.
    - **Hard failure** only when the session failed before *any* tool call landed (no partial progress to preserve).

    This preserves DJ-122's per-slot failure-isolation guarantee at the workflow boundary even though the underlying dispatch collapsed N slots into one session.

**Resolved design questions** (settled in chat 2026-05-21):

1. **Phase fanout doesn't replace slot fanout; both modes coexist as a workflow-author choice.** The two patterns address different concerns. Phase fanout amortizes session cost when items share research context or coherence value. Slot fanout preserves per-item attribution and isolation when items are genuinely independent. The `FanoutMode` flag is the orchestration choice point; the default for ACP-routed steps with cross-coherent items is `FanoutPhase`, for genuinely-independent items is `FanoutSlot`. Direct-SDK steps always behave as slot fanout (the session-amortization argument doesn't apply).

2. **The coding agent owns local parallelism via its own primitives; locutus doesn't orchestrate within a session.** Claude Code's Task() runtime is what makes phase fanout cheap; pushing per-phase dispatch back into locutus would defeat the amortization. The plan template can suggest parallelism ("phases are independent; use Task() to research them concurrently") but the actual decision is the agent's. This is the inversion of the DJ-127 model: locutus stops orchestrating fanout dispatch under ACP and lets the coding agent be the local runtime.

3. **Per-phase attribution lives on the tool-call payload.** Every write tool that takes a phase-keyed input (e.g., `spec_propose_decision.axis_id`, `spec_propose_new_node.id`) becomes self-attributing. The staging area keys commits by that field; the trace renderer pulls per-phase views over the combined session transcript. Operators see one step folder per phase-fanout call containing the session transcript plus N per-phase result YAMLs extracted from the staging area — the per-axis forensic clarity slot fanout has today, recovered from a different surface.

4. **Capability scoping is coarser but still bounded.** A phase-fanout session binds to the *set* of N phase ids, not a single one. The MCP boundary enforces "this session's `spec_propose_decision` calls must carry `axis_id` from this set"; calls with other axis ids are rejected. Less precise than DJ-127's per-session-per-axis binding, but still prevents accidental writes outside the dispatched set. The agent could theoretically write to the wrong axis within the set (e.g., commit the auth-provider decision under the compute-platform axis id); the validation surface is "set membership," not "axis matches commit content." Mitigation: tighten via tool-call validation in the staging area (cross-check commit content against axis_id description); accept that this is a softer guarantee than today's slot-isolated dispatch.

5. **Context budget is per-agent and the framework respects it.** A phase-fanout dispatch with N axes accumulates context for N axes worth of source evidence, surfaced_by lookups, and prior-round findings. For coding agents with bounded context (Claude Code at ~200k effective working tokens), N=4–6 axes is comfortable; N=15+ would push past quality thresholds. Step authors set a per-step `MaxPhases` cap (default: 8 for decision-elaboration, 6 for narrative-elaboration); when N exceeds the cap, the dispatch *partitions* into multiple phase-fanout sessions (e.g., 12 axes → two sessions of 6 each). Partitioning preserves the per-session amortization while keeping each session's context bounded.

6. **Workflow executor stays outer-orchestrator.** The DAG, spawners, convergence detection, budget caps, conditional dispatch all remain at the workflow layer (DJ-122's structural guardrails). The coding agent owns LOCAL orchestration within one phase-fanout dispatch only. A phase-fanout dispatch IS one workflow step; the convergence loop and spawner callbacks still run between dispatches. This preserves DJ-127's rejection of "coding agent as supervisor" while letting specific steps benefit from coding-agent local orchestration.

7. **Slot fanout remains the safe default; phase fanout opt-in per step.** Migration to phase fanout for any specific step is a workflow-author judgment call — the step author weighs "would these N items benefit from one-session coherence?" against "would per-item isolation matter more here?" Decision-elaboration is the canonical phase-fanout case (axes are coherent enough that cross-decision context helps and isolated enough that the coding agent can decide each on its merits). Critique-dimension dispatch is more borderline (each lens is genuinely independent; phase fanout's coherence benefit is small).

**Alternatives considered.**

- **Stay DJ-127-pure (per-axis per-session).** The conservative path: ship DJ-127 as drafted; live with the quota friction; let users batch refines across days. Rejected because the funding-model argument that justifies DJ-127 fails when subscription quotas get exhausted by a single refine. The phase-fanout collapse is what makes DJ-127's promise actually deliver.

- **One ACP session for the entire council.** Maximum amortization: spawn one Claude Code session at `locutus refine goals` start, hand it the whole council workflow as a plan, let it iterate to convergence. Rejected for the same reason DJ-127 rejected coding-agent-as-supervisor: structural guardrails (convergence detection, iteration budget, spawner callbacks, cycle detection) live in the workflow executor and don't reduce to prose for the coding agent to enforce. Phase fanout is the calibrated middle ground — coding-agent orchestration at the fanout-step grain only.

- **Auto-detect FanoutMode at runtime via heuristics.** Workflow executor inspects fanout items, decides slot vs phase based on item count, item similarity, transport availability. Rejected because step authors have intent information runtime heuristics don't: "these axes are coherent" is a domain claim, not a structural property visible to the executor. Explicit `FanoutMode` flag on the step keeps the choice with whoever knows.

- **Inline the plan in the agent's system prompt.** Render the N-phase plan as part of the agent's `.md` system prompt rather than as user-message content. Rejected because the agent's system prompt is the *role* description ("you are a decision-elaborator"); the plan is the *task* ("decide these specific 4 axes today"). Mixing role and task in the system prompt breaks the per-iteration cacheability the agent's prompt enjoys (DJ-130 cache layering) — the per-iteration plan content would invalidate the system-prompt cache prefix on every dispatch.

- **Phase fanout under direct-SDK transport too.** Conceptually, locutus could render the N-phase plan as a user message under direct-SDK and have a single model call commit N tool calls. Rejected because direct-SDK structured output is one-shot per call (no tool-call loop equivalent to ACP's session); collapsing N items into one direct-SDK call would either require N×structured-output (matches today) or one giant structured output with N sub-objects (loses the per-axis attribution and corrective-retry granularity DJ-130 designed for). Phase fanout's amortization argument is specifically about session-spawn cost, which only direct-SDK lacks.

- **Make phase-fanout the only ACP mode (no slot option).** Simpler model: ACP transport implies phase fanout always. Rejected because critic-dimension fanout doesn't benefit from one-session coherence — each lens is independently graded against its own focus question; cross-lens context is more noise than signal. Phase fanout has costs (coarser capability, harder per-axis forensics); the workflow author should be able to opt out where the benefit doesn't justify the cost.

**Consequences.**

- **Code:**
    - `internal/agent/workflow.go` — `WorkflowStep[S]` gains `FanoutMode FanoutMode` and `MaxPhases int` fields. `ExecuteRound`'s fanout dispatch grows a branch on `FanoutMode`: `FanoutSlot` runs today's per-item dispatch path; `FanoutPhase` calls the new ACP phase-dispatch primitive.
    - `internal/dispatch/acp/phase_dispatch.go` (new) — renders the per-step plan template, opens one ACP session with capability scoped to the N phase ids, polls the session staging area, returns `[]RoundResult` of length N (with per-slot success/failure status) when the session closes.
    - `internal/dispatch/acp/plan_render.go` (new) — markdown plan template engine; reads the agent's frontmatter to know which fanout-item fields project into the plan's per-phase context blocks.
    - `internal/mcp/staging.go` (DJ-127's staging surface) — extended to key commits by phase id and to expose per-phase result extraction.
    - `internal/mcp/capability.go` (DJ-127's capability surface) — extended to bind a session to a *set* of phase ids rather than a single id; tool-call validation rejects out-of-set commits.
    - `internal/agent/workflow_spec_generation*.go` — decision-elaboration step gains `FanoutMode: FanoutPhase, MaxPhases: 6` (cross-axis coherence value justifies phase mode; cap at 6 keeps context bounded). Critique-dimension step stays `FanoutSlot` (lens independence). Narrative-elaboration is `FanoutPhase` only when fanout breadth >2 (two features in one session is borderline; six is clearly worth it).
    - `internal/scaffold/agents/spec_decision_elaborator.md` — frontmatter gains `plan_template` block defining how per-axis fields render into the phase plan. Prompt body adjusts to assume "you receive a multi-phase plan; commit each phase via `spec_propose_decision`."
    - Tests: per-phase capability rejection (in-set commits succeed; out-of-set commits reject); partial-result handling (session fails after 3 of 5 commits; merge returns 3 successes + 2 failures); per-phase trace extraction (one step folder contains session transcript + N per-phase result YAMLs); fall-through to slot-fanout retry (partial-failure follow-up dispatches one session per missing axis on next iteration).

- **User-visible:**
    - Subscription-quota math: a moderate-complexity refine drops from 80–200 sessions to 15–35 sessions (5× reduction), comfortably fitting in a single Claude Max 5-hour window.
    - Per-step trace folders for phase-fanout dispatches contain the combined session transcript (one big markdown/jsonl file) plus N per-phase YAML extracts. Per-axis forensic clarity preserved; layout differs from slot-fanout's flat per-axis YAML list.
    - Phase-fanout dispatch latency: one session takes 2–10 minutes (DJ-127's per-session cost) regardless of N. Slot fanout takes 2–10 minutes per item, sequentially or with concurrency caps. Net: phase fanout is dramatically faster wall-clock on N>2 fanouts.
    - Partial-failure surface: progress UI shows "decision-elaboration: 3 of 5 phases committed; 2 axes will re-dispatch next iteration" instead of "1 of 5 elaborators errored; will retry."

- **Performance:**
    - Session count per refine: 5× reduction on canonical workloads (validated empirically; design target).
    - Per-session token use grows ~N× (one session processes N axes' worth of context). Coding-agent context budgets accommodate this up to MaxPhases per step.
    - Coding-agent native parallelism via Task() means within-session wall clock is ~max(per-phase research time), not sum. A 5-axis phase dispatch with parallel research takes ~one axis's research time, not 5×.

- **Migration:** per [[feedback-no-back-compat-until-self-hosting]], no shim. `FanoutMode` defaults to `FanoutSlot` on existing workflow definitions (today's behavior preserved). Phase-fanout adoption is per-step opt-in. The default per-step `MaxPhases` cap is conservative (6); operators tune up where coding agent quality holds at higher fanout breadth.

- **Documentation:**
    - CLAUDE.md gains a paragraph on `FanoutMode` as a workflow-author concept: when to use slot vs phase, the coherence-vs-isolation trade-off, the quota arithmetic that drives phase as the default for ACP-routed elaboration.
    - `docs/debugging-traces.md` (DJ-130's guide) gains a section on phase-fanout trace layout: per-step folder with combined session transcript + per-phase YAMLs; how to navigate from a per-phase YAML back to the originating section of the session transcript via phase id.

**Reversal criteria.** Revert if:

- (a) coding agents don't actually parallelize via Task() in practice. If Claude Code, given a 5-phase plan, processes phases serially despite the plan template suggesting parallelism, the wall-clock argument for phase fanout collapses (one session × serial-N-phases = N × one-axis time, same as slot-fanout sequentially). Mitigation: explicit `<parallel>...</parallel>` block in the plan template; per-phase budget in the plan ("each phase should take ≤2 minutes; the session as a whole ≤8 minutes" forces the agent to parallelize). Hard reversal: drop to slot fanout under ACP and live with the quota arithmetic.
- (b) per-phase quality degrades when many phases share one session. Anchoring or priming from earlier phases biases later phases (e.g., committing AWS for compute primes weighing AWS-coupled options heavier for unrelated axes). Surfaces as critic findings post-DJ-126 showing higher rates of cross-axis coupling than slot-fanout-produced decisions. Mitigation: lower MaxPhases (force tighter context isolation); per-phase prompt sentinels reminding the agent that phases are independent; if quality still degrades, decision-elaboration reverts to `FanoutSlot` under ACP — losing the quota efficiency but recovering quality.
- (c) partial-result handling proves operationally complex. Session-level failures preserving N–k commits sound clean on paper but the merge function's per-slot success/failure handling, the workflow's fall-through-to-slot-retry logic, and the trace renderer's partial-success display together accumulate enough surface area to outweigh the benefit. Mitigation: granular session checkpointing primitives (out of scope for initial landing); fall back to "phase fanout is all-or-nothing; partial failure forces full retry" with the workflow's natural convergence-loop re-dispatch absorbing it.
- (d) per-phase attribution within combined sessions is too lossy for forensics. Operators investigating "why did this axis come out that way" can't navigate from per-phase result to the relevant section of a 100KB session transcript. Mitigation: structured session transcript format (jsonl of agent turns + tool calls keyed by phase id) rather than free-form markdown; trace renderer extracts per-phase windows by phase id. If structural extraction can't recover slot-fanout's per-axis forensic clarity, the operational cost may outweigh the quota benefit and decision-elaboration reverts to `FanoutSlot`.
- (e) `MaxPhases` partitioning produces worse outcomes than uniform slot fanout. A 12-axis dispatch partitioned into two 6-axis sessions might produce inconsistent results across the partition boundary (different anchoring, different cross-axis weighting). Mitigation: partition strategy chooses semantically-coherent groupings (axes that share source evidence go in the same session); accept some loss of cross-session coherence vs the per-session-context-budget trade-off.

**Reference.** Depends on [DJ-127](#dj-127-spec-mutation-tools-as-mcp-write-surface-acp-driven-decision-elaboration-aligns-council-transport-with-subscription-based-ai-funding-model) shipping first — phase fanout reuses DJ-127's write-tool surface, session staging, capability scoping, locking, and ACP spawner. Extends [DJ-127](#dj-127-spec-mutation-tools-as-mcp-write-surface-acp-driven-decision-elaboration-aligns-council-transport-with-subscription-based-ai-funding-model)'s funding-model argument with the quota-arithmetic correction: DJ-127's per-agent-per-session dispatch exhausts the quota DJ-127 designs around; phase fanout makes the quota math close. Preserves [DJ-122](#dj-122-graph-mutation-workflow-executor-with-spawner-nodes-supersedes-dj-112-on-control-flow-topology)'s workflow executor unchanged structurally — only the per-step dispatch primitive grows a `FanoutMode` branch. Preserves [DJ-122](#dj-122-graph-mutation-workflow-executor-with-spawner-nodes-supersedes-dj-112-on-control-flow-topology)'s per-slot failure isolation in spirit via partial-result handling at the dispatch boundary. Aligns with [DJ-130](#dj-130-provider-mechanics-encapsulated-in-adapters-trace-recording-follows-the-provider-call-boundary-supersedes-unrecorded-5d15e7b-split-in-dispatcher-pattern)'s per-step folder trace layout — phase-fanout dispatches produce one step folder containing the combined session transcript + N per-phase YAMLs, the same parent-step-with-N-children shape DJ-130 introduced for split-pass calls. Motivated by the chat 2026-05-21 quota-arithmetic analysis on DJ-127's per-agent-per-session model.

## DJ-132: Candidate-Survey Agent for Decision Elaboration (Separates Enumeration from Judgment to Compress Initial Option Space; Extends DJ-124 Decision-Elaborator Workflow)

**Status:** proposed

**Context.** The sixth winplan re-run (`/Users/chetan/projects/winplan/.locutus/sessions/20260521/1958/24-20bf24/`) validated that the alternatives-merge-layer fix landed in commit [`ef2d209`](https://github.com/chetan/locutus/commit/ef2d209) eliminated the DJ-128-monotonicity oscillation: `dec-supabase-postgres-persistence` accumulated 17 alternatives across 5 revises without dropping any. The deliberation log finally compounds productively rather than churning. **But the trace surfaced a different inefficiency that the alternatives-drop pattern had been masking**: the initial decision-elaborator dispatch systematically *compresses* the option space.

Concrete evidence from the same trace:

- **compute-platform initial dispatch (0003)**: the reasoning block actively considered Vercel, Render, Railway, EKS, Fargate, and GCP (6 candidates). The committed response emitted Railway + AWS as 2 alternatives. Four candidates that the model had already weighed went unexpressed.
- **identity-and-access-control initial dispatch (0002)**: reasoning considered Auth0, Clerk, Supabase Auth (3 candidates); response emitted Auth0 + Supabase Auth (2 alternatives). Clerk — which the eventual decision picked after 3 revises — was thought about but not surfaced upfront.
- **data-persistence-strategy initial dispatch (0004)**: returned empty (`gemini split reason: empty response` on the balanced-tier Gemini); persistence axis never got a clean first commitment at all.

The pattern is mechanical: decision-elaborator's initial commit-mode emits 1-2 alternatives even when its own reasoning has surfaced 4-6 candidates. The 17 alternatives that eventually accumulate are the COMPENSATORY work of 5 critic-revise rounds, each round having the critic surface 2-3 candidates the elaborator missed and the next revise engaging with them. **The deliberation log is durable now, but its construction is wasteful** — each revise is another strong-tier grounded LLM call (~$0.30-0.60), wall-clock 1-3 minutes, and a structural cost on the convergence loop's iteration budget.

The root cause is at the elaborator's task framing. Three layered drivers, none unique to balanced Gemini:

1. **The prompt's mandate is permissive.** `RawDecisionProposal.alternatives` carries `minItems=1`; the elaborator's prompt asks for "the candidates a reasonable architect would weigh." Both anchor low: "at least one" sets the floor; "reasonable architect would weigh" reads as "the 2-3 you'd put in a decision doc."
2. **Training-data norms reinforce 2-3 alternatives.** Architectural decision records, RFCs, and design docs across the LLM training corpus conventionally surface 2-3 alternatives. The model treats this as the expected output genre regardless of schema description.
3. **Decision-task and enumeration-task are conflated.** "Pick X and weigh alternatives" puts the model in commit-mode; commit-mode prioritizes the chosen path's rationale, treats alternatives as brief "show your work" supporting evidence, and spends attention budget on justification rather than exhaustive option-space exploration.

The result is critics doing the enumeration work the elaborator should have done upfront. Each revise round's critic finds another 2-3 obvious candidates the elaborator overlooked. The architecture pays a strong-tier revise call for every candidate the initial elaboration missed — a regressive cost structure where every option the model could have named upfront for free costs $0.30-0.60 to surface after the fact.

**Why this surfaced now.** Until commit `ef2d209` (2026-05-21), the alternatives-drop pattern dominated the trace signal: revises mutated and lost the alternatives list across iterations. The mechanical-preservation fix eliminated that noise floor. Once revises started compounding productively, the *upstream* compression at initial dispatch became the chief remaining inefficiency. The sixth winplan re-run is the first trace where the compression pattern shows up cleanly without the drop pattern competing for attention.

**Decision.** Two coupled changes shipped together as DJ-132.

1. **New `spec_candidate_survey` agent runs per-axis BEFORE the decision-elaborator** on the initial-elaboration path. Fast tier (Haiku 4.5 / Flash-Lite / gpt-5-mini); grounded (`grounding: true`); output is a `CandidateList` of 6-10 enumeration entries. Each entry carries a name + one-sentence first-glance fit, no rationale, no citations, no judgment. The agent's only job is *enumeration*: search the web for current candidates in the axis's space (`"managed Postgres alternatives 2026"`, `"identity providers with RBAC"`), filter to viable + current options (drop discontinued / pivoted-away vendors), surface the list. Grounding is load-bearing: training-data-only enumeration produces hallucinated vendors (PostgresPro, AcmeDB) and stale candidates (Heroku free tier, Parse pre-acquisition); web search forces every entry to resolve to a real, current URL.

2. **Decision-elaborator's initial dispatch input gains the candidate list.** A new `Candidate List` section in the projected input enumerates the surveyed candidates; the elaborator's prompt revises to assume "you receive a candidate list pre-surveyed for this axis; pick from it + author proper rationale + write rejected_because for each unpicked + cite each." The elaborator's `alternatives` slice starts populated from the survey rather than empty. The elaborator retains grounding for per-candidate deep-dive verification (vendor docs, current pricing, capability claims) — different use of web search from the survey's broad enumeration.

The revise path is unchanged: revises engage with critic findings and the existing alternatives list (now substantially richer on initial commit). The critic's enumeration work shrinks because most obvious candidates are already on the alternatives slice from the survey; critics focus on structural concerns (cross-decision incoherence, math problems, missed compliance requirements) that genuinely need critic perspective.

**Resolved design questions** (settled in chat 2026-05-22):

1. **Survey runs on initial dispatch only, not on revises.** Revises engage with critic-surfaced candidates that may extend beyond the survey's enumeration. The survey's purpose is to compress the initial commit's option space, not to gate the convergence loop. A revise dispatch carrying critic findings goes to the elaborator directly with the prior decision's alternatives slice + the critic counterproposals; no second survey call.

2. **Survey uses fast tier + grounding.** Enumeration is a discovery task LLMs do well at fast tier; web search prevents hallucinated/stale entries. Spending strong tier on enumeration would waste budget on a task that doesn't need deep reasoning. The fast-tier survey call (~$0.03-0.08) amortizes against avoided strong-tier revise calls (~$0.30-0.60 each); typical break-even is one avoided revise per axis.

3. **Survey output is flat — no judgments, no rationale, no citations on entries.** Just name + one-sentence first-glance fit ("Postgres with PostGIS — mature geospatial extension, team familiarity"). Judgment is the elaborator's job; mixing into the survey would re-create the enumeration/judgment task conflation the survey exists to break.

4. **The elaborator's grounding stays.** Survey and elaborator do different things with web search: survey searches broadly for vendor enumeration (`"X alternatives 2026"`); elaborator searches deeply per candidate (`"Vercel function concurrency limits documentation"`). Both legitimately need grounding; the cost is justified by different work products.

5. **Survey input includes axis + GOALS.md + existing spec snapshot.** GOALS provides project-specific constraints that filter out non-viable candidates upfront (cost ceilings, scale requirements, compliance regimes). Existing spec lets the survey weight already-committed-elsewhere options higher (if the project already chose AWS for compute, surface AWS-native database candidates first). Scout brief's `technology_options` for this axis (when present) seed the survey with the scout's initial framing.

6. **CandidateList schema carries `minItems=3`, target 6-10.** The schema floor is conservative because some axes are genuinely narrow (custom domain-specific tooling with few real candidates). The prompt aims for 6-10 on well-trodden axes (databases, frontend frameworks, auth, observability) and 3-5 on specialized axes. Padding-prevention is in the prompt ("only enumerate candidates you actually find via search; don't invent to hit a count"), not the schema.

7. **Survey runs in parallel with sibling decision-elaborators.** Today's spec-generation workflow dispatches decision-elaboration via fanout (one elaborator per `axes_open` entry). Survey adds one fast-tier call per axis before each elaborator; the workflow runs surveys + elaborators per-axis in parallel within the existing fanout step. No serial bottleneck added.

**Alternatives considered.**

- **Tighten the elaborator's prompt for breadth-of-enumeration in-place.** Rewrite the alternatives section to push for 6-10 entries explicitly; reorder the elaborator's task to do enumeration first then commit. Cheaper (no new agent); cleaner than two-agent dispatch. Rejected because it re-conflates the two cognitive tasks the new architecture exists to separate — the elaborator's commit-mode attention still crowds out exhaustive enumeration, just with more pressure to fake breadth. The expected outcome is "elaborator surfaces 4-6 padded alternatives instead of 2 well-justified ones" — wrong tradeoff.

- **Raise `RawDecisionProposal.Alternatives` minItems from 1 to 5 via schema enforcement.** Provider strict-mode rejects responses with fewer than 5 alternatives, forcing the elaborator's hand. Crude but immediate. Rejected because it produces padding without quality improvement: the model fabricates straw-man alternatives ("MySQL — rejected because it's not Postgres") to satisfy the count, exactly the failure the original "single weak straw-man defeats the purpose" mandate was trying to prevent.

- **Two-phase elaboration within one elaborator call.** The elaborator prompt explicitly walks the model through "first enumerate candidates, then commit." Same agent, structured task. Rejected because the same context window holds both passes and the commit-pass compression still crowds out the enumeration-pass content when the model assembles the final response. The DJ-130 split exists precisely because two cognitive tasks in one LLM call don't get equal attention; this is the same failure mode at a smaller scale.

- **Devil's-advocate critic before elaborator.** A pre-elaborator critic looks at the axis description and brainstorms "candidates the elaborator might miss"; the elaborator gets the brainstorm as input. Functionally similar to the survey but framed adversarially. Rejected because the adversarial framing implies a relationship between two roles that don't have one (the survey isn't critiquing the elaborator; it's enabling the elaborator). The cognitive frame matters: an enumeration agent does enumeration well; a "critic-of-future-elaboration" does something murkier.

- **Critic specialization on candidate-surfacing.** Restructure DJ-129's dimension-driven critics so one critic role specifically does candidate-enumeration on every decision (an "options critic"). Rejected because critics are designed to challenge what the elaborator commits to, not to do work the elaborator should do upfront. Mixing the roles weakens both — critics become enumeration-shopkeepers, elaborators get lazy about initial enumeration knowing the critic will fill the gaps. Better to keep the role separation clean.

**Consequences.**

- **Code:**
    - [internal/agent/specgen.go](../internal/agent/specgen.go) — new `CandidateList` Go type with `Candidates []SurveyedCandidate` and `SurveyedCandidate{Name string, FirstGlanceFit string}`. Registered via `RegisterSchema` per the jsonschema-tag invariant; `minItems=3` on the slice; descriptive prose example payload.
    - [internal/scaffold/agents/spec_candidate_survey.md](../internal/scaffold/agents/spec_candidate_survey.md) (new) — agent prompt following the convention discipline (push constraints to schema tags; no JSON-mode priming language; explicit "enumerate, don't judge" framing). Fast-tier model preferences, `grounding: true`, `output_schema: CandidateList`.
    - [internal/agent/workflow_spec_generation_dj124.go](../internal/agent/workflow_spec_generation_dj124.go) — decision-elaboration fanout step gains a per-axis pre-step that runs the survey; survey output threads into the decision-elaborator's projection as a `Candidate List` section in the input.
    - [internal/agent/projection.go](../internal/agent/projection.go) — decision-elaborator input projection grows a `Candidate List` block when survey results are present (initial dispatch); revise dispatches skip the block (survey didn't run; alternatives slice already populated from prior).
    - [internal/scaffold/agents/spec_decision_elaborator.md](../internal/scaffold/agents/spec_decision_elaborator.md) — prompt revises to assume the candidate list when present. Initial-dispatch section: "pick from these N candidates + author rationale + write rejected_because per unpicked." Revise section unchanged (engages with critic findings + prior alternatives via merge).
    - Tests: `TestCandidateSurveyEmitsFlatList`, `TestSurveyOutputThreadsIntoElaboratorInput`, `TestRevisePathSkipsSurvey`, `TestSurveyEmptyFallsThroughToElaboratorWithEmptyAlternatives`. Existing decision-elaborator tests update for the candidate-list input.

- **User-visible:**
    - **Convergence accelerates on canonical workloads.** Average revises per axis drops from 3-5 to 1-2 because most candidates are on the alternatives slice from initial commit; critic enumeration work shrinks. Net refine wall-clock drops 20-40% on moderate-complexity projects; council convergence-rate climbs (more refines converge within budget rather than exhausting iteration cap).
    - **Initial decisions ship richer.** First-author decisions carry 6-10 alternatives instead of 1-2; deliberation log starts substantial rather than emerging across iterations.
    - **Per-step trace folders gain a new shape.** Per-axis decision dispatch now contains `<step>-<agent>-candidatesurvey-<axis>/` (the survey) followed by `<step>-<agent>-decisionelaborator-<axis>/` (the elaborator). Operators reading traces see the survey's enumeration explicitly before drilling into the elaborator's commit.
    - **Cost per axis on initial elaboration rises ~30-50%.** Survey adds ~$0.03-0.08 (fast tier, grounded). Elaborator cost unchanged. Net per-refine cost drops because reduced revise count amortizes (typically 2-3 avoided strong-tier revise calls per axis at $0.30-0.60 each).

- **Performance:**
    - Net token cost per axis-settled drops ~20-40% on canonical workloads (survey overhead < avoided revise overhead).
    - Net wall-clock per refine drops 20-40% (fewer iterations to settle each axis).
    - Quota arithmetic for subscription users improves further (fewer sessions per refine across the council loop — complementary to DJ-131's phase-fanout collapse).

- **Migration:** per [[feedback-no-back-compat-until-self-hosting]], no shim. Survey + elaborator dispatch pattern lands forward; existing persisted decisions stay in their shape. Operators who edit agent .md files don't need to migrate anything (new agent appears; existing agent prompts update).

- **Documentation:**
    - `CLAUDE.md` gains a paragraph on the enumeration-vs-judgment separation as a council architecture principle (parallel to DJ-130's reasoning-vs-formatting separation). Both are instances of "when one LLM call is asked to do two cognitive tasks that conflict in the attention budget, separate them."
    - `docs/agent-conventions.md` gains a section on the "enumeration agent" pattern: when an agent's job is exhaustive option-surfacing, the prompt explicitly frames the task as enumeration (not judgment); output schema is flat (no rationale/citations/judgments on entries); grounding is load-bearing for currency + hallucination prevention.
    - `docs/council.md` updates: workflow diagram gains the per-axis `spec_candidate_survey` pre-step before the `decisions` fanout (one survey call per axis, in parallel with sibling surveys, then per-axis sequential into the elaborator); pending-agents section's `spec_candidate_survey` entry moves up into the main per-agent reference block as a full entry alongside `spec_decision_elaborator` (covering output schema, model tier, thinking, grounding, governing DJ, design notes).

**Reversal criteria.** Revert if:

- (a) survey doesn't reduce average revise count meaningfully. The next winplan re-run trace shows survey added overhead without compressing the convergence loop. Mitigation: examine whether the survey is producing high-quality enumerations or weak/padded ones; tighten the survey prompt for breadth-quality. If the survey runs cleanly but critics still surface 3-5 candidates the survey missed per axis, the survey's coverage isn't actually solving the upstream compression problem — hard revert.
- (b) survey hallucinates or produces stale candidates despite grounding. Web-search results don't reliably reach current vendor inventories; survey output contains discontinued / pivoted-away options the elaborator wastes cycles weighing. Mitigation: tighten the survey prompt's filter discipline ("explicitly verify each candidate's current status before listing"); add a per-entry "last verified URL" field the trace can audit. If grounding-driven enumeration still produces stale lists, the failure points at search-quality (not survey design) and a more fundamental fix is needed.
- (c) decision-elaborator quality drops because of pre-narrowed candidate space. The elaborator anchors so hard on the survey's list that it stops considering candidates the survey missed. Surfaces as decisions whose rationale doesn't engage with obvious-but-unsurveyed options ("no consideration of self-hosted alternative even though GOALS would allow it"). Mitigation: revise the elaborator prompt to treat the survey list as a *starting point* not an exhaustive set; explicitly invite the elaborator to add candidates beyond the survey when relevant. If anchoring proves intractable, the survey's compression cost outweighs benefit — revert.
- (d) cost amortization doesn't hold on real workloads. The expected break-even of one avoided revise per axis doesn't materialize because critics still find structural concerns that drive revises regardless of upfront candidate breadth. Mitigation: separate "candidate-discovery" critic findings (which the survey should have eliminated) from "structural" critic findings (which the survey doesn't address); measure the former's reduction independently. If candidate-discovery findings drop but structural findings hold revise counts roughly constant, the survey is working as designed but the convergence loop has other bottlenecks — accept the smaller savings.
- (e) survey produces too much quality variance across axes. Survey works well on database/framework/auth axes (well-known categories with many candidates) but poorly on specialized domains (electoral software vendor integrations, voter-file ingestion vendors, niche compliance tooling). Mitigation: per-axis survey-needed flag in the workflow ("decision-elaborator handles enumeration on this axis directly"); deploy survey only on axes where empirical evidence supports its value. If the variance is dominant, the survey becomes a per-axis opt-in rather than a universal pre-step.

**Reference.** Extends [DJ-124](#dj-124-spec-generation-re-architecture--decisions-before-narrative-scout-as-judge-convergence-unified-import-flow-refines-dj-068-spec-graph-topology-replaces-dj-105-inline-decisions-schema-re-scopes-dj-123-in-flight-search)'s decision-elaborator workflow with a per-axis pre-step. Inherits the enumeration-vs-judgment separation pattern from [DJ-130](#dj-130-provider-mechanics-encapsulated-in-adapters-trace-recording-follows-the-provider-call-boundary-supersedes-unrecorded-5d15e7b-split-in-dispatcher-pattern)'s thinking-vs-formatting separation — both are instances of breaking cognitive task conflation in single LLM calls. Builds on the alternatives-merge-layer fix from commit [`ef2d209`](https://github.com/chetan/locutus/commit/ef2d209): without mechanical alternatives preservation, the survey's upfront enumeration would still erode across revises and the improvement wouldn't compound. Honors [DJ-128](#dj-128-decisions-as-deliberation-logs-structured-critic-counterproposals-revision-cap-as-commit-refines-dj-126-revise-loop-after-third-winplan-re-run)'s deliberation-log discipline by making the log substantial from initial commit rather than emerging through critic-driven discovery. Honors [DJ-129](#dj-129-dimension-driven-critics-scout-surfaced-critique-surfaces-replace-fixed-4-critic-lens-set-builds-on-dj-128-structured-counterproposal-discipline)'s critic dimensionality by letting critics focus on structural challenges rather than candidate enumeration. Complementary to [DJ-131](#dj-131-phase-fanout-collapse-for-acp-driven-council-steps-extends-dj-127-with-n-per-session-coding-agent-orchestration-aligns-subscription-quota-math-with-per-refine-fanout-volume) (when DJ-131 ships): phase-fanout collapse reduces per-axis session count via cross-axis amortization; DJ-132 reduces per-axis call count via better upfront enumeration. The two compose multiplicatively — fewer sessions, each session covers more axes, each axis takes fewer iterations. Motivated by the sixth winplan re-run at [`/Users/chetan/projects/winplan/.locutus/sessions/20260521/1958/24-20bf24/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260521/1958/24-20bf24/) and the chat 2026-05-22 discussion of initial-elaboration option-space compression.
