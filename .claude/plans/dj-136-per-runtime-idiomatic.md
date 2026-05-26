# DJ-136 — Per-Runtime Idiomatic Dispatch Layer

> **Governing DJ:** [DJ-136](../../docs/DECISION_JOURNAL.md#dj-136). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** designed; implementation not started.
> **Predecessors:** [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135) (multi-runtime pivot; this DJ reverses its resolved-question 14 and extends the publisher + runner layers); [DJ-119](../../docs/DECISION_JOURNAL.md#dj-119) (ACP wire layer); [DJ-134](../../docs/DECISION_JOURNAL.md#dj-134) (SpecStore, which Locutus's outer loop reads between iterations on Codex / Gemini).
> **Surface area:** new playbook-overlay loader + tests; new hook publisher subpackages per runtime; per-runtime dispatch strategy in `internal/runner/`; `spec_refinement.md` one-iteration refactor; new `spec_refinement.claude-code.md` overlay; new hook configs; docs updates.
> **Discipline (per memory):**
>
> - Tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching ([[feedback-test-first]]).
> - Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) as a checklist before editing playbook content under `internal/scaffold/plans/` ([[feedback-agent-conventions-checklist-first]]).
> - Phase 7 must include the [docs/council.md](../../docs/council.md) update ([[feedback-council-doc-maintenance]] + [[reference-council-doc]]); DJ-132 plan Phase 4 is the template ([[reference-dj-132-plan]]).

## Why this plan exists

DJ-135 shipped the multi-runtime pivot with the playbook as a single cross-runtime contract. Resolved-question 14 explicitly deferred per-runtime overrides because v1 stability mattered more than per-provider fine-tuning. That stake held through phases 1-7 of DJ-135 and the empirical winplan validation on 2026-05-25.

The 2026-05-26 winplan refine session at `~/projects/winplan/.locutus/sessions/20260526/1918/080000/` surfaced the cost of the LCD floor concretely: convergence loop discipline lives entirely in playbook prose ("loop until scout reports converged"), enforcement of subagent output schemas relies on prose-bound discipline rather than mechanical hooks, and Claude Code's `/goal` slash command (a session-scoped model-evaluated stop condition that auto-restarts turns until a condition is met) is sitting unused exactly where the prose-driven loop is most brittle.

DJ-136 reverses resolved-question 14 and adopts per-runtime idiomatic dispatch:

- `.<runtime>.md` overlay convention for playbooks (default `<activity>.md` required; overlays optional and total-override).
- Hook configs as a separate publisher-emitted layer with no cross-runtime canonical form.
- Asymmetric convergence: `/goal`-driven on Claude Code; Locutus-driven outer loop on Codex / Gemini.
- Playbook becomes one-iteration-shaped; the outer loop is the runtime's (or Locutus's) job.

The DJ captures the rationale; this plan breaks the work into shippable phases.

## Reference state (before DJ-136 starts)

