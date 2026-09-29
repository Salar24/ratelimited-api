package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// runStoreTests exercises any Store implementation against the same contract.
func runStoreTests(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()

	t.Run("create and get", func(t *testing.T) {
		s := newStore(t)
		code := uniqueCode()
		created, err := s.Create(ctx, code, "https://example.com")
		if err != nil {
			t.Fatal(err)
		}
		if created.Code != code || created.URL != "https://example.com" || created.Hits != 0 {
			t.Fatalf("unexpected link %+v", created)
		}
		got, err := s.Get(ctx, code)
		if err != nil {
			t.Fatal(err)
		}
		if got.URL != created.URL {
			t.Fatalf("Get URL = %q, want %q", got.URL, created.URL)
		}
	})

	t.Run("duplicate code conflicts", func(t *testing.T) {
		s := newStore(t)
		code := uniqueCode()
		if _, err := s.Create(ctx, code, "https://a.example"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Create(ctx, code, "https://b.example"); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("missing code", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Get(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get err = %v, want ErrNotFound", err)
		}
		if _, err := s.Resolve(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Resolve err = %v, want ErrNotFound", err)
		}
	})

	t.Run("resolve counts hits", func(t *testing.T) {
		s := newStore(t)
		code := uniqueCode()
		if _, err := s.Create(ctx, code, "https://example.com/x"); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			url, err := s.Resolve(ctx, code)
			if err != nil {
				t.Fatal(err)
			}
			if url != "https://example.com/x" {
				t.Fatalf("Resolve = %q", url)
			}
		}
		got, _ := s.Get(ctx, code)
		if got.Hits != 3 {
			t.Fatalf("Hits = %d, want 3", got.Hits)
		}
	})
}

var codeSeq atomic.Int64

func uniqueCode() string {
	return fmt.Sprintf("t%d-%d", time.Now().UnixNano(), codeSeq.Add(1))
}

func TestMemoryStore(t *testing.T) {
	runStoreTests(t, func(*testing.T) Store { return NewMemory() })
}

// TestPostgresStore runs when TEST_DATABASE_URL is set (as in CI).
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	runStoreTests(t, func(t *testing.T) Store {
		p, err := NewPostgres(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	})
}
