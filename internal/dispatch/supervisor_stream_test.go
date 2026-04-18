package dispatch

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStreamParser yields a scripted sequence of events. If errs[i] is
// non-nil, Next returns (events[i], errs[i]); otherwise it returns events[i]
// with no error. After the slice is exhausted, Next returns io.EOF.
type fakeStreamParser struct {
	events      []AgentEvent
	errs        []error
	pos         int
	closeCalled bool
}

func (p *fakeStreamParser) Next(ctx context.Context) (AgentEvent, error) {
	if err := ctx.Err(); err != nil {
		return AgentEvent{}, err
	}
	if p.pos >= len(p.events) {
		return AgentEvent{}, io.EOF
	}
	i := p.pos
	p.pos++
	var err error
	if i < len(p.errs) {
		err = p.errs[i]
	}
	return p.events[i], err
}

func (p *fakeStreamParser) Close() error {
	p.closeCalled = true
	return nil
}

// fakeStreamingDriver implements StreamingDriver with a pre-canned parser.
type fakeStreamingDriver struct {
	parser StreamParser
}

func (f *fakeStreamingDriver) BuildCommand(step spec.PlanStep, workDir string) *exec.Cmd {
	return exec.Command("echo", "mock")
}

func (f *fakeStreamingDriver) BuildRetryCommand(step spec.PlanStep, workDir, sessionID, feedback string) *exec.Cmd {
	return exec.Command("echo", "mock-retry")
}

func (f *fakeStreamingDriver) ParseStream(r io.Reader) StreamParser {
	return f.parser
}

func (f *fakeStreamingDriver) RespondToAgent(sessionID, response string) (*exec.Cmd, error) {
	return exec.Command("echo", "mock-resume"), nil
}

// mockNotifier captures Notify calls for assertion.
type mockNotifier struct {
	mu    sync.Mutex
	calls []ProgressParams
}

func (m *mockNotifier) Notify(ctx context.Context, p ProgressParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, p)
	return nil
}

func (m *mockNotifier) snapshot() []ProgressParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ProgressParams, len(m.calls))
	copy(out, m.calls)
	return out
}

// trackingRunnerWithClose returns a runner that records when its ReadCloser
// is closed. We don't need real bytes since the fake parser is scripted.
type trackingReadCloser struct {
	closed bool
}

func (t *trackingReadCloser) Read(p []byte) (int, error) { return 0, io.EOF }
func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}

// newTestSupervisorStream builds a Supervisor with batchRunner + a scripted
// fake streaming driver. Returns the supervisor, the driver, and the
// tracking ReadCloser so tests can assert lifecycle.
func newTestSupervisorStream(t *testing.T, parser StreamParser, notifier ProgressNotifier) (*Supervisor, *fakeStreamingDriver, *trackingReadCloser) {
	t.Helper()
	rc := &trackingReadCloser{}
	runner := func(cmd *exec.Cmd) (io.ReadCloser, error) { return rc, nil }
	sup := NewSupervisor(SupervisorConfig{
		MaxRetries:       1,
		ProgressNotifier: notifier,
	}, runner)
	return sup, &fakeStreamingDriver{parser: parser}, rc
}

func TestRunAttempt_Happy(t *testing.T) {
	events := []AgentEvent{
		{Kind: EventInit, SessionID: "sess-1", Timestamp: time.Now()},
		{Kind: EventText, Text: "analyzing", SessionID: "sess-1"},
		{Kind: EventToolCall, ToolName: "Read", ToolInput: map[string]any{"file_path": "/a.go"}, FilePaths: []string{"/a.go"}, SessionID: "sess-1"},
		{Kind: EventToolResult, Text: "file contents", SessionID: "sess-1"},
		{Kind: EventResult, Text: "done", SessionID: "sess-1"},
	}
	parser := &fakeStreamParser{events: events}
	sup, driver, rc := newTestSupervisorStream(t, parser, nil)

	result, err := sup.runAttempt(context.Background(), newTestStep(), driver, "/tmp/work", "", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "sess-1", result.sessionID, "session ID should be captured")
	assert.Equal(t, "done", result.finalText, "final text should come from EventResult")
	assert.Contains(t, result.files, "/a.go", "file paths from tool calls should accumulate")
	assert.True(t, parser.closeCalled, "parser should be closed after attempt")
	assert.True(t, rc.closed, "stream reader should be closed after attempt")
}

func TestRunAttempt_ParserErrorPropagates(t *testing.T) {
	parseErr := errors.New("boom: malformed NDJSON on line 3")
	events := []AgentEvent{
		{Kind: EventInit, SessionID: "sess-2"},
		{}, // error comes with this slot
	}
	errs := []error{nil, parseErr}
	parser := &fakeStreamParser{events: events, errs: errs}
	sup, driver, rc := newTestSupervisorStream(t, parser, nil)

	result, err := sup.runAttempt(context.Background(), newTestStep(), driver, "/tmp/work", "", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, parseErr, "runAttempt must surface the parser error")
	require.NotNil(t, result)
	assert.Equal(t, "sess-2", result.sessionID, "events seen before the error should still be captured")
	assert.True(t, parser.closeCalled, "parser must be closed even on error")
	assert.True(t, rc.closed, "stream reader must be closed even on error")
}

func TestRunAttempt_CtxCancel(t *testing.T) {
	// Give the parser enough events that it would not exit on its own.
	events := make([]AgentEvent, 20)
	for i := range events {
		events[i] = AgentEvent{Kind: EventText, Text: "chunk"}
	}
	parser := &fakeStreamParser{events: events}
	sup, driver, rc := newTestSupervisorStream(t, parser, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before runAttempt starts

	_, err := sup.runAttempt(ctx, newTestStep(), driver, "/tmp/work", "", "")
	require.ErrorIs(t, err, context.Canceled, "runAttempt must surface ctx.Canceled")
	assert.True(t, parser.closeCalled, "parser must be closed on ctx cancel")
	assert.True(t, rc.closed, "stream reader must be closed on ctx cancel")
}

func TestRunAttempt_EmitsProgressForToolCalls(t *testing.T) {
	events := []AgentEvent{
		{Kind: EventInit, SessionID: "sess-3"},
		{Kind: EventToolCall, ToolName: "Edit", ToolInput: map[string]any{"file_path": "cmd/auth.go"}, FilePaths: []string{"cmd/auth.go"}, SessionID: "sess-3"},
		{Kind: EventToolResult, Text: "ok", SessionID: "sess-3"},
		{Kind: EventText, Text: "edited", SessionID: "sess-3"}, // should NOT emit progress
		{Kind: EventResult, Text: "complete", SessionID: "sess-3"},
	}
	parser := &fakeStreamParser{events: events}
	notifier := &mockNotifier{}
	sup, driver, _ := newTestSupervisorStream(t, parser, notifier)

	_, err := sup.runAttempt(context.Background(), newTestStep(), driver, "/tmp/work", "", "")
	require.NoError(t, err)

	calls := notifier.snapshot()
	require.NotEmpty(t, calls, "notifier should have received progress for the tool call")

	var toolCallMsgs []string
	for _, c := range calls {
		toolCallMsgs = append(toolCallMsgs, c.Message)
	}

	found := false
	for _, m := range toolCallMsgs {
		if strings.Contains(m, "Edit") && strings.Contains(m, "cmd/auth.go") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a Notify call mentioning Edit + cmd/auth.go; got: %v", toolCallMsgs)

	// EventText (raw text chunks) should NOT trigger a Notify.
	for _, m := range toolCallMsgs {
		assert.NotContains(t, m, "edited", "EventText chunks should be suppressed")
	}
}
