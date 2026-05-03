# Plan: Revise Fanout, Scout Grounding, Spec-Lookup Tool

## Context

A real `locutus refine goals` run on winplan (2026-05-02, trace `.locutus/sessions/20260502/1216/35-ef2f20.yaml`) surfaced three distinct problems in the council:

1. **The architect's revise step destroys strategy decisions.** Pre-revise (trace line 3686+) every strategy carried rich inline decisions — `strat-web-application-framework` had "Adopt Next.js on Vercel" and "Use React Server Components" with full rationale, alternatives, and citations. Post-revise (line 4605+) the architect emitted `decisions: [{}]` empty placeholders on every single strategy. The reconciler's `isEmptyInlineDecision` correctly drops the placeholders, but the prior reconcile's real decisions are also gone — there is no fallback because revise replaces the whole proposal. All 8 persisted strategies ended up with zero decisions on disk. **Phase 3 fanout** (DJ-090) bounded elaborate's per-call output but left revise as a single architect call carrying the full RawSpecProposal — exactly the failure mode fanout was designed to fix, just one round later.

2. **The scout has no way to verify "current state of practice."** Foundational gaps like "explicit cloud-platform commitment" or "infrastructure-as-code tool" never surfaced because the scout's `implicit_assumptions[]` is bounded by training-cutoff intuition. Adding axes to the outliner's prompt is the wrong fix — it ages badly as best practices evolve. The right fix is to give the scout the ability to look things up.

3. **The reconciler inlines the entire spec into its prompt.** The `RawSpecProposal` plus full `ExistingSpec` snapshot land in the user message verbatim. At small spec sizes this is fine; at 100+ nodes it pressures context and makes every reconcile call expensive even when only a few nodes need attention. DJ-068 already establishes that `.borg/spec/` IS the manifest — but no tool exists for an agent to query it lazily. `.borg/manifest.json` is empty (just project metadata per DJ-081) and cannot be the spec index.

## Goal

