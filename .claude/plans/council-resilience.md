# Plan: Council Resilience — Make Spec Generation Model-Agnostic

## Context

Locutus is an open-source tool. End users will plug in whatever model they have credit for — Gemini Flash, Claude Sonnet, Llama variants, GPT-class models. The council's job is to produce a referentially-clean `SpecProposal` from GOALS.md regardless of which model is wired up. Today it can't.

A real run on `winplan` (GOALS.md ≈ 8 lines) with `googleai/gemini-3.1-pro-preview` failed three Pro Preview calls in a row to produce a complete proposal even when given mechanical, prescriptive instructions naming the exact missing IDs. The integrity-revise loop (`5a42eb2`) caught the failure and surfaced a hard error rather than silently stripping refs (`fix(council): hard fail on dangling refs instead of silent strip`). That's the right behaviour, but the underlying problem is structural.

The architect is asked to emit a single 2k-token JSON blob containing features, decisions, strategies, AND approaches, with cross-array referential integrity that JSON Schema cannot enforce. For a non-trivial GOALS.md that's ~30 cross-references the model has to track via attention alone while also generating prose. Stronger models (Opus, GPT-class) tolerate this; weaker models drop refs.

## Goal

Make the council produce a clean `SpecProposal` reliably across the model spectrum, without silently degrading the output and without forcing users onto the most expensive models.

Success looks like:

- Gemini 3 Flash and Claude Haiku produce clean proposals on a winplan-scale project.
- Total token cost per successful run drops, not rises.
- The integrity gate added in `5a42eb2` becomes a backstop that rarely fires, not the primary correctness mechanism.
- Approaches are produced from real code context, not invented during refine.

## Scope

In scope:

- Removing approaches from `SpecProposal` and the refine-time output.
- Moving approach synthesis to adopt's existing per-parent path.
- Decomposing the architect's single emit into outline + per-node elaboration + reconciliation.
- Wiring tool-mediated construction so integrity is enforced at registration time, not by post-validation.
- Sharpening the in-workflow critic prompts so issues caught early don't need post-hoc rescue.

Out of scope:

- Changing the on-disk shape under `.borg/spec/` (DJ-085 stability).
- Replacing the council pattern (survey → propose → critique → revise stays).
- Streaming the LLM responses (separate work; deferred per earlier discussion).

## Architectural shift

Each phase reduces the cognitive load on the architect and pushes correctness from "model attention" to "code state." Phases ship independently and the user can stop at any point if the early phases solve the problem at acceptable cost.

```text
Phase 1: Drop approaches from SpecProposal      → smallest model load drop, biggest conceptual fix
Phase 2: Outline → fanout elaborate → reconcile → cascade
                                                → decompose by node, converge by reconciler
Phase 3: Tool-mediated incremental construction → optional polish if Phase 2 isn't enough
Phase 4: Critic sharpening                      → defense in depth, cheap, independent
```

Phases 1 and 2 are likely sufficient on their own for most projects. Phase 3 is the answer if Phase 1+2 isn't enough on weak models. Phase 4 is independent of the others and worth landing whenever.

---

## Phase 1: Approaches out of refine, into adopt

The premise from CLAUDE.md is that approaches are "the synthesis layer for coding agents" — implementation sketches that bridge spec and code. They need code context. During refine that context doesn't exist; the architect has to invent the sketch, and those invented sketches drive a substantial fraction of the dangling-ref problem (every approach needs a `parent_id`, every feature/strategy carries an `approaches[]` cross-ref array).

The single-approach synthesizer already exists at [cmd/refine.go:`invokeSynthesizer`](cmd/refine.go) — it takes a parent's prose plus applicable decisions and produces an approach body. Adopt already classifies approaches (live/drifted/unplanned/failed). The gap is small: when adopt encounters an unplanned parent (feature or strategy with no approach yet), it should synthesize one on-demand using the existing path.

### Part 1.1: Strip approaches from the proposal types

1.1a. In [internal/agent/specgen.go](internal/agent/specgen.go), delete the following:

