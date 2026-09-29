package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Salar24/ratelimited-api/migrations"
)

const uniqueViolation = "23505"

// Postgres is a Store backed by PostgreSQL.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres connects to dsn and applies migrations.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	if _, err := pool.Exec(ctx, migrations.Schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Close releases the connection pool.
func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Create(ctx context.Context, code, url string) (Link, error) {
	var l Link
	err := p.pool.QueryRow(ctx,
		`INSERT INTO links (code, url) VALUES ($1, $2)
		 RETURNING code, url, hits, created_at`,
		code, url,
	).Scan(&l.Code, &l.URL, &l.Hits, &l.CreatedAt)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return Link{}, ErrConflict
	}
	if err != nil {
		return Link{}, fmt.Errorf("store: create: %w", err)
	}
	return l, nil
}

func (p *Postgres) Get(ctx context.Context, code string) (Link, error) {
	var l Link
	err := p.pool.QueryRow(ctx,
		`SELECT code, url, hits, created_at FROM links WHERE code = $1`, code,
	).Scan(&l.Code, &l.URL, &l.Hits, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	if err != nil {
		return Link{}, fmt.Errorf("store: get: %w", err)
	}
	return l, nil
}

func (p *Postgres) Resolve(ctx context.Context, code string) (string, error) {
	var url string
	err := p.pool.QueryRow(ctx,
		`UPDATE links SET hits = hits + 1 WHERE code = $1 RETURNING url`, code,
	).Scan(&url)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: resolve: %w", err)
	}
	return url, nil
}

func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
