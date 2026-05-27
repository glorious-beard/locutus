## DJ-094: Spec-Lookup Tools for the Reconciler + Per-Round Tool-Use Capture

**Status:** shipped

**Decision:** Two changes ship together:

1. **`spec_list_manifest` and `spec_get` tools** are registered against the Genkit runtime so the `spec_reconciler` agent can navigate the persisted spec lazily instead of receiving the entire `ExistingSpec` snapshot inlined into its prompt. The reconciler's frontmatter declares `tools: [spec_list_manifest, spec_get]`; the GenKit Generate path passes them via `ai.WithTools` when `req.Tools` is non-empty.

2. **Per-round tool-use capture** in the session trace. The middleware accumulates one `GenerateRound` snapshot per model invocation inside Genkit's tool-dispatch loop and surfaces them as `Rounds []recordedRound` on the per-call YAML. Single-round calls leave `Rounds` empty (the top-level `Reasoning`/`Response`/`RawMessage` carry that round's data); multi-round calls record every round so an operator can see what the model asked the tools to do, not just the final response after the loop completed.

**Tools surface:**

- `spec_list_manifest()` → returns a compact index grouped by kind (features, strategies, decisions, bugs, approaches). Each entry has `id`, `title`, `kind` (strategies only), and a `summary` collapsed to one line and truncated to 200 runes. Computed on-demand from `.borg/spec/<kind>/*.json` directory listings — no persisted manifest file. The spec directory IS the manifest per DJ-068.
- `spec_get(id)` → returns the raw JSON of one spec node by id. Kind is inferred from the id prefix (`feat-`, `strat-`, `dec-`, `bug-`, `app-`); approaches return their markdown body wrapped as a JSON string. Unknown prefix errors with a clear message; missing id surfaces the underlying read error.

Both are pure reads against `specio.FS`. Greenfield runs (no `.borg/spec/`) return empty manifests rather than errors — tools must be safe to call when the reconciler has nothing to look up.

**Why no persisted manifest file.** The user asked whether `.borg/manifest.json` should be populated since it's empty in real projects. DJ-081 already pins that file as the project-root marker (`{project_name, version, created_at}`); DJ-068 establishes that `.borg/spec/` IS the manifest. Adding a derived `.borg/spec/manifest.json` that we have to keep in sync with disk creates a drift surface for no benefit — `ListDir` is fast, the JSON files are small, and reading them at tool-call time is bounded by the reconciler's actual lookup pattern rather than total spec size.

