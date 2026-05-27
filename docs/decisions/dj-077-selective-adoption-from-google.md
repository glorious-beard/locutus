## DJ-077: Selective Adoption from Google ADK, Not Wholesale (Narrows DJ-029)

**Status:** settled

**Decision:** DJ-029 rejected wholesale adoption of `LangGraphGo` / `LangChainGo` on maturity and pattern-fit grounds and adopted a "custom orchestration" posture. That posture holds today but has narrowed: Locutus's custom orchestration has grown past the "~350 LOC" footprint cited in DJ-029 to roughly 2k+ LOC, and Google's Agent Development Kit ([adk-python](https://github.com/google/adk-python), [adk-go](https://github.com/google/adk-go)) shipped with first-party maintenance, active releases, and abstractions that map onto real gaps in Locutus (memory, evaluation). DJ-077 permits **selective, attribution-preserving adoption of ADK patterns and code at specific integration points**, while preserving DJ-029's rejection of wholesale adoption.

**Why not wholesale adoption:**

1. **adk-go is Gemini/Apigee-only at the model layer.** `model/gemini/` and `model/apigee/` are the only implementations; the `LLM` interface uses `genai.Content` as the content type, which is Gemini-flavored. Adopting wholesale would require a Genkit-adk-go adapter that translates Anthropic/OpenAI content into `genai.Content` shape — non-trivial, ongoing maintenance cost, lossy in places (tool-use formats, thinking blocks).
2. **adk-python is the canonical surface.** adk-go has a ~70% port: missing `evaluation/`, `code_executors/`, standalone `planners/`, `a2a/`. Anything beyond adk-go's current surface would require porting from Python. Wholesale adoption means committing to that ongoing port or depending on abstractions we only half-have.
3. **Session/runtime models don't map cleanly as a blanket.** adk-go's `session.Service` is conversation + user-scoped. Locutus is single-user, so the user-scoping is dead weight; adopting the whole runtime wholesale would complicate, not clarify. **Partial revisit (2026-04-23):** this bullet was too broad as originally written. The *event-log-per-session* shape does map onto `refine`-with-council, where a workstream behaves like a session and drafter/challenger turns behave like chat turns. Challenger and analyst agents cannot do their jobs against memory alone — they need the raw reasoning trace. A workstream-scoped session-event store lands as a Round 3 prerequisite, tracked in [`.claude/plans/gap-closeout-pre-round3-session-history.md`](../.claude/plans/gap-closeout-pre-round3-session-history.md). This is still not wholesale adoption of `session.Service` — we take the event-log shape, drop the user scoping, key by workstream.

**Where selective adoption IS appropriate:**

1. **Memory abstraction.** `memory.Service` is a tiny interface (two methods: `AddSessionToMemory`, `SearchMemory`), and the shape maps onto the agent-memory gap audited on 2026-04-23. Adapted (Content → plain string; user/app scoping → project scoping), it becomes our shared memory primitive. Copy-with-attribution.
2. **MCP toolset pattern.** `tool/mcptoolset/` demonstrates a clean way to expose MCP-server tools as agent tools with filter + confirmation. We already have MCP plumbing; this is pattern inspiration for when the planner or supervisor wants to invoke external MCP tools. Reference, not code copy.
3. **Evaluation framework (from adk-python).** Not present in adk-go. Port from adk-python as the basis for Round 4's `llm_review` assertion — rubric + LLM-as-judge shape beats writing a reviewer agent from scratch. Translation effort is real (~2 sessions) but delivers more than what we'd build.
4. **Workflow agents (sequential / parallel / loop) as reference.** `agent/workflowagents/` formalizes patterns our council executor does ad-hoc. Not code-copy today (our executor works), but good reference for future executor refactors.

**Licensing posture:**

- APL v2 → MIT is compatible. Derived files carry a top-of-file comment: `// Portions adapted from github.com/google/adk-go, Copyright 2025 Google LLC, Licensed under the Apache License 2.0.`
- A `NOTICE` file at repo root enumerates ADK-derived portions.
- If we copy verbatim chunks larger than incidental, `third_party/adk/LICENSE-APACHE` holds the license text.
- Locutus-written code remains MIT; ADK-derived portions remain APL v2. Mixed-license is standard practice.

**Implications for the gap-closeout plan:**

- **Pre-Round-3 increment — memory (shipped 2026-04-24):** `internal/memory/` ships the two-method `Service` interface (`AddSessionToMemory` + `SearchMemory`), an `Entry` shape adapted from `adk-go/memory` (string content instead of `genai.Content`, namespace scoping instead of user+app), and two implementations: `NewInMemoryService()` and `NewFileStoreService(fsys, root)` keyed by UUIDv4. Search is case-insensitive substring over `Content` (embedding-backed impl deferred). File layout: `<root>/<namespace>/<uuid>.yaml`, gitignored per DJ-073's transient-learned-state posture. Attribution lives in top-of-file comments and `NOTICE` at the repo root. Tracked in [`.claude/plans/gap-closeout-pre-round3-memory.md`](../.claude/plans/gap-closeout-pre-round3-memory.md).
- **Pre-Round-3 increment — session history (shipped 2026-04-24):** `internal/session/` ships a workstream-scoped, append-only event log: `Store` interface (`Append` + `Read`), a `SessionEvent` shape with roles that mirror the LLM provider API (`system`|`user`|`assistant`|`tool`) so events replay into a provider's messages array without translation, plus `InMemoryStore` and `FileStore`. File layout: `<root>/<workstream-id>/events.yaml` as a multi-document YAML stream, appended via `O_APPEND` + `fsync` on the OS-backed FS (`specio.OSFS.AppendFile` and `specio.MemFS.AppendFile` added). Corrupt trailing documents are logged and skipped. The design is Locutus-native enough that no `NOTICE` attribution is required — ADK's event-log-per-session shape was inspiration, not code. Tracked in [`.claude/plans/gap-closeout-pre-round3-session-history.md`](../.claude/plans/gap-closeout-pre-round3-session-history.md).
- **Round 4 — `llm_review` via eval framework (shipped 2026-04-24):** `internal/eval/` ships an `Evaluator` interface, an `EvalCase` / `EvalMetric` pair, and a `Runner` keyed by `spec.AssertionKind` — adapted from adk-python's evaluation framework, dropping its `EvalSet` / `Invocation` abstractions which assume a chat-turn runtime Locutus does not have. The MVP evaluator is `LLMJudge`, registered for `AssertionKindLLMReview`; future evaluators (safety, latency, multi-judge consensus) bind against new `AssertionKind`s without a central switch. New agent: [`internal/scaffold/agents/llm_judge.md`](../internal/scaffold/agents/llm_judge.md), narrow per-assertion judge with structured JSON output (passed / reasoning / confidence). `cmd/adopt_assertions.go`'s pass-with-note stub is gone; `runAssertions` now takes `(ctx, approach, repoDir, *eval.Runner, specio.FS)` and routes `llm_review` through the runner. Per-file size cap (64 KiB default, configurable per evaluator) prevents large artifacts from blowing context. Tracked in [`.claude/plans/gap-closeout.md`](../.claude/plans/gap-closeout.md) Round 4.
- **Rounds 3, 5–8:** unchanged in scope; memory and session-history primitives become available to later rounds as optional inputs.

**Supersession scope relative to DJ-029:**

DJ-029 remains in force for wholesale-framework adoption of LangGraphGo/LangChainGo — those specific rejections still apply. DJ-077 narrows DJ-029's "custom orchestration" implication: *selective* adoption of individual patterns/packages from well-maintained, license-compatible external frameworks is permitted when the alternative is reinventing the same abstraction. Future decisions about adopting other parts of ADK (or alternative frameworks) re-evaluate under this posture.

**Not in scope for DJ-077:**

- Wholesale migration to ADK runtime (executor, runner, session).
- Any commitment to port beyond the memory + evaluation surfaces. Each future adoption is its own decision.
- Multi-provider translation adapter for `genai.Content`. If we ever want adk-go's runtime, that adapter is a prerequisite; today we don't.
