## DJ-136: Per-Runtime Idiomatic Dispatch Layer — Playbook Overlays, Hook Configs, and Native Goal-Loops (Refines [DJ-135](dj-135-multi-runtime-pivot.md)'s Cross-Runtime-Contract Stance; Reverses Resolved-Question 14; Adds `.<runtime>.md` Playbook Overlay Convention to `internal/scaffold/plans/`; Introduces Hook Configs as a Per-Runtime Publisher Output; Asymmetric Convergence: `/goal`-Driven on Claude Code, Locutus-Driven on Codex / Gemini; Per-Runtime Use of Each Provider's Idiomatic Affordances Replaces the Lowest-Common-Denominator Floor)

**Status:** shipping (Phases 1-7 landed 2026-05-26 on branch `dj-136-per-runtime-idiomatic`; tests + vet green across the full suite; plan at [.claude/plans/dj-136-per-runtime-idiomatic.md](../.claude/plans/dj-136-per-runtime-idiomatic.md) marked DONE). Empirical validation against the three runtimes (winplan on Claude Code, Codex-only test project, Gemini-only test project) is deferred to a follow-up session — the in-tree unit tests cover the loop logic, overlay resolution, hook substitution, publisher emit shape, and dispatch branching, but end-to-end behavior against a real Claude Code `/goal` evaluator and a real Codex / Gemini ACP server is not yet exercised in this branch.

**Context.** [DJ-135](dj-135-multi-runtime-pivot.md) shipped the multi-runtime pivot with the playbook as a single cross-runtime contract — one `<activity>.md` file per activity, identical across Claude Code, Codex, and Gemini. Resolved-question 14 of that DJ explicitly deferred per-runtime overrides as a "future escape hatch only," with the rationale that v1 stability mattered more than fine-tuning per provider's idiosyncrasies. That stake held through phases 1-7 of DJ-135 and the empirical winplan validation run on 2026-05-25.

The 2026-05-26 winplan refine session (`~/projects/winplan/.locutus/sessions/20260526/1918/080000/`) surfaced the cost of the LCD floor concretely. The orchestrator's convergence loop discipline lives entirely in playbook prose ("dispatch a scout each iteration; loop until scout reports converged: true"); enforcement of subagent output schemas relies on prose-bound discipline rather than mechanical hooks; per-tool validation (e.g., axis id present on decisions) is implicit in agent prompts rather than explicit in a PreToolUse callback. Each of these is fragile against future drift and against runtime-specific failure modes — and crucially, Claude Code's `/goal` slash command (a session-scoped model-evaluated stop condition that auto-restarts turns until a condition is met) is sitting unused exactly where the prose-driven loop is most brittle.

External capability fetches on 2026-05-26 confirmed the asymmetry across the three primary runtimes:

