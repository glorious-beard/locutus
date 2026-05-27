## DJ-127: Spec Mutation Tools as MCP Write Surface; ACP-Driven Decision Elaboration (Aligns Council Transport With Subscription-Based AI Funding Model)

**Status:** subsumed by [DJ-135](dj-135-multi-runtime-pivot.md) on 2026-05-25 — the spec-mutation tools (`spec_propose_decision`, `spec_propose_feature`, `spec_propose_strategy`, `spec_revise_*`) shipped exactly as this DJ designed them, but the standalone framing retires in favor of DJ-135's larger reframe where these tools are the universal write surface of the per-project MCP daemon (not a council-specific ACP-driven decision-elaborator transport). DJ-135's resolved-question 14 implementation note: "DJ-127's write-tools design becomes the foundation layer for the MCP server." Prior status: proposed.

**Context.** Locutus's council is API-first by construction: direct-SDK adapters ([DJ-099](#dj-099-direct-sdk-llm-adapters-per-provider-supersede-genkit)) issue per-call requests against Anthropic / Google / OpenAI APIs, billed per-token. A non-trivial `locutus refine goals` session today costs $0.50–$30 depending on tier (cheapest Gemini Flash to premium Claude Opus). The visible cost matters less than the *invisible* friction it creates: every invocation is a billable event the user mentally accounts for. The deliberate user posture documented in `feedback_no_back_compat_until_self_hosting` and the `user_profile.md` memory — "Claude Max subscriber, avoids API token costs" — means the effective architecture constraint today is "use the cheapest free-tier Gemini path," which is exactly what shows up in the winplan validation traces (`gemini-3-flash-preview` throughout).

The developer ecosystem has shifted to subscription-based AI tooling. Most users running a serious AI workflow already pay for Claude Max ($200/month, Claude Code unlimited-ish), Codex Pro/Plus, or Gemini Advanced. These subscriptions cover *unlimited* (within reasonable quotas) coding-agent sessions. The marginal cost of one more `claude-code` invocation is zero up to the daily/weekly cap.

Locutus today fights this funding model. Direct-SDK adapters require explicit API keys, bill per call, and force users into a "is this refine worth $X?" decision before every invocation. The architecture is correct for an era when API access was the only way to drive LLMs; it's structurally misaligned with how developers actually pay for AI tooling today.

The transport infrastructure to bridge this gap already exists. [DJ-119](dj-119-agent-client-protocol-replaces.md) adopted ACP as the uniform transport for coding agents on the implementation side. [DJ-125](dj-125-in-flight-manifest.md) introduces the `InFlightManifest` typed-state surface plus the redirection of `spec_list_manifest` / `spec_get` / `spec_search` to in-flight state during council runs — the read-side of locutus's MCP server already serves both internal council agents and (in principle) any external MCP client. [DJ-126](dj-126-decision-revision.md) validates that the council architecture is stable enough to refactor without compounding debugging surface.

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

**Reference.** Builds on [DJ-119](dj-119-agent-client-protocol-replaces.md) (the ACP transport infrastructure this extends to the council). Requires [DJ-125](dj-125-in-flight-manifest.md) (the typed manifest surface the write tools mutate) and validation of [DJ-126](dj-126-decision-revision.md) (council architecture stable before transport refactor) as prerequisites. Complements [DJ-099](#dj-099-direct-sdk-llm-adapters-per-provider-supersede-genkit) — direct-SDK adapters remain the default for fast/judgment roles; ACP adds a parallel transport for research-heavy roles. Preserves [DJ-122](dj-122-spawner-executor.md)'s workflow executor unchanged structurally; only the dispatch primitive inside `executeAgent` gains a transport switch. Preserves [DJ-105](dj-105-elaborator-api-layer-required-not.md) / DJ-124's schema-enforcement guarantees by moving the contract from output schemas to tool-input schemas — same `jsonschema` library, same per-field tags, same API-layer rejection of malformed input. Motivated by the cost/DX alignment argument from chat 2026-05-18 (Claude Max subscription user model vs API-billed-per-call) and the third winplan validation trace (pending DJ-125 + DJ-126).
