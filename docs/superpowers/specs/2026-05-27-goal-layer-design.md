# Goal-layer node kinds: `Goal` and `AntiGoal`

**Design lock date:** 2026-05-27
**Status:** design (brainstorm artifact; the canonical project record is [DJ-139](../../decisions/dj-139-goal-layer-node-kinds.md))
**Predecessor:** [DJ-138](../../decisions/dj-138-refine-with-bias-cascade.md) (strong-bias cascade, ships before this)
**Successor:** [DJ-139](../../decisions/dj-139-goal-layer-node-kinds.md) — formalized as the project's decision-journal entry; this brainstorm artifact remains for historical reference.

## Why this exists

Locutus today derives the entire spec graph (decisions, features, strategies, approaches) from `GOALS.md` via a `refine goals` pass. The LLM's interpretation of `GOALS.md` — which atomic claims it extracted, which axes it surfaced, which boundaries it inferred — is **ephemeral**: it lives only in the agent's session log, not in the persisted graph. Two failure modes follow:

1. **Unbounded blast radius on GOALS.md edits.** When the user adds a new goal, the next `refine goals` re-derives the whole graph from scratch because there's no record of which goal-claims justified which existing decisions. A small GOALS.md edit can ripple through dozens of nodes unnecessarily.
2. **Implicit conflict surfaces.** When an operator runs `locutus import <feature>` and the feature conflicts with a scope claim, the agent has nowhere to bind the conflict — it has to read GOALS.md prose every time and re-judge "does this feature touch an out-of-scope area?" That works but is non-idempotent and unauditable.

The concrete example is the dashboard import attempt on winplan (session `20260527/0310/010000`): the import surfaced seven distinct scope conflicts against `dec-product-scope-boundary` and stopped without proposing a path forward because no surface exists for "extend GOALS.md by adding a carve-out."

This design adds **persisted, structured goal/anti-goal nodes** as the LLM's durable interpretation of `GOALS.md`. The interpretation stops being ephemeral; the graph gains a stable scope-layer that import flows can bind to; and `GOALS.md` edits become incremental diffs against the persisted layer rather than full re-derivations.

## Design choices (locked)

These were settled in chat during the brainstorm; this section captures the decisions verbatim so the implementation plan and any subagent prompts can cite them.

1. **Authorship: `GOALS.md` stays canonical; goal nodes are LLM interpretation.** Humans only edit `GOALS.md`. The `refine goals` pass reads `GOALS.md` and writes `goal-*` / `agoal-*` nodes as its persisted interpretation. Users can read goal nodes but don't author them directly. Matches Locutus's "humans author, Locutus implements" stance ([[feedback-locutus-voice-neutrality]]).
2. **Granularity: atomic claim.** One node per discrete statement, regardless of `GOALS.md` formatting. "Fundraising is out of scope, ceded to ActBlue" is one node; "must work for school-board through state-leg races" is another. Most semantically precise; gives the import flow fine-grained pivot points.
3. **Two node types with polarity in the type, not a field.** `Goal` (`goal-` prefix) for in-scope claims; `AntiGoal` (`agoal-` prefix) for out-of-scope claims. Decisions/features `.advances` Goals and `.respects` AntiGoals. Polarity is structural — the field name names the relationship, the LLM doesn't have to read a `kind` enum on every consumption.
4. **Upstream links: `.advances` and `.respects` are informational dotted lines, not required invariants.** Optional fields on Decision/Feature/Strategy/Approach. The agent populates them when there's a clear citation worth recording; empty/absent is fine. The graph doesn't enforce structural citation discipline because that would be over-prescriptive given the coding-agent runtime's reasoning capacity.
5. **AntiGoal carries `kept_in []string` for carve-outs.** Out-of-scope claims often have nuance — "fundraising is out, but aggregated CSV import for plan-vs-pace display is in." `kept_in` is a list of carve-out clauses. The LLM consumes them alongside `body` when judging whether a new feature fits.
6. **Axes survive but anchor at Goals only.** Per DJ-124 axes are "surfaced from goals, features, and strategies"; under this design axes anchor exclusively at Goal nodes. Features/strategies cite Goals via `.advances` and inherit the axis chain transitively. DJ-133's `dec-<axis-id>` decision-id shape stays intact.
7. **Diff-and-apply on `GOALS.md` change, never wipe-and-regen.** Goal/agoal IDs are referenced by `.advances` / `.respects` citations; wiping would break every citation on every edit. The sync algorithm does LLM-judgment matching against existing nodes' `source_clause` fields, preserving IDs across rephrasings and only minting new IDs for genuinely new claims.
8. **`goals_md_hash` lives in `.borg/manifest.json` as the short-circuit signal.** Single project-level hash, not per-node. On `refine goals` invocation, if the hash matches current `GOALS.md` content, step 1 (goal-layer sync) skips entirely.

