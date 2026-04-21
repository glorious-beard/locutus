# Plan: Verb-Set Consolidation — Phase D (Cleanup)

## Context

Phases A through C introduced the new eight-verb command set, wired the LLM, added dry-run, and built the reconcile loop. Along the way Phase A kept old command names alive as Kong aliases so existing scripts wouldn't break, and Phase B left `check` callable while its logic moved into `adopt`.

Phase D is the final sweep: delete the aliases, delete `check` as a standalone command, update `CLAUDE.md`, and rename straggling tests.

## Scope

- Delete compatibility aliases: `analyze` (Phase A alias of `assimilate`), `revisit` (Phase A alias of `refine`), `regen` (if it still exists as a separate command).
- Delete `check` CLI command and MCP tool. Its logic lives in `adopt` by this point.
- Rename test files to match their commands (`analyze_test.go` → `assimilate_test.go` once alias is gone).
- Update `CLAUDE.md` to:
  - Remove the stale `docs/IMPLEMENTATION_PLAN.md` reference
  - List the eight-verb command set as the canonical CLI
  - Point at `docs/DECISION_JOURNAL.md` (already correct)
- Rename the Go helper functions that were kept stable through Phases A–C for backward compatibility (`RunAnalyze` → `RunAssimilate`, etc.).
- Clean up any leftover "`LLM not configured`" code paths that were bypassed in Phase A but not deleted.
- Remove any `.borg/` path leakage from deleted commands.

## Principle: No mercy on dead code

Once the new verbs are proven (Phase C ships and runs end-to-end against a real repo), there is no value in keeping the old command names around. The user is the only consumer. Delete aggressively.

Scripts that referenced old names will break; that's acceptable because (1) they'd need to be updated anyway to use the new verbs' fuller functionality, and (2) the error messages from Kong will make the rename obvious.

## Part D1: Delete compatibility aliases

D1a. Remove `aliases:"analyze"` from the `AssimilateCmd` struct in `cmd/cli.go`.
D1b. Remove `aliases:"revisit"` from the `RefineCmd` struct in `cmd/cli.go`.
D1c. Delete the duplicate MCP tool registrations that handled both the old and new names in `cmd/mcp.go`. Keep only the new name.
D1d. Delete `RegenCmd` and `cmd/regen.go` entirely. Its functionality is subsumed by `adopt`.
D1e. Delete the `analyze` / `revisit` / `regen` MCP tools (if any compatibility shim remains).
D1f. Remove the `triage` / `diff` / `version` Kong struct fields from `cmd/cli.go` (Phase B should have done most of this; verify).

## Part D2: Delete `check` command

D2a. Delete `cmd/check.go`.
D2b. Remove `CheckCmd` from `cmd/cli.go`.
D2c. Remove the `check` MCP tool from `cmd/mcp.go` and its test expectations in `cmd/mcp_test.go`.
D2d. The factored `internal/check/CheckPrereqs` function stays — it's called from `adopt`.

## Part D3: Rename Go helpers

D3a. Rename `cmd.RunAnalyze` → `cmd.RunAssimilate` in `cmd/assimilate.go` (and any callers).
D3b. Rename `cmd.RunRevisit` → `cmd.RunRefine` in `cmd/refine.go`.
D3c. Rename `cmd.RunRegen` → fold into `cmd.RunAdopt` or delete.
D3d. Update all test files referencing the old helpers.

## Part D4: Rename test files

D4a. `cmd/analyze_test.go` → `cmd/assimilate_test.go` (should be done in Phase A; verify).
D4b. If any integration tests still reference deleted commands by name, update or remove.

## Part D5: Update CLAUDE.md

D5a. Replace the stale `docs/IMPLEMENTATION_PLAN.md` pointer at [CLAUDE.md:7-11](CLAUDE.md#L7-L11) with:

```markdown
## Sources of Truth

- `docs/DECISION_JOURNAL.md` — architectural decisions with rationale, alternatives, reversals. This is the authoritative design record.
- `.claude/plans/` — current implementation plans (per DJ-XX or per consolidation phase). Copy stable plans to `docs/plans/` once they land.

When these documents conflict with any other file in the repo, the `docs/` and `.claude/plans/` files win.
```

D5b. Add a "Commands" section listing the eight verbs with one-line descriptions.

D5c. Regenerate the `.borg/` scaffold reference if `CLAUDE.md` duplicates any of it; don't let them drift.

## Part D6: Smoke test after cleanup

D6a. `go build ./...` — must succeed.
D6b. `go test ./...` — must succeed.
D6c. Manual smoke against a fresh test directory:

```bash
cd /tmp && rm -rf phase-d-test && mkdir phase-d-test && cd phase-d-test
$LOCUTUS init
$LOCUTUS status
$LOCUTUS --version
$LOCUTUS analyze  # expect: Kong error "unknown command 'analyze'"
$LOCUTUS revisit  # expect: Kong error
$LOCUTUS check    # expect: Kong error
$LOCUTUS diff     # expect: Kong error
$LOCUTUS triage   # expect: Kong error
```

D6d. Confirm all eight new verbs work and all seven old ones are gone.

## Part D7: Release notes

D7a. Draft a `CHANGELOG.md` entry (the repo uses release-please):

```markdown
## [1.1.0]

### Changed
- **Command set consolidated to 8 verbs.** The CLI surface is now:
  `init`, `update`, `import`, `refine`, `assimilate`, `adopt`, `status`, `history`.
- `analyze` renamed to `assimilate`.
- `revisit` renamed to `refine` and generalized to accept any spec node ID.

### Added
- `adopt` — the reconcile loop; brings code into alignment with spec.
- `history` — query the historian for change events, alternatives, narrative.
- `--dry-run` on every mutating verb.
- `--version` global flag (was a subcommand).

### Removed
- `check` (folded into `adopt`'s pre-dispatch gate).
- `diff` (folded into `refine --dry-run`).
- `regen` (folded into `adopt`).
- `triage` (folded into `import`).
- `version` subcommand (now `--version` flag).
```

## Files Touched (expected)

**Deleted:**
- `cmd/check.go`, `cmd/check_test.go` (if exists)
- `cmd/regen.go`, `cmd/regen_test.go` (if exists)
- Any leftover old-name references

**Modified:**
- `cmd/cli.go` — strip aliases and dead command structs
- `cmd/mcp.go` — strip duplicate tool registrations
- `cmd/mcp_test.go` — update tool-list expectations
- `cmd/assimilate.go` — rename `RunAnalyze` → `RunAssimilate`
- `cmd/refine.go` — rename `RunRevisit` → `RunRefine`
- `CLAUDE.md` — drop IMPLEMENTATION_PLAN.md reference, add command list
- `CHANGELOG.md` — release notes (release-please may handle most of this)

## Dependencies

- Requires Phase A, B, and C to be done and verified in production use.
- No downstream dependencies — this is the last phase.

## Why this phase matters

A half-renamed codebase is worse than either the old or the new shape. Running it in production with aliases indefinitely produces rot: new contributors (or future Claude sessions) see two ways to do everything and guess wrong about which is canonical. The phase exists specifically to close that rot window.
