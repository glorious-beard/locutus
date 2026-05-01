# Plan: Council Resilience — Make Spec Generation Model-Agnostic

## Context

Locutus is an open-source tool. End users will plug in whatever model they have credit for — Gemini Flash, Claude Sonnet, Llama variants, GPT-class models. The council's job is to produce a referentially-clean `SpecProposal` from GOALS.md regardless of which model is wired up. Today it can't.

A real run on `winplan` (GOALS.md ≈ 8 lines) with `googleai/gemini-3.1-pro-preview` failed three Pro Preview calls in a row to produce a complete proposal even when given mechanical, prescriptive instructions naming the exact missing IDs. The integrity-revise loop (`5a42eb2`) caught the failure and surfaced a hard error rather than silently stripping refs (`fix(council): hard fail on dangling refs instead of silent strip`). That's the right behaviour, but the underlying problem is structural.

The architect is asked to emit a single 2k-token JSON blob containing features, decisions, and strategies, with cross-array referential integrity that JSON Schema cannot enforce. For a non-trivial GOALS.md that's ~20 cross-references the model has to track via attention alone while also generating prose. Stronger models (Opus, GPT-class) tolerate this; weaker models drop refs.

A post-Phase-1 winplan run on `googleai/gemini-3-flash-preview` confirmed this empirically: 23 dangling references after the integrity gate exhausted its retry budget, split roughly half between `feature.decisions[]` (10) and `strategy.decisions[]` (13). Phase 1 dropped approaches; the remaining cross-reference juggling between `features[]`/`strategies[]` and a separate top-level `decisions[]` array still defeats Flash-class models.

## Goal

Make the council produce a clean `SpecProposal` reliably across the model spectrum, without silently degrading the output and without forcing users onto the most expensive models.

Success looks like:

- Gemini 3 Flash and Claude Haiku produce clean proposals on a winplan-scale project.
- Total token cost per successful run drops, not rises.
- The integrity gate added in `5a42eb2` becomes a backstop that rarely fires, not the primary correctness mechanism.
- Approaches are produced from real code context, not invented during refine.

## Scope

In scope:

- Removing approaches from `SpecProposal` and the refine-time output. **(Phase 1, shipped.)**
- Moving approach synthesis to adopt's existing per-parent path. **(Phase 1, shipped.)**
- Eliminating cross-array referential integrity by emitting decisions inline under their parent and reconciling post-hoc.
- Decomposing the architect call into per-node fanout when inline-decisions alone isn't enough on big projects.
- Wiring tool-mediated construction so integrity is enforced at registration time, not by post-validation.
- Sharpening the in-workflow critic prompts so issues caught early don't need post-hoc rescue.

Out of scope:

- Changing the on-disk shape under `.borg/spec/` (DJ-085 stability).
- Replacing the council pattern (survey → propose → critique → revise stays).
- Streaming the LLM responses (separate work; deferred per earlier discussion).

## Architectural shift

Each phase reduces the cognitive load on the architect and pushes correctness from "model attention" to "code state." Phases ship independently and the user can stop at any point if the early phases solve the problem at acceptable cost.

```text
Phase 1: Drop approaches from SpecProposal       → smallest load drop, biggest conceptual fix
Phase 2: Inline decisions + reconciler           → eliminates dangling-ref class entirely
Phase 3: Outline + per-node elaborate (fanout)   → escalation when Phase 2's single architect
                                                   call still degrades on big projects
Phase 4: Tool-mediated incremental construction  → optional polish if Phase 1–3 isn't enough
Phase 5: Critic sharpening                       → defense in depth, cheap, independent
```

Phase 1 has shipped. Phases 1 and 2 together are expected to be sufficient for most projects. Phase 3 is the structural escalation if Phase 2's single architect call still degrades on weak models. Phase 4 is the long-term answer for full model-agnosticism. Phase 5 is independent of the others and worth landing whenever.

---

## Phase 1: Approaches out of refine, into adopt — SHIPPED

Recorded as DJ-087. The premise from CLAUDE.md is that approaches are "the synthesis layer for coding agents" — implementation sketches that bridge spec and code. They need code context. During refine that context doesn't exist; the architect has to invent the sketch, and those invented sketches drove a substantial fraction of the dangling-ref problem (every approach needed a `parent_id`, every feature/strategy carried an `approaches[]` cross-ref array).

What landed:

