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

1. **State generalisation.** Land Option A's executor changes. All existing council code keeps working under `WorkflowExecutor[*PlanningState]`. Tests prove the migration is mechanical.
2. **Justify (prototype migration).** Pick `justify --against` because it's recent in working memory, has the cleanest workflow shape, and the existing tests provide good coverage. Migrating it validates Option A against a real non-council use case.
3. **Refine cascade.** Smaller scope than supersede; good follow-up.
4. **Refine --supersede.** Larger; benefits from refine's groundwork.
5. **Import.**
6. **Adopt phases.** Last because it's the most complex orchestration — better to have the simpler verbs migrated first to refine the patterns before tackling adopt.

Each step in the migration ships independently and can be reviewed / merged separately. The goal isn't a big-bang flip; it's a steady walk.

## Tests

For each migrated verb, three classes of test:

1. **Equivalence** — the new workflow-driven implementation produces the same on-disk output as the old direct-call implementation against a fixed fixture and mocked LLM responses. Catches regressions in the cutover.
2. **Workflow-specific** — phase ordering, conditional firing, fanout filtering. Tests that exercise the workflow primitives, not the verb logic.
3. **Existing tests** — keep passing. The workflow conversion shouldn't change observable behaviour; tests at the cmd layer and integration tests should run unchanged or with minimal updates.

## Out of scope

- **Read-only verbs.** `list`, `explain`, `status`, `history` stay direct-call. No LLM = no workflow value.
- **Workflow file authoring on disk.** Per DJ-112, workflows are Go-valued. This plan doesn't reintroduce YAML.
- **Cross-workflow composition.** A workflow calling another workflow as a step is a future capability if real demand surfaces. Today: each verb is its own workflow.
- **Automatic resumability.** Resumability is the workflow-schema-and-resume plan's Phase B. This plan unifies the surface so future resumability work benefits every verb, but doesn't ship resume itself.

## DJ entry

A new DJ to settle the architecture:

**DJ-XXX: LLM-Touching Verbs Are Workflows; Read-Only Verbs Stay Direct**

Captures:

- The mix-by-accident state and the observability gap that motivated unification.
- The scope split: LLM-touching verbs go through the workflow executor; read-only verbs stay direct.
- The state-generalisation choice (Option A — generics) and why over the alternatives (interface, type-erased map).
- The migration order and the principle that each verb's migration ships as its own commit / PR for reviewability.
- Rejected alternatives: workflowize-everything (read-only verbs gain nothing), keep-the-mix (continues accumulating ceremony bottom-up), redesign-as-dataflow (too invasive a redesign for the marginal clarity gain).

References: DJ-112 (workflows in Go, not YAML — the substrate this plan extends), the workflow-schema-and-resume plan (Phase B benefits become uniform once verbs are unified).

## Open questions to settle before implementation

1. **Generics vs interface for the executor (Option A vs B).** The plan recommends A; final call before implementation.
2. **Should single-LLM-call verbs be one-step workflows, or does that cross into ceremony?** `justify` (solo) and `import` (intake without plan) are both single-call. Workflowizing them gains a phase-tag in traces but adds boilerplate. Probably workflowize for uniformity, but worth the explicit call.
3. **State naming convention.** `JustifyState`, `RefineState`, `SupersedeState`, etc. — confirm before everyone's IDE auto-complete normalises a different convention.
4. **Where do verb-specific workflow definitions live?** The council's lives in `internal/agent/workflows.go`. Per-verb workflows could co-locate (one big file) or split per verb (`internal/agent/workflows/justify.go`, etc.). Probably the latter once we have 5+ workflows; not critical day one.
