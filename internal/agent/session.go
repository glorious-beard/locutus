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

	"github.com/chetan/locutus/internal/agent/adapters"
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

// sessionRecorderContextKey carries the SessionRecorder for the duration
// of a LoggingExecutor.Run delegation. Adapters consult it indirectly via
// adapters.CallRecorderFromContext, which receives a bridge wrapping
// the parent stepHandle the LoggingExecutor opened. Most call sites
// don't need to touch this directly — LoggingExecutor.Run plumbs it on
// the agent's behalf.
type sessionRecorderContextKey struct{}

// WithSessionRecorder tags ctx with the SessionRecorder so deeper
// layers (the dispatch retry path, ad-hoc child calls) can record
// additional sub-calls under the active session if needed. Production
// wiring is one call from LoggingExecutor.Run; ad-hoc callers leave
// it unset and recording stays off.
func WithSessionRecorder(ctx context.Context, r *SessionRecorder) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionRecorderContextKey{}, r)
}

// SessionRecorderFromContext returns the SessionRecorder set via
// WithSessionRecorder, or nil when none was set.
func SessionRecorderFromContext(ctx context.Context) *SessionRecorder {
	if v, ok := ctx.Value(sessionRecorderContextKey{}).(*SessionRecorder); ok {
		return v
	}
	return nil
}

// parentCallIDContextKey carries the parent step's id for child SDK
// calls. DJ-130 Phase 3 uses this to link adapter-emitted per-SDK-call
// records to the step.yaml the LoggingExecutor opens at delegation
// time. Mirrors the WithRole / WithAgentID / WithCallTag pattern.
type parentCallIDContextKey struct{}

// WithParentCallID tags ctx with the parent step id so child call
// records can carry parent_call_id pointing at the step.yaml that
// groups them. Production wiring is one call from LoggingExecutor.Run
// after it opens the parent step.
func WithParentCallID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, parentCallIDContextKey{}, id)
}

// ParentCallIDFromContext returns the parent call id set via
// WithParentCallID, or "" when none.
func ParentCallIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(parentCallIDContextKey{}).(string); ok {
		return v
	}
	return ""
}

// rateLimitWaitCallbackKey carries a callback invoked when a single
// LLM call hits a 429 with a usable Retry-After hint and the executor
// decides to sleep on the same pick rather than rotate. Distinct from
// retryCallbackKey: that one fires from RunWithRetry's full re-walk;
// this one fires from inside Executor.Run's per-pick wait. Both feed
// the same `retrying` event on the sink so the operator sees
// rate-limit pauses as transient spinner state, not as warning lines.
type rateLimitWaitCallbackKey struct{}

// WithRateLimitWaitCallback returns a context whose rate-limit wait
// path invokes fn(sleep) right before sleeping. `sleep` is the
// duration we're about to wait — usually the provider's Retry-After
// hint; occasionally the same hint clamped to the "no fallback"
// branch when this is the only pick.
//
// Distinct from WithRetryCallback because the trigger is per-attempt
// inside Executor.Run, not per-walk inside RunWithRetry. Both
// callbacks can be set on the same context; they fire independently.
func WithRateLimitWaitCallback(ctx context.Context, fn func(sleep time.Duration)) context.Context {
	return context.WithValue(ctx, rateLimitWaitCallbackKey{}, fn)
}

