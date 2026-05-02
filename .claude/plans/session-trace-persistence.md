# Plan: Session Trace Persistence — Per-Call Files, Crash-Safe Mid-Call Data

## Context

The session recorder ([internal/agent/session.go](internal/agent/session.go)) persists every LLM call to a single YAML file at `.locutus/sessions/YYYYMMDD/HHMM/SS-xxxxxx.yaml`. The recorder holds the entire session in memory (`r.session.Calls` slice), and on every state transition (`Begin` at call start, `Finish` at call return) it marshals the *whole* session and atomically rewrites the file.

This shape was fine when sessions were 6–8 calls (greenfield `refine goals` pre-Phase-3). Three things have changed:

1. **Phase 3 fanout** produces 15–25+ calls per session as a baseline. Adopt with dozens of workstreams could push this much higher.
2. **`raw_message` capture** added per-call YAML payloads of ~5–50KB each (truncated/looped Gemini outputs are the largest offenders). 17 calls × 10KB avg = ~170KB rewritten on every flush.
3. **The user's framing**: the session trace exists so an operator can debug LLM activity — prompt issues, tool-call traces, degenerate loops. A crash that loses in-flight call data loses *exactly* the call most worth debugging. The current "atomic rewrite" property protects against partial-file corruption but doesn't address the case where a SIGKILL hits between `Begin` (input messages flushed) and `Finish` (output not yet flushed).

Both problems share a structural cause: a single-file format that requires holding everything in memory and rewriting everything on each flush.

## Goal

Make the session trace robust to large workflows and process-level crashes:

- Memory at runtime is bounded by the *in-flight* call working set, not by the cumulative session size.
- Disk I/O per state transition is O(1), not O(N) over total calls.
- A SIGKILL or panic at any point preserves every flushed call's input *and* whatever output was captured at the moment of death.
- The trace remains human-readable (current property: `cat <file>.yaml` is the debugging UI).

Success looks like:

- A session with 100 calls runs in ~constant memory after the first dozen calls — the recorder no longer hoards completed call payloads.
- A SIGKILL mid-call leaves the in-progress call's `messages:` block on disk; only the unrecorded output bytes are lost.
- An operator can `cat .locutus/sessions/<sid>/calls/0017-spec_feature_elaborator.yaml` to inspect one call without paging through 100.
- Test runtime doesn't regress meaningfully (per-call file I/O is small writes; should be comparable to the current single-file rewrite that's already O(N) per flush).

## Scope

In scope:

- New on-disk layout: directory per session with one file per call plus a session-level manifest.
- Recorder API change: `Begin` writes the per-call file once with input messages; `Finish` rewrites the same file with output. No more whole-session rewrites.
- In-memory compaction: the recorder keeps only the in-flight working set (handles for not-yet-finished calls) plus the manifest; completed calls flush to disk and drop from memory.
- Backward-read compatibility for old single-file sessions is **not** required. Nothing currently reads old sessions programmatically beyond tests; old sessions stay readable on disk by hand.

Out of scope (deferred to Phase 2):

- Streaming-aware capture of mid-call chunks. Today's capture is synchronous (middleware sees the full response after the LLM call returns). True mid-call durability — capture chunks as they arrive — requires switching to genkit's streaming mode and a new chunk-flush path. Worth it only if Pro-Preview-class long-running calls (6+ minutes) become a recurring debug target where partial-output mid-call would be load-bearing.

Out of scope entirely:

- Compaction / archival of old sessions (separate concern).
- A binary or compressed format. YAML stays — the per-call files are small enough.
- A query / search tool over sessions. The directory layout makes `grep -r` and `find` work fine.

## Architectural shift

```text
Current:  .locutus/sessions/YYYYMMDD/HHMM/SS-xxxxxx.yaml
          single YAML doc; in-memory session.Calls[]; flush rewrites whole file
                                                     ↓
                                  O(N²) total marshaling work
                                  unbounded memory growth
                                  in-flight output lost on crash

Phase 1:  .locutus/sessions/YYYYMMDD/HHMM/SS-xxxxxx/
          ├── session.yaml                              (manifest only)
          └── calls/
              ├── 0001-spec_scout.yaml                  (one file per call)
              ├── 0002-spec_outliner.yaml
              └── 0003-spec_feature_elaborator.yaml
                                                     ↓
                                  O(1) per state transition
                                  bounded memory (in-flight only)
                                  in-flight inputs survive crash
```

---

## Phase 1: Per-Call Files

### File layout

