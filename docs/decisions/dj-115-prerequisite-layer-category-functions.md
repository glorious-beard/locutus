## DJ-115: Prerequisite Layer Is a Category of Functions, Not an Interface

**Status:** shipped

**Decision:** Operations declare their prerequisites as direct function calls — `prereqs.EnsureSpecsContainSummaries(ctx, sctx, regen bool) error` is the first, with more to follow as the surface grows. There is no `Precondition` interface, no registry, no abstraction layer. Each prereq is a concrete assertion paired with an optional resolution path; the caller controls the resolution mode via the `regen bool` argument. `update` runs the full prereq set as part of its normal flow; individual verbs invoke the specific prereqs they depend on. The `--check-pre-reqs` flag on `update` invokes the prereq pass standalone (the dev compile-and-run loop), composing with `--offline` to skip the network/binary-fetch parts while still satisfying prereqs.

**The assertion-with-optional-resolution shape.** A prereq function takes `regen bool`:

- `regen=false` → assertion only. The function walks the on-disk shape, counts violations, returns a typed error (e.g. `*SummariesError`) listing the offending node ids. No LLM calls, no mutations. Used by `--dry-run` paths because dry-run promises not to mutate anything — including prereq-driven fills.
- `regen=true` → assertion + resolution. The function walks, and if any violations exist, dispatches the resolution workflow (for SummariesPresent: `FillSummariesWorkflow`) to bring the on-disk shape into conformance. Returns `*SummariesError` only when the workflow itself leaves nodes unfilled (e.g. provider exhausted retry budget on a transient).

The caller — a CLI verb — sets `regen = !c.DryRun`. Dry-run that hits an unfilled-summaries project fails with `N spec nodes missing Summary; rerun 'locutus update --check-pre-reqs' to fill`. This is the honest report — dry-run cannot predict the post-resolution state without mutating, and reporting truncation fallbacks would lie about what the actual run would see.

**The flag matrix on `update`.** Three flags compose:

| Combination | Prereq behavior |
| --- | --- |
| `update` | run (regen=true) along with all other update work |
| `update --reset` | run (regen=true) after the reset refresh |
| `update --offline` | skip (no LLM calls in offline mode by default) |
| `update --offline --reset` | skip prereqs; refresh local files only |
| `update --offline --check-pre-reqs` | run prereqs (regen=true); skip the binary-fetch but still satisfy prereqs |
| `update --check-pre-reqs` | equivalent to bare `update` for the prereq pass |

The rule is `shouldRunPrereqs = !c.Offline || c.CheckPreReqs`. `--check-pre-reqs` is the dev-loop primitive — compile a new binary locally, run `update --offline --check-pre-reqs`, and the on-disk shape gets verified against what the new binary expects without any network round-trip for defaults refresh.

**Why no interface.** A `Precondition` interface or registry slice would be premature abstraction for a single concrete implementation. Concrete functions read clearly at the call site, compose naturally with verb-specific dependencies (some verbs already have a dispatcher; the prereq reuses it instead of constructing its own), and don't pretend the prereq surface is uniform — `EnsureSpecsContainSummaries` needs `SummariesContext{FSys, Executor, Dispatcher}`; a hypothetical `EnsureTracesPresent` would need different inputs (assimilation pipeline, maybe a code-walker). Forcing them through a common interface would either flatten the signature into `any` or invent a context struct nobody reads. When a second prereq lands and the call-site composition pattern becomes obvious, the planned refactor is config-driven `WithXxx` builders rather than method-bag interfaces — but that's not warranted until the second concrete prereq exists.

**The `fill-summaries` workflow shape.** Resolution for `SummariesPresent` is a one-step parallel fanout workflow. The discovery phase happens upstream in the prereq layer (walks `.borg/spec/<kind>/` directories, returns `[]MissingSummaryNode{Kind, ID, Path, Content}`). The workflow's single step fans out one item per missing node, runs `spec_summarizer` (fast-tier) against the node's content, writes the produced `Summary` back to disk via `specio.SavePair` / `SaveMarkdown`. The merge handler accumulates per-id success / failure in the orchestrator goroutine so there's no shared-state contention between parallel `RunItem` slots. Concurrency is bounded by `models.yaml`'s per-model `concurrent_requests` cap — same envelope every other council workflow uses.

Per-item failures (parse failures, empty summaries, provider exhausted) land on `state.Failed[id]`. The prereq returns `*SummariesError{Failed: ...}` when the count is non-zero; the operator reruns. The retry budget within a single workflow run is 3 (the standard `executionRetryConfig`); beyond that the prereq accumulates failures rather than continuing-on-partial-success because the verb that invoked the prereq needs to know its inputs are complete before proceeding.

