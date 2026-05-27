## DJ-062: Permission Bridge via In-Process MCP Server, Not Stream Parsing (Reverses DJ-057)

**Status:** shipped

**Decision:** Permission events surface via a Unix-socket bridge from an in-process MCP server (`locutus mcp-perm-bridge` subcommand), not by tool-name matching on the agent's public event stream.

**Reverses DJ-057** (which proposed identifying permission events by matching the configured permission-prompt tool name in the stream). That premise was factually wrong for Claude Code. Verified experimentally against a running `claude --print --permission-prompt-tool mcp__perm__locutus_permission` with a stub MCP server: when the agent wants a restricted tool, Claude invokes the permission-prompt tool as a **separate MCP RPC** on a side channel, not as a `tool_use` event in the public stream. The stream only shows the original restricted tool (e.g., `Bash`) followed by a `tool_result` reflecting our allow/deny. So the stream parser can never see the permission request — it's invisible to stdout-based observation.

**What's correct now:** the supervisor opens a Unix socket per supervision session, spawns `locutus mcp-perm-bridge --socket <path>` as Claude's MCP server, and reads `PermRequest{id, tool, input}` off the socket as `AgentEvent{Kind: EventPermissionRequest, InteractionID: id, ...}`. `handleInteraction` asks the validator/guardian LLM for an allow/deny verdict, then routes back through `PermBridge.Respond`. No claude resume is needed — the blocked MCP RPC returns, Claude continues.

**`ClassifyToolName` survives as a utility:** DJ-057's intended mechanism is still sound for providers that *do* surface these events as tool calls in-stream (hypothetical). The function is kept and tested, just not wired into the Claude parser.

**AskUserQuestion visibility is unverified.** Claude's SDK docs describe an `AskUserQuestion` tool but we haven't confirmed whether it appears as a `tool_use` in `--print --output-format stream-json` mode. Treated as future extension; `EventClarifyQuestion` exists in the taxonomy and plumbs through the same bridge architecture if the fixture capture confirms it.
