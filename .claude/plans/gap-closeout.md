# Plan: DJ Gap Closeout (Rounds 1–8)

## Context

The 2026-04-23 audit surfaced a recurring "designed but unimplemented" pattern. The journal backfill (DJ Status field, committed in [2bab420](../../commit/2bab420)) now makes the gap list self-describing: six `settled` DJs and nine `shipping` DJs describe work that was decided but isn't fully observable in code. Plus a handful of implementation gaps that never got their own DJ.

This plan closes eight of those gaps, in priority order of user-visible impact per hour of work. The order was decided in the audit conversation and preserved here.

**Pre-Round-3 increment (DJ-077 selective ADK adoption):** adopts the `memory.Service` shape from Google ADK as `internal/memory/`, closing the agent-memory gap ahead of the rounds that could use it. See [gap-closeout-pre-round3-memory.md](gap-closeout-pre-round3-memory.md). Reshapes Round 4 (llm_review) to port adk-python's evaluation framework rather than writing a reviewer agent from scratch — effort grows from ~0.5 to ~2 sessions but the result is reusable eval plumbing.

## Method (every round)

Each round follows the same loop we used for Phase C:

1. **Cite governing DJs with Status.** Before writing code, state the DJ number, its current `Status:`, and the constraint it imposes. If the DJ is `settled` or `shipping`, note which part is in vs. out.
2. **Resolve ambiguities in chat.** Each round below lists the design questions that need user input (or an explicit "I'll decide X") before implementation.
3. **Write acceptance tests first.** Tests describe the behavior the round ships. They're the definition of done.
4. **Sketch the design.** Types, functions, integration points, non-goals. Small enough to discuss before coding.
5. **Implement until acceptance tests pass.** Plus race, vet, and full suite green.
6. **Update the governing DJ's Status** (`settled → shipping` or `shipping → shipped`). If scope had to shrink, propose a spin-off DJ capturing what's still pending.
7. **Commit** as a single feat/fix per conventional-commits rules.

**Non-negotiable rule:** no round starts implementation until its ambiguity list is resolved in chat. No improvising in code.

---

## Round 1: Assimilate Output Persistence

**Governing:** implementation gap (not a DJ). Touches DJ-014 (shipped — brownfield self-analysis), DJ-019 (shipped — heuristic first, LLM second), DJ-045 (shipping — gap analysis + remediation).

**Problem:** `RunAssimilate` produces a full `AssimilationResult` — inferred Features, Decisions, Strategies, Approaches, Entities, Gaps — and prints a count. Nothing is written to `.borg/spec/`. The pipeline works; its output is thrown away unless you pipe `--json`.

### Ambiguities to resolve

1. **Destination semantics.** Straight overwrite into `.borg/spec/`, or stage into `.borg/spec/.pending/` and require a `locutus assimilate --commit` step? Staging is safer but adds a concept.
2. **Conflict handling.** If a feature already exists at `.borg/spec/features/feat-x.json` (hand-authored or prior assimilate), does the new one overwrite, skip, or collide into an error?
3. **`status: inferred` vs. `status: assumed` vs. `status: active`.** DJ-019 says heuristic-derived goes in as `inferred`; LLM-enriched gets rationale added. What status do nodes actually land at? The Decision type already has `DecisionStatusAssumed` — is there a parallel `inferred` status?
4. **Idempotence.** Two consecutive `assimilate` runs against the same codebase should produce the same spec. Currently non-deterministic because LLM. Do we snapshot-test, or accept drift?
5. **Does `--dry-run` already do the right thing here?** Yes — `readOnlyFS` wraps writes; once we add the writes, `--dry-run` preserves the preview semantic for free.

### Acceptance tests

- `TestRunAssimilatePersistsInferredSpec`: MemFS fixture with a known Go project structure → `assimilate` run → verify `.borg/spec/features/*.json`, `decisions/*.json`, `strategies/*.json`, `approaches/*.md` land on disk matching the `AssimilationResult`.
- `TestRunAssimilateDryRunDoesNotWrite`: same fixture with `DryRun: true` → no files created.
- `TestRunAssimilateConflictPolicy`: pre-seed a feature, run assimilate, verify the conflict policy (decided in ambiguity 2) is honored.
- `TestRunAssimilateIdempotentOnSameInput`: mock LLM with fixed responses → run twice → identical spec tree.

