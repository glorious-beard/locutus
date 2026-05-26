# DJ-123 — In-Flight Spec Search Implementation Plan

> **Governing DJ:** [DJ-123: In-Flight Spec Search for Council Agents (Extends DJ-094 / DJ-116 to Mid-Council State)](../../docs/DECISION_JOURNAL.md). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes that don't belong in the DJ.
>
> **Status:** SUPERSEDED by DJ-134 on 2026-05-23 + DJ-135 on 2026-05-26. The Swappable wrapper stack retires in DJ-134 (the in-flight-vs-settled distinction is now an entry-tag in the unified SpecStore); the MCP `spec_get` / `spec_search` tools exposed in DJ-135 inherit the in-flight-aware reads. Historical: designed; implementation not started.
> **Surface area:** `internal/search/` (~150 LOC for the in-memory adapter) + `internal/agent/spec_tools.go` wiring + 2 elaborator prompt rewrites + 1 reconciler prompt addition + `internal/agent/workflow_spec_generation.go` plumbing + eval-test fixture extension.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Each phase verifies independently before moving on. **Prompt edits walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end before drafting** per [[feedback-agent-conventions-checklist-first]] — the DJ-122 spec_gate.md churn was the cost of treating that doc as background knowledge.

## Why this plan exists

DJ-123 extends the existing `spec_search` tool surface to operate against the in-flight `RawProposal` during a spec-council run. The total LOC is modest, but the work touches the search package (new in-memory adapter), the agent package (tool registration + workflow wiring), the scaffold (two elaborator prompts + a reconciler prompt addition), and the eval test (fixture extension). Doing it as one big diff would conflate the search infrastructure, the agent wiring, and the prompt work — three separable concerns each worth verifying on its own.

The plan keeps the existing on-disk `spec_search` path unchanged; the in-flight backing store is purely additive. The persisted index at `.locutus/spec_index/` is not touched.

## Reference state (before adoption)

- **On-disk Bluge index:** [internal/search/build.go](../../internal/search/build.go) builds an index from `.borg/spec/` via `buildAllDocuments`. Per-kind document builders (`featureDoc`, `strategyDoc`, `decisionDoc`, `bugDoc`, `approachDoc`) populate a shared field set (`fieldID`, `fieldIDTokens`, `fieldKind`, `fieldTitle`, `fieldSummary`, `fieldBody`, ...). Title-weighting + analyzer config lives here.
- **Agent tool surface:** [internal/agent/spec_tools.go](../../internal/agent/spec_tools.go) registers `spec_search` as `ToolNameSpecSearch`. The tool's input shape (`SpecSearchInput`) carries `Query`, `Kind?`, `Limit?`, `Explain?`. The output carries per-hit `FieldMatch` diagnostics per DJ-117. The tool descriptor (MCP wire format) is unchanged across the DJ-123 work — only the backing store changes.
- **Council state mutations:** [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) `mergeElaboratedFeatures`, `mergeElaboratedStrategies`, `mergeRevisedNodes`, and `mergeReconciledProposal` all touch `state.RawProposal`. These are the trigger points for re-indexing.
- **Eval coverage:** [internal/agent/spec_search_eval_test.go](../../internal/agent/spec_search_eval_test.go) is build-tagged `eval` and runs the LLM-interpretation eval against a fixed corpus. The eval asserts mid-tier portability of the tool's prompt + output shape. Extending it to the in-flight case validates the same property for the new backing store.

## Resolved design questions

Three open questions surfaced during the DJ chat (2026-05-14); resolved before this plan went to implementation.

1. **Reuse the existing field set + analyzer config from `internal/search/build.go`, not a fresh one for in-memory.** The shape of a strategy/decision/feature is the same whether it's on disk or in `RawProposal` JSON. Defining a parallel field set would split the search semantics across two implementations and double the cost of any future field addition (e.g., adding a `tags` field to decisions). The in-memory adapter consumes the same `featureDoc` / `strategyDoc` / etc. builders, parameterized by where the source data comes from.

2. **Re-index on every `RawProposal` mutation, no debounce.** Bluge in-memory writes for a ~50-strategy proposal are sub-millisecond. A debounce layer adds bookkeeping (dirty flags, deferred rebuild, cache-coherence questions) that's pure overhead at this corpus size. If the corpus grows past O(few hundred) strategies in the future, revisit.

