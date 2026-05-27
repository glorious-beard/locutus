## DJ-063: Sliding-Window Churn Rule over Consecutive Counter (Refines DJ-058)

**Status:** shipped

**Decision:** Escalate to `RefineStep` when ≥2 of the last 3 attempt outcomes are `churnDetected`. Validation-only failures occupy slots in the window without counting as churn.

**Refines DJ-058** (which described a simple `consecutiveChurns` counter incremented on churn and reset on any non-churn outcome). That rule fails on alternating patterns — churn → validation-fail → churn — because the reset on the middle attempt clears the counter even though the step is clearly stuck in a loop.

**Regression test:** `TestSupervise_AlternatingChurnFailChurn_Escalates` exercises exactly that pattern and would fail the consecutive-counter implementation. Added as the guard against any future revert.

**Non-churn outcomes:** stay in the window but don't contribute to the count. They push old churn out once the window fills (after the 4th attempt, the oldest slot is dropped). This preserves the "N-of-last-M" semantics without letting validation failures pile up as evidence of churn.
