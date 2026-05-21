# Agent prompt conventions

This file documents anti-patterns we've discovered (sometimes multiple times) in
the agent prompts under this directory, and the conventions that replace them.
If you're editing or creating an agent prompt, read this first.

## Scope

This file covers the **LLM-driven council and pipeline personas** defined under
`internal/scaffold/agents/` — scout, architect, critic, advocate, synthesizer,
refiner, archivist, and the rest. These are not the *coding agents* Locutus
delegates to during `adopt`. Coding agents (Claude Code, Codex, Gemini) are
external CLIs reached via the Agent Client Protocol; the transport lives under
`internal/dispatch/acp/`, the design is DJ-119, and there are no prompt files
for them under `internal/scaffold/agents/`. If you're here looking for how
Locutus drives the coding agent, you want `internal/dispatch/` and DJ-119, not
this file.

The driving lesson: LLMs autocomplete from their context. **Telling a model not to
do X often makes it do X**, especially with Anthropic models. Whenever a prompt
fix would make the agent reliable, the wrong fix is "add a longer don't-do-this
section." The right fix is to remove the anti-pattern priming and tighten the
structural constraints at the schema level.

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
