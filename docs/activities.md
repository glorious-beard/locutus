# Activities and the publisher

This doc covers the activity registry (`internal/activity/`), the per-runtime publisher (`internal/publisher/`), and the publishing lifecycle that ties them together. Both pieces landed in DJ-135 phases 3 and 4.

## Activity model

An **activity** is a named unit of coding work. Each one declares an ordered preference list of coding-agent runtimes capable of executing it. At dispatch time the resolver walks the preference list and returns the first runtime whose ACP-server binary is detectable on `$PATH`.

The six activities Locutus ships today map to the CLI verbs that dispatch them. Most are 1:1; the `refine` verb dispatches one of two activities depending on whether `--with` is present.

| Activity | CLI verb | Playbook source |
|---|---|---|
| `spec_refinement` | `locutus refine [<id>]` (no `--with`) | `.borg/plans/spec_refinement.md` |
| `spec_bias` | `locutus refine <id> --with "<bias>"` | `.borg/plans/spec_bias.md` |
| `feature_ingestion` | `locutus import` | `.borg/plans/feature_ingestion.md` |
| `code_adoption` | `locutus adopt` | `.borg/plans/code_adoption.md` |
| `code_assimilation` | `locutus assimilate` | `.borg/plans/code_assimilation.md` |
| `justification` | `locutus justify` | `.borg/plans/justification.md` |

Adding a new activity is three files: a registry entry in `agents.yaml`, a playbook in `internal/scaffold/plans/<activity>.md`, and (optionally) a CLI verb that calls `runActivityVerb(ctx, cli, activityName, contextNote)`.

### The `justification` activity (DJ-137)

`locutus justify <id> [--against "..."] [--format markdown|json]` dispatches `justification` — a one-shot, read-only activity that produces a structured defense of a named spec node. The playbook fetches the target via `mcp__locutus__spec_get`, performs a batched dependency-graph context fetch (full struct content: `rationale` + `alternatives`), optionally dispatches `justify-researcher` for grounded fact-checking and `spec-challenger` for adversarial dialogue (when `--against` is set), then dispatches `spec-advocate` to produce the defense.

Output formats:

