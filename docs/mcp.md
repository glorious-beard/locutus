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
| `spec_propose_decision` | `{id, title, summary?, status, confidence, rationale, alternatives?, axes?, surfaced_by?, influenced_by?}` | Upsert. Auto-commits per call. `axes` and `surfaced_by` are optional — when omitted, `axes` backfills from the id (per DJ-133 `dec-<axis-id>`). |
| `spec_propose_feature` | `{id, title, summary?, status, description?, acceptance_criteria?, decisions, approaches?}` | Upsert. `decisions` is required and must reference existing decision ids. |
| `spec_propose_strategy` | `{id, title, summary?, kind, status, decisions, approaches?, prerequisites?, commands?, skills?, influenced_by?}` | Upsert. Same shape as feature plus strategy-specific fields. |
| `spec_revise_decision` | same as `spec_propose_decision` | Same input shape as propose, but the id MUST already exist; the server preserves the original `created_at` and bumps `updated_at`. |
| `spec_revise_feature` | same as `spec_propose_feature` | Same as propose, id must exist. |
| `spec_revise_strategy` | same as `spec_propose_strategy` | Same as propose, id must exist. |
| `spec_mark_approach_drifted` | `{approach_id, event_id}` | Sets `Approach.invalidated_by_event_id` on the named approach. Called by the `spec_bias` cascade playbook (DJ-138) once per approach in the reference-graph closure. Idempotent on (approach_id, event_id). Rejects non-Approach ids and empty event_id. |

Coding agents see the tools prefixed with the MCP server name in their tool catalogue: `mcp__locutus__spec_list_manifest`, etc. Playbooks reference the prefixed form.

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
