package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/chetan/locutus/internal/specio"
)

// roleContextKey is the context value carrying the role tag for an LLM
// call. Unexported so callers go through WithRole / RoleFromContext.
type roleContextKey struct{}

// WithRole returns a context that tags subsequent LLM calls with role.
// SessionRecorder reads the value to label each recorded call. Empty
// role is allowed — the call is recorded with role: "" — but most call
// sites should set one ("proposer", "critic", "intake", "rewriter",
// etc.) so transcripts read like a council debate.
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleContextKey{}, role)
}

// RoleFromContext returns the role tag set via WithRole, or "" if none.
func RoleFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(roleContextKey{}).(string); ok {
		return v
	}
	return ""
}

// agentIDContextKey carries the source-agent identifier for an LLM
// call (e.g. "spec_feature_elaborator"). Distinct from role: a single
// agent may participate in multiple roles (architect runs on propose
// and revise, etc.). Trace consumers want to know "which .md file
// produced this output," which is the agent id.
type agentIDContextKey struct{}

// WithAgentID tags subsequent LLM calls with the source agent id so
// the session recorder can write it onto every recordedCall. Workflow
// executors call this before dispatching each agent's call; ad-hoc
// LLM call sites that aren't workflow agents (synthesizer, rewriter,
// integrity_revise) leave it unset and the trace shows agent_id: "".
func WithAgentID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, agentIDContextKey{}, id)
}

// AgentIDFromContext returns the agent id set via WithAgentID, or ""
// if none.
func AgentIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(agentIDContextKey{}).(string); ok {
		return v
	}
	return ""
}

// callTagContextKey carries an optional per-call tag the recorder
// appends to the filename. Used by the workflow's fanout dispatcher to
// stamp `feat-x` onto a per-element elaborator call so that
// `ls .locutus/sessions/<sid>/calls/` reads as a directory of named
// nodes rather than 12 indistinguishable
// `0017-spec_feature_elaborator.yaml` siblings. The agent_id stays as
// the bare agent name; the tag is filename-only (it's already in the
// call's messages content). Empty when not set.
type callTagContextKey struct{}

// WithCallTag returns a context that tags subsequent LLM calls with a
// filename suffix the recorder appends to the per-call YAML name.
// Production callers (the workflow fanout dispatcher) set it to the
// per-item id (e.g. "feat-dashboard"); ad-hoc call sites leave it
// unset.
func WithCallTag(ctx context.Context, tag string) context.Context {
	return context.WithValue(ctx, callTagContextKey{}, tag)
}

// CallTagFromContext returns the call tag set via WithCallTag, or ""
// if none.
func CallTagFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(callTagContextKey{}).(string); ok {
		return v
	}
	return ""
}

// acquiredCallbackKey carries a callback invoked when a throttled LLM
// call leaves the per-model semaphore queue and actually begins. The
// workflow executor uses it to flip a "queued" spinner to "running" in
// the CLI sink so the operator can tell waiting items from in-flight.
type acquiredCallbackKey struct{}

// WithAcquiredCallback returns a context whose LLM call invokes fn at
// the moment it leaves the per-model concurrency queue and starts
// hitting the provider. Used by the workflow executor to surface a
// "queued → running" transition; ad-hoc call sites can ignore it.
func WithAcquiredCallback(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, acquiredCallbackKey{}, fn)
}

// AcquiredCallbackFromContext returns the callback set via
// WithAcquiredCallback, or nil if none.
func AcquiredCallbackFromContext(ctx context.Context) func() {
	if v, ok := ctx.Value(acquiredCallbackKey{}).(func()); ok {
		return v
	}
	return nil
}

// retryCallbackKey carries a callback invoked when GenerateWithRetry
// loops back to retry a failed call (rate-limited or timed out). The
// workflow executor uses it to flip a spinner to "retrying" so the
// operator can see that a call isn't making progress on its first
// attempt — silent retries used to leave the spinner stuck in RUNNING
// while burning attempts in the background.
type retryCallbackKey struct{}

