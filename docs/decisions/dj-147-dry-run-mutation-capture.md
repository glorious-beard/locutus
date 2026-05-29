## DJ-147: `--dry-run` on `import` / `refine` / `adopt` / `assimilate` via Per-Session SpecStore Overlay; Capture at the MCP-Tool Boundary by a `captureOnly` Wrapper (Mirroring DJ-143's `requireRuntime`); Activation Through `LOCUTUS_DRY_RUN` + `LOCUTUS_DRY_RUN_FORMAT` Env Vars Forwarded via `_meta` at MCP `initialize`; New Read-Only `spec_dry_run_report` Tool + CLI-Side `tools.jsonl` Render Give a Dual Rendering Path (Agent-Narrated Markdown + Authoritative Headless Structured Output) — Phase B Revival on Post-DJ-135 Footing

**Status:** settled (designed 2026-05-29; no code yet). Revival of the long-deferred Phase B `--dry-run` work scoped in [`docs/plans/verb-set-phase-b.md`](../plans/verb-set-phase-b.md) when [DJ-072](dj-072-cli-surface-consolidated-8-verb.md)'s 8-verb consolidation shipped Phase A. Phase B never landed; its implementation premise (in-process council writes that a Go flag could suppress) was retired by [DJ-135](dj-135-multi-runtime-pivot.md)'s pivot to ACP-dispatched coding-agent runtimes that mutate the graph through MCP tools. DJ-147 keeps Phase B's principle (every mutating verb gets `--dry-run`, exits 0 with preview, zero side effects on `.borg/` / `.locutus/state/` / working tree) and Phase B's blast-radius primitive ([`internal/spec/graph.go:271` `(*SpecGraph).BlastRadius(id)`](../../internal/spec/graph.go#L271), still intact), but rebases the implementation onto the post-DJ-135 architecture: capture at the MCP-tool boundary by a daemon-side wrapper. Builds on [DJ-134](dj-134-unified-spec-store.md) (in-process `SpecStore` as the source of truth during a daemon session), [DJ-143](dj-143-per-runtime-tool-policy.md) (the `LOCUTUS_MODE` → `_meta` → daemon-side session-context pattern is the precedent for the `LOCUTUS_DRY_RUN` plumbing; the `requireRuntime` registration-site wrapper is the precedent for `captureOnly`), and [DJ-144](dj-144-cc-workflow-convergence.md) (Claude Code's in-runtime dynamic workflow runs end-to-end against the overlay, exercising the full convergence loop in preview).

**Context.** Locutus does not call LLMs in-process ([DJ-135](dj-135-multi-runtime-pivot.md)). The verbs that mutate the spec graph — `import`, `refine`, `adopt`, `assimilate` — dispatch a coding-agent runtime via ACP and pass it an activity playbook; the runtime executes the playbook by calling back into the per-project Locutus MCP daemon via `mcp__locutus__spec_*` tool calls that mutate the in-process `SpecStore` and persist to `.borg/spec/`. The Go-side verb code is a dispatcher; the writes don't live there. So Phase B's original implementation premise — *"the pipeline (`agent.Analyze`) already returns an `AssimilationResult` in memory; this phase just wires a flag that suppresses the final write pass"* — doesn't apply anymore. There is no in-process write pass to suppress.

The right boundary post-DJ-135 is the MCP tool. Every spec mutation flows through one of: `spec_propose_decision` / `spec_revise_decision` / `spec_propose_feature` / `spec_revise_feature` / `spec_propose_strategy` / `spec_revise_strategy` / `spec_propose_approach` / `spec_revise_approach` / `spec_propose_goal` / `spec_revise_goal` / `spec_delete_goal` / `spec_propose_antigoal` / `spec_revise_antigoal` / `spec_delete_antigoal` / `spec_mark_approach_drifted` / `spec_update_goals_md_hash`. Wrapping these at registration with a per-session capture-only adapter intercepts every mutation a workflow can propose, without touching any Go code outside the daemon. The DJ-143 `requireRuntime` wrapper at registration is the exact pattern; capture-only is the structural sibling — same registration-site shape, capture-and-return-success instead of allowlist-and-deny.

Two correctness properties are non-negotiable for a useful dry-run: **(a) the workflow runs faithfully end-to-end**, not just up to the first captured write — otherwise the preview misses cascade revisions, the citation walk, and iteration-2 convergence work; **(b) the operator gets a structured report of what would have landed**, not just the agent's narration of what it did. Property (a) requires a per-session in-memory shadow of the SpecStore so the agent's read-after-write sees its own would-be mutations. Property (b) requires the captured ordered list to be readable both during the run (so the agent can render it in its closing summary) and after the run (so the CLI can render an authoritative version independent of the agent's narration).

A third property the Phase B-era plan didn't have to worry about: **mode parity**. DJ-135 split dispatch into headless (CLI verb → ACP) and interactive (operator inside Claude Code types `/locutus-refine`). The headless path writes a session trace (`.locutus/sessions/<…>/tools.jsonl`); the interactive path doesn't. The dry-run report mechanism has to work in both, which means the primary report surface is the agent's closing message (mode-uniform), with the headless `tools.jsonl` render as a CLI-side authoritative backup.

**Decision.** Add a per-session overlay to the `SpecStore` that captures would-be mutations from sessions marked dry-run at MCP `initialize`; route every spec-mutating MCP tool's handler through a `captureOnly` wrapper at registration; add a single read-only `spec_dry_run_report` MCP tool that returns the session's ordered capture; add a `--dry-run` flag plus a `--format markdown|json` companion to the four mutating CLI verbs; render the report via the agent's closing summary (mode-uniform) and additionally via a CLI-side post-dispatch read of `tools.jsonl` (headless only). No new node kinds, no new verbs, no new file paths, no changes to Codex/Gemini behavior beyond the wrapper being available to their sessions too.

1. **Verb scope: `import`, `refine`, `adopt`, `assimilate`.** Phase B's table minus the Go-side `init` / `update` (deterministic file operations; their dry-run shape is different and not covered here). `assimilate` is in scope despite its current `code_assimilation.claude-code.md` playbook being a stub that no-ops on spec mutations — the Go-side comment in [`cmd/assimilate.go:11-12`](../../cmd/assimilate.go#L11) carries the design intent that the agent calls `spec_propose_*` directly; when the real playbook lands (the deferred DJ-135 phase 5 checkpoint 3 work), dry-run "just works" because the wrapper at registration time is already in place. Today's `assimilate --dry-run` produces an honest empty report.

2. **Activation: `LOCUTUS_DRY_RUN` + `LOCUTUS_DRY_RUN_FORMAT` env vars forwarded via `_meta`.** Mirrors [DJ-143](dj-143-per-runtime-tool-policy.md) §2 exactly. CLI flag `--dry-run` on the four Kong structs (`ImportCmd`, `RefineCmd`, `AdoptCmd`, `AssimilateCmd`) sets `LOCUTUS_DRY_RUN=1` in the env of the spawned coding-agent process; companion `--format markdown|json` (default `markdown`, Kong `enum` constraint) sets `LOCUTUS_DRY_RUN_FORMAT`. The bridge (`cmd/mcp.go`) reads both at startup (fail-safe defaults: false, `""`) via new `resolveLocutusDryRun()` + `resolveLocutusDryRunFormat()` helpers paralleling `resolveLocutusMode`, and forwards as `_meta["locutus.dry_run"]` + `_meta["locutus.dry_run_format"]` on the bridge's outbound `initialize` request alongside the existing `_meta["locutus.mode"]`. Daemon's `newInitializedHandler` in `internal/mcp/session_context.go` reads both fields, normalizes (boolean coercion for dry_run, lowercase + enum-check for format), and stores `(runtime, mode, dryRun, dryRunFormat)` on the session map. Two new accessors: `SessionDryRun(*mcp.ServerSession) bool` (consulted by the `captureOnly` wrapper) and `SessionDryRunFormat(*mcp.ServerSession) string` (consulted only by `spec_dry_run_report` for the agent-facing format hint). Orthogonal to `LOCUTUS_MODE` — dry-run composes with each mode independently (headless+dry-run is CLI preview; interactive+dry-run is operator preview from inside a coding-agent session).

3. **Per-session overlay on the `SpecStore` (the faithfulness mechanism).** A new `internal/agent/spec_store_overlay.go` extends `SpecStore` with an optional per-session shadow:

   ```go
   type sessionOverlay struct {
       mu       sync.RWMutex
       entries  map[storeKey]*StoreEntry   // would-be additions + revisions, keyed (kind, id)
       deleted  map[storeKey]struct{}      // would-be deletions, mask the base store on read
       captured []CapturedMutation         // ordered audit trail surfaced by spec_dry_run_report
   }

   type CapturedMutation struct {
       Tool      string    // "spec_propose_decision", "spec_revise_feature", ...
       Kind      SpecKind  // typed for the renderer
       ID        string
       Body      any       // the typed entry as committed-in-overlay
       Timestamp time.Time // session-local ordering
   }

   type SpecStore struct {
       // existing fields ...
       overlaysMu sync.RWMutex
       overlays   map[*mcp.ServerSession]*sessionOverlay  // empty entries for non-dry-run sessions
   }
   ```

   **Write path under dry-run:** the `captureOnly` wrapper (§4) calls `store.OverlayPut(sess, kind, id, body)`. This validates identically to `Put` (so the agent gets the same id-prefix-mismatch / shape-error feedback on malformed input), constructs the `StoreEntry`, appends to `captured`, and lands the entry in `overlay.entries` (or marks `overlay.deleted` for `spec_delete_*`). **No disk write, no Bluge mutation, no history event, no `spec://manifest` notification.** OTel spans still emit with a `locutus.dry_run=true` attribute so observability dashboards can filter previews in or out.

   **Read path under dry-run:** `spec_list_manifest` / `spec_get` / `spec_search` consult the overlay first via `store.OverlayView(sess)`. For each id requested: return the overlay's version if present; if `overlay.deleted[(kind,id)]`, return missing; otherwise return the base store's entry. `spec_search` queries the base Bluge index then layers in the overlay's `captured` entries via a post-filter on the result set — a soft fidelity gap on full-text searches that happen to match captured-but-not-yet-indexed text, accepted as the design's only known fidelity gap (see *Resolved design questions* #5).

   **Session close:** the overlay is discarded — `delete(store.overlays, sess)` from the daemon's session-close hook. Mid-session daemon restart drops the overlay along with everything else DJ-134's in-memory state owns; same failure mode normal sessions face today.

4. **`captureOnly[In, Out]` wrapper at registration (the boundary).** A generic adapter alongside `requireRuntimeAny` in `internal/mcp/session_context.go`. Same shape, different action:

   ```go
   func captureOnly[In, Out any](
       h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
       capture func(sess *mcp.ServerSession, in In) (Out, error),  // builds the would-be entry + applies to overlay
   ) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
       return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
           if !SessionDryRun(req.Session) {
               return h(ctx, req, in)
           }
           out, err := capture(req.Session, in)
           if err != nil {
               var zero Out
               return errorResult(err.Error()), zero, nil
           }
           return textResult("captured (dry-run)"), out, nil
       }
   }
   ```

   `capture` per tool is small: validate the input, build the typed body via the existing `buildDecisionBody` / `buildFeatureBody` / `buildStrategyBody` / `buildApproachBody` / `buildGoalBody` / `buildAntiGoalBody` helpers, call `OverlayPut`. Composes with `requireRuntimeAny` at registration — outer order is `requireRuntime → captureOnly → handler`, so a denied runtime fails before capture runs (no leaking would-be inputs from non-allowlisted runtimes). Zero overhead for non-dry-run sessions: the `SessionDryRun` lookup is a `sync.RWMutex.RLock` + map lookup, identical-shape to DJ-143's runtime/mode check.

   Tools wrapped: `spec_propose_decision`, `spec_revise_decision`, `spec_propose_feature`, `spec_revise_feature`, `spec_propose_strategy`, `spec_revise_strategy`, `spec_propose_approach`, `spec_revise_approach`, `spec_propose_goal`, `spec_revise_goal`, `spec_delete_goal`, `spec_propose_antigoal`, `spec_revise_antigoal`, `spec_delete_antigoal`, `spec_mark_approach_drifted`, `spec_update_goals_md_hash`. Read-only tools (`spec_list_manifest`, `spec_get`, `spec_search`) are not wrapped — they consult the overlay via the read-path described in §3. `spec_loop_*` (DJ-142) is not wrapped — loop counters are a fidelity requirement, not a spec mutation; the agent needs to know it's at iteration 2/20 even in dry-run.

5. **`spec_dry_run_report` MCP tool (the report surface).** A new read-only tool registered alongside the spec read tools: no input, returns `{captured: []CapturedMutation, format: string}` reading the calling session's `overlay.captured` list and the session's `SessionDryRunFormat`. Available to all three runtimes (no DJ-143 runtime restriction — it's a per-session read with no side effects beyond returning the in-flight overlay state). The agent calls it as the last step of its workflow when dry-run is active, per the contextNote (§6); it surfaces the structured output in its closing message rendered per the requested format.

6. **CLI contextNote tells the agent how to render the report.** `runActivityVerb` appends a dry-run-specific contextNote when `--dry-run` is set:

   > **Dry-run mode is active.** The daemon is capturing your `spec_propose_*` / `spec_revise_*` / `spec_delete_*` / `spec_mark_approach_drifted` / `spec_update_goals_md_hash` calls in a per-session overlay rather than persisting them, and discarding the overlay at session close. You will still see your own captured mutations in subsequent `spec_list_manifest` / `spec_get` / `spec_search` results so the workflow runs end-to-end against the would-be graph. After your final mutation phase (the citation walk for `refine`, or the admission decision for `import` / `adopt`), call `mcp__locutus__spec_dry_run_report` once with no arguments to retrieve the structured capture, then close with the report rendered as **{format-specific instruction}**.

   Format-specific instruction (one of):

   - **markdown** — *"Walk the structured capture and produce a prose closing summary: count the captured mutations by kind (decisions proposed, features revised, drift marks, …), name each by id with its title, and call out cascade impact (which existing nodes' citations the mutations would touch). Close with a one-line outcome (e.g. 'Dry-run: would commit 3 new decisions and revise 2 features against the dashboard subtree')."*
   - **json** — *"Emit the structured capture verbatim inside a single fenced ```json code block. No surrounding prose. The JSON shape is exactly what the tool returned — don't reformat or filter."*

7. **Dual rendering paths: agent-narrated (mode-uniform) + CLI-rendered (headless authoritative).** Two surfaces with orthogonal lifetimes — overlay dies with the session, `tools.jsonl` persists with the run trace — both backed by the same captured set:

   - **Agent's closing message** is the primary report surface in *both* modes. The agent calls `spec_dry_run_report`, gets the structured capture, renders per format, includes it as the closing section of its summary. Operator reads it inline — in headless via the runner's `output.md`, in interactive via the conversation flow.
   - **CLI post-dispatch render** is the headless-only authoritative backup. After dispatch returns, `runActivityVerb` reads the session's `tools.jsonl` (path the runner already knows — written by [internal/runner/run.go:263](../../internal/runner/run.go#L263)), filters to the mutation-family tool calls, and emits per format (markdown summary to stdout for the default; JSON array to stdout for `--format json`). When the agent's narration drifts from what the daemon actually captured (paraphrase, dropped field), the CLI version is authoritative — operators scripting against the result rely on this path. Exit code is always 0 on a successful dry-run regardless of what was captured; a non-zero exit means dispatch failed, not "would-be mutations were rejected."

   In interactive mode the operator gets only the agent's closing message — no `tools.jsonl` exists for that session. Acceptable: an interactive operator who wants machine-readable output can re-run headless.

8. **Subagent dispatches share the parent's overlay.** Per DJ-135's per-project daemon singleton + DJ-142's `(ServerSession, …)` keying, a subagent spawned via the `Task` tool gets its own ACP session against the parent's coding-agent runtime but shares the same MCP session against the Locutus daemon (the same `*mcp.ServerSession`). So a subagent's `spec_propose_*` call routes through the same `captureOnly` wrapper, lands in the same overlay, is visible to the parent's next read. No subagent-specific plumbing — fan-out parallelism per DJ-144 §4 composes naturally with the overlay.

9. **Concurrent sessions are isolated.** Two dry-run sessions attached to the same daemon get distinct overlays keyed by `*mcp.ServerSession`; A's writes are invisible to B and vice versa. A dry-run session sees the live base store including any mutations a *normal* concurrent session commits — i.e. dry-run previews against a moving target by design, accurately reflecting the current world.

10. **Side effects: history events skipped, search index untouched, notifications not fired, OTel spans tagged.** Under dry-run, history events don't fire (the audit trail is for what happened to the graph; dry-run didn't), the Bluge search index doesn't update (search has a soft fidelity gap on full-text hits against captured-but-not-indexed text, accepted), `notifications/resources/updated` on `spec://manifest` isn't sent (subscribers don't get woken up by would-be mutations), and OTel spans still emit with `locutus.dry_run=true` attribute (observability of dry-run *is* operational value).

**Resolved design questions** (chat 2026-05-29):

1. **Verb scope: ACP/MCP-driven verbs only — `import`, `refine`, `adopt`, `assimilate`.** Phase B's table also covered `init` and `update`, which today are deterministic Go file-ops with a different dry-run shape (preview what file would be created vs. preview what mutations a coding-agent would propose). Rejected: scope everything from Phase B's table — would mix two parallel patterns (Go-side preview helpers for init/update; MCP-capture wrappers for the ACP verbs), less coherent as a single DJ. Init/update can get their own dry-run treatment in a small follow-up if a real consumer surfaces.

2. **Fidelity model: faithful in-memory shadow, not "first wave + stop" or "honest stub."** Cheap models lose the cascade — iteration-2 reconciler revisions, citation walk, multi-axis fan-out — exactly the work a dry-run is *for*. Honest stub (capture but leave reads showing the unchanged base store) breaks read-after-write semantics; the agent panics, re-proposes, or aborts. The per-session overlay is the only model that preserves "the workflow runs faithfully end-to-end" without compromise.

3. **Mechanism shape: per-session overlay on the existing `SpecStore`, not whole-store checkpoint or separate ephemeral daemon.** Overlay scales with the size of the captured mutation set (typically tiny — one feature plus a handful of revisions); checkpoint pays a full-graph-copy cost up front per dry-run session (200+ nodes for a mature project = real memory); separate daemon defeats DJ-135's per-project singleton + breaks the multi-client coordination model. Overlay preserves DJ-134's single-source-of-truth invariant and is the smallest extension to the existing architecture.

4. **Capture surface: MCP `spec_*` tool boundary, not the Go-internal write path.** Phase B's premise (Go code mutates the graph; a flag suppresses the final write) is gone post-DJ-135 — the writes flow from the agent's MCP tool calls. The only consistent boundary is the tool handler. By corollary: state writes (`FileStateStore.Save`, zero callers today, predates ACP) are out of scope — when `adopt`'s drift-detection workflow matures and needs to persist reconciliation state, those writes will move onto MCP tools as a separate DJ (consolidating *all* daemon-managed state mutations onto the MCP tool surface), and the same `captureOnly` wrapper picks them up by virtue of the same boundary. The "expand MCP tool surface to cover the writes we need" principle is doing more than enabling dry-run; it's a soft architectural test (anything that *can't* move onto MCP tools is suspect).

5. **Search index fidelity gap accepted.** Under dry-run, the Bluge index isn't updated; `spec_search` post-filters the overlay's captured entries onto base index results. Full-text searches that match unique text in a captured-but-not-yet-indexed body have a fidelity gap. Rejected: per-session shadow Bluge index (re-indexing on every captured write is expensive and the typical agent search flow — search-by-id-prefix, search-by-kind, search-on-title — is covered by the overlay's entries directly). The gap is documented; the common search shapes are correct.

6. **Two report renderings, not one: agent-narrated (mode-uniform) + CLI-side `tools.jsonl` (headless authoritative).** Agent's closing message is the only path interactive operators have (no session trace exists); headless gets a second CLI-side render from `tools.jsonl` that's deterministic and machine-readable, authoritative when the two diverge. Rejected: agent-only (interactive operators get nothing scriptable, and the agent's narration is unreliable for headless tooling), CLI-only (interactive operators get nothing at all).

7. **Output format flag (`--format markdown|json`) lives on the CLI, signaled via env + `_meta`, consumed by the agent for the closing message and by the CLI for its own render.** Operator chooses once at invocation; both renderings honor it. JSON output is the structured capture verbatim; markdown is the agent's prose summary + the CLI's structured summary. Rejected: format-derived-from-stdout-isatty (would tangle UI inference with explicit operator choice).

8. **Exit code always 0 on a successful dry-run.** Phase B's principle, preserved verbatim. Non-zero exit means dispatch failed (transport error, daemon unreachable, agent crashed mid-run), not "would-be mutations were rejected." Operators scripting `--dry-run | jq` rely on the success exit; coupling exit code to mutation count would break that.

**Alternatives considered:**

- **Daemon-side Go-flag suppress-the-write-pass (the literal Phase B plan).** Rejected — the write pass moved out of Locutus Go code in DJ-135; there's nothing to suppress. The Go verb is a dispatcher.

- **CLI-driven temp-project diff (run normally against a tempfile-backed `.borg/spec/`, diff against the real one after).** Rejected — heaviest of the options reviewed, defeats DJ-135's per-project singleton + DJ-134's in-memory `SpecStore` benefits, doesn't compose with concurrent normal sessions wanting to attach to the same daemon during the dry-run.

- **Single-iteration "first wave + stop" capture.** Rejected per resolved-question 2 — loses the cascade, citation walk, iteration-2 convergence work. Preview missing the cascade isn't really a preview.

- **Whole-`SpecStore` checkpoint at session start (option B from the brainstorm).** Rejected per resolved-question 3 — pays a full-graph-copy cost up front per dry-run session, while the captured mutation set is typically tiny. The overlay scales with what's actually proposed, not with what already exists.

- **Separate ephemeral daemon per dry-run session (option C from the brainstorm).** Rejected per resolved-question 3 — defeats the per-project singleton, can't compose with operators wanting to preview *while* other sessions are attached, requires copying the spec graph to tempfs on fork.

- **Single agent-narrated report path (no CLI-side `tools.jsonl` render).** Rejected per resolved-question 6 — agent narration is unreliable for machine consumption; operators scripting against `--dry-run --json | jq` need a deterministic path. The CLI render is the authoritative source when agent narration drifts.

- **Cover state writes (`FileStateStore.Save`) under DJ-147.** Rejected per resolved-question 4 — state writes are zero callers today; when adopt's drift-detection workflow matures, those writes should move onto MCP tools as a separate DJ (consolidating the daemon's mutation surface), at which point the same wrapper covers them.

- **Format-derived-from-stdout-isatty.** Rejected per resolved-question 7 — couples UI inference with operator intent; a script piping to `jq` from a tty is a legitimate use case.

**Consequences:**

- **Code (add):**
  - `internal/agent/spec_store_overlay.go` — the `sessionOverlay` data structure, `OverlayPut`, `OverlayView` (overlay-then-base read), session-close cleanup.
  - `internal/mcp/tools_spec_read.go` (extend) or a new `internal/mcp/tools_dry_run.go` — register `spec_dry_run_report` MCP tool returning the calling session's `overlay.captured` plus `SessionDryRunFormat`.
  - `internal/mcp/session_context.go` (extend) — `captureOnly[In, Out]` generic wrapper alongside `requireRuntimeAny`; `SessionDryRun(*mcp.ServerSession) bool` + `SessionDryRunFormat(*mcp.ServerSession) string` accessors; `newInitializedHandler` reads `_meta["locutus.dry_run"]` + `_meta["locutus.dry_run_format"]` and stores them on the session map.
- **Code (modify):**
  - `internal/mcp/tools_spec_write.go` — wrap each of the 16 mutation tool registrations with `captureOnly(..., capturePropose<Kind>)` (or `captureRevise<Kind>` / `captureDelete<Kind>` / `captureMarkApproachDrifted` / `captureUpdateGoalsMdHash`). Each `capture*` helper is small: validate input, build typed body via the existing `build*Body` helper, call `OverlayPut`.
  - `cmd/mcp.go` — add `resolveLocutusDryRun() bool` + `resolveLocutusDryRunFormat() string` helpers paralleling `resolveLocutusMode`; forward both as `_meta["locutus.dry_run"]` + `_meta["locutus.dry_run_format"]` on the bridge's `initialize` request.
  - `cmd/import.go`, `cmd/refine.go`, `cmd/adopt.go`, `cmd/assimilate.go` — add `DryRun bool` + `Format string` Kong fields with `enum:"markdown,json" default:"markdown"`; pass through to `runActivityVerb`.
  - `cmd/activity_verb.go` — `runActivityVerb` sets `LOCUTUS_DRY_RUN=1` + `LOCUTUS_DRY_RUN_FORMAT=<value>` on the spawned coding-agent process env when `dryRun` is true, appends the dry-run contextNote per format; after dispatch returns, if `dryRun` is true and the run was headless, reads `tools.jsonl`, filters to mutation-family tool names, renders per format to stdout.
- **Code (test):**
  - Overlay unit tests: `OverlayPut` round-trip, `OverlayView` overlay-then-base merge semantics, deleted-mask correctness, concurrent overlay isolation.
  - `captureOnly` unit tests: passthrough on non-dry-run, capture-and-success on dry-run, composition with `requireRuntimeAny`.
  - `spec_dry_run_report` integration test: in-memory MCP transports, propose-then-report, assert ordered list shape + format string.
  - End-to-end test: drive a fake workflow through `import --dry-run`, assert nothing in `.borg/spec/` changed, assert `tools.jsonl` rendered the report correctly.
- **Docs:** this entry; [DECISION_JOURNAL.md](../DECISION_JOURNAL.md) manifest row; a "Dry-Run" passage in [docs/runtime-affordances.md](../runtime-affordances.md) explaining the `LOCUTUS_DRY_RUN` env / `_meta` plumbing alongside the DJ-143 mode/runtime plumbing; one-paragraph addition to [CLAUDE.md](../../CLAUDE.md) Sources of Truth describing the dry-run mechanism. A note in [`docs/plans/verb-set-phase-b.md`](../plans/verb-set-phase-b.md) marking the file as superseded by DJ-147 (Phase B's principle preserved; implementation differs).
- **Validation:** end-to-end against winplan — `./locutus refine feat-main-dashboard --dry-run` runs the full workflow, produces a markdown report in `output.md` + a CLI-side structured summary on stdout, leaves `.borg/spec/` untouched (verifiable by `git status .borg/spec/`). The mutation-family entries in `tools.jsonl` carry the same shape they would carry in a normal run; the daemon's persistence layer never sees a `SpecStore.Put` for them. A parallel `./locutus import docs/dashboard.md --dry-run --format json` produces stdout JSON suitable for `jq '.captured[] | select(.kind == "feature") | .id'`.
- **Follow-ups (separate DJs, tracked here):**
  - **Consolidate state mutations onto MCP tools.** When `adopt`'s drift-detection workflow needs to persist `ReconciliationState` per approach (the `FileStateStore.Save` path currently has zero callers), those writes should move onto a new family of state-mutating MCP tools rather than wiring direct Go callers to the file store. A separate DJ scopes that consolidation; DJ-147's `captureOnly` wrapper picks the new tools up by virtue of the same MCP-tool boundary, so dry-run-`adopt` will faithfully preview state writes from the day they ship.
  - **`init` / `update` dry-run.** Phase B's table included these; their dry-run shape is Go-side (preview what file/binary would be created). Out of scope for DJ-147. If a real consumer surfaces, a small follow-up adds them.
  - **`code_assimilation.claude-code.md` real playbook (deferred DJ-135 phase 5 checkpoint 3).** Today's stub no-ops on spec mutations, so `assimilate --dry-run` produces an empty report. When the real playbook lands and the agent actually calls `spec_propose_*` during inference, dry-run "just works" with no DJ-147 changes — the wrapper at registration is already in place. Noted here so the deferred work doesn't get re-derived independently.
