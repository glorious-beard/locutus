# Refined Plan: Direct-SDK Agent Execution Layer (Option B)

This plan supersedes [.claude/plans/agent-execution-layer.md](.claude/plans/agent-execution-layer.md). After exploring an alternative coding-agent-CLI executor (Option C), we evaluated and rejected it: with Locutus retaining workflow orchestration, C's structural advantage collapses to subscription billing alone, traded against CI/CD friction, lost session-trace infrastructure, subprocess overhead, and CLI version coupling. B is the right answer.

## Context

Genkit Go's edges have been biting us repeatedly: format-handler mutations, no Anthropic input-side `cache_control`, Gemini cache validator rejecting system prompts, schema-conformance pathologies (DJ-092 → DJ-095 → DJ-097 → DJ-098). We've already half-written the wrapper we'd need anyway: per-model concurrency caps, per-call timeouts, multi-round tool-use captures, session traces, schema registry, provider detection are all custom Locutus code on top of Genkit. Genkit is buying us plugin init and `ai.GenerateOption`-style request building. The rest we already own.

**Goal:** replace Genkit with an in-house direct-SDK adapter layer where the boundary lives at the **agent level**, not at a generic `LLM.Generate(prompt)` interface. The executor takes an agent definition + input and picks provider+model based on agent declarations + availability + activity-mapping overrides.

**Outcome:**
- Provider-native strict-mode schema enforcement on every adapter (Anthropic forced tool-use, Gemini `responseSchema`, OpenAI `response_format: json_schema strict:true`) — fixes the schema-conformance bug class at the API layer.
- Provider-side prompt caching works (Anthropic `cache_control`, Gemini `CachedContents`, OpenAI automatic prefix).
- OpenAI Chat Completions becomes the lowest-common-denominator adapter (covers OpenAI native + OpenRouter + DeepSeek + Mistral + Together + Groq + Fireworks + vLLM + Ollama + LM Studio).
- ~40 transitive Go modules dropped (Genkit pulls flow runtime, dotprompt, telemetry shims).
- All existing Locutus infrastructure preserved: per-call session traces, observability, CI/CD compatibility.

## Considered alternative: coding-agent CLI executors (rejected)

We seriously evaluated outsourcing per-call execution to coding-agent CLIs (Claude Code, Gemini CLI, Codex) running as subprocesses with filesystem IPC. The hypothesis: the agent runtime's multi-round self-correction loop would fix the schema-conformance bug class.

The path was rejected because:

