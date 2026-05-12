package agent

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/chetan/locutus/internal/agent/adapters"
	"github.com/stretchr/testify/assert"
)

// TestMaybeWaitOnPick_RoundsThroughEveryBranch covers the hybrid
// decision matrix the Run loop relies on. Each case maps to one
// row of the rule table in maybeWaitOnPick's doc comment.
func TestMaybeWaitOnPick_RoundsThroughEveryBranch(t *testing.T) {
	threshold := rateLimitWaitThresholdDefault

	cases := []struct {
		name        string
		err         error
		pickIdx     int
		pickCount   int
		wantWait    bool
		wantSleep   time.Duration
		wantSource  string
	}{
		{
			name:     "non-rate-limit error never waits",
			err:      errors.New("schema validation failed"),
			pickIdx:  0, pickCount: 3,
			wantWait: false,
		},
		{
			name:     "rate-limit without Retry-After advances",
			err:      &adapters.RateLimitError{RetryAfter: 0},
			pickIdx:  0, pickCount: 3,
			wantWait: false,
		},
		{
			name:       "Retry-After under threshold waits",
			err:        &adapters.RateLimitError{RetryAfter: 15 * time.Second},
			pickIdx:    0, pickCount: 3,
			wantWait:   true,
			wantSleep:  15 * time.Second,
			wantSource: "retry-after",
		},
		{
			name:       "Retry-After at threshold waits (boundary)",
			err:        &adapters.RateLimitError{RetryAfter: threshold},
			pickIdx:    0, pickCount: 3,
			wantWait:   true,
			wantSleep:  threshold,
			wantSource: "retry-after",
		},
		{
			name:     "Retry-After over threshold with next pick advances",
			err:      &adapters.RateLimitError{RetryAfter: threshold + time.Second},
			pickIdx:  0, pickCount: 3,
			wantWait: false,
		},
		{
			name:       "Retry-After over threshold with no next pick still waits",
			err:        &adapters.RateLimitError{RetryAfter: threshold + 10*time.Second},
			pickIdx:    0, pickCount: 1,
			wantWait:   true,
			wantSleep:  threshold + 10*time.Second,
			wantSource: "retry-after-no-fallback",
		},
		{
			name:     "Retry-After over threshold on last pick of multi-pick walk still waits",
			err:      &adapters.RateLimitError{RetryAfter: threshold + 5*time.Second},
			pickIdx:  2, pickCount: 3, // we're at the last index
			wantWait:   true,
			wantSleep:  threshold + 5*time.Second,
			wantSource: "retry-after-no-fallback",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sleep, source, wait := maybeWaitOnPick(tc.err, tc.pickIdx, tc.pickCount)
			assert.Equal(t, tc.wantWait, wait, "wait verdict")
			if tc.wantWait {
				assert.Equal(t, tc.wantSleep, sleep, "sleep duration")
				assert.Equal(t, tc.wantSource, source, "source label")
			}
		})
	}
}

// TestRateLimitWaitThreshold_HonorsEnvOverride verifies the env knob
// changes the threshold at the granularity of "next call". Hot-path
// re-read is intentional so an operator can adjust mid-run.
func TestRateLimitWaitThreshold_HonorsEnvOverride(t *testing.T) {
	t.Setenv("LOCUTUS_RATE_LIMIT_WAIT_THRESHOLD", "30s")
	assert.Equal(t, 30*time.Second, rateLimitWaitThreshold())

	t.Setenv("LOCUTUS_RATE_LIMIT_WAIT_THRESHOLD", "2m")
	assert.Equal(t, 2*time.Minute, rateLimitWaitThreshold())
}

// TestRateLimitWaitThreshold_FallsBackOnBadInput confirms the
// override is permissive: empty, unparseable, or non-positive values
// fall back to the default rather than failing.
func TestRateLimitWaitThreshold_FallsBackOnBadInput(t *testing.T) {
	cases := []string{"", "garbage", "-5s", "0s"}
	for _, v := range cases {
		t.Run(fmt.Sprintf("input=%q", v), func(t *testing.T) {
			if v == "" {
				os.Unsetenv("LOCUTUS_RATE_LIMIT_WAIT_THRESHOLD")
			} else {
				t.Setenv("LOCUTUS_RATE_LIMIT_WAIT_THRESHOLD", v)
			}
			assert.Equal(t, rateLimitWaitThresholdDefault, rateLimitWaitThreshold())
		})
	}
}
