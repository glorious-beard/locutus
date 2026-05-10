# Spec Node Supersession via `refine --supersede`

Closes the justify→refine handoff bug discovered 2026-05-09 in winplan: a
`justify --against` returned **BROKE DOWN** with three breaking points
against `dec-adopt-auth-js-nextauth-with-google-oidc-and`; the
suggested next step was `refine --brief`; the refiner ran, rewrote the
downstream feature prose, and left the decision file untouched at
confidence 0.9 with the original alternatives. The cascade event then
asserted "All applicable Decisions remain accurately represented" in
the same session where the verdict said the opposite.

The cause is structural: the refiner agent
([internal/scaffold/agents/refiner.md](internal/scaffold/agents/refiner.md))
is constitutionally forbidden from touching Decisions — "while keeping
every applicable Decision accurately represented." On a decision
target, `refine --brief` is a cascade-to-features operation. There is
no path in today's verb set from a BROKE DOWN verdict to actually
mutating the decision.

The same gap applies to Features and Strategies. A Feature can be
wrong-shaped in ways prose-rewriting can't fix — framed against a
stale goal, scoped too broadly, identity needing replacement rather
than refinement. A Strategy can shift wholesale (microservices →
modular monolith) in a way that's not a textual edit. `--brief`
edits prose; identity replacement needs its own path.

Bugs are excluded. The existing status lifecycle (`reported →
triaged → fixing → fixed`) plus "file a new bug against the right
root cause" already covers the rename-the-mistake case for bugs.

## Target shape

```
locutus refine <id> --supersede "<motivation>"
```

Where `<id>` resolves to a Decision, Feature, or Strategy. Bugs are
rejected with a hint pointing at the existing status-transition path.

The motivation string carries the same role `--brief` plays in the
existing cascade flow: it's the user's authoritative directive
explaining why the node must be replaced. `--supersede` and
`--brief` are mutually exclusive flags — pick one mode or the other.

Behavior (kind-aware):

1. A `refiner-supersede-<kind>` agent emits a replacement node — new
   title, new rationale, kind-specific structured fields refreshed
   (alternatives + confidence for Decisions; acceptance_criteria +
   description for Features; prerequisites + skills for Strategies),
   `influenced_by` / linked-decisions / linked-approaches carried
   forward where they still apply.
2. The new title's slug derives the new id.
3. **If the new slug differs from the old:** write the new node file,
   delete the old one, cascade through every reference shape (see
   table below), and append a `node_superseded` history event.
4. **If the new slug matches the old (rare, same-headline revision):**
   edit the node file in place. No id change, no cross-node cascade
   rewrites. Still delete affected Approaches. Still append a
   history event with `in_place: true` and `replacement_id ==
   superseded_id`.

The same-slug case is the cheap escape hatch from naming-collision
mechanics. We don't dedupe-suffix; we treat slug equality as the
signal that this is a content revision, not a replacement.

## Verb-set fit

Stays inside DJ-101's 8 mutating + 2 read-only cap. `--supersede` is a
flag on `refine`, sibling to `--brief` / `--diff` / `--rollback`. No
new verb.

A separate `supersede` verb was rejected because:
- 8-verb cap.
- The user mental model is "I want to refine this decision," not "I
  want to invoke a different operation type." `--supersede` reads as
  "refine, but for real this time."

## Cascade rules

Different reference shapes are affected depending on which kind is
superseded.

| Reference shape                         | Decision superseded                                                 | Feature superseded                                | Strategy superseded         |
|-----------------------------------------|---------------------------------------------------------------------|---------------------------------------------------|-----------------------------|
| `Feature.Decisions[]`                   | rewrite old → new id                                                | unchanged                                         | unchanged                   |
| `Strategy.Decisions[]`                  | rewrite old → new id                                                | unchanged                                         | unchanged                   |
| `Decision.InfluencedBy[]`               | rewrite old → new id                                                | unchanged                                         | unchanged                   |
| `Bug.FeatureID`                         | unchanged                                                           | rewrite old → new id                              | unchanged                   |
| `Approach` with target in `Decisions[]` | **invalidate the Approach**                                         | n/a                                               | n/a                         |
| `Approach` with `ParentID == old id`    | n/a                                                                 | **invalidate the Approach**                       | **invalidate the Approach** |
| Downstream prose                        | refiner re-pass on Features/Strategies that referenced the decision | refiner re-pass on Bugs filed against the feature | no automated prose cascade  |

