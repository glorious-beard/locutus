## DJ-045: Brownfield Includes Gap Analysis and Autonomous Remediation

**Status:** shipped (2026-04-25)

**Decision:** After inferring the spec from existing code, brownfield runs a gap analysis (missing tests, undocumented decisions, orphan code, missing quality strategies, stale docs) and fills the gaps autonomously with `assumed` decisions and strategies. Same pattern as greenfield — no pause for user input.

**Why autonomous, not pause:** Greenfield doesn't pause to ask the user about every decision — it assumes and the user reviews later. Brownfield should be the same. The only difference is the starting point: brownfield starts with `inferred` decisions (from code), greenfield starts empty. Gap-fill decisions are `assumed` (new, not recovered from code). Both converge to the same fully managed state.

**Gap categories:** Missing tests, missing acceptance criteria, undocumented decisions (code implies a choice but no decision is recorded), orphan code (files not traced to any strategy), missing quality strategies (no linter, no CI, no coverage), stale documentation.

**Implementation (Round 5, 2026-04-25):** [`internal/remediate/`](../internal/remediate/) ships `Plan` (the remediator agent's structured output with Decisions, Strategies, Features, FeatureUpdates), `Remediate(ctx, llm, gaps, existing) → *Result`, and `ApplyToAssimilation(plan, result, existing)` which merges remediation output into the AssimilationResult before persistence so the existing DJ-075 atomic-write pass writes everything in one go. The remediate pass runs **outside** the workflow YAML — `agent.Analyze`'s `parseAssimilationResults` previously merged the workflow's `remediate` round output blindly, with no consolidation, no attachment, and no opt-out; that round was removed from [`internal/scaffold/workflows/assimilation.yaml`](../internal/scaffold/workflows/assimilation.yaml). `cmd/assimilate.go` calls `remediate.Remediate` after `agent.Analyze` returns, gated by the new `--no-remediate` opt-out flag (default ON per the autonomy posture).
