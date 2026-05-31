# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## General Guidelines

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

### 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

### 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

### 3. Solve the root problem

**If a better design is needed to better solve the problem at hand, especially as that problem evolves over time, surface that new design so that we can decide whether to adopt it or not.**

The test: Every changed line should trace directly to the user's request.

### 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

## Project

Locutus — a Go CLI and MCP server that acts as an autonomous project manager for spec-driven software. It maintains a persistent spec graph (`Decision → (Feature | Strategy) → Approach` — decisions inform features and strategies; approaches are the synthesis layer for coding agents over features and strategies. Axes are surfaced from goals, features, and strategies and resolved by decisions; see DJ-124. Goals and anti-goals form a parallel **leaf layer** under `GOALS.md` — `goal-*` and `agoal-*` are persisted LLM interpretations of `GOALS.md` per DJ-139 with polarity encoded in the node kind, and the existing cascade graph optionally carries informational `.advances goal-*` / `.respects agoal-*` dotted-line citations to them. The goal layer is not structurally upstream of decisions; nothing in the cascade depends on it, the citations are populated by the orchestrator when a clear link is worth recording, and `refine goals` writes through the goal layer without cascading content through the rest of the graph), exposes that graph to coding agents (Claude Code, Codex, Gemini CLI) via MCP tools and resources, and dispatches activity-shaped work via ACP. The spec is the source of truth; artifacts are derived outputs.

Locutus itself does not make LLM calls. The coding-agent runtime — Claude Code via `claude-agent-acp`, Codex via `codex-acp`, Gemini CLI via `gemini --acp` — owns model selection and the conversation; Locutus exposes the spec graph and the activity playbooks, then watches the agent execute.

## Sources of Truth

