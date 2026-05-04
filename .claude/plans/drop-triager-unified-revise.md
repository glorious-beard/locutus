# Drop the Triager, Unify the Revise Step

Supersedes DJ-092, DJ-095, DJ-097. Replaces the three-bucket
`spec_revision_triager` agent and its `RevisionPlan` schema with a
unified per-finding (or per-cluster) elaborator fanout. The four
"shapes" the triager produced (revised feature, revised strategy, new
feature, new strategy) collapse into a single output type — a partial
`RawSpecProposal` with exactly one entry — and the discrimination
between them is implicit in the output's shape.

## Why

The triager has been the failure point three times in three
iterations:

- **DJ-092** added the additions bucket because critics' "missing X"
  findings were being silently dropped.
- **DJ-095** rewrote the schema example to fix `[{}]` placeholder
  emissions on empty buckets.
- **2026-05-04 winplan run** dropped 25 of 40 critic findings: the
  schema example changes (omitempty stripped one of three array
  fields from the rendered example) caused Gemini Flash to emit only
  two arrays instead of three, and `additions` was the one it dropped.

Every iteration added complexity to the triager prompt to fix the
previous iteration's failure. The deeper issue is that **the triager
is doing two jobs** — routing findings AND deciding intent (revise vs
add, feature vs strategy) — and an LLM under attention pressure
loses one of them. The schema-pattern-matching pathology (model picks
two same-shape arrays, drops the differently-shaped one) is universal
across providers, severity inversely proportional to model tier.

If a workflow's correctness depends on a specific model tier handling
3-bucket routing correctly, the workflow is brittle. The unified
design removes the multi-bucket routing problem entirely.

## What replaces the triager

Critics emit a flat list of findings (already true today). The new
flow:

```
critics → findings (flat list)
      → mechanical dispatch (Go, no LLM)
            ├── findings mentioning feat-X by id  → grouped per id, dispatched to spec_feature_elaborator
            ├── findings mentioning strat-X by id → grouped per id, dispatched to spec_strategy_elaborator
            └── findings without id reference     → clustered by topic, dispatched to elaborators
      → per-cluster fanout (one elaborator call per cluster)
      → reconciler dedupes inline decisions and merges canonical proposal
```

Every finding lands in exactly one cluster. Every cluster produces
exactly one elaborator call. Every elaborator call emits a partial
`RawSpecProposal` with exactly one entry.

### Discrimination via output shape

| Output shape | Means |
|---|---|
| `features: [{id: "feat-X", ...}]` where `feat-X` exists in current proposal | revise feature |
| `features: [{id: "feat-Y", ...}]` where `feat-Y` is fresh | new feature |
| `strategies: [{id: "strat-X", ...}]` where `strat-X` exists | revise strategy |
| `strategies: [{id: "strat-Y", ...}]` where `strat-Y` is fresh | new strategy |

No `kind` field, no `mode` field, no typed buckets. The shape of the
elaborator's output discriminates. The assembler downstream knows
"id matches existing → cascade-rewrite parent" vs "id is new → mint
canonical" via the same logic it uses today for additions.

## The clustering step

Two cluster sources, kept separate so the cheap mechanical case
doesn't pay the LLM cost:

### 1. Mechanical clustering (Go, no LLM)

Regex-extract `feat-[a-z0-9-]+` and `strat-[a-z0-9-]+` from each
finding's text. If a finding mentions exactly one existing-node id,
it joins that node's cluster. If it mentions two or more, copy the
verbatim text into each (matches the triager's "one node per concern"
rule). Findings with no id references go to the LLM clusterer.

This recovers most of today's "one revise call per affected node"
batching efficiency for free.

### 2. LLM clustering (single call, simple schema)

For findings without id references, a cheap LLM call groups them by
topic. Schema:

```yaml
clusters:
  - topic: "infrastructure-as-code and CI/CD"
    findings:
      - "verbatim finding 1"
      - "verbatim finding 2"
  - topic: "observability and SLOs"
    findings: ["..."]
```

Mandate: every input finding ends up in exactly one cluster. The
clusterer cannot drop, paraphrase, or annotate findings — only group
them.

This is the minimum-viable replacement for the triager. Its job is
SIMPLER than the triager's:

- One decision dimension (which cluster), not three (bucket + kind +
  intent).
- One output array shape, not three differently-shaped buckets.
- No cross-reference to existing-node ids (those findings already
  routed in the mechanical step).
- No "should this be a feature or a strategy?" decision — the
  elaborator decides per cluster.

A balanced-tier model (Gemini Flash, Claude Haiku, GPT-4o-mini)
handles this reliably because the schema and the prose agree on shape.

### Cluster → elaborator dispatch

For each cluster of unmatched findings, decide which elaborator to
invoke:

- If the cluster's topic mentions a feature-y noun ("dashboard",
  "export", "view", a user-facing capability) → spec_feature_elaborator