3. **The in-flight index is council-scoped, not process-scoped.** It lives on the `WorkflowExecutor[PlanningState]` instance, gets initialised when the executor starts, gets garbage-collected when the run ends. There's no global registry, no per-process cache, no on-disk persistence. Each council run sees only its own in-flight proposal. This also means concurrent council runs in the same process (currently not supported, but plausible) get isolated indexes — no cross-contamination.

## Phase 1 — In-memory Bluge index adapter

**Goal:** an in-memory Bluge index that mirrors the on-disk one's document shape, built from `state.RawProposal` JSON. Existing on-disk path stays bit-for-bit unchanged.

**Files expected to change:**

- New [internal/search/inflight.go](../../internal/search/inflight.go) — `InFlightIndex` struct wrapping a bluge.OnlineWriter (in-memory store). Method `Rebuild(rawProposal string) error` parses the JSON, walks features + strategies + decisions, and produces `bluge.Document`s using the existing `featureDoc` / `strategyDoc` / `decisionDoc` builders. Method `Search(input SpecSearchInput) (SpecSearchOutput, error)` mirrors the on-disk index's search method — same query parser, same field weights, same field-match diagnostic path. The two indexes implement a common `SpecSearchBackend` interface (small — `Search(input)` + `Rebuild(...)` for the in-flight variant).
- Update [internal/search/build.go](../../internal/search/build.go) — extract `featureDoc`, `strategyDoc`, etc. behind a small builder type they can share with the in-flight path. Existing on-disk callers go through the same builders with no behavior change.
- New [internal/search/inflight_test.go](../../internal/search/inflight_test.go) — tests:
  - `TestInFlightIndexBuildsFromRawProposal` — feed a fixture JSON, query a known term, assert hit.
  - `TestInFlightIndexRebuildReplacesPriorState` — sequential rebuilds; old hits are gone after a new payload.
  - `TestInFlightIndexEmitsFieldMatchDiagnostics` — DJ-117 per-field diagnostics fire for the in-flight backing.
  - `TestInFlightIndexHandlesMalformedJSON` — empty or malformed input doesn't panic, returns no hits.

**Verification:** `go build ./... && go vet ./... && go test ./internal/search/... -count=1 -race`.

**Estimated:** 2-3 hours.

## Phase 2 — `spec_search` tool wiring picks the backing store

**Goal:** the agent-facing `spec_search` tool can be wired to either backing store at tool-registration time, with the same input/output shape.

**Files expected to change:**

- [internal/agent/spec_tools.go](../../internal/agent/spec_tools.go) — the existing `spec_search` registration takes a `search.Backend` (or equivalent interface) at construction time. The on-disk path is one implementation; the in-flight path is the new one. Tool descriptor (input shape, output shape, MCP wire format) is unchanged.
- [internal/agent/spec_tools_test.go](../../internal/agent/spec_tools_test.go) — `TestSpecSearchToolUsesProvidedBackend` confirms the tool dispatches to whichever backend was registered.
- Optional: a thin factory in [internal/search/](../../internal/search/) that picks the backend based on whether a `RawProposal` is supplied to the spec-search dispatcher's constructor.

**Verification:** `go test ./internal/agent/... -count=1 -run 'TestSpecSearch'`. Existing on-disk tests continue passing without modification.

**Estimated:** 1-2 hours.

## Phase 3 — Council wiring: in-flight index lives on the WorkflowExecutor

**Goal:** during a `GenerateSpec` run, the in-flight index is constructed, registered as the backing store for `spec_search`, and rebuilt on every `RawProposal` mutation.

**Files expected to change:**

- [internal/agent/specgen.go](../../internal/agent/specgen.go) — `GenerateSpec` (or its workflow setup) constructs an `InFlightIndex` and registers it with the `spec_search` tool for this council run. The on-disk index (used by other verbs) is not consulted during the council.
- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) — `mergeElaboratedFeatures`, `mergeElaboratedStrategies`, `mergeRevisedNodes`, and `mergeReconciledProposal` each call the in-flight index's `Rebuild(state.RawProposal)` at the end of their merge work. Wrap this in a small helper to keep the call sites uniform.
- [internal/agent/workflow_spec_generation_test.go](../../internal/agent/workflow_spec_generation_test.go) — `TestInFlightIndexRebuiltOnRawProposalMerge` confirms the rebuild fires from each of the four merge functions; integration test that runs a minimal council with `spec_search` invoked from a fake elaborator returns hits against the latest `RawProposal`.

**Verification:** new tests green; existing council tests continue passing.

**Estimated:** 2-3 hours.

