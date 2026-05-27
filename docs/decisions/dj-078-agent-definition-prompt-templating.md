## DJ-078: Agent Definition and Prompt Templating Policy (Refines DJ-077)

**Status:** settled

**Decision:** DJ-077 permits selective adoption from Google ADK but leaves open *how* adopted patterns are surfaced. DJ-078 pins the two most load-bearing choices: serialization format and prompt templating syntax.

1. **Serialization: Locutus keeps its own format.** Agent definitions remain markdown files in `.borg/agents/` (per DJ-036), loaded via `internal/agent.NamedAgentFn`. We do **not** adopt ADK's YAML `LlmAgentConfig` shape or its 4600-line JSON-Schema artifact. Markdown agent defs evolve freely; YAML would freeze a surface we'd inherit maintenance on.
2. **Prompt templating: Go `text/template` syntax.** When an agent prompt needs variable interpolation, use `{{.Var}}` / `{{.Scope.Var}}` resolved by `text/template` (stdlib), not ADK's custom `{var}` regex. A prompt file *may* carry a companion data-dictionary block (YAML frontmatter or sibling `<name>.vars.yaml`) documenting each variable's name, type, source, and whether it's required — optional for MVP, promotable to a schema later.
3. **ADK adoption conforms to (1) and (2).** When a capability ADK already figured out (state-injection semantics, optional variables, static/dynamic split for caching, transfer-prompt generation, evaluation rubrics, etc.) earns its way into Locutus, we **copy the design** — not the code — and rewrite it against our markdown + `text/template` surface. Attribution still applies per DJ-077 (top-of-file comment + NOTICE entry) when the translation is faithful enough that ADK's design is recognizably load-bearing.

**Why `text/template` over ADK's `{var}`:**

- Go stdlib. Zero dependency, zero port cost. Already used by Go authors across the ecosystem; no new syntax to teach.
- `{{ }}` delimiters collide less with natural prose in agent instructions than `{ }`. ADK mitigates the collision with a non-identifier passthrough; we avoid the class of problem entirely.
- Templates compile once and validate variable references at parse time, surfacing typos before the first LLM call. ADK's regex substitutes silently (`{typo}` → empty string with `{var?}`, else `KeyError`).
- Richer constructs when we need them (`{{if}}`, `{{range}}`, `{{with}}`) without reaching back to ADK.

**Why keep markdown agent defs:**

- Every existing agent def in `internal/scaffold/agents/` and `.borg/agents/` is a markdown file today. Migrating to YAML would be a forced churn with no operational payoff.
- Markdown is legible to humans reviewing agent behavior in PRs. YAML with quoted instruction strings is not.
- Frontmatter (YAML block at top of a markdown file) already gives us a structured-metadata slot when needed — for the optional data dictionary, or future fields like `model:`, `description:`, `tags:` — without committing to ADK's full schema.

**What "copy the design, not the code" looks like:**

- If we later want ADK's static/dynamic instruction split for prompt caching, we implement it in Go against our markdown files: a frontmatter field `static_instruction: true` routes the body to the cacheable slot; dynamic bodies flow through `text/template`. We do **not** port `flows/llm_flows/instructions.py` line-for-line.
- If we later want ADK's `{var?}` optional-variable semantics, we implement it as a `text/template` FuncMap helper (`{{optional .var}}`), not by swapping template engines.
- If we later want sub-agent transfer prompts, we generate them in Go from our own `sub_agents:` frontmatter field — ADK's `_build_transfer_instruction_body` is the *shape* we match, not the source we ship.

**Interaction with existing DJs:**

- **DJ-036 (shipped):** unchanged. Agent defs remain external markdown.
- **DJ-077 (settled):** refined. Selective adoption still permitted; DJ-078 adds the constraint that adopted designs translate to Locutus's markdown + `text/template` surface rather than bringing ADK's serialization and templating conventions with them.
- **DJ-029 (shipped):** unchanged. Custom orchestration posture holds.

**Not in scope for DJ-078:**

- Immediate port of `inject_session_state`. Whether to land a `text/template`-based interpolation helper as a second pre-Round-3 increment (alongside memory) is a separate scheduling call, not a policy question. Today, no agent prompt in `internal/scaffold/agents/` uses variables; we ship templating when the first real use case lands.
- A committed data-dictionary schema. The optional companion block is permitted but unspecified until a concrete agent needs it.
- Any constraint on adopting ADK patterns *outside* agent definition and prompt formation (memory, evaluation, etc.) — those fall under DJ-077's general adoption posture.

**Reversal criteria:**

DJ-078 gets revisited if: (a) an ADK capability we need cannot be faithfully expressed against markdown + `text/template` without significant contortion; or (b) we discover operational need for a declarative agent config format that multiple tools (Locutus + external) must parse identically — at that point a shared YAML surface may earn its way in.
