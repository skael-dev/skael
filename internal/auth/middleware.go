package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
)

// Middleware returns a chi-compatible middleware that enforces authentication
// on /mcp and all /api/ routes except explicitly exempt paths. It checks two auth
// methods in order:
//  1. Session cookie (via scs session manager)
//  2. API key (X-API-Key header with "sk-" prefix, SHA-256 hashed)
//
// On success, the authenticated User is attached to the request context via
// ContextWithUser.
func Middleware(sessionManager *scs.SessionManager, userStore *UserStore, keyStore *KeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for non-API paths (SPA static files).
			if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/mcp" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip explicitly exempt API paths.
			if r.URL.Path == "/api/health" || r.URL.Path == "/api/health/ready" || r.URL.Path == "/api/capabilities" {
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
							Kind:  row.Kind,
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
				prefix := apiKey[:12]
				keyRow, err := keyStore.GetByPrefix(r.Context(), prefix)
				if err == nil && keyRow != nil && CheckAPIKey(keyRow.KeyHash, apiKey) {
					row, err := userStore.GetByID(r.Context(), keyRow.UserID)
					if err == nil && row != nil {
						user := &User{
							ID:    row.ID,
							Email: row.Email,
							Name:  row.Name,
							Role:  row.Role,
							Kind:  row.Kind,
						}
						key := &KeyInfo{ID: keyRow.ID, Scopes: keyRow.Scopes}
						if keyRow.SetID != nil {
							key.SetID = *keyRow.SetID
						}
						// A service key with no scope reaches nothing, whatever
						// wrote the row.
						if row.Kind == KindService && !key.Restricted() {
							writeAuthStatus(w, http.StatusForbidden, "forbidden")
							return
						}
						if key.Restricted() && !authorizeKey(key, r) {
							writeAuthStatus(w, http.StatusForbidden, "this key has no scope for this route")
							return
						}
						r = r.WithContext(ContextWithKey(ContextWithUser(r.Context(), user), key))

						// Fire-and-forget last-used update.
						keyID := keyRow.ID
						go keyStore.UpdateLastUsed(context.Background(), keyID)

						next.ServeHTTP(w, r)
						return
					}
				}
			}

			// No auth method matched.
			writeAuthError(w, "unauthorized")
		})
	}
}

// writeAuthError writes a JSON {"error": msg} response with HTTP 401.
func writeAuthError(w http.ResponseWriter, msg string) {
	writeAuthStatus(w, http.StatusUnauthorized, msg)
}

func writeAuthStatus(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
