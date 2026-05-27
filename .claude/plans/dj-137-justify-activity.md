# DJ-137 — Restore `locutus justify` as an ACP-Dispatched Activity

> **Governing DJ:** [DJ-137](../../docs/DECISION_JOURNAL.md#dj-137). The DJ is the authoritative design record; this plan tracks **progress against** the DJ and captures session-level implementation notes.
>
> **Status:** DONE — Phases 1-6 landed 2026-05-26 on branch `dj-137-justify-activity`. Per-phase summary:
>
> - **Phase 1** ✓ — Frontmatter audit on the five surviving agent prompts. Dropped `output_schema:` (referenced Go types retired in DJ-135 phase 5). Kept `models:` / `thinking:` / `grounding:` (consistent with the rest of the post-DJ-135 agent set; documented in commit message). Body cleanup on splitter + synthesizer to reframe council-era "the system fanned the challenge out" phrasing as "the orchestrator dispatched". Three new tests in `internal/scaffold/agents/justify_agents_dj137_test.go`.
> - **Phase 2** ✓ — `internal/scaffold/plans/justification.md` authored. One-shot shape (no convergence loop). Opens with `TodoWrite` directive per DJ-136 phase 3 convention. Six steps: read target, fetch dependency-graph context with full struct content, optionally dispatch researcher, optionally dispatch challenger (gated on `--against`), dispatch advocate, emit output in operator-chosen format (markdown / json per DJ-137 schema). Error envelope for missing-id targets in both formats. Six new tests in `internal/scaffold/plans/justification_dj137_test.go`.
> - **Phase 3** ✓ — `justification:` entry in `internal/activity/agents-default.yaml` with the three-runtime preference list. One new test in `internal/activity/registry_test.go` plus the existing `LoadsEmbeddedDefaults` test extended to assert the entry.
> - **Phase 4** ✓ — `cmd/justify.go` with `JustifyCmd { ID, Against, Format }` and kong-enum-validated `--format markdown|json`. `cmd/cli.go` registers it under the activity-dispatching verbs section. Eight new tests in `cmd/justify_test.go` covering all flag combinations + the `contextNote` shape contract.
> - **Phase 5** ✓ — Full test suite + vet green across all 30+ packages on the merged result. Empirical validation against winplan deferred to a follow-up session.
> - **Phase 6** ✓ — `CLAUDE.md` (verb count 8→9, new entry in activity-dispatching section, retired-verbs note updated), `docs/council.md` (new "Justify sub-council" section with Mermaid dialogue-flow diagram, agent-set reference; retired-agents section pruned), `docs/activities.md` (5-entry table, new "The `justification` activity" subsection).
>
> Empirical validation against real LLM dispatches (the six manual scenarios in Phase 5) is the called-out follow-up.
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

Recorded in chat 2026-05-26; settled in the DJ (with revisions after the user surfaced the `--format json` and fanout questions). Restated here for implementation reference.

1. **Output to stdout with `--format markdown|json` (default markdown).** Both formats ship in v1. The JSON schema is documented in the DJ's Decision section.
2. **`--against "..."` triggers the adversarial dialogue.** Orchestrator dispatches challenger first, then advocate with challenger brief in context.
3. **Dependency-graph context expansion is default behavior, with full struct content.** The playbook instructs the orchestrator to fetch the target's `influenced_by` / `decisions` / `feature_refs` via batched `spec_get`, carrying full `rationale` + `alternatives` per upstream node — not just title summaries. Under DJ-133's axis-aligned model, the typed JSON struct already encodes the council's comparative research; the advocate consumes it rather than re-derives it.
4. **No parallel-subagent per-child fanout (rejected, not deferred).** The retired pre-DJ-135 `--fanout` mode is superseded by the rich-context expansion above. Leaving fanout on the books as a v2 escape hatch would invite re-derivation of research the spec graph already encodes. If structurally-distinct per-child adversarial dialogue demand surfaces, that's a separate verb, not a justify flag.
5. **`justify-splitter` and `justify-synthesizer` stay published but unused.** Council-era plumbing; v1 doesn't reference them. The publisher emits them so operators can invoke ad-hoc if they want, but no v2 promise.
6. **No per-runtime playbook overlay in v1.** One cross-runtime default suffices.
7. **CLI shape extends the retired verb.** `locutus justify <id> [--against "..."] [--format markdown|json]`.
8. **No hooks or `/goal`.** Read-only one-shot activity; DJ-136's hook framework not needed.
9. **Five agent prompts ship as-is across all three runtimes.** No per-runtime agent overlay (deferred per DJ-135 resolved-question 14, not yet reversed).

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
    1. **Opening directive.** Call `TodoWrite` with the iteration plan — read target node, fetch dependency-graph context, optionally dispatch researcher, optionally dispatch challenger, dispatch advocate, emit output in operator-requested format. Update statuses as steps execute.
    2. **Inputs.** The target node id (from the operator's CLI invocation), optionally the `--against "..."` challenge text, the `--format` choice (markdown default, json optional) — all threaded into the playbook via the activity verb's context note. GOALS.md is read via the `Read` tool for context.
    3. **Step 1: fetch the target node.** Call `mcp__locutus__spec_get` with the id; if missing, surface a clear error and stop. Use the node's body, rationale, alternatives (if a decision), and back-references.
    4. **Step 2: fetch dependency-graph context (load-bearing).** Inspect the target's `influenced_by` / `decisions` / `feature_refs` arrays (whichever apply per node kind). Call `mcp__locutus__spec_get` once with the batched id list. The fetched nodes carry their **full content** — `rationale`, `chosen_option`, `alternatives` slice — not just titles. Under DJ-133's axis-aligned model, this is the comparative research the council surfaced; do not re-derive it. Both the challenger and the advocate receive these linked nodes in their context.
    5. **Step 3 (optional): dispatch `justify-researcher`** if the node's claims involve current-vendor or current-spec facts that warrant web verification (e.g., a decision citing a specific Neon plan tier or Stripe pricing band). Pass the node + the linked-context list + a list of specific claims to verify. Receive findings.
    6. **Step 4 (conditional on `--against`): dispatch `spec-challenger`** with the node + linked context + the user's challenge text. Receive a structured `ChallengeBrief` (2-5 concerns).
    7. **Step 5: dispatch `spec-advocate`** with the node + linked context + (optional) challenger brief + (optional) researcher findings. Receive the active defense.
    8. **Step 6: emit output.**
        - If `--format markdown` (default): synthesize a markdown document — heading with node id + title, then the defense; if adversarial, a "Concerns raised" section followed by "Response to concerns." Print to stdout.
        - If `--format json`: emit the JSON envelope per the schema in the DJ. The `context.influenced_by[]` entries carry full upstream decision structs (rationale + alternatives); the `defense.thesis` and `defense.supporting_arguments` come from the advocate's output; the `adversarial` block is present only when `--against` was set. Empty arrays under `context` (e.g., a leaf decision with no children) emit as `[]` for shape-stability. Print to stdout.
    9. **Stop directive.** The activity completes after the output lands on stdout; no follow-up turns.
- [docs/agent-conventions.md](../../docs/agent-conventions.md) — minor edit if any (the one-iteration-shape convention is already noted under DJ-136 Phase 3's changes).

**Tests:**

- `TestPlaybookJustification_HasPlanToolDirective` — playbook contains the `TodoWrite` opening directive.
- `TestPlaybookJustification_ReferencesRequiredAgents` — playbook references `spec-advocate`, `spec-challenger`, `justify-researcher` by their hyphenated agent ids.
- `TestPlaybookJustification_ReferencesSpecGetTool` — playbook calls out `mcp__locutus__spec_get` for the node fetch.
- `TestPlaybookJustification_RequiresDependencyTraversal` — playbook explicitly instructs the orchestrator to fetch linked nodes via the batched `spec_get` and pass them with full `rationale` + `alternatives` content.
- `TestPlaybookJustification_DocumentsBothFormats` — playbook covers both the markdown default and the `--format json` branch with the schema reference.
- DJ-136 Phase 1's orphan-overlay and drift-invariant tests pass against the new playbook (no overlay file exists, so no drift to check; the orphan test confirms the absence is fine).

**Empirical validation** (manual): run multiple scenarios against winplan (the canonical test bed):

1. `locutus justify dec-primary-datastore` — decision with 9 alternatives; advocate should defend Neon Postgres referencing alternatives like Supabase, Cloudflare D1, etc., not just generic Postgres claims.
2. `locutus justify strat-multitenant-isolation` — strategy; advocate must surface its upstream decisions (`dec-primary-datastore`, `dec-identity-and-tenancy`, `dec-data-residency-and-privacy`, `dec-ai-strategy-posture`, `dec-scope-partisan-posture`) with their rationale, not just name them.
3. `locutus justify feat-voter-universe --against "L2 propensity-score field gating is fragile under workspace migrations"` — feature + adversarial dialogue; challenger surfaces specific concerns, advocate addresses each.
4. `locutus justify dec-primary-datastore --format json | jq '.context.influenced_by[].alternatives'` — JSON output; confirm the structural shape, confirm `alternatives` arrays are populated.

**Verification:** `go build ./... && go vet ./... && go test ./internal/scaffold/... -count=1 -race`. Manual: the four scenarios above on winplan.

**Estimated:** 4-5 hours (the playbook prose is longer now with both formats + dep-traversal; the convention discipline applies; the JSON shape needs careful phrasing in the playbook so the orchestrator emits valid JSON without ad-hoc improvisation).

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

**Goal:** `locutus justify <id> [--against "..."] [--format markdown|json]` parses, dispatches the `justification` activity via `runActivityVerb`, and streams the orchestrator's output to stdout.

**Files expected to change:**

- New: [cmd/justify.go](../../cmd/justify.go). Body shape:

    ```go
    // JustifyCmd implements `locutus justify <id> [--against "..."] [--format markdown|json]`.
    // Dispatches the `justification` activity to a coding-agent runtime
    // via ACP, which executes the published playbook to produce a
    // structured defense of the spec node (optionally with adversarial
    // dialogue when --against is provided). Output format selectable
    // for downstream consumption (markdown default for human readers;
    // json for piping into jq, sub-orchestrators, or future dashboards).
    type JustifyCmd struct {
        ID      string `arg:"" required:"" help:"Spec node id to justify (e.g. dec-primary-datastore, feat-voter-universe, strat-multitenant-isolation)."`
        Against string `help:"Challenge prompt for adversarial dialogue. When set, spec-challenger writes a structured critique first and spec-advocate responds to it."`
        Format  string `help:"Output format: markdown (default) or json." default:"markdown" enum:"markdown,json"`
    }

    func (c *JustifyCmd) Run(ctx context.Context, cli *CLI) error {
        var b strings.Builder
        fmt.Fprintf(&b, "Target node: %s\n", c.ID)
        fmt.Fprintf(&b, "Output format: %s\n", c.Format)
        if c.Against != "" {
            fmt.Fprintf(&b, "\nChallenge from user (for adversarial dialogue):\n%s\n", c.Against)
        }
        return runActivityVerb(ctx, cli, "justification", b.String())
    }
    ```

    Kong's `enum:"markdown,json"` does the validation; passing `--format yaml` errors at parse time with a clear message.

- [cmd/cli.go](../../cmd/cli.go) — add `Justify JustifyCmd ...` to the root CLI struct under the activity-dispatching verbs section. Update any in-doc verb count from 8 to 9.

**Tests:**

- New `cmd/justify_test.go` — kong-parses:
  - `locutus justify dec-foo` (default format = markdown).
  - `locutus justify dec-foo --against "concern"` (with adversarial flag).
  - `locutus justify dec-foo --format json`.
  - `locutus justify dec-foo --against "concern" --format json` (combined).
  - `locutus justify dec-foo --format yaml` errors at parse time (kong enum validation).

  Mirror the pattern of any existing CLI verb test.

**Verification:** `go build ./... && go vet ./... && go test ./cmd/... -count=1 -race`. Manual: `./locutus justify --help` shows the verb, both flags, and the enum-constrained format values.

**Estimated:** 2-3 hours (the CLI surface is small; the test scaffolding may need new helpers if no existing pattern fits).

## Phase 5 — Tests + empirical validation

**Goal:** the activity works end-to-end against a real coding-agent runtime; the test suite covers the new surface; both output formats produce expected shapes.

**Tasks:**

- Run the full test suite: `go test ./... -count=1 -timeout 180s`. All 28+ packages should pass.
- **Manual validation 1: decision target, markdown default.** `./locutus justify dec-primary-datastore` against winplan. Expected: orchestrator emits a plan; fetches the decision + its `influences` (the strategies/features it feeds into); dispatches `spec-advocate`; prints a markdown defense referencing actual `chosen_option` / `rationale` / `alternatives` (9 of them on `dec-primary-datastore`), not generic Postgres claims.
- **Manual validation 2: strategy target, markdown default (the dependency-traversal smoke test).** `./locutus justify strat-multitenant-isolation`. Expected: orchestrator fetches the strategy + its 5 upstream decisions (`dec-primary-datastore`, `dec-identity-and-tenancy`, `dec-data-residency-and-privacy`, `dec-ai-strategy-posture`, `dec-scope-partisan-posture`) with full rationale + alternatives via a single batched `spec_get`; the advocate's defense names the upstream decisions and their rejected alternatives explicitly, not "trust me." This is the validation that dependency-graph context expansion is actually load-bearing.
- **Manual validation 3: adversarial dialogue.** `./locutus justify feat-voter-universe --against "L2 propensity-score field gating is fragile under workspace migrations"`. Expected: `spec-challenger` produces a `ChallengeBrief` with 2-5 concerns (visible in the SDK transcript); `spec-advocate` writes a response addressing those specific concerns. The output structure should make the dialogue clear (concerns section + response section), not a generic restatement.
- **Manual validation 4: JSON format.** `./locutus justify dec-primary-datastore --format json | jq '.context.influenced_by[].alternatives | length'`. Expected: a numeric output per influenced_by entry showing the alternatives slice is populated (each upstream decision carries its 6-9 alternatives). Then `./locutus justify strat-multitenant-isolation --format json | jq '.context.influenced_by | map(.id)'` to confirm the strategy's upstream decision ids land in the right slot.
- **Manual validation 5: JSON + adversarial combined.** `./locutus justify dec-primary-datastore --against "..." --format json | jq '.adversarial.concerns'`. Expected: array of `{concern, severity}` objects. Then `jq '.adversarial.response'` to confirm the advocate's response is present.
- **Manual validation 6: error path.** `./locutus justify nonexistent-id`. Expected: clear error from the orchestrator (the playbook's step-1 fetch returns `missing`; the orchestrator surfaces the error in plain text and stops). `./locutus justify nonexistent-id --format json` should still emit a structured error envelope, not a partial JSON document.

**Tests:** Beyond the unit tests in earlier phases, no new automated tests. Empirical validation is the primary signal for this activity (output quality is hard to automate against; reviewing by hand is correct here). The JSON shape validation in scenarios 4-5 is the closest we get to schema testing without standing up a JSON-schema validator.

**Verification:** Full test suite green; six manual scenarios produce the expected shapes.

**Estimated:** 3-4 hours (manual validation depends on real LLM dispatches; six scenarios across two formats × adversarial-or-not × valid-or-invalid id; counted conservatively).

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

## Total estimate: 14-20 hours single-stranded across 2-3 sessions

Slightly higher than the initial draft after folding in `--format json` and explicit dependency-graph context expansion. Still smaller than DJ-136 because the new code surface is genuinely small (one CLI verb, one playbook, one registry entry, frontmatter audit on existing prompts) and no per-runtime divergence machinery applies. The empirical validation in Phase 5 depends on real LLM dispatches across six scenarios; the figure is conservative.

## Pointers a fresh session should follow before resuming

1. Read the governing DJ end-to-end ([docs/DECISION_JOURNAL.md#dj-137](../../docs/DECISION_JOURNAL.md#dj-137)). The DJ owns the design; this plan owns the phases. If they disagree, the DJ wins and this plan needs updating.
2. Read [DJ-135](../../docs/DECISION_JOURNAL.md#dj-135) phase 5 to understand why the verb was retired and what specifically was deleted (commits `915a784` and `0731747`). The Go-side justify code retired; the agent prompts survived.
3. Read [DJ-136](../../docs/DECISION_JOURNAL.md#dj-136) for the per-runtime dispatch context this plan assumes is already in place. Specifically Phase 2 (plan-update event surfacing in the runner) is the load-bearing assumption — without it, the playbook's `TodoWrite` opening directive doesn't render to the operator.
4. Inspect the current state of the five agent prompts (`internal/scaffold/agents/{spec-advocate,spec-challenger,justify-researcher,justify-splitter,justify-synthesizer}.md`). Confirm they're present in HEAD and note the council-era frontmatter fields that need cleanup in Phase 1.
5. Walk [docs/agent-conventions.md](../../docs/agent-conventions.md) as a checklist before editing any of the five prompts ([[feedback-agent-conventions-checklist-first]]).

## What is explicitly out of scope

- **Parallel-subagent per-child fanout.** Rejected, not deferred — under DJ-133's axis-aligned model + DJ-134's SpecStore, the upstream decisions' `alternatives` slices already encode the research a fanout would re-derive. The rich-context expansion in Phase 2 supersedes fanout. If a future case for structurally-distinct per-child adversarial dialogue surfaces, that's a separate verb (`locutus challenge-each <parent>` or similar), not a justify flag.
- **Per-runtime playbook overlay.** Deferred per resolved-question 6 in the DJ. The cross-runtime default suffices.
- **Per-runtime agent prompt overlay.** DJ-135 resolved-question 14 deferred this for agents specifically; DJ-136 didn't lift the deferral for agents (only for plans). DJ-137 honors that.
- **`justify-splitter` / `justify-synthesizer` invocation in the playbook.** Kept published as agent prompts so operators can invoke ad-hoc, but the playbook doesn't reference them.
- **Hooks for justify.** Read-only activity, no need.
- **`/goal`-driven iteration for justify.** One-shot activity, no need.
- **`--include <id-list>` flag for manual context selection.** Rejected — dependency-graph traversal is automatic; manual selection defeats the encapsulation the verb exists to provide. The operator can already get the same effect by running `locutus justify` on each id individually if they truly want isolated defenses.
