# Activities and the publisher

This doc covers the activity registry (`internal/activity/`), the per-runtime publisher (`internal/publisher/`), and the publishing lifecycle that ties them together. Both pieces landed in DJ-135 phases 3 and 4.

## Activity model

An **activity** is a named unit of coding work. Each one declares an ordered preference list of coding-agent runtimes capable of executing it. At dispatch time the resolver walks the preference list and returns the first runtime whose ACP-server binary is detectable on `$PATH`.

The four activities Locutus ships today map 1:1 to the CLI verbs that dispatch them:

| Activity | CLI verb | Playbook source |
|---|---|---|
| `spec_refinement` | `locutus refine` | `.borg/plans/spec_refinement.md` |
| `feature_ingestion` | `locutus import` | `.borg/plans/feature_ingestion.md` |
| `code_adoption` | `locutus adopt` | `.borg/plans/code_adoption.md` |
| `code_assimilation` | `locutus assimilate` | `.borg/plans/code_assimilation.md` |

Adding a new activity is three files: a registry entry in `agents.yaml`, a playbook in `internal/scaffold/plans/<activity>.md`, and (optionally) a CLI verb that calls `runActivityVerb(ctx, cli, activityName, contextNote)`.

## `agents.yaml`

The activity registry is loaded from `agents.yaml`. Locutus ships an embedded default (`internal/activity/agents-default.yaml`); per-project overrides go in `.borg/agents.yaml`.

Schema:

```yaml
activities:
  <activity_name>:
    runtimes:
      - claude-code
      - codex
      - gemini
```

Runtime ids must match keys in `internal/dispatch/acp/registry.go` AgentSpawns — currently `claude-code`, `codex`, `gemini`. Adding a new runtime means landing a Spawn entry there first; agents.yaml entries pointing at unknown runtimes are rejected at registry-load time, not at first-dispatch time.

The default ships `claude-code` first for every activity. Empirical task-tool fit data (DJ-135 §"Reference state") suggests different runtimes excel at different activities — Codex for planning-heavy work, Claude Code for spec deliberation — but those preferences land as targeted overrides in a follow-up rather than baked into the shipping default.

### Override semantics

Override is **full-replacement per activity name**, not per-runtime merge. Example:

```yaml
# .borg/agents.yaml
activities:
  spec_refinement:
    runtimes: [gemini]
```

…produces a `spec_refinement` whose runtime list is exactly `[gemini]`. The embedded default's `[claude-code, codex, gemini]` is discarded for that activity. This is intentional: override intent stays explicit. Activities the override doesn't mention keep the embedded default's list.

## Runtime detection

`activity.Registry.Resolve(activityName, resolver)` walks the activity's runtime preference list in order and returns the first runtime whose ACP-server binary is found via `exec.LookPath`. The lookup table mapping runtime id → binary lives in `acp.AgentSpawns`:

| Runtime | Binary | Install |
|---|---|---|
| `claude-code` | `claude-agent-acp` | `npm i -g @agentclientprotocol/claude-agent-acp` |
| `codex` | `codex-acp` | GitHub release tarball (NOT crates.io) |
| `gemini` | `gemini --acp` | `npm i -g @google/gemini-cli` |

If no listed runtime resolves, `Resolve` returns an error naming every checked candidate so the operator can pick one and install it.

Tests stub the lookup via `&Resolver{LookPath: func(name string) (string, error) {...}}` so CI doesn't depend on what's actually installed on the host.

## Publisher

The publisher (`internal/publisher/`) reads the canonical agent prompts at `.borg/agents/*.md` and the activity playbooks at `.borg/plans/*.md`, then emits per-runtime copies in the format each runtime expects.

Per DJ-135 resolved-question 6 the copies are **tailored, not symlinks** — different runtimes use different file formats and different frontmatter shapes, and the publisher does the translation rather than punting on it. Per resolved-question 7, namespacing uses subdirectories where the runtime supports them and filename prefixes elsewhere.

### Per-runtime layout

#### Claude Code

- Subagents → `.claude/agents/locutus/<id>.md` (subdir namespacing — Claude Code supports it).
- Slash commands → `.claude/commands/locutus-<activity>.md`.
- MCP server descriptor → `.mcp.json` at project root.

Subagent frontmatter: `name`, `description`. The body is the canonical agent prompt verbatim. Description is derived as `<id> — Locutus <role> agent` when the canonical declares a `role`, else `<id> — Locutus agent`.

#### Codex

- Subagents → `.codex/agents/locutus-<id>.toml` (flat with filename prefix — Codex doesn't support subdir namespacing for agents).
- Slash commands → `.codex/commands/locutus-<activity>.toml`.
- MCP server descriptor → `.codex/config.toml` with `[mcp_servers.locutus]`.

TOML keys: `name`, `description`, `developer_instructions`. The agent body lives under `developer_instructions` as a TOML literal multi-line string (triple-single-quote) so backslashes and quotes don't need escaping.

#### Gemini

- Extension manifest → `.gemini/extensions/locutus/extension.json` (carries the MCP servers map; loading the extension auto-attaches Locutus).
- Subagents → `.gemini/extensions/locutus/agents/locutus-<id>.md` (markdown + frontmatter).
- Slash commands → `.gemini/extensions/locutus/commands/locutus-<activity>.toml`.

**Format-fidelity caveat.** The Codex TOML schema and Gemini extension schema are subject to upstream churn; the publisher's output matches what was specified in DJ-135's plan and verified empirically for Claude Code as of phase 5 ckpt 4. Codex and Gemini formats are best-effort until each is validated against its respective runtime — adjustments land alongside the empirical validation, not pre-emptively.

## Lifecycle

```
canonical                    .borg/                       runtime-published
─────────                    ──────                       ─────────────────
internal/scaffold/agents/    .borg/agents/                .claude/agents/locutus/
internal/scaffold/plans/  →  .borg/plans/             →  .codex/agents/locutus-*
                                                          .gemini/extensions/locutus/
                                                          .mcp.json
                                                          .codex/config.toml
                                                          .gemini/.../extension.json
                          ↑                            ↑
                          locutus init / update      locutus init / update --reset
                          --reset                    (re-emission is idempotent)
```

1. **`locutus init`**: scaffolds `.borg/`, copies the embedded agents + plans, then runs the publisher to emit per-runtime files.
2. **`locutus update --reset`**: refreshes `.borg/agents/` + `.borg/plans/` from the new binary's embed, then re-runs the publisher to push updates to every runtime's published copy.
3. **Per-runtime edits don't survive**: anything under `.claude/agents/locutus/`, `.codex/agents/locutus-*`, or `.gemini/extensions/locutus/` is overwritten on every reset. Project-local agent edits go in `.borg/agents/` and survive — Reset overwrites those too, but reset is an explicit operator action; init isn't (it's `writeIfMissing`).

## Convergence-by-construction

A theme that runs through the activity playbooks and the MCP write tools both: **commit, don't defer.** When a coding agent is mid-iteration and could either commit a decision with imperfect-but-defensible content or stop and ask the human, the playbooks say "commit." When the MCP write tool could either reject a propose call for missing-but-derivable fields or auto-fill them, it auto-fills (see `axes` backfill in `spec_propose_decision`). The premise is that the legacy council's failure mode was deferral — critics re-raising the same concerns across iterations, decisions stalling waiting for the human — and that the new path beats it by committing more aggressively and letting future revisions fix what needs fixing.

This shows up most explicitly in `internal/scaffold/plans/spec_refinement.md` under the heading "Convergence by construction." Playbook authors for new activities should preserve this discipline.