- `SpecProposal.Approaches`, `FeatureProposal.Approaches`, `StrategyProposal.Approaches`, and `ApproachProposal` removed from [internal/agent/specgen.go](internal/agent/specgen.go).
- `SpecProposal.Validate` and `SpecProposal.Strip` now check decisions only.
- The integrity-revise prompt and the architect's contract dropped every reference to approaches.
- Adopt synthesizes one approach per in-scope feature/strategy that arrives with none, persisting `app-<parent-id>.md` and updating the parent (`cmd/adopt_synthesize.go`).
- `--dry-run` runs the synthesizer through the read-only FS wrapper.

The post-Phase-1 winplan run on Gemini Flash showed approaches gone (the 0/10 historical count is confirmed) but ~23 remaining dangling refs in the decisions cross-references. Phase 2 targets that.

---

## Phase 2: Inline decisions + reconciler

The remaining failure mode is that the architect emits cross-references between separate top-level arrays — `features[].decisions[]` and `strategies[].decisions[]` against a global `decisions[]` array — and weaker models drop those refs while juggling prose.

The structural fix is to remove the cross-references from the architect's output entirely. Each feature and strategy carries its decisions **inline** as embedded objects with no IDs. There is no top-level `decisions[]` array in the architect's output. A reconciler step then clusters all inline decisions across the proposal, dedupes/resolves conflicts/keeps separate as appropriate, and assigns canonical IDs that the persistence layer expects.

This was prompted by an observation that decisions-first decomposition is a chicken-and-egg problem: the architect can't know which decisions to make until it knows what features and strategies need them. Inline decisions invert this — the architect emits each parent with the decisions it locally requires; the reconciler's job is the cross-cutting view.

### Why this beats fanout-first

The plan's prior Phase 2 (now Phase 3) decomposed the architect call into per-node elaborations to bound per-call load. The observation is that the load isn't raw token count; it's the cross-reference juggling. Removing the juggling without splitting the call delivers most of the benefit at a fraction of the surgery:

- **Eliminates the dangling-ref class entirely by construction.** No cross-references in the architect's output → nothing can dangle. The integrity gate becomes vestigial.
- **Architect prompt collapses.** Half of `spec_architect.md`'s mandates are about referential integrity ("every id you reference MUST appear..."). With inline decisions, that paragraph deletes; the architect describes each parent with its committed decisions and citations.
- **Cognitive load drops without fanout.** The architect doesn't need stable IDs, doesn't need to coordinate across separate arrays, doesn't need to keep a registry in attention.
- **Fanout becomes a clean, separable escalation.** If inline-on-Flash still fails on big projects, Phase 3 layers on top: split the existing architect call into per-node calls. The reconciler doesn't change.
- **Single-call architect preserves opinion.** Per-node fanout risks each elaborator picking different defaults for cross-cutting concerns (one feature picks Postgres, another picks ClickHouse). One architect emitting the whole shape with inline decisions keeps a coherent voice; the reconciler only intervenes when the architect has duplicated or contradicted itself.

### The flow

```text
survey         (1 LLM call, exists)
  ↓
propose        (1 LLM call, modified)   — architect emits features and strategies, each with
                                          inline decisions (no IDs, no cross-refs)
  ↓
reconcile      (1 LLM call + 1 Go fn,   — clusters all inline decisions across the proposal;
                NEW)                      verdict actions: dedupe, resolve_conflict,
                                          keep_separate, reuse_existing. Go assigns canonical
                                          IDs and rewrites parents to reference them.
  ↓
cascade        (0–M LLM calls,          — when reconcile flipped a decision (resolve_conflict
                NEW, only on conflicts)   with non-trivial loser), re-rewrites the affected
                                          feature/strategy prose under the winning decision.
                                          Reuses cascade.RewriteFeature / cascade.RewriteStrategy.
  ↓
critique       (4 LLM calls, exists)    — sees the reconciled, ref-clean proposal
  ↓
revise         (0–1 LLM calls, exists)
```

After Phase 2, the integrity-revise loop from `5a42eb2` becomes a pure backstop. It should not fire on any clean propose+reconcile run because there are no cross-refs to dangle.

### Settled design questions

These were debated and locked during plan iteration; recording the answers here so implementation doesn't re-litigate.

