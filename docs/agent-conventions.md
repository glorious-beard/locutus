# Agent prompt conventions

This file documents anti-patterns we've discovered (sometimes multiple times) in
the agent prompts under this directory, and the conventions that replace them.
If you're editing or creating an agent prompt, read this first.

## Scope

This file covers the canonical **agent prompts** at `internal/scaffold/agents/` and the **activity playbooks** at `internal/scaffold/plans/`. Both are markdown documents the coding-agent runtime reads — agent prompts become subagent files published to `.claude/agents/locutus/`, `.codex/agents/locutus-*.toml`, etc.; playbooks become activity prompts served by the MCP server (or runtime-published slash commands).

Per DJ-135 Locutus no longer makes LLM calls directly — the coding-agent runtime (Claude Code via `claude-agent-acp`, Codex via `codex-acp`, Gemini via `gemini --acp`) owns model selection and the conversation. The prompts under `internal/scaffold/agents/` are still LLM-facing prose; what changed is *who* sends them to the model. The anti-patterns below still hold — model behavior is unchanged by the dispatch surface.

The driving lesson: LLMs autocomplete from their context. **Telling a model not to do X often makes it do X**, especially with Anthropic models. Whenever a prompt fix would make the agent reliable, the wrong fix is "add a longer don't-do-this section." The right fix is to remove the anti-pattern priming and rephrase positively.

## Publishing and naming (DJ-135)

Canonical agent prompts ship from `internal/scaffold/agents/` and get **published** to each registered runtime by `internal/publisher/` on `locutus init` and `locutus update --reset`. The publisher emits per-runtime copies with translated frontmatter and namespacing:

| Runtime | Subagent path | Slash command path | MCP config |
|---|---|---|---|
| Claude Code | `.claude/agents/locutus/<id>.md` | `.claude/commands/locutus-<cli-verb>.md` | `.mcp.json` |
| Codex | `.codex/agents/locutus-<id>.toml` | `.codex/commands/locutus-<cli-verb>.toml` | `.codex/config.toml` |
| Gemini | `.gemini/extensions/locutus/agents/locutus-<id>.md` | `.gemini/extensions/locutus/commands/locutus-<cli-verb>.toml` | `.gemini/extensions/locutus/extension.json` |

The `<cli-verb>` is the short name operators recognize (`refine`, `import`, `adopt`, `assimilate`) — mapped from the activity name via `CanonicalActivity.CLIVerb` in the publisher. Operators reach for `/locutus-refine` more readily than `/locutus-spec-refinement`; the short form matches the CLI verb that dispatches the same activity.

What the publisher carries through (per `internal/publisher/publisher.go`):

- **The body** — the markdown prompt below the canonical frontmatter is emitted verbatim. Edit the canonical; the next reset propagates.
- **The `id`** — becomes the runtime subagent's `name` field. Hyphenated form (`spec-scout`, not `spec_scout`); per DJ-135 ckpt 2 this is enforced by `TestAllAgentsHaveHyphenatedID_DJ135`. Claude Code requires hyphens; Gemini and Codex accept both.
- **A derived `description`** — `<id> — Locutus <role> agent` when the canonical declares `role:`, else `<id> — Locutus agent`.

What does NOT propagate (because the runtime now owns these):

- `models: [{provider, tier}]` — coding-agent runtime picks the model.
- `thinking: on/off` — runtime decides whether to think.
- `output_schema: <name>` — the runtime doesn't enforce a Go-side schema. The subagent's prose tells the orchestrator what shape to return.
- `grounding: true` — runtime grounding is a runtime concern; canonical declares "this agent needs web search" as a soft requirement that the playbook can fall back from when the runtime doesn't ship search.

These fields still live on the canonical for documentation purposes (and as soft signals the playbook author can reference), but they don't drive a Locutus-side adapter selection anymore.

## Playbook authoring

The activity playbooks at `internal/scaffold/plans/` are a different surface from the agent prompts. Where an agent prompt is one model's instructions for its one task, a playbook is an **orchestrator's instructions** for driving the whole activity — it names which subagents to dispatch, which MCP tools to call, and how to decide when the activity is done.

