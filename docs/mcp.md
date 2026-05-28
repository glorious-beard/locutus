# Locutus MCP server

Locutus exposes its spec graph to coding agents (Claude Code, Codex, Gemini CLI, anything else that speaks the Model Context Protocol) through a per-project MCP server. This doc covers the server's surface — tools, resources, prompts, notifications — plus the daemon lifecycle and the stdio-over-socket bridge that makes the singleton look like a regular stdio MCP server.

The implementation lives under `internal/mcp/`; bridge + daemon subcommands are in `cmd/mcp{,.go,_daemon.go,_stop.go}`. The architectural rationale is DJ-135.

## Surface

### Tools

The read tools wrap `agent.SpecStore` directly (the DJ-134 unified store). The write tools open a per-call transaction, commit, and emit `notifications/resources/updated` for `spec://manifest`.

| Tool | Input | What it does |
|---|---|---|
| `spec_list_manifest` | `{}` | Returns the per-kind catalogue of every spec node (id, title, summary, origin, working flag). Cheap; start every iteration here. |
| `spec_get` | `{ids: [string]}` | Batched body fetch. Every id in one call; per-id loops are an anti-pattern that wastes tool-loop rounds. |
| `spec_search` | `{query, kind?, limit?}` | BM25 over title/summary/body. Use for topic-scoped questions ("what do we have on auth?"). |
| `spec_propose_decision` | `{id, title, summary?, status, confidence, rationale, alternatives?, axes?, surfaced_by?, influenced_by?, advances?, respects?}` | Upsert. Auto-commits per call. `axes` and `surfaced_by` are optional — when omitted, `axes` backfills from the id (per DJ-133 `dec-<axis-id>`). DJ-139 adds optional `advances []goal-*` and `respects []agoal-*` informational citation arrays. |
| `spec_propose_feature` | `{id, title, summary?, status, description?, acceptance_criteria?, decisions, approaches?, advances?, respects?}` | Upsert. `decisions` is required and must reference existing decision ids. DJ-139 adds optional `advances` / `respects` citation arrays. |
| `spec_propose_strategy` | `{id, title, summary?, kind, status, decisions, approaches?, prerequisites?, commands?, skills?, influenced_by?, advances?, respects?}` | Upsert. Same shape as feature plus strategy-specific fields. DJ-139 adds optional `advances` / `respects` citation arrays. |
| `spec_revise_decision` | same as `spec_propose_decision` | Same input shape as propose, but the id MUST already exist; the server preserves the original `created_at` and bumps `updated_at`. |
| `spec_revise_feature` | same as `spec_propose_feature` | Same as propose, id must exist. |
| `spec_revise_strategy` | same as `spec_propose_strategy` | Same as propose, id must exist. |
| `spec_mark_approach_drifted` | `{approach_id, event_id}` | Sets `Approach.invalidated_by_event_id` on the named approach. Called by the `spec_bias` cascade playbook (DJ-138) once per approach in the reference-graph closure. Idempotent on (approach_id, event_id). Rejects non-Approach ids and empty event_id. |
| `spec_propose_goal` | `{id, title, body, source_clause}` | Upsert a `goal-*` node — the persisted LLM interpretation of one in-scope claim from `GOALS.md` (DJ-139). `source_clause` is the verbatim excerpt from `GOALS.md` and is load-bearing for the diff-and-apply sync that preserves ids across `GOALS.md` rephrasings. Lands at `.borg/spec/goals/<id>.json`. |
| `spec_revise_goal` | same as `spec_propose_goal` | Same input shape as propose; id MUST already exist. Server preserves `created_at` and bumps `updated_at`. The path the goal-layer sync uses to track `GOALS.md` rephrasings without minting new ids. |
| `spec_delete_goal` | `{id, reason}` | Removes the `goal-*` node from the store and `.borg/spec/goals/<id>.json`. `reason` is recorded verbatim on the `goal_deleted` history event as the audit trail. Errors on unknown id. New pattern in the codebase — pre-DJ-139 the spec model was append-only; goal-layer deletion is real because `GOALS.md` edits can drop scope claims. |
| `spec_propose_antigoal` | `{id, title, body, source_clause, ceded_to?, kept_in?}` | Upsert an `agoal-*` node — one atomic out-of-scope carve-out (DJ-139). `ceded_to` names incumbents owning the ceded space (consumed by import conflict prose); `kept_in` names carve-out qualifiers that stay in scope despite the broader exclusion (consumed by carve-out fit judgment). Lands at `.borg/spec/antigoals/<id>.json`. |
| `spec_revise_antigoal` | same as `spec_propose_antigoal` | Same as propose; id must exist; `created_at` preserved. `ceded_to` and `kept_in` are replaced wholesale — pass the full updated list, not a delta. |
| `spec_delete_antigoal` | `{id, reason}` | Removes the `agoal-*` node from the store and disk. `reason` recorded on the `antigoal_deleted` history event. Errors on unknown id. |
| `spec_update_goals_md_hash` | `{hash, synced_at}` | Updates `.borg/manifest.json`'s `goals_md_hash` and `goals_md_synced_at` fields (DJ-139). Called by the `refine goals` playbook at the end of a successful Step 0 sync; the next run's manifest fetch reads the hash and short-circuits the matcher when `GOALS.md` hasn't changed. `hash` is the canonical `sha256:<hex>` form; `synced_at` is RFC3339. Preserves every other manifest field atomically through the store's mutex. |
| `spec_loop_begin` | `{activity, target}` → `{iteration, max_iterations}` | Starts (or recovers) the interactive self-loop's iteration record for the named run (DJ-142). Allocates a fresh record at iteration 0 when none is live for `(ServerSession, activity, target)`, or returns the live one's current iteration when it exists — the compression-recovery path: an agent that lost its place after a context compression re-calls this and resumes mid-loop. `max_iterations` is read from the activity registry (DJ-138), so `.borg/agents.yaml` overrides apply. The tier-3 `spec_refinement.interactive.md` playbook calls this once before its first pass. |
| `spec_loop_status` | `{activity, target}` → `{iteration, max_iterations, converged, last_verdict}` | Read-only inspection of the live loop record for `(ServerSession, activity, target)`. Does not advance the counter. |
| `spec_advance_iteration` | `{activity, target, converged, reason?}` → `{continue, iteration, reason}` | Records the scout's convergence verdict for the pass, increments the iteration counter, and returns the deterministic continuation decision: `continue` is `false` when `converged == true` OR `iteration >= max_iterations`, otherwise `true`. The self-loop playbook calls this at the end of each pass and honors `continue`. The iteration count and cap are server-tracked (no agent self-counting); the `converged` verdict is the scout's LLM judgment — same division of labor the headless harness uses when it reads the verdict line. |

