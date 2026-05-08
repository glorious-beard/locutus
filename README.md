# Locutus

**Autonomous project manager for spec-driven software.**

The Kubernetes model for product development: a persistent spec graph is the desired state, your code is the observed state, and a reconcile loop drives the diff toward zero. Every decision is a two-way door you can revisit, supersede, or have the spec defend on demand.

[![Go Reference](https://pkg.go.dev/badge/github.com/chetan/locutus.svg)](https://pkg.go.dev/github.com/chetan/locutus)
[![Release](https://img.shields.io/github/v/release/glorious-beard/locutus)](https://github.com/glorious-beard/locutus/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

---

## Why this exists

Coding agents have gotten very good at typing. They are still bad at remembering.

If you've shipped real work with Claude Code, Cursor, Codex, or Gemini, you've felt the symptoms:

- Context compaction silently drops the decisions that justified the code.
- Six weeks later, "why did we pick `pg-boss` over Temporal?" has no real answer — only the artifact.
- Agents revisit settled questions because the settling never persisted anywhere durable.
- Sunk-cost spirals on problems other industries solved twenty years ago.
- Every choice an agent made on your behalf turns out to be a *one-way door* you didn't know you were walking through.

Locutus fixes the layer the agents can't: durable architectural intelligence over time.

## The model

Think of it as a control plane for product decisions, not infrastructure. *The idea — declarative state, reconciliation loops — not the YAML.*

A persistent spec graph captures **what** and **why**:

```
Goal
 ├── Feature  ──┐
 │              ├── Approach
 └── Strategy ──┘
```

**Decisions** document the assumptions made and alternatives considered in crafting a Feature or Strategy. A single Decision can be referenced by any number of Features and Strategies — they're shared rationale, not sequential children. **Approaches** are the synthesis layer the coding agents read.

Three operations move work through it:

- **`import`** — admit a new goal, feature, or bug into the graph.
- **`refine`** — council-driven deliberation on any node. An advocate, a challenger, and a synthesizer rewrite the node together.
- **`adopt`** — the reconcile loop. Read the spec, observe the codebase, drive a coding agent (Claude Code, Codex, Gemini) until the diff is zero.

Three more let you reflect:

- **`assimilate`** — infer or update the spec from existing code. Brownfield onboarding.
- **`status`** — what's drifted, what's invalid, what's pending.
- **`history`** — the past-tense record of every spec mutation, queryable.

## Two-way doors, by design

Every decision Locutus makes — or that an agent makes on your behalf — is reversible:

- `refine <id>` reopens any node for fresh deliberation.
- `--rollback` undoes the most recent refine using bytes captured in `.borg/history/`.
- `superseded by` is a first-class status in the Decision Journal.
- `refine --against "..."` runs an adversarial council that pits an advocate against a challenger and writes the dialogue back into the record.

You can also make the spec defend itself, on demand:

```bash
locutus justify dec-adopt-nextauth
locutus justify dec-adopt-nextauth --against "why not Auth0 or Clerk?"
```

The first writes an active defense. The second runs the challenger first, then the advocate — an adversarial dialogue captured as part of the spec, not a one-shot answer that decays in chat scrollback.

## A taste

```bash
# Bootstrap the spec scaffold in a fresh repo
$ cd my-app && locutus init

# Admit a new feature; GOALS.md triage routes it under the right Goal
$ locutus import "let users sign in with Google"
  → admitted as feat-google-signin, linked to goal-account-onboarding

# Council deliberates on the feature, threading a focused intent
$ locutus refine feat-google-signin --brief "favor migration safety over speed"
  → 3 decisions proposed, 1 superseded, 2 strategies updated

# Make the spec defend a contested decision against a real challenge
$ locutus justify dec-adopt-nextauth --against "why not Auth0?"
  → adversarial dialogue written to dec-adopt-nextauth.md

# See what would change without touching code
$ locutus adopt --dry-run
  → 4 files to edit, 2 to create, 0 strategies in violation

# Reconcile code to spec; Locutus dispatches to your coding agent of choice
$ locutus adopt
```

## Install

**Pre-built binaries** for macOS and Linux are attached to each [GitHub release](https://github.com/glorious-beard/locutus/releases).

**From source:**

```bash
go install github.com/chetan/locutus@latest
```

Locutus needs an LLM provider key. Set one of:

```bash
export ANTHROPIC_API_KEY=...   # recommended
export OPENAI_API_KEY=...
export GEMINI_API_KEY=...
```

Optional: `LOCUTUS_MODELS_CONFIG` to point at a custom model registry; `LOCUTUS_LOG_LEVEL=debug|info|warn|error` to override verbosity. `.env` in the working directory is loaded automatically (shell-exported values still win).

First run:

```bash
cd your-repo
locutus init
locutus import "describe the first thing you want built"
```

## Command surface

| Verb | Purpose |
|---|---|
| `init` | Bootstrap `.borg/` scaffold in a project. |
| `update` | Self-update to the latest release. |
| `import <source>` | Admit a feature or bug; GOALS.md triage links it into the graph. |
| `refine <id>` | Council-driven deliberation on any node. `--brief`, `--diff`, `--rollback`. |
| `assimilate` | Infer or update spec from code. |
| `adopt` | Reconcile code to spec. The work loop. |
| `status` | State, drift, and validation errors. `--full` for a graph snapshot. |
| `history` | Query the past-tense record. `--narrative` regenerates an LLM-authored summary. |
| `explain <id>` | Render a node's rationale, alternatives, and back-references. No LLM. |
| `justify <id>` | Spec advocate writes a defense. `--against "..."` runs the challenger first. |

Every mutating verb supports `--dry-run`. Every CLI verb has MCP parity — see below.

## MCP integration

`locutus mcp` starts a stdio MCP server. Wire it into Claude Code, VS Code, or any MCP-compatible client:

```jsonc
{
  "mcpServers": {
    "locutus": {
      "command": "locutus",
      "args": ["mcp"]
    }
  }
}
```

Stdio is the common denominator across MCP clients today. HTTP transport is on the roadmap for multi-client scenarios.

## What's shipped, what's settled

Locutus uses a Decision Journal as its architectural record. Each entry carries an explicit status:

- **shipped** — code matches the decision. Safe to rely on.
- **shipping** — partially implemented. The DJ describes what's in vs. out.
- **settled** — design agreed, no code yet. A commitment, not yet a fact.
- **superseded by DJ-N** — read DJ-N for current direction.

Before relying on a Locutus feature, read the relevant DJ first. The journal is the source of truth on current behavior; this README is the elevator pitch.

See [`docs/DECISION_JOURNAL.md`](docs/DECISION_JOURNAL.md).

## For contributors

```bash
go build ./...
go test ./...
go test ./... -race
go vet ./...
```

Project layout:

- `cmd/` — Kong CLI command implementations and MCP entry points.
- `internal/` — the engine: spec graph (`spec/`, `specio/`), reconcile loop (`reconcile/`, `cascade/`), agent dispatch (`agent/`, `dispatch/`, `executor/`), council deliberation (`session/`), and the historian (`history/`).
- `docs/DECISION_JOURNAL.md` — authoritative design record.
- `.claude/plans/` — active implementation plans.
- [`CLAUDE.md`](CLAUDE.md) — guidance for AI collaborators working in this repo.

## License

MIT. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
