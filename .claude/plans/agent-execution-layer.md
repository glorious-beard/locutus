# Agent-Level Execution Layer

Replace Genkit Go with an in-house abstraction sized to Locutus's actual
needs. **NOT** a generic LLM-glue rewrite. The boundary lives at the
**agent level**, not at a `Generate(prompt) → response` interface.

## Why

Genkit Go gave us a fast start but we've now hit its edges three times:

1. Format handler mutates `resp.Message` in place; we wrote `captureMW`
   to snapshot before mutation (and again on error-path drops).
2. Provider-side prompt caching: Anthropic plugin doesn't accept
   `cache_control` input; Gemini caching validator rejects any request
   with a system role.
3. Tool/grounding compatibility matrix is uneven across providers, and
   Genkit hides the differences in ways that hurt rather than help
   (e.g., scout grounding is Gemini-only because Anthropic doesn't
   expose `web_search`).

We've already implicitly written ~half of the wrapper we'd need anyway:
per-model concurrency caps, per-call timeouts, multi-round tool-use
captures, session-trace recording, the schema registry, and provider
detection are all custom code on top of Genkit. Genkit is buying us
plugin init and `ai.GenerateOption`-style request building. The rest
we already own.

Surveyed alternatives:

- **LangChainGo**: covers many providers but caching is response-level
  (hash-and-cache), not provider-side prompt caching. Tool-use story
  uneven. Same gap, different API.
- **Eino (CloudWeGo)**: cleanest Go AI framework architecturally,
  smaller English-speaking community. Same provider-cache gap.
- **LiteLLM proxy + OpenAI Go SDK**: solves caching but adds a sidecar
  service (rejected for the same reasons we rejected Temporal).

None of the multi-provider Go SDKs solve our actual pain (provider-side
prompt caching, MCP tool integration, capability-aware routing) — these
are highly provider-specific and abstraction layers tend to lag.

## Design constraint: NOT a private Genkit

This is the load-bearing decision. We **do not** want to build a
generic "send prompts to any model" interface. That just rebuilds
Genkit's mistakes one layer down. Instead:

The abstraction lives at the **agent level**. The interface is "execute
this agent with this input"; the executor knows the agent's needs
(caching? tools? grounding? thinking? structured output?) and picks the
optimal provider+model combination based on:

- The agent's declared capability tier (fast / balanced / strong)
- The agent's declared needs (tools, grounding, caching, schema)
- Availability (which providers have env-var keys configured)
- User overrides (`.borg/models.yaml`)

Caller code says: "run the spec_strategy_elaborator on this cluster."
Executor code: picks Gemini or Anthropic or OpenAI based on the agent's
needs and what's configured, builds the provider-specific request with
cache markers / tool defs / structured-output schema, dispatches the
multi-round tool-use loop, captures the session trace, returns the
result.

```go
type AgentExecutor interface {
    Execute(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error)
}
```

That's the abstraction surface. Adapters live underneath, sized to do
one thing each (translate an `AgentInput` + provider context to a
provider-specific API call), and the executor's policy layer picks
which adapter to use.

## Provider matrix

Four adapters, three classes:

### Native provider SDKs

| Provider | SDK | Caching | Grounding | Thinking | Tools |
|---|---|---|---|---|---|
| **Anthropic** | `github.com/anthropics/anthropic-sdk-go` | `cache_control` markers (manual) | Not yet (no Anthropic web tool in their API as of plan time) | `thinking_config` extended thinking | `tool_use` blocks |
| **Gemini** | `google.golang.org/genai` | `CachedContents` API (manual create+reference) | `GoogleSearch` tool (mutually exclusive with custom tools) | `thinkingConfig.includeThoughts` | function calling |
| **OpenAI Responses** | `github.com/openai/openai-go` | Automatic prefix caching ≥1024 tok | Built-in `web_search_preview` tool (compatible with custom tools) | `reasoning_effort` (o-series) | function calling + built-ins |

### Lowest-common-denominator (LCD)

