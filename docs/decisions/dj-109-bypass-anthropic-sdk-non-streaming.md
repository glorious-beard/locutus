## DJ-109: Bypass Anthropic SDK Non-Streaming Preflight via Explicit Per-Request Timeout

**Status:** shipped

**Decision:** Pass `option.WithRequestTimeout(9m45s)` when constructing the Anthropic client. This bypasses the SDK's `CalculateNonStreamingTimeout` preflight check ("streaming is required for operations that may take longer than 10 minutes") which was rejecting any strong-tier request (Opus 4.7 with `max_output_tokens: 32768`) upfront — the SDK's heuristic estimates wall-clock as `(3600s × max_tokens / 128000)`, which is 921s = 15.4 min for those parameters and exceeds the 10-min default ceiling.

**Why an explicit timeout works:** the SDK's preflight at [`client.go:140-142`](https://github.com/anthropics/anthropic-sdk-go/blob/v1.23.0/client.go#L140-L142) returns immediately when `RequestTimeout` is set: *"if the user has set a specific request timeout, use that"*. Setting any non-zero value skips the heuristic entirely. The per-model `ModelNonStreamingTokens` cap doesn't apply to us — our pinned models (`claude-opus-4-7`, `claude-sonnet-4-6`, `claude-haiku-4-5-20251001`) aren't in that map; only older opus-4 / opus-4-1 variants are.

**Why 9m45s and not 30m or 1h:** observed Anthropic call durations on the strong tier top out at 1-3 minutes for the council's reconciler (the most demanding agent). 9m45s gives 3-10× headroom over realistic worst cases while staying just under the SDK's nominal "you should probably stream" mark. Setting it materially higher would say *"we're routinely doing single calls over 10 minutes"* — which would be a real signal to implement streaming, not to keep raising the number. The constant carries that contract in its comment.

**Why not implement streaming now:** streaming is the canonical path for long-running calls and is the right structural fix for genuinely-long workloads. But we don't have evidence yet that real calls exceed 9m45s. Implementing streaming is a substantial change to the dispatch loop (chunk accumulation, partial message handling, citation extraction across stream events) — not worth the lift before measurement justifies it. If a single call ever exceeds the 9m45s ceiling, that's the trigger to migrate.

**What happens if a call does exceed the cap:** the SDK returns a per-attempt timeout error, classified as `ErrTimeout` per Bug A. The retry loop kicks in (with the new Retry-After honoring per DJ-mid-this-session), and the executor's preference-walk fallback fires if Anthropic keeps timing out. None of those paths regress; the cap is a graceful-degradation boundary, not a hard stop.

**What lands:**

- New constant `anthropicRequestTimeout = 9*time.Minute + 45*time.Second` with the load-bearing rationale in its comment.
- `NewAnthropicAdapter` constructs the client with `option.WithRequestTimeout(anthropicRequestTimeout)`.

**What stays the same:**

- The locutus per-call context timeout (`DefaultLLMCallTimeout = 15m`, override via `LOCUTUS_LLM_TIMEOUT`). This still bounds total wall-clock above the SDK timeout.
- Per-agent frontmatter `timeout` field (e.g., `spec_feature_elaborator` has `timeout: 5m`). Those override the default for specific agents.
- Gemini and OpenAI adapters. Their SDKs don't have the same heuristic preflight.

**Reversal criteria:** revert the constant downward if a real call legitimately needs more than 9m45s (signal: session traces show successful calls approaching the cap regularly), at which point streaming is the right migration. Or revert upward if the cap proves too low for a specific agent (in which case prefer per-agent timeout overrides over raising the global cap). The constant is a clear contract; both directions are addressable in the same file.

**Reference:** triggered by a `refine goals` failure on the `spec_reconciler` step in winplan after DJ-107 routed strong-tier agents to anthropic-first. SDK source verified at `anthropic-sdk-go@v1.23.0/client.go:132-157` (`CalculateNonStreamingTimeout`). Anthropic SDK long-requests guidance at `platform.claude.com/docs/en/api/sdks/typescript`. Companion to DJ-108 (which moved Anthropic strict-mode to native `OutputConfig`); each fixed a different latent issue surfaced by the anthropic-first reorder.
