package store

import (
	"context"
	"sync"
	"time"
)

// Memory is an in-process Store for local development and tests.
type Memory struct {
	mu    sync.RWMutex
	links map[string]*Link
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{links: make(map[string]*Link)}
}

func (m *Memory) Create(_ context.Context, code, url string) (Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.links[code]; ok {
		return Link{}, ErrConflict
	}
	l := &Link{Code: code, URL: url, CreatedAt: time.Now().UTC()}
	m.links[code] = l
	return *l, nil
}

func (m *Memory) Get(_ context.Context, code string) (Link, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.links[code]
	if !ok {
		return Link{}, ErrNotFound
	}
	return *l, nil
}

func (m *Memory) Resolve(_ context.Context, code string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[code]
	if !ok {
		return "", ErrNotFound
	}
	l.Hits++
	return l.URL, nil
}

func (m *Memory) Ping(context.Context) error { return nil }