| Adapter | Description | Compatible providers |
|---|---|---|
| **OpenAI Chat Completions** | `github.com/openai/openai-go` against `/v1/chat/completions` with configurable base URL | OpenAI native, OpenRouter, DeepSeek, Mistral, Together, Groq, Fireworks, vLLM, Ollama, LM Studio, LocalAI, … any provider that implements the Chat Completions wire format |

The Chat Completions adapter takes `(base_url, api_key_env, model)` so
one implementation covers a long tail of providers. Capability detection
is per-provider (e.g., Ollama supports tools but not prompt caching;
DeepSeek has its own `cache_hit_tokens` reporting; Groq supports neither
caching nor reasoning models). The adapter declares what's universally
supported (chat + tools + structured output via `response_format:
json_schema`); per-provider quirks live in `.borg/models.yaml`.

This is the strategic value of the LCD adapter: **adding a new provider
becomes a config edit**, not a code change. When a customer wants to
run Locutus against a self-hosted vLLM instance or DeepSeek's API, no
new adapter is required.

## What lives where

```
internal/agent/
├── executor.go            # AgentExecutor + tier-based provider routing
├── policy.go              # picks (provider, model) given agent needs + availability
├── adapters/
│   ├── anthropic.go       # native Anthropic SDK adapter
│   ├── gemini.go          # native Gemini SDK adapter
│   ├── openai_chat.go     # Chat Completions LCD adapter
│   └── openai_responses.go # native Responses API adapter
├── caching.go             # adapter-side cache emission helpers
├── tools.go               # tool registration + per-provider tool emission
│                          # + multi-round tool-use loop + MCP client integration
├── schema.go              # structured output: emit per-provider schema enforcement
├── trace.go               # session-trace recording (already independent of Genkit)
└── concurrency.go         # per-provider concurrency caps (already independent)
```

The adapters are small because the executor handles all the
agent-level concerns (capability routing, cache prefix management,
tool dispatch, schema enforcement strategy). Each adapter is roughly:
"translate `AgentRequest` to `<sdk>.GenerateContent`, dispatch, return
`AgentResponse`." Tool-use loops live in `tools.go` once, not per
adapter — provider differences in the loop shape are encoded in
adapter-supplied tool-emission helpers.

## Locutus-shaped capabilities

The executor optimizes for the things we actually use, not generic
LLM features:

### Caching (first-class)

A single `Cacheable: true` boundary marker on a message means
"everything up to here is stable across calls in this fanout." The
executor compiles this into:

- **Anthropic adapter**: `cache_control: {type: "ephemeral"}` on the
  marked content block.
- **Gemini adapter**: `client.Caches.Create()` of the prefix on first
  call; `cached_content` reference on subsequent calls. The adapter
  hashes the prefix to dedupe cache entries within a session.
- **OpenAI Chat / Responses**: ignore the marker (automatic).

Caching is automatic for fanout calls (system prompt + scout brief +
outline are cacheable; per-item content is not). The agent .md files
don't need to know about caching; the executor handles it from the
projection structure (`Cacheable: true` is set on the stable user
message by the projection).

### Tool calling + MCP

Tools registered in two ways:

- **Native Go tools**: `tools.Register("spec_list_manifest", handler)`
  same as today. The executor passes them to whichever adapter is
  active.
- **MCP tools**: an MCP client (we already have the server side) lets
  agents use tools from any external MCP server. Configured per-agent
  via frontmatter:

  ```yaml
  ---
  id: spec_reconciler
  capability: balanced
  tools:
    - spec_list_manifest        # native Go tool
    - mcp:filesystem/read_file  # external MCP tool
  ---
  ```

  The executor proxies MCP calls during the tool-use loop transparently.
  Same tool-emission shape per provider; the adapter doesn't know
  whether a tool is native or MCP-backed.

### Structured output

Per-provider native enforcement:

- **Anthropic**: forced tool-use with the schema as a single tool.
- **Gemini**: `responseSchema` (incompatible with custom tools — the
  policy layer detects this conflict and falls back to prompt-only
  schema enforcement when `tools` are also requested, with a Warn).