In the in-place column (same slug, same id), only the Approach
invalidations apply. No id rewrites are needed because the id didn't
change.

**Why Approaches are invalidated, not deleted.** An Approach in
`done` stage carries a SpecHash that ties it to the artifacts the
coding agent produced (DJ-072). That linkage is what `status` reads
to detect drift, and what an `adopt` re-run uses to know "this code
already matches this spec." Deleting the approach throws away that
linkage and orphans the artifact files in the repo — the next adopt
synthesizes a fresh approach with no awareness that prior code
already exists from the previous pass.

Invalidation preserves the linkage as the "blast radius" input to
the next adopt run. The invalidated approach's `ArtifactPaths`,
`Body`, `Assertions`, and `Decisions[]` stay on disk. Adopt reads
them when synthesizing the replacement, producing a new approach
whose Body explicitly addresses both forward (the new spec) and
backward (cleanup or migration of prior artifacts).

This maps cleanly to the reconcile-loop boundary the verb set is
already organised around: `refine --supersede` is pure spec
mutation, `adopt` is the controller that brings code into alignment
with spec. Eagerly regenerating approaches inside supersede would
violate that boundary and bloat what should be a fast, synchronous
operation — at winplan scale a heavily-cited decision could
invalidate 10+ approaches, each requiring its own LLM synthesis
call to regenerate eagerly.

The same-slug in-place case applies the invalidation rule for the
same reason: structured fields changed materially, so the SpecHash
linkage is suspect. Better to force a reconcile pass than to claim
matching state we can't verify.

**Why no prose cascade for strategy supersede.** Strategies are
stand-alone in the typed model — nothing in Feature/Decision/Bug has
an explicit `strategy_id` reference. Soft prose mentions in
unrelated nodes are unverifiable; chasing them is fuzzy work that
the refiner can't do reliably. The user can spot-check via
`locutus list` if needed.

## Approach invalidation mechanics

Add one field to [`internal/spec/approach.go`](internal/spec/approach.go):

```go
type Approach struct {
    // ... existing fields ...

    // InvalidatedByEventID points at the supersede history event
    // that invalidated this approach. Empty when valid. Set by
    // refine --supersede when the approach's parent or any
    // referenced decision is replaced. Adopt reads the referenced
    // event to understand why the approach is stale and what the
    // replacement target should be.
    InvalidatedByEventID string `yaml:"invalidated_by_event_id,omitempty"`
}
```

Status is implied by presence/absence of the field. No separate
`Status` enum — the event id is the single source of truth for
"why was this invalidated" and a derived `IsInvalidated()` method on
Approach is enough for filter logic. This keeps the no-aspirational-
fields rule honest: the field has one consumer (adopt's
synthesis branch) plus three rendering callers (`list`, `explain`,
`status`).

Validator behaviour: an invalidated Approach is not flagged as a
dangling-reference target by `Loaded.findDanglingRefs`. A reference
to an invalidated approach from a Feature.Approaches[] /
Strategy.Approaches[] is still valid — the file exists, just in an
intentionally stale state.

## Adopt's regeneration branch

When `adopt` finds an invalidated Approach for a parent it's
processing, it routes through a new synthesis branch instead of the
fresh-synthesis path. The branch agent receives:

- The current parent Feature/Strategy and its current Decisions
  (post-supersede state).
- The invalidated approach's `ArtifactPaths`, `Body`, `Assertions`,
  and `Decisions[]` from before invalidation.
- The supersede event referenced by `InvalidatedByEventID`,
  including `motivation`, `superseded_id`, `replacement_id`.

The branch agent emits a new approach whose `Body` explicitly
covers both directions:

- **Forward:** what the coding agent must build to satisfy the new
  spec.
- **Backward:** what existing files from `ArtifactPaths` must be
  modified, replaced, or deleted to align with the new spec. Some
  files may carry forward unchanged; some may need substantial
  rewrites; some may be deleted entirely (e.g. NextAuth-specific
  middleware after a switch to WorkOS).

The new approach replaces the invalidated one on disk. The old
SpecHash is gone; a fresh hash is computed once the coding agent
runs and produces artifacts. The reconcile loop closes when adopt
exits with the new approach in `done` stage.

This is a meaningful adopt-side change with its own agent prompt —
not a one-liner. The agent definition lives at
[`internal/scaffold/agents/approach-regenerator.md`](internal/scaffold/agents/approach-regenerator.md)
(or similar) and is registered alongside the existing approach
synthesizer in adopt's planning step.

