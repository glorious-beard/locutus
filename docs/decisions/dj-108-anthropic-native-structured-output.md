## DJ-108: Anthropic Native Structured Output + Adaptive Thinking

**Status:** shipped

**Decision:** Migrate the Anthropic adapter from synthetic-tool-plus-forced-`tool_choice` strict-mode JSON enforcement to native `MessageNewParams.OutputConfig.Format.Schema`, and from the deprecated `thinking: {type: enabled, budget_tokens: N}` API to `thinking: {type: adaptive}` paired with `OutputConfig.Effort` ("low" | "medium" | "high" | "max"). Both changes ship together because they're entangled: the deprecated thinking-budget API is no longer supported on Opus 4.7+, and the strict-mode JSON enforcement via forced `tool_choice` was incompatible with extended thinking even on prior models.

The migration removes ~80 lines of adapter code (synthetic schema tool, forced-tool dispatch branch, "schema tool not invoked" error path, the thinking-allowed-when-not-forced gate from the prior hotfix) and replaces them with a single `OutputConfig.Format` assignment plus adaptive thinking.

**Why now:** the user's first attempt to run `refine goals` after DJ-107's anthropic-first reorder hit a 400 from Anthropic — *"Thinking may not be enabled when tool_choice forces tool use."* A short-lived hotfix suppressed thinking on forced-tool calls; that fix landed and worked, but it was the wrong shape. Investigating the SDK revealed `MessageNewParams.OutputConfig` and `ThinkingConfigAdaptiveParam` — the canonical APIs for structured output and thinking budget on current Claude models — neither of which the adapter was using. The adapter was essentially running on the pre-2026 implementation pattern, which still works on legacy models but not on Opus 4.7+.

**Why this works across all three pinned models:**

- **Opus 4.7 (strong):** the migration guide explicitly says the enabled-budget API is *no longer supported*; adaptive + Effort is the required path.
- **Sonnet 4.6 (balanced):** Anthropic's own prompt-engineering best-practices doc shows `thinking: {type: "adaptive"}` + `output_config: {effort: "high"}` as the canonical pattern for this model.
- **Haiku 4.5 (fast):** structured outputs were enabled in the December 4, 2025 release, expanding parity across the family.

So the migration is uniform — no per-model branching. The Models API exposes per-model capabilities (`structuredOutputs.supported`, `thinking.types.adaptive.supported`, `effort.high.supported`) for runtime introspection if a future need arises to vary behavior, but for the embedded models.yaml, all three pins support the unified path.

**Mapping `ThinkingLevel` to the new API:**

| `ThinkingLevel` | `Thinking` config                | `OutputConfig.Effort` (when schema set) |
| --------------- | -------------------------------- | --------------------------------------- |
| `ThinkingOff`   | unset (default: no thinking)     | unset                                   |
| `ThinkingOn`    | `ThinkingConfigAdaptiveParam{}`  | `medium`                                |
| `ThinkingHigh`  | `ThinkingConfigAdaptiveParam{}`  | `high`                                  |

`Effort` lives on `OutputConfig`, so it only attaches when `req.OutputSchema != nil`. Free-form-output calls get adaptive thinking without an Effort hint and the model self-paces.

**Why this composes cleanly with `Grounding`:** `OutputConfig` and `Tools` are independent fields on `MessageNewParams` — there's no `tool_choice` constraint forcing a specific tool, so the model is free to call `web_search` (or any other tool) before producing the structured response. Per the Anthropic docs example: *"Claude may call the tool first (tool_use) or respond with JSON (text)."* This was the architectural question that motivated the migration: with the old forced-`tool_choice` path, it was unclear whether server tools could fire intermediately. The new path makes it explicit and uncomplicated.

**Code structure changes:**

- New helper: `buildAnthropicMessageNewParams(req Request) MessageNewParams` — pure function, encapsulates all the param-building logic. Unit-testable in isolation; existing `buildAnthropicMessages` and `buildAnthropicTools` are called from inside it.
- `dispatch` loses the `forcedSchema bool` parameter and the entire forced-schema-tool extraction branch. The model's response is now uniformly read from text content blocks regardless of whether a schema was set.
- `buildAnthropicTools` loses the `includeSchemaTool` parameter — it never has reason to emit the synthetic tool now.
- Constants `schemaToolName` ("submit_response") and helper `thinkingAllowed` are deleted.
- Imports clean up (`log/slog` no longer needed in this file since the suppression Warn is gone).

**What lands:**

- 5 new tests in `anthropic_native_output_test.go` covering: `OutputConfig.Format.Schema` is set when schema present; no forced `tool_choice`; no synthetic tool; adaptive thinking + `Effort` mapping for `ThinkingOn`/`High`; thinking off clears both fields; adaptive without schema gets no `Effort`; grounding composes with structured output.
- The hotfix test (`anthropic_thinking_test.go`) is deleted — `thinkingAllowed` no longer exists; the constraint it tested no longer applies.

**What stays the same:**

- The Gemini and OpenAI adapters. Gemini 3+ already composes schema + tools + grounding cleanly; OpenAI's Responses API has had native `json_schema` strict mode since 2024.
- DJ-099-era system-prompt cache_control marker. Anthropic's caching mechanism is independent of the structured-output API.
- DJ-106 user-message prompt caching. Same — independent.
- The retry / fallback / per-pick concurrency / Retry-After honoring layers all remain.
- Spec generation, council shape, agent frontmatter, all remain.

**What gets simpler:**

- Anthropic dispatch is now uniform across schema and free-form calls — both paths read from text content blocks.
- The "Anthropic API rejects extended thinking + forced tool_choice" failure mode is no longer reachable; the gate that suppressed thinking is gone.
- Strict-mode JSON enforcement is server-side without the synthetic-tool intermediary.

**Reversal criteria:** revert if (a) the Anthropic API removes or substantially changes the `OutputConfig` API in a way that affects current models — would surface as a regression on a working refine run after a backend change, in which case fall back to whatever pattern the migration guide of the day prescribes; or (b) some current Anthropic model is found to NOT support `OutputConfig` despite the docs claim (would need per-model capability detection via the Models API at startup, which is a larger change). Neither is structurally hard; both are bounded to this adapter file.

**Reference:** Anthropic structured-outputs documentation (`platform.claude.com/docs/en/build-with-claude/structured-outputs`), Anthropic migration guide for thinking config (`platform.claude.com/docs/en/about-claude/models/migration-guide`), and the December 4, 2025 release note ("Structured outputs now support Claude Haiku 4.5") that confirmed cross-tier availability. Builds on DJ-099 (direct-SDK migration), DJ-105 (the strict-JSON enforcement principle this preserves), DJ-107 (the anthropic-first reorder that surfaced the latent thinking conflict). Supersedes the short-lived `thinkingAllowed` hotfix in commit 5dc0a3a — that fix worked but addressed the symptom; DJ-108 addresses the cause.
