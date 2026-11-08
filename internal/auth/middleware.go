package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	authall "github.com/alternayte/auth-all"
)

// AuthAllRoutes is the chi pattern auth-all serves a person's own account on.
const AuthAllRoutes = "/api/auth/*"

// Middleware enforces authentication on /mcp and all /api/ routes except exempt
// paths. auth-all resolves a session cookie or a bearer value, and Skael adds
// its key limits, which auth-all does not hold.
func Middleware(people *People, keyStore *KeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		authenticated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := authall.PrincipalFrom(r.Context())
			if principal == nil || principal.User == nil {
				writeAuthError(w, "unauthorized")
				return
			}
			if !authorize(w, r, principal, keyStore, next) {
				return
			}
		})
		var resolve http.Handler = authenticated
		if people != nil {
			resolve = people.Auth.LoadSession(authenticated)
		}

		accountRoutes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := authall.PrincipalFrom(r.Context())
			if strings.HasPrefix(r.URL.Path, "/api/auth/user/delete") && p != nil && p.User != nil && p.User.Role == RoleOwner {
				writeAuthStatus(w, http.StatusForbidden, "the owner cannot delete their own account")
				return
			}
			next.ServeHTTP(w, r)
		})
		var resolveAccount http.Handler = accountRoutes
		if people != nil {
			resolveAccount = people.Auth.LoadSession(accountRoutes)
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for non-API paths (SPA static files).
			if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/mcp" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip explicitly exempt API paths.
			// GitHub's webhook carries its own signature, which the handler checks.
			// The setup routes serve a fresh instance before anyone can sign in.
			if r.URL.Path == "/api/health" || r.URL.Path == "/api/health/ready" || r.URL.Path == "/api/capabilities" ||
				r.URL.Path == "/api/github/webhook" || r.URL.Path == "/api/setup" || r.URL.Path == "/api/setup/claim" {
				next.ServeHTTP(w, r)
				return
			}

			PassXAPIKey(r)

			// The admin plugin has neither the one-owner rule nor the service
			// account rule, so only Skael's /api/admin routes may reach it.
			if r.URL.Path == "/api/auth/admin" || strings.HasPrefix(r.URL.Path, "/api/auth/admin/") {
				writeAuthStatus(w, http.StatusForbidden, "forbidden")
				return
			}
			if isAuthAllPath(r.URL.Path) {
				resolveAccount.ServeHTTP(w, r)
				return
			}
			resolve.ServeHTTP(w, r)
		})
	}
}

func isAuthAllPath(path string) bool {
	if !strings.HasPrefix(path, "/api/auth/") {
		return false
	}
	return path != "/api/auth/account" && path != "/api/auth/keys" && !strings.HasPrefix(path, "/api/auth/keys/")
}

// authorize attaches the user and the key limits, then serves the request.
func authorize(w http.ResponseWriter, r *http.Request, p *authall.Principal, keyStore *KeyStore, next http.Handler) bool {
	user := &User{
		ID:    p.User.ID,
		Email: p.User.Email,
		Name:  p.User.DisplayName,
		Role:  p.User.Role,
		Kind:  KindOf(p.User),
	}
	ctx := ContextWithUser(r.Context(), user)

	if p.APIKey == nil {
		// The account route tells the SPA to ask for a new password.
		if p.User.MustChangePassword && r.URL.Path != "/api/auth/account" {
			writeAuthStatus(w, http.StatusForbidden, "password change required")
			return false
		}
		next.ServeHTTP(w, r.WithContext(ctx))
		return true
	}

	row, err := keyStore.GetByID(r.Context(), p.APIKey.ID)
	if err != nil || row == nil {
		writeAuthError(w, "unauthorized")
		return false
	}
	key := &KeyInfo{ID: row.ID, Scopes: row.Scopes}
	if row.SetID != nil {
		key.SetID = *row.SetID
	}
	// A service key with no scope reaches nothing, whatever wrote the row.
	if user.Kind == KindService && !key.Restricted() {
		writeAuthStatus(w, http.StatusForbidden, "forbidden")
		return false
	}
	if key.Restricted() && !authorizeKey(key, r) {
		writeAuthStatus(w, http.StatusForbidden, "this key has no scope for this route")
		return false
	}
	go keyStore.UpdateLastUsed(context.Background(), row.ID)
	next.ServeHTTP(w, r.WithContext(ContextWithKey(ctx, key)))
	return true
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
