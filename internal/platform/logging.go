package platform

import (
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// RequestLogger is a Chi-compatible middleware that logs every request
// using zerolog. Logs method, path, status, duration, and client IP.
//
// The IP is the one ClientIP resolved, so the logs name the same client the
// rate limiter charged — reading a 429 against the wrong address would make
// every limiter incident impossible to trace.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)

		if r.URL.Path == "/api/health" || r.URL.Path == "/api/health/ready" {
			return
		}

		evt := log.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", ww.status).
			Dur("duration", time.Since(start)).
			Str("ip", ClientIPFromRequest(r))

		if reqID := RequestIDFromContext(r.Context()); reqID != "" {
			evt = evt.Str("request_id", reqID)
		}

		evt.Msg("request")
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
