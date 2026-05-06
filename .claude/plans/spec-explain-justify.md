# Plan: explain and justify a spec node

## Context

A decision's JSON contains `rationale`, `architect_rationale`, `alternatives[].rejected_because`, and `citations`. That IS a static justification — but it's a JSON object, not an argument. A reader has to assemble the narrative themselves, and there's no way to defend the decision against a specific challenge that wasn't anticipated when the decision was authored.

Two distinct needs:
1. **Render** the existing justification fields plus back-references in human-readable form — no LLM call, reproducible, free.
2. **Generate** an active defense, optionally adversarial — LLM-driven, useful when an external stakeholder asks "why this?" or when revisiting a decision under new constraints.

This plan introduces three commands: `explain`, `justify`, `justify --against`. Each is a small composable surface. Together they turn the decision JSON from a record into a deliberation tool.

Depends on the per-node renderer from `spec-snapshot.md` — `RenderDecision`, `RenderFeature`, `RenderStrategy` are reused as the "pure render" path.

## Design

### CLI surface

```
locutus explain <id>                              # pure render, no LLM
locutus justify <id>                              # advocate writes a defense
locutus justify <id> --against "..."              # advocate vs challenger dialogue
locutus justify <id> --against-finding <session>:<index>  # challenge sourced from a critic finding
```

`<id>` is any spec node id — `dec-…`, `feat-…`, `strat-…`, `app-…`, `bug-…`. The command auto-detects from the prefix.

`--against` takes a free-form challenge prompt the user wants the advocate to address. `--against-finding` is sugar for "use the critic finding from a past session as the challenge"; cheaper to type when you're working from a stored complaint.

Both `explain` and `justify` honor `--format markdown|json`; default `markdown`.

### `locutus explain <id>` — no-LLM rendering

Composes the per-node renderer from `spec-snapshot.md` with the inverse-index lookups from `internal/spec/graph.go`. Output for `dec-…`:

```markdown
# `dec-adopt-datadog-for-unified-observability`

**Title:** Adopt Datadog for unified observability
**Status:** proposed
**Confidence:** 0.85

## Rationale

[rationale prose]

## Architect rationale

[architect_rationale prose]

## Alternatives considered

### Sumo Logic

**Their pitch:** [rationale]

**Why rejected:** [rejected_because]

### DIY ELK + Prometheus
[same shape]

## Citations

- *best_practice* — SRE Book Ch 6: Monitoring Distributed Systems
- *goals* — GOALS.md §4: cost discipline

## Lineage

**Influenced by:** (none) / `dec-other`, `dec-third`
**Influences:** `dec-derived-foo`

## Where this is used

**Strategies:** `strat-observability-incidents`
**Features:** (none directly)
**Approaches:** (none yet)

## Provenance

- Generated: 2026-05-06T14:42:00-07:00
- Updated: 2026-05-06T14:42:00-07:00
- Source session: `.locutus/sessions/20260506/1429/57-2b758f/`
```

For `feat-…`: feature description, acceptance criteria, decisions inlined with one-line rationales, relevant strategies, approaches if any.

For `strat-…`: body, decisions list, referenced-by features, kind-specific lineage.

`--format json` emits the same data in `SpecNodeExplanation` shape — feeds tooling.

No LLM call. Cheap. Reproducible. Always works offline once the spec is loaded.

### `locutus justify <id>` — single-agent defense

A small council slice — one agent, one round.

The advocate agent's job: take the full `explain` output as input plus the project's `GOALS.md` plus any neighboring decisions/strategies the renderer surfaced, and write a coherent paragraph-form defense that names the goal-clauses being satisfied, the constraints being respected, and the trade-offs being accepted.

**New agent:** `internal/scaffold/agents/spec_advocate.md`

