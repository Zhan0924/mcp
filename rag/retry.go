package rag

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"strings"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
//  Retry with Exponential Backoff + Jitter — P1 Reliability
// ──────────────────────────────────────────────────────────────────────────────

// RetryConfig configures retry behavior.
type RetryConfig struct {
	MaxRetries  int           // max number of retries (0 = no retry)
	InitialWait time.Duration // initial backoff delay
	MaxWait     time.Duration // max backoff delay cap
	Multiplier  float64       // backoff multiplier (e.g. 2.0 for doubling)
	JitterPct   float64       // jitter percentage (0.0 - 1.0)
}

// DefaultRetryConfig returns sensible defaults for database operations.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:  3,
		InitialWait: 200 * time.Millisecond,
		MaxWait:     5 * time.Second,
		Multiplier:  2.0,
		JitterPct:   0.2,
	}
}

// RetryableFunc is a function that may be retried.
type RetryableFunc func(ctx context.Context) error

// WithRetry executes fn with exponential backoff + jitter retry.
// Only retries on transient/retryable errors.
func WithRetry(ctx context.Context, cfg RetryConfig, name string, logger *slog.Logger, fn RetryableFunc) error {
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		lastErr = fn(ctx)
		if lastErr == nil {
			if attempt > 0 && logger != nil {
				logger.Info("retry succeeded", slog.String("op", name), slog.Int("attempt", attempt))
			}
			return nil
		}

		if !isRetryableError(lastErr) {
			return lastErr
		}

		if attempt >= cfg.MaxRetries {
			break
		}

		// Calculate backoff with jitter
		wait := calcBackoff(cfg, attempt)
		if logger != nil {
			logger.Warn("retrying after error",
				slog.String("op", name),
				slog.Int("attempt", attempt+1),
				slog.Int("max_retries", cfg.MaxRetries),
				slog.Duration("backoff", wait),
				slog.String("error", lastErr.Error()),
			)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: context cancelled during retry: %w", name, ctx.Err())
		case <-time.After(wait):
		}
	}
	return fmt.Errorf("%s: max retries (%d) exceeded: %w", name, cfg.MaxRetries, lastErr)
}

func calcBackoff(cfg RetryConfig, attempt int) time.Duration {
	base := float64(cfg.InitialWait) * math.Pow(cfg.Multiplier, float64(attempt))
	if base > float64(cfg.MaxWait) {
		base = float64(cfg.MaxWait)
	}
	// Add jitter: ±JitterPct
	jitter := base * cfg.JitterPct * (2*rand.Float64() - 1)
	d := time.Duration(base + jitter)
	if d < 0 {
		d = time.Duration(base)
	}
	return d
}

// isRetryableError determines if an error is transient and worth retrying.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	retryablePatterns := []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"eof",
		"timeout",
		"temporary failure",
		"unavailable",
		"too many requests",
		"service unavailable",
		"deadlock",
		"lock timeout",
		"neo4j",
	}
	for _, p := range retryablePatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
