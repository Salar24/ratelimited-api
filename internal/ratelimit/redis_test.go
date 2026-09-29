package ratelimit

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// These tests run against a real Redis when REDIS_ADDR is set (as in CI),
// because the Lua script relies on server-side TIME semantics.
func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set; skipping Redis integration test")
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { c.Close() })
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
	return c
}

func uniquePrefix(t *testing.T) string {
	return "test:" + t.Name() + ":" + time.Now().Format("150405.000000") + ":"
}

func TestRedisAllowsBurstThenLimits(t *testing.T) {
	c := newTestRedis(t)
	l := NewRedis(c, Config{Rate: 0.5, Burst: 3}, uniquePrefix(t))
	ctx := context.Background()

	for i := range 3 {
		res, err := l.Allow(ctx, "k")
		if err != nil {
			t.Fatal(err)
		}
		if !res.Allowed {
			t.Fatalf("request %d: expected allowed", i)
		}
	}
	res, err := l.Allow(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if res.Allowed {
		t.Fatal("4th request should be limited")
	}
	if res.RetryAfter <= 0 || res.RetryAfter > 2*time.Second {
		t.Fatalf("RetryAfter = %v, want within (0, 2s]", res.RetryAfter)
	}
}

func TestRedisRefills(t *testing.T) {
	c := newTestRedis(t)
	l := NewRedis(c, Config{Rate: 10, Burst: 1}, uniquePrefix(t))
	ctx := context.Background()

	if res, _ := l.Allow(ctx, "k"); !res.Allowed {
		t.Fatal("first request should be allowed")
	}
	if res, _ := l.Allow(ctx, "k"); res.Allowed {
		t.Fatal("second immediate request should be limited")
	}
	time.Sleep(150 * time.Millisecond) // 10/s => 1 token per 100ms
	if res, _ := l.Allow(ctx, "k"); !res.Allowed {
		t.Fatal("request after refill should be allowed")
	}
}

func TestRedisAtomicUnderConcurrency(t *testing.T) {
	c := newTestRedis(t)
	l := NewRedis(c, Config{Rate: 0.001, Burst: 25}, uniquePrefix(t))
	ctx := context.Background()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := l.Allow(ctx, "shared")
			if err != nil {
				t.Error(err)
				return
			}
			if res.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowed != 25 {
		t.Fatalf("allowed = %d, want exactly 25", allowed)
	}
}
