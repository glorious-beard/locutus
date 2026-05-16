# Codebase audit — `main@31e5923` — 2026-05-14

> **Source:** five parallel reviewer agents (`pr-review-toolkit:silent-failure-hunter`, `type-design-analyzer`, `comment-analyzer`, `pr-test-analyzer`, `code-reviewer`) run from worktrees branched off `main@31e5923` — pre-DJ-123 by design (the DJ-123 work had its own per-phase reviews and is not in scope here).
>
> **Status:** designed; not started. 54 raw findings deduplicated and categorized. Each finding carries an ID (`AUDIT-Cn` / `Hn` / `Mn` / `Ln`) for cross-referencing during execution.
>
> **Scope conventions used here:** the suggested fixes are written as work items, but the audit is *advisory*. Some findings (e.g. dead `internal/memory/`) want a directional call from the user before deletion; those are flagged inline.

## Cross-cutting patterns (highest leverage)

These shapes recur across files and reviewers; fixing them once at the layer that produces them is cheaper than fixing each instance.

- **Schema-tag discipline excellent at leaves, broken at `spec.*` types.** Type-design and code-quality reviewers independently flagged this. The validators in `reconcile.go`, `findings.go`, `justify.go` exist *because* `spec.Decision`/`Feature`/`Strategy` carry no `jsonschema` tags when embedded into registered LLM-response shapes. Pushing tags onto the persistence types simplifies a half-dozen validators simultaneously.
- **Substrate-swap terminology lag.** Every misleading comment traces to one of three platform transitions: DJ-099 (Genkit → direct SDKs), DJ-119 (per-step → per-workstream ACP), or the search package's "Phase 1/Phase 2" framing. A defensive grep for old identifiers after each substrate swap would have caught them.
- **"Log Warn and continue, don't record failure in caller-visible state."** Recurs at ~15 sites across `workflow_refine`, `findings`, `dispatcher`. Operator-stderr Warn is the only audit trail; user sees "no changes" for actual write failures.
- **"Discard FS ReadFile error, feed empty string to rewriter."** 4 sites where a transient I/O failure can clobber a file with a regenerated-from-scratch version.
- **Test names overstate what they test.** I caught one in DJ-123 Phase 3 (`TestGenerateSpecWiresInFlightIndex` didn't invoke GenerateSpec). The test-coverage reviewer found the same shape pervasive in `specgen_test.go` — production workflow shapes go untested.
- **Call-count-only test assertions** miss content regressions. Dispatcher tests are the bright counter-example.

## Recommended lint additions

Mechanical guards that would prevent regression of the dominant patterns.

- **LINT-1: `TestEveryRegisteredSchemaFieldHasDescription`** — walk every registered schema's reflected JSON, assert every leaf object property has a non-empty `description`. Mirrors the existing `TestSchemaDescriptionsAvoidPlaceholderPriming` recursion. Locks down [[AUDIT-C5]].
- **LINT-2: Enum-walker test** — for every Go enum-shaped field with an `enum=` tag, assert the tag values match the Go constants in `internal/spec/enums.go`. Catches drift like [[AUDIT-C1]] mechanically.
- **LINT-3: Adapter↔agent type-pair drift guard** — reflect `adapters.Round`/`adapters.ToolCall`/`adapters.Citation`/`adapters.Message` and their `agent.GenerateRound`/`agent.ToolCall`/`agent.Citation`/`agent.Message` mirrors; assert field-set agreement. Closes [[AUDIT-M14]] drift surface cheaply.
- **LINT-4: Export `challengerPlaceholderTokens`** from `internal/agent/justify.go` and import from `cmd/schema_conventions_test.go` so the lint list and validator list can never drift again. Closes [[AUDIT-M21]].

## CRITICAL — fix soon

Direct correctness/security impact, ≥2 reviewer confirmation, or CLAUDE.md rule violation on the wire.

### AUDIT-C1 — `Workstream.DetailLevel` enum drift

**File:** [`internal/spec/plan.go:66`](../../internal/spec/plan.go#L66) — schema tag `enum=high,enum=medium,enum=low,...`; Go constants in [`internal/spec/enums.go:65-69`](../../internal/spec/enums.go#L65) are `high` / `medium` / `detailed`.

**Issue:** strict-mode providers reject the legal Go value `detailed` and accept the illegal `low`. Runtime bug; no test asserts the constants and the tag agree.

**Fix:** align tag to `enum=high,enum=medium,enum=detailed`. Add LINT-2 to lock it down.

**Source:** code-reviewer #1

### AUDIT-C2 — `spec_architect.md` forbids the `scout_brief` citation kind it should be using

**File:** [`internal/scaffold/agents/spec_architect.md:84`](../../internal/scaffold/agents/spec_architect.md#L84) and prose at line 90.

**Issue:** prompt says citation `kind` MUST be `goals` / `doc` / `best_practice` / `spec_node` **"do not invent new ones like 'scout_brief'"**. But `spec.Citation.Kind` is `enum=goals,enum=doc,enum=best_practice,enum=spec_node,enum=scout_brief` at [`internal/spec/types.go:54`](../../internal/spec/types.go#L54), and the sibling elaborator prompts use `scout_brief` correctly. The architect is told to launder scout-grounded facts as fake `best_practice` citations — every spec-gen run produces wrong provenance. Also violates `docs/agent-conventions.md` §1 (anti-pattern priming on a token that's actually valid).

**Fix:** replace the four-kind list with the full five-value list; delete the "do not invent new ones like 'scout_brief'" phrasing.

**Source:** code-reviewer #6

### AUDIT-C3 — Default permission policy fails open

**File:** [`internal/dispatch/supervisor.go:108-114`](../../internal/dispatch/supervisor.go#L108).

**Issue:** `Supervisor.policyForWorkstream` returns `policy.AllowOncePolicy{}` if `s.cfg.PolicyForWorkstream == nil`, with no log or warning. A refactor that drops the production-wiring leaves the supervisor running unrestricted, indistinguishable from a properly-guarded run. Security-adjacent.

**Fix:** either return `policy.DenyAllPolicy{}` (fail closed) or emit a one-shot `slog.Warn` per Supervisor (the pattern used by `logMonitorDisabledOnce` for `FastLLM`).

**Source:** silent-failure-hunter #2

### AUDIT-C4 — ACP channel-close masquerades as a successful run

**File:** [`internal/dispatch/streaming.go:129-136`](../../internal/dispatch/streaming.go#L129) combined with [`internal/dispatch/acp/connection.go:199-202`](../../internal/dispatch/acp/connection.go#L199).

**Issue:** when ACP transport closes without an `EventResult`/`EventError` terminal (ctx cancellation, transport stall, process death), the receiver returns `result, nil` — a killed coding agent looks like a clean completion. The validator grades partial accumulation.

**Fix:** when `ok == false` without a recorded terminal, return an explicit error (`"acp transport closed mid-stream"`). Never treat an unterminated channel as success.

**Source:** silent-failure-hunter #1

### AUDIT-C5 — `spec.*` persistence types in 5 LLM-response schemas with no `jsonschema` tags

**Files:** registrations at [`internal/agent/refiner_supersede.go:21-37`](../../internal/agent/refiner_supersede.go#L21), [`internal/agent/assimilation.go:104`](../../internal/agent/assimilation.go#L104), [`internal/remediate/remediate.go:35`](../../internal/remediate/remediate.go#L35). Types at [`internal/spec/types.go:14-148`](../../internal/spec/types.go#L14) and [`internal/spec/approach.go:13`](../../internal/spec/approach.go#L13).

**Issue:** `spec.Decision`, `spec.Feature`, `spec.Strategy`, `spec.Approach`, `spec.Entity`, `spec.EntityField`, `spec.Relationship`, `spec.Bug` are embedded in `RewriteDecisionResult`, `RewriteFeatureResult`, `RewriteStrategyResult`, `AssimilationContribution`, and `remediate.Plan`. Their fields carry only `json`/`yaml` tags — no `jsonschema:"description=..."`, no `jsonschema:"enum=..."` on `Status`/`Kind` enum-shaped fields. The validators (`degenerateChallengerBrief`, `titleDenylist`, `titleStructuralNoise`, `isEmptyInlineDecision`) exist *because* the schema layer isn't enforcing — exactly the inversion CLAUDE.md warns about.

**Fix:** add `jsonschema` tags to every field on the persistence types. For typed-string enums (`DecisionStatus`, `FeatureStatus`, `BugStatus`, `BugSeverity`, `StrategyKind`, `PlanAction`, `DetailLevel`, `AssertionKind`), mirror the constant list as `enum=...`. Add LINT-1 to lock the rule down across all registered schemas.

**Source:** type-design-analyzer #1 + code-reviewer #3 (independent confirmation)

### AUDIT-C6 — `Assertion.Kind` missing `enum=` AND description names non-existent values

**File:** [`internal/spec/plan.go:107`](../../internal/spec/plan.go#L107). Constants at [`internal/spec/enums.go:75-84`](../../internal/spec/enums.go#L75) — 9 values, real names include `contains`, `not_contains`, `llm_review`.

**Issue:** `Kind` is `AssertionKind` (enum-shaped) but carries only `description=`. The prose description lists `test_pass / file_exists / pattern_match / llm_assert` — `pattern_match` and `llm_assert` are not in the const set. Model gets contradictory guidance.

**Fix:** emit all nine `enum=` tags, rewrite the description to match.

**Source:** code-reviewer #2

## HIGH

### AUDIT-H1 — `workflow_refine.go` swallows persistence + history failures

**File:** [`internal/agent/workflow_refine.go:331-415`](../../internal/agent/workflow_refine.go#L331) — three near-identical methods `applyFeatureResult` / `applyStrategyResult` / `applyBugResult`.

**Issue:** each method logs `slog.Warn` and `return`s on `specio.SavePair` failure (lines 344, 371, 400), on `markRefineApproachesDrifted` failure (349, 376, 409), and on `hist.Record` failure (354, 381, 414). The user-facing `refine` summary silently omits the node. Partial-success path leaves the spec in inconsistent state (feature body updated, downstream Approaches NOT marked drifted, history event NOT recorded) with only WARN to stderr.

**Fix:** accumulate failures into `state.Failed map[string]error` (same shape as `fill_summaries`); surface non-zero exit / structured error from the refine command.

**Source:** silent-failure-hunter #3

### AUDIT-H2 — Cascade / refine read prior body with discarded error

**Files:** [`internal/cascade/cascade.go:279`](../../internal/cascade/cascade.go#L279), [`internal/agent/workflow_refine.go:210`](../../internal/agent/workflow_refine.go#L210) and [`:365`](../../internal/agent/workflow_refine.go#L365), [`cmd/adopt_synthesize.go:104`](../../cmd/adopt_synthesize.go#L104).

**Issue:** `currentBody, _ := fsys.ReadFile(...)`. If the file is briefly unreadable (rename-race, EIO, encoding glitch), the rewriter is invoked with an *empty* prior body and prompted to rewrite from scratch. The cascade writes a new body over the previously-good file. The user loses content silently.

**Fix:** treat ReadFile error as fatal unless `errors.Is(err, fs.ErrNotExist)` (in which case there genuinely is no prior body). Never feed a discard-error empty string into a rewriter prompt.

**Source:** silent-failure-hunter #8

### AUDIT-H3 — Fanout cluster payload silently empties on JSON parse failure

**File:** [`internal/agent/projection.go:218-221`](../../internal/agent/projection.go#L218).

**Issue:** `_ = json.Unmarshal([]byte(snap.FanoutItem), &cluster)`. If `FanoutItem` is malformed JSON, the unmarshal error is discarded; `cluster` stays zero-valued; the elaborator is prompted with an empty topic, empty findings, and an empty CurrentCommitmentQuoted. The elaborator emits a degenerate proposal node that `mergeRevisedNodes` happily appends. This is exactly the schema-skeleton failure mode CLAUDE.md flags, riding in via a silent parse drop.

**Fix:** surface the unmarshal error — return an empty messages slice with a sentinel that the merge handler counts as failure, or panic-on-malformed since this is intra-process JSON the workflow produced itself.

**Source:** silent-failure-hunter #5

### AUDIT-H4 — Cycle-detection monitor goes silent after 3 transient errors

**Files:** [`internal/dispatch/monitor.go:74-85`](../../internal/dispatch/monitor.go#L74) + [`internal/dispatch/streaming.go:147-148`](../../internal/dispatch/streaming.go#L147).

**Issue:** after three consecutive `monitorCycle` failures (`circuitTripMax = 3`), `ShouldCheck` returns false for the remainder of the workstream attempt. The monitor LLM errors themselves are never logged — only the verdict path observes `cerr`. A fast-tier provider hiccup at the wrong moment permanently disables churn detection for the run, with no audit trail.

**Fix:** `slog.Warn("monitor: cycle-detection LLM error", "err", cerr)` inside `MarkChecked` (or at the call site); `slog.Warn` when the circuit trips.

**Source:** silent-failure-hunter #4

### AUDIT-H5 — Soft-accept of malformed summaries

**File:** [`internal/agent/workflow_fill_summaries.go:159-166`](../../internal/agent/workflow_fill_summaries.go#L159).

**Issue:** `if !spec.IsWellFormedSummary(summary) { slog.Warn(...); }` — the summary is written to disk regardless. "Soft check — log but accept" with no distinction between cosmetic and substantive failures. Operator catches it only if tailing stderr.

**Fix:** promote one-strike-and-reject for well-formedness rules that catch semantic (not cosmetic) defects, or persist the warning to the per-call session record so it shows up in `history`.

**Source:** silent-failure-hunter #6

### AUDIT-H6 — Strategy.Kind enum-shaped without `enum=` tag (three sites)

**Files:** [`internal/agent/raw_proposal.go:53`](../../internal/agent/raw_proposal.go#L53), [`internal/agent/specgen.go:79`](../../internal/agent/specgen.go#L79), [`internal/agent/elaboration.go:34`](../../internal/agent/elaboration.go#L34).

**Issue:** all three carry only `description=`. Constants are `foundational` / `derived` / `quality` ([`internal/spec/enums.go:14-20`](../../internal/spec/enums.go#L14)). Worse, `RawStrategyProposal.Kind`'s description says "Free-form short label (foundational / operational / security / data / etc.)" — `operational`, `security`, `data` are NOT in the Go enum, and persistence as `spec.Strategy.Kind` produces an unconstrained value.

**Fix:** `enum=foundational,enum=derived,enum=quality` on all three sites; remove the misleading invented kinds from `RawStrategyProposal.Kind`'s description.

**Source:** code-reviewer #5

### AUDIT-H7 — `DecisionSourceRef` has no jsonschema tags despite being on the wire

**File:** [`internal/agent/reconcile.go:139-143`](../../internal/agent/reconcile.go#L139).

**Issue:** `ParentKind` is enum-shaped (`feature` | `strategy`) and ungated. The hand-authored `ReconciliationVerdict` override at [`internal/agent/schemas.go:311-343`](../../internal/agent/schemas.go#L311) embeds the reflected source schema via `reflectStrictSchema(DecisionSourceRef{})`, so the absence of tags lands in the wire schema.

**Fix:** add `jsonschema:"enum=feature,enum=strategy,description=..."` to `ParentKind`; descriptions to `ParentID` / `Index`.

**Source:** code-reviewer #4

### AUDIT-H8 — `TestGenerateSpec*` tests don't exercise production workflow

**File:** [`internal/agent/specgen_test.go:47-66, 141-336`](../../internal/agent/specgen_test.go#L47).

**Issue:** every named `TestGenerateSpec*` test calls `generateSpecWithWorkflow(...testSpecGenWorkflow)` — a simplified pre-DJ-098 council shape. The production `SpecGenerationWorkflow` (outline + per-cluster fanout + gate spawner) has NO direct test. This is the same pattern caught in DJ-123 Phase 3 (`TestGenerateSpecWiresInFlightIndex` didn't invoke GenerateSpec) — recurs broadly.

**Fix:** add at least one end-to-end smoke test driving `SpecGenerationWorkflow` itself with scripted mock responses, so future refactors of the production shape are observable.

**Source:** pr-test-analyzer #2

### AUDIT-H9 — MemFS `sync.RWMutex` has no race exercise

**File:** [`internal/specio/memfs.go:23`](../../internal/specio/memfs.go#L23) and tests at [`internal/specio/specio_test.go`](../../internal/specio/specio_test.go).

**Issue:** commit `7f13ed5` added `sync.RWMutex` specifically so parallel-fanout tests (FillSummariesWorkflow et al.) could share a single MemFS across goroutines. Every test in `specio_test.go`, `atomic_test.go`, `pair_callback_test.go` is sequential. Dropping the lock wouldn't fail CI.

**Fix:** add a fanout test — N goroutines calling Open/ReadFile/WriteFile/Stat over interleaved disjoint paths, run under `-race`.

**Source:** pr-test-analyzer #1

### AUDIT-H10 — spec_search wrapper drops per-field diagnostics with no test coverage

**Files:** [`internal/agent/spec_tools.go:622, 641-651`](../../internal/agent/spec_tools.go#L622), test at [`internal/agent/spec_tools_search_test.go:115-165`](../../internal/agent/spec_tools_search_test.go#L115).

**Issue:** `SearchSpecNodes` explicitly sets `Explain: true` and copies per-field Matches/Contribution/ContributionPct from `search.Hit` into `SpecSearchHit`. The wrapper test only asserts `ID`, `Title`, `Kind`, `Summary`, `TotalMatches > 0`. `Score` and `Matches` are never inspected. A refactor that flips `Explain: false` or drops the copy loop would not fail any test — and the agent prompt depends on those diagnostics.

**Fix:** assert `top.Score > 0` and `top.Matches` contains at least one field entry with non-empty Terms and ContributionPct summing approximately to 1.0.

**Source:** pr-test-analyzer #3

### AUDIT-H11 — `internal/memory/**` is a dead package

**File:** [`internal/memory/`](../../internal/memory/).

**Issue:** `grep -rn "internal/memory"` across the worktree returns only `memory_test.go`. The exported `Service` interface ([`internal/memory/service.go:53`](../../internal/memory/service.go#L53)) has no caller.

**Fix:** delete the package unless there's a near-term consumer. If it's reserved for a coming DJ, ship it with that DJ rather than carrying dead surface. **Directional call needed from user before deletion.**

**Source:** code-reviewer #10

### AUDIT-H12 — `cmd/adopt.go:847` references nonexistent `SupervisorConfig.PolicyForStep`

**File:** [`cmd/adopt.go:847`](../../cmd/adopt.go#L847).

**Issue:** comment cites `SupervisorConfig.PolicyForStep` (doesn't exist; DJ-121 renamed to `PolicyForWorkstream` at [`internal/dispatch/supervisor.go:50`](../../internal/dispatch/supervisor.go#L50)). Same paragraph also says "per-step permission policy" — policy is per-workstream now.

**Fix:** s/PolicyForStep/PolicyForWorkstream/; drop "per-step" from the prose.

**Source:** comment-analyzer #3

### AUDIT-H13 — Three Genkit references in active source after DJ-099

**Files:** [`internal/agent/session.go:283`](../../internal/agent/session.go#L283), [`internal/agent/specgen.go:114`](../../internal/agent/specgen.go#L114), [`internal/agent/projection.go:327`](../../internal/agent/projection.go#L327).

**Issue:** comments reference "Genkit" / "Genkit's tool-dispatch loop" / "Genkit runtime in cmd/llm.go" / "Genkit's structured-output path" — Locutus uses direct provider SDKs since DJ-099. The actual paths are the executor's dispatcher (`internal/agent/dispatcher.go`), the adapter layer (`internal/agent/adapters/`), and the executor's ToolRegistry.

**Fix:** one-word substitutions per site (`"Genkit"` → "the adapter layer" / "the executor's tool registry" / equivalent).

**Source:** comment-analyzer #5

### AUDIT-H14 — `internal/search/search.go` package doc claims Phase 1/2 state that no longer matches

**File:** [`internal/search/search.go:9-19`](../../internal/search/search.go#L9). Also [`internal/search/build.go:221-223`](../../internal/search/build.go#L221), [`:253-254`](../../internal/search/build.go#L253), [`internal/search/fingerprint.go:17`](../../internal/search/fingerprint.go#L17).

**Issue:** line 9 claims "No CLI verb consumes it yet" — false (`cmd/list.go:110`, `cmd/searchhook.go`). Lines 18-19 cite `(*Index).Update`/`Delete` as "reserved for Phase 2" — those methods don't exist; the mutation hook ships and lives in `cmd/searchhook.go` calling `*bluge.Writer.Update`/`Delete` directly via `search.OpenWriterWithRetry`. Build.go and fingerprint.go comments similarly stale.

**Fix:** rewrite the package doc to enumerate what actually ships (`Open`, `OpenInMemory`, `(*Index).Search`, `OpenWriterWithRetry`, `BuildDocument`); replace "Phase 1/Phase 2" framing with a one-line pointer to `cmd/searchhook.go`. Same edit catches the stale fragments in build.go and fingerprint.go.

**Source:** comment-analyzer #1

## MEDIUM

### AUDIT-M1 — `Concern.Kind` enum-shaped but missing `enum=` tag

**File:** [`internal/agent/state.go:19`](../../internal/agent/state.go#L19).

**Issue:** description names "integrity / architecture / devops / sre / cost"; no `enum=`. `Concern.Severity` IS correctly tagged on the same struct — the inconsistency is the tell.

**Fix:** add `enum=` mirroring the prose set.

**Source:** type-design-analyzer #5

### AUDIT-M2 — `AssimilationContribution.Gap.Category` enum-shaped but no `enum=` tag

**File:** [`internal/agent/assimilation.go:85`](../../internal/agent/assimilation.go#L85).

**Issue:** description names a closed set ("missing_tests; orphan_code; …"); no `enum=`. `Severity` IS tagged.

**Fix:** add `enum=` mirroring the description.

**Source:** type-design-analyzer #5

### AUDIT-M3 — `OutlineStrategy.Kind` missing `enum=` tag

**File:** [`internal/agent/elaboration.go:34`](../../internal/agent/elaboration.go#L34).

**Issue:** description names "foundational/derived/quality"; no `enum=`.

**Fix:** subsumed by [[AUDIT-H6]] fix — same Strategy.Kind constants.

**Source:** type-design-analyzer #5

### AUDIT-M4 — `StrategyProposal.Kind` missing `enum=` tag

**File:** [`internal/agent/specgen.go:79`](../../internal/agent/specgen.go#L79).

**Issue:** same as AUDIT-M3 / AUDIT-H6 — third site for Strategy.Kind.

**Fix:** subsumed by AUDIT-H6 batched fix.

**Source:** type-design-analyzer #5

### AUDIT-M5 — `spec.Approach` lacks `json` tags entirely

**File:** [`internal/spec/approach.go:14-44`](../../internal/spec/approach.go#L14).

**Issue:** approach is markdown-only on disk so it carries `yaml` tags. But it's embedded in `AssimilationContribution.Approaches` (LLM JSON response), in `RegenerateApproachContext.PriorApproach` (marshalled into prompt JSON via `BuildSupersedeDecisionPrompt`/`buildRegenerateApproachPrompt`), and read out for snapshot rendering. The model sees Go-cased `"ID"` / `"ParentID"` / `"InvalidatedByEventID"` instead of snake_case.

**Fix:** add `json:"..."` mirrors for every field.

**Source:** type-design-analyzer #2

### AUDIT-M6 — `ConvergenceVerdict` — registered-by-association LLM shape with no jsonschema tags, prose-parsed

**File:** [`internal/agent/convergence.go:10`](../../internal/agent/convergence.go#L10).

**Issue:** `RunCouncil`'s convergence monitor parses the response via `parseConvergenceResponse` (string-scanning) rather than strict-mode JSON. Comment in `workflow_spec_generation.go:69` says "the older type stays for the remaining callers." Worst-of-both-worlds shape — has `json:"..."` tags hinting structure, but no schema enforcement and a lenient prose scanner.

**Fix:** either promote to strict-mode with `jsonschema:"enum=..."` on a verdict enum (like `SynthesisVerdict`), or drop the `json` tags and make the prose-parsing nature explicit.

**Source:** type-design-analyzer #3

### AUDIT-M7 — `PlanningState` is overloaded with operational pointers and pre-encoded JSON strings

**File:** [`internal/agent/state.go:34-124`](../../internal/agent/state.go#L34).

**Issue:** two `json:"-"` fields (`ConflictActions []AppliedAction`, `Existing *ExistingSpec`) signal operational overload. ~20-field surface; many strings carry serialised JSON (`RawProposal string`, `OriginalRawProposal string`, `Outline string`, `ScoutBrief string`) — double JSON pass at every projection boundary.

**Fix sketch:** split `PlanningState` into a serializable `PlanningSnapshot` (only what projections need) plus an unexported runtime struct (`Existing`, `ConflictActions`, `GateAxisRecurrence`, etc.). Lower-priority: replace JSON-string fields with typed structs.

**Source:** type-design-analyzer #4

### AUDIT-M8 — `SupersedeContext.OldNode any`

**File:** [`internal/agent/refiner_supersede.go:80`](../../internal/agent/refiner_supersede.go#L80).

**Issue:** each of `InvokeSupersedeDecision/Feature/Strategy` immediately does a type-switch (`sctx.OldNode.(*spec.Decision)`), returning an error on mismatch. Invariant "right OldNode for the right invoker" is unencoded.

**Fix:** three concrete context types (`SupersedeDecisionContext`, `SupersedeFeatureContext`, `SupersedeStrategyContext`) with typed `OldNode *spec.Decision` etc. Shared fields can embed a small `supersedeBase` struct.

**Source:** type-design-analyzer #6

### AUDIT-M9 — `internal/agent/specgen.go` stale comment + dead `CritiqueRounds` field

**Files:** [`internal/agent/specgen.go:90-102`](../../internal/agent/specgen.go#L90), [`cmd/specgen.go:55-61`](../../cmd/specgen.go#L55).

**Issue:** comment says "Capability, Model, and CritiqueRounds are retained for backwards compatibility but are advisory." The struct only has `CritiqueRounds`; `Capability` and `Model` don't exist. `CritiqueRounds` has zero readers — `grep -rn CritiqueRounds` returns only the field declaration plus `cmd/specgen.go:55-61` setting it to 1. The default-to-1 dance does nothing. Comment about "propose→critique→revise cycle catches dangling references" describes a workflow shape gone since DJ-098.

**Fix:** delete the `CritiqueRounds` field, the dead `if req.CritiqueRounds == 0 { ... }` block in `cmd/specgen.go`, and the stale comment.

**Source:** comment-analyzer #2 + code-reviewer #11 (independent confirmation)

### AUDIT-M10 — `internal/agent/reconcile.go:36-37` cites a removed Genkit file

**File:** [`internal/agent/reconcile.go:36-37`](../../internal/agent/reconcile.go#L36).

**Issue:** comment says "When tools are attached on Gemini routes, the API silently disables JSON mode (`plugins/googlegenai/gemini.go:311`)…". Per DJ-099 the project uses `google.golang.org/genai`, not Genkit plugins; `grep -rn googlegenai` returns one hit (this comment). The defensive fence-strip behavior may still be needed but the citation must be re-pointed or dropped.

**Fix:** drop the file:line, rephrase as a behavior observation about the current `genai` adapter, or remove the parenthetical entirely.

**Source:** comment-analyzer #4

### AUDIT-M11 — `internal/dispatch/dispatcher.go` uses pre-DJ-119 "streaming-driver" terminology

**File:** [`internal/dispatch/dispatcher.go:97, 113`](../../internal/dispatch/dispatcher.go#L97).

**Issue:** doc comments on `WorkstreamResult` and `ResumePoint` say "streaming-driver session ID" / "streaming-driver conversation ID." Elsewhere in the same package the value is correctly called "ACP session ID" (`supervisor.go:64-69`, `streaming.go:91-92`). Inconsistent naming on the two most-read structs.

**Fix:** s/streaming-driver/ACP/ in both spots.

**Source:** comment-analyzer #6

### AUDIT-M12 — `internal/agent/schemas.go` `MasterPlan` example uses placeholder tokens

**File:** [`internal/agent/schemas.go:22-27`](../../internal/agent/schemas.go#L22).

**Issue:** `RegisterSchema("MasterPlan", spec.MasterPlan{ ID: "plan-XXX", ..., Workstreams: []{{ ID: "ws-XXX", ... }} })`. Direct CLAUDE.md §5 violation — example payloads must use descriptive prose, never `dummy`/`placeholder`/`TBD`/`foo`. `plan-XXX`/`ws-XXX` are direct examples of schema-skeleton priming. Other example payloads in the same file (`feat-realtime-dashboard`, `Real-time dashboard`) get this right.

**Fix:** use real-looking slugs (`plan-onboarding-revamp`, `ws-server-api`) consistent with the rest of the file.

**Source:** comment-analyzer #7

### AUDIT-M13 — `internal/agent/specgen.go:248` names a workflow step that no longer exists

**File:** [`internal/agent/specgen.go:248`](../../internal/agent/specgen.go#L248).

**Issue:** comment says "RoundResult.Output holds the raw agent text (verdict JSON for reconcile, raw proposal JSON for propose/revise)…". There is no `propose` step in `NewSpecGenerationWorkflow` (workflow_spec_generation.go:168-228) — it's `elaborate_features` / `elaborate_strategies`, with revisions driven per-cluster from `gate` verdicts. The "propose/revise" labels are pre-DJ-098.

**Fix:** replace "propose/revise" with "elaborate / revise fanout".

**Source:** comment-analyzer #8

### AUDIT-M14 — Duplicate types across `adapters` and `agent` — drift risk

**Files:** [`internal/agent/adapters/adapter.go:132, 308, 318, 343`](../../internal/agent/adapters/adapter.go#L132) vs [`internal/agent/executor.go:35, 85, 95, 110`](../../internal/agent/executor.go#L35).

**Issue:** `adapters.Citation` vs `agent.Citation`, `adapters.Message` vs `agent.Message`, `adapters.ToolCall` vs `agent.ToolCall`, `adapters.Round` vs `agent.GenerateRound` — pairs duplicated because adapters can't import agent (would cycle). Translation layer at `Executor.Run` copies fields positionally. A field added to one and not the other is silently dropped.

**Fix:** LINT-3 — reflect both struct field sets, assert agreement.

**Source:** type-design-analyzer #7

### AUDIT-M15 — `search.Hit` ↔ `SpecSearchHit` and `FieldMatch` ↔ `SpecFieldMatch` duplication

**Files:** [`internal/search/search.go:79, 101`](../../internal/search/search.go#L79) vs [`internal/agent/spec_tools.go:532, 550`](../../internal/agent/spec_tools.go#L532).

**Issue:** same shape, different tags. Translation in `SearchSpecNodes` is field-for-field copying. Same drift risk as AUDIT-M14.

**Fix:** add to LINT-3's scope, or accept as documented duplication (the agent-package versions add JSON tags tuned for the LLM surface — design pressure is mostly the tag mismatch).

**Source:** type-design-analyzer #8

### AUDIT-M16 — `cmd/adopt.go:827-832` long-standing TODO without owner

**File:** [`cmd/adopt.go:827-832`](../../cmd/adopt.go#L827).

**Issue:** load-bearing TODO documenting a known security-sensitive behavior (deny-by-default for prompt-injected tool calls in the Guardian path) — but no DJ or issue ID attached since DJ-119/DJ-121.

**Fix:** append the DJ id this work blocks on, or upgrade to a DJ entry and leave a one-line pointer in the code.

**Source:** comment-analyzer #9

### AUDIT-M17 — `.borg/models.yaml` I/O errors silently fall through to defaults

**File:** [`internal/agent/model_config.go:194-206`](../../internal/agent/model_config.go#L194).

**Issue:** comment says "missing or unreadable falls through silently." Treating *missing* this way is reasonable; treating *unreadable* (EACCES, EIO, bad mode bits) this way isn't — a user whose `.borg/models.yaml` is intermittently unreadable silently gets embedded defaults instead of pinned models, and only realises after a cost spike.

**Fix:** distinguish `errors.Is(err, fs.ErrNotExist)` (silent) from any other ReadFile error (wrapped return).

**Source:** silent-failure-hunter #7

### AUDIT-M18 — Citation extraction silently swallows malformed adapter JSON

**Files:** [`internal/agent/adapters/citations.go:80-82`](../../internal/agent/adapters/citations.go#L80) (OpenAI), and similar in `extractGeminiCitations` / `extractAnthropicCitations` via type-assertion drops.

**Issue:** `if err := json.Unmarshal(rawOutput, &items); err != nil { return nil }`. If a provider SDK changes its output-array shape upstream, citations vanish from `AgentOutput.Citations` and `ToolCall.Sources` with no signal. Justify / research outputs that depend on grounded citations silently regress to ungrounded prose.

**Fix:** `slog.Warn` on unmarshal error including the agent ID — at minimum a per-process one-shot.

**Source:** silent-failure-hunter #9

### AUDIT-M19 — `mergeGateVerdict` no-ops on parse failure, trusts a sibling

**File:** [`internal/agent/workflow_spec_generation.go:570-577`](../../internal/agent/workflow_spec_generation.go#L570).

**Issue:** silently drops gate output if parse fails, relying on a cross-cutting contract ("the Spawn closure parses again") that isn't enforced. A future refactor changing Spawn order or removing the re-parse would silently disable the gate's contribution to `Concerns`/`FindingClusters` while still terminating each iteration normally.

**Fix:** propagate the parse error (via state.Failed), or assert that `parseSpecGateVerdict` was called by the spawner in this round.

**Source:** silent-failure-hunter #10

### AUDIT-M20 — Tautological error assertion in spec-gate test

**File:** [`internal/agent/workflow_spec_generation_test.go:490`](../../internal/agent/workflow_spec_generation_test.go#L490).

**Issue:** `assert.True(t, errors.Is(err, err), "error returned (parse failure or related)") // tautology; main check is non-nil`. The comment admits it. A regression that wraps the parse failure in a generic error type (losing parse-specific identity) would still pass.

**Fix:** define a sentinel (`ErrSpecGateParse`), wrap with `%w`, assert `errors.Is(err, ErrSpecGateParse)`. Or assert error-message substring.

**Source:** pr-test-analyzer #5

### AUDIT-M21 — Lint suite out of sync with the validator it claims to mirror

**Files:** [`cmd/schema_conventions_test.go:42-55`](../../cmd/schema_conventions_test.go#L42) vs [`internal/agent/justify.go:400-415`](../../internal/agent/justify.go#L400).

**Issue:** lint header says "Keep in sync with agent.challengerPlaceholderTokens." Lint list: `dummy, placeholder, tbd, foo, bar, baz, lorem, ipsum`. Validator list adds: `todo, example, sample, n/a, none, "..."`. A schema description that includes "for example, render the value" or "use 'todo' to mark open questions" slips the lint, ships, trips the runtime validator.

**Fix:** LINT-4 — export `challengerPlaceholderTokens` from `internal/agent` and import from the lint test so drift becomes structurally impossible.

**Source:** pr-test-analyzer #6

### AUDIT-M22 — Adapter packages hardcode env-var strings

**Files:** [`internal/agent/adapters/anthropic.go:57`](../../internal/agent/adapters/anthropic.go#L57), [`internal/agent/adapters/openai_responses.go:37`](../../internal/agent/adapters/openai_responses.go#L37), [`internal/agent/adapters/gemini.go:44-46`](../../internal/agent/adapters/gemini.go#L44).

**Issue:** `internal/agent/providers.go:10-17` defines `EnvKeyAnthropicAPI`, `EnvKeyGeminiAPI`, `EnvKeyGoogleAPI`, `EnvKeyOpenAIAPI`. Adapters call `os.Getenv("ANTHROPIC_API_KEY")` etc. directly. Adapters can't import agent (would cycle), so the constants are unreachable.

**Fix:** move env-var constants into a leaf package both layers can import (e.g. `internal/agent/adapters/envkeys.go`); have `internal/agent` re-export.

**Source:** code-reviewer #7

### AUDIT-M23 — `.borg/spec/...` literals in 200+ sites

**Files:** [`internal/agent/assimilation.go:260`](../../internal/agent/assimilation.go#L260), [`cmd/adopt.go:475-482`](../../cmd/adopt.go#L475), [`cmd/adopt_synthesize.go:65, 104, 138-159`](../../cmd/adopt_synthesize.go#L65), [`cmd/adopt_regenerate.go:90-121`](../../cmd/adopt_regenerate.go#L90), [`cmd/assimilate_persist.go:69-109`](../../cmd/assimilate_persist.go#L69), [`cmd/import.go:375`](../../cmd/import.go#L375), [`cmd/specgen_archive.go:50`](../../cmd/specgen_archive.go#L50), [`cmd/specgen.go:112`](../../cmd/specgen.go#L112), more.

**Issue:** `internal/search/fingerprint.go:18` already defines `const specRoot = ".borg/spec"` (unexported); `internal/specio/projectroot.go:13` defines `ProjectRootMarker`. No canonical home for `.borg/spec`, `.borg/agents`, `.borg/history`, the `.archived/` subdir, or per-kind subpaths (`features`, `decisions`, etc.).

**Fix:** promote one set of exported constants in `internal/specio` (or new `internal/borgpath`); replace literals package-by-package.

**Source:** code-reviewer #8

### AUDIT-M24 — Dead `StreamParser` interface (DJ-119 holdover)

**File:** [`internal/dispatch/events.go:12-22`](../../internal/dispatch/events.go#L12).

**Issue:** interface comment describes reading "provider-specific NDJSON" — a wire layer DJ-119 explicitly retired. No production type satisfies the interface; `grep` confirms only the type definition and a worktree-private test fixture. `AgentEvent` is still load-bearing under ACP; `StreamParser` is the holdover.

**Fix:** delete the interface (and the comment that mentions NDJSON).

**Source:** code-reviewer #9

### AUDIT-M25 — `justify_researcher.md` extended "DO NOT" cascade

**File:** [`internal/scaffold/agents/justify_researcher.md:38-67`](../../internal/scaffold/agents/justify_researcher.md#L38).

**Issue:** five back-to-back negative instructions ("DO NOT FALL BACK TO TRAINING-DATA RECALL," "Do not substitute training-data recall," "Do not invent dates, citations, version numbers, or source URLs from memory," "do not paper over with training-data recall," "Do not introduce citations that did not appear..."). Per `docs/agent-conventions.md` §1, this priming-by-negation is what caused the `justify_synthesizer` regression. The current Phase-4 lint catches specific anti-pattern wordings but doesn't catch *cascade* shape.

**Fix:** rewrite positively — "When search errors, set result to the literal sentinel below and stop. When search succeeds, cite the exact URLs returned." Sentinels and structural enforcement (e.g. ungrounded-finding flag in the schema) carry the load prose currently tries to.

**Source:** code-reviewer #12

### AUDIT-M26 — `cost_critic.md` "Do NOT add categories to your output schema"

**File:** [`internal/scaffold/agents/cost_critic.md:42`](../../internal/scaffold/agents/cost_critic.md#L42).

**Issue:** tells the model to ignore the schema-stability instruction — but the OutputSchema already pins `CriticIssues`. §4 anti-pattern: re-explaining shape constraints the schema enforces.

**Fix:** delete the line.

**Source:** code-reviewer #13

### AUDIT-M27 — `RegisterSpecTools` empty-root branch untested

**Files:** [`internal/agent/spec_tools.go:727-732`](../../internal/agent/spec_tools.go#L727), test at [`internal/agent/spec_tools_search_test.go:167-178`](../../internal/agent/spec_tools_search_test.go#L167).

**Issue:** no test calls `RegisterSpecTools(registry, fsys, "")` (or post-DJ-123: `nil` backend) and asserts the registry has spec_list_manifest + spec_get but NOT spec_search. An inverted condition would register a handler that errors on every call.

**Fix:** add `TestRegisterSpecTools_EmptyRootSkipsSpecSearch` (note: post-DJ-123 this is `TestRegisterSpecTools_NilBackendSkipsSpecSearch`, which DOES exist — verify the assertion shape against this finding).

**Source:** pr-test-analyzer #4

### AUDIT-M28 — `RunWithRetry` has multiple uncovered branches

**Files:** [`internal/agent/retry_test.go`](../../internal/agent/retry_test.go) (only 2 tests), source at [`internal/agent/retry.go`](../../internal/agent/retry.go).

**Issue:** uncovered: MaxAttempts exhaustion (returns lastErr at line 96), non-retryable error short-circuit (51-53), ctx cancellation during backoff (82-83 returns `ErrTimeout`), retry callback firing (56-58), MaxDelay cap on exponential growth (90-93). A regression that mis-categorises an error type as retryable, fails to honor ctx mid-sleep, or removes the MaxDelay cap would not fail any test.

**Fix:** table-driven tests for each branch using `recordingExec`.

**Source:** pr-test-analyzer #7

### AUDIT-M29 — `cmd/sink_cli.go` `plainSink` has zero test coverage

**File:** [`cmd/sink_cli.go:44-65`](../../cmd/sink_cli.go#L44) (plainSink + newPlainSink + OnEvent).

**Issue:** `grep -rn "TestPlainSink"` returns nothing. The `--plain` mode is the format machine consumers (CI, logs) parse. Format regressions (`step=X agent=Y` shape, timestamp format) would land silently.

**Fix:** one test writing to a `bytes.Buffer`, asserting the exact line shape for started/completed/error events.

**Source:** pr-test-analyzer #8

### AUDIT-M30 — `TestCLISinkRendersAgentLifecycle` doesn't actually test lifecycle

**File:** [`cmd/sink_cli_test.go:50-64`](../../cmd/sink_cli_test.go#L50).

**Issue:** sends "started" event, asserts spinner exists, sends "completed" event — but never inspects post-completed state (removal from map, duration recorded, etc.). The pterm race the user flagged is orthogonal to the assertion gap. As written, this is closer to "ran without panic" than "renders agent lifecycle."

**Fix:** either `t.Skip("pterm race; see TODO/DJ-xxx")` with a sync shim plan, or capture spinner state after completed and assert (spinner removed, `s.starts["survey/spec_scout"]` cleared).

**Source:** pr-test-analyzer #9

### AUDIT-M31 — Eval tests are CI-orphans

**Files:** [`internal/agent/spec_search_eval_test.go`](../../internal/agent/spec_search_eval_test.go), [`internal/agent/output_formatter_eval_test.go`](../../internal/agent/output_formatter_eval_test.go).

**Issue:** both gated behind `//go:build eval`. `.github/workflows/ci.yml:29` runs only `go test ./... -count=1` — no `-tags eval`. No Makefile / scripts / workflow invokes them. Header comments document how to run locally but nothing references the eval tag in wired-up automation.

**Fix:** either add a manual-trigger workflow (`workflow_dispatch` running `-tags=eval` with secret-injected API keys) and document the cadence in CLAUDE.md, or drop the build tag and `t.Skip` when keys aren't set so they run as no-op smoke in normal CI.

**Source:** pr-test-analyzer #10

### AUDIT-M32 — `CheckReadiness` has no direct test

**File:** [`internal/agent/convergence.go:106-131`](../../internal/agent/convergence.go#L106).

**Issue:** only exercised indirectly through RunCouncil tests. The "BLOCKED" string match at line 125 is case-aware (uppercases first) but no test pins: empty content treated as approved, mixed-case "blocked" prose, prose like "we are not blocked" which would false-match if `Contains` were ever swapped to a substring without word boundaries.

**Fix:** table-driven `TestCheckReadiness` with content variants {APPROVED, "approved", "BLOCKED: x", "we approve, not blocked", ""} and a mock executor.

**Source:** pr-test-analyzer #11

## LOW (cosmetic / informational)

### AUDIT-L1 — `Verdict` field in `justify_synthesizer.go` has `enum=` but no `description=`

**File:** [`internal/agent/justify_synthesizer.go:39`](../../internal/agent/justify_synthesizer.go#L39).

**Issue:** enum tags present, but every other verdict-shaped field in the codebase carries a description. Inconsistency.

**Fix:** add a short description for consistency.

**Source:** code-reviewer #14

### AUDIT-L2 — `PerDecisionResult.Verdict` comment-only enum

**File:** [`internal/agent/justify_synthesizer.go:97-104`](../../internal/agent/justify_synthesizer.go#L97).

**Issue:** input struct (not a registered LLM output) so CLAUDE.md doesn't bind, but comment-only enum is a foot-gun for callers.

**Fix:** typed `Verdict` enum or validated constants.

**Source:** code-reviewer #15

### AUDIT-L3 — `spec.MCPResponse` anemic, stringly-typed Status

**File:** [`internal/spec/response.go:4`](../../internal/spec/response.go#L4).

**Issue:** `Status string` (no enum), `Data any`, `Errors []string`. It's a wire protocol — partial pass. `FileChange.Action string` similarly stringly-typed; given the small closed set (`created` / `modified` / `deleted`), promote to typed enum.

**Fix:** typed status / action enums.

**Source:** type-design-analyzer #9 (smaller findings)

### AUDIT-L4 — `Workstream.Steps` deprecated but still required

**File:** related to plan.go schemas.

**Issue:** description marks it DEPRECATED but the planner schema doc still requires it — dual-write keeps the deprecation theatrical.

**Fix:** remove or wire deprecation into real validation.

**Source:** type-design-analyzer #9 (smaller findings)

### AUDIT-L5 — `Concern` constructor allows invalid state

**File:** [`internal/agent/state.go`](../../internal/agent/state.go).

**Issue:** `Concern.AgentID` and `Concern.Kind` get defaulted at merge time — type lets callers construct invalid `Concern` and patches it.

**Fix:** `NewConcern(agentID, severity, ...)` constructor so the default path is the only way in.

**Source:** type-design-analyzer #9 (smaller findings)

---

## Suggested execution order

Fix order proposed by reviewers and re-ranked by impact:

1. **AUDIT-C1** — `DetailLevel` enum drift (5-line fix, runtime bug)
2. **AUDIT-C2** — `spec_architect.md` scout_brief contradiction (3-line fix, wrong citations every run)
3. **AUDIT-H2** — Cascade/refine ReadFile error handling (data-loss path)
4. **AUDIT-C3** — Default-permission fail-open (security posture)
5. **AUDIT-C5 + LINT-1** — `spec.*` jsonschema tags + the schema-description lint (leverage move; simplifies validators)
6. **AUDIT-C6 + LINT-2** — `Assertion.Kind` fix + enum-walker lint
7. **AUDIT-C4** — ACP channel-close (correctness)
8. **AUDIT-H1** — `workflow_refine` failure recording (UX)
9. **AUDIT-H8 + AUDIT-H9** — production-workflow test + MemFS race test (regression-net)
10. **AUDIT-M9** — Delete `CritiqueRounds` + stale comment
11. **AUDIT-M24, AUDIT-H11** — Delete dead surface (`StreamParser`, `internal/memory`)
12. **Comment-rot cleanup batch** — AUDIT-H12, H13, H14, M10, M11, M13 (one PR; mechanical)
13. **AUDIT-M22, M23** — Constant promotion (env keys, borg paths)
14. **Remaining MEDIUM and LOW** — at owner's pace

## What this audit does NOT cover

- The DJ-123 work itself (`worktree-dj-123-inflight-search` branch). That had per-phase implementer → spec-reviewer → code-quality-reviewer cycles.
- Performance, profiling, allocation patterns. Out of scope for these reviewers.
- Documentation outside code and prompts (docs/DECISION_JOURNAL.md, docs/agent-conventions.md themselves). Reviewers read these as source of truth, not as audit targets.
- The MCP wire surface explicitly. Some findings touch tool descriptors but the MCP transport itself wasn't audited.
- ACP transport correctness beyond the channel-close issue. The supervisor / connection layer would warrant its own focused review.

## Worktree status

The 5 review worktrees still exist at:

- `.claude/worktrees/review-silent` (branch `review-silent-failure`)
- `.claude/worktrees/review-types` (branch `review-type-design`)
- `.claude/worktrees/review-comments` (branch `review-comment-analysis`)
- `.claude/worktrees/review-tests` (branch `review-pr-test`)
- `.claude/worktrees/review-code` (branch `review-code-quality`)

All at `main@31e5923` in detached state (one branch each). Safe to `git worktree remove` once this plan is reviewed.
