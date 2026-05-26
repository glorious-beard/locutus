# DJ-130 — Provider Mechanics Encapsulated in Adapters; Trace Recording Follows the Provider-Call Boundary

> **Governing DJ:** [DJ-130](../../docs/DECISION_JOURNAL.md#dj-130). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** SUPERSEDED by DJ-135 on 2026-05-26. The direct-SDK adapters and the reasoning/format split (`runSplit`, `requiresThinkingSchemaSplit`) retire per DJ-135 resolved-question 13 — Locutus makes no direct LLM calls under the new model; the coding-agent runtime owns provider mechanics. The cognitive-task-separation *principle* this DJ articulated carries forward and is preserved in `CLAUDE.md`'s "Cognitive Task Separation" section. Trace recording shifts to ACP-event capture under `.locutus/sessions/<sid>/`. Historical: DONE — Phases 1-4 + 6 landed 2026-05-21.
> **Predecessors:** [DJ-108](../../docs/DECISION_JOURNAL.md#dj-108) (Anthropic native structured output — set the direction); [DJ-122](../../docs/DECISION_JOURNAL.md#dj-122) (spawner-driven WorkflowExecutor — introduced the wiring gap); commit [`5d15e7b`](https://github.com/chetan/locutus/commit/5d15e7b) (unrecorded dispatcher-side split — what this plan retires).
> **Surface area:** dispatcher cleanup + workflow wiring + per-adapter split logic + recorder relocation + on-disk trace layout change.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Each phase verifies independently before moving on. **Per-adapter logic changes require empirical verification against the provider's API**; static unit tests aren't sufficient evidence that the split actually fires correctly on the real model.

## Why this plan exists

The fifth winplan re-run converged for the first time but produced a spec with 13 decisions, 2 features, and **zero strategies**. The root-cause investigation traced the failure to extended thinking + structured-output serialization dropping the scout's drafted strategies between thinking and JSON emission — a failure mode that affects Claude (`dummy` placeholders), Gemini (silently drops fields), and OpenAI gpt-5-nano (empty `{}` tool args) alike.

The workaround (a two-call reason-then-format split) exists in code as of commit `5d15e7b` but lives at the **wrong architectural layer** — the Dispatcher decides whether to split based on `def.Thinking != off && def.OutputSchema != ""`, encoding provider-specific knowledge at a layer that has no other reason to know about it. Worse: the spec-generation `WorkflowExecutor` bypasses the Dispatcher entirely ([`workflow.go:322`](../../internal/agent/workflow.go#L322)), so the split has been sitting unused for the spec-gen pathway since the day it shipped. And worst: the per-call YAML recorder records one YAML per `LoggingExecutor.Run`, but OTel records one span per provider call — when the split fires those surfaces drift apart precisely when an operator most needs to see both calls.

DJ-130 closes three coupled gaps:

1. **Relocate the split into each provider adapter.** The adapter is the layer that knows which provider it's talking to; provider-specific quirks belong there. Mirrors the direction DJ-108 explicitly set.
2. **Route workflow LLM calls through `Dispatcher.Dispatch`.** Closes the wiring gap DJ-122 introduced. The workflow expresses intent (one `Dispatch` per agent step); the dispatcher orchestrates retry/rotation/observability.
3. **Move per-call YAML recording into the adapter layer, aligned with the provider-call boundary.** OTel and YAML agree on what constitutes a "call." Per-step folders carry a parent `step.yaml` summary; child YAMLs (one per SDK call) link via `parent_call_id`.

## Reference state (before adoption)

- **`Dispatcher.dispatchSplit`** ([dispatcher.go:226](../../internal/agent/dispatcher.go#L226)) handles the two-call sequence. Gated by `shouldSplitForFormat` (line 195) on `def.Thinking != off && def.OutputSchema != ""` and the executor implementing `FormatProvider`.
- **`FormatProvider`** interface (dispatcher.go:141), satisfied by `*Executor.FormatPreferences()` (executor.go:237) returning rotation prefs from `models.yaml`'s `format_providers:` block.
- **`canonicalFormatterPrompt`** (dispatcher.go:213) is the system prompt the format pass uses.
- **`models.yaml` `format_providers:`** (models.yaml:91-94) lists `anthropic, openai, googleai` as the format-pass rotation.
- **`WorkflowExecutor.invokeOne`** ([workflow.go:322](../../internal/agent/workflow.go#L322)) calls `RunWithRetry(ctx, e.Executor, def, input, ...)` directly, bypassing `Dispatcher.Dispatch`.
- **`LoggingExecutor.Run`** ([session.go:744-765](../../internal/agent/session.go#L744-L765)) records one `SessionRecorder.Begin/Finish` pair per top-level `Run` invocation. One YAML per call.
- **`SessionRecorder`** ([session.go:347+](../../internal/agent/session.go#L347)) writes per-call YAMLs under `<session>/calls/NNNN-<agent>.yaml`.
- **Each adapter's `Run`** ([anthropic.go:185+](../../internal/agent/adapters/anthropic.go#L185), [gemini.go:64+](../../internal/agent/adapters/gemini.go#L64), [openai_responses.go](../../internal/agent/adapters/openai_responses.go)) opens a `provider.generate` OTel span and submits the request. No recorder access. When the dispatcher splits, two adapter `Run` calls happen but the YAML records one.

## Resolved design questions

Recorded in chat 2026-05-21; settled before this plan went to implementation.

1. **Adapter encapsulation, not LCD.** The Genkit / LangChain / Vercel AI SDK / BAML pattern (uniform `Adapter.Run`; caller decides whether to split) was considered and rejected. The failure is universal across all three providers we use and silent; LCD would give callers three different undocumented failure modes to know about, not a portable contract. Locutus has one internal caller (the workflow engine); the portability cost LCD optimizes for is zero. DJ-108 already set the direction.

2. **Per-step folder on-disk layout, not flat with sub-suffixes.** Per-step folders (`calls/0002-spec_scout/{step.yaml, 01-reason.yaml, 02-format.yaml}`) scale gracefully to N>2 child calls (future retry / fallback / streaming reassembly). Flat-with-suffixes (`0002a-...`, `0002b-...`) was rejected as a partial fix.

3. **Format pass uses each provider's own fast tier, not cross-provider rotation.** Each adapter handles its provider's fast-tier transient failures with the same retry shape `RunWithRetry` applies elsewhere. The `format_providers:` rotation is retired. If per-adapter retry proves insufficient for transient failures (e.g., Gemini Flash-Lite 503s the original eval surfaced), the reversal criteria in the DJ describe a cross-provider escape hatch behind an env flag — but the default keeps the layering principle intact.

4. **Static per-model overrides for `requiresThinkingSchemaSplit`, not dynamic capability detection.** Anthropic's Models API exposes capability flags but they describe what the model *claims* to support, not the empirical reliability. Per-model overrides in the adapter are the source of truth; the Models API can inform future model additions but doesn't replace the empirical knowledge.

5. **No `SplitMode` opt-out for callers.** Adds surface area for a need that hasn't materialized. Tests that want to see raw provider behavior set `Thinking: off` to bypass the split path explicitly.

6. **Token cost framing.** The naive "rises ~30%" framing is misleading — the baseline of one call was paying full cost for silently broken output. The split converts ~30% additional spend into the difference between near-zero useful output and the actual response. Per-token efficiency improves dramatically; dollar cost rises less than token count because the format pass uses each provider's cheaper fast tier.

## Phase 1 — Workflow wiring through `Dispatcher.Dispatch`

**Goal:** route per-step LLM calls through the existing Dispatcher rather than `RunWithRetry` directly. Smallest, lowest-risk change; isolates the wiring fix from the larger adapter restructure.

**Files expected to change:**

- [internal/agent/workflow.go](../../internal/agent/workflow.go):
  - `WorkflowExecutor[S]` gains a `Dispatcher AgentDispatcher` field. When nil, `invokeOne` constructs one via `NewDispatcher(e.Executor)` lazily.
  - `invokeOne` replaces:

    ```go
    resp, err := RunWithRetry(ctx, e.Executor, def, input, executionRetryConfig())
    ```

    with:

    ```go
    resp, err := dispatcher.Dispatch(ctx, def, input, DispatchOptions{Role: "workflow:" + step.ID})
    ```

  - `dispatcher` is the wired field or the lazy default.
- Callers that construct `WorkflowExecutor` (justify workflow, refine workflow, adopt workflow, spec-gen workflow at [specgen.go](../../internal/agent/specgen.go)) optionally pass an explicit Dispatcher; default construction stays one-line.

**Tests:**

- `TestWorkflowExecutorRoutesThroughDispatcher` — drive `WorkflowExecutor` with a mock dispatcher; assert `Dispatch` was called once per step with `DispatchOptions.Role` set.
- `TestWorkflowExecutorFallsBackToDefaultDispatcher` — construct without a Dispatcher field; assert calls succeed (lazy default fires).
- Existing workflow tests pass without change (their `MockExecutor` doesn't expose `FormatProvider`, so the split path doesn't fire; behavior is identical to today).

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race -skip TestCLISinkRendersAgentLifecycle`.

**Estimated:** 1-2 hours.

**Empirical check:** with Phase 1 alone landed, the dispatcher's `dispatchSplit` would fire for spec-gen agents. Run a smoke `locutus refine goals` against a tiny project; verify `-format` calls appear in the trace folder; verify per-call YAMLs are still flat (no Phase 3 yet). This confirms the wiring works before we restructure the recorder.

## Phase 2 — Per-adapter split logic

**Goal:** move the split decision and execution into each provider adapter. Retire `Dispatcher.dispatchSplit`, `Dispatcher.shouldSplitForFormat`, `FormatProvider`, `canonicalFormatterPrompt`, `format_providers:` config.

**Files expected to change:**

- [internal/agent/adapters/anthropic.go](../../internal/agent/adapters/anthropic.go):
  - New `requiresThinkingSchemaSplit(req Request) bool` — returns true for Opus 4.7 / Sonnet 4.6 / Haiku 4.5 with thinking-on + schema. Doc-comment names the failure mode (`dummy` placeholder), the model affected, and commit `5d15e7b` as the original surface.
  - New `runSplit(ctx context.Context, req Request) (*Response, error)` — calls SDK twice (reasoning then format) and returns merged Response. Reasoning call uses the agent's declared model; format call uses Haiku 4.5 with thinking off, schema set, no tools. The recorder handles emit per Phase 3.
  - `Run` gates: if `requiresThinkingSchemaSplit(req)` returns true, dispatch to `runSplit`; otherwise the existing DJ-108 native-`OutputConfig.Format.Schema` path runs unchanged.
  - `canonicalFormatterPrompt` moves here as a package-private constant (or to a shared `internal/agent/adapters/formatter.go`).
- [internal/agent/adapters/gemini.go](../../internal/agent/adapters/gemini.go) — same shape: `requiresThinkingSchemaSplit` returns true for Gemini 3 Pro Preview / Gemini 3 Flash with thinking-on + schema; `runSplit` calls `Models.GenerateContent` twice. The format pass uses Gemini Flash-Lite (provider's `fast:` tier).
- [internal/agent/adapters/openai_responses.go](../../internal/agent/adapters/openai_responses.go) — same shape for gpt-5 / gpt-5-mini / gpt-5-nano. Format pass uses gpt-5-mini or gpt-5-nano per `fast:` tier config.
- [internal/agent/adapters/formatter.go](../../internal/agent/adapters/formatter.go) (new, optional) — shared `CanonicalFormatterPrompt` constant the three adapters import. Or inline per-adapter if shared file feels overkill.
- [internal/agent/dispatcher.go](../../internal/agent/dispatcher.go):
  - `dispatchSplit`, `shouldSplitForFormat`, `mergeReasoningAndFormat`, `FormatProvider` interface, `canonicalFormatterPrompt` — all removed.
  - `Dispatch` becomes: if `def.Agents.Tools` (ReAct), call `dispatchReAct`; else call `dispatchOnce`. No split branch.
- [internal/agent/executor.go](../../internal/agent/executor.go):
  - `FormatPreferences() []ModelPreference` method retired.
- [internal/agent/mock_llm.go](../../internal/agent/mock_llm.go):
  - `FormatPrefs []ModelPreference` field + `FormatPreferences()` method retired.
- [internal/agent/model_config.go](../../internal/agent/model_config.go):
  - `FormatProviders []string` field on `ModelConfig` retired; `FormatProviderOrder()` method retired; validation that `format_providers` entries resolve to a `fast` tier — retired.
- [internal/agent/models.yaml](../../internal/agent/models.yaml):
  - `format_providers:` block removed. Each provider's existing `fast:` tier is what the adapter uses.

**Tests:**

Per-adapter:

- `TestAnthropicSplitsForThinkingPlusSchema` — request with `Thinking: on, OutputSchema: <X>` against a recorded SDK fixture; assert the adapter made two SDK calls (one with thinking, one with schema-only).
- `TestAnthropicNoSplitForThinkingOff` — request with `Thinking: off, OutputSchema: <X>` → one SDK call, schema enforced via `OutputConfig.Format.Schema` (existing DJ-108 path).
- `TestAnthropicNoSplitForNoSchema` — request with `Thinking: on, OutputSchema: nil` → one SDK call, no schema.
- `TestAnthropicSplitFailurePropagates` — make the format SDK call fail; assert the whole `Run` returns the error.
- Same shape for `gemini.go` and `openai_responses.go`.

Cross-adapter:

- Existing `dispatcher_split_test.go` is retired (its assertions move to the per-adapter tests above).
- `TestDispatcherHasNoSplitPath` — defensive assertion that `Dispatcher.shouldSplitForFormat` / `dispatchSplit` no longer exist (compile-time via removal; runtime guard via the dispatcher's surface).
- Eval test [output_formatter_eval_test.go](../../internal/agent/output_formatter_eval_test.go) restructures: per-provider matrix rather than cross-provider rotation. Each provider's adapter exercised through its own format pass.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race -skip TestCLISinkRendersAgentLifecycle`.

**Empirical check:** smoke `locutus refine goals` on a tiny project after Phase 2 lands. Compare per-call YAMLs to Phase-1-only baseline — should see two adapter-emitted calls per workflow step that had thinking+schema, both attributed to the same agent. The folder structure is still flat at this phase (Phase 3 introduces the per-step folders).

**Estimated:** 4-6 hours per adapter (3 adapters); total 12-18 hours.

## Phase 3 — Recorder relocation + per-step folder layout

**Goal:** move `SessionRecorder.Begin/Finish` into the adapter layer; restructure `LoggingExecutor` as a per-agent-step grouper; change on-disk per-call YAMLs to per-step folders with a parent `step.yaml`.

**Files expected to change:**

- [internal/agent/session.go](../../internal/agent/session.go):
  - New context helpers: `WithSessionRecorder(ctx, r) context.Context`, `SessionRecorderFromContext(ctx) *SessionRecorder`, `WithParentCallID(ctx, id) context.Context`, `ParentCallIDFromContext(ctx) string`. Mirror the existing `WithRole` / `WithAgentID` / `WithCallTag` pattern at [session.go:746-748](../../internal/agent/session.go#L746-L748).
  - `LoggingExecutor.Run` reshapes:
    - Opens a parent `SessionRecorder.BeginStep(role, agentID, callTag, def, input, started)` — new method that writes the parent `step.yaml` placeholder.
    - Plumbs the parent call ID and recorder onto ctx via `WithParentCallID` and `WithSessionRecorder`.
    - Delegates to `inner.Run` (which now reaches the adapter, which uses the recorder for per-SDK-call records).
    - Calls `parentHandle.Finish(mergedOutput, err)` which finalizes the step.yaml (sums tokens across children, computes duration, lists child call ids).
  - `SessionRecorder.Begin` gains an optional `parentCallID` parameter (or reads it from ctx); writes the child YAML under the parent's folder.
  - On-disk layout: `calls/<step-index>-<agent-id>/step.yaml + <call-index>-<role>.yaml`. The `<role>` segment names the sub-call's purpose (`reason`, `format`, `single` for non-split, future `retry-1`, etc.).
- [internal/agent/adapters/anthropic.go](../../internal/agent/adapters/anthropic.go), [gemini.go](../../internal/agent/adapters/gemini.go), [openai_responses.go](../../internal/agent/adapters/openai_responses.go):
  - Each adapter's `Run` (and `runSplit`) gets the recorder from ctx via `SessionRecorderFromContext`. For each SDK call, opens its own `recorder.Begin(role, agentID, def, input, started, parentCallID)` and calls `handle.Finish(response, err)` when the SDK call returns.
  - The `role` value on the child record names the SDK call's purpose. For non-split: `single`. For split: `reason` and `format`.
- Replay / inspection tools that walk per-call YAMLs (eval / debug surfaces):
  - Updated to walk the per-step folder structure. Old-flat-layout sessions are read with a back-compat shim during the transition.

**Tests:**

- `TestLoggingExecutorOpensParentStepRecord` — wraps a `MockExecutor` (no adapter); asserts the parent step.yaml is written even when no child calls fire (e.g., agent returns immediately).
- `TestSessionRecorderPerStepFolderLayout` — full integration: workflow → dispatcher → adapter; assert the resulting on-disk layout matches `calls/0001-spec_scout/{step.yaml, 01-single.yaml}` for a non-split call, `calls/0002-spec_scout/{step.yaml, 01-reason.yaml, 02-format.yaml}` for a split call.
- `TestParentStepYAMLAggregatesTokens` — after both child calls finish, the parent step.yaml's token fields are the sum.
- `TestAdapterEmitsChildRecordPerSDKCall` — exercise `anthropic.runSplit`; assert two `recorder.Begin` calls were made with the right roles.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race -skip TestCLISinkRendersAgentLifecycle`.

**Empirical check:** smoke `locutus refine goals` on a tiny project after Phase 3 lands. Verify the new on-disk layout matches the design: per-step folders with parent step.yaml + child call YAMLs. Verify token sums in step.yaml match the sum of the child YAMLs.

**Estimated:** 6-8 hours.

## Phase 4 — Documentation + cleanup

**Goal:** update CLAUDE.md and docs/agent-conventions.md to reflect the new layering. Create the operational debugging-traces guide. Retire dead code paths.

**Files expected to change:**

- [CLAUDE.md](../../CLAUDE.md):
  - New paragraph in the LLM section naming the layering invariant: "the workflow expresses intent; the dispatcher orchestrates retry/rotation/observability; the adapter handles provider-specific mechanics including any internal multi-call workaround. Observability follows the provider-call boundary on both surfaces (OTel and YAML)."
  - Reference DJ-130 as the governing decision.
  - "Sources of Truth" section gains a line referencing the new debugging-traces guide.
- [docs/agent-conventions.md](../../docs/agent-conventions.md):
  - §6 ("Extended thinking on for agents whose output is structured + short") gains a note: the adapter handles this case automatically now; the convention is informational rather than load-bearing for agent authors. Authors still see the failure mode in the convention doc as forensic context for understanding why the adapter splits.
- [docs/debugging-traces.md](../../docs/debugging-traces.md) (new): operational guide for using session traces and OTel data to debug council failures. Scope:
  - Session directory anatomy (`session.yaml`, `trace.jsonl`, `calls/`).
  - Per-step folder layout (post-DJ-130): `<step-index>-<agent-id>/{step.yaml, NN-<role>.yaml}`. What each file carries; when sub-calls fire (single vs split adapter behavior).
  - Finding the right session for a failure (by timestamp, command, exit signal; via history events `convergence_failed` / `convergence_stuck` / `convergence_revision_capped` / `decision_locked`).
  - Extracting the structured response from a YAML — the `response:` field's JSON-in-YAML escape pattern (and the grep/sed one-liners that work around it).
  - Common failure patterns and where to look:
    - "Output is thin / missing content" → thinking vs response divergence (the DJ-130 motivating case).
    - "Convergence failed" → walk scout iterations; check axes_open + concerns; check dispatch agent absence.
    - "Loop capped on revision" → `decision_revised` events; walk the alternatives chain.
    - "Schema validation rejected" → degenerateXxxValidator surfaces; trace back to the prompt.
  - OTel-YAML correlation via `span_id` on per-call YAMLs and OTel span trees.
  - Useful one-liners (grep / sed / jq patterns).
  - What NOT to do (don't edit per-call YAMLs — `.borg/spec/` is source of truth; don't infer convergence from trace.jsonl alone — consult `.borg/history/` events too).
- Final pass on dead code: any remaining `FormatProvider` / `format_providers` / `dispatchSplit` references that survived Phase 2 → removed. Grep-and-purge.

**Tests:** `go test ./... -count=1 -race -skip TestCLISinkRendersAgentLifecycle` clean; `go vet ./...` clean.

**Estimated:** 2-4 hours (1-2h for CLAUDE.md / agent-conventions / cleanup; 1-2h for the new debugging-traces guide).

## Phase 5 — Validation against winplan re-run

**Goal:** the same winplan project that triggered DJ-130 converges with a complete spec (features, strategies, decisions all populated).

**Process:**

1. Build the DJ-130 binary: `go build -o ~/go/bin/locutus-dj130 .`.
2. Run `locutus-dj130 update --offline --reset` against winplan.
3. Run `locutus-dj130 refine goals` against winplan with default 5-iteration budget.
4. Compare against the fifth winplan run at [`/Users/chetan/projects/winplan/.locutus/sessions/20260521/0055/07-b8e485/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260521/0055/07-b8e485/):
   - Did the loop converge? In how many iterations?
   - Final spec: how many features, strategies, decisions?
   - Per-step folder structure present in calls/?
   - Did scout iter-0 emit strategies in `new_nodes`? (Cross-reference the scout's structured response, not just thinking content.)
   - Token / wall-clock cost vs. the prior run — within the predicted ~15-20% session-time bound?

**What success looks like:** `locutus refine goals` exits with `converged: true` on the winplan project with a spec that includes ≥2 features and ≥2 strategies. Per-call traces show per-step folders with reason/format sub-calls for thinking-on schema agents. Scout iter-0's structured response carries the strategies its thinking drafted.

**What partial success looks like:** convergence with complete features/strategies but with one or more provider-side transient failures (e.g., Gemini Flash-Lite 503s) handled via per-adapter retry rather than cross-provider rotation. If the failure rate is materially higher than under the prior cross-provider rotation, reversal criterion (a) triggers.

**What failure looks like:** convergence still produces 0 strategies → the adapter split isn't firing as expected; diagnose against the per-step folder traces. OR convergence fails due to provider-side errors the per-adapter retry can't recover → reversal criterion (a) triggers and the cross-provider escape hatch is wired.

**Verification:** the winplan session traces are durable evidence. No automated assertion here.

**Estimated:** 1 hour of compute + manual review.

## Phase 6 — DJ-130 status flip + plan marked DONE

**Goal:** DJ-130 flips from `proposed` to `shipping` once Phase 5 validation passes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-130 status `proposed` → `shipping (Phases 1-5 landed YYYY-MM-DD)`.
- This plan file marked DONE.

**Verification:** `go test ./... -count=1 -race -skip TestCLISinkRendersAgentLifecycle` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 25-35 hours single-stranded across 3-5 sessions

## Pointers a fresh session should follow before resuming

1. Read DJ-130 in full ([docs/DECISION_JOURNAL.md#dj-130](../../docs/DECISION_JOURNAL.md#dj-130)). It's the authoritative design; this plan is progress tracking.
2. Read DJ-108 ([docs/DECISION_JOURNAL.md#dj-108](../../docs/DECISION_JOURNAL.md#dj-108-anthropic-native-structured-output--adaptive-thinking)) for the direction DJ-130 extends.
3. Read commit `5d15e7b` (the original split-in-Dispatcher pattern). The Phase 2 deletion sweep needs to remove every reference; this commit is the inventory.
4. Read the fifth winplan trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260521/0055/07-b8e485/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260521/0055/07-b8e485/) end-to-end before Phase 5. The iter-0 spec_scout YAML shows the thinking-content vs structured-response divergence; the rest of the trace shows the 0-strategy outcome.
5. Each phase has an empirical check. **Run them.** Static unit tests on adapter logic aren't sufficient evidence that the split actually fires correctly against the real provider API — the smoke runs are load-bearing.

## What is explicitly out of scope

- **Streaming response handling.** Adapter-internal multi-call patterns generalize to streaming (chunk accumulation, etc.) but that's a separate workstream. DJ-130 covers the split-call case; streaming is a future DJ.
- **Cross-provider escape hatch for the format pass.** Reversal criterion (a) describes an env-flagged opt-in if same-provider retry proves insufficient. Not implemented in the initial landing; added only if Phase 5 validation surfaces a need.
- **`AgentDef.SplitMode` caller opt-out.** Adds surface area for a need that hasn't materialized. Tests that want raw provider behavior set `Thinking: off`.
- **Dynamic capability detection via provider APIs.** Static per-model overrides in each adapter are the source of truth for the initial landing. The Models API can inform future additions.
- **OTel span shape changes.** OTel already records at the provider-call boundary; DJ-130 doesn't change span structure, just brings YAML recording into alignment.
- **Replay-tool migration of historical session traces.** Per-call YAMLs in existing session directories under the flat naming aren't migrated. Replay tools handle both layouts; old sessions remain readable.
