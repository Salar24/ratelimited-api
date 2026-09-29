// Package metrics defines the Prometheus metrics exported by the service.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics groups the service's collectors around a dedicated registry so
// tests can create independent instances.
type Metrics struct {
	Registry *prometheus.Registry

	HTTPRequests  *prometheus.CounterVec
	HTTPDuration  *prometheus.HistogramVec
	HTTPInFlight  prometheus.Gauge
	RateLimit     *prometheus.CounterVec
	LinksCreated  prometheus.Counter
	LinkRedirects prometheus.Counter
}

// New registers all collectors, plus Go runtime and process collectors.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests by method, route pattern and status code.",
		}, []string{"method", "route", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by method and route pattern.",
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		}, []string{"method", "route"}),
		HTTPInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
		RateLimit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratelimit_decisions_total",
			Help: "Rate limiter decisions: allowed, limited, or error (limiter backend failure).",
		}, []string{"decision"}),
		LinksCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "links_created_total",
			Help: "Short links created.",
		}),
		LinkRedirects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "link_redirects_total",
			Help: "Successful short link redirects.",
		}),
	}
	reg.MustRegister(
		m.HTTPRequests, m.HTTPDuration, m.HTTPInFlight,
		m.RateLimit, m.LinksCreated, m.LinkRedirects,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}
