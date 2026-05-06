package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"
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
			slog.Debug("retrying agent call",
				"agent", def.ID,
				"attempt", attempt,
				"max", cfg.MaxAttempts,
				"error", err,
				"delay", delay,
			)
			select {
			case <-ctx.Done():
				return nil, ErrTimeout
			case <-time.After(delay):
			}
			delay *= 2
			if delay > cfg.MaxDelay {
				delay = cfg.MaxDelay
			}
		}
	}
	return nil, lastErr
}
