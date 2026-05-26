# DJ-137 — Restore `locutus justify` as an ACP-Dispatched Activity

> **Governing DJ:** [DJ-137](../../docs/DECISION_JOURNAL.md#dj-137). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** designed; implementation not started.
> **Predecessors:** [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135) (council retirement that removed the pre-existing `cmd/justify.go` + Go-side justify dispatchers; the activity registry + publisher this plan extends); [DJ-136](../../docs/DECISION_JOURNAL.md#dj-136) (per-runtime idiomatic dispatch — DJ-137 assumes it has shipped, specifically Phase 2's plan-update event surfacing in the runner).
> **Surface area:** small. ~40-60 lines of CLI (one new `cmd/justify.go`), ~80-120 lines of new playbook prose (`internal/scaffold/plans/justification.md`), a one-line addition to `internal/activity/agents-default.yaml`, a frontmatter audit pass across 5 existing agent prompts, plus tests + docs.
> **Discipline (per memory):**
>
> - Tests → design pause in chat → code; mid-impl failures trigger design judgment, not test patching ([[feedback-test-first]]).
> - Cite DJ-137 + the precise constraint in chat before touching `cmd/justify.go`, the playbook, or the existing agent prompts ([[feedback-cite-djs-before-spec-work]]).
> - Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) as a checklist BEFORE editing the recovered agent prompts ([[feedback-agent-conventions-checklist-first]]); council-era prompts may carry anti-pattern priming, schema-skeleton placeholders, or thinking-leakage shapes that current conventions rule out.
> - Phase 6 (docs) MUST include the [docs/council.md](../../docs/council.md) update ([[feedback-council-doc-maintenance]] + [[reference-council-doc]]); DJ-132 plan Phase 4 is the template ([[reference-dj-132-plan]]).

## Why this plan exists

`locutus justify <id>` was retired in [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135) phase 5 alongside the Go-driven council it dispatched through. The user surfaced demand for the verb on 2026-05-26 — the spec-advocate + spec-challenger dialogue it produced is a deliberation aid nothing else in current Locutus replicates, and the use case ("challenge me on this specific concern, give me the structured rebuttal") isn't well-served by ad-hoc chat with the coding-agent runtime directly.

The architectural shape it should return as is exactly the shape `refine` takes today: a CLI verb → `runActivityVerb` → playbook → ACP dispatch → coding-agent runtime → MCP `spec_get` for the target node → subagent dispatch (`spec-advocate`, `spec-challenger`, `justify-researcher`) → synthesized output streamed back to stdout. DJ-137 articulates that restoration and breaks the work into phases that each leave the system in a working state.

A surprise the audit revealed: the five agent prompts the pre-DJ-135 verb dispatched (`spec-advocate.md`, `spec-challenger.md`, `justify-researcher.md`, `justify-splitter.md`, `justify-synthesizer.md`) **survived** the council deletion. They sit in `internal/scaffold/agents/` and the publisher per DJ-135 phase 4 already emits them to every detected runtime. They just haven't had a playbook to invoke them. That trims this work substantially — there's no agent prompt recovery from git history; just a frontmatter audit + a playbook to drive them.

## Reference state (before DJ-137 starts)

- **Activity registry** at [internal/activity/agents-default.yaml](../../internal/activity/agents-default.yaml) maps activities → prioritized runtime list. Four entries today (`spec_refinement`, `feature_ingestion`, `code_adoption`, `code_assimilation`). DJ-137 adds a fifth.
- **`runActivityVerb`** at [cmd/activity_verb.go](../../cmd/activity_verb.go) is the shared dispatch helper every activity-dispatching verb uses. Single line wrapper for justify will follow the [`refine`](../../cmd/refine.go) pattern verbatim.
- **Playbook loader** at [cmd/activity_verb.go:57-67](../../cmd/activity_verb.go#L57-L67) reads `.borg/plans/<activity>.md`. Under DJ-136 it will gain runtime-overlay resolution (`<activity>.<runtime>.md` first, `<activity>.md` fallback) — justify ships with the cross-runtime default only.
- **Five existing agent prompts** under [internal/scaffold/agents/](../../internal/scaffold/agents/):
    - `spec-advocate.md` — writes active defense (output: `AdversarialDefense` per the current frontmatter; that schema reference is council-era and gets cleaned up in Phase 1).
    - `spec-challenger.md` — writes 2-5 adversarial concerns against a user's challenge (output: `ChallengeBrief`).
    - `justify-researcher.md` — grounded web-research subagent that produces a findings document the advocate can cite.
    - `justify-splitter.md`, `justify-synthesizer.md` — council-era plumbing for parallel fanout. Kept published (operators may invoke ad-hoc) but the v1 justification playbook doesn't reference them.
- **Publisher** at [internal/publisher/](../../internal/publisher/) emits every agent under `internal/scaffold/agents/` to each runtime's agent directory. No publisher change needed — the prompts ship already.
- **CLI verb structure** in [cmd/cli.go](../../cmd/cli.go) — 8 verbs currently (4 activity-dispatching + 4 operational) + 2 read-only (explain + list) + 3 MCP. Adding justify takes the activity-dispatching count to 5.

## Resolved design questions

Recorded in chat 2026-05-26; settled in the DJ. Restated here for implementation reference.

1. **Output is markdown to stdout.** No `--format json` flag in v1.
2. **`--against "..."` triggers the adversarial dialogue.** Orchestrator dispatches challenger first, then advocate with challenger brief in context.
3. **Fanout across decisions deferred.** No `--fanout` flag in v1. Run multiple `locutus justify` invocations for parent justification.
4. **`justify-splitter` and `justify-synthesizer` stay published but unused in v1.** Reusable if a v2 fanout mode lands.
5. **No per-runtime playbook overlay in v1.** One cross-runtime default suffices.
6. **CLI shape matches the retired verb.** `locutus justify <id> [--against "..."]`.
7. **No hooks or `/goal`.** Read-only one-shot activity; DJ-136's hook framework not needed.
8. **Five agent prompts ship as-is across all three runtimes.** No per-runtime agent overlay (deferred per DJ-135 resolved-question 14, not yet reversed).

## Phase 1 — Frontmatter audit + body cleanup on the five existing agent prompts

**Goal:** the five surviving agent prompts under `internal/scaffold/agents/` conform to current [agent-conventions.md](../../docs/agent-conventions.md) discipline. Drop council-era frontmatter fields that don't apply under the playbook execution model; preserve the prose body content (which is largely correct).

**Files expected to change:**

- [internal/scaffold/agents/spec-advocate.md](../../internal/scaffold/agents/spec-advocate.md) — drop `output_schema: AdversarialDefense` (no Go-side schema enforcer); drop the `models:` array (the coding-agent runtime owns model selection); drop `thinking: on` (runtime owns thinking config). Audit the body for council-era references ("the council orchestrator dispatches you", "the workflow", any spec-of-Go-side machinery) and reshape to the playbook context ("an operator invoked `locutus justify <id>`; you receive the target node and produce a defense").
- [internal/scaffold/agents/spec-challenger.md](../../internal/scaffold/agents/spec-challenger.md) — same frontmatter audit. Body already describes the challenger's job in playbook-compatible language; minor edits if any.
- [internal/scaffold/agents/justify-researcher.md](../../internal/scaffold/agents/justify-researcher.md) — same. The grounded-research framing carries forward naturally.
- [internal/scaffold/agents/justify-splitter.md](../../internal/scaffold/agents/justify-splitter.md) — same audit even though v1 doesn't invoke it. The prompt stays in scaffold and gets published; cleanup keeps it portable for v2.
- [internal/scaffold/agents/justify-synthesizer.md](../../internal/scaffold/agents/justify-synthesizer.md) — same.

**Discipline:** [[feedback-agent-conventions-checklist-first]] is binding. Walk all six numbered anti-patterns + four positive patterns in [docs/agent-conventions.md](../../docs/agent-conventions.md) before each prompt edit. Specifically watch for:

- Anti-pattern priming (the prompt mentions a failure mode it doesn't want the model to repeat — this can prime the model toward the failure).
- Schema-skeleton placeholders ("dummy", "TBD", "foo" in example payloads).
- Thinking-leakage (instructions phrased in a way that elicits long reasoning chains in the visible output).
- Negative phrasing ("don't do X" → "do Y instead").

**Tests:**

- `TestSpecAdvocateAgentLoads` (or equivalent — there may already be an `internal/scaffold/agents/agents_justify_test.go` from the council era; check git history at commit `aac7559` and prior) — asserts the agent .md parses cleanly into the current `AgentDef` shape with the cleaned frontmatter.
- `TestJustifyAgentsConformToConventions` (new, optional) — a meta-test that grep-checks all five prompt bodies against the agent-conventions anti-pattern list. Cheap to write; useful as regression armor.

**Verification:** `go build ./... && go vet ./... && go test ./internal/scaffold/... -count=1 -race`. Visual diff each prompt before/after; confirm prose intent is preserved.

**Estimated:** 2-3 hours (mostly careful prose review, not new content authoring).

## Phase 2 — Author `internal/scaffold/plans/justification.md`

**Goal:** the cross-runtime default playbook that the orchestrator executes when `locutus justify <id>` dispatches. One-shot shaped (no convergence loop; no outer iteration). Per DJ-136 Phase 3's convention, the playbook opens by instructing the orchestrator to call `TodoWrite` (or runtime-equivalent) to lay out its plan so the runner's plan-update event surfacing (DJ-136 Phase 2) renders it inline.

**Files expected to change:**

- New: [internal/scaffold/plans/justification.md](../../internal/scaffold/plans/justification.md). Sections:
    1. **Opening directive.** Call `TodoWrite` with the iteration plan — read node, optionally dispatch challenger, optionally dispatch researcher, dispatch advocate, synthesize output. Update statuses as steps execute.
    2. **Inputs.** The target node id (from the operator's CLI invocation), optionally the `--against "..."` challenge text (passed via the activity verb's context note). GOALS.md is read via the `Read` tool for context.
    3. **Step 1: fetch the node.** Call `mcp__locutus__spec_get` with the id; if missing, surface a clear error and stop. Use the node's body, rationale, alternatives, citations, and back-references.
    4. **Step 2 (optional): dispatch `justify-researcher`** if the node's claims involve current-vendor or current-spec facts the researcher could verify via web search. Pass the node + a list of specific claims to verify. Receive findings.
    5. **Step 3 (conditional on `--against`): dispatch `spec-challenger`** with the node + the user's challenge text. Receive a structured `ChallengeBrief` (2-5 concerns).
    6. **Step 4: dispatch `spec-advocate`** with the node + (optional) challenger brief + (optional) researcher findings. Receive the active defense.
    7. **Step 5: synthesize and emit.** The orchestrator combines the advocate's defense (and, if `--against`, the dialogue structure) into a markdown output that prints to stdout. Format guidance: heading with the node id + title, then the defense; if adversarial, a "Concerns raised" section followed by "Response to concerns."
    8. **Stop directive.** The activity completes after the synthesis lands on stdout; no follow-up turns.
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — minor edit if any (the one-iteration-shape convention is already noted under DJ-136 Phase 3's changes).

**Tests:**

- `TestPlaybookJustification_HasPlanToolDirective` — playbook contains the `TodoWrite` opening directive.
- `TestPlaybookJustification_ReferencesRequiredAgents` — playbook references `spec-advocate`, `spec-challenger`, `justify-researcher` by their hyphenated agent ids.
- `TestPlaybookJustification_ReferencesSpecGetTool` — playbook calls out `mcp__locutus__spec_get` for the node fetch.
- DJ-136 Phase 1's orphan-overlay and drift-invariant tests pass against the new playbook (no overlay file exists, so no drift to check; the orphan test confirms the absence is fine).

**Empirical validation** (manual): run `locutus justify dec-primary-datastore` against a populated project (winplan is the canonical test bed). Confirm the orchestrator emits a plan, dispatches the advocate, and prints a defense to stdout. Then run `locutus justify dec-primary-datastore --against "Neon's branch-per-PR ceiling at 10 will block our 30-workspace E-Day operating point"` and confirm the adversarial dialogue produces both a `ChallengeBrief` (visible in the SDK transcript) and an advocate response addressing those specific concerns.

**Verification:** `go build ./... && go vet ./... && go test ./internal/scaffold/... -count=1 -race`. Manual: `locutus justify` on winplan.

**Estimated:** 3-4 hours (the playbook prose is moderate-length and the convention discipline applies).

## Phase 3 — Activity registry entry

**Goal:** `justification` becomes a known activity that `runActivityVerb` can dispatch. The runtime preferences match the other activities (Claude Code first, Codex second, Gemini third) — empirical evidence to date is Claude Code-only, but the structure should be parallel so adding Codex / Gemini later is one-line.

**Files expected to change:**

- [internal/activity/agents-default.yaml](../../internal/activity/agents-default.yaml) — add a `justification:` entry following the shape of `spec_refinement:`:

    ```yaml
    justification:
      runtimes: [claude-code, codex, gemini]
    ```

  (Exact key names match whatever the existing entries use — verify by reading the file at implementation time; the YAML schema is in [internal/activity/agents_yaml.go](../../internal/activity/agents_yaml.go).)

**Tests:**

- `TestActivityRegistry_LoadsJustification` — `activity.NewRegistry(fsys).Resolve("justification", nil)` returns a runtime id (with all three binaries stubbed-present, returns `claude-code`).
- The existing activity-registry test suite probably parameterizes over all entries; adding `justification` automatically extends coverage.

**Verification:** `go test ./internal/activity/... -count=1`.

**Estimated:** 30 minutes (one YAML line + test).

## Phase 4 — CLI verb at `cmd/justify.go`

**Goal:** `locutus justify <id> [--against "..."]` parses, dispatches the `justification` activity via `runActivityVerb`, and streams the orchestrator's output to stdout.

**Files expected to change:**

- New: [cmd/justify.go](../../cmd/justify.go). Body shape:

    ```go
    // JustifyCmd implements `locutus justify <id> [--against "..."]`.
    // Dispatches the `justification` activity to a coding-agent runtime
    // via ACP, which executes the published playbook to produce a
    // structured defense of the spec node (optionally with adversarial
    // dialogue when --against is provided).
    type JustifyCmd struct {
        ID      string `arg:"" required:"" help:"Spec node id to justify (e.g. dec-primary-datastore, feat-voter-universe)."`
        Against string `help:"Challenge prompt for adversarial dialogue. When set, spec-challenger writes a structured critique first and spec-advocate responds to it."`
    }

    func (c *JustifyCmd) Run(ctx context.Context, cli *CLI) error {
        contextNote := fmt.Sprintf("Target node: %s", c.ID)
        if c.Against != "" {
            contextNote += fmt.Sprintf("\n\nChallenge from user (for adversarial dialogue):\n%s", c.Against)
        }
        return runActivityVerb(ctx, cli, "justification", contextNote)
    }
    ```

- [cmd/cli.go](../../cmd/cli.go) — add `Justify JustifyCmd ...` to the root CLI struct under the activity-dispatching verbs section. Update any in-doc verb count from 8 to 9.

**Tests:**

- New `cmd/justify_test.go` — kong-parses `locutus justify dec-foo` and `locutus justify dec-foo --against "concern"` correctly. Mirror the pattern of any existing `cmd/refine_test.go` (or the equivalent test for an activity-dispatching verb).

**Verification:** `go build ./... && go vet ./... && go test ./cmd/... -count=1 -race`. Manual: `./locutus justify --help` shows the verb and flag.

**Estimated:** 2-3 hours (the CLI surface is small; the test scaffolding may need new helpers if no existing pattern fits).

## Phase 5 — Tests + empirical validation

**Goal:** the activity works end-to-end against a real coding-agent runtime; the test suite covers the new surface.

**Tasks:**

- Run the full test suite: `go test ./... -count=1 -timeout 180s`. All 28+ packages should pass.
- Manual validation 1: `./locutus justify dec-primary-datastore` against winplan. Expected: orchestrator emits a plan; dispatches `spec-advocate`; prints a markdown defense citing Neon Postgres's branch-per-PR isolation, the Postgres-native RLS posture, the multi-tenant write path discipline. Defense should reference the actual `chosen_option` / `rationale` / `alternatives` fields on the decision, not make up new claims.
- Manual validation 2: `./locutus justify dec-primary-datastore --against "Neon's branch-per-PR ceiling at 10 will block our 30-workspace E-Day operating point"`. Expected: `spec-challenger` produces a `ChallengeBrief` with 2-5 concerns (visible in the SDK transcript); `spec-advocate` writes a response addressing those specific concerns. The output structure should make the dialogue clear (concerns section + response section), not a generic restatement.
- Manual validation 3: `./locutus justify nonexistent-id`. Expected: clear error from the orchestrator (the playbook's step-1 fetch returns `missing`; the orchestrator surfaces the error in plain text and stops).

**Tests:** Beyond the unit tests in earlier phases, no new automated tests. Empirical validation is the primary signal for this activity (output quality is hard to automate against; reviewing by hand is correct here).

**Verification:** Full test suite green; three manual scenarios produce the expected shapes.

**Estimated:** 2-3 hours (manual validation depends on real LLM dispatches; counted conservatively).

## Phase 6 — Documentation

**Goal:** [CLAUDE.md](../../CLAUDE.md), [docs/council.md](../../docs/council.md), [docs/activities.md](../../docs/activities.md) all reflect the restored verb and the new activity. Per [[feedback-council-doc-maintenance]] this phase is mandatory, not a follow-up.

**Files expected to change:**

- [CLAUDE.md](../../CLAUDE.md):
    - Activity-dispatching verbs section: bump from 4 to 5 entries, add `locutus justify <id> [--against "..."]` with a one-sentence description.
    - Retired-verbs note: remove the "justify retired" line (or update it to reference the DJ-137 restoration).
    - Verb count headline: 8 → 9.
- [docs/council.md](../../docs/council.md):
    - New section: "Justify sub-council" — describes `spec-advocate` and `spec-challenger` (and optionally `justify-researcher`) as a separate sub-council invoked by the justification activity, distinct from the spec-generation council. Mermaid diagram showing the simple `challenger → advocate` dialogue flow (or just `advocate` for the non-adversarial path).
    - Per [[reference-dj-132-plan]], the dj-132 plan Phase 4 is the template for the update pattern.
- [docs/activities.md](../../docs/activities.md):
    - New section for the `justification` activity, paralleling the existing `spec_refinement` entry. Document the CLI invocation, the `--against` flag, the playbook path, the agent set, the expected output shape.

**Tests:** None new. Doc updates reviewed manually.

**Verification:** Read each updated doc end-to-end; confirm cross-references resolve; confirm Mermaid renders.

**Estimated:** 2 hours.

## Phase 7 — Status flip + plan marked DONE

**Goal:** with phases 1-6 shipped and validation green, flip DJ-137 status to `shipped` and mark this plan DONE.

**Files expected to change:**

- [docs/DECISION_JOURNAL.md](../../docs/DECISION_JOURNAL.md) — DJ-137 Status: `designed; implementation not started` → `shipped YYYY-MM-DD — Phases 1-6 landed; empirical validation on winplan confirmed the adversarial-dialogue path produces structured concern + response output.`
- This file — top-level Status: `designed; implementation not started` → `DONE`.

**Verification:** Final reading pass on the DJ; full test suite green; the three manual validations all produce the expected output shape.

**Estimated:** 30 minutes.

## Total estimate: 12-18 hours single-stranded across 2-3 sessions

Smaller than DJ-136 because the new code surface is genuinely small (one CLI verb, one playbook, one registry entry, frontmatter audit on existing prompts). The empirical validation in Phase 5 depends on real LLM dispatches and may extend the wall-clock; the figure is conservative.

## Pointers a fresh session should follow before resuming

1. Read the governing DJ end-to-end ([docs/DECISION_JOURNAL.md#dj-137](../../docs/DECISION_JOURNAL.md#dj-137)). The DJ owns the design; this plan owns the phases. If they disagree, the DJ wins and this plan needs updating.
2. Read [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135) phase 5 to understand why the verb was retired and what specifically was deleted (commits `915a784` and `0731747`). The Go-side justify code retired; the agent prompts survived.
3. Read [DJ-136](../../docs/DECISION_JOURNAL.md#dj-136) for the per-runtime dispatch context this plan assumes is already in place. Specifically Phase 2 (plan-update event surfacing in the runner) is the load-bearing assumption — without it, the playbook's `TodoWrite` opening directive doesn't render to the operator.
4. Inspect the current state of the five agent prompts (`internal/scaffold/agents/{spec-advocate,spec-challenger,justify-researcher,justify-splitter,justify-synthesizer}.md`). Confirm they're present in HEAD and note the council-era frontmatter fields that need cleanup in Phase 1.
5. Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) as a checklist before editing any of the five prompts ([[feedback-agent-conventions-checklist-first]]).

## What is explicitly out of scope

- **Fanout (justify a parent → fan out across its child decisions).** Deferred to v2. v1 ships single-node justify only.
- **`--format json` flag.** Deferred. Output is markdown to stdout.
- **Per-runtime playbook overlay.** Deferred per resolved-question 5 in the DJ. The cross-runtime default suffices.
- **Per-runtime agent prompt overlay.** DJ-135 resolved-question 14 deferred this for agents specifically; DJ-136 didn't lift the deferral for agents (only for plans). DJ-137 honors that.
- **`justify-splitter` / `justify-synthesizer` invocation in the v1 playbook.** Kept published as agent prompts, not used by the v1 activity.
- **Hooks for justify.** Read-only activity, no need.
- **`/goal`-driven iteration for justify.** One-shot activity, no need.
