## DJ-050: brownfield → assimilation Rename

**Status:** shipped

**Decision:** Rename "brownfield" to "assimilation" throughout the codebase: package names, types (`BrownfieldRequest` → `AssimilationRequest`), enum values (`PlanActionBrownfield` → `PlanActionAssimilation`), comments, and agent definitions.

**Why:** "Brownfield" is enterprise jargon that doesn't fit the Borg theme. "Assimilation" matches the project's naming convention and is more descriptive of what the pipeline actually does — it absorbs an existing codebase into the spec graph.
