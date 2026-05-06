# Plan: comprehensive spec snapshot

## Context

Today `locutus status` reports counts only — Features, Bugs, Decisions, Strategies, Entities, plus an orphan-count warning. `--json` emits the same `StatusData` shape. There is no command that produces a coherent end-to-end view of the spec graph; reading the project's plan today requires `cat`-ing the `.borg/spec/` directory, mentally cross-referencing decision IDs back to features and strategies, and inferring implementation state from approach files plus `.locutus/workstreams/` activity.

The DX cost is real: a 41-decision / 17-strategy / 7-feature spec is at the threshold where directory grep stops being adequate.

This plan is the foundation for two follow-ons (`explain` and `justify`) — both depend on the per-node renderer this plan introduces.

## Design

### CLI surface

Extend the existing `status` verb with two flags:

```
locutus status --full --format markdown     # narrative spec doc to stdout
locutus status --full --format json         # full graph as JSON to stdout
locutus status --full --format json --kind decision   # filter to one kind
locutus status --full --format markdown --status proposed  # filter by status
```

Defaults: `--format` defaults to `markdown` when `--full` is set; without `--full` the legacy counts-only output stands. `--kind` and `--status` are repeatable / OR'd within and AND'd across categories.

Choosing flag-on-existing-verb over a new `snapshot` verb because:
- The "comprehensive view" IS the natural extension of "status."
- New verbs raise the surface area; `--full` keeps the surface compact.
- `locutus status` is already in muscle memory for users.

### Output format: Markdown narrative

Ordering is intentional — read top to bottom and you get a real spec document:

```markdown
# <project name> — Specification snapshot

## Status

| Category   | Total | Proposed | Active | Inferred | Assumed |
|------------|-------|----------|--------|----------|---------|
| Features   | 7     | 7        | 0      | 0        | —       |
| Strategies | 17    | 17       | 0      | 0        | —       |
| Decisions  | 41    | 41       | 0      | 0        | 0       |
| Approaches | 0     |          |        |          |         |
| Bugs       | 0     |          |        |          |         |

**Implementation stages:** drafted: 24 · planned: 0 · implementing: 0 · done: 0 · drifted: 0

## Goals

> [verbatim from .borg/GOALS.md]

## Strategies

### Foundational

#### `strat-data-persistence` — Primary data store *(proposed · drafted)*

[strategy body verbatim from .md]

**Linked decisions (2):**
- `dec-use-clickhouse-cloud-...` — Use ClickHouse Cloud for massive voter file analytics
- `dec-use-postgresql-16-...` — Use PostgreSQL 16 on AWS Aurora Serverless v2 for OLTP

**Referenced by features:** feat-voter-crm, feat-win-modeling

---

### Quality
[same shape as Foundational]

### Derived
[same shape]

## Features

### `feat-turf-cutting` — Geospatial turf cutting *(proposed · drafted)*

[feature description verbatim from .md]

**Acceptance criteria:**
- [bullet list]

**Linked decisions (2):**
- `dec-generate-frontend-map-tiles-...` — Generate frontend map tiles dynamically via PostGIS ST_AsMVT
- `dec-use-mapbox-gl-js-...` — Use Mapbox GL JS for client-side rendering

**Relevant strategies (3):**
- `strat-geospatial-processing`
- `strat-data-persistence`
- `strat-frontend-architecture`

**Approaches:** none yet

---

[repeat per feature]

## Decisions

### `dec-adopt-datadog-for-unified-observability` *(proposed)*

**Title:** Adopt Datadog for unified observability

**Rationale:**
[rationale prose]

**Confidence:** 0.85

**Alternatives:**
- **Sumo Logic** — *Mature SIEM and log analytics platform.* Rejected because: cost scales unfavorably with log volume.
- **DIY ELK + Prometheus** — *Open-source flexibility.* Rejected because: operational overhead exceeds team capacity.

**Citations:**
- `best_practice` — *SRE Book Ch 6: Monitoring Distributed Systems*
- `goals` — *GOALS.md §4: cost discipline*

**Architect rationale:**
[architect_rationale prose]

**Referenced by:**
- Strategies: `strat-observability-incidents`
- Features: (none directly)
- Influenced by: (none) / Influences: (none)

---

[repeat per decision, alphabetized]

## Approaches

[only present if approaches exist; otherwise the section is omitted]

## Bugs

[only present if bugs exist]

## Validation

[any orphan refs, dangling decisions, or hash drift. Empty subsection if clean.]
```

