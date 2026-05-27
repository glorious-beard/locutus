## DJ-056: Fast-Tier LLM Monitor Replaces Go Heuristic Watchdog

**Status:** shipped

**Decision:** For fuzzy supervision decisions (churn detection, scope drift, stalled progress, invented requirements), use a fast-tier LLM ("Haiku-class") invoked periodically over a sliding event window. Go code handles only mechanical bookkeeping: ring buffer of recent events, cooldown clock between invocations, circuit breaker for repeated errors. No pattern-detection heuristics in Go.

**Why:** Heuristics for "what counts as churn" would always chase edge cases. Coding agents evolve, emit new event patterns, interleave legitimate retries with actual cycles. An LLM observes the pattern in context and adapts without code changes. Tuning happens in the `monitor.md` agent's prompt, not in Go. Cost is bounded by a cooldown (≥10 events between invocations) and a cheap model tier.

**Alternatives considered:** Pure Go heuristics (fragile, high maintenance). Pure LLM on every event (prohibitive cost). Tiered — Go watchdog triggers LLM judgment (still has the heuristic fragility problem). Picked pure periodic LLM because it shifts all judgment to the prompt, which is the right surface for this kind of decision.
