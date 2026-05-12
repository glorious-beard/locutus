package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRateLimitWaitCallback_FiresWithDuration verifies the callback
// receives the same sleep duration the executor is about to wait. The
// callback is the bridge that lets the sink layer flip a spinner to
// "rate-limited; waiting Ns" without exposing internal sleep state.
func TestRateLimitWaitCallback_FiresWithDuration(t *testing.T) {
	var (
		mu        sync.Mutex
		gotSleeps []time.Duration
	)

	cb := func(sleep time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		gotSleeps = append(gotSleeps, sleep)
	}

	ctx := WithRateLimitWaitCallback(context.Background(), cb)
	fromCtx := RateLimitWaitCallbackFromContext(ctx)
	require.NotNil(t, fromCtx, "callback must round-trip through the context helper")

	fromCtx(7 * time.Second)
	fromCtx(15 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []time.Duration{7 * time.Second, 15 * time.Second}, gotSleeps)
}

// TestRateLimitWaitCallback_NilWhenUnset ensures retrieving the
// callback from an unstamped context returns nil cleanly. Production
// code at Executor.runOnePickWithRetryAfter guards on nil before
// invoking — this test pins that the helper returns nil rather than a
// zero-valued func that would panic on call.
func TestRateLimitWaitCallback_NilWhenUnset(t *testing.T) {
	got := RateLimitWaitCallbackFromContext(context.Background())
	assert.Nil(t, got)
}
