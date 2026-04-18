# Plan: Streaming Supervision of Coding Agents

## Context

Today's supervisor runs each coding agent in batch mode: `CommandRunner` forks the process, waits for completion, returns `[]byte`, and the LLM validates the final output post-hoc. This works for tests but can't handle real agent behaviors that surface mid-execution:

- **Permission requests** (e.g., agent wants to run `rm -rf` or a non-allowlisted shell command)
- **Clarifying questions** (e.g., agent uses `AskUserQuestion` tool)
- **Churn** (e.g., agent reads same file 5 times, edits the same lines back and forth, cycles between two approaches) — distinct from retry: churn is repeating cycles *within a single attempt*, detected from the event stream, not a new attempt after failure
- **Scope drift** (e.g., agent modifies files not in `ExpectedFiles`, adds unrequested features)

### Churn vs retry

These are different phenomena and this plan keeps them distinct:

- **Retry** is vertical — a whole new attempt after a failure signal (test failed, validation rejected output, timeout). Lives in `Supervisor.Supervise`'s outer loop across attempts.
- **Churn** is horizontal — a repeating cycle of actions within a single attempt without clear progress (reads the same file, reverts and re-edits, loops between two approaches). Detected by observing the event stream mid-attempt.

Churn detection is a **cost-saving short-circuit**: if the agent is clearly cycling, abort the attempt early to avoid burning tokens, then let the retry loop handle the "try again" semantics with churn reasoning as feedback. Churn never replaces retry; it truncates doomed attempts so retry can fire sooner with better guidance.

Claude Code, Codex, and Gemini CLI all support NDJSON streaming. The supervisor should consume the stream as events arrive, use cheap Go code as a watchdog, and invoke the LLM for judgment when the watchdog triggers. This keeps LLM cost bounded while still using its nuance for fuzzy decisions.

## Principle: fast-tier LLM monitor (no Go heuristics for fuzzy decisions)

Every "fuzzy" supervision decision uses a fast-tier LLM (Haiku-class) as the monitor. No hand-written Go heuristics try to detect patterns in event streams — that's a losing battle where we'd perpetually chase edge cases as coding agents develop new behaviors.

The structure:

- **Sliding window monitor**: every K events (or every T seconds, whichever comes first), feed the last N events as a compact summary to a fast-tier LLM
- **Structured verdict**: the monitor returns `{"is_cycle": bool, "pattern": "...", "reasoning": "...", "confidence": 0.0-1.0}` — we only act when `is_cycle == true` and confidence is high
- **Cooldown**: minimum K events between invocations so we don't hammer even the cheap model
- **Circuit breaker**: if the monitor errors repeatedly or returns persistent `"unclear"`, stop invoking for this attempt and let it complete; validation catches problems at the end

