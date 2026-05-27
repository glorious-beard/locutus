## DJ-071: Pre-Flight Clarification Protocol — Coding Agent Ambiguity Resolution Before Implementation

**Status:** shipped

**Decision:** Introduce a `pre_flight` phase in the workstream execution lifecycle, sitting between `planned` and `in_progress`. During pre-flight, Locutus presents the Approach Body and PlanSteps to the coding agent in a constrained "clarify only" mode (no implementation). The agent returns a list of ambiguities. Locutus resolves each by consulting the spec graph or making an explicit assumption. All assumptions are recorded as new Decision nodes, which cascade through the spec graph exactly as any other Decision revision would (parent Feature/Strategy rewrites its present-tense statement; dependent Approaches are marked `drifted`). Once all ambiguities are resolved or the round limit is reached, the Approach transitions to `in_progress` and implementation begins.

**Why emulate rather than delegate to native agent planning:**
No coding agent (Claude Code, OpenAI Codex, Gemini Code, or others) exposes a programmatic planning API that Locutus can participate in. Claude Code has `--plan` mode but only interactively; there is no subprocess hook. Emulating the planning phase at the Locutus level is therefore required for consistency across all agents — and it is superior to native agent planning because the outputs (resolved ambiguities, captured assumptions) become durable spec graph artifacts rather than ephemeral internal agent state.

**Updated reconciliation lifecycle:**

```text
unplanned → planned → pre_flight → in_progress → live / failed
                                                        ↓
                                             drifted / out_of_spec → planned
```

- `pre_flight` — Workstream presented to coding agent; agent returns ambiguities; Locutus resolves and records assumptions as Decisions; bounded to a configurable maximum number of rounds (default: 3)

- If the round limit is reached with unresolved ambiguities, the remaining ambiguities are recorded as `assumed`-status Decisions (best-effort assumption) and execution proceeds

**Protocol detail:**

1. Locutus presents: Approach Body, PlanStep descriptions, relevant file context
2. Coding agent responds with: a structured list of questions or ambiguities (not code)
3. For each question, Locutus:
   a. Checks if the answer exists in the spec graph (Feature acceptance criteria, Decision rationale, Strategy constraints)
   b. If yes: returns the answer with a reference to the spec node
   c. If no: generates an assumption, creates a new Decision node (`status: assumed`, `confidence < 1.0`), cascades through spec graph, returns the assumption as the answer
4. Updated context (with resolved ambiguities) is appended to the Approach Body before handing off to the coding agent for implementation
5. The Approach's `UpdatedAt` timestamp is bumped; `spec_hash` in the state store is recomputed

**Why capture assumptions as Decisions rather than inline answers:**
Inline answers live in the agent session and disappear after execution. A Decision node persists, participates in the spec graph, can be revisited, and will cascade to all other Approaches that depend on the same parent if it's later revised. This is the mechanism that keeps the spec honest over successive workstream executions — the spec graph accumulates the team's actual decisions, not just the ones made at planning time.

**Relationship to existing escalation cascade:**
Pre-flight is distinct from the existing `RefineStep → ExplicitGuide → Replan → UserInput → Abort` escalation cascade, which handles failures *during* implementation. Pre-flight runs *before* implementation and cannot fail in the same way — unresolved questions are assumed, not escalated. If an assumption later proves wrong, `out_of_spec` drift surfaces it for correction.

**Impact on spec types:**
- `ReconcileStatus` gains `pre_flight` as a new status between `planned` and `in_progress`
- No other type changes required; new Decisions created during pre-flight follow the slug ID scheme (DJ-070) and the standard Decision lifecycle (DJ-069)

---

Session date: 2026-04-21
