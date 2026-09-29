package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ctxKey int

const requestIDKey ctxKey = iota

// statusRecorder captures the response status for logging and metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// requestID propagates X-Request-ID or generates one.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// observe records logs and Prometheus metrics for every request. It must
// wrap the mux so that r.Pattern is populated after routing.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		s.metrics.HTTPInFlight.Inc()
		defer s.metrics.HTTPInFlight.Dec()

		defer func() {
			if p := recover(); p != nil {
				s.log.Error("panic serving request", "panic", p, "request_id", requestIDFrom(r.Context()))
				rec.status = http.StatusInternalServerError
				writeError(rec, http.StatusInternalServerError, "internal server error")
			}

			route := r.Pattern
			if route == "" {
				route = "unmatched" // keep label cardinality bounded
			}
			elapsed := time.Since(start)
			status := strconv.Itoa(rec.status)
			s.metrics.HTTPRequests.WithLabelValues(r.Method, route, status).Inc()
			s.metrics.HTTPDuration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())

			level := slog.LevelInfo
			if rec.status >= 500 {
				level = slog.LevelError
			}
			s.log.Log(r.Context(), level, "request",
				"method", r.Method,
				"route", route,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", float64(elapsed.Microseconds())/1000,
				"client_ip", s.clientIP(r),
				"request_id", requestIDFrom(r.Context()),
			)
		}()

		next.ServeHTTP(rec, r)
	})
}

// rateLimit applies the token bucket per client IP.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := s.limiter.Allow(r.Context(), "ip:"+s.clientIP(r))
		if err != nil {
			s.metrics.RateLimit.WithLabelValues("error").Inc()
			s.log.Warn("rate limiter unavailable", "error", err, "fail_open", s.cfg.FailOpen)
			if s.cfg.FailOpen {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusServiceUnavailable, "rate limiter unavailable")
			return
		}

		h := w.Header()
		h.Set("X-RateLimit-Limit", strconv.Itoa(res.Limit))
		h.Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))

		if !res.Allowed {
			s.metrics.RateLimit.WithLabelValues("limited").Inc()
			h.Set("Retry-After", strconv.Itoa(int(math.Ceil(res.RetryAfter.Seconds()))))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		s.metrics.RateLimit.WithLabelValues("allowed").Inc()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) clientIP(r *http.Request) string {
	// Behind one trusted proxy, the last X-Forwarded-For entry is the address
	// the proxy saw; earlier entries are client-controlled and spoofable.
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
