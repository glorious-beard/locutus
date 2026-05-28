# DJ-142 — Idiomatic convergence drivers (interactive self-loop for Codex/Gemini)

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task.
>
> **Governing DJ:** [DJ-142](../../docs/DECISION_JOURNAL.md#dj-142). The DJ is the authoritative design record; this plan tracks **progress against** it.
>
> **Status:** READY — design locked in DJ-142 on 2026-05-27, loop-identity spike resolved (`(ServerSession, activity, target)` keying). Builds on [DJ-140](../../docs/DECISION_JOURNAL.md#dj-140) (does not supersede it); depends on [DJ-138](../../docs/DECISION_JOURNAL.md#dj-138) `max_iterations` in the activity registry and [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135) daemon singleton.
>
> **Surface area:** medium. New: a daemon-side loop-state store (~120 lines + tests), three loop MCP tools (~120 lines + tests), the tier-3 `spec_refinement.interactive.md` playbook (~one-iteration body duplicated + loop scaffolding) with a drift-guard test, publisher/resolution verification, docs incl. council.md. No changes to headless dispatch or the interactive-Claude `/goal` path.
>
> **Discipline (per memory):**
> - Tests → design pause → code; mid-impl failures trigger design judgment, not test patching ([[feedback-test-first]]).
> - Cite DJ-142 + the precise constraint in chat before touching the loop tools, the playbook, or the resolution path ([[feedback-cite-djs-before-spec-work]]).
> - Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) BEFORE authoring `spec_refinement.interactive.md` ([[feedback-agent-conventions-checklist-first]]) — it's a coding-agent prompt.
> - **docs/council.md update IS required** — DJ-140 collapsed the convergence diagram to one harness loop; DJ-142 adds the agent-self-loop driver for interactive non-Claude. Lands in Phase 5 ([[feedback-council-doc-maintenance]]).
> - No back-compat shims ([[feedback-no-back-compat-until-self-hosting]]); user runs `locutus update --offline --reset` before operations.
> - Loop-state fields all have consumers (the self-loop playbook reads them); no aspirational fields ([[feedback-no-aspirational-fields]]).
> - Locutus voice neutrality in the playbook prose ([[feedback-locutus-voice-neutrality]]).

## Why this plan exists

[DJ-140](../../docs/DECISION_JOURNAL.md#dj-140) unified headless convergence on the harness outer loop and added the `mode` axis to `ResolvePlaybook`, but left tier-3 (`spec_refinement.interactive.md`) empty — so interactive `/locutus-refine` on Codex/Gemini fell through to the one-shot tier-4 default. DJ-142 fills tier-3 with a self-looping playbook the coding agent drives itself, backed by deterministic daemon-side loop-state MCP tools (iteration counter + cap), so all three contexts reach the same outcome (converge-or-cap) via the idiomatic driver for each: harness loop (headless), `/goal` (interactive Claude Code), agent self-loop (interactive Codex/Gemini). Headless and interactive-Claude are unchanged.

## Reference state (before DJ-142 starts)

- **`ResolvePlaybook`** at [internal/scaffold/plans_overlay.go](../../internal/scaffold/plans_overlay.go) — 4-tier `<activity>[.<runtime>][.<mode>].md` (DJ-140). `mode=interactive` activates the mode tiers; Codex/Gemini interactive currently resolve to tier-4 `spec_refinement.md` because tier-3 is empty.
- **MCP server** built by `NewSpecServer(store *agent.SpecStore, fsys specio.FS, reg *activity.Registry, hist *history.Historian)` at [internal/mcp/server.go:57](../../internal/mcp/server.go#L57) — **already receives the activity registry `reg`**, so loop tools can read `reg.Lookup(activity).MaxIterations` (DJ-138) with no new wiring.
- **Tool handler shape** — `func(ctx, req *mcp.CallToolRequest, in <Input>) (*mcp.CallToolResult, any, error)`. Handlers today discard `req` (`_ *mcp.CallToolRequest`, e.g. [tools_spec_write.go:294](../../internal/mcp/tools_spec_write.go#L294)). `req.Session` is `*mcp.ServerSession` (go-sdk `shared.go:478`); distinct per socket connection (`server.Connect` per accept, [socket.go:137](../../internal/mcp/socket.go#L137)). `ServerSession.ID()` returns `""` on the socket transport, so use the pointer-derived token, not `.ID()`.
- **Publisher** resolves `mode=interactive` per runtime (DJ-140) via `scaffold.ResolvePlaybook`; once tier-3 exists, Codex/Gemini interactive commands resolve to it automatically.
- **`spec_refinement.md`** at [internal/scaffold/plans/spec_refinement.md](../../internal/scaffold/plans/spec_refinement.md) — the one-iteration body (the per-iteration work the tier-3 playbook duplicates) ending in the `converged:` verdict line.
- **Embed** — `//go:embed plans/*.md` ([scaffold.go:37](../../internal/scaffold/scaffold.go#L37)) globs new playbook files automatically; `update --reset` copies them to `.borg/plans/`.

## Phase 1 — Daemon-side loop-state store

**Goal:** a pure, unit-tested in-memory store keyed by `(sessionToken, activity, target)` with begin / status / advance / GC. No MCP coupling yet — the tool layer (Phase 2) supplies `sessionToken`.

**Files expected to change:**

- New: `internal/mcp/loopstate.go`:
    ```go
    type loopRecord struct {
        iteration   int       // completed iterations; fresh = 0
        maxIter     int
        converged   bool
        lastVerdict string
        runID       string    // server-internal, logging/correlation only
        updatedAt   time.Time // for TTL GC
    }
    type loopKey struct{ sessionToken, activity, target string }
    type loopStore struct {
        mu      sync.Mutex
        records map[loopKey]*loopRecord
        seq     uint64 // for runID minting
    }
    func newLoopStore() *loopStore
    // Begin allocates a fresh record (iteration 0) when none is live for key,
    // or returns the existing live record (recovery). maxIter from the caller
    // (registry lookup in Phase 2). Returns a copy of the record.
    func (s *loopStore) Begin(key loopKey, maxIter int) loopRecord
    func (s *loopStore) Status(key loopKey) (loopRecord, bool)
    // Advance records verdict, increments iteration, returns (record, cont).
    // cont == false when converged OR iteration >= maxIter. A terminated
    // record (cont==false) is dropped so a later Begin for the same key
    // starts fresh.
    func (s *loopStore) Advance(key loopKey, converged bool, reason string) (rec loopRecord, cont bool)
    // GC drops records older than ttl (defensive cleanup for abandoned loops).
    func (s *loopStore) GC(ttl time.Duration)
    ```
    Semantics: `iteration` counts completed iterations (fresh Begin → 0). `Advance` increments first, then evaluates `cont`: `cont = !converged && iteration < maxIter`. When `cont == false`, delete the record (terminal). `Begin` on an existing live record ignores the passed `maxIter` and returns the live record (recovery preserves the in-flight count).
- New: `internal/mcp/loopstate_test.go` — table/unit tests, no MCP transport.

**Discipline:** [[feedback-test-first]]. Write the store tests first.

**Tests added:**

- `TestLoopStore_BeginFreshStartsAtZero` — Begin on empty store → iteration 0, maxIter as passed, runID non-empty.
- `TestLoopStore_AdvanceIncrementsAndContinues` — after Begin, Advance(converged=false) → iteration 1, cont=true (maxIter>1).
- `TestLoopStore_AdvanceStopsOnConverged` — Advance(converged=true) → cont=false; record dropped (subsequent Status → not found).
- `TestLoopStore_AdvanceStopsAtCap` — maxIter=2; two Advances → second returns cont=false; record dropped.
- `TestLoopStore_BeginRecoversLiveRecord` — Begin, Advance to iteration 1, Begin again (same key) → returns iteration 1 (not reset).
- `TestLoopStore_FreshAfterTerminal` — converge a loop (record dropped), Begin same key again → iteration 0 (fresh run).
- `TestLoopStore_SessionTokenIsolation` — same (activity,target), different sessionToken → independent records; advancing one doesn't touch the other.
- `TestLoopStore_GCDropsStale` — record older than ttl is removed by GC; fresh ones survive.

**Commit message shape:** `feat(mcp): daemon-side loop-state store keyed by (session, activity, target) (DJ-142 phase 1)`. Trailer `Co-Authored-By: Claude <noreply@anthropic.com>`.

**Exit criteria:** `go test ./internal/mcp/ -run TestLoopStore` green; `go vet ./...` green. Store is pure and unused by any tool yet.

## Phase 2 — The three loop MCP tools

**Goal:** `spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration` registered, reading `req.Session` for the server-side scope and `reg` for `max_iterations`. End-to-end via the in-memory MCP client.

**Files expected to change:**

- New: `internal/mcp/tools_loop.go`:
    - A session-token registry mapping `*mcp.ServerSession` → stable string token: a `sync.Map` (key: `*mcp.ServerSession`, value: `string`) assigning `"sess-" + atomic-incremented-uint64` on first lookup. Decouples the store (string-keyed) from the SDK type and avoids pointer-address-reuse hazards. Helper `sessionToken(sess *mcp.ServerSession) string`.
    - Input structs: `loopBeginInput{Activity, Target string}`, `loopStatusInput{Activity, Target string}`, `advanceIterationInput{Activity, Target string; Converged bool; Reason string}`. `jsonschema` descriptions per the registration-not-prompts convention (DJ-134): explain that the agent passes `(activity, target)` from its run context, that iteration/cap are server-tracked, and that the scout's verdict is what `converged` reports.
    - `registerLoopTools(server *mcp.Server, ls *loopStore, reg *activity.Registry)`:
        - `spec_loop_begin`: token = `sessionToken(req.Session)`; `maxIter = reg.Lookup(in.Activity).MaxIterations` (fall back to `activity.DefaultMaxIterations` if the activity is unknown — defensive); `rec = ls.Begin(loopKey{token, in.Activity, in.Target}, maxIter)`; return `{iteration, max_iterations}` as structured content.
        - `spec_loop_status`: `rec, ok = ls.Status(key)`; return `{iteration, max_iterations, converged, last_verdict}` or a not-started zero state (`iteration:0, converged:false`) when `!ok`.
        - `spec_advance_iteration`: `rec, cont = ls.Advance(key, in.Converged, in.Reason)`; return `{continue: cont, iteration: rec.iteration, reason: in.Reason}`.
    - Descriptions: three new `desc…` consts.
- Modify `internal/mcp/server.go` `NewSpecServer` — construct `ls := newLoopStore()` and call `registerLoopTools(server, ls, reg)` alongside the existing `registerWriteTools` / read tools. (Optionally start a GC goroutine with a long TTL, e.g. 1h, cancelled on server shutdown — keep simple; a GC goroutine is acceptable but if shutdown plumbing is awkward, skip the timer and rely on terminal-record deletion + leave a TODO; terminal deletion already bounds growth for normal flows.)
- New: `internal/mcp/tools_loop_test.go` — drive via the in-memory MCP client (`newTestServer` pattern in `server_test.go`).

**Discipline:** [[feedback-cite-djs-before-spec-work]] (commit cites DJ-142 + the `(ServerSession, activity, target)` keying constraint). [[feedback-agent-conventions-checklist-first]] for the tool `Description` text.

**Tests added:**

- `TestSpecServer_RegistersLoopTools` — `ListTools` includes the three names.
- `TestSpecLoopBegin_ReturnsCapFromRegistry` — begin for `spec_refinement` → `max_iterations` matches the registry (assert 20 default, and an override via a seeded `.borg/agents.yaml` if the test server supports it; otherwise assert the default).
- `TestSpecLoopLifecycle_BeginAdvanceConverge` — begin → advance(false) → status shows iteration 1, continue path → advance(true) → `continue:false`.
- `TestSpecLoopAdvance_StopsAtCap` — with a low cap (seed an activity at max_iterations=2 or assert against default by advancing to the cap), advancing past the cap returns `continue:false`.
- `TestSpecLoop_SessionIsolation` — two distinct client sessions against one server (two `client.Connect` calls) running the same `(activity, target)` get independent counters. (This is the load-bearing test for the `ServerSession` scoping; confirm the in-memory transport yields distinct `req.Session` per client session.)
- `TestSpecLoopStatus_NotStartedIsZero` — status before begin → `iteration:0, converged:false`.

**Commit message shape:** `feat(mcp): spec_loop_begin/status/advance tools for the interactive self-loop driver (DJ-142 phase 2)`.

**Exit criteria:** `go test ./internal/mcp/...` green; `go vet ./...` green. Tools callable; `SessionIsolation` proves the scoping. No playbook uses them yet.

## Phase 3 — Tier-3 `spec_refinement.interactive.md` playbook + drift guard

**Goal:** the self-looping interactive playbook for non-Claude runtimes, with a drift-guard test pinning its per-iteration core to `spec_refinement.md`.

**Files expected to change:**

- Modify [internal/scaffold/plans/spec_refinement.md](../../internal/scaffold/plans/spec_refinement.md) — wrap the per-iteration body (scout → decision-elaborator fanout → revisions → cite) in sentinel comments so the drift guard can extract it:
    ```
    <!-- BEGIN per-iteration-core -->
    … existing per-iteration steps …
    <!-- END per-iteration-core -->
    ```
    No prose change inside the sentinels — just bracket the existing core. (If the headless playbook's structure makes a single contiguous core awkward, bracket the largest contiguous shared region and note what's excluded; the drift guard compares whatever is inside the sentinels.)
- New: `internal/scaffold/plans/spec_refinement.interactive.md` — the tier-3 self-loop variant. Structure:
    - Opening: the orchestrator drives its own convergence loop in this single session (interactive runtimes without a native goal-loop). Voice-neutral.
    - **Loop protocol** (the new scaffolding):
        1. Call `mcp__locutus__spec_loop_begin` with `{activity: "spec_refinement", target: <the Target: from run context>}`. Read `iteration` and `max_iterations`.
        2. Run one iteration of the per-iteration core (below).
        3. Call `mcp__locutus__spec_advance_iteration` with `{activity, target, converged: <scout verdict>, reason: <one-line>}`. If it returns `continue: false`, stop. Otherwise repeat from step 2.
        4. If you lose track mid-run (context compression), re-call `spec_loop_begin` with the same `{activity, target}` — it returns the current iteration so you resume rather than restart.
    - The per-iteration core: the SAME steps as `spec_refinement.md`, copied verbatim between matching `<!-- BEGIN per-iteration-core -->` / `<!-- END per-iteration-core -->` sentinels.
    - Note: this playbook does NOT emit a trailing `converged:` verdict line for a harness to read — convergence is reported through `spec_advance_iteration`. (It's never headless-dispatched: tier-3 only resolves under `mode=interactive`.)
- New: `internal/scaffold/plans/spec_refinement_interactive_drift_test.go` (package matching sibling tests in `internal/scaffold/plans/`) — extract the bytes between the sentinels in both files (read via `os.ReadFile` like sibling tests) and assert they are byte-identical. Fail with a message pointing at both files when they diverge.

**Discipline:** **[[feedback-agent-conventions-checklist-first]] binding** — walk the six anti-patterns + four positive patterns before writing the loop scaffolding. Positive phrasing ("re-call `spec_loop_begin` to resume" not "don't forget the run id"); realistic example ids in any payloads; tool-behavior text stays in the tool `Description` (Phase 2), the playbook says *when/why* to call them.

**Tests added:**

- `TestSpecRefinementInteractive_CoreMatchesCanonical` — the drift guard (sentinel extraction, byte-equality).
- `TestSpecRefinementInteractive_ReferencesLoopTools` — asserts the playbook references `spec_loop_begin` and `spec_advance_iteration`.
- `TestSpecRefinementInteractive_HasCompressionRecoveryNote` — asserts the re-call-begin-to-resume instruction is present (substring).
- Extend the DJ-140 resolution test (or add one): `mode=interactive, runtime=codex` against the embedded FS resolves to `spec_refinement.interactive.md` (tier 3), while `mode=headless` still resolves to `spec_refinement.md` and `mode=interactive, runtime=claude-code` resolves to the `/goal` wrapper.

**Commit message shape:** `feat(plans): tier-3 spec_refinement.interactive.md self-loop playbook + drift guard (DJ-142 phase 3)`.

**Exit criteria:** `go test ./internal/scaffold/...` green. Codex/Gemini interactive now resolve to the self-loop playbook; the drift guard holds.

## Phase 4 — Publisher / resolution verification

**Goal:** confirm (and lock with tests) that the publisher emits the self-loop body as the Codex/Gemini interactive command, while Claude Code keeps the `/goal` wrapper and headless is untouched.

**Files expected to change:**

- Likely none in `internal/publisher/` — DJ-140 already resolves `mode=interactive` per runtime, so tier-3 is picked up automatically. If a publisher test or helper hardcodes the expected Codex/Gemini body, update it.
- New/extend `internal/publisher/dj142_test.go`:
    - `TestPublisher_CodexInteractiveCommandIsSelfLoop` — after publishing against a project FS seeded with the embedded plans (incl. `spec_refinement.interactive.md`), `.codex/commands/locutus-refine.toml` body references `spec_loop_begin` (the self-loop), NOT just the one-shot body.
    - `TestPublisher_GeminiInteractiveCommandIsSelfLoop` — same for the Gemini command.
    - `TestPublisher_ClaudeCodeInteractiveCommandStillGoalWrapper` — regression: Claude Code's command still contains `/goal` (tier-1 wins, unchanged from DJ-140).
    Follow the test harness from `dj140_test.go` for how the project FS is seeded with the embedded scaffold (it must include the tier-3 file — if the harness copies the embedded plans, automatic; otherwise seed it explicitly).

**Discipline:** the DJ-140 publisher tests encode the prior contract; if any breaks because Codex/Gemini interactive bodies changed, update it to the new intent (self-loop), making the change visible in the test name/comment.

**Tests added:** see above (3).

**Commit message shape:** `test(publisher): lock Codex/Gemini interactive commands to the self-loop body (DJ-142 phase 4)`.

**Exit criteria:** `go test ./internal/publisher/...` green. Published interactive commands match the three-driver matrix.

## Phase 5 — Docs, council.md, status flips

**Goal:** docs reflect the three-driver matrix; DJ-142 + plan flipped; council.md updated (required).

**Files expected to change:**

- [CLAUDE.md](../../CLAUDE.md) — extend the DJ-140 "Unified headless convergence" bullet (or add a DJ-142 bullet) to the three-driver matrix: headless → harness loop (all runtimes); interactive Claude Code → `/goal`; interactive Codex/Gemini → agent self-loop via `spec_loop_*` tools, keyed `(ServerSession, activity, target)`. One outcome (converge/cap), idiomatic driver per context.
- [docs/runtime-affordances.md](../../docs/runtime-affordances.md) — document the tier-3 interactive self-loop + the loop-state tools + the `(activity, target)` agent-facing key with server-side session scoping.
- [docs/mcp.md](../../docs/mcp.md) — document `spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration`: inputs `(activity, target[, converged, reason])`, outputs, the server-side `(ServerSession, activity, target)` scoping, and that iteration/cap are server-tracked while the scout owns the convergence verdict.
- [docs/council.md](../../docs/council.md) — **required** ([[feedback-council-doc-maintenance]]): revise the convergence visualization to show three drivers reaching one outcome (harness loop headless; `/goal` interactive-Claude; agent self-loop interactive-Codex/Gemini). Keep the Mermaid valid.
- [docs/decisions/dj-142-idiomatic-convergence-drivers.md](../../docs/decisions/dj-142-idiomatic-convergence-drivers.md) — flip `Status:` `design` → `shipping` with a per-phase summary.
- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — flip the DJ-142 row `design` → `shipping`.
- This plan's `Status:` → `DONE`.

**Empirical validation (operator-confirmed, like DJ-139/140):** end-to-end interactive convergence on Codex/Gemini requires a real interactive session on those runtimes; note in the DJ status that it's the operator's to confirm. The in-tree tests cover the store lifecycle, tool surface, session isolation, resolution, drift guard, and publisher output.

**Discipline:** confirm in the Phase 5 commit message that council.md was updated (DJ-142 touches convergence, unlike DJ-138/139 which were council-exempt).

**Tests added:** none (docs); `go test ./internal/docs/...` must stay green (manifest bijection).

**Commit message shape:** `docs(dj-142): three-driver convergence matrix (CLAUDE.md, runtime-affordances, mcp, council) + status flip (DJ-142 phase 5)`.

**Exit criteria:** `go test ./... && go vet ./...` green. DJ-142 row + file both `shipping`; council.md shows three drivers.

## After all phases

- `go build ./... && go test ./... && go vet ./... && go test ./internal/mcp/... -race` — all green (the `-race` run matters: the loop store + session-token registry are concurrent-access under the daemon).
- DJ-142 status `shipping`; this plan `DONE`.

## Out of scope (DJ-142 Future Work)

- Unifying the headless verdict-line read onto the loop-state tools (all three drivers share one inspectable record). Deferred.
- Extending the interactive self-loop to `feature_ingestion` / `code_adoption` / `code_assimilation` / `spec_bias` via their own tier-3 `<activity>.interactive.md` once `spec_refinement` validates the pattern.
- An interactive turn-end backstop hook (Gemini `AfterAgent` / Codex equivalent) reading `spec_loop_status` to re-drive.

## Risks / known unknowns

- **(a) In-memory transport `req.Session` distinctness.** Phase 2's `SessionIsolation` test assumes two `client.Connect` calls against one server yield distinct `*ServerSession`. Verify early; if the in-memory transport shares a session, test the store's isolation directly (Phase 1 already does) and assert the token-registry assigns distinct tokens for distinct sessions via a smaller seam.
- **(b) Sentinel placement in `spec_refinement.md`.** The per-iteration core must be a contiguous region for clean sentinel extraction. If the headless playbook interleaves loop-discipline prose with per-iteration steps, bracket the largest contiguous shared region and duplicate only that; document what's outside the sentinels in the interactive variant. The drift guard compares only the bracketed bytes.
- **(c) Loop-state GC lifetime.** Terminal-record deletion bounds growth for normal converge/cap flows; abandoned loops (agent never advances to terminal) leak until a GC sweep or daemon restart. A long-TTL GC goroutine handles it; if shutdown plumbing is awkward, terminal deletion + a documented TODO is acceptable for v1 (the daemon is per-project and restarts clear it).
- **(d) Concurrent same-(activity,target) across sessions** is handled by the `ServerSession` scope; a bridge restart mid-run starts the loop fresh (accepted in the DJ).

## Reference

Synthesizes the 2026-05-27 conversation: DJ-140 left tier-3 empty; the framing correction (consistent *outcome*, idiomatic *driver*); the loop-identity spike (server-resolves-from-session not viable as an agent string, but server-side `ServerSession` scoping is) and the daemon/bridge clarification that landed the `(ServerSession, activity, target)` key. Full design in [DJ-142](../../docs/decisions/dj-142-idiomatic-convergence-drivers.md).