- `SpecProposal.Approaches []ApproachProposal`
- `FeatureProposal.Approaches []string`
- `StrategyProposal.Approaches []string`
- The `ApproachProposal` type itself.

1.1b. Update `SpecProposal.Validate` to drop approach-related checks. The `parentable` index becomes irrelevant; remove it.

1.1c. Update `SpecProposal.Strip` likewise (kept as the deliberate fallback).

1.1d. Update `SpecProposal.ToAssimilationResult` to stop populating `r.Approaches`.

### Part 1.2: Update the architect's contract

1.2a. Edit [internal/scaffold/agents/spec_architect.md](internal/scaffold/agents/spec_architect.md) to remove every reference to `approaches[]` from the task description, the field list, and the schema example. The architect now produces features, strategies, and decisions only.

1.2b. Edit [internal/scaffold/agents/spec_scout.md](internal/scaffold/agents/spec_scout.md) if it mentions approaches in its scout brief shape (it shouldn't — the scout brief is `ScoutBrief`, not `SpecProposal` — but verify).

1.2c. Update the `SpecProposal` example registered in [internal/agent/schemas.go](internal/agent/schemas.go) to drop `approaches` from the example value. This is what gets injected as the schema example into the architect's system prompt at generate time.

### Part 1.3: Adopt-time approach synthesis

1.3a. In adopt's classification path, when a `Feature` or `Strategy` has no children in the spec graph and the parent is selected for adoption, call `invokeSynthesizer` (or a new `synthesize.ApproachForParent` factored from it) to produce an approach. The synthesizer takes:

- parent kind, ID, title, prose
- applicable decisions (already loaded by classify)
- returns RewriteResult.RevisedBody

1.3b. Persist the synthesized approach via `specio.SaveMarkdown` to `.borg/spec/approaches/app-<parent-id>.md`. Use a deterministic ID derivation (`app-<parent-id>` or `app-<parent-id>-1` if multiple) so re-runs are idempotent.

1.3c. Update the parent's `approaches[]` field on the spec graph (in memory; persistence layer writes it back as a side effect of the existing persist path).

1.3d. Skip synthesis when adopt is invoked with a scope that excludes the parent. The synthesizer is per-parent on-demand; it shouldn't fire for parents the user isn't currently adopting.

1.3e. `--dry-run` runs the synthesizer but routes writes through the read-only FS wrapper, matching existing dry-run semantics.

### Part 1.4: Tests

1.4a. `internal/agent/specgen_test.go`: drop `approaches` from every fixture proposal. Drop the `parent_id` warning test from the validate cases (or move to a future per-approach validation test elsewhere).

1.4b. New test `TestSpecProposalNoApproaches`: confirms the Go type no longer has the field (compile-time check via struct tag inspection).

1.4c. `cmd/refine_test.go`: TestRefineGoalsGeneratesSpecGraph currently asserts `result.Generated.Approaches == 1`. Change to assert `Approaches == 0` and instead verify the rest landed.

1.4d. New test `cmd/adopt_synthesize_test.go`: feed adopt a project whose spec has features but no approaches, run with `--dry-run`, assert the synthesizer fired and produced the expected number of approaches in the dry-run report. (No persistence assertion — readOnlyFS swallows writes.)

1.4e. `cmd/adopt_test.go`: ensure existing adopt tests still pass with synthesis enabled. Tests that pre-seed approaches should still see those approaches; the new synthesis path only fires when missing.

### Part 1.5: Migration / docs

1.5a. Add a Decision Journal entry in `docs/DECISION_JOURNAL.md`: "DJ-XXX: Approaches are synthesized at adopt time, not refine time." Cite this plan and the winplan failure.

1.5b. Update CLAUDE.md's "Sources of Truth" section if it mentions approach generation timing (it shouldn't, but check).

1.5c. The `.borg/agents/spec_architect.md` ships via `locutus init` (scaffold). Existing projects keep their old version of the file until they re-init or manually update. Document this in the DJ entry.

