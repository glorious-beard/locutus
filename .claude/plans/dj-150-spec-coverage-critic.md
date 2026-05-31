# DJ-150 `spec-coverage-critic` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a fast-tier grounded `spec-coverage-critic` agent that enumerates deliverable-shape obligations at refine-time and surfaces uncovered ones as findings; integrate the critic into the spec-refinement and assimilate playbooks (default + Claude Code overlay × 2 playbooks); register the agent; document the new piece + codify DJ-150 §8's topology-vs-process bias check as a project-wide convention in agent-conventions.md and CLAUDE.md. No new node kinds, no new MCP tools, no new persistence — outcomes drive feature creation/revision via the existing `spec_propose_feature` / `spec_revise_feature` surface.

**Architecture:** Coverage critic mirrors DJ-132's `spec-candidate-survey` shape — fast tier + grounding + structured output — and DJ-129's dimension-critic dispatch pattern. The critic receives the deliverable shape(s) identified by the architect plus the current feature set; enumerates category obligations via web search; judges coverage by reading feature bodies in natural language; emits a `CoverageReport`. The architect's revise pass addresses uncovered obligations by extending existing feature scope (prose growth) or proposing new features (which then enter the graph as existing PM-shaped artifacts). Findings live in session state; nothing persists to `.borg/spec/` outside of feature mutations the architect makes.

**Tech Stack:** Markdown agent prompts under `internal/scaffold/agents/`; playbook prose under `internal/scaffold/plans/`; YAML registry at `internal/activity/agents-default.yaml`; doc updates under `docs/` + `CLAUDE.md`. Spec doc: [docs/decisions/dj-150-spec-coverage-critic.md](../../docs/decisions/dj-150-spec-coverage-critic.md). No Go code changes — the publisher auto-publishes new agent files from the scaffold directory walk; no MCP tool registration; no struct changes.

---

## Required reading before starting

