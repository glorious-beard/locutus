# DJ-134 — Unified In-Process Spec Store

> **Governing DJ:** [DJ-134](../../docs/DECISION_JOURNAL.md#dj-134-unified-in-process-spec-store-supersedes-the-swappable-wrapper-stack-eliminates-swappablespecsearch--swappablespeclistmanifest--swappablespecget--fsspecprovider--inflightspecstore-single-mutex-protected-store-backs-both-on-disk-persistence-and-in-flight-council-mutations-prepares-the-read-surface-for-tool-call-driven-spec-mutation). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** designed; implementation not started.
> **Predecessors:** commit [`52e16fd`](https://github.com/chetan/locutus/commit/52e16fd) (the proximal wrapper-pass-through fix DJ-134 supersedes); [DJ-123 Phase 3](../../docs/DECISION_JOURNAL.md#dj-123) (SwappableSpecSearch — retires); [DJ-125 Phase 3](../../docs/DECISION_JOURNAL.md#dj-125) (InFlightSpecStore overlay — retires); commit [`eb51a14`](https://github.com/chetan/locutus/commit/eb51a14) (manifest `Origin` / `Working` flags — carry forward as per-entry fields); [DJ-130](../../docs/DECISION_JOURNAL.md#dj-130) (LoggingExecutor wrapper — its `SpecSearch / SpecListManifest / SpecGet` pass-through methods retire); [DJ-088](../../docs/DECISION_JOURNAL.md#dj-088) (all-or-nothing council-output semantics — preserved via `store.Discard` on failed councils).
> **Successors (anticipated):** [DJ-127](../../docs/DECISION_JOURNAL.md#dj-127) write-tool surface lands on the unified store; tool-call-driven spec_create / spec_update / spec_delete handlers call `store.Put` / `store.Update` / `store.Delete` directly.
> **Sequencing:** ships after DJ-133 lands and validates. DJ-133's migration tool and DJ-134's store-collapse don't conflict but the DJs are independent — running them as separate, focused changes avoids a sprawling refactor commit that mixes axis-as-ID migration with internal-types collapse.
> **Surface area:** internal types only — no on-disk format change, no prompt change, no agent-config change, no CLI/MCP surface change. The change is concentrated in `internal/agent/` plus a small `cmd/llm.go` wiring shift.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Cite DJ-134 + the precise constraint in chat before touching `internal/agent/` files per `[[feedback-cite-djs-before-spec-work]]`. No agent prompt changes are expected; the `docs/agent-conventions.md` checklist isn't applicable here, but `[[feedback-council-doc-maintenance]]` still binds — `docs/council.md`'s "Spec-lookup tools" section updates in Phase 4.

## Why this plan exists

The seven trace folders across May 22–23 2026 winplan re-runs and the chat critique of 2026-05-23 surfaced a latent design flaw that had been costing tokens for weeks:

- The `*Executor → LoggingExecutor → NotifyingExecutor` wrapper chain meets a swap-based hot-replace primitive (`SwappableSpecSearch / SwappableSpecListManifest / SwappableSpecGet`) via type assertions against the outer wrapper. When a wrapper forgets to forward a swappable (the LoggingExecutor + NotifyingExecutor case until commit [`52e16fd`]), the assertion silently fails, the swap silently no-ops, and every spec_* tool call falls through to the on-disk default for the council's entire lifetime.
- The proximal fix (pass-through methods on both wrappers) closes the bug today. But the underlying design — nine types for one logical capability, an in-memory overlay that snapshots the persisted graph by reference at council start, an explicit swap-and-restore lifecycle that ties read-surface mutation to a non-data-bearing wrapper layer — keeps the next bug latent.
- Tool-call-driven spec mutation (DJ-127) is on the roadmap. Layering it on the current read stack would require a fourth swappable, a fourth pass-through method per wrapper, a fourth provider interface, and answering "does the overlay accept writes?" Every dimension the read stack already has, doubled.

DJ-134 collapses the read-side spec surface to one type — `SpecStore` — that holds the full spec graph plus per-entry origin and working tags, protected by one mutex, with `.borg/spec/` as its persistence backing rather than a parallel data source. Reads serve every consumer (RAG tools, CLI verbs, MCP); writes serve every producer (council merges, FS-persistence, future tool-call mutations). The wrapper-pass-through bug becomes structurally impossible because there's no swappable to forward.

## Reference state (before DJ-134 starts)

- **Swappable wrappers:** [SwappableSpecSearch](../../internal/agent/spec_search_swap.go) (~110 lines), [SwappableSpecListManifest + SwappableSpecGet + fsSpecProvider + InFlightSpecStore](../../internal/agent/spec_inflight_swap.go) (~620 lines), plus the executor wrapper pass-through methods on [LoggingExecutor](../../internal/agent/session.go) and [NotifyingExecutor](../../internal/agent/notifying_executor.go) — nine types for one logical capability.
- **Swap helpers** in [internal/agent/specgen.go](../../internal/agent/specgen.go) at `specSearchSwap / specListManifestSwap / specGetSwap` (lines ~370-404). Type-assertions against `AgentExecutor` lookup the inner swappable; they're the silent-failure point if a wrapper forgets to pass through.
- **Wiring** in [cmd/llm.go](../../cmd/llm.go) at `registerSpecToolsOnce` (lines ~188-228). Three `Set` calls (`SetSpecSearch`, `SetSpecListManifest`, `SetSpecGet`) wire three swappables into the executor; one `RegisterSpecTools` call wires them into the tool registry.
- **State plumbing** in [PlanningState.InFlightSpecStore](../../internal/agent/state.go) and `PlanningState.InFlightIndex` (the council holds direct pointers to both for its merge-driven updates).
- **Council lifecycle** in [generateSpecWithWorkflow](../../internal/agent/specgen.go) at lines ~515-595: builds the InFlightSpecStore, calls `Swap(inflightStore)` on both `listSwap` and `getSwap`, builds the `InFlightIndex` and calls `Swap(inflight)` on `searchSwap`, deferred-restores all three at council end.
- **Merge integration** in [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go) at `rebuildInFlightIndex` (line ~1510) and `setWorkingSignals` (line ~1547) — every RawProposal-mutation calls one or both of these to keep the overlay current.
- **Search projection** in [internal/search/inflight_index.go](../../internal/search/inflight_index.go) — `search.InFlightIndex` is a per-council Bluge index over the in-memory RawProposal that supports `Rebuild(rawProposal string)` re-indexing on every merge.
- **The fix** in commit [`52e16fd`](https://github.com/chetan/locutus/commit/52e16fd) (the pass-through methods on LoggingExecutor + NotifyingExecutor) + the locking test [executor_swap_passthrough_test.go](../../internal/agent/executor_swap_passthrough_test.go). DJ-134 retires both — the pass-through methods aren't needed (no swappable), the test's surface area no longer exists.

## Resolved design questions

Recorded in chat 2026-05-23, locked in DJ-134's text:

1. **One store, not two-with-an-overlay.** Settled-vs-in-flight is preserved as a **per-entry tag** on entries inside the single store, not as two stores with a wrapper deciding which to read. The manifest renderer reads `entry.Origin` from the node it already has in hand.

2. **The mutex is fine.** Single `sync.RWMutex`. Council writes come from one goroutine (single producer); tool reads come from many (multiple consumers); RWMutex is the right primitive. Empirically the existing swappable-wrappers' RWMutexes carry no observed contention — the unified store inherits that profile.

3. **Writes durably land at council teardown, not per-merge.** The store mutates in memory throughout the council run; only at successful convergence does a single `Persist()` call write proposed-and-promoted nodes to disk. Failed councils discard the in-memory delta; the FS stays at its pre-council state. Preserves DJ-088's all-or-nothing council-output semantics.

4. **The Bluge index is a write-through projection, not a separate swap.** Every `store.Put` updates the index synchronously under the same mutex. `search.InFlightIndex` retires entirely; the production-side `search.Index` becomes the index the store maintains.

5. **Tool-call write surface (DJ-127) lands on the unified store.** Tool-driven writes outside a council go to `Persist()` (single-write durability); tool-driven writes inside a council go to the same in-memory tag-as-proposed pool merge-driven writes use and follow the same convergence-or-discard lifecycle.

6. **The `Working` flag stays.** A field on `SpecStore.Entry` directly. Workflow's merge closures call `store.MarkWorking(ids)` rather than going through a side-channel `workingSignals` struct.

7. **Migration is internal.** No on-disk format change, no prompt change, no agent-config change. The change is concentrated in `internal/agent/` + small `cmd/llm.go` wiring shift. Per `[[feedback-no-back-compat-until-self-hosting]]` no shim is needed.

8. **Wrapper pass-through methods retire.** With swappables gone, `LoggingExecutor.SpecSearch / SpecListManifest / SpecGet` and the matching `NotifyingExecutor` methods delete. Future wrappers have nothing to forward; the silent-no-op failure mode is structurally closed.

## Phase 1 — Build `SpecStore`

**Goal:** stand up the new unified store in isolation, with full test coverage, before any caller rewires. The store needs to support the existing read shapes (`ListManifest`, `GetSpec`, search via Bluge), the existing write shapes (merge-driven `Put` with origin tagging), the working-flag lifecycle, and the council-scoped Persist / Discard semantics. Old code keeps running through this phase — Phase 1 is additive.

**Files expected to add:**

- [internal/agent/spec_store.go](../../internal/agent/spec_store.go) — the unified store. Concretely:
  - `type SpecStore struct` with `sync.RWMutex`, typed slices keyed by id for decisions / features / strategies / bugs / approaches, an embedded Bluge index, the working-signal set.
  - `type Entry interface` (or per-kind concrete types) carrying `ID`, `Title`, `Summary`, `Origin` (settled/proposed enum), `Working bool`, the typed body.
  - Constructor: `New(fsys specio.FS) (*SpecStore, error)` — loads all on-disk nodes (origin=settled), builds index.
  - Reads: `ListManifest() SpecManifest`, `GetSpec(id string) (json.RawMessage, error)`, `Search(query string, opts search.Options) ([]search.Hit, int, error)`.
  - Writes (merge surface): `Put(node any, origin Origin)`, `MarkWorking(ids []string)`, `ClearWorking()`.
  - Lifecycle: `MarkCouncilStart()` (snapshot current settled state for discard rollback), `MarkCouncilEnd(committed bool)` (promote proposed→settled + Persist on commit; discard proposed nodes on rollback).
  - Internal: index update on every `Put`; not-found-recovery (inline kind-matched id list) carries over from `InFlightSpecStore.GetSpec`.
- [internal/agent/spec_store_test.go](../../internal/agent/spec_store_test.go) — full coverage. Specifics in **Tests** below.

**Files expected to modify:**

- None in Phase 1 — the new store is unwired and not referenced by production code yet.

**Tests:**

- `TestSpecStore_LoadFromFSAllKinds` — every JSON file under `.borg/spec/{decisions,features,strategies,bugs,approaches}/` round-trips through `New` and is visible via `ListManifest` / `GetSpec` with `Origin: settled`.
- `TestSpecStore_PutTagsAsProposed` — `store.Put(feature, OriginProposed)` makes the feature visible via `ListManifest` (with `Origin: proposed`) and `GetSpec`; `Search` includes it in results.
- `TestSpecStore_PutPromotesAndDeduplicates` — `store.Put` on an id that exists as settled replaces the body and sets origin to proposed; one entry visible, not two.
- `TestSpecStore_GetSpecNotFoundInlinesKindList` — calling `GetSpec("dec-no-such-axis")` returns an error string that lists every dec- id in the store. Mirrors the existing InFlightSpecStore.GetSpec error-format contract.
- `TestSpecStore_MarkWorkingFlagsEntries` — `store.MarkWorking(["dec-foo"])` causes the manifest entry for `dec-foo` to have `Working: true`; `ClearWorking` resets.
- `TestSpecStore_SearchIsLiveAfterPut` — `Put` a new node, then `Search` for content in its body; the new node appears in hits without an explicit reindex call.
- `TestSpecStore_PersistFlushesProposedToFS` — proposed nodes land on disk as `.borg/spec/.../<id>.json` after `MarkCouncilEnd(true)`. Settled nodes are unchanged; proposed nodes become settled (origin=settled in memory).
- `TestSpecStore_DiscardRevertsProposed` — proposed nodes vanish from memory after `MarkCouncilEnd(false)`; `.borg/spec/` is untouched; settled nodes are unchanged.
- `TestSpecStore_ConcurrentReadsAndOneWriter` — drive a single writer goroutine doing N `Put` operations while M reader goroutines run `ListManifest` / `GetSpec` / `Search`; no panics, no torn reads (every observed manifest is a consistent snapshot — a reader either sees the pre-Put or post-Put state, never half), `go test -race` clean.
- `TestSpecStore_OriginPrefixDispatch` — `GetSpec` resolves `feat-`, `strat-`, `dec-`, `bug-`, `app-` prefixes to the right kind; malformed ids error with the existing `validSpecID` regex check.

**Verification:** `go build ./internal/agent/... && go vet ./internal/agent/... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 6-10 hours.

## Phase 2 — Wire `SpecStore` through the tool registry; retire the swappables

**Goal:** make `SpecStore` the single dependency the tool handlers see. Replace the three-swap-registration with one `store` registration. Retire the swappable types and the wrapper pass-through methods.

**Files expected to modify:**

- [cmd/llm.go](../../cmd/llm.go) `registerSpecToolsOnce` — collapses to:
  ```
  store, err := agent.NewSpecStore(fsys)
  if err != nil { ... }
  exec.SetSpecStore(store)
  agent.RegisterSpecTools(exec.Tools(), store)
  ```
  The `idx, _ := search.Open(...)` call retires (the store builds its own index). The three `Set*` calls + `NewSwappableSpec*` calls retire.
- [internal/agent/executor.go](../../internal/agent/executor.go) — `SetSpecSearch / SetSpecListManifest / SetSpecGet` collapse to `SetSpecStore(store *SpecStore)`. Accessor methods `SpecSearch() / SpecListManifest() / SpecGet()` collapse to `SpecStore() *SpecStore`.
- [internal/agent/spec_tools.go](../../internal/agent/spec_tools.go) `RegisterSpecTools` — signature collapses to `(registry *ToolRegistry, store *SpecStore)`. Each of the three tool handlers (spec_search, spec_list_manifest, spec_get) calls into `store` directly. The SwappableSpec* indirections retire from the handler bodies.
- [internal/agent/session.go](../../internal/agent/session.go) `LoggingExecutor.SpecSearch / SpecListManifest / SpecGet` — delete. Add `SpecStore() *SpecStore` pass-through (single method, single accessor, structurally identical to how other inner-executor delegations work on the wrapper).
- [internal/agent/notifying_executor.go](../../internal/agent/notifying_executor.go) `NotifyingExecutor.SpecSearch / SpecListManifest / SpecGet` — delete. Add `SpecStore() *SpecStore` pass-through.
- [internal/agent/mock_llm.go](../../internal/agent/mock_llm.go) `MockExecutor` — `SetSpecSearch / SetSpecListManifest / SetSpecGet` collapse to `SetSpecStore`. The three corresponding `SpecSearch() / SpecListManifest() / SpecGet()` getters collapse to `SpecStore() *SpecStore`.

**Files expected to delete:**

- [internal/agent/spec_search_swap.go](../../internal/agent/spec_search_swap.go) — entire file retires (SwappableSpecSearch).
- [internal/agent/spec_inflight_swap.go](../../internal/agent/spec_inflight_swap.go) — entire file retires (SpecManifestProvider, SpecGetProvider, fsSpecProvider, InFlightSpecStore, SwappableSpecListManifest, SwappableSpecGet, NewFSSpecProvider, SpecManifestAndGetProvider). The working-signal compute logic moves to spec_store.go's `MarkWorking` and the workflow's merge closures.
- [internal/search/inflight_index.go](../../internal/search/inflight_index.go) (and friends) — `search.InFlightIndex` retires; the store maintains its own index.
- [internal/agent/executor_swap_passthrough_test.go](../../internal/agent/executor_swap_passthrough_test.go) — retires (the surface it tests no longer exists; the structural-impossibility argument in DJ-134's body replaces the test).
- [internal/agent/spec_inflight_swap_test.go](../../internal/agent/spec_inflight_swap_test.go), [internal/agent/spec_manifest_origin_test.go](../../internal/agent/spec_manifest_origin_test.go) — review and either delete (if the surface is gone) or rewrite as `spec_store_test.go` cases (the underlying semantic — settled-vs-proposed tagging, working-flag derivation, not-found-inline-recovery — survives; the API the tests drive does not).

**Tests:**

- Existing tests under `internal/agent/spec_tools*` need their wiring updated. The semantic surface (what spec_search / spec_list_manifest / spec_get return) is unchanged; the wiring (build a store and register it, not build three swappables and wire each separately) collapses.
- Add `TestRegisterSpecToolsWiresStoreSingleDependency` confirming `RegisterSpecTools` accepts a single `*SpecStore` and the resulting handler dispatches against it.
- Add `TestLoggingExecutorAndNotifyingExecutorPassStoreThrough` confirming the wrapper chain forwards `SpecStore()` accessor; this is the structural replacement for the deleted `executor_swap_passthrough_test.go`. Single test, one accessor.

**Verification:** `go build ./... && go vet ./... && go test ./... -count=1 -race`. Walk `git grep -nE 'Swappable|InFlightSpecStore|FSSpecProvider|InFlightIndex'` — every hit should be gone (in code, tests, comments, docs other than DJ-134 history references).

**Estimated:** 6-10 hours.

## Phase 3 — Rewire the council against `SpecStore`

**Goal:** the council goes through `SpecStore` for all in-flight reads and writes. The `Swap(inflight)` / `Swap(prev)` lifecycle retires. The merge helpers stop mutating `RawProposal` text and instead call `store.Put`. The Working-flag and Bluge-index updates happen as side effects of `Put`.

**Files expected to modify:**

- [internal/agent/specgen.go](../../internal/agent/specgen.go) `generateSpecWithWorkflow`:
  - The `inflightStore := NewInFlightSpecStore(); listSwap.Swap(inflightStore); getSwap.Swap(inflightStore); defer ...` block retires.
  - The `search.NewInFlightIndex(); searchSwap.Swap(inflight); defer ...` block retires.
  - Replaced with: `store := exec.SpecStore(); store.MarkCouncilStart()` and a defer `store.MarkCouncilEnd(committed)` that decides between Persist and Discard based on the workflow's exit condition.
  - The `specSearchSwap / specListManifestSwap / specGetSwap` helpers retire; the council reads `exec.SpecStore()` directly.
  - The committed boolean comes from the workflow's terminal state (the `wf.Done()` outcome — successful convergence vs cap-hit vs context cancel).
- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go):
  - `rebuildInFlightIndex` retires (the store's write-through index updates synchronously on `Put`).
  - `updateWorkingSignals` retires; merge closures call `store.MarkWorking(ids)` and `store.ClearWorking()` directly.
  - The merge helpers (`mergeDecisions`, `mergeNarrative`, `mergeReconciledProposal`, `mergeFeatureBatch`, etc. in [workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go)) take `*SpecStore` instead of working against `RawProposal` string + `InFlightSpecStore`. Each parses the agent's structured output and calls `store.Put(node, OriginProposed)` per node.
- [internal/agent/state.go](../../internal/agent/state.go):
  - `PlanningState.InFlightSpecStore *InFlightSpecStore` field retires.
  - `PlanningState.InFlightIndex *search.InFlightIndex` field retires.
  - `PlanningState.RawProposal string` — decision: keep it as a re-derived audit artifact (`store.MarshalProposal() string`) for trace-recording symmetry with current YAML files. Merge helpers don't read it; serializer code that produces session traces re-derives it from the store at audit emission time. Reduces memory pressure (one less full-graph string copy in flight) without breaking trace shape.
  - `PlanningState.Existing *ExistingSpec` — the snapshot used for in-flight reads of persisted state retires (the store IS the persisted state, tagged settled).
- [internal/agent/workflow_spec_generation_test.go](../../internal/agent/workflow_spec_generation_test.go) — every test that constructs a `PlanningState{InFlightSpecStore: ..., InFlightIndex: ..., Existing: ...}` rewrites to construct a `*SpecStore` and rely on its public surface. `TestInFlightIndexRebuiltOnRawProposalMerge` → `TestSpecStoreIndexUpdatedOnPut`. `TestGenerateSpecSwapsInFlightIndexForRun` → `TestGenerateSpecRunsAgainstStore` (asserting `store.MarkCouncilStart` / `End` is called with the right `committed` flag). `TestInFlightIndexNilIsNoop` retires (no nil-overlay path exists; the store is always wired). `TestRepro_LiveWiring_ManifestVisibleAfterMerge` — variant of this test stays as a smoke test confirming a `store.Put(decision)` makes the decision visible via `store.ListManifest()` immediately afterward.

**Open ordering question — RawProposal as a re-derived artifact or workflow-mutated buffer?** The plan above commits to re-derived. The alternative is to keep `RawProposal` as a string the workflow mutates in parallel to the store, write to both, and let the audit serializer keep using the string. That preserves trace-emission code shape but wastes work. Re-deriving at audit time is one `store.MarshalProposal()` call per session trace boundary (handful per council run) and clean. **Decision:** re-derive.

**Tests:**

- `TestCouncilCommitFlushesStoreToFS` — drive a synthetic workflow to successful convergence; assert `.borg/spec/decisions/<id>.json` files exist for every proposed decision at workflow end.
- `TestCouncilFailureDiscardsStoreDelta` — drive a workflow that fails (cap-hit or injected cancel); assert no `.borg/spec/` writes happened; store's in-memory state at end reflects pre-council settled snapshot only.
- `TestMergeDecisionsCallsStorePutWithProposedOrigin` — single-step assertion that `mergeDecisions` produces store-side state with `Origin: proposed` and `Working` set correctly per the input axes.
- `TestStoreWorkingFlagSetByMergeClosure` — verifies `store.MarkWorking` is called with the union of (OpenAxisIDs, NewNodeIDs, OpenConcernDecisionIDs, OpenConcernNodeMatches) the existing `computeWorkingSignals` derived.
- `TestRawProposalReDerivedFromStoreForAuditing` — `store.MarshalProposal()` round-trips: build a store with N decisions, call MarshalProposal, unmarshal back, every original decision is present and shape-identical.

**Verification:** `go build ./... && go vet ./... && go test ./... -count=1 -race`. Manual: run `locutus refine goals` against a synthetic project and confirm the council's per-step YAML files still record the same shape of state (the `proposal` and `state_diff` sections in the YAML should match the pre-DJ-134 baseline modulo the cosmetic shift from "InFlightSpecStore.SetState" log lines to "SpecStore.Put" log lines).

**Estimated:** 8-12 hours.

## Phase 4 — Documentation

**Goal:** CLAUDE.md / council.md / debugging-traces.md reflect the unified store. Per `[[feedback-council-doc-maintenance]]` the council.md update is mandatory.

**Files expected to modify:**

- [CLAUDE.md](../../CLAUDE.md) — the "Sources of Truth" section gains a one-line note: *"The in-process `SpecStore` (DJ-134) is the in-memory source of truth for spec reads and writes during a session; `.borg/spec/` is its persistence backing, written atomically at council teardown on successful convergence."*
- [docs/council.md](../../docs/council.md) — the "Spec-lookup tools" / "How spec_search and spec_list_manifest resolve in-flight vs settled" subsection updates: *"All three tools dispatch against the unified `SpecStore`. Each entry carries an `origin` tag (`settled` for on-disk-loaded nodes; `proposed` for nodes the council added this iteration); the tool surface returns them in one manifest. There is no overlay-vs-default split."* The Mermaid diagram doesn't change shape (it's about agent dispatch, not storage internals).
- [docs/debugging-traces.md](../../docs/debugging-traces.md) — wherever it discusses "an spec_* tool returned unexpected data — check the InFlightSpecStore / fsSpecProvider split / swap state at council start," update to point at `SpecStore` and its `MarkCouncilStart` / `MarkCouncilEnd` lifecycle. Walk every occurrence of `InFlightSpecStore`, `fsSpecProvider`, `SwappableSpec`, `InFlightIndex` and update or remove.
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — no change required (the conventions are prompt/schema-level; the store is transparent at that layer).

**Verification:** `go test ./... -count=1 -race` clean; `go vet ./...` clean. Manually verify the council.md Mermaid still renders (no shape change expected). Walk `git grep -nE 'InFlightSpecStore|fsSpecProvider|SwappableSpec|InFlightIndex'` — only DJ-134's own backreferences in DECISION_JOURNAL.md should remain (historical record).

**Estimated:** 1-2 hours.

## Phase 5 — Empirical validation

**Goal:** confirm convergence behaviour is at least as good as the post-`52e16fd` baseline (the trace at `~/projects/winplan/.locutus/sessions/20260523/1339/40-fd0046/`). DJ-134 is an internal refactor; the user-visible improvement is only the slight latency drop from one fewer indirection per tool dispatch. The validation criterion is "no regression" rather than "new improvement."

**Process:**

1. Build the DJ-134 binary: `go build -o ~/go/bin/locutus-dj134 .`.
2. Run `locutus-dj134 update --offline --reset` against winplan.
3. Run `locutus-dj134 refine goals` with `LOCUTUS_SPEC_GEN_MAX_ITERATIONS=10`.
4. Measure against the post-`52e16fd` baseline:
    - **Reconciler completes within ≤2 rounds.** The DJ-134 refactor doesn't change the reconciler's reasoning surface; it should converge in the same round count.
    - **Tool dispatch latency is ≤ the baseline.** Per-step folder timing in `01-reason.yaml` should be equal or faster (one fewer pointer-chase per RAG tool dispatch).
    - **No tool-loop spirals.** Identical to baseline.
    - **`.borg/spec/` final state matches the baseline.** Same nodes, same content, same backreferences. (Modulo non-determinism from LLM responses; spot-check that the structural shape is preserved — decision count, feature count, strategy count are all within ±1 of baseline.)
    - **`go test ./... -race -count=1` clean.** Mandatory.

**What success looks like:** trace shape is indistinguishable from the post-`52e16fd` baseline; per-step latency is the same or slightly faster; the wrapper-pass-through bug surface is structurally removed (`git grep` confirms zero swappable types in the codebase). The internal architecture is simpler; the user-visible behaviour is unchanged.

**What partial success looks like:** a regression in one merge helper (e.g., `mergeNarrative` works through the store but `mergeReconciledProposal` accidentally goes through a stale RawProposal path), surfacing as a council that converges but produces slightly different output than baseline. Mitigation: identify the merge helper, port it correctly; the refactor scope is contained.

**What failure looks like:** a deadlock under the single-mutex RWLock (writer-during-read or read-during-write pattern the swap-wrappers hid via finer-grained locks); a memory leak from the write-through index not pruning discarded-proposed entries on `Discard`; an audit-trail divergence the trace serializer can't paper over. Reversal criteria (a), (b), (c) in DJ-134's text track these. Mitigation by branch: sub-divide the mutex (a), batch index updates (b), or rework the Discard semantics (c) without abandoning the unified-store invariant.

**Verification:** winplan session traces are durable evidence. No automated assertion beyond Phase 1-3 tests.

**Estimated:** 1-2 hours of compute + manual review.

## Phase 6 — DJ-134 status flip + plan marked DONE

**Goal:** DJ-134 flips from `proposed` to `shipping` once Phase 5 validation passes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-134 status `proposed` → `shipping (Phases 1-5 landed YYYY-MM-DD)`.
- This plan file marked DONE.

**Verification:** `go test ./... -count=1 -race` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 22-36 hours single-stranded across 4-5 sessions

(plus Phase 5 empirical compute time)

## Pointers a fresh session should follow before resuming

1. Read DJ-134 in full ([docs/DECISION_JOURNAL.md#dj-134](../../docs/DECISION_JOURNAL.md#dj-134-unified-in-process-spec-store-supersedes-the-swappable-wrapper-stack-eliminates-swappablespecsearch--swappablespeclistmanifest--swappablespecget--fsspecprovider--inflightspecstore-single-mutex-protected-store-backs-both-on-disk-persistence-and-in-flight-council-mutations-prepares-the-read-surface-for-tool-call-driven-spec-mutation)). It's the authoritative design; this plan is progress tracking.
2. Read commit [`52e16fd`](https://github.com/chetan/locutus/commit/52e16fd) (the wrapper-pass-through fix DJ-134 supersedes). The bug context is load-bearing for understanding why the swap-and-wrap pattern is the wrong primitive; the test it added is what DJ-134 deletes alongside the surface.
3. Walk the current swap stack end-to-end before touching any of it. Start at [cmd/llm.go:188-228](../../cmd/llm.go#L188-L228) (wiring); follow `RegisterSpecTools` into [internal/agent/spec_tools.go:733](../../internal/agent/spec_tools.go#L733); follow `*SwappableSpecListManifest` to [internal/agent/spec_inflight_swap.go:529](../../internal/agent/spec_inflight_swap.go#L529); follow `*InFlightSpecStore` to [spec_inflight_swap.go:76](../../internal/agent/spec_inflight_swap.go#L76); follow `LoggingExecutor.SpecListManifest` to [internal/agent/session.go](../../internal/agent/session.go); follow `NotifyingExecutor.SpecListManifest` to [internal/agent/notifying_executor.go:115](../../internal/agent/notifying_executor.go#L115). Five files, nine types, all collapsing.
4. Confirm DJ-133 has landed before starting Phase 1. DJ-134 doesn't depend on DJ-133's axis-as-ID convention; it just keeps the two refactors as separate, focused commits. If DJ-133 isn't done, finish it first.
5. Sketch the `SpecStore` API in chat (or in a `// MARK:` comment in spec_store.go) before writing any code. The user memory mandates a design pause; this is exactly the kind of refactor that rewards getting the API shape right upfront.

## What is explicitly out of scope

- **Re-architecting the council workflow.** DJ-134 is a storage-layer refactor; the workflow's per-step structure, the merge-and-reconcile loop, the convergence gate, the cap-as-commit logic all stay shape-identical. Only the storage primitive changes.
- **Implementing DJ-127's write-tool surface.** DJ-134 prepares the store API to accept tool-call-driven writes (single `Put` / `Update` / `Delete` surface). DJ-127 ships its own write tools and validation layer; that's a separate DJ.
- **Changing the on-disk format.** `.borg/spec/<kind>/<id>.json` stays. The store's `Persist` writes the same files in the same shape.
- **Changing the prompt surface.** No agent prompt edits. The model-facing tool API (`spec_search`, `spec_list_manifest`, `spec_get`) is shape-identical; tool descriptions don't change.
- **Migrating away from Bluge for search.** The store still uses Bluge; only the indexing strategy changes (write-through projection vs explicit `Rebuild` calls).
- **Splitting the single mutex into per-kind mutexes.** The single RWMutex is the right starting point. Reversal criterion (a) tracks the case where contention forces finer granularity — that's a follow-up if it materializes empirically, not part of DJ-134.
- **Updating prior DJ text that references InFlightSpecStore / SwappableSpec*.** Historical DJs are the durable design record; rewriting them retroactively would corrupt that record. References to those types in DJ-123 / DJ-125 / etc. stay as historical record; new DJs use SpecStore.
- **Eliminating `RawProposal` from `PlanningState` entirely.** Phase 3 keeps it as a re-derived audit artifact for trace-recording symmetry. A future cleanup could route audit serialization through the store directly; not part of DJ-134.
