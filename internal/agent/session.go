package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
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

type recordedCall struct {
	Index        int               `yaml:"index"`
	Role         string            `yaml:"role,omitempty"`
	StartedAt    string            `yaml:"started_at"`
	DurationMS   int64             `yaml:"duration_ms"`
	Model        string            `yaml:"model"`
	Messages     []recordedMessage `yaml:"messages"`
	OutputSchema bool              `yaml:"output_schema,omitempty"`
	Response     string            `yaml:"response,omitempty"`
	TokensUsed   int               `yaml:"tokens_used,omitempty"`
	Error        string            `yaml:"error,omitempty"`
}

type recordedMessage struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
}

// NewSessionRecorder creates a session file at
// .locutus/sessions/<YYYYMMDD>/<HHMMSS>/<short>.yaml on fsys. The nested
// layout makes housekeeping easy — `rm -rf .locutus/sessions/20260420`
// drops every session from that day, no glob or jq required.
//
// command is recorded for human reference (e.g. "refine goals",
// "import docs/foo.md"). projectRoot is informational — included in the
// file metadata but not used for path resolution (fsys is already rooted).
func NewSessionRecorder(fsys specio.FS, command, projectRoot string) (*SessionRecorder, error) {
	ts := time.Now()
	short := newShortSessionID()
	dateDir := ts.Format("20060102")
	timeDir := ts.Format("150405")
	dir := path.Join(".locutus/sessions", dateDir, timeDir)
	relPath := path.Join(dir, short+".yaml")
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session recorder mkdir: %w", err)
	}
	// Composite session id retains the full timestamp + short suffix so a
	// single string identifies the session in logs and matches across
	// the path components.
	sid := dateDir + "-" + timeDir + "-" + short
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

// Record stores one LLM call. Safe for concurrent use — workflow
// executors run agents in parallel.
func (r *SessionRecorder) Record(role string, req GenerateRequest, resp *GenerateResponse, callErr error, started time.Time, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	call := recordedCall{
		Index:        len(r.session.Calls) + 1,
		Role:         role,
		StartedAt:    started.Format(time.RFC3339),
		DurationMS:   duration.Milliseconds(),
		Model:        req.Model,
		OutputSchema: req.OutputSchema != nil,
	}
	for _, m := range req.Messages {
		call.Messages = append(call.Messages, recordedMessage{Role: m.Role, Content: m.Content})
	}
	if resp != nil {
		call.Response = resp.Content
		call.TokensUsed = resp.TokensUsed
		if call.Model == "" {
			call.Model = resp.Model
		}
	}
	if callErr != nil {
		call.Error = callErr.Error()
	}
	r.session.Calls = append(r.session.Calls, call)

	// Best-effort: a flush failure shouldn't break the user's flow.
	// The recorder is a debug artifact; if disk fills, we lose the
	// transcript, not the user's actual work.
	_ = r.flush()
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
type LoggingLLM struct {
	inner    LLM
	recorder *SessionRecorder
}

// NewLoggingLLM wraps inner with recording.
func NewLoggingLLM(inner LLM, recorder *SessionRecorder) *LoggingLLM {
	return &LoggingLLM{inner: inner, recorder: recorder}
}

// Generate delegates to the inner LLM and records the call.
func (l *LoggingLLM) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	started := time.Now()
	resp, err := l.inner.Generate(ctx, req)
	duration := time.Since(started)

	role := RoleFromContext(ctx)
	l.recorder.Record(role, req, resp, err, started, duration)
	return resp, err
}

// Recorder exposes the underlying recorder so callers can read the
// session id / path for log messages.
func (l *LoggingLLM) Recorder() *SessionRecorder { return l.recorder }