- **Multi-runtime via MCP + ACP (DJ-135).** Every CLI verb (`refine`, `import`, `adopt`, `assimilate`) dispatches the matching activity playbook to a coding-agent runtime via ACP; the runtime calls back into the per-project Locutus MCP daemon for spec graph reads and writes. The daemon is a singleton bound to a Unix socket at `.locutus/mcp.sock` so multiple coding agents attached to the same project share one in-process `SpecStore` and one notification fanout. The bridge command `locutus mcp` makes that singleton look like a stdio MCP server to clients that only know stdio. See `docs/mcp.md` and `docs/activities.md`.
- **Decision IDs are axis-shaped (DJ-133).** Every decision's `id` equals `dec-` followed by the primary axis ID it answers verbatim (axis `oltp-store` → `dec-oltp-store`). The id names the *question*; `title`, `chosen_option`, and `rationale` carry the human-readable *answer*. A revision that flips the chosen option keeps the same id — backreferences from features, strategies, and approaches stay byte-stable across flips. The MCP write tool `spec_propose_decision` backfills `axes` from the id when the agent omits it (per DJ-135 phase 5 convergence-by-construction discipline).
- **The in-process `SpecStore` is the source of truth for spec graph reads and writes during a daemon session (DJ-134).** `internal/agent/spec_store.go` holds typed entries tagged by origin (settled / proposed) and a working flag under one RWMutex, with a write-through Bluge search index. The MCP server's read tools (`spec_list_manifest`, `spec_get`, `spec_search`), write tools (`spec_propose_decision`, `spec_propose_feature`, `spec_propose_strategy`, `spec_revise_decision`), and the `spec://manifest` resource all read and write through it. `.borg/spec/` is its persistence backing, not a parallel data source. `spec_get` is batched by design — input is `{ids: [string]}`, output is `{results: {id: {status, body?, working?, reason?}}, available_ids?, working?}` with status in `settled | in_flight | missing`. There is no scalar-id variant; batching is structural.
- **Tool descriptions live in registration, not prompts (DJ-134).** When an activity playbook references a tool (the `mcp__locutus__spec_*` set), the playbook's job is workflow guidance — when to reach for the tool and why, in the context of the activity. Tool-behavior text (input shape, kind-prefix routing, in-flight-vs-settled semantics) lives in the tool's registered `Description` in `internal/mcp/tools_spec_{read,write}.go`. See `docs/agent-conventions.md` § "Tool descriptions live in registration, not prompts."
- **Per-runtime publishing is one-way (DJ-135).** The canonical agent prompts at `internal/scaffold/agents/*.md` and activity playbooks at `internal/scaffold/plans/*.md` are the source of truth. The publisher (`internal/publisher/`) reads them on `locutus init` and `locutus update --reset` and emits per-runtime copies (`.claude/agents/locutus/`, `.codex/agents/locutus-*.toml`, `.gemini/extensions/locutus/`) plus the runtime's MCP-server descriptor pointing at `locutus mcp`. Edits to the published copies are overwritten on the next reset; project-local agent edits go in `.borg/agents/`.
- **Per-runtime playbook overlays + hook publishing (DJ-136, mode axis DJ-140).** Playbook resolution is `<activity>[.<provider>][.<mode>].md` — `scaffold.ResolvePlaybook(base, dir, activity, runtime, mode)` walks four tiers specificity-descending: `<activity>.<runtime>.<mode>.md` → `<activity>.<runtime>.md` → `<activity>.<mode>.md` → `<activity>.md` (provider outranks mode at equal specificity). `mode` is `interactive` or `headless` and is passed by the consuming operation, never sniffed: dispatch passes `headless`, the publisher passes `interactive`. Today the only overlay is `spec_refinement.claude-code.interactive.md` — a thin `/goal`-directive wrapper that delegates to the published `/locutus-refine` slash command; it resolves ONLY for interactive publishing (per DJ-140), so headless dispatch falls through to the one-iteration default `spec_refinement.md`. Per-runtime hook fragments live at `internal/scaffold/hooks/<runtime>/<activity>.<ext>` and land at the runtime's conventional config file (Codex: `.codex/config.toml` `[[hooks]]`; Gemini: `.gemini/settings.json` hooks array). See [docs/runtime-affordances.md](docs/runtime-affordances.md).
- **Unified headless convergence (DJ-140, amends DJ-136).** The activity playbook is **one-iteration-shaped**; the outer loop is harness-owned for all three runtimes. Every headless ACP dispatch — Claude Code, Codex, Gemini — runs through Locutus's `OuterLoopRunner` in `internal/runner/loop.go` (the `runtime == "claude-code"` single-dispatch branch in `internal/runner/run.go` was removed). The runner reads the playbook's plain-text verdict line (`converged: true` or `converged: false; <reason>`) after each ACP session closes to decide whether to re-dispatch, bounded by each activity's configured `max_iterations` cap (default 20, per-activity tunable in `agents-default.yaml` / `.borg/agents.yaml` per DJ-138 phase 1); one-shot activities (e.g. `justify`) self-terminate by emitting `converged: true` on iteration 1. DJ-136's asymmetric design relied on Claude Code's `/goal` evaluator to drive the loop, but `/goal` is an interactive-session-scoped Claude Code built-in unavailable in the headless `claude-agent-acp` dispatch path; under DJ-140 it survives only as an interactive affordance — the published Claude Code `/locutus-refine` slash command is the `/goal` wrapper, invoked by operators inside an interactive session where `/goal` IS available.
- **Four idiomatic convergence drivers, one outcome (DJ-144 amends DJ-142, which completed DJ-140).** Every context reaches the same outcome — converge (scout reports `converged: true`) or stop at `max_iterations` — but the driver is idiomatic per context: **headless · Codex / Gemini** → the harness `OuterLoopRunner` re-dispatches each iteration and reads the verdict line; **interactive · Codex / Gemini** → the coding agent self-loops in one session via the daemon-side loop-state tools `spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration` (the tier-3 playbook `spec_refinement.interactive.md`); **Claude Code (both modes)** → an in-runtime dynamic workflow drives the loop and parallel fan-out for the four convergent activities (`spec_refinement`, `feature_ingestion`, `code_adoption`, `code_assimilation`), so `dispatchUsesOuterLoop("claude-code")` is **false** and the `max_iterations` cap is injected into the playbook body for the workflow script to enforce (the `{{max_iterations}}` token). `/goal` and the Claude Code `OuterLoopRunner` path were removed under DJ-144; `spec_loop_*` stays Codex/Gemini-only per DJ-143. The scout still owns the convergence *judgment* across all four drivers; the driver only re-triggers and counts. A per-runtime minimum-version registry (`internal/runtimepolicy/runtimes-default.yaml` + `.borg/runtimes.yaml`) reads the runtime version from `ClientInfo.version` at MCP `initialize` and logs a warn-and-proceed message when a runtime is below its declared floor (Claude Code's floor is `v2.1.154` for dynamic workflows). See [DJ-144](docs/decisions/dj-144-cc-workflow-convergence.md), [DJ-142](docs/decisions/dj-142-idiomatic-convergence-drivers.md), and [docs/runtime-affordances.md](docs/runtime-affordances.md).
- **`--dry-run` on the four mutating verbs (DJ-147).** `import`, `refine`, `adopt`, `assimilate` accept `--dry-run` + `--format markdown|json`. The workflow runs faithfully end-to-end against a per-session overlay on the SpecStore (DJ-134) — every `mcp__locutus__spec_*` write captures in the overlay rather than persisting; reads in the same session see the overlay so the cascade, citation walk, and convergence happen against the would-be graph. Signal travels env → `_meta` → session-context like DJ-143's `LOCUTUS_MODE`; the daemon registers an overlay on dry-run sessions. Report comes from a new read-only `spec_dry_run_report` MCP tool (the agent calls it as the closing step) plus, in headless, a CLI-side render from `tools.jsonl`. Exit code is always 0 on a successful dry-run. See [DJ-147](docs/decisions/dj-147-dry-run-mutation-capture.md).
- **`assimilate` is bidirectional brownfield reconciliation (DJ-148).** Reads source code, infers/revises features+decisions+strategies (code-is-truth direction), synthesizes approaches binding them to source files with `source_hash`. Produces coherent (spec, code, approach) state — the maintenance loop's outcome. Preconditions: `GOALS.md` exists + goal layer populated (operator runs `refine goals` first); refuses with helpful errors otherwise. Reuses refine's `scout` + per-domain analyzers (`backend-analyzer` / `frontend-analyzer` / `infra-analyzer`) + `gap-analyst` subagents with prompts rewritten for current conventions (axis-shaped ids per DJ-133, no entity persistence per DJ-076). Adds `spec_propose_approach` / `spec_revise_approach` MCP tools + per-approach `source_hash` body field (shared with DJ-149). Per-runtime convergence drivers inherited from DJ-144. DJ-147 dry-run inheritance automatic. Defers middle-out reconciliation, orphan handling, self-contained goal-layer sync, and `code-paths` cache to DJ-150+ as future work. See [DJ-148](docs/decisions/dj-148-assimilate-bidirectional-reconciliation.md).
- **`adopt` closes the spec → code gap (DJ-149).** Reads spec + state, identifies approaches needing work (unbound / spec-drifted / code-drifted / orphan-parent), dispatches the runtime to implement in stacked worktrees (`adopt/<NNN>-<approach-id>` with phase-N+1-branches-off-N), records state-asserted live status per DJ-068. Partially supersedes DJ-148's state-side fields on `spec.Approach`; state re-established at `.borg/state/` per DJ-068/DJ-096. `ReconciliationState.SpecHash string` migrates to `SpecHashes map[string]string` keyed by spec id for granular one-hop upstream subgraph drift detection (catches DJ-138 cascade drift, refine revisions to approach itself, renames as coincident add+remove). Seven new MCP tools: write (`state_record_reconciliation`, `state_refresh_artifacts`, `state_mark_status`, `state_delete_record`) + read (`state_list_records`, `state_get_record`, `state_compare_hashes`); all captureOnly-wrapped per DJ-147. `state_compare_hashes` is read-only server-side spec-hash diff (added / removed / changed) per approach — agent calls it in adopt's Step 2 to detect spec drift without reproducing server-side hash bytes. Adds `drift-classifier` subagent (semantic-vs-trivial code drift judgment). Per-runtime convergence drivers from DJ-144. See [DJ-149](docs/decisions/dj-149-adopt-spec-to-code-reconciliation.md).
- **Per-runtime tool restriction (DJ-143).** Some daemon tools are runtime-scoped — e.g. `spec_loop_*` is for Codex/Gemini interactive self-loop and not for Claude Code (which uses a dynamic workflow under DJ-144). The MCP go-sdk's tool registry is server-global (no per-session `tools/list` filtering), so DJ-143 enforces the scoping at call time via a `requireRuntime` wrapper at each restricted tool's registration site, paired with a leading sentence in each tool's `Description` that names the runtime audience. Runtime comes from `ClientInfo.name` at MCP `initialize`; mode comes from `LOCUTUS_MODE` env var forwarded by the bridge as `_meta["locutus.mode"]` (default `interactive`; the ACP harness sets `LOCUTUS_MODE=headless` on the spawned coding-agent process). A denied call returns an MCP error naming the runtime, mode, tool, and the docs reference. See [DJ-143](docs/decisions/dj-143-per-runtime-tool-policy.md) and `docs/runtime-affordances.md § Tool-Restriction`.
- **Strong-bias write cascades via `refine --with` (DJ-138).** `locutus refine <id> --with "<bias>"` dispatches the `spec_bias` activity — a write-cascade that walks the reference-graph closure of the target and applies the bias through `spec_revise_*` / `spec_propose_*` mutations + `spec_mark_approach_drifted` drift marks. Two directions: forward from a Decision target, backward-then-forward from a Feature/Strategy target. Conservative-closure-mark drift posture; git (`reset --hard` over `.borg/spec/`) is the architectural rollback layer — no transactional spec mutations. Cascade roots land as `spec_biased` history events; downstream events link back via `caused_by`. See [DJ-138](docs/decisions/dj-138-refine-with-bias-cascade.md) and `internal/scaffold/plans/spec_bias.md`.
- **Goal layer persists the LLM's interpretation of `GOALS.md` (DJ-139).** `goal-*` and `agoal-*` are first-class graph nodes carrying `source_clause` (the verbatim excerpt from `GOALS.md` they anchor to) plus `body` (the LLM's interpretation) plus `ceded_to` / `kept_in` on AntiGoals. The `refine goals` playbook runs **sync → iterate → cite** in order: Step 0 dispatches `spec-goal-diff-matcher` to reconcile the persisted goal layer against current `GOALS.md` (short-circuits on a `goals_md_hash` manifest match), applies the diff via `spec_propose_goal` / `spec_revise_goal` / `spec_delete_goal` and the AntiGoal variants, then persists the new hash via `spec_update_goals_md_hash`; the deliberation iteration runs as before with the goal layer as implicit context; the citation walk lands last, judging `.advances` / `.respects` against the final state. Import (`feature_ingestion.md`) reads the goal layer too — conflict detection is now a structural graph test against `agoal-*` bodies + `kept_in` rather than LLM-judging `GOALS.md` prose. Per [DJ-141](docs/decisions/dj-141-unanchored-goal-provenance.md), each node is **anchored** (non-empty `source_clause`, a verbatim `GOALS.md` excerpt) or **unanchored** (empty `source_clause`, a free-text `origin` note for a claim inferred from the mission statement or crystallized into a decision — exactly one of the two is set). The matcher deletes anchored nodes when their clause leaves `GOALS.md` but **never** deletes unanchored nodes by absence; a `GOALS.md` edit that states an inferred claim **promotes** the unanchored node in place (sets `source_clause`, clears `origin`, preserves id), and an opposite-polarity edit **auto-resolves** toward `GOALS.md` (delete + re-propose, with the nodes that cited the retired node surfaced as at-risk). `goals_md_synced_at` is server-stamped, not agent-supplied. See [DJ-139](docs/decisions/dj-139-goal-layer-node-kinds.md) and `internal/scaffold/plans/spec_refinement.md`.
- `docs/DECISION_JOURNAL.md` + `docs/decisions/` — architectural decisions with rationale, alternatives considered, and reversals. `DECISION_JOURNAL.md` is the manifest table (number, title, status, link); full text for each entry lives at `docs/decisions/dj-NNN-<slug>.md`. External cross-references use the short anchor `DECISION_JOURNAL.md#dj-NNN`, which lands on the manifest row and links forward to the full text. The bijection between manifest rows and on-disk files is enforced by `internal/docs/decisions_manifest_test.go`.
- `.claude/plans/` — active implementation plans. Copy to `docs/plans/` once a phase stabilises.
- `docs/agent-conventions.md` — conventions for the canonical agent prompts under `internal/scaffold/agents/`. **Read this before editing or creating any file under `internal/scaffold/agents/` or `internal/scaffold/plans/`.** Covers anti-pattern priming, positive-phrasing patterns, hyphenated naming, and the publishing-translation contract.
- `docs/debugging-traces.md` — operational guide for walking session traces. The shape changed under DJ-135: the agent's reasoning lives in *its* session log (Claude Code, etc.), and Locutus's `.locutus/sessions/<date>/<time>/<sid>/` carries the playbook body it received, the full ACP event stream (`events.jsonl`), the distilled tool-call ↔ result log (`tools.jsonl`), and the agent's final output text.
- `docs/council.md` — workflow diagram for the spec-refinement playbook and a per-agent reference. Re-framed in DJ-135: the "council" is no longer Locutus-orchestrated; it's a coding-agent's execution of the published playbook. The agents themselves (spec-scout, spec-decision-elaborator, etc.) are unchanged.

