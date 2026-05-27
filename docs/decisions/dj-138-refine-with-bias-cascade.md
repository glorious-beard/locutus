## DJ-138: Add `--with "<bias>"` Strong-Bias Cascade to `locutus refine` for Targeted Override of Deliberation-Layer Nodes — Subsumes the Retired Pre-[DJ-135](dj-135-multi-runtime-pivot.md) `--brief` / `--supersede` Flag Surface as a Single Flag with Two-Directional Cascade; Forward Cascade on Decision Targets, Backward+Forward Cascade on Feature/Strategy Targets; Phase-Aware Conservative Drift Marking via `InvalidatedByEventID` Reference-Graph Closure (Day-1) with Witness-State Precision Deferred to a Follow-Up DJ; Per-Activity Iteration Caps Introduced via `internal/activity/agents-default.yaml` Replacing the Hardcoded `const maxIterations = 20` in `internal/runner/run.go`; New `spec_mark_approach_drifted` MCP Tool for Explicit Drift Writes; New `spec_biased` Root History Event with `caused_by` Linking Cascade Children into a Walkable Tree; Best-Effort Mid-Cascade Failure with Git as the Architectural Rollback Layer — No Transactional Spec Mutations; Approach and Bug Nodes Rejected as `refine` Targets Entirely, Goal Additionally Rejected Under `--with`

**Status:** shipping (designed 2026-05-26; implemented 2026-05-27 across 6 phases in `main`). Phase 1 — per-activity iteration caps in `agents.yaml`; Phase 2 — `spec_mark_approach_drifted` MCP tool; Phase 3 — `spec_biased` + `approach_drifted` history events with `caused_by` linkage; Phase 4 — `refine --with` CLI flag with tightened Decision/Feature/Strategy-only validation plus the plain-mode Approach/Bug rejection; Phase 5 — `spec_bias` activity + cross-runtime playbook at `internal/scaffold/plans/spec_bias.md`; Phase 6 — `status --full` surfaces a "Recent biases" section sourced from `spec_biased` events. Phase 7 (this section) — docs updated, status flipped. Empirical end-to-end validation against winplan deferred to operator's first real `--with` invocation.

**Context.** [DJ-135](dj-135-multi-runtime-pivot.md) phase 5 retired the pre-existing `refine --brief / --supersede / --diff / --rollback` flag surface alongside the Go council that drove it (commits `915a784` rewiring the CLI verbs to ACP dispatch, `0731747` deleting the council). DJ-135's Status note flagged the retirement with a conditional revival promise: *"the `refine --brief / --supersede / --diff / --rollback` and `history --narrative / --regenerate-narrative` flags retired alongside; if you need them back, they land as ACP-dispatched activities in a follow-up."* [DJ-137](dj-137-justify-activity.md) is the first redemption of that promise — it restored `justify` as a read-only ACP-dispatched activity. DJ-138 is the second, and it restores the **write-cascade** surface — but with a narrower, more powerful shape than the four-flag pre-DJ-135 vocabulary.

The user surfaced demand on 2026-05-26 with a clear architectural observation: the pre-DJ-135 surface was *too wide* with overlapping flags. `--brief` was a soft bias on the rewriter; `--supersede` was a hard structural replacement that cascaded id rewrites; `--diff` and `--rollback` were post-hoc inspection and undo. A single flag — provisionally `--with "<bias>"` — can subsume the soft and hard cases by letting the playbook judge intensity from the bias text. Forceful bias text triggers structural changes; rationale-only bias text strengthens prose without flipping options. The post-hoc inspection and undo flags (`--diff`, `--rollback`) are subsumed by the architectural posture that **git is the rollback layer** — `.borg/spec/` is checked into project history, so `git reset --hard` provides the right granularity at familiar developer ergonomics.

The user's second insight is the **novel backward direction**: the pre-DJ-135 surface only cascaded forward (decision → features → strategies → approaches). DJ-138 adds the inverse — `--with` targeting a Feature or Strategy walks *backward* to identify upstream decisions that need to flip to satisfy the bias, applies those flips (with the same add-and-promote semantics as forward), then forward-cascades from each flipped decision back through the rest of the graph. The target node itself is rewritten *last*, with the now-stabilized upstream state as input. This inversion is the inverse of `--supersede`, which used to replace the target directly.