// WithRetryCallback returns a context whose retry-eligible LLM call
// invokes fn(attempt, err) right before each backoff sleep, where
// `attempt` is the just-failed attempt number (1-indexed) and `err`
// is the error that triggered the retry. fn fires at most
// MaxAttempts-1 times per call (no fn invocation when the final
// attempt fails — that surfaces as a regular error event).
func WithRetryCallback(ctx context.Context, fn func(attempt int, err error)) context.Context {
	return context.WithValue(ctx, retryCallbackKey{}, fn)
}

// RetryCallbackFromContext returns the callback set via
// WithRetryCallback, or nil if none.
func RetryCallbackFromContext(ctx context.Context) func(int, error) {
	if v, ok := ctx.Value(retryCallbackKey{}).(func(int, error)); ok {
		return v
	}
	return nil
}

// SessionRecorder writes a YAML transcript of every LLM call as a
// directory tree under .locutus/sessions/<sid>/, with a small manifest
// file (`session.yaml`) and one file per call under `calls/`.
//
// Per-call files mean each Begin/Finish flushes only that one call's
// content (bounded by node complexity), not the whole session. Memory
// at runtime tracks only the in-flight working set, not cumulative
// session size — important for fanout-heavy workflows that emit 20+
// calls. A SIGKILL between Begin (input on disk) and Finish (output
// not yet on disk) leaves the in-progress call's input file readable
// so an operator can debug "why did this call take forever?"
//
// Each call file is written atomically (tmp + rename on OSFS), so a
// crash mid-flush leaves either the prior version or the new version
// of that one file — never partial.
type SessionRecorder struct {
	fsys specio.FS
	dir  string // .locutus/sessions/<sid>/

	mu        sync.Mutex
	manifest  sessionManifest
	inFlight  map[int]*callHandle
	nextIndex int
}

// sessionManifest is the on-disk shape of <dir>/session.yaml.
// Intentionally small and stable: it's written once at construction
// and updated only on clean Close. The directory listing of calls/
// IS the calls list; no count or per-call summary is persisted here.
type sessionManifest struct {
	SessionID   string `yaml:"session_id"`
	StartedAt   string `yaml:"started_at"`
	CompletedAt string `yaml:"completed_at,omitempty"`
	Command     string `yaml:"command"`
	ProjectRoot string `yaml:"project_root,omitempty"`
}

// Call status values written to per-call YAML files. "in_progress"
// means Begin() has fired but the underlying Generate() has not yet
// returned — readers tail the file to see what's currently in flight.
// "interrupted" means Close() ran while the call was still in flight
// (e.g. the process is shutting down without waiting for the call).
const (
	CallStatusInProgress = "in_progress"
	CallStatusCompleted  = "completed"
	CallStatusError      = "error"
	CallStatusInterrupted = "interrupted"
)

type recordedCall struct {
	Index          int               `yaml:"index"`
	AgentID        string            `yaml:"agent_id,omitempty"`
	Role           string            `yaml:"role,omitempty"`
	Status         string            `yaml:"status,omitempty"`
	StartedAt      string            `yaml:"started_at"`
	CompletedAt    string            `yaml:"completed_at,omitempty"`
	DurationMS     int64             `yaml:"duration_ms,omitempty"`
	Model          string            `yaml:"model"`
	Messages       []recordedMessage `yaml:"messages"`
	OutputSchema   bool              `yaml:"output_schema,omitempty"`
	Reasoning      string            `yaml:"reasoning,omitempty"`
	Response       string            `yaml:"response,omitempty"`
	RawMessage     string            `yaml:"raw_message,omitempty"`
	InputTokens    int               `yaml:"input_tokens,omitempty"`
	OutputTokens   int               `yaml:"output_tokens,omitempty"`
	ThoughtsTokens int               `yaml:"thoughts_tokens,omitempty"`
	TotalTokens    int               `yaml:"total_tokens,omitempty"`
	// Rounds is populated only for multi-round tool-use calls
	// (Genkit's tool-dispatch loop drives multiple model invocations
	// for one Generate call). Each entry records what the model
	// emitted that round — including any tool_request parts in the
	// raw message — so an operator can see the full conversation, not
	// just the final response after the loop completed. Single-round
	// calls leave this nil and rely on the top-level Reasoning /
	// Response / RawMessage fields.
	Rounds []recordedRound `yaml:"rounds,omitempty"`
	Error  string          `yaml:"error,omitempty"`
}

