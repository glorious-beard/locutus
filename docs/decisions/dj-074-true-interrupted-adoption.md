## DJ-074: True `--resume` for Interrupted Adoption

**Status:** shipped (2026-04-25); refined by DJ-120 then DJ-121

**Refined by [DJ-120](dj-120-adopt-resume-narrows-step-level.md) (2026-05) and [DJ-121](dj-121-adoption.md) (2026-05):** the resume-grain promise has narrowed twice. DJ-074 (below) committed two layers — *step-level* resume (worktree rebuilt from feature branch, completed PlanSteps skipped) and *conversation-level* resume (`--resume <AgentSessionID>` on the coding-agent CLI). DJ-120 dropped conversation-level resume when the ACP lifecycle (DJ-119) replaced the driver model — the agent subprocess no longer survives a Locutus restart, so there's no conversation to revive. DJ-121 dropped step-level resume on the Locutus side when `PlanStep` was removed from the spec model; step continuity is now preserved at the *agent's* level via a worktree-resident `_locutus/checklist.md`. The feature-branch durability guarantee below — completed workstreams' work persists on `locutus/<ws-id>` — is unchanged. Read DJ-121 for the current resume contract.

The current `adopt` invalidates any leftover plan subdirectory from `.locutus/workstreams/` and replans from scratch, even when nothing has drifted. DJ-073's resume-path contract explicitly specifies per-session resume ("Restart the coding agent with `--resume <AgentSessionID>`, skipping PlanSteps already marked complete") but landing that cleanly requires two pieces of plumbing the Phase C MVP skipped. This DJ captures the design so future work can execute it without re-deriving the shape.

**Decision:** Implement true resume for the DJ-073 "no drift detected" branch with the following components:

1. **Session-ID capture at dispatch.** `dispatch.WorkstreamResult` gains an `AgentSessionID string` field; the supervisor already observes session IDs in the streaming event feed (cf. `internal/dispatch/streaming.go::attemptResult.sessionID`) and just needs to surface the final one. `cmd/adopt.go` then writes `AgentSessionID` onto the `ActiveWorkstream` record so it persists alongside `StepStatus`.

2. **Skip-to-step mode in the dispatcher.** `runWorkstream` currently iterates `ws.Steps` from index 0 unconditionally. Add an input shape — either a `resumeFrom` parameter or an overload — that accepts the step ID (or index) to start from and a session ID to pass through to the driver. The worktree must be derived from the existing `locutus/<ws-id>` feature branch so the already-completed steps' merged work forms the starting state, not a fresh `main`.

3. **Driver `--resume` support.** `StreamingDriver.BuildCommand` gains a `SessionID string` field on its request struct; `ClaudeCodeDriver` translates it to `--resume <id>` on the `claude -p` invocation, and `CodexDriver` to `codex exec --session <id>`. Drivers without `--resume` capability reject with a clear error — the caller then falls back to invalidate-and-replan.

4. **`adopt` resume branch fleshed out.** Replace the current `resumeOrInvalidateActivePlans` (which always wipes) with a classifier-driven dispatcher:
   - **All covered Approaches unchanged:** for each `ActiveWorkstream`, find the first `StepProgress` whose Status is not `complete`. Dispatch with `resumeFrom=<step-id>` and `AgentSessionID=<rec.AgentSessionID>`. Steps already complete are skipped.
   - **Any covered Approach drifted:** invalidate as today.
   - **All covered Approaches live:** archive (`DeletePlan`) as today.

5. **User flag shape.** Default behaviour becomes *auto-resume when possible*, matching DJ-073's spec. An explicit `adopt --discard-in-flight` flag forces invalidation for the "I know this plan is wrong, start over" case. No `--resume <session-id>` argument — the ID is always read from persisted state, per the user's observation that a human-supplied session ID is an anti-pattern (the record is the source of truth).

**Why separated from DJ-073:** DJ-073's Phase C MVP shipped correct persistence and correct invalidate-and-replan. Shipping a half-finished resume (session-id captured but dispatcher can't skip steps; or skip-to-step works but no session-id reuse so a fresh conversation restarts and re-does prior agent work) would burn tokens and create spurious file churn. The clean increment is either *all three* plumbing pieces (capture + skip + driver flag) or none. DJ-074 gates the feature on that.