## Node shapes

`validSpecID` regex extends from `^(feat|strat|dec|bug|app)-...` to `^(goal|agoal|feat|strat|dec|bug|app)-...`.

```go
// internal/spec/goal.go (new file)
type Goal struct {
    ID           string    `json:"id"`            // goal-<slug>
    Title        string    `json:"title"`         // human-readable headline
    Body         string    `json:"body"`          // claim text, Locutus-rewritten for clarity
    SourceClause string    `json:"source_clause"` // verbatim quote from GOALS.md (load-bearing for diff matching)
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}

// internal/spec/antigoal.go (new file)
type AntiGoal struct {
    ID           string    `json:"id"`            // agoal-<slug>
    Title        string    `json:"title"`
    Body         string    `json:"body"`          // the exclusion claim
    SourceClause string    `json:"source_clause"` // verbatim quote from GOALS.md
    CededTo      []string  `json:"ceded_to,omitempty"`  // named incumbents owning the ceded space
    KeptIn       []string  `json:"kept_in,omitempty"`   // carve-outs that stay in scope despite the exclusion
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}
```

Existing kinds (Decision, Feature, Strategy, Approach) gain two optional fields:

```go
// Both fields are optional; populated by the agent when a clear citation exists.
// Empty or absent is the common case for the citation walk; graph readers treat
// them as informational, not invariants.
Advances []string `json:"advances,omitempty"`  // goal-* ids
Respects []string `json:"respects,omitempty"`  // agoal-* ids
```

## MCP tool surface

Six new tools register in `internal/mcp/tools_spec_write.go`:

| Tool | Input | Behavior |
|---|---|---|
| `spec_propose_goal` | `{id, title, body, source_clause}` | Create. Auto-commits per call; emits `notifications/resources/updated`. |
| `spec_revise_goal` | `{id, title, body, source_clause}` | Update (id must exist). Preserves `created_at`. |
| `spec_delete_goal` | `{id, reason}` | Remove. `reason` is a short clause recorded in the history event so the audit trail survives the node. |
| `spec_propose_antigoal` | `{id, title, body, source_clause, ceded_to?, kept_in?}` | Create. |
| `spec_revise_antigoal` | `{id, title, body, source_clause, ceded_to?, kept_in?}` | Update. |
| `spec_delete_antigoal` | `{id, reason}` | Remove. |

The `*_delete_*` tools are new — pre-DJ-138 there was no MCP delete surface (decisions/features can be revised but not removed, because the model is append-only). For goals, deletion is real: a removed scope claim should disappear. The history event preserves the audit trail.

The existing propose/revise tools for decision/feature/strategy gain optional `advances []string` and `respects []string` parameters. Backwards compatible — existing callers that don't pass them get empty fields.

## Manifest schema

`.borg/manifest.json` gains two optional fields:

```json
{
  "project_name": "winplan",
  "version": "0.1.0",
  "goals_md_hash": "sha256:abc123...",
  "goals_md_synced_at": "2026-05-27T03:10:00Z"
}
```

`goals_md_hash` is the SHA-256 of the current `GOALS.md` bytes at the time the goal layer was last synced. `goals_md_synced_at` is timestamp metadata for operator inspection. Both fields default to empty/zero on projects that haven't run `refine goals` post-feature-land; on first run the sync proceeds (treated as a hash mismatch) and the fields populate.

## `refine goals` workflow

A `refine goals` invocation runs three steps in order. The citation walk is **last** so it operates against the final state of all nodes (catches misapplied citations that pre-iteration walking would produce).

### Step 1: Goal-layer bootstrap/sync

Short-circuited by hash comparison: if `hash(GOALS.md_now) == manifest.goals_md_hash`, skip step 1 entirely.

On hash mismatch (or absent hash — first run):

