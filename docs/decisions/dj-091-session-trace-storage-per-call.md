## DJ-091: Session Trace Storage Is a Per-Call File Layout

**Status:** shipped

**Decision:** A session is a directory, not a file. `SessionRecorder` writes:

```text
.locutus/sessions/<YYYYMMDD>/<HHMM>/<SS>-<short>/
├── session.yaml          # manifest: session_id, started_at, completed_at, command, project_root
└── calls/
    ├── 0001-spec_scout.yaml
    ├── 0002-spec_outliner.yaml
    └── …                # one YAML file per LLM call
```

`Begin` writes the per-call file with `status: in_progress` and the input messages; `Finish` rewrites the same file with response/error/tokens/raw_message and drops the in-memory handle. Every flush is bounded to one per-call file (atomic via tmp + rename). The recorder no longer holds the cumulative `session.Calls[]` slice in memory — the directory listing IS the calls list. A new optional `Close` stamps `completed_at` on the manifest and flips any still-in-flight calls to `status: interrupted` for clean shutdown; sessions that crash without Close leave `completed_at` absent on disk, which itself is diagnostic.

**Why:** Three shipped changes turned a once-fine single-file format into a structural problem:

1. **Phase 3 fanout (DJ-090)** produces 15–25+ calls per session as a baseline. Adopt with dozens of workstreams could push this much higher. Single-file rewrite is O(N) per flush, total work O(N²) over a session.
2. **`raw_message` capture** added per-call YAML payloads of ~5–50KB each (truncated/looped Gemini outputs are the largest offenders). 17 calls × 10KB avg meant rewriting ~170KB on every state transition.
3. **Crash-mid-call ergonomics.** The session trace exists so an operator can debug LLM activity — prompt issues, tool-call traces, degenerate loops. A SIGKILL between Begin (input flushed to memory) and Finish (output captured) used to lose *exactly* the in-flight call most worth debugging. The atomic-rewrite property protected against partial-file corruption but not against process death between flushes. Now the input messages land on disk before Begin returns, so the prompt that hung is preserved.

The shape is the smallest change that fixes all three: per-call files give bounded flush size (one call's content), bounded memory (in-flight working set only — realistically ≤10 calls under per-model concurrency caps), and crash-survivable inputs (one fsync per Begin).

**Why not streaming.** True mid-call durability — capturing chunks as the LLM emits them — would require switching to genkit's streaming mode and a per-call append-log sidecar. That's real engineering work for a contingent benefit. Today's middleware-after-return capture already records `raw_message` for the truncation/loop cases we've actually hit. The streaming-aware path is scoped as Phase 2 of the persistence plan and deferred until measured failure says otherwise.

**Why not migrate old sessions.** Nothing reads single-file sessions programmatically; existing files on disk stay readable by hand. The path shape change is observable to users who tail trace files — the CLI banner now reads `Session: <dir>/ (per-call YAML under calls/)` instead of `Session: <file>` to make the layout discoverable.

**What's new:**

- `SessionRecorder` is directory-rooted (`dir`, not `path`); `manifest` and `inFlight map[int]*callHandle` replace `session sessionFile`.
- `callHandle.flush()` writes one per-call file atomically; `Begin`/`Finish` no longer touch sibling calls.
- `Close()` marks the manifest complete and flushes interrupted stragglers; `import` and `refine` CLI paths call it before printing the session banner.
- `CallStatusInterrupted` joins the existing `in_progress`/`completed`/`error` set.
- Per-call file naming: `<NNNN>-<agent_id>.yaml` (4-digit zero-padded; sorts lexically; agent_id makes `ls calls/` an at-a-glance summary). When `agent_id` is empty (ad-hoc call sites like the synthesizer), the filename is just `<NNNN>.yaml`.
- New tests cover the load-bearing properties: crash-mid-call preserves input on disk, in-flight count returns to zero after each Finish, per-call writes don't touch sibling files, Close stamps the manifest and interrupted stragglers.

**Reversal criteria:** revert if (a) per-directory file counts hit a filesystem ceiling on real workflows — not a current concern at 25 calls per session, but worth watching if adopt sessions push past several hundred calls; or (b) the loss of a single-file `cat` UX hurts more than the per-call discoverability helps — the directory layout is `find/grep` friendly, so this would surprise.

**Reference:** plan at [.claude/plans/session-trace-persistence.md](../.claude/plans/session-trace-persistence.md), Phase 1. Phase 2 (streaming-aware mid-call capture) is deferred.
