## DJ-026: Historian Uses LLM for Narrative, Not Just Deterministic Recording

**Status:** shipped

**Decision:** The historian has two layers. Layer 1 (deterministic): records structured JSON events (what changed, old/new values, alternatives). Layer 2 (LLM): writes a compelling human-readable narrative connecting decisions to the broader project arc.

**Why LLM for narrative:** Structured JSON events are queryable but not useful to a human reader. The shoe project's LOG.md reads as a story ("After five days attempting CT-scan-derived sock maps, the domain translator identified that the hosiery industry has standard pattern templates..."). That narrative quality — highlighting what's surprising, noting reversals, providing context — requires an LLM. A mechanical event log would never produce that.

**The two layers complement each other:** JSON events are the source of truth for blast radius, revisit queries, and machine consumption. The narrative summary in `.borg/history/summary.md` is a derived artifact for human reading — a rich project history that explains not just what happened but why it matters.

**Implementation notes (landed 2026-04-23 as Round 2 of the gap-closeout plan):**

- **Manifest + detail layout.** The narrative isn't a single monolithic file. `.borg/history/summary.md` is a manifest — timeline + index of targets with deeper history — and `.borg/history/details/<target-id>.md` hold the per-target narrative depth for any target with ≥ N events (configurable; default N=2). The manifest is cheap to regenerate and scannable in 30 seconds; the detail files are where the motivational narrative lives.

- **Two agents per DJ-036.** The manifest is written by the **archivist** (`internal/scaffold/agents/archivist.md`, fast tier) — terse, faithful, non-interpretive. The detail files are written by the **analyst** (`internal/scaffold/agents/analyst.md`, balanced tier) — causal reasoning, motivation analysis, honest when the record is sparse. The split matches the cost/value curve: fast-tier for the 80% case that's just structural index updates; balanced-tier for the 20% that needs narrative depth.

- **Debounce via embedded hash.** Both layers carry a `<!-- locutus-narrative-hash: ... -->` comment in their body. On re-run, `GenerateNarrative` recomputes the hash over the current event set (or per-target subset) and skips the LLM if it matches. `--force` bypasses. Per-target debounce is independent of the manifest-level debounce — a target whose events haven't changed isn't re-analysed even when other targets churn.

- **User-controlled scoping.** `locutus history --regenerate-narrative` is the trigger. `--since YYYY-MM-DD` and `--until YYYY-MM-DD` narrow the event window. The debounce hash is computed over the scoped set, so the same window produces the same skip-or-regen decision on repeat.

- **Package decoupling.** `internal/history` does not import `internal/agent` (the inverse dependency already exists for event records), so the LLM contract in `history` is a narrow `GenerateFn func(ctx, userPrompt) (string, error)`. The system-prompt identity of each agent is the caller's concern — cmd-layer closes over the loaded agent definition and passes the wrapped closure in. Helper at `agent.NamedAgentFn(fsys, llm, agentID)` loads the def and returns the callback; its shared home in the agent package is explicitly so `internal/cascade` and `internal/preflight` (which today hard-code system prompts against their own agent files) can adopt the same pattern in a future cleanup.

**Follow-up (not done in Round 2):** `internal/cascade/cascade.go` and `internal/preflight/preflight.go` still inline their system prompts instead of loading `rewriter.md` / `preflight.md` via `agent.NamedAgentFn`. The agent def files exist and carry the correct personas; the two packages just need the same wrapper call `cmd/history.go` uses now. Tracked as a refactor, not a DJ — the designed behaviour is unchanged.