- Otherwise (default) → spec_strategy_elaborator

Implemented as a Go heuristic: simple keyword list, defaulting to
strategy. If the heuristic picks wrong, the reconciler (or the next
refine pass) corrects. This isn't load-bearing — most "missing X"
findings are missing-strategy gaps.

Alternative: include `kind: "feature" | "strategy"` in the clusterer's
output schema. The clusterer decides per topic. Slightly more LLM
work, slightly more reliable. **Recommendation:** start with the Go
heuristic; promote to LLM-decided kind if the heuristic misroutes
materially in real runs.

## What gets removed

- `internal/scaffold/agents/spec_revision_triager.md` — deleted.
- `internal/agent/revision.go` — `RevisionPlan`, `NodeRevision`,
  `AddedNode` types deleted (or repurposed; see below).
- `extractFanoutItems` paths `revision_plan.feature_revisions`,
  `revision_plan.strategy_revisions`,
  `revision_plan.additions.features`,
  `revision_plan.additions.strategies` — deleted; replaced with
  `findings.clusters`.
- `internal/agent/projection.go` — `projectTriage` deleted;
  `projectReviseNode` and `projectAdditionElaborate` collapse into
  one `projectFindingCluster` (see below).
- `RegisterSchema("RevisionPlan", ...)` — deleted.
- Workflow steps `triage`, `revise_feature_additions`,
  `revise_strategy_additions` — deleted from
  `internal/scaffold/workflows/spec_generation.yaml`.

## What gets added

- `internal/scaffold/agents/spec_finding_clusterer.md` — new agent
  whose only job is grouping unmatched findings by topic.
- `internal/agent/findings.go` (new file) —
  `ClusterFindings(findings, existingIDs) FindingClusters` runs the
  mechanical cluster pass; the LLM clusterer runs as a workflow step
  and merges its output into the same `FindingClusters` shape.
- `RegisterSchema("FindingClusters", ...)` — schema for the
  clusterer's output.
- New workflow step `cluster_findings` between `critique` and
  `revise` (conditional on having any unmatched findings).
- Updated workflow step `revise` (singular): fanout over
  `findings.clusters`, dispatched to the right elaborator agent
  per-cluster (mechanical kind decision in Go).

## What gets updated

### Elaborator agents (`spec_feature_elaborator.md`, `spec_strategy_elaborator.md`)

Today's split between "revise mode" and "addition mode" addendums
collapses into a single mode: **address-this-cluster mode**. The
elaborator receives:

- the cluster's topic
- the cluster's verbatim findings
- the existing-nodes list
- (optional) a prior `RawFeatureProposal` / `RawStrategyProposal`
  block when the cluster targets an existing node

The elaborator emits one `RawFeatureProposal` / `RawStrategyProposal`
that addresses every finding in the cluster. The id is preserved
verbatim if the cluster targets an existing node; otherwise the
elaborator picks a slug-derived id.

The two distinct addendums in today's .md (revise-mode and
addition-mode) collapse into one because the decision rule is the
same: "if you got a prior block, preserve its id; otherwise pick
one." That rule lives in one paragraph, not two.

### Assembler (`assembleRevisedRawProposal`)

The merge logic stays largely the same:

- Per-finding outputs grouped by id.
- ID-matches-existing → revision (override the existing node).
- ID-is-fresh → addition (append).
- ID-collision among new ids (two clusters independently invented the
  same fresh id) → assembler logs and keeps the first; reconciler
  catches the rest.

Semantically-equivalent additions (two clusters about the same topic
producing different fresh ids) are NOT deduped at assembly time; the
reconciler's existing inline-decision dedup handles inline overlap,
and node-level overlap surfaces as duplicate strategies that the
next refine pass cleans up. **Out of scope for this plan:** node-level
semantic dedup at the reconciler. Add as a follow-up if real runs show
it's needed; the clustering step already handles 95% of the
"5 critics flagged the same gap" case by collapsing those 5 findings
into 1 cluster upstream.

### Workflow YAML

Before:

```yaml
- id: triage
  agent: spec_revision_triager
  conditional: has_concerns
- id: revise_features
  agent: spec_feature_elaborator
  fanout: revision_plan.feature_revisions
  conditional: has_concerns
- id: revise_strategies
  agent: spec_strategy_elaborator
  fanout: revision_plan.strategy_revisions
  conditional: has_concerns
- id: revise_feature_additions
  agent: spec_feature_elaborator
  fanout: revision_plan.additions.features
  conditional: has_additions
- id: revise_strategy_additions
  agent: spec_strategy_elaborator
  fanout: revision_plan.additions.strategies
  conditional: has_additions
```

After:

```yaml
- id: cluster_findings
  agent: spec_finding_clusterer
  conditional: has_unmatched_findings   # set by the mechanical pre-pass
- id: revise
  fanout: findings.clusters             # each cluster carries its dispatch agent
  conditional: has_concerns
```

