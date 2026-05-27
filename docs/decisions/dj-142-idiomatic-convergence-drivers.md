## DJ-142: Consistent Convergence *Outcome*, Idiomatic Convergence *Driver* Per Context — Completes [DJ-140](dj-140-headless-convergence-unification.md) by Filling the Empty Tier-3 `spec_refinement.interactive.md` Slot So Interactive Codex/Gemini Self-Loop to Convergence Instead of Running One-Shot; Adds Daemon-Side Loop-State MCP Tools (`spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration`) Scoped by a Server-Issued `run_id` for Deterministic Iteration-Count + Cap Enforcement While the Scout Still Owns the Convergence *Judgment*; Headless Stays Harness-Driven and Interactive-Claude-Code Stays `/goal`-Driven — Three Idiomatic Drivers, One Outcome

**Status:** design (designed 2026-05-27 in chat; builds on [DJ-140](dj-140-headless-convergence-unification.md), does not supersede it; implementation plan at [.claude/plans/dj-142-idiomatic-convergence-drivers.md](../.claude/plans/dj-142-idiomatic-convergence-drivers.md)).

**Context.** [DJ-140](dj-140-headless-convergence-unification.md) fixed the headless `/goal`-unavailable bug by unifying headless dispatch on the harness `OuterLoopRunner` and adding the `mode` (interactive | headless) axis to `ResolvePlaybook` (`<activity>[.<provider>][.<mode>].md`, four tiers). But DJ-140 left tier-3 (`<activity>.<mode>.md`, i.e. `spec_refinement.interactive.md`) **empty**. Consequence: interactive `/locutus-refine` on Codex/Gemini resolved through to the tier-4 one-iteration default and ran **one-shot** — no convergence loop. Claude Code interactive got `/goal` (tier-1 `spec_refinement.claude-code.interactive.md`); headless got the harness loop; but interactive non-Claude runtimes got a single pass with the operator left to re-invoke manually.

The framing that resolves this (chat 2026-05-27): **the convergence *outcome* must be consistent across every context — drive the spec graph to a fixed point, or stop at `max_iterations` — but the *driver* should be idiomatic to each context.** The `mode × runtime` resolution machinery DJ-140 built exists precisely to select the right driver per context; the gap was a missing playbook file, not a missing mechanism. An earlier design attempt in the same conversation over-corrected by homogenizing the *driver* into one agent-self-loop everywhere (discarding `/goal` and the harness loop for mechanism-uniformity) — that is lowest-common-denominator design and was rejected per [[feedback-runtime-idiomatic-no-lcd]]. DJ-142 keeps each idiomatic driver and only fills the missing one.

The loop-state tools require a notion of *which run's loop* they are tracking, because the Locutus MCP daemon is a per-project singleton shared across multiple connected coding agents (DJ-135). Loop state (iteration counter, recorded verdict) must be namespaced per run so concurrent or sequential runs don't clobber each other's counters — hence a server-issued `run_id`.

**Decision.** Fill tier-3 with an interactive self-looping playbook for non-Claude runtimes, backed by deterministic daemon-side loop-state MCP tools. Keep DJ-140's headless and interactive-Claude paths unchanged.

1. **Three idiomatic drivers, one outcome.** Selected via DJ-140's existing `ResolvePlaybook(mode, runtime)` resolution + the publisher's per-runtime interactive resolution:

    | Context | Resolved playbook | Loop driver | Convergence judgment | Cap enforcement |
    |---|---|---|---|---|
    | Headless (all runtimes) | `spec_refinement.md` (tier 4, single-iteration) | Locutus `OuterLoopRunner` (re-dispatch per iteration) | scout verdict line | harness counter (DJ-138 `max_iterations`) |
    | Interactive · Claude Code | `spec_refinement.claude-code.interactive.md` (tier 1, single-iteration + `/goal`) | Claude Code `/goal` evaluator | scout verdict in transcript | `/goal` condition (cap referenced in condition) |
    | Interactive · Codex / Gemini | **`spec_refinement.interactive.md` (tier 3, loop defined in playbook)** — *new* | the coding agent itself, in one session | scout verdict reported to `spec_advance_iteration` | daemon counter via loop-state tools |

    Every row reaches the same outcome: converge (scout reports `converged: true`) or stop at `max_iterations`. DJ-140's first two rows are unchanged; DJ-142 adds the third.