Conventions specific to playbooks:

- **Use the prefixed MCP tool names.** Claude Code (and other MCP-aware runtimes) prefix MCP server tools as `mcp__<server>__<tool>` in the agent's catalogue. Reference `mcp__locutus__spec_list_manifest`, not `spec_list_manifest`. The agent doesn't have to ToolSearch to find them.
- **Start with an explicit first action.** Empirical observation: a playbook that says "Your very first action is to call mcp__locutus__spec_list_manifest" gets the agent into the right context immediately. Without that, the agent often explores filesystem first and wastes round-trips.
- **Name subagents by their hyphenated id.** Claude Code's Task tool, Codex's equivalent, and Gemini's all reference subagents by their published filename basename. Use `spec-decision-elaborator`, not "the decision elaborator."
- **Encode the convergence-by-construction discipline explicitly.** The previous Go-coded council retired because it couldn't reliably converge — critics re-raised concerns, decisions stalled, runs timed out. The playbook's prose has to push the orchestrating agent to commit aggressively and let revisions fix what needs fixing. See the "Convergence by construction" section in `spec_refinement.md` for the canonical formulation.
- **No anti-pattern lists in playbooks either.** Same model autocomplete behavior applies. Describe the desired iteration shape positively; don't enumerate failure modes the agent should avoid.
- **One-iteration shape (DJ-136, unified harness DJ-140).** Activity playbooks describe **one iteration** of work; the harness owns the outer loop. For headless ACP dispatch that harness is Locutus's runner driving an outer dispatch loop between iterations — uniformly across Claude Code, Codex, and Gemini (DJ-140 retired DJ-136's `/goal`-driven Claude Code path because `/goal` is interactive-only and unavailable over `claude-agent-acp`). A playbook MUST end with a plain-text convergence verdict line the harness can read (canonical form: `converged: true` or `converged: false; <reason>`); one-shot activities emit `converged: true` on iteration 1. A playbook MUST NOT contain its own "loop until converged" framing — that prose competes with the harness for ownership of the termination decision and was the driver of the iteration-stuck failure mode the May 2026 winplan run surfaced.
- **Request the runtime's plan tool early.** Playbooks open with a directive to call `TodoWrite` (Claude Code) or the runtime's equivalent plan tool. The runner surfaces plan entries inline (DJ-136 phase 2's `EventPlan` rendering) so the operator sees what the orchestrator scheduled and how far through it the run is. Without this directive the plan-tool affordance goes unused and the operator's view is tool-call-only.

### Per-runtime playbook overlays (DJ-136)

Activity playbooks live at `internal/scaffold/plans/<activity>.md` as the cross-runtime default. A runtime that needs an idiomatic framing (e.g. Claude Code's interactive `/goal`-driven outer loop) gets a sibling overlay at `internal/scaffold/plans/<activity>.<runtime>[.<mode>].md` whose body **totally replaces** the default for that runtime. The loader (`scaffold.ResolvePlaybook(base, dir, activity, runtime, mode)`) resolves four tiers specificity-descending (`<activity>.<runtime>.<mode>.md` → `<activity>.<runtime>.md` → `<activity>.<mode>.md` → `<activity>.md`), provider outranking mode at equal specificity; `mode` is passed by the consuming operation (dispatch → `headless`, publish → `interactive`), never sniffed (DJ-140). The one existing overlay `spec_refinement.claude-code.interactive.md` resolves only for interactive publishing.

The runtime token in the overlay filename matches an `internal/dispatch/acp/registry.go` `AgentSpawns` key (`claude-code` / `codex` / `gemini`); overlays for unregistered runtimes are rejected by `TestPlaybookOverlay_NoOrphanOverlays`. Every overlay must:

- Have a sibling default `<activity>.md` to fall back to (orphan-overlay test).
- Reference the same `mcp__locutus__*` tool surface as the default (drift-invariant test).
- Reference the same `spec-*` agent ids as the default (drift-invariant test).

Difference is allowed in prose, framing, loop discipline, and runtime-specific directives (e.g. a `/goal` line on the Claude Code interactive overlay — published as a slash command, never dispatched headlessly). Drift management is human discipline plus the grep-invariant test — there is no sync-tracking header convention.

## Anti-patterns to avoid in prompts

## Anti-patterns to avoid in prompts

### 1. Anti-pattern lists are themselves anti-patterns

Don't write a list of forbidden tokens or phrases. They prime the model on the
exact patterns you don't want.

**Don't:**

```text
Do not write "let me rewrite" or "actually" or "fresh attempt" in any field.
Not "broke_down because…", not "broke_down — corrected:", not "verdict: broke_down".
```

This was the documented cause of `justify_synthesizer` reliably failing on
claude-sonnet (commit `abb9119`): the model reproduced the literal tokens
("Actually:", "CORRECTION:") in the rejected output.

**Do:** describe the desired output positively. The model can't autocomplete
patterns you haven't shown it.

```text
The verdict field is one of exactly three string values: held_up,
partially_held_up, or broke_down. Choose one and write it as the field's value.
Reasoning about the choice belongs in the rationale field.
```

### 2. "If you find yourself writing X" is anti-pattern priming with a different hat

Same problem as (1). The model latches on to the forbidden phrasing and
reproduces it.

**Don't:** "If you find yourself writing 'must support' / 'should provide' /
'needs to handle', rewrite."

**Do:** describe what to write instead. "Strategies name a commitment ('Use
PostgreSQL 16 with PostGIS') with a brief reason, not a requirement statement."

### 3. "READ THIS BEFORE EMITTING" sections at the bottom

Sections placed below the main task description don't get priority in the
model's attention budget — they get treated as warnings about edge cases.
Anything important about output shape goes IN the task description, not
after it.

**Don't:** trail a long "Output discipline" / "READ THIS BEFORE EMITTING"
section after the task body.

**Do:** fold output constraints into the task body where the field is
described.

### 4. "No preamble", "No scratchpad", "No internal monologue"

Telling Anthropic-style models "no preamble" mentions the option and makes it
slightly more available. The OutputSchema already enforces JSON; trust it
rather than re-explaining in prose. If a model is preambling around its JSON
in practice, the fix is usually `thinking: off` (see §6), not a prose
reminder.

### 5. Placeholder values in example payloads

`RegisterSchema` example payloads should use **descriptive prose** for example
field values, never `"dummy"`, `"placeholder"`, `"TBD"`, `"foo"`, or similar.
The example payload travels into the model's context as "what a valid
response looks like"; placeholder tokens prime the schema-skeleton failure
mode the `degenerateChallengerBrief` validator exists to catch.

**Do:**

```go
RegisterSchema("ChallengeBrief", ChallengeBrief{
    Concerns: []AdversarialConcern{{
        Weakness:        "the specific weakness in the chosen approach",
        Evidence:        "GOALS clause, search result, or known pattern that supports the concern",
        Counterproposal: "an alternative or test that would resolve the question",
    }},
})
```

**Don't:**

```go
RegisterSchema("ChallengeBrief", ChallengeBrief{
    Concerns: []AdversarialConcern{{Weakness: "dummy", Evidence: "dummy", Counterproposal: "dummy"}},
})
```

### 6. Extended thinking on for agents whose output is structured + short

When the response is an enum value, a structured verdict, or a small JSON
object with few free-text fields, extended thinking ("thinking: on") is at
best wasted budget. At worst, Anthropic's tool-use-based structured output
lets the model continue deliberating inside enum string fields, producing
output like `"partially_held_up ✗\n\nActually: partially_held_up"`.

If the agent has a `rationale` field (or equivalent free-text field where the
model can show its work), thinking is double-spending. Add `thinking: off` to
the frontmatter:

```yaml
---
id: justify_synthesizer
…
thinking: off
---
```

The override is per-agent and lives in `AgentDef.Thinking`. It supersedes the
tier's default.

**DJ-130 update:** the adapter layer now handles this failure mode
automatically — when an agent declares thinking-on + a structured output
schema, each provider adapter's `requiresThinkingSchemaSplit` predicate
fires and `runSplit` issues two SDK calls (reasoning pass with thinking
on + schema cleared, then a format pass with thinking off + schema set
against the provider's own fast tier). The convention here is now
informational rather than load-bearing for agent authors: shipping
`thinking: on` with a schema is no longer silently corrupting. Authors
still see the failure mode here as forensic context for understanding
why the adapter splits, and the convention remains the right call when
the agent's prose-mode reasoning isn't worth two SDK calls' worth of
cost.

## Patterns to prefer

### Push constraints into the schema, not the prompt

The model receives the JSON schema for the output on every call. The richer
the schema, the less the prompt has to say. Adding a `jsonschema` struct tag
travels into the schema doc the model sees before generating the response.

**Enum constraints:**

```go
Verdict string `json:"verdict" jsonschema:"enum=held_up,enum=partially_held_up,enum=broke_down"`
```

**Field descriptions** (use this liberally; it's the single biggest lever
against degenerate-output failures):

```go
Weakness string `json:"weakness" jsonschema:"description=The specific weakness in the chosen approach. Must be a complete sentence describing what's wrong; one-word labels are rejected by the validator."`
```

**Length / shape constraints:**

```go
Concerns []AdversarialConcern `json:"concerns" jsonschema:"minItems=2,maxItems=5"`
```

The general rule: anything you would write in the prompt about a specific
field's shape probably belongs on that field's struct tag. The prompt
shouldn't need to re-document field-level constraints that the schema can
carry.

### Trust the OutputSchema for shape enforcement

The OutputSchema already forces the model into the JSON shape via the
provider's strict structured-output mode. The prompt doesn't need to say
"respond with valid JSON" — that's the job of the schema. The prompt's job
is describing the task and the meaning of the fields, not the syntax.

### Use positive phrasing throughout

"The defense field is two to three paragraphs of strategy-level prose" beats
"don't write less than two paragraphs and don't write more than three." The
positive form gives the model a target; the negative form lists boundaries
it has to remember not to cross.

### Plan the prompt around the schema

Walking the model through the JSON shape (Field A is X, Field B is Y, …)
mirrors the schema and reinforces the structure. Anti-pattern lists fight
the schema by giving the model material to autocomplete.

### Enumeration agents

When an agent's job is exhaustive option-surfacing — surveying the
candidate space for a decision, mining the related work for a research
brief, naming every API consumer affected by a refactor — the prompt
explicitly frames the task as enumeration, not judgment. The two
disciplines compete for attention budget; one prompt can't do both
well, which is why DJ-130 (reasoning/formatting split) and DJ-132
(enumeration/judgment split) exist. The conventions for enumeration
agents follow from that framing:

- **Output schema is flat — no rationale, no citations on entries, no
  judgments.** Each entry carries only the fields the downstream
  judgment-agent needs to weigh it (typically name + first-glance
  fit). Mixing rationale into the survey re-creates the task
  conflation the survey was meant to break.
- **Grounding is load-bearing for currency + hallucination
  prevention.** Training-data-only enumeration produces invented
  vendors and stale candidates (Heroku free tier, Parse
  pre-acquisition). Web search forces every entry to resolve to a
  real, current source. Enumeration is one of the few agent roles
  where grounding is structural rather than supplementary.
- **The prompt explicitly says "enumerate, don't judge."** Positive
  framing on the task; the model can't autocomplete a discipline you
  haven't named. Judgment-task vocabulary ("pick the best", "weigh
  the trade-offs") in an enumeration prompt invites the model to do
  judgment too, which crowds out enumeration breadth.
- **The schema's `minItems` is a floor, not a target.** Set it
  conservatively (3-5) so genuinely-narrow axes can pass; aim higher
  in the prompt (6-10 on well-trodden spaces). Padding-prevention
  belongs in the prompt — "only enumerate candidates you actually
  find via search; don't invent to hit a count" — not in the schema,
  because the schema can't tell the difference between real and
  padded entries.
- **`thinking: off` in the frontmatter.** Enumeration is discovery,
  not reasoning. The work is "search broadly and list what you find";
  extended thinking doesn't make a list longer or more accurate, just
  more expensive.

The canonical example is `spec_candidate_survey` ([DJ-132](DECISION_JOURNAL.md#dj-132)) — runs per-axis before the decision-elaborator on the initial-elaboration path, emits a flat `CandidateList` of 6-10 entries, feeds into the elaborator's projection as a pre-populated candidate set.

### Stable identifiers vs. current content

Identifiers name the *question* a spec node answers; content fields name the *answer*. The two have different lifecycle requirements: ids must stay byte-stable across revisions so backreferences don't drift, while content (title, body, rationale, chosen-option) is rewritten freely as the decision evolves. Conflating them — building the id out of the current answer — makes the id drift whenever the answer changes, which then forces the workflow to compensate (axis-intersection match, ambiguity detection, etc.). The convention pushes the compensation work back into the schema:

- **Decisions** name their axis. The id is `dec-<axis-id>` (DJ-133); the chosen option lives in `title` / `chosen_option`. A Flip changes the body, not the id. The elaborator copies the axis ID verbatim into the output's `id` field — no slug-from-chosen derivation.
- **Features and strategies** are slug-from-title-or-summary today. The convention applies in spirit (the id names a stable handle; description changes are revisions, not new nodes) but the slug derivation is content-based because there is no "axis" abstraction at that layer — the title IS the question the node answers. This is acceptable because feature / strategy titles don't drift the way a decision's chosen option does.

The agent-prompt implication: when an LLM agent author asks "where does the id come from?", the answer is usually "from the structural input the dispatcher gave you (the axis for a decision, the title for a feature)" — not "from the choice you're about to make." Prompts that frame the id as derived from the agent's own deliberation invite Flip-drift; prompts that frame it as copied from a structural input keep ids durable. The DJ-133 elaborator prompt is the canonical example of the latter framing.

### Matching agents

When an agent's job is reconciling a new authoritative source against persisted nodes that need to keep their ids stable across rewordings, the prompt is structurally a **matcher** — not an author and not a judge. The canonical example is `spec-goal-diff-matcher` ([DJ-139](DECISION_JOURNAL.md#dj-139)): given the current text of `GOALS.md` plus the project's existing `goal-*` / `agoal-*` nodes, it emits a structured diff (`unchanged` / `modified` / `deleted` / `added`) that the orchestrator applies via the goal-layer MCP tools. The matcher mints new ids only for genuinely-new claims; existing ids ride through rewordings via the `source_clause` anchor.

The conventions specific to matchers:

- **The output is the diff, period.** A matcher that authors `.advances` / `.respects` citations, recomputes manifest hashes, or calls MCP tools is exceeding scope. Diff-emit and orchestrator-apply are separate disciplines and benefit from being separate agents.
- **The id-preservation discipline is load-bearing prompt copy.** The matcher's prompt must explicitly name the field that anchors id stability across rewordings (`source_clause` for the goal layer) and direct matching to that field first, body second. Without this, the matcher mints a new id every time the LLM rephrases — invalidating every back-reference and forcing the rest of the graph to chase the rename. The right phrasing is positive ("match by `source_clause` similarity") not negative ("don't mint a new id when the body rewords").
- **Every existing node lands in exactly one diff category.** The prompt must mandate that the union of `unchanged` + `modified` + `deleted` covers the full input set; omitting an existing node from all three would leave the orchestrator unable to apply the diff coherently. Same for the forward direction — every current claim in the authoritative source corresponds to exactly one entry across `unchanged` + `modified` + `added`. Make this discipline explicit in a "Mandates" section near the end of the prompt body.
- **Example payloads use realistic ids per anti-pattern #5.** Schema-skeleton mode is especially harmful for matchers because the output drives downstream graph mutation. The `spec-goal-diff-matcher` worked example uses `agoal-fundraising`, `goal-strategic-planning-tool`, `agoal-budget-tracking` — real winplan-style ids the model sees as legitimate, not `foo` / `bar` / `dummy` placeholders.
- **Polarity is structural.** When the matcher distinguishes between in-scope and out-of-scope claims (`goal-` vs `agoal-` prefixes), the prompt names the polarity read from the carve-out language ("out of scope", "owned by", "ceded to") rather than from sentence-surface positivity. The id prefix encodes polarity; the matcher's discipline is reading the claim's polarity correctly so the orchestrator's tool dispatch keys off the right kind.

Matching is its own pattern alongside enumeration and judgment — DJ-130 (reasoning/formatting split), DJ-132 (enumeration/judgment split), and DJ-139 (matching as a separate pass before iteration) all factor disciplines that compete for one prompt's attention budget into separate agents.

### Tool descriptions live in registration, not prompts

When a prompt references a tool the agent can call (spec_list_manifest, spec_get, spec_search, future write tools), the prompt's job is **workflow guidance**: when to reach for the tool, what question it answers in the current task, how the result feeds the next step. The prompt's job is **not** to describe what the tool does or what fields its output carries — that belongs in the `ToolDef.Description` and `InputSchema.properties[].description` passed to `RegisterSpecTools` (or its sibling registration call for non-spec tools).

The rule:

- **Workflow phrases stay in the prompt.** *"Walk the manifest before grading concerns."* *"Issue one `spec_get` with every id you'll need from the open concerns + axes lists — sequential single-id calls cost rounds against the tool-loop cap."* *"Use `spec_search` for topic-scoped lookups when you don't know the id."* These name when and why the model should reach for a tool in the context of the agent's task; that context only exists in the prompt.
- **Tool-behavior text moves to registration.** *"Each manifest entry carries an `origin` field (`settled` / `proposed`) and a `working` flag…"* belongs in `ToolDef.Description` for `spec_list_manifest`, not in every prompt that mentions the tool. *"Returns the full JSON of N spec nodes by id; partial-result shape `{found, missing}`…"* belongs in `ToolDef.Description` for `spec_get`. *"Free-text query, optional kind filter, default limit 20…"* belongs in `InputSchema.properties[].description` for `spec_search`. The model reads tool descriptions on every call; duplicating them in the prompt is bloat that travels into the cache on every dispatch.

**Why this matters beyond cleanliness.** Tool registration is the single canonical source for tool semantics; the prompt is one of N consumers. When the prompt re-describes the tool, the two surfaces drift — a field added at registration (the `origin` tag was added in commit [`eb51a14`](https://github.com/chetan/locutus/commit/eb51a14)) doesn't automatically reach the prompt, and old prompt text that contradicts the new registration confuses the model. Caught in the May 2026 winplan run where the scout's prompt described an older `origin`-less manifest shape while registration had already updated; the model split attention between the two stories.

**The audit question for prompt edits.** When adding or reviewing a prompt that mentions a tool, ask: *"is this WHEN-to-use, or WHAT-the-tool-does?"* WHEN-to-use stays. WHAT-the-tool-does moves to the registration call (or already lives there and the prompt text is redundant). The audit is mechanical — walk the prompt, classify every sentence about a tool. It takes minutes per file and prevents the drift class entirely.

The DJ-134 prompt sweep is the first systematic application of this rule across the council; expect future prompt edits to honor it from the start.

## When you're tempted to add an anti-pattern list

Stop and ask: is the failure caused by the model not knowing the rule, or
by the model behaving in a way the rule can't fix? If it's the second
(thinking-leakage, schema-skeleton mode, structured-output lenience),
prose won't help — change the structural condition (thinking: off, richer
schema tags, better example payloads). Prose anti-pattern lists are the
"add another comment to the spec" of prompt engineering: they accumulate
without improving behaviour and eventually start causing the failures
themselves.

## Reference

- Commit `abb9119` — the justify_synthesizer fix that this file documents.
- DJ-099 — provider-substrate-level fallback design; the validator-driven
  degenerate-output retry path that anti-pattern guards are usually trying
  to avoid.