The user's third insight is **phase awareness**: the presence of any Approach node in the spec graph marks an architectural phase boundary. Pre-approach, the spec graph is exploratory and refinement is essentially zero-cost (no code or synthesis to drift). Post-approach, refinement has consequences — drifted approaches imply code that may need to change. The cascade must record drift faithfully in Phase 2 so downstream tooling (`adopt`, `assimilate`, `status`) can detect and respond. This phase distinction resolves an apparent tension in the design: "approaches marked drifted but not re-synthesized" holds in both phases, but in Phase 1 the set of approaches is empty.

**Decision.** Add `--with "<bias>"` to `locutus refine` as a strong-bias cascade flag with two-directional traversal, conservative reference-graph drift marking, and per-activity iteration caps. Minimum new surface; existing MCP tools and history machinery extended rather than replaced.

1. **CLI surface** — `locutus refine <id> --with "<bias>"`.
    - `<id>` mandatory; the existing optional positional in [cmd/refine.go](../../cmd/refine.go) becomes required-when-`--with`-present (validation in `Run()` before `runActivityVerb` is called).
    - Valid kinds for `<id>` under `--with`: **Decision, Feature, Strategy**. Goal / Approach / Bug rejected with explicit error messages.
    - Approach and Bug are **additionally rejected as refine targets entirely**, not just under `--with` — Approach because it's the synthesis layer (produced by adopt/assimilate, not deliberated); Bug because it's its own subgraph out of scope for refine. This tightens validation in plain `refine [<id>]` mode too, where the absence of `--with` previously accepted any node id.
    - `<bias>` non-empty natural-language string. Validation rejects empty / whitespace-only.
    - Without `--with`: existing behavior preserved — `<id>` optional, defaults to `"goals"`, broad whole-graph refinement via the existing `spec_refinement` activity.

2. **New activity** — `spec_bias`, registered in [internal/activity/agents-default.yaml](../../internal/activity/agents-default.yaml) alongside the existing five (`spec_refinement`, `feature_ingestion`, `code_adoption`, `code_assimilation`, `justification`). Same three-runtime preference list (`claude-code`, `codex`, `gemini`) by default. The CLI verb routes `--with`-present invocations to `spec_bias`; `--with`-absent invocations continue routing to `spec_refinement`.

3. **New playbook** at `internal/scaffold/plans/spec_bias.md` — cross-runtime default; no `.<runtime>.md` overlay per [DJ-136](dj-136-per-runtime-idiomatic.md)'s convention until a per-runtime UX win surfaces. The playbook directs the orchestrator to:
    1. Read the target node via `mcp__locutus__spec_get`.
    2. Walk the reference graph closure (forward from a Decision target; backward then forward from a Feature/Strategy target).
    3. Apply mutations via existing MCP tools (`spec_revise_decision`, `spec_revise_feature`, `spec_revise_strategy`, `spec_propose_decision`).
    4. Mark drifted approaches via the new `spec_mark_approach_drifted` tool (see point 7).
    5. Iterate until convergence or the activity's iteration cap fires.
    6. Emit a final report naming touched nodes and any open drift outside the closure.

4. **Forward cascade semantics (Decision target).** The playbook reads the bias and classifies intent:
    - **Promote** an option already in `rejected_alternatives` → flip `chosen_option` via `spec_revise_decision`; demote previous chosen.
    - **Add-and-promote** an option not previously listed → add to `rejected_alternatives` with the bias text as rationale, then promote. Single atomic step, no confirmation gate.
    - **Rationale-only refresh** — bias doesn't propose a different option; the playbook tightens or restructures decision prose without flipping. Accepted as a legitimate `--with` operation (strengthening a decision without semantic change is the degenerate-but-valid form of strong bias).
    - The decision's id stays byte-stable across flips per [DJ-133](dj-133-decisions-identified-by-axis.md)'s axis-shaped id design — backreferences from features/strategies/approaches don't need rewriting.
    - Forward cascade from the flipped decision walks **citing** features/strategies (via existing reference graph) **and proactively adds new citations** to features/strategies that should cite the decision but don't yet. Rewrites each via `spec_revise_feature` / `spec_revise_strategy`. Marks dependent approaches drifted.

