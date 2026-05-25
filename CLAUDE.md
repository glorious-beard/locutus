# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Locutus — a Go CLI and MCP server that acts as an autonomous project manager for spec-driven software. It maintains a persistent spec graph (`Goal → Decision → (Feature | Strategy) → Approach` — decisions inform features and strategies; approaches are the synthesis layer for coding agents over features and strategies. Axes are surfaced from goals, features, and strategies and resolved by decisions; see DJ-124), exposes that graph to coding agents (Claude Code, Codex, Gemini CLI) via MCP tools and resources, and dispatches activity-shaped work via ACP. The spec is the source of truth; artifacts are derived outputs.

Locutus itself does not make LLM calls. The coding-agent runtime — Claude Code via `claude-agent-acp`, Codex via `codex-acp`, Gemini CLI via `gemini --acp` — owns model selection and the conversation; Locutus exposes the spec graph and the activity playbooks, then watches the agent execute.

## Sources of Truth

- **Multi-runtime via MCP + ACP (DJ-135).** Every CLI verb (`refine`, `import`, `adopt`, `assimilate`) dispatches the matching activity playbook to a coding-agent runtime via ACP; the runtime calls back into the per-project Locutus MCP daemon for spec graph reads and writes. The daemon is a singleton bound to a Unix socket at `.locutus/mcp.sock` so multiple coding agents attached to the same project share one in-process `SpecStore` and one notification fanout. The bridge command `locutus mcp` makes that singleton look like a stdio MCP server to clients that only know stdio. See `docs/mcp.md` and `docs/activities.md`.
- **Decision IDs are axis-shaped (DJ-133).** Every decision's `id` equals `dec-` followed by the primary axis ID it answers verbatim (axis `oltp-store` → `dec-oltp-store`). The id names the *question*; `title`, `chosen_option`, and `rationale` carry the human-readable *answer*. A revision that flips the chosen option keeps the same id — backreferences from features, strategies, and approaches stay byte-stable across flips. The MCP write tool `spec_propose_decision` backfills `axes` from the id when the agent omits it (per DJ-135 phase 5 convergence-by-construction discipline).
- **The in-process `SpecStore` is the source of truth for spec graph reads and writes during a daemon session (DJ-134).** `internal/agent/spec_store.go` holds typed entries tagged by origin (settled / proposed) and a working flag under one RWMutex, with a write-through Bluge search index. The MCP server's read tools (`spec_list_manifest`, `spec_get`, `spec_search`), write tools (`spec_propose_decision`, `spec_propose_feature`, `spec_propose_strategy`, `spec_revise_decision`), and the `spec://manifest` resource all read and write through it. `.borg/spec/` is its persistence backing, not a parallel data source. `spec_get` is batched by design — input is `{ids: [string]}`, output is `{results: {id: {status, body?, working?, reason?}}, available_ids?, working?}` with status in `settled | in_flight | missing`. There is no scalar-id variant; batching is structural.
- **Tool descriptions live in registration, not prompts (DJ-134).** When an activity playbook references a tool (the `mcp__locutus__spec_*` set), the playbook's job is workflow guidance — when to reach for the tool and why, in the context of the activity. Tool-behavior text (input shape, kind-prefix routing, in-flight-vs-settled semantics) lives in the tool's registered `Description` in `internal/mcp/tools_spec_{read,write}.go`. See `docs/agent-conventions.md` § "Tool descriptions live in registration, not prompts."
- **Per-runtime publishing is one-way (DJ-135).** The canonical agent prompts at `internal/scaffold/agents/*.md` and activity playbooks at `internal/scaffold/plans/*.md` are the source of truth. The publisher (`internal/publisher/`) reads them on `locutus init` and `locutus update --reset` and emits per-runtime copies (`.claude/agents/locutus/`, `.codex/agents/locutus-*.toml`, `.gemini/extensions/locutus/`) plus the runtime's MCP-server descriptor pointing at `locutus mcp`. Edits to the published copies are overwritten on the next reset; project-local agent edits go in `.borg/agents/`.
- `docs/DECISION_JOURNAL.md` — architectural decisions with rationale, alternatives considered, and reversals. Authoritative design record.
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

8 verbs (DJ-101 set) + 3 MCP subcommands (DJ-135).

**Activity-dispatching verbs (4):**

1. `locutus refine [target]` — dispatch the `spec_refinement` activity. Optional positional `<target>` becomes a focus note appended to the playbook.
2. `locutus import [source]` — dispatch the `feature_ingestion` activity. Content comes from `<source>` file path or stdin.
3. `locutus adopt [--scope X]` — dispatch the `code_adoption` activity. Optional `--scope` becomes a focus note.
4. `locutus assimilate` — dispatch the `code_assimilation` activity.

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

The `justify` verb retired with the council in DJ-135 phase 5. The `refine --brief / --supersede / --diff / --rollback` and `history --narrative / --regenerate-narrative` flags retired alongside; if you need them back, they land as ACP-dispatched activities in a follow-up.

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
