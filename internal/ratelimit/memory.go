package ratelimit

import (
	"context"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Memory is an in-process token-bucket limiter. It is safe for concurrent use
// but only limits traffic within a single instance.
type Memory struct {
	cfg Config
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

// NewMemory returns an in-memory limiter.
func NewMemory(cfg Config) *Memory {
	return &Memory{cfg: cfg, now: time.Now, buckets: make(map[string]*bucket)}
}

// Allow consumes one token for key if available.
func (m *Memory) Allow(_ context.Context, key string) (Result, error) {
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(m.cfg.Burst), last: now}
		m.buckets[key] = b
	}
	var res Result
	b.tokens, res = refill(m.cfg, b.tokens, now.Sub(b.last))
	b.last = now
	return res, nil
}

// Sweep removes buckets that have been idle long enough to be full again,
// since they carry no state worth keeping.
func (m *Memory) Sweep() int {
	idle := time.Duration(float64(m.cfg.Burst) / m.cfg.Rate * float64(time.Second))
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	removed := 0
	for k, b := range m.buckets {
		if now.Sub(b.last) >= idle {
			delete(m.buckets, k)
			removed++
		}
	}
	return removed
}

// RunJanitor calls Sweep every interval until ctx is cancelled.
func (m *Memory) RunJanitor(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Sweep()
		}
	}
}