The "**Referenced by**" / "**Relevant strategies**" lines are computed back-references — the spec stores forward references only (`feature.decisions = [id, …]`, `strategy.decisions = [id, …]`), so the renderer walks the whole graph once to build the inverse index before rendering.

### Output format: JSON

Same data, no prose framing. Stable shape so tooling can diff between runs:

```json
{
  "project_name": "winplan",
  "generated_at": "2026-05-06T14:42:00-07:00",
  "status_counts": {
    "features": {"total": 7, "proposed": 7, "active": 0, "inferred": 0, "removed": 0},
    "decisions": {"total": 41, "proposed": 41, "active": 0, "inferred": 0, "assumed": 0},
    "strategies": {"total": 17, "proposed": 17, "active": 0, "inferred": 0},
    "approaches": {"total": 0},
    "bugs": {"total": 0}
  },
  "implementation_stages": {
    "drafted": 24, "planned": 0, "implementing": 0, "done": 0, "drifted": 0
  },
  "goals": "...",
  "strategies": [
    {
      "id": "strat-data-persistence",
      "title": "Primary data store",
      "kind": "foundational",
      "status": "proposed",
      "implementation_stage": "drafted",
      "body": "...",
      "decisions": ["dec-…", "dec-…"],
      "referenced_by_features": ["feat-voter-crm", "feat-win-modeling"]
    },
    ...
  ],
  "features": [
    {
      "id": "feat-turf-cutting",
      "title": "Geospatial turf cutting",
      "status": "proposed",
      "implementation_stage": "drafted",
      "description": "...",
      "acceptance_criteria": ["..."],
      "decisions": ["dec-…"],
      "approaches": [],
      "relevant_strategies": ["strat-geospatial-processing", "..."]
    },
    ...
  ],
  "decisions": [
    {
      "id": "dec-adopt-datadog-…",
      "title": "Adopt Datadog for unified observability",
      "status": "proposed",
      "rationale": "...",
      "confidence": 0.85,
      "alternatives": [...],
      "citations": [...],
      "architect_rationale": "...",
      "influenced_by": [],
      "influences": [],
      "referenced_by": {
        "strategies": ["strat-observability-incidents"],
        "features": []
      }
    },
    ...
  ],
  "approaches": [],
  "bugs": [],
  "validation": {
    "dangling_decision_refs": [],
    "orphans": [],
    "hash_drift": []
  }
}
```

Implementation note: the JSON form is **always** built first; the Markdown renderer consumes the JSON struct. This guarantees they cover the same data.

### Implementation stage derivation

The five derived stages, computed per spec node from existing data:

| stage | condition |
|---|---|
| `drafted` | node exists; no Approach references it OR no approach is `active` |
| `planned` | at least one Approach (status=`proposed` or `active`) references this feature/strategy AND no `.locutus/workstreams/<approach-id>/` directory exists |
| `implementing` | a `.locutus/workstreams/<approach-id>/` directory exists |
| `done` | approach is `active` AND `approach.SpecHash` matches the current node's hash AND no workstream dir exists |
| `drifted` | approach is `active` AND `approach.SpecHash` does NOT match the current node's hash |

