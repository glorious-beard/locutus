# Plan: Pre-Round-3 — Workstream Session-History Event Store

## Context

Second pre-Round-3 increment, alongside [`gap-closeout-pre-round3-memory.md`](gap-closeout-pre-round3-memory.md). Closes the reasoning-trace gap surfaced in the 2026-04-23 session design discussion: council-style refinement needs the raw transcript, not just distilled memory. A challenger critiquing a drafter's approach must see the drafter's actual reasoning; an analyst reconstructing motivation must see the chatter, not the summary.

**Governing:**

- **DJ-077 (settled, partially revisited 2026-04-23)** — blanket dismissal of `session.Service` was too broad. Event-log-per-session shape maps onto refine-with-council; user-scoping does not. This plan takes the former, drops the latter.
- **DJ-073 (shipped)** — workstream persistence for crash recovery. Session history extends the workstream record with an event log; same directory, same lifecycle.
- **DJ-068 (shipped)** — manifest/state separation. Events are neither; they're transcript — a third persistence class.
- **DJ-078 (settled)** — templating policy. Session history feeds the `history` slot in `AgentCall`; orthogonal to templating but designed to compose.
- Memory adoption (pre-Round-3 increment #1) — session history is the **raw** trace; memory is the **distilled** knowledge. Both stores, different purposes, landing in the same session pair.

## Why this is a Round 3 prerequisite, not Round 3 itself

Round 3 ships `refine` for non-Decision kinds (Goal, Feature, Strategy). Non-Decision refinement is where council agents first appear: drafter proposes an approach, challenger critiques it against the governing strategies, drafter revises. Challengers on a memory-only substrate can only see what the drafter chose to publish — they can't push back on hidden assumptions in the reasoning trace. Build the substrate before the agents that depend on it.

## Scope

In scope:

- New `internal/session/` package with `SessionEvent` type, `Store` interface, in-memory impl, and file-backed impl at `.locutus/workstreams/<id>/events.yaml` (append-only).
- Workstream-scoped keying only. No user scoping.
- Append + range-read API. No search, no filtering beyond time range.
- Events carry: id, workstream id, agent name, role (`system` / `user` / `assistant` / `tool`), content, timestamp, optional parent event id for branching council turns.
- Acceptance tests.

Out of scope:

- Wiring into specific agents. Round 3 adopts it for refine council work; other rounds opt in as needed.
- Compaction / retention. Events accumulate; pruning is a later round if it becomes a problem.
- Cross-workstream queries. If an analyst needs multiple workstreams, it iterates.
- Semantic search over event content. That's what memory is for.
- Chat-turn semantics beyond the workstream session. Locutus is not a chat app.

## Resolved ambiguities

1. **Event content type.** `string` (same as memory.Entry). No multimodal content; text in, text out. If we ever need richer payloads, widen then.
2. **Storage layout.** `.locutus/workstreams/<id>/events.yaml` — single append-only YAML-array file per workstream, not one-file-per-event. Rationale: events are append-frequent, read-sequentially; per-file would explode the inode count and slow range reads. Gitignored (per DJ-073's workstream-is-transient posture).
3. **Append durability.** Append is `open(O_APPEND) → write → fsync → close`. No temp-and-rename (unlike memory's per-entry files) because events are strictly append-only and losing the tail on a crash is acceptable (the workstream is already dead if we crashed mid-append).
4. **Event ID.** UUIDv4. Same as memory.
5. **Roles.** Mirror the LLM API shape (`system`, `user`, `assistant`, `tool`) so events can round-trip into a provider request's `messages` array without translation. Agent name lives in a separate field, not mashed into the role.
6. **Parent event ID.** Optional. Populated when a challenger responds to a specific drafter turn, so branching critiques are reconstructible. Null for linear turns.

## Acceptance tests

All in `internal/session/session_test.go`:

- `TestInMemoryAppendAndRead` — append N events, read returns them in insertion order.
- `TestInMemoryWorkstreamIsolation` — events in workstream A don't leak into reads against workstream B.
- `TestFileBackedRoundTrip` — append via file store, re-open (new store instance over same fs), read returns all events.
- `TestFileBackedAppendIsAppendOnly` — after append, file on disk is strictly longer; no rewriting.
- `TestFileBackedCorruptTailIgnored` — truncate last 10 bytes of events.yaml; store logs warning, returns all events up to the last parseable one.
- `TestReadRangeFiltersByTime` — append 10 events across a minute, read with `Since=t+30s` returns only events at or after that timestamp.
- `TestParentEventIDRoundTrip` — append child event with `ParentID` set, read preserves the reference.
- `TestConcurrentAppendSafe` — two goroutines appending concurrently produce N+M events, no loss, no corruption (file store uses a mutex; test with `-race`).

## Design sketch

```go
// internal/session/event.go

package session

type SessionEvent struct {
    ID           string
    WorkstreamID string
    AgentName    string    // "drafter", "challenger-strategy-foo", etc.
    Role         string    // "system" | "user" | "assistant" | "tool"
    Content      string
    Timestamp    time.Time
    ParentID     string    // optional; empty for linear turns
}

// internal/session/store.go

type Store interface {
    Append(ctx context.Context, ev SessionEvent) error
    Read(ctx context.Context, workstreamID string, opts ReadOpts) ([]SessionEvent, error)
}

type ReadOpts struct {
    Since time.Time // zero = no lower bound
    Until time.Time // zero = no upper bound
    Limit int       // 0 = no limit
}

// inmemory.go — map[workstreamID][]SessionEvent, mutex-guarded.
// filestore.go — .locutus/workstreams/<id>/events.yaml, append via O_APPEND + fsync, mutex-guarded per workstream id.
```

## Files touched

**New:**

- `internal/session/event.go` — SessionEvent type.
- `internal/session/store.go` — Store interface + ReadOpts.
- `internal/session/inmemory.go` — in-memory impl.
- `internal/session/filestore.go` — file-backed impl.
- `internal/session/session_test.go` — 8 tests.

**Modified:**

- `.gitignore` — already covers `/.locutus/workstreams/` per DJ-073; no new entry needed.
- `NOTICE` — if the file-store design borrows enough from `adk-go/session/inmemory` to warrant attribution, add a line. Expected: the shape is generic enough (append-only event log, mutex-guarded) that we probably don't need it; decide during implementation.

## Effort

~1 session. Smaller than the memory primitive because there's no search dimension. ~150 LOC for impls, ~200 for tests.

## Verification

`go build ./...`, `go vet ./...`, `go test ./... -race` all green. Manual sanity: append three events to a workstream, `cat .locutus/workstreams/<id>/events.yaml`, confirm legible YAML with events in order.

## Post-shipment actions

- **DJ-077 update:** add a "shipped" pointer next to the 2026-04-23 partial-revisit note, referencing `internal/session/`.
- **Round 3 plan (to be drafted):** refine-for-non-Decision-kinds relies on this substrate. Draft the Round 3 plan only after this increment ships, so the plan can cite concrete types.
- **Agent-call assembly:** when Round 3 lands, define the `AgentCall` shape (system + history + memory + user) in `internal/agent/`. Not part of this plan; the substrate must exist first.

## What happens if nobody uses session history

Same DJ-077 disposition as memory: if six months pass with no importers, delete the package with a one-line commit and note the reversal. But this primitive has a committed caller — Round 3's challenger agents. The risk of no-adoption is lower here than for memory.

## Interaction with the memory increment

Land in the same session pair, in this order:

1. Memory first (`gap-closeout-pre-round3-memory.md`). Establishes the two-store pattern and the NOTICE file.
2. Session history second (this plan). Slots into the established pattern; shorter because the path is already cut.

Both ship before Round 3 starts. Do not interleave; each should land as its own commit with its own DJ status update.
