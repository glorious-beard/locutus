# Plan: Persistent BM25 search index for the spec graph

## Context

`locutus list` today is a token-count-and-weight scorer hand-rolled in [cmd/list.go](../cmd/list.go). For each query it walks the loaded spec graph, counts case-insensitive substring matches per field (Title=3, ID=2, Body=1, plus the newly-added Summary=2 from DJ-114), sums weights, sorts. The recent Summary-scoring addition closes most of the synonym-bridge gap at small scale, but the underlying approach has a low ceiling:

- No IDF — common words ("data") rank the same as rare ones ("WorkOS").
- No length normalization — long rationales accidentally rank higher just by having more tokens to match.
- No stemming — "authentication" misses nodes whose Summary says "authenticate".
- No phrase, prefix, or fuzzy queries.

At winplan's current ~270 nodes after a single `refine goals` step, the heuristic is fine. At the realistic target scale (1500–3000 nodes for a moderately developed project; potentially more for long-lived ones), common-word dominance and the absence of length normalization become real annoyances, and operators reach for `grep` instead.

The new spec_list_manifest / spec_get tools (DJ-094, DJ-115) gave LLM agents a navigation surface; this plan brings the same quality improvement to the operator-facing `list` verb and lays the groundwork for an MCP `spec_search` follow-up that exposes ranked search to council agents.

## Decisions

**Engine: Bluge.** Pure-Go, BM25-first, leaner than Bleve. Built by Bleve's principal author with hindsight from Bleve's evolution. No CGo — preserves locutus's static-binary property. ~half Bleve's vendored footprint.

**Storage: persistent in `.locutus/spec_index/`.** Gitignored, regenerable cache. Matches DJ-103's pattern for the narrative summary cache. Operator escape hatch: `rm -rf .locutus/spec_index/`.

**Replace heuristic outright.** No `--fts` transition flag. The verb surface stays small and the existing list-test suite gets rewritten against BM25 semantics in one pass.

**Cross-process exclusion: Bluge's native flock.** Verified in source. [`index/lock/lock_nix.go`](https://github.com/blugelabs/bluge/blob/master/index/lock/lock_nix.go) uses `unix.Flock(LOCK_EX | LOCK_NB)`; [`index/lock/lock_windows.go`](https://github.com/blugelabs/bluge/blob/master/index/lock/lock_windows.go) uses `LockFileEx`. Both create a `bluge.pid` file in the index directory containing the holder's PID. We don't need a separate `gofrs/flock` dep.

**Package location: `internal/search/`.** Search-shaped concern that depends on `internal/spec/`; putting it under `internal/spec/` would invert the dependency direction. Mirrors `internal/render/`, `internal/cascade/`.

## Design

### Index shape

Single index. `kind` is a keyword field used as a filter, not a separate index per kind. The per-kind speedup at our scale (3000 nodes) is negligible; the unified shape simplifies cross-kind queries (which dominate operator use).

### Field mapping

Each indexed node emits a Bluge document via `bluge.NewDocument(id).AddField(...)`. The field set is per-kind but uniform in analyzer/weight policy:

| Field | Analyzer | Boost | Notes |
|---|---|---|---|
| `_id` (Bluge identity) | — | — | Same string as `id` field; used by Bluge for upserts |
| `id` | keyword | — | Exact-match lookup (e.g., `id:dec-postgres`) |
| `id_tokens` | simple (whitespace + lowercase) | 2.0 | Slug body — `dec-postgres-with-pgvector` → `postgres pgvector` |
| `kind` | keyword | — | Filter only, never searched as free text |
| `title` | en (Porter stem + English stopwords) | 3.0 | Curated headline |
| `summary` | en | 2.0 | DJ-114 authored "what" line |
| `body` / `rationale` / `description` | en | 1.0 | Long prose; BM25 length-normalizes |
| `alternatives.*`, `bug.root_cause`, `bug.fix_plan`, `bug.reproduction_steps`, `provenance.architect_rationale`, `provenance.citations[].excerpt` | en | 1.0 | Same as body |

Stopwords and edge cases:

- Three-letter acronyms ("PII", "RLS", "TLS", "JWT") survive the English stopword list; safe.
- Hyphenated compound tokens ("AES-256-GCM") tokenize as `[aes, 256, gcm]` — numeric tokens match numeric queries, which is what we want.
- Slug ids index twice: once as keyword for `id:exact-match`, once tokenized for `postgres` to match `dec-postgres-with-pgvector`.

### Lifecycle: fingerprint-based invalidation

