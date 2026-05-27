## DJ-059: Streaming Supervision Deferred to Follow-Up Plan

**Status:** superseded by DJ-061

**Decision:** Supervisor currently runs coding agents in batch mode (`CommandRunner` returns `[]byte`). Streaming supervision — NDJSON event loop with mid-attempt churn detection, permission/question routing, MCP progress forwarding — is captured in `.claude/plans/streaming-supervision.md` for execution in a future session.

**Why:** The current batch supervisor works for all existing tests and the 8 tiers as originally specified. Streaming requires ~10 new files, touches every driver, and significantly expands the supervision surface. Better to keep it as a coherent follow-up plan than jam it into the already-large tier sequence.

**Plan scope includes:** normalized `AgentEvent`, pull-based stream parser per driver, sliding-window LLM monitor, permission/question tool-name registry, MCP progress notifications, heartbeat and size-bomb timeouts for mid-stream detection (belt-and-suspenders with reassembly-based monitor), context-cancellation propagation to kill forked processes.