When these documents conflict with any other file in the repo, `docs/` and `.claude/plans/` win.

## Architecture invariants

- **No in-process LLM calls.** Locutus does not import an LLM SDK. The coding-agent runtime owns the conversation; Locutus exposes data and orchestrates dispatch. If you find yourself adding an Anthropic/Gemini/OpenAI SDK import, you're about to recreate the council that DJ-135 retired.
- **MCP tools are the only spec-mutation path.** The `spec_propose_*` and `spec_revise_*` tools registered in `internal/mcp/tools_spec_write.go` are the surface area for graph writes. Direct `SpecStore.Put` calls outside the MCP handlers are reserved for tests and the daemon's own boot-time load.
- **One activity = one playbook = one MCP prompt = one CLI verb.** The mapping is in `internal/activity/agents-default.yaml` and `internal/scaffold/plans/<activity>.md`. Adding an activity means adding a row to the registry, a playbook file, and (optionally) a CLI verb that calls `runActivityVerb`.
- **The daemon is per-project.** Two projects = two daemons = two sockets. The singleton-per-project pattern is what makes cross-client coordination work (writes through one bridge are visible to reads on another). See `internal/mcp/socket.go` + `internal/mcp/bootstrap.go`.
- **Hyphenated agent ids.** Per DJ-135 resolved-question 8, Claude Code requires hyphens; Gemini and Codex accept both. Canonical agent files use hyphens (`spec-scout`, `spec-decision-elaborator`). The invariant is enforced by `internal/scaffold/agents/hyphenated_ids_dj135_test.go`.

