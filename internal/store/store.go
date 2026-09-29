// Package store persists short links.
package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("link not found")
	ErrConflict = errors.New("code already in use")
)

// Link is a shortened URL.
type Link struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	Hits      int64     `json:"hits"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is the persistence interface used by the HTTP layer.
type Store interface {
	// Create inserts a link, returning ErrConflict if the code exists.
	Create(ctx context.Context, code, url string) (Link, error)
	// Get returns the link for code or ErrNotFound.
	Get(ctx context.Context, code string) (Link, error)
	// Resolve returns the target URL for code and records a hit.
	Resolve(ctx context.Context, code string) (string, error)
	// Ping checks that the backing store is reachable.
	Ping(ctx context.Context) error
}
