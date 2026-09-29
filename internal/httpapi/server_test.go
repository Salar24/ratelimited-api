package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Salar24/ratelimited-api/internal/config"
	"github.com/Salar24/ratelimited-api/internal/metrics"
	"github.com/Salar24/ratelimited-api/internal/ratelimit"
	"github.com/Salar24/ratelimited-api/internal/store"
)

func newTestServer(t *testing.T, lim ratelimit.Limiter, cfg config.Config) *Server {
	t.Helper()
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://sho.rt"
	}
	if lim == nil {
		lim = ratelimit.NewMemory(ratelimit.Config{Rate: 1000, Burst: 1000})
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, store.NewMemory(), lim, metrics.New(), log)
}

func do(s http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return v
}

func TestCreateGetRedirect(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})

	rec := do(s, "POST", "/api/v1/links", `{"url":"https://go.dev/doc"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body)
	}
	created := decode[linkResponse](t, rec)
	if created.ShortURL != "http://sho.rt/"+created.Code {
		t.Fatalf("short_url = %q", created.ShortURL)
	}

	rec = do(s, "GET", "/"+created.Code, "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev/doc" {
		t.Fatalf("redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = do(s, "GET", "/api/v1/links/"+created.Code, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}
	if got := decode[linkResponse](t, rec); got.Hits != 1 {
		t.Fatalf("hits = %d, want 1", got.Hits)
	}
}

func TestCreateCustomCode(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})

	rec := do(s, "POST", "/api/v1/links", `{"url":"https://example.com","code":"my-link"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	rec = do(s, "POST", "/api/v1/links", `{"url":"https://example.org","code":"my-link"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", rec.Code)
	}
}

func TestCreateValidation(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	cases := map[string]string{
		"malformed json": `{"url":`,
		"missing url":    `{}`,
		"relative url":   `{"url":"/just/a/path"}`,
		"bad scheme":     `{"url":"javascript:alert(1)"}`,
		"ftp scheme":     `{"url":"ftp://example.com"}`,
		"unknown field":  `{"url":"https://example.com","extra":1}`,
		"invalid code":   `{"url":"https://example.com","code":"a b"}`,
		"too long url":   `{"url":"https://example.com/` + strings.Repeat("a", 2100) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if rec := do(s, "POST", "/api/v1/links", body); rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	for _, path := range []string{"/nope", "/api/v1/links/nope"} {
		if rec := do(s, "GET", path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestRateLimitPerClientIP(t *testing.T) {
	lim := ratelimit.NewMemory(ratelimit.Config{Rate: 0.001, Burst: 2})
	s := newTestServer(t, lim, config.Config{})

	for i := range 2 {
		rec := do(s, "GET", "/api/v1/links/x", "")
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d unexpectedly limited", i)
		}
		if want := []string{"1", "0"}[i]; rec.Header().Get("X-RateLimit-Remaining") != want {
			t.Fatalf("request %d remaining = %q, want %s", i, rec.Header().Get("X-RateLimit-Remaining"), want)
		}
	}

	rec := do(s, "GET", "/api/v1/links/x", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must include Retry-After")
	}

	// Operational endpoints are exempt.
	if rec := do(s, "GET", "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d while limited", rec.Code)
	}
}

func TestTrustProxyUsesLastForwardedHop(t *testing.T) {
	lim := ratelimit.NewMemory(ratelimit.Config{Rate: 0.001, Burst: 1})
	s := newTestServer(t, lim, config.Config{TrustProxy: true})

	// A client spoofing the first XFF entry still maps to the proxy-observed IP.
	do(s, "GET", "/x", "", "X-Forwarded-For", "1.1.1.1, 203.0.113.7")
	rec := do(s, "GET", "/x", "", "X-Forwarded-For", "9.9.9.9, 203.0.113.7")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF bypassed limiter: status %d", rec.Code)
	}

	rec = do(s, "GET", "/x", "", "X-Forwarded-For", "198.51.100.1")
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("different client IP should have its own bucket")
	}
}

type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{}, errors.New("redis down")
}

func TestLimiterFailureModes(t *testing.T) {
	open := newTestServer(t, failingLimiter{}, config.Config{FailOpen: true})
	if rec := do(open, "GET", "/api/v1/links/x", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("fail-open status = %d, want request to pass through (404)", rec.Code)
	}

	closed := newTestServer(t, failingLimiter{}, config.Config{FailOpen: false})
	if rec := do(closed, "GET", "/api/v1/links/x", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("fail-closed status = %d, want 503", rec.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	do(s, "POST", "/api/v1/links", `{"url":"https://example.com"}`)

	rec := do(s, "GET", "/metrics", "")
	body := rec.Body.String()
	for _, want := range []string{
		`http_requests_total{method="POST",route="POST /api/v1/links",status="201"} 1`,
		`ratelimit_decisions_total{decision="allowed"} 1`,
		`links_created_total 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func TestRequestIDPropagation(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	if rec := do(s, "GET", "/healthz", "", "X-Request-ID", "abc123"); rec.Header().Get("X-Request-ID") != "abc123" {
		t.Fatalf("X-Request-ID = %q", rec.Header().Get("X-Request-ID"))
	}
	if rec := do(s, "GET", "/healthz", ""); len(rec.Header().Get("X-Request-ID")) != 16 {
		t.Fatalf("generated X-Request-ID = %q", rec.Header().Get("X-Request-ID"))
	}
}