## Phase 4 — Elaborator prompt updates (the load-bearing piece)

**Goal:** `spec_strategy_elaborator.md` and `spec_feature_elaborator.md` direct the agent to search the in-flight proposal for existing commitments on the foundational axis they're elaborating, and to cite hits in their rationale.

**Process discipline (mandatory):** Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end **before drafting**. Audit the draft against each numbered anti-pattern after writing. Per [[feedback-agent-conventions-checklist-first]] — the DJ-122 spec_gate.md churn was the cost of skipping this step.

**Files expected to change:**

- [internal/scaffold/agents/spec_strategy_elaborator.md](../../internal/scaffold/agents/spec_strategy_elaborator.md) — add a section (positioned in the task body, not at the bottom — §3 of agent-conventions) that frames the search behavior positively. Approximate shape:
  > **Before committing to a foundational choice**, query the proposal for existing commitments on the same axis. Use both the domain term and the technical term as separate queries (e.g., `auth provider` and `identity management`; `vector tile provider` and `Mapbox`). Cite any hits in your rationale and state whether you align with the existing commitment or supersede it with named justification.
  Refine wording during drafting. **No anti-pattern list.** No "do not pick a different vendor" rule. Positive direction; constraint travels via the tool's own description.
- [internal/scaffold/agents/spec_feature_elaborator.md](../../internal/scaffold/agents/spec_feature_elaborator.md) — same shape, scaled to feature-level concerns (the feature's foundational decisions should align with the project's foundational strategies, search-discoverable via the same tool).
- [internal/scaffold/agents/spec_reconciler.md](../../internal/scaffold/agents/spec_reconciler.md) — minor addition acknowledging `spec_search` availability for verifying inline-decision dedup hypotheses. Reconciler usage is exploratory in v1; the elaborators are the primary consumers.
- Tool description on `ToolNameSpecSearch` in [internal/agent/spec_tools.go](../../internal/agent/spec_tools.go) — verify the description reads cleanly when read by the elaborator at tool-use time; positive framing of the in-flight case ("the tool searches the *current* spec proposal during a council run, and the persisted spec graph otherwise").
- `cmd/agent_md_guard_test.go` (or wherever the every-agent thinking/format guards live) — extend the lint suite if it has a "no anti-pattern wording" check; add one if not. Catching this pattern automatically prevents another DJ-122-style cycle.

**Verification:** existing council tests (which mock the elaborator agent calls) continue passing — the prompt change doesn't affect mock-driven flows. The behavioral change is verified by Phase 6.

**Estimated:** 1-2 hours (mostly the audit pass against agent-conventions; the prose itself is short).

## Phase 5 — Instrumentation (search-call count + empty-result rate per session)

**Goal:** each council run logs how many `spec_search` calls each elaborator made and what fraction returned zero hits. The DJ-123 reversal criterion (a) names a >25% empty-result rate as the threshold past which BM25-only stops being viable; the instrumentation is how we measure that.

**Files expected to change:**

- [internal/agent/spec_tools.go](../../internal/agent/spec_tools.go) or wherever the `spec_search` dispatch wraps the call — emit a span event or session-trace record per call carrying `(query, hit_count, took_ms)`.
- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) — at the end of each council run, log aggregate stats: total search calls, calls-by-agent, empty-result rate. The session trace already exists; this rides on it.
- [internal/agent/workflow_spec_generation_test.go](../../internal/agent/workflow_spec_generation_test.go) — `TestInFlightSearchInstrumentationCaptured` runs a minimal council and asserts the per-call records are present in the session-trace output.

**Verification:** new test green; instrumentation visible in a smoke run's session trace.

**Estimated:** 1 hour.

## Phase 6 — Validation against winplan re-run

**Goal:** the same winplan project that exposed the cross-iteration recurrence problem (the run that hit budget at iter-4) converges inside budget with DJ-123 in place. The empty-result rate from Phase 5 is below 25% per the reversal criterion.

**Process:**

1. Run `locutus update --offline --reset` against winplan to refresh the elaborator + reconciler prompts.
2. Run `locutus refine goals` against winplan with default budget (5).
3. Compare against the DJ-122-only run at `/Users/chetan/projects/winplan/.locutus/sessions/20260514/1753/54-c319a3/`:
   - Did the cross-iteration contradictions (IdP, tenancy, runtime, map tile, cost ceiling) resolve and stay resolved?
   - Did the loop terminate inside budget (converged: true), or did it still hit the budget-exhausted path?
   - Empty-result rate per Phase 5 instrumentation — below the 25% reversal threshold?
