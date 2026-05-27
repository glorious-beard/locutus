## DJ-006: Skills Over Templates

**Status:** shipped

**Decision:** No template engine. Use SKILL.md files to guide LLM generation.

**Context:** The user is the author of `stamp` (github.com/glorious-beard/stamp), an MCP-based template rendering tool. They stopped development because well-written SKILL.md files provided equivalent DX without the template engine complexity.

**Why skills:** Templates are deterministic but rigid. Skills guide the LLM to produce correct code while allowing it to adapt to context. The skill is the expert knowledge; the LLM is the flexible executor.
