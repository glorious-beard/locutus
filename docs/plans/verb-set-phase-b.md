# Plan: Verb-Set Consolidation — Phase B (Fold-ins + Dry-Run)

## Context

After Phase A, the CLI has both the new verb set (`refine`, `assimilate`, `history`) and the old verbs still hanging around as aliases or independent commands. Phase B folds overlapping verbs into their proper homes and introduces `--dry-run` as a consistent flag on every mutating verb.

The fold-ins are:

- `triage` → becomes the admission gate inside `import`
- `diff` → becomes `refine <id> --dry-run` (cascade preview) and later `adopt --dry-run` (plan preview, Phase C)
- `check` → becomes the prereq gate inside `adopt` (Phase C); this phase moves the logic but leaves `check` callable
- `version` (subcommand) → already handled by `--version` flag in Phase A

## Principle: Dry-run on every mutating verb; read-only verbs don't need it

| Verb | Mutates? | `--dry-run` supported |
|---|---|---|
| `init` | yes (creates files) | yes — report what would be created |
| `update` | yes (replaces binary) | yes — report target version |
| `import` | yes (creates spec node) | yes — report triage verdict, would-be-ID, destination |
| `refine` | yes (rewrites spec, cascades) | yes — report cascade blast radius |
| `assimilate` | yes (writes spec graph) | yes — report inferred spec without writing |
| `adopt` | yes (dispatches agents, writes code, updates state) | yes — report workstream plan + prereq status (Phase C) |
| `status` | no | n/a |
| `history` | no | n/a |

Every dry-run exits 0 with preview output. No side effects on `.borg/`, `.locutus/state/`, or the working tree.

## Part B1: Fold `triage` into `import`

B1a. `import` currently takes `--input <file>` or a `--content` string (MCP). The admission gate logic lives in `agent.EvaluateAgainstGoals` (existing, LLM-backed). Move the call into `import`:

1. Read input.
2. If `.borg/GOALS.md` exists, call `EvaluateAgainstGoals` on the input.
3. If verdict is `accepted`: proceed to create the Feature/Bug node.
4. If `rejected` or `duplicate`: print the verdict and rationale, exit non-zero. No node created.
5. If `--skip-triage` is passed: bypass evaluation entirely (for cases where the user knows better).
6. If GOALS.md is absent: skip evaluation with a warning, proceed to import. Triage isn't a hard gate in a brand-new repo.

B1b. `import --dry-run`: run steps 1–3 but do not write to `.borg/spec/`. Print the verdict + where the node would land + the generated ID.

B1c. Delete `cmd/triage.go`, `cmd/triage_test.go`, and the `triage` MCP tool.

B1d. Keep the Go helper `agent.EvaluateAgainstGoals` — it's the implementation; only the top-level command goes away.

## Part B2: Fold `diff` into `refine --dry-run`

B2a. `refine <id> --dry-run` output:

```
Refining decision/<id>:

Blast radius (if decision changes):
  Features rewritten:   <count>
  Strategies rewritten: <count>
  Approaches drifted:   <count>
    - <approach-id-1>
    - <approach-id-2>

No changes written.
```

The blast-radius computation reuses `spec.ComputeBlastRadius` from `internal/spec/graph.go` (already implemented).

B2b. For Decision targets: show blast radius directly.
For Feature/Strategy targets: show which Approaches would be regenerated.
For Approach targets: show parent and applicable Decisions that would inform re-synthesis.

B2c. Delete `cmd/diff.go`, `cmd/diff_test.go`, and the `diff` MCP tool. The Go helper `RunDiff` moves into `cmd/refine.go` as an unexported `computeBlastRadius` helper.

## Part B3: Move `check` into dispatcher pre-flight (keep CLI for now)

B3a. Extract the prereq-check logic into a reusable function in `internal/check/check.go`: `CheckPrereqs(ctx, fsys) ([]PrereqResult, error)`. (Most of this probably exists; verify and factor.)

B3b. Phase B does **not** delete the `check` CLI command yet — that happens in Phase D once `adopt` is proven. For Phase B, the CLI command calls the same factored function.

B3c. Phase C will wire `CheckPrereqs` into `adopt` as the first pre-dispatch gate.

## Part B4: `--dry-run` on `assimilate`

B4a. `assimilate --dry-run`: run the full pipeline (inventory → heuristic inference → LLM inference → gap analysis) but do not write any spec files. Print a summary:

```
Assimilation preview:
  Features inferred:   <n>
  Decisions inferred:  <n> (<m> assumed)
  Strategies inferred: <n>
  Bugs/gaps flagged:   <n>

No spec files written to .borg/.
```

B4b. The pipeline (`agent.Analyze`) already returns an `AssimilationResult` in memory — this phase just wires a flag that suppresses the final write pass.

## Part B5: `--dry-run` on `refine`

B5a. For Phase B, this shows the cascade blast radius only (from B2a). Actual council deliberation preview — "here's what the council would discuss" — is out of scope; that would require running the council without saving history, which is non-trivial.

B5b. A sensible upgrade path in Phase C or later: `refine --dry-run --council-preview` runs one round of council and prints it, still without writing to spec.

## Part B6: Consolidate MCP tools

B6a. Final MCP tool list after Phase B:

- `init`
- `status`
- `import` (with `dry_run: bool`)
- `refine` (with `dry_run: bool`)
- `assimilate` (with `dry_run: bool`)
- `history`
- `update` — low priority; most MCP clients won't call it
- `check` — still present, feeds into Phase C's `adopt`

B6b. Delete MCP tools: `diff`, `triage`, `analyze` (superseded by `assimilate` alias-only from Phase A).

B6c. Keep the Phase A compatibility aliases for one more cycle (deleted in Phase D).

## Final Verification

```bash
go build ./...
go test ./...

# Manual smoke:
echo '---
id: test-feature
title: Test Feature
type: feature
---
A test feature body.' > /tmp/issue.md

./locutus import --input /tmp/issue.md --dry-run
# expected: triage verdict, would-be ID, no .borg/ write

./locutus import --input /tmp/issue.md
# expected: triage passes, feature created

./locutus refine some-decision-id --dry-run
# expected: blast radius, no writes

./locutus assimilate --dry-run
# expected: inferred-spec summary, no .borg/ write
```

## Files Touched (expected)

- `cmd/import.go` — add triage call and `--dry-run`
- `cmd/refine.go` — add `--dry-run` with blast-radius output
- `cmd/assimilate.go` — add `--dry-run`
- `cmd/triage.go` — **deleted**
- `cmd/triage_test.go` — **deleted**
- `cmd/diff.go` — **deleted** (helpers moved into `cmd/refine.go`)
- `cmd/diff_test.go` — **deleted**
- `cmd/mcp.go` — drop deleted tools, add dry-run input fields
- `cmd/mcp_test.go` — update tool-list assertions
- `internal/check/check.go` — factor `CheckPrereqs` out as a reusable function

## Dependencies

- Depends on Phase A (renamed `refine` and `assimilate` commands, LLM wiring).
- Phase C depends on B for the `CheckPrereqs` factoring and consistent `--dry-run` shape.
- Phase D depends on B (deletes the compatibility aliases that B leaves in place).