- **Claude Code [`/goal` docs](https://code.claude.com/docs/en/goal)** documents `/goal <condition>` as a session-scoped prompt-based Stop hook (requires v2.1.139+). Each turn ends with an evaluator call to a small fast model that judges the condition against the conversation transcript; "no" verdicts feed the reason back as next-turn guidance, "yes" clears the goal. Condition up to 4KB. Implementation rides on the same Stop-hook surface available to user-authored scripts.
- **Codex [hook docs](https://developers.openai.com/codex/hooks)** documents the full lifecycle: SessionStart, SubagentStart, PreToolUse, PermissionRequest, PostToolUse, PreCompact, PostCompact, UserPromptSubmit, SubagentStop, Stop. Hooks can inject context, block/allow tool executions, rewrite inputs, deny/approve permissions, and stop turns. Configuration via `hooks.json` or inline `[hooks]` in `config.toml`. **No goal-loop equivalent** — Stop hook fires but does not auto-restart turns.
- **Gemini CLI [hook docs](https://geminicli.com/docs/hooks/)** documents SessionStart, SessionEnd, BeforeAgent, AfterAgent, BeforeModel, AfterModel, BeforeToolSelection, BeforeTool, AfterTool, PreCompress, Notification. AfterAgent can "force retry or halt" (closest to goal-loop semantics, but without the model-evaluated stop condition). Configuration in `settings.json` with matcher + command shape; merges project → user → system → extensions.

[[reference-runtime-hook-asymmetry]] captures the comparison. The shape is: Claude Code has the richest native affordance set (rich hooks + `/goal` goal-loop primitive); Codex has a full hook lifecycle but no goal-loop; Gemini has a full hook lifecycle with limited retry-or-halt semantics in AfterAgent but no goal-loop. Idiomatic use looks different on each runtime.

The user's strategic framing (chat 2026-05-26, captured in [[feedback-runtime-idiomatic-no-lcd]]): chasing the LCD across three runtimes racing to parity is a losing strategy. The runtimes will keep adding capabilities; each is differently funded (Anthropic + OpenAI on tens-of-billions of capital, Google on internal dogfooding at Cloud-scale plus TPU cost advantages); over the next 12-24 months the gap between LCD and best-affordance will only widen. Locutus should capitalize on each runtime's strengths and pay the per-runtime divergence tax instead of restricting itself to what all three runtimes can do uniformly.

**Decision.** Reverse [DJ-135](dj-135-multi-runtime-pivot.md) resolved-question 14. The playbook content stays cross-runtime where it's portable; everything around it becomes runtime-aware. Specifically:

1. **Playbook overlay convention** at `internal/scaffold/plans/`: `<activity>.md` is the required default (every activity must have one); `<activity>.<runtime>.md` is an optional per-runtime overlay where `<runtime>` matches the AgentSpawns key (`claude-code`, `codex`, `gemini`). When the resolver sees an overlay matching the dispatched runtime, the overlay's content fully replaces the default's content for that dispatch. The published copy under `.borg/plans/` inherits the same overlay convention.

2. **Total override, not partial-include.** v1 ships with overlay-or-default, no templating, no include directive, no partial merge. Duplication is the cost; a partial-include mechanism arrives only if duplication pain emerges empirically. Most activities will never need an overlay; the ones that do are the ones where the difference is structurally significant.

3. **Hooks are a separate publisher-emitted layer.** Per-runtime hook configs live at `internal/scaffold/hooks/<runtime>/<activity>.{json,toml}` and emit to runtime-specific config locations (`.claude/settings.json` hooks field for Claude Code; `.codex/hooks.json` or inline `[hooks]` in `.codex/config.toml`; `.gemini/settings.json` hooks for Gemini). There is no default-fallback shape for hooks — they're inherently per-runtime because the event names and config schemas differ. The publisher emits hooks for every runtime that has a matching subdir; runtimes without hook configs run with no hooks.

4. **Asymmetric convergence loop.** On Claude Code the dispatch sends a `/goal <condition>` directive; the condition references the published `.claude/commands/locutus-refine.md` slash command and names the termination predicate (scout reports `converged: true` with empty `axes_open`, OR 20 iterations consumed). Iteration is driven by Claude Code's evaluator. On Codex and Gemini the dispatch sends one one-iteration-shaped playbook execution; Locutus's runner reads spec state via SpecStore between iterations and re-dispatches until the same termination predicate holds. Same logical loop, different driver.

5. **Per-tool enforcement moves into hooks where each runtime supports it.** Validation of `spec_propose_*` inputs (axis id present, etc.) becomes a PreToolUse hook on Claude Code and Codex, a BeforeTool hook on Gemini. Cascade-revision triggering after every feature/strategy commit becomes a PostToolUse / AfterTool hook. The hooks call back into Locutus MCP tools or out to small CLI helpers we ship as part of the publisher output.

6. **Agent prompts stay cross-runtime.** No `.<runtime>.md` overlay surface for `internal/scaffold/agents/` in this DJ. Agent prompts have been deeply tuned to be runtime-neutral and there's no forcing case yet. Revisit if a real divergence demand emerges.

7. **Playbook becomes one-iteration-shaped.** The cross-runtime default `spec_refinement.md` describes one iteration's worth of work (scout, decision-elaborator fanout, revisions, cascade, commit) — not the outer loop. The outer loop is the runtime's responsibility (via `/goal` on Claude Code) or Locutus's responsibility (on Codex / Gemini). This is the structural simplification that makes the asymmetric design tractable: prose discipline doesn't have to encode the loop on either path because each path has a mechanical loop driver.

8. **Drift between default and overlay is managed by human discipline + a grep-invariant test.** A test asserts every overlay's "must-be-present" surface (MCP tool names referenced, agent names referenced, activity verb invoked) is consistent with the default's. Prose divergence is permitted; structural divergence is flagged. No sync-tracking header (e.g., commit-sha-anchored "synced from") in v1 — the override count will stay small enough early that review catches drift.

9. **Plan updates from the agent runtime become a surfaced event class.** The [ACP agent-plan spec](https://agentclientprotocol.com/protocol/agent-plan) defines `session/update` notifications with `"sessionUpdate":"plan"` carrying full-replacement plan entries (content + priority high/medium/low + status pending/in_progress/completed). The acp-go-sdk already types these as `SessionUpdatePlan` + `PlanEntry`. [DJ-135](dj-135-multi-runtime-pivot.md)'s Phase 1 deferred them as part of the "surfaced supervisor events in Phase 1" scope cut (see the `default` branch comment in [internal/dispatch/acp/events.go:83-88](../internal/dispatch/acp/events.go#L83-L88)). DJ-136 reverses that deferral: plan updates render inline in the progress writer, the playbook's one-iteration directive instructs the orchestrator to call `TodoWrite` (or the runtime's equivalent plan tool) to lay out its iteration plan, and the plan entries' status transitions become the operator-visible scaffolding of iteration progress. This converts iteration scaffolding from prose-bound discipline ("loop while doing X") into a structured artifact every runtime can render. Claude Code's `/goal` evaluator also benefits — plan-entry status transitions are visible in the conversation transcript and become structured signal the evaluator can use to judge progress.

**Resolved design questions** (settled in chat 2026-05-26):

1. **Runtime ID naming matches `internal/dispatch/acp/registry.go`'s AgentSpawns keys.** `claude-code` / `codex` / `gemini`, hyphenated where Claude Code requires it. Yes, `spec_refinement.claude-code.md` is mildly clunky compared to a short alias like `.claude.md`, but a separate "short name for plans" lookup grows footguns; every other surface in the codebase (activity registry, AgentSpawns, publisher subdirs) already uses these IDs verbatim.

2. **Default must always exist; overlay is opt-in.** Test enforces. A plan that ships only `spec_refinement.claude-code.md` without the default is a build failure. This makes the overlay convention safe to extend: adding a new runtime later defaults to "use the cross-runtime playbook" until someone authors an overlay.

3. **Total override v1, partial-include / templating deferred.** The user weighed the duplication tax against the templating-engine complexity and chose total override. A partial-include mechanism is a future extension if real pain emerges; not pre-emptive.

4. **Hooks have no default-fallback shape.** They're config files, not prose; their event names + config schemas differ per runtime. The publisher reads `internal/scaffold/hooks/<runtime>/` and emits or skips per runtime presence. No cross-runtime canonical hook file.

5. **Asymmetric convergence is the deliberate design.** Symmetric design (Locutus drives all loops; `/goal` is a redundant safety net) was considered and rejected; sacrifices the Claude Code-native UX win (operators get a `◎ /goal active` indicator and `/goal clear` they can act on) for cross-runtime symmetry the user doesn't want. Per [[feedback-runtime-idiomatic-no-lcd]].

6. **Agent overlay surface deferred.** Same shape *could* apply to `internal/scaffold/agents/*.md` overlays but no forcing case yet. The cost of duplication on a deeply-tuned cross-runtime prompt is higher than the cost on a playbook (agents are reused across activities; playbooks are activity-specific). Re-evaluate if a real divergence demand emerges.

7. **Drift management is human discipline + grep test, not sync-tracking header.** The grep test asserts structural invariants (same MCP tools / agents / verbs); prose divergence is permitted. Sync-tracking via commit-sha-anchored headers is a more rigorous discipline but deferred — at v1 override count it's overkill.

**Alternatives considered.**

- **Keep the LCD playbook contract; tune prose more carefully.** Rejected per [[feedback-runtime-idiomatic-no-lcd]]: chasing LCD across runtimes racing to parity is a losing strategy. The LCD will keep moving as runtimes evolve, and we'd perpetually be behind the leading edge of each. Each individual case where an overlay would help is small; the cumulative loss across 12-24 months of runtime evolution is large.

- **Partial-include / templating mechanism for overlays from v1.** Considered for de-duplication. Rejected: total override v1 is simpler to reason about, and overlays will be rare enough early that duplication is bounded. A partial mechanism arrives if real pain emerges. The wrong direction is to over-engineer the override mechanism before we see the failure mode it solves.

- **Symmetric design (Locutus drives all convergence loops; `/goal` set as a redundant ceiling).** Considered for cross-runtime symmetry. Rejected: sacrifices Claude Code-native UX (`◎ /goal active` indicator, `/goal clear`, model-evaluated stop) for parity with weaker runtimes. The user explicitly wants per-runtime idiomatic use, not LCD.

- **Sync-tracking headers (overlay starts with `<!-- synced-from: spec_refinement.md@<sha> -->`).** Considered for tighter drift detection. Deferred to a future iteration if the grep-invariant test proves insufficient. At v1 override count, the cost of the header discipline exceeds the value over human review.

- **Agent prompts also get `.<runtime>.md` overlays.** Considered for fine-tuning prompts per runtime quirks (e.g., Codex's slightly-different tool-use idioms). Deferred: agent prompts are runtime-neutral by design and we haven't seen a forcing case. The duplication cost on a deeply-tuned cross-runtime prompt is higher than on a playbook.

- **Hooks share a cross-runtime canonical form, publisher translates to per-runtime configs.** Considered for symmetry with the playbook overlay convention. Rejected: hook event names differ structurally across runtimes (`PreToolUse` ≠ `BeforeTool`; `Stop` ≠ `AfterAgent`); a canonical form would either be the union of all runtimes (huge surface) or the intersection (which falls back into LCD).

**Consequences.**

- **Code (add):**
    - Per-runtime plan loader in `internal/runner/` (or `internal/activity/`): `loadActivityPlaybook(fsys, activityName, runtime)` tries `<activity>.<runtime>.md` first, falls back to `<activity>.md`. ~30 lines + tests.
    - `internal/scaffold/hooks/` directory structure (one subdir per runtime). Embedded via `go:embed`. The publisher reads from here at scaffold + reset time.
    - Per-runtime hook publishers under `internal/publisher/<runtime>/hooks.go` (one per runtime). Each emits the runtime's hook config format to the runtime's expected location.
    - `internal/runner/loop.go` (or similar): the outer-loop driver Locutus runs on Codex / Gemini. Reads SpecStore between iterations, decides whether convergence-predicate holds, re-dispatches if not, bounded by max-iteration ceiling.
    - Per-runtime dispatch strategy in `internal/runner/run.go` or a new `internal/runner/dispatch.go`: branches on runtime to (a) build `/goal <condition>` prompt for Claude Code, or (b) drive the outer loop for Codex / Gemini.
    - `dispatch.EventPlan` event kind in `internal/dispatch/` for plan-update notifications; `translateUpdate` in [internal/dispatch/acp/events.go](../internal/dispatch/acp/events.go) handles `SessionUpdatePlan` and emits `EventPlan` events carrying the full entry list (full-replacement per the ACP spec); the runner's event loop renders inline plan blocks via the progress writer.
    - Tests: orphan-overlay prevention (every `.<runtime>.md` has a sibling `.md`), grep invariants (overlays reference same MCP tools / agents / verbs as default), hook config schema validation per runtime, plan-event translation + rendering.

- **Code (modify):**
    - `cmd/activity_verb.go`'s `loadActivityPlaybook` gains a runtime parameter and routes to the new loader.
    - `internal/scaffold/plans/spec_refinement.md` becomes one-iteration-shaped (strip outer-loop discipline; surface scout's converged verdict in plain text so the evaluator on Claude Code or Locutus on Codex / Gemini can read it).
    - `internal/publisher/` learns to publish hook configs alongside agents + slash commands. Per-runtime publisher subdir gains a hooks emit step.
    - `internal/runner/run.go`: the `EventToolCall` handling stays the same; the new dispatch-strategy branching wraps the existing `DispatchActivity` call.

- **Code (add — new playbook overlays at v1):**
    - `internal/scaffold/plans/spec_refinement.claude-code.md` — `/goal`-shaped variant pointing at the published `/locutus-refine` slash command. Termination condition references the scout's plain-text convergence verdict.
    - (Codex / Gemini start with no overlay; the default one-iteration-shaped `spec_refinement.md` is what they get.)

- **Code (add — new hook configs at v1):**
    - `internal/scaffold/hooks/claude-code/spec_refinement.json` — PreToolUse on `spec_propose_decision` (validate axis id present); PostToolUse on feature / strategy writes (trigger cascade-revision detection); Stop hook is owned by `/goal` and not duplicated.
    - `internal/scaffold/hooks/codex/spec_refinement.toml` — PreToolUse + PostToolUse equivalents.
    - `internal/scaffold/hooks/gemini/spec_refinement.json` — BeforeTool + AfterTool equivalents.
    - All three call back into a small Locutus CLI helper (`locutus hook-validate-decision`, `locutus hook-cascade-trigger`, etc.) that runs in-process against the MCP daemon socket.

- **User-visible:**
    - On Claude Code, `locutus refine goals` now drops the operator into a `◎ /goal active` session. Operator can `/goal clear` to abort or `/goal` to check status (turns, tokens, last evaluator reason). Convergence is decided by Claude Code's evaluator, not Locutus.
    - On Codex / Gemini, `locutus refine goals` runs as before but the per-tool validation surface is hooks-driven (operator sees PreToolUse / BeforeTool denials surface with structured reasons rather than ad-hoc prose disagreements in subagent output).
    - `.claude/settings.json`, `.codex/hooks.json`, `.gemini/settings.json` gain a `hooks` field with Locutus entries (idempotent insertion; doesn't clobber user-authored hooks). Removable via `locutus update --reset` rewrite.
    - The user-visible playbook contract changes shape: `spec_refinement.md` is one iteration, not a multi-iteration script. Operators editing the playbook see less prose discipline, more focused content.
    - Plan updates from the agent render inline in the progress writer as the iteration unfolds. When the orchestrator calls `TodoWrite` (or its runtime-specific equivalent), the rendered block shows entries with status icons (`○` pending, `⟳` in_progress, `✓` completed) so the operator can see what the agent has scheduled for itself and how far through it is — without needing to attach to the agent's session UI. Plan updates are full-replacement per the ACP spec; the renderer prints the current full plan whenever the entry set or any status changes.

- **Documentation:**
    - New [docs/runtime-affordances.md](../docs/runtime-affordances.md) — per-runtime affordance map (hook events, slash command formats, goal-loop availability), naming conventions, publisher output, overlay convention.
    - Update [docs/agent-conventions.md](../docs/agent-conventions.md) — note that playbook overlays are now permitted; agent prompts still cross-runtime; per-runtime hook authoring conventions.
    - Update [CLAUDE.md](../CLAUDE.md) — top-level section on per-runtime playbook overlays + hook publishing; note the asymmetric convergence model.
    - Update [docs/council.md](../docs/council.md) — Mermaid diagram updates to reflect that convergence is driven by `/goal` on Claude Code (with a small evaluator-loop visualization) and by Locutus's runner on Codex / Gemini (with the iteration-budget loop visualization). Per [[feedback-council-doc-maintenance]] this update must land in the plan's Phase N.
    - Update [docs/debugging-traces.md](../docs/debugging-traces.md) — Claude Code session logs now include the `/goal` evaluator's verdicts after each turn; the trace shape gains an evaluator-rejection-reason field.

- **Migration:**
    - Direct cut per [[feedback-no-back-compat-until-self-hosting]]. The `spec_refinement.md` rewrite to one-iteration shape lands in the same phase as the Claude Code path; Codex / Gemini paths land in subsequent phases. The user runs `locutus update --offline --reset` to refresh scaffolds + publish hook configs.
    - No spec-graph migration (SpecStore + `.borg/spec/` carry forward unchanged).
    - Existing winplan project: the next `refine` invocation picks up the new playbook + hook configs after `update --reset`. No user action required beyond that.

- **Performance:**
    - On Claude Code, `/goal` adds one small-fast-model call per turn (Haiku default, ~negligible per the [goal docs](https://code.claude.com/docs/en/goal)). Convergence loops should be tighter because the evaluator's "no, because X" reason directly guides the next turn rather than relying on the orchestrator's self-discipline to re-dispatch.
    - On Codex / Gemini, Locutus's outer loop adds one SpecStore read + convergence check between iterations (cheap; in-process). The bottleneck stays "subagent dispatch latency," same as today.
    - Hooks add per-tool overhead (one CLI subprocess invocation per hook). On Claude Code's PreToolUse for `spec_propose_decision` this is small (~10-50ms) and worth it for the mechanical enforcement. On hot paths (e.g., PostToolUse on every tool call) we want hooks to be ~minimal — favor structured-output validation that returns fast.

**Reversal criteria.** Revert specific facets if:

- (a) **The overlay convention's drift problem becomes acute.** If `<activity>.<runtime>.md` files diverge from their defaults in ways the grep-invariant test misses, and bugs trace to the drift (e.g., an overlay references a retired MCP tool name), the sync-tracking header discipline lands as a follow-up. Mitigation before reversal: tighten the grep-invariant test's surface; consider commit-sha-anchored headers.

- (b) **`/goal` proves unreliable for convergence-evaluation.** If the evaluator's small fast model produces false positives (declares convergence when scout actually said `converged: false`) or false negatives (loops past genuine convergence), the Claude Code path's reliance on `/goal` fails the core hypothesis. Mitigation before reversal: tune the goal condition prose; verify the scout surfaces its verdict prominently in the conversation transcript; consider redundant Locutus-side convergence check that overrides `/goal` if it disagrees. Full revert sends Claude Code back onto the Locutus-driven outer-loop path (symmetric with Codex / Gemini).

- (c) **Per-runtime hook maintenance burden disproportionate to value.** If each runtime's hook config requires nontrivial bespoke maintenance per runtime release (event names change, schema fields rename, behavior shifts), the publisher complexity exceeds the value of mechanical enforcement. Mitigation before reversal: collapse hooks back into prose discipline in the playbook overlay, retain Locutus-driven enforcement at MCP-tool entry points instead.

- (d) **Cross-runtime divergence becomes operationally confusing for users.** If operators report meaningful confusion between "what happens on Claude Code vs Codex vs Gemini," and the asymmetric design proves too high-friction for documentation to bridge, partial symmetry (e.g., Locutus drives outer loop on all three; `/goal` is set on Claude Code as a UX nicety only) may be the right trade. Mitigation before reversal: docs/runtime-affordances.md tightens; operator-visible UX converges where it can.

**Reference.** Synthesizes the design conversation across chat 2026-05-26 covering: (a) the diagnosis that prose-bound iteration discipline is the still-fragile residual after DJ-135 shipped; (b) the per-runtime capability fetch ([Claude Code /goal](https://code.claude.com/docs/en/goal), [Codex hooks](https://developers.openai.com/codex/hooks), [Gemini hooks](https://geminicli.com/docs/hooks/)); (c) the strategic framing that LCD is a losing strategy with runtimes racing to parity. Reverses resolved-question 14 of [DJ-135](dj-135-multi-runtime-pivot.md). Builds on [DJ-135](dj-135-multi-runtime-pivot.md)'s publisher + activity registry as the foundation this layer extends. Per [[feedback-runtime-idiomatic-no-lcd]] (durable preference: per-runtime idiomatic, no LCD) and [[reference-runtime-hook-asymmetry]] (the fetched-and-validated capability matrix). Plan at [.claude/plans/dj-136-per-runtime-idiomatic.md](../.claude/plans/dj-136-per-runtime-idiomatic.md).
