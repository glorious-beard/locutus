## DJ-087: Approaches Are Synthesized at Adopt Time, Not Refine Time

**Status:** shipped

**Decision:** The spec-generation council (`refine goals`, `import`) no longer emits Approach nodes. The `SpecProposal` JSON contract drops `approaches[]` entirely, along with `Feature.Approaches` and `Strategy.Approaches` cross-reference arrays. Approach synthesis moves to `adopt`: when the reconciler encounters a Feature or Strategy in scope that has no Approach attached, it invokes the existing single-approach synthesizer to produce one on demand, persists it as `app-<parent-id>.md`, and updates the parent's `approaches[]` slice on disk.

**Why:** A real `refine goals` run on `winplan` (GOALS.md ~8 lines) with `googleai/gemini-3.1-pro-preview` failed three Pro Preview calls in a row to produce a referentially-clean `SpecProposal` even with mechanical, prescriptive integrity-revise prompts. The hard-fail behaviour from `5a42eb2` correctly surfaced the failure but didn't address the root cause: the architect was being asked to emit a single 2k-token JSON blob with ~30 cross-references that JSON Schema cannot enforce, all maintained by attention alone. Stronger models tolerate this; the open-source Gemini Flash / Claude Haiku tier we want to support does not.

Approaches were the worst offenders in that load: every approach needs a `parent_id` resolving to a feature or strategy, and every feature/strategy carries an `approaches[]` cross-ref array. CLAUDE.md already framed approaches as "the synthesis layer for coding agents" — implementation sketches that bridge spec and code. They need code context. During refine that context doesn't exist; the architect invents the sketch, and those invented sketches drive a substantial fraction of the dangling-ref problem.

**How approaches reach disk:** `adopt` already classifies approaches (live/drifted/unplanned/failed). The single-approach synthesizer at [cmd/refine.go](../cmd/refine.go)'s `invokeSynthesizer` already takes a parent's prose plus applicable decisions and returns a `RewriteResult.RevisedBody`. The new path in [cmd/adopt_synthesize.go](../cmd/adopt_synthesize.go) walks the spec graph for parents in scope with empty `Approaches`, calls the synthesizer per parent, persists the result via `specio.SaveMarkdown`, and updates the parent JSON. Re-runs are idempotent: the deterministic ID `app-<parent-id>` collides on re-run and we skip parents whose `Approaches[]` already names the new ID.

**Out of scope:** the on-disk shape under `.borg/spec/` is unchanged (DJ-085 stability). `spec.Feature.Approaches` and `spec.Strategy.Approaches` stay; only the LLM-facing `SpecProposal` types lose them.

**Migration:** existing projects with persisted approaches keep them — `adopt` only synthesizes when `Approaches` is empty for a parent. The architect agent file at `.borg/agents/spec_architect.md` ships via `locutus init` (scaffold). Existing projects keep their old version until they re-init or run `locutus update --offline --reset`.

**Reversal criteria:** revert if (a) per-parent synthesis at adopt time has materially worse cost or wall-clock than the single-call architect path it replaces *and* the architect path becomes reliable on weak models (unlikely without Phase 2's outline → fanout decomposition), or (b) the deterministic `app-<parent-id>` ID scheme collides with user-authored approach IDs in practice — at which point we add a numeric suffix or move ID assignment into the synthesizer agent.

**Reference:** plan at [.claude/plans/council-resilience.md](../.claude/plans/council-resilience.md), Phase 1.
