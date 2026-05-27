## DJ-021: Genkit Go — LLM Plumbing Only, Not Agent Orchestration

**Status:** shipped

**Decision:** Use Genkit Go strictly for LLM access (multi-provider Generate, tool registration, structured output). All agent orchestration, definition loading, and persistence is built by Locutus.

**Research finding:** Genkit Go cannot read agent definition files (AGENTS.md, SKILL.md) or memory files. It has no native agent support — agents are built manually using flows and tool definitions. The JS/TS version is significantly more mature for agent development, but we're in Go. Genkit Go's session system is in-memory only with no file-based persistence.

**What Genkit Go gives us:** Multi-provider model selection by config string, `ai.Generate()` with structured output, tool registration, system prompts, conversation history management.

**What Locutus builds on top:** SKILL.md loading and injection, agent registry and routing, supervision loop, historian, brownfield analysis, memory/persistence, all file-based spec I/O.
