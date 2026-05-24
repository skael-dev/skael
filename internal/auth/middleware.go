package auth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// Middleware returns a chi-compatible middleware that enforces X-API-Key
// authentication on all /api/ routes except /api/health and /api/openapi.json.
// Non-/api/ paths (SPA static files and index.html catch-all) are always exempt.
func Middleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for non-API paths (SPA) and explicitly exempt API paths.
			if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/health" || r.URL.Path == "/api/openapi.json" {
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
