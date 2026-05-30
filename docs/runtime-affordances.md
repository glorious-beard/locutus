# Runtime affordances

Reference map of which dispatch + enforcement affordances each coding-agent runtime exposes, and how Locutus uses them. Authoritative design lives in [DJ-135](DECISION_JOURNAL.md#dj-135) (multi-runtime pivot) and [DJ-136](DECISION_JOURNAL.md#dj-136) (per-runtime idiomatic dispatch); this file is the operational map.

## Runtime ID set

| Runtime ID | Spawn command | Source |
|---|---|---|
| `claude-code` | `claude-agent-acp` (stdio) | Anthropic's ACP shim |
| `codex` | `codex-acp` | GitHub release tarball |
| `gemini` | `gemini --acp` | Official Google Gemini CLI |

The ID set is the source of truth for the `<runtime>` token in `<activity>.<runtime>.md` overlays and `internal/scaffold/hooks/<runtime>/<activity>.<ext>` hook fragments. Adding a runtime means adding an `acp.AgentSpawns` entry + the corresponding scaffold subdirectories.

## Affordance comparison

| Affordance | claude-code | codex | gemini |
|---|---|---|---|
| Interactive convergence loop | in-runtime dynamic workflow (DJ-144) — the tier-2 keyword playbook contains "workflow" and surfaces as `/locutus-refine` etc. | agent self-loop via loop-state tools (DJ-142) | agent self-loop via loop-state tools (DJ-142) |
| Headless convergence loop | in-runtime dynamic workflow (DJ-144) — single ACP dispatch, `dispatchUsesOuterLoop("claude-code") == false` | Locutus `OuterLoopRunner` (DJ-140) | Locutus `OuterLoopRunner` (DJ-140) |
| PreToolUse hook | ✓ `.claude/settings.json` hooks | ✓ `.codex/config.toml` `[[hooks]]` | ✓ `.gemini/settings.json` `BeforeTool` |
| PostToolUse hook | ✓ | ✓ | ✓ `AfterTool` |
| MCP server attachment | `.mcp.json` | `[mcp_servers.locutus]` in config.toml | extension manifest |
| Slash commands | `.claude/commands/*.md` | `.codex/commands/*.toml` | `.gemini/extensions/*/commands/*.toml` |
| Subagent dispatch | Task tool | analogous | analogous |
| Subdirectory namespacing | ✓ (`.claude/agents/locutus/`) | — (filename prefix) | — (extension scope + filename prefix) |

DJ-136 originally made `/goal` a load-bearing asymmetry — Claude Code's evaluator-after-each-turn loop drove headless convergence while Codex/Gemini relied on Locutus's runner. DJ-140 retired that asymmetry for headless dispatch (since `/goal` is interactive-only and unavailable in the headless `claude-agent-acp` path) and unified everyone on `OuterLoopRunner`. DJ-144 then **reinstated** an in-runtime loop for Claude Code on a new footing: dynamic workflows are headless-reachable via the keyword trigger (the tier-2 `<activity>.claude-code.md` playbook contains "workflow"), so Claude Code now converges in-runtime in both modes and is off `OuterLoopRunner` entirely. The `/goal` wrapper (`spec_refinement.claude-code.interactive.md`) was deleted under DJ-144 §7; the published `/locutus-refine` slash-command body is now the workflow-shaped tier-2 keyword playbook itself.

## Per-runtime emitted files

Run `locutus init` or `locutus update --reset` to (re-)emit all of these from the canonical scaffold.

### claude-code

```
.claude/
├── agents/locutus/<id>.md            # one per agent
├── commands/locutus-<cli-verb>.md    # one per activity (refine, import, adopt, assimilate)
└── settings.json                     # currently unused for hooks
.mcp.json                             # locutus MCP server attachment
```

The `<cli-verb>` matches the CLI verb that dispatches the activity (`refine` ↔ `spec_refinement`, `import` ↔ `feature_ingestion`, `adopt` ↔ `code_adoption`, `assimilate` ↔ `code_assimilation`). Operators recognize `/locutus-refine` more readily than `/locutus-spec-refinement`.

The tier-2 keyword playbook `internal/scaffold/plans/<activity>.claude-code.md` is the body of both the published `.claude/commands/locutus-<verb>.md` (interactive slash command) and the headless ACP dispatch prompt (DJ-144 took Claude Code off `OuterLoopRunner`, so it's a single dispatch). The playbook contains the word "workflow" and the `{{max_iterations}}` token: the keyword triggers the dynamic-workflow path in Claude Code v2.1.154+, and the token is substituted from the activity registry's `max_iterations` cap at dispatch by `cmd/activity_verb.go`'s `injectMaxIterations`. The pre-DJ-144 `/goal` wrapper (`spec_refinement.claude-code.interactive.md`) was deleted; a future saved-workflow `.js` script (Phase 3.2 of DJ-144's plan, currently spike-gated) will pin the orchestration deterministically for interactive runs, but until that lands the markdown body carries the activation contract.

### codex

```
.codex/
├── agents/locutus-<id>.toml          # one per agent
├── commands/locutus-<cli-verb>.toml  # one per activity
└── config.toml                       # MCP server + [[hooks]] section
```

The `.codex/config.toml` carries the MCP server registration at the top and the Locutus hooks section (DJ-136 phase 5) below a delimited marker (`# === locutus hooks (DJ-136) ===`). Re-emit strips the prior section and re-adds, so the file converges to a single block across re-runs.

### gemini

```
.gemini/
├── extensions/locutus/
│   ├── agents/locutus-<id>.md
│   ├── commands/locutus-<cli-verb>.toml
│   └── extension.json                # MCP server inside the manifest
└── settings.json                     # hooks array; user-authored hooks preserved across re-publish
```

## Headless convergence driver (DJ-140, amended by DJ-144 for Claude Code)

For headless ACP dispatch the driver now splits along runtime lines:

| | claude-code | codex · gemini |
|---|---|---|
| Driver | in-runtime dynamic workflow (DJ-144) | Locutus `OuterLoopRunner.Run` (DJ-140 unchanged) |
| Where the loop body lives | the workflow script Claude Code authors from the tier-2 keyword playbook (`<activity>.claude-code.md`, contains the word "workflow") | dispatched as the ACP prompt body (the one-iteration default `<activity>.md`) |
| Who decides re-dispatch | the workflow script itself — `dispatchUsesOuterLoop("claude-code") == false`, a single ACP dispatch covers the entire convergence | Locutus's outer-loop Go code, after every ACP session closes |
| Termination predicate | scout `converged: true` read by the script, OR `{{max_iterations}}` injected into the playbook body | `IsConverged(finalText)` over the verdict line, OR the activity's `max_iterations` cap |
| Iteration cadence | in-session (fan-out + barrier, up to ~16 concurrent subagents) | per ACP-session — coarse-grained |

DJ-144 reinstated the `runtime == "claude-code"` single-dispatch path that DJ-140 had removed — but as "the workflow loops in-runtime", not the old "/goal evaluator loops." The cap travels as a `{{max_iterations}}` token in the playbook body, substituted from the activity registry (DJ-138) at dispatch by `cmd/activity_verb.go`'s `injectMaxIterations`.

The interactive path matches: Claude Code interactive receives the same tier-2 `<activity>.claude-code.md` playbook as its `/locutus-<verb>.md` slash-command body. The "workflow" keyword in the body activates a dynamic workflow in Claude Code v2.1.154+ — both modes converge in-runtime via the same playbook. A pinned saved-workflow `.js` script (`.claude/workflows/<activity>.js`) is the deterministic interactive end-state per DJ-144 §5; it is deferred (Phase 3.2/3.4 of the implementation plan) pending operator-side verification of the saved-workflow file format. Codex/Gemini interactive stay on the DJ-142 `spec_loop_*` self-loop tier-3 playbook. There is no `/goal` wrapper anywhere now; it was removed under DJ-144 §7. One-shot activities like `justify` are unaffected — they aren't in the four convergent activities and dispatch as before.

## Per-runtime minimum-version registry (DJ-144 §9)

Dynamic workflows require Claude Code v2.1.154+; older or workflow-disabled versions silently no-op the keyword trigger and run as a single non-looping pass. To catch this Locutus ships an embedded `internal/runtimepolicy/runtimes-default.yaml` with a per-runtime `min_version`, overridable at `.borg/runtimes.yaml`:

```yaml
runtimes:
  claude-code:
    min_version: "2.1.154"
  codex:
    min_version: ""
  gemini:
    min_version: ""
```

The runtime version is read from `ClientInfo.version` at MCP `initialize` (the same `Implementation` struct DJ-143 reads `ClientInfo.name` from for runtime identification, so detection covers both modes with no `--version` subprocess probe). When the detected version is below the floor Locutus logs a `slog.Warn` naming the runtime, detected version, and required floor — and dispatches anyway. The check **never blocks**: a sub-floor Claude Code still completes the activity as a single non-looping pass; an unparseable or absent version falls open silently (no false-positive warnings). Empty `min_version` strings declare "no floor for this runtime"; nothing warns. The registry has room to grow per-feature if a future runtime-gated affordance lands.

## Interactive convergence driver for Codex / Gemini (DJ-142: tier-3 self-loop)

Codex and Gemini have no native goal-loop, so DJ-136 left interactive `/locutus-refine` on those runtimes running **one-shot** (it fell through to the one-iteration default `spec_refinement.md`). DJ-142 fills DJ-140's empty tier-3 `<activity>.<mode>.md` slot with `internal/scaffold/plans/spec_refinement.interactive.md` — a self-looping playbook the coding agent drives **itself, in one session**, so the convergence *outcome* matches the other drivers (converge-or-cap) while the *driver* stays idiomatic. As of DJ-144 the matrix has four cells, not three:

| Context | Driver | Convergence judgment | Cap enforcement |
|---|---|---|---|
| Headless · Codex / Gemini | Locutus `OuterLoopRunner` (re-dispatch per iteration) | scout verdict line | harness counter |
| Interactive · Codex / Gemini | the coding agent itself, looping in one session | scout verdict reported to `spec_advance_iteration` | daemon counter via loop-state tools |
| Claude Code (both modes, DJ-144) | in-runtime dynamic workflow script (the keyword playbook's "workflow" string activates it) | scout verdict read by the script | script counter against the `{{max_iterations}}` token injected at dispatch |

The self-loop is backed by three daemon-side MCP loop-state tools (full reference in [docs/mcp.md](mcp.md)):

- `spec_loop_begin {activity, target}` → `{iteration, max_iterations}` — called once before the first pass; allocates a fresh record or recovers the live one.
- `spec_loop_status {activity, target}` → `{iteration, max_iterations, converged, last_verdict}` — read-only inspection.
- `spec_advance_iteration {activity, target, converged, reason?}` → `{continue, iteration, reason}` — called at the end of each pass; records the scout's verdict, increments the counter, and returns `continue: false` when `converged == true` OR `iteration >= max_iterations`.

**Division of labor mirrors the harness exactly:** the iteration count + cap are server-tracked and deterministic (no agent self-counting); the convergence *judgment* is the scout's LLM verdict, the same thing the harness reads from the verdict line headlessly. The cap is sourced from the activity registry (DJ-138), so `.borg/agents.yaml` overrides apply uniformly across all three drivers.

**Run scoping + compression recovery.** Loop records are keyed server-side by `(ServerSession, activity, target)`. The agent supplies only `(activity, target)` — both re-derivable from its run context (the playbook + the `Target:` line), so nothing opaque has to survive a context compression: recovery is simply re-calling `spec_loop_begin`, which returns the live record's current iteration. The `ServerSession` half is server-side only (read from `req.Session`, one per socket connection); because the per-project daemon is shared across multiple `locutus mcp` bridges (DJ-135), it keeps two concurrent coding-agent sessions running the same `(activity, target)` from clobbering each other's counters. Terminal records (converge/cap) are deleted immediately, so a fresh run after a converged one starts at iteration 0. Claude Code never reaches tier-3 (under DJ-144 the tier-2 `<activity>.claude-code.md` workflow playbook wins; DJ-143 also denies `spec_loop_*` to Claude Code at call time as a structural backstop); headless never reaches the `.interactive` tiers (`mode=headless` skips them).

## Playbook resolution and mode (DJ-140)

`scaffold.ResolvePlaybook(base, dir, activity, runtime, mode)` selects the prompt body. Resolution walks four tiers, specificity-descending, provider outranking mode at equal specificity:

1. `<activity>.<runtime>.<mode>.md`
2. `<activity>.<runtime>.md`
3. `<activity>.<mode>.md`
4. `<activity>.md`

`mode` is `interactive` or `headless`. It is determined by the consuming operation, never sniffed:

- **Dispatch** (`runActivityVerb` → ACP) resolves with `mode=headless`. The `.interactive.md` tiers are skipped, so resolution behaves like the old two-tier provider-overlay→default fallback.
- **Publish** (the publisher emitting `.claude/commands/`, `.codex/commands/`, `.gemini/…`) resolves with `mode=interactive`, because published commands are invoked interactively by the operator.

File presence is the capability matrix. After DJ-144 deleted `spec_refinement.claude-code.interactive.md` (the old `/goal` wrapper), the populated tiers are: tier-2 `<activity>.claude-code.md` (DJ-144's workflow-shaped keyword playbook for all four convergent activities) and tier-3 `spec_refinement.interactive.md` (the DJ-142 Codex/Gemini self-loop). So Claude Code resolves to tier-2 in **both** modes (provider outranks the generic `.interactive` tier at equal specificity, and Claude Code is off the harness loop per DJ-144 §6); Codex/Gemini interactive resolves to the tier-3 self-loop unchanged; and Codex/Gemini headless skips the mode tiers and falls through to the one-iteration default `<activity>.md`.

## Tool-Restriction (DJ-143)

The Locutus MCP daemon registers tools globally per `Server.AddTool`, but the MCP go-sdk does not expose per-session `tools/list` filtering. Some tools — notably the DJ-142 `spec_loop_*` family — only make sense for specific runtimes. DJ-143 enforces those scoping rules at **call time** via a `requireRuntime` wrapper at each restricted tool's registration site, paired with a **list-time** signal in each tool's `Description`.

Mechanism:

- Each session captures `(runtime, mode)` at MCP `initialize`: `clientInfo.name` → runtime; `_meta["locutus.mode"]` → mode.
- The mode field is set by the `locutus mcp` bridge from `LOCUTUS_MODE` env (default `interactive`); the ACP harness sets `LOCUTUS_MODE=headless` when it spawns the coding-agent runtime for dispatch, so the bridge inherits it through the coding-agent process.
- Restricted tools are wrapped with `requireRuntimeAny(handler, "codex", "gemini")` at registration. A call from a denied runtime returns an MCP tool error of the shape:

  > `tool "spec_loop_begin" is not exposed to runtime "claude-code" (mode=interactive); allowed runtimes: codex, gemini; see docs/runtime-affordances.md § Tool-Restriction`

- Each restricted tool's `Description` starts with a leading sentence naming the runtime audience and contrasting against the wrong audience, so the agent reading the tool list at initialize time has a textual signal — paired with the call-time enforcement, the description discourages calls before they're attempted.

Operator note: there is **no hot-reload**. The runtime allowlist for a tool lives in code at the registration site; changes require a binary rebuild + daemon restart (`locutus mcp-stop` followed by the next connect re-forking the daemon).

## Dry-Run (DJ-147)

The four mutating verbs (`import`, `refine`, `adopt`, `assimilate`) accept `--dry-run` — the workflow runs end-to-end against a per-session overlay on the SpecStore, captures every would-be `spec_propose_*` / `spec_revise_*` / `spec_delete_*` / `spec_mark_approach_drifted` / `spec_update_goals_md_hash` call, and discards the overlay at session close. Nothing reaches `.borg/spec/`, the Bluge index, history, or `spec://manifest` notifications.

Activation mirrors DJ-143's mode plumbing: CLI flag → `LOCUTUS_DRY_RUN=1` env (plus `LOCUTUS_DRY_RUN_FORMAT=markdown|json`) on the spawned coding-agent → bridge reads + forwards as `_meta["locutus.dry_run"]` + `_meta["locutus.dry_run_format"]` on its `initialize` → daemon's session-context map stores them and registers an overlay on the SpecStore.

The agent retrieves the capture via the read-only `spec_dry_run_report` MCP tool (no input, returns `{format, captured: [...]}`) and renders it in its closing message — verbatim fenced JSON for `--format json`, prose summary for the default `markdown`. In headless dispatch the CLI additionally reads the session's `tools.jsonl` post-dispatch and emits an authoritative structured render — the agent's narration is convenience; the CLI render is contract.

## Assimilate (DJ-148)

`locutus assimilate` reads brownfield source code, infers/revises features+decisions+strategies (code-is-truth direction), and synthesizes approaches binding inferred specs to source files with `source_hash`. Producing coherent (spec, code, approach) state is the verb's outcome per [DJ-148](decisions/dj-148-assimilate-bidirectional-reconciliation.md).

**Preconditions** (refused with a helpful error if missing): `GOALS.md` exists; goal layer is populated (operator runs `locutus refine goals` first). The operator's brownfield bootstrap workflow is `locutus init → edit GOALS.md → locutus refine goals → locutus assimilate → locutus adopt`, with each verb having one clear purpose.

The activity uses the same per-runtime convergence-driver pattern as `spec_refinement`: Claude Code drives the analyzer fan-out as a dynamic workflow (`code_assimilation.claude-code.md`); Codex/Gemini interactive self-loops via `spec_loop_*` (`code_assimilation.interactive.md`); Codex/Gemini headless uses the `OuterLoopRunner` (`code_assimilation.md` default fallback). `max_iterations` defaults to 3 per the registry (lower than `spec_refinement`'s 20 because assimilate is single-pass-shaped).

DJ-147 dry-run inherits automatically — the two new MCP tools (`spec_propose_approach`, `spec_revise_approach`) are wrapped via the same `captureOnly` registration-site adapter as the other 14 mutation tools.

## Cross-references

- [DJ-135](DECISION_JOURNAL.md#dj-135) — multi-runtime pivot; introduces the ACP / MCP architecture.
- [DJ-136](DECISION_JOURNAL.md#dj-136) — per-runtime idiomatic dispatch; introduces overlays, hook publishing, asymmetric convergence.
- [DJ-140](DECISION_JOURNAL.md#dj-140) — unifies headless convergence on the Locutus outer loop for all runtimes; adds the `mode` axis to `ResolvePlaybook`; relocates the `/goal` wrapper to an interactive-only variant.
- [DJ-142](DECISION_JOURNAL.md#dj-142) — fills DJ-140's empty tier-3 with the interactive self-loop for Codex/Gemini; adds the daemon-side loop-state tools (`spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration`) keyed `(ServerSession, activity, target)`.
- [`docs/council.md`](council.md) — playbook iteration shape; depicts the harness split.
- [`docs/agent-conventions.md`](agent-conventions.md) — playbook and overlay authoring conventions.
- [`internal/scaffold/plans/`](../internal/scaffold/plans/) — canonical playbooks + per-runtime overlays.
- [`internal/scaffold/hooks/`](../internal/scaffold/hooks/) — per-runtime hook fragments.
- [`internal/publisher/`](../internal/publisher/) — per-runtime emit logic.
