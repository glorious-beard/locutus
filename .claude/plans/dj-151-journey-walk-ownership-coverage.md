# DJ-151 Journey-Walk Enumeration + Ownership-Test Coverage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Amend `spec-coverage-critic` with a persona journey-walk enumeration mode (co-equal with the existing grounded web-search pass, dual provenance in `CoverageReport`) and replace the prose-coverage judgment with an ownership test (covered = a feature's declared scope claims the surface + an acceptance criterion exercises it). Extend the critic's dispatch input with `acceptance_criteria` across all five playbook files that carry the dispatch prose. Update docs. No new node kinds, no new MCP tools, no registry changes, no Go behavior changes (test-file edits only).

**Architecture:** Same agent, same dispatch cadence and position (Phase 0 per deliverable shape per refine iteration; inherited assimilate step). The journey walk runs *inside* the existing single dispatch: derive personas per-run from goal layer + feature prose → walk each persona through the full lifecycle (arrival/acquisition → authenticate → orient/navigate → core loop → empty/first-run → failure states → account/workspace management → departure) → emit journey obligations with `{persona, step}` provenance → run the grounded pass unchanged → judge the merged obligation list with the ownership test. Downstream findings flow (scout convergence-blocking, elaborator revise pass, DJ-138 cascade, DJ-087 synthesis) is untouched.

**Tech Stack:** Markdown agent prompt + playbook prose under `internal/scaffold/`; Go test edits in `internal/scaffold/scaffold_test.go`; docs under `docs/` + `CLAUDE.md`. Spec doc: [docs/decisions/dj-151-journey-walk-ownership-coverage.md](../../docs/decisions/dj-151-journey-walk-ownership-coverage.md). `CoverageReport` is prompt-declared — there is no Go schema registry to touch.

---

## Required reading before starting

