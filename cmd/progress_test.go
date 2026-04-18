package cmd

import (
	"context"
	"sync"
	"testing"

	"github.com/chetan/locutus/internal/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSession implements the narrow mcpProgressSession interface so
// tests can verify what the wrapper sends to the SDK.
type fakeSession struct {
	mu    sync.Mutex
	calls []*mcp.ProgressNotificationParams
	err   error
}

func (f *fakeSession) NotifyProgress(ctx context.Context, params *mcp.ProgressNotificationParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, params)
	return f.err
}

func (f *fakeSession) snapshot() []*mcp.ProgressNotificationParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*mcp.ProgressNotificationParams, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestNewProgressNotifier_NilTokenReturnsNil(t *testing.T) {
	// MCP spec: a caller without a progress token isn't asking for
	// progress updates. The wrapper should short-circuit so the
	// supervisor can skip emission entirely.
	n := newProgressNotifier(&fakeSession{}, nil)
	assert.Nil(t, n, "nil token must produce a nil notifier")
}

func TestMCPHandler_WrapsSessionNotifier(t *testing.T) {
	sess := &fakeSession{}
	notifier := newProgressNotifier(sess, "tok-123")
	require.NotNil(t, notifier)

	err := notifier.Notify(context.Background(), dispatch.ProgressParams{
		Message: "Agent Edit: cmd/auth.go",
		Current: 3,
		Total:   10,
	})
	require.NoError(t, err)

	calls := sess.snapshot()
	require.Len(t, calls, 1)
	p := calls[0]
	assert.Equal(t, "tok-123", p.ProgressToken,
		"token captured at construction must flow to every Notify call")
	assert.Equal(t, "Agent Edit: cmd/auth.go", p.Message)
	assert.InDelta(t, 3.0, p.Progress, 0.001, "Current -> Progress float")
	assert.InDelta(t, 10.0, p.Total, 0.001, "Total -> Total float")
}

func TestMCPHandler_WrapsSessionNotifier_DefaultsZero(t *testing.T) {
	// Message-only calls (the common case from Supervisor.emitProgress)
	// should produce a valid ProgressNotificationParams without requiring
	// the dispatch layer to compute Progress/Total fractions it doesn't
	// have.
	sess := &fakeSession{}
	notifier := newProgressNotifier(sess, "tok-x")
	require.NoError(t, notifier.Notify(context.Background(), dispatch.ProgressParams{
		Message: "Agent wants permission: Bash",
	}))

	calls := sess.snapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, "tok-x", calls[0].ProgressToken)
	assert.Equal(t, "Agent wants permission: Bash", calls[0].Message)
	assert.InDelta(t, 0.0, calls[0].Progress, 0.001)
	assert.InDelta(t, 0.0, calls[0].Total, 0.001)
}

func TestMCPHandler_SessionErrorPropagates(t *testing.T) {
	sess := &fakeSession{err: assert.AnError}
	notifier := newProgressNotifier(sess, "tok-err")
	err := notifier.Notify(context.Background(), dispatch.ProgressParams{Message: "x"})
	assert.Error(t, err, "session errors should surface to the caller; supervisor decides whether to log or abort")
}

func TestMCPHandler_MultipleNotifiesSequential(t *testing.T) {
	// A single tool invocation typically produces many progress updates
	// as the agent works; the wrapper should forward them all, in order,
	// all tagged with the same token.
	sess := &fakeSession{}
	notifier := newProgressNotifier(sess, "tok-multi")

	messages := []string{
		"Agent Read: cmd/auth.go",
		"Agent wants permission: Bash",
		"Agent Edit: cmd/auth.go",
	}
	for _, m := range messages {
		require.NoError(t, notifier.Notify(context.Background(), dispatch.ProgressParams{Message: m}))
	}

	calls := sess.snapshot()
	require.Len(t, calls, len(messages))
	for i, c := range calls {
		assert.Equal(t, messages[i], c.Message, "message at index %d", i)
		assert.Equal(t, "tok-multi", c.ProgressToken, "token consistent across calls")
	}
}