- **Activity registry** at [internal/activity/](../../internal/activity/) — `agents.yaml` maps activities → prioritized runtime list (`claude-code` / `codex` / `gemini`). `Resolve()` returns the first detected runtime.
- **AgentSpawns** at [internal/dispatch/acp/registry.go:21-25](../../internal/dispatch/acp/registry.go#L21-L25) — canonical runtime IDs are `claude-code`, `codex`, `gemini`. The `<runtime>` slot in `<activity>.<runtime>.md` matches these IDs verbatim.
- **Publisher** at [internal/publisher/](../../internal/publisher/) — emits per-runtime agent + slash-command files at scaffold + reset time. Already has per-runtime subdirs; extends to also emit hook configs.
- **Activity playbook loader** at [cmd/activity_verb.go:57-67](../../cmd/activity_verb.go#L57-L67) — currently reads `.borg/plans/<activity>.md` flat. Extends to try `<activity>.<runtime>.md` first.
- **Runner** at [internal/runner/run.go](../../internal/runner/run.go) — `DispatchActivity` is the entry point for ACP-driven activity execution. Per-runtime dispatch strategy slots in here (build `/goal` prompt for Claude Code; drive outer loop for Codex / Gemini).
- **Default playbook** at [internal/scaffold/plans/spec_refinement.md](../../internal/scaffold/plans/spec_refinement.md) — currently multi-iteration-shaped with outer-loop discipline embedded. Refactor to one-iteration-shaped.
- **DJ-135 phase plan** at [.claude/plans/dj-135-multi-runtime-pivot.md](../../.claude/plans/dj-135-multi-runtime-pivot.md) — for reference of the phase structure DJ-136 extends.

## Resolved design questions

Recorded in chat 2026-05-26; settled before this plan went to implementation. The DJ's "Resolved design questions" section captures the full set; this list re-states the implementation-relevant ones.

1. **Runtime IDs match AgentSpawns** (`claude-code`, `codex`, `gemini` — hyphenated). No alias / short-name registry.
2. **Default `<activity>.md` is required**; overlay is opt-in. Test enforces orphan-overlay prevention.
3. **Total override v1**, not partial-include. Templating arrives only if real duplication pain emerges.
4. **Hooks have no cross-runtime canonical form** — publisher reads `internal/scaffold/hooks/<runtime>/` and emits per runtime.
5. **Asymmetric convergence** is the deliberate design. `/goal` on Claude Code; Locutus's runner on Codex / Gemini.
6. **Agent overlay surface deferred** — no `.<runtime>.md` for `internal/scaffold/agents/` in this DJ.
7. **Drift management** is human discipline + grep-invariant test, not sync-tracking headers. Grep test asserts overlays reference same MCP tools / agents / verbs as defaults.

## Phase 1 — Per-runtime playbook overlay loader + orphan / drift tests

**Goal:** the playbook loader resolves `<activity>.<runtime>.md` before `<activity>.md`. Test suite asserts every overlay has a default and references the same MCP tools / agents / verbs. No content change to existing playbooks yet — just the loader + tests, so the change is invisible to runtime behavior at this phase.

**Files expected to change:**

- [cmd/activity_verb.go](../../cmd/activity_verb.go) — `loadActivityPlaybook` gains a `runtime` parameter; tries `<activity>.<runtime>.md` first, falls back to `<activity>.md`. The runtime is the resolved-from-registry runtime ID.
- New: `internal/scaffold/plans_overlay.go` or similar — small helper that owns the overlay-resolution logic. Used by both the activity verb (resolving `.borg/plans/`) and the publisher (resolving embedded `internal/scaffold/plans/`).
- New: `internal/scaffold/plans/overlays_test.go` — orphan-overlay prevention test. Walks embedded `internal/scaffold/plans/`; asserts every `.<runtime>.md` file has a matching `.md` sibling and that `<runtime>` is in the AgentSpawns key set.
- New: `internal/scaffold/plans/drift_invariant_test.go` — grep-invariant test. For each overlay, asserts:
  - Same set of `mcp__locutus__*` tool names mentioned in both files.
  - Same set of agent IDs mentioned (the hyphenated `spec-*` names).
  - Same activity verb name in the heading or playbook intro.
  - Difference allowed in prose, framing, loop discipline, and `/goal`-specific directives.

**Tests:**

- `TestPlaybookOverlay_ResolvesRuntimeSpecificFirst` — given a fsys with both `spec_refinement.md` and `spec_refinement.claude-code.md` present, `loadActivityPlaybook(fsys, "spec_refinement", "claude-code")` returns the overlay content; called with runtime `codex` returns the default.
- `TestPlaybookOverlay_FallsBackToDefault` — overlay absent → returns default.
- `TestPlaybookOverlay_DefaultRequired` — both absent → error mentioning the canonical path.
- `TestPlaybookOverlay_NoOrphanOverlays` — embedded `internal/scaffold/plans/` walk; every `.<runtime>.md` has a sibling `.md`.
- `TestPlaybookOverlay_DriftInvariants` — for each overlay in embedded plans, run the grep-invariant comparison against the default.

**Verification:** `go build ./... && go vet ./... && go test ./cmd/... ./internal/scaffold/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 2 — Plan-update event surfacing in the runner

**Goal:** ACP `session/update` notifications with `"sessionUpdate":"plan"` are surfaced in the runner's progress writer as rendered plan blocks. Today these are dropped on the floor in [internal/dispatch/acp/events.go:83-88](../../internal/dispatch/acp/events.go#L83-L88)'s `default` branch (DJ-135 Phase-1 scope cut); DJ-136 reverses that. After this phase the runner CAN surface plan updates; Phase 3 makes the playbook actually request them.

**Why this is its own phase:** the runner-side wiring is mechanical and independent of any playbook content change. Splitting it out keeps the plan-surface change small, testable on its own (with stubbed plan events from `acpsdk.SessionUpdatePlan`), and lets Phase 3's playbook rewrite land against a runner that already knows how to render. If Phase 3 ships without Phase 2 in place, the plan notifications would arrive and get dropped silently — exactly the failure mode DJ-136 is reversing.

**Files expected to change:**

- New `dispatch.EventPlan` kind in [internal/dispatch/event.go](../../internal/dispatch/event.go) — carries the full entry list (slice of `{Content, Priority, Status}` triples) from the notification. Full replacement per the ACP spec; no diff state managed by the dispatch layer.
- [internal/dispatch/acp/events.go](../../internal/dispatch/acp/events.go) — `translateUpdate` gains a `case u.Plan != nil` branch that emits `EventPlan` with the entries mapped from `acpsdk.PlanEntry` to the dispatch-layer shape. The retired `default` comment line ("plan, availableCommandsUpdate, ...") loses the `plan` reference.
- [internal/runner/run.go](../../internal/runner/run.go) — event-loop switch gains a `case dispatch.EventPlan:` branch. Renders the full plan block inline via the progress writer, guarded by `heartbeat.Lock` (same mutex as tool-call / Subscribe-backfill lines, ensures atomic multi-line block). Render shape:

  ```text
  [12:18:55] plan (3 entries):
       ✓ Read GOALS.md
       ⟳ Dispatch spec-scout for axis survey
       ○ Commit decisions from scout's open axes
  ```

  Status glyphs: `○` pending, `⟳` in_progress, `✓` completed. Priority is captured in the event but not surfaced in the default render — keep it on the `Raw` event for archive consumers / future renderers.
- [internal/runner/render_plan.go](../../internal/runner/render_plan.go) (new) — small helper that takes `[]dispatch.PlanEntry` + the progress writer's clock function and produces the multi-line block. Easier to unit-test than inlining in the event-loop case.

**Tests:**

- `TestEventPlan_TranslateFullReplacement` — given an ACP `SessionNotification` whose `Update.Plan` carries 3 entries with mixed statuses, `translateUpdate` emits one `EventPlan` event with the entries in order, mapped statuses, and a non-empty `Raw`.
- `TestEventPlan_RenderInProgressWriter` — given a stub progress writer and a 3-entry plan, the renderer produces the expected multi-line block with correct glyphs.
- `TestEventPlan_EmptyEntriesRendersGracefully` — an empty plan notification (the agent retracting its plan) renders a single "plan (0 entries) — cleared" line, not a spurious blank block.
- `TestEventPlan_BackfillsWithToolCallProgressMutex` — concurrent emission of `EventPlan` + `EventToolCall` does not interleave lines (the mutex on the progress writer holds across the full block).

**Verification:** `go build ./... && go vet ./... && go test ./internal/runner/... ./internal/dispatch/... -count=1 -race`. Manual verification deferred to Phase 3 once a playbook actually requests plans.

**Estimated:** 3-4 hours.

## Phase 3 — `spec_refinement.md` one-iteration refactor (cross-runtime default) + TodoWrite directive

**Goal:** the cross-runtime default playbook becomes one-iteration-shaped. Strip the outer-loop discipline ("loop until converged"); surface the scout's convergence verdict in plain text so a `/goal` evaluator (on Claude Code) or Locutus's runner (on Codex / Gemini) can read it. Prose discipline shifts from "you, the orchestrator, must drive the loop" to "do one iteration well; the harness will tell you when to stop." The playbook also gains an opening directive instructing the orchestrator to use the runtime's plan tool (`TodoWrite` on Claude Code; equivalent on Codex / Gemini if exposed) to lay out the iteration's plan, with status updates as steps execute — which Phase 2's `EventPlan` rendering then surfaces inline in the operator's view.

**Files expected to change:**

- [internal/scaffold/plans/spec_refinement.md](../../internal/scaffold/plans/spec_refinement.md) — substantive rewrite. The phase structure (scout → decision-elaborator fanout → revise → cascade → commit) is preserved; the outer loop and the "iterate until converged" framing retire. Two new directives:
  1. **Opening:** "Before doing any work in this iteration, call `TodoWrite` (or your runtime's equivalent plan tool, if available) with the entries you intend to execute. Update entries to `in_progress` / `completed` as you proceed. The harness renders your plan entries inline, so the operator can see what you've scheduled and how far through it you are."
  2. **Closing:** "Surface the scout's convergence verdict (`converged: true` or `converged: false` with reasons) in plain text at the end of this iteration." This is the signal the goal evaluator (or Locutus's runner) reads.
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — note the one-iteration-shape convention for activity playbooks; the harness owns the outer loop; playbooks should request plan tools where available.

**Tests:**

- `TestPlaybookSpecRefinement_HasConvergenceVerdictDirective` — grep the playbook for the convergence-verdict surfacing directive; required.
- `TestPlaybookSpecRefinement_HasPlanToolDirective` — grep the playbook for the `TodoWrite` / plan-tool directive; required.
- `TestPlaybookSpecRefinement_NoOuterLoopFraming` — grep for retired phrases ("until converged," "loop until," "iteration N+1") and fail if present. Catches accidental regressions.
- The Phase 1 drift-invariant test still passes (no overlays exist yet).

**Empirical validation** (manual): run `locutus refine goals` against winplan; confirm the orchestrator calls `TodoWrite` early in the iteration; confirm the runner renders a plan block per Phase 2's design; confirm status updates re-render as work progresses.

**Verification:** Read the rewritten playbook end-to-end; confirm it's coherent as a single iteration's worth of work. Run the full test suite. Manual: `locutus refine` on winplan.

**Estimated:** 3-4 hours (the playbook rewrite is editorial; the tests are mechanical; the empirical validation depends on a working Phase 2).

## Phase 4 — Claude Code path: `/goal` dispatch + `spec_refinement.claude-code.md` overlay + slash command publish

**Goal:** when the dispatch runtime is `claude-code`, the prompt sent over ACP is a `/goal <condition>` directive whose condition references the published `/locutus-refine` slash command and names the convergence termination predicate. Iteration is driven by Claude Code's evaluator. The overlay carries the runtime-specific instructions; the cross-runtime default stays one-iteration-shaped.

**Files expected to change:**

- New: [internal/scaffold/plans/spec_refinement.claude-code.md](../../internal/scaffold/plans/spec_refinement.claude-code.md) — the Claude Code overlay. Content: a `/goal` directive whose body is the termination condition (scout reports `converged: true` with empty `axes_open`, OR 20 iterations consumed). The condition body MUST stay under 4KB per [Claude Code /goal docs](https://code.claude.com/docs/en/goal). References the `/locutus-refine` slash command as the per-iteration work.
- [internal/runner/run.go](../../internal/runner/run.go) or new `internal/runner/dispatch.go` — per-runtime dispatch strategy. For `claude-code`, the playbook body sent to the ACP session is the `/goal` directive (the overlay's content); for `codex` / `gemini`, the body is the one-iteration default + Locutus drives the outer loop (Phases 5-6).
- [internal/publisher/claudecode/](../../internal/publisher/claudecode/) — emit `.claude/commands/locutus-refine.md` whose body is the one-iteration default playbook. (Published slash commands continue to come from the cross-runtime default; the overlay only carries the `/goal` directive that invokes the slash command.)
- [cmd/refine.go](../../cmd/refine.go) (or wherever the refine verb lives) — no change needed beyond Phase 1's loader update.

**Tests:**

- `TestClaudeCodeOverlay_HasGoalDirective` — overlay contains the `/goal` line and a termination predicate that mentions `converged` and an iteration ceiling.
- `TestClaudeCodeOverlay_FitsIn4KB` — overlay body is ≤ 4000 bytes per the Claude Code `/goal` constraint.
- `TestClaudeCodeOverlay_DriftInvariant` — Phase 1's drift-invariant test passes against the overlay (same MCP tools / agent names / verb as default).
- `TestDispatchStrategy_ClaudeCodeUsesOverlay` — given runtime `claude-code` and both files present, the dispatcher uses the overlay body; given runtime `codex`, uses the default.
- `TestPublisherEmitsRefineSlashCommand` — after a scaffold pass, `.claude/commands/locutus-refine.md` exists with the one-iteration default body.

**Empirical validation** (not automated): run `locutus refine` on the winplan project with the Claude Code path; confirm `◎ /goal active` indicator appears in the session; confirm convergence is decided by `/goal`'s evaluator (the trace shows evaluator's "no, because ..." reasons between iterations). Compare convergence reliability vs. the prior winplan session at `1918/080000`.

**Verification:** `go build ./... && go vet ./... && go test ./... -count=1 -race`. Manual: `locutus refine` on winplan.

**Estimated:** 6-8 hours (overlay authoring + dispatcher strategy + slash command emit + empirical validation).

## Phase 5 — Codex path: Locutus outer loop + Codex hook configs

**Goal:** when the dispatch runtime is `codex`, Locutus's runner drives the outer loop: dispatch one iteration of the one-iteration default playbook, read SpecStore between iterations to check convergence, re-dispatch if not converged, bounded by iteration ceiling. PreToolUse + PostToolUse hook configs land at `.codex/hooks.json` (or inline `[hooks]` in `.codex/config.toml`) to enforce per-tool invariants mechanically.

**Files expected to change:**

- New: `internal/runner/loop.go` — the Locutus-driven outer loop. Accepts a runtime, max iterations, and a convergence-check function. Reads SpecStore between iterations.
- [internal/runner/run.go](../../internal/runner/run.go) — extend the per-runtime dispatch strategy: for `codex`, call into `loop.go`'s driver.
- New: `internal/scaffold/hooks/codex/spec_refinement.toml` — hook config. PreToolUse on `mcp__locutus__spec_propose_decision` (validate axis id present); PostToolUse on feature / strategy writes (trigger cascade-revision detection).
- New: [internal/publisher/codex/](../../internal/publisher/codex/) — gain a hooks emit step. Reads `internal/scaffold/hooks/codex/`; writes to `.codex/hooks.json` or inline `[hooks]` in `.codex/config.toml` (whichever is conventional per [Codex hooks docs](https://developers.openai.com/codex/hooks)). Idempotent insertion; preserves user-authored hooks adjacent to Locutus's.
- New: `cmd/hook_validate_decision.go` (or similar) — small Locutus subcommand the hook config invokes. Reads the proposed decision input, checks invariants, exits non-zero with structured reason on violation. Same binary, internal subcommand.

**Tests:**

- `TestCodexHookConfig_ValidPath` — published `.codex/hooks.json` (or `.codex/config.toml`) parses and references the `locutus hook-validate-decision` subcommand.
- `TestHookValidateDecision_RejectsMissingAxisID` — the subcommand exits non-zero when input lacks axis id.
- `TestRunnerOuterLoop_CodexDispatchesUntilConverged` — given a stubbed SpecStore whose convergence check returns false twice then true, the outer loop dispatches three iterations and stops.
- `TestRunnerOuterLoop_RespectsIterationCeiling` — given convergence check that never returns true, loop stops at the ceiling.
- `TestCodexPublisher_EmitsHookConfig` — after a scaffold pass, the Codex hook config exists with expected shape.

**Empirical validation**: run `locutus refine` against a Codex-only test project; confirm the outer loop drives multiple iterations; confirm hook denials surface with structured reasons in the operator's view.

**Verification:** `go build ./... && go vet ./... && go test ./... -count=1 -race`. Manual: refine on a Codex test project.

**Estimated:** 10-14 hours (outer loop + hook config + validation subcommand + publisher emit + tests + empirical).

## Phase 6 — Gemini path: Gemini hook configs (reuses Phase 5's outer loop)

**Goal:** Gemini runs the same Locutus-driven outer loop as Codex. Gemini hook configs land at `.gemini/settings.json`'s hooks field (per [Gemini hooks docs](https://geminicli.com/docs/hooks/)) using BeforeTool / AfterTool event names (Gemini's equivalent of PreToolUse / PostToolUse).

**Files expected to change:**

- New: `internal/scaffold/hooks/gemini/spec_refinement.json` — hook config. BeforeTool on `mcp__locutus__spec_propose_decision` (calls `locutus hook-validate-decision`); AfterTool on feature / strategy writes (calls `locutus hook-cascade-trigger`).
- New: [internal/publisher/gemini/](../../internal/publisher/gemini/) — gain a hooks emit step. Reads `internal/scaffold/hooks/gemini/`; writes to `.gemini/settings.json` hooks field. Idempotent; preserves user-authored hooks.
- [internal/runner/run.go](../../internal/runner/run.go) — per-runtime dispatch strategy: for `gemini`, same as Codex (call into `loop.go`'s outer loop driver).

**Tests:**

- `TestGeminiHookConfig_ValidPath` — published `.gemini/settings.json`'s hooks field parses and references the Locutus hook subcommands.
- `TestGeminiPublisher_EmitsHookConfig` — after a scaffold pass, the Gemini hook config exists with expected shape.
- `TestRunnerOuterLoop_GeminiDispatchesUntilConverged` — same as Phase 5's Codex test, parameterized for `gemini`.

**Empirical validation**: run `locutus refine` against a Gemini-only test project; confirm outer loop drives iterations; confirm BeforeTool denials surface.

**Verification:** `go build ./... && go vet ./... && go test ./... -count=1 -race`. Manual: refine on a Gemini test project.

**Estimated:** 4-6 hours (hook config + publisher emit + tests + empirical). Smaller than Phase 5 because the outer loop is already built.

## Phase 7 — Documentation

**Goal:** the docs catch up. New `docs/runtime-affordances.md` captures the per-runtime affordance map. `docs/council.md` updates per [[feedback-council-doc-maintenance]]. `CLAUDE.md` gains a section on per-runtime playbook overlays + hook publishing. `docs/agent-conventions.md` gains the one-iteration-shape convention.

**Files expected to change:**

- New: [docs/runtime-affordances.md](../../docs/runtime-affordances.md) — per-runtime affordance map (hook events per runtime, slash command formats, `/goal` availability), naming conventions (`.<runtime>.md` overlay), publisher output structure (where each runtime's emitted files land). Cross-references `internal/scaffold/hooks/`, `internal/publisher/*/`, and the runtime IDs.
- [docs/council.md](../../docs/council.md) — Mermaid diagram updates. The convergence loop now has two depictions: a `/goal`-driven loop on Claude Code (with the evaluator-after-each-turn visualization) and a Locutus-driven outer loop on Codex / Gemini (with the iteration-budget visualization). Per [[reference-dj-132-plan]], this update is required to land in this phase, not as a follow-up.
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — note the one-iteration-shape convention for activity playbooks; the harness owns the outer loop; overlays carry runtime-specific framing only.
- [CLAUDE.md](../../CLAUDE.md) — top-level section on per-runtime overlays + hook publishing + asymmetric convergence. Add to the Sources-of-Truth list.
- [docs/debugging-traces.md](../../docs/debugging-traces.md) — note that Claude Code traces now include `/goal` evaluator verdicts after each turn; Codex / Gemini traces include per-iteration SpecStore convergence check results.

**Tests:** None new. The doc updates are reviewed manually.

**Verification:** Read each updated doc end-to-end; confirm cross-references resolve; confirm Mermaid renders.

**Estimated:** 4-5 hours.

## Phase 8 — Status flip + plan marked DONE

**Goal:** with all phases shipped, flip DJ-136 to shipping status, mark this plan DONE, and capture the empirical evidence in the DJ.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-136 Status line: `designed; implementation not started` → `shipping (Phases 1-7 landed YYYY-MM-DD; empirical validation against Claude Code via /goal, Codex via outer loop, Gemini via outer loop confirmed in Phase N — see plan)`.
- This file — top-level Status: `designed; implementation not started` → `DONE`.

**Verification:** Final reading pass on the DJ; full test suite green; final empirical refine on winplan against all three runtimes.

**Estimated:** 1 hour.

## Total estimate: 33-45 hours single-stranded across 7-8 sessions

Conservative because empirical validation per phase requires real test projects (winplan is the canonical Claude Code project; Codex / Gemini test projects may need scaffolding from scratch). The number assumes no major design rework mid-implementation; if Phase 4's `/goal` empirical validation reveals reliability gaps with the evaluator, Phase 4 expands.

## Pointers a fresh session should follow before resuming

1. Read the governing DJ end-to-end ([docs/DECISION_JOURNAL.md#dj-136](../../docs/DECISION_JOURNAL.md#dj-136)). The DJ owns the design; this plan owns the phases. If they disagree, the DJ wins and this plan needs updating.
2. Read [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135) resolved-question 14 — this DJ explicitly reverses it. Understanding *why* the deferred-overlays stance was the right call at v1 helps reason about which overlay candidates are worth shipping vs. holding off.
3. Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) before editing any playbook content. The one-iteration-shape refactor in Phase 3 needs to honor positive-phrasing + schema-tag conventions just like prior playbook authoring did.
4. Check [[feedback-runtime-idiomatic-no-lcd]] memory for the durable preference behind this DJ. Useful framing when judging "should this feature land in the overlay or in the default?"
5. Check [[reference-runtime-hook-asymmetry]] memory for the per-runtime hook surface as fetched 2026-05-26. If the runtimes have evolved meaningfully (new hook events, retired ones), refresh the memory first.

## What is explicitly out of scope

- **Per-runtime agent prompt overlays.** Agents stay cross-runtime; the `.<runtime>.md` convention applies to plans only.
- **Other activities' overlays.** Only `spec_refinement` gets a `claude-code.md` overlay in this DJ. `feature_ingestion`, `code_adoption`, `code_assimilation` may get overlays later if a forcing case emerges; not pre-emptive.
- **Partial-include / templating for overlays.** Total override v1. Templating arrives only if duplication pain emerges.
- **Sync-tracking headers for drift management.** Grep-invariant test only at v1.
- **Windows-specific socket / hook config quirks.** This DJ assumes Unix-ish operating systems (matches DJ-135's stance). Windows support is a separate concern.
- **Hooks for activities beyond `spec_refinement`.** Only `spec_refinement` gets hook configs in this DJ. Other activities adopt the hook pattern in follow-on work.
