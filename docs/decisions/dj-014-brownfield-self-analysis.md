## DJ-014: Brownfield Self-Analysis

**Status:** shipped

**Decision:** Don't scaffold `.borg/` for the Locutus repo during implementation. Use brownfield analysis in a later session.

**Alternatives:** Could manually create spec files from PLAN.md now.

**Why later:** Brownfield recovery captures actual state, not planned state. Some decisions may shift during implementation. Also dogfoods the brownfield feature — if it can't recover Locutus's own architecture, we have a bug.