1. Read `GOALS.md` and the existing `goal-*` / `agoal-*` nodes (via `mcp__locutus__spec_list_manifest` + batched `spec_get`).
2. Dispatch a `spec-goal-diff-matcher` subagent (new agent prompt under `internal/scaffold/agents/`). It receives:
   - Current `GOALS.md` text.
   - List of existing nodes: `{id, source_clause, body, ceded_to?, kept_in?}` per node.
   It returns a structured diff:
   - **Unchanged**: list of existing node ids whose `source_clause` still appears verbatim in `GOALS.md`.
   - **Modified**: list of `{existing_id, new_source_clause, new_body, new_ceded_to?, new_kept_in?}` for nodes whose claim has been rephrased or refined. ID preserved.
   - **Deleted**: list of `{existing_id, reason}` for nodes whose source clause is gone from `GOALS.md`.
   - **Added**: list of `{kind: goal|agoal, title, body, source_clause, ceded_to?, kept_in?}` for genuinely new claims with no existing match. The agent proposes a slug; the orchestrator validates uniqueness against existing IDs.
3. The orchestrator applies the diff via `spec_propose_*` / `spec_revise_*` / `spec_delete_*` calls.
4. Update `.borg/manifest.json` with the new `goals_md_hash` and `goals_md_synced_at`.

Edge cases the matcher handles:
- **Claim split** (one existing node → two new claims): revise one to track the "evolution" candidate (highest semantic similarity to original source clause); add the other as new.
- **Claim merge** (two existing nodes → one new claim): revise the more-cited node to cover the merged claim; delete the other.
- **Significant rephrasing without semantic change**: revise body, keep ID, preserve all citations.

### Step 2: Spec graph iteration

The existing `spec_refinement` playbook runs (`internal/scaffold/plans/spec_refinement.md`), now reading the goal layer as anchor context. Changes to the orchestrator playbook:

- Step "Start here" extends to fetch `goal-*` and `agoal-*` nodes alongside the manifest and `GOALS.md`. The agent has the persisted interpretation in context, not just the prose.
- Subagents (`spec-scout`, `spec-decision-elaborator`, etc.) see the goal layer when reasoning about which axes to surface, which decisions to make, etc. Their existing prompts gain a short addition: "the goal layer at `goal-*` / `agoal-*` is the authoritative scope statement; consult it alongside GOALS.md."
- Iteration converges per the existing playbook's verdict line; cap inherited from `agents.yaml` (DJ-138 phase 1).

Scenario 2b's conflict-amplifying case (a Goal transitions to an AntiGoal) is handled in this step: the iteration sees the new AntiGoal, judges features that previously advanced the now-excluded area, and revises them. Content changes happen here, not in step 1 or step 3.

### Step 3: Citation walk

For every `dec-*` / `feat-*` / `strat-*` / `app-*` node, the orchestrator (or a dedicated `spec-citation-walker` subagent) judges `.advances` and `.respects` against the *final* state of the goal layer + node.

Optimization for the no-change case: skip nodes whose body wasn't touched in step 2 AND whose existing citations point at goal-layer ids that didn't change in step 1. Those citations are still accurate; no re-judgment needed.

**At-risk surface fires here.** Features whose final state has empty `.advances` after the walk get reported in the `refine goals` output AND persist as a `status --full` validation warning ("features without goal anchors — review needed"). Resolution is operator judgment — `refine goals` never auto-deletes features.

## Workflow scenarios

### Scenario A: First `refine goals` on an existing project (winplan migration)

1. Operator runs `locutus update --offline --reset` to get the new binary.
2. Operator runs `locutus refine goals`.
3. `goals_md_hash` is absent → step 1 runs. The matcher gets `GOALS.md` (7 lines for winplan) and an empty existing-nodes list.
4. **Migration affordance**: on first run for a project with existing `dec-*` content that encodes scope (the `dec-product-scope-boundary` pattern), the matcher prompt is extended with "for first-run bootstrap, treat decisions whose body enumerates scope claims as secondary sources alongside GOALS.md." This is a one-time bootstrap behavior so winplan-style projects don't need to flesh out `GOALS.md`'s empty "Out of Scope" section before the first sync. Subsequent runs drop the secondary-source behavior and only read `GOALS.md`.
5. Matcher returns: ~10 `Added` claims (the goals/anti-goals decomposed from the boundary decision body), zero `Modified` / `Deleted` / `Unchanged`.
6. Orchestrator applies the diff. `goals_md_hash` updates.
7. Step 2 runs (full graph iteration with the new goal layer as context). Probably no major content changes — the iteration finds the graph already converged because the boundary decision matches the new anti-goals.
8. Step 3 runs (citation walk). Every existing dec/feat/strat/app gets visited; `.advances` and `.respects` populate based on the agent's judgment. ~50-100 node visits for winplan-scale; bounded one-time cost.
9. `dec-product-scope-boundary` stays in place as historical documentation. The new `agoal-*` nodes are authoritative; the decision body is redundant but harmless. An operator who wants to trim it can run `locutus refine dec-product-scope-boundary --with "scope claims now live in agoal-* nodes; reduce body to a pointer"` later — explicit, opt-in.