**Discovery in the meantime:** `locutus status --in-flight` lists every leftover plan with its `AgentSessionID`, per-workstream step progress, and next-pending step. That's enough to decide whether a run should be resumed (nothing drifted, sessions valid) or discarded (new spec coming, sessions stale) before invoking `adopt`.

**Alternatives considered:**

- **Fresh-session replay.** Skip driver `--resume` support; use a new session each resume, but still skip already-complete steps. Rejected: the agent loses conversation context from the original session, and the MVP can't reliably model "this step is already done" to a fresh agent without re-writing prompts. Partial credit for the tokens saved on skipped steps, but the agent-context loss dominates.
- **Prompt-driven checkpoint.** Rather than driver `--resume`, serialize the conversation state into the Approach body and re-inject it on replay. Rejected as brittle — the conversation state is the agent's private model; trying to externalize it via prose reliably has failed in practice (see DJ-025's "council rounds are the conversation" note).
- **Interactive prompt on leftover detection.** When `adopt` detects a leftover plan, stop and ask the user `[r]esume, [d]iscard?`. Rejected: `adopt` must remain scriptable. Flags (`--discard-in-flight`) carry the same signal without blocking automation.

**Dependencies & next steps:** Implementing DJ-074 touches `internal/dispatch/supervisor.go` (session-id surfacing), `internal/dispatch/dispatcher.go` (resume-from-step mode), `internal/dispatch/drivers/*` (driver flag), and `cmd/adopt.go` (branching replace). Estimated one focused session if the driver flag work is scoped to Claude Code first and Codex lands as a follow-up.

**Implementation (Round 7, 2026-04-25):** landed in three commits.

- **Phase A** (`c20604e`, dispatch layer): `StepOutcome.SessionID`, `WorkstreamResult.AgentSessionID`, `ResumePoint{StepID, SessionID}`, `Supervisor.SuperviseFrom` (sibling that pre-seeds sessionID), `runWorkstream` accepts `*ResumePoint` (skip-to-step + worktree-from-base via `CreateWorktreeFromBase`), `workstreamHasStep` validates the step ID before any side effects.
- **Phase B** (`672b33f`, plumbing): `DispatchFunc` and `Dispatcher.Dispatch` signatures grew `resume map[string]*dispatch.ResumePoint`; `AdoptCmd.DiscardInFlight` + `--discard-in-flight` CLI flag; `recordStepProgress` persists `AgentSessionID` on `ActiveWorkstream`.
- **Phase C** (this commit, policy): `classifyActivePlans` does drift-aware classification — for each leftover plan, walks records, computes current `ComputeSpecHash` for each covered Approach and compares against the persisted state's `SpecHash`. Verdicts: any drift → invalidate; all live → archive; otherwise resume. `RunAdoptWithConfig` now short-circuits to a new `runAdoptDispatchAndVerify` helper when `PlanToResume` is non-nil — the planner is **not** invoked, the persisted plan is used directly, pre-flight is skipped (it ran on the prior invocation). `buildResumePoint` walks each ActiveWorkstream's `StepStatus` and points `ResumePoint.StepID` at the first non-`StepComplete` step with the workstream's persisted `AgentSessionID`. At most one resumable plan per invocation; multiple leftover resumable plans → first wins, rest invalidate.

Driver `--resume` support landed implicitly: Claude Code's existing `BuildRetryCommand` already issues `--resume <id>`, and Phase A wired `SuperviseFrom` to call `BuildRetryCommand` on the resumed step's first attempt. Codex / Gemini support is still deferred per the original DJ scoping.

20 tests across `internal/dispatch/resume_test.go` and `cmd/adopt_integration_test.go` cover: surfacing, skip-to-step, sessionID pre-seed, unknown-step error, sessionID propagation, `--discard-in-flight` force invalidate, archive-when-all-live, invalidate-on-drift, resume-when-clean (planner not called, dispatch sees correct ResumeMap).
