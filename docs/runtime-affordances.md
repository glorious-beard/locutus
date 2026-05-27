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
| Goal-driven outer loop | ✓ `/goal` slash command | — (Locutus drives the loop) | — (Locutus drives the loop) |
| PreToolUse hook | ✓ `.claude/settings.json` hooks | ✓ `.codex/config.toml` `[[hooks]]` | ✓ `.gemini/settings.json` `BeforeTool` |
| PostToolUse hook | ✓ | ✓ | ✓ `AfterTool` |
| MCP server attachment | `.mcp.json` | `[mcp_servers.locutus]` in config.toml | extension manifest |
| Slash commands | `.claude/commands/*.md` | `.codex/commands/*.toml` | `.gemini/extensions/*/commands/*.toml` |
| Subagent dispatch | Task tool | analogous | analogous |
| Subdirectory namespacing | ✓ (`.claude/agents/locutus/`) | — (filename prefix) | — (extension scope + filename prefix) |

The `/goal` row is the asymmetry that drives DJ-136's per-runtime split. Claude Code's evaluator-after-each-turn loop is well-suited to the convergence-judgement-by-scout pattern; Codex and Gemini's hook surfaces are equivalent for the mechanical-enforcement use case but lack a session-wide goal evaluator.

## Per-runtime emitted files

Run `locutus init` or `locutus update --reset` to (re-)emit all of these from the canonical scaffold.

### claude-code

```
.claude/
├── agents/locutus/<id>.md            # one per agent
├── commands/locutus-<cli-verb>.md    # one per activity (refine, import, adopt, assimilate)
└── settings.json                     # currently unused for hooks (DJ-136 uses /goal)
.mcp.json                             # locutus MCP server attachment
```

The `<cli-verb>` matches the CLI verb that dispatches the activity (`refine` ↔ `spec_refinement`, `import` ↔ `feature_ingestion`, `adopt` ↔ `code_adoption`, `assimilate` ↔ `code_assimilation`). Operators recognize `/locutus-refine` more readily than `/locutus-spec-refinement`.

The `.claude-code.md` overlay variant — `internal/scaffold/plans/spec_refinement.claude-code.md` — is dispatched as the prompt body for spec_refinement; it's a thin `/goal` wrapper that invokes `/locutus-refine` each iteration.

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

## Per-runtime convergence drivers

| Driver | claude-code | codex / gemini |
|---|---|---|
| Where the loop body lives | `.claude/commands/locutus-refine.md` (the cross-runtime one-iteration playbook) | dispatched as the ACP prompt body |
| Who decides re-dispatch | Claude Code's `/goal` evaluator (model-evaluated, after every turn) | Locutus's `OuterLoopRunner.Run` (Go loop, after every ACP session closes) |
| Termination predicate | Final line begins with `converged: true`, OR 20 iterations | `IsConverged(finalText)` returns true, OR 20 iterations |
| Iteration cadence | Evaluator runs after every turn — fine-grained | Per ACP-session — coarse-grained |

The trade-off: the evaluator is faster to react (turn-level) but Claude Code only; the runner is slower (session-level) but works on any ACP-speaking runtime. Both terminate cleanly at 20 iterations.

## Cross-references

- [DJ-135](DECISION_JOURNAL.md#dj-135) — multi-runtime pivot; introduces the ACP / MCP architecture.
- [DJ-136](DECISION_JOURNAL.md#dj-136) — per-runtime idiomatic dispatch; introduces overlays, hook publishing, asymmetric convergence.
- [`docs/council.md`](council.md) — playbook iteration shape; depicts the harness split.
- [`docs/agent-conventions.md`](agent-conventions.md) — playbook and overlay authoring conventions.
- [`internal/scaffold/plans/`](../internal/scaffold/plans/) — canonical playbooks + per-runtime overlays.
- [`internal/scaffold/hooks/`](../internal/scaffold/hooks/) — per-runtime hook fragments.
- [`internal/publisher/`](../internal/publisher/) — per-runtime emit logic.