### Scenario B: Operator imports a feature that conflicts with an AntiGoal (dashboard case)

1. `locutus import docs/dashboard.md`.
2. Import playbook walks graph, finds the dashboard tiles touch `agoal-fundraising`, `agoal-budget`, `agoal-events`, `agoal-people-crm`.
3. Playbook drafts a unified diff against `GOALS.md`:
    ```diff
    - Fundraising is out of scope, delegated to ActBlue.
    + Fundraising is out of scope, delegated to ActBlue. Aggregated CSV
    + imports of actuals for plan-vs-pace display are permitted.
    ```
    (similar diffs for budget, events, people-crm). Playbook reports the diff and stops; doesn't auto-apply.
4. Operator reviews the diff, edits `GOALS.md`.
5. Operator runs `locutus refine goals`. Hash mismatch → step 1 syncs the goal layer (revises the four AntiGoals with new `kept_in` arrays). Step 2 finds no content changes needed (no existing dec/feat/strat conflicts with the new state). Step 3 finds no citation updates needed (no existing nodes cite the affected AntiGoals yet).
6. Operator re-runs `locutus import docs/dashboard.md`. Playbook reads `agoal-fundraising.kept_in`, finds the dashboard fits, admits `feat-campaign-overview-dashboard` with `.respects: [agoal-fundraising, agoal-budget, agoal-events, agoal-people-crm]` and `.advances: [goal-strategic-planning-tool]`.

### Scenario C: GOALS.md transitions a Goal to an AntiGoal (conflict-amplifying case)

1. Operator edits `GOALS.md`: `Fundraising tracking is in scope` → `Fundraising is out of scope, delegated to ActBlue`.
2. `locutus refine goals`. Hash mismatch.
3. Step 1: matcher returns `Deleted: [goal-fundraising-tracking]`, `Added: [agoal-fundraising]`.
4. Step 2: iteration sees the new AntiGoal. Subagents revise features that previously advanced `goal-fundraising-tracking`. Some features may get fully revised (to navigate the new boundary); some may surface as candidates for deletion (operator-judgment).
5. Step 3: citation walk. Features that had `.advances: [goal-fundraising-tracking]` get walked; their `.advances` citation is removed (the goal is gone). If the iteration revised the feature to respect the new AntiGoal, the walk adds `.respects: [agoal-fundraising]`. Features that ended up with empty `.advances` surface in the at-risk report.

This is the case where the iteration AND the citation walk both do real work, and the operator sees a meaningful report afterward listing nodes that need follow-up attention.

### Scenario D: GOALS.md adds an in-scope entry that supplants an AntiGoal

Mirror of Scenario C. Step 1: matcher returns `Deleted: [agoal-fundraising]`, `Added: [goal-fundraising-tracking]`. Citation walk transitions any `.respects: [agoal-fundraising]` to `.advances: [goal-fundraising-tracking]` where the agent judges the feature now advances the new in-scope claim; just removes the citation where the feature was navigating around the boundary but isn't itself a fundraising-tracking feature. No at-risk surface — anti-goal removal can't invalidate existing nodes.

### Scenario E: GOALS.md loosens a Goal's constraint

Step 1: matcher returns `Modified: [{goal-strategic-planning-tool, new body with relaxed wording}]`. Possibly an `Added` if the loosening surfaces a distinct atomic claim. Step 2: no content changes needed (existing features still advance the goal). Step 3: no citation updates (citations stay valid). Gentlest case. Operator just gets more headroom for future imports.

## Import playbook updates

`internal/scaffold/plans/feature_ingestion.md` (the `import` playbook) gets the following changes:

