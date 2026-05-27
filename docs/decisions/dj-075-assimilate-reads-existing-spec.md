## DJ-075: Assimilate Reads Existing Spec, Writes Back Atomically

**Status:** shipped

**Decision:** `locutus assimilate` is an idempotent spec-writer, not just a spec-proposer. Before running inference, it loads the current `.borg/spec/` into an `ExistingSpec` snapshot and passes that to the scout prompt so the LLM can distinguish new nodes from updates to existing ones (matching IDs = update, new IDs = new). After the pipeline returns, the inferred nodes are persisted back to `.borg/spec/` with per-file atomicity and a top-level sentinel that surfaces crashed prior runs. Nodes without an explicit status default to the `inferred` lifecycle state.

**Why this is a DJ rather than an implementation note:**

Until 2026-04-23 the assimilation pipeline returned an `AssimilationResult` and threw it away — the command printed a count and nothing landed on disk. That made `assimilate` effectively a one-shot inference demo: running it a second time re-inferred the same spec without any awareness of what already existed, and nothing a caller could commit. Resolving that is a real architectural decision (input shape, conflict policy, crash safety, status defaults), not a mechanical fix. Capturing it here so future rounds can build on the invariants instead of re-deriving them.

**Key invariants:**

1. **`ExistingSpec` is loaded before inference.** `RunAssimilate` calls `loadExistingSpec` and attaches the snapshot to `AssimilationRequest.ExistingSpec`. The scout prompt is extended with an `## Existing spec` section listing every current Feature, Decision, Strategy, Approach, and Entity so the LLM emits updates (matching IDs) alongside new nodes rather than duplicating concepts under fresh IDs.
2. **Per-file atomic writes.** Each node persists via `specio.SavePair` / `SaveMarkdown`, which use write-temp-and-rename semantics internally. Any single file is either fully written or not touched. A crash mid-loop may leave some new files present and others absent, but never half-written content — `git status` surfaces exactly what landed, and `git restore .` reverts cleanly.
3. **Sentinel file for crash detection.** `.borg/spec/.assimilating` is written at the start of the persistence loop and removed on success. If a later run finds it present, the prior run crashed; the caller can warn before proceeding. No new verb or flag required.
4. **Conflict resolution via merge-aware LLM.** No policy baked into the persistence layer. The LLM received the existing spec and already made the merge decision; the persistence layer just writes whatever the LLM produced. Matching IDs overwrite existing files; new IDs create new ones. Hand-authored spec that the LLM correctly identifies as `active` gets preserved because the LLM emits it unchanged.
5. **`inferred` as the default status.** Features and Decisions landed via assimilate without an explicit status default to `FeatureStatusInferred` / `DecisionStatusInferred`. This distinguishes inferred-from-code from assumed-during-pre-flight (DJ-071) — both have `confidence < 1.0` and want refinement, but provenance differs: `inferred` means "this decision is implicit in existing code," `assumed` means "Locutus guessed because the spec was silent."
6. **Dry-run preserved for free.** The existing `readOnlyFS` wrapper drops every write, including the sentinel. `--dry-run` still runs inference end-to-end and reports what would land without touching disk. A minor wrinkle (`WalkInventory` type-asserted to concrete FS types) was fixed by an `Unwrap()` escape hatch on `readOnlyFS`.

**Alternatives considered:**

- **Staging to `.borg/spec/.pending/` + explicit `--commit`.** Safer (human reviews diff before promoting) but adds a verb/flag for a workflow `git` already provides. `git diff` before committing *is* the staging step. Rejected.
- **Skip-existing conflict policy.** Pre-load was dismissed initially; the fallback was "don't overwrite hand edits." Rejected once we realized the LLM can't correctly enhance an existing feature without knowing it exists — the skip policy hid the input the LLM needed to make the right call.
- **Error on conflict.** Would block re-running `assimilate` after any code change that touched an already-inferred area. Rejected as hostile to iteration.
- **`inferred` status reused from `assumed`.** Semantically subtle but real: assumed = pre-flight guess with no evidence; inferred = read from code with evidence. Keeping them separate gives human reviewers the right prompt ("verify a guess" vs. "verify I read this right").

**Impact on other DJs:**

- **DJ-014 (shipped):** brownfield self-analysis now produces committable spec, not just a console summary. No change to DJ-014 itself.
- **DJ-019 (shipped):** "Heuristic first, LLM second" still governs the inference shape; DJ-075 extends it with a persistence contract.
- **DJ-045 (shipping):** Remediation (Round 5 of the gap-closeout plan) now has a reliable dependency — the spec is on disk before the remediator runs, so cross-cutting vs. feature-specific gap attribution has a real graph to attach to.
- **DJ-068 (shipped):** `.borg/spec/` remains the authoritative manifest; nothing in DJ-075 moves state into or out of the state store.
- **DJ-069 (shipped):** Node kinds preserved; only the write-back path is new.

**Not in scope for DJ-075:**

- Entity persistence. Resolved separately in DJ-076: Entity stays as in-memory context on `AssimilationResult`, never persisted to `.borg/spec/entities/`.
- Deleting spec nodes that no longer match the code. Reverse drift ("feature is in spec, code has removed it") is a different problem — belongs in `adopt`'s `out_of_spec` surfacing, not in `assimilate`.
- Remediation of detected gaps (that's Round 5 of the gap-closeout plan, governed by DJ-045).