```yaml
---
id: spec_advocate
role: justification
models:
  - {provider: googleai, tier: balanced}
  - {provider: anthropic, tier: balanced}
  - {provider: openai, tier: balanced}
output_schema: JustificationBrief
---

# Identity

You are the spec advocate. A user has asked you to defend a specific
spec node — explain why this decision/feature/strategy is the right
choice for this project given the goals, constraints, and alternatives
considered.

# Context

You receive:
- The full node content (rationale, alternatives, citations, etc.)
- GOALS.md (verbatim)
- The project's other relevant decisions and strategies (the back-refs
  from the explain output)

# Task

Write a 2-4 paragraph defense in plain prose. Cover:
1. What problem this node solves and which goal-clauses motivate it.
2. Why the chosen path beats the listed alternatives, citing specific
   constraints (cost, performance, operational complexity, vendor
   relationships).
3. What this commits the project to that should be reconsidered if
   constraints change — i.e., the conditions under which this decision
   would NOT hold.

Be specific about which goal-clauses you're citing. Avoid generic
language like "best practice" without a concrete reference.

# Output

JustificationBrief schema with three fields:
- defense (string): the 2-4 paragraph prose defense
- goal_clauses_cited (array of strings): the exact GOALS.md excerpts you cited
- conditions_under_which_invalid (array of strings): bullet list of
  constraint changes that would prompt revisiting this node
```

**New schema:**

```go
type JustificationBrief struct {
    Defense                     string   `json:"defense"`
    GoalClausesCited            []string `json:"goal_clauses_cited"`
    ConditionsUnderWhichInvalid []string `json:"conditions_under_which_invalid"`
}
```

Output in Markdown form:

```markdown
# Justifying `dec-adopt-datadog-for-unified-observability`

## Defense

[2-4 paragraph prose]

## Goals being served

- "From GOALS.md §4: ..."
- "From GOALS.md §7: ..."

## Conditions under which this should be revisited

- If the team grows past 30 engineers (Datadog's per-host pricing exits favorable territory)
- If on-prem deployment becomes a requirement
- ...

---

*Generated 2026-05-06 against spec hash <X>; session: .locutus/sessions/<sid>/*
```

Single agent run. Uses `RunInto` against the `JustificationBrief` schema. Recorded as a session like every other agent call.

### `locutus justify <id> --against "..."` — adversarial dialogue

Two-agent call sequence:

1. **Challenger agent** — given the node + the user's challenge prompt, formulates a structured critique. Output: a list of specific concerns with rationale.

2. **Advocate agent** (same as above, called a second time) — given the node + the challenger's critique, addresses each concern point-by-point. Outputs the defense PLUS a verdict: `held_up`, `partially_held_up`, or `broke_down`.

**New agent:** `internal/scaffold/agents/spec_challenger.md`

```yaml
---
id: spec_challenger
role: justification
models:
  - {provider: googleai, tier: strong}
  - {provider: openai, tier: strong}
  - {provider: anthropic, tier: strong}
grounding: true
output_schema: ChallengeBrief
---

# Identity

You are the spec challenger. A user has flagged a possible weakness
in a specific spec node and wants you to formulate the strongest
version of that critique. You are an adversary to the spec, not an
ally — your job is to surface the genuine concerns the user implied,
not to be diplomatic.

# Context

You receive:
- The full node content
- GOALS.md
- The user's challenge prompt
- (When grounding is on) Search for current state-of-practice
  evidence — has the alternative the user mentioned matured / become
  cheaper / become unsupported?

# Task

For each concrete concern the user's challenge implies, write:
- The specific weakness in the chosen approach
- Evidence (cite GOALS, search results, or known patterns)
- A counterproposal or test that would resolve the question

Output 2-5 concerns. Less is fine if the challenge is narrow.

# Output

ChallengeBrief schema:
- concerns: array of {weakness, evidence, counterproposal}
```

The advocate's second-call output extends `JustificationBrief`:

