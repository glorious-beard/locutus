## DJ-102: Refine Becomes a Deliberate-Evolution Loop

**Status:** shipped

**Decision:** `locutus refine` grows three new flags — `--brief "..."`, `--diff`, `--rollback` — backed by automatic history capture of pre/post bytes on every refine that mutates a node. The verb stays singular but its semantics shift from "regenerate the node" to "deliberately evolve the node."

**The three flags and what they do:**

| Flag | Effect | Cost |
| --- | --- | --- |
| `--brief "..."` | Threads a focused refinement intent into the rewriter prompt as a directive. Substantive rewrite expected. | One LLM call (existing). |
| `--diff` | After the refine writes, prints a unified diff over the per-node Markdown render of prior vs. new bytes. | Free; no extra LLM call. |
| `--rollback` | Restores the spec sidecar to the OldValue captured on the most recent `spec_refined` event. Records a `spec_rolled_back` event so subsequent rollbacks walk past it. | Free; no LLM call. Mutually exclusive with `--brief`, `--diff`, `--dry-run`. |

**Why automatic history capture, not opt-in:** the rollback path needs the prior bytes preserved verbatim, and `--diff` benefits from the same data. Once the side-effect is automatic, it also feeds `locutus history <id>` (existing surface) with structured refine events for free. Per-node history was previously emitted as `feature_refined` / `strategy_refined` / etc. without `OldValue` / `NewValue` payloads — useful for audit, not enough for rollback. The new `spec_refined` event carries both, replacing the per-kind labels for refine writes. Decision-path cascade keeps its existing operational events because the cascade rewrites *parents*, not the decision itself; the user-edited decision JSON has no pre-edit state to preserve.

**Why brief is threaded via context, not a parameter:** `cascade.WithBrief(ctx, brief)` and `cascade.BriefFromContext(ctx)` follow the same pattern `agent.WithRole` already uses for per-call metadata. Adding a `brief string` parameter to `RewriteFeature` / `RewriteStrategy` / `RewriteBug` / `InvokeRewriter` would touch every council-pipeline caller (assimilate, post-reconcile cascade, intake's planning pass) — most of which never carry an intent. Context keeps signatures stable for the no-brief path; the rewriter pulls the value via one helper at the prompt-assembly point.

**Why two agents (rewriter + refiner) instead of one:** the cascade rewriter is conservative ("minimum diff", don't paraphrase for stylistic preference), the refine path is deliberate (the user explicitly asked for change; deliver it). A single prompt with a "if Refinement intent is present, embrace it; else hew to minimum diff" branch arbitrates the conditional at inference time and can under-edit in refine mode because "minimum diff" is on the page. Two purpose-built agents — `rewriter.md` (cascade) + `refiner.md` (refine) — produce more predictable output. Dispatch is deterministic: `cascade.invokeRewriter` checks `BriefFromContext` and loads the matching scaffold .md.

DJ-097's lesson stays applicable: rules belong in the agent's `.md`, not in code or projection layers. Splitting reduces the conditional surface inside the .md to zero on either side.

The synthesizer (Approach refines) currently keeps a single agent with conditional brief handling. The cost of splitting is duplication; the trigger to revisit is observed conservativeness in refine-mode resyntheses. Symmetric with rewriter if it shows up.

**Why diff renders Markdown via per-node renderers, not raw JSON:** JSON-level diff is mechanically correct but full of noise from `updated_at` / `created_at` timestamp churn, `alternatives` reordering, and other artifacts that don't represent semantic change. Markdown rendering normalizes those out and shows the human-meaningful change. Both sides of the diff render through the same stub-Loaded path so back-reference resolution from the live graph (which moves around as other nodes change) doesn't surface as spurious diff lines for THIS refine.

**Why diff helper is in-tree, not `sergi/go-diff`:** ~140 LoC of LCS over lines covers the use case. Refine-sized inputs (a single node's rendered Markdown) are hundreds of lines max; the O(n*m) memory of classic LCS is fine. Adding a dependency for a feature this size is heavier than maintaining the helper.

**Why scaffold-loaded prompts via `scaffold.LoadAgent`:** previously `cascade.invokeRewriter` and `cmd/refine.go invokeSynthesizer` carried inline `agent.AgentDef{...}` literals while well-authored `internal/scaffold/agents/{rewriter,synthesizer}.md` files existed for the same agents. The inline path won at runtime; the scaffold .md was dead code from the cascade's perspective. Editing `.borg/agents/rewriter.md` did nothing today — the user couldn't tune behavior even though the surface looked customizable.

`scaffold.LoadAgent(fsys, id)` reads `.borg/agents/<id>.md` first (per-project user override) and falls back to the embedded scaffold copy when the project file is absent (fresh installs, tests on uninitialized FSes). The scaffold .md is the source of truth for the initial prompt; the project copy is a user-owned override. Helpers like `intake.go` (no scaffold .md exists) inline because there's nothing to override; the inline pattern stays correct for *that* shape, just wrong for rewriter/synthesizer.

**Pre-existing bug fixed while here:** the rewriter and synthesizer agents had no `OutputSchema` declared on the inline `AgentDef`, so the API request had no strict-mode JSON enforcement. Gemini 3 occasionally returned `{"prose":"..."}` instead of `{"revised_body":"..."}` — silently wiping the parent Description because `cascade.RewriteFeature` treats an unmatched RevisedBody as "empty body, save it." Registering `RewriteResult` in `cascade`'s `init()` and setting `output_schema: RewriteResult` in the scaffold .md frontmatter forces strict-mode matching against the actual struct. The schema lives in cascade (not internal/agent) so the registered example stays the source of truth alongside the consumer.

**Cuts from the original plan, both for the no-aspirational-fields rule (mirrors the `--against-finding` cut from DJ-101's justify):**

- `--address-finding <session>:<idx>`: sugar to load a recorded critic finding as the brief. No real consumer today; the workflow (review → recall finding → re-challenge) hasn't surfaced. ~10 LoC if it does.
- `--dry-run --diff` combined mode: requires plumbing "LLM-without-save" through every cascade rewriter so the diff renders against a proposed-but-not-persisted node. Invasive for marginal gain over the existing `--dry-run`-as-cascade-blast-radius preview. Add when the use case appears.

**MCP parity:** the existing `refine` MCP tool gains `brief`, `diff`, `rollback` parameters with the same handler shape. Mutual-exclusion validation lives in the shared dispatcher so CLI and MCP enforce the same invariants. An IDE-side agent driving "the SRE critic flagged X; address it" issues `refine({id, brief: "..."})` and gets the diff back as the tool result — recoverable via `refine({id, rollback: true})` if the agent's interpretation was off.

**Reversal criteria:** revert `--brief` if observed refine-mode rewrites drift from the user's intent more often than they hit it (the refiner becomes a noisy paraphrase). Revert `--rollback` if real users discover that decision-path refines (which don't auto-record `spec_refined` events because the user-edited decision has no pre-state) leave them confused about what's rollback-able. The rollback gap is documented in the result Note; if the doc is insufficient, the cascade path needs its own pre-edit capture.

**Reference:** depends on DJ-100's per-node renderers (consumed by `--diff`). Bridges from DJ-101's `justify --against` adversarial verdict, which suggests a `refine --brief "..."` invocation pre-populated with the breaking points — closes the deliberation loop. DJ-097 ("rules belong in the agent's `.md`") is the precedent that justified moving prompts off inline literals to scaffold .md files.