// RateLimitWaitCallbackFromContext returns the callback set via
// WithRateLimitWaitCallback, or nil if none.
func RateLimitWaitCallbackFromContext(ctx context.Context) func(time.Duration) {
	if v, ok := ctx.Value(rateLimitWaitCallbackKey{}).(func(time.Duration)); ok {
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

	// DJ-130 Phase 3: per-step folder bookkeeping. BeginStep
	// allocates a nextStepIndex, opens a folder under calls/, and
	// tracks the in-flight stepHandles so Close can mark a SIGKILL'd
	// step as interrupted alongside its child calls.
	inFlightSteps map[int]*stepHandle
	nextStepIndex int
}

// sessionManifest is the on-disk shape of <dir>/session.yaml.
// Intentionally small and stable: it's written once at construction
// and updated only on clean Close. The directory listing of calls/
// IS the calls list; no count or per-call summary is persisted here.
//
// TraceID is the W3C-format hex trace id (32 chars) shared by every
// OTel span emitted during this session. Surfaced alongside SessionID
// so a reader holding either id can find the other (the OTLP-JSON
// trace artifact at <dir>/trace.jsonl is keyed by TraceID; per-call
// YAMLs and this manifest are keyed by SessionID). Empty when the
// OTel SDK isn't initialized — existing fixtures that never call
// InitTracer stay byte-identical to today's manifest output.
type sessionManifest struct {
	SessionID   string `yaml:"session_id"`
	TraceID     string `json:"trace_id,omitempty" yaml:"trace_id,omitempty"`
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
	// SpanID is the hex span id of the matching `provider.generate`
	// OTel span. Cross-references this YAML to the OTLP-JSON trace at
	// <session>/trace.jsonl: a reader holding the span id can find the
	// per-call detail here, and a reader holding this YAML can find
	// the span (and its workflow.phase / agent.dispatch ancestors)
	// there. Empty when the OTel SDK isn't initialized — the no-op
	// tracer returns an invalid span context, SpanIDFromContext
	// returns "", and omitempty keeps the rendered YAML
	// byte-identical to today's fixtures.
	SpanID         string            `json:"span_id,omitempty" yaml:"span_id,omitempty"`
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
	// CacheCreationInputTokens / CacheReadInputTokens surface the
	// provider's prompt-cache metering. Anthropic populates both;
	// OpenAI Responses populates only Read (no separate creation
	// charge); Gemini leaves both at zero today. Operators read the
	// trace to confirm DJ-106 user-message caching is firing — a
	// non-zero CacheReadInputTokens on later council fanout calls is
	// the signal that the static prefix hit the cache.
	CacheCreationInputTokens int `yaml:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `yaml:"cache_read_input_tokens,omitempty"`
	// Citations is the aggregate of provider-native search sources
	// the call grounded against. Mirror of AgentOutput.Citations.
	// Surfaced at the top level so single-round grounded calls (the
	// common case for the researcher) emit citations without forcing
	// a Rounds entry.
	Citations []recordedCitation `yaml:"citations,omitempty"`
	// ToolCalls is the flat per-tool-invocation summary for
	// server-side tools (Anthropic web_search). Surfaced at the top
	// level so an operator inspecting a trace can immediately see
	// whether a query returned evidence or errored — without grepping
	// the encrypted raw_message blob.
	ToolCalls []recordedToolCall `yaml:"tool_calls,omitempty"`
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
	Index                    int                `yaml:"index"`
	Reasoning                string             `yaml:"reasoning,omitempty"`
	Text                     string             `yaml:"text,omitempty"`
	Message                  string             `yaml:"message,omitempty"`
	InputTokens              int                `yaml:"input_tokens,omitempty"`
	OutputTokens             int                `yaml:"output_tokens,omitempty"`
	ThoughtsTokens           int                `yaml:"thoughts_tokens,omitempty"`
	CacheCreationInputTokens int                `yaml:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                `yaml:"cache_read_input_tokens,omitempty"`
	Citations                []recordedCitation `yaml:"citations,omitempty"`
}

// recordedCitation is one provider-native search source surfaced in
// the session trace. Mirrors agent.Citation with YAML tags.
type recordedCitation struct {
	URL     string `yaml:"url,omitempty"`
	Title   string `yaml:"title,omitempty"`
	Snippet string `yaml:"snippet,omitempty"`
}

// recordedToolCall is one server-side tool invocation surfaced in the
// session trace. Mirrors agent.ToolCall with YAML tags so a per-call
// file shows the query plus its outcome inline:
//
//	tool_calls:
//	  - name: web_search
//	    query: "TanStack Start production ready stable release 2025"
//	    status: error
//	  - name: web_search
//	    query: "Next.js App Router cold start GCP Cloud Run performance 2024 2025"
//	    status: success
//
// Auditors reading a trace can immediately identify queries that
// returned no evidence — claims attributed to those queries should be
// treated as ungrounded regardless of what the model wrote.
type recordedToolCall struct {
	Name      string `yaml:"name"`
	Query     string `yaml:"query,omitempty"`
	Status    string `yaml:"status"`
	ErrorCode string `yaml:"error_code,omitempty"`
}

type recordedMessage struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
}

