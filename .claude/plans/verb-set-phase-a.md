# Plan: Verb-Set Consolidation — Phase A (Wiring + Renames + History)

## Context

The CLI grew from the 8-tier build in an enumerative way (one subcommand per tier deliverable). After DJ-068/DJ-069/DJ-071 landed, several commands became misnamed, others were stubbed, and the surface duplicated concepts (`diff` / `status`, `triage` / `import`, `revisit` / `regen`).

The target verb set is eight commands covering the actual lifecycle:

| # | Verb | Purpose |
|---|------|---------|
| 1 | `init` | Bootstrap `.borg/` scaffold |
| 2 | `update` | Refresh binary and embedded defaults |
| 3 | `import <source>` | Admit a new feature/bug with alignment evaluation |
| 4 | `refine <node>` | Council-driven deliberation on any spec node |
| 5 | `assimilate` | Infer or update spec from code |
| 6 | `adopt` | Bring code into alignment with spec |
| 7 | `status` | Show state, drift, and validation errors |
| 8 | `history` | Query the past-tense record |

Phase A is the cheap, low-risk subset: renames, LLM wiring, and `history`. It leaves the heavy reconciler work (`adopt`) for Phase C.

## Scope

In scope for Phase A:

- Rename `analyze` → `assimilate` (CLI subcommand name, MCP tool name, test file names)
- Rename `revisit` → `refine`; widen to accept any node kind (Goal, Feature, Strategy, Decision, Approach)
- Construct a Genkit-backed `agent.LLM` at CLI startup / MCP server startup; pass it to handlers that need it
- Delete all `"LLM not configured"` error paths from command `Run()` methods; they should work when an API key is present
- Add `history` as a CLI subcommand and MCP tool backed by `internal/history`

Out of scope for Phase A (covered by later phases):

- Fold-ins (`triage` into `import`, `diff` into `--dry-run`, `check` into `adopt`) — Phase B
- The reconciler loop, cascade, pre-flight, and `adopt` command itself — Phase C
- Final deletion of superseded commands and `CLAUDE.md` cleanup — Phase D

## Principle: Keep CLI surface stable until Phase D

Phase A renames a verb (`analyze` → `assimilate`, `revisit` → `refine`). Rather than delete the old names in this phase, keep them as Kong aliases that dispatch to the same `Run()` body. This lets Phase A and Phase B ship independently without breaking any user scripts that exist; Phase D deletes the aliases cleanly.

The alias mechanism is a one-line Kong tag: `cmd:"" help:"..." aliases:"analyze,old-name"`.

## Part A1: Build a shared LLM constructor

A1a. Add `cmd/llm.go` with a single helper:

```go
// mustLLM constructs a Genkit-backed LLM once per process. Exits with a
// clear error message if no provider is configured.
func mustLLM() (agent.LLM, error) {
    return agent.NewGenKitLLM()
}
```

A1b. Update each command that needs an LLM (`refine`, `assimilate`, `triage` path inside `import`, `history` narrative query) to call `mustLLM()` and pass the result into the existing `Run*` helper functions.

A1c. Same wiring in `cmd/mcp.go`: build the LLM once at `NewMCPServerWithDir`, close over it in each `AddTool` handler. If `DetectProviders().Any() == false`, return a clear error in tool handlers (not on server startup — users can still use `init`/`status`/`diff` without a key).

A1d. Tests: ensure `agent.NewGenKitLLM()` is exercised once per run through a happy-path env; `MockLLM` continues to cover unit tests.

## Part A2: Rename `analyze` → `assimilate`

A2a. Rename the Kong struct: `AnalyzeCmd` → `AssimilateCmd` in `cmd/cli.go` and `cmd/analyze.go`. File rename: `cmd/analyze.go` → `cmd/assimilate.go`, `cmd/analyze_test.go` → `cmd/assimilate_test.go`.

A2b. Add `aliases:"analyze"` on the new `Assimilate` struct so the old command name still works.

A2c. Wire the LLM through to `RunAnalyze` (keep the Go helper name stable to avoid touching test signatures; tests will migrate in Phase D). Replace the `"LLM not configured"` error with the actual invocation.