// recordedRound is one model invocation inside a tool-use loop. Mirror
// of GenerateRound with YAML tags. Message holds the JSON of the
// model's *ai.Message for that round (text + reasoning + tool_request
// parts).
type recordedRound struct {
	Index          int    `yaml:"index"`
	Reasoning      string `yaml:"reasoning,omitempty"`
	Text           string `yaml:"text,omitempty"`
	Message        string `yaml:"message,omitempty"`
	InputTokens    int    `yaml:"input_tokens,omitempty"`
	OutputTokens   int    `yaml:"output_tokens,omitempty"`
	ThoughtsTokens int    `yaml:"thoughts_tokens,omitempty"`
}

type recordedMessage struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
}

// CallsDirName is the subdirectory under a session directory that
// holds per-call YAML files. Exported for tools that walk a session.
const CallsDirName = "calls"

// SessionManifestFile is the manifest filename within a session
// directory. Exported for tools that walk a session.
const SessionManifestFile = "session.yaml"

// NewSessionRecorder creates a session directory at
// .locutus/sessions/<YYYYMMDD>/<HHMM>/<SS>-<short>/ on fsys. The
// per-minute directory keeps housekeeping easy — `rm -rf
// .locutus/sessions/20260420` drops a day, `rm -rf .../20260420/1407`
// drops a minute — without exploding into a single-file directory per
// second when sessions don't actually fire that fast. Within the
// session directory, `session.yaml` is the manifest and `calls/`
// holds one YAML file per recorded LLM call.
//
// command is recorded for human reference (e.g. "refine goals",
// "import docs/foo.md"). projectRoot is informational — included in
// the manifest but not used for path resolution (fsys is already
// rooted).
func NewSessionRecorder(fsys specio.FS, command, projectRoot string) (*SessionRecorder, error) {
	ts := time.Now()
	short := newShortSessionID()
	dateDir := ts.Format("20060102")
	hourMinDir := ts.Format("1504")
	secPrefix := ts.Format("05")
	parent := path.Join(".locutus/sessions", dateDir, hourMinDir)
	dir := path.Join(parent, secPrefix+"-"+short)
	callsDir := path.Join(dir, CallsDirName)
	if err := fsys.MkdirAll(callsDir, 0o755); err != nil {
		return nil, fmt.Errorf("session recorder mkdir: %w", err)
	}
	// Composite session id retains the full timestamp + short suffix so a
	// single string identifies the session in logs and matches across
	// the path components.
	sid := dateDir + "-" + hourMinDir + secPrefix + "-" + short
	rec := &SessionRecorder{
		fsys: fsys,
		dir:  dir,
		manifest: sessionManifest{
			SessionID:   sid,
			StartedAt:   ts.Format(time.RFC3339),
			Command:     command,
			ProjectRoot: projectRoot,
		},
		inFlight: make(map[int]*callHandle),
	}
	if err := rec.writeManifest(); err != nil {
		return nil, err
	}
	return rec, nil
}

// SessionID returns the session ID (also the directory basename).
func (r *SessionRecorder) SessionID() string { return r.manifest.SessionID }

// Path returns the FS-relative path of the session directory. Tools
// that want to enumerate calls should look under <Path()>/calls/.
func (r *SessionRecorder) Path() string { return r.dir }

// ManifestPath returns the FS-relative path of the session manifest
// file. Provided for tooling and tests; production callers shouldn't
// need it.
func (r *SessionRecorder) ManifestPath() string {
	return path.Join(r.dir, SessionManifestFile)
}

// Record stores one agent call as a single completed entry.
// Equivalent to Begin(...) immediately followed by Finish(...) and
// retained for callers that don't need the live placeholder.
// callTag, when non-empty, is appended to the per-call file name
// as a stable suffix (see WithCallTag). Safe for concurrent use.
func (r *SessionRecorder) Record(role, agentID, callTag string, def AgentDef, input AgentInput, out *AgentOutput, callErr error, started time.Time, duration time.Duration) {
	h := r.Begin(role, agentID, callTag, def, input, started)
	completedAt := started.Add(duration)
	h.finishAt(out, callErr, completedAt, duration)
}

