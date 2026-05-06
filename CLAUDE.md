# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Locutus — a Go CLI and MCP server that acts as an autonomous project manager for spec-driven software. It maintains a persistent spec graph (`Goal → (Feature | Strategy) → Decision`, with `Approach` nodes as the synthesis layer for coding agents), produces execution plans, delegates coding to external agents, and supervises their output. The spec is the source of truth; artifacts are derived outputs.

## Sources of Truth

- `docs/DECISION_JOURNAL.md` — architectural decisions with rationale, alternatives considered, and reversals. Authoritative design record.
- `.claude/plans/` — active implementation plans (current consolidation work is in `verb-set-phase-{a,b,c,d}.md`). Copy to `docs/plans/` once a phase stabilises.

When these documents conflict with any other file in the repo, `docs/` and `.claude/plans/` win.

## Command Surface

The verb set splits into 8 mutating/operational verbs plus 2 read-only deliberation aids (DJ-101).

**Mutating and operational (8):**

1. `locutus init` — Bootstrap `.borg/` scaffold.
2. `locutus update` — Refresh binary and embedded defaults.
3. `locutus import <source>` — Admit a new feature/bug with GOALS.md triage.
4. `locutus refine <node>` — Council-driven deliberation on any spec node.
5. `locutus assimilate` — Infer or update spec from code.
6. `locutus adopt` — Bring code into alignment with spec (reconcile loop).
7. `locutus status` — Show state, drift, and validation errors. With `--full` emits a comprehensive snapshot of the spec graph (DJ-100).
8. `locutus history` — Query the past-tense record.

**Read-only deliberation aids (2):**

- `locutus explain <id>` — Render a single spec node's rationale, alternatives, citations, and back-references. No LLM.
- `locutus justify <id> [--against "..."]` — Spec advocate writes an active defense; `--against` runs the challenger first for an adversarial dialogue.

Every mutating verb supports `--dry-run`. `locutus mcp` starts the MCP server; `locutus mcp-perm-bridge` is a hidden internal subprocess. Every CLI verb has MCP parity.

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
- **YAML**: `gopkg.in/yaml.v3`
- **Testing**: `github.com/stretchr/testify/assert`
- **Console output**: `github.com/pterm/pterm`
- **Logging**: `log/slog` (stdlib)
- **LLM**: direct-SDK adapters per provider (DJ-099) — `github.com/anthropics/anthropic-sdk-go`, `google.golang.org/genai`, `github.com/openai/openai-go` (Responses API).
