## DJ-143: Per-Runtime, Per-Mode Tool-Exposure Policy via `tools.yaml`; Runtime Identified at MCP `initialize` via `ClientInfo.name`; Mode Carried Through `LOCUTUS_MODE` Env Var → `_meta["locutus.mode"]` Forward; Daemon Filters `tools/list` and `tools/call` Per-Session — Follow-Up to DJ-142

**Status:** settled (designed 2026-05-28; no code yet). Follow-up to [DJ-142](dj-142-idiomatic-convergence-drivers.md), which shipped the three idiomatic convergence drivers (`/goal` for Claude Code interactive; `OuterLoopRunner` for all headless; `spec_loop_*` daemon tools for Codex/Gemini interactive self-loop) but registered the `spec_loop_*` tools unconditionally, so Claude Code interactive sessions saw them too.

**Context.** DJ-142 unified the convergence story across three runtime + mode contexts. The `spec_loop_begin` / `spec_loop_status` / `spec_advance_iteration` MCP tools were added on the daemon to give Codex/Gemini interactive runtimes a server-side loop counter for their self-loop tier-3 playbook (`spec_refinement.interactive.md`). Claude Code interactive uses `/goal` instead and is **not** supposed to touch those tools — its published slash-command wrapper (`spec_refinement.claude-code.interactive.md`) explicitly tells the agent *"do not loop manually; the evaluator owns the iteration cadence."*

The tools were registered globally on the daemon at startup, so they appeared in every connecting client's `tools/list` regardless of runtime. The tools' registered `Description` strings start with *"Begin (or recover) an interactive self-loop run…"* — language that reads to any interactive-session agent as an invitation to use them. The concrete failure surfaced on the first real winplan run after DJ-142 shipped: an operator invoked `/locutus-refine` inside Claude Code. The model received the `/goal`-wrapper slash-command body ("do not loop manually") AND saw `spec_loop_*` in its tool list with "interactive self-loop run" in the descriptions, called `spec_advance_iteration`, got back `iteration: 1/20, continue: true`, and then resolved the contradiction by deferring to the wrapper's explicit "evaluator owns cadence" directive — stopping after one iteration, waiting for an evaluator that the prior session-state had already shown can drive the loop correctly when `spec_loop_*` isn't in the way.

The root cause is not in `/goal`, the wrapper, or the playbook — each is correct under its intended `(runtime, mode)`. The defect is that **DJ-142's loop tools are exposed on an axis that doesn't match their intended consumer**, with no mechanism on the daemon to scope tool exposure per connecting client.

DJ-143 adds that mechanism: a small `tools.yaml` policy, per-session runtime/mode identification at MCP `initialize` time, and a daemon-side filter at both `tools/list` and `tools/call`. The same mechanism is reusable for any future runtime-scoped tool — `spec_loop_*` is the first consumer, not a special case.

**Decision.** A central `tools.yaml` config file + ClientInfo-based runtime identification + env-var-based mode signal + per-session filtering on the daemon. No new node kinds, no new MCP-spec extensions, no per-runtime MCP descriptor changes.

