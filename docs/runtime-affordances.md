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
| Interactive goal-driven loop | ✓ `/goal` slash command (interactive sessions only) | — | — |
| Headless convergence loop | Locutus `OuterLoopRunner` (DJ-140) | Locutus `OuterLoopRunner` | Locutus `OuterLoopRunner` |
| PreToolUse hook | ✓ `.claude/settings.json` hooks | ✓ `.codex/config.toml` `[[hooks]]` | ✓ `.gemini/settings.json` `BeforeTool` |
| PostToolUse hook | ✓ | ✓ | ✓ `AfterTool` |
| MCP server attachment | `.mcp.json` | `[mcp_servers.locutus]` in config.toml | extension manifest |
| Slash commands | `.claude/commands/*.md` | `.codex/commands/*.toml` | `.gemini/extensions/*/commands/*.toml` |
| Subagent dispatch | Task tool | analogous | analogous |
| Subdirectory namespacing | ✓ (`.claude/agents/locutus/`) | — (filename prefix) | — (extension scope + filename prefix) |

DJ-136 originally made the `/goal` row a load-bearing asymmetry — Claude Code's evaluator-after-each-turn loop drove headless convergence while Codex/Gemini relied on Locutus's runner. DJ-140 retired that asymmetry for headless dispatch: `/goal` is an interactive-session-scoped Claude Code built-in and is unavailable in the headless `claude-agent-acp` dispatch path (verified 2026-05-27 — same v2.1.150 binary, run-mode-gated). Headless convergence is now the Locutus `OuterLoopRunner` for all three runtimes. `/goal` survives only as an interactive affordance: the published `/locutus-refine` slash command is a `/goal` wrapper that operators invoke from inside a Claude Code TUI session, where `/goal` IS available.

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

The interactive overlay `internal/scaffold/plans/spec_refinement.claude-code.interactive.md` — a thin `/goal` wrapper that invokes `/locutus-refine` each iteration — is published as `.claude/commands/locutus-refine.md` for interactive operators. It is NOT dispatched headlessly (DJ-140): headless `spec_refinement` resolves to the one-iteration default `spec_refinement.md` and runs under the Locutus outer loop.

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

## Headless convergence driver (DJ-140: unified)

For headless ACP dispatch — every CLI verb on every runtime — the driver is uniform:

| Driver | claude-code · codex · gemini |
|---|---|
| Where the loop body lives | dispatched as the ACP prompt body (the one-iteration default `spec_refinement.md`) |
| Who decides re-dispatch | Locutus's `OuterLoopRunner.Run` (Go loop, after every ACP session closes) |
| Termination predicate | `IsConverged(finalText)` returns true, OR the activity's `max_iterations` cap (default 20) |
| Iteration cadence | Per ACP-session — coarse-grained |

DJ-140 removed the `runtime == "claude-code"` single-dispatch branch in `internal/runner/run.go`, so all three runtimes run through `runOuterLoopDispatch`. One-shot activities (e.g. `justify`) emit `converged: true` on iteration 1 and exit after a single session — identical cost to the old single-dispatch path.

The interactive path is separate. When an operator invokes `/locutus-refine` inside a Claude Code TUI session, the published `/goal` wrapper drives convergence via Claude Code's native evaluator (model-evaluated, after every turn). That path is operator-driven; Locutus only publishes the command and serves MCP. `/goal` is unavailable headlessly, which is why headless dispatch never depends on it.

## Playbook resolution and mode (DJ-140)

`scaffold.ResolvePlaybook(base, dir, activity, runtime, mode)` selects the prompt body. Resolution walks four tiers, specificity-descending, provider outranking mode at equal specificity:

1. `<activity>.<runtime>.<mode>.md`
2. `<activity>.<runtime>.md`
3. `<activity>.<mode>.md`
4. `<activity>.md`

`mode` is `interactive` or `headless`. It is determined by the consuming operation, never sniffed:

- **Dispatch** (`runActivityVerb` → ACP) resolves with `mode=headless`. The `.interactive.md` tiers are skipped, so resolution behaves like the old two-tier provider-overlay→default fallback.
- **Publish** (the publisher emitting `.claude/commands/`, `.codex/commands/`, `.gemini/…`) resolves with `mode=interactive`, because published commands are invoked interactively by the operator.

File presence is the capability matrix. Today only `spec_refinement.claude-code.interactive.md` populates an `.interactive` tier — so Claude Code publishing resolves to the `/goal` wrapper, while Codex/Gemini publishing and all headless dispatch fall through to the one-iteration default `spec_refinement.md`.

## Cross-references

- [DJ-135](DECISION_JOURNAL.md#dj-135) — multi-runtime pivot; introduces the ACP / MCP architecture.
- [DJ-136](DECISION_JOURNAL.md#dj-136) — per-runtime idiomatic dispatch; introduces overlays, hook publishing, asymmetric convergence.
- [DJ-140](DECISION_JOURNAL.md#dj-140) — unifies headless convergence on the Locutus outer loop for all runtimes; adds the `mode` axis to `ResolvePlaybook`; relocates the `/goal` wrapper to an interactive-only variant.
- [`docs/council.md`](council.md) — playbook iteration shape; depicts the harness split.
- [`docs/agent-conventions.md`](agent-conventions.md) — playbook and overlay authoring conventions.
- [`internal/scaffold/plans/`](../internal/scaffold/plans/) — canonical playbooks + per-runtime overlays.
- [`internal/scaffold/hooks/`](../internal/scaffold/hooks/) — per-runtime hook fragments.
- [`internal/publisher/`](../internal/publisher/) — per-runtime emit logic.