// callHandle is returned from Begin and threaded into Finish so the
// recorder can update the call's per-call file. Each handle owns its
// path and the in-memory recordedCall struct that gets mutated then
// flushed on Finish; after Finish the handle drops out of inFlight
// and its memory is GC-eligible.
type callHandle struct {
	recorder *SessionRecorder
	index    int
	filePath string
	started  time.Time
	call     recordedCall
}

// Begin assigns the next call index, writes the per-call file with
// `status: in_progress` and the input messages, and returns a handle
// for Finish. callTag, when non-empty, is appended to the per-call
// filename as a stable suffix (e.g. fanout calls pass the per-item id
// like "feat-dashboard" so siblings are distinguishable from a
// directory listing). The file is on disk before Begin returns so a
// tail of the session directory reveals what's currently in flight;
// a SIGKILL between Begin and Finish preserves the input messages
// but loses the output. Safe for concurrent use.
func (r *SessionRecorder) Begin(role, agentID, callTag string, def AgentDef, input AgentInput, started time.Time) *callHandle {
	r.mu.Lock()
	r.nextIndex++
	idx := r.nextIndex
	r.mu.Unlock()

	call := recordedCall{
		Index:        idx,
		AgentID:      agentID,
		Role:         role,
		Status:       CallStatusInProgress,
		StartedAt:    started.Format(time.RFC3339),
		OutputSchema: def.OutputSchema != "",
	}
	systemPrompt := BuildSystemPrompt(def)
	if systemPrompt != "" {
		call.Messages = append(call.Messages, recordedMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range input.Messages {
		call.Messages = append(call.Messages, recordedMessage{Role: m.Role, Content: m.Content})
	}

	h := &callHandle{
		recorder: r,
		index:    idx,
		filePath: r.callFilePath(idx, agentID, callTag),
		started:  started,
		call:     call,
	}

	r.mu.Lock()
	r.inFlight[idx] = h
	r.mu.Unlock()

	// Best-effort flush: if the per-call write fails, log and proceed.
	// The recorder is observability — a full failure path would mask
	// the actual LLM error the operator is trying to trace.
	if err := h.flush(); err != nil {
		slog.Warn("session recorder: in-progress call flush failed",
			"session", r.manifest.SessionID, "index", idx, "error", err)
	}
	return h
}

// Finish completes the call this handle was issued for. Idempotent
// on a nil handle so callers can defer h.Finish(...) without nil
// checks even when Begin was never called.
func (h *callHandle) Finish(out *AgentOutput, callErr error) {
	if h == nil || h.recorder == nil {
		return
	}
	completedAt := time.Now()
	h.finishAt(out, callErr, completedAt, completedAt.Sub(h.started))
}

// finishAt is the shared backend for Finish (real-time) and Record
// (synthetic time). Mutates the handle's recordedCall, flushes the
// per-call file, then drops the handle from the recorder's in-flight
// set so the call's payload becomes GC-eligible.
func (h *callHandle) finishAt(out *AgentOutput, callErr error, completedAt time.Time, duration time.Duration) {
	h.call.CompletedAt = completedAt.Format(time.RFC3339)
	h.call.DurationMS = duration.Milliseconds()
	if callErr != nil {
		h.call.Status = CallStatusError
		h.call.Error = callErr.Error()
	} else {
		h.call.Status = CallStatusCompleted
	}
	if out != nil {
		h.call.Response = out.Content
		h.call.Reasoning = out.Reasoning
		h.call.RawMessage = out.RawMessage
		h.call.InputTokens = out.InputTokens
		h.call.OutputTokens = out.OutputTokens
		h.call.ThoughtsTokens = out.ThoughtsTokens
		h.call.TotalTokens = out.TotalTokens
		if h.call.Model == "" {
			h.call.Model = out.Model
		}
		// Multi-round tool-use captures: copy each round's snapshot
		// into the per-call file so the trace shows what the model
		// emitted in each round (including tool_request parts), not
		// just the final response. Single-round calls leave Rounds
		// nil — the top-level Reasoning/Response/RawMessage already
		// carry that round's data.
		if len(out.Rounds) > 0 {
			h.call.Rounds = make([]recordedRound, len(out.Rounds))
			for i, r := range out.Rounds {
				h.call.Rounds[i] = recordedRound{
					Index:          r.Index,
					Reasoning:      r.Reasoning,
					Text:           r.Text,
					Message:        r.Message,
					InputTokens:    r.InputTokens,
					OutputTokens:   r.OutputTokens,
					ThoughtsTokens: r.ThoughtsTokens,
				}
			}
		}
	}
	if err := h.flush(); err != nil {
		slog.Warn("session recorder: finish flush failed",
			"session", h.recorder.manifest.SessionID, "index", h.index, "error", err)
	}
	h.recorder.mu.Lock()
	delete(h.recorder.inFlight, h.index)
	h.recorder.mu.Unlock()
}

// Close stamps the manifest's completed_at and marks any still-in-flight
// calls as interrupted on disk. Safe to call multiple times; idempotent
// past the first call. Optional — sessions left without Close still have
// their per-call files on disk; the manifest just lacks completed_at,
// which itself is a useful "this session never finished cleanly"
// diagnostic.
func (r *SessionRecorder) Close() error {
	r.mu.Lock()
	r.manifest.CompletedAt = time.Now().Format(time.RFC3339)
	stragglers := make([]*callHandle, 0, len(r.inFlight))
	for _, h := range r.inFlight {
		stragglers = append(stragglers, h)
	}
	r.inFlight = make(map[int]*callHandle)
	r.mu.Unlock()

	for _, h := range stragglers {
		h.call.Status = CallStatusInterrupted
		if err := h.flush(); err != nil {
			slog.Warn("session recorder: close flush failed",
				"session", r.manifest.SessionID, "index", h.index, "error", err)
		}
	}
	return r.writeManifest()
}

// callFilePath builds the per-call file path:
//
//	<dir>/calls/<NNNN>-<agent>-<tag>.yaml  when agent and tag are set
//	<dir>/calls/<NNNN>-<agent>.yaml        when only agent is set
//	<dir>/calls/<NNNN>.yaml                when neither is set
//
// 4-digit zero-padded index sorts lexically out of the box; 9999
// calls per session is more headroom than any realistic workflow
// needs. The tag is the per-item identifier from the workflow's
// fanout dispatcher (e.g. "feat-dashboard") so a directory listing
// reads as named nodes rather than indistinguishable per-agent
// siblings. Tags are slug-shaped already (they come from spec node
// IDs); we don't sanitize further.
func (r *SessionRecorder) callFilePath(idx int, agentID, callTag string) string {
	name := fmt.Sprintf("%04d", idx)
	if agentID != "" {
		name = name + "-" + agentID
	}
	if callTag != "" {
		name = name + "-" + callTag
	}
	return path.Join(r.dir, CallsDirName, name+".yaml")
}

// flush writes the call's current state to its per-call file. Atomic
// on OSFS; straight write on MemFS.
func (h *callHandle) flush() error {
	data, err := yaml.Marshal(&h.call)
	if err != nil {
		return err
	}
	return specio.AtomicWriteFile(h.recorder.fsys, h.filePath, data, 0o644)
}

// writeManifest atomically rewrites <dir>/session.yaml. Called once
// at construction and again on Close. Cheap — manifest is small and
// stable.
func (r *SessionRecorder) writeManifest() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeManifestLocked()
}

func (r *SessionRecorder) writeManifestLocked() error {
	data, err := yaml.Marshal(&r.manifest)
	if err != nil {
		return err
	}
	return specio.AtomicWriteFile(r.fsys, path.Join(r.dir, SessionManifestFile), data, 0o644)
}

// inFlightCount returns the number of calls that have started but not
// yet finished. Test-only observability for the memory-bound assertion.
func (r *SessionRecorder) inFlightCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.inFlight)
}