Fix the immediate user-visible bug (#1), then add the two structural capabilities the council is missing (#2, #3): grounding for the scout, lazy spec navigation for the reconciler.

Success looks like:

- A real refine run on winplan produces strategies with the same decision richness on disk as the elaborate fanout originally emitted. The empty-placeholder failure mode is structurally impossible.
- The scout's brief cites recent material (post-training-cutoff) when it commits on a foundational axis. Cloud-platform-commitment and IaC-tool gaps surface when GOALS.md leaves them implicit.
- The reconciler operates against a manifest + lookup tool instead of an inlined dump. Per-call context size is bounded by the reconciler's own working set, not by total spec size.

## Scope

In scope:

- Restructure the revise step as a fanout-per-affected-node, mirroring the Phase-3 elaborate pattern.
- Add a `grounding` field to agent frontmatter; wire it to the Gemini provider's `GoogleSearch` tool. Enable on the scout.
- Add a Genkit tool surface (`spec_list_manifest`, `spec_get`) backed by `specio.SpecStore`. Wire to the reconciler agent. Update the reconciler prompt to navigate-then-fetch instead of receive-everything-inline.

Out of scope:

- Anthropic web_search support (the Genkit Go anthropic plugin doesn't yet expose it; revisit when upstream lands or if grounding-on-scout proves load-bearing enough to justify a custom implementation).
- A separate `.borg/spec/manifest.json` file. The spec directory IS the manifest per DJ-068; the tool reads it dynamically. (Open question — see Open Questions; if a persisted file is preferred, it slots in as Phase 3.5.)
- A persisted research cache for the scout. Search results land in the trace via the call payload; that's the audit surface.
- Other agents getting tools. Elaborators stay with their current per-node prompts. Critics stay free-form. Only the reconciler is rewritten in Phase 3.

## Architectural shifts

```text
Today:
  elaborate → reconcile → critique → revise (single architect call) → reconcile_revise
                                              ↑
                                  full RawSpecProposal in/out
                                  empty placeholders on strategies
                                  unbounded output token pressure

Phase 1:
  elaborate → reconcile → critique → triage → revise_features (fanout)  → reconcile_revise
                                            → revise_strategies (fanout) →
                                            → revise_additions (single)  →
                                              ↑
                                  one revised node per call, scoped to its concerns
                                  no cross-node interference
                                  the empty-placeholder failure mode is structurally absent

Phase 2:
  scout (Gemini Pro/Flash with GoogleSearch tool enabled) → grounded scout brief

Phase 3:
  reconciler (with spec_list_manifest + spec_get tools)
    ├─ no full-spec inlining in the prompt
    └─ navigate the manifest, fetch only what it needs
```

---

## Phase 1: Revise Fanout

### Problem

Today's revise is one architect call. The architect receives the full RawSpecProposal + every critic finding and is asked to emit a "complete corrected RawSpecProposal." This regresses to pre-Phase-3 behavior: too much input, too much output, the model short-circuits by stubbing out entire sections (the empty `decisions: [{}]` placeholders we saw on every strategy).

### Shape

Replace `revise` with three steps:

1. **`triage`** — a single fast-tier call that classifies each critic finding into:
   - `affects: [node-id, …]` — concerns targeting one or more existing features/strategies
   - `additions[]` — concerns proposing a missing feature/strategy that doesn't exist yet
   - `discarded[]` — concerns judged off-topic or already addressed

   Output schema: `RevisionPlan { revisions: [{node_id, concerns: [string]}], additions: [string], discarded: [string] }`. The triage agent doesn't author content — it just routes. This is the same role the architect plays today, just extracted into its own bounded call.

2. **`revise_nodes` (fanout)** — one elaborator call per item in `revisions[]`. Reuses `spec_feature_elaborator` / `spec_strategy_elaborator` (already exists from Phase 3) with one new context section: the concerns targeting *this* node. Output is a `RawFeatureProposal` / `RawStrategyProposal` for that one node — the same shape elaborate emits.

3. **`revise_additions`** (conditional) — fires only when `additions[]` is non-empty. A single architect call asked to emit *only* the new features/strategies that address the additions concerns. Output: a partial `RawSpecProposal` containing just the new entries.

The `merge_as` handler stitches the original RawSpecProposal with the revised nodes (by ID swap) and the additions (append). Reconcile_revise runs unchanged against the merged proposal.

### Why triage is its own step

Without triage, every elaborator call would have to filter the global concerns list to find what applies to its node — duplicated work, inconsistent judgment across siblings. Triage is a single bounded call (small input: the concerns list; small output: the routing plan). It also makes it explicit that critics surface *concerns*, not *patches*; the architect/elaborator decides how to address each concern.

The triage agent uses fast-tier; it's a routing call, not a judgment call.

### Critic findings stay free-form

Critics today emit `{agent_id, severity, text, kind}`. Triage parses the text to map to node IDs (the concerns already mention IDs in practice — see trace line 5229: "feat-voter-universe-segmentation" appears in the critic text). We do *not* require critics to emit `affects[]` themselves — that pushes routing logic into every critic prompt and creates inconsistency. Triage is the single point of judgment.

### Workflow YAML changes

```yaml
- id: triage
  agent: spec_revision_triager
  parallel: false
  depends_on: [critique]
  conditional: has_concerns
  merge_as: revision_plan

- id: revise_features
  agent: spec_feature_elaborator
  parallel: true
  fanout: revision_plan.feature_revisions
  depends_on: [triage]
  conditional: has_concerns
  merge_as: revised_features

- id: revise_strategies
  agent: spec_strategy_elaborator
  parallel: true
  fanout: revision_plan.strategy_revisions
  depends_on: [triage]
  conditional: has_concerns
  merge_as: revised_strategies

- id: revise_additions
  agent: spec_architect
  parallel: false
  depends_on: [triage]
  conditional: has_additions
  merge_as: addition_proposals

- id: reconcile_revise
  agent: spec_reconciler
  parallel: false
  depends_on: [revise_features, revise_strategies, revise_additions]
  conditional: has_concerns
  merge_as: reconciled_proposal
```

`has_additions` is a new conditional — true when `revision_plan.additions` is non-empty. Plumbed through `executor` the same way `has_concerns` is today.

### What changes in code

- New agent: `spec_revision_triager.md` (frontmatter + prompt). Output schema `RevisionPlan`.
- New types: `RevisionPlan`, `NodeRevision { node_id, kind, concerns: []string }` in `internal/agent/`.
- Workflow executor: extend `extractFanoutItems` to handle `revision_plan.feature_revisions` and `revision_plan.strategy_revisions`. The fanout item shape is `NodeRevision`, not `OutlineFeature`/`OutlineStrategy`; the projection function needs to assemble the correct elaborator prompt for revise mode (same elaborator agent, different context section).
- The `spec_feature_elaborator` and `spec_strategy_elaborator` prompts gain a small "If revising an existing node, the user message includes a `concerns:` block — your output must address each concern explicitly while preserving everything else" section. The elaborator's existing instructions don't have to change much; revise mode just adds context.
- New merge handler: stitch original RawSpecProposal + revised nodes + additions into a single RawSpecProposal that reconcile_revise consumes.
- The architect prompt's "On revise rounds" section is removed — the architect is no longer the revise author.

### Phase 1 ship criteria

- A real `locutus refine goals` run on winplan produces strategies on disk with non-empty `decisions: []`. Specifically `strat-web-application-framework` carries the Next.js + Vercel decision (or whatever the elaborator emitted; the point is it survives revise).
- `go test ./...` and `go vet ./...` pass.
- A single critic concern targeting one feature triggers exactly one revise call (per-node fanout, not whole-graph rewrite).
- DJ entry recording the structural fix.

---

## Phase 2: Scout Grounding via Agent Frontmatter

### Problem

The scout's brief is bounded by training intuition. "Cloud platform commitment" and "IaC tool" don't show up in `implicit_assumptions[]` because the model isn't reasoning about *what's missing from this specific GOALS.md against current best practice* — it's reasoning from priors.

### Shape

Add a `grounding` field to agent frontmatter. When `true`, the LLM call is configured with the provider's native search-grounding capability:

- **Gemini path** (`googleai/gemini-*`): the `GoogleSearch` tool is added to the request via `genai.Tool{GoogleSearch: &genai.GoogleSearch{}}`. Genkit Go's googlegenai plugin already supports this — see `plugins/googlegenai/gemini.go:330` and the `GoogleSearch` examples in the plugin tests.
- **Anthropic path**: not supported by Genkit Go's anthropic plugin yet. Setting `grounding: true` on an Anthropic-routed agent logs a warning and proceeds without grounding, so the agent still runs (just ungrounded).

Frontmatter:

```yaml
---
id: spec_scout
role: survey
capability: strong
temperature: 0.4
thinking_budget: 4096
grounding: true        # NEW
output_schema: ScoutBrief
---
```

### Important constraint

Per Genkit's googlegenai plugin tests (`googleai_live_test.go:241`): **Gemini does not support combining `GoogleSearch` with function calling**. An agent with `grounding: true` cannot also have custom Genkit tools attached. This is a hard provider constraint, not a Locutus design choice.

For our council this is fine — the scout uses grounding (no tools), the reconciler uses spec_lookup tools (no grounding). Different agents, no collision.

`output_schema` (responseSchema) coexistence with `GoogleSearch` is a separate question — flagged as an Open Question; verified during implementation.

### Scout prompt change

The scout prompt today says "be a senior engineer briefing the architect." With grounding enabled, the prompt adds:

> Use Google Search to verify your domain_read and technology_options against current state of practice. Do not enumerate everything search returns — search is a sanity check that grounds your commitments in recent material, not a replacement for engineering judgment. When you commit on a tool/framework choice, it should be one you can defend against what actually exists today.

The prompt does NOT instruct the scout to add new categories of `implicit_assumptions`. The grounding raises the floor on what the scout already does; we don't change its responsibilities.

### What changes in code

- `AgentDef.Grounding bool` field in `internal/agent/council.go`.
- `GenerateRequest.Grounding bool` field in `internal/agent/llm.go`.
- `BuildGenerateRequest` threads it.
- `GenKitLLM.buildProviderConfig` (or equivalent) appends `GoogleSearch` tool when `req.Grounding && providerIsGemini`.
- Anthropic path logs a warning ("grounding requested but not supported on Anthropic provider; proceeding ungrounded") so the user gets a one-line signal in stderr/trace.
- `internal/scaffold/agents/spec_scout.md` adds `grounding: true` to frontmatter and the prompt addition above.
- DJ entry capturing the constraint and the per-provider gap.

### Cost note

Gemini's grounded calls are billed differently from ungrounded calls (search results count toward usage). Worth flagging to the user but not gating Phase 2 on a cost analysis — first run on winplan tells us in real numbers.

### Phase 2 ship criteria

- Scout call on a real refine run shows grounded output (search citations or refreshed-relative-to-training references in the brief).
- `grounding: false` on every other agent — no behavior change for agents we didn't touch.
- Anthropic-routed scout (e.g. when user sets only `ANTHROPIC_API_KEY`) logs a warning and produces an ungrounded brief without erroring.
- `go test ./...` and `go vet ./...` pass.
- DJ entry.

---

## Phase 3: Spec-Lookup Tool for the Reconciler

### Problem

The reconciler today gets the entire `RawSpecProposal` and the entire `ExistingSpec` snapshot inlined into its user message. At winplan-scale (~12 features + ~10 strategies + ~4 decisions per run) this is fine. At adopt-scale or extended-spec-scale it pressures context and is wasteful — the reconciler typically reasons about clusters, not the whole graph.

### Shape

Two Genkit tool definitions, registered for the reconciler agent only:

- **`spec_list_manifest()`** → returns a compact index of every persisted spec node:
  ```json
  {
    "features":   [{"id": "feat-x", "title": "...", "summary": "..."},   ...],
    "strategies": [{"id": "strat-y", "title": "...", "kind": "foundational", "summary": "..."}, ...],
    "decisions":  [{"id": "dec-z", "title": "...", "rationale_summary": "..."}, ...]
  }
  ```
  Computed on-demand from `.borg/spec/<kind>/*.json` directory listings. No persisted manifest file. The `summary` field is a one-line truncation of the body/description so the manifest stays scannable.

- **`spec_get(id: string)`** → returns the full spec node JSON for one id. Looks up the kind from the id prefix (`feat-`, `strat-`, `dec-`, `bug-`, `app-`) and reads the corresponding file.

Both are pure reads against `specio.SpecStore`. Zero-copy, no caching beyond the OS page cache.

### No persisted manifest file

The user's intuition was that `.borg/manifest.json` should carry the spec index. DJ-081 already pins that file as the project-root marker, and DJ-068 establishes that `.borg/spec/` IS the manifest. Adding a derived `.borg/spec/manifest.json` that we have to keep in sync with disk creates a drift surface for no benefit — `ListDir` is fast, the JSON files are small, and reading them at tool-call time is bounded by the reconciler's actual lookup pattern rather than by total spec size.

If the user prefers a persisted file (cheaper for very large specs, durable across runs), it slots in as Phase 3.5 — same tool surface, just a different read path under the hood.

### Reconciler prompt rewrite

The reconciler prompt today receives the proposal + existing snapshot inline. The new prompt:

- Receives only the `RawSpecProposal` (the new inline-decisions output from elaborate). The architect is the only producer of new content; that has to be in the prompt.
- Receives a one-line note: "Existing spec is available via `spec_list_manifest` and `spec_get`. Use them when checking whether a proposed inline decision matches an existing decision (`reuse_existing` action)."
- The reconciler's task is unchanged: emit a `ReconciliationVerdict` clustering inline decisions and naming dedupe / resolve_conflict / reuse_existing actions.

The reconciler can choose to call `spec_list_manifest` zero, one, or many times depending on whether the raw proposal even references existing decisions. For a greenfield run (no existing spec), the tool calls return empty manifests and the reconciler proceeds.

### Tool granularity matters

If the manifest is too thin, the reconciler will burn turns on `list → get → get → get` to find what it needs. The right granularity is: manifest entries carry enough one-line context (title + summary + kind) that the reconciler can decide to fetch full content or skip. Worth tuning during implementation; mentioned here so it's not glossed over.

### `output_schema` + tools coexistence

The reconciler emits a structured `ReconciliationVerdict` (responseSchema). Both Anthropic and Gemini support tool use alongside structured output, but exact behavior varies (Gemini's structured-output mode + tools is documented as compatible since `google.genai` v0.10+; Anthropic's tool-use returns `tool_use` content blocks and structured output happens on the final `tool_result`-following turn). Verify during implementation; flagged as an open question.

### What changes in code

- New file `internal/agent/spec_tools.go`:
  - `RegisterSpecTools(g *genkit.Genkit, store *specio.SpecStore) []ai.ToolRef` — defines and registers `spec_list_manifest` and `spec_get`, returns the tool refs to attach to the reconciler agent's `BuildGenerateRequest`.
  - Tool implementations call into `specio.SpecStore` (already has the read primitives).
- `AgentDef.Tools []string` field — names of registered tools to attach. Frontmatter:
  ```yaml
  tools:
    - spec_list_manifest
    - spec_get
  ```
- `GenerateRequest.Tools []ai.ToolRef` and the GenKit Generate call passes them via `ai.WithTools(...)`.
- `internal/scaffold/agents/spec_reconciler.md` adds `tools:` to frontmatter and the prompt update.
- New tests covering: (a) `spec_list_manifest` returns the expected shape on a populated MemFS spec dir; (b) `spec_get` returns the right node by id; (c) the reconciler agent end-to-end run via mocked LLM tool-call dispatch.
- DJ entry.

### Phase 3 ship criteria

- A real refine run on winplan: the reconciler call's input message no longer carries the inlined ExistingSpec dump; the trace shows tool-call entries (`spec_list_manifest`, `spec_get`) for any reuse-existing logic.
- Greenfield refine (no existing spec) still works — tool calls return empty manifests and reconcile proceeds.
- `go test ./...` and `go vet ./...` pass.
- DJ entry.

---

## Sequencing recommendation

1. **Phase 1 first** — smallest, fixes the immediate user-visible failure (strategies losing decisions). Triage agent + revise fanout + workflow update. One PR-sized commit.
2. **Phase 2 next** — small, validates a research feature on one agent. Grounding wiring + scout frontmatter. One commit.
3. **Phase 3 last** — biggest, touches reconciler + adds new tool surface. One commit.

Each phase is independently shippable and reverts cleanly if something breaks.

## Risks and reversibility

- **Phase 1 — triage misroutes a concern.** If triage assigns a concern to the wrong node, the wrong elaborator addresses it (or no elaborator does). Mitigation: the triage prompt includes the full concern text and the manifest of node IDs; the cost of a misroute is one wasted elaborator call, not a corrupted spec. Reversible per-run; the prior single-call revise is restorable in a few lines if triage proves unreliable.
- **Phase 2 — grounded calls cost more.** Gemini's grounded inference is metered separately. Worth measuring on the first run; if the cost-per-refine becomes uncomfortable, gate grounding behind an env var (`LOCUTUS_GROUNDING=off`) or capability tier.
- **Phase 2 — Anthropic users get an ungrounded scout.** They see a warning but the workflow doesn't fail. Equivalent to today's behavior; no regression. When the Genkit Go anthropic plugin gains web_search support, we wire it through the same `Grounding` flag.
- **Phase 3 — reconciler tool-call dispatch loop.** The Genkit tool-use loop runs the model in a multi-turn loop until it stops calling tools. Misbehaving prompt could induce a degenerate tool-call loop. Mitigation: cap tool-call rounds in the GenerateRequest (Genkit supports this) and surface tool-call counts in the trace.
- **Phase 3 — manifest staleness.** Since the manifest is computed on-demand from disk listings, stale data isn't a risk. If we move to Phase 3.5 (persisted manifest), drift becomes a real concern and we'd add a regen-on-write step.

## Open questions

- **Phase 1 — additions handling.** Today's plan has `revise_additions` as a single architect call that emits new features/strategies. An alternative is to drop additions in Phase 1 (additions become a separate `import` flow) and only handle node-targeted revisions. The cleaner shape depends on how often critics propose missing features in practice. Recommendation: ship Phase 1 with `revise_additions` to preserve current capability; revisit if the additions path is rarely exercised.
- **Phase 2 — `output_schema` + `GoogleSearch` coexistence on Gemini.** The plan assumes both can be set. Verify during implementation; if they conflict, the scout's `output_schema: ScoutBrief` may need to drop to free-form parse-on-receive.
- **Phase 3 — manifest summary length.** How long is "one-line summary" in the manifest? 200 chars feels right for features/strategies; decisions might need more (the rationale's first sentence). Tunable per-kind.
- **Phase 3 — should the elaborators get `spec_get` too?** They might benefit from looking up a sibling decision while elaborating a feature. Out of scope for this plan; revisit if elaborator quality shows the same context-pressure pattern.

## What this doesn't fix

- The architect's **outliner** still picks the foundational strategy set with no research. Phase 2 grounds the *scout*; the outliner consumes the scout's brief but doesn't search itself. If the scout-with-grounding doesn't surface foundational gaps the outliner misses, we'd extend grounding to the outliner — same wiring.
- Cost ceilings, caching, and rate-limit handling for grounded calls are not in scope. Phase 2 is the simplest "wire it on, see what happens" move.