Two-agent flow per supersede:

1. `refiner-supersede-<kind>` emits the replacement node.
2. Existing `refiner` runs once per affected downstream node (the
   prose-cascade column above) with the motivation as the brief
   argument. Existing agent — no modification needed; it's still
   operating on Feature/Strategy/Bug prose with current references.

## Why deletion over `status: superseded`

A tombstoned node serves three potential purposes:

1. Audit trail of what was previously decided.
2. Backstop for justify against the old node.
3. Validator stability for any historical reference still in flight.

Each is better served elsewhere:

1. **`git log .borg/spec/<kind>/<id>.md`** is the durable record of
   the prior content. Git is built for this exact use case.
2. **`.borg/history/evt-*.json`** captures the supersession event
   with motivation, breaking points, and cascade scope (DJ-103). The
   `locutus history` verb queries it.
3. **No historical reference should remain in flight after cascade.**
   The cascade is total: every Feature/Strategy/Decision pointer
   rewritten, every affected Approach deleted, every Bug repointed.
   After cascade, nothing in the spec graph references the old id. A
   `status: superseded` flag on a node nobody references is graph
   noise.

Tombstones additionally pollute discovery. At winplan's current 208
decisions and a year of supersession churn, the difference between
"208 live + 0 dead" and "208 live + 30 dead" is real for `locutus
list` quality.

## refiner-supersede agents

Three new agent definitions, one per replaceable kind, mirroring how
the council uses `spec_feature_elaborator` and
`spec_strategy_elaborator` as separate agents:

- [internal/scaffold/agents/refiner-supersede-decision.md](internal/scaffold/agents/refiner-supersede-decision.md)
- [internal/scaffold/agents/refiner-supersede-feature.md](internal/scaffold/agents/refiner-supersede-feature.md)
- [internal/scaffold/agents/refiner-supersede-strategy.md](internal/scaffold/agents/refiner-supersede-strategy.md)

Each differs from the existing `refiner.md` in:

- Allowed to emit a new node of its own kind (with the kind-specific
  structured fields).
- Receives the old node in full, the user motivation, and the
  justify session id if one preceded the supersede call (so the
  agent can lift breaking-point analysis verbatim into the new
  structured fields rather than rephrasing).
- Output schema: kind-specific —
  `RewriteDecisionResult` / `RewriteFeatureResult` /
  `RewriteStrategyResult`. Each contains the replacement node plus
  an `architect_rationale` summarising the supersession reasoning.

Kind-specific constraints:

- **Decision agent.** The new alternatives section must include every
  alternative from the old decision plus the option that prompted
  supersession (the canonical winplan case: WorkOS gets added).
- **Feature agent.** The new acceptance_criteria must cover every
  criterion from the old feature that the motivation does not
  explicitly retire. Status carries forward unless the motivation
  changes it.
- **Strategy agent.** The new prerequisites and skills lists carry
  forward unless the motivation explicitly drops them. Strategy
  kind (foundational / quality / etc.) carries forward unless the
  motivation changes it.

None of the three agents edit cascade targets. Cascade is mechanical
for references and delegated to the existing refiner for downstream
prose.

## History event

```json
{
  "id": "20260509T120953-001-node-superseded",
  "timestamp": "2026-05-09T12:09:53-07:00",
  "kind": "node_superseded",
  "node_kind": "decision",
  "superseded_id": "dec-adopt-auth-js-nextauth-with-google-oidc-and",
  "replacement_id": "dec-adopt-workos-for-auth-and-org-management",
  "in_place": false,
  "motivation": "Address: WorkOS was never evaluated; ...",
  "cascaded": {
    "features_decisions_rewritten": ["feat-campaign-org-management"],
    "strategies_decisions_rewritten": [],
    "decisions_influenced_by_rewritten": [],
    "bugs_feature_id_rewritten": [],
    "approaches_invalidated": ["app-campaign-auth-bootstrap"]
  },
  "rationale": "<refiner-supersede architect_rationale>",
  "justify_session": ".locutus/sessions/20260509/120800/.../session.yaml"
}
```

`node_kind` is one of `decision` / `feature` / `strategy`. Only
cascade buckets that apply to the kind are populated; the rest stay
empty.

For the in-place case: `in_place: true`, `replacement_id ==
superseded_id`, all reference-rewrite buckets empty (only
`approaches_invalidated` populated).

`approaches_invalidated` is the durable record of which approaches
got their `InvalidatedByEventID` set to this event. The id-pointer
forms a closed loop: the event lists the approaches it invalidated,
each invalidated approach points back at the event.

