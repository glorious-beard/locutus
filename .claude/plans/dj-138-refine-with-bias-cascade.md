# DJ-138 — `locutus refine --with` Strong-Bias Cascade

> **Governing DJ:** [DJ-138](../../docs/DECISION_JOURNAL.md#dj-138). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** DONE — implemented 2026-05-27 across all 7 phases on `main`. Phase 1: per-activity `max_iterations` in registry. Phase 2: `spec_mark_approach_drifted` MCP tool. Phase 3: `spec_biased` + `approach_drifted` history events with `caused_by` linkage. Phase 4: `refine --with` flag + tightened Approach/Bug rejection in both modes. Phase 5: `spec_bias` activity + cross-runtime playbook. Phase 6: `status --full` surfaces recent `spec_biased` events. Phase 7: docs (CLAUDE.md, activities.md, mcp.md) updated, DJ status flipped to `shipping`. Empirical end-to-end validation against winplan deferred to operator's first `--with` invocation.
>
> **Predecessors:** [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135) (retired the pre-existing `refine --brief / --supersede / --diff / --rollback` flag surface; established the activity-dispatch architecture this DJ builds on); [DJ-136](../../docs/DECISION_JOURNAL.md#dj-136) (per-runtime idiomatic dispatch — DJ-138 ships with a cross-runtime default playbook and inherits the plan-event surfacing); [DJ-137](../../docs/DECISION_JOURNAL.md#dj-137) (justify restoration — proves the activity-dispatch pattern for revived verbs and establishes the read-only counterpart to DJ-138's write-cascade); [DJ-133](../../docs/DECISION_JOURNAL.md#dj-133) (axis-shaped decision ids — the load-bearing invariant that makes decision flips byte-stable across `--with` invocations, eliminating the id-rewriting machinery the pre-DJ-135 `--supersede` carried).
>
> **Surface area:** medium. ~50-70 lines of CLI (`cmd/refine.go` extension), ~120-160 lines of new playbook prose (`internal/scaffold/plans/spec_bias.md`), one new MCP tool (`spec_mark_approach_drifted`) at ~40-60 lines of handler code + registration, two new history event types at ~30-50 lines each, an activity-registry refactor for per-activity iteration caps (~30 lines) plus the parallel updates to `internal/runner/run.go`, validation tightening on plain `refine` (~20 lines), status renderer extension (~40 lines), tests + docs.
>
> **Discipline (per memory):**
>
> - Tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching ([[feedback-test-first]]).
> - Cite DJ-138 + the precise constraint in chat before touching `cmd/refine.go`, the playbook, the new MCP tool, or the new event types ([[feedback-cite-djs-before-spec-work]]).
> - Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) as a checklist BEFORE authoring `internal/scaffold/plans/spec_bias.md` ([[feedback-agent-conventions-checklist-first]]); the playbook is a coding-agent prompt and is subject to the same anti-pattern priming, positive-phrasing, and tool-description-in-registration rules as the agent prompts under `internal/scaffold/agents/`.
> - **No** [docs/council.md](../../docs/council.md) update required — DJ-138 does not touch the council. The council was retired in DJ-135 phase 5; `spec_bias`'s playbook does not invoke the surviving `spec-advocate` / `spec-challenger` / `justify-researcher` sub-council. The [[feedback-council-doc-maintenance]] checklist is satisfied by the explicit exemption documented in DJ-138's Documentation consequence.
> - No back-compat shims needed for AgentDef or activity-registry shape changes ([[feedback-no-back-compat-until-self-hosting]]) — the user runs `locutus update --offline --reset` before every operation.
> - Locutus voice neutrality in playbook prose ([[feedback-locutus-voice-neutrality]]): no "good-enough", "MVP", "ship-ready" framing in the playbook; describe what the orchestrator does, not how good Locutus thinks it is.
> - No aspirational fields ([[feedback-no-aspirational-fields]]): the `caused_by` field has a real consumer (`locutus history --since`); `spec_biased.blast_radius` has a real consumer (the pre-cascade estimate printed to stdout); `approach_drifted` events have a real consumer (`status` rendering + audit walks). All three are justified; no other new fields enter the schema.

## Why this plan exists

The user surfaced demand for the pre-DJ-135 write-cascade surface on 2026-05-26 with two architectural improvements over the old four-flag (`--brief`, `--supersede`, `--diff`, `--rollback`) vocabulary:

1. **Collapse the flag surface to a single `--with "<bias>"`** that the playbook judges intensity from — `--brief`'s soft-rewrite and `--supersede`'s structural-flip become one verb with two emergent behaviors.
2. **Add a backward direction** that didn't exist in the pre-DJ-135 model — `--with` targeting a Feature or Strategy walks *backward* to identify upstream decisions to flip, then forward-cascades from each. The old surface only cascaded forward (decision → features → strategies → approaches).

The user's third insight — the phase boundary at "presence of any Approach node" — resolves an apparent design tension by separating the pre-approach exploratory phase (where refinement is essentially free) from the post-approach commitment phase (where refinement has consequences code may need to reflect). The same conservative-closure-mark drift posture works in both phases; the set of affected approaches is just empty in Phase 1.

DJ-138 articulates that design. This plan breaks the implementation into phases that each leave the system in a working state.

## Reference state (before DJ-138 starts)

- **CLI surface** at [cmd/refine.go](../../cmd/refine.go) — `RefineCmd { Target string }` with `Target` as positional arg, optional, defaulting to `"goals"`. Dispatches `spec_refinement` activity via `runActivityVerb`. No validation of node-kind on `Target` today; the playbook receives `"Target: <value>"` as a context note and decides what to do.
- **Activity registry** at [internal/activity/agents-default.yaml](../../internal/activity/agents-default.yaml) maps activities → prioritized runtime list. After DJ-137 ships, six entries exist: `spec_refinement`, `feature_ingestion`, `code_adoption`, `code_assimilation`, `justification`. DJ-138 adds `spec_bias` as the seventh. No `max_iterations` field exists in the schema today — the cap is hardcoded as `const maxIterations = 20` in [internal/runner/run.go:137](../../internal/runner/run.go#L137).
- **Activity struct** at [internal/activity/registry.go](../../internal/activity/registry.go) — `Activity { Name string; Runtimes []string }`. DJ-138 extends with `MaxIterations int`.
- **Existing MCP write tools** at [internal/mcp/tools_spec_write.go](../../internal/mcp/tools_spec_write.go) — `spec_propose_decision`, `spec_propose_feature`, `spec_propose_strategy`, `spec_revise_decision`, `spec_revise_feature`, `spec_revise_strategy`. DJ-138 adds `spec_mark_approach_drifted`.
- **Approach struct** at [internal/spec/approach.go](../../internal/spec/approach.go) — already carries `InvalidatedByEventID string` field with `IsInvalidated() bool` accessor. Set by pre-DJ-135 `--supersede` in the council era; not currently written by any MCP path. The doc comment explicitly anticipates the cascade-mark use case ("the approach's other fields … remain on disk as the blast-radius input the next adopt run consumes").
- **History events** — the event log lives under `.borg/spec/.history/` (per [docs/debugging-traces.md](../../docs/debugging-traces.md) and the existing event schema). Existing event types include `spec_revised` and `spec_proposed`. No `caused_by` field; events have no causal-tree linkage today. DJ-138 adds the field as optional plus two new event types (`spec_biased`, `approach_drifted`).
- **Status renderer** — `locutus status` and `locutus status --full` at [cmd/status.go](../../cmd/status.go) (or wherever the status entry point lives — check before Phase 6). Already surfaces drifted approaches via `Approach.IsInvalidated()`; needs extension to surface recent `spec_biased` events for `--full` mode.
- **Playbook iteration discipline** — the post-DJ-135-phase-5 convergence pattern (commits `fd8506a`, `4baf3fe`) is the model `spec_bias.md` follows. Look at `internal/scaffold/plans/spec_refinement.claude-code.md` for the canonical example of a converging playbook.

## Resolved design questions

Recorded in chat 2026-05-26 and settled in [DJ-138](../../docs/DECISION_JOURNAL.md#dj-138). Restated here for implementation reference (verbatim from the DJ where applicable):

1. **Bias shape is natural-language string** — `--with "use postgres, the team owns ops"`. No structured directives, no hybrid forms, no multiple `--with` flags per invocation.
2. **New-option case: add-and-promote in one shot** — atomic, no confirmation gate, no draft-and-pause. Strong bias = strong obedience.
3. **Full cascade in both directions** — forward from flipped decisions; backward then forward from feature/strategy targets.
4. **`<id>` mandatory and non-Goal under `--with`** — explicit node-targeted mode; no discovery-from-bias-text.
5. **Valid kinds under `--with`: Decision / Feature / Strategy** — the mutable deliberation layer.
6. **Approach and Bug rejected as refine targets entirely** — not just under `--with`. Tightens plain `refine` validation.
7. **Forward cascade adds proactive citations** — features/strategies that should cite a flipped decision get a citation added.
8. **Backward cascade may add new decisions** — bias implies an axis with no decision yet → `spec_propose_decision`, axis-shaped id, then cite.
9. **Rationale-only refresh is a valid `--with`** — tightening prose without flipping options is the degenerate-but-valid form of strong bias.
10. **Phase boundary is the presence of any Approach** — Phase 1 drift set is empty; Phase 2 drift set is the reference-graph closure.
11. **Drift posture: conservative closure mark (option A)** — `InvalidatedByEventID` set on every approach in the closure. Witness-state precision (option B) deferred to a follow-up DJ.
12. **Iteration cap: per-activity in agents-default.yaml** — default 20 for `spec_bias`. Refactor in scope: replace the hardcoded const with a registry lookup for **all** activities.
13. **Approach-drift write surface: explicit `spec_mark_approach_drifted` MCP tool** — every drift mark is a traced tool call in `tools.jsonl`.
14. **Best-effort cascade, git as the rollback layer** — no transactional spec mutations.
15. **No paternalistic dirty-working-tree check** — documented as DX hygiene, not enforced.
16. **Cross-runtime playbook only** — no `.<runtime>.md` overlay in v1.

## Phase 1 — Activity-registry refactor for per-activity iteration caps

**Goal:** all six activities (`spec_refinement`, `feature_ingestion`, `code_adoption`, `code_assimilation`, `justification`, plus the upcoming `spec_bias`) read their iteration cap from the activity registry rather than the hardcoded `const maxIterations = 20` in `internal/runner/run.go`. Foundational refactor — `spec_bias` depends on it and the other five benefit from it.

**Files expected to change:**

- [internal/activity/registry.go](../../internal/activity/registry.go) — extend `Activity` struct with `MaxIterations int` field; default to **20** when the YAML omits it (back-compat with hand-written `.borg/agents.yaml`).
- [internal/activity/agents-default.yaml](../../internal/activity/agents-default.yaml) — add explicit `max_iterations: 20` to all five existing activity entries. The default behavior matches the hardcoded const, so this is a zero-behavior-change baseline.
- [internal/runner/run.go](../../internal/runner/run.go) — `runOuterLoopDispatch` accepts the cap as a parameter (probably `maxIterations int`); the hardcoded `const maxIterations = 20` at line 137 is removed. `runActivityVerb` (in `cmd/activity_verb.go`) passes the resolved activity's cap to `runOuterLoopDispatch`.
- [cmd/activity_verb.go](../../cmd/activity_verb.go) — `runActivityVerb` resolves the activity once and threads `act.MaxIterations` into the dispatch path. No other changes.
- [internal/activity/registry_test.go](../../internal/activity/registry_test.go) — new test asserting (a) the YAML loader picks up `max_iterations` when set, (b) the default-20 fallback fires when the field is absent.
- [internal/runner/run_test.go](../../internal/runner/run_test.go) (or wherever the outer-loop is tested) — new test asserting the cap parameter is honored: a fake dispatch that never reports `converged: true` terminates after the configured cap (test with cap=3, assert exactly 3 iterations).

**Discipline:** [[feedback-test-first]] is binding. Write the registry test asserting the new field shape **first**, watch it fail, then add the struct field. Same for the runner cap test: failing test first, then plumb the parameter through.

**Tests added:**

- `TestActivityRegistryLoadsMaxIterations` — covers explicit value in YAML.
- `TestActivityRegistryDefaultsMaxIterationsTo20` — covers omitted value.
- `TestOuterLoopHonorsConfiguredCap` — covers the cap parameter actually firing termination.

**Commit message shape:** `refactor(activity,runner): per-activity max_iterations replaces hardcoded const (DJ-138 phase 1)`.

**Exit criteria:** `go test ./internal/activity/... ./internal/runner/... ./cmd/...` green; `go vet ./...` green. The five existing activities keep their behavior (default 20). No new user-visible surface yet.

## Phase 2 — New MCP tool: `spec_mark_approach_drifted`

**Goal:** the cascade has an explicit, traced write surface for marking approaches drifted. Every drift mark appears as a tool call in `.locutus/sessions/<...>/tools.jsonl`. Foundational for the playbook (Phase 5) — the tool must exist before the playbook can reference it.

**Files expected to change:**

- [internal/mcp/tools_spec_write.go](../../internal/mcp/tools_spec_write.go) — add the tool registration + handler. The tool takes `{approach_id: string, event_id: string}` and sets `Approach.InvalidatedByEventID = event_id`. Description text per [docs/agent-conventions.md](../../docs/agent-conventions.md) — full registration-level prose explaining the conservative-closure-mark semantics, why drift is set rather than re-synthesizing, and the relationship to `spec_biased` events. No playbook-level prose duplicates this.
- [internal/agent/spec_store.go](../../internal/agent/spec_store.go) — verify (don't necessarily add) that `SpecStore.Put` accepts an updated `Approach` with `InvalidatedByEventID` set and persists the field. If not, add a small `SpecStore.MarkApproachDrifted(id, eventID string)` helper that's idiomatic for the store's typed-entry pattern.
- [internal/mcp/tools_spec_write_drift_test.go](../../internal/mcp/tools_spec_write_drift_test.go) — new test file covering: (a) the tool's surface contract (input shape, output shape); (b) the persistence round-trip (call tool → read back via `spec_get` → assert `InvalidatedByEventID` is set); (c) idempotency (calling twice with the same event id is a no-op); (d) rejection of unknown approach ids; (e) rejection of non-Approach target ids (decision/feature/strategy ids are not Approaches and must error).

**Discipline:** [[feedback-cite-djs-before-spec-work]] — state in the commit message that DJ-138 governs this tool and the constraint is "explicit traced writes preferred over implicit side-effects." [[feedback-agent-conventions-checklist-first]] applies to the tool's `Description` text — it ships as part of the MCP tool surface, walked by orchestrators, and is subject to the same prose conventions as agent prompts.

**Tests added:**

- `TestSpecMarkApproachDriftedSetsField`
- `TestSpecMarkApproachDriftedIdempotent`
- `TestSpecMarkApproachDriftedRejectsUnknownID`
- `TestSpecMarkApproachDriftedRejectsNonApproachTarget`

**Commit message shape:** `feat(mcp): spec_mark_approach_drifted tool for explicit cascade drift writes (DJ-138 phase 2)`.

**Exit criteria:** `go test ./internal/mcp/... ./internal/agent/...` green. The tool is registered and callable; no MCP client has invoked it yet (that lands in Phase 5).

## Phase 3 — New history event types + `caused_by` field

**Goal:** `--with` runs produce a walkable causal tree in the history log. `spec_biased` is the root; child `spec_revised` / `spec_proposed` / `approach_drifted` events carry `caused_by` pointing at the root. Foundational for the audit trail and for `locutus history --since <bias-event-id>`.

**Files expected to change:**

- [internal/history/](../../internal/history/) — the event schema lives here (the exact file depends on the current layout; check `event.go` or similar before starting). Add:
    - Optional `caused_by string` field to the shared event base type. Existing events ignore it when unset; serialization preserves it when present.
    - New `spec_biased` event type with: `target_id`, `bias_text`, `dispatch_timestamp`, `blast_radius_estimate` (a small struct: `{decisions_touched, features_rewritten, strategies_rewritten, approaches_drifted int}`), `acp_session_id string`.
    - New `approach_drifted` event type with: `approach_id`, `upstream_event_id` (which decision/feature/strategy rewrite triggered the drift), `caused_by` (the originating `spec_biased` event id).
- [internal/history/event_test.go](../../internal/history/event_test.go) — new tests covering (a) `spec_biased` event creation + serialization round-trip; (b) `approach_drifted` event creation + serialization round-trip; (c) `caused_by` field round-trip on existing event types; (d) event-log read-back of a small cascade tree (one `spec_biased` + two `spec_revised` children linked via `caused_by`).
- [docs/debugging-traces.md](../../docs/debugging-traces.md) — extend the event-shape section to document the new event types and the `caused_by` linkage. Keep additions consistent with the existing prose style (no marketing voice).

**Discipline:** [[feedback-no-aspirational-fields]] is binding — every new field has a documented consumer. `caused_by` is consumed by Phase 6 (`status --full`) and by `locutus history --since`. `blast_radius_estimate` is consumed by the pre-cascade stdout print (Phase 5). `upstream_event_id` is consumed by `status` rendering (Phase 6). No optional fields without consumers.

**Tests added:**

- `TestSpecBiasedEventRoundTrip`
- `TestApproachDriftedEventRoundTrip`
- `TestCausedByFieldOnExistingEvents`
- `TestHistoryReadBackOfCascadeTree`

**Commit message shape:** `feat(history): spec_biased + approach_drifted events with caused_by linkage (DJ-138 phase 3)`.

**Exit criteria:** `go test ./internal/history/...` green. The event types are serializable, persistent, and round-trip correctly. No write path produces them yet (that lands in Phase 5).

## Phase 4 — CLI surface: `--with` flag + tightened validation

**Goal:** `locutus refine <id> --with "<bias>"` parses, validates, and dispatches the `spec_bias` activity. Plain `refine [<id>]` continues to work but with tightened kind validation (Approach + Bug rejected).

**Files expected to change:**

- [cmd/refine.go](../../cmd/refine.go) — extend `RefineCmd` struct with `With string` field; kong tags: `optional:""` `help:"Strong-bias cascade — applies the bias to <target> and cascades through the spec graph in both directions. Requires <target>; rejects Goal, Approach, Bug kinds."`. Update `Run()` to:
    1. Validate empty/whitespace bias when `With != ""` → reject with clear message.
    2. Validate `Target == "goals"` (the default) when `With != ""` → reject with "`--with` requires an explicit non-Goal target".
    3. Resolve `Target` to a spec node; if missing, reject with "unknown spec id: <id>" (use existing fuzzy-suggest from `locutus list` if available).
    4. Reject Goal / Approach / Bug kinds when `With != ""`.
    5. Reject Approach / Bug kinds **regardless of `With`** (the plain-mode tightening from DJ-138 resolved-question 6) — Goal stays allowed in plain mode as today.
    6. Route to `spec_bias` activity when `With != ""`; preserve routing to `spec_refinement` otherwise.
- `contextNote()` helper — extend to include the bias text and the activity name in the context note when `--with` is present. The playbook receives both via the same `Target:` / `Bias:` / `Run-ID:` lines pattern the other activities use.
- [cmd/refine_test.go](../../cmd/refine_test.go) (extend if it exists; create if not) — new tests covering:
    - `--with` rejected when `Target` is omitted / equals `goals`.
    - `--with` rejected when bias is empty / whitespace-only.
    - `--with` rejected when target id doesn't resolve.
    - `--with` rejected when target resolves to Goal.
    - `--with` rejected when target resolves to Approach.
    - `--with` rejected when target resolves to Bug.
    - `--with` accepted when target resolves to Decision / Feature / Strategy + bias is non-empty.
    - Plain `refine <approach-id>` rejected (the new tightening).
    - Plain `refine <bug-id>` rejected (the new tightening).
    - Plain `refine` and `refine goals` still work as today.
    - The `contextNote()` shape when `--with` is present (target + bias + run-id all in the context).
    - The activity routing (`runActivityVerb` called with `spec_bias` when `With != ""`).

**Discipline:** [[feedback-test-first]] applies — write the failing validation tests first, then add the `With` field + the validation logic. The plain-mode tightening (Approach/Bug rejection) is a *behavior change* on an existing verb; the test names should make this explicit so it's visible in test output if it surfaces as a regression in another consumer.

**Tests added:** see file list above (~12 new tests).

**Commit message shape:** `feat(cmd/refine): --with strong-bias flag + tighten kind validation (DJ-138 phase 4)`.

**Exit criteria:** `go test ./cmd/...` green; `go vet ./...` green. `locutus refine <id> --with "..."` parses cleanly and dispatches the `spec_bias` activity, but no playbook exists yet so dispatch fails at the activity-resolution step. That's expected — Phase 5 lands the playbook.

## Phase 5 — New `spec_bias` activity + playbook

**Goal:** the `spec_bias` activity is registered in the activity registry; the playbook at `internal/scaffold/plans/spec_bias.md` directs the orchestrator through the strong-bias cascade. End-to-end `locutus refine <id> --with "..."` works.

**Files expected to change:**

- [internal/activity/agents-default.yaml](../../internal/activity/agents-default.yaml) — new `spec_bias:` entry with the standard three-runtime preference list (`claude-code`, `codex`, `gemini`) and `max_iterations: 20` (matching `spec_refinement` per DJ-138 resolved-question 12).
- `internal/scaffold/plans/spec_bias.md` — new playbook. Cross-runtime default (no `.<runtime>.md` overlay per DJ-138 resolved-question 16). Length target ~120-160 lines. Structure:
    - **Front-matter:** activity metadata.
    - **Opening:** the orchestrator's job in one paragraph (apply a strong bias to a target node and cascade the implications through the spec graph).
    - **`TodoWrite` directive** per DJ-136 phase 3 convention — surfaces the iteration plan inline so the operator sees the work scoped before any model call lands.
    - **Step 1 (read target):** `mcp__locutus__spec_get` on the target id; abort with a clear error if missing or wrong kind.
    - **Step 2 (read closure context):** fetch the reference-graph closure via batched `spec_get` — features/strategies citing the target decision (forward case) or the target's cited decisions (backward case).
    - **Step 3 (classify intent):** read the bias text in the context of the target's current state; decide whether it's a promote / add-and-promote / rationale-only / structural change. The playbook gives the orchestrator concrete examples of each case.
    - **Step 4 (apply mutations):** the appropriate `spec_revise_*` / `spec_propose_*` calls. The playbook explicitly enumerates the four MCP write tools available and tells the orchestrator which to use when.
    - **Step 5 (mark drifted approaches):** `spec_mark_approach_drifted` for every approach in the closure. The playbook gives the closure-walk algorithm explicitly: "every approach whose `parent_id` is a rewritten Feature/Strategy, plus every approach whose `decisions[]` includes a flipped decision id."
    - **Step 6 (iterate):** re-walk the closure; if any node still shows drift relative to the new state, dispatch the next iteration. Otherwise emit the final report.
    - **Step 7 (final report):** structured output naming touched nodes, the per-cascade blast-radius summary, any open drift outside the closure. Designed to be human-readable on stdout but also parseable.
    - **Convergence note:** the convergence criterion is *full closure walk produces zero new mutations*. Iteration cap is the activity's configured `max_iterations`.
    - **Error envelope:** what to do on partial-cascade failures (record what landed, surface what didn't, exit non-zero).
    - **Re-invocation note:** the playbook MUST detect nodes whose state already reflects the bias (via `spec_search` over current state) and skip them on subsequent runs — this is the playbook-authoring contract DJ-138 references for cascade idempotency.
- [internal/scaffold/plans/spec_bias_test.go](../../internal/scaffold/plans/spec_bias_test.go) — new playbook-shape tests covering:
    - Playbook file is loadable + non-empty.
    - Playbook references the expected MCP write tools (`spec_revise_decision`, `spec_revise_feature`, `spec_revise_strategy`, `spec_propose_decision`, `spec_mark_approach_drifted`).
    - Playbook references `TodoWrite` for iteration-plan surfacing.
    - Playbook includes the re-invocation skip-discipline note (assert it via a known substring like "already reflects the bias" or similar).
    - Playbook includes the closure-walk algorithm explicitly (assert via substring).
- [internal/activity/registry_test.go](../../internal/activity/registry_test.go) — extend with `TestActivityRegistryHasSpecBias` asserting the new entry loads and resolves to a runtime.

**Discipline:** **[[feedback-agent-conventions-checklist-first]] is binding for this phase.** Walk all six numbered anti-patterns + four positive patterns in [docs/agent-conventions.md](../../docs/agent-conventions.md) BEFORE writing each step of the playbook. Specifically watch for:

- **Anti-pattern priming** — the playbook should not say "don't flip decisions speculatively" because that primes the orchestrator to think about speculative flipping. Say what to do instead.
- **Tool-description-in-registration** — the playbook references MCP tools by name; the tools' behavior text lives in their `Description` at registration time (Phase 2 handled `spec_mark_approach_drifted`), not in the playbook prose. The playbook says **when** and **why** to use a tool; not **what it does internally**.
- **Positive phrasing throughout** — "mark drifted approaches via the closure walk" rather than "don't forget to mark approaches drifted".
- **No schema-skeleton placeholders** — example payloads use realistic spec-graph ids (e.g., `dec-oltp-store`, `feat-realtime-sync`), not `foo` or `bar`.

[[feedback-locutus-voice-neutrality]] also applies — the playbook's voice should describe what the orchestrator does, not how good the system is at it.

**Tests added:** see file list above (~6 new tests).

**Commit message shape:** `feat(activity,plans): spec_bias activity + playbook (DJ-138 phase 5)`.

**Exit criteria:** `go test ./internal/activity/... ./internal/scaffold/plans/...` green. End-to-end smoke test: `locutus refine <decision-id> --with "..."` on a small test fixture spec graph completes a cascade and produces expected mutations + drift marks + history events. This smoke test may be deferred to Phase 7 if it requires fixture infrastructure not present today.

## Phase 6 — Status renderer integration

**Goal:** `locutus status --full` surfaces recent `spec_biased` events alongside the existing drift summary. Small additive UI change that gives the operator visibility into recent bias-driven cascades.

**Files expected to change:**

- [cmd/status.go](../../cmd/status.go) (or wherever the `--full` renderer lives — check before starting) — extend the renderer to query the history log for `spec_biased` events from the last N (configurable, default 10) and emit a small section. Each entry shows: timestamp, target id + kind, bias text (truncated to ~80 chars), blast-radius summary, ACP session id (for linking to the session directory under `.locutus/sessions/`).
- [internal/history/](../../internal/history/) — add a small query helper if needed: `RecentBiased(n int) []SpecBiasedEvent`. If a generic recent-events query already exists, extend or filter rather than duplicating.
- [cmd/status_test.go](../../cmd/status_test.go) — new test covering: (a) `--full` includes the new section when recent `spec_biased` events exist; (b) absent gracefully when no biases have run; (c) truncation behavior on long bias text; (d) ordering (most recent first).

**Discipline:** the renderer is the operator's primary surface for "what's been happening in this spec graph"; keep the output dense and scannable, not verbose. Match the existing `--full` section style. [[feedback-locutus-voice-neutrality]] applies — describe what happened, don't editorialize.

**Tests added:**

- `TestStatusFullSurfacesRecentBiased`
- `TestStatusFullAbsentWhenNoBiases`
- `TestStatusFullTruncatesLongBias`
- `TestStatusFullOrdersBiasesMostRecentFirst`

**Commit message shape:** `feat(cmd/status): surface recent spec_biased events in --full (DJ-138 phase 6)`.

**Exit criteria:** `go test ./cmd/...` green. Manual smoke test: after a `--with` run, `locutus status --full` shows the bias in the new section.

## Phase 7 — Documentation, validation, and the empirical end-to-end

**Goal:** documentation is updated to reflect the new surface; an empirical validation run against a real spec graph (winplan, or a fresh test fixture) demonstrates the cascade works end-to-end on a realistic graph.

**Files expected to change:**

- [CLAUDE.md](../../CLAUDE.md) — under "Command Surface":
    - Update the `refine` entry to document `--with` and the tightened validation.
    - Update the retired-flags note from DJ-135's era to indicate `--with` subsumes `--brief` and `--supersede` (and that `--diff` / `--rollback` are subsumed by git).
    - **Fix the stale framing on the `<target>` positional** — CLAUDE.md currently says "focus note appended to the playbook" but the actual implementation (and help text) treats `<target>` as a node id (defaulting to `goals`). Replace with accurate prose.
    - Update verb count if necessary (the verb count itself didn't change — `refine` still exists — but the resolved-flag surface changed).
- [docs/activities.md](../../docs/activities.md) — new section for `spec_bias` paralleling the existing `spec_refinement` entry. Cover: what the activity does, when to use it (`--with` invocations), the playbook location, the MCP tools it exercises, the convergence criterion, the iteration cap.
- [docs/mcp.md](../../docs/mcp.md) — document `spec_mark_approach_drifted` alongside the other write tools. Match the existing prose style.
- [docs/refine.md](../../docs/refine.md) — new file (or extend if one exists in the docs hierarchy). Cover the `--with` surface in operator-facing prose. Include the **commit-before-risky-operations DX hygiene note** explicitly, with the git-as-rollback framing from DJ-138.
- Optional: add an example invocation to one of the docs that shows what the operator sees: the dispatch line, the blast-radius estimate, the cascade events, the final report.

**Empirical validation:** run `locutus refine <some-decision-id> --with "..."` against the winplan spec graph (`~/projects/winplan/.borg/spec/`, per the user's reference shoe-project memory note). Observe:

- The cascade dispatches and converges within the iteration cap.
- The history log shows the `spec_biased` root + the expected child events linked via `caused_by`.
- `locutus status --full` surfaces the bias.
- `locutus history --since <bias-event-id>` walks the cascade tree chronologically (if `--since` was implemented; otherwise raw event-log inspection).
- Git diff on `.borg/spec/` shows the expected mutations and no spurious churn.

Document the empirical findings inline in this plan's Phase 7 section (a per-phase summary like dj-137-justify-activity.md's Status section) so future readers see what actually happened vs. what was planned.

**Discipline:** [[feedback-council-doc-maintenance]] **does not apply** to this DJ — DJ-138 doesn't touch the council. The explicit exemption is documented in DJ-138's Documentation consequence section. Confirm in the Phase 7 commit message that the council.md update was considered and explicitly skipped, so the reviewer doesn't trip the checklist.

**Tests added:** none (this phase is docs + empirical validation, no new code).

**Commit message shape:** `docs(dj-138): CLAUDE.md, activities, mcp, refine + empirical validation against winplan (DJ-138 phase 7)`.

**Exit criteria:** all docs updated; the empirical validation note in this plan describes what the cascade actually did on a real graph (with link to the session directory if applicable). DJ-138 status in [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md#dj-138) flipped from `design` to `shipping`.

## After all phases

- Run the full test suite + vet across all packages: `go test ./... && go vet ./...`. Both green.
- Run `go test ./... -race` once to confirm no concurrency regressions from the activity-registry refactor (Phase 1) or the new MCP tool (Phase 2).
- Update DJ-138's `Status:` line in `docs/DECISION_JOURNAL.md` from "design" to "shipping" with a per-phase summary mirroring the DJ-137 pattern.
- This plan's `Status:` line at the top flips to `DONE`.

## Out of scope (explicitly)

These are tee'd up as Future Work in [DJ-138](../../docs/DECISION_JOURNAL.md#dj-138); not part of this plan:

- **Witness-state precision optimization** — adding `ParentHashAtSynthesis` to `Approach` + `ComputeFeatureHash` / `ComputeStrategyHash` + an `equivalent` flag on the rewriter output. Lands in a follow-up DJ when post-approach false-drift becomes a felt pain point.
- **Multi-target `--with`** — applying the same bias to multiple targets in one cascade. Deferred until operator demand surfaces.
- **`locutus history --since <bias-event-id> --tree`** — tree-rendering for the cascade event graph. The data structure (the `caused_by` field) lands in Phase 3; the renderer is a small follow-up.
- **Per-runtime playbook overlay for `spec_bias`** — re-evaluate if a Claude Code-specific `/goal` integration or Codex/Gemini hook-driven flow produces meaningfully better UX.

## Risks / known unknowns

- **(a) The backward cascade's "implied upstream decision" identification is LLM-judgment-heavy.** Risk: the orchestrator might over-flip decisions (over-zealous interpretation of the bias) or under-flip (miss decisions the bias implies). Mitigation: the playbook explicitly tells the orchestrator to enumerate cited decisions first, then consider non-cited but graph-adjacent decisions, before considering brand-new decisions. The per-iteration touched-set bounds the worst case. Empirical validation in Phase 7 surfaces real behavior.

- **(b) The conservative closure mark overcounts on rationale-only refreshes.** Acknowledged limitation; Future Work item (witness-state precision) addresses it when it becomes a felt pain point. Phase 7's empirical validation should record whether this is a real DX issue on winplan or whether the over-counting is rare enough to ignore.

- **(c) The iteration cap of 20 may be too low for large spec graphs.** Mitigation: per-project override via `.borg/agents.yaml`. If the cap fires during Phase 7's empirical validation on winplan, bump the cap in that project's config rather than the default.

- **(d) The plain-mode tightening (rejecting Approach + Bug ids in plain `refine`) is a behavior change** that might surface as a regression in workflows that previously passed those ids by accident or convention. Likelihood: very low under DJ-135's framing (plain `refine` is whole-graph refinement; node-id targets are an opt-in scope). Mitigation: the error message is explicit and tells the operator the correct surface (adopt / assimilate for approaches; the bug-handling subgraph for bugs).

- **(e) `caused_by` field on existing event types** might break a downstream consumer that hard-codes the event schema. Mitigation: the field is optional; unmarshalers ignoring unknown fields (the Go default) handle this transparently. If a strict-mode unmarshaler exists somewhere in the tree, audit and update in Phase 3 rather than discovering at runtime.

## Reference

Synthesizes the design conversation on 2026-05-26: user surfaced demand for the pre-DJ-135 write-cascade surface but with reduced flag surface area + the novel backward direction; chat traversed 7 design sections (CLI surface, Decision target forward cascade, Feature/Strategy target backward cascade, cascade bounds, convergence model, error / failure modes, history + observability) with extended detours into drift handling (3 viable postures), the phase-boundary insight (Approach presence as architectural phase), and the git-as-rollback architectural stance. The full design lands in [DJ-138](../../docs/DECISION_JOURNAL.md#dj-138) (added 2026-05-26 at line 4818 of `docs/DECISION_JOURNAL.md`).