- **No IDs in the architect's output.** Architects can't fabricate IDs because IDs are reconciler-assigned. The architect describes each decision by title + rationale + alternatives + citations. The reconciler clusters by content and mints `dec-<slug>` IDs from the canonical title.
- **Existing-spec snapshot is reconciler-aware.** When extending a spec, the reconciler also matches new clusters against `Existing.Decisions`. The verdict action `reuse_existing: <existing-id>` says "this cluster maps to an existing decision; reuse its ID instead of minting a new one." Pass `Existing` to the reconciler in the same shape the architect sees today.
- **Three categories of overlap, only two get reconciled.**
  - **Identical** (same conclusion + similar reasoning) → dedupe.
  - **Conflicting** (incompatible answers to the same question) → pick winner + cascade rewrite.
  - **Compatible** (different aspects of the same topic) → keep separate. The approach synthesizer at adopt time integrates them when planning the implementation; spec-time merging would be lossy and premature.
- **Conflict resolution priority:** best-practice → ecosystem popularity. Reconciler applies in that order with documented rationale.
- **Loser disposition:** when a conflict has a winner, the loser is recorded in the winner's `alternatives[]` with `rejected_because` populated by the reconciler's reasoning. DJ-085 provenance preserved. Affected nodes get cascade-rewritten under the winner.
- **Reconciler context.** The reconciler sees the *full* decision objects (title, rationale, architect_rationale, citations, confidence, alternatives) for every inline decision in the proposal, plus the existing-spec decision snapshot when present. Picking a winner without architect_rationale and citations would produce arbitrary picks; we want good picks.
- **No mechanical dedupe pre-pass.** Title fuzzy-matching, embeddings with similarity thresholds, and pairwise N² comparisons are all fragile. One LLM call clusters and judges all decisions in a single pass; one Go function applies the verdict mechanically. All judgment in the LLM, all surgery in code.
- **`InfluencedBy` is dropped from the architect's contract.** Today's `DecisionProposal.InfluencedBy` carries inter-decision references that re-introduce the cross-reference problem. Greenfield generation doesn't need it; influence relationships can be added during refine if they matter. Removing `InfluencedBy` from the architect output is a clean contract simplification, not a regression.
- **Cascade-rewrite error handling.** Cascade rewrites use `GenerateWithRetry` and the existing challenge-protocol for malformed JSON. Transient errors retry; hard errors bubble up uniformly. There's no new "partial rewrite" failure mode to design around.

### Part 2.1: Type rework

2.1a. Introduce `RawSpecProposal` in [internal/agent/specgen.go](internal/agent/specgen.go) (or a new `internal/agent/raw_proposal.go` if specgen.go gets too dense). Shape:

```go
type RawSpecProposal struct {
    Features   []RawFeatureProposal  `json:"features,omitempty"`
    Strategies []RawStrategyProposal `json:"strategies,omitempty"`
}

type RawFeatureProposal struct {
    ID                 string                  `json:"id"`
    Title              string                  `json:"title"`
    Description        string                  `json:"description"`
    AcceptanceCriteria []string                `json:"acceptance_criteria,omitempty"`
    Decisions          []InlineDecisionProposal `json:"decisions,omitempty"`
}

type RawStrategyProposal struct {
    ID        string                   `json:"id"`
    Title     string                   `json:"title"`
    Kind      string                   `json:"kind"`
    Body      string                   `json:"body"`
    Decisions []InlineDecisionProposal `json:"decisions,omitempty"`
}

// InlineDecisionProposal mirrors DecisionProposal but with no ID and no
// InfluencedBy. The reconciler assigns IDs.
type InlineDecisionProposal struct {
    Title              string             `json:"title"`
    Rationale          string             `json:"rationale"`
    Confidence         float64            `json:"confidence"`
    Alternatives       []spec.Alternative `json:"alternatives,omitempty"`
    Citations          []spec.Citation    `json:"citations,omitempty"`
    ArchitectRationale string             `json:"architect_rationale,omitempty"`
}
```

2.1b. Register `RawSpecProposal` in [internal/agent/schemas.go](internal/agent/schemas.go) with the same example-driven schema injection used today.

2.1c. The existing `SpecProposal` type stays — it's the *output* of the reconciler. `ToAssimilationResult` already operates on `SpecProposal`; no change there.

### Part 2.2: Reconciler agent + Go surgery

2.2a. New agent `spec_reconciler` ([internal/scaffold/agents/spec_reconciler.md](internal/scaffold/agents/spec_reconciler.md)) — `capability: balanced`, `temperature: 0.2`, output schema `ReconciliationVerdict`.