4. Capture the new session trace as the durable comparison artifact alongside the older one.

**What "success" looks like:** the loop terminates with `converged: true` in fewer than 5 iterations against winplan, the in-flight search hit rate is meaningful (>75% non-empty), and the failure modes that hit budget last time are absent. **What "partial success" looks like:** the loop converges but within-iteration parallel-fanout contradictions are still visible — that's expected, the within-iteration follow-up DJ becomes the next move. **What "failure" looks like:** convergence rate didn't improve, OR the empty-result rate exceeded 25% (signal that BM25 isn't enough, hybrid embedding becomes the follow-up).

**Verification:** the winplan session trace is the durable evidence. No automated assertion here — Phase 6 is the manual evaluation step DJ-123's reversal criteria depend on.

**Estimated:** 30 minutes of compute + manual review.

## Phase 7 — Documentation + cleanup

**Goal:** DJ-123 status flips from `proposed` to `shipping`; this plan marked DONE; any agent-conventions discoveries from Phase 4 fold back into [docs/agent-conventions.md](../../docs/agent-conventions.md) if they uncovered new failure modes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-123 status `proposed` → `shipping (Phases 1-6 landed YYYY-MM-DD; within-iteration parallel-fanout follow-up tracked separately)`.
- This plan file marked DONE.
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — if Phase 4 surfaced any new prompt-engineering trap worth documenting, fold it in. Likely not necessary; the existing doc covers what we know.

**Verification:** `go test ./... -count=1 -race` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 8-12 hours single-stranded across 2-3 sessions.

## Pointers a fresh session should follow before resuming

1. Read [DJ-123](../../docs/DECISION_JOURNAL.md) in full. It's the authoritative design; this plan is progress tracking.
2. Read the predecessor chain: [DJ-094](../../docs/DECISION_JOURNAL.md) (the spec-lookup tool surface this builds on), [DJ-116](../../docs/DECISION_JOURNAL.md) (the on-disk Bluge index that the in-flight one mirrors), [DJ-117](../../docs/DECISION_JOURNAL.md) (per-field diagnostics this preserves), [DJ-122](../../docs/DECISION_JOURNAL.md) (the convergence loop this serves).
3. Read [internal/search/build.go](../../internal/search/build.go) end-to-end before adding the in-flight adapter. The field set + analyzer choices there are load-bearing; the in-flight path reuses them exactly.
4. Read [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) — Phase 3's merge-callback wiring touches exactly the four `mergeXxxRawProposal` functions.
5. Before Phase 4, **re-read [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end**. Per [[feedback-agent-conventions-checklist-first]], do not rely on remembered conventions. After drafting, audit each prompt section-by-section against the numbered anti-patterns.
6. Phase 6 is the validation step. Don't ship the DJ-123 entry as `shipping` until winplan re-runs cleanly. The reversal criteria are the empirical bar; the instrumentation from Phase 5 is how we measure them.
7. Per the no-back-compat-until-self-hosting posture, this is additive — no migration shims. New binaries write fresh prompt files on `update --reset`. Old binaries continue to operate as before (no in-flight search, no elaborator search behavior).

## What is explicitly out of scope

- **Within-iteration parallel-fanout contradictions.** The case where two iter-N elaborators querying simultaneously each get the iter-(N-1) snapshot and still pick opposite values. DJ-123 closes cross-iteration recurrence; this is the within-iteration version. The fix candidates (sequential revise, pre-revise coordinator, cross-strategy reconciler check) wait on data from the DJ-123 winplan re-run to decide which is load-bearing.

- **Embedding-cosine or hybrid search.** Bluge supports vector fields natively, so adding embedding is additive infrastructure. The DJ-123 design commits to BM25-with-title-weighting in v1 specifically so the Phase 5 instrumentation can tell us whether hybrid is actually needed. If the empty-result rate sits below 25%, hybrid is unnecessary; if it spikes above, the follow-up DJ is well-motivated.

- **Recurrence-key normalization** (token-set match across iterations to catch the gate's axis-name drift). Separate small follow-up that's complementary to DJ-123 but not blocked by it; could land independently any time. Tracked separately.

- **Cross-strategy contradiction detection inside the reconciler.** Catches contradictions that slip through in-flight search. Complementary to DJ-123, not a substitute, and depends on what DJ-123's first real run shows about whether the elaborator-side fix is enough.