- **[docs/decisions/dj-151-journey-walk-ownership-coverage.md](../../docs/decisions/dj-151-journey-walk-ownership-coverage.md)** — the governing spec. Read all 7 decision points + 8 resolved questions; note especially §5 (granularity discipline extends to journey steps) and RQ4 (flat ownership test; scout's wontfix valve absorbs constraint-class false-uncovereds).
- **[docs/decisions/dj-150-spec-coverage-critic.md](../../docs/decisions/dj-150-spec-coverage-critic.md)** — the amended decision; everything DJ-151 does not name stands as DJ-150 wrote it.
- **[docs/agent-conventions.md](../../docs/agent-conventions.md)** — MANDATORY before touching the prompt. Walk the six anti-patterns + four positive patterns as a checklist. The critical line for this DJ: the eight lifecycle stages are *structural walk scaffolding* (like the existing field-walk Task structure) and are allowed; per-shape example obligations ("a web app needs a landing page") are canned-example priming and are NOT — the walk must generate them, the prompt must not enumerate them.
- **[internal/scaffold/agents/spec-coverage-critic.md](../../internal/scaffold/agents/spec-coverage-critic.md)** — current prompt. Note the existing "thoughtful person" Discipline paragraph (DJ-150 amendment) — the journey walk *replaces* it as the structural form of the same intent; do not keep both (redundant priming).
- **[internal/scaffold/scaffold_test.go](../../internal/scaffold/scaffold_test.go)** (~line 443) — the existing frontmatter/body contract test for this agent; you extend it.
- **[internal/scaffold/plans/spec_refinement.md](../../internal/scaffold/plans/spec_refinement.md)** §step 1 (~line 94) — the canonical dispatch prose; the other four playbook files mirror or reference it.
- **[docs/council.md](../../docs/council.md)** — `spec-coverage-critic` per-agent section (~line 183), workflow diagram node (~line 66), convergence-semantics paragraph (~line 303).

## Phase ordering & independence

- **Phase 1** (agent prompt + contract test) is foundational.
- **Phase 2** (five playbook files) depends on Phase 1's output shape being final.
- **Phase 3** (documentation: council.md, CLAUDE.md) depends on Phases 1–2 landing but is content-independent of Phase 2's mechanics.
- **Phase 4** (validation + operator handoff) last.
- The DJ doc, manifest row, and DJ-150 status-header amendment were written at design time (2026-07-06) and are already on disk — Phase 4 verifies the manifest bijection test passes rather than creating them.

**Commit discipline:** commit after every green test step. Conventional prefixes (`feat:` / `fix:` / `test:` / `docs:`).

---

## Phase 1 — Agent prompt

### Task 1: Rewrite `internal/scaffold/agents/spec-coverage-critic.md` for DJ-151

**Files:**
- Modify: `internal/scaffold/agents/spec-coverage-critic.md`
- Modify: `internal/scaffold/scaffold_test.go` (contract-test extension)

> **MANDATORY**: Walk `docs/agent-conventions.md` as a checklist before authoring. The known trap for this task: lifecycle-stage structure is allowed; per-shape example obligations are canned-example priming and are not.

- [ ] **Step 1: Read conventions + current prompt + contract test**

```
cat docs/agent-conventions.md
cat internal/scaffold/agents/spec-coverage-critic.md
sed -n '430,480p' internal/scaffold/scaffold_test.go
```

- [ ] **Step 2: Rewrite the prompt**

Frontmatter unchanged (`id`, `thinking: off`, `grounding: true`, `role: critic`, fast tiers, `timeout: 3m`, `output_schema: CoverageReport`). Body changes:

1. **Identity** — the critic now enumerates via two co-equal modes: the persona journey walk (tacit/lifecycle obligations, grounded in the walk) and web-search grounding (documented/regulatory obligations, grounded in citations). Name DJ-151 alongside DJ-150.
2. **Context** — features array becomes `{id, title, summary, acceptance_criteria, body_excerpt}`; note acceptance criteria are load-bearing for the coverage judgment.
3. **Task, new leading field-walk section: the journey walk.** Derive the personas this deliverable serves from the goal layer and feature prose (derived per-run; nothing persists). For each persona, walk the eight lifecycle stages — arrival/acquisition, authenticate, orient/navigate, core loop, empty/first-run states, failure states, account/workspace management, departure — and emit every stage the deliverable must support as an `ObligationEntry`. The walk starts where the deliverable shape's users actually start (a hosted web app at a URL; a mobile app at the store listing; an API at its documentation; a CLI at install). Keep stages at category level per the existing granularity discipline.
4. **`ObligationEntry` shape** — add `source: journey | grounded`; `journey_provenance: {persona, step}` required when `source: journey`; `citations[]` (minItems 1) required when `source: grounded`; exactly one provenance kind populated.
5. **Coverage-judgment threshold → ownership test.** Covered = at least one feature whose declared scope (title, summary, or acceptance criteria) claims the obligation's surface or concern as something that feature builds, AND at least one of that feature's acceptance criteria exercises it. Ambient mention, assumed-as-context prose, aspirational language never count. Keep and sharpen the existing asymmetry rationale (false uncovered is recoverable via the scout's disposition; false covered silently leaves a gap).
6. **Discipline** — the "thoughtful person" paragraph is subsumed by the journey walk; remove it rather than keeping redundant priming. Grounded-pass discipline, web-search failure modes, shape-boundary rule, and role boundary stay.

- [ ] **Step 3: Extend the contract test**

In `scaffold_test.go`'s spec-coverage-critic contract test, add body assertions locking: journey-walk mode present (assert on a stable phrase, e.g. the eight-stage list or "journey"), ownership-test threshold present (e.g. "acceptance criteria" in the judgment section), `source` field documented. Keep assertions on stable structural phrases, not full sentences.

- [ ] **Step 4: Verify**

```
cd /Users/chetan/projects/locutus && go test ./internal/scaffold/... -count=1 2>&1 | tail -5
```
Expected: all pass (including hyphenated-ids and the extended contract test).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/agents/spec-coverage-critic.md internal/scaffold/scaffold_test.go
git commit -m "feat(agents): journey-walk enumeration + ownership-test coverage in spec-coverage-critic (DJ-151)"
```

---

## Phase 2 — Playbook dispatch prose (5 files)

### Task 2: `internal/scaffold/plans/spec_refinement.md`

**Files:**
- Modify: `internal/scaffold/plans/spec_refinement.md`

- [ ] **Step 1: Locate the dispatch step**

```
grep -n "coverage" internal/scaffold/plans/spec_refinement.md
```
Step 1 (~line 94) carries the canonical dispatch prose.

- [ ] **Step 2: Update the prose**

Three edits, no structural moves: (a) features input array gains `acceptance_criteria` (`{id, title, summary, acceptance_criteria, body_excerpt}` — state that acceptance criteria feed the ownership judgment); (b) one sentence describing the two enumeration modes and dual provenance in the returned report (journey entries carry `{persona, step}`, grounded entries carry citations); (c) where the step describes coverage, reflect the ownership test in one sentence (covered requires an owning feature, not ambient mention). Downstream handling (uncovered list → scout → reconcile/cascade) unchanged — do not touch steps 6–7 beyond what already flows.

- [ ] **Step 3: Verify + commit**

```
cd /Users/chetan/projects/locutus && go test ./internal/scaffold/plans/ -count=1 2>&1 | tail -5
```

```bash
git add internal/scaffold/plans/spec_refinement.md
git commit -m "feat(plans): journey-walk + ownership-test critic dispatch prose in refine playbook (DJ-151)"
```

### Task 3: `spec_refinement.claude-code.md` + `spec_refinement.interactive.md`

**Files:**
- Modify: `internal/scaffold/plans/spec_refinement.claude-code.md`
- Modify: `internal/scaffold/plans/spec_refinement.interactive.md`

- [ ] **Step 1: Locate critic references in both overlays**

```
grep -n "coverage" internal/scaffold/plans/spec_refinement.claude-code.md internal/scaffold/plans/spec_refinement.interactive.md
```

- [ ] **Step 2: Mirror Task 2's input-shape edit in each**

Overlays that restate the dispatch input get the `acceptance_criteria` addition; overlays that reference the default playbook's section by name need no content change (verify the reference still reads correctly). Keep overlay edits minimal — the default playbook carries the long form.

- [ ] **Step 3: Verify + commit**

```
cd /Users/chetan/projects/locutus && go test ./internal/scaffold/plans/ -count=1 2>&1 | tail -5
```

```bash
git add internal/scaffold/plans/spec_refinement.claude-code.md internal/scaffold/plans/spec_refinement.interactive.md
git commit -m "feat(plans): DJ-151 critic input shape in refine CC + interactive overlays"
```

### Task 4: `code_assimilation.md` + `code_assimilation.claude-code.md`

**Files:**
- Modify: `internal/scaffold/plans/code_assimilation.md`
- Modify: `internal/scaffold/plans/code_assimilation.claude-code.md`

- [ ] **Step 1: Locate + apply the same edits as Tasks 2–3** (assimilate's critic step dispatches over the gap-analyst's reconciled feature set; same input-shape + dual-provenance + ownership sentences).

- [ ] **Step 2: Verify + commit**

```
cd /Users/chetan/projects/locutus && go test ./internal/scaffold/plans/ -count=1 2>&1 | tail -5
```

```bash
git add internal/scaffold/plans/code_assimilation.md internal/scaffold/plans/code_assimilation.claude-code.md
git commit -m "feat(plans): DJ-151 critic dispatch prose in assimilate playbooks"
```

---

## Phase 3 — Documentation

### Task 5: `docs/council.md`

**Files:**
- Modify: `docs/council.md`

- [ ] **Step 1: Update three sites**

```
grep -n "coverage" docs/council.md
```
(a) The `spec-coverage-critic` per-agent section (~line 183): two enumeration modes, ownership test, input shape with acceptance_criteria, DJ-151 reference. (b) The workflow-diagram node (~line 66): update the label only if it names enumeration mechanics. (c) The convergence-semantics paragraph (~line 303): journey-derived obligations participate identically; convergence still requires zero uncovered obligations.

- [ ] **Step 2: Commit**

```bash
git add docs/council.md
git commit -m "docs(council): DJ-151 journey-walk + ownership-test critic reference"
```

### Task 6: `CLAUDE.md`

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Extend the DJ-150 bullet**

Per DJ-151 Consequences: extend the existing DJ-150 "Sources of Truth" bullet with a DJ-151 sentence (journey-walk enumeration added, prose coverage reversed to ownership test, amends DJ-150; link the DJ doc) — one critic, one bullet.

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(CLAUDE.md): DJ-151 amendment sentence on the coverage-critic bullet"
```

---

## Phase 4 — Validation

### Task 7: Full suite + manifest bijection

- [ ] **Step 1: Full build + race-clean suite**

```
cd /Users/chetan/projects/locutus && go build ./... && go vet ./... && go test ./... -race -count=1 2>&1 | grep -vE '^ok|no test files' | tail -10
```
Expected: empty output.

- [ ] **Step 2: Manifest bijection (DJ-151 row ↔ dj-151 file, both written at design time)**

```
cd /Users/chetan/projects/locutus && go test ./internal/docs/ -count=1 2>&1 | tail -5
```
Expected: pass.

### Task 8: Operator handoff — winplan re-refine validation

E2E validation requires a real coding-agent subprocess (operator's subscription). Surface as operator tasks:

```bash
# 0. Refresh winplan's published copies from the new scaffold.
( cd /Users/chetan/projects/winplan && locutus update --offline --reset )

# 1. Dry-run refine; inspect proposed feature mutations.
( cd /Users/chetan/projects/winplan && locutus refine goals --dry-run --format json )

# Expectation: journey-derived obligations for hosted-code-with-users from the visitor/member
# walks — public entry, auth surface, navigation shell, failure/first-run states — judged
# UNCOVERED under the ownership test (winplan's features mention auth but none owns it),
# driving proposed features. Constraint-class obligations may appear and be dispositioned
# wontfix by the scout (expected valve, not a bug).

# 2. If sane: full refine, then adopt to materialize (DJ-087 synthesis path).
( cd /Users/chetan/projects/winplan && locutus refine goals && locutus adopt )

# Success criterion for the DJ's trigger: the next sufficiency audit finds an owning feature
# (and approach, and page) for entry/auth/navigation — "10 of 15 features have no UI page"
# collapses. Note: `live`-status semantics are NOT fixed by DJ-151 (deferred verification DJ).
```

---

## Self-Review

- **Spec coverage:** DJ-151 §1 (journey walk, persona derivation, no persistence) → Task 1. §2 (dual provenance) → Task 1 + prose in Tasks 2–4. §3 (ownership test) → Task 1 + prose in Tasks 2–4. §4 (input gains acceptance_criteria, five playbook files) → Tasks 2–4. §5 (granularity) → Task 1 prompt discipline. §6 (downstream unchanged) → enforced by NOT editing reconcile/cascade steps. §7 (first-run expectation) → Task 8 operator notes. All 7 decision points have implementing tasks.
- **Placeholder scan:** none. Judgment points at execution time: exact prompt phrasing (Task 1, governed by the conventions checklist) and which overlay files restate vs reference the dispatch input (Task 3 Step 2 resolves it by reading).
- **Consistency:** `source: journey | grounded`, `journey_provenance: {persona, step}`, and the eight lifecycle stages are named identically in the DJ, the prompt task, and the playbook prose tasks. Five playbook files enumerated identically in DJ §4 and Phase 2.
- **No Go behavior changes.** Only `scaffold_test.go` assertions. Publisher auto-emits the revised agent file on `locutus update --reset`; no registration changes.