```text
.locutus/sessions/<YYYYMMDD>/<HHMM>/<SS>-<short>/
├── session.yaml          # manifest: session_id, started_at, completed_at, command, project_root
└── calls/
    ├── 0001-<agent_id>.yaml
    ├── 0002-<agent_id>.yaml
    └── …
```

**Why a directory not a single file**: every per-state-transition flush updates exactly one file (the current call's), bounded in size to that call's content. The session manifest is small and stable; it's written once at construction and updated only on session close. Old single-file sessions stay readable on disk by hand; they don't need migration.

**Why 4-digit padded index**: sorts lexically out of the box (`ls calls/` returns them in order without `sort -n`); 9999 calls per session is more headroom than any realistic workflow needs.

**Why include `agent_id` in the filename**: `ls calls/` is the directory listing UI for "what happened in this session." Filename agent IDs let an operator scan without opening every file. The agent ID is also redundantly inside the file frontmatter for parsing tools.

**Per-call file shape**: same as the current `recordedCall` struct — index, agent_id, role, status, started_at, completed_at, duration_ms, model, messages, output_schema, reasoning, response, raw_message, token counts, error. Each file is a complete record of one call.

**Session manifest shape**:

```yaml
session_id: 20260502-093021-abc123
started_at: "2026-05-02T09:30:21-07:00"
completed_at: "2026-05-02T09:35:14-07:00"   # optional; populated on clean shutdown
command: refine goals
project_root: /path/to/project
```

No `calls:` field. The directory listing IS the calls list.

### Part 1.1: Recorder API change

`SessionRecorder` becomes directory-rooted, not file-rooted.

Internal state:

- `dir string` — `.locutus/sessions/<sid>/`
- `manifest sessionManifest` — small struct with the manifest fields
- `inFlight map[int]*callHandle` — handles for not-yet-finished calls; keyed by index so concurrent `Finish` calls find their slot
- `mu sync.Mutex` — guards `inFlight` and `manifest`
- `nextIndex int` — monotonically increasing, hands out call indices

`callHandle` carries the call's path (`<dir>/calls/<NNNN>-<agent>.yaml`) plus enough state for `Finish` to write the final file content.

Public surface stays close to today's:

- `NewSessionRecorder(fsys, command, projectRoot string)` — creates the directory, writes `session.yaml` manifest, returns the recorder.
- `Begin(role, agentID, req, started)` — assigns the next index, writes the per-call file with `status: in_progress` and the input messages, returns a `callHandle`.
- `(*callHandle).Finish(resp, err)` — rewrites the call's file with response/error/tokens/raw_message/etc., status set to `completed` or `error`. Drops the handle from `inFlight`.
- `Path()` — returns the session directory path (was the file path; the directory IS the session now).
- `SessionID()` — unchanged.
- `Close()` — writes the manifest's `completed_at`, drops any remaining `inFlight` handles (those calls' files are already on disk in `in_progress` state; Close stamps them as `interrupted` for clarity).

`Record(...)` (the `Begin`-immediately-followed-by-`Finish` shortcut) still works; it just calls Begin and Finish in sequence.

### Part 1.2: Memory compaction

The recorder no longer holds the cumulative session in memory. After `Finish` rewrites the per-call file and drops the handle from `inFlight`, the call's content is GC'd. Memory at any point holds:

- Manifest fields (small, fixed).
- Open call handles in `inFlight` — bounded by per-model concurrency × number of parallel fanout steps. Realistically ≤10 in flight at once.

For a 100-call session with at most 4 calls in flight at any point, memory pressure is ~constant in N.

### Part 1.3: Crash safety

- `Begin` flushes input messages to disk before returning. A SIGKILL between Begin and Finish leaves the call's input on disk with `status: in_progress`. The output is lost (it never made it to the in-memory `callHandle`), but the input — which is what the operator needs to debug "why did this call take forever?" — is preserved.
- `Finish` flushes output to disk before clearing the in-flight slot. A SIGKILL between Finish and the slot-clear is benign — file is written.
- Atomic write per file (write-to-tmp + rename on OSFS) means a crash mid-flush leaves either the previous version or the new version of *that one file* — never partial.

### Part 1.4: Test updates

- `internal/agent/session_test.go` reads `rec.Path()` as a single file today. Update to read the manifest from `<dir>/session.yaml` and the calls from `<dir>/calls/*.yaml`. Add helpers like `loadSession(fs, rec)` that returns a `(manifest, []recordedCall)` view stitched from the disk layout.
- `cmd/refine_test.go`, `cmd/adopt_synthesize_test.go`, etc. that touch session state need the same update.
- New test `TestSessionRecorderSurvivesCrashMidCall`: Begin a call, simulate a crash by *not* calling Finish, instantiate a fresh recorder pointing at the same dir, verify the in-progress call's file is still on disk with input messages and `status: in_progress`.
- New test `TestSessionRecorderPerCallFileIsAtomic`: verify each Begin/Finish updates exactly one per-call file, leaving siblings untouched (write timestamps or contents check).
- New test `TestSessionRecorderInFlightCountDoesNotGrow`: run a 50-call sequence, verify `len(rec.inFlight)` returns to 0 after each Finish (memory bound assertion).

