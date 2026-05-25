# DJ-135 — Multi-Runtime Pivot: Council Becomes Playbook, Locutus Becomes MCP Server + Activity Registry

> **Governing DJ:** [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135-locutus-pivots-from-in-process-council-to-activity-driven-multi-runtime-execution-via-acp-and-mcp-council-becomes-a-coding-agent-executed-playbook-locutus-retains-the-spec-graph--activity-registry-agentsyaml-drives-per-activity-runtime-selection-with-detection-based-fallback-per-project-singleton-mcp-server-coordinates-shared-state-tools-are-the-universal-readwrite-floor--resources-the-additive-human-attach-surface-supersedes-the-workflowexecutor-based-council-architecture-subsumes-dj-127s-write-tools-design-as-the-foundation-layer). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** DONE (2026-05-25). All 7 phases shipped across 16 commits on branch `dj-135-phase-1-mcp-foundation`. Empirical validation against a fresh `/tmp/lt-p5c4-empirical*` project with Claude Code via `claude-agent-acp` confirmed the new path end-to-end (ckpt 4): ACP attach, MCP server attach, playbook delivery, scout dispatch, candidate-survey fanout, decision-elaborator dispatch, `mcp__locutus__spec_propose_decision` invocation. Convergence-by-construction discipline locked via schema loosening (`axes` backfills from id) and playbook tightening (explicit first-action directive + prefixed MCP tool names). Council deletion in ckpt 5 removed ~51k lines across 207 files; net branch size +5k / −60k. See `git log main..dj-135-phase-1-mcp-foundation` for the commit list.
>
> Codex and Gemini empirical validation deferred — Claude Code only for v1. Format adjustments to the per-runtime publisher land alongside each validation, not pre-emptively.
>
> **Predecessors:** [DJ-119](../../docs/DECISION_JOURNAL.md#dj-119) (ACP wire layer — already shipped; this pivot rests on it), [DJ-127](../../docs/DECISION_JOURNAL.md#dj-127) (write-tools-as-MCP-surface — subsumed and reframed as the foundation layer of this pivot; the standalone framing retires), [DJ-134](../../docs/DECISION_JOURNAL.md#dj-134) (unified SpecStore — the foundation the MCP server's tool surface runs against; no structural change). Supersedes the WorkflowExecutor-based council from [DJ-122](../../docs/DECISION_JOURNAL.md#dj-122), [DJ-124](../../docs/DECISION_JOURNAL.md#dj-124), [DJ-125](../../docs/DECISION_JOURNAL.md#dj-125), [DJ-126](../../docs/DECISION_JOURNAL.md#dj-126), [DJ-128](../../docs/DECISION_JOURNAL.md#dj-128), [DJ-129](../../docs/DECISION_JOURNAL.md#dj-129), [DJ-130](../../docs/DECISION_JOURNAL.md#dj-130), [DJ-131](../../docs/DECISION_JOURNAL.md#dj-131).
>
> **Surface area:** large. New: MCP server + singleton bootstrap, activity registry, publisher (per-runtime), CLI orchestrator (ACP path), prompts surface, activity playbooks. Retired: WorkflowExecutor, gate, integrity-revise, dispatcher, direct-SDK adapters, council bookkeeping in state.go. Modified: CLI verb implementations, agent prompt rename pass, cmd/llm.go simplification.
>
> **Discipline (per memory):** Cite DJ-135 + the precise constraint in chat before touching files in scope per [[feedback-cite-djs-before-spec-work]]. Tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching per [[feedback-test-first]]. Council doc maintenance is mandatory per [[feedback-council-doc-maintenance]] — `docs/council.md` rewrites substantially in Phase 6. The agent-conventions checklist must be walked before any prompt edit per [[feedback-agent-conventions-checklist-first]]. The pattern that motivated this pivot — patch-treadmill on tactical fixes — is what we're actively trying to break out of; design pauses in chat before each phase are mandatory.

## Why this plan exists

Twelve DJs in five weeks (DJ-124 → DJ-134) landed real fixes to the council architecture. Each was individually defensible; the trajectory was not — cumulative complexity grew faster than convergence reliability improved. The winplan run at `~/projects/winplan/.locutus/sessions/20260523/1920/18-f5bf96/` is the concrete evidence: the council surfaced 6 axes correctly, the architect's reasoning pass produced a clean integrity repair, but the format pass on `gemini-3.1-flash-lite` silently dropped the entire decisions array — and the run reported SUCCESS without writing anything to disk because the integrity loop chased a ghost the format pass created upstream.

The diagnosis from chat 2026-05-23 + 2026-05-24: the council's substantive work (research, grounding, axis surfacing, cross-decision integrity) is correct and is the load-bearing differentiator; what's brittle is the *execution layer* — a Go-encoded WorkflowExecutor coordinating multi-agent debate inside one process, with format-pass splits, retry loops, integrity-revise architects, and provider-specific tuning baked into prompts. The pivot moves execution out of Locutus into coding agents (Claude Code, Codex, Gemini CLI, etc.) driven via ACP; Locutus becomes the spec graph + activity registry + MCP server.

This plan sequences the implementation in phases that each leave the system in a working state. The legacy Go council stays available as a fallback until each activity has been migrated and validated; the crossover happens activity-by-activity.

## Reference state (before DJ-135 starts)

- **SpecStore (DJ-134) shipped**: `internal/agent/spec_store.go` is the unified spec graph; reads/writes via `Put` / `GetSpec(ids)` / `ListManifest` / `Search`; transactional with `Begin` / `Commit` / `Rollback`. The MCP server's tool surface runs against this directly — no structural change required.
- **ACP client (DJ-119) shipped**: `internal/dispatch/acp/` is the wire layer for spawning + driving coding agents. The CLI orchestrator path uses this.
- **Council WorkflowExecutor still live**: `internal/agent/workflow_spec_generation*.go`, `specgen.go`, `dispatcher.go`, the direct-SDK adapters in `internal/agent/adapters/`. This stays in place during the migration; retires phase-by-phase as activities migrate.
- **Agent prompts** at `internal/scaffold/agents/*.md` use underscored names (`spec_scout`, `spec_decision_elaborator`). Rename pass to hyphenated form (`spec-scout`, etc.) is Phase 2.
- **No agents.yaml exists yet.** Phase 3 introduces it.
- **No publisher exists yet.** Phase 4 introduces it.
- **A v1 MCP server already exists at `cmd/mcp.go` (499 lines, byte-identical to the version landed pre-DJ-128).** It exposes the CLI verbs as MCP tools (`init`, `status`, `import`, `assimilate`, `refine`, `adopt`, `history`, `explain`, `list`, `justify`) over `mcp.StdioTransport`, with `cmd/sink_mcp.go` (145 lines) translating council workflow events into `notifications/progress` + `notifications/message`. `cmd/mcp_test.go` (269 lines) drives it via `mcp.NewInMemoryTransports()`. **This entire v1 surface — verb-as-tool, single-stdio-session, council-backed — is what DJ-135 retires.** Phase 1 deletes all three files in the same phase it introduces the v2 graph-as-tool surface. Per [[feedback-no-back-compat-until-self-hosting]], no transitional shim.
- **Read-side spec tool handlers already exist as `RegisterSpecTools(registry, store)` in `internal/agent/spec_tools.go:750`** — wired against the council's in-process `ToolRegistry`, not against `mcp.Server`. The handler bodies (BuildSpecManifest, LookupSpecNode, SearchSpecNodes) are SpecStore-backed and directly reusable; the registration call site is not. Phase 1's `internal/mcp/tools_spec_read.go` wraps the same handlers in `mcp.AddTool` registrations.
- **Write-side propose/revise tools do not exist** at the tool level. `SpecStore.Put(kind, id, body, origin)` + `Begin`/`Commit`/`Rollback` are the underlying primitives. Phase 1's `internal/mcp/tools_spec_write.go` is the first time these are exposed as MCP tools.
- **MCP Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.6.1) already in go.mod.** The `Transport` interface is one method (`Connect(ctx) (Connection, error)`); `IOTransport{Reader, Writer}` operates on any `io.ReadCloser`/`io.WriteCloser` pair and is the building block for socket-backed transports (`net.Conn` satisfies both interfaces). `InMemoryTransport` uses `net.Pipe()` under the hood, confirming the SDK already operates over arbitrary `io.ReadWriteCloser`. No custom transport implementation needed; the singleton daemon is a `net.Listen("unix", ...)` accept-loop calling `Server.Connect(ctx, &IOTransport{conn, conn}, nil)` per accepted connection.