The fanout dispatches to the elaborator named in the cluster's
metadata (set by the mechanical pre-pass for id-matched clusters and
by the Go heuristic for LLM-clustered ones). This requires extending
the workflow loader to support per-fanout-item agent selection. The
shape:

```go
type FindingCluster struct {
    Topic    string   `json:"topic"`
    Findings []string `json:"findings"`
    AgentID  string   `json:"agent_id"`  // "spec_feature_elaborator" or "spec_strategy_elaborator"
    NodeID   string   `json:"node_id,omitempty"` // set when targeting an existing node
}
```

The fanout dispatcher reads `cluster.AgentID` per item and routes
accordingly. This is a small executor change — `extractFanoutItems`
returns items, and `executeAgent` is parameterized on `cluster.AgentID`
instead of the step's static agent.

## Cost analysis

Today (when working): 1 triager call + ~7-15 elaborator calls per
revise round.

Proposed: 1 mechanical pre-pass (free) + 1 LLM clusterer call (cheap,
balanced tier) + ~7-15 elaborator calls per revise round.

Net delta: 1 extra LLM call (the clusterer) at balanced tier, ~$0.01.
The triager was strong-tier-eligible to fix the May-4 regression;
moving to clusterer (balanced tier) is actually cheaper.

The "more elaborator calls" framing from the previous design
discussion turned out to be wrong: clustering recovers the
per-affected-node batching, so the count stays roughly the same.
What changes is reliability — clustering's failure modes don't
silently lose findings.

## Tests

- **Mechanical-cluster test**: feed findings with id references,
  assert clustering by id and by elaborator dispatch.
- **LLM-clusterer snapshot test**: rendered prompt for the new
  agent; assert the schema example shows ALL clusters populated and
  the prose mandates lossless grouping.
- **Per-cluster elaborator snapshot test**: rendered prompt receives
  the cluster's findings + existing nodes; assert no triager-era
  contamination remains.
- **End-to-end stub test**: synthetic critic findings → workflow →
  assert per-finding outputs reconcile correctly (no dropped
  findings, no duplicate canonical strategies).
- **Regression test**: replay the May-4 winplan critic findings as a
  fixture; assert all 40 findings produce at least one revision or
  addition (no findings silently dropped).

## Decision-journal entry to add

```
DJ-098 (proposed): drop the spec_revision_triager. The three-bucket
RevisionPlan was a brittle shape-translation layer between critics
(free-form findings) and elaborators (typed schemas). Three iterations
of fixes (DJ-092, DJ-095, DJ-097) each surfaced a new failure mode at
the same boundary. Replaced with a mechanical pre-pass + a single-job
LLM clusterer + per-cluster elaborator fanout. The discrimination
between revise/add and feature/strategy moves from an upstream routing
decision to an implicit consequence of the elaborator's output shape.
This eliminates the routing-judgment problem entirely; cluster
correctness is provider-agnostic and tier-resilient.
```

## Sequencing

One PR. The change touches:

- `internal/scaffold/agents/`: delete `spec_revision_triager.md`,
  add `spec_finding_clusterer.md`, simplify
  `spec_feature_elaborator.md` and `spec_strategy_elaborator.md`
  (one mode, not two).
- `internal/agent/`: delete `RevisionPlan`/`NodeRevision`/`AddedNode`,
  add `FindingClusters`/`FindingCluster`, rewrite `findings.go`,
  delete `projectTriage`, rewrite `projectReviseNode` and
  `projectAdditionElaborate` as a single `projectFindingCluster`,
  update assembler.
- `internal/scaffold/workflows/spec_generation.yaml`: 5-step revise
  block becomes 2 steps.
- Tests: update snapshot tests, add regression fixture, update unit
  tests.

Not breaking the change into phases because the surface is
internally consistent — partial migration would require running both
the old triager and the new clusterer simultaneously, which doubles
the failure surface during the transition. The user has stated
multiple times the project isn't in alpha; full replacement is the
right approach.

## Out of scope

- **Node-level semantic dedup at reconcile time.** If two clusters
  produce semantically-similar new strategies with different ids
  (e.g. `strat-observability` and `strat-monitoring`), they survive
  to the persisted spec. The clustering step's job is to prevent
  this by clustering related findings together; if it fails, the
  next refine pass cleans up. Add reconciler-level node dedup as a
  follow-up if real runs show clustering misses material overlap.
- **Workflow YAML schema cleanup** (phases-with-jobs, see
  `workflow-schema-and-resume.md`). Independent change; can land
  before or after this one.
- **Resumable session traces** (Phase B of
  `workflow-schema-and-resume.md`). Independent.
- **Replacing the workflow engine with Temporal/Restate.** Discussed
  and rejected; orthogonal.
