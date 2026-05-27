## DJ-004: MCP Transport

**Status:** shipped

**Decision:** Stdio-first, optional HTTP.

**Research finding:** VS Code only supports stdio for MCP servers. Claude Code supports both stdio and HTTP. Stdio is the common denominator.

**Pattern:** `locutus mcp` starts stdio MCP server (spawned by client). `locutus mcp --http :8080` for remote/multi-client scenarios later.