### Part 1.5: Documentation

- Add a DJ entry: "DJ-XXX: Session trace storage moves to per-call file layout." Cite this plan and the user-feedback that motivated it.
- Update CLAUDE.md if it mentions the session file path shape (it doesn't, currently — verify).
- The path shape change is observable to users tailing trace files. Update the CLI banner that prints the trace location (currently `Session: <path>`) to print the directory and mention the per-call layout.

### Phase 1 ship criteria

- `go test ./...` and `go vet ./...` pass.
- A real `locutus refine goals` run on winplan produces a session directory with one file per call; `cat .locutus/sessions/<sid>/calls/0017-*.yaml` shows the call's full record.
- A SIGINT during a long-running elaborator preserves the in-flight call's input messages on disk.
- Memory profile of a 50-call test run shows the recorder's allocation set returning to ~baseline after each Finish.

---

## Phase 2 (deferred): Streaming-Aware Mid-Call Capture

Today's capture is synchronous: the middleware sees the full `*ai.ModelResponse` after the model call returns. A SIGKILL during a 6-minute Pro Preview call loses every output token the model emitted before the crash, even though the bytes existed on the wire.

Phase 2 would switch to genkit's streaming mode:

- Pass `ai.WithStreaming(callback)` on every Generate call.
- The callback receives `*ai.ModelResponseChunk` as the model emits chunks.
- The recorder appends each chunk to a per-call sidecar (`.locutus/sessions/<sid>/calls/0017-spec_feature_elaborator.chunks.ndjson`).
- After the call completes (or the process dies), the chunks log preserves every chunk that hit the recorder.
- After the call completes cleanly, the chunks log can be folded into the per-call YAML's `response:` field and the sidecar deleted.

Cost: more I/O during the call (an append per chunk × possibly hundreds of chunks). Benefit: a crash mid-call preserves every output token streamed so far.

**Defer until measured**: the trigger for shipping Phase 2 is a recurring failure mode where mid-call data would have changed the diagnosis. So far our long-running calls have either succeeded or hit a single end-of-output failure (truncation, loop) that the post-call middleware capture already records via `raw_message`. If Pro Preview keeps regularly dying mid-call without leaving a trace we can read, Phase 2 becomes load-bearing.

---

## Sequencing recommendation

1. **Phase 1 first as a single change.** Per-call file layout + recorder API + memory compaction + tests + DJ entry. One PR-sized commit.
2. **Phase 2 only if measured failure says so.** Streaming capture is real engineering work for a contingent benefit.

## Risks and reversibility

- **Test churn.** Every test that reads `rec.Path()` as a file needs updating to read a directory. Bounded — a one-time edit across the test suite.
- **External tooling that reads session files.** None known. The DJ entry should explicitly call out that the path shape changed; users with custom tooling adapt or pin to an older version.
- **Filesystem object count.** A 100-call session creates 102 files (manifest + 100 calls + the calls/ directory entry). Modern filesystems handle this fine. For very long sessions (1000+ calls), per-directory file counts could matter on some filesystems; not a current concern.
- **Reverting** is a `git revert` plus deleting any in-progress new-format sessions on disk. Old single-file sessions remain readable.

## Open questions

- **Manifest update on Close**: do we update `completed_at` on every successful run, or only on clean shutdown? Probably only on clean shutdown — a SIGKILL leaves it absent, which is itself diagnostic ("this session never finished cleanly").
- **In-flight slot count cap**: should we error or block when `inFlight` exceeds N? Today there's no cap. The per-model concurrency throttle in GenKitLLM already bounds parallel calls; the recorder's in-flight set tracks them. No cap needed; observability only.
- **Should the session manifest carry a count of total calls?** Tooling could derive it from `ls calls/ | wc -l`. Probably skip it — derivable, and it'd require mid-session manifest updates which we're trying to avoid.
- **Per-call file naming for fanout calls**: the per-item stepID in fanout already disambiguates (e.g., `elaborate_features (feat-x)`); should that suffix be in the filename? Probably yes — `0017-spec_feature_elaborator-feat-x.yaml` reads better than `0017-spec_feature_elaborator.yaml` when 12 of those siblings exist.
