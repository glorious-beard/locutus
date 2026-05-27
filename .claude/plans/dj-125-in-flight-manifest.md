# DJ-125 — In-Flight Manifest + Enriched Concern Model

> **Governing DJ:** [DJ-125](../../docs/DECISION_JOURNAL.md#dj-125). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** SUPERSEDED by DJ-134 (2026-05-23) + DJ-135 (2026-05-26). InFlightSpecStore overlay retired in DJ-134 (concern + working-flag carry as per-entry fields in the unified store); cap-as-commit retires per DJ-135 resolved-question 13 (subscription economics). The concern model lives in agent-prompt prose now rather than Go-side bookkeeping. Historical: Phases 1-8 landed 2026-05-20.
> **Predecessor:** [DJ-124](../../docs/DECISION_JOURNAL.md#dj-124) (landed Phases 1-8; Phase 9 validation revealed the gaps this plan closes).
> **Surface area:** Concern model expansion + new `InFlightManifest` data type + spec-lookup tool redirection + manifest-based projection rewrites + mechanical concern-disposition pre-pass + scout grading.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Each phase verifies independently before moving on. **Prompt edits walk [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end before drafting** per [[feedback-agent-conventions-checklist-first]].

## Why this plan exists

The second winplan re-run after DJ-124 + the 8K → 200K `defaultMaxChars` bump ([trace](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/)) failed to converge with a different signature than the first. The iter-3 critics correctly saw the full proposal (~30K tokens) and produced 6 substantive findings, but the `convergence_failed` event surfaced 30+ "unresolved concerns" because `mergeCriticIssues` appends to `state.Concerns` and nothing ever clears stale entries.

Two coupled structural gaps:

1. **`state.Concerns` is append-only.** Inherited from DJ-122 where the gate didn't gate on no-concerns; DJ-124 made the scout gate on `len(Concerns)==0` without flipping the producer's contract.
2. **Projection-by-blob.** Tactical 200K cap won't scale past dogfood-sized projects. The architecturally honest answer is manifest-first RAG, mirroring the persisted-graph pattern.

Both gaps live in the council's state model and projection layer. DJ-125 promotes concerns to a first-class structured shape and introduces the in-flight manifest as the primary projection surface.

## Reference state (before adoption)

- **`Concern`** ([internal/agent/state.go](../../internal/agent/state.go)) carries `AgentID`, `Severity`, `Kind`, `Text`. No iteration metadata, no status enum, no decision/axis cross-references.
- **`mergeCriticIssues`** ([internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go)) appends to `state.Concerns`; never clears. Calls `appendIntegrityFindings` and `runMechanicalCluster` after the append.
- **`projectChallenge`** ([internal/agent/projection.go:414](../../internal/agent/projection.go)), **`projectScout`** ([internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go)), **`projectReconcile`**, **`projectOpenAxis`**, **`projectAffectedNode`** — all dump `state.RawProposal` or `state.ProposedSpec` as a blob.
- **`defaultMaxChars`** ([internal/agent/compact.go](../../internal/agent/compact.go)) currently 200000. The bump unblocked iter-3 of the second winplan run but won't scale.
- **In-flight `spec_search`** ([internal/search/inflight.go](../../internal/search/inflight.go)) — DJ-123 redirected `spec_search` to in-flight Bluge during council runs via `SwappableSpecSearch`. `spec_list_manifest` and `spec_get` still target on-disk only.
- **Scout convergence rule** in `scoutSpawnFor` ([internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go)) — `Converged: true ⟺ axes_open==[] AND len(Concerns)==0`. Stale concerns block convergence indefinitely.
- **`SpecGenRequest` and `PlanningState`** — carry `Concerns []Concern` as a flat slice. No notion of disposition or iteration-of-origin.

## Resolved design questions

Recorded in chat 2026-05-18; settled before this plan went to implementation.

1. **Concerns are durable, not cleared.** Old concerns stay in `state.Concerns` for forensics and audit; their `Status` field communicates whether they currently block convergence. The alternative ("clear concerns each iteration") loses history and forces critics to re-discover every issue every iteration.

2. **Disposition is a four-state enum.** `open` / `addressed` / `stale` / `wontfix`. `addressed` and `stale` differ in agency: `stale` means a related axis or decision now covers it (mechanical match); `addressed` means the scout judged the concern resolved by the current proposal (semantic judgment with `justification`). `wontfix` is the explicit "real concern but acceptable tradeoff" disposition for things like deliberate cost-vs-availability tradeoffs the user accepts.

3. **Mechanical pre-pass handles 80% of staleness; scout grades the rest.** Most "missing X" findings have an exact-ID match against the manifest the moment the underlying decision lands. Cheap regex match; no LLM call. The scout's grading effort focuses on judgment calls (contradictions, factual claims, integration gaps).

4. **`spec_list_manifest` / `spec_get` redirect to in-flight during council, same as `spec_search`.** Generalizes DJ-123's `SwappableSpecSearch` pattern to all three RAG tools. Agents see one tool surface regardless of whether they're reading the working proposal or the persisted graph; the dispatcher decides the backing store at council-init time.

5. **Manifest is the primary projection surface; full content is fetched on demand.** Each agent's prompt carries the manifest (axes/decisions/features/strategies/concerns with state + summaries) + the specific working item in full (the axis being decided, the feature being elaborated). Detail-fetch via `spec_get(id)` against in-flight. Same RAG pattern as the persisted graph.

6. **`compactContext` becomes obsolete for manifest-rendered projections.** The 200K cap stays as defense-in-depth on the few paths that still render full content (per-fanout-item full bodies, error paths), but the primary projections shrink to manifest size.

7. **Scout's `ScoutBrief` gains `concern_dispositions[]`; not a separate agent.** The scout already has the full manifest context and is the convergence judge; adding grading to its output keeps the role coherent. Separate agent considered and rejected (more surface area; same reasoning).

8. **Cycle-detection key shifts to manifest axis state.** `DecidedAxesByIter` becomes redundant once the manifest carries axis state directly (`settled-by-dec-X` / `open`). The check moves from "did this axis appear in a prior iter's `axes_open`?" to "did this axis appear in `axes_open` despite being marked `settled` in the manifest?" Same outcome, cleaner substrate. Deferred to a later cleanup phase since the existing logic works.

## Phase 1 — `Concern` model expansion

**Goal:** add iteration metadata, status, and cross-reference fields to `Concern`. Keep backwards compatibility with persisted state via zero-value defaults.

**Files expected to change:**

- [internal/agent/state.go](../../internal/agent/state.go):
  - New type `ConcernStatus` with consts: `ConcernStatusOpen`, `ConcernStatusAddressed`, `ConcernStatusStale`, `ConcernStatusWontfix`.
  - `Concern` gains:
    - `IterationRaised int` — `jsonschema:"description=Iteration index when this concern was first raised. Zero for concerns raised before iteration tracking (legacy concerns from pre-DJ-125 sessions)."`
    - `Status ConcernStatus` — `jsonschema:"enum=open,enum=addressed,enum=stale,enum=wontfix,description=Current disposition. open blocks convergence; addressed/stale/wontfix do not. Mechanical pre-pass sets stale; scout grading sets addressed and wontfix."`
    - `RelatedDecisionIDs []string` — `jsonschema:"description=Decision IDs this concern references. Populated by mergeCriticIssues via regex match against the manifest plus optional structured surfacing by the critic. Powers DJ-126's decision-revision dispatch."`
    - `RelatedAxisIDs []string` — `jsonschema:"description=Axis IDs this concern references. Populated mechanically. Powers staleness checks against the manifest's axis-state field."`
  - `snapshotPlanningState` deep-copies the new slices.

**Tests:**

- `TestConcernStatusEnumValues` — assert the four values.
- `TestSnapshotPlanningStateDeepCopiesConcernFields` — mutating a snapshot's `Concerns[i].RelatedDecisionIDs` doesn't mutate the original.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 1-2 hours.

## Phase 2 — `InFlightManifest` data type + builder

**Goal:** introduce the manifest data type that becomes the council's working view of the spec graph. Build from `state.RawProposal`, `state.Existing`, `state.Concerns`.

**Files expected to change:**

- [internal/agent/manifest.go](../../internal/agent/manifest.go) (new):
  - `type InFlightManifest struct { Axes []ManifestAxis; Decisions []ManifestDecision; Features []ManifestNode; Strategies []ManifestNode; Concerns []ManifestConcern }`.
  - Per-item state markers:
    - `ManifestAxis.State` enum: `settled` / `open` / `under_evaluation`. `SettledByDecisionID string` populated when `State == settled`.
    - `ManifestDecision.State` enum: `settled_prior` / `settled_this_iter` / `flagged`. `FlaggedConcernIDs []string` populated when `State == flagged`.
    - `ManifestNode.State` enum: `authored` / `pending_narrative` / `pending_decision_ref`.
    - `ManifestConcern` mirrors `Concern` (id, iteration_raised, status, related_ids) plus a `Summary` string truncated for one-line rendering.
  - All fields jsonschema-tagged per CLAUDE.md so the type can travel into an output schema if a future agent needs to emit a manifest fragment.
  - `BuildManifest(state *PlanningState) (*InFlightManifest, error)` — walks `state.RawProposal` (parsed as `RawSpecProposal`), `state.Existing`, `state.AxesOpen`, `state.NewNodesFromScout`, `state.Concerns`. Returns a fully-populated manifest with state markers computed.
  - `RenderManifest(m *InFlightManifest) string` — produces a compact text rendering suitable for agent prompts. Format: sectioned by kind, one-line per item with state marker.

**Tests:**

- `TestBuildManifestPopulatesAxisState` — fixture with axes in three states; assert state markers match.
- `TestBuildManifestPopulatesDecisionState` — fixture with decisions across iterations and one flagged by a concern; assert states.
- `TestRenderManifestStableOutput` — same input produces same output bytes (order-stable).
- `TestRenderManifestCompactSize` — render of a 30-decision graph is <8K chars (regression guard against re-introducing blob-projection).

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 3 — `spec_list_manifest` / `spec_get` redirect to in-flight

**Goal:** generalize DJ-123's `SwappableSpecSearch` pattern to all three RAG tools. During council runs, the tools read the in-flight manifest; otherwise they read the persisted on-disk graph.

**Files expected to change:**

- [internal/agent/spec_tools.go](../../internal/agent/spec_tools.go):
  - Add `SwappableSpecListManifest` and `SwappableSpecGet` mirroring the existing `SwappableSpecSearch`.
  - The tools' input/output schemas are unchanged; only the backing store is configurable.
  - Tool dispatch picks the backing store at council-init time (in-flight) or at agent-load time (on-disk default).
- [internal/search/inflight.go](../../internal/search/inflight.go):
  - Extend the in-flight Bluge index with manifest-shape accessors. `ListManifest(filter)` returns the in-flight manifest's items in the same shape `spec_list_manifest` produces against the on-disk graph. `Get(id)` returns the full content from `state.RawProposal`.
- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - Council init wires the in-flight versions of all three tools.
  - Each `mergeDecisions`, `mergeNarrative`, `mergeReconciledProposal` triggers a manifest rebuild (mirrors the existing `rebuildInFlightIndex` pattern).

**Tests:**

- `TestSwappableSpecListManifestRoutesToInFlight` — wire an in-flight store; call returns in-flight content, not on-disk.
- `TestSwappableSpecGetRoutesToInFlight` — same shape.
- `TestInFlightManifestRebuildAfterMergeDecisions` — after `mergeDecisions` runs, the manifest reflects the new decision.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... ./internal/search/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 4 — Manifest-based projections

**Goal:** replace blob dumps in every projection with `RenderManifest(state) + agent-specific working item full content`.

**Files expected to change:**

- [internal/agent/projection.go](../../internal/agent/projection.go):
  - `projectChallenge` (critics) — render manifest; critics see structural overview + can fetch specific decision/feature bodies via `spec_get`. Remove `compactContext` call.
  - `projectReconcile` — same shape; reconciler reads the manifest + can fetch details.
- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - `projectScout` — render manifest + concerns section + prior `ScoutBrief` (for cross-iteration baseline). No blob dump of `RawProposal`.
  - `projectOpenAxis` (decision-elaborator fanout) — render manifest + the specific `OpenAxis` being decided in full + sibling settled decisions via manifest.
  - `projectAffectedNode` (narrative-elaborator fanout) — render manifest + the specific feature/strategy being elaborated in full + pre-populated decision references.

Each projection still carries the agent's specific working item verbatim — only the *surrounding context* moves from blob to manifest.

**Tests:**

- `TestProjectChallengeRendersManifest` — critic projection contains manifest output and no full-proposal JSON dump.
- `TestProjectScoutIncludesManifestAndConcerns` — scout projection has both.
- `TestProjectionsStayBelowSizeCap` — each projection on a 30-decision graph stays under 16K chars. Regression guard.
- Existing fanout tests update to assert the new shape.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race -skip TestCLISinkRendersAgentLifecycle`.

**Estimated:** 3-4 hours.

## Phase 5 — `mergeCriticIssues` extracts related IDs

**Goal:** populate `Concern.RelatedDecisionIDs` and `Concern.RelatedAxisIDs` at the moment the concern is recorded.

**Files expected to change:**

- [internal/agent/workflow_spec_generation.go](../../internal/agent/workflow_spec_generation.go):
  - `mergeCriticIssues` runs the existing `idRefRegex` against each concern's text to find `dec-*` / `feat-*` / `strat-*` references; the references go into `RelatedDecisionIDs`.
  - A separate regex (or manifest lookup) extracts axis IDs from concern text. Manifest-driven match: for each axis ID in the current manifest, check if the concern text contains it; if so, add to `RelatedAxisIDs`.
  - `IterationRaised` set from the result's `IterationIndex` (same pattern as `mergeDecisions`).
  - `Status` initialized to `ConcernStatusOpen`.

**Tests:**

- `TestMergeCriticIssuesExtractsDecisionIDs` — fixture critic finding mentioning `dec-postgres`; assert `RelatedDecisionIDs` contains it.
- `TestMergeCriticIssuesExtractsAxisIDs` — fixture with axis IDs in the manifest; concern text references one; assert `RelatedAxisIDs` populated.
- `TestMergeCriticIssuesPopulatesIterationRaised` — fixture with `RoundResult.IterationIndex=3`; assert concern has `IterationRaised=3`.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 1-2 hours.

## Phase 6 — Mechanical concern-disposition pre-pass

**Goal:** automatically mark concerns `stale` when a related axis is settled or a related decision exists in the graph. Cheap regex/lookup pass; no LLM call.

**Files expected to change:**

- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - New function `mechanicalDisposeConcerns(state *PlanningState)` — walks `state.Concerns` where `Status == open`. For each concern:
    - If any `RelatedAxisID` is `settled` in the current manifest → set `Status = stale`.
    - Else if any `RelatedDecisionID` exists in the current graph (in-flight or `state.Existing`) AND the concern's text matches a known "missing X" pattern → set `Status = stale`.
    - Else leave `Status = open` for the scout to grade.
  - Called after `mergeCriticIssues` and after every `mergeDecisions` (so newly-decided axes immediately stale prior concerns).
  - Each transition records a debug log line for forensic visibility.

**Tests:**

- `TestMechanicalDisposeConcernsStalesOnSettledAxis` — fixture: concern with `RelatedAxisIDs: ["auth-provider"]`, manifest has axis settled. After call, `Status == stale`.
- `TestMechanicalDisposeConcernsLeavesContradictionsOpen` — fixture: concern naming two decisions that both exist in the graph (a contradiction); the pre-pass does not stale it (decisions exist but the concern is about their interaction).
- `TestMechanicalDisposeConcernsIdempotent` — running twice produces the same state.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 2-3 hours.

## Phase 7 — Scout grading of remaining `open` concerns

**Goal:** extend `ScoutBrief` with `concern_dispositions[]`; `mergeScoutBrief` applies them; scout's prompt explains the grading discipline.

**Files expected to change:**

- [internal/agent/specgen.go](../../internal/agent/specgen.go):
  - New type `ConcernDisposition struct { ConcernID string; Disposition string; Justification string }` with `jsonschema` tags. `Disposition` enum: `addressed` / `wontfix` / `still_open`. (No `stale` — the scout doesn't mark stale; the mechanical pre-pass owns that.)
  - `ScoutBrief.ConcernDispositions []ConcernDisposition` with description naming the grading discipline.
- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go):
  - `mergeScoutBrief` applies dispositions onto `state.Concerns` by ID match.
  - `scoutSpawnFor` convergence rule changes: `Converged ⟺ axes_open == [] AND no concerns with Status == open`.
- [internal/scaffold/agents/spec_scout.md](../../internal/scaffold/agents/spec_scout.md):
  - New section: "Grading open concerns". Walks the concern-grading task: scout receives the manifest with concerns marked `open` (after mechanical pre-pass); scout grades each as `addressed` / `wontfix` / `still_open` with a one-sentence justification. The grading is part of the scout's convergence judgment.
  - Mandatory walk of [docs/agent-conventions.md](../../docs/agent-conventions.md) before drafting; audit each numbered anti-pattern after writing.

**Process discipline:** the scout's prompt section on concern grading is high-stakes (premature `addressed` causes spurious convergence; over-conservative `still_open` causes infinite loops). Specific examples for each disposition kind; literal-sentinel pattern from `justify_researcher.md` doesn't apply here (no grounded search) but the same "be honest about your judgment" framing carries over.

**Tests:**

- `TestScoutBriefSchemaCarriesConcernDispositions` — registered schema has the field with the right enum.
- `TestMergeScoutBriefAppliesConcernDispositions` — scout output marks concern as `addressed`; merge updates `state.Concerns[i].Status`.
- `TestScoutConvergenceWithStaleAndAddressedConcerns` — `state.Concerns` has stale + addressed + open mix; scout output marks the last open one addressed; convergence holds.
- `TestScoutPromptDescribesConcernGrading` — scaffolded prompt mentions "concern_dispositions" and the four-state disposition.

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... ./internal/scaffold/... -count=1 -race -skip TestCLISinkRendersAgentLifecycle`.

**Estimated:** 4-5 hours.

## Phase 8 — `compactContext` cleanup

**Goal:** the 200K cap was tactical defense-in-depth; with manifest projections in place it's largely redundant. Either revert to a saner default or remove the `compactContext` calls from `projectChallenge` and `projectReconcile` entirely.

**Files expected to change:**

- [internal/agent/compact.go](../../internal/agent/compact.go):
  - Either: revert `defaultMaxChars` to ~32K (defense-in-depth on the few paths that still render full content), OR
  - Keep at 200K as a safety net but update the doc comment to reflect that it's no longer load-bearing.
- [internal/agent/projection.go](../../internal/agent/projection.go) and [convergence.go](../../internal/agent/convergence.go):
  - `compactContext` calls in `projectChallenge` and `projectReconcile` removed (manifest rendering replaces them). The two `compactContext` calls in [convergence.go](../../internal/agent/convergence.go) (line 43, 65) belong to the legacy convergence path that DJ-122 superseded; those can stay as-is or be cleaned up in a follow-up.

**Tests:** existing tests pass; if the cap is bumped back down, ensure no projection exceeds the new cap.

**Verification:** `go test ./internal/agent/... -count=1 -race -skip TestCLISinkRendersAgentLifecycle`.

**Estimated:** 30 minutes.

## Phase 9 — Validation against winplan re-run

**Goal:** the same winplan project that triggered DJ-125 converges within the default budget under the new architecture.

**Process:**

1. Build the DJ-125 binary: `go build -o ~/go/bin/locutus-dj125 .`.
2. Run `locutus-dj125 update --offline --reset` against winplan to refresh agent prompts.
3. Run `locutus-dj125 refine goals` against winplan with default 5-iteration budget.
4. Compare against the failing run at `/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`:
   - Did the loop converge? In how many iterations?
   - How many concerns at exit, by `Status`? (`open` should be small or zero; `stale` and `addressed` make up the bulk.)
   - Did the projection size stay bounded? (Should never exceed ~30K chars even on the iter-N proposal.)
   - Did scout grading produce reasonable justifications, or did it rubber-stamp `addressed`?

**What success looks like:** `locutus refine goals` exits with `converged: true` in ≤4 iterations on the winplan project. The concerns at exit are either `addressed` (with substantive justification) or `wontfix` (with the user-facing tradeoff named). No `open` concerns at convergence.

**What partial success looks like:** convergence within budget but with multiple `wontfix` concerns whose justifications are weak ("not a real issue"). Indicates the scout's grading prompt needs tightening but the architecture is correct.

**What failure looks like:** convergence still doesn't happen. Likely cause: cross-decision contradictions that DJ-125 doesn't address (those are the DJ-126 candidate); OR scout grading is too conservative; OR mechanical pre-pass missed real stale cases. Diagnose against the trace and decide whether DJ-126 is urgent or whether scout grading needs tightening.

**Verification:** the winplan session traces are durable evidence. No automated assertion here.

**Estimated:** 1 hour of compute + manual review.

## Phase 10 — DJ-125 status flip + plan marked DONE

**Goal:** DJ-125 flips from `proposed` to `shipping` once Phase 9 validation passes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-125 status `proposed` → `shipping (Phases 1-9 landed YYYY-MM-DD)`.
- This plan file marked DONE.

**Verification:** `go test ./... -count=1 -race -skip TestCLISinkRendersAgentLifecycle` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 18-25 hours single-stranded across 3-5 sessions

## Pointers a fresh session should follow before resuming

1. Read DJ-125 in full ([docs/DECISION_JOURNAL.md#dj-125](../../docs/DECISION_JOURNAL.md#dj-125)). It's the authoritative design; this plan is progress tracking.
2. Read DJ-124 ([docs/DECISION_JOURNAL.md#dj-124](../../docs/DECISION_JOURNAL.md#dj-124)) and DJ-123 ([docs/DECISION_JOURNAL.md#dj-123](../../docs/DECISION_JOURNAL.md#dj-123)) for the predecessor architecture and the `SwappableSpecSearch` pattern this generalizes.
3. Read the second winplan trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/) end-to-end before Phase 6. The stale-concerns failure mode is visible in the `convergence_failed` event's accumulated concerns list.
4. Before any prompt edit (Phase 7), **re-read [docs/agent-conventions.md](../../docs/agent-conventions.md) end-to-end**. Per [[feedback-agent-conventions-checklist-first]], do not rely on remembered conventions. After drafting the new scout grading section, audit section-by-section against the numbered anti-patterns.
5. Phase 9 is the validation step. Don't flip DJ-125 status to `shipping` until winplan re-runs cleanly OR the failure mode shifts to cross-decision contradictions (DJ-126 territory).

## What is explicitly out of scope

- **Decision re-elaboration for cross-decision contradictions.** Tracked separately as [DJ-126](../../docs/DECISION_JOURNAL.md#dj-126). DJ-125 provides the substrate (`Concern.RelatedDecisionIDs`); DJ-126 builds the dispatch on top.
- **Removal of legacy gate helpers** (`gateSpawnFor`, `mergeGateVerdict`, etc.). They were preserved in DJ-124's Stage C because test surfaces still drive them. Cleanup is a follow-up.
- **Cycle-detection refactor to use manifest axis state directly.** The existing `DecidedAxesByIter` map works; cleanup is a follow-up once DJ-125 lands and the manifest is the canonical axis-state source.
- **Operator-facing surfaces for the manifest.** A future `locutus manifest` verb or `locutus history --concerns` could surface this richer state; out of scope for the council-internal work here.
- **Embedding-based search for the in-flight Bluge index.** DJ-123's reversal criterion (a) named >25% empty-result rate as the threshold. Independent of DJ-125; tracked separately.
