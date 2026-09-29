package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestMemory(cfg Config) (*Memory, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	m := NewMemory(cfg)
	m.now = clk.now
	return m, clk
}

func TestMemoryAllowsBurstThenLimits(t *testing.T) {
	m, _ := newTestMemory(Config{Rate: 1, Burst: 3})
	ctx := context.Background()

	for i := range 3 {
		res, _ := m.Allow(ctx, "k")
		if !res.Allowed {
			t.Fatalf("request %d: expected allowed", i)
		}
		if want := 2 - i; res.Remaining != want {
			t.Fatalf("request %d: remaining = %d, want %d", i, res.Remaining, want)
		}
	}

	res, _ := m.Allow(ctx, "k")
	if res.Allowed {
		t.Fatal("4th request should be limited")
	}
	if res.RetryAfter != time.Second {
		t.Fatalf("RetryAfter = %v, want 1s", res.RetryAfter)
	}
}

func TestMemoryRefillsOverTime(t *testing.T) {
	m, clk := newTestMemory(Config{Rate: 2, Burst: 2})
	ctx := context.Background()

	_, _ = m.Allow(ctx, "k")
	_, _ = m.Allow(ctx, "k")
	if res, _ := m.Allow(ctx, "k"); res.Allowed {
		t.Fatal("bucket should be empty")
	}

	clk.advance(500 * time.Millisecond) // +1 token at 2/s
	if res, _ := m.Allow(ctx, "k"); !res.Allowed {
		t.Fatal("expected one token after 500ms")
	}

	clk.advance(10 * time.Second) // refill is capped at burst
	for i := range 2 {
		if res, _ := m.Allow(ctx, "k"); !res.Allowed {
			t.Fatalf("request %d after long idle should be allowed", i)
		}
	}
	if res, _ := m.Allow(ctx, "k"); res.Allowed {
		t.Fatal("refill must not exceed burst")
	}
}

func TestMemoryKeysAreIndependent(t *testing.T) {
	m, _ := newTestMemory(Config{Rate: 1, Burst: 1})
	ctx := context.Background()

	if res, _ := m.Allow(ctx, "a"); !res.Allowed {
		t.Fatal("a should be allowed")
	}
	if res, _ := m.Allow(ctx, "b"); !res.Allowed {
		t.Fatal("b should be allowed independently of a")
	}
	if res, _ := m.Allow(ctx, "a"); res.Allowed {
		t.Fatal("a should now be limited")
	}
}

func TestMemorySweepRemovesIdleBuckets(t *testing.T) {
	m, clk := newTestMemory(Config{Rate: 1, Burst: 5})
	ctx := context.Background()

	_, _ = m.Allow(ctx, "old")
	clk.advance(3 * time.Second)
	_, _ = m.Allow(ctx, "recent")
	clk.advance(2 * time.Second) // "old" idle 5s (== refill time), "recent" idle 2s

	if n := m.Sweep(); n != 1 {
		t.Fatalf("Sweep removed %d buckets, want 1", n)
	}
	if _, ok := m.buckets["recent"]; !ok {
		t.Fatal("recent bucket should be kept")
	}
}

func TestMemoryConcurrentNeverExceedsBurst(t *testing.T) {
	m := NewMemory(Config{Rate: 0.001, Burst: 50})
	ctx := context.Background()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res, _ := m.Allow(ctx, "shared"); res.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowed != 50 {
		t.Fatalf("allowed = %d, want exactly 50", allowed)
	}
}