```
on locutus <verb> start:
  fp_disk    = read .locutus/spec_index/fingerprint
  fp_current = "<schema_ver>:<mtime_aggregate_hash>"
  if fp_disk != fp_current OR index missing/corrupt:
    rebuild from .borg/spec/
    persist new index + new fingerprint
  else:
    open existing index (~50-100ms for 3000 nodes)
  
  run verb...
  
  if verb mutated specs:
    update index incrementally (one writer.Update / writer.Delete per node)
    persist new fingerprint
  exit
```

**Fingerprint algorithm:** `sha256(schema_ver + "\n" + sorted(path||"\t"||mtime_unix_nano||"\t"||size for each spec file))`. Single pass, ~5-10ms for 3000 files. Catches additions, deletions, mtime-changing edits. Misses mtime-preserving edits (`cp -p`, restoring from backup) — handled by `rm -rf .locutus/spec_index/` as the operator escape hatch.

**Schema version:** integer constant `internal/search.SchemaVersion`. Bump manually whenever the field mapping changes. Mismatch on load → rebuild from scratch.

### Concurrency model

**Read verbs (`list`, future `find`):** open a `bluge.Reader` directly from disk via `bluge.OpenReader(config)`. No writer lock. Concurrent reads coexist freely. Slight staleness (bounded to whatever the most recent writer hasn't committed yet) is acceptable for query workflows.

**Write verbs (`import`, `refine`, `adopt`, prereq `fill-summaries`):** open a `bluge.Writer` with retry on lock contention. Retry schedule: `[0, 250ms, 500ms, 1s]` — total worst-case wait 1.75s across 4 attempts. On retry exhaustion, fail with a clear error reading the holder's PID from `.locutus/spec_index/bluge.pid`:

```
locutus refine: index lock held by another locutus process (PID 47123).
Wait for it to finish, or kill the holder if it's stuck.
```

**MCP server:** does NOT hold the writer continuously. Reads (the new `spec_search` tool plus the existing `spec_list_manifest` / `spec_get`) go through a `Reader`. Mutating verbs the MCP server dispatches in-process (`import`, `refine`, `adopt`) acquire the writer briefly via the same `OpenWriterWithRetry` path CLI verbs use, apply the mutation, and release. Same lock-hold profile as CLI — avoids the "MCP blocks all CLI mutations" pathology a continuously-held writer would cause. This is in scope because Phase 1's writer plumbing is shared between CLI and MCP execution paths; nothing MCP-specific to build beyond what the verbs already get.

**Lock-busy detection:** `errors.Is(err, syscall.EWOULDBLOCK)` on Unix, equivalent on Windows. Bluge propagates the syscall error through its error chain; we don't need to string-match.

### Hook into the mutation path

Plumb index updates through `specio.SavePair` and `specio.SaveMarkdown` — the two functions every spec write goes through. Add an optional callback:

```go
type SpecWriteCallback func(kind, id string, deleted bool)

// internal/specio
var onSpecWrite SpecWriteCallback  // process-global; set once at cmd init

func SetSpecWriteCallback(cb SpecWriteCallback) { onSpecWrite = cb }

func SavePair[T any](fsys FS, basePath string, obj T, body string) error {
    // ... existing logic ...
    if onSpecWrite != nil {
        onSpecWrite(inferKindFromPath(basePath), obj.ID, false)
    }
    return nil
}
```

Argument for the callback approach: every spec mutation must go through `SavePair` / `SaveMarkdown`. Centralizing the index hook here means a verb can't accidentally bypass indexing by routing through a different write path. The optional shape (`nil` = no-op) means tests and non-search contexts don't pay anything.

For deletes (refine `--supersede`), wire a similar `RemovePair` helper or pass `deleted=true` through the callback when the verb knows the node is being removed.

### CLI query syntax

Free-text by default, Bluge query-string syntax when reserved characters are present. Bluge's `bluge.NewQueryStringQuery` parses this natively:

- `auth` → tokenize-and-OR with BM25 ranking (typical case)
- `"row level security"` → phrase
- `auth*` → prefix
- `auth~2` → fuzzy with edit distance 2
- `kind:decision AND postgres` → field-scoped boolean
- `+postgres -mysql` → must / must-not

Help text gets a short "Advanced: quote for phrases, `*` for prefix" hint. Most operators will type free text and get good results without learning the advanced syntax.

### Output shape unchanged

`ListResult` / `ListHit` JSON shape stays identical. Score is still opaque — order is the API. Markdown rendering unchanged. JSON consumers (CLI scripting, MCP integration) see no break.

### Test rewrites

Existing list tests in [cmd/list_test.go](../cmd/list_test.go) fall into two groups:

1. **Smoke tests that assert "X ranks above Y" on small contrived fixtures.** These should mostly survive — BM25 produces the same ordering on these cases. Spot-check each and adjust as needed.
2. **Tests that pin specific score magnitudes** (e.g., asserting weightSummary > weightBody numerically). These have to be rewritten as ordering assertions or dropped — BM25 doesn't expose comparable absolute scores.

New tests to add:

- Index build + persist + load round-trip with fingerprint validation
- Fingerprint mismatch triggers rebuild
- Schema version mismatch triggers rebuild
- Concurrent CLI process retry (subprocess holds writer; second invocation retries and succeeds when first releases)
- Read-only fallback when writer is held by another process (list during refine simulation)
- Stemming: query "authentication" matches node whose Summary says "authenticate"
- Phrase: `"row level security"` matches RLS-related nodes; does not match nodes with the words scattered apart
- Prefix: `auth*` matches `dec-adopt-authentication-*`
- Kind filter: `kind:decision` AND'd with free-text scope
- BM25 IDF behavior: a rare token outranks a common token in the same document

### MCP `spec_search` tool — in scope

The agent-facing reason for landing this work at the small-corpus stage: prevent recall gaps from compounding as new features get imported and existing nodes get refined. Operators can grep; agents can't. The plumbing the index needs is the same whether the consumer is `list` or `spec_search`, so we register both in the same commit.

**Tool surface** (registered alongside `spec_list_manifest` / `spec_get` in [internal/agent/spec_tools.go](../internal/agent/spec_tools.go)):

```
spec_search(query string, kind?: string, limit?: int) → SpecSearchResult
```

- `query` — free-text or Bluge query-string syntax (same parser the CLI uses).
- `kind` — optional filter: `"feature"`, `"strategy"`, `"decision"`, `"bug"`, `"approach"`. Omitted means "all kinds."
- `limit` — optional cap on returned hits. Default 20; max 100 (clamped).
- Returns `{hits: [{id, title, kind, summary}], total_matches: N}`. Shape mirrors `SpecManifestEntry` for the per-hit payload; the wrapper adds `total_matches` so the agent knows when its result is a truncated view.

**When agents should reach for it vs `spec_list_manifest`:**

- `spec_list_manifest` stays as the "show me the structure of the spec" tool — small projects, broad scans, agents that need to enumerate.
- `spec_search` is the "find the relevant N nodes" tool — when the agent has a specific topic or area in mind. The reconciler checking for existing decisions matching an inline proposal, the architect_critic verifying a referenced id exists in context, the gap_analyst looking for prior coverage of a topic — all benefit from ranked top-N over flat enumeration.

**Agent prompt updates** (paired with this tool registration):

- `spec_reconciler.md` — mention `spec_search(query)` as the recommended way to check whether an inline decision matches an existing canonical one, instead of scanning the full manifest.
- `architect_critic.md`, `devops_critic.md`, `sre_critic.md`, `cost_critic.md`, `critic.md` — update the "spec-lookup tools" section to recommend `spec_search` for topic-scoped checks, `spec_list_manifest` only for full-graph scans.
- `gap_analyst.md` — same.
- `spec_architect.md`, `spec_feature_elaborator.md`, `spec_strategy_elaborator.md`, `spec_outliner.md` — recommend `spec_search` for "does this concept already exist?" checks during authoring.

The prompts stay backwards-compatible: agents that don't pick up the new recommendation keep working via `spec_list_manifest`. The new tool is additive.

**Why not deprecate `spec_list_manifest`:** enumeration is a legitimate need for small projects and for agents that genuinely want the full structural view. Removing it would force agents into search-shaped workflows even when scanning the full set is the right move. Both tools coexist.

### MCP fsnotify watcher — forward-looking

Not in this commit. The single-writer-per-mutation model in MCP (above) means each mutation produces a fresh index update, and the in-process Reader can be refreshed after each. Cross-process changes (CLI verb writing while MCP is up) need fsnotify on `.borg/spec/` to trigger Reader refresh in the MCP server.

Defer until: MCP + CLI concurrent use becomes a real workflow.

### Forward-looking: fuzzy "did you mean" on empty results

Bluge supports `query.SetFuzziness(...)` as a query modifier. If a free-text search returns zero hits, the renderer could re-run as a fuzzy query and present suggestions:

```
No matches for "authrntication".
Did you mean:
  - dec-adopt-workos (authentication)
  - feat-campaign-org-management (authenticate via Google OIDC)
```

Nice-to-have, not load-bearing. Defer until operator feedback says misspellings are a real frustration.

## Implementation phases

**Phase 1 — index infrastructure.** New `internal/search/` package. `BuildIndex(fsys)` walks `.borg/spec/` and constructs the Bluge index. `OpenIndex(fsys)` opens an existing on-disk index with fingerprint validation; rebuilds on mismatch. `Search(query, opts)` runs a Bluge query and returns `[]ListHit`. `OpenWriterWithRetry` with the retry schedule from the concurrency section, plus the lock-busy diagnostic that reads `bluge.pid` for the PID. No callers yet.

**Phase 2 — mutation hook.** Add `SpecWriteCallback` to `internal/specio`. Wire it into `SavePair`, `SaveMarkdown`, and a new `RemovePair` helper (cascade supersede needs the delete signal). Set the callback in `cmd/cli.go` init (or equivalent process-start point) so any verb that reaches `specio` writes also updates the index. No verb-level changes.

**Phase 3 — replace `list`, add read-only fallback.** Rewrite [cmd/list.go](../cmd/list.go)'s `RunList` to use `internal/search.Search`, opening a `Reader` directly from disk (never the writer). Delete the score functions (`scoreDecision`, etc.). Rewrite tests in [cmd/list_test.go](../cmd/list_test.go) and [cmd/list_summary_test.go](../cmd/list_summary_test.go) as ordering / capability assertions. Verify staleness behavior under concurrent-writer simulation.

**Phase 4 — `spec_search` MCP tool registration.** Add `spec_search(query, kind?, limit?)` to [internal/agent/spec_tools.go](../internal/agent/spec_tools.go) alongside the existing `spec_list_manifest` / `spec_get`. The tool body opens a `Reader` against the on-disk index (same path `list` uses) and returns the ranked top-N as `SpecSearchResult{hits, total_matches}`. Tool wiring follows DJ-094's pattern. Add schema example via `RegisterSchema`. Tests for the tool's input validation (limit clamping, kind whitelist, query non-empty) and output shape.

**Phase 5 — agent prompt updates.** Update the 19 agent prompts that already document `spec_list_manifest` / `spec_get` (reconciler, approach-regenerator, architect, the three elaborators, the five critics, gap_analyst, the three supersede agents, refiner, rewriter, synthesizer, scout, outliner) to recommend `spec_search` for topic-scoped lookups and reserve `spec_list_manifest` for full-graph scans. The wording should make the choice obvious — short examples per agent are better than a generic blurb.

**Phase 6 — DJ entry.** Write DJ-N covering: engine choice (Bluge), the in-`.locutus/` persistence model, the fingerprint-invalidation pattern, the read-vs-write concurrency split, the `spec_search` MCP tool as the agent-facing surface (with the rationale "ship the foundation and the agent benefit together, learning from DJ-094's propagation gap"), and the explicit remaining deferrals (MCP fsnotify watcher, fuzzy fallback).

Phases 1-5 ship as one commit; Phase 6 is the DJ in the same commit. Splitting earlier would leave the codebase in inconsistent states — Phase 4's `spec_search` tool depends on Phase 1's infrastructure, and Phase 5's prompt updates assume Phase 4's tool exists.

## Reversal criteria

Revert if:

- **Bluge's index format or API stability turns out to bite us** during the upgrade-treadmill. Mitigations exist (schema-version-triggered rebuild, fingerprint invalidation), but if version bumps consistently require code changes to mapping or query construction, the dep cost outweighs the benefit and we'd fall back to option B (improved heuristic in place).
- **The persistent disk cache turns out to cause more confusion than it saves time.** Specifically: operators editing `.borg/spec/` by hand and not understanding why `list` shows stale results, or `.locutus/spec_index/` getting committed accidentally despite being gitignored. Mitigation is the `rm -rf` escape hatch and a clearer "stale results, rebuild via X" diagnostic; if those don't suffice, revert to in-memory-only with the per-invocation rebuild tax accepted.
- **Cross-process collision UX turns out worse than expected** — the 1.75s retry budget is insufficient in practice, or the PID-bearing error message isn't actionable enough. Mitigations: longer retry budget, or hook MCP and CLI mutation coordination through a control channel.

## Reference

- DJ-094 — Spec-lookup tools for the reconciler (the LLM-facing precursor to this work)
- DJ-103 — History narrative cache in `.locutus/` (the `.locutus/` cache pattern this plan extends)
- DJ-114 — Authored `Summary` field (the curated text this index leverages)
- DJ-115 — Prerequisite layer (the dependency-management pattern this work may eventually plug into for index-staleness)
- [Bluge source — index/lock/lock_nix.go](https://github.com/blugelabs/bluge/blob/master/index/lock/lock_nix.go)
- [Bluge source — index/lock/lock_windows.go](https://github.com/blugelabs/bluge/blob/master/index/lock/lock_windows.go)
- [Bluge source — index/directory_fs.go](https://github.com/blugelabs/bluge/blob/master/index/directory_fs.go)
