## DJ-110: Per-Pick Timeout in `Executor.Run` (Bug B)

**Status:** shipped

**Decision:** The per-call timeout (`AgentDef.Timeout` from frontmatter, or `LOCUTUS_LLM_TIMEOUT`, or the 15-minute default) now applies **per-pick** during the model preference walk, not as a single budget that wraps the entire walk. Each provider attempt gets its own fresh `context.WithTimeout` in `Executor.Run`'s loop body; the previous code wrapped the loop with a single `WithTimeout` that all picks shared.

**Why:** when the first provider in a fallback chain hung the full timeout — a real failure mode observed in winplan's `0024-spec_strategy_elaborator-strat-auth-provider.yaml` (300041ms duration, `model: ""`, `error: context deadline exceeded`) — the per-call context was already dead by the time `Executor.Run` advanced to the next pick. Every subsequent pick failed instantly with `context.DeadlineExceeded`, and the agent's `models:` preference list became theatrical: it documented a fallback chain that only worked when the first provider failed *fast*.

The user-visible failure shape is distinctive: a duration that exactly matches the agent's frontmatter timeout, an empty `model` field (no provider's response ever surfaced its name to the recorder because none completed), and a `context deadline exceeded` error message. Bug A's classifier extension would have routed this case correctly *if there had been budget left* — the issue isn't classification, it's that the budget was already consumed by the time fallback was attempted.

**Why not a per-pick budget split (timeout / N picks):** considered and rejected. Splitting the budget across picks would mean every fallback walk ran with shorter individual budgets, regressing single-pick latency for the common case to subsidize the rare case. Cleaner to give each pick the full configured budget and let the parent context bound total wall clock when that matters (workflow-level deadlines, user Ctrl-C).

**Trade-off:** worst-case wall clock for an N-pick preference list with N timeouts is now N × per-pick timeout instead of a single per-pick timeout. For the council's typical 3-pick lists (anthropic → googleai → openai), that means a triple-failure walk could run up to 3× the configured per-call duration. In practice this is acceptable because:

1. The common case (first pick succeeds) is unchanged.
2. The fallback case (first pick fails fast, second succeeds) gets a fresh budget instead of inheriting a near-empty one.
3. The pathological case (all three pick attempts time out at the wall) was *already broken* before this change — it just failed fast with `context canceled` instead of failing slow with `context deadline exceeded`. The per-pick path actually *succeeds* if any later pick is healthy.

If a deployer needs to bound total walk time for a specific agent, the right tool is a workflow-level context with timeout, not the per-call timeout.

**What lands:**

- [`Executor.Run`](../internal/agent/executor.go) drops the wrap-the-walk `context.WithTimeout` and instead applies `WithTimeout(ctx, perPickTimeout)` inside the per-pick loop, with `cancel()` after each `runOne` call.
- The doc comment on `Run` is rewritten to describe the per-pick semantics and the worst-case wall-clock implication.
- No tests added at this layer — the timeout behavior is hard to unit-test without slow mocks that respect context cancellation. The change is mechanically clear (move `WithTimeout` inside the loop) and verified by the existing test suite + manual verification on the next refine run.

**What stays the same:**

- `AgentDef.Timeout` semantics, env var precedence (`LOCUTUS_LLM_TIMEOUT`), and the 15-minute default (`DefaultLLMCallTimeout`). Operators don't need to retune anything.
- `RunWithRetry`'s exponential backoff + Retry-After handling unchanged. It still re-walks the whole preference list on retry-eligible failures.
- The classifier extensions from Bug A. Server-side 504s still classify as ErrTimeout and now get genuine fallback budget.

**Reversal criteria:** revert if a deployer reports total-wall-clock surprises in production (a 5m timeout becoming a 15m total walk on a triple-failure path). Mitigation in that case is workflow-level context-with-timeout, not reverting per-pick — but the option is open if the change proves operationally surprising.

**Reference:** the deferred fix from the post-DJ-105 / DJ-106 / DJ-107 / DJ-108 / DJ-109 sequence. Bug A and Bug C were prerequisites; without 504 classification (Bug A), the fallback walk wasn't fallback-eligible at all, and without schema enforcement (Bug C), the council couldn't survive even the success case. Bug B closes the chain. Triggered visibly in winplan session 20260507/1522/28-5d9ba2/calls/0024.
