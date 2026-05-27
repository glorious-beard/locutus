# DJ-140 — Headless convergence unification

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task.
>
> **Governing DJ:** [DJ-140](../../docs/DECISION_JOURNAL.md#dj-140). The DJ is the authoritative design record; this plan tracks **progress against** it.
>
> **Status:** READY — design locked in DJ-140 on 2026-05-27. Amends [DJ-136](../../docs/DECISION_JOURNAL.md#dj-136) (its asymmetric-convergence resolved-questions are superseded). Unblocks DJ-139's `refine goals` bootstrap, which currently fails with "/goal isn't available in this environment" on the headless ACP path.
>
> **Surface area:** small-medium. `run.go` branch removal (~10 lines), `ResolvePlaybook` mode axis (~25 lines + signature change), one caller update (`cmd/activity_verb.go`), publisher mode-aware resolution (~30 lines), one playbook file rename, one one-shot-playbook verdict-line verification, docs. No new node types, no new MCP tools.
>
> **Discipline (per memory):**
> - Tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching ([[feedback-test-first]]).
> - Cite DJ-140 + the precise constraint in chat before touching the dispatch path, `ResolvePlaybook`, or the playbook files ([[feedback-cite-djs-before-spec-work]]).
> - Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) before touching any playbook file (the rename + the justification verdict-line check) ([[feedback-agent-conventions-checklist-first]]).
> - **docs/council.md update IS required** — DJ-136 added a `/goal`-driven-convergence visualization; DJ-140 revises it to show the harness outer loop driving all three runtimes. Lands in Phase 5 ([[feedback-council-doc-maintenance]]).
> - No back-compat shims ([[feedback-no-back-compat-until-self-hosting]]) — `ResolvePlaybook`'s signature change ripples to its one caller + tests; update them directly. The user runs `locutus update --offline --reset` before every operation, so the renamed playbook + republished commands take effect cleanly.
> - Locutus voice neutrality in any prose touched ([[feedback-locutus-voice-neutrality]]).

## Why this plan exists

`locutus refine goals` (and every converging verb) is broken on Claude Code via ACP: the dispatched `/goal …` overlay returns "/goal isn't available in this environment" because `/goal` is an interactive-only built-in that the headless `claude-agent-acp` run mode doesn't expose (verified: Claude Code v2.1.150, above the v2.1.139 floor — run-mode gating, not version). DJ-140 reverses DJ-136's asymmetric-convergence design per DJ-136's own pre-registered risk (b): unify all headless dispatch on the harness `OuterLoopRunner`, relocate the `/goal` wrapper to a published interactive slash command, and add a `mode` axis to playbook resolution so content selection is `<activity>[.<provider>][.<mode>].md`.

## Reference state (before DJ-140 starts)

- **Dispatch branch** at [internal/runner/run.go:122](../../internal/runner/run.go#L122) — `if runtime == "claude-code" { return runOneIteration(...) }; return runOuterLoopDispatch(...)`. The claude-code short-circuit relies on `/goal` to loop. `runOuterLoopDispatch` already exists and works for Codex/Gemini; `runOneIteration` is also called per-iteration inside it.
- **`ResolvePlaybook`** at [internal/scaffold/plans_overlay.go:27](../../internal/scaffold/plans_overlay.go#L27) — `ResolvePlaybook(base fs.FS, dir, activityName, runtime string)`. Two-tier resolution: `<activity>.<runtime>.md` then `<activity>.md`. Sole caller: [cmd/activity_verb.go:77](../../cmd/activity_verb.go#L77).
- **The `/goal` overlay** at [internal/scaffold/plans/spec_refinement.claude-code.md](../../internal/scaffold/plans/spec_refinement.claude-code.md) — the `/goal Drive… by repeatedly invoking /locutus-refine` wrapper. Currently resolved by `ResolvePlaybook` for runtime=claude-code and sent as the ACP prompt (the broken path).
- **Publisher** at [internal/publisher/claudecode.go:157](../../internal/publisher/claudecode.go#L157) — `PublishActivity` writes `.claude/commands/locutus-<verb>.md` from `act.PlanBody` (currently the canonical one-iteration body). Codex/Gemini publishers write their command forms similarly. The `CanonicalActivity.PlanBody` field is the body source; check how it's populated to make it mode-aware.
- **Validation hooks** at `internal/scaffold/hooks/{codex,gemini}/spec_refinement.{toml,json}` — per-tool validation (axis-id present, cascade detection), NOT convergence-loop hooks. Out of scope for DJ-140; leave untouched.
- **One-shot playbook** at [internal/scaffold/plans/justification.md](../../internal/scaffold/plans/justification.md) — read-only `justify` activity. Must be checked for a `converged: true` verdict line.

## Phase 1 — Remove the runtime dispatch branch (unify on outer loop)

**Goal:** every headless dispatch runs through `runOuterLoopDispatch` regardless of runtime. `runOneIteration` stays (outer loop calls it per-iteration); only the top-level claude-code short-circuit is deleted.

**Files expected to change:**

- [internal/runner/run.go](../../internal/runner/run.go) — in `DispatchActivity`, delete the `if runtime == "claude-code" { return runOneIteration(...) }` branch; always `return runOuterLoopDispatch(ctx, projectRoot, runtime, spawn, playbookBody, maxIterations, out, progress)`. Update the `DispatchActivity` doc comment (the "Per-runtime dispatch strategy (DJ-136 phase 5)" block) to describe the unified outer loop + the DJ-140 reasoning (cite DJ-140; note `/goal` is interactive-only).
- [internal/runner/run_test.go](../../internal/runner/run_test.go) or a new `run_dispatch_test.go` — assert that dispatch uses the outer loop for claude-code. Since `DispatchActivity` spawns a real ACP subprocess, test at the seam: extract a tiny helper (or assert via the existing `OuterLoopRunner` tests) that the claude-code path no longer single-shots. Simplest: a unit test on a refactored `selectDispatchStrategy(runtime)` returning an enum, OR assert the doc-level invariant by confirming the branch is gone via a behavioral test with a stub spawn. If a clean seam doesn't exist, add a `loopDispatch bool` decision function `dispatchUsesOuterLoop(runtime string) bool` that always returns true and unit-test that (documents intent, trivially testable, removes the magic branch).

**Discipline:** [[feedback-test-first]]. Write the failing test asserting claude-code uses the outer loop first.

**Tests added:**

- `TestDispatchUsesOuterLoopForAllRuntimes` — table over {claude-code, codex, gemini}, asserts the outer-loop strategy is selected (via the extracted decision function).

**Commit message shape:** `refactor(runner): unify headless dispatch on outer loop for all runtimes (DJ-140 phase 1)`.

**Exit criteria:** `go test ./internal/runner/...` green; `go vet ./...` green. claude-code headless dispatch no longer single-shots. (It still sends the `/goal` overlay until Phase 3 — so end-to-end refine goals isn't fixed yet, but the loop driver is correct.)

## Phase 2 — Add the `mode` axis to `ResolvePlaybook`

**Goal:** `ResolvePlaybook` gains a `mode` parameter and 4-tier specificity-descending resolution. `ModeHeadless` / `ModeInteractive` constants exist.

**Files expected to change:**

- [internal/scaffold/plans_overlay.go](../../internal/scaffold/plans_overlay.go) — add `ModeHeadless = "headless"` and `ModeInteractive = "interactive"` constants. Change the signature to `ResolvePlaybook(base fs.FS, dir, activityName, runtime, mode string)`. Implement the resolution walk:
    1. `<activity>.<runtime>.<mode>.md`
    2. `<activity>.<runtime>.md`
    3. `<activity>.<mode>.md`
    4. `<activity>.md`
    Skip tiers whose components are empty (empty runtime skips 1-2; `mode=headless` is the default — decide whether `headless` ever has its own files; for now headless has no dedicated tier-3 files so tier-3 only matters for `interactive`). Update the doc comment with the precedence rule (provider outranks mode at equal specificity) and the publish-vs-dispatch mode determination.
- [cmd/activity_verb.go](../../cmd/activity_verb.go) — update the sole caller to pass `scaffold.ModeHeadless`.
- [internal/scaffold/plans_overlay_test.go](../../internal/scaffold/plans_overlay_test.go) (create if absent) — table tests over the 4-tier matrix using a MemFS / fstest.MapFS seeded with various file combinations.

**Discipline:** [[feedback-test-first]] + [[feedback-no-back-compat-until-self-hosting]] (signature change updates the one caller + tests directly, no shim).

**Tests added:**

- `TestResolvePlaybook_MostSpecificWins` — all four files present → tier 1 chosen.
- `TestResolvePlaybook_ProviderBeatsModeAtEqualSpecificity` — `<activity>.<runtime>.md` + `<activity>.<mode>.md` present, no tier-1 → tier 2 (provider) chosen.
- `TestResolvePlaybook_FallsBackToDefault` — only `<activity>.md` present → tier 4.
- `TestResolvePlaybook_InteractiveOverlayResolvesOnlyForInteractiveMode` — `<activity>.<runtime>.interactive.md` present; mode=headless → NOT chosen (falls to tier 4); mode=interactive → chosen (tier 1).
- `TestResolvePlaybook_EmptyRuntimeSkipsProviderTiers`.

**Commit message shape:** `feat(scaffold): mode axis on ResolvePlaybook — <activity>[.<provider>][.<mode>].md (DJ-140 phase 2)`.

**Exit criteria:** `go test ./internal/scaffold/... ./cmd/...` green; `go vet ./...` green.

## Phase 3 — Relocate the `/goal` wrapper to the interactive variant

**Goal:** the `/goal` overlay is renamed so it resolves only for `mode=interactive`; headless Claude Code dispatch falls through to the one-iteration default. This is the change that actually fixes `refine goals`.

**Files expected to change:**

- Rename [internal/scaffold/plans/spec_refinement.claude-code.md](../../internal/scaffold/plans/spec_refinement.claude-code.md) → `internal/scaffold/plans/spec_refinement.claude-code.interactive.md`. Content unchanged (it still drives `/goal` referencing `/locutus-refine`). Use `git mv` to preserve history.
- Verify the embed directive in [internal/scaffold/scaffold.go](../../internal/scaffold/scaffold.go) globs `plans/*.md` (so the renamed file is still embedded) — if it lists files explicitly, update the list.
- [internal/scaffold/plans_overlay_test.go] or a DJ-140 test — assert that for `activity=spec_refinement, runtime=claude-code, mode=headless`, `ResolvePlaybook` against the embedded FS returns `spec_refinement.md` (NOT the interactive variant); for `mode=interactive` it returns `spec_refinement.claude-code.interactive.md`.
- Update any DJ-136 test that asserts the old overlay path. Search: `grep -rn "spec_refinement.claude-code.md" --include=*.go`.

**Discipline:** [[feedback-agent-conventions-checklist-first]] — the renamed file is a playbook; confirm its prose still reads correctly as an interactive command (it references `/goal` and `/locutus-refine`, both valid interactively).

**Tests added:**

- `TestSpecRefinementHeadlessResolvesToDefaultNotGoalWrapper`
- `TestSpecRefinementInteractiveResolvesToGoalWrapper`

**Commit message shape:** `fix(plans): relocate /goal wrapper to interactive variant — headless falls back to one-iteration default (DJ-140 phase 3)`.

**Exit criteria:** `go test ./internal/scaffold/...` green. With Phases 1-3, `locutus refine goals` on Claude Code now ACP-dispatches the one-iteration `spec_refinement.md` under the harness outer loop — the `/goal` failure is gone. (Empirical re-run in Phase 6.)

## Phase 4 — Publisher emits the interactive variant per runtime

**Goal:** the publisher resolves each runtime's slash-command body with `mode=interactive`. Claude Code's `/locutus-refine` becomes the `/goal` wrapper; Codex/Gemini fall through to the one-iteration default.

**Files expected to change:**

- [internal/publisher/](../../internal/publisher/) — locate where `CanonicalActivity.PlanBody` is populated (the canonical-activity assembly, likely `canonical.go`). Make the per-runtime publish resolve the body via `scaffold.ResolvePlaybook(embeddedPlansFS, "plans", activity, runtime, scaffold.ModeInteractive)` rather than using a single shared `PlanBody`. Each runtime publisher (`claudecode.go`, `codex.go`, `gemini.go`) gets the mode-interactive-resolved body for its runtime. Claude Code → `/goal` wrapper; Codex/Gemini → default (no `.interactive.md` for them).
- [internal/publisher/publisher_test.go](../../internal/publisher/publisher_test.go) + [internal/publisher/dj136_test.go](../../internal/publisher/dj136_test.go) — update assertions: `.claude/commands/locutus-refine.md` now contains the `/goal` directive (was the one-iteration body); `.codex/commands/locutus-refine.toml` and the Gemini command still contain the one-iteration body. Add a DJ-140 test asserting Claude Code's published command contains `/goal` and Codex/Gemini's do not.

**Discipline:** [[feedback-cite-djs-before-spec-work]]. The DJ-136 publisher tests encode the old contract; updating them is expected (the contract changed). Make the change visible in test names.

**Tests added:**

- `TestPublisher_ClaudeCodeCommandIsGoalWrapper`
- `TestPublisher_CodexAndGeminiCommandsAreOneIterationBody`

**Commit message shape:** `feat(publisher): publish /goal wrapper as Claude Code interactive command; plain body for Codex/Gemini (DJ-140 phase 4)`.

**Exit criteria:** `go test ./internal/publisher/...` green. `locutus init` / `update --reset` emits the `/goal` wrapper as `/locutus-refine` for Claude Code, one-iteration body for the others.

## Phase 5 — One-shot verdict-line verification + docs + DJ status

**Goal:** ensure one-shot activities self-terminate under the universal outer loop; update all docs; flip DJ-140 status.

**Files expected to change:**

- [internal/scaffold/plans/justification.md](../../internal/scaffold/plans/justification.md) — verify it ends with a `converged: true` verdict line (the universal outer loop now wraps `justify` on Claude Code too; without the verdict it would loop to the cap). If absent, add a closing instruction: the justify playbook is one-shot, so its final line is always `converged: true`. Add/confirm a test in `internal/scaffold/plans/` asserting `justification.md` contains the verdict line.
- Audit other activity playbooks for the verdict line: `grep -L "converged:" internal/scaffold/plans/*.md` — any converging or one-shot activity dispatched headlessly needs it. Add the line to any that lack it (with justification per the activity's loop semantics).
- [CLAUDE.md](../../CLAUDE.md) — update the "Asymmetric convergence (DJ-136)" bullet to note DJ-140's unification: headless convergence is the harness outer loop for all three runtimes; `/goal` is an interactive-only affordance published as the `/locutus-refine` command body for Claude Code. Update the per-runtime publishing bullet if it describes the overlay as an ACP prompt.
- [docs/runtime-affordances.md](../../docs/runtime-affordances.md) — revise the asymmetric-convergence section; document the `<activity>[.<provider>][.<mode>].md` resolution + publish-vs-dispatch mode determination.
- [docs/council.md](../../docs/council.md) — **required update** ([[feedback-council-doc-maintenance]]): revise the convergence visualization so all three runtimes show the harness outer loop (remove the `/goal`-evaluator-drives-Claude-Code branch from the Mermaid diagram; note `/goal` as an interactive-only path).
- [docs/debugging-traces.md](../../docs/debugging-traces.md) — note Claude Code headless dispatch now shows outer-loop iterations like Codex/Gemini, not a `/goal` evaluator.
- [docs/decisions/dj-136-per-runtime-idiomatic.md](../../docs/decisions/dj-136-per-runtime-idiomatic.md) — amend Status with a forward-pointer: risk (b) fired 2026-05-27; the asymmetric-convergence resolved-questions are superseded by [DJ-140](dj-140-headless-convergence-unification.md).
- [docs/decisions/dj-140-headless-convergence-unification.md](../../docs/decisions/dj-140-headless-convergence-unification.md) — flip Status from `design` to `shipping` with a per-phase summary.
- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — flip DJ-140 row status `design` → `shipping`.
- This plan's Status → `DONE`.

**Empirical validation:** re-run `locutus refine goals` on winplan. Confirm: the dispatch no longer emits "/goal isn't available"; the outer loop runs the one-iteration `spec_refinement.md`; the DJ-139 goal-layer bootstrap fires (goal-*/agoal-* nodes get proposed from `dec-product-scope-boundary`); convergence or the iteration cap terminates cleanly. Document findings inline in this phase.

**Discipline:** the council.md update is mandatory here — confirm it in the commit message so the reviewer sees the checklist was honored (unlike DJ-138/139 which were council-exempt, DJ-140 touches the convergence visualization).

**Tests added:**

- `TestJustificationPlaybookEmitsConvergedVerdict` (and any sibling one-shot playbooks).

**Commit message shape:** `docs(dj-140): unify convergence docs (CLAUDE.md, runtime-affordances, council, traces) + one-shot verdict lines + empirical validation (DJ-140 phase 5)`.

**Exit criteria:** `go test ./... && go vet ./...` green. `locutus refine goals` on winplan completes without the `/goal` error. DJ-140 + DJ-136 statuses updated; council.md reflects the unified loop.

## After all phases

- `go build ./... && go test ./... && go vet ./... && go test ./... -race` — all green.
- DJ-140 status `shipping`; this plan `DONE`.

## Out of scope (explicitly)

Tee'd up as Future Work in DJ-140:
- MCP-tracked loop state (counter + verdict in get/advance tools).
- Stop hook as an optional interactive accelerator.
- Re-fetching the Codex/Gemini hook-capability matrix.
- The Codex/Gemini validation hooks (`internal/scaffold/hooks/`) are untouched — they're per-tool validation, not convergence.

## Risks / known unknowns

- **(a) Embed glob vs explicit file list.** If `scaffold.go`'s embed directive lists plan files explicitly rather than globbing `plans/*.md`, the rename in Phase 3 silently drops the file from the binary. Mitigation: Phase 3 verifies the embed directive first.
- **(b) One-shot playbooks lacking a verdict line loop to the cap.** The universal outer loop means any headlessly-dispatched playbook without `converged:` runs `max_iterations` times. Mitigation: Phase 5 audits all playbooks (`grep -L "converged:"`). This is the highest-value correctness check in the plan.
- **(c) `CanonicalActivity.PlanBody` may be consumed in multiple places.** Phase 4 changes how the published body is resolved; if `PlanBody` feeds other consumers (not just publishing), they may need the headless body, not the interactive one. Mitigation: grep all `PlanBody` readers before changing its population; prefer resolving at publish time per-runtime rather than mutating the shared field.
- **(d) Publisher tests encode the DJ-136 contract.** Updating them is expected, but ensure the updates reflect intent (Claude Code command = `/goal` wrapper) rather than rubber-stamping whatever the new output is.

## Reference

Synthesizes the 2026-05-27 debugging + design conversation that began when `locutus refine goals` failed on winplan with "/goal isn't available in this environment" (sessions `20260527/1804/240000` and `…/430000`). Traced to `/goal` being an interactive-only built-in unreachable in headless ACP dispatch (Claude Code v2.1.150, run-mode gating). The conversation worked through MCP-tracked loop state, PostToolUse/Stop hooks, and the turn-end kernel (only a Stop hook or external re-prompt can force continuation past turn-end), then situated the problem against established agent-loop best practice (harness owns control flow; runtime affordances are volatile accelerators) and landed on unifying headless convergence on the harness outer loop. Full design in [DJ-140](../../docs/decisions/dj-140-headless-convergence-unification.md); amends [DJ-136](../../docs/decisions/dj-136-per-runtime-idiomatic.md).
