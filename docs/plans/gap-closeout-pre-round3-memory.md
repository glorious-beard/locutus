# Plan: Pre-Round-3 — Agent Memory Adoption from ADK

## Context

Inserts before Round 3 of the [gap-closeout plan](gap-closeout.md). Closes the "no agent-level memory primitive" gap surfaced in the 2026-04-23 audit, adopting the `memory.Service` shape from [adk-go](https://github.com/google/adk-go/blob/main/memory/service.go) under DJ-077's selective-adoption posture. Same session, while context is fresh on the ADK audit.

**Governing:**

- **DJ-077 (settled)** — permits attribution-preserving adoption of ADK patterns at specific integration points. Memory is explicitly named.
- **DJ-029 (shipped)** — custom orchestration still holds; this adoption is *selective*, not wholesale.
- **DJ-026 (shipped)** — historian narrative is project memory for humans. Agent memory is the companion — project memory for agents.
- **DJ-068 (shipped)** — state store is manifest/state observed state. Agent memory is a third layer: learned context, not desired or observed state.

## Scope

In scope:

- New `internal/memory/` package with an adapted `Service` interface, an `inmemory.Service`, and a file-backed service at `.locutus/memory/`.
- Attribution for the ADK-derived portions: NOTICE file at repo root, top-of-file comments on copied interfaces.
- A `memory.Entry` type suited to Locutus (plain-text content, project-scoped, no user/app fields).
- Acceptance tests.
- DJ-077 update: Status `settled → shipped` for the memory portion.

Out of scope:

- Wiring memory into any specific agent. Callers adopt in later rounds as operational value becomes clear. The adoption bet is "build the shape now; use it when we need it" per DJ-077's framing.
- Vertex / external backends. In-memory + file-backed only.
- Semantic search via embeddings. Keyword search over content is enough for MVP; the interface leaves room for swap-in later.
- Refactoring cascade / preflight / triage to use memory. That's a follow-up round, gated on the pre-Round-3 prompt-externalization sweep noted in DJ-026's Round 2 follow-up.

## Resolved ambiguities

1. **Entry content type.** Adk-go uses `*genai.Content`. We use `string` with a freeform metadata map. Reason: we don't want a genai dependency, and most agent notes are markdown fragments — a string with metadata is sufficient and keeps the interface provider-neutral.
2. **Scoping.** Adk-go scopes entries by `UserID` + `AppName`. We scope by `Namespace` (e.g., `archivist`, `planner`, `project`) to match our single-user, single-project model. Agents writing and reading each own a namespace; the `project` namespace is for cross-agent knowledge.
3. **Search.** Keyword `Contains` over `Entry.Content` for MVP. The `SearchRequest.Query` field is preserved for a future embedding-backed impl.
4. **Persistence layout.** File-backed service uses `.locutus/memory/<namespace>/<entry-id>.yaml`. Gitignored (same rationale as `.locutus/workstreams/` per DJ-073: transient learned context, not audit state). Legible via `cat` for debugging.
5. **Entry ID.** UUIDv4. Agents don't need to pick IDs; the store generates them.

## Acceptance tests

All in `internal/memory/memory_test.go`:

- `TestInMemoryAddAndSearch` — add a few entries to a namespace, SearchMemory by keyword returns matching entries.
- `TestInMemoryNamespaceIsolation` — entries in namespace A must not appear in SearchMemory against namespace B.
- `TestFileBackedRoundTrip` — add via file-backed service, re-open (new service instance over same fs), search returns entry.
- `TestFileBackedCorruptEntryIgnored` — a hand-edited file with broken YAML is logged + skipped, doesn't break subsequent search.
- `TestAddSessionToMemoryAppendsEvents` — existing API shape: a Session-like batch gets ingested as N entries, preserving order.
- `TestEntryMetadataRoundTrip` — custom metadata preserved through save/load.

## Design sketch

```go
// internal/memory/service.go
// Adapted from github.com/google/adk-go/memory/service.go.
// Copyright 2025 Google LLC. Apache License 2.0.

package memory

type Service interface {
    AddSessionToMemory(ctx context.Context, namespace string, entries []Entry) error
    SearchMemory(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

type Entry struct {
    ID             string
    Namespace      string
    Content        string                 // plain text / markdown
    Author         string                 // which agent wrote it (optional)
    Timestamp      time.Time
    CustomMetadata map[string]any
}

type SearchRequest struct {
    Namespace string // scope; empty means cross-namespace
    Query     string // keyword for MVP; embedding-backed later
    Limit     int    // 0 = no limit
}

type SearchResponse struct {
    Entries []Entry
}

// inmemory.go — map[namespace][]Entry, mutex-guarded.
// filestore.go — .locutus/memory/<namespace>/<uuid>.yaml with YAML marshaling.
```

## Files touched

**New:**

- `internal/memory/service.go` — Service interface + types (adapted-from-ADK attribution).
- `internal/memory/inmemory.go` — in-memory implementation.
- `internal/memory/filestore.go` — file-backed implementation.
- `internal/memory/memory_test.go` — 6 tests.
- `NOTICE` — repo-root file enumerating ADK-derived portions.

**Modified:**

- `.gitignore` — add `/.locutus/memory/` (consistent with `/.locutus/workstreams/` per DJ-073).

## Effort

~1 session. The adk-go code is ~100 LOC for the interface + inmemory impl; our adaptations and file-backed impl add ~200 LOC; tests ~200 LOC. Shape is small and well-understood from the audit.

## Verification

`go build ./...`, `go vet ./...`, `go test ./... -race` all green. Plus a manual sanity check: create an entry via inmemory, Search, confirm the content returns.

## Post-shipment actions

- **DJ-077 status:** update the memory portion to `shipped` with a pointer to `internal/memory/`.
- **DJ-026 followup:** no change; historian narrative is independent of agent memory (different audiences).
- **Gap-closeout plan:** add a note referencing this pre-Round-3 increment; subsequent rounds may opt into memory without having to build the primitive themselves.
- **Evaluation framework port (Round 4 reshape):** separate future plan; referenced from DJ-077 but not part of this increment.

## What happens if nobody uses memory

That's a legitimate risk — we're building the shape now without a committed caller. DJ-077 names it as a bet: operational evidence will say whether agent memory earns its place. If six months from now nothing imports `internal/memory/`, we delete the package with a one-line commit and supersede DJ-077's memory section. That's cheaper than inventing the shape under deadline later.