Prompt: cluster all inline decisions across the proposal (and the existing-spec snapshot, when present); emit a structured action list with four action kinds:

- `dedupe` — identical decisions → pick canonical title/body, list source parents.
- `resolve_conflict` — incompatible decisions on same question → pick winner with `rejected_because` for the loser; list source parents.
- `keep_separate` — different aspects of the same topic → both survive; list source parents.
- `reuse_existing` — cluster maps to an existing decision in the snapshot → use existing ID; list source parents.

For conflicts, pick a winner per (a) industry best practice, (b) ecosystem popularity, in that priority.

Tier rationale: this is judgment work over bounded context (10–20 decisions × ~200 tokens each). Fast tier risks over/under-merging silently; strong is overkill for non-creative judgment over articulated inputs. Balanced is the floor that produces reliable verdicts; users can override per-agent via `LOCUTUS_MODELS_CONFIG`.

2.2b. New file `internal/agent/reconcile.go` exporting:

```go
type ReconciliationVerdict struct {
    Actions []ReconciliationAction `json:"actions"`
}

type ReconciliationAction struct {
    Kind            string                   // "dedupe" | "resolve_conflict" | "keep_separate" | "reuse_existing"
    Canonical       InlineDecisionProposal   // winning/canonical body for dedupe and resolve_conflict
    ExistingID      string                   // populated for reuse_existing
    Sources         []DecisionSourceRef      // which parent.decisions[i] entries belong to this cluster
    Loser           *InlineDecisionProposal  // populated for resolve_conflict
    RejectedBecause string                   // populated for resolve_conflict
}

type DecisionSourceRef struct {
    ParentKind string // "feature" | "strategy"
    ParentID   string
    Index      int
}

// ApplyReconciliation transforms a RawSpecProposal + verdict into a clean
// SpecProposal with canonical decision IDs. Returns the conflict actions
// so the caller can trigger cascade rewrites for affected nodes.
func ApplyReconciliation(raw *RawSpecProposal, verdict ReconciliationVerdict, existing *ExistingSpec) (*SpecProposal, []ReconciliationAction, error)
```

The Go function is deterministic: same raw + same verdict → same output. All judgment was upstream in the LLM call.

2.2c. ID assignment. The reconciler emits canonical title strings; Go derives `dec-<slug>` IDs from them. Collisions across the proposal are a reconciler bug (a clustering should produce one canonical title per cluster); guard by appending `-2`, `-3` at apply time as a safety net.

### Part 2.3: Workflow integration

2.3a. Edit [internal/scaffold/workflows/spec_generation.yaml](internal/scaffold/workflows/spec_generation.yaml) to insert reconcile between propose and critique:

```yaml
rounds:
  - id: survey
    agent: spec_scout
    merge_as: scout_brief
  - id: propose
    agent: spec_architect
    depends_on: [survey]
    merge_as: raw_proposal       # was proposed_spec
  - id: reconcile                # NEW
    agent: spec_reconciler
    depends_on: [propose]
    merge_as: reconciled_proposal
  - id: critique
    agents: [architect_critic, devops_critic, sre_critic, cost_critic]
    parallel: true
    depends_on: [reconcile]
    merge_as: critic_issues
  - id: revise
    agent: spec_architect
    depends_on: [critique]
    conditional: has_concerns
    merge_as: revisions
max_rounds: 1
```

2.3b. Reconcile's `merge_as` handler runs `ApplyReconciliation` and replaces the upstream `RawSpecProposal` with the canonical `SpecProposal` in `PlanningState`. Subsequent steps (critique, revise) operate on the clean shape.

2.3c. The architect's revise round still receives concerns and emits a complete `SpecProposal` (canonical shape). Revise is now post-reconcile so the architect re-emitting in the canonical shape is consistent — no IDs to coordinate because the previous round already settled them, and the architect can reuse those IDs deterministically.

### Part 2.4: Cascade rewrite trigger

2.4a. After `ApplyReconciliation` returns, iterate the conflict-resolution actions. For each action whose loser was referenced by a feature or strategy:

- Compute the new applicable decision set (post-reconciliation).
- Call `cascade.RewriteFeature(ctx, llm, fsys, *node, applicable, nil)` or `cascade.RewriteStrategy(...)` as appropriate.
- Replace the node's description (or strategy body) with the rewriter's output.
- Skip when the rewriter returns `Updated=false` (the prose was already consistent).

