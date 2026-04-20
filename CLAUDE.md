# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Locutus — a Go CLI and MCP server that acts as an autonomous project manager for spec-driven software. It maintains a persistent spec graph (`Goal → (Feature | Strategy) → Decision`, with `Approach` nodes as the synthesis layer for coding agents), produces execution plans, delegates coding to external agents, and supervises their output. The spec is the source of truth; artifacts are derived outputs.

## Sources of Truth

- `docs/IMPLEMENTATION_PLAN.md` — full architecture, package layout, type definitions, detailed designs, and 8-tier build order. This is the implementation reference.
- `docs/DECISION_JOURNAL.md` — 71 architectural decisions with rationale, alternatives considered, and reversals. Consult when a design choice seems ambiguous.

When these two documents conflict with any other file in the repo (README, PLAN.md, etc.), the docs/ files win.

## Build & Test

```bash
go build ./...
go test ./...
go test ./path/to/pkg          # single package
go test ./path/to/pkg -run TestName  # single test
go vet ./...
```

## Libraries

- **CLI**: `github.com/alecthomas/kong`
- **YAML**: `gopkg.in/yaml.v3`
- **Testing**: `github.com/stretchr/testify/assert`
- **Console output**: `github.com/pterm/pterm`
- **Logging**: `log/slog` (stdlib)