### Phase 1 ship criteria

- `go test ./...` and `go vet ./...` pass.
- `locutus refine goals` against winplan produces 0 approach references and 0 dangling refs on a clean run.
- `locutus adopt --dry-run` against the same project shows synthesized approaches in the report.
- Token cost per refine run measurably lower (compare session YAML output_tokens).

---

## Phase 2: Outline → fanout elaborate → reconcile (with cascade)

After Phase 1 the proposal still asks one architect to produce all features, strategies, AND decisions in a single response, with cross-refs between them. Even with approaches gone, that's a working-memory load that scales with project size.

The fix is to decompose the work the way an engineer would: a 100k-foot outline first, then drill down per-node, then reconcile the cross-cutting decisions that emerge. Each individual call is small, bounded, and well-suited to even weak models. Cross-cutting consistency is enforced *after* generation by a reconciler step rather than maintained by attention during generation.

### The flow

```text
survey         (1 LLM call, exists)
  ↓
outline        (1 LLM call, NEW)        — features + strategies, titles + 1-line summaries only, no decisions
  ↓
elaborate      (N+M LLM calls, NEW)     — fanout: one call per outlined feature, one per outlined strategy
                                          each elaborator emits its node + the decisions it depends on
                                          decisions get title-derived ids (dec-<slug>); collisions are expected
  ↓
reconcile      (1 LLM call + 1 Go fn,   — judges every decision across all elaborations: dedupe / resolve_conflict /
                NEW)                      keep_separate, emits a structured verdict; Go applies the verdict
                                          mechanically (rewrite refs, populate alternatives)
  ↓
cascade        (0–M LLM calls,          — for each conflict whose loser was referenced by a feature/strategy,
                NEW, only on conflicts)   re-runs cascade.RewriteFeature / cascade.RewriteStrategy so the
                                          affected node's prose reflects the winning decision
  ↓
critique       (4 LLM calls, exists)    — sees the reconciled, ref-clean proposal
  ↓
revise         (0–1 LLM calls, exists)  — conditional on critic concerns
```

The integrity-revise loop from `5a42eb2` becomes a pure backstop. After Phase 2 it should rarely fire on clean elaborate runs, because the reconciler is the primary integrity mechanism.

### Why this beats decisions-first decomposition

(The original Phase 2 was a two-pass propose: decisions first, then structure. The current design supersedes it.)

- **Per-call load bounded by node complexity, not project size.** Decisions-first still scaled with project size; per-node elaboration scales per individual feature, which is roughly constant.
- **Parallelism.** Feature and strategy elaborations are independent — fan out, wall-clock collapses.
- **Failure isolation.** One bad feature elaboration is one feature to retry, not the whole proposal.
- **Reconciliation is a real architectural feature, not a workaround.** It models how engineers plan: divergent then convergent. The reconciler agent's task is narrow and well-defined.
- **Composes with the existing cascade machinery.** When the reconciler flips a node's decisions, [cascade.RewriteFeature](internal/cascade/) / [cascade.RewriteStrategy](internal/cascade/) already exist to rewrite the node's prose under the new decision set. We're not inventing new machinery for the cascade rewrite — it's the same code path `refine <feature-id>` uses today.

### Settled design questions

These were debated and locked during plan iteration; recording the answers here so implementation doesn't re-litigate.

