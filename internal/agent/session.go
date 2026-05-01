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

// SessionRecorder writes a YAML transcript of every LLM call to
// .locutus/sessions/<sid>.yaml. The recorder rewrites the file
// atomically on each Record() so a crash mid-call leaves the previous
// N-1 calls intact; only the in-flight call is at risk.
//
// The file format is human-readable: prompts and responses are emitted
// as YAML literal blocks (|) so newlines and markdown survive verbatim
// — the file IS the readable transcript, no parallel summary.md needed.
type SessionRecorder struct {
	fsys specio.FS
	path string

	mu      sync.Mutex
	session sessionFile
}

// sessionFile is the on-disk YAML shape. Keep field tags ordered as you
// want them rendered — yaml.v3 honors struct order.
type sessionFile struct {
	SessionID   string         `yaml:"session_id"`
	StartedAt   string         `yaml:"started_at"`
	Command     string         `yaml:"command"`
	ProjectRoot string         `yaml:"project_root,omitempty"`
	Calls       []recordedCall `yaml:"calls"`
}

// Call status values written to the YAML transcript. "in_progress"
// means Begin() has fired but the underlying Generate() has not yet
// returned — readers tail the file to see what's currently in flight.
const (
	CallStatusInProgress = "in_progress"
	CallStatusCompleted  = "completed"
	CallStatusError      = "error"
)

type recordedCall struct {
	Index          int               `yaml:"index"`
	Role           string            `yaml:"role,omitempty"`
	Status         string            `yaml:"status,omitempty"`
	StartedAt      string            `yaml:"started_at"`
	CompletedAt    string            `yaml:"completed_at,omitempty"`
	DurationMS     int64             `yaml:"duration_ms,omitempty"`
	Model          string            `yaml:"model"`
	Messages       []recordedMessage `yaml:"messages"`
	OutputSchema   bool              `yaml:"output_schema,omitempty"`
	Response       string            `yaml:"response,omitempty"`
	InputTokens    int               `yaml:"input_tokens,omitempty"`
	OutputTokens   int               `yaml:"output_tokens,omitempty"`
	ThoughtsTokens int               `yaml:"thoughts_tokens,omitempty"`
	TotalTokens    int               `yaml:"total_tokens,omitempty"`
	Error          string            `yaml:"error,omitempty"`
}

type recordedMessage struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
}

// NewSessionRecorder creates a session file at
// .locutus/sessions/<YYYYMMDD>/<HHMM>/<SS>-<short>.yaml on fsys. The
// per-minute directory keeps housekeeping easy — `rm -rf
// .locutus/sessions/20260420` drops a day, `rm -rf .../20260420/1407`
// drops a minute — without exploding into a single-file directory per
// second when sessions don't actually fire that fast.
//
// command is recorded for human reference (e.g. "refine goals",
// "import docs/foo.md"). projectRoot is informational — included in the
// file metadata but not used for path resolution (fsys is already rooted).
func NewSessionRecorder(fsys specio.FS, command, projectRoot string) (*SessionRecorder, error) {
	ts := time.Now()
	short := newShortSessionID()
	dateDir := ts.Format("20060102")
	hourMinDir := ts.Format("1504")
	secPrefix := ts.Format("05")
	dir := path.Join(".locutus/sessions", dateDir, hourMinDir)
	relPath := path.Join(dir, secPrefix+"-"+short+".yaml")
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session recorder mkdir: %w", err)
	}
	// Composite session id retains the full timestamp + short suffix so a
	// single string identifies the session in logs and matches across
	// the path components.
	sid := dateDir + "-" + hourMinDir + secPrefix + "-" + short
	rec := &SessionRecorder{
		fsys: fsys,
		path: relPath,
		session: sessionFile{
			SessionID:   sid,
			StartedAt:   ts.Format(time.RFC3339),
			Command:     command,
			ProjectRoot: projectRoot,
		},
	}
	if err := rec.flush(); err != nil {
		return nil, err
	}
	return rec, nil
}

// SessionID returns the session ID (also the file basename without
// extension).
func (r *SessionRecorder) SessionID() string { return r.session.SessionID }

// Path returns the FS-relative path of the session file.
func (r *SessionRecorder) Path() string { return r.path }

// Record stores one LLM call as a single completed entry. Equivalent to
// Begin(...) immediately followed by Finish(...) and retained for callers
// that don't need the live placeholder. Safe for concurrent use.
func (r *SessionRecorder) Record(role string, req GenerateRequest, resp *GenerateResponse, callErr error, started time.Time, duration time.Duration) {
	h := r.Begin(role, req, started)
	completedAt := started.Add(duration)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finalize(h.sliceIdx, resp, callErr, completedAt, duration)
	_ = r.flush()
}

// callHandle is returned from Begin and threaded into Finish so the
// recorder can update the entry it appended at start. Indices are stable
// because calls only ever append — parallel workflow agents won't shuffle
// an already-issued index.
type callHandle struct {
	recorder *SessionRecorder
	sliceIdx int
	started  time.Time
}