**Why no persisted file is the right answer for now.** If we ever ship a `Genkit` runtime that talks to remote MCP servers, a persisted manifest becomes load-bearing (the remote can't `ListDir` cheaply). Today's runtime is in-process; the on-demand path is correct.

**Hard provider constraint.** On Gemini, attaching tools silently disables API-level JSON mode (`plugins/googlegenai/gemini.go:311` — `if hasOutput && len(input.Tools) == 0`). The reconciler's `output_schema: ReconciliationVerdict` still injects the schema as system-prompt documentation, but the API doesn't enforce conformance when tools are attached. The defensive mitigation is `stripJSONFences` in `mergeReconcile` — Gemini wraps its output in ```` ```json ... ``` ```` out of training-distribution habit when JSON mode is off, and the parser would reject the wrapping. The fence-stripper trims it before `json.Unmarshal`. If the model produces malformed JSON beyond a fence wrap, the reconciler's verdict parse fails and surfaces as a workflow error — same path as today.

The Gemini constraint also means tools and `grounding: true` (DJ-093) cannot coexist on the same agent. Not a collision in our council: the scout uses grounding (no tools); the reconciler uses tools (no grounding).

**Why per-round capture ships in this DJ.** Without per-round capture, today's middleware overwrites `capturedText`/`capturedReasoning`/`capturedMessageRaw` on each model invocation. In a multi-round tool-use loop, only the **final round** survives in the trace — the model's actual `tool_request` blocks from earlier rounds are silently lost. The reconciler is the first and only consumer of multi-round tool-use today; without per-round capture, the first real tool-using run would produce a trace that hides exactly the calls an operator would want to debug. The fix is small (~30 lines) and keeps the trace's debugging value intact as the council's tool surface grows.

The capture shape: `recordedRound { Index, Reasoning, Text, Message, InputTokens, OutputTokens, ThoughtsTokens }`. `Message` is the JSON-serialised `*ai.Message` for that round, containing every part (text, reasoning, `tool_request`). Tool **response** payloads (what the runtime returned to the model) appear as input messages on the **next** round; an operator can reconstruct the conversation by reading rounds in order. Capturing tool-response payloads explicitly is a follow-up if it proves load-bearing for debugging.

**What's new:**

- `internal/agent/spec_tools.go`: `SpecManifest`, `SpecManifestEntry`, `BuildSpecManifest(fsys)`, `LookupSpecNode(fsys, id)`, `RegisterSpecTools(g, fsys)`. Constants `ToolNameSpecListManifest`, `ToolNameSpecGet`. The pure functions are testable with MemFS without going through Genkit registration.
- `AgentDef.Tools []string` field with frontmatter tag `yaml:"tools,omitempty"`.
- `GenerateRequest.Tools []string` and `GenerateRequest.Rounds []GenerateRound`. `BuildGenerateRequest` threads `def.Tools` through.
- `GenKitLLM.Genkit()` accessor exposes the runtime so tool registration can run after `NewGenKitLLM`.
- `cmd/llm.go.recordingLLM` calls `registerSpecToolsOnce` after `getLLM()` so tools are bound to the same `fsys` the rest of the command operates on.
- `GenKitLLM.Generate` attaches `ai.WithTools(toolRefs...)` when `req.Tools` is non-empty (using `ai.ToolName` to satisfy `ToolRef`).
- `captureMW` accumulates `GenerateRound` per invocation; the result is assigned to `out.Rounds` only when `len > 1`.
- `recordedCall.Rounds []recordedRound` field; `callHandle.finishAt` copies `resp.Rounds` into the per-call YAML.
- `mergeReconcile` strips markdown fences from the verdict via `stripJSONFences` before `json.Unmarshal`.
- `spec_reconciler.md` frontmatter declares `tools: [spec_list_manifest, spec_get]`; the prompt's "Existing spec snapshot" context section is replaced with a tool-usage hint pointing at the same lookup surface.
- `projectReconcile` no longer inlines `ExistingSpec.Decisions` into the prompt; emits a tool-usage hint sentence when an existing spec is present and nothing extra otherwise.
- New tests for: greenfield manifest, populated manifest, summary truncation, prefix routing, threading through AgentDef → GenerateRequest, frontmatter tools parsing, projection no-inline behavior, fence-stripper variants, end-to-end fenced-verdict tolerance, per-round persistence, single-round-omits-Rounds.

**What stays the same:**

- The reconciler's task (cluster inline decisions, emit verdict). Output schema unchanged.
- The cascade rewrite path on `resolve_conflict` actions.
- The session-trace per-call file layout (DJ-091); `Rounds` is a new optional field within the existing `recordedCall` shape.
- All other agents (scout, outliner, elaborators, critics, triager, architect) — none gain tool wiring. Only the reconciler.

**Reversal criteria:** revert if (a) Gemini's API rejects the responseSchema + tools combination at runtime in a way the fence-stripper can't accommodate (e.g. truncated mid-JSON before a closing fence) — at which point we'd drop `output_schema` for the reconciler entirely and parse loosely; or (b) the per-round capture inflates trace files past comfort on long tool-use loops — at which point we'd cap `Message` size per round or move multi-round captures to a sibling sidecar. Neither failure mode is structural.

**Reference:** plan at [.claude/plans/council-tools-and-revise-fanout.md](../.claude/plans/council-tools-and-revise-fanout.md), Phase 3. Per-round capture was folded in during implementation after the user flagged the trace-visibility gap.
