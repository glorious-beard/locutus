## DJ-030: File Conflict Prevention at Plan Time, Rebase as Fallback

**Status:** shipped (plan-time prevention; rebase fallback deferred)

**Decision:** The critic flags file overlaps between parallel workstreams during planning. The planner restructures to eliminate them (merge workstreams, add dependency edges, or extract shared files into a dedicated workstream). If unanticipated overlaps occur at runtime (agent touches files not in ExpectedFiles), fall back to sequential rebase with conflict resolution.

**Why plan-time prevention over runtime merge:** Merge conflicts during agent execution are expensive — the agent may need to re-run steps, and automated conflict resolution is unreliable. Preventing overlaps at plan time is cheaper and more predictable. The rebase fallback handles edge cases where agents touch unexpected files.

**Implementation (Round 6, 2026-04-25):** [`internal/overlap/`](../internal/overlap/) ships a layout-agnostic Go detector — `overlap.Detect(plan, approachesByID)` returns inter-workstream conflicts as `Report{WorkstreamA, WorkstreamB, SharedFiles}`. The "files this workstream touches" set is the union of every step's `ExpectedFiles` and the referenced Approach's `ArtifactPaths`, so a planner that populates either field has its declared writes counted. Sequential workstreams (connected by transitive `DependsOn`) are exempt; intra-workstream sharing is allowed. `cmd/adopt.go:planWithOverlapRetry` wraps `cfg.Plan` in a retry loop: on overlap detection, the next call sees an "Overlap conflicts" prompt section listing conflicts and naming the two valid resolutions (merge the workstreams or add a `depends_on` edge). After 3 retries with persistent overlap, adopt errors out with the conflict surfaced.

**Detection layer choice:** despite the DJ's "critic flags" wording, the detector is Go code, not an LLM critic. File-set intersection is mechanical and deterministic; the qualitative call (how to resolve) is delegated to the planner on retry, which is already an LLM. No critic agent step.

**Rebase fallback:** explicitly out of scope for Round 6. Plan-time prevention is the priority per the DJ; runtime rebase is a separate future concern.

**Known followup:** the synthesizer agent (Round 3) and planner currently allocate file sets without bias toward agent-shaped granularity. Operating evidence may show that smaller, agent-shaped files reduce overlap-retry pressure. A separate DJ should consider whether the synthesizer prompt and the planner's `ArtifactPaths` allocation should prefer finer-grained file boundaries — and whether brownfield assimilation should *split* existing files toward that target as part of remediation. Tracked in [`.claude/plans/gap-closeout.md`](../.claude/plans/gap-closeout.md) Round 6 status.