## Resolved design questions

Captured in DJ-135 §"Resolved design questions" — 15 items spanning council-shape-is-right, convergence-by-construction, activity-driven config, singleton MCP server, three-trigger-path convergence, publisher discipline, subdirectory namespacing, hyphenated naming, tools-as-universal-floor + resources-as-additive, notifications surface, scaffold lifecycle, .locutus/ vs .borg/ partition, flat-subscription economics, deferred per-runtime overrides, A2UI skipped.

Read the DJ for the full reasoning; each phase below cites the relevant resolved-question items where they constrain implementation choices.

## Phase 1 — Foundation: v2 MCP server (spec graph surface) + singleton daemon, v1 retired

**Goal:** replace the v1 verb-as-tool MCP surface at `cmd/mcp.go` with a v2 graph-as-tool surface backed by the SpecStore, served by a per-project singleton daemon over a Unix socket. The v2 server exposes only the spec read/write tools and the `spec://manifest` resource; activity prompts arrive in Phase 5. v1 deletes in the same commit window — no two-architectures-in-one-server transition.

**Files expected to add:**

- `internal/mcp/server.go` — v2 server constructor. `NewSpecServer(store *agent.SpecStore) *mcp.Server` wires the `mcp.Server` instance, registers all spec_* tools, registers the `spec://manifest` resource, and configures session-scoped notification routing. The SDK handles JSON-RPC dispatch + capability negotiation; this file's job is purely registration. ~200-300 lines, not 600-800 — the SDK is doing the heavy lifting.
- `internal/mcp/socket.go` — socket transport helpers. `ListenSocket(path) (net.Listener, error)` (with stale-socket cleanup), `ServeOnSocket(ctx, listener, server *mcp.Server) error` (accept loop + per-conn `server.Connect(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}, nil)`).
- `internal/mcp/bridge.go` — client-side bridge. `BridgeStdioToSocket(ctx, sockPath) error` proxies the calling process's stdin↔socket↔stdout. To external MCP clients (Claude Code, etc.) this is indistinguishable from a stdio MCP server.
- `internal/mcp/bootstrap.go` — singleton discovery. `EnsureDaemon(projectRoot string) (sockPath string, err error)`: probe `.locutus/mcp.sock`; if responsive, return; if absent or stale (probe fails with ECONNREFUSED or path-doesn't-exist), fork `locutus mcp-daemon --project <root>` and poll until it's listening (bounded by timeout). Stale-socket cleanup is "try-and-fail-on-Listen" — POSIX socket files aren't auto-cleaned by the kernel.
- `internal/mcp/tools_spec_read.go` — read tools registered with `mcp.AddTool`: `spec_list_manifest`, `spec_get`, `spec_search`. Handler bodies delegate to the existing `BuildSpecManifest`, `LookupSpecNode`, `SearchSpecNodes` in `internal/agent/spec_tools.go` (which is already SpecStore-backed). Tool descriptions live in the registration call per DJ-134's "tool descriptions live in registration, not prompts" rule.
- `internal/mcp/tools_spec_write.go` — write tools: `spec_propose_decision`, `spec_propose_feature`, `spec_propose_strategy`, `spec_revise_decision`. Each validates input, calls `SpecStore.Put(kind, id, body, origin)`, and emits `notifications/resources/updated` for `spec://manifest`. **Transactional model TBD in test-design phase:** options are (a) implicit one-tool-call-per-transaction with auto-commit (simplest; matches MCP's stateless-tool semantics), or (b) explicit `spec_begin` / `spec_commit` / `spec_rollback` tools (matches SpecStore's API but exposes session state to the MCP client). Pick before writing.
- `internal/mcp/resources_spec.go` — registers `spec://manifest` as a resource. `resources/read` returns the same JSON as `spec_list_manifest`. Per resolved-question 9, no per-node resources in v1.
- `internal/mcp/notifications.go` — emit `notifications/resources/updated` for `spec://manifest` when SpecStore commits. Subscription set tracked per-session. (The v1 `notifications/progress` + `notifications/message` machinery from `cmd/sink_mcp.go` is council-driven and deletes with v1; the v2 server has no in-process LLM activity to report.)
- `cmd/mcp.go` — **rewrite**, not new. `locutus mcp` subcommand becomes: `EnsureDaemon(root)` → `BridgeStdioToSocket(ctx, sock)`. No tool wiring, no `NewMCPServerWithDir` call. ~30 lines.
- `cmd/mcp_daemon.go` — `locutus mcp-daemon --project <root>` subcommand. Opens the SpecStore (its own instance, separate from the council's `cmd/llm.go` cache during the migration window), constructs `NewSpecServer(store)`, calls `ServeOnSocket`. Blocks until ctx cancellation or socket close.

**Files expected to delete (in the same commit):**

- `cmd/sink_mcp.go` (145 lines) — council event → MCP notification translator. v2 has no in-process council to translate from.
- `cmd/mcp_test.go` (269 lines) — exercises the v1 verb-as-tool surface via `mcp.NewInMemoryTransports()`. Tests for the v2 surface live in `internal/mcp/*_test.go`.
- The verb-as-tool registrations inside `cmd/mcp.go` (helpers `textResult` / `errorResult` / `formatRefineResultForMCP`, the `initInput` / `statusInput` / `importInput` / etc. type set) all delete as part of the `cmd/mcp.go` rewrite.

**Files expected to modify:** none. `cmd/llm.go` stays untouched per [the dedicated note at the top of this section](#) — its retirement is bundled with the council in Phase 5. The daemon opens its own SpecStore instance in `cmd/mcp_daemon.go`; during the migration window the council's `cmd/llm.go` cache and the daemon's instance both read the same on-disk `.borg/spec/`, and the *daemon* is the only writer because the council still operates over the legacy CLI (not through MCP) until Phase 5.

**Open design questions to settle in the test-design checkpoint (before any implementation):**

1. **Transaction model for write tools** — implicit per-call auto-commit vs explicit begin/commit/rollback tools. Affects how `spec_propose_*` schemas look.
2. **Daemon idle shutdown** — fire-and-forget timer per disconnect, or no idle shutdown (relies on OS process management / explicit `locutus mcp stop`)? Plan originally listed `TestMcpDaemon_IdleShutdown` — is idle shutdown actually wanted, or YAGNI for a per-project daemon?
3. **Socket location for non-Unix platforms** — Windows doesn't have Unix sockets in the same form. v1 scope: Unix-only (`darwin` + `linux`). Document the constraint; revisit if a Windows user appears.
4. **Concurrent-write safety** — `SpecStore.Begin/Commit/Rollback` are RWMutex-guarded; concurrent MCP clients calling write tools serialize through that mutex. Confirm this is acceptable; if not, the daemon needs higher-level queuing.

**Tests (subject to revision during checkpoint):**

- `TestSpecServer_RegistersAllReadTools` — confirms `spec_list_manifest`, `spec_get`, `spec_search` appear in `tools/list` via in-memory transport.
- `TestSpecServer_RegistersAllWriteTools` — confirms write tools appear (set depends on transaction-model decision).
- `TestSpecServer_SpecGetReturnsBatchedResults` — in-memory transport: call `spec_get(ids: [...])`, verify response shape matches the SpecStore's `GetSpec` contract (status + body / available_ids / working).
- `TestSpecServer_SpecProposeFeatureWritesToSpecStore` — call `spec_propose_feature`, observe the SpecStore now carries the proposed entry with `origin: proposed`.
- `TestSpecServer_ResourceManifestRendersFullGraph` — `resources/read spec://manifest` returns the same content as `spec_list_manifest` tool.
- `TestSpecServer_NotificationsResourcesUpdatedFiresOnWrite` — after `spec_propose_feature` succeeds, subscribed clients receive `notifications/resources/updated` for `spec://manifest`.
- `TestSocketDaemon_AcceptsAndServes` — `ServeOnSocket` over a real Unix socket in `t.TempDir()`; dial from a goroutine, drive a `tools/list` call, confirm response.
- `TestSocketDaemon_ConcurrentClientsShareSpecStore` — two goroutines dial the same socket; mutation via client A is visible to client B's read.
- `TestSocketDaemon_StaleSocketCleanedOnListen` — pre-create a stale socket file at the path; `ListenSocket` removes it and binds fresh.
- `TestBootstrap_DiscoversLiveDaemon` — pre-existing live daemon → `EnsureDaemon` returns its sock without forking.
- `TestBootstrap_ForksWhenSocketAbsent` — no daemon → `EnsureDaemon` forks `locutus mcp-daemon`, polls, returns sock. (May need to be skipped in CI if subprocess-spawning is constrained; mark as integration.)
- `TestCmdMcp_BridgesStdioToSocket` — pipe `tools/list` into the bridge's stdin, observe response on stdout (in-memory listener + pipe pair).

**Verification:** `go build ./... && go vet ./... && go test ./internal/mcp/... ./cmd/... -count=1 -race`. Manual: spawn `locutus mcp` (forks daemon transparently), drive `tools/list` from stdin, observe spec_* tool catalog. Spawn a second `locutus mcp` in another terminal — verify only one `.locutus/mcp.sock` exists, both clients hit the same daemon, a write through client A is visible to client B's read.

**Estimated:** 10-14 hours (reduced from 12-18 because the SDK does more than the original plan assumed and `RegisterSpecTools` handlers are reusable).

## Phase 2 — Hyphenated naming rename pass

**Goal:** rename every agent canonical from underscored (`spec_scout`, `spec_decision_elaborator`, etc.) to hyphenated (`spec-scout`, `spec-decision-elaborator`). Per resolved-question 8, hyphens are the only naming convention that works on all three runtimes (Claude Code requires hyphens; Gemini accepts both; Codex accepts both). This rename must land before the publisher (Phase 4) so it writes hyphenated names from the start rather than translating at publish time.

**Files expected to modify:**

- `internal/scaffold/agents/*.md` — rename files and update `id:` frontmatter. ~20 agent files.
- Go code with string-literal references to agent ids: grep `internal/agent/` for `"spec_*"` literals and update mechanically. Tests will catch any miss.
- Trace folder naming convention (DJ-130) uses agent ids; carries through to hyphenated automatically.

**Tests:**

- Existing scaffold tests should continue to pass after the rename; if any test embeds the old underscored name as a literal, update it.
- `TestAgentIDsUseHyphenatedConvention` (new) — walks every scaffolded agent and asserts `id:` matches hyphenated form (no underscores after the kind prefix).

**Verification:** `go test ./... -count=1`. Mechanical change; if it compiles and tests pass, it's correct.

**Estimated:** 1-2 hours.

## Phase 3 — Activity registry + agents.yaml

**Goal:** stand up the activity registry that maps activity names (spec_refinement, planning, frontend_implementation, etc.) to prioritized runtime preferences. Detection-based fallback selects the first available runtime. Per resolved-questions 3 (activity-driven config) and 4 (singleton coordinates shared state).

**Files expected to add:**

- `internal/activity/registry.go` — activity definitions + lookup. Each activity has: name, prioritized runtime list, plan file path (`.borg/plans/<activity>.md`), required subagents.
- `internal/activity/agents_yaml.go` — load + parse `agents.yaml`. Schema validation. Default content embedded; user can override per-project.
- `internal/activity/detect.go` — runtime detection. Check `$HOME/.claude/`, `$HOME/.codex/`, `$HOME/.gemini/`, or the binary in `$PATH`. First detected from the prioritized list wins.
- `internal/scaffold/agents-yaml-default.yaml` — embedded default `agents.yaml`. Ships with reasonable per-activity runtime preferences based on the empirical task-tool fit data in DJ-135's context (Claude Code primary for spec_refinement; Codex primary for planning; Cursor primary for frontend_implementation; Claude Code primary for backend_implementation).

**Tests:**

- `TestActivityRegistry_ResolvesByName` — `Lookup("spec_refinement")` returns the activity definition.
- `TestAgentsYAML_ParsesAndValidates` — well-formed agents.yaml parses; malformed errors clearly.
- `TestRuntimeDetection_PrefersFirstAvailable` — given a fake $HOME with only `.claude/` present, detection returns claude-code regardless of agents.yaml order.
- `TestAgentsYAML_ProjectOverridesShipDefaults` — `.borg/agents.yaml` if present overrides embedded defaults.

**Verification:** `go build ./... && go vet ./... && go test ./internal/activity/... -count=1`. Manual: in a project with multiple runtimes installed, `locutus activity which spec_refinement` reports which runtime got selected.

**Estimated:** 4-6 hours.

## Phase 4 — Publisher (per-runtime emission)

**Goal:** the publisher reads canonical `.borg/agents/*.md` + `.borg/plans/*.md` and emits per-runtime copies in the right format/location for each registered runtime. Per resolved-question 6 (copies tailored, not symlinks) and 7 (subdirectory namespacing where supported, prefix elsewhere).

**Files expected to add:**

- `internal/publisher/publisher.go` — orchestrator: walk agents.yaml's listed runtimes, for each runtime emit subagents + slash commands + (where applicable) extension manifest.
- `internal/publisher/claudecode/` — Claude Code publisher: subagents to `.claude/agents/locutus/<name>.md`, slash commands to `.claude/commands/locutus-<activity>.md`. Markdown + YAML frontmatter; subdirectory namespacing.
- `internal/publisher/codex/` — Codex publisher: subagents to `.codex/agents/locutus-<name>.toml`, slash commands to `.codex/commands/locutus-<activity>.toml`. TOML; filename prefix.
- `internal/publisher/gemini/` — Gemini publisher: subagents to `<gemini-ext>/agents/locutus-<name>.md`, slash commands to `<gemini-ext>/commands/locutus-<activity>.toml`. Markdown + extension manifest; filename prefix.
- `internal/publisher/translate.go` — shared translation: Locutus frontmatter (`models: [{provider, tier}]`, `thinking`, `output_schema`, `grounding`) → runtime-specific equivalent. MCP server config injection so each published subagent knows to call back into Locutus via the singleton.

**Files expected to modify:**

- `cmd/init.go` — call publisher after scaffolding `.borg/` defaults.
- `cmd/update.go` — `--reset` flag triggers publisher re-emission (and `mcp-update-reset` slash command equivalent).

**Tests:**

- `TestPublisher_ClaudeCodeEmitsSubagent` — given a canonical `spec-scout.md` with Locutus frontmatter, publish to `.claude/agents/locutus/spec-scout.md` with correct Claude Code frontmatter (`name`, `description`, `tools`, `model`).
- `TestPublisher_CodexEmitsSubagentTOML` — same, but `.codex/agents/locutus-spec-scout.toml` with TOML frontmatter (`name`, `description`, `developer_instructions`, `model`).
- `TestPublisher_GeminiEmitsSubagentMD` — same, but Gemini extension format.
- `TestPublisher_SlashCommandPointsAtCanonicalPlan` — Claude Code's `.claude/commands/locutus-refine-goals.md` content references `.borg/plans/refine-goals.md`.
- `TestPublisher_MCPServerConfigInjected` — each published subagent's frontmatter includes the MCP server config so the runtime knows how to call Locutus's MCP tools.
- `TestPublisher_ReEmissionOverwritesGenerated` — second call to publisher overwrites previously-emitted files (per `update --reset` lifecycle).

**Verification:** `go build ./... && go vet ./... && go test ./internal/publisher/... -count=1`. Manual: in a test project, run `locutus init`; observe `.claude/agents/locutus/`, `.codex/agents/locutus-*.toml`, etc. populated with expected content. Try loading the subagent in Claude Code; verify it appears.

**Estimated:** 8-12 hours.

## Phase 5 — Activity playbooks + MCP prompts + retire the Go council (direct cut)

**Goal:** author the activity playbooks, expose them as MCP prompts, rewire the CLI verbs to dispatch via ACP, AND delete the Go council code in the same phase. Per resolved-question 5 (three trigger paths converge on the playbook) and the DJ's Migration framing (direct cut, no fallback). The current Go council has never converged reliably; there's no value in keeping it as a parallel path during development. Once this phase lands, the legacy council code is gone.

**Files expected to add:**

- `internal/scaffold/plans/refine-goals.md` — the spec-refinement activity playbook. Markdown imperative instructions: "Dispatch spec-scout. Once it returns, for each axis in scout.axes_open, dispatch spec-decision-elaborator. Wait for all to complete. For each new node from scout.new_nodes, dispatch the appropriate elaborator. Once all decisions are committed, dispatch spec-reconciler. For each critic dimension in scout.dimensions, dispatch spec-critic-elaborator. Loop until scout.converged is true." Convergence-by-construction discipline: "If an axis is open and not surfaced as a concern, assume the best-practice default and commit; do not surface to human."
- `internal/scaffold/plans/plan-feature.md` — placeholder for future planning-activity work.
- `internal/mcp/prompts_activity.go` — registers prompts for each activity: `locutus.refine_goals`, `locutus.plan_feature`, etc. `prompts/get` returns the playbook content as a multi-turn message sequence.

**Files expected to modify:**

- `cmd/refine.go` — replace Go-encoded council orchestration with: detect runtime from agents.yaml; spawn it via ACP; hand it the published `refine-goals` plan. The runtime executes; calls back into Locutus's MCP tools to mutate the graph.
- Other CLI verbs (`import`, `adopt`, `assimilate`) that referenced the legacy council path migrate to the activity-driven model in the same commit.

**Files expected to delete (in the same phase, after the new path lands and compiles):**

- `cmd/llm.go` — entire file. With the council gone, every helper here is dead code: `getLLM`, `buildExecutor`, `newExecutor`, `recordingLLM`, `registerSpecToolsOnce`, `emitBannerOnce`, `initTracerForSession` / `ShutdownTracer` (the MCP daemon owns its own tracer lifecycle), `heartbeatEnabledForMode`. Locutus stops making LLM calls directly; nothing left to wire.
- `internal/agent/workflow_spec_generation.go`, `workflow_spec_generation_dj124.go`, `workflow_spec_generation_test.go` — the council workflow.
- `internal/agent/specgen.go` — integrity-revise architect, runSpecGeneration's council bootstrap.
- `internal/agent/dispatcher.go` — ReAct iteration loop.
- `internal/agent/adapters/anthropic.go`, `gemini.go`, `openai_responses.go` — direct-SDK adapters. Locutus no longer makes LLM calls directly.
- `internal/agent/state.go` PlanningState and council-internal bookkeeping (AxesOpen, NewNodesFromScout, OpenConcernDecisionIDs, LockedDecisionIDs, AxisRevisionCount, DecidedAxesByIter).
- DJ-128's cap-as-commit logic; DJ-130's reasoning/format split (`runSplit`, `requiresThinkingSchemaSplit`); revision-count limits.

**Tests:**

- `TestMcpServer_RegistersActivityPrompts` — `prompts/list` returns `locutus.refine_goals`.
- `TestMcpServer_PromptsGetReturnsPlaybook` — `prompts/get locutus.refine_goals` returns content matching `.borg/plans/refine-goals.md`.
- `TestRefineGoalsPlaybookReferencesPublishedSubagents` — the playbook content uses hyphenated subagent names that match what the publisher emits.
- `TestCLI_RefineGoalsDispatchesViaACP` — `locutus refine goals` end-to-end against a mock ACP server: detects runtime, spawns it, hands it the playbook, observes the spawned agent calling Locutus's MCP tools.
- Existing council tests delete alongside the council code. New test coverage focuses on the dispatch + playbook path.

**Verification:** `go build ./... && go vet ./... && go test ./... -count=1 -race`. Manual empirical: run `locutus refine goals` against winplan with Claude Code as the detected runtime; observe Claude Code session opening, executing the playbook, calling spec tools, and writing a populated `.borg/spec/` to disk. **The validation criterion is "the run produces a populated spec graph on disk," not "equal-to-or-better than the legacy path" — the legacy path's failure mode is what motivated this DJ and is not a meaningful baseline.**

**Estimated:** 14-21 hours (playbook authoring is iterative; expect to refine prompts based on first-run observations; deletion is mechanical but voluminous).

## Phase 6 — Documentation

**Goal:** rewrite the docs that anchor the new architecture. Per [[feedback-council-doc-maintenance]], council.md is mandatory.

**Files expected to modify:**

- `CLAUDE.md` — new top-level section on the multi-runtime pivot. The source-of-truth shift (Locutus is MCP server + activity registry, not in-process council). The activity → runtime mapping via agents.yaml. The publisher's role. The MCP surface (tools/prompts/resources). The singleton lifecycle.
- `docs/council.md` — rewrites substantially. Per-agent prompt content preserved but the "council workflow" framing changes from "Locutus orchestrates" to "coding agent executes the playbook." Mermaid diagrams update.
- `docs/agent-conventions.md` — per-runtime publishing section: what translates, what doesn't. Hyphenated naming convention. Playbook authoring guidance.
- `docs/debugging-traces.md` — traces during an activity now live in the calling agent's surface (Claude Code session log etc.) augmented by Locutus's `.locutus/sessions/` (MCP tool calls only). Trace shape changes meaningfully.

**Files expected to add:**

- `docs/activities.md` — the activity registry, agents.yaml schema, runtime detection, publisher behavior, lifecycle.
- `docs/mcp.md` — Locutus's MCP server: tools/prompts/resources surface, notification semantics, singleton lifecycle, transport (stdio-over-socket).

**Verification:** `go test ./... -count=1 -race` clean. Manually verify Mermaid diagrams in council.md still render. Walk `git grep -nE 'workflow_spec_generation|specgen.go|integrity_revise'` — references in docs should be updated or removed (the code is already gone after Phase 5).

**Estimated:** 4-6 hours.

## Phase 7 — Status flip + plan marked DONE

**Goal:** DJ-135 flips from `proposed` to `shipping` once Phases 1-6 land and Phase 5's empirical winplan run produces a populated spec graph.

**Files expected to modify:**

- `docs/DECISION_JOURNAL.md` — DJ-135 status `proposed` → `shipping (Phases 1-6 landed YYYY-MM-DD)`.
- This plan file marked DONE.

**Verification:** `go test ./... -count=1 -race` clean. Empirical: winplan refine via the new path produces a populated `.borg/spec/`.

**Estimated:** 30 minutes (purely status flip and plan close-out).

---

## Total estimate: 43-65 hours single-stranded across 6-8 sessions

(plus Phase 5 empirical winplan run for validation)

## Pointers a fresh session should follow before resuming

1. **Read DJ-135 in full** ([docs/DECISION_JOURNAL.md#dj-135](../../docs/DECISION_JOURNAL.md#dj-135-locutus-pivots-from-in-process-council-to-activity-driven-multi-runtime-execution-via-acp-and-mcp-council-becomes-a-coding-agent-executed-playbook-locutus-retains-the-spec-graph--activity-registry-agentsyaml-drives-per-activity-runtime-selection-with-detection-based-fallback-per-project-singleton-mcp-server-coordinates-shared-state-tools-are-the-universal-readwrite-floor--resources-the-additive-human-attach-surface-supersedes-the-workflowexecutor-based-council-architecture-subsumes-dj-127s-write-tools-design-as-the-foundation-layer)). The DJ is the authoritative design; this plan is progress tracking.
2. **Read DJ-119** ([docs/DECISION_JOURNAL.md#dj-119](../../docs/DECISION_JOURNAL.md#dj-119)) for the ACP wire layer this pivot rests on. Familiarity with `internal/dispatch/acp/` is prerequisite to Phase 5.
3. **Read DJ-134** ([docs/DECISION_JOURNAL.md#dj-134](../../docs/DECISION_JOURNAL.md#dj-134)) for the SpecStore foundation layer. The MCP server's tool surface runs against this directly.
4. **Read the winplan trace** at `~/projects/winplan/.locutus/sessions/20260523/1920/18-f5bf96/` end-to-end before Phase 5. The format-pass-drops-decisions failure is the concrete motivating evidence; the playbook architecture's job is to make this class of failure structurally impossible.
5. **Read the Stack Overflow Developer Survey 2025 AI section** ([survey.stackoverflow.co/2025/ai](https://survey.stackoverflow.co/2025/ai/)) for the empirical multi-tool usage data that supports the multi-runtime architecture.
6. **Verify ACP server availability** for at least Claude Agent + Codex + Gemini before starting Phase 5. Per DJ-119's Phase 0 verification this is confirmed; re-verify if any of those projects underwent significant releases since 2026-05-13.
7. **Walk `docs/agent-conventions.md` as a checklist** before any prompt edit per [[feedback-agent-conventions-checklist-first]]. Phase 5's playbook authoring is the relevant trigger.
8. **Cite DJ-135 + the precise constraint** before each phase's first commit per [[feedback-cite-djs-before-spec-work]].

## What is explicitly out of scope

- **Per-runtime prompt overrides** (resolved-question 14). Deferred to a future DJ if empirical evidence shows portable prompts degrade meaningfully on specific runtimes.
- **A2UI output** (resolved-question 15). Skipped for v1; revisit if a specific A2UI-capable client emerges.
- **Multi-project orchestration** — Locutus is per-project. Operators running multiple projects in parallel run multiple Locutus daemons (one per project, bound to that project's socket).
- **Token / cost accounting at the Locutus level.** Coding agents call their own LLMs under their own subscriptions; Locutus has no visibility into per-call cost. Aggregate observability moves to the calling agent's tools (Claude Code's session log, etc.).
- **Backward compatibility shims for the legacy CLI verb invocations** — per [[feedback-no-back-compat-until-self-hosting]], CLI verbs change semantics in-place; users run `locutus update --offline --reset` before next use. **Fallback dispatch to the Go council is also explicitly not retained** — Phase 5 deletes the legacy council code directly. The current Go council has never produced a clean convergent run; keeping it as fallback would signal otherwise and split engineering attention.
- **Migration of existing on-disk specs.** SpecStore's persistence shape carries forward unchanged (DJ-134); no migration needed.
- **Renaming the `.borg/` directory** — the directory name is durable per existing convention. The pivot's new files all land under existing `.borg/` and `.locutus/` partitions per resolved-question 12.
- **Web UI / dashboard for spec graph inspection** — a future Locutus dashboard COULD consume `notifications/resources/updated` on `spec://manifest` to render live, but standing it up is outside this DJ's scope.
- **Cross-runtime in-flight transaction state** beyond what the singleton + Unix socket provides. Two Locutus daemons in two unrelated projects don't share anything; that's by design (per-project scoping).
