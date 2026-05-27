# DJ-139 — Goal-layer node kinds (`Goal` and `AntiGoal`)

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task.
>
> **Governing DJ:** [DJ-139](../../docs/DECISION_JOURNAL.md#dj-139). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** READY — design locked in DJ-139 on 2026-05-27. [DJ-138](../../docs/DECISION_JOURNAL.md#dj-138) is a prerequisite (shipped 2026-05-27 — `per-activity max_iterations` lands first so the new `refine goals` workflow inherits the configurable cap).
>
> **Predecessors:** [DJ-124](../../docs/DECISION_JOURNAL.md#dj-124) (axes from goals/features/strategies — DJ-139 tightens to axes-from-goals-only); [DJ-133](../../docs/DECISION_JOURNAL.md#dj-133) (axis-shaped decision ids — preserved); [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135) (activity-driven multi-runtime — DJ-139's `refine goals` reorganization rides on the existing playbook + MCP-tool surface); [DJ-138](../../docs/DECISION_JOURNAL.md#dj-138) (per-activity `max_iterations` — DJ-139 inherits the configurable cap for the bootstrap citation walk).
>
> **Surface area:** medium-large. New: two `spec.*` types (~70 lines), `SpecStore` extension (~250 lines net across `internal/agent/spec_store.go`), six MCP write tools (~250 lines in `internal/mcp/tools_spec_write.go`), new `spec-goal-diff-matcher` canonical agent prompt (~120 lines), two playbook overhauls (`spec_refinement.md` ~80 lines added; `feature_ingestion.md` ~40 lines added), manifest schema additions (~30 lines plumbing), status renderer extension (~50 lines), tests + docs across the diff.
>
> **Discipline (per memory):**
>
> - Tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching ([[feedback-test-first]]).
> - Cite DJ-139 + the precise constraint in chat before touching new MCP tools, the diff-matcher prompt, the playbooks, or the spec types ([[feedback-cite-djs-before-spec-work]]).
> - Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) as a checklist BEFORE authoring `internal/scaffold/agents/spec-goal-diff-matcher.md` and the playbook updates ([[feedback-agent-conventions-checklist-first]]); the diff-matcher's prompt is load-bearing — its output drives every `refine goals` sync.
> - **No** [docs/council.md](../../docs/council.md) update required — DJ-139 does not touch the existing council. The `spec-goal-diff-matcher` is a new subagent dispatched directly by the `refine goals` orchestrator; it doesn't join the `spec_refinement` deliberation council (spec-scout/elaborator/critic/reconciler). The [[feedback-council-doc-maintenance]] checklist is satisfied by the explicit exemption documented in DJ-139's Documentation consequence.
> - No back-compat shims needed for spec-type or MCP-tool shape changes ([[feedback-no-back-compat-until-self-hosting]]) — the user runs `locutus update --offline --reset` before every operation. The optional `Advances`/`Respects` fields on existing kinds are additive; no shim needed.
> - Locutus voice neutrality in playbook + agent-prompt prose ([[feedback-locutus-voice-neutrality]]): no "MVP", "good-enough", "ship-ready" framing in any prompt. Describe what the orchestrator/agent does, not how good Locutus thinks it is.
> - No aspirational fields ([[feedback-no-aspirational-fields]]): every field on `spec.Goal` and `spec.AntiGoal` has a documented consumer in DJ-139. Confirmed: `Title` (rendered by explain/list), `Body` (consumed by import-conflict-detection), `SourceClause` (load-bearing for diff matching), `CededTo` (consumed by import conflict prose), `KeptIn` (consumed by carve-out fit judgment), `Advances`/`Respects` (rendered by explain, queried by status validation). No others.
>
> **What's deliberately deferred to follow-up DJs** (per DJ-139 Future Work, restated here so the plan stays scoped):
> - Witness-state precision for citation drift.
> - `locutus refine --with` targeting `goal-*` / `agoal-*` nodes (DJ-138 cascade extension).
> - `locutus apply-goals-diff <patch>` helper.
> - Inverse-direction `locutus drift goals` verb.
> - `goals_synced` cascade root event.
> - Per-runtime playbook overlays for `refine goals`.

## Why this plan exists

The Locutus spec graph today derives entirely from `GOALS.md` via the `refine goals` pass, but the LLM's interpretation of `GOALS.md` — which atomic claims were extracted, which axes surfaced, which boundaries inferred — is **ephemeral**. The interpretation lives only in the session log, never on disk. Two failure modes follow:

1. **Unbounded blast radius** when `GOALS.md` is edited (the dashboard import attempt on winplan, session `20260527/0310/010000`, surfaced this).
2. **Implicit conflict surfaces** with no graph-binding when `locutus import <feature>` hits scope claims (same session — the import stopped without proposing a path forward because no surface existed for "extend GOALS.md by adding a carve-out").

DJ-139 fixes both by persisting the LLM's interpretation as first-class `goal-*` and `agoal-*` nodes. This plan breaks the implementation into 9 phases, each leaving the system in a working state.

## Reference state (before DJ-139 starts)

- **Spec types** at [internal/spec/types.go](../../internal/spec/types.go) — `Decision`, `Feature`, `Strategy` live alongside each other; `Approach` is in its own file at `approach.go`. Goal and AntiGoal will go into `types.go` matching the existing convention.
- **`validSpecID` regex** at [internal/agent/spec_tools.go:51](../../internal/agent/spec_tools.go#L51) — `^(feat|strat|dec|bug|app)-[a-z0-9]+(-[a-z0-9]+)*$`. Extends to include `goal` and `agoal`.
- **SpecStore** at [internal/agent/spec_store.go](../../internal/agent/spec_store.go) — 862 lines. Holds typed entries per kind in separate maps (`features`, `strategies`, `decisions`, `bugs`, `approaches`). The kind-switch sites are: `Put`, `lookupLocked`, `idsForKindLocked`, `workingPriorLocked`, `loadFromFS`, `ListManifest`, and the transaction snapshot machinery (`takeSnapshot` + `restoreSnapshot`). Each needs extending for two new kinds.
- **MCP write tools** at [internal/mcp/tools_spec_write.go](../../internal/mcp/tools_spec_write.go) — 479 lines. Registers `spec_propose_decision`, `spec_propose_feature`, `spec_propose_strategy`, `spec_revise_decision`, `spec_revise_feature`, `spec_revise_strategy`, and (post-DJ-138) `spec_mark_approach_drifted`. Goal/AntiGoal propose/revise/delete tools land here.
- **Manifest** at [internal/spec/types.go](../../internal/spec/types.go) (struct definition) and [internal/scaffold/scaffold.go](../../internal/scaffold/scaffold.go) (write site at scaffold time). Currently carries `ProjectName`, `Version`, `CreatedAt`. DJ-139 adds `GoalsMdHash string` and `GoalsMdSyncedAt time.Time`.
- **`refine goals` playbook** at [internal/scaffold/plans/spec_refinement.md](../../internal/scaffold/plans/spec_refinement.md) — the deliberation council playbook. DJ-139 wraps this with goal-layer sync (before iteration) and citation walk (after).
- **`import` playbook** at [internal/scaffold/plans/feature_ingestion.md](../../internal/scaffold/plans/feature_ingestion.md) — DJ-139 extends conflict detection to read the goal layer and draft GOALS.md diffs.
- **Canonical agents** at [internal/scaffold/agents/](../../internal/scaffold/agents/) — `spec-goal-diff-matcher.md` is the new prompt that lands here.
- **Status renderer** at [cmd/snapshot.go](../../cmd/snapshot.go) + [internal/render/snapshot.go](../../internal/render/snapshot.go) — gains goal-layer counts and the at-risk-features section (validation block extension).

## Phase 1 — Spec types + `SpecStore` extension for `goal-*` and `agoal-*` kinds

**Goal:** `spec.Goal` and `spec.AntiGoal` types exist; `SpecStore` persists them roundtrip through `.borg/spec/goals/` and `.borg/spec/antigoals/`; `validSpecID` accepts the new prefixes. End-to-end: a test can `Put` a goal, persist, reload from disk, and read it back via the existing lookup machinery.

**Files expected to change:**

- [internal/spec/types.go](../../internal/spec/types.go) — add `Goal` and `AntiGoal` struct types with the field shapes from DJ-139 (id, title, body, source_clause, ceded_to/kept_in on AntiGoal, timestamps). YAML/JSON tags consistent with existing types.
- [internal/spec/enums.go](../../internal/spec/enums.go) — add `KindGoal` and `KindAntiGoal` to the `NodeKind` enum if it exists in this file (verify by reading; otherwise add wherever the existing `NodeKind` lives).
- [internal/agent/spec_tools.go](../../internal/agent/spec_tools.go) — extend `validSpecID` regex from `^(feat|strat|dec|bug|app)-...` to `^(goal|agoal|feat|strat|dec|bug|app)-...`. Update the kind-from-prefix branch in `lookupLocked` to recognize `goal-` and `agoal-`.
- [internal/agent/spec_store.go](../../internal/agent/spec_store.go) — add `KindGoal` and `KindAntiGoal` to the `SpecKind` constants; add `goalEntry` and `antiGoalEntry` types matching `featureEntry` shape; add `goals` and `antiGoals` maps to `SpecStore`; extend `Put`, `lookupLocked`, `idsForKindLocked`, `workingPriorLocked`, `loadFromFS`, `ListManifest`, `rebuildIndex`, and `takeSnapshot`/`restoreSnapshot` to cover both new kinds. Disk layout: `.borg/spec/goals/<id>.json` and `.borg/spec/antigoals/<id>.json` (pure JSON, no markdown sidecar — these are LLM-derived structured data, not human-authored markdown).
- [internal/scaffold/scaffold.go](../../internal/scaffold/scaffold.go) — extend `directories` slice to include `.borg/spec/goals` and `.borg/spec/antigoals` so `locutus init` creates them.
- New test: [internal/agent/spec_store_goals_test.go](../../internal/agent/spec_store_goals_test.go) — covers (a) `Put` round-trip for both kinds via `SpecStore.Put`; (b) `loadFromFS` reads them back after persistence; (c) `ListManifest` includes them under new `Goals` and `AntiGoals` fields (extend `SpecManifest`); (d) `lookupLocked` resolves `goal-x` and `agoal-x` ids to the correct typed bodies; (e) `validSpecID` rejects malformed ids and accepts well-formed ones.
- Update [internal/agent/spec_store_test.go](../../internal/agent/spec_store_test.go) — extend the existing manifest-shape and roundtrip tests where they enumerate kinds (any test that asserts "all five kinds" needs updating to "all seven kinds").

**Discipline:** [[feedback-test-first]] is binding. Write the spec-type round-trip test FIRST asserting the new field shapes; watch it fail with "spec.Goal undefined"; then add the types. Same for the SpecStore extension: failing test first, then plumb the new kinds through.

**Tests added:**

- `TestSpecStorePutGoalRoundTrip` — write a `spec.Goal` via `Put(KindGoal, ...)`, persist, read back, assert all fields equal.
- `TestSpecStorePutAntiGoalRoundTrip` — same for `spec.AntiGoal` including `CededTo` and `KeptIn` slice round-trip.
- `TestSpecStoreLoadFromFSRecognizesGoalsAndAntiGoals` — write JSON files directly to `.borg/spec/goals/foo.json` and `.borg/spec/antigoals/bar.json`; construct a new SpecStore; assert both load.
- `TestSpecStoreListManifestIncludesGoalsAndAntiGoals` — populate both kinds; assert `ListManifest` returns them under new `Goals` and `AntiGoals` slices.
- `TestSpecStoreLookupLockedResolvesGoalIDs` — call internal lookup with `goal-x` and `agoal-x` ids; assert correct typed body.
- `TestValidSpecIDAcceptsGoalAndAntiGoalPrefixes` — table test covering `goal-foo`, `agoal-bar`, plus negative cases (malformed prefixes, mixed case, etc.).

**Commit message shape:** `feat(spec,agent): goal-* and agoal-* node kinds in SpecStore (DJ-139 phase 1)`.

**Exit criteria:** `go test ./internal/spec/... ./internal/agent/...` green; `go vet ./...` green. No MCP tools touch the new kinds yet (that's Phase 3); no playbook reads them yet (Phase 6). The kinds are addressable from Go code via `SpecStore`.

## Phase 2 — Optional `.advances` / `.respects` fields on existing node kinds

**Goal:** Decision, Feature, Strategy, and Approach gain optional `Advances []string` and `Respects []string` fields. Existing MCP propose/revise tools accept these as optional input parameters. End-to-end: a test can propose a feature with `advances: ["goal-x"]` and `respects: ["agoal-y"]`, read it back via `spec_get`, and observe the citations populated.

**Files expected to change:**

- [internal/spec/types.go](../../internal/spec/types.go) — add `Advances []string \`json:"advances,omitempty"\`` and `Respects []string \`json:"respects,omitempty"\`` to `Decision`, `Feature`, `Strategy`. Use `omitempty` so existing nodes without citations roundtrip cleanly.
- [internal/spec/approach.go](../../internal/spec/approach.go) — add the same two fields with the same tags.
- [internal/mcp/tools_spec_write.go](../../internal/mcp/tools_spec_write.go) — extend `proposeDecisionInput`, `proposeFeatureInput`, `proposeStrategyInput` (and their revise aliases) with optional `Advances []string` and `Respects []string` fields tagged with `jsonschema` descriptions explaining the polarity. Update `buildDecisionBody`, `buildFeatureBody`, `buildStrategyBody` to thread the new fields into the spec types they construct.
- [internal/mcp/tools_spec_write.go](../../internal/mcp/tools_spec_write.go) descriptions — extend `descSpecProposeDecision` etc. to mention the new optional citation fields (the agent reads tool descriptions on every call; the descriptions are the canonical source per the tool-descriptions-in-registration convention).
- New test: [internal/mcp/tools_spec_write_citations_test.go](../../internal/mcp/tools_spec_write_citations_test.go) — covers (a) `spec_propose_feature` with `advances` and `respects` populated; (b) round-trip via `spec_get`; (c) optional behavior (omit both fields and round-trip cleanly); (d) `spec_revise_feature` updates citations.

**Discipline:** Same as Phase 1 — test-first. The fields are additive and backward compatible; no shims needed because no production data needs migration mid-flight ([[feedback-no-back-compat-until-self-hosting]]).

**Tests added:**

- `TestSpecProposeFeatureAcceptsAdvancesAndRespects`
- `TestSpecProposeDecisionAcceptsAdvancesAndRespects`
- `TestSpecProposeStrategyAcceptsAdvancesAndRespects`
- `TestSpecReviseFeatureUpdatesCitations`
- `TestExistingNodesWithoutCitationsRoundTripCleanly` — defensive: explicitly assert empty-omitempty doesn't write garbage on disk.

**Commit message shape:** `feat(spec,mcp): optional advances/respects citation fields on decision/feature/strategy/approach (DJ-139 phase 2)`.

**Exit criteria:** `go test ./internal/spec/... ./internal/mcp/...` green; `go vet ./...` green. Citation fields are populatable via MCP tool calls; nothing reads them yet (rendering lands in Phase 8).

## Phase 3 — Six new MCP write tools for goal-layer mutation

**Goal:** `spec_propose_goal`, `spec_revise_goal`, `spec_delete_goal`, and the three `*_antigoal` variants are registered and callable. The delete tools are a new pattern in the codebase (pre-DJ-139 the spec model was append-only); the history event preserves the audit trail.

**Files expected to change:**

- [internal/mcp/tools_spec_write.go](../../internal/mcp/tools_spec_write.go) — add `descSpecProposeGoal`, `descSpecReviseGoal`, `descSpecDeleteGoal`, and the three AntiGoal description constants per the registration-conventions in [docs/agent-conventions.md](../../docs/agent-conventions.md). Add `proposeGoalInput`, `reviseGoalInput`, `deleteGoalInput`, and the three AntiGoal variants as input structs with `jsonschema` tags. Add `buildGoalBody` and `buildAntiGoalBody` helpers paralleling `buildDecisionBody`. Register the six tools in `registerWriteTools` with handlers that auto-commit per call.
- [internal/agent/spec_store.go](../../internal/agent/spec_store.go) — add `DeleteGoal(id)` and `DeleteAntiGoal(id)` methods to `SpecStore` (also extends the transaction snapshot to capture pre-delete state for rollback). The delete is in-memory + on-disk: remove from the map AND remove the JSON file under `.borg/spec/goals/<id>.json`.
- [internal/history/](../../internal/history/) — extend the historian with `RecordGoalDeleted(id, reason)` and `RecordAntiGoalDeleted(id, reason)` helpers paralleling `RecordRefined`. Events carry id, reason, timestamp. No new event-payload structs needed; the existing `Event.Kind` + `Event.TargetID` + `Event.Rationale` fields suffice.
- New tests: [internal/mcp/tools_spec_write_goal_test.go](../../internal/mcp/tools_spec_write_goal_test.go) and `tools_spec_write_antigoal_test.go` — cover (a) propose creates the node; (b) revise preserves `CreatedAt`, bumps `UpdatedAt`; (c) delete removes from store AND from disk; (d) delete with unknown id errors cleanly; (e) the history event lands.

**Discipline:** [[feedback-cite-djs-before-spec-work]] applies — commit message references DJ-139 + the constraint "delete tools are reserved for the goal layer because the model is otherwise append-only." [[feedback-agent-conventions-checklist-first]] applies to the tool `Description` text — registration-level prose, not playbook-level. Walk the six numbered anti-patterns BEFORE authoring each description.

**Tests added (12 total — 6 per node kind):**

- `TestSpecProposeGoalCreatesNode`
- `TestSpecReviseGoalPreservesCreatedAt`
- `TestSpecDeleteGoalRemovesFromStoreAndDisk`
- `TestSpecDeleteGoalRejectsUnknownID`
- `TestSpecDeleteGoalRecordsHistoryEvent`
- `TestSpecProposeGoalRejectsMalformedID` (must use `goal-` prefix)
- ...and the six parallel `AntiGoal` tests.

**Commit message shape:** `feat(mcp,agent,history): six MCP write tools for goal-* and agoal-* node mutation (DJ-139 phase 3)`.

**Exit criteria:** `go test ./internal/mcp/... ./internal/agent/... ./internal/history/...` green. All six tools registered and callable; history events land for deletes; no playbook calls them yet (that's Phase 6/7).

## Phase 4 — `goals_md_hash` field on `.borg/manifest.json`

**Goal:** `spec.Manifest` carries `GoalsMdHash string` and `GoalsMdSyncedAt time.Time` fields; `locutus init` initializes them empty; reads from existing manifests without these fields succeed (backwards compatible via `omitempty`); a helper function exists for computing the SHA-256 of `GOALS.md` content.

**Files expected to change:**

- [internal/spec/types.go](../../internal/spec/types.go) — add `GoalsMdHash string \`json:"goals_md_hash,omitempty"\`` and `GoalsMdSyncedAt time.Time \`json:"goals_md_synced_at,omitempty"\`` to the `Manifest` struct.
- [internal/spec/types.go](../../internal/spec/types.go) or a new file [internal/spec/manifest_hash.go](../../internal/spec/manifest_hash.go) — add `ComputeGoalsMdHash(content []byte) string` helper that returns `"sha256:" + hex(sha256(content))`. Constant prefix so future hash-algo changes can extend without breaking existing values.
- [internal/scaffold/scaffold.go](../../internal/scaffold/scaffold.go) — `locutus init` already writes a manifest; leave `GoalsMdHash` and `GoalsMdSyncedAt` empty (Phase 6's playbook populates them on first `refine goals`).
- New helpers in [internal/agent/spec_store.go](../../internal/agent/spec_store.go) or a new file: `ReadManifestHash(fsys)` and `WriteManifestHash(fsys, hash, syncedAt)` — atomic read-modify-write of the manifest so the hash update doesn't clobber other fields. These get called by the `refine goals` orchestrator (the orchestrator runs in the coding agent's process, not the daemon, so the writes go through the MCP server's existing write path OR a new MCP tool).
- **Decision point:** does manifest update flow through MCP, or is it a direct disk write by the orchestrator? Look at how `refine goals` currently updates `.borg/manifest.json` (if it does). If there's no existing MCP-mediated manifest write, the cleanest path is a new MCP tool `spec_update_goals_md_hash {hash, synced_at}`. Add this tool registration if needed.
- New test: [internal/spec/manifest_hash_test.go](../../internal/spec/manifest_hash_test.go) — covers (a) `ComputeGoalsMdHash` is deterministic for same input; (b) different inputs produce different hashes; (c) the manifest round-trips with the new fields populated; (d) backward compat — a manifest written by the pre-DJ-139 codebase reads back cleanly (empty hash + zero time).
- Test: [internal/agent/manifest_hash_io_test.go](../../internal/agent/manifest_hash_io_test.go) — covers atomic read-modify-write semantics; concurrent writes don't lose fields.

**Discipline:** Hash format choice is load-bearing — `"sha256:<hex>"` is a stable convention used elsewhere (verify by greppping the codebase for `sha256:` prefixes; if a different convention is dominant, match it). Walk [[feedback-no-aspirational-fields]] — both new fields have explicit consumers in Phase 6 (the short-circuit check).

**Tests added:**

- `TestComputeGoalsMdHashDeterministic`
- `TestComputeGoalsMdHashDifferentInputsProduceDifferentHashes`
- `TestManifestRoundTripsWithGoalsMdHashFields`
- `TestManifestBackwardCompatWithoutGoalsMdHashFields`
- `TestManifestHashAtomicReadModifyWrite`

**Commit message shape:** `feat(spec,agent): goals_md_hash + goals_md_synced_at fields on manifest with atomic update helpers (DJ-139 phase 4)`.

**Exit criteria:** `go test ./internal/spec/... ./internal/agent/...` green. Manifest schema extended; helpers in place for Phase 6 to use. No playbook reads or writes the hash yet.

## Phase 5 — `spec-goal-diff-matcher` canonical agent prompt

**Goal:** A new canonical agent prompt at `internal/scaffold/agents/spec-goal-diff-matcher.md` that takes (a) current `GOALS.md` text and (b) a list of existing goal-* / agoal-* nodes (id + source_clause + body + ceded_to + kept_in), and returns a structured diff: `Unchanged`, `Modified` (with id preserved), `Deleted` (with reason), `Added` (with kind + proposed slug). The agent is dispatched by the `refine goals` orchestrator in Phase 6.

**Files expected to change:**

- New file: [internal/scaffold/agents/spec-goal-diff-matcher.md](../../internal/scaffold/agents/spec-goal-diff-matcher.md) — canonical agent prompt with hyphenated id (per [[reference-runtime-hook-asymmetry]] and the DJ-135 hyphenation invariant). Frontmatter: `id: spec-goal-diff-matcher`, `role: matching`, `models: [{provider: anthropic, tier: balanced}, {provider: googleai, tier: balanced}]`. Body: ~120 lines following the conventions in [docs/agent-conventions.md](../../docs/agent-conventions.md). The prompt's central instruction: "for each existing node, find its closest current claim in GOALS.md by comparing against `source_clause`, or mark for deletion; for each new GOALS.md claim, find its closest existing node or mark as new; preserve IDs on revisions."
- The prompt explicitly enumerates edge cases (claim split, claim merge, significant rephrasing) and gives realistic example payloads with winplan-style ids (`agoal-fundraising`, `goal-strategic-planning-tool`) per anti-pattern #5 (no placeholder values).
- The prompt's output schema: a structured JSON object with `unchanged []string`, `modified []{id, new_source_clause, new_body, new_ceded_to?, new_kept_in?}`, `deleted []{id, reason}`, `added []{kind, title, body, source_clause, ceded_to?, kept_in?, proposed_slug}`. Schema rigor lives in the JSON-tag descriptions on the response struct (defined Go-side wherever the matcher's output gets validated).
- New test: [internal/scaffold/agents/spec_goal_diff_matcher_test.go](../../internal/scaffold/agents/spec_goal_diff_matcher_test.go) (or extend the existing test file `hyphenated_ids_dj135_test.go` if it covers all agents): covers (a) the file is loadable + non-empty; (b) the frontmatter `id:` is `spec-goal-diff-matcher`; (c) the body references the four diff categories (`Unchanged`, `Modified`, `Deleted`, `Added`); (d) the body references `source_clause` as the matching anchor; (e) the body includes realistic example payloads with concrete winplan-style ids (not placeholders like `foo`/`bar`).
- New test for example-payload realism: assert the prompt body contains at least one of `agoal-fundraising` / `goal-strategic-planning-tool` / `dec-oltp-store` (real spec-graph ids — anti-pattern #5 enforcement).

**Discipline:** **[[feedback-agent-conventions-checklist-first]] is BINDING for this phase.** Walk all six anti-patterns + four positive patterns in [docs/agent-conventions.md](../../docs/agent-conventions.md) BEFORE writing each section of the prompt:

- Anti-pattern priming — the prompt should NOT say "don't merge unrelated claims" because that primes the model on the failure mode. Say what to do instead.
- Tool-description-in-registration — the prompt references existing `mcp__locutus__spec_*` tools, but the matcher itself doesn't call MCP tools (it returns structured data to the orchestrator). So this convention applies less here than for the playbook updates.
- Positive phrasing throughout — "match by source_clause similarity" rather than "don't match by title."
- No schema-skeleton placeholders — example payloads use realistic ids.

**Tests added:**

- `TestSpecGoalDiffMatcherPromptLoadsAndIsNonEmpty`
- `TestSpecGoalDiffMatcherPromptFrontmatterHasHyphenatedID`
- `TestSpecGoalDiffMatcherPromptDescribesFourDiffCategories`
- `TestSpecGoalDiffMatcherPromptReferencesSourceClauseAnchor`
- `TestSpecGoalDiffMatcherPromptUsesRealisticExamplePayloads`

**Commit message shape:** `feat(agents): spec-goal-diff-matcher canonical prompt for GOALS.md → goal-layer diff (DJ-139 phase 5)`.

**Exit criteria:** `go test ./internal/scaffold/agents/...` green. The prompt ships in the canonical agent set; publishers (`locutus init`, `update --reset`) emit per-runtime copies; no playbook dispatches it yet (Phase 6).

## Phase 6 — `refine goals` workflow reorganization (sync → iterate → cite)

**Goal:** `locutus refine goals` runs three steps in order: (1) goal-layer bootstrap/sync with hash short-circuit; (2) existing spec_refinement iteration with goal layer as context; (3) citation walk against the final state. The one-time bootstrap affordance for existing `dec-*-scope-boundary` style nodes is in step 1.

**Files expected to change:**

- [internal/scaffold/plans/spec_refinement.md](../../internal/scaffold/plans/spec_refinement.md) — major prose update. Add a new "Step 0: Goal-layer sync" at the top:
  - Read `GOALS.md` content; compute SHA-256.
  - Call `mcp__locutus__spec_list_manifest`; check whether the manifest's `goals_md_hash` matches.
  - On match → skip to Step 1 (existing iteration). Update the TodoWrite plan to reflect the skip.
  - On mismatch → dispatch `spec-goal-diff-matcher` via the `Task` tool, passing current `GOALS.md` + the list of existing `goal-*` / `agoal-*` nodes (fetched via batched `spec_get`).
  - For first-run bootstrap (no existing goal-* / agoal-* nodes AND existing `dec-*` content encodes scope), the matcher's invocation includes the affordance instruction: "treat decisions whose body enumerates scope claims as secondary sources alongside GOALS.md for this one-time bootstrap." Subsequent passes drop this branch.
  - Apply the matcher's diff via `spec_propose_goal` / `spec_revise_goal` / `spec_delete_goal` and AntiGoal variants.
  - Update manifest with new hash + timestamp via `spec_update_goals_md_hash` (the new tool from Phase 4).
- Extend "Start here" in the existing playbook to fetch `goal-*` / `agoal-*` nodes alongside the manifest. The subagents (spec-scout, etc.) get the goal layer as context implicitly via the manifest fetch.
- Add a new step at the end of the iteration: "Step N+1: Citation walk." For every dec/feat/strat/app node touched in this iteration OR cited a goal-layer id that changed in step 0, the orchestrator judges `.advances` and `.respects` against the final state. Skip nodes whose body wasn't touched AND whose citations point at unchanged goal-layer ids. Update via `spec_revise_*` with only the citation fields populated.
- At-risk surface: features whose final state has empty `.advances` after the walk are surfaced in the playbook's final report under a new "Features without goal anchors" section.
- Update the convergence verdict line guidance: convergence now also requires the citation walk to complete with no further updates needed.
- New test: [internal/scaffold/plans/spec_refinement_dj139_test.go](../../internal/scaffold/plans/spec_refinement_dj139_test.go) — covers (a) the playbook references `spec-goal-diff-matcher`; (b) the playbook references the manifest hash and the new manifest-update tool; (c) the playbook describes the three steps in order (sync, iterate, cite); (d) the playbook references the at-risk surface; (e) the playbook references the bootstrap affordance for scope-encoding decisions.

**Decision point during implementation:** Does the citation walk get its own subagent dispatch (`spec-citation-walker`) or is it inline orchestrator judgment? The DJ leaves this open. Inline is simpler (no new agent); subagent gives focused prompt + cleaner tool-call boundary. Default to inline for v1; promote to subagent if the inline judgment proves unreliable in empirical use.

**Discipline:** **[[feedback-agent-conventions-checklist-first]] BINDING.** The playbook is a coding-agent prompt; same conventions apply. Watch especially for:

- The three-step prose must use positive framing — describe what the orchestrator does, not what it must avoid.
- Tool-description-in-registration — reference `mcp__locutus__spec_*` tools by name but DON'T re-describe their inputs/outputs (those live in the tool descriptions Phase 3 wrote).
- The bootstrap affordance is a one-time behavior — the prompt must clearly mark it as conditional on "first run with no existing goal-* nodes" rather than describing it as a permanent feature.

**Tests added:**

- `TestSpecRefinementPlaybookReferencesGoalDiffMatcher`
- `TestSpecRefinementPlaybookReferencesManifestHash`
- `TestSpecRefinementPlaybookDescribesThreeStepsInOrder`
- `TestSpecRefinementPlaybookReferencesAtRiskSurface`
- `TestSpecRefinementPlaybookReferencesBootstrapAffordance`

**Commit message shape:** `feat(plans): refine goals workflow sync→iterate→cite with goal-layer bootstrap (DJ-139 phase 6)`.

**Exit criteria:** `go test ./internal/scaffold/plans/...` green. End-to-end smoke test (deferred to Phase 9's empirical validation): `locutus refine goals` on a fresh winplan-style spec graph completes the bootstrap and produces `goal-*` / `agoal-*` nodes from a populated `dec-product-scope-boundary`.

## Phase 7 — `feature_ingestion` (import) playbook update

**Goal:** `locutus import <feature>` reads the goal layer, detects conflicts structurally against `agoal-*` nodes (instead of LLM-judging against GOALS.md prose), drafts a unified GOALS.md diff when the feature plausibly fits under an extended/new carve-out, and populates `.advances` / `.respects` on admission.

**Files expected to change:**

- [internal/scaffold/plans/feature_ingestion.md](../../internal/scaffold/plans/feature_ingestion.md) — prose update:
  - Context-fetch step extends to read `goal-*` and `agoal-*` nodes alongside the manifest.
  - Conflict-detection step rewritten: instead of LLM-judging "does this feature touch out-of-scope language in GOALS.md prose," check the feature's described behavior against each `agoal-*` node's `body` and `kept_in` arrays. The conflict is a structural test (does the feature's domain overlap an anti-goal's exclusion?), not a prose comparison.
  - Conflict-resolution step gains a new branch: when conflicts surface AND the feature plausibly fits under an extended/new carve-out, the playbook drafts a unified diff against `GOALS.md` (using `Read` tool to see current GOALS.md, then emitting a `diff` block to stdout). The existing "stop and ask" behavior is the fallback when no diff suggestion is appropriate.
  - On feature admission, the playbook populates `.advances` (which goals the feature advances) and `.respects` (which anti-goals it navigates around) via the propose tool's new optional parameters from Phase 2.
- New test: [internal/scaffold/plans/feature_ingestion_dj139_test.go](../../internal/scaffold/plans/feature_ingestion_dj139_test.go) — covers (a) the playbook references `goal-*` / `agoal-*` node prefixes; (b) the playbook references `kept_in` for carve-out fit judgment; (c) the playbook describes the unified-diff drafting behavior; (d) the playbook describes populating `.advances` / `.respects` on admission.

**Discipline:** Same playbook-authoring discipline as Phase 6. Walk anti-patterns; positive framing; no placeholder ids in examples.

**Tests added:**

- `TestFeatureIngestionPlaybookReadsGoalLayer`
- `TestFeatureIngestionPlaybookReferencesKeptInForCarveOuts`
- `TestFeatureIngestionPlaybookDescribesGoalsMdDiffDrafter`
- `TestFeatureIngestionPlaybookPopulatesAdvancesAndRespectsOnAdmit`

**Commit message shape:** `feat(plans): feature_ingestion reads goal layer + drafts GOALS.md diffs for carve-outs (DJ-139 phase 7)`.

**Exit criteria:** `go test ./internal/scaffold/plans/...` green. Empirical validation deferred to Phase 9's dashboard re-run.

## Phase 8 — Status renderer + at-risk surface

**Goal:** `locutus status --full` surfaces goal-layer counts, recent goal-layer activity, and the "features without goal anchors" at-risk surface.

**Files expected to change:**

- [internal/render/snapshot.go](../../internal/render/snapshot.go) — extend `SnapshotData` with `GoalCount int`, `AntiGoalCount int`, and `FeaturesWithoutGoalAnchors []string` fields. Extend `BuildSnapshotData` to compute these from the loaded spec. Extend `SnapshotMarkdown` to render a "Goal layer" section (counts) and an at-risk section under Validation.
- [cmd/snapshot.go](../../cmd/snapshot.go) — `GatherSnapshotData` already loads the spec; the goal-layer counts come from the loaded spec naturally. The at-risk computation walks features and checks `.advances` empty.
- New test: [cmd/snapshot_goal_layer_test.go](../../cmd/snapshot_goal_layer_test.go) — covers (a) snapshot includes goal/antigoal counts; (b) snapshot lists features without goal anchors when any exist; (c) absent when none exist; (d) markdown rendering of the new sections.
- [internal/spec/loaded.go](../../internal/spec/loaded.go) — extend `Loaded` to include `Goals []GoalNode` and `AntiGoals []AntiGoalNode` slices alongside the existing `Features`, `Strategies`, etc. Add `GoalNode` and `AntiGoalNode` types paralleling `FeatureNode` (struct wrapping the spec type + load metadata). Add `GoalNodeByID` and `AntiGoalNodeByID` accessor methods. Extend `LoadSpec` to walk `.borg/spec/goals/` and `.borg/spec/antigoals/` and populate these slices.

**Discipline:** [[feedback-locutus-voice-neutrality]] — describe what's there, don't editorialize. "5 features without goal anchors" not "5 features need attention."

**Tests added:**

- `TestSnapshotIncludesGoalLayerCounts`
- `TestSnapshotListsFeaturesWithoutGoalAnchors`
- `TestSnapshotAbsentWhenNoFeaturesWithoutAnchors`
- `TestSnapshotMarkdownRendersGoalLayerSection`
- `TestLoadSpecPopulatesGoalsAndAntiGoals`

**Commit message shape:** `feat(render,cmd/status): goal-layer counts + features-without-anchors at-risk surface (DJ-139 phase 8)`.

**Exit criteria:** `go test ./internal/render/... ./internal/spec/... ./cmd/...` green. `locutus status --full` shows the new sections.

## Phase 9 — Documentation, empirical validation, and DJ status flip

**Goal:** All docs reflect the new surface; empirical validation against the winplan dashboard scenario demonstrates the end-to-end flow; DJ-139 status flips from `design` to `shipping`.

**Files expected to change:**

- [CLAUDE.md](../../CLAUDE.md) — under "Project" / "Sources of Truth" / "Architecture invariants" / "Command Surface": update the spec-model summary line to include `Goal → Decision → (Feature | Strategy) → Approach` with the goal-layer mention. Document the `goal-` / `agoal-` prefixes alongside the existing five. Update the validator-regex example. Add a Sources-of-Truth bullet for the goal layer with a one-line description and DJ-139 link.
- [docs/mcp.md](../../docs/mcp.md) — document the six new MCP tools in the existing tool table.
- [docs/activities.md](../../docs/activities.md) — extend the `spec_refinement` activity section with the new three-step workflow (sync → iterate → cite). Mention the `spec-goal-diff-matcher` subagent's role.
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — add `spec-goal-diff-matcher` to the canonical agent set; document its load-bearing prompt requirements (preserve IDs via `source_clause`, mint new IDs only for genuinely new claims). Note that its example payloads use realistic ids per anti-pattern #5.
- [docs/debugging-traces.md](../../docs/debugging-traces.md) — extend if relevant (mention the new tool call shapes that show up in `tools.jsonl` during `refine goals` sync).
- [docs/decisions/dj-139-goal-layer-node-kinds.md](../../docs/decisions/dj-139-goal-layer-node-kinds.md) — flip `Status:` from `design` to `shipping` with per-phase summary mirroring DJ-138's pattern.
- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — flip DJ-139's manifest-row status column from `design` to `shipping`.
- [.claude/plans/dj-139-goal-layer-node-kinds.md](../../.claude/plans/dj-139-goal-layer-node-kinds.md) — this plan's `Status:` flips to `DONE`.

**Empirical validation:** run `locutus refine goals` against winplan (`~/projects/winplan/.borg/spec/`, per the reference shoe-project memory note). Observe:

- The goal-layer bootstrap fires (no existing `goal-*` / `agoal-*` nodes).
- The matcher correctly decomposes `dec-product-scope-boundary` into individual `agoal-*` nodes.
- The citation walk populates `.advances` / `.respects` on existing features/decisions.
- `locutus status --full` shows the new goal-layer section.
- Then run `locutus import docs/dashboard.md` (the same import that failed in session `20260527/0310/010000`).
- Observe the playbook now drafts a `GOALS.md` diff for the carve-outs.

Document the empirical findings inline in this plan's Phase 9 section so future readers see what actually happened vs. what was planned.

**Discipline:** [[feedback-council-doc-maintenance]] does NOT apply — DJ-139 doesn't touch the council. The explicit exemption is documented in DJ-139's Documentation consequence section. Confirm in the Phase 9 commit message that council.md was considered and explicitly skipped, so the reviewer doesn't trip the checklist.

**Tests added:** none (docs + empirical validation; no new code).

**Commit message shape:** `docs(dj-139): CLAUDE.md, mcp, activities, agent-conventions + empirical validation against winplan (DJ-139 phase 9)`.

**Exit criteria:** all docs updated; the empirical-validation note in this plan describes what `refine goals` and `import` actually did on a real graph. DJ-139 manifest row + per-decision file both show `shipping`.

## After all phases

- Run the full test suite + vet + race: `go build ./... && go test ./... && go vet ./... && go test ./... -race`. All four green.
- Update DJ-139's `Status:` line in `docs/decisions/dj-139-goal-layer-node-kinds.md` from `design` to `shipping` with per-phase summary mirroring DJ-138.
- This plan's `Status:` flips to `DONE`.

## Out of scope (explicitly)

These are tee'd up as Future Work in DJ-139; not part of this plan:

- Witness-state precision for citation drift.
- `locutus refine --with` targeting `goal-*` / `agoal-*` nodes.
- `locutus apply-goals-diff <patch>` helper.
- Inverse-direction `locutus drift goals` verb.
- `goals_synced` cascade root event.
- Per-runtime playbook overlays for `refine goals`.

## Risks / known unknowns

- **(a) Diff-matcher consistency.** The `spec-goal-diff-matcher` agent's matching judgments may be inconsistent across runs. Mitigation: source_clause comparison provides textual grounding; the prompt explicitly directs ID preservation via source_clause matching. Empirical validation in Phase 9 surfaces real behavior.
- **(b) Citation walk cost on large graphs.** Winplan-scale (~100 nodes) bootstrap walk is ~50-100 judgments. Larger projects feel it more. Mitigation: subsequent passes use the no-change optimization (skip nodes whose body wasn't touched + whose citations point at unchanged goal-layer ids).
- **(c) The bootstrap affordance heuristic.** "Treat existing scope-encoding decisions as secondary sources on first run" is a one-time hack. Risk: scope claims spread across multiple decisions might require operator help to seed the goal layer. Mitigation: operator can hand-seed `GOALS.md` before the first refine goals if the heuristic falls short. Document this in CLAUDE.md or a release note.
- **(d) Polarity confusion as a coding-agent failure mode.** A new class of agent error: features with `.advances: [agoal-X]` (wrong polarity) or `.respects: [goal-Y]` (wrong polarity). Schema-level validation rejects malformed ids based on prefix (Phase 1's `validSpecID` regex); semantic polarity (the agent put a goal-id in `.respects` because it thought the feature was navigating it) needs prompt discipline in the citation walk and a follow-up at-risk surface in `status --full` validation.
- **(e) Existing manifests need careful read-path handling.** Phase 4 introduces `GoalsMdHash` and `GoalsMdSyncedAt` as optional fields; existing winplan manifests don't have them. The Go decode handles missing JSON fields gracefully (zero-values), but the atomic read-modify-write helpers must preserve all other manifest fields when updating. Phase 4's tests cover this explicitly.
- **(f) The `spec-goal-diff-matcher` agent prompt is novel and load-bearing.** Anti-pattern priming would be especially harmful here because the matcher produces structured output (the diff). [[feedback-agent-conventions-checklist-first]] is enforced via Phase 5's discipline section.

## Reference

Synthesizes the design conversation on 2026-05-27 starting from the failed dashboard import on winplan (session `20260527/0310/010000`). The user identified the core problem: GOALS.md → spec graph is non-idempotent in goal interpretation, so GOALS.md edits have unbounded blast radius and import conflicts have no graph-binding. The brainstorm crystallized a goal-layer of persisted LLM interpretation as the structural fix; the polarity-typed Goal/AntiGoal split (user reframing of an enum-discriminator approach), the informational-citation model (user pushback on initial over-prescriptive schema), and the hash-keyed sync algorithm fell out of subsequent design questions. Final design lives in [DJ-139](../../docs/decisions/dj-139-goal-layer-node-kinds.md).