`justify_session` is a non-load-bearing pointer (DJ-085 pattern). The
durable record of the breaking points lives in the new node's
structured fields + provenance, not in the session file.

## Atomicity and rollback

A single supersede operation touches up to six file kinds:

- One node delete + one node write (or one in-place rewrite) for the
  superseded target.
- N feature rewrites (id-reference updates + prose pass).
- M strategy rewrites (id-reference updates).
- K decision-influenced-by rewrites.
- B bug rewrites (FeatureID + prose pass, only on feature supersede).
- P approach invalidations (`InvalidatedByEventID` field set).
- One history event append.

Use the same staging pattern existing `refine` uses
(`specio.AtomicWriteFile` per file, but batched). On any pre-commit
failure: drop the staged set, leave on-disk untouched. Pre-commit
failure modes:

- refiner-supersede agent returns an empty or schema-invalid
  replacement.
- Cascade refiner fails on any downstream prose pass.
- Approach invalidation write fails (concurrent modification).

Rollback semantics:

- **Within the same commit (no `git commit` yet):** user runs
  `git reset --hard HEAD`. Locutus rollback is unnecessary.
- **After `git commit`:** `locutus refine --rollback` extends to
  multi-file restore. The history event already carries the rationale
  and cascade scope; the per-file pre-state bytes need to be
  captured at supersede time so rollback can restore them. This means
  the existing `.borg/history/evt-*.json` per-event format extends
  with a `pre_state` blob keyed by file path. That's a non-trivial
  bytes-on-disk addition — keep an eye on it.

## Rendering invalidated approaches

The three read-only views need to surface invalidation distinctly so
the user can see outstanding reconcile work without filtering
through every approach.

- **`locutus list`** — invalidated approaches appear in results with
  an `[invalidated]` badge after the kind tag:
  `app-foo-bar (approach) [invalidated] — Title`. No default
  filtering; the user wants to see them as part of "what needs
  reconciling."
- **`locutus explain`** — render the approach's last-valid content
  with a top banner above the per-node section:
  `> ⚠ Invalidated by event 20260509T120953-001-node-superseded.
  Run \`locutus adopt\` to regenerate.` Pure render, no LLM,
  consistent with DJ-101.
- **`locutus status`** — invalidated approaches roll up under a new
  `## Pending reconcile` section, distinct from `## Drifted`.
  Drifted means code drifted from a valid spec; invalidated means
  spec drifted under valid code. Both need adopt; the headline tells
  the user which is which.

## justify integration

`justify`'s "Suggested next step" output renderer (today emits a
`refine --brief "..."` command) needs to route on verdict + target
kind:

| Verdict        | Target kind                     | Suggested next step                            |
|----------------|---------------------------------|------------------------------------------------|
| BROKE DOWN     | decision \| feature \| strategy | `refine <id> --supersede "<breaking points>"`  |
| BROKE DOWN     | bug                             | `refine <id> --brief "..."` (existing)         |
| held / nuanced | any                             | `refine <id> --brief "..."` (existing)         |

Bugs route to `--brief` even on BROKE DOWN because the supersede
path doesn't accept bugs. The renderer also has the option of
suggesting a status transition (`reported → triaged` etc.) when the
verdict is bug-shaped, but that's deferred to a follow-up.

This is a one-function change in the justify renderer; no new agent.
The breaking-points string is already produced by the advocate's
verdict block today.

## Tests

Loader-shaped tests, no LLM, one set per replaceable kind:

**Decision supersede:**

- New slug: fixture with two features, one strategy, one approach
  referencing `dec-foo`. Run cascade with target `dec-bar`. Assert
  dec-foo deleted, dec-bar written, both features rewritten, strategy
  rewritten, approach's `InvalidatedByEventID` populated with the new
  event id, approach file otherwise unchanged, history event lists
  all four.
- In-place (same slug): dec file rewritten in place, feature/strategy
  id-references untouched, approach invalidated.
- Approach with `dec-foo` plus three other decisions in `Decisions[]`:
  the Approach is invalidated (its `Decisions[]` list and
  `ArtifactPaths` stay intact for the next adopt to read).
- No downstream references: `dec-old → dec-new` with empty cascade
  buckets; history event still written.

**Feature supersede:**