- **ID assignment**: elaborate calls invent decision IDs locally (slugified from titles). Reconciler normalizes during dedupe — rewrites refs to canonical IDs. Outline does NOT pre-assign decision IDs; it stays focused on "what features and strategies exist."
- **Conflict resolution priority**: best-practice → popularity. Reconciler applies in that order with documented rationale.
- **Loser disposition**: when a conflict has a winner, the loser is recorded in the winner's `alternatives[]` with `rejected_because` populated by the reconciler's reasoning. DJ-085 provenance preserved. Affected nodes get cascade-rewritten under the winner — same flow as a manual `refine <feature-id>` after a decision flip.
- **Reconciler context**: the reconciler sees the *full* decision objects (title, rationale, architect_rationale, citations, confidence, alternatives) for every decision across all elaborations. Picking a winner without architect_rationale and citations would produce arbitrary picks; we want good picks.
- **Compatible decisions don't merge.** Three categories of overlap, only two of them get reconciled:
  - **Identical** (same conclusion + similar reasoning) → dedupe.
  - **Conflicting** (incompatible answers to the same question) → pick winner + cascade rewrite.
  - **Compatible** (different aspects of the same topic) → leave as separate entries. The approach synthesizer at adopt time integrates them when planning the implementation; spec-time merging would be lossy and premature.
- **No mechanical dedupe pre-pass.** Title fuzzy-matching, embeddings with similarity thresholds, and pairwise N² comparisons are all fragile. One LLM call clusters and judges all decisions in a single pass; one Go function applies the verdict mechanically. All judgment in the LLM, all surgery in code.
- **Cascade-rewrite error handling**: cascade rewrites use `GenerateWithRetry` and the existing challenge-protocol for malformed JSON. Transient errors retry; hard errors bubble up uniformly. There's no new "partial rewrite" failure mode to design around.

### Part 2.1: Workflow YAML and `fanout` primitive

2.1a. Edit `internal/scaffold/workflows/spec_generation.yaml`:

