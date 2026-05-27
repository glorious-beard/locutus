## DJ-053: Capability Tiers with Multi-Provider Resolution

**Status:** superseded by DJ-067

**Decision:** Agent frontmatter specifies `capability: fast|balanced|strong` instead of a specific model. The capability tier resolves to an actual model at `BuildGenerateRequest` time via configurable mapping. Default mapping uses Anthropic models (Haiku/Sonnet/Opus). Future: discover available providers from env vars (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`) and map tiers to the best available model per provider, possibly via LLM-powered routing for task-specific provider selection.

**Why:** Hardcoding model names in agent defs couples content authoring to specific providers. Capability tiers let users: swap providers without rewriting agents, pay Haiku prices for bounded-judgment tasks (convergence, historian, watchdog), reserve Opus for complex architectural work.

**Implementation today:** Static tier → default Anthropic model mapping. Multi-provider LLM routing deferred to future plan.
