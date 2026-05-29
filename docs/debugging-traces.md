# Debugging Locutus Sessions

When `locutus refine`, `locutus import`, `locutus adopt`, or `locutus assimilate` produces unexpected output — the spec graph doesn't move, decisions don't commit, the agent stalls — the session trace under `.locutus/sessions/` is the load-bearing artifact for diagnosis. This guide walks the post-DJ-135 trace layout, names common failure patterns, and points at the one-liners that get you to the relevant event quickly.

The trace shape changed meaningfully under DJ-135. Prior versions of this doc described per-step folders with `step.yaml` parents and per-SDK-call YAML children — that was the in-process council's trace, which retired with the council. The new shape captures the ACP event stream from the coding agent's session; the agent's *reasoning* (chain of thought, internal subagent dispatches) lives in *its* session log (Claude Code's `~/.claude/projects/<project-hash>/`, etc.), not in Locutus's directory.

## Where the trace lives

Every activity-dispatching CLI verb opens one session directory under `.locutus/sessions/`:

```
.locutus/sessions/
  20260525/                       # YYYYMMDD
    0707/                         # HHMM
      310000/                     # 6-digit suffix from time-of-second
        playbook.md               # initial user message delivered to the agent
        events.jsonl              # full ACP event stream, one JSON line per event
        tools.jsonl               # filtered tool_call + tool_result events
        output.md                 # agent's final text output (concatenated EventText)
```

That's it. Four files per session. No nested per-step folders, no per-SDK-call YAMLs — the runtime's own session log carries the per-LLM-call detail if you need it.

### Inventory

- **`playbook.md`** — the exact playbook body delivered as the agent's initial user message, including any per-invocation context (`--target`, `--scope`, import content). First read this to confirm the playbook reached the agent intact.
- **`events.jsonl`** — every ACP event the dispatcher observed during the session, in order. Each line is a JSON-encoded `dispatch.AgentEvent` with `Kind`, `Timestamp`, `SessionID`, `ToolName`, `ToolInput`, `Text`, `FilePaths`, and a `Raw` field carrying the underlying ACP notification.
- **`tools.jsonl`** — same shape as events.jsonl, filtered to `Kind: "tool_call"` and `Kind: "tool_result"` events only. Fast scan for "did the agent ever call X?"
- **`output.md`** — every `EventText` event's text concatenated, in order. Equivalent to the agent's final assistant message(s).

## Finding the right session

By timestamp — the session directory's name encodes the start time. Most-recent session today:

```bash
ls -td .locutus/sessions/$(date +%Y%m%d)/*/* | head -1
```

By verb — every dispatcher run logs its session dir to stderr on completion (`→ session: /path/to/dir (runtime=claude-code)`). Grab from your terminal scrollback or shell history.

By outcome — sessions where the spec graph didn't change are usually the failing ones. The on-disk spec under `.borg/spec/` is the source of truth for what landed; if `git status .borg/spec/` shows no diff after a run, the agent either didn't commit anything or got partway through before the run ended.

## Common failure patterns

### Agent never calls `mcp__locutus__spec_*` tools

Symptom: `tools.jsonl` contains only `Bash`, `Read`, `ToolSearch`, `WebSearch` events — no `mcp__locutus__*` calls.

Likely causes:
- The MCP server didn't attach. Check that `.mcp.json` exists at the project root and points at `locutus mcp`. Confirm `.locutus/mcp.sock` was created during the run (best evidence: the file's mtime falls within the run window; `stat` it).
- The agent went exploratory-first. The current playbook (`internal/scaffold/plans/spec_refinement.md`) opens with an explicit "Your very first action is to call `mcp__locutus__spec_list_manifest`" directive. If a custom playbook doesn't have that, the agent often explores filesystem before noticing the MCP tools.

One-liner to confirm MCP attached:

```bash
grep -c 'mcp__locutus__' .locutus/sessions/<date>/<time>/<sid>/tools.jsonl
```

Zero means the tools never got called. Non-zero means the attachment worked and the issue is elsewhere.

### Tool call validation errors

Symptom: `tools.jsonl` shows `tool_call_update` events with `status: "failed"` for the `mcp__locutus__spec_propose_*` tools.

The error message is in the event's content. Common ones:
- `validating "arguments": validating root: required: missing properties: ["X"]` — the agent omitted a required field. Per DJ-135 ckpt 4 we loosened the schema for `axes` and `surfaced_by` on decisions (server auto-backfills); other required fields are genuinely needed.
- `id "<bad-id>" is malformed: expected kebab-case with prefix goal-, agoal-, feat-, strat-, dec-, bug-, or app-` — the agent invented an id that doesn't match the regex. Usually means the agent didn't read the canonical id convention; tighten the elaborator subagent's prompt to name the convention explicitly. The `goal-` / `agoal-` prefixes are DJ-139's addition.
- `id "<wrong-prefix>" lacks <expected>- prefix for Kind<X>` — agent used the wrong prefix. Same fix.