```yaml
rounds:
  - id: survey
    agent: spec_scout
    merge_as: scout_brief
  - id: outline                    # NEW — high-level features + strategies
    agent: spec_outliner           # NEW agent
    depends_on: [survey]
    merge_as: outline
  - id: elaborate_features         # NEW
    agent: spec_elaborator         # NEW agent
    parallel: true
    fanout: outline.features       # NEW workflow primitive
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
  - id: reconcile                  # NEW
    agent: spec_reconciler         # NEW agent
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

2.1b. Add the `fanout` primitive to the workflow YAML schema and `WorkflowExecutor`. The executor today supports `parallel: true` over a fixed agent list; `fanout` extends this to "spawn N copies of one agent, each given a different element from a state slice." The executor reads the slice from `PlanningState` (e.g., the outline's features) and spawns one agent invocation per element, threading the element through the projection function as the agent's primary input.

### Part 2.2: New agents

2.2a. **`spec_outliner`** ([internal/scaffold/agents/spec_outliner.md](internal/scaffold/agents/spec_outliner.md)) — `capability: balanced`, output schema `Outline { features: [{id, title, summary}], strategies: [{id, title, kind, summary}] }`. Prompt: produce a high-level outline based on GOALS + scout brief. Each item gets a slug-derived ID and a one-line summary. No decisions, no detailed descriptions, no acceptance criteria.

2.2b. **`spec_elaborator`** ([internal/scaffold/agents/spec_elaborator.md](internal/scaffold/agents/spec_elaborator.md)) — `capability: balanced`, output schema `ElaboratedNode { feature?: FeatureProposal, strategy?: StrategyProposal, decisions: [DecisionProposal] }`. Two projection variants:

- `elaborate_one_feature` — system + GOALS + scout brief + outline (just titles+summaries) + the specific feature being elaborated (id, title, summary). Output: full `FeatureProposal` (id, title, description, acceptance_criteria, decisions[]) plus the decisions it depends on.
- `elaborate_one_strategy` — same shape, for strategies.

Decisions get title-derived IDs (`dec-<slug>`). Collisions across elaborations are expected; the reconciler resolves them.

2.2c. **`spec_reconciler`** ([internal/scaffold/agents/spec_reconciler.md](internal/scaffold/agents/spec_reconciler.md)) — `capability: balanced`, `temperature: 0.2`, output schema `ReconciliationVerdict`. Prompt: cluster all decisions by topic; emit a structured action list with three action kinds (`dedupe`, `resolve_conflict`, `keep_separate`). For conflicts, pick a winner per (a) industry best practice, (b) ecosystem popularity, in that priority. Document the loser's rationale in `rejected_because` so the surgery step can populate the winner's `alternatives[]`.

Tier rationale: this is judgment work over bounded context (10–20 decisions × ~200 tokens each). Fast tier risks over/under-merging silently; strong is overkill for non-creative judgment over articulated inputs. Balanced is the floor that produces reliable verdicts; users can override per-agent via `LOCUTUS_MODELS_CONFIG`.

### Part 2.3: Reconciliation surgery (Go function)

2.3a. New file `internal/agent/reconcile.go` exporting `ApplyReconciliation(prop *SpecProposal, verdict ReconciliationVerdict) []ReconciliationAction` — applies dedupe/resolve_conflict actions mechanically: rewrites refs in features and strategies, populates `alternatives[]` on winners, removes losers from the proposal. Returns the list of conflict-resolution actions (so the caller knows which nodes need cascade rewrites).

2.3b. New `ReconciliationVerdict` and `ReconciliationAction` types co-located with `ApplyReconciliation`.

### Part 2.4: Cascade rewrite trigger

2.4a. After `ApplyReconciliation` returns, iterate the conflict-resolution actions. For each action whose loser was referenced by a feature or strategy:

- Compute the new applicable decision set (post-reconciliation).
- Call `cascade.RewriteFeature(ctx, llm, fsys, *node, applicable, nil)` or `cascade.RewriteStrategy(...)` as appropriate.
- Replace the node's description (or strategy body) with the rewriter's output.
- Skip when the rewriter returns `Updated=false` (the prose was already consistent).

2.4b. Cascade calls inherit the existing `GenerateWithRetry` machinery for transient error handling. Hard errors bubble up uniformly with other LLM errors; the user retries the whole spec generation.

### Part 2.5: Sub-proposal types

2.5a. Add `Outline` (features + strategies, slim form), `ElaboratedNode` (one feature OR strategy + its decisions), and `ReconciliationVerdict` (action list) types in `internal/agent/specgen.go` or a new `internal/agent/elaboration.go`.

2.5b. Register schemas in [internal/agent/schemas.go](internal/agent/schemas.go).

2.5c. Merge handler that assembles a complete `SpecProposal` from outline + elaborated_features + elaborated_strategies + reconciled decision set. Lives in the workflow's reconcile merge_as handler.

### Part 2.6: Tests

2.6a. Update `TestGenerateSpecCleanProposalSkipsRevise` and friends to expect the new call topology: 1 scout + 1 outline + N feature elaborations + M strategy elaborations + 1 reconcile + 4 critics + 0–M cascade calls + 0–1 revise.

2.6b. New `TestReconcilerDedupesIdenticalDecisions` — feed the reconciler two near-identical decisions across feature elaborations; verify the verdict marks one canonical, refs are rewritten.

2.6c. New `TestReconcilerResolvesConflicts` — feed two genuinely-conflicting decisions; verify the verdict picks one with rationale, the loser lands in `alternatives[]`, and a cascade rewrite is triggered for the affected feature.

2.6d. New `TestReconcilerKeepsCompatibleDecisionsSeparate` — feed two decisions that are different aspects of one topic; verify both survive and no cascade fires.

2.6e. New `TestFanoutSpawnsOnePerOutlineItem` — feed an outline with 3 features, mock the elaborator, verify exactly 3 elaboration calls fire with distinct inputs.

2.6f. New `TestApplyReconciliationIsDeterministic` — pure Go function test; same verdict + same proposal → same output. No LLM in the loop.

### Phase 2 ship criteria

- Gemini Flash produces clean proposals against winplan in ≥4 of 5 consecutive runs.
- Total token cost per successful run ≤ current Pro Preview cost (more calls, smaller each, no full-proposal regeneration on retries).
- The integrity-revise loop fires on <10% of runs.

---

## Phase 3: Tool-mediated incremental construction

Phases 1 and 2 reduce the load. Phase 3 changes the contract: instead of asking the architect to emit a self-consistent JSON object, give it tools that *enforce* consistency at registration time. The architect builds the proposal incrementally by calling tools; the tools reject calls that violate integrity. There's no possible way to emit a dangling reference because the function rejects it at the call site.

This is the long-term architecture for model-agnosticism. Even weak models handle "make a tool call" reliably; what they struggle with is "emit a 2k-token consistent blob."

### Part 3.1: Tool definitions

3.1a. In a new file `internal/agent/spectools.go`, define the construction tools using `genkit.DefineTool`:

- `add_decision(input: DecisionInput) → {id: string}` — registers a decision, returns the assigned ID.
- `add_strategy(input: StrategyInput, decision_ids: []string) → {id: string}` — fails if any decision_id isn't registered.
- `add_feature(input: FeatureInput, decision_ids: []string) → {id: string}` — same.
- `finalize() → SpecProposal` — returns the assembled proposal. Validates structural completeness (at least one feature, etc.) before returning.

3.1b. The tool implementations close over a per-call `ConstructionState` struct holding the in-progress proposal. State is per-Generate-call, not process-wide; concurrent generations get separate states.

3.1c. ID assignment: the tool generates IDs from the title (`dec-<slugified-title>`) or accepts an architect-supplied ID if provided (and validates uniqueness). The architect can't fabricate; it can only request.

### Part 3.2: Wire tools through `GenKitLLM`

3.2a. Add `Tools []ToolDef` to `GenerateRequest` in [internal/agent/llm.go](internal/agent/llm.go).

3.2b. In `GenKitLLM.Generate`, if `req.Tools` is non-empty, register them with the underlying genkit Generate call via `ai.WithTools(...)`. Genkit handles the tool-call loop internally.

3.2c. Tool calls must surface in session traces. Either:

- Inline: extend `recordedCall` with a `ToolCalls []ToolCall` field, captured from `resp.Message.Content` parts of kind `PartToolRequest`/`PartToolResponse`.
- Or: expand into separate top-level entries with a `parent_index` field linking back.

Choose inline — simpler for grep/tail, matches the existing one-Generate-one-entry shape.

### Part 3.3: Architect prompt for tool mode

3.3a. New system prompt mode for the architect when tools are wired:

> Your output is built by calling tools. Call `add_decision` for every decision (in any order). Then call `add_strategy` and `add_feature` for the structural nodes — these tools require decision IDs you've already registered. Finally call `finalize` to commit. The tools will reject invalid references; if you see an error, fix and retry. Do not emit a JSON proposal directly.

3.3b. The agent definition in `.borg/agents/spec_architect.md` switches modes based on whether the workflow step requests tool-mode (a new flag in the workflow YAML).

### Part 3.4: Workflow integration

3.4a. Workflow YAML gets a new `tools: [...]` field per step that lists which tools the agent has access to. `WorkflowExecutor.executeAgent` looks them up and passes via `GenerateRequest.Tools`.

3.4b. The propose step (Phase 2 already split this; in Phase 3 we may merge it back into a single `propose_with_tools` step since tools obviate the multi-pass need).

### Part 3.5: Tests

3.5a. Mock LLM gains tool-call simulation: a `MockResponse` can specify a sequence of tool calls with synthetic responses, then a final text response.

3.5b. Test that the construction tools reject invalid references with named errors.

3.5c. Test that a successful tool sequence produces a clean SpecProposal via the finalize call.

3.5d. Integration test: run a full council with tool-mode enabled against a mock that exercises the tool path.

### Part 3.6: Migration

3.6a. Tool mode is opt-in initially via a workflow YAML field. Existing workflows keep the JSON-emit path. Once tool mode is shaken out, the YAML default flips and we delete the JSON-emit path.

3.6b. Document the choice in a Decision Journal entry: "DJ-XXX: Tool-mediated proposal construction." Reference the model-agnosticism rationale.

### Phase 3 ship criteria

- Gemini Flash produces clean proposals against winplan with zero integrity violations across 5 consecutive runs.
- Anthropic-side caveat handled: forced-tool-use for `responseSchema` interacts with extra tools — confirmed working (or documented as a Gemini-only feature for v1).
- Token cost per successful run drops (smaller per-call inputs/outputs, no full proposal regeneration on retries).

---

## Phase 4: Critic sharpening (independent of 1–3)

Even after the structural fixes, the critic → revise round inside the workflow demonstrably failed in the winplan run: `architect_critic` flagged the integrity issue verbatim, and revise ignored it. Two issues:

1. The critique → revise hop has the same prompt-dilution problem the post-workflow integrity revise had before we sharpened it (`78da6b5`).
2. The critics emit free-form prose findings; revise has to interpret them. Specific machine-readable findings would be more actionable.

### Part 4.1: Tighten the revise prompt

4.1a. The prompt the architect sees in revise should mirror the directive shape we use in `reviseForIntegrity`: explicit rejection ("the prior proposal is rejected"), enumerated violations, prescriptive actions.

4.1b. Update [internal/agent/projection.go](internal/agent/projection.go)'s revise projection to render each critic concern as `- {kind}: {finding}` rather than prose paragraphs.

### Part 4.2: Add a mechanical integrity critic to the workflow

4.2a. New non-LLM critic (a simple Go function) that runs in the critique step alongside the LLM critics. It runs `proposal.Validate()` and emits the warnings as `CriticIssues` entries with `kind: integrity`.

4.2b. The integrity critic always runs and is cheap — it's a Go function call, not an LLM call. The LLM critics keep their current focus (architecture/devops/SRE/cost critique).

4.2c. The revise round now sees integrity violations *during* the workflow, not just after. The post-workflow integrity loop becomes a backstop for cases where revise still produces a broken proposal.

### Phase 4 ship criteria

- The post-workflow integrity loop fires on <10% of clean-flow runs (most issues caught and repaired in-workflow).
- Existing critic prompts unchanged; only revise prompt and the new integrity critic are touched.

---

## Sequencing recommendation

1. **Land Phase 1 first.** Single solid commit. Re-test winplan. If Pro Preview now produces clean proposals (likely, given the load drop), Phases 2–4 can be paced based on real data.
2. **If Phase 1 isn't enough on weak models**, ship Phase 2. It's a moderate change with clear test coverage.
3. **Phase 4 lands whenever** — it's independent and improves both the current and future architectures.
4. **Phase 3 is the long-term answer.** Don't ship it until Phases 1–2 have demonstrated their ceiling. If Phase 1+2 reliably produces clean proposals on Flash-class models, Phase 3 is optional polish; if they don't, Phase 3 is mandatory.

## Risks and reversibility

- **Phase 1 changes `SpecProposal` shape.** Existing projects with persisted approaches keep them; only new refines stop emitting them. The persistence layer reads the on-disk shape, which is unchanged. Low risk.
- **Phase 2 changes the workflow YAML.** Users who customized their workflow will need to update; ship a migration note.
- **Phase 3 changes the `LLM` interface.** Adding `Tools []` to `GenerateRequest` is additive; existing callers remain valid. The bigger change is that tool-mode prompts assume tool-call support — providers that don't support tools fall back to the JSON-emit path.
- **Phase 4 is purely additive.** The new integrity critic runs alongside existing critics; sharpened revise prompt is a string change.

Every phase ships as one or more focused commits with tests. Reverting any single phase is a `git revert <hash>` away.

## Open questions

- Phase 2: should the `propose_structure` round see the decision *bodies* (rationale, alternatives) or just the ID + title list? More context = better referencing, but also more tokens. Default: ID + title + one-sentence summary.
- Phase 3: do we expose the construction tools to the user's MCP clients, or is tool-mode an internal mechanism for the council only? Internal-only seems right; user-facing tools are the eight verbs.
- Phase 4: should the mechanical integrity critic short-circuit the revise round when its only finding is integrity-related (i.e., skip the LLM critics entirely on a fast retry)? Worth measuring; not strictly necessary.
