package auth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

// Middleware returns a chi-compatible middleware that enforces X-API-Key
// authentication on all routes except /api/health and /api/openapi.json.
func Middleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for exempt paths.
			if r.URL.Path == "/api/health" || r.URL.Path == "/api/openapi.json" {
				next.ServeHTTP(w, r)
				return
			}

			provided := r.Header.Get("X-API-Key")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(apiKey)) != 1 {
				writeAuthError(w, "unauthorized")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeAuthError writes a JSON {"error": msg} response with HTTP 401.
func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
