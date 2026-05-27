## DJ-090: Outline + Per-Node Elaborate Fanout (Council Resilience Phase 3)

**Status:** shipped

**Decision:** The spec-generation council's single architect call is replaced by a three-step shape:

1. **Outline** (1 LLM call) — `spec_outliner` emits an `Outline` JSON: feature and strategy titles + one-line summaries only. No decisions, no detailed descriptions. The outline IS the spec's structural skeleton.
2. **Elaborate fanout** (N+M LLM calls in parallel) — for each outlined feature and strategy, a per-node elaborator (`spec_feature_elaborator`, `spec_strategy_elaborator`) emits the full `RawFeatureProposal` / `RawStrategyProposal` with inline decisions for that one node. Per-call output is bounded by node complexity, not project size.
3. **Assemble + Reconcile** — the merge handler stitches the per-element outputs into a full `RawSpecProposal`; the existing Phase-2 reconciler (DJ-088) consumes it unchanged and produces the canonical `SpecProposal`.

The reconciler doesn't change. Only the upstream call topology changes. Critique and revise downstream are also unchanged.

A new `fanout` field on `WorkflowStep` names a state path (`outline.features` / `outline.strategies`); `WorkflowExecutor.ExecuteRound` spawns one agent invocation per element, threading the element JSON through `StateSnapshot.FanoutItem` for the projection function to render. Per-model concurrency caps (new `concurrent_requests` knob in `models.yaml`) bound actual parallelism so fanout never floods a model past its rate-limit window.

**Why:** A real winplan run on Pro Preview (April 30 2026) truncated mid-JSON at 64k output tokens during the revise step — i.e., the model couldn't fit a corrected RawSpecProposal for a moderately-sized multi-deliverable spec into its output budget. That's not a knob-tuning problem; it's a structural one. The architect was being asked to emit too much per call.

Phase 3's per-node fanout collapses each call's output to ~one node's worth of JSON (4–8k tokens regardless of project size). Truncation stops being a recurring failure mode; parallelism makes wall-clock better; one bad elaborator output retries one node instead of the whole proposal. The plan considered this in advance — Phase 3 was written ahead of Phase 2 with this exact failure mode in mind.

**Why we waited.** The plan's sequencing recommended Phases 1+2 first, then Phase 3 only if measured failure said so. We measured: Phase 1+2 alone weren't enough on Pro Preview at 64k cap. Phase 3 was the next move, not a hedge.

**Why concurrency caps belong in models.yaml.** Without per-model throttling, a 10-feature project would fire 10+ concurrent calls and trip free-tier or preview-model rate limits, then stall on backoff retries. The cap belongs in YAML (the user can tune per project, per quota tier) rather than as a compiled-in constant. Embedded defaults: 2 for preview models (3-flash-preview, 3.1-pro-preview), 4 for stable Gemini Flash, 5 for Claude Haiku, 3–4 for Sonnet/Opus. Generous on paid tiers; conservative on previews.

**What stays the same:**

- Reconciler agent and `ApplyReconciliation` logic (DJ-088).
- Critique + revise + reconcile_revise flow (DJ-088, DJ-089).
- Cascade rewrite on conflict-resolution actions.
- Integrity critic in the critique merge pass (DJ-089).
- `RawSpecProposal` shape (just emitted incrementally instead of all-at-once).

**What's new:**

- `Outline`, `OutlineFeature`, `OutlineStrategy` types in [internal/agent/elaboration.go](../internal/agent/elaboration.go).
- Three new agents: `spec_outliner.md`, `spec_feature_elaborator.md`, `spec_strategy_elaborator.md`.
- `WorkflowStep.Fanout` field and `extractFanoutItems` resolver.
- `StateSnapshot.FanoutItem` and `StateSnapshot.Outline` for per-element projection.
- `assembleRawProposal` Go function — stitches fanout outputs into `state.RawProposal` (best-effort; malformed elaborator outputs are dropped with a slog warning rather than aborting the assembly).
- `ModelKnobs.ConcurrentRequests` and the per-model semaphore in `GenKitLLM`.

**Reversal criteria:** revert if (a) the outliner's per-item summaries are so thin that elaborators systematically fail to commit on coherent decisions — at which point the outline schema needs richer per-item context, not abandonment of fanout; or (b) the per-model concurrency caps are a meaningful bottleneck for paid-tier users on big projects — at which point we make the caps tier-aware rather than hard-coding defaults. Neither failure mode is structural; both are tunable.

**Reference:** plan at [.claude/plans/council-resilience.md](../.claude/plans/council-resilience.md), Phase 3. Builds on DJ-088 (Phase 2's reconciler is reused unchanged) and DJ-089 (Phase 5's critic sharpening still applies to the post-reconcile critique).
