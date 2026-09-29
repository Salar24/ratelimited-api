// Package ratelimit implements token-bucket rate limiting with an in-memory
// backend for single instances and a Redis backend for distributed deployments.
package ratelimit

import (
	"context"
	"time"
)

// Result describes the outcome of a single rate-limit check.
type Result struct {
	Allowed    bool
	Limit      int           // bucket capacity (burst)
	Remaining  int           // whole tokens left after this request
	RetryAfter time.Duration // how long until one token is available; zero if allowed
}

// Limiter decides whether a request identified by key may proceed.
type Limiter interface {
	Allow(ctx context.Context, key string) (Result, error)
}

// Config defines a token bucket: it refills at Rate tokens per second up to Burst.
type Config struct {
	Rate  float64
	Burst int
}

// refill computes the new token count and outcome for a bucket. It is shared
// by the in-memory limiter and mirrored by the Redis Lua script.
func refill(cfg Config, tokens float64, elapsed time.Duration) (newTokens float64, res Result) {
	if elapsed > 0 {
		tokens += elapsed.Seconds() * cfg.Rate
	}
	if tokens > float64(cfg.Burst) {
		tokens = float64(cfg.Burst)
	}

	res.Limit = cfg.Burst
	if tokens >= 1 {
		tokens--
		res.Allowed = true
	} else {
		res.RetryAfter = time.Duration((1 - tokens) / cfg.Rate * float64(time.Second))
	}
	res.Remaining = int(tokens)
	return tokens, res
}