2.4b. Cascade calls inherit the existing `GenerateWithRetry` machinery for transient error handling. Hard errors bubble up uniformly with other LLM errors; the user retries the whole spec generation.

### Part 2.5: Architect prompt rewrite

2.5a. Update [internal/scaffold/agents/spec_architect.md](internal/scaffold/agents/spec_architect.md):

- Drop every "referential integrity" mandate. There is nothing to dangle.
- Drop the "every id you reference MUST appear..." paragraph.
- Reframe the task: emit features and strategies with their decisions inline. No decision IDs; the reconciler assigns them.
- Keep every other mandate (alternatives, citations, architect_rationale, opinion-having, breadth-of-domain).

2.5b. Update the `RawSpecProposal` schema example registered in [internal/agent/schemas.go](internal/agent/schemas.go) to show inline decisions with no IDs.

### Part 2.6: Tests

2.6a. Update `TestGenerateSpecCleanProposalSkipsRevise` and friends to expect the new call topology: 1 scout + 1 propose + 1 reconcile + 4 critics + 0–M cascade calls + 0–1 revise.

2.6b. New `TestReconcilerDedupesIdenticalDecisions` — feed the reconciler two near-identical inline decisions across two parents; verify the verdict marks one canonical, both parents reference the same canonical ID after `ApplyReconciliation`.

2.6c. New `TestReconcilerResolvesConflicts` — feed two genuinely-conflicting inline decisions; verify the verdict picks one with rationale, the loser lands in `alternatives[]`, and a cascade rewrite is triggered for the affected feature.

2.6d. New `TestReconcilerKeepsCompatibleDecisionsSeparate` — feed two decisions that are different aspects of one topic; verify both survive with distinct IDs and no cascade fires.

2.6e. New `TestReconcilerReusesExistingDecisionID` — feed a `RawSpecProposal` and an `Existing.Decisions` snapshot containing a matching decision; verify the verdict emits `reuse_existing` and the canonical ID matches the existing one.

2.6f. New `TestApplyReconciliationIsDeterministic` — pure Go function test; same verdict + same proposal → same output. No LLM in the loop.

2.6g. Compile-time guard: `TestRawSpecProposalNoCrossRefs` confirms `InlineDecisionProposal` has no `ID` field and no fields that point at other decisions/features/strategies by ID — the structural property the design depends on.

### Phase 2 ship criteria

- Gemini Flash produces clean proposals against winplan in ≥4 of 5 consecutive runs.
- Total token cost per successful run ≤ current Pro Preview cost (one extra LLM call for reconcile, but no integrity-revise retries).
- The integrity-revise loop fires on <10% of runs; ideally 0% on clean propose+reconcile.

---

## Phase 3: Outline + per-node elaborate (fanout escalation)

Phase 2 keeps a single architect call with inline decisions. On a 30-feature project, that call still has to emit 30 features × ~3 decisions each ≈ 100+ inline decision objects in one shot. Token-wise that's bounded; cognitively-on-Flash-class-models it may not be.

Phase 3 layers fanout on top of Phase 2's reconciler. The reconciler doesn't change; only the upstream call topology does.

### The fanout flow

```text
survey         (1 LLM call, exists)
  ↓
outline        (1 LLM call, NEW)        — features + strategies, titles + 1-line summaries only
  ↓
elaborate      (N+M LLM calls, NEW)     — fanout: one call per outlined feature, one per
                                          outlined strategy. Each elaborator emits its node
                                          with inline decisions. No IDs, no cross-refs.
  ↓
reconcile      (1 LLM call + 1 Go fn,   — same agent and same Go surgery as Phase 2
                same as Phase 2)
  ↓
cascade        (0–M LLM calls,          — same as Phase 2
                same as Phase 2)
  ↓
critique       (4 LLM calls, exists)
  ↓
revise         (0–1 LLM calls, exists)
```

### Why this structure beats decisions-first decomposition

The chicken-and-egg problem with decisions-first decomposition is that the architect can't know which decisions to make until it knows what features and strategies need them. Phase 3 sidesteps that by doing structure first (outline) and decisions inline with their parent (elaborate), then dedupe/conflict resolution post-hoc (reconcile).

- **Per-call load bounded by node complexity, not project size.** Per-node elaboration scales per individual feature, which is roughly constant.
- **Parallelism.** Feature and strategy elaborations are independent — fan out, wall-clock collapses.
- **Failure isolation.** One bad feature elaboration is one feature to retry, not the whole proposal.
- **Composes with the existing cascade machinery.** When the reconciler flips a node's decisions, [cascade.RewriteFeature](internal/cascade/) / [cascade.RewriteStrategy](internal/cascade/) already exist to rewrite the node's prose under the new decision set.