// Begin appends an in-progress entry for this call and flushes
// immediately so a tail of the YAML reveals what is currently in
// flight. The caller MUST follow up with Finish (use defer when the
// surrounding function takes the response/error in one place). Safe
// for concurrent use.
func (r *SessionRecorder) Begin(role string, req GenerateRequest, started time.Time) *callHandle {
	r.mu.Lock()
	defer r.mu.Unlock()

	call := recordedCall{
		Index:        len(r.session.Calls) + 1,
		Role:         role,
		Status:       CallStatusInProgress,
		StartedAt:    started.Format(time.RFC3339),
		Model:        req.Model,
		OutputSchema: req.OutputSchema != nil,
	}
	for _, m := range req.Messages {
		call.Messages = append(call.Messages, recordedMessage{Role: m.Role, Content: m.Content})
	}
	r.session.Calls = append(r.session.Calls, call)
	idx := len(r.session.Calls) - 1

	// Best-effort flush — see flush comment in Record.
	_ = r.flush()

	return &callHandle{recorder: r, sliceIdx: idx, started: started}
}

// Finish completes the entry that Begin appended. Idempotent on a nil
// handle so callers can defer h.Finish(...) without nil checks even
// when Begin was never called.
func (h *callHandle) Finish(resp *GenerateResponse, callErr error) {
	if h == nil || h.recorder == nil {
		return
	}
	completedAt := time.Now()
	duration := completedAt.Sub(h.started)
	r := h.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finalize(h.sliceIdx, resp, callErr, completedAt, duration)
	_ = r.flush()
}

// finalize writes the response/error/timing fields onto an existing
// in-progress entry. Caller must hold r.mu.
func (r *SessionRecorder) finalize(idx int, resp *GenerateResponse, callErr error, completedAt time.Time, duration time.Duration) {
	if idx < 0 || idx >= len(r.session.Calls) {
		return
	}
	call := &r.session.Calls[idx]
	call.CompletedAt = completedAt.Format(time.RFC3339)
	call.DurationMS = duration.Milliseconds()
	if callErr != nil {
		call.Status = CallStatusError
		call.Error = callErr.Error()
	} else {
		call.Status = CallStatusCompleted
	}
	if resp != nil {
		call.Response = resp.Content
		call.InputTokens = resp.InputTokens
		call.OutputTokens = resp.OutputTokens
		call.ThoughtsTokens = resp.ThoughtsTokens
		call.TotalTokens = resp.TotalTokens
		if call.Model == "" {
			call.Model = resp.Model
		}
	}
}

func (r *SessionRecorder) flush() error {
	data, err := yaml.Marshal(&r.session)
	if err != nil {
		return err
	}
	return specio.AtomicWriteFile(r.fsys, r.path, data, 0o644)
}

// newShortSessionID returns 6 hex chars from crypto/rand, distinguishing
// two invocations that share a HHMMSS directory.
func newShortSessionID() string {
	var rnd [3]byte
	_, _ = io.ReadFull(rand.Reader, rnd[:])
	return hex.EncodeToString(rnd[:])
}

// LoggingLLM wraps any LLM and routes every Generate call through a
// SessionRecorder before delegating. The role tag for each call is read
// from ctx via RoleFromContext (set by callers via WithRole).
//
// Heartbeat: when HeartbeatEnabled is true, an in-flight call emits a
// periodic "still running" log line so an operator watching stderr
// sees the call hasn't deadlocked. Callers that already render
// per-call progress through another channel (CLI spinners, MCP
// progress notifications) should pass false to keep stderr quiet.
type LoggingLLM struct {
	inner            LLM
	recorder         *SessionRecorder
	HeartbeatEnabled bool
}

// NewLoggingLLM wraps inner with recording. Heartbeat defaults to off
// — callers turn it on with NewLoggingLLMWithHeartbeat when they
// don't have a per-call UI of their own. Existing callers that don't
// pass a heartbeat preference get silent behavior, matching the CLI
// rich path where the spinner is the visibility surface.
func NewLoggingLLM(inner LLM, recorder *SessionRecorder) *LoggingLLM {
	return &LoggingLLM{inner: inner, recorder: recorder}
}

// NewLoggingLLMWithHeartbeat is the same as NewLoggingLLM but
// configures the heartbeat. Used by --plain CLI mode and the MCP
// server, which do not own per-call UI.
func NewLoggingLLMWithHeartbeat(inner LLM, recorder *SessionRecorder, heartbeat bool) *LoggingLLM {
	return &LoggingLLM{inner: inner, recorder: recorder, HeartbeatEnabled: heartbeat}
}

// Generate delegates to the inner LLM and records the call. The
// recorder gets an in-progress entry at start so a tail of the session
// YAML reveals what's currently in flight. A heartbeat goroutine logs
// "still running" every heartbeatInterval so an operator watching
// stderr knows the call hasn't deadlocked even when the underlying
// non-streaming Generate produces no output of its own.
func (l *LoggingLLM) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	started := time.Now()
	role := RoleFromContext(ctx)
	handle := l.recorder.Begin(role, req, started)

	var stop func()
	if l.HeartbeatEnabled {
		stop = startHeartbeat(role, req.Model, started)
	} else {
		stop = func() {}
	}
	defer stop()

	resp, err := l.inner.Generate(ctx, req)
	handle.Finish(resp, err)
	return resp, err
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
func (l *LoggingLLM) Recorder() *SessionRecorder { return l.recorder }
