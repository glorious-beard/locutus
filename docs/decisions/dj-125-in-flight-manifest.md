## DJ-125: In-Flight Manifest + Enriched Concern Model (Refines DJ-094 / DJ-123 In-Flight Surface; Closes DJ-124's Concern-Disposition Gap)

**Status:** superseded by [DJ-135](dj-135-multi-runtime-pivot.md) on 2026-05-25 — the InFlightSpecStore overlay retired in [DJ-134](dj-134-unified-spec-store.md); the concern model lives in agent-prompt prose now rather than as Go-side bookkeeping. Cap-as-commit specifically retires per DJ-135 resolved-question 13 (subscription economics remove the cost pressure that motivated capping). Prior status: proposed.

**Context.** DJ-124's scout-driven convergence loop exposed two coupled limitations in the council's state model that the incremental architecture from DJ-098 → DJ-122 → DJ-124 assumed away:

1. **`state.Concerns` is append-only.** `mergeCriticIssues` appends to the slice; nothing ever clears it. Under DJ-122 the gate was the convergence judge and didn't gate on `len(Concerns)==0` — it judged the assembled proposal directly. DJ-124 made the scout the judge and the rule became `axes_open == [] AND len(Concerns) == 0`. With concerns accumulating monotonically, the "no concerns" gate never holds, and the loop never converges even when the council is making real progress.

2. **Projection-by-blob.** `projectChallenge`, `projectScout`, `projectReconcile`, and the per-fanout projections dump the entire `state.RawProposal` or `state.ProposedSpec` into every agent's prompt. The first winplan re-run with DJ-124 failed because `compactContext`'s 8K cap truncated the critic's view to ~17% of the assembled decisions, producing spurious "missing X" findings against decisions that existed past the cliff. The cap was bumped to 200K chars as an immediate unblock (commit `be883a0`); that's a tactical fix on an architectural problem. As spec graphs grow past 200K chars on real-world projects (the goal of dogfooding Locutus), the same regression returns at the new cap.

The deeper structural inconsistency: the persisted spec graph already uses a manifest-detail RAG pattern via `spec_list_manifest` / `spec_get` / `spec_search` (DJ-094, DJ-116, DJ-117), and the in-flight `spec_search` was redirected to council-time state by DJ-123. But `spec_list_manifest` and `spec_get` still target only the on-disk spec, and the projections still blob-dump in-flight state. Agents have RAG tools available to them but the projections pre-load everything anyway. The primary axis stays blob-first with search as a supplementary tool; the architecturally honest pattern is manifest-first with detail-fetch on demand, the way the on-disk graph already works.

The second winplan re-run with the truncation fix in place (trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/)) made the failure modes legible:

- Iter-3 critics produced 6 substantive findings — cross-decision contradictions, factual errors, hallucinated citations, financial incoherence, integration gaps. These are real issues a critic loop should surface.
- Iter-3's `convergence_failed` event listed 30+ "unresolved concerns" — most of which were iter-0 / iter-1 spurious "missing X" findings that no longer reflected the current proposal. The scout correctly stopped re-surfacing those axes in `axes_open` (it saw the decisions in `state.RawProposal`), but the old concerns persisted in `state.Concerns` and blocked `Converged: true`.

**Why this surfaced now.** DJ-124 is the first architecture to make the scout responsible for convergence judgment. The append-only Concerns model was inherited from DJ-122 where it served as a per-iteration record consumed by the revise step — that step re-processed concerns each iteration and didn't gate on their absence. DJ-124 changed the consumer's contract without changing the producer's: `mergeCriticIssues` still appends, but now the scout reads cumulatively-accumulated state. The bug is a coupling break the workflow rewrite didn't notice.

The projection-blob issue surfaced concurrently. DJ-124's scout-driven flow accumulates decisions monotonically across iterations (where DJ-122's flow re-assembled the proposal per iteration), so the assembled proposal grows much faster, exceeding any fixed truncation cap within 2-3 iterations on a real project.

**Decision.** Two coupled changes shipped together as DJ-125:

1. **Promote concerns to a first-class state model.** `Concern` gains:
    - `IterationRaised int` — when the critic first surfaced this finding. Lets the scout reason about staleness.
    - `Status` enum (`open`, `addressed`, `stale`, `wontfix`) — the scout's grade of whether the concern still blocks convergence given the current proposal.
    - `RelatedDecisionIDs []string` — decision IDs the concern references. Parsed mechanically from the text via `idRefRegex`; optionally surfaced by the critic in a structured `referenced_decisions` field. Powers DJ-126's decision-revision dispatch.
    - `RelatedAxisIDs []string` — axis IDs the concern references. Powers the scout's "is this axis still open?" judgment.

    State transitions: created `open` by `mergeCriticIssues`; transitioned to `stale` mechanically when a related axis is settled or a related decision exists; transitioned to `addressed` by the scout when judgment is required (contradictions, factual claims); never deleted (durable for forensics).