Extract failed-tool errors quickly:

```bash
python3 -c "
import json
for line in open('.locutus/sessions/<sid>/events.jsonl'):
    e = json.loads(line)
    u = e.get('Raw', {}).get('update', {})
    if u.get('status') == 'failed':
        for c in u.get('content', []):
            t = (c.get('content', {}) if isinstance(c.get('content'), dict) else {}).get('text', '')
            if t: print(t[:300])
"
```

### Spec graph never advances past iteration 1

Symptom: `tools.jsonl` shows the scout subagent dispatched once, candidate-surveys fanned out, but no `spec_propose_decision` calls landed.

Likely causes:
- The candidate-survey subagents are doing heavy grounding (web search) and the timeout fires before they return. The pre-DJ-135 council had a hard 5-minute budget per workflow phase; the new model has whatever timeout the CLI is bound to. Try a simpler GOALS.md (fewer axes), or live with a longer wall-clock.
- The decision-elaborator subagent returned a body the playbook didn't notice. Check the Task tool result event for the elaborator dispatch — if the returned body is well-formed but the orchestrator's next action isn't a `spec_propose_decision`, the playbook's prose isn't clear enough about the propose-after-elaborate step.

### Goal-layer sync didn't apply expected changes

Symptom (DJ-139): `tools.jsonl` from a `locutus refine goals` run shows `spec-goal-diff-matcher` dispatched, returned a diff, but the corresponding `spec_propose_goal` / `spec_revise_goal` / `spec_delete_goal` (or AntiGoal variants) calls are missing or partial.

Useful tool-call shapes to grep for:

- `mcp__locutus__spec_propose_goal` / `mcp__locutus__spec_propose_antigoal` — Step 0 commits for new goal-layer nodes.
- `mcp__locutus__spec_revise_goal` / `mcp__locutus__spec_revise_antigoal` — Step 0 commits when an existing node's `source_clause` or `body` shifts.
- `mcp__locutus__spec_delete_goal` / `mcp__locutus__spec_delete_antigoal` — Step 0 commits when a goal-layer node has no corresponding claim in current `GOALS.md`.
- `mcp__locutus__spec_update_goals_md_hash` — Step 0's closing call. Missing this call means the next `refine goals` run won't short-circuit even when `GOALS.md` is unchanged.

Likely causes when the diff was returned but not applied:

- The matcher's response shape didn't match what the playbook expects. Look at the `Task` tool result event for the matcher dispatch; the response is structured JSON with `unchanged` / `modified` / `deleted` / `added` arrays. If those keys are missing or named differently, the orchestrator can't walk the diff.
- The hash-update tool call was skipped. Without `spec_update_goals_md_hash`, the goal layer mutates but the manifest's `goals_md_hash` stays stale; the next run re-dispatches the matcher unnecessarily and may re-apply equivalent changes.

Citation-walk tool calls (Step N+1) appear as `spec_revise_decision` / `spec_revise_feature` / `spec_revise_strategy` invocations where the body fields stay identical to the prior settled state and only `advances` / `respects` arrays change. Grep `tools.jsonl` for those tools with input payloads containing `"advances":` or `"respects":` to isolate the citation-walk subset.

### Loop never converges (20-iteration cap fires)

Symptom: `output.md` reports the iteration cap fired with axes still open or concerns still active.

Per the convergence-by-construction discipline in `internal/scaffold/plans/spec_refinement.md`:
- If a concern recurs across two iterations with no new evidence, the playbook says to flip it to `wontfix`. If you see the same concern texts iterations 1, 2, 3, … the orchestrating agent isn't applying the wontfix discipline. Tighten the playbook prose.
- If axes are still open at the cap, the scout isn't surfacing them as decided. Check `tools.jsonl` for the scout's `axes_open` output across iterations — if it's the same axis names every time, the elaborator path isn't committing decisions for them (see "Spec graph never advances" above).

Under DJ-140 the headless outer loop is harness-driven for all three runtimes (Claude Code, Codex, Gemini) — they trace identically. Locutus's runner emits one progress line per iteration transition (`→ iteration N of 20`) and an explicit `✗ iteration ceiling reached` when the cap fires. Each iteration's session lives under its own `.locutus/sessions/<date>/<time>/<sid>/`; the runner re-dispatches across separate ACP sessions so per-iteration archives are independent and can be walked individually.

There is no `/goal` evaluator log to cross-reference: DJ-136's `/goal`-driven Claude Code path was retired for headless dispatch (`/goal` is interactive-only and unavailable over `claude-agent-acp`). `/goal` reasoning appears only when an operator drives `/locutus-refine` from inside an interactive Claude Code session, in which case it lives in the runtime's own session log under `~/.claude/projects/...` — not in any Locutus-dispatched session.

### Hook denial blocks a tool call

Symptom (codex / gemini only): a `spec_propose_decision` call surfaces as a hook denial in the agent's session, often with a "decision id … must start with `dec-`" reason.