### Part 3.1: Outline agent + workflow primitive

3.1a. New agent `spec_outliner` ([internal/scaffold/agents/spec_outliner.md](internal/scaffold/agents/spec_outliner.md)) — `capability: balanced`, output schema `Outline { features: [{id, title, summary}], strategies: [{id, title, kind, summary}] }`. Prompt: produce a high-level outline based on GOALS + scout brief. Each item gets a slug-derived ID and a one-line summary. No decisions, no detailed descriptions, no acceptance criteria.

3.1b. Add the `fanout` primitive to the workflow YAML schema and `WorkflowExecutor`. The executor today supports `parallel: true` over a fixed agent list; `fanout` extends this to "spawn N copies of one agent, each given a different element from a state slice." The executor reads the slice from `PlanningState` (e.g., the outline's features) and spawns one agent invocation per element, threading the element through the projection function as the agent's primary input.

### Part 3.2: Elaborator agent

3.2a. New agent `spec_elaborator` ([internal/scaffold/agents/spec_elaborator.md](internal/scaffold/agents/spec_elaborator.md)) — `capability: balanced`, output schema `RawFeatureProposal` or `RawStrategyProposal`. Two projection variants:

- `elaborate_one_feature` — system + GOALS + scout brief + outline (just titles+summaries) + the specific feature being elaborated (id, title, summary). Output: full `RawFeatureProposal` with inline decisions.
- `elaborate_one_strategy` — same shape, for strategies.

The output shape from Phase 2 is reused verbatim. No new types.

### Part 3.3: Workflow YAML

3.3a. Edit [internal/scaffold/workflows/spec_generation.yaml](internal/scaffold/workflows/spec_generation.yaml):

```yaml
rounds:
  - id: survey
    agent: spec_scout
    merge_as: scout_brief
  - id: outline                    # NEW
    agent: spec_outliner
    depends_on: [survey]
    merge_as: outline
  - id: elaborate_features         # NEW
    agent: spec_elaborator
    parallel: true
    fanout: outline.features
    depends_on: [outline]
    merge_as: elaborated_features
    projection: elaborate_one_feature
  - id: elaborate_strategies       # NEW
    agent: spec_elaborator
    parallel: true
    fanout: outline.strategies
    depends_on: [outline]
    merge_as: elaborated_strategies
    projection: elaborate_one_strategy
  - id: reconcile                  # SAME AS PHASE 2
    agent: spec_reconciler
    depends_on: [elaborate_features, elaborate_strategies]
    merge_as: reconciled_proposal
  - id: critique
    agents: [architect_critic, devops_critic, sre_critic, cost_critic]
    parallel: true
    depends_on: [reconcile]
    merge_as: critic_issues
  - id: revise
    agent: spec_architect
    depends_on: [critique]
    conditional: has_concerns
    merge_as: revisions
max_rounds: 1
```

3.3b. The reconcile step now consumes the union of fanout outputs (assemble a single `RawSpecProposal` from `elaborated_features ∪ elaborated_strategies` in the merge handler) and runs the same reconciler from Phase 2.

### Part 3.4: Tests

3.4a. New `TestFanoutSpawnsOnePerOutlineItem` — feed an outline with 3 features, mock the elaborator, verify exactly 3 elaboration calls fire with distinct inputs.

3.4b. The reconciler tests from Phase 2 already cover the convergence path. Phase 3 adds the divergence path (fanout) but the convergence is unchanged.

3.4c. End-to-end: `TestPhase3FullPipelineWinplan` — fixture from a real winplan-shaped GOALS, mock all stages, verify 1 scout + 1 outline + N+M elaborate + 1 reconcile + 4 critics + 0–M cascade + 0–1 revise.

### Phase 3 ship criteria

- Gemini Flash produces clean proposals against winplan-scale projects in ≥4 of 5 consecutive runs (where Phase 2 alone fell short).
- Total token cost per successful run ≤ current Pro Preview cost (more calls, smaller each, no full-proposal regeneration on retries).
- Reconciler verdict quality is unchanged from Phase 2 (the same agent runs against the same shape).

---

## Phase 4: Tool-mediated incremental construction

Phases 1–3 reduce the load. Phase 4 changes the contract: instead of asking the architect/elaborator to emit a self-consistent JSON object, give it tools that *enforce* consistency at registration time. The agent builds the proposal incrementally by calling tools; the tools reject calls that violate integrity. There's no possible way to emit a dangling reference because the function rejects it at the call site.

This is the long-term architecture for model-agnosticism. Even weak models handle "make a tool call" reliably; what they struggle with is "emit a 2k-token consistent blob."

After Phase 2 there are far fewer ways for output to be invalid (no cross-references), but inline decision content can still be malformed (missing alternatives, missing citations, incompatible alternatives shape). Phase 4's tools enforce *content* validity at registration time — same construction-by-tools idea, narrower target surface.

### Part 4.1: Tool definitions

4.1a. In a new file `internal/agent/spectools.go`, define the construction tools using `genkit.DefineTool`:

- `add_inline_decision(input: InlineDecisionInput) → {accepted: bool, error?: string}` — registers a decision under the current parent, validates required fields (alternatives, citations).
- `add_strategy(input: StrategyInput) → {id: string}` — opens a strategy context; subsequent `add_inline_decision` calls bind to it until `commit_strategy()`.
- `add_feature(input: FeatureInput) → {id: string}` — same.
- `commit_strategy()` / `commit_feature()` — closes the current parent context.
- `finalize() → RawSpecProposal` — returns the assembled proposal. Validates structural completeness (at least one feature, etc.) before returning.

4.1b. The tool implementations close over a per-call `ConstructionState` struct holding the in-progress proposal. State is per-Generate-call, not process-wide; concurrent generations get separate states.

### Part 4.2: Wire tools through `GenKitLLM`

4.2a. Add `Tools []ToolDef` to `GenerateRequest` in [internal/agent/llm.go](internal/agent/llm.go).

4.2b. In `GenKitLLM.Generate`, if `req.Tools` is non-empty, register them with the underlying genkit Generate call via `ai.WithTools(...)`. Genkit handles the tool-call loop internally.

4.2c. Tool calls must surface in session traces. Inline-extend `recordedCall` with a `ToolCalls []ToolCall` field, captured from `resp.Message.Content` parts of kind `PartToolRequest`/`PartToolResponse` — simpler for grep/tail, matches the existing one-Generate-one-entry shape.

### Part 4.3: Architect/elaborator prompt for tool mode

4.3a. New system prompt mode for the architect/elaborator when tools are wired:

> Your output is built by calling tools. Open a feature with `add_feature`, register its decisions with `add_inline_decision`, then `commit_feature`. Repeat for every feature and strategy. Finally call `finalize` to commit. Tools reject malformed input; if you see an error, fix and retry. Do not emit a JSON proposal directly.

4.3b. The agent definition switches modes based on whether the workflow step requests tool-mode (a new flag in the workflow YAML).

### Part 4.4: Workflow integration

4.4a. Workflow YAML gets a new `tools: [...]` field per step listing which tools the agent has access to. `WorkflowExecutor.executeAgent` looks them up and passes via `GenerateRequest.Tools`.

### Part 4.5: Tests

4.5a. Mock LLM gains tool-call simulation: a `MockResponse` can specify a sequence of tool calls with synthetic responses, then a final text response.

4.5b. Test that the construction tools reject malformed input with named errors.

4.5c. Test that a successful tool sequence produces a clean RawSpecProposal via the finalize call.

4.5d. Integration test: run a full council with tool-mode enabled against a mock that exercises the tool path.

### Part 4.6: Migration

4.6a. Tool mode is opt-in initially via a workflow YAML field. Existing workflows keep the JSON-emit path. Once tool mode is shaken out, the YAML default flips and we delete the JSON-emit path.

### Phase 4 ship criteria

- Gemini Flash produces clean proposals against winplan with zero malformed-content errors across 5 consecutive runs.
- Anthropic-side caveat handled: forced-tool-use for `responseSchema` interacts with extra tools — confirmed working (or documented as a Gemini-only feature for v1).
- Token cost per successful run drops (smaller per-call inputs/outputs, no full proposal regeneration on retries).

---

## Phase 5: Critic sharpening (independent of 1–4)

Even after the structural fixes, the critic → revise round inside the workflow demonstrably failed in the original winplan run: `architect_critic` flagged the integrity issue verbatim, and revise ignored it. Two issues:

1. The critique → revise hop has the same prompt-dilution problem the post-workflow integrity revise had before we sharpened it (`78da6b5`).
2. The critics emit free-form prose findings; revise has to interpret them. Specific machine-readable findings would be more actionable.

### Part 5.1: Tighten the revise prompt

5.1a. The prompt the architect sees in revise should mirror the directive shape we use in `reviseForIntegrity`: explicit rejection ("the prior proposal is rejected"), enumerated violations, prescriptive actions.

5.1b. Update [internal/agent/projection.go](internal/agent/projection.go)'s revise projection to render each critic concern as `- {kind}: {finding}` rather than prose paragraphs.

### Part 5.2: Add a mechanical integrity critic to the workflow

5.2a. New non-LLM critic (a simple Go function) that runs in the critique step alongside the LLM critics. It runs `proposal.Validate()` and emits any findings as `CriticIssues` entries with `kind: integrity`.

5.2b. The integrity critic always runs and is cheap — it's a Go function call, not an LLM call. The LLM critics keep their current focus (architecture/devops/SRE/cost critique).

5.2c. The revise round now sees integrity violations *during* the workflow, not just after. The post-workflow integrity loop becomes a backstop for cases where revise still produces a broken proposal. After Phase 2 there should be nothing for the integrity critic to flag in the common case; the critic is then load-bearing only on regressions.

### Phase 5 ship criteria

- The post-workflow integrity loop fires on <10% of clean-flow runs (most issues caught and repaired in-workflow).
- Existing critic prompts unchanged; only revise prompt and the new integrity critic are touched.

---

## Sequencing recommendation

1. **Phase 1 has shipped.** DJ-087.
2. **Land Phase 2 next.** This is the structural fix for the winplan failure mode. Single moderate change with clear test coverage. Re-test winplan on Flash after.
3. **Phase 5 lands whenever** — it's independent and improves both the current and future architectures. A natural pairing with Phase 2 since Phase 2's reconciler removes most of what the integrity critic would flag.
4. **Phase 3 only if Phase 2 isn't enough on big projects.** The fanout escalation is real surgery (new workflow primitive, two new agents); don't pay for it until measured failure says so.
5. **Phase 4 is the long-term answer.** Don't ship until Phases 1–3 have demonstrated their ceiling.

## Risks and reversibility

- **Phase 2 changes the architect's output contract.** `RawSpecProposal` is a new shape; the old `SpecProposal` survives as the post-reconcile shape. Existing tests that mocked `SpecProposal` from the proposer need to mock `RawSpecProposal` instead. The persistence layer is untouched.
- **Phase 2 introduces a new LLM call (reconcile) on every run.** Net token cost: +1 reconcile call, –N integrity-revise retries. On winplan today the net is favorable (2 integrity-revise attempts at 36s + 63s vs one reconcile call). On clean Pro Preview runs the reconcile is pure overhead, but it's bounded (one call, balanced tier).
- **Reconciler over-merging or under-merging is a new failure mode.** Mitigation: tests for each verdict kind (dedupe / resolve_conflict / keep_separate / reuse_existing) plus end-to-end on winplan-shaped input. Reconciler temperature stays low (0.2).
- **Phase 3 changes the workflow YAML and adds the fanout primitive.** Users who customized their workflow will need to update; ship a migration note.
- **Phase 4 changes the `LLM` interface.** Adding `Tools []` to `GenerateRequest` is additive; existing callers remain valid. The bigger change is that tool-mode prompts assume tool-call support — providers that don't support tools fall back to the JSON-emit path.
- **Phase 5 is purely additive.** The new integrity critic runs alongside existing critics; sharpened revise prompt is a string change.

Every phase ships as one or more focused commits with tests. Reverting any single phase is a `git revert <hash>` away.

## Open questions

- Phase 2: should the reconciler's `keep_separate` action emit a hint (e.g., "topic: storage; aspects: read-vs-write") so a future `refine` pass can offer to merge them? Probably yes, but not load-bearing for v1.
- Phase 3: should outline pre-assign decision-count expectations per node ("this feature will have ~3 decisions") so the elaborator has a target? Default: no — let the elaborator decide; over-specifying invites the architect's training-distribution defaults to leak in.
- Phase 4: do we expose the construction tools to the user's MCP clients, or is tool-mode an internal mechanism for the council only? Internal-only seems right; user-facing tools are the eight verbs.
- Phase 5: should the mechanical integrity critic short-circuit the revise round when its only finding is integrity-related? Worth measuring; not strictly necessary.
