package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/chetan/locutus/internal/agent/adapters"
)

// RetryConfig controls retry behavior for agent calls.
type RetryConfig struct {
	MaxAttempts int           // total attempts (1 = no retry)
	BaseDelay   time.Duration // initial backoff delay
	MaxDelay    time.Duration // cap on backoff delay
}

// DefaultRetryConfig returns sensible defaults: 3 attempts, 1s
// base, 10s max.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
	}
}

// RunWithRetry wraps an AgentExecutor.Run call with exponential
// backoff retry on rate-limit (ErrRateLimit) and timeout
// (ErrTimeout) errors.
//
// When ctx carries a retry callback (via WithRetryCallback), the
// callback fires on every retry-eligible failure right before the
// backoff sleep. Workflow executors use this to surface a
// "retrying" spinner state — silent retries used to leave the
// operator staring at a RUNNING spinner that was actually burning
// attempts on rate-limit backoff.
func RunWithRetry(ctx context.Context, exec AgentExecutor, def AgentDef, input AgentInput, cfg RetryConfig) (*AgentOutput, error) {
	var lastErr error
	delay := cfg.BaseDelay
	notify := RetryCallbackFromContext(ctx)

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		out, err := exec.Run(ctx, def, input)
		if err == nil {
			return out, nil
		}
		lastErr = err

		if !errors.Is(err, ErrRateLimit) && !errors.Is(err, ErrTimeout) {
			return out, err
		}

		if attempt < cfg.MaxAttempts {
			if notify != nil {
				notify(attempt, err)
			}
			// Honor a provider-supplied Retry-After hint when one
			// rode through on the rate-limit error. Sleeping for the
			// exact duration the API asked for is materially better
			// than guessing — it stops us from hammering early when
			// the provider is in a measured cool-down, and stops us
			// from overshooting when the provider would have served
			// the next request quickly.
			sleep := delay
			source := "exponential"
			var rlErr *adapters.RateLimitError
			if errors.As(err, &rlErr) && rlErr.RetryAfter > 0 {
				sleep = rlErr.RetryAfter
				source = "retry-after"
			}
			slog.Debug("retrying agent call",
				"agent", def.ID,
				"attempt", attempt,
				"max", cfg.MaxAttempts,
				"error", err,
				"delay", sleep,
				"delay_source", source,
			)
			select {
			case <-ctx.Done():
				return nil, ErrTimeout
			case <-time.After(sleep):
			}
			// Advance the exponential schedule regardless of which
			// source we used this round — if the next failure has
			// no hint, we resume from a higher floor instead of
			// resetting to BaseDelay.
			delay *= 2
			if delay > cfg.MaxDelay {
				delay = cfg.MaxDelay
			}
		}
	}
	return nil, lastErr
}