A2d. MCP: rename the tool from `analyze` to `assimilate` in `cmd/mcp.go`; keep registering `analyze` as a second tool that delegates to the same handler for one release cycle.

A2e. Update the test expectations in `cmd/mcp_test.go` to include both `assimilate` and `analyze`.

## Part A3: Rename `revisit` → `refine` and generalize

A3a. Rename Kong struct: `RevisitCmd` → `RefineCmd`. File rename: `cmd/revisit.go` → `cmd/refine.go`.

A3b. Widen the argument type. Current `revisit` accepts "Decision or strategy ID". The generalized `refine <id>` accepts any spec node ID (Goal, Feature, Strategy, Decision, Approach, Bug). At runtime, look up the node kind from the spec graph and dispatch to the appropriate council pattern:

- **Decision** — call existing revision path (historian alternatives + blast radius + council rounds).
- **Feature** / **Strategy** — regenerate present-tense statement against current child Decisions; regenerate affected Approaches.
- **Approach** — re-synthesize the body from its parent Feature/Strategy + applicable Decisions.
- **Goal** — council-led re-scoping; cascades into Feature/Strategy updates.
- **Bug** — handled same as Feature for now (they share the admission shape).

A3c. For Phase A, only the Decision path needs to work end-to-end (it's what `revisit` did before). Other kinds return a clear `not yet implemented for <kind>` message. Phase B or C will fill in the rest as they interact with the reconciler.

A3d. Alias: `aliases:"revisit"` so existing invocations keep working.

A3e. MCP: add `refine` tool; keep `revisit` as a delegating duplicate.

## Part A4: Add `history` command

A4a. Create `cmd/history.go`:

```go
type HistoryCmd struct {
    ID       string `arg:"" optional:"" help:"Target node ID to show history for."`
    Narrative bool  `help:"Print the narrative summary from .borg/history/summary.md."`
    Alternatives bool `help:"List alternatives considered for the target ID."`
}
```

A4b. Behavior:

- No flags, no arg: list all events, newest first, with kind + target + one-line rationale.
- With `<id>`: filter to events for that target.
- `--alternatives <id>`: show `historian.Alternatives(id)`.
- `--narrative`: dump `.borg/history/summary.md` if present.

A4c. MCP: add `history` tool with the same inputs. Output is text; the structured JSON event list can be reached via `--json`.

A4d. Tests: unit tests against a `MemFS` with fixture events.

## Part A5: Global `--version` flag

A5a. Remove `VersionCmd` subcommand from `cmd/cli.go`. Replace with Kong `--version` flag at the CLI struct level, using `kong.Vars{"version": version}` already plumbed from `main.go`.

A5b. Keep `cmd/version.go` helper if any other code consumes the version struct; otherwise delete.

## Final Verification

```bash
go build ./...
go test ./...
ANTHROPIC_API_KEY=$REAL_KEY go test ./internal/agent/... -run TestGenKit -v

# Manual smoke:
./locutus --version
./locutus init testproj
./locutus history
./locutus refine some-decision-id --dry-run  # expected: "not yet implemented"; dry-run plumbing lands in Phase B
ANTHROPIC_API_KEY=$REAL_KEY ./locutus assimilate  # should run, not error on "LLM not configured"
```

## Files Touched (expected)

- `cmd/cli.go` — rename structs, add `HistoryCmd`, drop `VersionCmd`, wire `--version` flag
- `cmd/llm.go` — new: shared LLM constructor
- `cmd/analyze.go` → `cmd/assimilate.go`
- `cmd/revisit.go` → `cmd/refine.go`
- `cmd/history.go` — new
- `cmd/mcp.go` — update tool names, add `history`/`refine`/`assimilate`
- `cmd/version.go` — delete or reduce
- `cmd/*_test.go` — rename test files to match
- `main.go` — no change (already passes `version` var)

## Dependencies

- Phase A depends on nothing; it's the starting point.
- Phase B depends on A (dry-run plumbing on renamed verbs).
- Phase C depends on A (consumes the LLM wiring in the reconciler's pre-flight path).
- Phase D depends on B and C (deletes aliases only after new verbs are proven).