// recordedStep is the on-disk shape of a per-step `step.yaml` — the
// parent record DJ-130 Phase 3 added so per-step folders carry a
// summary the operator can read first before drilling into per-SDK
// child YAMLs. Token counts sum across children; duration spans from
// first child start to last child finish; ChildCalls names each
// child in order.
type recordedStep struct {
	Index         int      `yaml:"index"`
	AgentID       string   `yaml:"agent_id,omitempty"`
	Role          string   `yaml:"role,omitempty"`
	CallTag       string   `yaml:"call_tag,omitempty"`
	Status        string   `yaml:"status,omitempty"`
	StartedAt     string   `yaml:"started_at"`
	CompletedAt   string   `yaml:"completed_at,omitempty"`
	DurationMS    int64    `yaml:"duration_ms,omitempty"`
	Model         string   `yaml:"model,omitempty"`
	OutputSchema  bool     `yaml:"output_schema,omitempty"`
	SpanID        string   `yaml:"span_id,omitempty"`
	InputTokens   int      `yaml:"input_tokens,omitempty"`
	OutputTokens  int      `yaml:"output_tokens,omitempty"`
	ThoughtsTokens int     `yaml:"thoughts_tokens,omitempty"`
	TotalTokens   int      `yaml:"total_tokens,omitempty"`
	CacheCreationInputTokens int `yaml:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `yaml:"cache_read_input_tokens,omitempty"`
	ChildCalls    []string `yaml:"child_calls,omitempty"`
	Error         string   `yaml:"error,omitempty"`
}

// CallsDirName is the subdirectory under a session directory that
// holds per-call YAML files. Exported for tools that walk a session.
const CallsDirName = "calls"

// StepFileName is the parent-summary filename written under each
// per-step folder under <session>/calls/<NNNN>-<agent>[-<tag>]/.
const StepFileName = "step.yaml"

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
		inFlight:      make(map[int]*callHandle),
		inFlightSteps: make(map[int]*stepHandle),
	}
	if err := rec.writeManifest(); err != nil {
		return nil, err
	}
	return rec, nil
}

// SessionID returns the session ID (also the directory basename).
func (r *SessionRecorder) SessionID() string { return r.manifest.SessionID }

// SetTraceID stamps the W3C trace id (32 hex chars) on the manifest
// and reflushes session.yaml. Called by the CLI after InitTracer
// returns and a root span is opened so the manifest cross-references
// the OTLP-JSON trace artifact.
//
// The id is picked up by reading the active span context from a
// caller-supplied ctx — the cmd layer constructs the recorder, opens
// its verb-level span, then calls SetTraceID with the span's ctx.
// Best-effort: a flush failure logs and proceeds (the per-call YAMLs
// are still on disk; only the manifest pointer is missing).
func (r *SessionRecorder) SetTraceID(traceID string) {
	r.mu.Lock()
	r.manifest.TraceID = traceID
	r.mu.Unlock()
	if err := r.writeManifest(); err != nil {
		slog.Warn("session recorder: trace id flush failed",
			"session", r.manifest.SessionID, "error", err)
	}
}

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
//
// DJ-130 Phase 3: when the handle was opened via stepHandle.BeginChild
// (a per-SDK-call record under a parent step folder), stepRef points
// at the owning step and callID names the child's stable id (used in
// the step.yaml's child_calls list and as the per-call YAML filename
// minus extension). Both nil/empty for flat callHandles opened via
// the legacy SessionRecorder.Begin path.
type callHandle struct {
	recorder *SessionRecorder
	index    int
	filePath string
	started  time.Time
	call     recordedCall

	stepRef *stepHandle
	callID  string
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
		h.call.CacheCreationInputTokens = out.CacheCreationInputTokens
		h.call.CacheReadInputTokens = out.CacheReadInputTokens
		if h.call.Model == "" {
			h.call.Model = out.Model
		}
		// Multi-round tool-use captures: copy each round's snapshot
		// into the per-call file so the trace shows what the model
		// emitted in each round (including tool_request parts), not
		// just the final response. Single-round calls leave Rounds
		// nil — the top-level Reasoning/Response/RawMessage already
		// carry that round's data.
		if len(out.Citations) > 0 {
			h.call.Citations = make([]recordedCitation, len(out.Citations))
			for i, c := range out.Citations {
				h.call.Citations[i] = recordedCitation{URL: c.URL, Title: c.Title, Snippet: c.Snippet}
			}
		}
		if len(out.ToolCalls) > 0 {
			h.call.ToolCalls = make([]recordedToolCall, len(out.ToolCalls))
			for i, t := range out.ToolCalls {
				h.call.ToolCalls[i] = recordedToolCall{
					Name:      t.Name,
					Query:     t.Query,
					Status:    t.Status,
					ErrorCode: t.ErrorCode,
				}
			}
		}
		if len(out.Rounds) > 0 {
			h.call.Rounds = make([]recordedRound, len(out.Rounds))
			for i, r := range out.Rounds {
				rr := recordedRound{
					Index:                    r.Index,
					Reasoning:                r.Reasoning,
					Text:                     r.Text,
					Message:                  r.Message,
					InputTokens:              r.InputTokens,
					OutputTokens:             r.OutputTokens,
					ThoughtsTokens:           r.ThoughtsTokens,
					CacheCreationInputTokens: r.CacheCreationInputTokens,
					CacheReadInputTokens:     r.CacheReadInputTokens,
				}
				if len(r.Citations) > 0 {
					rr.Citations = make([]recordedCitation, len(r.Citations))
					for j, c := range r.Citations {
						rr.Citations[j] = recordedCitation{URL: c.URL, Title: c.Title, Snippet: c.Snippet}
					}
				}
				h.call.Rounds[i] = rr
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

// stepHandle is the parent-step bookkeeping LoggingExecutor.Run opens
// before delegating. Adapters discover it (indirectly via the
// callRecorderBridge) and call BeginChild for each SDK round-trip;
// the agent-side LoggingExecutor finalizes the step after the inner
// Run returns. Children written under the step's folder; the parent
// step.yaml carries summed token counts + child id list + duration.
type stepHandle struct {
	recorder *SessionRecorder
	index    int
	dir      string
	filePath string
	started  time.Time
	step     recordedStep

	mu           sync.Mutex
	childCounter int
	children     []*callHandle
}

// childCallID returns a stable identifier for a child file (no
// extension). Mirrors callFilePath's name shape but bounded to the
// step folder (4-digit step + 2-digit child + role).
func (s *stepHandle) childCallID(role string) (filename, callID string, idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.childCounter++
	idx = s.childCounter
	base := fmt.Sprintf("%02d", idx)
	if role != "" {
		base = base + "-" + role
	}
	return base + ".yaml", base, idx
}

// BeginChild opens a child per-SDK-call record under this step's
// folder. Returns the callHandle so callers can Finish it with the
// adapter Response. Safe for concurrent use; the child counter is
// atomic with respect to the step's mutex.
func (s *stepHandle) BeginChild(role, model string, def AgentDef, input AgentInput, started time.Time) *callHandle {
	filename, callID, idx := s.childCallID(role)
	call := recordedCall{
		Index:        idx,
		AgentID:      s.step.AgentID,
		Role:         role,
		Status:       CallStatusInProgress,
		StartedAt:    started.Format(time.RFC3339),
		Model:        model,
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
		recorder: s.recorder,
		index:    idx,
		filePath: path.Join(s.dir, filename),
		started:  started,
		call:     call,
		stepRef:  s,
		callID:   callID,
	}
	s.mu.Lock()
	s.children = append(s.children, h)
	s.mu.Unlock()
	if err := h.flush(); err != nil {
		slog.Warn("session recorder: child call in-progress flush failed",
			"session", s.recorder.manifest.SessionID, "step", s.index, "child", idx, "error", err)
	}
	return h
}

// Finish finalizes the step.yaml: sums token counts across children,
// computes duration, lists child call ids, sets status. Idempotent
// on nil. Called by LoggingExecutor.Run after the inner adapter
// dispatch returns.
func (s *stepHandle) Finish(err error) {
	if s == nil {
		return
	}
	completedAt := time.Now()
	s.step.CompletedAt = completedAt.Format(time.RFC3339)
	s.step.DurationMS = completedAt.Sub(s.started).Milliseconds()
	if err != nil {
		s.step.Status = CallStatusError
		s.step.Error = err.Error()
	} else {
		s.step.Status = CallStatusCompleted
	}

	s.mu.Lock()
	ids := make([]string, 0, len(s.children))
	for _, c := range s.children {
		ids = append(ids, c.callID)
		s.step.InputTokens += c.call.InputTokens
		s.step.OutputTokens += c.call.OutputTokens
		s.step.ThoughtsTokens += c.call.ThoughtsTokens
		s.step.TotalTokens += c.call.TotalTokens
		s.step.CacheCreationInputTokens += c.call.CacheCreationInputTokens
		s.step.CacheReadInputTokens += c.call.CacheReadInputTokens
		// First child's model is representative (the strong-tier
		// reasoning pass for splits; the single model otherwise).
		if s.step.Model == "" && c.call.Model != "" {
			s.step.Model = c.call.Model
		}
	}
	s.step.ChildCalls = ids
	s.mu.Unlock()

	if flushErr := s.flush(); flushErr != nil {
		slog.Warn("session recorder: step finish flush failed",
			"session", s.recorder.manifest.SessionID, "step", s.index, "error", flushErr)
	}
	s.recorder.mu.Lock()
	delete(s.recorder.inFlightSteps, s.index)
	s.recorder.mu.Unlock()
}

func (s *stepHandle) flush() error {
	data, err := yaml.Marshal(&s.step)
	if err != nil {
		return err
	}
	return specio.AtomicWriteFile(s.recorder.fsys, s.filePath, data, 0o644)
}

// BeginStep opens the parent step record DJ-130 Phase 3 introduced.
// LoggingExecutor.Run calls this before delegating; adapter-emitted
// per-SDK-call records (via callRecorderBridge below) land under the
// returned step's folder. Safe for concurrent use.
func (r *SessionRecorder) BeginStep(role, agentID, callTag string, def AgentDef, input AgentInput, started time.Time) *stepHandle {
	r.mu.Lock()
	r.nextStepIndex++
	idx := r.nextStepIndex
	r.mu.Unlock()

	dir := r.stepDirPath(idx, agentID, callTag)
	if err := r.fsys.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("session recorder: step mkdir failed",
			"session", r.manifest.SessionID, "step", idx, "error", err)
	}
	step := recordedStep{
		Index:        idx,
		AgentID:      agentID,
		Role:         role,
		CallTag:      callTag,
		Status:       CallStatusInProgress,
		StartedAt:    started.Format(time.RFC3339),
		OutputSchema: def.OutputSchema != "",
	}
	h := &stepHandle{
		recorder: r,
		index:    idx,
		dir:      dir,
		filePath: path.Join(dir, StepFileName),
		started:  started,
		step:     step,
	}
	r.mu.Lock()
	r.inFlightSteps[idx] = h
	r.mu.Unlock()
	if err := h.flush(); err != nil {
		slog.Warn("session recorder: in-progress step flush failed",
			"session", r.manifest.SessionID, "step", idx, "error", err)
	}
	return h
}

// stepDirPath builds the per-step folder path:
//
//	<dir>/calls/<NNNN>-<agent>-<tag>/   when agent and tag are set
//	<dir>/calls/<NNNN>-<agent>/         when only agent is set
//	<dir>/calls/<NNNN>/                 when neither is set
func (r *SessionRecorder) stepDirPath(idx int, agentID, callTag string) string {
	name := fmt.Sprintf("%04d", idx)
	if agentID != "" {
		name = name + "-" + agentID
	}
	if callTag != "" {
		name = name + "-" + callTag
	}
	return path.Join(r.dir, CallsDirName, name)
}

// callRecorderBridge adapts a stepHandle so it satisfies the
// adapters.CallRecorder interface — adapters call Begin/Finish on
// per-SDK-call handles; the bridge routes the work onto stepHandle's
// per-step folder. Defined here (not in adapters/) because the bridge
// needs to convert adapters.Request → agent.AgentInput, which would
// import-cycle the other direction.
type callRecorderBridge struct {
	step *stepHandle
}

func (b *callRecorderBridge) Begin(ctx context.Context, role, model string, req adapters.Request, started time.Time) adapters.CallHandle {
	// Project adapters.Request → AgentDef + AgentInput so the
	// recorded YAML carries the messages the adapter actually sent
	// (not the messages the upper layer originally projected — they
	// can diverge mid-split when the format pass swaps in
	// CanonicalFormatterPrompt + the reasoning pass's output).
	def := AgentDef{
		ID:           b.step.step.AgentID,
		SystemPrompt: req.SystemPrompt,
		OutputSchema: "",
	}
	if req.OutputSchema != nil {
		def.OutputSchema = "<schema>" // truthy marker so recordedCall.OutputSchema reflects the call shape
	}
	input := AgentInput{}
	for _, m := range req.Messages {
		input.Messages = append(input.Messages, Message{Role: string(m.Role), Content: m.Content})
	}
	child := b.step.BeginChild(role, model, def, input, started)
	child.call.SpanID = SpanIDFromContext(ctx)
	return &callHandleBridge{child: child}
}

// callHandleBridge bridges adapters.Response → AgentOutput on Finish
// so the per-SDK-call YAML carries the same fields LoggingExecutor.Run
// stamped pre-DJ-130 (tokens, citations, tool calls, rounds).
type callHandleBridge struct {
	child *callHandle
}

func (b *callHandleBridge) Finish(resp *adapters.Response, err error) {
	if b == nil || b.child == nil {
		return
	}
	out := agentOutputFromAdapterResponse(resp)
	b.child.Finish(out, err)
}

// agentOutputFromAdapterResponse is a local subset of the projection
// in executor.outputFromResponse, narrowed to what the per-call
// recorder cares about. Defined here so the bridge doesn't have to
// reach into the executor package.
func agentOutputFromAdapterResponse(resp *adapters.Response) *AgentOutput {
	if resp == nil {
		return nil
	}
	out := &AgentOutput{
		Content:                  resp.Content,
		Reasoning:                resp.Reasoning,
		RawMessage:               resp.RawMessage,
		Model:                    resp.Model,
		InputTokens:              resp.InputTokens,
		OutputTokens:             resp.OutputTokens,
		ThoughtsTokens:           resp.ThoughtsTokens,
		TotalTokens:              resp.TotalTokens,
		CacheCreationInputTokens: resp.CacheCreationInputTokens,
		CacheReadInputTokens:     resp.CacheReadInputTokens,
	}
	if len(resp.Citations) > 0 {
		out.Citations = make([]Citation, len(resp.Citations))
		for i, c := range resp.Citations {
			out.Citations[i] = Citation{URL: c.URL, Title: c.Title, Snippet: c.Snippet}
		}
	}
	if len(resp.ToolCalls) > 0 {
		out.ToolCalls = make([]ToolCall, len(resp.ToolCalls))
		for i, t := range resp.ToolCalls {
			out.ToolCalls[i] = ToolCall{Name: t.Name, Query: t.Query, Status: t.Status, ErrorCode: t.ErrorCode}
		}
	}
	if len(resp.Rounds) > 1 {
		out.Rounds = make([]GenerateRound, len(resp.Rounds))
		for i, r := range resp.Rounds {
			gr := GenerateRound{
				Index:                    r.Index,
				Reasoning:                r.Reasoning,
				Text:                     r.Text,
				Message:                  r.Message,
				InputTokens:              r.InputTokens,
				OutputTokens:             r.OutputTokens,
				ThoughtsTokens:           r.ThoughtsTokens,
				CacheCreationInputTokens: r.CacheCreationInputTokens,
				CacheReadInputTokens:     r.CacheReadInputTokens,
			}
			if len(r.Citations) > 0 {
				gr.Citations = make([]Citation, len(r.Citations))
				for j, c := range r.Citations {
					gr.Citations[j] = Citation{URL: c.URL, Title: c.Title, Snippet: c.Snippet}
				}
			}
			out.Rounds[i] = gr
		}
	}
	return out
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
	stepStragglers := make([]*stepHandle, 0, len(r.inFlightSteps))
	for _, s := range r.inFlightSteps {
		stepStragglers = append(stepStragglers, s)
	}
	r.inFlightSteps = make(map[int]*stepHandle)
	r.mu.Unlock()

	for _, h := range stragglers {
		h.call.Status = CallStatusInterrupted
		if err := h.flush(); err != nil {
			slog.Warn("session recorder: close flush failed",
				"session", r.manifest.SessionID, "index", h.index, "error", err)
		}
	}
	for _, s := range stepStragglers {
		s.step.Status = CallStatusInterrupted
		if err := s.flush(); err != nil {
			slog.Warn("session recorder: close step flush failed",
				"session", r.manifest.SessionID, "step", s.index, "error", err)
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

// Run delegates to the inner AgentExecutor and records the call.
//
// DJ-130 Phase 3 reshape: instead of a flat one-YAML-per-Run record,
// Run opens a parent stepHandle (writes <step>/step.yaml with status
// in_progress) and plumbs a callRecorderBridge onto ctx so the
// downstream adapter Run / runSplit emits one per-SDK-call YAML per
// real provider round-trip — children under the step's folder. After
// the inner Run returns, stepHandle.Finish sums child token counts +
// computes duration + finalizes the step.yaml.
//
// A heartbeat goroutine logs "still running" every heartbeatInterval
// so an operator watching stderr knows the call hasn't deadlocked
// even when the underlying non-streaming Run produces no output of
// its own.
func (l *LoggingExecutor) Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error) {
	started := time.Now()
	role := RoleFromContext(ctx)
	agentID := AgentIDFromContext(ctx)
	callTag := CallTagFromContext(ctx)
	step := l.recorder.BeginStep(role, agentID, callTag, def, input, started)

	// Plumb the recorder + parent id so adapters can emit per-SDK-call
	// child records into the step's folder. WithSessionRecorder is for
	// callers that want to access the recorder directly; the bridge is
	// what the adapter layer actually consults via
	// adapters.CallRecorderFromContext.
	ctx = WithSessionRecorder(ctx, l.recorder)
	ctx = WithParentCallID(ctx, fmt.Sprintf("step-%04d", step.index))
	ctx = adapters.WithCallRecorder(ctx, &callRecorderBridge{step: step})

	var stop func()
	if l.HeartbeatEnabled {
		stop = startHeartbeat(role, def.ID, started)
	} else {
		stop = func() {}
	}
	defer stop()

	out, err := l.inner.Run(ctx, def, input)
	if step != nil {
		step.step.SpanID = SpanIDFromContext(ctx)
	}
	step.Finish(err)
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