### Design sketch

```go
// RunAssimilate gains a persistence pass after the pipeline returns:
result, err := agent.Analyze(ctx, llm, effective, req)
if err != nil { ... }
if !dryRun {
    if err := persistAssimilationResult(fsys, result, conflictPolicy); err != nil {
        return result, err
    }
}
```

New helper `persistAssimilationResult(fsys, result, policy)` in `cmd/assimilate.go` iterates over each spec kind and calls `specio.SavePair` / `SaveMarkdown`.

### Non-goals

- Remediation loop (Round 5).
- Deleting spec nodes that no longer match the code (that's reverse-drift, belongs in `adopt`).
- Retrofitting existing `.borg/` with post-hoc status fields.

### Files

`cmd/assimilate.go`, `cmd/assimilate_test.go`.

### Effort

~0.5 session.

---

## Round 2: Historian LLM Narrative Layer

**Governing:** DJ-026 (**settled**) — "The historian has two layers. Layer 1 (deterministic): records structured JSON events. Layer 2 (LLM): writes a compelling human-readable narrative."

Layer 1 ships. Layer 2 doesn't. `locutus history --narrative` errors because `.borg/history/summary.md` never gets written.

### Ambiguities to resolve

1. **When does narrative regenerate?** Options: (a) every event append (expensive — rewrites the whole file every time); (b) on-demand via `locutus history --regenerate-narrative`; (c) debounced — append event, then regenerate if >N events or >T minutes since last regen.
2. **Prompt shape.** Full event history as JSON? A sliding window of last K events? Chunked per week? The shoe project's summary.md (reference) reads as a continuous narrative — implies full-history context, which costs.
3. **Whole-file overwrite vs. append.** DJ-026 says "derived artifact." Overwrite is simpler; append avoids re-running the LLM on unchanged history.
4. **Which tier?** Balanced or fast? Narrative quality matters; the shoe project read well because the LLM had the full arc. Probably balanced.
5. **Do we store narrative sections per target?** E.g., `summary.md` is overall, `history/by-target/<id>.md` is per-node. Not specified in DJ-026; I'd skip for MVP.

### Acceptance tests

- `TestHistorianGenerateNarrativeFromEvents`: seed 5 events via `Historian.Record`, call new `GenerateNarrative` with MockLLM → `summary.md` written with LLM-returned content.
- `TestHistoryCLINarrativeFlag`: `locutus history --narrative` reads `summary.md` if present, prints a clear "no summary yet; run `locutus history --regenerate-narrative`" if not.
- `TestHistoryRegenerateNarrative`: new flag invokes the generator, overwrites `summary.md`.
- `TestNarrativeIncludesRecentEvents`: assert the prompt sent to the LLM contains at least the event Rationale/Target fields (pinning the prompt shape).

### Design sketch

```go
// internal/history/narrative.go
func (h *Historian) GenerateNarrative(ctx context.Context, llm agent.LLM) error
```

New agent prompt at `internal/scaffold/agents/historian_narrative.md` — balanced tier, temperature ~0.6, output is plain markdown. On-demand regeneration (ambiguity 1 → option b). Whole-file overwrite. Write to `.borg/history/summary.md`.

`HistoryCmd` gains a `--regenerate-narrative` flag; the existing `--narrative` flag keeps read-only semantics.

### Non-goals

- Per-target narrative files.
- Automatic regeneration on every event append.
- Incremental / diff narrative.

### Files

`internal/history/narrative.go` (new), `internal/history/narrative_test.go` (new), `cmd/history.go`, `cmd/history_test.go`, `internal/scaffold/agents/historian_narrative.md` (new).

### Effort

~0.75 session.

---

## Round 3: `refine` for Non-Decision Kinds

**Governing:** DJ-069 (**shipped** — DAG node redesign), DJ-072 (**shipped** — verb-set consolidation). `refine` wired for Decision via cascade; Feature / Strategy / Approach / Goal / Bug return "not yet implemented" at [cmd/refine.go:73](cmd/refine.go#L73).

### Ambiguities to resolve

1. **What does `refine <feature-id>` actually do?** Plausible behaviors:
   - a) Re-derive Feature.Description from its current child Decisions (reverse cascade — treat children as source of truth, regenerate parent prose).
   - b) Invoke council to propose changes to the Feature; save; cascade to children.
   - c) Refine each child Decision in turn (transitive refine).

   My read of DJ-069 is (a): the Feature's prose IS the denormalization of its children, so when you "refine" it, you're asking the rewriter to resync it with current child content. Option (b) is closer to "revise the Feature's intent," which is a different verb-level concept.

