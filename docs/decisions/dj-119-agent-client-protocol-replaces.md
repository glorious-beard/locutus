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