- **[docs/decisions/dj-150-spec-coverage-critic.md](../../docs/decisions/dj-150-spec-coverage-critic.md)** — the governing spec. Read all 8 decision points + 8 resolved design questions + alternatives + consequences.
- **[docs/decisions/dj-132-candidate-survey-agent.md](../../docs/decisions/dj-132-candidate-survey-agent.md)** — the fast-tier grounded-enumeration pattern DJ-150's critic mirrors. Read the schema discipline, the grounding rationale, the cost analysis.
- **[docs/decisions/dj-129-dimension-driven-critics.md](../../docs/decisions/dj-129-dimension-driven-critics.md)** — the critic-dispatch-at-refine-time pattern. Read the integration model with the architect/elaborator workflow.
- **[docs/agent-conventions.md](../../docs/agent-conventions.md)** — MANDATORY before authoring the critic prompt. Walk the six anti-patterns + four positive patterns as a checklist; the critic prompt MUST NOT enumerate example obligations (that would re-introduce the anti-pattern this DJ exists to avoid).
- **[internal/scaffold/agents/spec-candidate-survey.md](../../internal/scaffold/agents/spec-candidate-survey.md)** — closest sibling: fast tier, grounding on, structured output. Use it as the structural model for the new agent's front-matter and body shape.
- **[internal/scaffold/plans/spec_refinement.md](../../internal/scaffold/plans/spec_refinement.md) + [.claude-code.md](../../internal/scaffold/plans/spec_refinement.claude-code.md)** — the refine playbook + CC overlay; identify the architect-pass → elaborator-fan-out boundary where the critic dispatch slots in.
- **[internal/scaffold/plans/code_assimilation.md](../../internal/scaffold/plans/code_assimilation.md) + [.claude-code.md](../../internal/scaffold/plans/code_assimilation.claude-code.md)** — assimilate gets the same dispatch step (since assimilate runs the architect via the gap-analyst's feature/strategy proposals; same coverage discipline applies).
- **[internal/activity/agents-default.yaml](../../internal/activity/agents-default.yaml)** — the registry; understand the entry shape used by sibling critics.

## Phase ordering & independence

- **Phase 1** (agent authoring) is foundational; everything else assumes the agent exists.
- **Phase 2** (refine + assimilate playbook integration, 4 files) depends on Phase 1's agent id existing.
- **Phase 3** (registry entry) depends on Phase 1.
- **Phase 4** (documentation) depends on the agent + playbooks landing, but can run in parallel with Phase 3.
- **Phase 5** (validation) last.

**Commit discipline:** commit after every green test step. Conventional prefixes (`feat:` / `fix:` / `refactor:` / `test:` / `docs:`).

---

## Phase 1 — Agent authoring

### Task 1: Author `internal/scaffold/agents/spec-coverage-critic.md`

**Files:**
- Create: `internal/scaffold/agents/spec-coverage-critic.md`

> **MANDATORY**: Walk `docs/agent-conventions.md` checklist before authoring per the project's strict requirement (six numbered anti-patterns + four positive patterns).

- [ ] **Step 1: Read the conventions checklist + the sibling agent**

Run:
```
cat docs/agent-conventions.md
cat internal/scaffold/agents/spec-candidate-survey.md
cat internal/scaffold/agents/spec-architect.md
```

Note the front-matter convention (id, role, models per provider, thinking, grounding, output_schema). Note that `spec-candidate-survey` uses `thinking: off` + `grounding: true` + fast tier; that is the right pattern for DJ-150's critic.

- [ ] **Step 2: Author the agent prompt**

Create `internal/scaffold/agents/spec-coverage-critic.md` with this shape:

```markdown
---
id: spec-coverage-critic
thinking: off
grounding: true
role: critic
models:
  - {provider: anthropic, tier: fast}
  - {provider: googleai, tier: fast}
  - {provider: openai, tier: fast}
output_schema: CoverageReport
---

# Identity

You are the coverage critic for Locutus's spec-refinement and assimilate pipelines (DJ-150). Given a project's identified deliverable shape and its current feature set, you enumerate the category obligations the deliverable carries by virtue of its shape, then judge whether the current features cover each obligation.

Your enumeration is grounded — every obligation you surface cites at least one authoritative source (framework documentation, industry guidance, accessibility standards, regulatory references, recognized product-design references) that you found via web search at runtime. Your coverage judgment reads feature titles, summaries, and bodies in natural language and decides whether each obligation's concern is addressed within the existing scope.

You use a fast-tier model with grounding because the work is breadth-first enumeration plus structural matching, both of which fast tier handles well when sources are grounded.

# Context

You receive as a user message:

- **Deliverable shape** — one entry: `{shape_id, shape_label, source_evidence}`. The shape was identified by the architect; you reason about obligations that follow from this shape's category. Common shapes include `hosted-code-with-users`, `hosted-code-api-only`, `mobile-app`, `firmware-embedded`, `hardware-pcb`, `cli-or-library`, `documentation`. Treat the shape literally — do not assume a SaaS web app when the shape says firmware.
- **Current features** — array of `{id, title, summary, body_excerpt}`. The `body_excerpt` is the first ~500 characters of each feature's body, sufficient for coverage judgment without paying the full-body token cost.
- **Goal layer** — array of `{id, title, description}` for context on what the project is trying to achieve; sometimes a goal directly implies an obligation (e.g., "support voters with screen readers" implies an accessibility obligation).

# Task

Produce a `CoverageReport` — an array of `obligations`, each with the fields:

- **title** — short noun phrase naming the obligation (e.g., "Public entry surface", "Authenticated routing boundary", "In-app navigation system"). Title-case; no punctuation at the end.
- **description** — one to three sentences stating what the obligation requires and why it is a category requirement for this deliverable shape. Frame it as a *category requirement*, not as an implementation task — "a first-encounter surface that connects users to the product's value proposition" is correct; "a landing page with a hero section, value-prop above the fold, and a sign-in CTA in the header" is too granular.
- **citations** — array of `{source, url}`, minItems 1. Each entry resolves to a real, current source via web search. Industry guidance, framework docs, accessibility standards, named principles — all valid. Hallucinated or stale sources are not acceptable; if no real source supports the obligation, omit the obligation.
- **covered_by** — array of feature ids whose body covers the obligation's concern. May be empty if no current feature addresses the obligation. Use the actual `id` strings from the input features array.
- **rationale** — one short sentence explaining the coverage judgment. For covered obligations: which feature(s) cover it and how. For uncovered obligations: what's missing from the current feature set.

# Discipline

Enumerate every obligation the deliverable carries — including obvious or minor ones. Category obligations that seem too obvious to mention are exactly the ones the spec graph silently assumes; surfacing them is the whole point. A web app for users needs a public entry surface; surface it. An API needs an authentication boundary; surface it. A mobile app needs in-app navigation; surface it.

Stay at the category level. The critic enumerates obligations; the architect's revise pass decides whether to extend an existing feature or propose a new one. Do not propose features yourself; that crosses the role boundary.

Cite real sources. Web-search every obligation before listing it. If your search returns nothing concrete, you are probably enumerating something too project-specific or too implementation-flavored — re-frame as a category requirement and search again.

# Anti-hallucination

If web search returns no authoritative source for an obligation you intuited from category knowledge, omit the obligation rather than fabricating a citation. Better to miss a real obligation than to surface one with a citation that doesn't resolve.

If you cannot identify any obligations for a deliverable shape (rare; this would be a very small or unusual shape), emit an empty obligations array with a short note in the rationale explaining why.

Do not emit prose outside the structured output.
```

- [ ] **Step 3: Verify hyphenated-ids invariant**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/agents/ -run HyphenatedIds -v 2>&1 | tail -5`
Expected: PASS — the new agent's `id: spec-coverage-critic` passes the hyphenation check.

- [ ] **Step 4: Verify full scaffold suite**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/... -count=1 2>&1 | tail -5`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/agents/spec-coverage-critic.md
git commit -m "feat(agents): spec-coverage-critic for deliverable-shape obligation enumeration (DJ-150)"
```

---

## Phase 2 — Refine + assimilate playbook integration

### Task 2: Integrate the critic into `internal/scaffold/plans/spec_refinement.md`

**Files:**
- Modify: `internal/scaffold/plans/spec_refinement.md`

- [ ] **Step 1: Identify the dispatch point**

Run: `cd /Users/chetan/projects/locutus && grep -nE "Architect|elaborator|dispatch|fan-?out" internal/scaffold/plans/spec_refinement.md | head -20`

Locate the boundary between the architect's first pass (where deliverable shapes are identified and initial features land) and the elaborator fan-out (where features and decisions get elaborated). DJ-150 §1 places the critic dispatch between these two steps.

- [ ] **Step 2: Add the dispatch step**

Insert a new section between the architect pass and the elaborator fan-out. Use this prose as the template; adapt the heading numbering to match the existing playbook's structure:

```markdown
## Step <N> — Coverage critic (per identified deliverable shape)

After the architect identifies deliverable shapes and proposes the first feature set, dispatch `spec-coverage-critic` once per identified shape via the `Task` tool. Input to each dispatch:
- The deliverable shape entry from the architect's output.
- The current `features[]` array — for each feature, supply `{id, title, summary, body_excerpt: first ~500 chars of body}`.
- The goal layer — `{id, title, description}` for every `goal-*` and `agoal-*` node.

The critic returns a `CoverageReport` array. For each entry whose `covered_by` is empty, flag it as an uncovered obligation. Pass the uncovered-obligation list into the elaborator fan-out's input alongside the existing per-node findings.

The elaborator's revise pass addresses each uncovered obligation by either (a) extending an existing feature's body to discuss the obligation's concern (when an existing feature is the natural fit — surface the rationale in the feature's revision note), or (b) proposing a new feature via `spec_propose_feature` whose body covers the obligation. The choice is the architect/elaborator's judgment; the critic does not propose features.

