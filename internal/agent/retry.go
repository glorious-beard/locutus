package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// RetryConfig controls retry behavior for LLM calls.
type RetryConfig struct {
	MaxAttempts int           // total attempts (1 = no retry)
	BaseDelay   time.Duration // initial backoff delay
	MaxDelay    time.Duration // cap on backoff delay
}

// DefaultRetryConfig returns sensible defaults: 3 attempts, 1s base, 10s max.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
	}
}

// GenerateWithRetry wraps an LLM.Generate call with exponential backoff retry
// on rate limit (429) and timeout errors.
func GenerateWithRetry(ctx context.Context, llm LLM, req GenerateRequest, cfg RetryConfig) (*GenerateResponse, error) {
	var lastErr error
	delay := cfg.BaseDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		resp, err := llm.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// Only retry on rate limit or timeout.
		if !errors.Is(err, ErrRateLimit) && !errors.Is(err, ErrTimeout) {
			return nil, err
		}

		if attempt < cfg.MaxAttempts {
			slog.Debug("retrying LLM call",
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