1. **`tools.yaml` schema — per-runtime, per-mode allow/deny cells with "deny wins" resolution.** Canonical at `internal/scaffold/tools.yaml` (embedded into the binary), project-override at `.borg/tools.yaml` (emitted by the publisher on first `locutus init`, preserved across `locutus update --reset`). Schema:

   ```yaml
   claude-code:
     headless:    { allow: ["*"], deny: [] }                    # harness OuterLoopRunner drives
     interactive: { allow: ["*"], deny: ["spec_loop_*"] }       # /goal drives; spec_loop_* derails it
   codex:
     headless:    { allow: ["*"], deny: [] }                    # harness drives
     interactive: { allow: ["*"], deny: [] }                    # agent self-loops via spec_loop_*
   gemini:
     headless:    { allow: ["*"], deny: [] }
     interactive: { allow: ["*"], deny: [] }
   ```

   Resolution is "deny wins" — `allow: ["*"]` + an explicit deny entry blocks just that tool. Wildcards in deny entries use a glob-suffix match (`spec_loop_*` matches `spec_loop_begin`, `spec_loop_status`, `spec_advance_iteration`, and any future `spec_loop_*` addition). Unknown runtime / unmapped mode → allow-all (operator-experimenting friendly; an unknown MCP client isn't locked out by the policy).

2. **Runtime identification at MCP `initialize` via `ClientInfo.name`.** The MCP protocol's `initialize` exchange already carries `clientInfo.name` — Claude Code, Codex, and Gemini each identify distinctly. The daemon's `initialize` handler reads it, normalizes (lowercase, trim), and stores it on the session. No new MCP-spec extension; using a field the protocol already requires every client to send.

3. **Mode carried through `LOCUTUS_MODE` env var, forwarded via `_meta["locutus.mode"]`.** MCP's `initialize` carries no mode field, so the path is:
   - **ACP harness** (`internal/runner/loop.go`, headless-dispatch path) sets `LOCUTUS_MODE=headless` in the env it passes to the spawned `locutus mcp` bridge subprocess.
   - **Bridge** (`cmd/mcp.go`) reads `os.Getenv("LOCUTUS_MODE")` once at startup. Empty/unset → `"interactive"` (the operator-typed-slash-command default). Forwards the resolved value to the daemon as `_meta["locutus.mode"]` on its `initialize` request — MCP allows arbitrary `_meta` fields and the `locutus.` namespace prefix avoids collision with any future MCP-spec field.
   - **Daemon** `initialize` handler reads `_meta["locutus.mode"]` alongside `clientInfo.name`, normalizes, stores `{Runtime, Mode}` on the session.

   No CLI-surface change to `locutus mcp`. No per-runtime MCP-descriptor change — every runtime's published descriptor still invokes the same bridge binary, and operator-typed sessions get the bridge's default (`interactive`).

4. **Daemon-side filtering at `tools/list` and `tools/call`.** Both gates are wired:
   - `tools/list` is filtered per-session: the response includes only tools the session's `(runtime, mode)` is allowed to see. Denied tools simply do not appear in Claude Code's list — no temptation, no token cost on the agent's tool-picker. If the go-sdk's session model doesn't support per-session list overrides, the equivalent is per-session tool registration at `initialize` time.
   - `tools/call` is the defense-in-depth backstop: every tool's handler is wrapped in a policy guard that checks the calling session's `(runtime, mode)` before delegating. A denied call returns an MCP error naming the runtime, the mode, the tool, and the docs reference: `"tool 'spec_loop_begin' is not exposed to runtime 'claude-code' (mode=interactive); see docs/runtime-affordances.md § Tool-Policy"`.

5. **One new package, two new files, three small edits.** Total code surface:
   - **New:** `internal/tools/policy.go` (`LoadPolicy`, `Policy.IsAllowed`), `internal/scaffold/tools.yaml` (canonical embed), `internal/mcp/session_context.go` (`initialize` hook + session-metadata accessor).
   - **Modified:** `internal/mcp/server.go` (wire the `tools/list` filter and the `policyGuard` adapter on `AddTool`), `cmd/mcp.go` (read `LOCUTUS_MODE`, forward via `_meta`), `internal/runner/loop.go` (set `LOCUTUS_MODE=headless` on the spawned bridge env), publisher (emit `.borg/tools.yaml` from the canonical embed on first init).
   - **Doc:** a "Tool-Policy" passage in `docs/runtime-affordances.md` covering canonical defaults, override file, deny-message shape, and the restart-to-reload posture.

6. **No hot-reload of the policy.** Operators edit `.borg/tools.yaml` and restart the daemon (`locutus mcp-stop` + next connect re-forks). Hot-reload would add a mechanism with no real consumer — the policy file changes maybe quarterly, the daemon restarts instantly, and a stale-policy bug under hot-reload would be far harder to diagnose than a "did you restart the daemon" check.

7. **Operational cleanup for the DJ-142 leak.** The diagnostic winplan run advanced the daemon's in-memory loop counter for `(Claude Code session, spec_refinement, goals)` to iteration 1. Before DJ-143 lands, an operator restarting the daemon clears it. After DJ-143 lands, the same scenario can't recur — Claude Code interactive sessions will never see `spec_loop_*` in their tool list.

**Resolved design questions** (chat 2026-05-28):

1. **Filter mechanism: detect at MCP `initialize` via `ClientInfo.name` (option A), not a bridge-passed `--runtime` flag (option B) and not a call-time-only guard (option C).** Rejected B as parallel-with-publisher symmetry that doesn't pay for the extra bridge + publisher machinery — the MCP protocol already carries client identity at session start, and the daemon is a local socket where bridge-vs-protocol identification has no security delta. Rejected C as half-measure that lets the wrong tools stay in `tools/list`, costing tokens and inviting the agent to call them before the deny fires.

2. **Policy lives in a dedicated `tools.yaml` (server-level config), not in playbook frontmatter.** Tools are cross-activity — `spec_loop_*` is wrong for Claude Code regardless of which activity the agent is running. Frontmatter would duplicate the rule across every playbook and force re-declaration when a new activity ships. Server-level config has one place to declare, audit, and extend.

3. **Schema is explicit per-(runtime, mode), with wildcards in deny entries.** Rejected a leaner runtime-only schema (deferring mode until a real consumer emerges) — the schema's job is forward-compatibility for the next runtime-scoped tool, and explicit cells are self-documenting. Wildcards earn their keep: `spec_loop_*` as one entry covers the family today and inherits any future `spec_loop_*` addition without re-editing every runtime cell.

4. **Defaults for unmapped cells: allow-all.** A connecting client whose `ClientInfo.name` isn't in the file (operator experimenting with a new MCP client), or a known runtime with an unmapped mode, gets all tools. Rejected deny-all-by-default as hostile to operators experimenting with new MCP clients; we can tighten later if a real consumer emerges, per [[feedback-no-aspirational-fields]].

5. **Mode signal: `LOCUTUS_MODE` env var on the bridge, forwarded to the daemon via `_meta["locutus.mode"]`.** Rejected bridge `--mode` CLI flag (more explicit but grows the bridge's CLI surface and requires updating every spawner; same end-state semantics). Rejected MCP-init-meta-only with no env or flag (defers the question of where the bridge learns the mode in the first place).

6. **Project file fully replaces canonical (no merge).** Same posture as `.borg/agents.yaml`: simple, predictable, easy to audit. Operators with a one-off requirement edit `.borg/tools.yaml` and accept that the next reset preserves their file (resets do NOT overwrite the project copy). Rejected merge-canonical-into-project semantics as a complexity tax with no real consumer.

7. **Filter both `tools/list` and `tools/call` (defense-in-depth).** `tools/list` is the primary mechanism — denied tools don't appear, so the agent never sees the temptation and pays no token cost. `tools/call` is the backstop — if a client somehow constructs a denied call (cached list, manual request), the daemon rejects with a documented error. Rejected `tools/call`-only enforcement (leaks the existence of denied tools through `tools/list`) and `tools/list`-only enforcement (no backstop against cached lists).

**Alternatives considered:**

- **Bridge-passed `--runtime` flag + matching publisher emit.** Considered (option B) for symmetric "(runtime, mode) flows the same channel as the publisher's per-runtime dispatch." Rejected per RQ1 — the parallel was driven by symmetry-for-symmetry's-sake rather than what works; MCP already carries runtime identity at session start, and the daemon's local-socket context has no adversary against which an explicit-flag would be more authoritative than a protocol-level field.

- **Per-playbook frontmatter `filter:` field.** Considered as an alternative to `tools.yaml`. Rejected per RQ2 — tools are cross-activity; the rule would have to be duplicated across every playbook that the wrong-runtime agent might invoke, and would force re-declaration on every new activity. Server-level config is the natural home for server-level policy.

- **Call-time guard only (no `tools/list` filter).** Considered as the simplest possible mechanism. Rejected per RQ7 — denied tools would still appear in the agent's tool-picker, costing tokens and inviting calls before the deny fires.

- **Hot-reload of `tools.yaml`.** Considered for ops ergonomics. Rejected per Decision §6 — no real consumer for hot-reload, and the daemon-restart posture is simpler and harder to bug.

- **Per-runtime tool naming** (e.g., `claude_spec_loop_begin` vs `codex_spec_loop_begin`). Rejected — breaks the single-daemon design and forces the playbook prose to know which name to use per-runtime; the policy approach keeps tool names stable and runtime-agnostic.

**Consequences:**

- **Code (add):**
  - `internal/tools/policy.go` — policy loader + `IsAllowed` matcher with glob-suffix wildcard support; load-time validation rejects malformed cells.
  - `internal/scaffold/tools.yaml` — canonical defaults embedded into the binary.
  - `internal/mcp/session_context.go` — `initialize` hook reads `clientInfo.name` + `_meta["locutus.mode"]`, normalizes, stores on session; one accessor for the filter to consume.
- **Code (modify):**
  - `internal/mcp/server.go` — wire `tools/list` per-session filter + `policyGuard` adapter on every `AddTool`.
  - `cmd/mcp.go` — read `LOCUTUS_MODE` env once, forward via `_meta["locutus.mode"]` on `initialize`; debug-log the resolved `(runtime, mode)` so an operator inspecting logs sees what the daemon received.
  - `internal/runner/loop.go` (and the ACP-dispatch wrapper) — set `LOCUTUS_MODE=headless` in the env passed to the spawned `locutus mcp` bridge.
  - Publisher (`internal/publisher/canonical.go` or equivalent) — emit `.borg/tools.yaml` from the canonical embed on `locutus init`; preserve on `locutus update --reset` (do not overwrite operator-edited project file).
- **Code (test):**
  - Policy unit tests: deny wins; glob-suffix wildcards; unknown runtime + unmapped mode → allow; malformed config → load error.
  - Session-context test: `initialize` correctly extracts `runtime` + `mode` from `clientInfo.name` + `_meta`; missing mode → `"interactive"` default.
  - Integration test (in-memory MCP transports): two simulated sessions (`claude-code` interactive + `codex` interactive), `tools/list` returns different lists, `tools/call` on a denied tool errors with the documented message naming runtime/mode/tool/docs-ref, `tools/call` on an allowed tool succeeds.
- **Docs:** this entry; [DECISION_JOURNAL.md](../DECISION_JOURNAL.md) manifest row; a "Tool-Policy" passage in [docs/runtime-affordances.md](../runtime-affordances.md) covering canonical defaults, the `.borg/tools.yaml` override, the deny-message shape, and the restart-to-reload posture. [CLAUDE.md](../../CLAUDE.md) gains one paragraph in Sources of Truth describing the per-runtime tool policy.
- **Validation:** end-to-end against winplan — after the operator runs `locutus update --reset` (publisher emits `.borg/tools.yaml`) and restarts the daemon (`locutus mcp-stop`), running `/locutus-refine` inside Claude Code interactive shows `spec_loop_*` absent from `tools/list` (visible in the bridge's debug log). The `/goal` evaluator drives the loop correctly without `spec_loop_*` distraction. A parallel test from a Codex interactive session against the same daemon shows `spec_loop_*` present in its `tools/list` and the agent self-loops correctly.
- **Follow-ups (separate DJs, tracked here):**
  - **DJ-144 placeholder — Provider vs runtime naming audit.** The codebase mixes provider names (`anthropic` / `googleai` / `openai`) — semantically correct in `models.yaml` and agent frontmatter `models:` arrays where the axis is model-selection — with runtime names (`claude-code` / `codex` / `gemini`) — semantically correct in publisher dispatch, MCP client identification, and tool policy where the axis is the runtime binary. The two axes happen to align 1:1 today but aren't the same thing. A separate grep-and-doc DJ audits for mis-axis usage and pins the distinction in `docs/agent-conventions.md`.
  - **DJ-145 placeholder — Cross-runtime interactive delegation.** Today `agents-default.yaml`'s per-activity runtime mapping only governs the headless ACP-dispatch path; interactive sessions run in the host runtime regardless. A separate architectural DJ weighs whether interactive sessions should support cross-runtime delegation (e.g., a Claude Code session spawning a Codex ACP run for a specific activity), or whether the current "host runtime for interactive" semantics are the deliberate end-state. DJ-143's filtering scales cleanly to either resolution — a spawned cross-runtime session presents as its own runtime identity at MCP-connect.