## Command Surface

9 verbs (DJ-101 set + DJ-137 `justify`) + 3 MCP subcommands (DJ-135).

**Activity-dispatching verbs (5):**

1. `locutus refine [<id>] [--with "<bias>"]` — without `--with`: dispatch the `spec_refinement` activity, optional positional `<id>` scopes the run to a node's subtree (defaults to `goals`, the root). With `--with`: dispatch the `spec_bias` activity (DJ-138). `<id>` becomes mandatory and must resolve to a Decision (`dec-`), Feature (`feat-`), or Strategy (`strat-`); the playbook walks the reference-graph closure and cascades the bias forward (Decision targets) or backward-then-forward (Feature/Strategy targets). Approach (`app-`) and Bug (`bug-`) ids are rejected in both modes — those layers have their own surfaces (adopt/assimilate for approaches; bug subgraph). Goal (`goals`) additionally rejected under `--with` because root-cascade is over-broad.
2. `locutus import [source]` — dispatch the `feature_ingestion` activity. Content comes from `<source>` file path or stdin.
3. `locutus adopt [--scope X]` — dispatch the `code_adoption` activity. Optional `--scope` becomes a focus note.
4. `locutus assimilate` — dispatch the `code_assimilation` activity.
5. `locutus justify <id> [--against "..."] [--format markdown|json]` — dispatch the `justification` activity. Produces a structured defense of the named spec node by dispatching `spec-advocate` (and optionally `spec-challenger` + `justify-researcher`). Read-only; one-shot. JSON output schema documented in DJ-137.

