# DJ-132 — Candidate-Survey Agent for Decision Elaboration

> **Governing DJ:** [DJ-132](../../docs/DECISION_JOURNAL.md#dj-132). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** SUPERSEDED by DJ-135 on 2026-05-26. The cost-optimization *framing* ("candidate-survey is cheaper than richer-elaborator") retires per DJ-135 resolved-question 13 (subscription economics). The `spec-candidate-survey` agent itself remains in `internal/scaffold/agents/` and runs as part of the playbook-executed council shape — the enumeration-vs-judgment separation is preserved as a cognitive-task-separation pattern. Empirical winplan refine 2026-05-26 shows the agent producing 6-9 alternatives per decision under the playbook-driven path. Historical: designed; implementation never started under the Go-orchestrated framing, but the agent prompt shipped and is in active use under DJ-135.
> **Predecessors:** [DJ-124](../../docs/DECISION_JOURNAL.md#dj-124) (decision-elaborator workflow this extends); [DJ-128](../../docs/DECISION_JOURNAL.md#dj-128) (deliberation log discipline — survey makes the log substantial from initial commit); [DJ-130](../../docs/DECISION_JOURNAL.md#dj-130) (cognitive-task-separation pattern — survey separates enumeration from judgment); commit [`ef2d209`](https://github.com/chetan/locutus/commit/ef2d209) (alternatives-merge-layer preservation — without it, the survey's upfront enumeration would erode across revises).
> **Surface area:** new agent prompt + new schema type + new workflow pre-step + decision-elaborator projection update + decision-elaborator prompt revision.
> **Discipline (per memory):** tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching. Walk `docs/agent-conventions.md` as a checklist before authoring the survey prompt (it's a new agent under `internal/scaffold/agents/`).

## Why this plan exists

Commit `ef2d209` landed mechanical alternatives preservation in the merge layer, eliminating the DJ-128 monotonicity oscillation that had been dominating the council's revise loop. The sixth winplan re-run validated the fix: `dec-supabase-postgres-persistence` accumulated 17 alternatives across 5 revises without dropping any. Productive compounding instead of churning.

But the trace surfaced a second inefficiency the drop-pattern had been masking: **the initial decision-elaborator dispatch systematically compresses the option space**. Concrete evidence:

- compute-platform initial dispatch: reasoning considered 6 candidates (Vercel, Render, Railway, EKS, Fargate, GCP); response emitted 2 alternatives. Four pre-considered candidates went unexpressed.
- identity-and-access-control initial: reasoning considered 3 (Auth0, Clerk, Supabase Auth); response emitted 2. Clerk (the eventual chosen) was thought about but unsurfaced upfront.
- data-persistence-strategy initial: returned empty on balanced-tier Gemini; the axis got no clean initial commit at all.

The 17 alternatives that eventually accumulate on the most-debated decision are compensatory work done by 5 critic-revise rounds, each round surfacing 2-3 candidates the elaborator missed. **The deliberation log is durable now, but its construction is wasteful** — each revise is a strong-tier grounded LLM call (~$0.30-0.60), wall-clock 1-3 minutes, charged against the convergence loop's iteration budget.

Root cause: the elaborator's task conflates *enumeration* and *judgment* in one call. Commit-mode crowds out exhaustive option-space exploration; the model defaults to the decision-doc genre's 2-3 alternative pattern regardless of how many candidates its reasoning surfaced.

DJ-132 separates the two tasks. A new fast-tier `spec_candidate_survey` agent runs per-axis BEFORE the elaborator, with grounding for current-vendor enumeration. The elaborator receives the candidate list as input and runs commit-mode against a pre-populated alternatives slice. The pattern parallels DJ-130's reasoning-vs-formatting separation at a different layer.

## Reference state (before DJ-132 starts)

- **Decision-elaboration fanout** at [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go) dispatches one decision-elaborator call per `axes_open` entry. Initial dispatch and revise dispatch go through the same agent (`spec_decision_elaborator`) with different projections.
- **Decision-elaborator agent** at [internal/scaffold/agents/spec_decision_elaborator.md](../../internal/scaffold/agents/spec_decision_elaborator.md) — strong tier, grounded, writes `RawDecisionProposal` with full alternatives slice.
- **Alternatives-merge-layer fix** at commit `ef2d209` (2026-05-21): merge function preserves prior alternatives mechanically when the elaborator emits a delta. The DJ-128 monotonicity discipline is now code-enforced rather than prompt-driven.
- **Existing schema registrations** at [internal/agent/schemas.go](../../internal/agent/schemas.go) cover `ScoutBrief`, `RawSpecProposal`, `RawDecisionProposal`, etc. DJ-132 adds `CandidateList`.
- **Six winplan re-runs of empirical evidence** under `~/projects/winplan/.locutus/sessions/`. The sixth run (`20260521/1958/24-20bf24`) is the cleanest signal of the upstream compression pattern DJ-132 addresses.

## Resolved design questions

Recorded in chat 2026-05-22; settled before this plan went to implementation.

1. **Survey runs on initial dispatch only, not on revises.** Revises engage with critic findings + existing alternatives; survey doesn't fit the revise path (the alternatives slice is already populated; survey enumeration would duplicate prior work).

2. **Fast tier + grounded.** Enumeration is discovery work LLMs do well at fast tier. Grounding is load-bearing — training-data-only enumeration produces hallucinated vendors (PostgresPro, AcmeDB) and stale candidates (Heroku free, Parse pre-acquisition). Web search forces every entry to resolve to a real, current URL.

3. **Output is flat — no judgments, no rationale, no citations on entries.** Just name + one-sentence first-glance fit. Judgment is the elaborator's job; mixing into the survey re-creates the task conflation the survey exists to break.

4. **Elaborator keeps its grounding.** Survey does broad enumeration searches (`"X alternatives 2026"`); elaborator does per-candidate deep-dive verification (`"X capabilities documentation"`, `"X current pricing"`). Different uses of web search; both legitimate.

5. **Survey input includes axis + GOALS.md + existing spec snapshot.** GOALS filters non-viable candidates upfront (cost ceilings, scale requirements, compliance regimes). Existing spec lets survey weight already-committed-elsewhere options higher. Scout brief's `technology_options` for this axis seed the survey when present.

6. **CandidateList schema carries `minItems=3`, prompt targets 6-10.** Conservative floor because some axes are genuinely narrow. Padding-prevention is in the prompt ("only enumerate candidates you actually find via search; don't invent to hit a count"), not the schema.

7. **Survey runs in parallel with sibling decision-elaborators.** Today's fanout dispatches one elaborator per axis in parallel; survey adds one fast-tier call per axis before each elaborator in the same fanout step. No serial bottleneck.

## Phase 1 — Schema + agent prompt

**Goal:** define the `CandidateList` Go type and registered example; author the `spec_candidate_survey.md` agent prompt following the convention discipline. No workflow integration yet; just the standalone agent definition.

**Files expected to change:**

- [internal/agent/specgen.go](../../internal/agent/specgen.go) — new `CandidateList` and `SurveyedCandidate` types. Every field tagged per the jsonschema-tag invariant (`minItems=3` on `Candidates`; descriptive `description=...` on `Name` and `FirstGlanceFit`).
- [internal/agent/schemas.go](../../internal/agent/schemas.go) — `RegisterSchema("CandidateList", CandidateList{...})` with a descriptive prose example payload (no placeholders like "dummy" / "TBD").
- [internal/scaffold/agents/spec_candidate_survey.md](../../internal/scaffold/agents/spec_candidate_survey.md) (new) — agent prompt. Follows `docs/agent-conventions.md` checklist: no JSON-mode priming language; field-name section headings allowed; enumeration framing explicit ("enumerate, don't judge"); fast-tier model preferences; `grounding: true`; `output_schema: CandidateList`. Walks the four-job structure the convention recommends.

**Tests:**

- `TestCandidateListSchemaRegistered` — `SchemaFor("CandidateList")` returns a schema with the expected fields and `minItems=3`.
- `TestSpecCandidateSurveyAgentLoads` — the agent .md parses cleanly into `AgentDef` with the expected frontmatter (fast tier, grounded, output_schema).

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 2-3 hours.

## Phase 2 — Workflow pre-step integration

**Goal:** the decision-elaboration fanout step gains a per-axis pre-call that runs the survey. Survey output threads into the elaborator's projection on the initial dispatch path only.

**Files expected to change:**

- [internal/agent/workflow_spec_generation_dj124.go](../../internal/agent/workflow_spec_generation_dj124.go) — decision-elaboration fanout step extends with a survey pre-step. Per-axis dispatch order: survey → elaborator (sequential within one axis; parallel across axes within the fanout step).
- [internal/agent/projection.go](../../internal/agent/projection.go) — `projectDecisionElaboration` gains a `Candidate List` section when survey output is present (initial dispatch); skip when absent (revise dispatch).
- [internal/agent/state.go](../../internal/agent/state.go) — `PlanningState.AxisSurveys map[string]CandidateList` carries per-axis survey results between dispatch boundaries; populated by the survey merge handler, consumed by the elaborator projection.

**Tests:**

- `TestSurveyDispatchedBeforeElaboratorOnInitialPath` — workflow test wiring the survey + elaborator; assert survey calls come before elaborator calls per axis.
- `TestSurveyOutputThreadsIntoElaboratorInput` — survey emits a `CandidateList`; elaborator's projected input contains a `Candidate List` section with the surveyed entries.
- `TestRevisePathSkipsSurvey` — revise dispatch (with critic findings on a prior decision) doesn't run survey; elaborator projection has no `Candidate List` block.
- `TestSurveyEmptyFallsThroughGracefully` — survey returns empty (or errored); elaborator runs against an empty candidate list and falls back to its own enumeration (current behavior).

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 6-8 hours.

## Phase 3 — Decision-elaborator prompt revision

**Goal:** elaborator prompt updates to assume the candidate list when present. Initial-dispatch section pre-narrows the task ("pick from these N candidates"); revise section unchanged.

**Files expected to change:**

- [internal/scaffold/agents/spec_decision_elaborator.md](../../internal/scaffold/agents/spec_decision_elaborator.md):
    - New section in Task block: "Initial dispatch with candidate list" — explains the survey input, the elaborator's role ("pick from these + author rationale + write rejected_because for each unpicked + cite each"), the option to surface additional candidates the survey missed (anti-anchoring).
    - Existing "Revise mode" section unchanged — revises don't see the candidate list.
    - Walk `docs/agent-conventions.md` checklist (the file change is in `internal/scaffold/agents/` and the user memory mandates this walk before any edit in that directory).

**Tests:**

- Existing `spec_decision_elaborator` integration tests pass without change (mock executor; survey path skipped when no candidate list is wired).
- New: `TestElaboratorEngagesAllSurveyedCandidates` — drive elaborator with a mock survey of 5 candidates; assert the response's alternatives slice has at least 4 entries (the chosen + at least 3 of the unpicked surveyed candidates as alternatives).
- New: `TestElaboratorCanSurfaceAdditionalCandidatesBeyondSurvey` — elaborator receives a 3-candidate survey but commits to a candidate not in the survey; assert the response is valid (anti-anchoring works).

**Verification:** `go build ./... && go vet ./... && go test ./internal/agent/... -count=1 -race`.

**Estimated:** 3-4 hours.

## Phase 4 — Documentation

**Goal:** CLAUDE.md gains a paragraph on the enumeration-vs-judgment separation as a council architecture principle. `docs/agent-conventions.md` gains a section on the "enumeration agent" pattern. `docs/council.md` gains the new agent's per-step position in the Mermaid diagram + a full per-agent reference entry.

**Files expected to change:**

- [CLAUDE.md](../../CLAUDE.md) — new paragraph in the LLM section: "Cognitive task separation. DJ-130 separated reasoning from formatting at the adapter layer. DJ-132 separates enumeration from judgment at the workflow layer. Both are instances of: when one LLM call is asked to do two cognitive tasks that conflict in the attention budget, separate them into sequential calls that each focus on one task."
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — new section "Enumeration agents": when an agent's job is exhaustive option-surfacing, the prompt explicitly frames the task as enumeration (not judgment); output schema is flat (no rationale/citations/judgments on entries); grounding is load-bearing for currency + hallucination prevention.
- [docs/council.md](../../docs/council.md) — two changes:
    1. **Workflow diagram (Mermaid).** Add a per-axis `spec_candidate_survey` step before the `decisions` fanout. Shape it as a fanout node (same hex color class as the other fanout steps). Edges: scout's `axes_open` → survey fanout (parallel across axes), then per-axis `survey-i → decisions-i` sequential into the elaborator. Keep the diagram conservative for VS Code preview compatibility (no `direction TB` inside subgraphs; quoted single-line labels; square-bracket shapes; classDef styling).
    2. **Per-agent reference.** Promote the existing "Pending agents" entry for `spec_candidate_survey` into a full per-agent reference block alongside `spec_decision_elaborator`. Include: output schema (`CandidateList`), model tier (fast), thinking (off), grounding (true — load-bearing for currency + hallucination prevention), governing DJ (DJ-132), and design notes (enumerate-don't-judge framing; flat output; runs initial dispatch only, not on revises).
    3. The "Pending agents" subsection either retires entirely (if no other pending agents remain at that point) or shrinks to whatever new pending entries DJ-133+ introduce.

**Verification:** `go test ./... -count=1 -race` clean; `go vet ./...` clean. Manually verify `docs/council.md`'s Mermaid diagram renders in both Mermaid Playground and VS Code preview (the council.md file currently uses a conservative Mermaid subset for compatibility; the survey-step addition must preserve that).

**Estimated:** 2-3 hours.

## Phase 5 — Validation against winplan re-run

**Goal:** run a 7th winplan re-run with DJ-132 landed; verify the survey reduces revise count and convergence accelerates.

**Process:**

1. Build the DJ-132 binary: `go build -o ~/go/bin/locutus-dj132 .`.
2. Run `locutus-dj132 update --offline --reset` against winplan.
3. Run `locutus-dj132 refine goals` against winplan with `LOCUTUS_SPEC_GEN_MAX_ITERATIONS=10` (matching the 6th re-run's budget so comparisons are apples-to-apples).
4. Measure:
    - Survey output quality: per-axis candidate list length (target: 6-10 on well-trodden axes, 3-5 on specialized); entries are real (URLs resolve, no hallucinated vendors); entries are current (no discontinued/pivoted options).
    - Initial elaborator output: alternatives slice length (target: ≥4 vs the previous ~2).
    - Revise count per axis (target: ≤2 vs the previous ~3-5).
    - Convergence: did the council converge within budget (target: yes, vs the 6th re-run's exhaustion at iter 5).
    - Per-axis cost: survey + elaborator + revises vs prior elaborator + revises (target: net lower).

**What success looks like:** average per-axis revise count drops from 3-5 to ≤2; convergence achieved within budget on the same winplan project; net cost per axis lower.

**What partial success looks like:** revise count drops but convergence still hits budget exhaustion; mitigated by raising `LOCUTUS_SPEC_GEN_MAX_ITERATIONS` and accepting that GOALS.md is genuinely thin enough to need more iteration regardless of survey quality.

**What failure looks like:** survey produces hallucinated/stale candidates that the elaborator wastes cycles weighing → reversal criterion (b) triggers. OR elaborator anchors so hard on survey it stops considering candidates the survey missed → reversal criterion (c) triggers. OR revise count doesn't meaningfully drop → reversal criterion (a) triggers.

**Verification:** the winplan session traces are durable evidence. No automated assertion here.

**Estimated:** 1-2 hours of compute + manual review.

## Phase 6 — DJ-132 status flip + plan marked DONE

**Goal:** DJ-132 flips from `proposed` to `shipping` once Phase 5 validation passes.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-132 status `proposed` → `shipping (Phases 1-5 landed YYYY-MM-DD)`.
- This plan file marked DONE.

**Verification:** `go test ./... -count=1 -race` clean; `go vet ./...` clean.

**Estimated:** 30 minutes.

---

## Total estimate: 14-20 hours single-stranded across 3-4 sessions

(plus Phase 5 empirical compute time)

## Pointers a fresh session should follow before resuming

1. Read DJ-132 in full ([docs/DECISION_JOURNAL.md#dj-132](../../docs/DECISION_JOURNAL.md#dj-132)). It's the authoritative design; this plan is progress tracking.
2. Read commit `ef2d209` (the alternatives-merge-layer preservation that DJ-132 builds on). Without that fix, the survey's upfront enumeration would erode across revises and the improvement wouldn't compound.
3. Read DJ-124 ([docs/DECISION_JOURNAL.md#dj-124](../../docs/DECISION_JOURNAL.md#dj-124)) for the decision-elaborator workflow DJ-132 extends.
4. Read DJ-130 ([docs/DECISION_JOURNAL.md#dj-130](../../docs/DECISION_JOURNAL.md#dj-130)) for the cognitive-task-separation pattern DJ-132 mirrors at a different layer.
5. Walk `docs/agent-conventions.md` checklist before authoring the survey prompt and revising the elaborator prompt. The user memory mandates this walk for any edit under `internal/scaffold/agents/`.
6. Read the sixth winplan re-run trace at `~/projects/winplan/.locutus/sessions/20260521/1958/24-20bf24/` end-to-end before Phase 5. The compression pattern at initial elaboration is the load-bearing motivation; the trace makes it concrete.

## What is explicitly out of scope

- **Survey on revise dispatches.** Revises engage with critic findings on existing alternatives; survey enumeration would duplicate work. Initial dispatch only.
- **Anti-anchoring critic for survey output.** A critic role that double-checks whether the survey missed obvious candidates. Out of scope for initial landing; the elaborator's anti-anchoring prompt language and existing dimension-driven critics absorb this. If empirical evidence shows survey misses systematically, a future DJ adds the anti-anchoring critic.
- **Per-axis survey-needed flag.** An opt-out for axes where survey doesn't help (specialized domains with thin web-search results). Reversal criterion (e) describes this as the fallback if quality variance across axes proves dominant; not part of initial landing.
- **Survey for non-decision axes** (e.g., narrative-elaborator, scout). Decision-elaboration is where the compression pattern is structurally worst. Other agent roles don't have the same exhaustive-enumeration need. Future DJs may extend the pattern if empirical evidence justifies.
- **Phase-fanout integration** with the survey ([DJ-131](../../docs/DECISION_JOURNAL.md#dj-131)). DJ-131 collapses N axes into one ACP session; the survey would also collapse into the same plan-rendering. Conceptually compatible but architecturally separate; combining is a future DJ.