- New slug: fixture with two bugs filed against `feat-foo` and one
  approach with `ParentID: feat-foo`. Run cascade with target
  `feat-bar`. Assert feat-foo deleted, feat-bar written, both bugs
  repointed (`FeatureID: feat-bar`), approach invalidated (parent
  ref unchanged, `InvalidatedByEventID` populated), history event
  lists bugs and approach.
- In-place: feat file rewritten, bug FeatureIDs untouched, approach
  invalidated.

**Strategy supersede:**

- New slug: fixture with one approach `ParentID: strat-foo`. Run
  cascade with target `strat-bar`. Assert strat-foo deleted,
  strat-bar written, approach invalidated, no other nodes touched.
- In-place: strat file rewritten, approach invalidated.

**Cross-cutting:**

- `--supersede` and `--brief` both passed: error before any mutation.
- `--supersede` with no argument: error before any mutation.
- `--supersede` against a bug id: error before any mutation, hint
  points at the existing status-transition path.
- Validator post-cascade across all kinds: invalidated approaches do
  not produce dangling-reference flags; live id rewrites have no
  unresolved targets.
- Loaded.findOrphans behaviour: invalidated approaches still count
  as referenced via Feature/Strategy.Approaches[] and aren't flagged
  as orphans.

**Rendering for invalidated approaches:**

- `locutus list <q>` includes an invalidated approach matching `q`
  with the `[invalidated]` badge in its row.
- `locutus explain <invalidated-app-id>` renders the banner above
  the per-node section.
- `locutus status` lists the invalidated approach under
  `## Pending reconcile` with the originating event id.

LLM-stub tests (mock_llm), one per agent:

- Each `refiner-supersede-<kind>` stub returns a valid replacement;
  assert cascade fires exactly once with the right files on disk.
- Stub returns a node whose slug equals the old one; assert in-place
  path fires.
- Stub returns invalid output (empty title, missing required field);
  assert no on-disk changes, error surfaced.

Rollback tests:

- Run supersede end-to-end against a fixture for each kind, then
  `--rollback`, assert the spec graph matches the pre-supersede state
  byte-for-byte (deleted source node restored, downstream id-rewrites
  reverted, invalidated approaches' `InvalidatedByEventID` cleared).

Adopt-side branch tests (separate from the supersede flow):

- Adopt encounters an invalidated approach: regenerator agent is
  invoked instead of the fresh-synthesis agent, and is fed the
  invalidated approach's prior content + the supersede event.
- The regenerated approach's Body covers both forward (new spec) and
  backward (cleanup of prior artifacts) — assertion on the agent
  output prompt structure, not the prose itself.

## Files

- [cmd/refine.go](cmd/refine.go): add `--supersede` flag, mutual-exclusion
  validation against `--brief`, kind-aware routing (reject bugs).
- [internal/agent/refiner_supersede.go](internal/agent/refiner_supersede.go):
  three new agent invocations emitting
  `RewriteDecisionResult` / `RewriteFeatureResult` /
  `RewriteStrategyResult`. Mirrors the structure of
  `internal/agent/elaboration.go` / `internal/agent/reconcile.go`.
- `internal/scaffold/agents/refiner-supersede-decision.md`,
  `internal/scaffold/agents/refiner-supersede-feature.md`,
  `internal/scaffold/agents/refiner-supersede-strategy.md`: new agent
  definitions.
- [internal/agent/schemas.go](internal/agent/schemas.go): add the
  three `Rewrite*Result` schemas.
- [internal/spec/approach.go](internal/spec/approach.go): add the
  `InvalidatedByEventID` field plus `IsInvalidated()` helper.
- [internal/spec/loaded.go](internal/spec/loaded.go): adjust
  `findDanglingRefs` and `findOrphans` so invalidated approaches
  don't produce false positives.
- `internal/spec/cascade.go` (or wherever existing cascade helpers
  live): add the multi-file supersede commit, including approach
  invalidation writes, decision-influenced-by rewrites, bug
  feature_id rewrites.
- `internal/agent/approach_regenerator.go` and
  `internal/scaffold/agents/approach-regenerator.md`: new agent and
  invocation for adopt's invalidated-approach branch.
- [cmd/adopt.go](cmd/adopt.go): route invalidated approaches through
  the regenerator branch instead of fresh synthesis.
- [internal/render/list.go](cmd/list.go) /
  [internal/render/spec.go](internal/render/spec.go) /
  [internal/render/snapshot.go](internal/render/snapshot.go): surface
  the `[invalidated]` badge in `list`, the banner in `explain`, and
  the `Pending reconcile` section in `status`.
- [internal/render/justify.go](internal/render/justify.go) (or the
  suggested-next-step renderer wherever it lives): route to
  `--supersede` on BROKE DOWN against decision/feature/strategy
  targets.
- [cmd/mcp.go](cmd/mcp.go): surface `supersede` parameter on the MCP
  `refine` tool input shape.
- [docs/DECISION_JOURNAL.md](docs/DECISION_JOURNAL.md): new DJ entry
  (see below).
- Test files under `cmd/` and `internal/agent/` matching the
  established `_test.go` patterns.

## DJ entry

Title: **Spec Node Supersession Deletes the Old Node; History Is the
Durable Record.**

Captures:

- The justify→refine handoff bug, with the winplan
  `dec-adopt-auth-js-nextauth-...` example as the canonical
  motivating case.
- Scope is Decisions, Features, and Strategies. Bugs use their
  existing status lifecycle.
- Why deletion beats `status: superseded` for the superseded node:
  git carries content history, `.borg/history/` carries the
  supersession event and motivation (DJ-103 pattern), and a
  tombstone serves no purpose after total cascade.
- **Why approaches are invalidated rather than deleted.** SpecHash
  linkage between approach and produced artifacts is the load-
  bearing tie that `status` reads for drift detection and that
  `adopt` reads to know what code already exists. Deletion orphans
  artifact files; invalidation preserves the blast-radius input
  the next adopt run needs. The `InvalidatedByEventID` pointer
  forms a closed loop with the supersede event.
- Cascade rules with the per-kind table and the
  reconcile-loop-boundary reasoning for keeping LLM-heavy
  regeneration inside `adopt` rather than `refine --supersede`.
- Same-slug in-place fallback as the cheap escape from naming
  collisions; in-place still invalidates approaches because the
  structured fields changed materially.
- Per-kind agent flow: `refiner-supersede-<kind>` emits the
  replacement node, existing `refiner` runs prose cascades on
  affected downstream nodes, new `approach-regenerator` runs at
  adopt time when an invalidated approach is encountered.
- `--supersede` and `--brief` are mutually exclusive on `refine`.

Rejected alternatives:

- **New top-level `supersede` verb.** Breaks DJ-101's 8-verb cap. The
  user mental model is "refine this node," not "invoke a different
  operation type."
- **`status: superseded` flag on the old node.** Duplicates the
  durable record git + history already carry. Pollutes `locutus list`
  output. No node references it after cascade, so it serves no
  graph-traversal purpose either.
- **Eager approach regeneration inside `refine --supersede`.** Would
  cross the spec-mutation / reconcile-loop boundary the verb set is
  organised around, and would force N synthesis LLM calls into a
  flow that should stay fast and synchronous. At winplan scale a
  heavily-cited decision could trigger 10+ regenerations per
  supersede.
- **Deleting approaches and orphaning artifact code.** Loses the
  SpecHash → code-hash linkage that `status` and `adopt` rely on,
  and leaves the next adopt synthesizing fresh approaches with no
  awareness that prior code exists.
- **Auto-fire on BROKE DOWN verdicts.** Locutus does not auto-mutate
  on agent verdicts. The user invokes `--supersede` explicitly.
- **Including bugs in scope.** The existing status lifecycle plus
  filing a fresh bug against the right root cause already covers
  the rename-the-mistake case for bugs without the supersede
  cascade weight.

Reference: bridges from DJ-101 (verb-set cap, justify as active
defense), DJ-085 (decision provenance is denormalized into the
decision), DJ-103 (history events as durable past-tense record).

## Out of scope

- Auto-fire on BROKE DOWN verdicts. Always user-driven.
- Versioned ids (`dec-...-v2`, `feat-...-v2`). Slug change is the
  version.
- Cross-project node cloning.
- Confidence-threshold-driven supersession.
- Bug supersession. Bugs use status transitions (`reported →
  triaged → fixing → fixed`) plus a fresh filing for wrong-root-cause
  cases.
- Bug-shaped justify suggesting a status transition. Today the
  renderer just routes BROKE-DOWN-against-bug to `--brief`; a
  smarter routing is a follow-up.
- Recovery from arbitrary manual operations on invalidated
  approaches (e.g. `git rm` on the invalidated file). If the user
  manually deletes an invalidated approach, adopt synthesizes from
  scratch with no blast-radius input — same shape as adopt against
  a never-synthesized parent. Tighter coupling between spec and
  hand-edited file state would run counter to the two-way-door DX
  the verb set is organised around.