Each blocks until the ACP session closes (Q3 of DJ-135 phase 5). Sessions land under `.locutus/sessions/<date>/<time>/<sid>/`.

**Operational verbs (4):**

5. `locutus init` — Bootstrap `.borg/` scaffold + emit per-runtime publish files + write MCP-server config.
6. `locutus update` — Refresh binary. `--reset` overwrites `.borg/agents/` + `.borg/plans/` from embedded defaults and re-publishes per-runtime copies.
7. `locutus status` — Show spec summary. `--full` emits a comprehensive snapshot of the spec graph (DJ-100).
8. `locutus history` — Print the past-tense event timeline. `--alternatives <id>` lists alternatives recorded for a target.

**Read-only deliberation aid (1):**

- `locutus explain <id>` — Render a single spec node's rationale, alternatives, citations, and back-references. No LLM, no dispatch.

**Search aid (1):**

- `locutus list <query>` — Find spec node ids matching a free-text query. No LLM, no dispatch.

**MCP subcommands (3):**

- `locutus mcp` — Smart client/server. Discovers or forks the per-project daemon, then bridges stdin/stdout to its socket. To external MCP clients (Claude Code, Codex, Gemini CLI) this is indistinguishable from a stdio MCP server.
- `locutus mcp-daemon --project <root>` — Internal: the long-lived singleton. Operators don't invoke directly; `mcp` forks it via `EnsureDaemon` when no daemon is responsive.
- `locutus mcp-stop` — Remove the per-project socket so the accept loop unwinds.