2. **New tier-3 playbook `internal/scaffold/plans/spec_refinement.interactive.md`.** A multi-iteration self-looping variant: the orchestrator calls `spec_loop_begin` once to get a `run_id` and the cap, then loops — each pass does one iteration's work (scout → decision-elaborator fanout → revisions → cite, the same per-iteration body as `spec_refinement.md`), reports the scout's verdict via `spec_advance_iteration`, and continues while the tool returns `{continue: true}`. Resolves for any interactive runtime without its own tier-1/tier-2 overlay — today Codex and Gemini. Claude Code never reaches it (its tier-1 `/goal` wrapper wins); headless never reaches it (`mode=headless` skips the mode tiers). The per-iteration *work* is shared with `spec_refinement.md` to avoid drift — see resolved-question 4.

3. **New daemon-side loop-state MCP tools.** Registered in `internal/mcp/`, backed by per-run state held in the daemon (not the SpecStore — loop state is run-scoped ephemeral bookkeeping, not spec graph content). The tools key on a **loop identity** whose exact form is pending the spike in resolved-question 5 (server-resolved-from-session, agent-ambient-session-id, or recoverable server token) — shown below as `<loop-identity>`:
    - `spec_loop_begin {activity}` → `{<loop-identity>, iteration: 0, max_iterations}`. Allocates (or, if the identity already has a live record, recovers) a loop-state record; `max_iterations` is read from the activity registry (DJ-138), so per-project `.borg/agents.yaml` overrides apply uniformly across all three drivers.
    - `spec_loop_status {<loop-identity>}` → `{iteration, max_iterations, converged, last_verdict}`. Read-only inspection.
    - `spec_advance_iteration {<loop-identity>, converged: bool, reason?}` → records the verdict, increments the counter, returns `{continue: bool, iteration, reason}`. `continue` is `false` when `converged == true` OR `iteration >= max_iterations` — the deterministic part. The agent calls this at the end of each pass and honors the `continue` result.

    **Division of labor mirrors the harness exactly:** the *iteration count and cap* are server-tracked and deterministic (no agent self-counting — the original guardrail concern); the *convergence judgment* is the scout's LLM verdict, same as the harness reads from the verdict line headless. The tools don't judge convergence; they record it and enforce the cap.