Starting parameters (all tuned via the monitor's *prompt*, not Go code):

- K = 15 events between checks
- T = 30 seconds max interval
- N = last 20 events fed to monitor
- Cooldown = at least 10 events between invocations

This gives us:

- **Adaptability** — new coding agent behaviors are handled by updating the monitor agent's prompt, not by adding Go heuristics
- **Nuance** — LLM understands semantic churn (same goal, different approaches) vs mechanical repetition
- **Bounded cost** — fast tier + sliding window + cooldown keeps monitor calls sparse relative to event volume

The same fast-tier monitor pattern applies to other fuzzy decisions: scope drift, stalled progress, invented requirements. Each has its own prompt and structured verdict schema.

### Where Go code still has a role

Go code handles **structural, unambiguous decisions** — things that are true by definition, not by judgment:

- Parsing NDJSON into `AgentEvent`s (schema-driven)
- Tracking the event window (ring buffer bookkeeping)
- Enforcing cooldown between monitor invocations (counter math)
- Routing events from an in-process MCP tool handler (our `locutus_permission` bridge) into the supervisor's event channel — not inferred from text, emitted directly by the handler

Everything judgment-based goes to the LLM.

## Scope

- Normalized `AgentEvent` type shared across provider drivers
- Streaming `CommandRunner` returning `io.ReadCloser`
- **Claude Code** NDJSON parser + reassembler (other coding-agent drivers deferred; see below)
- **Watchdog + LLM judge pattern** for fuzzy supervision decisions
- Permission handler via an in-process MCP bridge (`locutus mcp-perm-bridge`) — not by parsing the agent's stdout stream
- Integration with existing `Supervisor.Supervise` retry loop (one implementation, not two)
- MCP progress notifications emitted from supervisor event loop

Deferred (future plans):

- **Codex and Gemini CLI drivers.** Codex captured partial start+error events on 2026-04-17 but requires `OPENAI_API_KEY` for a full happy-path fixture. Gemini not yet attempted. Land the claude driver end-to-end first; add other drivers in a follow-up part once fixtures and auth are available.
- `AskUserQuestion` equivalent routing — verify whether Claude Code exposes a question-prompt mechanism we can bridge; if so, handle via the same MCP-bridge pattern as permission. Otherwise drop from scope.
- Multi-provider LLM routing (fast/balanced/strong → specific model via LLM pick)
- Rate limiting per provider (tokens per minute)
- Cost budget enforcement
- Agent tools registered via Genkit
- Wiring GenKit LLM provider actually end-to-end

## Part 1: Normalized event type

```go
// AgentEvent is a provider-agnostic event from a coding agent stream.
type AgentEvent struct {
    Kind        EventKind
    Timestamp   time.Time
    SessionID   string          // set on init events
    ToolName    string          // set on tool events
    ToolInput   map[string]any  // tool call parameters
    Text        string          // accumulated text for text/result events
    FilePaths   []string        // files touched by this event
    Raw         json.RawMessage // original provider event for debugging
}

type EventKind string

const (
    // Parsed directly from provider NDJSON — these are always reliably identifiable:
    EventInit       EventKind = "init"
    EventText       EventKind = "text"
    EventToolCall   EventKind = "tool_call"     // complete tool call
    EventToolResult EventKind = "tool_result"   // tool finished, agent resumes
    EventRetry      EventKind = "api_retry"
    EventResult     EventKind = "result"         // final response
    EventError      EventKind = "error"

    // Emitted by the Part 7 MCP bridge (not by the stream parser):
    EventPermissionRequest EventKind = "permission_request"
    EventClarifyQuestion   EventKind = "clarify_question"
)
```

### How `EventPermissionRequest` and `EventClarifyQuestion` get set

These events do **not** come from parsing the coding agent's public stream. Verified against real Claude Code output on 2026-04-17: `--permission-prompt-tool <name>` causes claude to invoke the permission tool as a **separate MCP RPC**, not as a tool_use in the stream. The stream only shows the original restricted tool's tool_use (e.g., `Bash`) followed by a tool_result reflecting our allow/deny.

So permission detection happens inside **our own MCP tool handler**, not in the stream parser. See Part 7 for the full MCP-bridge architecture.

`ClassifyToolName` remains defined and tested in Part 1 as a utility: drivers whose providers *do* surface permission/question routing as tool names (hypothetical — none known today) can use it. It is not wired into the claude stream parser in Part 3+4, because for claude the stream never contains a permission/question tool_use.

### What the LLM monitor handles

Everything judgment-based about tool calls goes to the monitor:

- Is this a cycle? (same tool called repeatedly without progress)
- Is this drift? (editing files outside `ExpectedFiles`)
- Is this a stall? (long gap between meaningful events)
- Is this invented scope? (adding unrequested features)

The monitor sees `EventToolCall` events and makes these determinations via its prompt. Go code never tries to classify tool intent.

### Routing summary

| Signal | Source | Latency |
|---|---|---|
| Permission needed | MCP bridge tool handler (Part 7) emits event | Immediate — the agent is blocked waiting |
| Clarifying question | Same bridge, different tool (TBD in Part 7) | Immediate — the agent is blocked waiting |
| Cycle / drift / stall / scope creep | LLM monitor with sliding window | Periodic (every K events or T seconds) |

Permission and question routing is immediate because the coding agent is *blocked* waiting for a response — we can't wait K events. Cycle/drift/stall are observations over time, not blocking calls, so periodic LLM checks are acceptable.

## Part 2: Streaming CommandRunner

```go
// Old: type CommandRunner func(cmd *exec.Cmd) ([]byte, error)
// New:
type CommandRunner func(cmd *exec.Cmd) (io.ReadCloser, error)
```

Production runner: `cmd.StdoutPipe()` + `cmd.Start()`; returns a `ReadCloser` that waits for the process on close. Test runner: wraps `[]byte` as `io.NopCloser(bytes.NewReader(...))` via a `batchRunner(bytes)` helper so existing tests still work.

## Part 3: Per-driver stream parsers (pull-based)

The plan change: use a pull-based iterator instead of a channel. Supervisor controls lifecycle, no goroutine leak on cancellation.

```go
// StreamParser reads provider-specific NDJSON and yields normalized events.
type StreamParser interface {
    Next(ctx context.Context) (AgentEvent, error) // returns io.EOF when done
    Close() error
}

type AgentDriver interface {
    BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd
    BuildRetryCommand(...)
    ParseOutput(output []byte) (DriverOutput, error)  // batch, keep for tests
    ParseStream(r io.Reader) StreamParser              // streaming
    RespondToAgent(sessionID, response string) (*exec.Cmd, error)  // returns resume command
}
```

Each driver implements `ParseStream` with its provider's NDJSON schema. The parser owns a delta reassembler internally (content_block_start → delta accumulator → content_block_stop emits complete `AgentEvent`).

### The claude stream parser has no permission detection

Claude Code's permission-prompt tool is not visible as a public stream event — confirmed against real output. The parser therefore has no permission or question classification logic. It recognizes: `system/init`, `stream_event` (message/content lifecycle and deltas), `user` (tool_result), `result`, `error`, `api_retry`, `rate_limit_event`. Assistant-level full-message events are ignored when `--include-partial-messages` is set (we rely on the stream_event reassembly instead, to avoid double-emitting).

### `RespondToAgent` is a resume invocation, not a live write

Returning `*exec.Cmd` makes it explicit: responding to the agent means invoking a new process with `--resume <session_id>` plus the response text. The current session ends; the next process continues from its state. This is correct for all three providers (Claude Code, Codex, Gemini CLI) — they're all session-based. The mental model in the supervisor is "pause, respond, resume in a new invocation" — not "inject a message into the running process."

## Part 4: Event reassembler

The stream emits deltas; we need complete logical events. Reassembler lives inside each driver's parser:

- Track current content block type (text vs tool_use vs thinking)
- Accumulate `input_json_delta` chunks until `content_block_stop`
- Accumulate `text_delta` chunks until `content_block_stop`
- Drop `signature_delta` (thinking-block signature — not useful to the supervisor)
- Parse accumulated JSON into `ToolInput` map
- Emit the complete event on block stop

Provider-specific because delta formats differ.

## Part 5: Supervisor event loop (single implementation)

The existing batch supervisor is replaced with the streaming one. Tests that used `[]byte` runners use `batchRunner()` to wrap bytes as a reader. One implementation; tests still pass.

```go
func (s *Supervisor) runAttempt(ctx, step, driver, workDir, sessionID, feedback) (*attemptResult, error) {
    cmd := buildCommand(...)
    stream, err := s.runner(cmd)
    if err != nil { return nil, err }
    defer stream.Close()

    parser := driver.ParseStream(stream)
    defer parser.Close()

    var result attemptResult
    mon := newMonitor()

    // events comes from two sources: the claude stream parser and the MCP
    // permission bridge (Part 7). Both funnel into one channel so runAttempt
    // sees one unified event stream.
    events := s.mergeParserAndBridge(ctx, parser)

    for evt := range events {
        s.emitProgress(evt)
        result.accumulate(evt)
        mon.Observe(evt)

        // Structural signals emitted by the MCP bridge (not by the stream parser).
        switch evt.Kind {
        case EventPermissionRequest, EventClarifyQuestion:
            response, err := s.handleInteraction(ctx, step, evt)
            if err != nil { return &result, err }
            // For permission, the bridge is waiting on a socket for the decision;
            // handleInteraction returns the response which the caller forwards
            // back to the bridge. No claude resume needed — claude resumes when
            // the MCP tool returns.
            continue
        }

        // Periodic LLM monitor check — no heuristics, just cooldown.
        if mon.ShouldCheck() {
            verdict, err := s.monitorCycle(ctx, step, mon.RecentEvents())
            mon.MarkChecked(err)
            if err != nil {
                // Circuit breaker handles repeated errors inside MarkChecked.
                continue
            }
            if verdict.IsCycle && verdict.Confidence >= 0.7 {
                return &result, &churnDetected{
                    pattern:   verdict.Pattern,
                    reasoning: verdict.Reasoning,
                }
            }
        }
    }
    return &result, nil
}
```

## Part 6: Fast-tier LLM monitor (no heuristics)

The monitor is a sliding window over the event stream, invoked periodically to ask a fast-tier LLM whether the agent is cycling. There are no Go heuristics deciding whether an event pattern is "suspicious" — that judgment is the LLM's job.

```go
type monitor struct {
    events        []AgentEvent // ring buffer, last ~30 events
    eventsSinceCheck int        // counter since last LLM invocation
    lastCheckAt      time.Time
    windowSize       int         // how many events to send to the LLM
    checkEveryEvents int         // minimum events between checks (default 15)
    checkEveryTime   time.Duration // or every T seconds (default 30s)
    circuitTrips     int         // consecutive monitor errors → stop invoking
}

// Observe records an event in the ring buffer.
func (m *monitor) Observe(evt AgentEvent)

// ShouldCheck returns true when enough events or time has passed since the
// last monitor invocation, and the circuit breaker is not tripped.
func (m *monitor) ShouldCheck() bool

// RecentEvents returns the last N events for the monitor prompt.
func (m *monitor) RecentEvents() []AgentEvent

// MarkChecked advances the check counter after a monitor invocation.
func (m *monitor) MarkChecked(err error)
```

The monitor has zero pattern-detection logic. It's a ring buffer with a cooldown clock. All decisions about "is this cycling?" come from the LLM.

### LLM judge for churn

When the watchdog fires, the supervisor calls the LLM with the recent event summary:

```go
type CycleVerdict struct {
    IsCycle    bool    `json:"is_cycle"`
    Reasoning  string  `json:"reasoning"`
    Confidence float64 `json:"confidence"`  // 0.0-1.0
    Pattern    string  `json:"pattern,omitempty"` // "file_thrashing", "tool_loop", "approach_cycling", etc.
}

func (s *Supervisor) monitorCycle(ctx context.Context, step spec.PlanStep, events []AgentEvent) (*CycleVerdict, error) {
    def := s.cfg.AgentDefs["monitor"]
    if def.ID == "" {
        // Monitor agent optional. Disable cycle detection and log once per
        // supervisor (tracked via s.monitorDisabledLogged) so misconfiguration
        // is discoverable without being fatal — validation at attempt end
        // still catches problems.
        s.logMonitorDisabledOnce()
        return &CycleVerdict{IsCycle: false}, nil
    }

    summary := summarizeEvents(events) // compact representation of recent activity
    prompt := fmt.Sprintf("Step goal: %s\n\nRecent agent activity:\n%s", step.Description, summary)

    req := agent.BuildGenerateRequest(def, []agent.Message{{Role: "user", Content: prompt}})
    // Uses FastLLM (new config field) rather than the strong-tier cfg.LLM —
    // keeps monitor cost bounded even before multi-tier routing lands.
    resp, err := agent.GenerateWithRetry(ctx, s.cfg.FastLLM, req, fastRetry)
    if err != nil { return nil, err }

    var verdict CycleVerdict
    if err := json.Unmarshal([]byte(resp.Content), &verdict); err != nil {
        return nil, err
    }
    return &verdict, nil
}
```

The `monitor` agent uses the fast tier. Its prompt is the only place we describe what constitutes a cycle vs healthy iteration — so tuning happens in the `.md` file, not in Go.

A new agent definition `monitor.md` is added to `internal/scaffold/agents/` with:

- **Identity**: You observe a coding agent's recent activity and detect cycles
- **Context**: A step goal and a list of recent events (tool calls, edits, text)
- **Output Format**: `CycleVerdict` JSON schema
- **Quality Criteria**: Distinguish cycles from healthy iteration. Re-reading a file before editing is normal. Editing then reverting suggests doubt. Editing A then B then A is a flip-flop. Repeated tool calls with no state change is a loop.

### Extending the monitor pattern

The same fast-tier LLM monitor pattern applies to other fuzzy decisions. Each gets its own agent def and verdict schema:

- **Scope drift monitor** (`scope_monitor.md`): periodically checks whether modified files stay within `ExpectedFiles` and adjacent legitimate changes
- **Stall monitor** (`stall_monitor.md`): detects when the agent has gone quiet — is it thinking hard or stuck?
- **Scope monitor** (`feature_monitor.md`): identifies when the agent is adding unrequested features (invented requirements)

Each has its own sliding window and cooldown, invoked on the same event stream. Supervisor config gains a `Monitors []MonitorConfig` field listing which monitors to run. Start with just `monitor` (cycle detection) and add others as we see specific failure modes in real usage.

Each gets its own `judge*` method on the supervisor. Each uses a dedicated agent def if available, falling back to a generic watchdog. Triggering signals are cheap; judgments are LLM-driven.

## Part 7: Permission handler via MCP bridge

Verified experimentally: Claude Code's `--permission-prompt-tool` invokes the configured MCP tool as a **separate RPC**, not as a tool_use in the public stream. So the permission flow has to happen inside our own MCP tool handler — out-of-band of the claude stdout stream, then re-merged into the supervisor's event channel.

Locutus is already an MCP server (Tier 8). We extend it with a `mcp-perm-bridge` subcommand whose sole job is to be the MCP endpoint claude talks to for permission prompts, and to forward each request over a Unix socket to the supervisor that spawned claude.

### Wiring

1. The supervisor picks a fresh socket path (e.g., `/tmp/locutus-perm-<uuid>.sock`) and starts listening on it before spawning claude.
2. The supervisor writes an `--mcp-config` JSON configuring one MCP server whose `command` is `locutus mcp-perm-bridge` and `args` include `--socket <path>`.
3. The supervisor invokes claude with `--permission-prompt-tool mcp__<bridge-name>__locutus_permission --mcp-config <path> --strict-mcp-config`.
4. When claude wants to run a restricted tool, it spawns `locutus mcp-perm-bridge` via stdio MCP, which dials the Unix socket and authenticates (e.g., shared secret passed via arg).
5. Bridge forwards `{tool_name, input, tool_use_id}` to the supervisor over the socket.
6. Supervisor constructs `AgentEvent{Kind: EventPermissionRequest, ToolName: <requested>, ToolInput: <input>, ...}` and puts it on the unified event channel consumed by `runAttempt`.
7. `runAttempt` routes to `handleInteraction`, which calls the validator (or dedicated "guardian") agent, gets a verdict, and sends the decision back over the socket.
8. Bridge returns the decision to claude via MCP; claude either runs the tool or skips it.

### Decision protocol

Over the socket, requests and responses are single-line JSON:

```json
// request (bridge → supervisor)
{"id":"<tool_use_id>","tool":"Bash","input":{"command":"rm /tmp/x"}}

// response (supervisor → bridge)
{"id":"<tool_use_id>","behavior":"allow"}
// or
{"id":"<tool_use_id>","behavior":"deny","message":"unsafe: writes outside workdir"}
```

Bridge translates `{behavior,message}` into the MCP content-block shape Claude Code expects.

### Handler shape

```go
// Supervisor-side interaction handler: consumes an EventPermissionRequest from
// the MCP bridge, asks the validator/guardian agent, returns the decision to
// the bridge over the socket. No claude resume needed — claude resumes
// automatically when the MCP tool call returns.
func (s *Supervisor) handleInteraction(ctx context.Context, step spec.PlanStep, evt AgentEvent) error {
    def := s.cfg.AgentDefs["validator"] // or a dedicated "guardian" agent
    prompt := buildInteractionPrompt(step, evt) // step desc + tool + input
    req := agent.BuildGenerateRequest(def, []agent.Message{{Role: "user", Content: prompt}})
    resp, err := agent.GenerateWithRetry(ctx, s.cfg.LLM, req, fastRetry)
    if err != nil { return err }
    return s.permBridge.Respond(evt.ToolInput["__tool_use_id__"].(string), parseVerdict(resp.Content))
}
```

### What about clarifying questions?

To be verified in Part 7: does Claude Code expose a question-prompt mechanism we can bridge analogously? Possible candidates are the `AskUserQuestion` tool (Anthropic Agent SDK), stop-reason `tool_use` with a specific name, or nothing at all. If nothing exists, `EventClarifyQuestion` is dead code and can be dropped; if a mechanism exists, it plugs into the same bridge architecture with a second tool name.

## Part 8: How churn short-circuits attempts (separate from retry)

Churn detection is an **intra-attempt abort**, not a retry trigger. When the watchdog + LLM judge determine the agent is churning within a single invocation, we stop that attempt early to save tokens. The retry loop is unchanged — it still handles failed attempts by running another attempt.

```go
func (s *Supervisor) Supervise(ctx, step, driver, workDir) (*StepOutcome, error) {
    // attemptOutcomes records per-attempt outcomes for a sliding-window rule:
    // escalate if >=2 of the last 3 attempts ended in churnDetected, regardless
    // of intervening validation failures. This catches alternating churn/fail
    // patterns that a simple "consecutive churn" counter would miss.
    var attemptOutcomes []outcomeKind

    for attempt := 1; attempt <= MaxRetries; attempt++ {
        result, err := s.runAttempt(...)

        // --- Intra-attempt signals that aborted runAttempt ---

        if churnErr, ok := err.(*churnDetected); ok {
            attemptOutcomes = append(attemptOutcomes, outcomeChurn)
            if churnCountInLastN(attemptOutcomes, 3) >= 2 {
                // Two of the last three attempts cycled — the step itself is
                // likely the problem; escalate so the planner can refine it.
                return &StepOutcome{
                    Success:    false,
                    Attempts:   attempt,
                    Escalation: string(EscalateRefineStep),
                }, nil
            }
            feedback = fmt.Sprintf("Previous attempt got stuck in a cycle (%s). Avoid repeating: %s",
                churnErr.pattern, churnErr.reasoning)
            continue
        }

        if err != nil {
            // Other error: treat as attempt failure, feedback from error.
            attemptOutcomes = append(attemptOutcomes, outcomeError)
            feedback = err.Error()
            continue
        }

        // --- Normal validation path ---

        verdict := s.validate(ctx, step, result.output)
        if verdict.pass {
            return &StepOutcome{Success: true, Attempts: attempt}, nil
        }
        attemptOutcomes = append(attemptOutcomes, outcomeValidationFail)
        feedback = verdict.reasoning
    }

    return &StepOutcome{Success: false, Attempts: MaxRetries}, nil
}
```

Key distinctions captured in the code:

- **Churn is detected while an attempt is running** (inside `runAttempt`, via watchdog + LLM judge on the event stream). The attempt aborts with a `churnDetected` error.
- **Retry is triggered by a failed attempt** — any reason (validation failed, churn aborted, timeout, error). The outer loop runs another attempt.
- **The sliding window over `attemptOutcomes` tracks churn across the last N=3 attempts.** Repeated churn suggests the *step* is wrong (escalate to `RefineStep`); validation failures suggest the *implementation* is wrong (retry with feedback). The window approach catches alternating churn → validation-fail → churn patterns that a simple consecutive-churn counter would miss.
- **Non-churn outcomes stay in the window.** They don't clear prior churn entries; they just occupy a slot that pushes old churn out once the window fills up.
- **Permission/question interactions do NOT split the attempt.** They're handled in-band via the MCP bridge while claude is still running; no resume command, no attempt counter increment.

## Part 9: MCP progress forwarding

Supervisor holds an optional `ProgressNotifier` interface:

```go
type ProgressNotifier interface {
    Notify(ctx context.Context, params ProgressParams) error
}

type ProgressParams struct {
    Token   string
    Message string
    Current int
    Total   int
}
```

In `cmd/mcp.go`, the tool handlers construct a notifier that wraps the MCP session's progress callback and pass it through the dispatcher to supervisors. Tests use a mock notifier.

Before implementation: verify the progress-notification API on `github.com/modelcontextprotocol/go-sdk/mcp`. The `ProgressNotifier` interface shape stays the same; only the inner wrapper call changes to match the actual SDK surface (may be `req.Session.NotifyProgress(...)`, a middleware, or a context-scoped helper depending on SDK version).

Events that get forwarded:

- Tool calls with file paths ("Agent is editing `cmd/auth.go`")
- Permission requests ("Agent wants permission to: `<command>`")
- Clarifying questions ("Agent asked: `<question>`")
- Churn warnings ("Supervisor detected churn, intervening")
- Final validation result ("PASS" / "FAIL: ...")

Events that don't get forwarded: individual `content_block_delta`s (too noisy), API retries (internal detail), raw text deltas.

## Files to modify

New files:

- `internal/dispatch/events.go` — `AgentEvent`, `EventKind` types, `summarizeEvents` helper **(Part 1 — done)**
- `internal/dispatch/runner.go` — streaming `CommandRunner`, `ProductionRunner`, `batchRunner` **(Part 2 — done)**
- `internal/dispatch/monitor.go` — sliding-window monitor (ring buffer + cooldown; no pattern detection)
- `internal/dispatch/judge.go` — LLM monitor invocations (cycle, drift, stall)
- `internal/dispatch/drivers/claude_stream.go` — Claude Code NDJSON parser + reassembler
- `internal/dispatch/bridge.go` — supervisor-side permission bridge (socket listener + request/response protocol)
- `cmd/mcp_perm_bridge.go` — `locutus mcp-perm-bridge` subcommand (MCP server exposing `locutus_permission`, forwards over socket)
- `internal/scaffold/agents/monitor.md` — cycle-detection monitor agent (fast tier)

Modify:

- `internal/dispatch/supervisor.go` — streaming event loop (replace batch version), `CommandRunner` signature, add `ProgressNotifier` + `FastLLM` to config, integrate monitor + LLM judgment, merge bridge events
- `internal/dispatch/drivers/driver.go` — `AgentDriver` gains `ParseStream` and `RespondToAgent`
- `internal/dispatch/supervisor_test.go` — wrap byte buffers with `batchRunner()` helper **(Part 2 — done)**
- `internal/dispatch/dispatcher.go` — plumb `ProgressNotifier` through to supervisors
- `cmd/mcp.go` — wrap MCP session's progress notifier for dispatch tool handlers

Deferred (separate follow-up plan):

- `internal/dispatch/drivers/codex_stream.go` + `gemini_stream.go` — need real fixtures once auth is configured

## Test strategy (test-first, per-part acceptance gates)

Discipline: for each part, acceptance tests below are **written and committed first**. The part is not complete until every listed test passes with real assertions — no `t.Skip`, no `// TODO`, no stub bodies. We progress to the next part only after the current part's tests are green.

### Part 1 — `AgentEvent` & helpers (`internal/dispatch/events_test.go`) — done

- `TestEventKind_String` — every declared `EventKind` stringifies to its declared constant value.
- `TestClassifyToolName_Permission` — tool name `"locutus_permission"` against Claude driver config yields `EventPermissionRequest` (utility is tested even though the claude parser doesn't currently consult it).
- `TestClassifyToolName_Question` — tool name `"AskUserQuestion"` yields `EventClarifyQuestion`.
- `TestClassifyToolName_Unregistered` — any other tool name yields `EventToolCall`.
- `TestClassifyToolName_EmptyConfigFallsBackToToolCall` — empty registry → EventToolCall.
- `TestSummarizeEvents_Compact` — a fixed 12-event slice produces a stable string under ~1KB, including tool names and file paths but excluding full `Raw` bodies.

### Part 2 — streaming `CommandRunner` (`internal/dispatch/runner_test.go`) — done

- `TestProductionRunner_StreamsStdout` — runs `/bin/sh -c 'printf "a\nb\n"'`; ReadCloser yields exactly `"a\nb\n"`.
- `TestProductionRunner_CloseWaits` — Close blocks until process exits; non-zero exit code surfaces as `*exec.ExitError`.
- `TestProductionRunner_CtxCancelKills` — cancelling ctx terminates child process; Close returns without hang.
- `TestBatchRunner_WrapsBytes` — `batchRunner([]byte("x"))` returns ReadCloser yielding exactly `"x"` then EOF.
- `TestBatchRunner_EmptyBytes` — nil input yields an empty ReadCloser.

### Part 3 & Part 4 — claude stream parser + reassembler

Fixtures captured from real claude CLI on 2026-04-17 (hand-saved, sanitized to strip user-specific tool/MCP lists from init events):

- `testdata/claude_simple.ndjson` — init + short text + result
- `testdata/claude_with_tool_use.ndjson` — init + tool_use reassembly (9 input_json_deltas) + user/tool_result + text reassembly (8 text_deltas) + result

`internal/dispatch/drivers/claude_stream_test.go`:

- `TestClaudeStream_InitEvent` — first event from `claude_simple.ndjson` is `EventInit` with non-empty `SessionID`.
- `TestClaudeStream_TextReassembly` — multi-delta text chunks collapse into one `EventText` with full accumulated content (~470 chars across 8 deltas in the tool-use fixture).
- `TestClaudeStream_ToolCallReassembly` — `claude_with_tool_use.ndjson` yields one `EventToolCall` with `ToolName=="Read"` and `ToolInput` matching the recorded JSON map (deep-equal, not substring).
- `TestClaudeStream_ToolResult` — the `user`-typed event after the tool call yields `EventToolResult` with the recorded content.
- `TestClaudeStream_ResultEvent` — the final `result` event yields `EventResult` with the final text.
- `TestClaudeStream_IgnoresRateLimitAndAssistantDuplicates` — `rate_limit_event` and full-message `assistant` events do not produce duplicate events when `stream_event` deltas cover the same content.
- `TestClaudeStream_IgnoresThinkingSignatureDeltas` — thinking-block signature deltas are consumed without producing events.
- `TestClaudeStream_EOFTerminates` — after last fixture event, `Next` returns `io.EOF`.
- `TestClaudeStream_CtxCancelMidStream` — cancelling ctx before EOF returns `ctx.Err()`.
- `TestClaudeDriver_RespondToAgent` — returns an `*exec.Cmd` whose args include `--resume <sessionID>` and whose stdin or final arg carries the response text.

Codex and Gemini parser tests are deferred to a follow-up part (separate plan) — see Deferred list in Scope. No stubs or skipped tests are introduced for those in this plan.

### Part 5 — supervisor event loop (`internal/dispatch/supervisor_stream_test.go`) — done

- `TestRunAttempt_Happy` — scripted parser emits init → text → tool_call → tool_result → result → EOF; `runAttempt` returns `nil` error and `attemptResult` accumulates the tool call plus final text.
- `TestRunAttempt_ParserErrorPropagates` — parser returns a non-EOF error mid-stream; `runAttempt` returns that error and closes the parser.
- `TestRunAttempt_CtxCancel` — cancelling ctx mid-stream aborts the loop; parser.Close + stream.Close both called.
- `TestRunAttempt_EmitsProgressForToolCalls` — mock `ProgressNotifier` receives a Notify for a `ToolCall` event with `FilePaths` set.

The bridge-merge test originally drafted here (`TestRunAttempt_MergesBridgeEvents`) depends on `handleInteraction` + the MCP bridge, both Part 7 concerns. The equivalent assertion lives in Part 7 as `TestRunAttempt_PermissionEventMergedMidStream`. Monitor integration (`mon.Observe`, `mon.ShouldCheck`, `monitorCycle`) is added to `runAttempt` in Part 6.

### Part 6 — monitor + LLM judge (`internal/dispatch/monitor_test.go`, `judge_test.go`) — done

- `TestMonitor_RingBufferEviction` — observing `windowSize+5` events keeps only the last `windowSize` in `RecentEvents()`.
- `TestMonitor_CooldownByEventCount` — `ShouldCheck` false before `checkEveryEvents` observations, true immediately after.
- `TestMonitor_CooldownByTime` — with a fake clock, `ShouldCheck` true once `checkEveryTime` has elapsed even with few events.
- `TestMonitor_CircuitBreaker_TripsAfterThreshold` — 3 consecutive `MarkChecked(nonNilErr)` calls cause `ShouldCheck` to stay false for the remainder of the attempt; documented threshold = 3.
- `TestMonitorCycle_MissingAgent_LogsOnceAndReturnsFalse` — empty `AgentDefs["monitor"]`: first call emits INFO log, returns `IsCycle=false`; subsequent calls return false without re-logging (use `slog` test handler to capture).
- `TestMonitorCycle_ParsesVerdict` — mock `FastLLM` returns `{"is_cycle":true,"confidence":0.85,"pattern":"file_thrashing","reasoning":"..."}`; `monitorCycle` returns a matching `CycleVerdict`.
- `TestMonitorCycle_MalformedJSON_ReturnsError` — mock `FastLLM` returns `"not json"`; `monitorCycle` returns a non-nil error and does not panic.
- `TestMonitorCycle_UsesFastLLMNotStrong` — with distinct mock clients for `cfg.LLM` and `cfg.FastLLM`, only the fast client is invoked.

### Part 7 — permission bridge (`internal/dispatch/bridge_test.go`, `cmd/mcp_perm_bridge_test.go`) — done

- `TestPermBridge_SocketRoundtrip` — spawn bridge-as-subprocess, send it an MCP `tools/call` for `locutus_permission`, assert it forwards the tool_name+input over the socket to a fake supervisor, and returns the supervisor's decision to the MCP caller.
- `TestPermBridge_AllowResponseShape` — supervisor responds `{behavior:"allow"}`; bridge returns a valid MCP tool_result content block that claude Code accepts.
- `TestPermBridge_DenyResponseShape` — supervisor responds `{behavior:"deny",message:"reason"}`; bridge returns a denial content block with the message.
- `TestHandleInteraction_PermissionAllow` — `EventPermissionRequest` arrives on the supervisor event channel → validator LLM returns `"allow"` → bridge receives `{behavior:"allow"}` via the fake socket.
- `TestHandleInteraction_PermissionDeny` — validator returns `"deny: unsafe"` → bridge receives the deny payload with message verbatim.
- `TestRunAttempt_PermissionEventMergedMidStream` — end-to-end: stream parser is emitting claude events; a bridge-originated `EventPermissionRequest` appears mid-stream; `runAttempt` calls `handleInteraction`, response goes out via the bridge, stream continues; no new `*exec.Cmd` is returned and no attempt counter change.

### Part 8 — churn ↔ retry integration (`internal/dispatch/supervise_test.go`) — done

- `TestSupervise_ChurnOnceThenPass` — attempt 1 aborts with `churnDetected`; attempt 2 passes validation. Outcome: `Success=true`, `Attempts=2`. Attempt 2's `feedback` arg contains the churn pattern string.
- `TestSupervise_TwoChurnsInWindowEscalates` — attempts 1 and 2 both churn → outcome `Success=false`, `Escalation=EscalateRefineStep`, `Attempts=2` (sliding window: ≥2/last-3).
- `TestSupervise_AlternatingChurnFailChurn_Escalates` — churn → validation-fail → churn. Under sliding-window rule, the second churn is 2 of the last 3 → `Escalation=EscalateRefineStep`. Regression guard for the sliding-window rule.
- `TestSupervise_PermissionDuringAttempt_DoesNotSplitAttempt` — a permission interaction is handled mid-attempt; the attempt counter does not advance; validation of the completed attempt still runs at the end.
- `TestSupervise_ValidationFailNoChurn_Retries` — attempt returns normally, validation fails → attempt counter advances, churn window unchanged.

### Part 9 — MCP progress forwarding (`internal/dispatch/progress_test.go`, `cmd/mcp_test.go`)

- `TestProgressNotifier_ForwardsToolCallsWithFiles` — mock notifier captures `Notify`; a `ToolCall` event with `FilePaths=["cmd/auth.go"]` produces a message containing `"cmd/auth.go"`.
- `TestProgressNotifier_ForwardsPermissionEvents` — `EventPermissionRequest` produces a message mentioning the requested tool/command.
- `TestProgressNotifier_SuppressesNoise` — `EventText` deltas and `EventRetry` produce zero `Notify` calls.
- `TestMCPHandler_WrapsSessionNotifier` — `cmd/mcp.go` handler constructs a `ProgressNotifier` from the MCP session; assertion shape pending SDK-API verification but the test exists and is meaningful either way.

### Cross-part

- Fixtures directory: `internal/dispatch/drivers/testdata/*.ndjson` — hand-captured, not generated; each fixture committed with the test that consumes it.
- Opt-in live integration: `LOCUTUS_INTEGRATION_TEST=1 go test -run TestClaudeCodeLive` — runs `claude -p --output-format stream-json --include-partial-messages` on a trivial task, verifies the full event pipeline including a real MCP permission bridge.
- No test may use `t.Skip` to defer work; if a test can't be written, the part isn't ready.

Note: we don't hand-craft "churning" fixtures because cycle detection is the LLM's job, not heuristic. The LLM monitor is tested via mocked responses (supervisor tests) and via live integration against real coding agents.

## Verification

1. `go build ./...` — no compilation errors
2. `go test ./... -race -count=1` — all tests pass including new streaming tests
3. Supervisor tests: mock LLM returns cycle verdict → attempt aborts; mock LLM returns healthy → attempt continues
4. Manual test: run MCP server, invoke a streaming tool from Claude Code, verify progress notifications appear in the host UI and that a permission prompt routes through `locutus mcp-perm-bridge` and surfaces as an `EventPermissionRequest` in the supervisor's logs