Decisions inherit their stage from the most-advanced feature/strategy that references them (decisions don't have approaches directly).

The derivation lives in a new `internal/spec/stage.go`; it takes the spec graph + `.locutus/workstreams/` listing and produces a per-node stage map.

### File layout

**New:**
- `internal/spec/graph.go` — `LoadGraph(fsys) (*SpecGraph, error)` that loads features/strategies/decisions/approaches/bugs into one in-memory graph with inverse-index helpers (`g.FeaturesReferencingDecision(id)`, `g.StrategiesReferencingDecision(id)`, `g.DecisionsInfluencingDecision(id)`).
- `internal/spec/stage.go` — per-node implementation-stage derivation.
- `internal/render/spec.go` — per-node Markdown renderer (one function per kind: `RenderFeature`, `RenderStrategy`, `RenderDecision`, `RenderApproach`, `RenderBug`). The snapshot renderer composes these.
- `internal/render/snapshot.go` — full-snapshot Markdown renderer (table of contents + sections).
- `cmd/snapshot.go` — `gatherSnapshotData(fsys) (*SnapshotData, error)` and the JSON marshaller. (Keep `cmd/status.go` light; add the `--full` flag wiring there but delegate to the new gatherer.)

**Modified:**
- `cmd/status.go` — extend `StatusCmd` with `Full bool`, `Format string`, `Kind []string`, `Status []string`. Branch on `Full`: existing path stays; `--full` calls `gatherSnapshotData` and routes to the chosen format.

**Tests:**
- `internal/spec/graph_test.go` — fixture spec dir, assert inverse index correctness and dangling-ref detection.
- `internal/spec/stage_test.go` — table-driven across the five stage conditions.
- `internal/render/spec_test.go` — golden-file tests for per-node rendering across all kinds.
- `internal/render/snapshot_test.go` — golden-file test for the full-snapshot Markdown output against a fixture graph.
- `cmd/status_test.go` — extend with `--full --format json` round-trip and `--full --format markdown` smoke test (no LLM, deterministic).

### Performance and size

A spec with 100 features / 200 decisions / 50 strategies fits in <1 MB of JSON. Markdown form is similarly bounded. No streaming required — single buffered write.

`LoadGraph` is single-pass over the directory; O(N) where N is total node count. Inverse index built once during load. Cached in-memory for the duration of one command invocation.

For very large specs (thousands of nodes) the Markdown gets unwieldy — at that point per-feature filtering via `--kind` / `--status` is the answer. A future `--feature <id>` filter could constrain the snapshot to one feature's slice of the graph.

## Verification

- Golden-file tests for the full-snapshot output against a reference fixture (the winplan-shape from this DJ-099 run is a good seed).
- `--format json | jq` extracts well-known paths (`.features[0].id`, `.decisions | map(.id) | length`).
- Round-trip: snapshot → JSON → reload as `SnapshotData` → snapshot → byte-identical.
- `locutus status --full --format markdown | wc -l` over a winplan-sized spec is in the 500–1500-line range; readable in a single editor pane.

## MCP exposure

Following the existing pattern in `cmd/mcp.go` — every CLI verb has a
1:1 MCP tool counterpart — the `status` MCP tool (already registered)
gains the same `full` / `format` / `kind` / `status` parameters as the
CLI flag. One handler shared between CLI and MCP via the
`gatherSnapshotData` core; only the I/O wrapping differs.

**MCP tool input shape:**

```go
type statusInput struct {
    Full   bool     `json:"full,omitempty"`
    Format string   `json:"format,omitempty"`  // "markdown" | "json"; default markdown when full
    Kind   []string `json:"kind,omitempty"`    // optional kind filter
    Status []string `json:"status,omitempty"`  // optional status filter
}
```

**Output:** for `format=markdown` the result is a single `text` content
block carrying the rendered Markdown. For `format=json` the result is
a `text` content block with the JSON as its body (clients deserialize)
— the MCP SDK's structured-result path is fine but text-as-JSON keeps
the contract uniform across formats and matches how `history` and
`status` already return data today.

**Why an MCP-callable snapshot matters:** an agentic client (Claude
Code, Cursor, etc.) connected to a project's `locutus mcp` server can
ingest the full spec in one tool call before answering "what's this
project about?" or "what decisions cover the auth strategy?" — without
having to read the directory tree. The `--kind` / `--status` filters
keep the response size manageable when only a slice is needed.

Tool registration goes in `cmd/mcp.go` alongside the existing tool
registrations; the handler is a thin wrapper around `gatherSnapshotData`
plus format selection.

## Sequencing

This plan ships first because the per-node renderer (`internal/render/spec.go`) is consumed by the `explain` plan and the renderer behind `refine --diff`. Build it once, reuse twice.

## Out of scope

- HTML / PDF output formats. Markdown is the lingua franca; downstream tooling can convert.
- Filtering by feature → walking transitive dependencies (only the feature's directly-linked decisions appear). A `--feature <id> --transitive` flag would be a future extension.
- Diffing two snapshots. `git diff` between two `--format json` outputs covers most cases; a structured diff verb is a separate plan.
- Snapshot persistence under `.borg/snapshots/`. Output goes to stdout; piping to a file is the user's choice. Persisted snapshots add a sync surface.