1. **Workflow orchestration via the CLI's own dispatcher (Y) doesn't generalize.** Claude Code's Task tool fans out cleanly; Gemini CLI and Codex don't have equivalents. Per-call dispatch (X) is the only portable model. With X, most of C's structural advantage disappears.
2. **Pre-auth CLI requirement breaks CI/CD.** Locutus runs interactively today, but headless deployment matters; CLI-based auth (OAuth + browser) doesn't work unattended without falling back to API keys, which defeats the subscription-billing benefit.
3. **Session-trace infrastructure regression.** Existing per-call YAML traces with structured fields (`InputTokens`, `OutputTokens`, `ThoughtsTokens`, `Rounds`, captured `Message`) become parsing-against-`stream.ndjson` exercises. Locutus loses direct LLM visibility.
4. **Bug-class fix isn't uniquely C's.** Provider-native strict-mode schema enforcement (which Genkit didn't surface cleanly) prevents structural conformance failures at the API level. A direct-SDK adapter that always uses strict mode likely fixes DJ-098-class bugs as effectively as the agent loop, with much lower latency and cost.
5. **Subprocess overhead at fanout scale** (~5–15s per call vs ~2–5s) and CLI version coupling are real ongoing costs.

The C path remains viable as a future opt-in `ClaudeCodeExecutor` implementation behind the same `AgentExecutor` interface, for users who specifically want the subscription-billing benefit. Not the primary architecture.

## Design constraint: agent-level abstraction, NOT a generic LLM-glue rewrite

The load-bearing decision. The executor takes an agent definition + input, not a prompt + model. Capability routing, cache-prefix management, tool-use loop, schema enforcement strategy — all live in the executor. Adapters are thin: translate `AgentRequest` to a provider-specific SDK call, dispatch, return `AgentResponse`. Adapters know their provider's API; the executor knows Locutus's needs.

```go
type AgentExecutor interface {
    Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error)
}
```

## Provider matrix

Three native adapters — one per provider, on each provider's primary forward-looking API:

| Provider | SDK | Caching | Grounding | Thinking | Strict schema |
|---|---|---|---|---|---|
| **Anthropic** | `github.com/anthropics/anthropic-sdk-go` | `cache_control` markers | not in SDK today | `thinking_config` extended thinking | forced tool-use with schema |
| **Gemini** | `google.golang.org/genai` | `CachedContents` API | `GoogleSearch` (excludes custom tools) | `thinkingConfig.includeThoughts` | `responseSchema` |
| **OpenAI Responses** | `github.com/openai/openai-go` against `/v1/responses` | automatic prefix ≥1024 tok | built-in `web_search_preview` (compatible with custom tools) | `reasoning_effort` (o-series) | `response_format: {type: "json_schema", strict: true}` + `text.format` |

OpenAI's Chat Completions API is in maintenance mode; OpenAI is investing in Responses. Functionally Responses is a superset (built-in tools, grounding, response chaining, same strict json_schema mode). One OpenAI adapter, on the API OpenAI is investing in.

The Responses adapter gives us **grounding parity with custom tools** — agents that need both (where Gemini's `GoogleSearch` would exclude them) route to OpenAI Responses. Scout's `models:` priority list, for example, can declare Gemini as primary (cheaper grounding) and OpenAI Responses as fallback (when scout also needs custom tools).

### Deferred

- **LCD via Chat Completions against arbitrary base URLs** (OpenRouter, DeepSeek, Mistral, Together, Groq, Fireworks, vLLM, Ollama, …): requires per-provider config (env var name, base URL, auth header style, capability flags). Add on demand as dedicated Chat-Completions-compatible adapter when there's real demand for non-OpenAI Chat-format providers.
- **Coding-agent CLI executors** (`ClaudeCodeExecutor`, etc.): viable opt-in path for users who want subscription billing. Same `AgentExecutor` interface; spawn CLI subprocess. Deferred until/unless real demand surfaces.

## Architecture

```
internal/agent/
├── executor.go            # AgentExecutor + activity-aware dispatch
├── policy.go              # picks (provider, model) given agent + availability + activity mapping
├── providers.go           # DetectProviders (moved from genkit.go), ProviderName constants
├── adapters/
│   ├── anthropic.go       # native Anthropic SDK adapter
│   ├── gemini.go          # native Gemini SDK adapter
│   └── openai_responses.go # OpenAI Responses adapter
├── caching.go             # cache marker helpers (per-adapter glue)
├── tools.go               # tool registration + multi-round tool-use loop
├── schema.go              # provider-native strict-mode schema enforcement helpers
└── concurrency.go         # per-(provider/model) semaphores
```

The executor is the only thing that knows about all the capability-aware concerns. Adapters are intentionally narrow.

## Strict-mode schema enforcement

Every adapter uses provider-native strict mode for structured output:

- **Anthropic**: forced tool-use with the schema as a single tool. The model literally cannot return text — only the tool call with the schema-conforming JSON.
- **Gemini**: `responseSchema` (incompatible with custom tools — policy layer detects this conflict and falls back to prompt-only schema enforcement when both are needed, with a Warn).
- **OpenAI Chat**: `response_format: {type: "json_schema", strict: true}`.

Schemas are still registered via Go reflection (`RegisterSchema(name, exampleStruct)`); adapters consume them in their provider-native format.

This is the load-bearing fix for the DJ-098 class of bugs. Genkit's path didn't surface these strict modes cleanly; direct-SDK control means we always use them. Defensive merge-handler guards remain as belt-and-suspenders.

## Model selection: per-agent frontmatter + tier table

Two-layer configuration:

**Layer 1 — `.borg/models.yaml`**: tier definitions per provider, including the operational knobs (max output tokens, thinking budget) that agents shouldn't have to think about.

```yaml
providers:
  anthropic:
    fast:
      model: claude-haiku-4-5-20251001
      max_output_tokens: 8192
      thinking: off
    balanced:
      model: claude-sonnet-4-6
      max_output_tokens: 16384
      thinking: on
    strong:
      model: claude-opus-4-7
      max_output_tokens: 32768
      thinking: high
  googleai:
    fast:     {model: gemini-2.5-flash-lite, max_output_tokens: 8192,  thinking: off}
    balanced: {model: gemini-2.5-flash,      max_output_tokens: 16384, thinking: on}
    strong:   {model: gemini-2.5-pro,        max_output_tokens: 32768, thinking: on}
  openai:
    fast:     {model: gpt-5-mini, max_output_tokens: 8192,  thinking: off}
    balanced: {model: gpt-5,      max_output_tokens: 16384, thinking: on}
    strong:   {model: o3-pro,     max_output_tokens: 32768, thinking: high}
```

`thinking` is a coarse `off`/`on`/`high` enum that adapters translate to provider-specific values:

- Anthropic: `on` → `thinking_config: {type: enabled, budget_tokens: 4096}`; `high` → 8192.
- Gemini: `on` → `thinkingConfig.includeThoughts: true` + 4096 budget; `high` → 8192 budget.
- OpenAI o-series: `on` → `reasoning_effort: medium`; `high` → `reasoning_effort: high`.

This shape extends today's `.borg/models.yaml` (tier list per provider) with the operational defaults, eliminating the need for per-agent `temperature`, `thinking_budget`, and `max_tokens` overrides. **Pre-optimized executors mantra**: provider knobs that exist do so for deployment-level tuning, not per-agent flexibility.

**Layer 2 — Agent frontmatter**: priority-ordered list of `(provider, tier)` preferences. Each agent declares its own routing.

```yaml
# spec_strategy_elaborator.md
---
id: spec_strategy_elaborator
role: planning
output_schema: RawStrategyProposal
models:
  - {provider: anthropic, tier: balanced}   # primary
  - {provider: googleai, tier: strong}      # fallback
---
```

```yaml
# spec_scout.md  (needs grounding)
---
id: spec_scout
role: survey
output_schema: ScoutBrief
grounding: true
models:
  - {provider: googleai, tier: strong}
  - {provider: openai, tier: strong}        # OpenAI Responses fallback (when added)
---
```

```yaml
# spec_reconciler.md  (custom tools — must NOT be Gemini, which excludes tools+responseSchema)
---
id: spec_reconciler
role: reconcile
output_schema: ReconciliationVerdict
tools: [spec_list_manifest, spec_get]
models:
  - {provider: anthropic, tier: strong}
  - {provider: openai, tier: strong}
---
```

**Resolution at dispatch time:**

1. Walk `def.Models` in declaration order.
2. For each `(provider, tier)`: is the provider configured (API key env var present, capability available)?
3. First match: resolve tier → concrete model via `models.yaml.providers[<provider>][<tier>]`. Dispatch through that adapter.
4. On retryable failure (rate limit, transient error), advance to the next entry in the list. WARN event when fallback fires.
5. If no entries are reachable, surface error to the workflow.
6. For agents without an explicit `models:` list, fall back to `capability:` (existing field) interpreted as "any configured provider, this tier" — preserves backward compatibility during migration.

**What this is over the activity-centric proposal:**

- **Cohesion.** Agent definition + routing intent live in one file.
- **Less central config.** `models.yaml` stays tier-table-shaped (already implemented today).
- **Per-deployment override unchanged.** Edit `models.yaml` to remap `balanced → haiku` budget-wide. Edit an agent .md to change its provider preference.
- **Provider-availability mechanism preserved.** Same shape as today's Genkit-path tier resolution against `DetectProviders()`.

## Pre-optimized per-provider executors

Each adapter **bakes in** the right behavior for its provider. No configurable knobs exposed to agents or projections. The executor knows how its provider caches, what strict-mode shape it accepts, how to set thinking budgets.

- **AnthropicAdapter**: `cache_control` markers on stable prefix; forced tool-use schema; `thinking_config` for non-zero thinking budgets; per-model concurrency cap.
- **GeminiAdapter**: `CachedContents` create + reference; `responseSchema` (or prompt-only when tools also requested); `thinkingConfig.includeThoughts` always on.
- **OpenAIChatAdapter**: automatic prefix caching (no markers); strict json_schema mode; `reasoning_effort` mapped from thinking budget for o-series models.

Configuration knobs that exist do so because Locutus genuinely needs to tune them across activities (`models.yaml`), not for generic flexibility.

## What we keep

- **`schemaRegistry` + `RegisterSchema()`** ([internal/agent/schemas.go](internal/agent/schemas.go)): the Go reflection registry stays. Adapters consume registered schemas in their provider-native format.
- **`workflow.go`'s orchestration** — fanout dispatch, `mergeResults` per-step handlers, conditional gates, retry/timeout. Each per-step LLM call now dispatches through `AgentExecutor.Run` instead of `LLM.Generate`. Orchestration shape is unchanged; the call mechanic is.
- **Deterministic Go logic** — reconciliation surgery (`ApplyReconciliation`), `MechanicalCluster`, `appendIntegrityFindings`, `assembleRawProposal`/`assembleRevisedRawProposal`. All stay as Go functions invoked by `workflow.go` between LLM steps.
- **Agent definitions** under [internal/scaffold/agents/](internal/scaffold/agents/) in their existing Locutus frontmatter format. No move to `.claude/agents/`; we're not using Claude Code's subagent format here.
- **Session traces** ([internal/agent/session.go](internal/agent/session.go)) — per-call YAML capture stays exactly as today. Adapters populate the same fields (`InputTokens`, `OutputTokens`, `ThoughtsTokens`, `Rounds`, `Message`) so traces stay shape-compatible.
- **`PlanningState`** ([internal/agent/state.go](internal/agent/state.go)): unchanged; accumulates per-step results across the workflow as it does today.
- **`MockLLM`** ([internal/agent/mock_llm.go](internal/agent/mock_llm.go) or wherever) — analog `MockExecutor` for unit tests. Same record/replay pattern.

## What we move

- **`DetectProviders()`** out of `genkit.go` into a new `providers.go`. Function signature unchanged.
- **Concurrency caps** — today on `GenKitLLM.concurrencyLimits`. Move to provider-agnostic `concurrency.go`; the executor wraps each adapter call in the per-model semaphore.

## What we drop

- **Genkit Go entirely** ([internal/agent/genkit.go](internal/agent/genkit.go)): provider plugin init, `ai.GenerateOption`, capture middleware, tool registry shim.
- **`captureMW`** — workaround for Genkit's format-handler mutation. Not needed when adapters construct `GenerateResponse` directly from the SDK reply.
- **`spec_tools.go`** — Genkit-registered `spec_list_manifest` and `spec_get` tools. Replace with a Locutus-owned tool registry (`tools.Register(name, schema, handler)`) consumed by adapters' tool-use loops.

## How this resolves the recurring bug class

| Past failure | Underlying issue | How direct-SDK strict mode addresses it |
|---|---|---|
| Triager dropping 25/40 findings (DJ-098) | Model emitted only 2 of 3 schema arrays under attention pressure | Strict mode at API level — model literally cannot omit required fields |
| `[{}]` placeholder regression | Model filled empty bucket with placeholder shape | Strict mode rejects empty objects in arrays where item schema requires fields; defensive guard catches anything that slips through |
| `winner` vs `canonical` drift | Prose said "winner" while schema field was `canonical` | Adapter passes the schema as the source of truth; prose mandates remain but are no longer the only enforcement |
| Empty `decisions: [{}]` placeholders | Architect short-circuited under multi-finding pressure | Per-cluster invocation = bounded scope per call (already achieved via DJ-098); strict mode prevents structural placeholders |
| Gemini Flash dropping additions field | Schema example shape collapse | `responseSchema` is the API-level constraint; example pattern-matching pathology disappears |

Three rounds of prose-mandate fixes were treating symptoms; strict-mode at the API layer fixes the cause. The direct-SDK adapters use this on every call, not just when prose remembers to ask for it.

## Migration plan

Single work stream, no feature flag. The current Genkit-based path doesn't reliably work (DJ-098-class bugs); phasing from one broken state through a flag-gated intermediate doesn't buy anything. Git is the safety net — revert to a prior commit if something fundamental breaks.

**Sequence (commits, not phases — all on main):**

1. **Strict-mode viability spike** (1–2 days). Throw-away code: direct Anthropic SDK with forced tool-use, direct Gemini SDK with `responseSchema`, both running the May-4 winplan fixture (40-finding clusterer input). Validate strict mode actually fixes the bug class (40/40 lossless on both providers). Spike code is not merged; it's a yes/no decision input.

2. **Land the executor scaffolding.** `AgentExecutor` interface, `policy.go` with frontmatter-based routing, `providers.go` (moved `DetectProviders`), `concurrency.go` (moved semaphores), `schema.go` (strict-mode helpers), updated `models.yaml` shape with per-tier operational defaults. No adapter implementations yet; existing Genkit path keeps working.

3. **Land the four adapters in sequence.** One commit per adapter (Anthropic, Gemini, OpenAI Chat, OpenAI Responses), each with unit tests against mocked SDK clients. Adapters not yet wired into `workflow.go`.

4. **Land the `tools.go` registry + tool-use loop.** Replaces `spec_tools.go`. Reconciler agent will consume this once wired.

5. **Wire the executor into `workflow.go` and migrate every agent in one commit.** Drop the `LLM` interface + `GenKitLLM`. Delete `genkit.go`, `spec_tools.go`. Drop `firebase/genkit/go` from `go.mod`. All council agents now dispatch via `AgentExecutor.Run`.

6. **Validate end-to-end.** Run `locutus refine goals` on the winplan project. Confirm output matches or exceeds v3 baseline (12 strategies / 7 features / 30 decisions). Confirm caching reduces input tokens by ≥50% on the elaborate fanout (Anthropic + Gemini).

7. **Update agent frontmatter to declare per-agent `models:` priority lists.** Migrate from `capability:` tier hints to explicit provider preferences. One commit; small mechanical change.

If step 1's spike fails (strict mode doesn't fix the bug class), the rest of the migration is invalidated. We'd reconsider. Steps 2–4 are net-additive and reversible. Steps 5–7 are the actual cutover; if winplan regression appears, git reset to step 4's commit and diagnose.

This is sized as a focused work-stream rather than a phased migration. No feature flag, no parallel paths.

## Critical files to modify

**New:**

- `internal/agent/executor.go` — `AgentExecutor` interface, `AgentInput`/`AgentOutput` types
- `internal/agent/policy.go` — activity/availability → `(provider, model)` resolution + table tests
- `internal/agent/providers.go` — moved `DetectProviders`, `ProviderName` constants
- `internal/agent/adapters/anthropic.go` — Anthropic SDK adapter
- `internal/agent/adapters/gemini.go` — Gemini SDK adapter
- `internal/agent/adapters/openai_responses.go` — OpenAI Responses API adapter (built-in `web_search_preview` for grounding-with-tools)
- `internal/agent/concurrency.go` — per-(provider/model) semaphores
- `internal/agent/schema.go` — provider-native strict-mode schema helpers
- `internal/agent/tools.go` — Locutus-owned tool registry + multi-round tool-use loop

**Modified:**

- `internal/agent/llm.go` — keep `LLM` interface during migration window; add `AgentExecutor` alongside
- `internal/agent/workflow.go` — `executeAgent` calls the executor when the flag is set
- `internal/agent/state.go` — thread the executor alongside `LLM` in `WorkflowExecutor`
- `internal/agent/model_config.go` — schema flips from tier-centric to activity-centric
- `cmd/llm.go` — instantiate the new executor (post phase 2)

**Deleted (phase 2):**

- `internal/agent/genkit.go`
- `internal/agent/spec_tools.go` — replaced by `tools.Register` calls in `cmd/llm.go`

## Verification

- **Phase 0 strict-mode validation:** May-4 fixture lossless completeness on Anthropic and Gemini direct-SDK paths.
- **Adapter unit tests:** mocked SDK clients; assert request translation (schema, caching markers, tool defs) and response parsing.
- **Policy table test:** `(def, availability, overrides) → (provider, model, err)` covering tier preferences, capability filters, override precedence, no-compatible-provider error.
- **Integration tests per provider:** gated on env-var presence; same agent definition runs end-to-end on each.
- **Cache assertion test:** run a fanout twice in succession on Anthropic and Gemini; assert second-call cached-token count > 0.
- **Tool-use loop test:** synthetic tool returning a fixed payload; assert multi-round dispatch terminates correctly across all adapters.
- **End-to-end winplan:** `locutus refine goals` on the new path; verify ≥ v3 baseline (12 strategies / 7 features / 30 decisions); verify caching reduces input tokens by ≥50% on the elaborate fanout.
- **Migration safety:** with `LOCUTUS_USE_DIRECT_SDK=0` after each migration, behavior on the old path unchanged.

## Risks

1. **Strict mode doesn't fully fix the bug class.** Mitigation: the strict-mode viability spike validates this directly before broader work. If strict mode + defensive guards aren't enough, prompts get tightened or the deferred CLI-executor path reopens for the affected agents.
2. **Tool-use loop semantics differ across providers.** Mitigation: tool-loop logic central in `tools.go`; per-provider differences in small adapter helpers.
3. **Sustained-primary-failure fallback drift.** Cascade-to-fallback can multiply cost silently. Mitigation: WARN event on every fallback; sustained primary failures are alertable.

Note: provider SDK churn and frontier-model evolution are operating-environment realities, not specific risks. Adapt or die. The narrow risk register above is what's specific to this plan.

## Driver architecture: in-tree, plugin-ready interface

Adapters live in-tree under `internal/agent/adapters/`. Plugin support (e.g., via `github.com/hashicorp/go-plugin`) deferred until external demand surfaces. Reasoning:

- Realistic adapter count is 3 (Anthropic, Gemini, OpenAI Chat) plus any additions over time. In-tree maintenance is cheap.
- No third-party ecosystem yet — plugins are right for Terraform-scale ecosystems, not a CLI tool with a small officially-supported integration set.
- A plugin interface, once published, becomes a public API. Hard to evolve.
- In-tree → plugin migration is a one-way move we can do later as long as `AgentExecutor` and adapter interfaces stay clean.

Trigger to revisit: an external user requesting in-tree support for a provider Locutus doesn't intend to officially maintain. That's the signal extensibility is worth building.

## Out of scope

- Streaming responses
- Image / audio / multimodal input
- Prompt versioning / A/B routing
- Provider load balancing across regions
- OpenAI Responses API adapter (deferred; future executor)
- Coding-agent CLI executor (deferred; future executor)
- Replacing the workflow executor (`workflow-schema-and-resume.md` is independent)

## Sequencing relative to other plans

This plan should land **before** [.claude/plans/workflow-schema-and-resume.md](.claude/plans/workflow-schema-and-resume.md) — the YAML cleanup is easier on top of a stable executor abstraction. Independent of [.claude/plans/drop-triager-unified-revise.md](.claude/plans/drop-triager-unified-revise.md) (already shipped — DJ-098).

## Decision-journal entry (when implemented)

```text
DJ-099 (proposed): replace Genkit Go with in-house direct-SDK adapters
behind an agent-level AgentExecutor interface. Three adapters (Anthropic
+ Gemini + OpenAI Chat as LCD); activity-centric models.yaml routes per
council step; provider-native strict-mode schema enforcement on every
adapter fixes the DJ-098 bug class at the API layer. ~40 transitive Go
modules dropped. Existing session-trace infrastructure preserved.
Coding-agent CLI executor (Claude Code, Gemini CLI, Codex) considered
and rejected as primary architecture: workflow orchestration via the
CLI's own dispatcher doesn't generalize across CLIs; pre-auth requirement
breaks CI/CD; existing observability infrastructure would have to be
re-engineered against stream.ndjson. CLI executor remains a future
opt-in implementation behind the same AgentExecutor interface for users
who want subscription billing.
```
