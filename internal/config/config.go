// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr            string        // HTTP listen address
	BaseURL         string        // public base URL used to build short links
	DatabaseURL     string        // Postgres DSN; empty uses the in-memory store
	RedisURL        string        // Redis URL; empty uses the in-memory limiter
	RateLimitRPS    float64       // token refill rate per client
	RateLimitBurst  int           // bucket capacity per client
	FailOpen        bool          // allow requests when the limiter backend errors
	TrustProxy      bool          // take client IP from X-Forwarded-For
	ShutdownTimeout time.Duration // graceful shutdown deadline
	LogLevel        string        // debug, info, warn, error
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	c := Config{
		Addr:        env("ADDR", ":8080"),
		BaseURL:     env("BASE_URL", "http://localhost:8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		LogLevel:    env("LOG_LEVEL", "info"),
	}

	var err error
	if c.RateLimitRPS, err = envFloat("RATE_LIMIT_RPS", 5); err != nil {
		return c, err
	}
	if c.RateLimitBurst, err = envInt("RATE_LIMIT_BURST", 10); err != nil {
		return c, err
	}
	if c.FailOpen, err = envBool("RATE_LIMIT_FAIL_OPEN", true); err != nil {
		return c, err
	}
	if c.TrustProxy, err = envBool("TRUST_PROXY", false); err != nil {
		return c, err
	}
	if c.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		return c, err
	}

	if c.RateLimitRPS <= 0 {
		return c, fmt.Errorf("config: RATE_LIMIT_RPS must be > 0")
	}
	if c.RateLimitBurst < 1 {
		return c, fmt.Errorf("config: RATE_LIMIT_BURST must be >= 1")
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func parse[T any](key string, def T, fn func(string) (T, error)) (T, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	out, err := fn(v)
	if err != nil {
		return def, fmt.Errorf("config: invalid %s=%q: %w", key, v, err)
	}
	return out, nil
}

func envInt(key string, def int) (int, error) { return parse(key, def, strconv.Atoi) }
func envBool(key string, def bool) (bool, error) {
	return parse(key, def, strconv.ParseBool)
}
func envDuration(key string, def time.Duration) (time.Duration, error) {
	return parse(key, def, time.ParseDuration)
}
func envFloat(key string, def float64) (float64, error) {
	return parse(key, def, func(s string) (float64, error) { return strconv.ParseFloat(s, 64) })
}
