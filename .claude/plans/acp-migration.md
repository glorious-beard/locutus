# ACP Migration — Implementation Plan

> **Governing DJ:** [DJ-119: Agent Client Protocol Replaces the Coding-Agent Driver Layer](../../docs/DECISION_JOURNAL.md#dj-119)
> in `docs/DECISION_JOURNAL.md`. The DJ is the authoritative design record;
> this plan tracks **progress against** the DJ and captures session-level
> implementation notes that don't belong in the DJ.
>
> **Status:** All phases complete (0, 1, 2, 3, 4, 5, 6, 7, 8). DJ-119 migration done.
> **Last updated:** 2026-05-14.
> **Branch:** main (Phase 1+2+3+4+5+6+7+8 changes uncommitted; Phase 0 verification artifacts under `/tmp/acp-verify/`).

## Why this plan exists

The work spans roughly 6–9 days single-stranded. A fresh session needs to
pick up cleanly without re-deriving the design decisions or re-running the
verification step. This file is that handoff document.

The user's discipline (per memory): **tests → design pause in chat → code;
mid-impl failures trigger design judgment, not test patching.** Honour this
between phases. Each phase has explicit design decisions in chat before
code lands.

## Reference state (what exists right now)

### Phase 0 — verification: DONE 2026-05-13

Confirmed all three target ACP servers support the spawn-per-workstream
lifecycle model that DJ-119 assumes. None require `session/resume`; none
require `session/load`-with-replay. Two consecutive `session/prompt` calls
inside one `session/new` completed cleanly on every agent.

| Agent | Install | Protocol | `loadSession` | `sessionCapabilities` |
| --- | --- | --- | --- | --- |
| Gemini `--acp` (native) | preinstalled | v1 | true | `{}` (none) |
| Claude Code (`claude-agent-acp`) | `npm i -g @agentclientprotocol/claude-agent-acp@0.33.1` | v1 | true | close, fork, list, **resume** |
| Codex (`codex-acp`) | GitHub release tarball v0.14.0 (NOT on crates.io) | v1 | true | close, list |

**Verification artifacts** (re-runnable):

- Harness source: `/tmp/acp-verify/cmd/phase0/main.go` (220 LOC, uses `coder/acp-go-sdk` v0.13.0)
- Results JSON: `/tmp/acp-verify/{gemini,claude,codex}.json`
- Re-run: `cd /tmp/acp-verify && go build -o phase0 ./cmd/phase0 && ./phase0 -label NAME -cmd 'AGENT ARGS'`

**Findings folded back into DJ-119:**
- Capability degradation strategy required — agents do not have a uniform
  capability surface. Gemini has the minimum set; the ACP client wrapper
  must tolerate missing-capability cases (no `close` → SIGTERM; no `list`
  → own session registry; no `resume` → don't call).
- `codex-acp` is GitHub-release-only, not crates.io. Phase 7 ops detail
  needs to reflect this.
- Claude Code's `_meta.claudeCode.promptQueueing: true` is opportunistic,
  not load-bearing for Phase 1. Worth a separate small DJ later if useful.

### Phase 1 + Phase 2 — `internal/dispatch/acp/` foundation + event translation: DONE 2026-05-14

Phases 1 and 2 were tightly coupled enough that shipping them separately
would have been awkward (the `Connection.Prompt` method needs translated
events to feed its channel). Total: **749 lines across 4 files; 3 tests
pass** against a fakeAgent wired via `io.Pipe`.

**Files created:**

| File | Purpose | Key types |
| --- | --- | --- |
| [`internal/dispatch/acp/connection.go`](../../internal/dispatch/acp/connection.go) | Spawn + lifecycle + public API | `Spawn`, `Connection`, `Open`, `openWithIO`, `Capabilities`, `NewSession`, `Prompt`, `Cancel`, `Close` |
| [`internal/dispatch/acp/client.go`](../../internal/dispatch/acp/client.go) | `acp.Client` impl + per-session routing + policy plumbing | `Policy`, `PolicyDecision`, `AllowOncePolicy`, `client`, `activePrompt` |
| [`internal/dispatch/acp/events.go`](../../internal/dispatch/acp/events.go) | Pure translation `SessionNotification → dispatch.AgentEvent` | `translateUpdate` |
| [`internal/dispatch/acp/connection_test.go`](../../internal/dispatch/acp/connection_test.go) | Happy-path + permission-policy tests, fakeAgent + io.Pipe wiring | `fakeAgent`, `wireFake`, 3 tests |

**Go module:** `github.com/coder/acp-go-sdk v0.13.0` is now a direct
dependency in [`go.mod`](../../go.mod).

**Tests pass:**

```bash
go test ./internal/dispatch/acp/ -count=1
# ok  	github.com/chetan/locutus/internal/dispatch/acp	0.396s
```

**Public surface (the supervisor's contract):**

```go
type Spawn struct { Cmd string; Args []string; Env []string }

type Policy interface {
    Decide(ctx, acpsdk.ToolCallUpdate, []acpsdk.PermissionOption) (PolicyDecision, error)
}
type PolicyDecision struct { OptionId string }  // empty = cancel

func Open(ctx, Spawn, archiveDir) (*Connection, error)
func (c *Connection) Capabilities() acpsdk.AgentCapabilities
func (c *Connection) NewSession(ctx, cwd, mcpServers) (sessionID string, err error)
func (c *Connection) Prompt(ctx, sessionID, text, policy) (<-chan dispatch.AgentEvent, error)
func (c *Connection) Cancel(ctx, sessionID) error
func (c *Connection) Close() error
```

**Design decisions taken (don't relitigate without reason):**

1. **`Policy` is per-Prompt, not per-Connection.** The user's argument
   from chat: more flexible, can emulate the per-Connection case
   trivially, and we already need per-session state on the `client` for
   routing notifications so adding `policy` to that table costs nothing.
   Future use case it enables: differentiated policies per retry
   attempt (strict on first try, looser after churn).

2. **`Prompt` returns events as a `<-chan dispatch.AgentEvent` that
   closes when the turn completes.** The terminal event is always either
   `EventResult` (carrying `stopReason` as Text) or `EventError`. This
   matches the supervisor's existing event-loop idiom in
   [streaming.go runAttempt](../../internal/dispatch/streaming.go).

3. **The `client` holds a `map[SessionId]*activePrompt`** guarded by a
   RWMutex. `register`/`unregister` are the only mutators. Defensive
   late-delivery handling: notifications/permission-requests with no
   registered active prompt are dropped (debug-logged for SessionUpdate;
   cancelled for RequestPermission).

4. **Phase 1 advertises NO client capabilities** in `initialize` —
   no `fs`, no `terminal`. Implementations of `WriteTextFile` /
   `ReadTextFile` / `terminal/*` return "capability not advertised"
   errors as defensive guards. A well-behaved agent won't call them
   because they're not advertised. Adding these later is a
   forward-compatible change.

5. **`openWithIO` (lowercase) is the spawn-free constructor for tests.**
   Public `Open` spawns a subprocess; tests use the lowercase variant
   wired through `io.Pipe()` pairs to a `fakeAgent` running on
   `acp.NewAgentSideConnection`. Same SDK code path, no subprocess
   overhead, no real LLM calls.

**Explicitly deferred to later phases:**

- JSON-RPC frame archive (`archiveDir` parameter accepted but ignored).
  Phase 1 follow-up — small, additive, do whenever debugging needs it.
- Graceful `session/close` per session before subprocess kill. Phase 3
  concern once the supervisor tracks active sessions.
- Production `Policy` implementation porting the current
  [`internal/dispatch/bridge.go`](../../internal/dispatch/bridge.go)
  policy logic — Phase 4.
- W3C trace context propagation via `_meta.traceparent` — Phase 5.
- `session/cancel` integration with supervisor ctx-cancel — Phase 3 when
  the supervisor actually drives Prompt.

### Phase 3 — Supervisor lifecycle change: DONE 2026-05-14

The supervisor now talks to the agent through `dispatch.PromptConn`, an
in-package interface that the cmd layer satisfies with a thin
`*acp.PromptConnAdapter` over `*acp.Connection`. One Connection + one
session is opened per workstream; all steps and all retry attempts share
that single conversation, so the per-attempt subprocess spawn is gone.

**Files changed (net):**

| Action | File | Notes |
| --- | --- | --- |
| Added | [`internal/dispatch/prompt_conn.go`](../../internal/dispatch/prompt_conn.go) | `PromptConn` interface + `buildPromptText` helper |
| Rewrote | [`internal/dispatch/supervisor.go`](../../internal/dispatch/supervisor.go) | `Supervise(ctx, step, conn, sessionID)`; `SuperviseFrom` folded back into `Supervise`; `permBridge` field gone |
| Rewrote | [`internal/dispatch/streaming.go`](../../internal/dispatch/streaming.go) | `runAttempt` is now a channel-drain over `conn.Prompt`; parser pump, bridge merge, and `handleInteraction` routing all removed |
| Rewrote | [`internal/dispatch/dispatcher.go`](../../internal/dispatch/dispatcher.go) | `Drivers map[string]StreamingDriver` → `OpenConn func(ctx, agentID) (PromptConn, error)`; `runWorkstream` opens connection + session per workstream |
| Added | [`internal/dispatch/acp/promptconn.go`](../../internal/dispatch/acp/promptconn.go) | `PromptConnAdapter` wraps `*acp.Connection` to satisfy `dispatch.PromptConn`, supplying a fixed policy per Connection |
| Added | [`internal/dispatch/acp_supervise_test.go`](../../internal/dispatch/acp_supervise_test.go) | New fakePromptConn-based tests for the 4 plan scenarios (happy, retry-with-feedback, churn, ctx-cancel) |
| Updated | [`cmd/adopt.go`](../../cmd/adopt.go) | `realDispatch` populates `OpenConn` via `acpOpenConn` + an `agentSpawns` registry (`claude-code → claude-agent-acp`, `codex → codex-acp`, `gemini → gemini --acp`) |
| Deleted | `internal/dispatch/interaction.go` + `_test.go` | `handleInteraction` orphaned by `permBridge` field removal — Phase 4 deletes `bridge.go` itself |
| Deleted | `internal/dispatch/supervise_test.go` | scriptedStreamingDriver — obsolete |
| Deleted | `internal/dispatch/supervisor_stream_test.go` | fakeStreamingDriver — obsolete |
| Deleted | `internal/dispatch/dispatcher_test.go` | alwaysPassDriver / fileWritingDriver — obsolete; replacement coverage lives in acp_supervise_test.go and the cmd-level integration tests |
| Deleted | `internal/dispatch/resume_test.go` | tested the now-removed `--resume <id>` codepath; the StepID-skip resume contract still lives in `dispatcher.go` and is exercised via cmd tests |
| Deleted | `internal/dispatch/drivers/` (the whole package) | Phase 8 was scheduled to do this; doing it now makes Phase 3 compile cleanly and avoids carrying dead code for two more phases |
| Deleted | `internal/dispatch/live_integration_test.go` | NDJSON-stream e2e against real `claude` CLI; Phase 6 writes the ACP-flavoured replacement |

**Design decisions taken (don't relitigate without reason):**

1. **`PromptConn` is declared on the consumer side**, not imported from
   `acp`. The naive design — `dispatch` imports `acp.Policy` for
   `PromptConn.Prompt`'s signature — closes a cycle through `acp`'s
   existing import of `dispatch.AgentEvent`. Instead, `PromptConn.Prompt`
   takes no policy argument; the `acp.PromptConnAdapter` holds a fixed
   policy per Connection and supplies it to the underlying
   `acp.Connection.Prompt` call. Phase 4 will need to decide whether to
   extend `PromptConn` (add a Policy parameter, accepting the cycle by
   introducing a leaf package) or stash a guardian closure inside the
   adapter (no interface change). The plan's Phase 1 commitment to
   "per-Prompt policy injection" is preserved at the `acp.Connection`
   API level even though `PromptConn` doesn't expose it.

2. **`SuperviseFrom` folded into `Supervise`.** Under the spawn-per-
   workstream lifecycle, the session is held open across attempts — the
   per-attempt `--resume <sessionID>` mechanism the old driver model
   needed is now automatic (next `Prompt` on the same `sessionID` is
   the next user message in the same conversation). There is no longer
   any code path that re-attaches to a prior session within a workstream.

3. **DJ-074 cross-process resume semantics narrow to step-level (git),
   not conversation-level (ACP session).** The old driver model could
   restart Locutus, find a `--resume <sessionID>` in the workstream
   record, and rejoin the prior agent conversation across that process
   boundary. Under ACP the agent subprocess died with Locutus — there
   is no conversation to rejoin without spawning a fresh agent and
   issuing `session/load <prior-id>`. The persisted `AgentSessionID`
   field on `WorkstreamResult` is preserved for telemetry and as a
   future hook for `session/load`-based resume; runWorkstream no longer
   replays it into a new session. The durable guarantee of DJ-074 — the
   feature branch carries the already-merged work — remains intact:
   step skipping by `resumeFrom.StepID` still runs, just against a
   fresh ACP session. A dedicated DJ should formalize this narrowing
   before any user-visible behaviour changes flow from it.

4. **Production policy not yet ported — `acp.AllowOncePolicy` is the
   Phase-3 placeholder.** This is Phase 4's job, explicitly. The cmd
   layer's `acpOpenConn` constructs the adapter with the default policy
   (`nil` → `AllowOncePolicy`); Phase 4 swaps in a guardian Policy that
   calls the validator LLM, ports the prompt-building logic from the
   now-deleted `handleInteraction`, and translates `ALLOW` / `DENY`
   verdicts to the appropriate ACP `PermissionOption` selection.

5. **`drivers/` and `live_integration_test.go` deleted now, not Phase 6/8.**
   The plan scheduled both for later phases but they were dead the
   moment the supervisor stopped talking to `StreamingDriver`. Carrying
   them as broken-compile detritus for two more phases would be
   worse-than-noise; deleting them now keeps `go build ./...` clean.
   Phase 6 will add an ACP-flavoured live integration test against
   `gemini --acp` or `claude-agent-acp`. Phase 8 still has to delete
   `bridge.go` + `cmd/mcp_perm_bridge*.go` + scaffold defaults.

**Tests pass:**

```bash
go build ./... && go vet ./...
go test ./internal/dispatch/... ./cmd/ -count=1
# ok  github.com/chetan/locutus/internal/dispatch       2.973s
# ok  github.com/chetan/locutus/internal/dispatch/acp   0.596s
# ok  github.com/chetan/locutus/cmd                     3.615s
```

**Public surface change for downstream callers:**

```go
// Before:
d := &dispatch.Dispatcher{
    LLM:     llm,
    Drivers: map[string]dispatch.StreamingDriver{"claude-code": drivers.ClaudeCodeDriver{}},
    Runner:  dispatch.ProductionRunner,
}

// After:
d := &dispatch.Dispatcher{
    LLM:      llm,
    OpenConn: acpOpenConn, // closes over agentSpawns map
}
// dispatch.Runner is still on the struct (used for non-ACP subprocess work)
// but is no longer the coding-agent transport.
```

`cmd/adopt.go` is the only production wire-up; tests construct
`Dispatcher`s in-line and stub `OpenConn` directly.

**Explicitly NOT done in Phase 3 (still pending in later phases):**

- Production `Policy` impl porting bridge.go's validator-LLM permission
  decisions → **Phase 4**.
- `bridge.go`, `cmd/mcp_perm_bridge*.go`, and `bridge_test.go` deletion
  → **Phase 4** (they still compile cleanly today, just orphaned).
- W3C `_meta.traceparent` propagation → **Phase 5**.
- ACP-flavoured live integration test → **Phase 6**.
- DJ amendment formalizing the narrowing of DJ-074 to step-level resume
  semantics under ACP → **Phase 8** (or its own DJ if it turns out to
  affect users before then).

### Phase 4 — Permission policy port: DONE 2026-05-14

The supervisor's permission gate now flows through ACP's native
`session/request_permission` method into a `policy.Policy` implemented
by `internal/dispatch/guardian.Guardian`. The Unix-socket bridge that
DJ-010 used (`bridge.go` + the `locutus mcp-perm-bridge` subprocess + the
embedded `locutus_permission` MCP tool) is gone — Guardian ports the
validator-LLM-asks-ALLOW-or-DENY logic verbatim from the pre-DJ-119
`handleInteraction` function, which Phase 3 had already deleted.

**Files changed (net):**

| Action | File | Notes |
| --- | --- | --- |
| Added | [`internal/dispatch/policy/policy.go`](../../internal/dispatch/policy/policy.go) | Provider-neutral `Policy`, `Request`, `Decision`, `Option`, `Location`, `AllowOncePolicy`. The leaf package that breaks the `dispatch ↔ acp` import cycle. |
| Added | [`internal/dispatch/guardian/guardian.go`](../../internal/dispatch/guardian/guardian.go) | Production guardian Policy. Holds an LLM + validator AgentDef + the current PlanStep. `Decide` calls the validator LLM with an ALLOW/DENY prompt and translates the verdict into the agent's offered allow_once/reject_once options. |
| Added | [`internal/dispatch/guardian/guardian_test.go`](../../internal/dispatch/guardian/guardian_test.go) | 7 tests covering: ALLOW→allow_once, DENY→reject_once, unparseable→deny, no-validator→deny, nil-LLM→error, LLM-error→deny+propagate, ALLOW-with-no-allow-option→cancel. |
| Modified | [`internal/dispatch/acp/connection.go`](../../internal/dispatch/acp/connection.go) | `Connection.Prompt` now takes `policy.Policy`; `NewSession` drops the mcpServers parameter (the SDK still gets an empty slice on the wire — Phase 1's "no client capabilities" stance hasn't changed, only the public API). |
| Modified | [`internal/dispatch/acp/client.go`](../../internal/dispatch/acp/client.go) | `Policy` / `PolicyDecision` / `AllowOncePolicy` removed (moved to `dispatch/policy`); `RequestPermission` now translates acpsdk types into `policy.Request` at the boundary and `policy.Decision` back, so Policy implementations never import acpsdk. |
| Modified | [`internal/dispatch/prompt_conn.go`](../../internal/dispatch/prompt_conn.go) | `PromptConn.Prompt` signature extends to take `policy.Policy`. `*acp.Connection` now satisfies `PromptConn` directly — the previous adapter is gone. |
| Modified | [`internal/dispatch/supervisor.go`](../../internal/dispatch/supervisor.go) | New `SupervisorConfig.PolicyForStep func(step) policy.Policy`. `Supervise` resolves the per-step policy once and threads it through every `runAttempt` of that step. Fallback when unset: `policy.AllowOncePolicy{}`. |
| Modified | [`internal/dispatch/streaming.go`](../../internal/dispatch/streaming.go) | `runAttempt` takes a `policy.Policy` and forwards it to `conn.Prompt`. The dead `EventPermissionRequest` / `EventClarifyQuestion` arms in `progressMessage` are gone (those events are no longer emitted). |
| Modified | [`internal/dispatch/dispatcher.go`](../../internal/dispatch/dispatcher.go) | `Dispatcher.PolicyForStep` field added; `runWorkstream` threads it into `SupervisorConfig`. |
| Modified | [`internal/dispatch/events.go`](../../internal/dispatch/events.go) | Removed `EventPermissionRequest`, `EventClarifyQuestion`, `DriverConfig`, `ClassifyToolName`, and `AgentEvent.InteractionID` — all of them were producer-less after the bridge deletion (no aspirational fields). |
| Modified | [`internal/dispatch/events_test.go`](../../internal/dispatch/events_test.go) | Drop tests for the removed event kinds and `ClassifyToolName`. |
| Modified | [`internal/dispatch/progress_test.go`](../../internal/dispatch/progress_test.go) | Drop tests for the removed permission / clarify-question progress arms. |
| Modified | [`internal/dispatch/acp/connection_test.go`](../../internal/dispatch/acp/connection_test.go) | Tests now call `conn.NewSession(ctx, cwd)` (no nil mcpServers) and use `policy.AllowOncePolicy{}` instead of `acp.AllowOncePolicy{}`. |
| Modified | [`internal/dispatch/acp/meta_test.go`](../../internal/dispatch/acp/meta_test.go) | Same shape updates. |
| Modified | [`internal/dispatch/acp_supervise_test.go`](../../internal/dispatch/acp_supervise_test.go) | `fakePromptConn.Prompt` takes a `policy.Policy`; tests record the policy threaded in. |
| Modified | [`cmd/adopt.go`](../../cmd/adopt.go) | `acpOpenConn` returns `*acp.Connection` directly (no adapter); `realDispatch` sets `Dispatcher.PolicyForStep` to a closure that constructs a fresh `guardian.Guardian` per step. |
| Modified | [`cmd/cli.go`](../../cmd/cli.go) | `McpPermBridge` subcommand entry removed. |
| Deleted | `internal/dispatch/bridge.go`, `internal/dispatch/bridge_test.go` | Unix-socket permission bridge; replaced by ACP's native `session/request_permission`. |
| Deleted | `cmd/mcp_perm_bridge.go`, `cmd/mcp_perm_bridge_test.go` | The `locutus mcp-perm-bridge` subprocess that Claude Code's `--permission-prompt-tool` invoked. Gone with the driver model. |
| Deleted | `internal/dispatch/acp/promptconn.go` | The Phase 3 `PromptConnAdapter` shim. `*acp.Connection` now satisfies `dispatch.PromptConn` directly. |

**Design decisions taken (don't relitigate without reason):**

1. **`dispatch/policy` is a leaf package; `acp` adapts at the boundary.**
   This breaks the import cycle that would otherwise close between
   `dispatch` (which needs to reference Policy in `PromptConn.Prompt`)
   and `acp` (which already imports `dispatch` for `AgentEvent`). The
   abstract `policy.Request` / `Decision` / `Option` types let Policy
   implementations live without an acpsdk dependency — `Guardian` for
   example takes the same shape whether it's wired against ACP, a future
   transport, or a unit test.

2. **`Policy` per-Prompt was preserved.** I considered the
   "closure-in-adapter" option from Phase 3's retrospective but
   abandoned it: Guardian's `Step` field needs to change per Supervise
   call, the adapter is workstream-scoped, and rebuilding the adapter
   per step from a shared Connection got progressively uglier. The
   leaf-package extension is the right answer. The `PromptConn` interface
   now carries `Prompt(ctx, sessionID, text, pol policy.Policy)` and
   `SupervisorConfig.PolicyForStep` is the production wire-in point.

3. **Guardian ports the pre-DJ-119 behaviour verbatim.** No new
   policy-tightening. The plan called this out explicitly ("Phase 3
   ports the *current* (advisory) behavior verbatim; the policy
   tightening deserves its own DJ") and Phase 4 honored it. The
   alternatives the original `handleInteraction` already encoded are
   preserved: nil LLM → cancel-with-error; missing validator AgentDef →
   deny-by-default; LLM call errors → deny-with-fallback-Decision +
   propagate the error; ALLOW prefix → first allow_once option;
   anything else → first reject_once option. Tightening to typed
   deny-by-default-with-whitelist is a future DJ.

4. **Validator AgentDef wiring is a TODO, not part of Phase 4.** The
   `Dispatcher.AgentDefs` field is not currently populated by
   `realDispatch` for any supervision agent (validator, monitor, etc.)
   — it's a pre-existing gap. Phase 4's `guardianPolicyForStep` closure
   constructs a `Guardian` with an empty `Def`, which deliberately
   triggers the deny-by-default fallback. This matches the pre-DJ-119
   `handleInteraction` behaviour when no validator was configured. A
   broader supervision-agent registry story (outside DJ-119's scope)
   would close this gap; the Phase 4 wiring is forward-compatible.

5. **Dead event vocabulary removed.** `EventPermissionRequest`,
   `EventClarifyQuestion`, `AgentEvent.InteractionID`, `DriverConfig`,
   and `ClassifyToolName` were all producer-less after the bridge was
   deleted. Per the user's `feedback_no_aspirational_fields` memory rule,
   they're gone. If a future transport surfaces permission requests as
   inline events again, that's a fresh design — easier to re-introduce
   with the right shape than to maintain a vestigial vocabulary.

6. **`acp.Connection.NewSession` lost its mcpServers parameter.** The
   SDK still wants the field on the wire; it gets an empty slice
   internally. Phase 1's no-client-capabilities posture is unchanged,
   but the public API now matches `dispatch.PromptConn.NewSession`
   exactly — that's how `*acp.Connection` satisfies the interface
   without an adapter. Adding MCP servers later is a `NewSessionWithMCP`
   addition, not a signature change.

**Tests pass:**

```bash
go build ./... && go vet ./...
go test ./... -count=1
# 27/27 packages pass; new tests:
#   internal/dispatch/guardian — 7 tests
#   internal/dispatch/policy   — no tests (exercised via dispatch + acp)
```

**Public surface changes that downstream callers see:**

- `dispatch.SupervisorConfig.PolicyForStep` is new — production callers should populate it.
- `dispatch.Dispatcher.PolicyForStep` is new — same.
- `*acp.Connection` is now a `dispatch.PromptConn` directly; the
  `acp.PromptConnAdapter` type is gone.
- `acp.Connection.Prompt` signature: `Prompt(ctx, sessionID, text, policy.Policy)`.
- `acp.Connection.NewSession` signature: `NewSession(ctx, cwd)` (mcpServers gone).
- `acp.Policy`, `acp.PolicyDecision`, `acp.AllowOncePolicy` — removed; use `dispatch/policy.*` instead.
- `dispatch.EventPermissionRequest`, `EventClarifyQuestion`, `DriverConfig`, `ClassifyToolName`, `AgentEvent.InteractionID` — removed.
- `locutus mcp-perm-bridge` subcommand — removed.

**Explicitly NOT done in Phase 4 (still pending or for later DJs):**

- Production validator AgentDef wiring in `realDispatch` (pre-existing
  gap; not in DJ-119's scope).
- Policy tightening to typed deny-by-default-with-whitelist semantics
  (deserves its own DJ per the original plan note).
- ACP-flavoured live integration test → **Phase 6** (still pending).

---

### Phase 5 — W3C trace context propagation: DONE 2026-05-14

`_meta.traceparent` now flows both directions on the ACP transport per
DJ-119's "Strict upgrades" note. Outgoing: every `Initialize`,
`NewSession`, `Prompt`, and `Cancel` call injects the active span's
SpanContext (when ctx carries one) as a W3C traceparent string into the
SDK request's `Meta` map. Inbound: `SessionUpdate` and `RequestPermission`
handlers extract any caller-supplied `_meta.traceparent` and surface it
through the package slog logger as the structured field
`remote_traceparent`. The protocol's other W3C reservations (`tracestate`,
`baggage`) are deliberately out of scope — Locutus has no upstream
producer for them and aspirational fields would just be load-bearing
later as constraints we forgot we'd taken on.

**Files changed (net):**

| Action | File | Notes |
| --- | --- | --- |
| Added | [`internal/dispatch/acp/meta.go`](../../internal/dispatch/acp/meta.go) | `injectTraceparent`, `encodeTraceparent`, `extractTraceparent`, `logInboundTraceparent` |
| Added | [`internal/dispatch/acp/meta_test.go`](../../internal/dispatch/acp/meta_test.go) | 7 new tests against fakeAgent + io.Pipe (round-trip, no-span no-op, preserve caller-supplied, outgoing-with-span, outgoing-without-span, inbound-on-SessionUpdate, inbound-on-RequestPermission) |
| Modified | [`internal/dispatch/acp/connection.go`](../../internal/dispatch/acp/connection.go) | Inject traceparent on `Initialize` / `NewSession` / `Prompt` / `Cancel` |
| Modified | [`internal/dispatch/acp/client.go`](../../internal/dispatch/acp/client.go) | Log inbound traceparent on `SessionUpdate` / `RequestPermission` |
| Modified | [`internal/dispatch/acp/connection_test.go`](../../internal/dispatch/acp/connection_test.go) | Extended `fakeAgent` with `onNewSession` hook so tests can inspect the inbound request payload from the agent side |

**Design decisions taken (don't relitigate without reason):**

1. **OTel over a string-only helper.** `go.opentelemetry.io/otel/trace`
   is already a direct dependency (used by `internal/agent/otel.go`).
   Reusing `oteltrace.SpanContext` lets one type flow from a supervisor
   span → outgoing `_meta.traceparent` string → inbound parse without
   introducing a parallel string-only implementation. Adopting the OTel
   types here also makes the eventual span-link upgrade (see decision 2)
   a one-line change rather than a refactor.

2. **Inbound surface is slog, not a span link, today.** The client's
   `SessionUpdate` / `RequestPermission` handlers do not create OTel
   spans — there is no handler-side span to attach a link to. Logging
   via `slog.Debug("...", "remote_traceparent", tp, ...)` makes the
   inbound context discoverable in trace correlation tools without
   committing to a span-creation model the rest of Locutus doesn't have
   yet. `logInboundTraceparent`'s signature accommodates a future
   promotion to span links without touching callers.

3. **Never overwrite caller-supplied `_meta.traceparent`.**
   `injectTraceparent` checks for an existing key on the meta map before
   writing. Coexisting `_meta` extensions (Phase 0 flagged
   `_meta.claudeCode.promptQueueing` as one such case) are untouched —
   the SDK's `Meta` field is a shared dict, not ours exclusively.

4. **Phase 5 is additive — no public-API change.** `Connection`,
   `PromptConnAdapter`, and `Client` keep the same surface; the
   supervisor in `streaming.go` and the cmd wire-up in `adopt.go` are
   untouched. Phase 4 (production policy port) can land without any
   merge friction from Phase 5.

**Tests pass:**

```bash
go build ./... && go vet ./...
go test ./internal/dispatch/acp/ -count=1 -v   # 13/13 pass
go test ./internal/dispatch/... -count=1       # 2/2 packages pass
```

**Public surface change:** none.

---

### Phase 7 — `locutus init` preflight for ACP agent binaries: DONE 2026-05-14

`locutus init` now checks every binary declared in the ACP agent registry
against `$PATH` and surfaces a per-agent install hint for each one that's
missing. The check is read-only — it does not invoke the binary, only
resolves it via `exec.LookPath`. `init` still writes the `.borg/` scaffold
unconditionally; the preflight is a check-and-report, not check-and-fail.

**Files changed (net):**

| Action | File | Notes |
| --- | --- | --- |
| Added | [`internal/dispatch/acp/registry.go`](../../internal/dispatch/acp/registry.go) | Canonical `AgentSpawns` map, now exported. Phase 3's local `agentSpawns` in `cmd/adopt.go` moved here so `init` and `adopt` share one source of truth. |
| Added | [`internal/dispatch/acp/preflight.go`](../../internal/dispatch/acp/preflight.go) | `Preflight()` + `PreflightResult` + `MissingCount` + `installHints` table. `lookPath` indirection lets tests stub `exec.LookPath`. |
| Added | [`internal/dispatch/acp/preflight_test.go`](../../internal/dispatch/acp/preflight_test.go) | Three tests: missing-with-hints, all-present, all-missing — all using the stubbed `lookPath`. |
| Updated | [`cmd/init.go`](../../cmd/init.go) | Calls `acp.Preflight()`, renders a per-agent block to stderr, also exposes the result slice in `--json` output under `"acp_preflight"`. |
| Updated | [`cmd/adopt.go`](../../cmd/adopt.go) | Local `agentSpawns` deleted; `acpOpenConn` now reads from `acp.AgentSpawns`. |

**Design decisions taken (don't relitigate without reason):**

1. **Registry lives in `internal/dispatch/acp` as `AgentSpawns`** (the
   suggested home from the Phase 7 task), not `internal/preflight/acp/`.
   `internal/preflight/` is already the home of the DJ-071 clarification
   protocol — a wholly different concept. Co-locating the registry with
   the `Spawn` type it produces values for keeps the package self-contained:
   `acp.AgentSpawns` is the map, `acp.Preflight()` is the check.

2. **Preflight is check-and-report, not check-and-fail.** `locutus init`
   completes successfully — writes `.borg/` scaffold, updates `.gitignore`,
   prints the status summary — regardless of which ACP binaries are
   missing. The user can install agents at their leisure and re-run any
   verb that dispatches code (`adopt`). Failing `init` because an agent
   the user doesn't intend to use isn't installed would be hostile; the
   missing-binary message is itself the actionable signal and the closing
   line ("`locutus adopt` will fail for any workstream routed to a missing
   agent until installed") tells the user the consequence.

3. **Preflight output rendered to stderr, not stdout.** Matches the
   existing `init` "warning: not inside a git repository" line, which
   also goes to stderr. Keeps stdout clean for the status-summary table
   that JSON-mode callers might be parsing positionally (they should use
   `--json`, but be kind).

4. **JSON mode surfaces the full preflight slice.** `--json init` returns
   `{"status": "ok", "project": "...", "acp_preflight": [...]}`. The
   preflight results are part of the structured response so scripts
   wrapping `locutus init` can decide what to do about missing agents
   (e.g. an installer that pipes the install hints into the user's
   provisioning system).

5. **Install hints are concrete strings, not URL-only.** Each hint
   contains the actual install command (or, for `codex-acp`, the URL to
   the release page plus the critical "NOT on crates.io" warning). The
   `codex-acp` hint is the one with non-obvious failure mode — Phase 0
   verification confirmed `cargo install codex-acp` fails because the
   crate isn't published. The test asserts the hint cites the GitHub
   releases URL so a regression that drops back to the cargo suggestion
   is loud.

6. **Gemini install hint settled on `npm i -g @google/gemini-cli`.**
   That's the canonical install for the official Google Gemini CLI per
   the upstream README at
   [google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli);
   the `--acp` flag is shipped in that package. The hint also names the
   upstream README so users wanting a non-npm install path have a single
   reference.

7. **`PreflightResult` is the minimal shape `init` actually renders.**
   `AgentID`, `Binary`, `Found`, `ResolvedAt`, `InstallHint`. No
   `Version`, no `LastChecked`, no `ProbeOutput` — adding those without
   a UX consumer would be aspirational per the user's
   `feedback_no_aspirational_fields` memory rule. Revisit if version-skew
   surfaces as a real failure mode (e.g. Phase 0 verification needing to
   re-pin a known-good build).

**Tests pass:**

```bash
go build ./... && go vet ./...
go test ./internal/dispatch/acp/ -count=1
# ok  github.com/chetan/locutus/internal/dispatch/acp  0.4s
```

Full suite (`go test ./... -count=1`) is green except for a pre-existing
flake in `cmd.TestRunRefineSupersede_*` that reproduces on unmodified
main (≈2/5 runs); unrelated to Phase 7.

**UX example (no binaries on PATH):**

```text
ACP coding-agent binaries:
  missing  claude-code (claude-agent-acp)
           install: npm i -g @agentclientprotocol/claude-agent-acp@0.33.1 (requires Node + npm)
  missing  codex (codex-acp)
           install: download the latest release tarball from https://github.com/zed-industries/codex-acp/releases and place `codex-acp` on $PATH (note: NOT on crates.io — `cargo install codex-acp` does not work)
  missing  gemini (gemini)
           install: npm i -g @google/gemini-cli (see https://github.com/google-gemini/gemini-cli for alternatives)

3 of 3 ACP agent binaries not on $PATH. `locutus adopt` will fail for any workstream routed to a missing agent until installed.
```

**Explicitly NOT done in Phase 7 (still pending in later phases):**

- A `locutus doctor` / `locutus preflight` verb that re-runs the check on
  demand without re-bootstrapping. Out of scope for Phase 7 — the user
  can re-run `init` against an already-initialised project, which is a
  no-op for the scaffold step and prints the fresh preflight. A dedicated
  verb is a Phase-8-or-later UX nicety.
- Version probing (calling `claude-agent-acp --version` etc) to detect
  version skew between the user's installed ACP server and the Phase 0
  known-good build. Deferred until a real version-skew bug surfaces.
- Surface the preflight in `locutus status`. The status command's
  scope is the spec graph; binary-on-PATH state belongs in `init` (the
  bootstrap moment) and a future `doctor` verb.

---

### Phase 8 — Docs + DJ amendments + scaffold audit: DONE 2026-05-14

Pure docs/scaffold work. No Go code changed; build, vet, and the full
test suite stayed green throughout.

**Files changed (net):**

| Action | File | Notes |
| --- | --- | --- |
| Modified | [`docs/DECISION_JOURNAL.md`](../../docs/DECISION_JOURNAL.md) (DJ-010) | Forward-pointer block added near the DJ header noting DJ-010's wire-layer portion is superseded by DJ-119 while the supervision orchestration model below is preserved. Status field bumped to `shipped (partially superseded by DJ-119)`. |
| Added | [`docs/DECISION_JOURNAL.md`](../../docs/DECISION_JOURNAL.md) (DJ-120) | New DJ formalizing the narrowing of DJ-074: cross-process `adopt` resume narrows from conversation-level (`--resume <id>`) to step-level (git feature branch + `StepID` skipping). Alternatives considered include `session/load`-based revival, a daemonized agent subprocess, and detaching the agent on Locutus exit — all deferred. |
| Modified | [`README.md`](../../README.md) | `adopt` bullet now mentions ACP; new "Coding agents" section listing supported agent ids (`claude-code` / `codex` / `gemini`) and their ACP-server binaries, pointing readers at `locutus init` preflight as the source of truth for install commands. Removed any historical references to `--permission-prompt-tool`, `locutus_permission`, `mcp-perm-bridge`, NDJSON streaming. |
| Modified | [`docs/agent-conventions.md`](../../docs/agent-conventions.md) | Audited — file covers council/pipeline personas, not coding-agent wire layer; no references to `StreamingDriver`, `BuildCommand`, `--permission-prompt-tool`, or related deprecated terms. No surgical edits required. |
| Audited (no changes) | [`internal/scaffold/`](../../internal/scaffold/) | Grepped for `StreamingDriver`, `BuildCommand`, `ParseStream`, `--permission-prompt-tool`, `locutus_permission`, `stream-json`, `--include-partial-messages`. Zero hits. The "driver" mentions in `synthesizer.md` and `refiner-supersede-*.md` are semantic uses ("change driver", "database driver" as a hypothetical) — none reference the pre-DJ-119 wire layer. |

**Design decisions taken (don't relitigate without reason):**

1. **DJ-010 is partially superseded, not deprecated.** The forward-pointer
   is surgical: it tells the reader the wire-layer description below is
   replaced by DJ-119, but the supervision orchestration model (retry
   loop, validator, monitor, churn detection) is unchanged and still
   load-bearing. A reader landing on DJ-010 for orchestration context
   gets correct information; one looking for the transport story gets
   redirected.

2. **DJ-120 is a refinement of DJ-074, not a replacement.** It captures
   the narrowing of the resume contract under the ACP lifecycle that
   Phase 3 already implicitly took. Phrasing the DJ as "refines" rather
   than "supersedes" is accurate: DJ-074's feature-branch durability
   guarantee is preserved untouched; only the conversation-level resume
   half is changed. If `session/load`-based revival becomes load-bearing
   later, that would be a new DJ superseding DJ-120, not DJ-074.

3. **`docs/plans/streaming-supervision.md` left as-is.** It's a
   historical plan doc, not user-facing reference material. The DJ-line
   principle ("the journal is the source of truth on current behavior")
   covers the contradiction: DJ-119 is the current story, the old plan
   doc is preserved for context. Phase 4 may want to add a superseded-by
   header when it deletes `bridge.go` and the `--permission-prompt-tool`
   plumbing.

4. **No scaffold edits, but the audit was explicit.** A "zero hits"
   record under multiple grep patterns is the artifact of the audit —
   future phases should re-grep the same patterns to verify nothing
   reintroduces the deprecated terms. Treated as a known-clean baseline.

**Tests pass:**

```bash
go build ./... && go vet ./...   # both clean
go test ./... -count=1           # all 26 packages pass
```

---

### Phase 6 — Fresh protocol-contract tests: DONE 2026-05-14

In-process coverage of the ACP contract grew from 3 happy-path tests
(connection_test.go) plus 7 traceparent tests (meta_test.go) plus 3
permission/policy tests (guardian + acp_supervise) to a comprehensive
contract suite. New env-var-gated live test verifies the full pipeline
against a real ACP-server subprocess.

**Files added:**

| File | Tests | Notes |
| --- | --- | --- |
| [`internal/dispatch/acp/contract_test.go`](../../internal/dispatch/acp/contract_test.go) | 10 tests (one with 5 sub-cases for stop-reason variants, one with 4 sub-cases for tool-call-update status) | Comprehensive in-process protocol coverage |
| [`internal/dispatch/acp/live_test.go`](../../internal/dispatch/acp/live_test.go) | 1 test, env-var-gated | Replaces the deleted `live_integration_test.go` |

**Contract test inventory:**

1. **`TestContract_AcpConnectionSatisfiesPromptConn`** — compile-time
   guard that `*acp.Connection` satisfies `dispatch.PromptConn`. Without
   it, a future signature change on either side could quietly break
   `cmd/adopt.go`'s `acpOpenConn`, which returns `*Connection` where
   `PromptConn` is wanted.
2. **`TestContract_SequentialPromptsOnOneSession`** — three prompts on
   one Connection + one session must all succeed with their events
   routed to the right caller. Verifies the DJ-119 spawn-per-workstream
   lifecycle premise.
3. **`TestContract_ToolCallEventTranslation`** — `SessionUpdateToolCall`
   → `EventToolCall` with `ToolName` from `Title`, `ToolInput` from
   `RawInput`, and `FilePaths` lifted from `Locations`.
4. **`TestContract_ToolCallUpdateTerminalStatusOnly`** (4 sub-cases) —
   `status=completed` surfaces as `EventToolResult`; `status=nil`,
   `in_progress`, and `pending` are all dropped. Keeps the monitor's
   event stream free of progress-only noise.
5. **`TestContract_AgentThoughtChunkSurfacesAsText`** — `AgentThoughtChunk`
   reasoning content routes to `EventText` so the monitor's
   cycle-detection sees the agent's narration.
6. **`TestContract_StopReasonVariantsCarryAsTerminalText`** (5 sub-cases) —
   `EndTurn`, `MaxTokens`, `MaxTurnRequests`, `Refusal`, `Cancelled` all
   produce a terminal `EventResult` with the `stopReason` verbatim in
   `Text`. The validator distinguishes these.
7. **`TestContract_NonTextContentDropped`** — image content blocks are
   dropped from the supervisor event stream (preserved on `Raw` for
   archival). Keeps the validator's input free of binary blobs.
8. **`TestContract_SessionCancelPropagates`** — `conn.Cancel` triggers a
   `session/cancel` that surfaces as a terminal `EventResult` with
   `Text="cancelled"`. The supervisor's `runAttempt` ctx-cancel path
   relies on this.
9. **`TestContract_LateNotificationAfterPromptComplete`** — a
   `SessionUpdate` arriving after the events channel has closed must
   not panic or hang. Validates the client's defensive `lookup`-then-
   drop late-delivery handling.
10. **`TestContract_NilPolicyCancelsPermissionRequest`** — passing nil
    as the Policy to `conn.Prompt` results in `outcome: cancelled` for
    any `session/request_permission` the agent issues. The documented
    semantics of nil in connection.go.
11. **`TestContract_AllowOncePolicySelectsCorrectOption`** —
    `policy.AllowOncePolicy` scans the full option list and picks any
    allow-kind option (even if it's not first). Edge-case guard for the
    Phase 3 placeholder.

**Live integration test:**

`TestLiveACP` (in `live_test.go`, package `acp_test`) opens a real ACP
subprocess and runs Initialize + NewSession + Prompt + Close end-to-end.

- Gated by the `LOCUTUS_LIVE_ACP` env var; supported values are the
  registered agent ids (`claude-code`, `codex`, `gemini`). Without the
  env var the test skips with a one-line hint.
- When the env var is set but the agent's binary isn't on `$PATH`, the
  test skips with a hint pointing at `locutus init` (Phase 7's preflight).
- Sends a trivial "Reply with just the word OK." prompt — chosen to
  avoid tool-use permission flows, so the test covers no-policy paths.
- Drains the event stream and asserts a terminal `EventResult` with a
  non-error stop reason. Counts text vs tool events for `t.Logf`
  diagnostics.

**Run instructions:**

```bash
# In-process contract tests (always run)
go test ./internal/dispatch/acp/ -count=1 -run TestContract

# Live test against a real ACP server (opt-in)
LOCUTUS_LIVE_ACP=gemini      go test -run TestLiveACP ./internal/dispatch/acp/
LOCUTUS_LIVE_ACP=claude-code go test -run TestLiveACP ./internal/dispatch/acp/
LOCUTUS_LIVE_ACP=codex       go test -run TestLiveACP ./internal/dispatch/acp/
```

**Known teardown caveat (documented in live_test.go):** `acp.Connection`
wires the subprocess's stderr to `os.Stderr` so production users see
agent diagnostics. On agents that continue writing stderr after Kill
(observed: `gemini --acp` writing its own startup-phase output), the
test binary's stderr FD can stay open past `go test`'s WaitDelay and
the framework reports `exec: WaitDelay expired before I/O complete`.
The test assertions all pass before that point; the warning is a
cleanup artifact, not a regression signal. Look at the `t.Logf` lines
to verify the actual outcome.

**Design decisions taken (don't relitigate without reason):**

1. **Tests are fresh, not conversions.** The plan explicitly calls this
   out: "existing tests are scaffolding against an unexercised contract,
   so converting them isn't a preservation exercise." Every test under
   `contract_test.go` is written from scratch against the ACP contract,
   not adapted from the deleted NDJSON-driver tests. Same for
   `live_test.go`.

2. **In-process coverage runs in `go test ./...`; live coverage is
   opt-in.** Same gating model the pre-DJ-119 live test used
   (`LOCUTUS_INTEGRATION_TEST=1` → `LOCUTUS_LIVE_ACP=<agent>`). Keeps
   CI fast and reproducible, makes the live path discoverable for
   anyone running `go test -run TestLiveACP`.

3. **Live test handles "env var set, binary missing" gracefully.** Skip
   with a hint pointing at `locutus init`'s preflight (Phase 7), rather
   than fail. A user trying multiple agents shouldn't see a hard failure
   when one isn't installed.

4. **Live test is a smoke test, not a behavior test.** It checks the
   wire end-to-end (Initialize → NewSession → Prompt → terminal event →
   Close), not what the model says. Prompts the model is unlikely to
   issue tool calls for, so the test also covers the no-policy path
   without needing a guardian wired.

**Tests pass:**

```bash
go build ./... && go vet ./...
go test ./... -count=1
# 27/27 packages pass; contract suite: 11 tests + 9 sub-cases
# live test: skipped without env var
```

---

## All phases complete

DJ-119 migration is done. Subsequent work that builds on the ACP
transport (validator AgentDef wiring in `realDispatch`, policy
tightening to typed deny-by-default, `session/load`-based cross-process
resume revival) belongs in its own DJ, not this plan.

---

## How to verify the current state

```bash
# Build clean, vet clean, tests pass
go build ./... && go vet ./... && go test ./...

# acp package tests in particular
go test ./internal/dispatch/acp/ -v -count=1

# Re-run Phase 0 verification against any ACP server
cd /tmp/acp-verify && go build -o phase0 ./cmd/phase0
./phase0 -label gemini      -cmd 'gemini --acp'
./phase0 -label claude-code -cmd 'claude-agent-acp'
./phase0 -label codex       -cmd '/tmp/codex-acp-binary'
```

## Pointers a fresh session should follow before resuming Phase 3

1. Read [`docs/DECISION_JOURNAL.md`](../../docs/DECISION_JOURNAL.md)
   §DJ-119 in full. The DJ is the authoritative design; this plan is
   progress tracking.
2. Read the four files under
   [`internal/dispatch/acp/`](../../internal/dispatch/acp/) — the public
   interface and design decisions are documented inline.
3. Read [`internal/dispatch/supervisor.go`](../../internal/dispatch/supervisor.go)
   and [`streaming.go`](../../internal/dispatch/streaming.go) before
   designing the Phase 3 surgery. The current `runAttempt` event-loop
   shape is what we're preserving; only the transport changes.
4. Walk the policy decisions in
   [`internal/dispatch/bridge.go`](../../internal/dispatch/bridge.go) and
   its supervisor-side handler to inform the Phase 4 Policy port. Confirm
   with the user before writing the production policy — port-verbatim vs
   tighten-with-DJ is a real choice.
5. **Don't relitigate the design decisions taken in Phase 1** unless
   surfacing a reason in chat first. The per-Prompt Policy injection,
   no-fs-no-terminal capability stance, channel-based Prompt return, and
   defensive late-delivery handling are committed.

## Cross-references

- DJ-119 (authoritative design): `docs/DECISION_JOURNAL.md`
- DJ-010 (supervision design preserved): `docs/DECISION_JOURNAL.md`
- DJ-091 (session trace storage preserved): `docs/DECISION_JOURNAL.md`
- Phase 0 harness: `/tmp/acp-verify/cmd/phase0/main.go` (not under
  Locutus repo; lives in the cloned `coder/acp-go-sdk` checkout so it
  can use the SDK's local module without setup)
- Phase 0 result JSONs: `/tmp/acp-verify/{gemini,claude,codex}.json`