- **OpenAI Chat**: `response_format: {type: "json_schema", strict:
  true}`.
- **OpenAI Responses**: same `response_format` shape, plus
  `text.format` for richer cases.

Schemas are still registered via Go reflection (`RegisterSchema(name,
exampleStruct)`). Adapters consume the registered schema in their
provider-native format.

### Grounding

- **Gemini**: `GoogleSearch` tool (mutually exclusive with custom
  tools — policy layer catches this).
- **OpenAI Responses**: `web_search_preview` built-in tool (compatible
  with custom tools).
- **Anthropic**: not supported in `anthropic-sdk-go` today; policy layer
  routes a grounding-required agent away from Anthropic.
- **Chat Completions LCD**: passthrough; depends on the underlying
  provider.

The scout agent (currently Gemini-locked because of grounding) gets
provider parity once OpenAI Responses is live.

### Extended thinking

- **Anthropic**: `thinking_config` block.
- **Gemini**: `thinkingConfig.includeThoughts: true` + budget.
- **OpenAI Chat (o-series)** / **Responses (o-series)**:
  `reasoning_effort: low|medium|high`.

Configured via `thinking_budget` in agent frontmatter, mapped per
adapter.

### Concurrency, timeouts, tracing

These live where they already do: outside the adapters, in
`concurrency.go` and `trace.go`. The migration preserves them.

## Provider+model selection policy

`policy.go` decides (provider, model) given:

```go
type AgentNeeds struct {
    Capability     string  // "fast" | "balanced" | "strong"
    Tools          bool    // any custom tools registered
    Grounding      bool    // web search needed
    Caching        bool    // cacheable prefix present
    StructuredOut  bool    // output_schema declared
    ThinkingBudget int
}

func Pick(needs AgentNeeds, availability ProviderAvailability, overrides Overrides) (provider, model string)
```

Rules (in order):

1. Honor explicit `model:` override in agent frontmatter.
2. Honor `models.yaml` per-tier override.
3. Filter providers by availability (env vars set).
4. Filter providers by capability compatibility:
   - Grounding required + Gemini available → Gemini (custom tools must
     not also be required).
   - Grounding required + tools required → OpenAI Responses (built-in
     web_search compatible with custom tools).
   - Caching required + Gemini available → Gemini (manual cache).
   - Caching required + Anthropic available → Anthropic (cache_control).
   - All other cases → tier preference order (configurable).
5. Within filtered providers, pick the model from the tier list.
6. If no provider matches, return a clear "no compatible provider for
   needs X+Y+Z" error at request build time, not at call time.

The policy is pure (no I/O), so it's testable in isolation. Adding a
new capability (e.g., "audio input") is a one-field addition to
`AgentNeeds` plus a per-adapter capability declaration.

## Migration plan

### Phase 0: design lock

Land the `AgentExecutor` interface, `AgentNeeds`, `policy.go`, and the
adapter package shape. No adapter implementations yet. Existing
`LLM` interface and `GenKitLLM` continue working unchanged behind the
scenes; the new interface lives alongside as a stub.

Deliverable: types and interfaces in place; nothing wired up.

### Phase 1: bounded prototype

Pick the **clusterer** as the prototype agent:

- Single call (no tools, no fanout interactions).
- Structured output (`LLMFindingClusters`).
- Balanced tier.
- Stable prefix (existing nodes + findings to cluster).

Implement just enough of the adapter set to run this one agent end-to-
end on Anthropic and Gemini natively, plus OpenAI Chat as LCD. Measure:

- Lines of code per adapter.
- Caching behavior on Anthropic + Gemini (capture cache hit/miss in
  trace).
- Test surface (unit + integration).
- Round-trip latency.

This is the go/no-go decision point. If the prototype is lateral
motion, we stop and stay on Genkit. If it's clearly cleaner, proceed
to phase 2.

### Phase 2: per-agent migration

