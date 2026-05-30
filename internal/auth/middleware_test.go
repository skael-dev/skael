package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/testutil"
)

// setupMiddlewareTest creates a router with session manager, auth middleware,
// auth routes (for signup/login), and a protected /api/protected endpoint.
// Returns an httptest.Server and test infrastructure.
func setupMiddlewareTest(t *testing.T) (*httptest.Server, *scs.SessionManager, *auth.UserStore, *auth.KeyStore) {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	sessionManager := scs.New()

	r := chi.NewMux()
	r.Use(sessionManager.LoadAndSave)
	r.Use(auth.Middleware(sessionManager, userStore, keyStore))

	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))

	// Register auth routes so we can sign up and get sessions.
	auth.RegisterRoutes(api, sessionManager, userStore, keyStore, false)

	// Register a protected test endpoint that returns the user from context.
	huma.Register(api, huma.Operation{
		OperationID: "protected",
		Method:      http.MethodGet,
		Path:        "/api/protected",
		Summary:     "Protected test endpoint",
	}, func(ctx context.Context, input *struct{}) (*struct {
		Body auth.User
	}, error) {
		user := auth.UserFromContext(ctx)
		if user == nil {
			return nil, huma.Error401Unauthorized("no user in context")
		}
		return &struct {
			Body auth.User
		}{Body: *user}, nil
	})

	// Register a health endpoint.
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
	}, func(ctx context.Context, input *struct{}) (*struct {
		Body struct {
			Status string `json:"status"`
		}
	}, error) {
		out := &struct {
			Body struct {
				Status string `json:"status"`
			}
		}{}
		out.Body.Status = "ok"
		return out, nil
	})

	// SPA path handler.
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("spa")) //nolint:errcheck
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return srv, sessionManager, userStore, keyStore
}

// newClientWithJar creates an http.Client with a fresh cookie jar.
func newClientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar}
}

// signupUser signs up a user and returns the client (with session cookie).
func signupUser(t *testing.T, srv *httptest.Server, email, name, password string) *http.Client {
	t.Helper()
	client := newClientWithJar(t)
	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    email,
		"name":     name,
		"password": password,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	return client
}

func TestMiddleware_NonAPIPath_PassesThrough(t *testing.T) {
	srv, _, _, _ := setupMiddlewareTest(t)

	// Non-API paths should pass through without auth.
	client := newClientWithJar(t)
	paths := []string{"/", "/settings", "/skills/my-skill", "/favicon.ico"}
	for _, path := range paths {
		resp, err := client.Get(srv.URL + path)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "path %s should pass through", path)
		resp.Body.Close()
	}
}

func TestMiddleware_HealthEndpoint_PassesThrough(t *testing.T) {
	srv, _, _, _ := setupMiddlewareTest(t)

	client := newClientWithJar(t)
	resp, err := client.Get(srv.URL + "/api/health")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestMiddleware_AuthLoginEndpoint_PassesThrough(t *testing.T) {
	srv, _, _, _ := setupMiddlewareTest(t)

	// /api/auth/login should be accessible without auth (the login handler itself
	// will return 401 for bad creds, but the middleware should let it through).
	client := newClientWithJar(t)
	resp := doPost(t, client, srv.URL+"/api/auth/login", map[string]string{
		"email":    "nobody@example.com",
		"password": "anything",
	})
	// 401 from the handler, not from middleware — the handler returns "invalid credentials"
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestMiddleware_ValidSession_PassesWithUser(t *testing.T) {
	srv, _, _, _ := setupMiddlewareTest(t)

	// Sign up to get a session cookie.
	client := signupUser(t, srv, "session@example.com", "Session User", "password123")

	// Access the protected endpoint.
	resp, err := client.Get(srv.URL + "/api/protected")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var user auth.User
	decodeJSON(t, resp, &user)
	require.Equal(t, "session@example.com", user.Email)
	require.Equal(t, "Session User", user.Name)
}

func TestMiddleware_ValidAPIKey_PassesWithUser(t *testing.T) {
	srv, _, _, keyStore := setupMiddlewareTest(t)

	// Sign up to create a user, then create an API key for them.
	client := signupUser(t, srv, "apikey@example.com", "API Key User", "password123")

	// Create API key via the route.
	resp := doPost(t, client, srv.URL+"/api/auth/keys", map[string]string{"name": "test-key"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var keyResp struct {
		Key string `json:"key"`
	}
	decodeJSON(t, resp, &keyResp)
	require.NotEmpty(t, keyResp.Key)
	_ = keyStore // keyStore is available if needed

	// Use the API key with a fresh client (no session cookie).
	freshClient := newClientWithJar(t)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/protected", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", keyResp.Key)

	resp2, err := freshClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	var user auth.User
	decodeJSON(t, resp2, &user)
	require.Equal(t, "apikey@example.com", user.Email)
}

func TestMiddleware_NoAuth_Returns401(t *testing.T) {
	srv, _, _, _ := setupMiddlewareTest(t)

	client := newClientWithJar(t)
	resp, err := client.Get(srv.URL + "/api/protected")
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	resp.Body.Close()
	require.Equal(t, "unauthorized", body["error"])
}

func TestMiddleware_InvalidAPIKey_Returns401(t *testing.T) {
	srv, _, _, _ := setupMiddlewareTest(t)

	client := newClientWithJar(t)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/protected", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", "sk-invalidkey1234567890abcdef12")

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	resp.Body.Close()
	require.Equal(t, "unauthorized", body["error"])
}
