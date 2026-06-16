package platform

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests",
	}, []string{"method", "path", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
)

func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		path := normalizePath(r.URL.Path)
		httpRequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(ww.status)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
	})
}

func normalizePath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) >= 4 && parts[1] == "api" && parts[2] == "skills" {
		parts[3] = ":name"
		if len(parts) >= 6 && parts[4] == "versions" {
			parts[5] = ":version"
		}
	}
	return strings.Join(parts, "/")
}
