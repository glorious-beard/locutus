## DJ-008: Planner + Delegator, Not Coder

**Status:** shipped

**Decision:** Locutus produces execution plans for external coding agents. It does not generate application code itself.

**Journey:**
1. Initially planned Locutus as a code generator (generates code directly via LLM)
2. User noted that Claude Code, Codex, Charlie, Gemini etc. have billions in R&D behind them. Competing on code quality is a losing game.
3. Pivoted to planner model: Locutus focuses on decisions, strategies, and execution plans. External agents handle implementation.
4. Exception: spec-derived artifacts (Taskfile.yml, AGENTS.md, proto definitions) are generated directly — they're deterministic transforms, not creative coding.

**Why this works:** Locutus's unique value is architectural intelligence (decisions, strategies, history). Code generation is commodity. By delegating coding, Locutus is agent-agnostic — works with any coding agent, benefits from improvements in any of them.
