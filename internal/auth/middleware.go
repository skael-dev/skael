package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
)

// Middleware returns a chi-compatible middleware that enforces authentication
// on all /api/ routes except explicitly exempt paths. It checks three auth
// methods in order:
//  1. Session cookie (via scs session manager)
//  2. API key (X-API-Key header with "sk-" prefix, bcrypt-hashed)
//  3. Legacy API key (constant-time comparison against a static key)
//
// On success, the authenticated User is attached to the request context via
// ContextWithUser.
func Middleware(sessionManager *scs.SessionManager, userStore *UserStore, keyStore *KeyStore, legacyAPIKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for non-API paths (SPA static files).
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}

			// Skip explicitly exempt API paths.
			if r.URL.Path == "/api/health" || r.URL.Path == "/api/openapi.json" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip public auth endpoints.
			if r.URL.Path == "/api/auth/signup" || r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/auth/logout" {
				next.ServeHTTP(w, r)
				return
			}

			// 1. Try session cookie.
			if sessionManager != nil && userStore != nil {
				userID := sessionManager.GetString(r.Context(), "user_id")
				if userID != "" {
					row, err := userStore.GetByID(r.Context(), userID)
					if err == nil && row != nil {
						user := &User{
							ID:    row.ID,
							Email: row.Email,
							Name:  row.Name,
							Role:  row.Role,
						}
						r = r.WithContext(ContextWithUser(r.Context(), user))
						next.ServeHTTP(w, r)
						return
					}
				}
			}

			// 2. Try API key (X-API-Key header with "sk-" prefix).
			apiKey := r.Header.Get("X-API-Key")
			if apiKey != "" && strings.HasPrefix(apiKey, "sk-") && keyStore != nil && userStore != nil {
				prefix := apiKey[:8]
				keyRow, err := keyStore.GetByPrefix(r.Context(), prefix)
				if err == nil && keyRow != nil && CheckAPIKey(keyRow.KeyHash, apiKey) {
					row, err := userStore.GetByID(r.Context(), keyRow.UserID)
					if err == nil && row != nil {
						user := &User{
							ID:    row.ID,
							Email: row.Email,
							Name:  row.Name,
							Role:  row.Role,
						}
						r = r.WithContext(ContextWithUser(r.Context(), user))

						// Fire-and-forget last-used update.
						keyID := keyRow.ID
						go keyStore.UpdateLastUsed(context.Background(), keyID)

						next.ServeHTTP(w, r)
						return
					}
				}
			}

			// 3. Try legacy API key (constant-time comparison).
			if legacyAPIKey != "" && apiKey != "" {
				if subtle.ConstantTimeCompare([]byte(apiKey), []byte(legacyAPIKey)) == 1 {
					user := &User{
						ID:    "system",
						Email: "",
						Name:  "system",
						Role:  "admin",
					}
					r = r.WithContext(ContextWithUser(r.Context(), user))
					next.ServeHTTP(w, r)
					return
				}
			}

			// No auth method matched.
			writeAuthError(w, "unauthorized")
		})
	}
}

// writeAuthError writes a JSON {"error": msg} response with HTTP 401.
func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