Coding agents see the tools prefixed with the MCP server name in their tool catalogue: `mcp__locutus__spec_list_manifest`, etc. Playbooks reference the prefixed form.

#### Loop-state tools and run scoping (DJ-142)

`spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration` back the interactive self-loop driver for runtimes without a native goal-loop (Codex, Gemini); they are exercised only by the tier-3 `spec_refinement.interactive.md` playbook. Headless dispatch keeps the harness `OuterLoopRunner` + verdict-line read, and interactive Claude Code keeps `/goal` — neither calls these tools. See [docs/runtime-affordances.md](runtime-affordances.md).

Loop state is daemon-side ephemeral bookkeeping (iteration counter + recorded verdict), held alongside the per-project daemon singleton — NOT in the `SpecStore`, so it never persists to `.borg/spec/` or appears in the manifest. Each record is keyed server-side by **`(ServerSession, activity, target)`**:

- The agent supplies only `(activity, target)` — both re-derivable from its run context (the playbook it's running + the `Target:` line), so there is no opaque token to lose across a context compression. Recovery is "re-call `spec_loop_begin`."
- The server adds the connection's `*mcp.ServerSession` (read from the `CallToolRequest`'s `req.Session`, one per socket connection — `server.Connect` per accept) to the key. Since the per-project daemon is shared across multiple `locutus mcp` bridges (multiple coding-agent sessions), this disambiguates two concurrent sessions running the same `(activity, target)` so their counters don't collide.
- Record lifecycle: deleted immediately on terminate (converge or cap), so a fresh run after a converged one finds no live record and starts at iteration 0. (A TTL sweep method exists for abandoned loops but isn't yet scheduled — those clear on daemon restart.) A bridge restart mid-run yields a new `ServerSession` and starts fresh — rare, and clean since spec graph state persists in the `SpecStore` regardless. A server-internal `run_id` is minted per record for logging/correlation only; the agent never handles it.

### Resources

One resource: `spec://manifest`. Content is the same JSON `spec_list_manifest` returns. Useful when a client wants to attach the manifest as context rather than pay a tool-call round-trip per iteration.

Clients that call `resources/subscribe` for `spec://manifest` receive `notifications/resources/updated` on every successful write tool. Cross-session coordination: client A subscribes, client B writes, client A is notified. The subscription set is tracked server-wide (across all active bridge sessions), so the fanout includes every attached client regardless of which one originated the write.

### Prompts

One prompt per activity that has a playbook in `.borg/plans/<activity>.md`. Activities without a plan file get no prompt registration — the activity registry still resolves them for CLI dispatch, but `prompts/get` returns nothing.

| Prompt name | Source | Persona |
|---|---|---|
| `spec_refinement` | `.borg/plans/spec_refinement.md` | "You are executing a Locutus activity playbook…" (see persona constant in `internal/mcp/prompts_activity.go`) |
| `feature_ingestion` | `.borg/plans/feature_ingestion.md` | same |
| `code_adoption` | `.borg/plans/code_adoption.md` | same |
| `code_assimilation` | `.borg/plans/code_assimilation.md` | same |

The Q1 split from DJ-135 phase 5 is: persona text lives on `Prompt.Description` (shown in client prompt-picker UIs); the playbook body lives on a single user-role `PromptMessage`. MCP has no system role at the protocol level; description is the conventional spot for system framing.

## Daemon lifecycle

```
        ┌─────────────────────────────────────────────┐
        │            locutus mcp (bridge)             │
        │ stdin/stdout ↔ unix socket .locutus/mcp.sock│
        └────────────────────┬────────────────────────┘
                             │
                             │ probes; forks if missing
                             ▼
        ┌─────────────────────────────────────────────┐
        │  locutus mcp-daemon --project <root>        │
        │  net.Listen("unix", .locutus/mcp.sock)      │
        │  accept loop → Server.Connect per conn      │
        │  shared SpecStore, shared subscriptions     │
        └─────────────────────────────────────────────┘
```

- **Bootstrap.** `cmd/mcp.go` runs `mcp.EnsureDaemon(ctx, root, binary)`. It probes the canonical socket path (`.locutus/mcp.sock` under the project root); if a connect succeeds, the daemon is already up. If the probe fails, EnsureDaemon forks `<binary> mcp-daemon --project <root>` with `Setsid` (or `CREATE_NEW_PROCESS_GROUP` on Windows) so the daemon outlives the CLI invocation, then polls up to 5 seconds for the daemon to bind.
- **Listener.** `mcp.ListenSocket(path)` removes any stale socket file (POSIX leaves them after SIGKILL), creates `.locutus/` at `0o700`, and chmods the socket to `0o600` so only the project owner can connect.
- **Accept loop.** `mcp.ServeOnSocket(ctx, listener, server)` accepts conns, wraps each in an `mcp.IOTransport`, and calls `server.Connect`. Per-session goroutines block on `ServerSession.Wait` until the client closes.
- **Shutdown.** ctx cancellation closes the listener AND every in-flight conn. Without forcing conn-close, `ServerSession.Wait` (which returns only on client-side close) would hang idle sessions forever. Race-guarded with a `closing` flag so an Accept that races with cancellation doesn't leak a session.
- **Teardown.** `locutus mcp-stop` removes the socket file. The accept loop fails on the next would-be connection and the daemon exits. A PID-file + SIGTERM path is planned (see TODO in `internal/mcp/bootstrap.go`) but the socket-remove approach is sufficient for v1.

### Bridge

`locutus mcp` is a thin netcat-style bridge: it dials the socket and pumps bytes between stdin↔socket. To an external MCP client (Claude Code, Codex, Gemini CLI) the invocation is indistinguishable from a stdio MCP server.

Critical detail: when the calling MCP client closes stdin (signaling "no more requests"), the bridge half-closes the socket's write side via `UnixConn.CloseWrite()` so the daemon sees EOF on its read but can still write outstanding responses back through the still-open read side. Closing the whole conn on stdin EOF would race with the daemon's in-flight responses.

## Project-scope MCP config

`locutus init` writes `.mcp.json` at project root with:

```json
{
  "mcpServers": {
    "locutus": {
      "command": "locutus",
      "args": ["mcp"]
    }
  }
}
```

Claude Code reads this on session start and attaches the server automatically. Codex reads `.codex/config.toml` with an equivalent `[mcp_servers.locutus]` block. Gemini reads `.gemini/extensions/locutus/extension.json` with `mcpServers` in the extension manifest. All three configs point at `locutus mcp` so the runtime can spawn the bridge as a child process.

## Session recording

The MCP server itself doesn't log tool calls server-side under the current design — the recording happens client-side at the dispatch layer (`internal/runner/run.go`). Every ACP event from the coding agent (including tool-call observations) lands under `.locutus/sessions/<date>/<time>/<sid>/`:

| File | Content |
|---|---|
| `playbook.md` | The exact playbook body delivered as the initial user message. |
| `events.jsonl` | Every ACP event observed during the session (one line per event, JSON). |
| `tools.jsonl` | Tool-call ↔ tool-result events filtered out of events.jsonl for fast scanning. |
| `output.md` | The agent's final text output (concatenated EventText). |

If the agent operates outside a CLI-dispatched session (e.g. an operator opens Claude Code by hand and uses the published subagents directly), the MCP server doesn't capture a session — those calls live only in the runtime's own session log (Claude Code's `.claude/projects/`, etc.). A server-side tool-call log is a follow-up if direct-runtime use becomes the common path.

## Testing the surface

In-memory transport tests live in `internal/mcp/*_test.go`:

- `server_test.go` — registration + every read/write tool round-tripped through `mcp.NewInMemoryTransports`.
- `resources_test.go` — cross-checks `resources/read spec://manifest` against the `spec_list_manifest` tool result.
- `notifications_test.go` — subscribe → write → observe; plus the negative case where an unsubscribed client correctly receives nothing.
- `socket_test.go` — real Unix sockets in `/tmp/lt-mcp-*` (macOS sockaddr_un path-length budget rules out `t.TempDir()`).
- `bridge_test.go` — initialize round-trip through `BridgeIOToSocket` against a real socket daemon.
- `bootstrap_test.go` — `EnsureDaemon` happy path + bogus-binary fork-failure.

End-to-end with a real ACP runtime is exercised by `locutus refine` against a project; see DJ-135 ckpt 4 empirical validation notes.
