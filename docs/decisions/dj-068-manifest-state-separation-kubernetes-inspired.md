## DJ-068: Manifest/State Separation — Kubernetes-Inspired Reconciliation Model

**Status:** shipped

**Decision:** The spec graph (desired state / manifest) and the runtime state store (observed state) are separate concerns. The spec graph is immutable-ish desired state; the state store is a mutable record of what has actually been reconciled.

**State store entry shape:**

```yaml
approach_id: oauth-login           # always an Approach node ID — only Approaches own artifacts
spec_hash: sha256:abc123           # hash of the Approach spec node at last reconcile
artifacts:                         # path → sha256; per-file for granular drift detection
  src/auth/oauth.go: sha256:def456
  src/auth/oauth_test.go: sha256:789abc
status: live
last_reconciled: 2026-04-20T14:32:00Z
workstream_id: ws-2                # last workstream that planned this Approach; N Approaches share one WorkstreamID
```

Only `Approach` nodes have state store entries. `Feature` and `Strategy` nodes derive their status from their Approach children — if all Approaches under a Feature are `live`, the Feature is implicitly live. Goals derive from their Feature and Strategy children. No state store entries are created for Features, Strategies, Decisions, or Goals.

`artifacts` is a `map[string]string` (path → SHA-256) rather than a single aggregate hash. A single hash would require re-hashing every file on every reconciliation check with no way to report which specific file drifted. Per-path hashes let the reconciler identify exactly which artifact changed, report it precisely in `out_of_spec` output, and enable future optimisations (mtime pre-check before re-hashing unchanged files). Paths double as the artifact list, so no separate `artifact_paths` field is needed.

**State store lives in-repo** (`.locutus/state/*.yaml`). Version-controlled, diffable, auditable via `git log`. Consistent with the spec-as-source-of-truth principle.

**Spec node lifecycle:**

- `unplanned` — spec exists; not covered by any workstream. Valid long-term resting state.
- `planned` — included in the master plan; topologically sorted; agent not yet dispatched.
- `in_progress` — agent dispatched and working.
- `live` — reconciler ran tests and they passed. The asserting of actual state.
- `failed` — reconciler ran tests and they failed, or agent errored. Routes back to `planned` for retry.
- `drifted` — `spec_hash` changed since last reconcile (spec is newer than artifacts; forward drift). Routes to `planned`.
- `out_of_spec` — `artifact_hash` changed outside Locutus (code edited manually; backward drift). Surfaces for human review with three resolution paths: (1) update or create a spec node to cover the change, then re-plan; (2) accept the change as a fix and mark `live`; (3) revert the artifact and re-reconcile from spec.

**The reconciler asserts `live` or `failed`** by running tests — not by checking that code was written. This is the mechanism that makes the state store an honest account of the system's actual condition.

**Dependency resolution at plan creation time:** When a spec node is added to the master plan, the planner walks its full transitive dependency subgraph, collects all non-`live` nodes, and topologically sorts the resulting set into the workstream. The user never needs to manually include dependencies; the planner discovers them. `unplanned` is not a warning state — nodes sit there until they become reachable from an active workstream.

**History is a separate concern.** The spec graph reflects only active desired state. The history agent captures what changed, when, and why. There is no `superseded` or `deprecated` state in the state store — outdated nodes are removed or replaced, and the history agent holds the record. This matches the Kubernetes model: the manifest shows current desired state; audit history lives elsewhere.

**Why this separation:** Without a distinct state store, the spec graph does double duty as both desired state and operational status. This conflation makes drift detection, reconciliation targeting, and workstream planning harder than it needs to be. The separation gives the reconciler a clean loop: diff spec_hash vs. stored spec_hash, diff artifact_hash vs. stored artifact_hash, act on the result.

**Alternatives considered:**

- Encode status directly on spec nodes — rejected because it mixes desired state with observed state, making the spec graph both harder to read and harder to version cleanly.
- Out-of-repo state store (local SQLite or similar) — rejected in favor of in-repo YAML. In-repo state is diffable, survives repo clones, and participates in the same version control as the spec.