2. **Strategy refine vs. Feature refine.** Strategies don't have a `Description` field the same way; their prose lives in the `.md` body. Same mechanism (reverse cascade) applies but the target field differs.

3. **Approach refine.** This IS re-synthesis from parent + applicable Decisions. Different prompt than rewriter — it's the synthesis prompt the planner uses when generating a fresh Approach. Worth a dedicated `synthesizer` agent or reuse of the planner's relevant step.

4. **Goal refine.** GOALS.md is a file, not a spec node. Is refining a Goal re-generating GOALS.md from the features beneath it? Probably out of scope — Goals are human-authored.

5. **Bug refine.** Same admission shape as Feature per `import`. Probably same refine shape. Confirm.

### Acceptance tests

- `TestRefineFeatureRegeneratesDescription`: fixture Feature with stale Description → refine → MockLLM returns updated Description → verify file on disk updated + state entries of child Approaches flipped to drifted.
- `TestRefineStrategyUpdatesBody`: similar against a Strategy's markdown body.
- `TestRefineApproachResynthesizesBody`: fixture Approach with child relationship to a revised parent Feature → refine → Body regenerated from parent + applicable Decisions.
- `TestRefineGoalReturnsExplicitNotSupported`: confirm explicit error with helpful message.
- `TestRefineBugSameAsFeature`: symmetry check.

### Design sketch

Route by kind in `RunRefine`:

```go
switch kind {
case spec.KindDecision:
    // existing cascade-based path
case spec.KindFeature, spec.KindBug:
    return refineFeatureLike(ctx, llm, fsys, graph, id)
case spec.KindStrategy:
    return refineStrategy(ctx, llm, fsys, graph, id)
case spec.KindApproach:
    return refineApproach(ctx, llm, fsys, graph, id)
case spec.KindGoal:
    return nil, fmt.Errorf("goals are human-authored; edit GOALS.md directly")
}
```

Feature/Strategy refines reuse the rewriter agent from cascade. Approach refine needs a new synthesizer agent prompt (or a reused planner step — decide in ambiguity 3).

### Non-goals

