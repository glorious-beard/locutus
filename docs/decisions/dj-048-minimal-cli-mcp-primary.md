## DJ-048: Minimal CLI, MCP as Primary Interface, Headless via --json

**Status:** shipped

**Decision:** CLI is minimal interaction (stdin prompts for revisit, pterm spinners for progress, text output). MCP is the primary interactive interface — supported by VS Code, Claude Code, JetBrains, Cursor, Windsurf, Zed, Gemini CLI, and likely Antigravity. Headless mode via `--json` flag on every command. Rich TUI is a future feature if demanded.

**Why not rich CLI now:** MCP covers all major IDEs via stdio transport. Most users will access Locutus through whatever AI assistant their IDE provides. A rich bubbletea-based TUI would cost 500-1000 LOC, delay shipping, and serve a narrow audience (power terminal users). Start minimal, add later.

**Three modes, same core:** Every command produces structured data (MCPResponse). MCP returns JSON to the client. CLI renders via pterm. Headless outputs raw JSON. The difference is presentation only — all three share the same engine.

---

Session date: 2026-04-16 to 2026-04-17 — post-Tier-8 refinements