For multi-deliverable projects (e.g., hardware + firmware + mobile companion + cloud backend), dispatch the critic once per shape in parallel. Coverage is judged per-shape — a hosted-backend feature does not cover a mobile-app obligation by default.

Findings live in session state (the critic's transcript is written under `.locutus/sessions/<sid>/`). Nothing persists to `.borg/spec/` outside of the feature mutations the elaborator makes.
```

- [ ] **Step 3: Run playbook tests**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/plans/ -count=1 2>&1 | tail -5`
Expected: all pass (the invariants_dj144_test.go should not be triggered by this addition; the critic dispatch is via the `Task` tool, not via direct MCP writes, which is the invariant the test enforces).

- [ ] **Step 4: Commit**

```bash
git add internal/scaffold/plans/spec_refinement.md
git commit -m "feat(plans): dispatch spec-coverage-critic per deliverable shape during refine (DJ-150)"
```

---

### Task 3: Integrate the critic into `internal/scaffold/plans/spec_refinement.claude-code.md`

**Files:**
- Modify: `internal/scaffold/plans/spec_refinement.claude-code.md`

- [ ] **Step 1: Locate the architect → elaborator boundary in the CC overlay**

Run: `cd /Users/chetan/projects/locutus && grep -nE "architect|elaborator|parallel\(\)|pipeline\(\)" internal/scaffold/plans/spec_refinement.claude-code.md | head -20`

Find the equivalent boundary in the workflow-form overlay.

- [ ] **Step 2: Add the workflow-form dispatch**

Insert a workflow-style step that mirrors Task 2's default playbook addition, expressed in the dynamic-workflow primitives (`parallel()` across deliverable shapes when multi-deliverable). Reference the default playbook's `Step <N> — Coverage critic` section for full prose; the overlay adds only the workflow-native handle. Example shape:

```markdown
### Step <N> — Coverage critic fan-out (parallel per deliverable shape)

After the architect pass returns its identified deliverable shapes + first feature set, dispatch `spec-coverage-critic` via `parallel()` — one dispatch per identified shape. Each dispatch receives the shape entry, the current feature array with body excerpts, and the goal layer. Collect every dispatch's `CoverageReport`; concatenate the uncovered-obligation entries; feed them into the elaborator fan-out's input alongside the existing per-node findings.

See `spec_refinement.md` § "Coverage critic" for the full prose on input shape, role boundary, and how the elaborator addresses uncovered obligations.
```

- [ ] **Step 3: Verify + commit**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/scaffold/plans/ -count=1 2>&1 | tail -5`
Expected: all pass.

```bash
git add internal/scaffold/plans/spec_refinement.claude-code.md
git commit -m "feat(plans): coverage-critic parallel fan-out in spec-refinement CC overlay (DJ-150)"
```

---

### Task 4: Integrate the critic into `internal/scaffold/plans/code_assimilation.md`

**Files:**
- Modify: `internal/scaffold/plans/code_assimilation.md`

Same pattern as Task 2, applied to assimilate. The dispatch fits between the gap-analyst's reconciliation step (where features/strategies get confirmed/revised/proposed from code-is-truth analysis) and the approach synthesis step.

- [ ] **Step 1: Locate the gap-analyst → emit boundary**

Run: `cd /Users/chetan/projects/locutus && grep -nE "gap-analyst|Step 5|Step 6|reconcil|emit" internal/scaffold/plans/code_assimilation.md | head -20`

- [ ] **Step 2: Add the dispatch step**

Use the same prose template as Task 2, with one assimilate-specific adjustment: the deliverable shape is inferred by the analyzers' classification of the source code (backend / frontend / infra components) rather than by the architect (assimilate doesn't run the architect — the gap-analyst plays a similar role). Frame the critic dispatch as running over the gap-analyst's reconciled feature set against the inferred deliverable shape.

- [ ] **Step 3: Verify + commit**

```bash
git add internal/scaffold/plans/code_assimilation.md
git commit -m "feat(plans): dispatch spec-coverage-critic during assimilate (DJ-150)"
```

---

### Task 5: Integrate the critic into `internal/scaffold/plans/code_assimilation.claude-code.md`

**Files:**
- Modify: `internal/scaffold/plans/code_assimilation.claude-code.md`

Same workflow-form mirror as Task 3, applied to the assimilate CC overlay.

- [ ] **Step 1: Locate the workflow's gap-analyst → emit boundary**

- [ ] **Step 2: Add the workflow-form dispatch**

Use the same shape as Task 3.

- [ ] **Step 3: Verify + commit**

```bash
git add internal/scaffold/plans/code_assimilation.claude-code.md
git commit -m "feat(plans): coverage-critic parallel fan-out in assimilate CC overlay (DJ-150)"
```

---

## Phase 3 — Registry

### Task 6: Register `spec-coverage-critic` in `internal/activity/agents-default.yaml`

**Files:**
- Modify: `internal/activity/agents-default.yaml`

- [ ] **Step 1: Inspect existing entries**

Run: `cd /Users/chetan/projects/locutus && grep -nE "spec-candidate-survey|spec-architect|^[a-z-]+:" internal/activity/agents-default.yaml | head -30`

Identify the entry shape used for sibling critic/survey agents.

- [ ] **Step 2: Add the entry**

Match the sibling agent shape. Typical fields are agent id, role, the activities it participates in (refine + assimilate), and any tier/grounding hints if the registry tracks them. The critic participates in both `spec_refinement` and `code_assimilation` activities.

- [ ] **Step 3: Verify**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/activity/... -count=1 2>&1 | tail -5`
Expected: all pass; the registry-validation test (if any) accepts the new entry.

- [ ] **Step 4: Commit**

```bash
git add internal/activity/agents-default.yaml
git commit -m "feat(activity): register spec-coverage-critic for refine + assimilate (DJ-150)"
```

---

## Phase 4 — Documentation

### Task 7: Update `docs/council.md` with the critic reference

**Files:**
- Modify: `docs/council.md`

- [ ] **Step 1: Identify the insertion point**

Run: `cd /Users/chetan/projects/locutus && grep -nE "spec-architect|spec-candidate-survey|critic|elaborator" docs/council.md | head -20`

Find the section listing per-agent reference entries.

- [ ] **Step 2: Add the critic entry**

Add a paragraph describing `spec-coverage-critic`'s role, input, output, and dispatch position in the refine/assimilate pipelines. Reference DJ-150 for the rationale. Keep it brief — the agent's prompt and DJ-150 carry the long form; council.md is the cross-agent reference.

If the council document includes a Mermaid workflow diagram, update it to show the critic dispatch between the architect pass and the elaborator fan-out.

- [ ] **Step 3: Commit**

```bash
git add docs/council.md
git commit -m "docs(council): document spec-coverage-critic in the spec-generation reference (DJ-150)"
```

---

### Task 8: Codify the topology-vs-process bias check in `docs/agent-conventions.md`

**Files:**
- Modify: `docs/agent-conventions.md`

This task codifies DJ-150 §8 as a project-wide convention.

- [ ] **Step 1: Identify the insertion point**

Run: `cd /Users/chetan/projects/locutus && grep -nE "^## |^### " docs/agent-conventions.md | head -20`

Find a natural section break — likely at the end of the existing anti-pattern enumeration, or in a "broader project conventions" section if one exists.

- [ ] **Step 2: Add the bias-check section**

Add a new section. Use this prose as the template:

```markdown
## Topology vs process: when to add a new node kind

Before introducing a new node kind in the spec graph, apply this test: does the proposed kind correspond to an artifact independently recognized in PM/engineering practice (architecture decision records, feature briefs, technical design documents, goal trees)? If yes, the node kind has a real-world counterpart and the addition is justified. If no — if the proposal exists solely because an AI agent fails to do something a human would handle by skill — the fix is in *process*, not topology: better prompts, completeness critics, grounded enumeration at runtime, explicit playbook prose.

This convention emerged from [DJ-150](decisions/dj-150-spec-coverage-critic.md) when "deliverable obligations" was almost promoted to a new graph node kind before recognizing that obligations are not an artifact in standard practice; the cleaner fix was a runtime critic that surfaces uncovered obligations as findings, with the architect addressing them by extending existing features or proposing new ones (features are the recognized PM artifact).

The bias is structural: structural fixes feel durable while process fixes feel softer. Don't trust that feeling. Accumulated graph topology that exists only to correct AI failure modes becomes complexity future contributors cannot justify. Reserve graph topology for what the project would need even without AI involvement.

Counter-test before proposing a node kind: would a human team need this artifact even without AI involvement? If yes, the node kind is justified. If no, find the process fix.
```

- [ ] **Step 3: Commit**

```bash
git add docs/agent-conventions.md
git commit -m "docs(conventions): codify topology-vs-process bias check (DJ-150)"
```

---

### Task 9: Add the DJ-150 bullet to `CLAUDE.md`

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Identify the insertion point**

Run: `cd /Users/chetan/projects/locutus && grep -nE "DJ-149|DJ-148" CLAUDE.md | head -5`

The DJ-150 bullet goes immediately before the DJ-149 bullet (the bullets are reverse-chronological under "Sources of Truth").

- [ ] **Step 2: Author the bullet**

Add a bullet matching the existing format (long descriptive sentence, links to the DJ doc, mentions the key contributions and the topology-vs-process discipline). Use this template:

```markdown
- **Deliverable-shape obligations as coverage-critic findings (DJ-150).** `spec-coverage-critic` runs at refine-time per identified deliverable shape, enumerates category obligations via grounded web search (no canned examples, no registry), and judges coverage by reading existing feature bodies in natural language. Uncovered obligations become findings; the architect's revise pass either extends an existing feature's scope or proposes a new feature to close the gap. No new graph node kinds, no `covers` citation slice on features, no persistent obligation registry — outcomes drive feature mutations via existing `spec_propose_feature` / `spec_revise_feature` tools. Triggered by winplan's empty home-page surface after the first end-to-end adopt run (`next build` succeeded, 810 tests passed, `app/page.tsx` was a 9-line placeholder because no feature owned the public entry surface). Codifies the **topology-vs-process bias check**: before any future DJ proposes a new node kind, the proposed kind must correspond to a recognized PM/engineering artifact (ADRs, features, design docs, goal trees); otherwise the fix lives in prompts / critics / playbook prose. See [DJ-150](docs/decisions/dj-150-spec-coverage-critic.md).
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(CLAUDE.md): add DJ-150 bullet + topology-vs-process discipline"
```

---

## Phase 5 — Validation

### Task 10: Full suite + scaffold verification

- [ ] **Step 1: Full build + race-clean suite**

Run:
```
cd /Users/chetan/projects/locutus && go build ./... && go vet ./... && go test ./... -race -count=1 2>&1 | grep -vE '^ok|no test files' | tail -10
```
Expected: empty output (all pass).

- [ ] **Step 2: Verify the bijection holds (DJ-150 row in manifest matches dj-150-*.md filename)**

Run: `cd /Users/chetan/projects/locutus && go test ./internal/docs/ -count=1 2>&1 | tail -5`
Expected: pass (the manifest test enforces the bijection between DECISION_JOURNAL.md rows and on-disk files).

- [ ] **Step 3: Rebuild binary**

```bash
cd /Users/chetan/projects/locutus && go build -o locutus . && ls -la locutus | awk '{print $5, $9}'
```

### Task 11: Operator handoff — winplan re-refine validation

E2E validation requires a real Claude Code subprocess (operator's subscription). Surface as operator tasks:

```bash
# 1. Dry-run a refine against winplan and look at the report.
( cd /Users/chetan/projects/winplan && ./locutus refine goals --dry-run --format json | jq '.captured[] | select(.tool == "spec_revise_feature" or .tool == "spec_propose_feature")' )

# Expectation: the coverage critic surfaces obligations for the hosted-code-with-users shape including (at minimum) something like "public entry surface"; the elaborator's revise pass either extends an existing feature OR proposes a new one (e.g., feat-landing or feat-public-surface).

# 2. If dry-run looks good, full refine.
( cd /Users/chetan/projects/winplan && ./locutus refine goals )

# Expectation: a new feature lands in the spec graph; cascade marks dependent approaches drifted via SpecHashes; next `locutus adopt` will regenerate / synthesize the affected approach(es).

# 3. Then adopt to materialize the feature into code.
( cd /Users/chetan/projects/winplan && ./locutus adopt )

# Expectation: adopt picks up the newly-proposed feature (via synthesize_approach if no approach exists yet) OR the regenerate path (if a refine pass also revised dependent approaches); rendered home page at / is no longer the 9-line placeholder.
```

The operator validates by re-rendering the winplan home page after the adopt run.

---

## Self-Review

- **Spec coverage:** DJ-150 §1 (new agent) → Task 1. §2 (no `covers` citation slice) → enforced by NOT adding one in any task. §3 (findings are session-state) → embedded in playbook prose at Tasks 2-5. §4 (refine playbook integration) → Tasks 2-5. §5 (drift cascade unchanged) → no task; the existing DJ-138 cascade handles feature mutations naturally. §6 (stochasticity bounded by grounding) → embedded in the agent prompt at Task 1. §7 (multi-deliverable dispatch) → embedded in playbook prose at Tasks 2-3. §8 (topology-vs-process test as convention) → Task 8. All 8 decision points have implementing tasks.

- **Placeholder scan:** No TBD/TODO. One judgement point at execution time: the exact wording of the agent prompt body in Task 1 (the implementer reads agent-conventions, walks the checklist, applies positive phrasing — the template prose is a starting point, not a fixed string).

- **Type consistency:** `CoverageReport` shape consistent across the agent prompt, the playbook prose, and the DJ doc. `spec-coverage-critic` agent id consistent throughout. `Task` tool used for dispatch consistently (not Workflow — that's a different tool, runtime-specific).

- **No code changes required.** Phase 1-5 touch markdown, YAML, and docs only. The publisher (`internal/publisher/`) walks `internal/scaffold/agents/` and auto-emits per-runtime copies on `locutus init` / `locutus update --reset`; the new agent reaches each runtime's directory without any registration step in Go code.

- **Known calibrations at execution time:** Task 6's registry entry shape depends on the existing schema in `agents-default.yaml`; the implementer reads sibling entries to find the right format. Task 7's council.md insertion point depends on the document's current structure; if the Mermaid diagram needs updating, the implementer makes that judgment after reading the current state.