- Council-driven intent revision (that's a different verb; possibly a future `revise` or `--council-mode` flag).
- Goal refine.

### Files

`cmd/refine.go`, `cmd/refine_test.go`, potentially `internal/scaffold/agents/synthesizer.md` (new, if ambiguity 3 resolves that way).

### Effort

~1 session.

---

## Round 4: `llm_review` Assertion Kind

**Governing:** DJ-035 (**settled**) — "Assertions can be either deterministic or LLM-based (`llm_review`)." Stubbed at [cmd/adopt_assertions.go:81](cmd/adopt_assertions.go#L81) as pass-with-note.

### Ambiguities to resolve

1. **Reviewer agent or critic/stakeholder?** DJ-035 says "independent reviewer LLM call." Dedicated agent is cleaner — separates "does the code do what the spec says" from planning-time critique.
2. **Prompt inputs.** Approach body + artifact diffs + the Assertion's `Prompt` field? Or just Prompt + file contents?
3. **Pass threshold.** LLM returns `{passed: bool, confidence: float, reasoning: string}`. Do we gate on confidence > X, or trust `passed` directly? Probably trust `passed` — the reviewer is supposed to be opinionated.
4. **Caching.** Same artifact-hash set and same Assertion should produce the same review. Worth caching? Probably not for MVP — each `adopt` is expected to produce new artifacts.
5. **Tier.** Balanced or strong? Reviewer quality matters more than speed for this use.

### Acceptance tests

- `TestEvaluateLLMReviewPassing`: Assertion with Prompt "verify tests exist" + MockLLM returning `{passed: true}` → `evaluateAssertion` returns `(true, rationale)`.
- `TestEvaluateLLMReviewFailing`: MockLLM returns `{passed: false, reasoning: "no test files"}` → false + readable output.
- `TestEvaluateLLMReviewNoLLMConfigured`: reviewer agent called with nil LLM → assertion fails with "llm review requires provider configured."
- `TestEvaluateLLMReviewPromptIncludesArtifactPaths`: pin the prompt shape so future changes don't break the reviewer.

### Design sketch

New agent prompt `internal/scaffold/agents/reviewer.md`. New helper in `cmd/adopt_assertions.go`:

```go
func evaluateLLMReview(a spec.Assertion, repoDir string, llm agent.LLM, approach spec.Approach) (bool, string)
```

Threaded through `runAssertions` via a new signature that takes `(llm, approach, assertions, repoDir)`. Callers: `cmd/adopt.go` already has the llm + approach at verify time.

### Non-goals

- Multi-reviewer consensus.
- Review caching.
- Prompt injection via the `Assertion.Pattern` field (kept minimal — `Prompt` is the sole LLM input).

### Files

`cmd/adopt_assertions.go`, `cmd/adopt_assertions_test.go` (new), `cmd/adopt.go` (signature thread-through), `internal/scaffold/agents/reviewer.md` (new).

### Effort

~0.5 session.

---

## Round 5: Assimilate → Remediation

**Governing:** DJ-045 (**shipping**) + DJ-046 (**shipping**) — "Brownfield runs a gap analysis and fills the gaps autonomously with `assumed` decisions and strategies."

The `gap_analyst` and `remediator` prompts both exist ([internal/scaffold/agents/](internal/scaffold/agents/)). The pipeline produces a `Gap` list today. Nothing turns gaps into spec nodes.

### Ambiguities to resolve

1. **Which gaps auto-remediate vs. surface for human review?** DJ-045 says "no pause for user input." But some gaps (e.g., "no test framework chosen") might warrant a stop. Probably: auto-remediate everything as `assumed` Decisions / `proposed` Features; the human reviews the spec diff. Matches the "spec is the source of truth; assumed Decisions are honest" posture.
2. **Cross-cutting vs. feature-specific split (DJ-046).** Which gap categories are cross-cutting (→ single `project-remediation` Feature) vs. attached to an existing Feature? Possibly: "missing CI" and "no lint" are cross-cutting; "auth has no tests" is feature-specific.
3. **Does remediation run automatically in `assimilate`, or only when `assimilate --remediate` is passed?** Automatic is simpler; flag-gated is safer. DJ says autonomous; probably automatic.
4. **How does the remediator know about existing Features?** It needs to read the just-assimilated spec to decide cross-cutting vs. feature-specific. Either the pipeline does both in sequence, or remediator is a post-pass.

### Acceptance tests

- `TestRemediateCreatesAssumedDecisions`: fixture with `Gap{Category: "missing_test_framework"}` → remediator invoked → new Decision written with `status: assumed`, rationale captures the gap.
- `TestRemediateCrossCuttingGapsAttachToProjectRemediationFeature`: fixture with two cross-cutting gaps + one feature-specific → verify `project-remediation` Feature created containing refs to the two, and the third attaches to the right existing Feature.
- `TestAssimilateWithRemediationFlag`: end-to-end — full fixture → `assimilate --remediate` → spec + remediation landed.
- `TestAssimilateWithoutRemediationLeavesGapsInReport`: default assimilate doesn't touch spec beyond the inferred shape.

### Design sketch

New package `internal/remediate`:

```go
func Remediate(ctx context.Context, llm agent.LLM, fsys specio.FS, gaps []agent.Gap, existingSpec *spec.SpecGraph) (*RemediationResult, error)
```

Called from `RunAssimilate` after persistence (Round 1), guarded by a `Remediate bool` flag on `AssimilateCmd`.

### Non-goals

- Remediation for non-brownfield paths (Round 1 is prereq).
- Human-in-the-loop remediation.

### Files

`internal/remediate/remediate.go` (new), `internal/remediate/remediate_test.go` (new), `cmd/assimilate.go` (flag + wire), `cmd/assimilate_test.go`.

### Effort

~1 session. Hard-gated on Round 1.

---

## Round 6: File-Overlap Check at Plan Time (DJ-030)

**Governing:** DJ-030 (**settled**) — "The critic flags file overlaps between parallel workstreams during planning. The planner restructures to eliminate them."

`PlanStep.ExpectedFiles` is declared but never checked. `Approach.ArtifactPaths` is the richer signal and probably what we actually want.

### Ambiguities to resolve

1. **ExpectedFiles vs. ArtifactPaths as the source.** `Approach.ArtifactPaths` is filled in; `PlanStep.ExpectedFiles` is aspirational. Use `ArtifactPaths` for the check? Or require the planner to populate `ExpectedFiles`?
2. **Check in the critic (LLM) or in Go code?** DJ-030 says critic flags overlaps; implies LLM. But overlap detection is mechanical — a Go check is cheaper and 100% accurate. Probably: Go code detects overlap; planner is shown the list and asked to restructure; critic verifies the restructuring.
3. **What action on conflict?** Options: (a) planner restructures the plan (merge workstreams or add dependency edges); (b) abort plan with error; (c) warn and continue. DJ says (a).
4. **Hard error vs. warning.** If the planner can't eliminate the overlap after N retries, what happens? Probably error out — DJ-030 promises prevention.

### Acceptance tests

- `TestDetectOverlapInPlan`: MasterPlan with two parallel workstreams both covering `auth.go` → detector returns overlap list.
- `TestDetectOverlapIgnoresSequentialWorkstreams`: workstream B depends on A → shared file is fine (sequential).
- `TestPlannerRetriesOnOverlap`: MockLLM planner produces a plan with overlap → detector flags → planner receives feedback → second call returns restructured plan → proceed.
- `TestPlannerErrorOnPersistentOverlap`: after N retries still overlapping → error surfaces.

### Design sketch

New `internal/plan_overlap` or inside `internal/spec/`:

```go
func DetectFileOverlap(plan *spec.MasterPlan, approachesByID map[string]spec.Approach) []OverlapReport
```

Called in `runPlannerForCandidates` in `cmd/adopt.go` between plan generation and persistence. If overlaps detected, feed back to planner prompt with the conflict list; retry up to 3 times; error on persistent conflict.

### Non-goals

- Runtime rebase fallback (DJ-030 mentions it — deferred; plan-time prevention is the priority).
- Cross-plan overlap (two concurrent `adopt` invocations). Not in scope.

### Files

`internal/spec/overlap.go` (new) or `internal/planner/overlap.go`, `cmd/adopt.go`, tests.

### Effort

~0.5 session.

---

## Round 7: True `--resume` (DJ-074)

**Governing:** DJ-074 (**settled**) — full proposal already in the journal.

### Ambiguities to resolve

1. **Order of plumbing work.** Three pieces: (A) session-ID surfacing from supervisor → WorkstreamResult; (B) skip-to-step mode in dispatcher's `runWorkstream`; (C) driver `--resume` flag. DJ-074 suggests A → C → B. Resolve or accept.
2. **Worktree base for resume.** Fresh worktree from `main` would lose earlier steps' work. Derive from `locutus/<ws-id>` feature branch (which earlier merged steps produced). Any edge cases if that branch doesn't exist?
3. **Drivers without `--resume`.** Codex is deferred. Gemini is deferred. Claude Code has `--resume`. For now, fail over to invalidate on drivers that don't support it? Or error?
4. **Flag name.** `adopt --discard-in-flight` or `adopt --no-resume` for the opt-out? DJ-074 suggests the former.

### Acceptance tests

- `TestWorkstreamResultCarriesSessionID`: mock supervisor produces a WorkstreamResult with AgentSessionID populated.
- `TestAdoptPersistsSessionIDOnActiveWorkstream`: dispatch succeeds → reload `ActiveWorkstream` → AgentSessionID matches.
- `TestDispatcherSkipsCompletedSteps`: workstream with steps 1/2/3 where 1+2 are complete → `runWorkstream` with `resumeFrom: step-3` runs only step-3.
- `TestAdoptResumesNonDriftedPlan`: seed a stale plan subdirectory; no drift detected → `adopt` picks it up, resumes the right workstreams, finishes → plan archived.
- `TestAdoptDiscardInFlightForceFlag`: seed a stale plan + `--discard-in-flight` → behaves like today (invalidate + replan).

### Design sketch

Per DJ-074:

1. `dispatch.WorkstreamResult.AgentSessionID` added.
2. `Supervisor.Supervise` surfaces `attemptResult.sessionID` into `StepOutcome` (new field) or directly into the WorkstreamResult.
3. `Dispatcher.runWorkstream` signature grows a `resumeFrom *ResumePoint` parameter carrying `{StepID, SessionID}`. If set, worktree is created from `locutus/<ws-id>` instead of main; the step loop skips to the named step ID.
4. `StreamingDriver.BuildCommand` request struct gets `SessionID string`; `ClaudeCodeDriver` translates to `--resume <id>`.
5. `cmd/adopt.go:resumeOrInvalidateActivePlans` grows three branches (per DJ-074 "Resume protocol").
6. `AdoptCmd --discard-in-flight` flag forces invalidation.
7. Update DJ-074 Status to `shipped`.

### Non-goals

- Codex / Gemini driver `--resume` (still deferred).
- Session-ID recovery across reboots when the agent session cache is wiped.

### Files

`internal/dispatch/supervisor.go`, `internal/dispatch/streaming.go`, `internal/dispatch/dispatcher.go`, `internal/dispatch/drivers/*.go`, `cmd/adopt.go`, `cmd/adopt_integration_test.go`, plus tests.

### Effort

~1.5 sessions (biggest remaining round).

---

## Round 8: Team-Facing Enforcement (DJ-032 + DJ-038 + DJ-040)

These three are grouped because they share a common frame: they only matter when Locutus is producing PRs for a human review cycle. For a solo workflow they're overhead.

**Governing:**
- DJ-032 (**shipping**) — PR-per-workstream, auto-merge locally, human pushes. Code does local-merge only; no PR creation, no review step.
- DJ-038 (**settled**) — on-demand specialist agents for plan fleshing-out.
- DJ-040 (**settled**) — test-first workstream pattern as a structural gate.

### Decision to make before the round starts

**Is Locutus going team-facing?** If yes, invest in these. If Locutus stays a solo tool, freeze them as permanent-`settled` with a note in each DJ explaining why and skip this round.

### Ambiguities to resolve (if we go)

1. **PR creation target.** Local branches (today) + `gh pr create` when remote is set? Or GitLab/Gitea support too? Scope to GitHub first.
2. **Review step mechanics.** Pre-merge LLM reviewer running against the PR diff? Or an assertion-enforcement pass (spec alignment, no stubs, assertions pass)? DJ-032 names the review criteria.
3. **Specialist agent first.** Test-architect is the obvious MVP. UI-designer and schema-designer depend on project context.
4. **Test-first enforcement.** Structural check = "first PlanStep of every workstream must have an assertion describing a failing test." Easy static check. But does every workstream really need this? Data-only or doc-only Approaches don't.

### Acceptance tests

Deferred until the ambiguity 1 gate decides whether to pursue.

### Non-goals

Until a team-facing decision is made, this round is deferred in full.

### Files

Depends on decision.

### Effort

~2 sessions if pursued. Zero if Locutus stays solo-facing.

---

## Total cost estimate

| Round | Effort | Depends on |
|---|---|---|
| 1. Assimilate persistence | 0.5 | — |
| 2. Historian narrative | 0.75 | — |
| 3. `refine` non-Decision | 1.0 | — |
| 4. `llm_review` assertion | 0.5 | — |
| 5. Assimilate remediation | 1.0 | Round 1 |
| 6. File-overlap check | 0.5 | — |
| 7. True `--resume` | 1.5 | — |
| 8. Team-facing enforcement | 0–2.0 | user decision |

**Rounds 1–6** closes the "settled and shipping" list except `--resume` and team-facing work. ~4.25 sessions.
**Round 7** adds ~1.5.
**Round 8** is gated on your solo-vs-team call.

Net: ~6 focused sessions to retire every entry on the designed-but-unimplemented list except optional team-facing enforcement.

---

## Discipline check (from the protocol memory)

Each round's opening move in chat: **"Per DJ-NN (status: `settled|shipping|shipped`): [constraint]. Therefore: [implication]. Ambiguities to resolve: [list]."** No code before the ambiguity list is resolved. No scope creep without updating this plan. Status field on the governing DJ gets updated on round completion.

If any round discovers a new gap (as Phase C did with DJ-073 and DJ-074), add a round to this plan rather than absorbing it silently.