**Concurrency and contention.** Sequential within a single invocation — the prereq fills, the workflow returns, then the calling verb runs. No race surface between the prereq and the verb because they run in order. Cross-invocation (two parallel `locutus` runs) is bounded by file-level atomic writes in specio (tmp + rename): last writer wins, both Summary values are valid, and a refine racing a summarizer fill has its rewrite-of-the-whole-node naturally supersede a summarizer-only Summary write.

**What's new:**

- `internal/prereqs/` package ([doc.go](../internal/prereqs/doc.go), [summaries.go](../internal/prereqs/summaries.go)). Single function `EnsureSpecsContainSummaries(ctx, sctx, regen) error` plus a typed `*SummariesError`.
- `FillSummariesWorkflow` ([internal/agent/workflow_fill_summaries.go](../internal/agent/workflow_fill_summaries.go)) with `FillSummariesState`, `MissingSummaryNode`, `runSummarizeOne`, `mergeSummarizeResults`. Per-kind write-back (`writeBackJSONSummary[T]` for Feature/Strategy/Decision/Bug, `writeBackApproachSummary` for the markdown-only Approach).
- `spec_summarizer` fast-tier agent ([internal/scaffold/agents/spec_summarizer.md](../internal/scaffold/agents/spec_summarizer.md)) with `SpecSummaryResult` schema ([internal/agent/spec_summarizer.go](../internal/agent/spec_summarizer.go)).
- `UpdateCmd.CheckPreReqs` flag + the `shouldRunPrereqs` matrix ([cmd/update.go](../cmd/update.go)). `--check-pre-reqs` and `--offline` compose orthogonally.
- `cmd/prereqs.go` helper (`runSpecPrereqs(ctx, fsys, llm, regen)`) so individual verbs (`import`, `refine`, `adopt`) invoke prereqs at their LLM-acquisition point without each one repeating the dispatcher construction.

**What stays the same:**

- `.borg/manifest.json` content (DJ-081).
- The verb set's 8-verb cap (DJ-101) — no new `prereq` verb. The flag on `update` is the surgical surface; the implicit pass in every other operation is the natural one.
- DJ-094's tools (`spec_list_manifest`, `spec_get`) — the prereq fills the data they read; no change to the tool contract.

**Rejected alternatives:**

- **`Precondition` interface with a registry.** Premature abstraction for one impl; flattens distinct input signatures into a `any`-typed bag. Reconsider when the second concrete prereq lands.
- **Trigger prereq resolution from `spec_list_manifest` (a read tool that secretly writes).** The original draft idea, rejected after the contention analysis. Even though file-level atomicity made it safe, the tool semantics — a tool documented as a pure read suddenly mutating spec JSON, with N LLM calls of latency inside what looks like a single tool call — would break the trace's interpretability and the agent's mental model of cost. Resolution happens at the verb boundary, where the cost is visible to the caller.
- **Continue-on-partial-failure in `FillSummariesWorkflow`.** Leaves the calling verb running against a partially-conformant on-disk shape. The invariant "prereqs satisfied at op start" only holds if the prereq fails when it can't reach satisfaction; soft-fail breaks the invariant.
- **Sequential summarize loop instead of parallel fanout.** Linear in node count; for a 270-node legacy project that's ~270× the wall time. Per-model concurrency caps already bound the actual parallelism, so the fanout is bounded by what the provider tolerates, not by goroutine count.
- **Always-required `Summary` at the JSON-schema level (no prereq, just rejection).** Would force every authoring agent into one synchronized release. The prereq path is the graceful enforcement — the field is required at the system level, with a lazy fill, instead of required at every individual write call.

**Reversal criteria:** revert if (a) the prereq's wall-time cost on routine `import` / `refine` invocations becomes painful enough that operators want to disable it per-verb — at which point we'd add per-verb opt-out flags or move resolution to a background-only path (lazy fill on a timer). (b) A second prereq fails to fit the `Ensure...(regen)` shape — at which point the planned `WithXxx`-builder refactor accelerates. Neither failure mode invalidates the operation-prerequisite framing; only the surface area changes.

**Reference:** depends on DJ-094 (spec-lookup tools the filled summaries feed), DJ-081 (`.borg/manifest.json` as project-root marker — unchanged), DJ-068 (`.borg/spec/` IS the manifest — the prereq updates the manifest in place), DJ-112 (workflows in Go, agents in `.borg/agents/` — the `fill-summaries` workflow follows this pattern). Companion to DJ-114 (the field this prereq fills).