DJ-136 phase 5 lands the `locutus hook-validate-decision` subcommand wired up via `.codex/config.toml` `[[hooks]]` or `.gemini/settings.json` hooks array. The hook reads the proposed decision input and rejects malformed ids before the MCP write tool fires. The structured reason on stderr is the operator's evidence — fix the elaborator's id-shape adherence (DJ-133: `dec-<axis-id>`) and re-run. Claude Code doesn't ship this hook fragment; under DJ-140 headless dispatch its convergence is still the same Locutus outer loop, and id-shape adherence rests on convergence-by-construction discipline at the playbook layer plus the MCP write tool's own validation.

### Upstream / network errors mid-stream

Symptom: `events.jsonl` ends with an `EventError` event containing something like `Internal error: API Error: Unable to connect to API (ECONNRESET)`.

Not architectural — upstream API hiccups happen. Re-run usually succeeds. If they recur consistently, check the runtime's own session log (Claude Code's `~/.claude/projects/...`) for additional error context.

## Claude Code workflow runs (DJ-144): coarser ACP event stream, intact daemon-side audit

Under DJ-144, Claude Code converges via an in-runtime dynamic workflow for the four convergent activities (`spec_refinement`, `feature_ingestion`, `code_adoption`, `code_assimilation`). The workflow's internal subagent calls and intermediate results stay in the background runner's own context and **do not surface as ACP `tool_call` events** — only the final answer returns to the conversation context Locutus observes. So `events.jsonl` becomes coarser for Claude Code workflow runs than for Codex/Gemini (where each iteration is a fresh ACP session whose tool calls all surface).

The **spec-mutation audit trail stays intact daemon-side**: every `mcp__locutus__spec_*` write still routes through the per-project MCP daemon and is recorded in `tools.jsonl` and as history events. So *what changed in the graph* remains fully auditable even though *the agent's intermediate reasoning* is less visible. When debugging a Claude Code workflow run, prefer the daemon-side tools log over the ACP event stream for "what was committed and why" questions; reach for the runtime's own session log (below) for "what was the agent thinking" questions.

Codex/Gemini are unchanged — their iterations are still ACP sessions and their tool calls still surface in `events.jsonl` per the existing patterns.

## Cross-referencing with the runtime's session log

Locutus captures the ACP event stream — what the dispatcher *observed*. The full story (the agent's reasoning, its prompt-engineering choices, sub-prompts to subagents) lives in the runtime's own session log:

- **Claude Code** — `~/.claude/projects/<sanitized-project-path>/<session-id>.jsonl`. The session id is the same id Locutus's events log carries on every event's `SessionID` field.
- **Codex** — TBD (validated empirically when Codex empirical run lands).
- **Gemini CLI** — TBD.

Cross-reference: grab the SessionID from any line in Locutus's `events.jsonl`, then grep the runtime's session directory for that id.

## Useful one-liners

```bash
# How many MCP tool calls did the agent make?
wc -l .locutus/sessions/<sid>/tools.jsonl

# Which MCP tools did it call, with counts?
python3 -c "
import json, collections
counts = collections.Counter()
for line in open('.locutus/sessions/<sid>/events.jsonl'):
    e = json.loads(line)
    if e['Kind'] != 'tool_call': continue
    cc = e.get('Raw', {}).get('update', {}).get('_meta', {}).get('claudeCode', {}).get('toolName', '')
    if cc: counts[cc] += 1
for k, v in counts.most_common():
    print(f'{v:4d}  {k}')
"

# Show every propose/revise tool call's arguments (the agent's actual decision body).
python3 -c "
import json
for line in open('.locutus/sessions/<sid>/events.jsonl'):
    e = json.loads(line)
    u = e.get('Raw', {}).get('update', {})
    cc = u.get('_meta', {}).get('claudeCode', {}).get('toolName', '')
    if cc and 'propose' in cc or 'revise' in cc:
        if u.get('sessionUpdate') == 'tool_call':
            print(cc)
            print(json.dumps(u.get('rawInput', {}), indent=2)[:1000])
            print('---')
"

# What were the agent's free-text outputs? (the orchestrator's narration)
grep '"Kind":"text"' .locutus/sessions/<sid>/events.jsonl | python3 -c "
import json, sys
for line in sys.stdin:
    e = json.loads(line)
    t = e.get('Text', '')
    if t: print(t)
"
```

## What NOT to do

- **Don't read events.jsonl as a single object.** It's JSON Lines — one object per line. `jq -s` or `python3 -c 'json.load(...)'` will fail; use line-by-line parsing.
- **Don't infer agent state from `output.md` alone.** Output is the agent's final assistant text. Tool calls and tool results happen in the middle of the conversation and don't show up there — always cross-reference with `tools.jsonl`.
- **Don't trust LLM-side `WorkflowEvent` semantics that show up in old code or DJs.** The pre-DJ-135 council emitted typed workflow events that the dispatcher mapped to specific sinks. That whole layer retired; the only events in the new path are the raw ACP events captured here.