The `justify` verb retired with the council in DJ-135 phase 5 and was restored under DJ-137 (2026-05-26) as an ACP-dispatched activity reusing the surviving `spec-advocate` / `spec-challenger` / `justify-researcher` agent prompts. The `refine --brief` and `refine --supersede` flags were subsumed by `refine --with` under DJ-138 (2026-05-26) — a single strong-bias flag where the playbook judges intensity from the bias text. `refine --diff` and `refine --rollback` are subsumed by `git diff` and `git reset --hard` over `.borg/spec/` (the spec graph lives in version control; git is the architectural rollback layer). The `history --narrative / --regenerate-narrative` flags retired with no current revival plan.

## Build & Test

```bash
go build ./...
go test ./...
go test ./path/to/pkg                  # single package
go test ./path/to/pkg -run TestName    # single test
go vet ./...
go test ./... -race                    # race detector
```

## Libraries

- **CLI**: `github.com/alecthomas/kong`
- **MCP**: `github.com/modelcontextprotocol/go-sdk` v1.6.1
- **ACP**: `github.com/coder/acp-go-sdk` (per DJ-119)
- **Spec search**: `github.com/blugelabs/bluge` (per DJ-123)
- **YAML**: `gopkg.in/yaml.v3`
- **Testing**: `github.com/stretchr/testify/assert`
- **Console output**: `github.com/pterm/pterm`
- **Logging**: `log/slog` (stdlib)

No LLM SDK imports — Locutus does not call providers directly (per DJ-135). The coding-agent runtime owns the conversation.