// newShortSessionID returns 6 hex chars from crypto/rand, distinguishing
// two invocations that share a HHMMSS directory.
func newShortSessionID() string {
	var rnd [3]byte
	_, _ = io.ReadFull(rand.Reader, rnd[:])
	return hex.EncodeToString(rnd[:])
}

// LoggingExecutor wraps any AgentExecutor and routes every Run call
// through a SessionRecorder before delegating. The role tag for each
// call is read from ctx via RoleFromContext (set by callers via
// WithRole).
//
// Heartbeat: when HeartbeatEnabled is true, an in-flight call emits
// a periodic "still running" log line so an operator watching stderr
// sees the call hasn't deadlocked. Callers that already render
// per-call progress through another channel (CLI spinners, MCP
// progress notifications) should pass false to keep stderr quiet.
type LoggingExecutor struct {
	inner            AgentExecutor
	recorder         *SessionRecorder
	HeartbeatEnabled bool
}

// NewLoggingExecutor wraps inner with recording. Heartbeat defaults
// to off — callers turn it on with NewLoggingExecutorWithHeartbeat
// when they don't have a per-call UI of their own. Existing callers
// that don't pass a heartbeat preference get silent behavior,
// matching the CLI rich path where the spinner is the visibility
// surface.
func NewLoggingExecutor(inner AgentExecutor, recorder *SessionRecorder) *LoggingExecutor {
	return &LoggingExecutor{inner: inner, recorder: recorder}
}

