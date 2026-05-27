## DJ-046: Hybrid Remediation — Cross-Cutting + Feature-Specific

**Status:** shipped (2026-04-25)

**Decision:** Cross-cutting gaps (missing CI, linter config, coverage thresholds) become a single consolidated "project-remediation" feature. Feature-specific gaps (missing auth tests, undocumented auth decisions) attach to their respective features.

**Why hybrid:** Pure consolidation loses the feature-level context ("these missing tests are for auth"). Pure per-feature loses the cross-cutting view ("the project has no CI at all"). Hybrid gives both: the consolidated feature handles infrastructure gaps, individual features handle their own quality gaps.

**Implementation (Round 5, 2026-04-25):** Consolidation and attachment rules live in the [`remediator` agent prompt](../internal/scaffold/agents/remediator.md), not in `internal/remediate/`. The agent is told: cross-cutting quality gaps go under one `f-project-remediation` Feature with separate Decision+Strategy pairs; feature-specific gaps emit a `FeatureUpdate{FeatureID, AddedDecisions}` against the existing Feature. The package code faithfully threads the agent's structured output into the AssimilationResult, pulling existing-spec Features into the result when a `FeatureUpdate` references one, so the persistence pass writes them back with the new Decision references.

**Cascade-skip caveat:** Round 5 ships without firing `cascade.Cascade` after remediation — the remediator writes new Decisions and updates parent Features in coordination, so the resulting prose is consistent by construction. If empirical drift emerges between remediator-authored prose and the rewriter's voice in later runs, revisit; a follow-up DJ would document the trigger.