- Context-fetch step extends to read `goal-*` and `agoal-*` nodes alongside the manifest.
- Conflict-detection step changes shape: instead of LLM-judging "does this feature touch out-of-scope language in GOALS.md prose," the playbook checks the feature's described behavior against each `agoal-*` node's `body` and `kept_in` arrays. Conflicts are structural graph-tests, not prose-judgments.
- Conflict-resolution step gains a new branch: when a conflict surfaces AND the feature plausibly fits under an extended/new carve-out, the playbook drafts a `GOALS.md` diff and surfaces it for operator review. The existing "stop and ask" behavior remains the fallback when no diff suggestion is appropriate.
- On admission, the playbook populates `.advances` and `.respects` on the new feature node via the propose tool's new optional parameters.

## What's explicitly out of scope (future work)

- **Witness-state precision for citation drift detection.** When a goal's body changes, the cascade currently has no way to detect whether existing `.advances` citations are still accurate vs. now-stale. Bounded by re-walking and re-judging during step 3; can be optimized later if it becomes a felt pain point. Parallel to DJ-138's deferred Phase-2 work.
- **`locutus refine --with` against goal/agoal targets.** Strong-bias revision of a goal node, with cascade through downstream features that cite it. DJ-138 cascade machinery handles this mechanically; the playbook just needs to learn the new node prefix.
- **`locutus apply-goals-diff <patch>` helper.** A small utility that applies the import playbook's drafted `GOALS.md` diff for the operator. Saves manual paste-and-edit. Trivial to add when the import flow's diff output is stable.
- **Per-runtime playbook overlay** for the goal-layer playbook. Defer until a real UX win surfaces (matches DJ-138's stance on overlays).
- **`spec_biased`-style cascade root event for `refine goals` bootstrap.** The existing `spec_revised` event log already captures per-node writes; rolling them up into a synthetic `goals_synced` root event is navigation nicety, not a structural need.
- **Splitting the citation walk into a separate verb.** Considered during brainstorm; rejected as over-fragmentation. `refine goals` bundles sync + iterate + cite.

## Risks / known unknowns

- **(a) Diff-matcher consistency.** The `spec-goal-diff-matcher` agent's matching judgments may be inconsistent across runs. Mitigation: source_clause comparison provides textual grounding; the prompt explicitly directs "preserve existing IDs by matching against source_clause; only mint a new ID when a claim has no existing match." Empirical validation lands when the playbook is exercised on a real GOALS.md edit.
- **(b) Citation walk cost on large graphs.** For a winplan-scale graph (~100 nodes) the bootstrap citation walk is ~50-100 judgments — bounded but real. Larger projects (~500+ nodes) would feel it more. Mitigation: the no-change optimization in step 3 keeps subsequent passes cheap; bootstrap is one-time.
- **(c) Migration heuristic for `dec-product-scope-boundary`-style nodes.** The "treat existing scope-encoding decisions as secondary source for the bootstrap pass" affordance is a one-time hack. Risk: an existing project where scope claims are spread across multiple decisions (not concentrated in one boundary decision) might require operator help to seed the goal layer. Mitigation: the operator can hand-seed `GOALS.md` before the first refine goals if the heuristic falls short.
- **(d) Polarity confusion as a coding-agent failure mode.** A new class of agent error: features with `.advances: [agoal-X]` (wrong polarity) or `.respects: [goal-Y]` (wrong polarity). Schema-level validation (the propose/revise tools reject malformed ids based on prefix) catches the obvious case; semantic polarity (e.g., the agent put a goal-id in `.respects` because it thought the feature was working around it) needs prompt discipline in the citation walker.
- **(e) The `spec-goal-diff-matcher` subagent prompt is novel and load-bearing.** It needs careful authoring per [[reference-agent-conventions-doc]] — anti-pattern priming would be especially harmful here because the matcher is producing structured output (a diff). The prompt should describe the desired output shape positively, use realistic example payloads with real winplan-style ids, and trust the OutputSchema for shape enforcement.

## Reference

Synthesizes the design conversation on 2026-05-27, beginning with the failed dashboard import on winplan (session `20260527/0310/010000`). The user identified the core problem: GOALS.md → spec graph is non-idempotent in goal interpretation, so GOALS.md edits have unbounded blast radius. The brainstorm crystallized a goal-layer of persisted LLM interpretation as the structural fix; the polarity-typed Goal/AntiGoal split, the informational-citation model, and the hash-keyed sync algorithm fell out of subsequent design questions.
