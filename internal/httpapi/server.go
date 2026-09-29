// Package httpapi exposes the link-shortening HTTP API.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Salar24/ratelimited-api/internal/config"
	"github.com/Salar24/ratelimited-api/internal/metrics"
	"github.com/Salar24/ratelimited-api/internal/ratelimit"
	"github.com/Salar24/ratelimited-api/internal/shortcode"
	"github.com/Salar24/ratelimited-api/internal/store"
)

const maxURLLength = 2048

// Server wires the store, rate limiter and metrics into an http.Handler.
type Server struct {
	cfg     config.Config
	store   store.Store
	limiter ratelimit.Limiter
	metrics *metrics.Metrics
	log     *slog.Logger
	handler http.Handler
}

// New builds the server and its routes.
func New(cfg config.Config, st store.Store, lim ratelimit.Limiter, m *metrics.Metrics, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, store: st, limiter: lim, metrics: m, log: log}

	mux := http.NewServeMux()

	// Operational endpoints are not rate limited.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}))

	limited := func(h http.HandlerFunc) http.Handler { return s.rateLimit(h) }
	mux.Handle("POST /api/v1/links", limited(s.handleCreate))
	mux.Handle("GET /api/v1/links/{code}", limited(s.handleGet))
	mux.Handle("GET /{code}", limited(s.handleRedirect))

	s.handler = requestID(s.observe(mux))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

type createRequest struct {
	URL  string `json:"url"`
	Code string `json:"code,omitempty"`
}

type linkResponse struct {
	store.Link
	ShortURL string `json:"short_url"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg := validateURL(req.URL); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	var (
		link store.Link
		err  error
	)
	if req.Code != "" {
		if !shortcode.Valid(req.Code) {
			writeError(w, http.StatusBadRequest, "code must be 3-32 characters of letters, digits, '-' or '_'")
			return
		}
		link, err = s.store.Create(r.Context(), req.Code, req.URL)
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "code already in use")
			return
		}
	} else {
		link, err = s.createRandom(r.Context(), req.URL)
	}
	if err != nil {
		s.log.Error("create link", "error", err, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusInternalServerError, "could not create link")
		return
	}

	s.metrics.LinksCreated.Inc()
	w.Header().Set("Location", "/api/v1/links/"+link.Code)
	writeJSON(w, http.StatusCreated, s.toResponse(link))
}

// createRandom retries on the (rare) collision of a generated code.
func (s *Server) createRandom(ctx context.Context, target string) (store.Link, error) {
	for range 5 {
		code, err := shortcode.Generate(shortcode.DefaultLength)
		if err != nil {
			return store.Link{}, err
		}
		link, err := s.store.Create(ctx, code, target)
		if !errors.Is(err, store.ErrConflict) {
			return link, err
		}
	}
	return store.Link{}, errors.New("exhausted retries generating unique code")
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	link, err := s.store.Get(r.Context(), r.PathValue("code"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "link not found")
		return
	}
	if err != nil {
		s.log.Error("get link", "error", err, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusInternalServerError, "could not fetch link")
		return
	}
	writeJSON(w, http.StatusOK, s.toResponse(link))
}

func (s *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	target, err := s.store.Resolve(r.Context(), r.PathValue("code"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "link not found")
		return
	}
	if err != nil {
		s.log.Error("resolve link", "error", err, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusInternalServerError, "could not resolve link")
		return
	}
	s.metrics.LinkRedirects.Inc()
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady reports whether dependencies are reachable, for load balancer
// and Kubernetes readiness checks.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "store": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) toResponse(l store.Link) linkResponse {
	return linkResponse{Link: l, ShortURL: strings.TrimRight(s.cfg.BaseURL, "/") + "/" + l.Code}
}

func validateURL(raw string) string {
	if raw == "" {
		return "url is required"
	}
	if len(raw) > maxURLLength {
		return "url is too long"
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "url must be an absolute http(s) URL"
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