Migrate agents one at a time, behind a feature flag (`LOCUTUS_USE_NEW_LLM=1`).
Order by complexity:

1. Clusterer (already migrated in phase 1)
2. Outliner, scout (single call, no tools)
3. Elaborators (fanout, structured output, caching benefit highest)
4. Critics (fanout, structured output)
5. Reconciler (tools — native + MCP integration test)

Each migration ships behind the flag; default flips to new path once
all agents are migrated and a clean winplan run is verified.

### Phase 3: remove Genkit

Delete `internal/agent/genkit.go`, drop the `firebase/genkit/go` and
`firebase/genkit/go/plugins/{anthropic,googlegenai}` deps, retain the
underlying SDKs (`anthropic-sdk-go`, `google.golang.org/genai`,
`openai-go`).

Approximate dep-tree shrinkage: ~40 transitive Go modules removed
(Genkit pulls flow runtime, dotprompt, telemetry shims, etc.).

### Tests

- Adapter unit tests: each adapter's `AgentRequest → SDK call → response`
  translation. Mocked SDK clients.
- Integration tests: one per provider, gated on env-var presence.
  Same agent definition runs end-to-end on each.
- Policy tests: `(needs, availability, overrides) → (provider, model)`
  table-driven.
- Cache assertion test: run a fanout twice in succession, assert the
  second call reports cache hit (where supported by the adapter).
- Tool-use loop test: native + MCP tool dispatch, multi-round.

## Risks

1. **Migration takes longer than estimated.** Tool-use loop semantics
   differ across providers and may take more than the budgeted 1–2
   weeks. Mitigation: bounded prototype (phase 1) is the early
   sniff-test; abandon if it smells wrong.

2. **Provider SDK churn.** Anthropic and Gemini Go SDKs are stable but
   evolving. We become responsible for upgrading. Mitigation: stable
   release versions only; don't pin to pre-releases.

3. **MCP integration complexity.** The MCP client side is new code.
   Mitigation: scope MCP tool support as a separate phase if needed;
   the executor can ship without MCP and gain it later.

4. **OpenAI Responses API is new.** Surface area is still stabilizing.
   Mitigation: ship Chat Completions LCD first; treat Responses as a
   second-priority adapter pending API stability.

## Out of scope

- **Streaming responses.** Locutus doesn't expose streaming today;
  adapters can support it later when a use case appears (e.g., live
  progress in `locutus mcp` clients).
- **Image / audio / multimodal input.** No agent uses these today.
- **Prompt versioning / A/B routing.** Single deployed prompt per
  agent; no infra for this yet.
- **Provider load balancing across regions.** Single client per
  provider.
- **Distributed-trace export (OpenTelemetry).** Local session traces
  only.
- **Replacing the workflow executor.** The phases-with-jobs YAML
  cleanup (`workflow-schema-and-resume.md`) is independent and can
  land before, after, or alongside this.

## Sequencing relative to other plans

Independent of:
- `drop-triager-unified-revise.md` (already shipped)
- `workflow-schema-and-resume.md` (YAML cleanup + resume; orthogonal)

Dependent on / blocks:
- Provider-side caching benefits land in phase 2.
- MCP tool integration depends on the executor's tool subsystem
  landing in phase 1.

## Decision-journal entry to add (when implemented)

```
DJ-099 (proposed): replace Genkit Go with an in-house agent execution
layer. The boundary lives at the agent level, not at a generic
LLM.Generate() interface. Adapters per provider (Anthropic, Gemini,
OpenAI Chat as LCD, OpenAI Responses) translate agent requests to
provider-native API calls; the executor handles capability-aware
routing, cache prefix management, tool-use loops (native + MCP),
and structured output enforcement strategy. Drops a transitive dep
tree of ~40 modules; gains provider-side prompt caching, direct
control over the multi-round tool-use loop, and MCP client integration.
The Chat Completions LCD adapter makes new providers (DeepSeek,
Mistral, vLLM, Ollama, …) a config edit rather than a code change.
```