5. **Backward cascade semantics (Feature/Strategy target).** The playbook reads the bias in the context of the target's current state and identifies implied upstream decision changes. Three possible outcomes per implicated decision:
    - **Flip an existing cited decision** — same add-and-promote semantics as forward (4).
    - **Add a citation** that doesn't currently exist — same proactive-citation posture as forward.
    - **Add a brand-new decision** if the bias implies an axis with no decision yet — uses `spec_propose_decision` with axis-shaped id per DJ-133, then adds the citation.
    
    The cascade then **forward-cascades from each flipped decision** per (4) — sibling features/strategies citing those decisions get rewritten, dependent approaches marked drifted. The target node itself is rewritten **last**, so its body reflects the settled upstream state rather than intermediate cascade state. This ordering is the inverse of pre-DJ-135 `--supersede`, which replaced the target first.

6. **Phase-aware conservative drift marking.** Approaches affected by the cascade are marked drifted via `Approach.InvalidatedByEventID` (the same field pre-DJ-135 `--supersede` used) by reference-graph closure:
    - Every approach whose `ParentID` is a rewritten Feature/Strategy → drifted.
    - Every approach whose `Decisions []string` contains a flipped decision id → drifted.
    - **Approaches are not re-synthesized by `--with`** — that's adopt's role. The drift signal lets adopt and status detect that synthesis is stale.
    - The mark is conservative (set on every approach in the closure, regardless of whether the upstream change actually altered the synthesis output). This overcounts on rationale-only refreshes (point 4) but is correct (no false negatives), cheap (pure graph walk, no LLM call), and debuggable (every drift is auditable via the new `approach_drifted` history event).
    - **Phase-1 (pre-approach) graphs** have an empty drift set — the closure walks features/strategies but finds no approaches. The cascade does its deliberation-layer work and exits cleanly.
    - **Phase-2 (post-approach) graphs** record drift faithfully so adopt/assimilate/status can detect and respond.

7. **New MCP tool** — `spec_mark_approach_drifted` taking `{approach_id, event_id}`. Sets `InvalidatedByEventID` on the named approach to the named event id. Narrow, single-purpose, explicit — preferred over implicit side-effects in `spec_revise_*` cascades because every drift mark gets its own traced tool call in the session's `tools.jsonl`, making the cascade auditable in post-mortem session walks. This is the only new MCP write tool; everything else reuses existing `spec_revise_*` / `spec_propose_*`.

8. **Cascade bounds.**
    - **Structural bound:** the reference graph closure, monotonically grown by proactive new citations the playbook adds. Forward from decisions; backward then forward from features/strategies; leaf walk to approaches.
    - **Operational bound:** per-run touched-set of node ids. A node enters the set the moment a mutation lands; subsequent passes skip it. Prevents cycles like `flip dec-A → rewrite feat-X → bias implies flip dec-B → rewrite feat-X again`.
    - **Iteration bound:** per-activity cap (see point 9).
    - **Worst-case upper bound:** `|deliberation-layer nodes|` (every Decision/Feature/Strategy touched at most once). Finite even on adversarial cascades.
    - **Convergence:** the cascade terminates when a full closure walk produces zero new mutations.

9. **Per-activity iteration caps.** Extend [internal/activity/agents-default.yaml](../../internal/activity/agents-default.yaml) and the `activity.Activity` struct in [internal/activity/registry.go](../../internal/activity/registry.go) with a `max_iterations: int` field. All six activities (`spec_refinement`, `feature_ingestion`, `code_adoption`, `code_assimilation`, `justification`, `spec_bias`) read their cap from the registry; the hardcoded `const maxIterations = 20` in [internal/runner/run.go](../../internal/runner/run.go) is replaced by an activity-registry lookup. Default value is **20** for `spec_bias`, matching the existing `spec_refinement` value. Per-project override via `.borg/agents.yaml`.