```go
type AdversarialDefense struct {
    JustificationBrief
    PointByPointAddressed []AddressedConcern `json:"point_by_point_addressed"`
    Verdict               string             `json:"verdict"`  // "held_up", "partially_held_up", "broke_down"
    BreakingPoints        []string           `json:"breaking_points,omitempty"`
}

type AddressedConcern struct {
    ConcernSummary string `json:"concern_summary"`
    Response       string `json:"response"`
    StillStands    bool   `json:"still_stands"`
}
```

If `Verdict == "broke_down"` or `partially_held_up`, the output suggests `locutus refine <id> --brief "..."` with the breaking points pre-populated as a refine brief — bridges naturally to the `spec-refine-brief-diff` plan.

Markdown output renders the dialogue:

```markdown
# Justifying `dec-adopt-datadog-for-unified-observability`

**Challenge:** "Vendor lock-in concerns; what about open-source observability?"

## Challenger's concerns

### 1. Vendor lock-in over a 3-year horizon
*Evidence:* Datadog's pricing has historically increased 15-20%/year;
GOALS.md §4 names "cost discipline" as a constraint.

*Counterproposal:* Run a 6-month pilot of Grafana Cloud (or self-hosted
Grafana + Loki + Mimir) before committing to Datadog as the foundation.

### 2. ...

## Advocate's response

[defense paragraphs]

### Point-by-point

**Concern 1 (vendor lock-in):**
[response paragraph]
*Still stands:* yes / partially / no

### 2 (...)
[same shape]

## Verdict: PARTIALLY HELD UP

**Breaking points:**
- The 6-month pilot suggestion is reasonable; the current rationale
  doesn't address why we'd commit to Datadog before validating costs at
  WinPlan's scale.

## Suggested next step

```
locutus refine dec-adopt-datadog-for-unified-observability \
  --brief "Address vendor-lock-in concerns: validate Datadog cost at our scale via 6-month pilot, or commit to a self-hosted alternative."
```

---

*Generated 2026-05-06 against spec hash <X>; session: .locutus/sessions/<sid>/*
```

### `--against-finding <session>:<index>` — sugar

Given a session ID and a critic-finding index, load the finding text from `.locutus/sessions/<session>/calls/<critic>.yaml` and use it as the `--against` prompt. Saves typing when iterating from a recorded review.

### File layout

**New:**
- `cmd/explain.go` — `ExplainCmd` (no LLM)
- `cmd/justify.go` — `JustifyCmd` (LLM via the council slice)
- `internal/scaffold/agents/spec_advocate.md`
- `internal/scaffold/agents/spec_challenger.md`
- `internal/agent/justify.go` — `RunJustify(ctx, exec, fsys, id) (*JustificationOutput, error)` and `RunJustifyAgainst(ctx, exec, fsys, id, challenge) (*AdversarialDefense, error)`. Single-call orchestrator analogous to `IntakeDocument` (no workflow YAML; this is too small for a workflow).
- `internal/agent/justify_schemas.go` — `JustificationBrief`, `ChallengeBrief`, `AdversarialDefense`, `AddressedConcern` types + `RegisterSchema` calls
- `internal/render/justify.go` — Markdown rendering of justification + adversarial dialogue
- `cmd/explain_test.go` — golden-file tests against a fixture graph (no LLM)
- `cmd/justify_test.go` — `MockExecutor`-backed test ensuring the right agent is dispatched and the output is rendered correctly. No live API in unit tests.

**Modified:**
- `internal/agent/schemas.go` — register `JustificationBrief`, `ChallengeBrief`, `AdversarialDefense`
- `cmd/cli.go` — add `Explain` and `Justify` subcommands

### Output and persistence

Both `explain` and `justify` write to stdout by default. Neither persists into the spec graph — these are deliberation aids, not state mutations. Routes to filesystem via shell redirect (`> docs/justify-dec-foo.md`).

Sessions are recorded via the existing `SessionRecorder` so the LLM transcripts are available under `.locutus/sessions/`.