4. **The loop identity is per-invocation and compression-robust (the "session ID notion").** It must be unique per `/locutus-refine` invocation (so a fresh run starts at iteration 1, not resuming a stale converged loop) AND survive context compression mid-run (so a long self-loop doesn't lose the handle). It is distinct from the `.locutus/sessions/<…>/<sid>/` directory id (a dispatch artifact). Per-project daemon singleton means multiple agents — or sequential refine-then-import in one interactive session — hold independent loop records without collision. Records are in-memory in the daemon, GC'd when the loop terminates (converged or capped) or on a TTL. The exact identity mechanism is resolved-question 5's spike.

5. **Headless and interactive-Claude unchanged.** The loop-state tools are exercised *only* by the tier-3 self-loop playbook. Headless keeps the harness `OuterLoopRunner` + verdict-line regex (DJ-140); interactive-Claude keeps `/goal` (DJ-140). No turn-end backstop is added for the interactive self-loop: in interactive mode the operator is present and re-invokes if the agent ends early — acceptable, and the deterministic cap/iteration tools make premature self-termination the only residual risk (the operator's to notice), not a counting or cap failure.

**Resolved design questions** (chat 2026-05-27):

1. **Consistency is on outcome, not driver.** Converge-or-cap is uniform; the driver is idiomatic per context. Rejected: one uniform driver everywhere (homogenizing to an agent-self-loop + harness backstop across all modes) — LCD, discards `/goal` and the harness loop where they're the better idiom, per [[feedback-runtime-idiomatic-no-lcd]].

2. **Fill tier-3 rather than special-case in code.** The gap is a missing playbook file; the resolution machinery already selects it. No new branching in the dispatch or publish paths — `spec_refinement.interactive.md` simply exists and tier-3 resolution finds it for non-Claude interactive runtimes.

3. **Loop-state tools track iteration + cap deterministically; scout owns convergence judgment.** Mirrors the harness split exactly, so the three drivers agree on *what convergence means* (the scout's verdict) and differ only in *who re-triggers the next pass*.

4. **The per-iteration work is duplicated between `spec_refinement.md` and `spec_refinement.interactive.md`, guarded by a drift test (chat 2026-05-27).** The interactive variant copies the per-iteration step sequence and adds the self-loop scaffolding around it; a test asserts the per-iteration core matches the canonical playbook so the two can't silently diverge. Rejected for now: a shared include/fragment the build concatenates, and a thin-wrapper-delegates form — both add loader complexity before we know whether duplicate-editing is actually painful. Revisit the loader-composition approach if the two playbooks start requiring frequent paired edits. (Considered and deferred, not the primary risk it first appeared.)

5. **The loop identity must survive context compression — exact mechanism pending a spike (chat 2026-05-27).** The deciding criterion is *what the agent is least likely to lose track of mid-run*, because an interactive self-loop can undergo context compression between iterations. An opaque server-issued token the agent must remember is the **worst** option (lost on compression). Candidates, ranked by compression-robustness:
    - **(c, preferred) Server resolves the loop from a per-invocation session identity the MCP layer already sees** — the agent passes nothing; nothing to lose. Viable only if the daemon can observe a distinct MCP session per `/locutus-refine` invocation through the `locutus mcp` socket bridge. **Spike this first.**
    - **(b) The coding agent's own ambient runtime session id** — unique per invocation and re-readable (not "remembered"), so robust to compression *if* the runtime exposes it to the agent. Runtime-dependent; verify per Codex/Gemini.
    - **(a, fallback) Server-issued token via `spec_loop_begin`, made recoverable** — `spec_loop_begin` is idempotent per invocation-identity so a post-compression re-call returns the live record (current iteration, not a reset). Only acceptable with the recovery property; a bare non-recoverable token is rejected.
    The fresh-run-vs-recovery distinction (a new `/locutus-refine` must start at iteration 1, not resume a stale converged loop) is why a pure `(activity, target)` key is insufficient — it can't tell a fresh invocation from a compression-recovery. So the identity must be per-invocation. The spike decides between (c)/(b)/(a); the tool signatures in Decision point 3 are written against an abstract "loop identity" until then.

6. **Loop-state lives in the daemon, not the SpecStore.** It's run-scoped ephemeral bookkeeping, not spec graph content — it must not persist to `.borg/spec/` or appear in the manifest. Rejected: modeling loop state as a spec node (pollutes the graph with transient process state).

7. **Builds on DJ-140, does not supersede it.** DJ-140's headless-harness + interactive-Claude-`/goal` decisions stand; DJ-142 completes the matrix. No DJ-140 status change beyond a forward-pointer.

**Alternatives considered:**

- **Leave interactive Codex/Gemini one-shot; tell operators to use the headless CLI for convergence.** Rejected — it's the status quo DJ-140 left, and it's genuinely weak DX on non-Claude runtimes, conflicting with the runtime-idiomatic stance. The headless CLI *is* a valid convergence path, but an interactive operator on Codex shouldn't have to drop to the CLI to converge.

- **One uniform agent-self-loop driver across all modes/runtimes (+ harness backstop headless).** Rejected — discards `/goal` (the right idiom interactively on Claude Code) and the harness loop (the robust idiom headless) purely for mechanism-uniformity. LCD. The outcome-consistency goal doesn't require driver-uniformity.

- **Encode the loop-driver choice in playbook frontmatter or the activity registry.** Rejected (carried from DJ-140's analysis) — the dispatch/publish paths already select the playbook by `mode × runtime`; the driver follows from which playbook resolves. No config field needed.

- **Track loop state by MCP connection instead of an explicit `run_id`.** Rejected — conflates sequential loops in one session (resolved-question 5).

- **Persist loop state in the SpecStore.** Rejected — transient process bookkeeping doesn't belong in the spec graph (resolved-question 6).

- **Add the loop-state tools to the headless path too (replace the verdict-line regex with MCP loop-state for inspectability).** Deferred, not rejected — a real inspectability improvement, but out of scope for DJ-142 which fills the interactive gap. Tee'd up as Future Work; would make all three drivers share the same loop-state record.

**Consequences:**

- **Code (add):**
    - `internal/scaffold/plans/spec_refinement.interactive.md` — new tier-3 self-looping playbook. Must emit `converged:` semantics via `spec_advance_iteration`, not the verdict line (the harness isn't reading it here). Walk [docs/agent-conventions.md](../agent-conventions.md) before authoring ([[feedback-agent-conventions-checklist-first]]).
    - `internal/mcp/tools_loop.go` (or similar) — `spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration` registrations + handlers; descriptions per the registration-not-prompts convention (DJ-134).
    - Daemon-side loop-state store keyed by `run_id` (in-memory; GC on terminate/TTL). Lives alongside the per-project daemon singleton (`internal/mcp/` or `internal/agent/`).
    - Tests: loop-state lifecycle (begin → status → advance → continue:false on converge and on cap); `run_id` isolation (two concurrent runs don't collide); `max_iterations` sourced from the registry incl. `.borg/agents.yaml` override; resolution test that interactive Codex/Gemini resolve tier-3 while headless resolves tier-4 and interactive Claude resolves tier-1; the per-iteration-core drift guard (resolved-question 4).

- **Code (modify):**
    - Publisher — already resolves `mode=interactive` per runtime (DJ-140); with tier-3 populated it will publish the self-loop body for Codex/Gemini and the `/goal` wrapper for Claude Code automatically. Verify, add a publisher test asserting Codex/Gemini interactive commands now contain the loop protocol (reference `spec_loop_begin`) rather than the one-shot body.
    - `internal/runner/` — no change to the headless loop; the loop-state tools are orthogonal to `OuterLoopRunner`.

- **User-visible:**
    - Interactive `/locutus-refine` on Codex/Gemini now self-loops to convergence (or cap) instead of one pass. Interactive parity-of-outcome across all three runtimes.
    - No CLI surface change; headless and interactive-Claude behavior unchanged.

- **Documentation:**
    - [CLAUDE.md](../../CLAUDE.md) — extend the DJ-140 convergence bullet to the three-driver matrix.
    - [docs/runtime-affordances.md](../runtime-affordances.md) — document the tier-3 interactive self-loop + loop-state tools.
    - [docs/mcp.md](../mcp.md) — document `spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration` + the `run_id` scoping.
    - **[docs/council.md](../council.md) update required** ([[feedback-council-doc-maintenance]]): DJ-140 revised the convergence visualization to a single harness loop; DJ-142 adds the agent-self-loop driver for interactive non-Claude — the diagram must show all three drivers reaching the one outcome.

**Future Work:**

- **Unify the headless verdict-line read onto the loop-state tools.** Replace the `OuterLoopRunner`'s verdict-line regex with a read of the same `spec_loop_status` record (the harness would `spec_loop_begin` on behalf of the run and re-dispatch on `continue:true`). Makes all three drivers share one inspectable loop-state record; deferred to keep DJ-142 scoped.
- **Extend the interactive self-loop to the other converging activities** (`feature_ingestion`, `code_adoption`, `code_assimilation`, `spec_bias`) via their own tier-3 `<activity>.interactive.md` variants, once `spec_refinement` validates the pattern.
- **Interactive turn-end backstop.** If premature self-termination proves a real problem in interactive Codex/Gemini use, consider a runtime hook (Gemini `AfterAgent` retry/halt; Codex equivalent) reading `spec_loop_status` to re-drive — the "Stop-hook as optional accelerator" idea from DJ-140, now with concrete loop-state to read.

**Reference.** Synthesizes the 2026-05-27 conversation continuing from DJ-140: the realization that DJ-140 left tier-3 empty (interactive Codex/Gemini fell to one-shot), and the framing correction that convergence *outcome* must be uniform while the *driver* stays idiomatic per context — using the `mode × runtime` resolution DJ-140 already built. The loop-state MCP tools + `run_id` scoping were surfaced as the deterministic guardrail for the agent-self-loop driver. Builds on [DJ-140](dj-140-headless-convergence-unification.md); depends on [DJ-138](dj-138-refine-with-bias-cascade.md) `max_iterations` and [DJ-135](dj-135-multi-runtime-pivot.md) daemon singleton.