- **`--format markdown`** (default) — human-readable defense with optional `## Concerns raised` and `## Response to concerns` sections when adversarial dialogue ran.
- **`--format json`** — structured envelope per the schema in [DJ-137](DECISION_JOURNAL.md#dj-137). The `context.influenced_by[]` array carries the full upstream-decision structs (`rationale` + `alternatives` slice with `name` / `summary` / `rejection_reason` / `citations`) so downstream tooling can spot-check whether the advocate engaged with the considered alternatives.

The activity is **read-only** — no `spec_propose_*` calls, no hooks, no `/goal` outer loop. The orchestrator runs to completion and the verb returns. Missing-id errors surface as a structured error envelope in the requested format (so a JSON-piping consumer doesn't trip on a bare error message).

See [docs/council.md](council.md#the-justify-sub-council-dj-137) for the per-agent reference and the dialogue-flow diagram.

### The goal-layer workflow on `spec_refinement` (DJ-139)

`locutus refine` (without `--with`, dispatching `spec_refinement`) runs three steps in order — **sync → iterate → cite** — wrapping the existing deliberation iteration with goal-layer sync at the front and a citation walk at the back. The reorganization persists the LLM's interpretation of `GOALS.md` as first-class `goal-*` / `agoal-*` nodes rather than re-deriving it every run from prose.

- **Step 0 — Goal-layer sync.** The orchestrator reads `GOALS.md`, computes `sha256:<hex>` over its bytes, and compares against the manifest's `goals_md_hash` field. On a hash match, the rest of Step 0 is skipped — the persisted goal layer is up to date. On a hash mismatch (or an absent hash on first run), the orchestrator dispatches the **`spec-goal-diff-matcher`** subagent with the current `GOALS.md` text and the array of existing `goal-*` / `agoal-*` node bodies. The matcher returns a structured diff (`unchanged` / `modified` / `deleted` / `added`) anchored against each node's `source_clause` field; the orchestrator applies the diff via `spec_propose_goal` / `spec_revise_goal` / `spec_delete_goal` and the AntiGoal variants, then persists the new hash via `spec_update_goals_md_hash`. A one-time bootstrap affordance fires on the very first run for a project where no `goal-*` / `agoal-*` nodes exist yet — the matcher's input gains a directive to treat existing scope-encoding decisions (e.g. `dec-product-scope-boundary`) as secondary claim sources alongside `GOALS.md`. Subsequent runs drop the affordance and read only `GOALS.md`.
- **The iteration.** The existing seven-step deliberation runs as before (survey, decide open axes, elaborate new nodes, critique, reconcile, cascade revisions, confirm landings). The goal layer is implicit context the manifest carries through; the subagents (spec-scout, spec-decision-elaborator, etc.) read `goal-*` / `agoal-*` ids alongside the rest of the graph.
- **Step N+1 — Citation walk.** For every node touched this iteration AND every node whose existing `.advances` / `.respects` arrays reference a `goal-*` / `agoal-*` id that changed in Step 0, the orchestrator judges citation arrays against the final goal-layer state and commits updates via `spec_revise_*` with only the citation fields populated. The walk lands **last** so it operates against the settled state rather than intermediate node bodies (walking citations before the iteration would target node states that get rewritten — the misapplied-citation failure mode). Inline orchestrator judgment, no subagent dispatch.

The convergence verdict at the end of the iteration reflects all three steps: `converged: true` only when the scout reported convergence AND the citation walk produced no further updates. The "Features without goal anchors" section in the report surfaces features whose final `.advances` is empty — operator-facing at-risk signal, mirrored in `locutus status --full`.

The `feature_ingestion` playbook (dispatched by `locutus import`) also reads the goal layer under DJ-139: conflict detection becomes a structural graph test against `agoal-*` bodies + `kept_in` arrays rather than LLM-judging `GOALS.md` prose. When a feature plausibly fits under an extended or new carve-out, the playbook drafts a unified diff against `GOALS.md` for operator review; on admission the playbook populates the new feature's `.advances` / `.respects` via the propose tool's new optional citation fields. See [DJ-139](decisions/dj-139-goal-layer-node-kinds.md) for the full design.

### The `spec_bias` activity (DJ-138)

`locutus refine <id> --with "<bias>"` dispatches `spec_bias` — a write-cascade activity that applies a strong-bias natural-language instruction to a Decision / Feature / Strategy target and propagates the implications through the spec graph. The playbook reads the target via `mcp__locutus__spec_get`, walks the reference-graph closure (forward from a Decision target; backward then forward from a Feature/Strategy target), applies mutations via the existing `spec_revise_*` / `spec_propose_*` MCP tools, then marks every affected approach drifted via `mcp__locutus__spec_mark_approach_drifted`. Convergence is *full closure walk produces zero new mutations* — bounded by the activity's `max_iterations` cap (default 20).

The activity is a **strong-bias cascade**: the operator opted in by typing `--with` and the playbook is written to obey aggressively (add-and-promote a new option without a confirmation gate, add proactive citations to features that should cite a flipped decision but don't yet, create new decisions when the bias implies an axis with no decision). Mid-cascade failures leave the graph in a partially-updated state — recovery is idempotent re-run (the playbook detects nodes whose state already reflects the bias and skips them) or `git reset --hard` over `.borg/spec/`. There is no transactional spec rollback.

The cascade records a `spec_biased` root event at dispatch time and links every downstream `spec_revised` / `spec_proposed` / `approach_drifted` event back to the root via `caused_by`. `locutus status --full` surfaces the 10 most-recent cascades in a "Recent biases" section; `locutus history --since <bias-event-id>` walks the cascade subtree chronologically.

Target validation tightens here vs. plain `refine`: Approach (`app-`) and Bug (`bug-`) ids are rejected as refine targets entirely (in both `--with` and plain modes — DJ-138 resolved-question 6). Goal (`goals`) is additionally rejected under `--with` because cascading from the root would over-blast the graph. See [DJ-138](decisions/dj-138-refine-with-bias-cascade.md) for the full design and the alternatives considered.

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
    max_iterations: 20  # optional; defaults to 20 (DJ-138 phase 1)
```

Runtime ids must match keys in `internal/dispatch/acp/registry.go` AgentSpawns — currently `claude-code`, `codex`, `gemini`. Adding a new runtime means landing a Spawn entry there first; agents.yaml entries pointing at unknown runtimes are rejected at registry-load time, not at first-dispatch time.

`max_iterations` is the per-activity ceiling for the outer-loop runner — moved out of the hardcoded `const maxIterations = 20` in `internal/runner/run.go` and into the registry per [DJ-138](decisions/dj-138-refine-with-bias-cascade.md) phase 1 so projects can tune iteration budgets per activity. Omitted fields default to 20 (back-compat with hand-written `.borg/agents.yaml`).

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
