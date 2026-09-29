// Command server runs the rate-limited link API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Salar24/ratelimited-api/internal/config"
	"github.com/Salar24/ratelimited-api/internal/httpapi"
	"github.com/Salar24/ratelimited-api/internal/metrics"
	"github.com/Salar24/ratelimited-api/internal/ratelimit"
	"github.com/Salar24/ratelimited-api/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Storage: Postgres when configured, otherwise in-memory.
	var st store.Store
	if cfg.DatabaseURL != "" {
		pg, err := store.NewPostgres(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pg.Close()
		st = pg
		log.Info("using postgres store")
	} else {
		st = store.NewMemory()
		log.Warn("DATABASE_URL not set; using in-memory store")
	}

	// Rate limiting: Redis when configured (shared across replicas),
	// otherwise per-instance in-memory buckets.
	limCfg := ratelimit.Config{Rate: cfg.RateLimitRPS, Burst: cfg.RateLimitBurst}
	var lim ratelimit.Limiter
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return err
		}
		rdb := redis.NewClient(opts)
		defer rdb.Close()
		if err := rdb.Ping(ctx).Err(); err != nil {
			return err
		}
		lim = ratelimit.NewRedis(rdb, limCfg, "ratelimit:")
		log.Info("using redis rate limiter")
	} else {
		mem := ratelimit.NewMemory(limCfg)
		go mem.RunJanitor(ctx, time.Minute)
		lim = mem
		log.Warn("REDIS_URL not set; using in-memory rate limiter")
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(cfg, st, lim, metrics.New(), log),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr,
			"rate_limit_rps", cfg.RateLimitRPS, "rate_limit_burst", cfg.RateLimitBurst)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down", "timeout", cfg.ShutdownTimeout.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	log.Info("stopped")
	return nil
}