10. **Failure model — best-effort, not transactional.** Mid-cascade MCP write failures leave the graph in a partially-updated state. Each `spec_revise_*` write commits independently via the existing per-call write-through to `.borg/spec/`. Recovery paths:
    - **Re-invocation:** the playbook is written to detect nodes whose state already reflects the bias (via `spec_search` over the current state) and skip them on subsequent runs. Partial cascades complete cleanly on the second pass *when the playbook honors this contract* — this is a playbook-authoring discipline, not an automatic property of the system. The playbook tests assert the skip behavior.
    - **History inspection:** the `spec_biased` root event (point 11) records the bias before the cascade starts, so even a totally failed cascade is auditable.
    - **Git rollback:** the architectural rollback layer. `git reset --hard` over `.borg/spec/` returns the working tree to the last commit at familiar developer ergonomics. Locutus runs locally; the spec is files; files live in git; git owns versioning. No transactional spec-mutation system is built or planned.
    - **No "dirty working tree" check.** Paternalistic; against the local-tool philosophy. Operators are trusted to commit before risky operations; this is documented as DX hygiene, not enforced.

11. **History as a walkable tree.** New event type `spec_biased` as the root of every `--with` run. Carries: target id, bias text, dispatch timestamp, pre-cascade blast-radius estimate, ACP session id. Children link to the root via a new optional `caused_by string` field (the bias event's id) added to existing `spec_revised` / `spec_proposed` events. New event type `approach_drifted` for the drift marks, also linked via `caused_by`. The event tree enables `locutus history --since <bias-event-id>` to walk a single `--with` run's full footprint chronologically. Existing event log location (`.borg/spec/.history/`) unchanged; events committable like the rest of the spec graph.

12. **Status integration.** `locutus status` and `locutus status --full` already surface drifted approaches via `Approach.IsInvalidated()`. No new rendering code needed for the drift signal. `--full` additionally surfaces recent `spec_biased` events (last N, configurable) so the operator sees what biases have run lately — small additive change to the existing status renderer.

**Resolved design questions** (chat 2026-05-26):

1. **Bias shape is natural-language string** — `--with "use postgres, the team owns ops"`. Consistent with the pre-DJ-135 `--brief` / `--supersede` surface; maximum flexibility; all LLM judgment in the playbook. Rejected: structured directives (`--with promote:dec-foo=postgres`), hybrid structured+freeform, multiple `--with` flags per invocation.

2. **New-option case: add-and-promote in one shot** — when the bias implies an option not currently in the decision's `rejected_alternatives`, the playbook adds it (with the bias as rationale) and promotes it atomically. No confirmation gate, no draft-and-pause. Strong bias = strong obedience. The operator opted into `--with`; surprise is acceptable; git is the rollback. Rejected: confirmation gate, draft-and-pause, refuse-if-not-pre-listed, bias-text-judged intensity.

3. **Full cascade in both directions** — forward from flipped decisions through every citing feature/strategy + approach; backward from feature/strategy targets through implied upstream decisions then forward-cascading from each. Strong bias = strong blast radius. Rejected: targeted cascade (only direct edges), two-step explicit cascade with separate `locutus cascade` verb.

4. **`<id>` mandatory and non-Goal under `--with`** — explicit node-targeted mode; no discovery-from-bias-text fallback. Predictable, validated upfront. Rejected: playbook-discovers-target, hybrid id-or-discovery, new verb (`locutus override` / `locutus bias`).

5. **Valid kinds: Decision / Feature / Strategy** — the mutable deliberation layer. Goals are human-authored. Approaches are the synthesis layer (adopt's domain). Bugs are out of scope for refine entirely. The Approach and Bug exclusions tighten validation in plain `refine [<id>]` mode too.

6. **Forward cascade adds proactive citations** — a feature/strategy that should cite a flipped decision but doesn't yet gets a citation added as part of the cascade. The reference graph grows during a cascade, monotonically.

7. **Backward cascade may add new decisions** — when the bias implies an axis that has no decision yet, the playbook creates one via `spec_propose_decision` with axis-shaped id, then cites it from the target. Same proactive posture as forward citations.

8. **Rationale-only refresh is a valid `--with`** — a bias that strengthens a decision's rationale without flipping options is accepted; the playbook tightens prose and exits. This preserves user intent ("I have a strong opinion about this decision, even though I'm not changing the option") and creates an auditable history event of the bias even when the cascade is a no-op.

9. **Phase boundary is the presence of any Approach** — pre-approach the cascade does deliberation-layer work and finds no approaches to drift; post-approach the cascade records drift faithfully. The "approaches drifted but not re-synthesized" stance from chat holds in both phases by construction (Phase 1's drift set is empty, not violated).

10. **Drift posture: conservative closure mark (option A)** — Day-1 uses reference-graph closure via `InvalidatedByEventID`. Over-marks on rationale-only refreshes (false positives possible). **Rejected for Day-1:** witness-state precision (option B — adding `ParentHashAtSynthesis` to `Approach` + an `equivalent: true/false` flag on the rewriter agent's output), re-synthesis precision (option C — re-synthesizing affected approaches as part of `--with`'s cascade, which would pull adopt's synthesis work into `--with` and contradict the layered design). Witness-state precision is **deferred to a follow-up DJ** as a Phase-2 optimization for when post-approach false-drift becomes a felt pain point; tee'd up in this DJ's Future Work below.

11. **Iteration cap: per-activity in agents-default.yaml** — default 20 for `spec_bias`, matching `spec_refinement`. Per-project override via `.borg/agents.yaml`. The hardcoded const in `internal/runner/run.go` is replaced by a registry lookup for **all** activities (refactor in scope of this DJ to avoid leaving an inconsistent half-config state).

12. **Approach-drift write surface: explicit `spec_mark_approach_drifted` MCP tool (option A)** — every drift mark is a traced tool call in `tools.jsonl`. Rejected: implicit side-effect in `spec_revise_*` cascades — would lose the audit trail and make the cascade harder to debug.

13. **Best-effort cascade, git as the rollback layer** — no transactional spec mutations. Mid-cascade failures leave partial state; recovery is idempotent re-run or `git reset --hard`. The local-tool architecture makes git the natural rollback granularity; building transactional rollback in Locutus would reinvent what git already provides at the right granularity (per-intent, per-commit).

14. **No paternalistic "dirty working tree" check before dispatch** — `--with` runs whether or not the working tree is clean. Pre-`--with` commit hygiene is documented, not enforced.

15. **Cross-runtime playbook only (no `.<runtime>.md` overlay)** — `spec_bias` is one-shot-dispatched in the same shape on Claude Code, Codex, and Gemini. DJ-136's overlay convention exists; this activity doesn't benefit from per-runtime divergence in v1. Re-evaluate if a real UX win surfaces.

**Alternatives considered:**

- **Revive the pre-[DJ-135](dj-135-multi-runtime-pivot.md) four-flag surface verbatim** (`--brief`, `--supersede`, `--diff`, `--rollback`). Rejected — too wide with overlap, as the user explicitly flagged on 2026-05-26. `--brief` and `--supersede` collapse cleanly into `--with` with playbook-judged intensity; `--diff` and `--rollback` are subsumed by git (`.borg/spec/` lives in version control). Restoring the four-flag surface would also mean re-implementing the supersede id-cascade machinery from scratch under the post-DJ-133 axis-shaped-id model, which doesn't need it (id stability across flips means no id rewriting).

- **Structured directives** (`--with promote:dec-oltp-store=postgres` / `--with demote:dec-oltp-store`). Rejected — less expressive than natural language, doesn't match the existing CLI surface (refine/justify/import/adopt/assimilate all take natural-language context notes), and offloads judgment to the operator that the playbook is better positioned to handle (which alternative to promote, what rationale to record, whether a new decision is needed).

- **Hybrid structured+freeform** (`--with promote:dec-oltp-store=postgres "team owns ops"`). Rejected — two paths to test and document, and the structured prefix is rarely what the operator wants (they think in intent, not in graph operations).

- **Multiple `--with` directives per invocation** (`--with X --with Y`). Rejected — highest expressive power but highest failure surface; the cascade interactions between multiple biases on the same run are hard to reason about and don't match how operators think about strong-bias refinement (one bias per intent unit; if you have two intents, run `--with` twice).

- **Confirmation gate / draft-and-pause** before applying the cascade. Rejected — contradicts strong-bias-obedience. The operator opted in by typing `--with`; the cascade is fast; mistakes are recoverable via git. Adding a gate would dull the verb's intent.

- **Witness-state precision (option B) on Day-1.** Adds `ParentHashAtSynthesis` and `DecisionHashesAtSynthesis` to `Approach`; rewriter emits an `equivalent: true/false` flag the cascade reads to short-circuit no-op rewrites. **Deferred** (not rejected) — a real precision improvement, but a Phase-2 concern that lands cleanly once false-drift becomes a felt pain point. Calling out as Future Work below.

- **Re-synthesis precision (option C) on Day-1.** Re-synthesize affected approaches during `--with`'s cascade; compare new `ComputeSpecHash(approach)` to recorded value; only mark drifted on mismatch. Rejected — pulls adopt's synthesis work into `--with`, contradicting the layered design where `--with` is deliberation-layer-only and adopt is synthesis-layer. Also expensive (full synthesis pass per affected approach).

- **Transactional spec rollback** — snapshot before `--with` dispatches; revert on failure. Rejected — Locutus runs locally and the spec lives in git; `git reset --hard` provides per-intent rollback at the right granularity with familiar ergonomics. Building transactional rollback in Locutus would reinvent versioning poorly.

- **New verb** (`locutus override <id> "<bias>"` / `locutus bias <id> "<bias>"`). Rejected — refine is the natural home for a write-cascade verb. A separate verb would multiply the surface without semantic gain. The `<id> --with "<bias>"` shape reads naturally in operator vocabulary.

- **Playbook discovers target from bias text** (no `<id>` positional under `--with`). Rejected — unreliable resolution, hard to validate upfront, surprises the operator when the playbook resolves to a different node than expected. Explicit id is cheap to type and removes ambiguity.

- **Approach and Bug as valid `--with` targets.** Rejected — approaches are the synthesis layer (adopt/assimilate domain), not deliberated. Bugs are their own subgraph out of scope for refine. The exclusion tightens validation in plain `refine [<id>]` mode too, where these were previously accepted as freeform targets despite being semantically wrong.

- **Targeted cascade** (only directly-cited features/strategies; no proactive new citations). Rejected — under-cascades when the bias implies a relationship that should exist but doesn't. The strong-bias posture aligns with "fix the graph to match the bias," which means adding citations where they belong.

- **Two-step explicit cascade** (`--with` does the single override; user re-invokes `locutus cascade <id>` to propagate). Rejected — composable but adds friction the strong-bias posture rejects. If the operator wanted to do one override and stop, they'd use a different surface (direct MCP tool call, `locutus refine` with focus-only context note).

**Consequences:**

- **Code (add):**
    - `internal/scaffold/plans/spec_bias.md` — new playbook. ~100-140 lines. One-iteration shaped per the post-DJ-135-phase-5 convergence pattern. Includes the validation-of-target-kind logic for the orchestrator (defensive check; the CLI already validates, but the playbook double-checks because MCP-prompt callers bypass the CLI).
    - `internal/activity/agents-default.yaml` — new `spec_bias:` entry with the standard three-runtime preference list and `max_iterations: 20`.
    - `internal/mcp/tools_spec_write.go` — new tool `spec_mark_approach_drifted` taking `{approach_id, event_id}`. Sets `Approach.InvalidatedByEventID`. Description per `[docs/agent-conventions.md](../../docs/agent-conventions.md)` — registration-level prose, not playbook-level.
    - `internal/history/spec_biased.go` — new event type. Records target id, bias text, dispatch timestamp, blast-radius estimate, ACP session id.
    - `internal/history/approach_drifted.go` — new event type. Records approach id, the upstream event that drifted it, the originating bias event.
    - `cmd/refine_with_test.go` (or extend `cmd/refine_test.go` if it exists) — covers `--with` validation (id required, non-Goal, non-Approach, non-Bug, non-empty bias), the mutual-exclusion-with-default-target shape, and the kong-parsing surface.
    - `internal/mcp/tools_spec_write_drift_test.go` — covers the new MCP tool's surface contract.

- **Code (modify):**
    - `cmd/refine.go` — add `With string` field with `kong` `optional:""` and `help:"Strong-bias cascade..."` tags. Validation in `Run()`: when `With != ""`, require non-default `Target` (reject `goals`); resolve `Target` to a node and reject Goal/Approach/Bug kinds; reject empty/whitespace bias. Route to `spec_bias` activity instead of `spec_refinement` when `With != ""`.
    - `cmd/refine.go` (plain mode) — also tighten validation: reject Approach and Bug ids as `Target` regardless of `--with`. (Goal allowed in plain mode as today.)
    - `internal/activity/registry.go` — extend `Activity` struct with `MaxIterations int` field; default to 20 if unspecified (back-compat with hand-written `.borg/agents.yaml`). Plumb through `Resolve()` callers.
    - `internal/runner/run.go` — replace the hardcoded `const maxIterations = 20` with an activity-registry lookup. `runOuterLoopDispatch` accepts the cap as a parameter; `runActivityVerb` passes the resolved activity's cap.
    - `internal/history/event.go` (or wherever the shared event schema lives) — add optional `caused_by string` field; existing event types ignore it when unset.
    - `internal/spec/approach.go` — `IsInvalidated()` unchanged; comment updated to reference DJ-138 alongside the existing DJ-135-era reference, noting that `spec_mark_approach_drifted` is now the canonical write path.
    - `internal/cli/status*.go` (whichever file renders `--full`) — extend to surface recent `spec_biased` events.

- **User-visible:**
    - `locutus refine <id> --with "<bias>"` returns the write-cascade surface — strong-bias, atomic, full-cascade in both directions, conservative drift marking.
    - Plain `locutus refine` and `locutus refine [<id>]` continue to work as today — broad refinement with optional focus.
    - Approach and Bug ids rejected as refine targets in both modes — this is a *behavior tightening* on plain `refine` that may surface a small regression if any operator was passing those (unlikely under DJ-135's framing but worth noting in the changelog).
    - Per-cascade blast-radius estimate printed before the cascade enters its first iteration: e.g., `--with dispatched. Estimated blast: 1 decision, 4 features, 2 strategies, 11 approaches drifted.`
    - `locutus history --since <bias-event-id>` walks a single `--with` run's cascade tree chronologically (relies on the new `caused_by` field).
    - `locutus status --full` surfaces recent `spec_biased` events alongside the existing drift summary.

- **Documentation:**
    - [CLAUDE.md](../../CLAUDE.md) — update the `refine` entry under "Command Surface" to document the `--with` flag and the tightened validation. Update the retired-flags note from DJ-135's era to indicate `--with` subsumes `--brief` and `--supersede`. Also fix the stale "focus note appended to the playbook" framing for the `<target>` positional — it is and has always been a node id (defaulting to `goals`), not freeform text.
    - [docs/activities.md](../../docs/activities.md) — new section for the `spec_bias` activity paralleling the existing `spec_refinement` entry.
    - [docs/mcp.md](../../docs/mcp.md) — document `spec_mark_approach_drifted` alongside the other write tools.
    - **Note: docs/council.md unchanged.** Per [[feedback-council-doc-maintenance]], council-touching DJs MUST include council.md updates. DJ-138 does not touch the council (the council was retired in DJ-135 phase 5); it adds a new playbook-driven activity that doesn't invoke the surviving `spec-advocate` / `spec-challenger` / `justify-researcher` sub-council. No council.md update required.
    - Document the **commit-before-risky-operations DX hygiene** in `docs/refine.md` (new file, if one doesn't exist) or as a note in the existing CLI documentation. Mention git-as-rollback explicitly so the operator's mental model matches the architectural stance.

**Future Work (tee'd up for follow-up DJs):**

- **Witness-state precision optimization (Phase-2 drift handling).** Add `ParentHashAtSynthesis` and `DecisionHashesAtSynthesis` to `Approach`; add `ComputeFeatureHash` / `ComputeStrategyHash` parallel to the existing `ComputeSpecHash` (which is approach-only today). The rewriter agent emits `equivalent: true/false` on its output; the cascade short-circuits drift-marking when equivalent. Reduces false-positive drift in Phase-2 graphs where re-synthesis cost is real. Lands as its own DJ when post-approach false-drift becomes a felt pain point — premature now.

- **Multi-target `--with`.** Allow `locutus refine <id-a> <id-b> --with "..."` to apply the same bias to multiple targets in one cascade. Considered and rejected for Day-1 (high failure surface, unclear cascade interactions). May surface as demand if operators routinely run two consecutive `--with` invocations with the same bias text.

- **`locutus history --since <bias-event-id> --tree`.** A tree-renderer for the cascade event graph. The data structure (caused_by links) lands in DJ-138; the tree rendering is a small follow-up if operators ask for it.

- **Per-runtime playbook overlay for `spec_bias`.** Re-evaluate if a real UX win surfaces — e.g., Claude Code-specific `/goal` integration that lets the operator interactively guide the cascade.
