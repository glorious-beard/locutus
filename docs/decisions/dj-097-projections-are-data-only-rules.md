## DJ-097: Projections Are Data-Only; Rules Belong in the Agent's `.md`

**Status:** shipped

**Decision:** Projection functions in [internal/agent/projection.go](../internal/agent/projection.go) emit data only — context, fanout items, concerns to address. They do NOT carry directives ("Emit a RevisionPlan…", "Produce one Raw…Proposal…", "Findings you judge non-actionable are simply omitted"). All rules of behavior live in the agent's `.md` system prompt under `# Identity / # Context / # Task / # Mandates`.

**Why:** the system prompt and the projection's user-message tail are two surfaces that BOTH render into the model's context, but they're maintained in two different files with two different mental models. When rules drift between them, the user message wins at inference time and silently overrides the system prompt. DJ-095's Phase 4 broke for one run (May-4 winplan trace) because the triager's system prompt was rewritten to mandate "every finding routes to one of three buckets" but the projection tail still said "findings you judge non-actionable are simply omitted." The user-message instruction won; 22 of 32 critic findings got dropped — exactly the behavior the system-prompt rewrite was supposed to eliminate.

The structural fix: never let projections carry directives. With that constraint, drift between the two surfaces becomes structurally impossible because there's only one surface for rules.

**Concrete changes in [projection.go](../internal/agent/projection.go):**

- `projectElaborateOne` — dropped "Produce the full Raw…Proposal for the item above. Preserve its id and title verbatim. Decisions are inline; the reconciler downstream dedupes across siblings." That directive lives in [spec_feature_elaborator.md](../internal/scaffold/agents/spec_feature_elaborator.md) and [spec_strategy_elaborator.md](../internal/scaffold/agents/spec_strategy_elaborator.md) Task sections.
- `projectTriage` — dropped the routing-completeness reminder at the tail. The mandate lives in [spec_revision_triager.md](../internal/scaffold/agents/spec_revision_triager.md), now the only surface that owns it. Added an explicit "emit empty arrays as `[]`, never `[{}]`" rule to the .md to defuse the secondary regression.
- `projectReviseNode` — dropped "Produce the corrected Raw*Proposal… Address every concern… Re-emit the FULL node." The "revise mode" addendum in each elaborator's .md owns this.
- `projectAdditionElaborate` — dropped "Produce one Raw*Proposal that addresses the finding above. Invent the id…" The "addition mode" addendum in each elaborator's .md owns this.
- `projectReconcile` — dropped "Emit a ReconciliationVerdict naming the clusters that need dedupe / resolve_conflict / reuse_existing." Lives in [spec_reconciler.md](../internal/scaffold/agents/spec_reconciler.md). Kept the data-conditional flag noting whether an existing spec is present (data, not directive — different per call).

What remains in projections is strictly data: section headers, fanout items as plain key-value pairs, concern lists grouped by kind, prior content blocks, existing-nodes lists.

**Snapshot tests for rendered prompts** ([prompt_snapshot_test.go](../internal/agent/prompt_snapshot_test.go) + `testdata/golden/<step>.txt`):

- A test renders the FULL system + user message for each council step against a fixed fixture, diffs against a golden file, and fails on any change.
- Update mode: `LOCUTUS_UPDATE_GOLDEN=1 go test ./internal/agent -run TestRenderedPrompt`. Rebuilding goldens forces the diff into the PR review surface.
- Eight scenarios covered: triage, revise_features, revise_strategies, revise_feature_additions, revise_strategy_additions, elaborate_features, elaborate_strategies, reconcile_greenfield. The high-leverage ones — i.e. the steps where the DJ-095 contradiction bug actually bit.
- A second test (`TestRenderedPromptHasNoContradictions`) is a permanent invariant: the triager's rendered prompt must NOT contain "simply omitted" / "non-actionable" alongside the routing-completeness mandate. Reintroducing either by accident fails the test loudly.

What this catches:

1. Drift between system prompt and projection (the actual DJ-095 bug). A rule change in either surface produces a golden diff that the reviewer either accepts (refresh golden) or rejects (the change wasn't supposed to land).
2. Schema-example regressions — `RegisterSchema` changes flow into the system prompt via `BuildGenerateRequest`, so any schema edit shows up in the snapshots.
3. Subtle restructurings — context reordering, header changes, accidental whitespace shifts — that wouldn't fail behaviorally but would change what the model actually sees.

What this doesn't catch:

- Semantic correctness of the rules themselves. A coherent prompt that's just *wrong* will produce wrong-but-consistent output and snapshot diffs won't help. That's a different test layer (real LLM runs).
- Behavior changes that don't show up in the prompt — model config, tool availability, capability tier resolution. Those need their own tests.

**What stays the same:**

- `BuildGenerateRequest` continues to assemble system prompt (agent .md body + appended schema) + user messages (from projection). No structural change to the rendering pipeline.
- `buildRevisePrompt` (used by the legacy single-call revise path on non-spec-generation workflows) still carries directive content. Out of scope; the spec-generation council no longer uses that path. If that path ever comes back into active use, the same DJ-097 rule applies and it should be moved into the corresponding agent's .md.
- `projectChallenge` / `projectResearch` / `projectRecord` keep small directive fragments ("Review the proposal above", "Investigate these concerns:", "Record the council session above"). These are border-line — short data-labels that double as one-line task statements — and the agents that use them aren't the council agents currently exercised by `refine goals`. Defer until those paths come into active maintenance.

**Process implication:** when modifying an agent's behavior, the agent's `.md` is the single source of truth. The projection's job is to format the data that .md tells the agent to consume. Anyone reviewing a prompt change should ask: "did this rule change show up in the snapshot diff?" If yes, the golden has to refresh and the change is visible to reviewers. If no, the change is in code that doesn't render into the prompt and shouldn't affect agent behavior.

**Reversal criteria:** revert if data-only projections turn out to lack the localization that prompt experimentation needed (e.g., A/B testing wording for one step without rebuilding the whole .md). At that point a structured "directive-fragments" mechanism — first-class, not free-form — would be the right abstraction. Today that's premature.

**Reference:** corrects the regression that broke DJ-095's Phase 4 in the May-4 winplan run. The Phase 4 design itself stands; this DJ corrects an implementation drift surface that DJ-095 didn't anticipate.
