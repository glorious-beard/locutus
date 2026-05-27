## DJ-093: Scout Grounding via `grounding:` Frontmatter Field

**Status:** shipped

**Decision:** Agent frontmatter gains a `grounding: bool` field. When `true`, the LLM call is wired with the provider's native search-grounding capability:

- **Gemini routes** (`googleai/gemini-*`): the `genai.GoogleSearch` tool is appended to the request's `GenerateContentConfig.Tools` via Genkit's `ai.WithConfig` option. The model can search the live web during the call to verify claims against current material.
- **Anthropic routes**: the Genkit Go anthropic plugin doesn't yet expose `web_search`. The runtime logs a `slog.Warn("grounding requested but unsupported on Anthropic; proceeding ungrounded")` and produces a normal ungrounded request — the call still succeeds; only the search capability is dropped. Wire `web_search` through here on the same flag when upstream lands it.

The scout (`spec_scout.md`) is the first agent opted in. Frontmatter:

```yaml
grounding: true
```

The scout prompt gains a `# Use Search to Verify Current State of Practice` section instructing the agent to use search as a sanity check (verify version numbers, recent best-practice shifts, vendor status) — explicitly NOT as an enumeration tool, and explicitly NOT a license to add output schema fields. The scout's responsibilities and output shape are unchanged; grounding raises the floor on what `domain_read` and `implicit_assumptions` can ground themselves against.

**Why:** Foundational gaps like "explicit cloud-platform commitment" and "infrastructure-as-code tool" never surfaced in real winplan runs because the scout's `implicit_assumptions[]` was bounded by training-cutoff intuition. Adding axes to the outliner's prompt is the wrong fix — it ages badly as practice evolves. The right fix is to give the scout the ability to verify what it commits on against current material.

**Why grounding lives on the agent, not on the request.** Per-call Grounding flags push the decision into every callsite. Frontmatter scope is per-agent, which matches how the council reasons about responsibilities — the scout *is* the agent that surveys current state of practice; other agents *aren't* and shouldn't pay for grounded calls. The threading: `AgentDef.Grounding` → `BuildGenerateRequest` → `GenerateRequest.Grounding` → `buildProviderConfig` (attaches `GoogleSearch` or logs the Anthropic warning).

**Hard provider constraint.** Per Genkit's googlegenai live test (`plugins/googlegenai/googleai_live_test.go:241`): "The Gemini API does not support combining GoogleSearch with function calling." An agent with `grounding: true` cannot also have custom Genkit function-call tools attached. For our council that's not a collision — the scout uses grounding (no other tools); the reconciler will use spec_lookup tools (no grounding). For users who configure agents differently, this constraint will surface as an `INVALID_ARGUMENT` from Gemini.

`output_schema` (responseSchema) coexistence with GoogleSearch on Gemini: the plugin's "JSON mode is not compatible with tools" check (`gemini.go:311`) only excludes Genkit `input.Tools` (function calling), not `gcc.Tools` (the GoogleSearch attachment), so the scout's `output_schema: ScoutBrief` should still apply at the same time as grounding. If Gemini's API ever rejects this combination at runtime, drop `output_schema` for the scout and parse JSON from prose.

**Cost note.** Grounded Gemini calls are billed differently from ungrounded calls (search results count toward usage). First runs on real projects will tell us in real numbers; if the cost-per-refine becomes uncomfortable, gate grounding behind an env var (`LOCUTUS_GROUNDING=off`).

**What's new:**

- `AgentDef.Grounding bool` field with frontmatter tag `yaml:"grounding,omitempty"`.
- `GenerateRequest.Grounding bool` field.
- `BuildGenerateRequest` threads `def.Grounding` into the request.
- `buildProviderConfig` attaches `GoogleSearch` for Gemini routes when `req.Grounding`; logs a structured warning for Anthropic routes.
- The googleai branch's "no config needed" early-return is gated on `!req.Grounding` so the GoogleSearch attachment always materializes a config.
- `spec_scout.md` frontmatter sets `grounding: true` and the prompt body documents the search-as-sanity-check role.
- Tests: `TestBuildProviderConfig` gains four grounding subtests (Gemini attach, default-off, materializes-config, Anthropic non-fatal). `TestLoadAgentDefsParsesGrounding` confirms frontmatter round-trips. `TestBuildGenerateRequestThreadsGrounding` confirms the AgentDef → GenerateRequest path.

**What stays the same:**

- The scout's responsibilities, output schema (`ScoutBrief`), and prompt structure (Identity / Context / Task / Quality Criteria).
- All other agents — the reconciler, elaborators, critics, architect, triager — leave grounding off.
- The model-tier resolution; grounding is orthogonal to capability tier.

**Reversal criteria:** revert if (a) the scout-with-grounding produces noticeably worse briefs than ungrounded (e.g. search-result-aggregation displacing engineering judgment) — at which point the prompt's "search is a sanity check" framing needs sharpening; or (b) per-call costs become a meaningful operating concern — at which point we add an env-var gate or capability-tier-based opt-in. Neither failure mode is structural.

**Reference:** plan at [.claude/plans/council-tools-and-revise-fanout.md](../.claude/plans/council-tools-and-revise-fanout.md), Phase 2. Phase 3 (spec_lookup tool for the reconciler) follows.
