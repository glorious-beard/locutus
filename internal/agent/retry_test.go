package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunWithRetry_HonorsRetryAfter confirms RunWithRetry uses the
// Retry-After hint from a *RateLimitError instead of its own
// exponential backoff when the provider supplied one. The test
// configures BaseDelay/MaxDelay to be much larger than the hint and
// asserts the actual sleep matches the hint.
func TestRunWithRetry_HonorsRetryAfter(t *testing.T) {
	exec := &recordingExec{
		responses: []execResult{
			{err: &adapters.RateLimitError{RetryAfter: 50 * time.Millisecond}},
			{out: &AgentOutput{Content: "ok"}},
		},
	}
	cfg := RetryConfig{
		MaxAttempts: 3,
		// BaseDelay much larger than the hint so we can confirm
		// the hint is actually load-bearing.
		BaseDelay: 5 * time.Second,
		MaxDelay:  10 * time.Second,
	}

	start := time.Now()
	out, err := RunWithRetry(context.Background(), exec, AgentDef{ID: "test"}, AgentInput{}, cfg)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "ok", out.Content)
	assert.Less(t, elapsed, 1*time.Second, "RunWithRetry should sleep ~50ms (the Retry-After hint), not the 5s BaseDelay")
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "must still wait the hinted duration")
}

// TestRunWithRetry_FallsBackToExponentialWhenNoHint confirms the
// classic exponential-backoff path still fires when the error
// doesn't carry a Retry-After hint (e.g., Gemini rate-limit, or
// transient timeout). With BaseDelay=10ms the second attempt fires
// after that delay.
func TestRunWithRetry_FallsBackToExponentialWhenNoHint(t *testing.T) {
	exec := &recordingExec{
		responses: []execResult{
			{err: adapters.ErrRateLimit}, // plain sentinel, no hint
			{out: &AgentOutput{Content: "ok"}},
		},
	}
	cfg := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    100 * time.Millisecond,
	}

	start := time.Now()
	out, err := RunWithRetry(context.Background(), exec, AgentDef{ID: "test"}, AgentInput{}, cfg)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "ok", out.Content)
	assert.GreaterOrEqual(t, elapsed, 10*time.Millisecond, "must wait BaseDelay between attempts")
	assert.Less(t, elapsed, 1*time.Second, "no hint, so exponential from BaseDelay")
}

// recordingExec is a minimal AgentExecutor that returns a scripted
// sequence of (output, error) per call. Used to drive RunWithRetry
// through specific failure paths without touching real adapters.
type recordingExec struct {
	responses []execResult
	calls     int
}

type execResult struct {
	out *AgentOutput
	err error
}

func (e *recordingExec) Run(ctx context.Context, def AgentDef, input AgentInput) (*AgentOutput, error) {
	if e.calls >= len(e.responses) {
		return nil, errors.New("recordingExec: out of scripted responses")
	}
	r := e.responses[e.calls]
	e.calls++
	return r.out, r.err
}
