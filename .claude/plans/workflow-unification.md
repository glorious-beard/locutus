# Workflow Unification for LLM-Touching Verbs

The current orchestration layer is a mix-by-accident:

- **Workflows** (formal `internal/agent/workflow.go` executor + Go-valued workflow definitions per DJ-112): the spec-generation council and assimilation. Stateful step execution, conditional / fanout primitives, per-call YAML traces tagged by phase.
- **Hand-rolled direct LLM calls** for everything else: intake, justify (with and without `--against`), justify fan-out (the multi-agent split → per-decision → synthesize pipeline this morning's commits added), the cascade rewriter, the supersede agents, the approach regenerator, and adopt's phase-shaped flow.

The hand-rolled paths have accumulated bottom-up. They lack the workflow layer's observability, conditional-execution primitives, and (future) resumability hooks. This plan proposes converting the LLM-touching verbs to workflow-shaped definitions while leaving read-only verbs alone.

## Discomfort being addressed

Three concrete pain points surfaced this week:

1. **Justify fan-out shape is identical to the council's `outline → elaborate-fanout → reconcile` pattern.** I built it as direct calls (sequential splitter → per-decision loop → synthesizer) because reusing the workflow executor would have required generalising `PlanningState`, which is council-specific. Reinventing the same primitives is the smell.
2. **Provider rotation per retry was hand-rolled into `dispatchChallengerWithRetry`** (commit `2581b62`). A workflow-step-level retry policy with rotation would be reusable across every agent dispatch. Today it's a justify-only feature.
3. **No resumability for non-workflow verbs.** Phase B of the workflow-schema-and-resume plan only benefits operations that go through the workflow executor. Multi-step LLM operations on hand-rolled paths (refine cascade, fan-out justify, supersede prose-cascade) would each need bespoke resume implementations.

## Scope

**IN — convert to workflows:**

- `import` (intake → optional plan)
- `refine` (decision-target cascade)
- `refine --supersede` (replacement-emit + cascade-rewrite + prose-cascade)
- `justify` (single-target adversarial)
- `justify --against` (single-target or fan-out per parent kind)
- `adopt` Phase 0 (synthesize-missing-approaches), Phase 0b (regenerate-invalidated-approaches), and the dispatch loop's per-workstream agent invocations

**ALREADY workflows:**

- Spec-generation council (`WorkflowSpecGen`)
- Assimilation council (`WorkflowAssimilation`)

**OUT — stay direct:**

- `list`, `explain`, `status`, `history` — pure file reads + render. No LLM calls. Workflowizing them would be ceremony for no observability gain.
- `init`, `update` — scaffold operations, no LLM.

**DEFERRED — its own design pass:**

- `history --narrative` — LLM-touching but reads from on-disk events, not from a pipeline of agents. May or may not benefit from being a workflow; revisit after the main migration.

## State generalisation — the architectural lift

The workflow executor today is hard-coded against `*PlanningState`:

```go
type WorkflowStep struct {
    ID          string
    Agents      []string
    Conditional func(*PlanningState) bool
    Fanout      func(*PlanningState) ([]string, error)
    Project     func(StateSnapshot) []Message
    Merge       func(*PlanningState, []RoundResult)
}

func (e *WorkflowExecutor) ExecuteRound(ctx context.Context, step WorkflowStep, state *PlanningState) ([]RoundResult, error)
```

`PlanningState` carries council-specific fields (`ProposedSpec`, `OpenConcerns`, scout brief, raw proposal, reconciled proposal, integrity findings, etc.). Reusing this struct as the state container for justify or refine would mean either contorting the struct with fields most workflows ignore or accepting that "PlanningState" is a misleading name for a state god-object.

Three viable approaches:

### Option A — Generics on the executor

```go
type WorkflowStep[S any] struct {
    ID          string
    Agents      []string
    Conditional func(*S) bool
    Fanout      func(*S) ([]string, error)
    Project     func(StateSnapshot[S]) []Message
    Merge       func(*S, []RoundResult)
}

type Workflow[S any] struct {
    Rounds    []WorkflowStep[S]
    MaxRounds int
}

type WorkflowExecutor[S any] struct {
    Executor  AgentExecutor
    AgentDefs map[string]AgentDef
    Workflow  *Workflow[S]
    Events    chan WorkflowEvent
}
```

Each verb declares its own state type:

```go
type JustifyState struct {
    Split           *ChallengeSplit
    PerDecision     map[string]*FanOutDecisionResult
    Synthesis       *SynthesisVerdict
}

type RefineState struct {
    DecisionID    string
    Refined       bool
    Cascaded      []CascadeResult
}

type SupersedeState struct {
    OldNode       NodeRef
    NewNode       NodeRef
    Plan          *cascade.SupersedePlan
    Cascaded      cascade.CascadeRecord
    ProseCascaded []ProseCascadeResult
}
```

**Pro:** strongest type safety. Council's `PlanningState` becomes one possible state, not the only one. Step closures get typed access to the verb's state without `interface{}` casts.

**Con:** Go generics on the executor mean signature changes throughout — all the existing council step closures need to be rewritten as `WorkflowStep[*PlanningState]`. Mechanical but pervasive.

### Option B — `WorkflowState` interface

```go
type WorkflowState interface {
    StepComplete(stepID string)
    IsStepComplete(stepID string) bool
}

// PlanningState, JustifyState, RefineState all implement it
```

**Pro:** no generics churn. Adding a new state is just defining a struct that implements the interface.

**Con:** weaker type safety. Step closures would either accept `WorkflowState` (and type-assert) or accept the concrete type (and the executor still has to be generic on something to dispatch). Likely needs both: the executor takes `WorkflowState` for plumbing methods, and per-step closures use type assertions or generic helpers to pull verb-specific data.

### Option C — Type-erased state container

```go
type WorkflowState struct {
    Outputs map[string]any   // step id → output
    Errors  map[string]error
}
```

Each step's output goes into the map. Verb-specific accessors pull typed views.

**Pro:** minimal executor changes; everything fits in one struct.

**Con:** least type safety. Step authors write `state.Outputs["splitter"].(*ChallengeSplit)` everywhere. Lose the "just look at the struct field" ergonomics of typed states.

### Recommendation: Option A

The generics churn is mostly mechanical (all existing closures gain `[*PlanningState]` parameterisation), and the resulting model is clean enough to be worth the one-time refactor. Council-specific fields stay in `PlanningState`; verb-specific fields live in their own state types. The executor is type-safe per workflow.

Council code changes look like:

```go
// before
func (e *WorkflowExecutor) ExecuteRound(ctx, step, state *PlanningState) ([]RoundResult, error)

// after
func (e *WorkflowExecutor[S]) ExecuteRound(ctx, step, state *S) ([]RoundResult, error)
```

Existing call sites in `internal/agent/specgen.go`, `assimilation.go` etc. construct `WorkflowExecutor[*PlanningState]` and proceed unchanged otherwise.

## Unified agent dispatcher — the second substrate layer

The workflow executor handles per-verb orchestration (phases, fanout, conditional execution, state). It does NOT handle per-agent dispatch (tool registry, iteration cap, retry, provider rotation, structured-output parsing). Today each invocation function — `InvokeSplitter`, `InvokeSynthesizer`, `RunJustify`, `RunResearch`, `cascade.RewriteFeature`, etc. — does its own dispatch. That's the second mix-by-accident this plan should fix.

**Proposal:** every LLM operation in Locutus goes through one `AgentDispatcher`. AgentDef gets two optional fields that decide the dispatch shape:

```go
type AgentDef struct {
    // existing
    ID            string
    SystemPrompt  string
    OutputSchema  string
    Models        []ModelPreference
    Grounding     bool

    // dispatch shape
    Tools         []ToolDef  // empty = no Locutus-side tools
    MaxIterations int        // 0 or 1 = single-call; >1 = ReAct loop
}

type AgentDispatcher interface {
    Dispatch(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error)
}
```

The dispatcher branches on shape:

**Structured one-shot** (default — most agents today). Trigger: `Tools == nil && Grounding == false`. Build messages → one Generate → parse output. No loop.

**Provider-side grounding** (researcher, scout, cost_critic). Trigger: `Grounding == true`. Build messages → one Generate (the provider runs an internal tool loop with web_search) → parse output, surface `tool_calls` metadata. No Locutus-side loop; the provider's loop is opaque.

**Locutus-side ReAct** (new — for advocate, challenger, regenerator candidates). Trigger: `len(Tools) > 0 && MaxIterations > 1`. Build messages → loop { Generate → if tool_calls present, execute via the Tools registry, append results, continue → else parse final output }. Bounded by `MaxIterations`.

All three shapes share the substrate: provider rotation per attempt, retry policy, structured-output parsing, per-call YAML traces.

### Why unify

1. **One observability surface.** Per-call YAML records gain `iterations`, `tool_calls_executed`, `loop_terminated_by` fields (`omitempty` for shapes that don't use them). Operators reading session traces see one shape regardless of agent type. Today the challenger has its own retry log, the splitter and synthesizer just got theirs (commit `293624e`), the council researcher has provider-side tool_call surfacing — three places to look for "what happened during this LLM call."
2. **Promotion is cheap.** When `justify_splitter` later needs to look up parent approaches before classifying, today that's "rewrite the call site, change the invocation function, add a custom dispatch path." Unified: add one entry to the agent's `Tools` slice; the prompt updates to mention the tool. Zero call-site changes.
3. **Less special-casing.** Today's code has parallel tracks for "single LLM call with structured output" (most agents) and "single LLM call with provider grounding" (researcher). Adding ReAct would be a third. Unified: one dispatcher with three branches, each branch ~50 lines.
4. **Aligns with the workflow layer.** Workflow executor handles per-verb concerns; dispatcher handles per-agent concerns; provider adapters handle per-provider concerns. Three layers, each with one job. Clean composition.

### What it costs

The downside surface is mostly hypothetical when the design has a fast path:

- **Ceremony in AgentDef.** Adding `Tools` and `MaxIterations` fields. Both zero-valued for the common case (`MaxIterations: 0` defaults to single-call; `Tools: nil` means no Locutus-side tools). Splitter / advocate / refiner .md scaffolds stay as terse as today.
- **Trace-shape noise.** New `iterations`, `tool_calls_executed` fields appear in YAML. Use `omitempty` so structured-one-shot traces look identical to today's.
- **Mental shift on tool capability.** "All agents are tool-capable, they just don't list any" vs today's "tool-using agents are special." Frame as: agents declare needs; the dispatcher exposes only what's listed. The mental model stays "no tools listed = no tool capabilities."
- **Three dispatch branches.** The dispatcher's three modes (structured, grounded, ReAct) share infrastructure but differ in core loop. Don't try to over-unify the loops themselves — three branches in one function is fine; one true loop handling all three would be a mess.

### Layer separation

```
WorkflowExecutor[S]
   ↓ (workflow knows: phases, fanout, conditional, state accumulation)
   ↓
AgentDispatcher
   ↓ (dispatcher knows: tool registry, iteration cap, retry, rotation, structured output, observability)
   ↓
ProviderAdapter (anthropic.go / gemini.go / openai_responses.go from DJ-099)
   ↓ (adapter knows: provider SDK call, strict-mode schema, cache_control, response normalisation)
```

Each layer has one concern. Adding ReAct doesn't disrupt workflows or providers. Adding workflow phases doesn't disrupt agents or providers. Adding a new provider doesn't disrupt agents or workflows. The substrate becomes Locutus's own version of what frameworks like Eino expose — built in-house, fits DJ-099's direct-SDK model, layered cleanly.

### Tool registry shape

For the ReAct case, the dispatcher needs a way to register Go-side tools and let the model invoke them by name:

```go
type ToolDef struct {
    Name        string
    Description string
    InputSchema string  // JSON schema
    Handler     func(ctx context.Context, input []byte) ([]byte, error)
}
```

Agents declare which tools they need; the dispatcher exposes them to the provider as tool definitions, executes the handlers when the model emits tool_calls, threads results back as messages, and continues the loop. This is the pattern Eino's `flow/agent/react/react.go` uses; we adapt it without depending on Eino.

Tool implementations live in Locutus packages (`internal/spec/tools.go`, etc.) and are registered with the dispatcher at start-up. A typical tool for a future agent might be `read_node(id)` returning the rendered explain output, or `query_decisions_by_topic(query)` returning matching decision IDs.

### Migration impact on the per-verb shapes below

The per-verb workflow sketches in the next section assume agents go through the dispatcher. A workflow phase that lists `agent: spec_advocate` means "dispatcher.Dispatch(ctx, advocate_def, input)" — not a hand-rolled invocation function. The existing `InvokeSplitter`, `InvokeSynthesizer`, `RunJustify`, etc. either delete (replaced by direct dispatcher calls from the workflow) or shrink to thin convenience wrappers around the dispatcher.

## OpenTelemetry instrumentation

Per-LLM-call YAMLs at `.locutus/sessions/<sid>/calls/<NNNN>-*.yaml` have repeatedly caught degenerate generation and malformed prompts during council and justify work. They're richer than the OTel `gen_ai.*` semantic conventions strictly require (full reasoning, raw_message blob, multi-round captures, citation arrays). **They stay** as the per-call leaf observability.

What's missing is structure *above* the leaf — phase boundaries, fanout shape, retry/rotation, and (once ReAct lands) iteration loops. Today operators reconstruct the workflow shape mentally by reading sequential YAML files. OTel adds the structural layer the YAMLs already assume.

### What OTel adds that session YAMLs don't

| Concern                              | Today                                  | With OTel                       |
|--------------------------------------|----------------------------------------|---------------------------------|
| Workflow phase boundaries            | implicit in `agent_id` ordering        | `workflow.phase` span           |
| Fanout structure                     | `call_tag` suffix on filenames         | parent/child span hierarchy     |
| Retry + rotation                     | repeated YAMLs same `agent_id`         | one child span per attempt      |
| ReAct iterations                     | not yet captured                       | one child span per iteration    |
| Locutus-side tool execution          | not yet captured                       | per-tool `tool.invoke` span     |
| Cross-call timeline / critical path  | manual reconstruction                  | trace visualisation             |
| Token-spend aggregation per phase    | sum YAMLs by `agent_id` prefix         | aggregate over span attributes  |

### Span hierarchy

```
locutus.verb (root)
  attrs: locutus.verb=justify, locutus.node.id=dec-foo, locutus.session.id=<sid>
  │
  ├── workflow.phase: classify
  │     └── agent.dispatch: justify_splitter
  │           └── llm.attempt #1
  │                 └── provider.generate           ← gen_ai.* leaf
  │
  ├── workflow.phase: per_decision (fanout)
  │     ├── agent.dispatch: spec_challenger (dec-a)
  │     │     ├── llm.attempt #1 (anthropic, degenerate)
  │     │     ├── llm.attempt #2 (rotated → googleai)
  │     │     └── llm.attempt #3 (rotated → openai, success)
  │     ├── agent.dispatch: justify_researcher (dec-a)
  │     │     └── llm.attempt #1 (grounded; provider tool_calls in span events)
  │     └── agent.dispatch: spec_advocate (dec-a)
  │
  └── workflow.phase: synthesize
        └── agent.dispatch: justify_synthesizer
```

ReAct adds one layer between `agent.dispatch` and `llm.attempt`:

```
agent.dispatch: approach_regenerator
  ├── react.iteration #1
  │     ├── llm.attempt → provider.generate
  │     └── tool.invoke: read_node(strat-foo)
  ├── react.iteration #2
  │     ├── llm.attempt → provider.generate
  │     └── tool.invoke: query_decisions_by_topic("auth")
  └── react.iteration #3 (terminal)
        └── llm.attempt → provider.generate
```

### Exporters

**Default: file exporter to `.locutus/sessions/<sid>/trace.jsonl`** — OTLP-JSON, one span per line, written by the OTel SDK pointed at the existing session directory. Always-on. Sits alongside the per-call YAMLs so the trace is preserved next to the artifacts that produced it. Replayable: `cat trace.jsonl | otel-cli replay` (or equivalent) loads into Jaeger / Tempo / Grafana for visualisation. The user has called out the existing per-call YAMLs as "invaluable" for debugging degenerate generation; the file exporter extends that property to workflow-shaped data.

**Optional: OTLP HTTP exporter** activated when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. Both exporters compose via `MultiSpanProcessor` — operators running a local collector get live spans without losing the file artifact.

### Cross-reference with session YAMLs

The session manifest grows a `trace_id` field; the leaf `recordedCall` gains a `span_id` field referencing the matching `provider.generate` span. A reader holding either ID can find the other:

```yaml
# session.yaml
session_id: 20260510-1430-12-a3f9c2
trace_id: 4bf92f3577b34da6a3ce929d0e0e4736
started_at: 2026-05-10T14:30:12Z
```

```yaml
# calls/0017-spec_advocate-dec-a.yaml
index: 17
agent_id: spec_advocate
span_id: 00f067aa0ba902b7
model: claude-sonnet-4-6
...
```

Both fields are `omitempty` so traces produced by code paths that don't initialise the SDK (most unit tests) stay byte-identical to today's fixtures.

### Attribute conventions

Provider-level spans carry the OTel `gen_ai.*` semantic conventions: `gen_ai.system`, `gen_ai.request.model`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`, `gen_ai.usage.cache_read_input_tokens`, `gen_ai.usage.cache_creation_input_tokens`, `gen_ai.response.id`, `gen_ai.operation.name`. Locutus-specific attributes live under `locutus.*`: `locutus.verb`, `locutus.workflow.phase`, `locutus.agent.id`, `locutus.attempt`, `locutus.degenerate.reason`, `locutus.tool.name`. Splitting the namespace keeps the trace replayable through any OTel-aware tool while letting Locutus dashboards filter on `locutus.*`.

### Where instrumentation lives

In the substrate, not in per-verb handlers:

- `WorkflowExecutor[S].ExecuteRound` → `workflow.phase` span
- `AgentDispatcher.Dispatch` → `agent.dispatch` span, per-attempt `llm.attempt` children, and (in ReAct mode) `react.iteration` + `tool.invoke` spans
- `ProviderAdapter.Generate` → `provider.generate` leaf with `gen_ai.*` attributes

Verb handlers thread `ctx` through (already standard) and the spans materialise. No verb-level OTel awareness needed. Sampling is always-on — each `locutus <verb>` is one bounded trace; CLI volume doesn't justify head/tail sampling.

### Cost

- ~150 LOC SDK setup (TracerProvider construction, dual exporters, shutdown wiring).
- ~100 LOC instrumentation calls across the three substrate layers.
- Direct dependencies promoted from indirect (`go.opentelemetry.io/otel`) plus three SDK packages added (`sdk/trace`, `exporters/otlp/otlptrace/otlptracehttp`, `exporters/stdout/stdouttrace`).
- File-write overhead is sub-millisecond per span; LLM call latency dominates.

## Per-verb workflow shapes

Each migrated verb's workflow is sketched below. The actual closures will live alongside the existing handlers in their respective package files (or move into `internal/agent/workflows.go` as the council workflows do).

### `justify --against` (with fan-out)

```
Phases:
  1. classify (single)        — agent: justify_splitter
                                conditional: parent has decisions
  2. per_decision (fanout)    — agent: spec_challenger / justify_researcher / spec_advocate
                                fanout: split.DecisionShards (filtered to non-empty)
                                each item runs the existing 3-step adversarial sub-flow
  3. synthesize (single)      — agent: justify_synthesizer
                                conditional: per_decision phase produced results
                                            OR parent_prose_shard non-empty

Fallback (parent has no decisions):
  Phases:
    1. challenge (single)     — agent: spec_challenger
    2. research (single)      — agent: justify_researcher
    3. defend (single)        — agent: spec_advocate
```

The fan-out and fallback could be one workflow with conditional phases, or two workflows selected by `parentDecisionIDs(...) == 0` at the cmd layer. Two workflows are simpler.

### `justify` (solo defense)

```
Phases:
  1. defend (single)          — agent: spec_advocate
```

Trivially a one-step workflow. Borderline case for "is this worth workflowizing" — but uniformity wins.

### `refine` (decision target, cascade)

```
Phases:
  1. cascade (fanout)         — agent: refiner
                                fanout: features + strategies referencing the decision
                                each item gets a prose-rewrite pass
  2. record_history (single)  — no LLM; merge step that emits the cascade events
```

### `refine --supersede`

```
Phases:
  1. emit_replacement (single)         — agent: refiner-supersede-<kind>
  2. compute_cascade (single)          — no LLM; cascade.ComputeSupersedePlan
  3. apply_cascade (single)            — no LLM; cascade.ApplySupersede{Decision,Feature,Strategy}
  4. prose_cascade (fanout)            — agent: refiner
                                          fanout: plan.FeaturesToRewrite + StrategiesToRewrite + BugsToRewrite
                                          conditional: !plan.InPlace
```

### `import`

```
Phases:
  1. intake (single)                   — agent: intake
  2. plan (single)                     — agent: planner
                                          conditional: not skip_plan and intake admitted the doc
```

### `adopt` (Phase 0 + 0b + dispatch)

```
Phases:
  1. synthesize_missing (fanout)       — agent: synthesizer
                                          fanout: parents lacking approaches
  2. regenerate_invalidated (fanout)   — agent: approach-regenerator
                                          fanout: approaches with InvalidatedByEventID set
  3. classify (single)                 — no LLM; reconcile.Classify
  4. preflight (fanout)                — no LLM; per-prereq checks
  5. dispatch (fanout)                 — agent: variable per workstream
                                          fanout: workstreams from the master plan
```

Adopt's full reconcile loop is more complex than the others; this is the first-pass shape. Refinement may surface additional phases.

## Migration order

1. **State generalisation (Option A).** Land the executor's generic parameterisation. All existing council code keeps working under `WorkflowExecutor[*PlanningState]`. Tests prove the migration is mechanical.
2. **Agent dispatcher substrate.** Land `AgentDispatcher` with the two existing dispatch shapes (structured one-shot, provider-side grounding) wired up. Existing `InvokeSplitter`, `InvokeSynthesizer`, `RunJustify`, `RunResearch`, `cascade.RewriteFeature`, etc. shrink to thin wrappers around the dispatcher. Verifies the substrate against existing agent uses before any verb migration.
3. **OpenTelemetry instrumentation.** Wire the OTel SDK into the three substrate layers: workflow executor opens `workflow.phase` spans, dispatcher opens `agent.dispatch` and per-attempt `llm.attempt` spans, adapters open `provider.generate` spans with `gen_ai.*` attributes. File exporter writes OTLP-JSON to `.locutus/sessions/<sid>/trace.jsonl` alongside the per-call YAMLs. OTLP HTTP exporter activates on `OTEL_EXPORTER_OTLP_ENDPOINT`. ReAct branch (next phase) inherits the instrumentation; its iteration / tool-invoke spans drop in for free as part of the new dispatch shape.
4. **ReAct branch on the dispatcher.** Add the third dispatch shape (Locutus-side tool-use loop) plus the tool registry. No agent uses it yet; the branch is dormant until step 6+. Tests via mock_llm scripting tool_calls / non-tool_calls outputs.
5. **Justify (prototype verb migration).** Pick `justify --against` because it's recent in working memory, has the cleanest workflow shape, and existing tests provide good coverage. Workflow definition uses the dispatcher; per-decision steps still call structured-one-shot agents. Validates Option A against a real non-council use case.
6. **First ReAct adoption.** Pick one agent that genuinely benefits — `approach-regenerator` is the strongest candidate (multi-step planning over prior artifacts). Move it to ReAct mode; verify quality lift vs. structured one-shot on real fixtures.
7. **Refine cascade.** Smaller scope than supersede; good follow-up.
8. **Refine --supersede.** Larger; benefits from refine's groundwork.
9. **Import.**
10. **Adopt phases.** Last because it's the most complex orchestration — better to have the simpler verbs migrated first to refine the patterns before tackling adopt.

Each step ships independently and can be reviewed / merged separately. The goal isn't a big-bang flip; it's a steady walk. Steps 1–4 build the substrate without changing observable behaviour; steps 5+ migrate one verb / agent at a time.

## Tests

For each migrated verb, four classes of test:

1. **Equivalence** — the new workflow-driven implementation produces the same on-disk output as the old direct-call implementation against a fixed fixture and mocked LLM responses. Catches regressions in the cutover.
2. **Workflow-specific** — phase ordering, conditional firing, fanout filtering. Tests that exercise the workflow primitives, not the verb logic.
3. **Existing tests** — keep passing. The workflow conversion shouldn't change observable behaviour; tests at the cmd layer and integration tests should run unchanged or with minimal updates.
4. **Trace shape** — for at least one fixture per verb, the OTel instrumentation produces the documented span hierarchy. Tests assert on span names + parent links + key attributes (`locutus.workflow.phase`, `locutus.agent.id`, `gen_ai.system`), not on timing. The file exporter writes to a temp directory so test fixtures can read back `trace.jsonl` and verify shape. Most unit tests run with the global no-op tracer (instrumentation is free) and don't need updates; only the verb-level integration tests opt into a real TracerProvider.

## Out of scope

- **Read-only verbs.** `list`, `explain`, `status`, `history` stay direct-call. No LLM = no workflow value.
- **Workflow file authoring on disk.** Per DJ-112, workflows are Go-valued. This plan doesn't reintroduce YAML.
- **Cross-workflow composition.** A workflow calling another workflow as a step is a future capability if real demand surfaces. Today: each verb is its own workflow.
- **Automatic resumability.** Resumability is the workflow-schema-and-resume plan's Phase B. This plan unifies the surface so future resumability work benefits every verb, but doesn't ship resume itself.

## DJ entry

A new DJ to settle the architecture.

Title: **LLM-Touching Verbs Are Workflows; Agents Go Through One Dispatcher; Read-Only Verbs Stay Direct.**

Captures:

- The two mix-by-accident states this plan addresses: orchestration (workflows for council, hand-rolled for everything else) and dispatch (each invocation function does its own retry / rotation / structured-output handling).
- The three-layer substrate: `WorkflowExecutor[S]` → `AgentDispatcher` → `ProviderAdapter`. Each layer has one concern.
- The state-generalisation choice (Option A — generics) and why over the alternatives (interface, type-erased map).
- The unified-agent-dispatcher choice and the three dispatch shapes it supports (structured one-shot, provider-side grounding, Locutus-side ReAct), with a fast-path branch so structured one-shot agents pay no ceremony tax.
- The observability split: per-LLM-call YAMLs at `.locutus/sessions/<sid>/calls/` stay as the leaf trace (richer than `gen_ai.*` semconv requires — full reasoning, raw_message, multi-round captures); OTel instrumentation in the substrate adds workflow / dispatcher / retry / ReAct structure above the leaf. File exporter writes OTLP-JSON to `.locutus/sessions/<sid>/trace.jsonl` (always-on); OTLP HTTP exporter activates on `OTEL_EXPORTER_OTLP_ENDPOINT`. Spans use `gen_ai.*` for provider-level attributes and `locutus.*` for verb / phase / attempt metadata.
- The scope split: LLM-touching verbs go through the workflow executor; read-only verbs stay direct.
- Why the substrate is built in-house rather than adopting Eino or another framework: the substrate is small (workflow executor + agent dispatcher + provider adapters ~ a few thousand LOC total), DJ-099's direct-SDK adapters stay load-bearing, and the lag risk of any external framework on cutting-edge provider features is real (Eino-ext lagged on Anthropic adaptive thinking; same shape would bite Locutus on every future provider feature). Eino's [`flow/agent/react/react.go`](https://github.com/cloudwego/eino/blob/main/flow/agent/react/react.go) is the reference implementation we adapt patterns from without taking the dependency.
- The migration order and the principle that each step ships as its own commit / PR for reviewability.
- Rejected alternatives: workflowize-everything (read-only verbs gain nothing), keep-the-mix (continues accumulating ceremony bottom-up), redesign-as-dataflow (too invasive for the marginal clarity gain), adopt Eino as a runtime dependency (re-introduces the same framework-coupling concerns DJ-099 set out to solve).

References: DJ-099 (direct-SDK adapters — the substrate the dispatcher sits on top of), DJ-112 (workflows in Go, not YAML — the substrate this plan extends), the workflow-schema-and-resume plan (Phase B benefits become uniform once verbs are unified).

## Resolved questions

Settled before implementation began. Recorded here so subagents executing
later phases inherit the same conventions.

### Phase 1 (state generalisation)

- **Generics vs interface for the executor.** Option A (generics) — landed
  in commit `eaaaf62`. `WorkflowStep[S]`, `Workflow[S]`,
  `WorkflowExecutor[S]`, `StateSnapshot[S]`. Council uses
  `WorkflowExecutor[PlanningState]`.

### Phase 3 (OTel instrumentation)

- **OTel file exporter format.** Custom OTLP-JSON span processor
  (~30 LOC). The user-facing promise is "replay through Jaeger / Tempo /
  `otel-cli`," and that requires canonical OTLP-JSON. `stdouttrace` is a
  debugging exporter, not a wire-format one — diverging shapes would
  break the replay story.
- **Trace ID derivation.** Generate a W3C trace ID independently. Surface
  both `trace_id` and `session_id` in `session.yaml`. Session ID
  (~22 chars) doesn't have 128 bits of entropy, so deriving from it
  would either lose entropy or pad — neither is clean.
- **Span processor.** `SimpleSpanProcessor` (synchronous flush on
  `OnEnd`). Matches per-call YAMLs' SIGKILL-survivability for completed
  spans. Open spans on a SIGKILL are lost; the YAMLs stay authoritative
  for crash analysis (each `recordedCall.Begin` flushes input before the
  adapter runs, so completed calls survive).
- **No-op tracer in tests.** Confirmed per plan. `otel.Tracer(...)`
  returns a no-op when the SDK isn't initialised; existing test fixtures
  need no changes. Only the verb-level trace-shape tests (Tests class 4)
  initialise a real TracerProvider scoped to a temp dir.

### Phase 4 (ReAct branch)

- **Tool registry scope.** Drop the per-agent allowlist entirely until
  Locutus supports externally-defined tools. Today every tool is an
  internal Locutus-defined read-only spec lookup; `AgentDef.Tools` is
  documentation that loosely matches reality, not an enforcement
  surface. Counter-stance: expose every registered tool to every agent;
  the model picks what to call. When external tools (with side
  effects) eventually land, design a richer capability model then —
  the per-name allowlist isn't expressive enough for that case anyway.
  Implementation: remove `AgentDef.Tools`, drop the `tools:` block
  from `spec_reconciler.md`, change `buildAdapterRequest` to expose
  every entry in the global `ToolRegistry`.
- **Tool-call observability shape.** Extend `recordedCall.Rounds`. Each
  ReAct iteration becomes one `GenerateRound`. Tool invocations during
  ReAct go in the round that emitted them — same shape as the
  provider-side multi-round captures already use. Avoids a parallel
  `ReActSteps` field that duplicates the structure.
- **AgentDef changes.** Add `MaxIterations int \`yaml:"max_iterations,omitempty"\``
  (zero default = single call). No back-compat shims; the user runs
  `locutus update --offline --reset` before every operation, so
  AgentDef shape can evolve freely until Locutus reaches self-hosting.
  ReAct trigger: `MaxIterations > 1` is the sole signal — Tools field
  doesn't exist any more (see prior bullet).

### Phase 5+ (verb migrations)

- **Workflowize single-call verbs.** Yes. `justify` (solo) and `import`
  (intake without plan) become one-step workflows. Cost: ~10 LOC per
  verb. Gain: every verb gets a `workflow.phase` span uniformly, the
  codebase shape is consistent, and a single-call verb that later
  grows phases migrates trivially.
- **State naming.** `<Verb>State` — `JustifyState`, `RefineState`,
  `SupersedeState`, `ImportState`, `AdoptState`. Mirrors existing
  `PlanningState`. All in `internal/agent`; no package-level
  disambiguation needed.
- **Where workflow definitions live.** Per-verb files in
  `internal/agent/workflow_<verb>.go`. To keep the convention
  uniform, the existing council workflows split into
  `workflow_planning.go`, `workflow_assimilation.go`,
  `workflow_spec_generation.go` (replacing the consolidated
  `workflows.go`). Cross-workflow shared closures
    (`projectDefault`, `mergeNoop`, `firstNonEmpty`,
    `marshalFanoutItems`) move to `workflow_helpers.go`. Each
    `workflow_<verb>.go` carries the state struct (when
    verb-specific), the `Workflow` declaration, and verb-specific
    fanout / conditional / merge closures. Sub-packaging
    (`internal/agent/workflows/...`) was rejected — it would create
    import cycles with shared agent types and isn't worth the
    structure for ~8 files.