2. **In-flight manifest as the primary projection surface.** Add `InFlightManifest` — a structured view over `state.RawProposal` carrying:
    - `axes` — every axis the loop has seen, with state (`settled-by-dec-X` / `open` / `under-evaluation`) and surfacing-node references.
    - `decisions` — id, title, summary, state (`settled-prior` / `settled-this-iter` / `flagged`), axes covered, surfaced-by references.
    - `features` / `strategies` — id, title, summary, decision-reference set.
    - `concerns` — id, iteration_raised, status, related decisions and axes (cross-links into the rest of the manifest).

    `spec_list_manifest` and `spec_get` are redirected to the in-flight manifest during council runs (mirror DJ-123's `spec_search` pattern via the existing `SwappableSpecSearch` adapter). Projections replace blob dumps with the manifest plus the agent's specific working item in full (the axis being decided, the feature being elaborated, the critic's lens). Each agent sees the structural shape of the graph plus its own focus; details are fetched on demand via the tools.

3. **Mechanical concern-disposition pre-pass.** Before the scout sees concerns, a Go pre-pass walks each concern's `RelatedAxisIDs` / `RelatedDecisionIDs` against the manifest. Concerns whose named axis is `settled` or whose named decision exists in the graph are marked `Status: stale`. The mechanical pass handles ~80% of "missing X" false positives automatically; the scout grades the remaining 20% (judgment calls about contradictions, factual claims, integration gaps).

4. **Scout grades remaining open concerns.** `ScoutBrief` extends with `concern_dispositions: [{concern_id, disposition, justification}]`. `mergeScoutBrief` applies the dispositions onto `state.Concerns`. The scout's convergence rule becomes:

    `Converged: true ⟺ axes_open == [] AND no concerns with Status == open`

    `addressed`, `stale`, and `wontfix` concerns don't gate convergence but stay in the manifest for `locutus history` / `locutus explain` and the eventual operator surfaces. The disposition's `justification` field gives the scout authority to explain its grading — load-bearing for `wontfix` ("real concern but acceptable tradeoff given goals X") so the user can audit the scout's judgment.

**Alternatives considered.**

- **Just bump `defaultMaxChars` higher.** The 8K → 200K bump worked tactically for the second winplan run. Specs grow to millions of characters on real projects (the goal of Locutus); bumping the cap perpetually is whack-a-mole; manifests are the architecturally honest answer. The bump becomes obsolete once DJ-125 ships.

- **Clear `state.Concerns` at iteration boundaries.** The "Fix 1" candidate from chat (2026-05-18). Loses history (no way to ask "when did this issue first surface?"), forces critics to re-discover every issue every iteration, and leaves no path to "concern was `wontfix`-marked because it's a real-but-acceptable tradeoff." Rejected as a tactical patch on a structural problem; disposition is the right primitive.

- **Per-iteration concern stamps without disposition.** Stamp each concern with `IterationRaised`; scout's convergence rule reads only current-iteration concerns. Simpler than full disposition tracking but loses the explicit `wontfix` / `addressed` distinction the scout needs to communicate. The single-bit "current-iter or not" doesn't carry enough state for the architecture DJ-126 builds on.

- **ACP-style coding-agent concern review.** Spawn an ACP reviewer agent that grades each concern. Rejected per the "don't unify ACP with structured-output council" decision (chat 2026-05-18) — grading is structured output; the scout already produces structured output; making the scout the grader keeps the role unified and the schema enforcement intact.

- **Manifest-only without enriched concerns.** Promote the proposal to a manifest but leave concerns as-is. Doesn't solve the stale-concerns blocker; convergence still fails. The two changes are coupled — the manifest provides the substrate the concern grading queries against, and the concern enrichment is what makes the convergence rule reliable.

- **Use the LLM clusterer (DJ-098) for staleness grading.** The existing `spec_finding_clusterer` agent could be repurposed to grade concerns. Rejected: clusterer is for grouping unrelated findings into topical clusters before revise dispatch; staleness grading is a different shape of judgment (per-concern, against current state, with structured disposition output). Two different agents serving two different needs.

**Consequences.**

- **Code:**
    - `internal/agent/state.go` — `Concern` gains `IterationRaised`, `Status`, `RelatedDecisionIDs`, `RelatedAxisIDs`; `ConcernStatus` enum.
    - `internal/agent/manifest.go` (new) — `InFlightManifest` data type + `BuildManifest(state)` builder. Includes axes, decisions, features, strategies, concerns with cross-references and per-item state markers.
    - `internal/search/inflight.go` — extend the in-flight Bluge index path with manifest-shaped accessors used by `spec_list_manifest` and `spec_get`.
    - `internal/agent/spec_tools.go` — tool backing stores swap from "on-disk only" to "in-flight during council; on-disk otherwise" (same pattern DJ-123 used for `spec_search`).
    - `internal/agent/projection.go` and `internal/agent/workflow_spec_generation_dj124.go` — `projectChallenge`, `projectScout`, `projectReconcile`, `projectOpenAxis`, `projectAffectedNode` all replace blob dumps with `RenderManifest(state)` + the per-agent working item.
    - `internal/agent/workflow_spec_generation_dj124.go` — `mergeCriticIssues` extracts `RelatedDecisionIDs` / `RelatedAxisIDs` mechanically from finding text (regex match against the current manifest); a new `mechanicalDisposeConcerns(state)` pass runs after the critic merge; `mergeScoutBrief` applies the scout's `concern_dispositions` array.
    - `internal/agent/specgen.go` — `ScoutBrief` schema gains `concern_dispositions []ConcernDisposition` with `jsonschema` tags per CLAUDE.md (enum for `disposition`, description on `justification`).
    - `internal/scaffold/agents/spec_scout.md` — section added describing concern grading. Walk `docs/agent-conventions.md` end-to-end before drafting per `feedback_agent_conventions_checklist_first`.
    - `internal/agent/compact.go` — `defaultMaxChars` revisited; manifest-rendered projections are bounded by structural shape rather than character count, so the cap can drop back closer to a sane working size or stay at 200K as defense-in-depth.
    - Tests across `internal/agent/`, `internal/search/`, `internal/scaffold/`, `cmd/` covering manifest building, concern enrichment, mechanical disposition, scout grading, manifest-based projections. Existing workflow tests update to assert manifest-rendered prompts and concern-status reasoning.

- **Documentation:**
    - CLAUDE.md gets a short note on the manifest-detail pattern for in-flight state, paralleling the existing persisted-graph paragraph.

- **User-visible:**
    - Session traces carry richer concern history — operators see when a concern was first raised, its current status, what (if anything) addressed it.
    - The `convergence_failed` terminal's rationale becomes legible: instead of dumping 30+ concerns from across all iterations, it shows only `Status: open` concerns at exhaustion with iteration-raised metadata; `stale` / `addressed` / `wontfix` concerns appear in an appendix for forensic context.
    - `locutus history` and the future `locutus explain` benefit transparently — they walk concern→decision references to show "this decision was the response to that critic finding."
    - Per-call prompts shrink substantially (manifest is ~10% the size of the blob proposal at iter-3 scale), so wall-clock per iteration drops.

- **Performance:**
    - Manifest build per merge: O(N) over decision/feature/strategy count. Sub-millisecond for ~50-node graphs; scales linearly to thousands.
    - Mechanical disposition pre-pass: O(M × K) where M is concern count and K is per-concern regex matches against axis/decision IDs. Negligible.
    - Prompt token counts drop substantially for critic / reconciler / per-fanout projections — the manifest is a fraction of the full proposal JSON.

- **Migration:** per the no-back-compat-until-self-hosting posture, no shim. `Concern` gains fields; old persisted state loads with zero-value defaults (`IterationRaised: 0`, `Status: open`, empty related-id slices) and the mechanical disposition pass handles them on first re-iteration. The manifest infrastructure is council-internal; the persisted spec graph's schema doesn't change.

**Reversal criteria.** Revert if:

- (a) the mechanical disposition pre-pass marks concerns `stale` too aggressively, causing the loop to converge with unresolved real issues. Surfaces as users complaining `refine goals` declared convergence on a proposal with obvious gaps. Mitigation: tighten the regex matching (require exact id match, not substring); fall back to "always require scout grading" if matching is too loose.
- (b) the scout's concern grading is unreliable — marks `open` concerns `addressed` without real justification, or `addressed` concerns `open` (loop never converges). Surfaces as either premature convergence or budget exhaustion despite manifest correctness. Mitigation: tighten the scout's prompt around grading discipline; add a critic-style "did the scout grade correctly?" agent in a follow-up.
- (c) the manifest-rendered projection loses information the agents need. Surfaces as critic findings about subtle issues that the manifest's per-item summary doesn't capture but the full proposal would. Mitigation: extend the manifest's per-item summary fields; in the worst case, agents can `spec_get(id)` to fetch full detail — that's the RAG escape valve.

**Reference.** Extends [DJ-094](#dj-094-spec-lookup-tools-spec_list_manifest--spec_get-are-agent-facing-mcp-tools) by redirecting `spec_list_manifest` / `spec_get` to in-flight state during council runs. Extends [DJ-123](dj-123-in-flight-spec-search.md) by generalizing the in-flight-redirection pattern from search to the full RAG tool surface. Closes a structural gap in [DJ-124](dj-124-decisions-before-narrative.md) (the scout's `len(Concerns)==0` convergence rule). Motivated by the second winplan re-run trace at [`/Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/`](file:///Users/chetan/projects/winplan/.locutus/sessions/20260518/1258/30-ae0157/) which exposed the stale-concerns failure mode and the projection-blob scaling limit. Powers [DJ-126](dj-126-decision-revision.md) by providing the `Concern.RelatedDecisionIDs` substrate the decision-revision dispatch reads.
