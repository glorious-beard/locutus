## DJ-029: Genkit Go + Custom Orchestration, Not LangGraphGo

**Status:** shipped

**Decision:** Keep Genkit Go for LLM access. Build ~350 LOC of custom orchestration for Locutus's council and supervision patterns. Do not add LangGraphGo or LangChainGo.

**Alternatives considered:**
- LangChainGo + LangGraphGo (drop Genkit) — Three LangGraphGo implementations exist in Go (`tmc`, `dshills`, `smallnest`), all immature (alpha/early-stage). Python LangGraph hit 1.0 but Go ports haven't caught up. Would add alpha dependencies and require untested integration.
- Genkit Go + LangGraphGo (both) — Two frameworks to integrate, nobody has tested this combination, architectural mismatch between LangGraph's ReAct-loop model and Locutus's council deliberation pattern.

**Why custom orchestration:**
- Locutus's patterns are specific: council rounds (sequential with parallel LLM calls), workstream DAG (topological sort + goroutines), supervision loop (retry with state tracking). These are ~350 LOC total, not a generic framework.
- LangGraph is designed for stateful agent conversation loops (ReAct, tool-use). Locutus's council is a deliberation among specialized roles — a different pattern that doesn't cleanly map to LangGraph's graph nodes.
- We already have state persistence (specio + historian) and don't need LangGraph's checkpointing.
- Genkit Go's config-string model selection (`anthropic/claude-sonnet-4-20250514`) directly supports manifest-driven provider switching, which LangChainGo doesn't offer as cleanly.

**What we build:** DAG executor (~150 LOC), council round manager (~200 LOC). What Genkit Go provides: multi-provider LLM access, structured output, tool registration.
