## DJ-061: Streaming Supervision Plan Executed End-to-End (Closes DJ-059)

**Status:** shipped

**Decision:** The streaming-supervision plan deferred in DJ-059 shipped across 13 commits. Batch `CommandRunner` signature replaced with `io.ReadCloser`, supervisor's outer loop rewritten around `runAttempt`, NDJSON parser + delta reassembler for Claude Code, fast-tier LLM monitor with ring buffer + cooldown + circuit breaker, MCP permission bridge via a `locutus mcp-perm-bridge` subcommand + Unix socket, MCP progress forwarding through a session-wrapped notifier.

**What actually shipped vs what the plan specified:** All 9 parts closed with real assertions and no `t.Skip`. 61 new tests, entire repo clean under `go test -race -count=1`. Live smoke test against real `claude --output-format stream-json` green end-to-end (~9s via Claude Max OAuth, zero API tokens). The pre-existing batch `Supervise` is removed; streaming is the only supervisory path.

**Deferred from the plan:** Codex and Gemini CLI driver fixtures/parsers (need real captures once each provider's auth is configured). The `locutus mcp-perm-bridge` subcommand is built and tested but not yet wired into `ClaudeCodeDriver.BuildCommand` — the supervisor exposes a `permBridge` hook that tests set directly; production wire-up happens when the Dispatcher gets a CLI entry point.
