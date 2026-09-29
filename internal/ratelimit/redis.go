package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// tokenBucketScript refills and consumes a bucket atomically. It uses the
// Redis server clock so that multiple app instances agree on time.
//
// KEYS[1] bucket key
// ARGV[1] refill rate (tokens/second)
// ARGV[2] burst (capacity)
//
// Returns {allowed (0|1), tokens remaining, retry-after seconds} with the
// floats encoded as strings, because Lua numbers are truncated to integers
// when converted to Redis replies.
var tokenBucketScript = redis.NewScript(`
local key   = KEYS[1]
local rate  = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])

local t   = redis.call("TIME")
local now = tonumber(t[1]) + tonumber(t[2]) / 1000000

local state  = redis.call("HMGET", key, "tokens", "ts")
local tokens = tonumber(state[1])
local ts     = tonumber(state[2])
if tokens == nil then
  tokens = burst
  ts = now
end

local elapsed = now - ts
if elapsed > 0 then
  tokens = math.min(burst, tokens + elapsed * rate)
end

local allowed = 0
local retry = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry = (1 - tokens) / rate
end

redis.call("HSET", key, "tokens", tostring(tokens), "ts", tostring(now))
redis.call("PEXPIRE", key, math.ceil(burst / rate * 1000) + 1000)

return {allowed, tostring(tokens), tostring(retry)}
`)

// Redis is a distributed token-bucket limiter backed by a Redis Lua script.
type Redis struct {
	client redis.Scripter
	cfg    Config
	prefix string
}

// NewRedis returns a limiter storing buckets under keys prefixed with prefix.
func NewRedis(client redis.Scripter, cfg Config, prefix string) *Redis {
	return &Redis{client: client, cfg: cfg, prefix: prefix}
}

// Allow consumes one token for key if available.
func (r *Redis) Allow(ctx context.Context, key string) (Result, error) {
	raw, err := tokenBucketScript.Run(ctx, r.client,
		[]string{r.prefix + key},
		r.cfg.Rate, r.cfg.Burst,
	).Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: redis script: %w", err)
	}
	if len(raw) != 3 {
		return Result{}, fmt.Errorf("ratelimit: unexpected reply %v", raw)
	}

	allowed, _ := raw[0].(int64)
	tokens, err := parseFloat(raw[1])
	if err != nil {
		return Result{}, err
	}
	retry, err := parseFloat(raw[2])
	if err != nil {
		return Result{}, err
	}

	return Result{
		Allowed:    allowed == 1,
		Limit:      r.cfg.Burst,
		Remaining:  int(tokens),
		RetryAfter: time.Duration(retry * float64(time.Second)),
	}, nil
}

func parseFloat(v any) (float64, error) {
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("ratelimit: expected string, got %T", v)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("ratelimit: parse %q: %w", s, err)
	}
	return f, nil
}