// NewLoggingExecutorWithHeartbeat is the same as NewLoggingExecutor
// but configures the heartbeat. Used by --plain CLI mode and the
// MCP server, which do not own per-call UI.
func NewLoggingExecutorWithHeartbeat(inner AgentExecutor, recorder *SessionRecorder, heartbeat bool) *LoggingExecutor {
	return &LoggingExecutor{inner: inner, recorder: recorder, HeartbeatEnabled: heartbeat}
}

// Run delegates to the inner AgentExecutor and records the call. The
// recorder gets an in-progress entry at start so a tail of the per-
// call file reveals what's currently in flight. A heartbeat goroutine
// logs "still running" every heartbeatInterval so an operator
// watching stderr knows the call hasn't deadlocked even when the
// underlying non-streaming Run produces no output of its own.
func (l *LoggingExecutor) Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error) {
	started := time.Now()
	role := RoleFromContext(ctx)
	agentID := AgentIDFromContext(ctx)
	callTag := CallTagFromContext(ctx)
	handle := l.recorder.Begin(role, agentID, callTag, def, input, started)

	var stop func()
	if l.HeartbeatEnabled {
		stop = startHeartbeat(role, def.ID, started)
	} else {
		stop = func() {}
	}
	defer stop()

	out, err := l.inner.Run(ctx, def, input)
	handle.Finish(out, err)
	return out, err
}

// EnvKeyLLMHeartbeat overrides the heartbeat interval. Accepts any
// time.ParseDuration string. "0" disables the heartbeat entirely.
const EnvKeyLLMHeartbeat = "LOCUTUS_LLM_HEARTBEAT"

// DefaultLLMHeartbeatInterval is the cadence at which an in-flight LLM
// call emits a "still running" log line. Long enough not to spam an
// operator watching stderr; short enough that a hung call is obvious
// well before any timeout fires.
const DefaultLLMHeartbeatInterval = 30 * time.Second

// startHeartbeat emits a single slog.Info every interval until the
// returned stop function is called. Callers should `defer stop()`. A
// returned no-op stop is used when the heartbeat is disabled (interval
// <= 0) so callers don't need to branch.
func startHeartbeat(role, model string, started time.Time) (stop func()) {
	interval := DefaultLLMHeartbeatInterval
	if v := os.Getenv(EnvKeyLLMHeartbeat); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		} else {
			slog.Warn("invalid LOCUTUS_LLM_HEARTBEAT; using default",
				"value", v, "default", DefaultLLMHeartbeatInterval)
		}
	}
	if interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				slog.Info("LLM call in progress",
					"role", role,
					"model", model,
					"elapsed", now.Sub(started).Round(time.Second).String(),
				)
			}
		}
	}()
	return func() { close(done) }
}

// Recorder exposes the underlying recorder so callers can read the
// session id / path for log messages.
func (l *LoggingExecutor) Recorder() *SessionRecorder { return l.recorder }