The `JustificationBrief` schema gets registered in the global registry so any future feature (e.g. attaching a justification snapshot to a node) can consume the structured form.

## Verification

- `cmd/explain_test.go` — fixture spec dir, expected Markdown output, assert byte-equality (or `LOCUTUS_UPDATE_GOLDEN=1` to refresh).
- `cmd/justify_test.go` — `MockExecutor` returns scripted JustificationBrief; assert the rendered Markdown contains the expected sections, the agent ID was `spec_advocate`, and (for the adversarial path) two calls fired with both `spec_challenger` and `spec_advocate` agent IDs.
- Smoke: `locutus explain dec-foo` in a winplan-shaped fixture shouldn't error and produces deterministic output across runs.
- Live integration (gated on env): `locutus justify dec-foo --against "what about Cockroach?"` against winplan, eyeball check that the dialogue is coherent.

## MCP exposure

Following the existing CLI ↔ MCP parity pattern in `cmd/mcp.go`, both
verbs land as MCP tools alongside the CLI commands.

**`explain` MCP tool** — read-only, no LLM, fast. Lets a connected
agent look up any spec node by id without reading the directory.

```go
type explainInput struct {
    ID     string `json:"id"`
    Format string `json:"format,omitempty"`  // "markdown" | "json"; default markdown
}
```

Result: single `text` content block with the rendered explanation.

**`justify` MCP tool** — fires LLM calls; returns the structured
brief or adversarial dialogue. Dispatch path is identical to the CLI;
only the I/O wrapping differs (text-content for Markdown, JSON-as-text
for JSON form).

```go
type justifyInput struct {
    ID             string `json:"id"`
    Against        string `json:"against,omitempty"`
    AgainstFinding string `json:"against_finding,omitempty"`  // "<session>:<idx>"
    Format         string `json:"format,omitempty"`
}
```

Mutual exclusion of `against` and `against_finding` is enforced in the
handler (returns an MCP error before dispatching).

**Why both belong in MCP:** an agent assisting a developer in their
IDE can answer "why did we choose Datadog?" by calling `explain
dec-adopt-datadog-…` (cheap, instant). For "we're considering switching
to Cockroach — does our Postgres decision still hold?" the agent calls
`justify dec-postgres-… --against "Cockroach for cross-region writes"`
and surfaces the dialogue verdict. Both flows happen inside the
agent's existing conversation; no terminal context-switch.

**Cost note:** `justify` runs LLM calls on the locutus side. An agent
calling it through MCP triggers nested LLM activity — fine for ad-hoc
questions, expensive if a client agent loops over every decision. The
single-agent variant is balanced; the adversarial variant runs two
calls. Agents calling these tools should treat them as "asks a
specialist" rather than "filters a list."

Tool registrations live in `cmd/mcp.go` alongside existing entries;
handlers are thin wrappers around `cmd/explain.go` and `cmd/justify.go`
core functions.

## Sequencing

Depends on `spec-snapshot.md` landing first (per-node renderer + graph loader + inverse index). After that:
- `explain` lands first — purely additive, no LLM, no schema, easy to verify.
- `justify` (single-agent) lands second.
- `justify --against` lands third — needs the new challenger agent and the dialogue rendering.

The three can ship as one PR if convenient; the dependency is just internal.

## Out of scope

- Persisting justifications back into the decision JSON. Worth considering later (a `JustificationProvenance` field that grows over time as new justifications are generated), but adds spec-write surface and isn't load-bearing for the deliberation loop.
- Multi-round dialogue (advocate-challenger-advocate-challenger). Two rounds are enough to surface the structural argument; more rounds drift toward the LLM monologuing. If real demand surfaces, extend later.
- Comparing justifications across spec versions (was the rationale stronger before that refine?). Falls out naturally once the history layer captures justifications; for now it's offline diff between stored outputs.
- Auto-routing the dialogue verdict into a `refine` call. The output suggests the command; user invokes manually. Adds correctness margin.
